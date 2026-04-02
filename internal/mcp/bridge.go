package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	transport "github.com/mark3labs/mcp-go/client/transport"
	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/kanban"
)

const (
	bridgeReconnectWindow       = 5 * time.Second
	bridgeReconnectPollInterval = 100 * time.Millisecond
	initializedNotificationName = "notifications/initialized"
)

type daemonDiscoverFunc func(context.Context, string) (*DaemonEntry, error)

type bridgeRemoteFactory func(DaemonEntry) (transport.BidirectionalInterface, error)

type handshakeReplayMode int

const (
	replayHandshakeNone handshakeReplayMode = iota
	replayHandshakeInitializeOnly
	replayHandshakeFull
)

type stdioBridge struct {
	dbPath string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	discover              daemonDiscoverFunc
	newRemote             bridgeRemoteFactory
	reconnectWindow       time.Duration
	reconnectPollInterval time.Duration

	writeMu sync.Mutex

	remoteMu    sync.RWMutex
	remote      transport.BidirectionalInterface
	remoteEntry *DaemonEntry
	reconnectMu sync.Mutex

	handshakeMu             sync.Mutex
	initializeRequest       *transport.JSONRPCRequest
	initializedNotification *mcpapi.JSONRPCNotification

	pendingMu        sync.Mutex
	pendingResponses map[string]chan *transport.JSONRPCResponse

	issueContextMu sync.Mutex
	issueContext   *bridgeIssueContext
}

type bridgeIssueContext struct {
	IssueID         string
	IssueIdentifier string
	IssueTitle      string
	ProjectID       string
	ProjectName     string
	WorkspacePath   string
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

	bridge := newStdioBridge(dbPath, stdin, stdout, stderr)
	if err := bridge.connect(ctx); err != nil {
		return err
	}
	defer bridge.closeRemote()
	return bridge.serve(ctx)
}

func newStdioBridge(dbPath string, stdin io.Reader, stdout, stderr io.Writer) *stdioBridge {
	return &stdioBridge{
		dbPath:                dbPath,
		stdin:                 stdin,
		stdout:                stdout,
		stderr:                stderr,
		discover:              DiscoverDaemonForDBPath,
		newRemote:             newBridgeRemote,
		reconnectWindow:       bridgeReconnectWindow,
		reconnectPollInterval: bridgeReconnectPollInterval,
		pendingResponses:      map[string]chan *transport.JSONRPCResponse{},
	}
}

func newBridgeRemote(entry DaemonEntry) (transport.BidirectionalInterface, error) {
	return transport.NewStreamableHTTP(entry.BaseURL,
		transport.WithContinuousListening(),
		transport.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + entry.BearerToken,
		}),
	)
}

