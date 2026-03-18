package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	transport "github.com/mark3labs/mcp-go/client/transport"
	mcpapi "github.com/mark3labs/mcp-go/mcp"
)

func TestServeBridgeStdioPathForwardsRequestsToDaemon(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())

	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	store := testStore(t, dbPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version")
	if err != nil {
		t.Fatalf("StartManagedDaemon failed: %v", err)
	}
	defer func() { _ = handle.Close() }()

	stdin := bytes.NewBufferString(
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"" + mcpapi.LATEST_PROTOCOL_VERSION + "\",\"clientInfo\":{\"name\":\"bridge-test\",\"version\":\"1.0.0\"},\"capabilities\":{}}}\n" +
			"{\"method\":\"notifications/initialized\",\"params\":{}}\n" +
			"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n" +
			"{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"server_info\",\"arguments\":{}}}\n",
	)
	var stdout bytes.Buffer

	if err := ServeBridgeStdioPath(context.Background(), dbPath, stdin, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("ServeBridgeStdioPath failed: %v", err)
	}

	responses := decodeBridgeResponses(t, stdout.Bytes())
	if len(responses) != 3 {
		t.Fatalf("expected three bridge responses, got %d: %s", len(responses), stdout.String())
	}

	initialize := responseByID(t, responses, float64(1))
	if _, ok := initialize["result"].(map[string]any); !ok {
		t.Fatalf("expected initialize result payload, got %#v", initialize)
	}

	toolsList := responseByID(t, responses, float64(2))
	if len(toolsList["result"].(map[string]any)["tools"].([]any)) == 0 {
		t.Fatalf("expected at least one tool in tools/list response, got %#v", toolsList)
	}

	serverInfo := responseByID(t, responses, float64(3))
	result := serverInfo["result"].(map[string]any)
	content := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("expected tool content in server_info response, got %#v", serverInfo)
	}
	text := content[0].(map[string]any)["text"].(string)
	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("decode server_info envelope: %v", err)
	}
	data := envelope["data"].(map[string]any)
	if runtimeAvailable, _ := data["runtime_available"].(bool); !runtimeAvailable {
		t.Fatalf("expected runtime_available=true, got %#v", envelope)
	}
}

