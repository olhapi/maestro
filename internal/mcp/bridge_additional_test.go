package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	transport "github.com/mark3labs/mcp-go/client/transport"
	mcpapi "github.com/mark3labs/mcp-go/mcp"
)

type scannerErrorReader struct {
	first bool
}

func (r *scannerErrorReader) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		copy(p, " \n")
		return 2, nil
	}
	return 0, errors.New("scanner boom")
}

func TestMCPBridgeServeBridgeStdioSuccess(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "1")
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
		inMemoryDaemonBasePort.Store(0)
	})

	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	store := testStore(t, dbPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version")
	if err != nil {
		t.Fatalf("StartManagedDaemon failed: %v", err)
	}
	defer func() { _ = handle.Close() }()

	stdin := bytes.NewBuffer(nil)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := ServeBridgeStdio(context.Background(), store, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("ServeBridgeStdio failed: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no bridge output for an empty stdin, got %q", stdout.String())
	}
}

func TestMCPBridgeServeBridgeStdioPathConnectFailure(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := ServeBridgeStdioPath(context.Background(), filepath.Join(t.TempDir(), "missing.db"), bytes.NewBuffer(nil), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no live Maestro daemon found") {
		t.Fatalf("expected connect failure for missing daemon, got %v", err)
	}
}

