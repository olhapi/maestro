package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/pkg/config"
)

func setupTestRunner(t *testing.T, command string, mode string) (*Runner, *kanban.Store, *config.Manager, string, string) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	workspaceRoot := filepath.Join(tmpDir, "workspaces")

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	workflowContent := `---
tracker:
  kind: kanban
polling:
  interval_ms: 1000
workspace:
  root: ` + workspaceRoot + `
hooks:
  timeout_ms: 1000
agent:
  max_concurrent_agents: 2
  max_turns: 3
  max_retry_backoff_ms: 10000
  mode: ` + mode + `
codex:
  command: ` + command + `
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 1000
  turn_timeout_ms: 10000
---
Issue {{ issue.identifier }} {{ issue.title }}{% if attempt %} retry {{ attempt }}{% endif %}
`
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0o644); err != nil {
		t.Fatalf("Failed to write workflow: %v", err)
	}

	manager, err := config.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}
	runner := NewRunner(manager, store)

	t.Cleanup(func() {
		_ = store.Close()
	})

	return runner, store, manager, workspaceRoot, tmpDir
}

func TestGetOrCreateWorkspace(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Test Issue", "", 0, nil)

	workflow, err := runner.workflowProvider.Current()
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := runner.getOrCreateWorkspace(workflow, issue)
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	expectedPath := filepath.Join(workspaceRoot, issue.Identifier)
	if workspace.Path != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, workspace.Path)
	}
}

func TestBuildTurnPrompt(t *testing.T) {
	runner, store, manager, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Fix Login Bug", "Users cannot log in", 1, []string{"bug", "urgent"})
	workflow, _ := manager.Current()

	prompt, err := runner.buildTurnPrompt(workflow, issue, 2, 1)
	if err != nil {
		t.Fatalf("Failed to build prompt: %v", err)
	}
	for _, part := range []string{issue.Identifier, "Fix Login Bug", "retry 2"} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("expected prompt to contain %q, got %q", part, prompt)
		}
	}
}

func TestContinuationPrompt(t *testing.T) {
	runner, store, manager, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Continue", "", 0, nil)
	workflow, _ := manager.Current()

	prompt, err := runner.buildTurnPrompt(workflow, issue, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Continuation guidance") {
		t.Fatalf("expected continuation prompt, got %q", prompt)
	}
}

func TestRunAgentStdio(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Test", "Description", 0, nil)

	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected successful run, got %+v", result)
	}
	if !strings.Contains(result.Output, issue.Identifier) {
		t.Fatalf("expected output to contain rendered prompt, got %q", result.Output)
	}
}

func TestRunAttemptIncludesAttempt(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Retry", "Body", 0, nil)

	result, err := runner.RunAttempt(context.Background(), issue, 3)
	if err != nil {
		t.Fatalf("RunAttempt failed: %v", err)
	}
	if !strings.Contains(result.Output, "retry 3") {
		t.Fatalf("expected retry attempt in output, got %q", result.Output)
	}
}

func TestWorkspaceDeterministic(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	workflow, _ := runner.workflowProvider.Current()

	ws1, _ := runner.getOrCreateWorkspace(workflow, issue)
	ws2, _ := runner.getOrCreateWorkspace(workflow, issue)
	if ws1.Path != ws2.Path {
		t.Error("Expected deterministic workspace path")
	}
	expected := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))
	if ws1.Path != expected {
		t.Errorf("Expected path %s, got %s", expected, ws1.Path)
	}
}

func TestSanitizeWorkspaceKey(t *testing.T) {
	if got := sanitizeWorkspaceKey("MT/Det"); got != "MT_Det" {
		t.Fatalf("expected MT_Det, got %s", got)
	}
	if got := sanitizeWorkspaceKey("../escape"); got == "" || strings.Contains(got, "..") || strings.Contains(got, "/") {
		t.Fatalf("unexpected sanitized key: %s", got)
	}
}

func TestWorkspaceReplacesStaleFilePath(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Stale", "", 0, nil)
	path := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))

	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, _ := runner.workflowProvider.Current()
	ws, err := runner.getOrCreateWorkspace(workflow, issue)
	if err != nil {
		t.Fatalf("expected workspace recovery, got err: %v", err)
	}
	fi, err := os.Stat(ws.Path)
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected workspace dir at %s", ws.Path)
	}
}

func TestRunAgentAppServerModeTracksSession(t *testing.T) {
	tmpDir := t.TempDir()
	traceFile := filepath.Join(tmpDir, "trace.log")
	scriptPath := filepath.Join(tmpDir, "fake-codex.sh")
	script := `#!/bin/sh
trace_file="$TRACE_FILE"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"th1"}}}' ;;
    4) printf '%s\n' '{"id":3,"result":{"turn":{"id":"tu1"}}}'; printf '%s\n' '{"method":"turn/completed","params":{"threadId":"th1","turnId":"tu1","usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}}'; exit 0 ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_FILE", traceFile)

	runner, store, _, _, _ := setupTestRunner(t, "sh "+scriptPath, config.AgentModeAppServer)
	issue, _ := store.CreateIssue("", "", "AppServer", "", 0, nil)

	res, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Success || res.AppSession == nil {
		t.Fatalf("expected app session, got %+v", res)
	}
	if res.AppSession.SessionID != "th1-tu1" {
		t.Fatalf("unexpected session id: %s", res.AppSession.SessionID)
	}
}

func TestCleanupWorkspaceRunsBeforeRemoveHook(t *testing.T) {
	traceFileDir := t.TempDir()
	traceFile := filepath.Join(traceFileDir, "cleanup.log")
	command := "cat"
	runner, store, manager, workspaceRoot, repoDir := setupTestRunner(t, command, config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Cleanup", "", 0, nil)

	workflowText := `---
tracker:
  kind: kanban
workspace:
  root: ` + workspaceRoot + `
hooks:
  before_remove: echo cleaned >> ` + traceFile + `
  timeout_ms: 1000
agent:
  mode: stdio
codex:
  command: cat
---
{{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(repoDir, "WORKFLOW.md"), []byte(workflowText), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}

	if _, err := runner.Run(context.Background(), issue); err != nil {
		t.Fatal(err)
	}
	if err := runner.CleanupWorkspace(context.Background(), issue); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(traceFile); err != nil {
		t.Fatalf("expected before_remove hook output, got %v", err)
	}
}