func TestBridgeHandleRemoteRequestRoundTrip(t *testing.T) {
	stdoutCh := make(chan []byte, 1)
	bridge := &stdioBridge{
		stdout:           channelWriter(stdoutCh),
		pendingResponses: map[string]chan *transport.JSONRPCResponse{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	responseCh := make(chan *transport.JSONRPCResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		response, err := bridge.handleRemoteRequest(ctx, transport.JSONRPCRequest{
			ID:     mcpapi.NewRequestId("req-7"),
			Method: "roots/list",
			Params: map[string]any{"cursor": "next"},
		})
		if err != nil {
			errCh <- err
			return
		}
		responseCh <- response
	}()

	var stdout []byte
	select {
	case stdout = <-stdoutCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge output")
	}

	requests := decodeBridgeResponses(t, stdout)
	if len(requests) != 1 {
		t.Fatalf("expected one forwarded client request, got %d", len(requests))
	}
	outgoing := requests[0]
	if outgoing["jsonrpc"] != mcpapi.JSONRPC_VERSION {
		t.Fatalf("expected default jsonrpc version %q, got %#v", mcpapi.JSONRPC_VERSION, outgoing["jsonrpc"])
	}
	if outgoing["method"] != "roots/list" {
		t.Fatalf("expected roots/list request, got %#v", outgoing)
	}

	idRaw := json.RawMessage(`"req-7"`)
	if err := bridge.completePendingResponse(rawJSONRPCMessage{
		ID:      &idRaw,
		JSONRPC: "",
		Result:  json.RawMessage(`{"roots":[]}`),
	}); err != nil {
		t.Fatalf("completePendingResponse failed: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("handleRemoteRequest returned error: %v", err)
	case response := <-responseCh:
		if response.JSONRPC != mcpapi.JSONRPC_VERSION {
			t.Fatalf("expected normalized jsonrpc version %q, got %q", mcpapi.JSONRPC_VERSION, response.JSONRPC)
		}
		var result map[string]any
		if err := json.Unmarshal(response.Result, &result); err != nil {
			t.Fatalf("decode remote response result: %v", err)
		}
		roots := result["roots"].([]any)
		if len(roots) != 0 {
			t.Fatalf("expected empty roots list, got %#v", roots)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote request response")
	}

	if err := bridge.completePendingResponse(rawJSONRPCMessage{ID: &idRaw}); err != nil {
		t.Fatalf("completePendingResponse should ignore unknown ids: %v", err)
	}
	if len(bridge.pendingResponses) != 0 {
		t.Fatalf("expected pending responses to be cleared, got %d", len(bridge.pendingResponses))
	}
}

func TestBridgeHelperFunctions(t *testing.T) {
	if err := ServeBridgeStdio(context.Background(), nil, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected ServeBridgeStdio to reject a nil store")
	}

	if _, err := parseRequestID(nil); err == nil {
		t.Fatal("expected parseRequestID to reject a missing id")
	}

	params, err := notificationParams(json.RawMessage(`{"_meta":{"origin":"client"},"scope":"all"}`))
	if err != nil {
		t.Fatalf("notificationParams failed: %v", err)
	}
	if params.Meta["origin"] != "client" {
		t.Fatalf("expected metadata to be preserved, got %#v", params.Meta)
	}
	if params.AdditionalFields["scope"] != "all" {
		t.Fatalf("expected additional fields to be preserved, got %#v", params.AdditionalFields)
	}

	if _, err := notificationParams(json.RawMessage(`[`)); err == nil {
		t.Fatal("expected notificationParams to reject invalid JSON")
	}

	if got := rawParams(json.RawMessage(`null`)); got != nil {
		t.Fatalf("expected nil raw params for null, got %#v", got)
	}
	if got := normalizeJSONRPCVersion(""); got != mcpapi.JSONRPC_VERSION {
		t.Fatalf("expected default jsonrpc version %q, got %q", mcpapi.JSONRPC_VERSION, got)
	}
}

func TestBridgeReconnectReplaysHandshakeAndRetriesRequest(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "maestro.db")

	entryA := DaemonEntry{StoreID: "store-a", DBPath: dbPath, BaseURL: "http://daemon-a/mcp", BearerToken: "token-a"}
	entryB := DaemonEntry{StoreID: "store-b", DBPath: dbPath, BaseURL: "http://daemon-b/mcp", BearerToken: "token-b"}

	var (
		discoverCalls int
		remoteBMu     sync.Mutex
		remoteBOrder  []string
	)

	remoteA := &fakeBridgeRemote{
		sendRequestFunc: func(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			switch request.Method {
			case string(mcpapi.MethodInitialize):
				return fakeBridgeResponse(request.ID, `{"protocolVersion":"`+mcpapi.LATEST_PROTOCOL_VERSION+`"}`), nil
			case "tools/list":
				return nil, fmt.Errorf("request failed: %w", transport.ErrSessionTerminated)
			default:
				return nil, fmt.Errorf("unexpected remote A request %q", request.Method)
			}
		},
		sendNotificationFunc: func(ctx context.Context, notification mcpapi.JSONRPCNotification) error {
			if notification.Method != initializedNotificationName {
				return fmt.Errorf("unexpected remote A notification %q", notification.Method)
			}
			return nil
		},
	}

	remoteB := &fakeBridgeRemote{
		sendRequestFunc: func(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			remoteBMu.Lock()
			remoteBOrder = append(remoteBOrder, request.Method)
			remoteBMu.Unlock()
			switch request.Method {
			case string(mcpapi.MethodInitialize):
				return fakeBridgeResponse(request.ID, `{"protocolVersion":"`+mcpapi.LATEST_PROTOCOL_VERSION+`"}`), nil
			case "tools/list":
				return fakeBridgeResponse(request.ID, `{"tools":[{"name":"server_info"}]}`), nil
			default:
				return nil, fmt.Errorf("unexpected remote B request %q", request.Method)
			}
		},
		sendNotificationFunc: func(ctx context.Context, notification mcpapi.JSONRPCNotification) error {
			remoteBMu.Lock()
			remoteBOrder = append(remoteBOrder, notification.Method)
			remoteBMu.Unlock()
			if notification.Method != initializedNotificationName {
				return fmt.Errorf("unexpected remote B notification %q", notification.Method)
			}
			return nil
		},
	}

	bridge := newStdioBridge(dbPath, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{})
	bridge.discover = func(ctx context.Context, gotDBPath string) (*DaemonEntry, error) {
		discoverCalls++
		if gotDBPath != dbPath {
			return nil, fmt.Errorf("unexpected db path %q", gotDBPath)
		}
		if discoverCalls == 1 {
			return &entryA, nil
		}
		return &entryB, nil
	}
	bridge.newRemote = func(entry DaemonEntry) (transport.BidirectionalInterface, error) {
		switch entry.BaseURL {
		case entryA.BaseURL:
			return remoteA, nil
		case entryB.BaseURL:
			return remoteB, nil
		default:
			return nil, fmt.Errorf("unexpected remote entry %#v", entry)
		}
	}

	if err := bridge.connect(ctx); err != nil {
		t.Fatalf("bridge.connect failed: %v", err)
	}

	var stdout bytes.Buffer
	bridge.stdout = &stdout
	sendBridgeMessage(t, bridge, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+mcpapi.LATEST_PROTOCOL_VERSION+`","clientInfo":{"name":"bridge-test","version":"1.0.0"},"capabilities":{}}}`, &stdout)
	sendBridgeMessage(t, bridge, `{"method":"notifications/initialized","params":{}}`, &stdout)
	responses := sendBridgeMessage(t, bridge, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, &stdout)

	tools := responseByID(t, responses, float64(2))["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected retried tools/list to succeed, got %#v", responses)
	}
	if remoteA.requestCount("tools/list") != 1 {
		t.Fatalf("expected one failed tools/list attempt on remote A, got %d", remoteA.requestCount("tools/list"))
	}
	remoteBMu.Lock()
	defer remoteBMu.Unlock()
	wantOrder := []string{string(mcpapi.MethodInitialize), initializedNotificationName, "tools/list"}
	if fmt.Sprint(remoteBOrder) != fmt.Sprint(wantOrder) {
		t.Fatalf("unexpected remote B replay order: got %v want %v", remoteBOrder, wantOrder)
	}
}

func TestBridgeReconnectRetriesInitializedNotification(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "maestro.db")

	entryA := DaemonEntry{StoreID: "store-a", DBPath: dbPath, BaseURL: "http://daemon-a/mcp", BearerToken: "token-a"}
	entryB := DaemonEntry{StoreID: "store-b", DBPath: dbPath, BaseURL: "http://daemon-b/mcp", BearerToken: "token-b"}

	var discoverCalls int
	var remoteBNotifications []string

	remoteA := &fakeBridgeRemote{
		sendRequestFunc: func(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			return fakeBridgeResponse(request.ID, `{"protocolVersion":"`+mcpapi.LATEST_PROTOCOL_VERSION+`"}`), nil
		},
		sendNotificationFunc: func(ctx context.Context, notification mcpapi.JSONRPCNotification) error {
			return fmt.Errorf("notification failed: %w", transport.ErrSessionTerminated)
		},
	}

	remoteB := &fakeBridgeRemote{
		sendRequestFunc: func(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			if request.Method != string(mcpapi.MethodInitialize) {
				return nil, fmt.Errorf("unexpected remote B request %q", request.Method)
			}
			return fakeBridgeResponse(request.ID, `{"protocolVersion":"`+mcpapi.LATEST_PROTOCOL_VERSION+`"}`), nil
		},
		sendNotificationFunc: func(ctx context.Context, notification mcpapi.JSONRPCNotification) error {
			remoteBNotifications = append(remoteBNotifications, notification.Method)
			return nil
		},
	}

	bridge := newStdioBridge(dbPath, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{})
	bridge.discover = func(ctx context.Context, gotDBPath string) (*DaemonEntry, error) {
		discoverCalls++
		if discoverCalls == 1 {
			return &entryA, nil
		}
		return &entryB, nil
	}
	bridge.newRemote = func(entry DaemonEntry) (transport.BidirectionalInterface, error) {
		if entry.BaseURL == entryA.BaseURL {
			return remoteA, nil
		}
		return remoteB, nil
	}

	if err := bridge.connect(ctx); err != nil {
		t.Fatalf("bridge.connect failed: %v", err)
	}

	var stdout bytes.Buffer
	bridge.stdout = &stdout
	sendBridgeMessage(t, bridge, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+mcpapi.LATEST_PROTOCOL_VERSION+`","clientInfo":{"name":"bridge-test","version":"1.0.0"},"capabilities":{}}}`, &stdout)
	if _, err := sendBridgeNotification(t, bridge, `{"method":"notifications/initialized","params":{}}`, &stdout); err != nil {
		t.Fatalf("notifications/initialized should reconnect successfully: %v", err)
	}

	if fmt.Sprint(remoteBNotifications) != fmt.Sprint([]string{initializedNotificationName}) {
		t.Fatalf("expected remote B to receive retried initialized notification, got %v", remoteBNotifications)
	}
}

func TestBridgeSurvivesDaemonRestartWithoutRestartingBridge(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())

	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	store := testStore(t, dbPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handleA, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version-a")
	if err != nil {
		t.Fatalf("StartManagedDaemon A failed: %v", err)
	}

	var stdout bytes.Buffer
	bridge := newStdioBridge(dbPath, bytes.NewBuffer(nil), &stdout, &bytes.Buffer{})
	if err := bridge.connect(ctx); err != nil {
		t.Fatalf("bridge.connect failed: %v", err)
	}
	defer bridge.closeRemote()

	initializeBridgeSession(t, bridge, &stdout)

	if err := handleA.Close(); err != nil {
		t.Fatalf("handleA.Close failed: %v", err)
	}
	handleB, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version-b")
	if err != nil {
		t.Fatalf("StartManagedDaemon B failed: %v", err)
	}
	defer func() { _ = handleB.Close() }()

	responses := sendBridgeMessage(t, bridge, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, &stdout)
	tools := responseByID(t, responses, float64(2))["result"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("expected tools/list to succeed after daemon restart, got %#v", responses)
	}
}

func TestBridgeReconnectWaitsForReplacementDaemon(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())

	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	store := testStore(t, dbPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handleA, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version-a")
	if err != nil {
		t.Fatalf("StartManagedDaemon A failed: %v", err)
	}

	var stdout bytes.Buffer
	bridge := newStdioBridge(dbPath, bytes.NewBuffer(nil), &stdout, &bytes.Buffer{})
	bridge.reconnectWindow = 800 * time.Millisecond
	bridge.reconnectPollInterval = 20 * time.Millisecond
	if err := bridge.connect(ctx); err != nil {
		t.Fatalf("bridge.connect failed: %v", err)
	}
	defer bridge.closeRemote()

	initializeBridgeSession(t, bridge, &stdout)

	if err := handleA.Close(); err != nil {
		t.Fatalf("handleA.Close failed: %v", err)
	}

	handleBCh := make(chan *DaemonHandle, 1)
	go func() {
		time.Sleep(120 * time.Millisecond)
		handleB, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version-b")
		if err != nil {
			t.Errorf("StartManagedDaemon B failed: %v", err)
			handleBCh <- nil
			return
		}
		handleBCh <- handleB
	}()

	responses := sendBridgeMessage(t, bridge, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, &stdout)
	handleB := <-handleBCh
	if handleB == nil {
		t.Fatal("replacement daemon was not started")
	}
	defer func() { _ = handleB.Close() }()

	tools := responseByID(t, responses, float64(2))["result"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("expected delayed reconnect to succeed, got %#v", responses)
	}
}

func TestBridgeReconnectFailsCleanlyWhenDaemonDoesNotReturn(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())

	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	store := testStore(t, dbPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handleA, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version-a")
	if err != nil {
		t.Fatalf("StartManagedDaemon A failed: %v", err)
	}

	var stdout bytes.Buffer
	bridge := newStdioBridge(dbPath, bytes.NewBuffer(nil), &stdout, &bytes.Buffer{})
	bridge.reconnectWindow = 150 * time.Millisecond
	bridge.reconnectPollInterval = 20 * time.Millisecond
	if err := bridge.connect(ctx); err != nil {
		t.Fatalf("bridge.connect failed: %v", err)
	}
	defer bridge.closeRemote()

	initializeBridgeSession(t, bridge, &stdout)

	if err := handleA.Close(); err != nil {
		t.Fatalf("handleA.Close failed: %v", err)
	}

	responses := sendBridgeMessage(t, bridge, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, &stdout)
	response := responseByID(t, responses, float64(2))
	errPayload, ok := response["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected JSON-RPC error after reconnect timeout, got %#v", response)
	}
	message, _ := errPayload["message"].(string)
	if !bytes.Contains([]byte(message), []byte("no live Maestro daemon found")) {
		t.Fatalf("expected reconnect failure message, got %q", message)
	}
}

