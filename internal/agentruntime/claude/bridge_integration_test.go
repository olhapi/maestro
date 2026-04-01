package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	transport "github.com/mark3labs/mcp-go/client/transport"
	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/kanban"
	maestromcp "github.com/olhapi/maestro/internal/mcp"
)

func TestClaudeMCPBridgeSeesMaestroToolSurface(t *testing.T) {
	t.Setenv("MAESTRO_DAEMON_REGISTRY_DIR", t.TempDir())

	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	handle, err := maestromcp.StartManagedDaemon(ctx, store, nil, nil, "test-version")
	if err != nil {
		t.Fatalf("StartManagedDaemon: %v", err)
	}
	t.Cleanup(func() {
		_ = handle.Close()
	})

	clientStdinR, clientStdinW := io.Pipe()
	clientStdoutR, clientStdoutW := io.Pipe()
	bridgeErrCh := make(chan error, 1)
	go func() {
		err := maestromcp.ServeBridgeStdioPath(ctx, dbPath, clientStdinR, clientStdoutW, io.Discard)
		_ = clientStdoutW.Close()
		bridgeErrCh <- err
	}()

	bridgeTransport := transport.NewIO(clientStdoutR, clientStdinW, io.NopCloser(bytes.NewReader(nil)))
	client := mcpclient.NewClient(bridgeTransport)
	callCtx, cancelCall := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCall()
	if err := client.Start(callCtx); err != nil {
		t.Fatalf("Start MCP client: %v", err)
	}

	if _, err := client.Initialize(callCtx, mcpapi.InitializeRequest{
		Params: mcpapi.InitializeParams{
			ProtocolVersion: mcpapi.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpapi.Implementation{Name: "claude-bridge-test", Version: "test"},
			Capabilities:    mcpapi.ClientCapabilities{},
		},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := client.ListTools(callCtx, mcpapi.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, want := range []string{"server_info", "create_issue", "list_issues", "get_runtime_snapshot"} {
		if !hasToolName(tools.Tools, want) {
			t.Fatalf("expected MCP tool %q in surface, got %#v", want, toolNames(tools.Tools))
		}
	}

	serverInfo, err := client.CallTool(callCtx, mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{
			Name:      "server_info",
			Arguments: map[string]interface{}{},
		},
	})
	if err != nil {
		t.Fatalf("CallTool(server_info): %v", err)
	}
	envelope := decodeBridgeEnvelope(t, serverInfo)
	data, ok := envelope["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected envelope data map, got %#v", envelope)
	}
	if projectCount, ok := data["project_count"].(float64); !ok || projectCount != 0 {
		t.Fatalf("expected empty project count, got %#v", data["project_count"])
	}
	if issueCount, ok := data["issue_count"].(float64); !ok || issueCount != 0 {
		t.Fatalf("expected empty issue count, got %#v", data["issue_count"])
	}
	if runtimeAvailable, ok := data["runtime_available"].(bool); !ok || runtimeAvailable {
		t.Fatalf("expected runtime_available=false without a provider, got %#v", data["runtime_available"])
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close MCP client: %v", err)
	}
	select {
	case err := <-bridgeErrCh:
		if err != nil {
			t.Fatalf("bridge exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not exit after client close")
	}
}

func decodeBridgeEnvelope(t *testing.T, result *mcpapi.CallToolResult) map[string]interface{} {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected tool content")
	}
	text, ok := result.Content[0].(mcpapi.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", result.Content[0])
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(text.Text), &envelope); err != nil {
		t.Fatalf("decode tool envelope: %v", err)
	}
	return envelope
}

func hasToolName(tools []mcpapi.Tool, want string) bool {
	for _, tool := range tools {
		if tool.Name == want {
			return true
		}
	}
	return false
}

func toolNames(tools []mcpapi.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