func TestMCPBridgeConnectServeAndReplayBranches(t *testing.T) {
	bridge := newStdioBridge("db", bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{})
	bridge.discover = func(context.Context, string) (*DaemonEntry, error) {
		return nil, errors.New("discover boom")
	}
	if err := bridge.connect(context.Background()); err == nil || !strings.Contains(err.Error(), "discover boom") {
		t.Fatalf("expected discover error, got %v", err)
	}

	bridge = newStdioBridge("db", bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{})
	bridge.discover = func(context.Context, string) (*DaemonEntry, error) {
		return &DaemonEntry{BaseURL: "http://example/mcp", BearerToken: "token"}, nil
	}
	bridge.newRemote = func(entry DaemonEntry) (transport.BidirectionalInterface, error) {
		return nil, errors.New("open boom")
	}
	if err := bridge.connect(context.Background()); err == nil || !strings.Contains(err.Error(), "open boom") {
		t.Fatalf("expected newRemote error, got %v", err)
	}

	var closed bool
	oldRemote := &fakeBridgeRemote{
		closeFunc: func() error {
			closed = true
			return nil
		},
	}
	newRemote := &fakeBridgeRemote{
		startFunc: func(context.Context) error {
			return nil
		},
	}
	bridge = newStdioBridge("db", bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{})
	bridge.remote = oldRemote
	bridge.discover = func(context.Context, string) (*DaemonEntry, error) {
		return &DaemonEntry{BaseURL: "http://example/mcp", BearerToken: "token"}, nil
	}
	bridge.newRemote = func(entry DaemonEntry) (transport.BidirectionalInterface, error) {
		return newRemote, nil
	}
	if err := bridge.connect(context.Background()); err != nil {
		t.Fatalf("connect success failed: %v", err)
	}
	if !closed {
		t.Fatal("expected previous remote to be closed")
	}
	if bridge.currentRemote() != newRemote {
		t.Fatalf("expected new remote to be installed, got %#v", bridge.currentRemote())
	}

	bridge = &stdioBridge{
		dbPath:           "db",
		stdin:            &scannerErrorReader{},
		stdout:           &bytes.Buffer{},
		pendingResponses: map[string]chan *transport.JSONRPCResponse{},
	}
	if err := bridge.serve(context.Background()); err == nil || !strings.Contains(err.Error(), "scanner boom") {
		t.Fatalf("expected scanner error to surface, got %v", err)
	}

	bridge = &stdioBridge{remote: &fakeBridgeRemote{}}
	bridge.discover = func(context.Context, string) (*DaemonEntry, error) {
		t.Fatal("discover should not be called when another remote is already installed")
		return nil, nil
	}
	if err := bridge.reconnect(context.Background(), &fakeBridgeRemote{}, replayHandshakeFull); err != nil {
		t.Fatalf("expected reconnect to skip when a different remote is already installed, got %v", err)
	}

	bridge = &stdioBridge{}
	if err := bridge.replayHandshake(context.Background(), &fakeBridgeRemote{}, replayHandshakeNone); err != nil {
		t.Fatalf("expected replayHandshakeNone to short-circuit, got %v", err)
	}
	if err := bridge.replayHandshake(context.Background(), &fakeBridgeRemote{}, replayHandshakeFull); err != nil {
		t.Fatalf("expected missing handshake cache to short-circuit, got %v", err)
	}

	bridge = &stdioBridge{
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
	if _, err := bridge.sendRequest(context.Background(), transport.JSONRPCRequest{ID: mcpapi.NewRequestId("req-5"), Method: "tools/list"}, replayHandshakeFull); err == nil || !strings.Contains(err.Error(), "no daemon") {
		t.Fatalf("expected reconnect failure to surface discover error, got %v", err)
	}
}

func TestMCPBridgeSendNotificationReconnectRestriction(t *testing.T) {
	bridge := &stdioBridge{
		dbPath:          "db",
		stdout:          &bytes.Buffer{},
		reconnectWindow: 25 * time.Millisecond,
		discover: func(context.Context, string) (*DaemonEntry, error) {
			return &DaemonEntry{BaseURL: "http://example/mcp", BearerToken: "token"}, nil
		},
		newRemote: func(entry DaemonEntry) (transport.BidirectionalInterface, error) {
			return &fakeBridgeRemote{
				sendNotificationFunc: func(context.Context, mcpapi.JSONRPCNotification) error {
					return nil
				},
			}, nil
		},
	}
	bridge.swapRemote(&fakeBridgeRemote{
		sendNotificationFunc: func(context.Context, mcpapi.JSONRPCNotification) error {
			return transport.ErrSessionTerminated
		},
	}, nil)

	if err := bridge.sendNotification(context.Background(), mcpapi.JSONRPCNotification{Notification: mcpapi.Notification{Method: "notifications/other"}}, replayHandshakeFull); err == nil || !strings.Contains(err.Error(), "may have been delivered before disconnect") {
		t.Fatalf("expected non-replayable notification to fail after reconnect, got %v", err)
	}
}

func TestMCPBridgeSendNotificationWithNilRemote(t *testing.T) {
	bridge := &stdioBridge{
		dbPath:          "db",
		reconnectWindow: 25 * time.Millisecond,
		discover: func(context.Context, string) (*DaemonEntry, error) {
			return &DaemonEntry{BaseURL: "http://example/mcp", BearerToken: "token"}, nil
		},
		newRemote: func(entry DaemonEntry) (transport.BidirectionalInterface, error) {
			return &fakeBridgeRemote{}, nil
		},
	}

	if err := bridge.sendNotification(context.Background(), mcpapi.JSONRPCNotification{Notification: mcpapi.Notification{Method: initializedNotificationName}}, replayHandshakeNone); err != nil {
		t.Fatalf("expected notification to connect and send, got %v", err)
	}
}

func TestMCPBridgeAnnotatesToolsCallRequestsWithMaestroMetadata(t *testing.T) {
	bridge := &stdioBridge{
		issueContext: &bridgeIssueContext{
			IssueID:         "issue-1",
			IssueIdentifier: "MAES-19",
			IssueTitle:      "Bridge approval prompt",
			ProjectID:       "project-1",
			ProjectName:     "Maestro",
			WorkspacePath:   "/tmp/workspace",
		},
	}

	request, err := bridge.annotateToolsCallRequest(transport.JSONRPCRequest{
		JSONRPC: mcpapi.JSONRPC_VERSION,
		ID:      mcpapi.NewRequestId("req-1"),
		Method:  string(mcpapi.MethodToolsCall),
		Params:  json.RawMessage(`{"name":"approval_prompt","arguments":{"tool_name":"Bash","input":{"command":"pwd"},"tool_use_id":"toolu_123"},"_meta":{"existing":"value"}}`),
	})
	if err != nil {
		t.Fatalf("annotateToolsCallRequest failed: %v", err)
	}

	params, err := jsonObjectFromAny(request.Params)
	if err != nil {
		t.Fatalf("jsonObjectFromAny failed: %v", err)
	}
	meta, ok := params["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected _meta payload, got %#v", params["_meta"])
	}
	if got := meta["existing"]; got != "value" {
		t.Fatalf("expected existing meta to be preserved, got %#v", got)
	}
	if got := meta["claudecode/toolUseId"]; got != "toolu_123" {
		t.Fatalf("expected tool-use id meta, got %#v", got)
	}
	if got := meta["maestro/issue_id"]; got != "issue-1" {
		t.Fatalf("expected issue id meta, got %#v", got)
	}
	if got := meta["maestro/issue_identifier"]; got != "MAES-19" {
		t.Fatalf("expected issue identifier meta, got %#v", got)
	}
	if got := meta["maestro/workspace_path"]; got != "/tmp/workspace" {
		t.Fatalf("expected workspace path meta, got %#v", got)
	}

	fallbackRequest, err := bridge.annotateToolsCallRequest(transport.JSONRPCRequest{
		JSONRPC: mcpapi.JSONRPC_VERSION,
		ID:      mcpapi.NewRequestId("req-2"),
		Method:  string(mcpapi.MethodToolsCall),
		Params:  json.RawMessage(`{"name":"approval_prompt","arguments":{"tool_name":"Bash","input":{"command":"pwd"}},"_meta":{"existing":"value"}}`),
	})
	if err != nil {
		t.Fatalf("annotateToolsCallRequest fallback failed: %v", err)
	}

	fallbackParams, err := jsonObjectFromAny(fallbackRequest.Params)
	if err != nil {
		t.Fatalf("jsonObjectFromAny fallback failed: %v", err)
	}
	fallbackMeta, ok := fallbackParams["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected fallback _meta payload, got %#v", fallbackParams["_meta"])
	}
	if got := fallbackMeta["claudecode/toolUseId"]; got != "req-2" {
		t.Fatalf("expected request id fallback tool-use id, got %#v", got)
	}
	if got := fallbackMeta["claude/toolUseId"]; got != "req-2" {
		t.Fatalf("expected legacy tool-use id fallback, got %#v", got)
	}
}

func TestMCPBridgeReconnectContextCanceledAndOpenError(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	bridge := &stdioBridge{dbPath: "db", reconnectWindow: 25 * time.Millisecond}
	if err := bridge.reconnect(canceledCtx, nil, replayHandshakeFull); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("expected canceled context to stop reconnect, got %v", err)
	}

	bridge = &stdioBridge{
		dbPath:                "db",
		reconnectWindow:       25 * time.Millisecond,
		reconnectPollInterval: 1 * time.Millisecond,
		discover: func(context.Context, string) (*DaemonEntry, error) {
			return &DaemonEntry{BaseURL: "http://example/mcp", BearerToken: "token"}, nil
		},
		newRemote: func(entry DaemonEntry) (transport.BidirectionalInterface, error) {
			return nil, errors.New("open boom")
		},
	}
	if err := bridge.reconnect(context.Background(), nil, replayHandshakeFull); err == nil || !strings.Contains(err.Error(), "open boom") {
		t.Fatalf("expected openRemote error to surface, got %v", err)
	}
}