func decodeBridgeResponses(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(body), []byte{'\n'})
	responses := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal(line, &decoded); err != nil {
			t.Fatalf("decode bridge output line %q: %v", string(line), err)
		}
		responses = append(responses, decoded)
	}
	return responses
}

func responseByID(t *testing.T, responses []map[string]any, want any) map[string]any {
	t.Helper()
	for _, response := range responses {
		if response["id"] == want {
			return response
		}
	}
	t.Fatalf("missing response for id %#v in %#v", want, responses)
	return nil
}

type channelWriter chan []byte

func (w channelWriter) Write(p []byte) (int, error) {
	w <- append([]byte(nil), p...)
	return len(p), nil
}

type fakeBridgeRemote struct {
	startFunc            func(context.Context) error
	sendRequestFunc      func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error)
	sendNotificationFunc func(context.Context, mcpapi.JSONRPCNotification) error
	closeFunc            func() error

	mu                  sync.Mutex
	requestCounts       map[string]int
	notificationCounts  map[string]int
	notificationHandler func(mcpapi.JSONRPCNotification)
	requestHandler      transport.RequestHandler
}

func (r *fakeBridgeRemote) Start(ctx context.Context) error {
	if r.startFunc != nil {
		return r.startFunc(ctx)
	}
	return nil
}

