package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestBinarySmokeRunAndMCP(t *testing.T) {
	bin := buildSmokeBinary(t)

	repoPath := t.TempDir()
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	workspaceRoot := filepath.Join(repoPath, "workspaces")
	workflow := `---
tracker:
  kind: kanban
  active_states: [ready, in_progress, in_review]
  terminal_states: [done, cancelled]
polling:
  interval_ms: 50
workspace:
  root: ` + workspaceRoot + `
hooks:
  timeout_ms: 1000
agent:
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 100
  mode: stdio
codex:
  command: cat
  approval_policy: never
  read_timeout_ms: 500
  turn_timeout_ms: 1000
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(workflow), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	addr := freeAddr(t)
	dbPath := filepath.Join(repoPath, "maestro.db")
	runCmd := exec.Command(bin, "run", "--workflow", workflowPath, "--db", dbPath, "--port", addr, guardrailsAcknowledgementFlag, repoPath)
	if err := runCmd.Start(); err != nil {
		t.Fatalf("start run command: %v", err)
	}
	t.Cleanup(func() {
		if runCmd.Process != nil {
			_ = runCmd.Process.Kill()
		}
	})

	deadline := time.Now().Add(3 * time.Second)
	var runErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			runErr = nil
			break
		}
		runErr = err
		time.Sleep(25 * time.Millisecond)
	}
	if runErr != nil {
		t.Fatalf("run command never served health: %v", runErr)
	}
	_ = runCmd.Process.Signal(os.Interrupt)
	if err := runCmd.Wait(); err != nil {
		t.Fatalf("run command wait: %v", err)
	}

	client, err := mcpclient.NewStdioMCPClient(bin, "mcp", "--db", dbPath)
	if err != nil {
		t.Fatalf("new stdio mcp client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := client.Initialize(ctx, mcp.ClientCapabilities{}, mcp.Implementation{Name: "smoke", Version: "test"}, "2024-11-05")
	if err != nil {
		t.Fatalf("initialize mcp client: %v", err)
	}
	if result.ServerInfo.Name == "" {
		t.Fatalf("expected server info in initialize result: %+v", result)
	}
	tools, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("expected at least one MCP tool")
	}
}
