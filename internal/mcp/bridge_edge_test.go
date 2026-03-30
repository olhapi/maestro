package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	transport "github.com/mark3labs/mcp-go/client/transport"
	mcpapi "github.com/mark3labs/mcp-go/mcp"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestMCPBridgeValidationAndWriteErrors(t *testing.T) {
	if err := ServeBridgeStdioPath(context.Background(), "db", bytes.NewBuffer(nil), nil, &bytes.Buffer{}); err == nil {
		t.Fatal("expected nil stdout to be rejected")
	}
	if err := ServeBridgeStdioPath(context.Background(), "db", nil, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected nil stdin to be rejected")
	}

	bridge := &stdioBridge{
		stdout:           failingWriter{},
		pendingResponses: map[string]chan *transport.JSONRPCResponse{},
	}
	if err := bridge.writeJSON(map[string]any{"func": func() {}}); err == nil {
		t.Fatal("expected JSON encoding failure to be surfaced")
	}
	if _, err := bridge.handleRemoteRequest(context.Background(), transport.JSONRPCRequest{ID: mcpapi.NewRequestId("req-write"), Method: "roots/list"}); err == nil {
		t.Fatal("expected handleRemoteRequest write failure to be surfaced")
	}

	if err := bridge.handleIncomingMessage(context.Background(), []byte(`{`)); err == nil {
		t.Fatal("expected invalid JSON to be rejected")
	}
	if err := bridge.handleIncomingMessage(context.Background(), []byte(`{"jsonrpc":"2.0"}`)); err != nil {
		t.Fatalf("expected empty message to be ignored, got %v", err)
	}

	if err := bridge.completePendingResponse(rawJSONRPCMessage{ID: func() *json.RawMessage {
		raw := json.RawMessage(`"req-1"`)
		return &raw
	}()}); err != nil {
		t.Fatalf("completePendingResponse without pending request failed: %v", err)
	}
}

func TestMCPBridgeForwardingErrorBranches(t *testing.T) {
	stdout := &bytes.Buffer{}
	bridge := newStdioBridge("db", bytes.NewBuffer(nil), stdout, &bytes.Buffer{})

	bridge.remote = &fakeBridgeRemote{
		sendRequestFunc: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			return nil, errors.New("request boom")
		},
	}
	if err := bridge.handleIncomingMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":"req-1","method":"tools/list","params":{}}`)); err != nil {
		t.Fatalf("handleIncomingMessage(request error) failed: %v", err)
	}
	responses := decodeBridgeResponses(t, stdout.Bytes())
	if len(responses) != 1 || responses[0]["error"] == nil {
		t.Fatalf("expected request error response, got %#v", responses)
	}

	stdout.Reset()
	bridge.remote = &fakeBridgeRemote{
		sendRequestFunc: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			return &transport.JSONRPCResponse{
				JSONRPC: mcpapi.JSONRPC_VERSION,
				ID:      mcpapi.NewRequestId("req-2"),
				Error:   &mcpapi.JSONRPCErrorDetails{Code: -1, Message: "remote error"},
			}, nil
		},
	}
	if err := bridge.handleIncomingMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":"req-2","method":"tools/list","params":{}}`)); err != nil {
		t.Fatalf("handleIncomingMessage(response error) failed: %v", err)
	}
	responses = decodeBridgeResponses(t, stdout.Bytes())
	if len(responses) != 1 || responses[0]["error"] == nil {
		t.Fatalf("expected remote error response, got %#v", responses)
	}

	stdout.Reset()
	bridge.remote = &fakeBridgeRemote{
		sendNotificationFunc: func(context.Context, mcpapi.JSONRPCNotification) error {
			return errors.New("notification boom")
		},
	}
	if err := bridge.handleIncomingMessage(context.Background(), []byte(`{"method":"notifications/initialized","params":{}}`)); err == nil || !strings.Contains(err.Error(), "notification boom") {
		t.Fatalf("expected notification error to propagate, got %v", err)
	}
	if len(stdout.Bytes()) != 0 {
		t.Fatalf("expected notification error to avoid stdout output, got %q", stdout.String())
	}
}

func TestMCPBridgeReconnectAndReplayErrorBranches(t *testing.T) {
	bridge := &stdioBridge{
		dbPath:                "db",
		stdout:                &bytes.Buffer{},
		pendingResponses:      map[string]chan *transport.JSONRPCResponse{},
		reconnectWindow:       10 * time.Millisecond,
		reconnectPollInterval: 1 * time.Millisecond,
		discover: func(context.Context, string) (*DaemonEntry, error) {
			return nil, errors.New("no daemon")
		},
		newRemote: func(entry DaemonEntry) (transport.BidirectionalInterface, error) {
			return nil, errors.New("unexpected open")
		},
	}

	timeoutBridge := &stdioBridge{
		dbPath:          "db",
		reconnectWindow: 0,
	}
	if err := timeoutBridge.reconnect(context.Background(), nil, replayHandshakeFull); err == nil || !strings.Contains(err.Error(), "timed out waiting for a live Maestro daemon") {
		t.Fatalf("expected zero-window reconnect timeout, got %v", err)
	}

	if _, err := bridge.sendRequest(context.Background(), transport.JSONRPCRequest{ID: mcpapi.NewRequestId("req-3"), Method: "tools/list"}, replayHandshakeFull); err == nil || !strings.Contains(err.Error(), "no daemon") {
		t.Fatalf("expected reconnect timeout to surface discover error, got %v", err)
	}
	if err := bridge.sendNotification(context.Background(), mcpapi.JSONRPCNotification{Notification: mcpapi.Notification{Method: "notifications/initialized"}}, replayHandshakeFull); err == nil || !strings.Contains(err.Error(), "no daemon") {
		t.Fatalf("expected notification reconnect failure, got %v", err)
	}

	request := transport.JSONRPCRequest{
		JSONRPC: mcpapi.JSONRPC_VERSION,
		ID:      mcpapi.NewRequestId("req-4"),
		Method:  string(mcpapi.MethodInitialize),
		Params:  json.RawMessage(`{"protocolVersion":"` + mcpapi.LATEST_PROTOCOL_VERSION + `"}`),
	}
	bridge.cacheInitializeRequest(request)
	remote := &fakeBridgeRemote{
		sendRequestFunc: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			return nil, nil
		},
	}
	if err := bridge.replayHandshake(context.Background(), remote, replayHandshakeFull); err == nil || !strings.Contains(err.Error(), "no response") {
		t.Fatalf("expected replay handshake to reject nil response, got %v", err)
	}

	remote.sendRequestFunc = func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
		return &transport.JSONRPCResponse{
			JSONRPC: mcpapi.JSONRPC_VERSION,
			ID:      mcpapi.NewRequestId("req-4"),
			Error:   &mcpapi.JSONRPCErrorDetails{Code: -1, Message: "bad replay"},
		}, nil
	}
	if err := bridge.replayHandshake(context.Background(), remote, replayHandshakeFull); err == nil || !strings.Contains(err.Error(), "bad replay") {
		t.Fatalf("expected replay handshake error to propagate, got %v", err)
	}
}
