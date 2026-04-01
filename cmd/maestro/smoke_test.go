package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/httpserver"
	"github.com/olhapi/maestro/internal/kanban"
	maestromcp "github.com/olhapi/maestro/internal/mcp"
	"github.com/olhapi/maestro/internal/orchestrator"
)

func buildSmokeBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "maestro-smoke")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/maestro")
	cmd.Dir = filepath.Clean(filepath.Join(filepath.Dir(mustCallerFile(t)), "..", ".."))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build smoke binary: %v\n%s", err, output)
	}
	return binPath
}

func mustCallerFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller")
	}
	return file
}

func freeAddr(t *testing.T) string {
	t.Helper()
	return syntheticAddr()
}

func TestBinarySmokeRunAndMCP(t *testing.T) {
	buildSmokeBinary(t)

	repoPath := t.TempDir()
	registryDir := t.TempDir()
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	workspaceRoot := filepath.Join(repoPath, "workspaces")
	workflow := `---
tracker:
  kind: kanban
polling:
  interval_ms: 50
workspace:
  root: ` + workspaceRoot + `
  branch_prefix: maestro/
hooks:
  timeout_ms: 1000
orchestrator:
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 100
  max_automatic_retries: 8
  dispatch_mode: parallel
phases:
  review:
    enabled: false
  done:
    enabled: false
runtime:
  default: codex-stdio
  codex-stdio:
    provider: codex
    transport: stdio
    command: cat
    approval_policy: never
    turn_timeout_ms: 1000
    read_timeout_ms: 500
    stall_timeout_ms: 300000
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(workflow), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	t.Setenv("MAESTRO_DAEMON_REGISTRY_DIR", registryDir)
	t.Setenv("MAESTRO_HTTPSERVER_INPROCESS", "1")
	t.Setenv("MAESTRO_MCP_INPROCESS", "1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	dbPath := filepath.Join(repoPath, "maestro.db")
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	orch := orchestrator.NewSharedWithExtensions(store, nil, repoPath, workflowPath)

	addr := freeAddr(t)
	healthServer, err := httpserver.Start(ctx, addr, store, orch)
	if err != nil {
		t.Fatalf("start health server: %v", err)
	}
	if got := healthServer.BaseURL(); got != "" {
		t.Fatalf("expected in-process health server to hide its public URL, got %q", got)
	}

	daemon, err := maestromcp.StartManagedDaemon(ctx, store, orch, nil, version)
	if err != nil {
		t.Fatalf("start managed daemon: %v", err)
	}
	if daemon.Entry.BaseURL == "" {
		t.Fatal("expected daemon base URL")
	}

	deadline := time.Now().Add(10 * time.Second)
	var healthErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			healthErr = nil
			break
		}
		healthErr = err
		time.Sleep(25 * time.Millisecond)
	}
	if healthErr != nil {
		t.Fatalf("health server never became ready: %v", healthErr)
	}

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
		t.Fatalf("start mcp client: %v", err)
	}

	result, err := client.Initialize(callCtx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "smoke", Version: "test"},
			Capabilities:    mcp.ClientCapabilities{},
		},
	})
	if err != nil {
		t.Fatalf("initialize mcp client: %v", err)
	}
	if result.ServerInfo.Name == "" {
		t.Fatalf("expected server info in initialize result: %+v", result)
	}
	tools, err := client.ListTools(callCtx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("expected at least one MCP tool")
	}

	serverInfo, err := client.CallTool(callCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "server_info",
			Arguments: map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("server_info failed: %v", err)
	}
	envelope := decodeSmokeEnvelope(t, serverInfo)
	if runtimeAvailable, _ := envelope["data"].(map[string]any)["runtime_available"].(bool); !runtimeAvailable {
		t.Fatalf("expected runtime_available=true, got %#v", envelope)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("close mcp client: %v", err)
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

func decodeSmokeEnvelope(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected tool content")
	}

	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", result.Content[0])
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(text.Text), &envelope); err != nil {
		t.Fatalf("decode tool envelope: %v", err)
	}
	return envelope
}
