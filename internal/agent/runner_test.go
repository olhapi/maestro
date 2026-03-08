package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/pkg/config"
)

func setupTestRunner(t *testing.T) (*Runner, *kanban.Store, string) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	workspaceRoot := filepath.Join(tmpDir, "workspaces")

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create a test workflow
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	workflowContent := `---
workspace_root: ` + workspaceRoot + `
agent:
  executable: echo
  timeout: 10
---

You are working on {{.Identifier}}: {{.Title}}

{{.Description}}
`
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0644); err != nil {
		t.Fatalf("Failed to write workflow: %v", err)
	}

	workflow, err := config.LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}

	runner := NewRunner(workflow, store)

	t.Cleanup(func() {
		store.Close()
	})

	return runner, store, workspaceRoot
}

func TestGetOrCreateWorkspace(t *testing.T) {
	runner, store, workspaceRoot := setupTestRunner(t)

	issue, _ := store.CreateIssue("", "", "Test Issue", "", 0, nil)

	// First call creates workspace
	workspace, err := runner.getOrCreateWorkspace(issue)
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	expectedPath := filepath.Join(workspaceRoot, issue.Identifier)
	if workspace.Path != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, workspace.Path)
	}

	// Check directory exists
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Error("Expected workspace directory to exist")
	}

	// Second call returns same workspace
	workspace2, err := runner.getOrCreateWorkspace(issue)
	if err != nil {
		t.Fatalf("Failed to get existing workspace: %v", err)
	}

	if workspace.Path != workspace2.Path {
		t.Error("Expected same workspace path on second call")
	}
}

func TestBuildPrompt(t *testing.T) {
	runner, store, _ := setupTestRunner(t)

	issue, _ := store.CreateIssue("", "", "Fix Login Bug", "Users cannot log in", 1, []string{"bug", "urgent"})

	prompt, err := runner.buildPrompt(issue)
	if err != nil {
		t.Fatalf("Failed to build prompt: %v", err)
	}

	// Check template variables are replaced
	if !strings.Contains(prompt, issue.Identifier) {
		t.Error("Expected prompt to contain issue identifier")
	}
	if !strings.Contains(prompt, "Fix Login Bug") {
		t.Error("Expected prompt to contain issue title")
	}
	if !strings.Contains(prompt, "Users cannot log in") {
		t.Error("Expected prompt to contain issue description")
	}
}

func TestRunAgent(t *testing.T) {
	runner, store, _ := setupTestRunner(t)

	issue, _ := store.CreateIssue("", "", "Test", "Description", 0, nil)

	// The test workflow uses 'echo' which should succeed
	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 'echo' should succeed
	if !result.Success {
		t.Errorf("Expected successful run, got error: %v", result.Error)
	}

	// Check workspace was created
	_, err = store.GetWorkspace(issue.ID)
	if err != nil {
		t.Error("Expected workspace to be created")
	}
}

func TestRunAgentWithTimeout(t *testing.T) {
	runner, store, _ := setupTestRunner(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Should fail due to timeout
	result, err := runner.Run(ctx, issue)
	if err != nil {
		// Context cancellation during setup is ok
		return
	}

	if result.Success {
		t.Error("Expected run to fail due to timeout")
	}
}

func TestWorkspaceDeterministic(t *testing.T) {
	runner, store, workspaceRoot := setupTestRunner(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)

	// Create workspace twice
	ws1, _ := runner.getOrCreateWorkspace(issue)
	ws2, _ := runner.getOrCreateWorkspace(issue)

	if ws1.Path != ws2.Path {
		t.Error("Expected deterministic workspace path")
	}

	// Check path uses sanitized identifier
	expected := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))
	if ws1.Path != expected {
		t.Errorf("Expected path %s, got %s", expected, ws1.Path)
	}
}

func TestWorkspacePreservesChanges(t *testing.T) {
	runner, store, _ := setupTestRunner(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)

	// Create workspace
	ws, _ := runner.getOrCreateWorkspace(issue)

	// Write a file
	testFile := filepath.Join(ws.Path, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Get workspace again
	ws2, _ := runner.getOrCreateWorkspace(issue)

	// File should still exist
	if _, err := os.Stat(filepath.Join(ws2.Path, "test.txt")); os.IsNotExist(err) {
		t.Error("Expected workspace to preserve local changes")
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
	runner, store, workspaceRoot := setupTestRunner(t)
	issue, _ := store.CreateIssue("", "", "Stale", "", 0, nil)

	path := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := runner.getOrCreateWorkspace(issue)
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
	dbPath := filepath.Join(tmpDir, "test.db")
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	traceFile := filepath.Join(tmpDir, "trace.log")
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	workflowContent := `---
workspace_root: ` + workspaceRoot + `
agent:
  executable: ` + filepath.Join(tmpDir, "fake-codex.sh") + `
  args: []
  mode: app_server
  timeout: 10
---
Prompt {{.Identifier}}
`
	script := `#!/bin/sh
trace_file="${TRACE_FILE}"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"th1"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"tu1"}}}'
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"th1","turnId":"tu1","usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`
	if err := os.WriteFile(filepath.Join(tmpDir, "fake-codex.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_FILE", traceFile)
	wf, err := config.LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(wf, store)
	issue, _ := store.CreateIssue("", "", "AppServer", "", 0, nil)
	res, err := r.Run(context.Background(), issue)
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
