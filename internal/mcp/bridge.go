package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	transport "github.com/mark3labs/mcp-go/client/transport"
	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/kanban"
)

type stdioBridge struct {
	remote  transport.Interface
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	writeMu sync.Mutex

	pendingMu        sync.Mutex
	pendingResponses map[string]chan *transport.JSONRPCResponse
}

type rawJSONRPCMessage struct {
	JSONRPC string                      `json:"jsonrpc"`
	ID      *json.RawMessage            `json:"id,omitempty"`
	Method  string                      `json:"method,omitempty"`
	Params  json.RawMessage             `json:"params,omitempty"`
	Result  json.RawMessage             `json:"result,omitempty"`
	Error   *mcpapi.JSONRPCErrorDetails `json:"error,omitempty"`
}

func ServeBridgeStdio(ctx context.Context, store *kanban.Store, stdin io.Reader, stdout, stderr io.Writer) error {
	if store == nil {
		return fmt.Errorf("store is required")
	}
	return ServeBridgeStdioPath(ctx, store.DBPath(), stdin, stdout, stderr)
}

func ServeBridgeStdioPath(ctx context.Context, dbPath string, stdin io.Reader, stdout, stderr io.Writer) error {
	if stdin == nil {
		return fmt.Errorf("stdin is required")
	}
	if stdout == nil {
		return fmt.Errorf("stdout is required")
	}

	entry, err := DiscoverDaemonForDBPath(ctx, dbPath)
	if err != nil {
		return err
	}

	remote, err := transport.NewStreamableHTTP(entry.BaseURL,
		transport.WithContinuousListening(),
		transport.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + entry.BearerToken,
		}),
	)
	if err != nil {
		return err
	}
	defer remote.Close()

	bridge := &stdioBridge{
		remote:           remote,
		stdin:            stdin,
		stdout:           stdout,
		stderr:           stderr,
		pendingResponses: map[string]chan *transport.JSONRPCResponse{},
	}
	remote.SetNotificationHandler(bridge.handleRemoteNotification)
	remote.SetRequestHandler(bridge.handleRemoteRequest)

	if err := remote.Start(ctx); err != nil {
		return err
	}
	return bridge.serve(ctx)
}

func (b *stdioBridge) serve(ctx context.Context) error {
	scanner := bufio.NewScanner(b.stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := b.handleIncomingMessage(ctx, []byte(line)); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (b *stdioBridge) handleIncomingMessage(ctx context.Context, data []byte) error {
	var message rawJSONRPCMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return err
	}

	switch {
	case strings.TrimSpace(message.Method) != "" && message.ID != nil:
		return b.forwardClientRequest(ctx, message)
	case strings.TrimSpace(message.Method) != "":
		return b.forwardClientNotification(ctx, message)
	case message.ID != nil:
		return b.completePendingResponse(message)
	default:
		return nil
	}
}

func (b *stdioBridge) forwardClientRequest(ctx context.Context, message rawJSONRPCMessage) error {
	id, err := parseRequestID(message.ID)
	if err != nil {
		return err
	}

	response, err := b.remote.SendRequest(ctx, transport.JSONRPCRequest{
		JSONRPC: normalizeJSONRPCVersion(message.JSONRPC),
		ID:      id,
		Method:  message.Method,
		Params:  rawParams(message.Params),
	})
	if err != nil {
		return b.writeJSON(map[string]any{
			"jsonrpc": mcpapi.JSONRPC_VERSION,
			"id":      id.Value(),
			"error": map[string]any{
				"code":    -32603,
				"message": err.Error(),
			},
		})
	}
	if response.Error != nil {
		return b.writeJSON(map[string]any{
			"jsonrpc": mcpapi.JSONRPC_VERSION,
			"id":      id.Value(),
			"error":   response.Error,
		})
	}
	return b.writeJSON(map[string]any{
		"jsonrpc": mcpapi.JSONRPC_VERSION,
		"id":      id.Value(),
		"result":  rawParams(response.Result),
	})
}

func (b *stdioBridge) forwardClientNotification(ctx context.Context, message rawJSONRPCMessage) error {
	params, err := notificationParams(message.Params)
	if err != nil {
		return err
	}
	return b.remote.SendNotification(ctx, mcpapi.JSONRPCNotification{
		JSONRPC: normalizeJSONRPCVersion(message.JSONRPC),
		Notification: mcpapi.Notification{
			Method: message.Method,
			Params: params,
		},
	})
}

func (b *stdioBridge) completePendingResponse(message rawJSONRPCMessage) error {
	id, err := parseRequestID(message.ID)
	if err != nil {
		return err
	}

	key := id.String()
	b.pendingMu.Lock()
	ch, ok := b.pendingResponses[key]
	if ok {
		delete(b.pendingResponses, key)
	}
	b.pendingMu.Unlock()
	if !ok {
		return nil
	}

	response := &transport.JSONRPCResponse{
		JSONRPC: normalizeJSONRPCVersion(message.JSONRPC),
		ID:      id,
		Result:  message.Result,
		Error:   message.Error,
	}
	ch <- response
	return nil
}

func (b *stdioBridge) handleRemoteNotification(notification mcpapi.JSONRPCNotification) {
	_ = b.writeJSON(notification)
}

func (b *stdioBridge) handleRemoteRequest(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	wait := make(chan *transport.JSONRPCResponse, 1)
	key := request.ID.String()

	b.pendingMu.Lock()
	b.pendingResponses[key] = wait
	b.pendingMu.Unlock()
	defer func() {
		b.pendingMu.Lock()
		delete(b.pendingResponses, key)
		b.pendingMu.Unlock()
	}()

	if err := b.writeJSON(map[string]any{
		"jsonrpc": normalizeJSONRPCVersion(request.JSONRPC),
		"id":      request.ID.Value(),
		"method":  request.Method,
		"params":  request.Params,
	}); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-wait:
		return response, nil
	}
}

func (b *stdioBridge) writeJSON(value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}

	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if _, err := b.stdout.Write(append(body, '\n')); err != nil {
		return err
	}
	return nil
}

func parseRequestID(raw *json.RawMessage) (mcpapi.RequestId, error) {
	var id mcpapi.RequestId
	if raw == nil {
		return id, fmt.Errorf("missing request id")
	}
	if err := json.Unmarshal(*raw, &id); err != nil {
		return id, err
	}
	return id, nil
}

func notificationParams(raw json.RawMessage) (mcpapi.NotificationParams, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return mcpapi.NotificationParams{}, nil
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return mcpapi.NotificationParams{}, err
	}
	params := mcpapi.NotificationParams{
		AdditionalFields: map[string]any{},
	}
	if meta, ok := fields["_meta"].(map[string]any); ok {
		params.Meta = meta
		delete(fields, "_meta")
	}
	for key, value := range fields {
		params.AdditionalFields[key] = value
	}
	return params, nil
}

func rawParams(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

func normalizeJSONRPCVersion(version string) string {
	if strings.TrimSpace(version) == "" {
		return mcpapi.JSONRPC_VERSION
	}
	return version
}