func (r *fakeBridgeRemote) SendRequest(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	r.mu.Lock()
	if r.requestCounts == nil {
		r.requestCounts = map[string]int{}
	}
	r.requestCounts[request.Method]++
	r.mu.Unlock()
	if r.sendRequestFunc != nil {
		return r.sendRequestFunc(ctx, request)
	}
	return fakeBridgeResponse(request.ID, `{}`), nil
}

func (r *fakeBridgeRemote) SendNotification(ctx context.Context, notification mcpapi.JSONRPCNotification) error {
	r.mu.Lock()
	if r.notificationCounts == nil {
		r.notificationCounts = map[string]int{}
	}
	r.notificationCounts[notification.Method]++
	r.mu.Unlock()
	if r.sendNotificationFunc != nil {
		return r.sendNotificationFunc(ctx, notification)
	}
	return nil
}

func (r *fakeBridgeRemote) SetNotificationHandler(handler func(notification mcpapi.JSONRPCNotification)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notificationHandler = handler
}

func (r *fakeBridgeRemote) SetRequestHandler(handler transport.RequestHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requestHandler = handler
}

func (r *fakeBridgeRemote) Close() error {
	if r.closeFunc != nil {
		return r.closeFunc()
	}
	return nil
}

func (r *fakeBridgeRemote) GetSessionId() string {
	return ""
}

