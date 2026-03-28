package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("MAESTRO_MAIN_HELPER") != "1" {
		return
	}
	if recordPath := os.Getenv("MAESTRO_RECORD_BROWSER_OPEN"); recordPath != "" {
		dashboardInteractiveCheck = func() bool { return true }
		dashboardOpenPollInterval = 10 * time.Millisecond
		dashboardOpenTimeout = time.Second
		dashboardBrowserLauncher = func(ctx context.Context, rawURL string) error {
			return os.WriteFile(recordPath, []byte(rawURL), 0o644)
		}
	}
	raw := os.Getenv("MAESTRO_MAIN_ARGS")
	var args []string
	if raw != "" {
		args = strings.Split(raw, "\n")
	}
	os.Args = append([]string{"maestro"}, args...)
	main()
	os.Exit(0)
}

func runMainHelper(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
	daemonRegistryDir := filepath.Join(t.TempDir(), "daemon-registry")
	cmd.Env = append(os.Environ(),
		"MAESTRO_MAIN_HELPER=1",
		"MAESTRO_DAEMON_REGISTRY_DIR="+daemonRegistryDir,
		"MAESTRO_HTTPSERVER_INPROCESS=1",
		"MAESTRO_MCP_INPROCESS=1",
		"MAESTRO_MAIN_ARGS="+strings.Join(args, "\n"),
	)
	var stdout lockedBuffer
	var stderr lockedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func freeAddrForHelper(t *testing.T) string {
	t.Helper()
	return syntheticAddr()
}

var helperPortCounter atomic.Uint32

func syntheticAddr() string {
	return "127.0.0.1:" + strconv.Itoa(30000+int(helperPortCounter.Add(1)))
}

func TestMainEntryUsageVersionAndErrors(t *testing.T) {
	stdout, stderr, err := runMainHelper(t)
	if err == nil {
		t.Fatal("expected usage exit")
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Fatalf("expected usage output, got stdout=%q stderr=%q", stdout, stderr)
	}

	stdout, stderr, err = runMainHelper(t, "--log-level", "verbose", "version")
	if err == nil {
		t.Fatal("expected invalid log level exit")
	}
	if !strings.Contains(stderr, "invalid --log-level") {
		t.Fatalf("expected invalid log level error, got stdout=%q stderr=%q", stdout, stderr)
	}

	stdout, stderr, err = runMainHelper(t, "unknown-command")
	if err == nil {
		t.Fatal("expected unknown command exit")
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Fatalf("expected unknown command error, got stdout=%q stderr=%q", stdout, stderr)
	}

	stdout, stderr, err = runMainHelper(t, "version")
	if err != nil {
		t.Fatalf("version failed: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if strings.TrimSpace(stdout) == "" || !strings.Contains(stdout, "maestro ") {
		t.Fatalf("unexpected version output: %q", stdout)
	}
}

func TestMainEntryRunCommand(t *testing.T) {
	repoPath := t.TempDir()
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	dbPath := filepath.Join(repoPath, "maestro.db")
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

	addr := freeAddrForHelper(t)
	daemonRegistryDir := filepath.Join(t.TempDir(), "daemon-registry")
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
	cmd.Env = append(os.Environ(),
		"MAESTRO_MAIN_HELPER=1",
		"MAESTRO_DAEMON_REGISTRY_DIR="+daemonRegistryDir,
		"MAESTRO_HTTPSERVER_INPROCESS=1",
		"MAESTRO_MCP_INPROCESS=1",
		"MAESTRO_MAIN_ARGS="+strings.Join([]string{
			"run", "--workflow", workflowPath, "--db", dbPath, "--port", addr, "--" + strings.TrimPrefix(guardrailsAcknowledgementFlag, "--"), repoPath,
		}, "\n"),
	)
	var stdout lockedBuffer
	var stderr lockedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start run helper: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	for {
		if ctx.Err() != nil {
			t.Fatalf("run helper never started daemon registry: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		entries, err := os.ReadDir(daemonRegistryDir)
		if err == nil {
			for _, entry := range entries {
				if filepath.Ext(entry.Name()) == ".json" {
					goto registryReady
				}
			}
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read daemon registry dir: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}

registryReady:
	if strings.Contains(stdout.String(), "Dashboard: ") {
		t.Fatalf("did not expect Dashboard URL in in-process mode, got stdout=%q", stdout.String())
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt run helper: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait run helper: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}