func (b *stdioBridge) connect(ctx context.Context) error {
	entry, err := b.discover(ctx, b.dbPath)
	if err != nil {
		return err
	}

	remote, err := b.openRemote(ctx, *entry)
	if err != nil {
		return err
	}

	old := b.swapRemote(remote, entry)
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (b *stdioBridge) openRemote(ctx context.Context, entry DaemonEntry) (transport.BidirectionalInterface, error) {
	remote, err := b.newRemote(entry)
	if err != nil {
		return nil, err
	}
	remote.SetNotificationHandler(b.handleRemoteNotification)
	remote.SetRequestHandler(b.handleRemoteRequest)
	if err := remote.Start(ctx); err != nil {
		_ = remote.Close()
		return nil, err
	}
	return remote, nil
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
	request, err := bridgeRequestFromMessage(message)
	if err != nil {
		return err
	}
	if strings.TrimSpace(message.Method) == string(mcpapi.MethodToolsCall) {
		request, err = b.annotateToolsCallRequest(request)
		if err != nil {
			return err
		}
	}

	response, err := b.sendRequest(ctx, request, replayModeForMethod(message.Method))
	if err != nil {
		return b.writeJSON(map[string]any{
			"jsonrpc": mcpapi.JSONRPC_VERSION,
			"id":      request.ID.Value(),
			"error": map[string]any{
				"code":    -32603,
				"message": err.Error(),
			},
		})
	}
	if response.Error != nil {
		return b.writeJSON(map[string]any{
			"jsonrpc": mcpapi.JSONRPC_VERSION,
			"id":      request.ID.Value(),
			"error":   response.Error,
		})
	}
	if message.Method == string(mcpapi.MethodInitialize) {
		b.cacheInitializeRequest(request)
	}
	return b.writeJSON(map[string]any{
		"jsonrpc": mcpapi.JSONRPC_VERSION,
		"id":      request.ID.Value(),
		"result":  rawParams(response.Result),
	})
}

func (b *stdioBridge) forwardClientNotification(ctx context.Context, message rawJSONRPCMessage) error {
	notification, err := bridgeNotificationFromMessage(message)
	if err != nil {
		return err
	}
	if err := b.sendNotification(ctx, notification, replayModeForMethod(message.Method)); err != nil {
		return err
	}
	if message.Method == initializedNotificationName {
		b.cacheInitializedNotification(notification)
	}
	return nil
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

func (b *stdioBridge) sendRequest(ctx context.Context, request transport.JSONRPCRequest, replayMode handshakeReplayMode) (*transport.JSONRPCResponse, error) {
	remote := b.currentRemote()
	if remote == nil {
		if err := b.reconnect(ctx, nil, replayMode); err != nil {
			return nil, err
		}
		remote = b.currentRemote()
	}

	response, err := remote.SendRequest(ctx, request)
	if err == nil || !shouldReconnect(err) {
		return response, err
	}
	if err := b.reconnect(ctx, remote, replayMode); err != nil {
		return nil, err
	}
	if !canReplayRequestMethod(request.Method) {
		return nil, fmt.Errorf("request %q may have been delivered before disconnect; session restored without replay: %w", request.Method, err)
	}
	return b.currentRemote().SendRequest(ctx, request)
}

func (b *stdioBridge) sendNotification(ctx context.Context, notification mcpapi.JSONRPCNotification, replayMode handshakeReplayMode) error {
	remote := b.currentRemote()
	if remote == nil {
		if err := b.reconnect(ctx, nil, replayMode); err != nil {
			return err
		}
		remote = b.currentRemote()
	}

	err := remote.SendNotification(ctx, notification)
	if err == nil || !shouldReconnect(err) {
		return err
	}
	if err := b.reconnect(ctx, remote, replayMode); err != nil {
		return err
	}
	if !canReplayNotificationMethod(notification.Method) {
		return fmt.Errorf("notification %q may have been delivered before disconnect; session restored without replay: %w", notification.Method, err)
	}
	return b.currentRemote().SendNotification(ctx, notification)
}

func (b *stdioBridge) reconnect(ctx context.Context, failed transport.BidirectionalInterface, replayMode handshakeReplayMode) error {
	b.reconnectMu.Lock()
	defer b.reconnectMu.Unlock()

	current := b.currentRemote()
	if current != nil && failed != nil && current != failed {
		return nil
	}

	deadline := time.Now().Add(b.reconnectWindow)
	var lastErr error
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !time.Now().Before(deadline) {
			if lastErr != nil {
				return lastErr
			}
			return fmt.Errorf("timed out waiting for a live Maestro daemon for %s", b.dbPath)
		}

		attemptCtx, cancel := context.WithDeadline(ctx, deadline)
		entry, err := b.discover(attemptCtx, b.dbPath)
		if err == nil {
			remote, openErr := b.openRemote(attemptCtx, *entry)
			if openErr == nil {
				replayErr := b.replayHandshake(attemptCtx, remote, replayMode)
				if replayErr == nil {
					cancel()
					old := b.swapRemote(remote, entry)
					if old != nil {
						_ = old.Close()
					}
					return nil
				}
				lastErr = replayErr
				_ = remote.Close()
			} else {
				lastErr = openErr
			}
		} else {
			lastErr = err
		}
		cancel()

		timer := time.NewTimer(b.reconnectPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (b *stdioBridge) replayHandshake(ctx context.Context, remote transport.BidirectionalInterface, replayMode handshakeReplayMode) error {
	if replayMode == replayHandshakeNone {
		return nil
	}

	initializeRequest, initializedNotification := b.cachedHandshake()
	if initializeRequest == nil {
		return nil
	}

	response, err := remote.SendRequest(ctx, *initializeRequest)
	if err != nil {
		return err
	}
	if response == nil {
		return fmt.Errorf("initialize replay returned no response")
	}
	if response.Error != nil {
		return response.Error.AsError()
	}
	if replayMode != replayHandshakeFull || initializedNotification == nil {
		return nil
	}
	return remote.SendNotification(ctx, *initializedNotification)
}

func (b *stdioBridge) cacheInitializeRequest(request transport.JSONRPCRequest) {
	cloned := cloneJSONRPCRequest(request)
	b.handshakeMu.Lock()
	defer b.handshakeMu.Unlock()
	b.initializeRequest = &cloned
	b.initializedNotification = nil
}

func (b *stdioBridge) cacheInitializedNotification(notification mcpapi.JSONRPCNotification) {
	cloned := notification
	b.handshakeMu.Lock()
	defer b.handshakeMu.Unlock()
	b.initializedNotification = &cloned
}

func (b *stdioBridge) cachedHandshake() (*transport.JSONRPCRequest, *mcpapi.JSONRPCNotification) {
	b.handshakeMu.Lock()
	defer b.handshakeMu.Unlock()

	var initializeRequest *transport.JSONRPCRequest
	if b.initializeRequest != nil {
		cloned := cloneJSONRPCRequest(*b.initializeRequest)
		initializeRequest = &cloned
	}

	var initializedNotification *mcpapi.JSONRPCNotification
	if b.initializedNotification != nil {
		cloned := *b.initializedNotification
		initializedNotification = &cloned
	}
	return initializeRequest, initializedNotification
}

func (b *stdioBridge) currentRemote() transport.BidirectionalInterface {
	b.remoteMu.RLock()
	defer b.remoteMu.RUnlock()
	return b.remote
}

func (b *stdioBridge) swapRemote(remote transport.BidirectionalInterface, entry *DaemonEntry) transport.BidirectionalInterface {
	b.remoteMu.Lock()
	defer b.remoteMu.Unlock()
	old := b.remote
	b.remote = remote
	if entry == nil {
		b.remoteEntry = nil
	} else {
		cloned := *entry
		b.remoteEntry = &cloned
	}
	return old
}

func (b *stdioBridge) closeRemote() {
	old := b.swapRemote(nil, nil)
	if old != nil {
		_ = old.Close()
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

func bridgeRequestFromMessage(message rawJSONRPCMessage) (transport.JSONRPCRequest, error) {
	id, err := parseRequestID(message.ID)
	if err != nil {
		return transport.JSONRPCRequest{}, err
	}
	return transport.JSONRPCRequest{
		JSONRPC: normalizeJSONRPCVersion(message.JSONRPC),
		ID:      id,
		Method:  message.Method,
		Params:  cloneRawParams(message.Params),
	}, nil
}

func (b *stdioBridge) annotateToolsCallRequest(request transport.JSONRPCRequest) (transport.JSONRPCRequest, error) {
	params, err := jsonObjectFromAny(request.Params)
	if err != nil {
		return request, err
	}

	meta := map[string]any{}
	if existing, ok := params["_meta"].(map[string]any); ok {
		meta = cloneJSONMap(existing)
	}
	if meta == nil {
		meta = map[string]any{}
	}

	if args, ok := params["arguments"].(map[string]any); ok {
		toolUseID := strings.TrimSpace(asString(args["tool_use_id"]))
		if toolUseID == "" {
			toolUseID = strings.TrimSpace(fmt.Sprint(request.ID.Value()))
		}
		if toolUseID != "" {
			if _, exists := meta["claudecode/toolUseId"]; !exists {
				meta["claudecode/toolUseId"] = toolUseID
			}
			if _, exists := meta["claude/toolUseId"]; !exists {
				meta["claude/toolUseId"] = toolUseID
			}
		}
	}

	if issueContext, ok := b.issueContextForCurrentWorkspace(); ok && issueContext != nil {
		meta["maestro/issue_id"] = issueContext.IssueID
		meta["maestro/issue_identifier"] = issueContext.IssueIdentifier
		meta["maestro/issue_title"] = issueContext.IssueTitle
		meta["maestro/project_id"] = issueContext.ProjectID
		meta["maestro/project_name"] = issueContext.ProjectName
		meta["maestro/workspace_path"] = issueContext.WorkspacePath
	}

	if len(meta) > 0 {
		params["_meta"] = meta
		request.Params = params
	}
	return request, nil
}

func bridgeNotificationFromMessage(message rawJSONRPCMessage) (mcpapi.JSONRPCNotification, error) {
	params, err := notificationParams(message.Params)
	if err != nil {
		return mcpapi.JSONRPCNotification{}, err
	}
	return mcpapi.JSONRPCNotification{
		JSONRPC: normalizeJSONRPCVersion(message.JSONRPC),
		Notification: mcpapi.Notification{
			Method: message.Method,
			Params: params,
		},
	}, nil
}

func (b *stdioBridge) issueContextForCurrentWorkspace() (*bridgeIssueContext, bool) {
	if b == nil {
		return nil, false
	}

	b.issueContextMu.Lock()
	if b.issueContext != nil {
		cached := *b.issueContext
		b.issueContextMu.Unlock()
		return &cached, true
	}
	b.issueContextMu.Unlock()

	ctx, err := discoverBridgeIssueContext(b.dbPath)
	if err != nil || ctx == nil {
		return nil, false
	}

	b.issueContextMu.Lock()
	if b.issueContext == nil {
		b.issueContext = ctx
	}
	cached := *b.issueContext
	b.issueContextMu.Unlock()
	return &cached, true
}

func discoverBridgeIssueContext(dbPath string) (*bridgeIssueContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	cwdPath := filepath.Clean(absCwd)

	store, err := kanban.NewReadOnlyStore(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()

	const pageSize = 500
	for offset := 0; ; offset += pageSize {
		summaries, total, err := store.ListIssueSummaries(kanban.IssueQuery{
			Limit:  pageSize,
			Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		for i := range summaries {
			summary := summaries[i]
			workspacePath := strings.TrimSpace(summary.WorkspacePath)
			if workspacePath == "" {
				continue
			}
			absWorkspacePath, err := filepath.Abs(workspacePath)
			if err != nil {
				continue
			}
			if filepath.Clean(absWorkspacePath) != cwdPath {
				continue
			}
			return &bridgeIssueContext{
				IssueID:         strings.TrimSpace(summary.ID),
				IssueIdentifier: strings.TrimSpace(summary.Identifier),
				IssueTitle:      strings.TrimSpace(summary.Title),
				ProjectID:       strings.TrimSpace(summary.ProjectID),
				ProjectName:     strings.TrimSpace(summary.ProjectName),
				WorkspacePath:   cwdPath,
			}, nil
		}
		if offset+len(summaries) >= total || len(summaries) == 0 {
			break
		}
	}
	return nil, nil
}

func jsonObjectFromAny(value any) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func rawParams(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

func cloneRawParams(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func cloneJSONRPCRequest(request transport.JSONRPCRequest) transport.JSONRPCRequest {
	cloned := request
	if raw, ok := request.Params.(json.RawMessage); ok {
		cloned.Params = append(json.RawMessage(nil), raw...)
	}
	return cloned
}

func replayModeForMethod(method string) handshakeReplayMode {
	switch method {
	case string(mcpapi.MethodInitialize):
		return replayHandshakeNone
	case initializedNotificationName:
		return replayHandshakeInitializeOnly
	default:
		return replayHandshakeFull
	}
}

func canReplayRequestMethod(method string) bool {
	switch method {
	case string(mcpapi.MethodInitialize),
		string(mcpapi.MethodPing),
		string(mcpapi.MethodResourcesList),
		string(mcpapi.MethodResourcesTemplatesList),
		string(mcpapi.MethodResourcesRead),
		string(mcpapi.MethodPromptsList),
		string(mcpapi.MethodPromptsGet),
		string(mcpapi.MethodToolsList),
		string(mcpapi.MethodListRoots),
		string(mcpapi.MethodTasksGet),
		string(mcpapi.MethodTasksList),
		string(mcpapi.MethodTasksResult),
		string(mcpapi.MethodCompletionComplete):
		return true
	default:
		return false
	}
}

func canReplayNotificationMethod(method string) bool {
	return method == initializedNotificationName
}

func shouldReconnect(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, transport.ErrSessionTerminated) {
		return true
	}
	var requestErr *url.Error
	return errors.As(err, &requestErr)
}

func normalizeJSONRPCVersion(version string) string {
	if strings.TrimSpace(version) == "" {
		return mcpapi.JSONRPC_VERSION
	}
	return version
}