func (r *fakeBridgeRemote) requestCount(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requestCounts[method]
}

func fakeBridgeResponse(id mcpapi.RequestId, body string) *transport.JSONRPCResponse {
	return &transport.JSONRPCResponse{
		JSONRPC: mcpapi.JSONRPC_VERSION,
		ID:      id,
		Result:  json.RawMessage(body),
	}
}

func initializeBridgeSession(t *testing.T, bridge *stdioBridge, stdout *bytes.Buffer) {
	t.Helper()
	sendBridgeMessage(t, bridge, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+mcpapi.LATEST_PROTOCOL_VERSION+`","clientInfo":{"name":"bridge-test","version":"1.0.0"},"capabilities":{}}}`, stdout)
	if _, err := sendBridgeNotification(t, bridge, `{"method":"notifications/initialized","params":{}}`, stdout); err != nil {
		t.Fatalf("notifications/initialized failed: %v", err)
	}
}

func sendBridgeMessage(t *testing.T, bridge *stdioBridge, message string, stdout *bytes.Buffer) []map[string]any {
	t.Helper()
	stdout.Reset()
	if err := bridge.handleIncomingMessage(context.Background(), []byte(message)); err != nil {
		t.Fatalf("handleIncomingMessage(%s) failed: %v", message, err)
	}
	return decodeBridgeResponses(t, stdout.Bytes())
}

func sendBridgeNotification(t *testing.T, bridge *stdioBridge, message string, stdout *bytes.Buffer) ([]map[string]any, error) {
	t.Helper()
	stdout.Reset()
	err := bridge.handleIncomingMessage(context.Background(), []byte(message))
	return decodeBridgeResponses(t, stdout.Bytes()), err
}
