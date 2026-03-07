package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/pkg/config"
)

func setupTestOrchestrator(t *testing.T) (*Orchestrator, *kanban.Store, string) {
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
poll_interval: 1
max_concurrent: 2
workspace_root: ` + workspaceRoot + `
active_states:
  - ready
  - in_progress
  - in_review
terminal_states:
  - done
  - cancelled
agent:
  executable: echo
  timeout: 5
---

Test prompt for {{.Identifier}}
`
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0644); err != nil {
		t.Fatalf("Failed to write workflow: %v", err)
	}

	workflow, err := config.LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}

	orch := New(store, workflow)

	t.Cleanup(func() {
		store.Close()
	})

	return orch, store, workspaceRoot
}

func TestNewOrchestrator(t *testing.T) {
	orch, _, _ := setupTestOrchestrator(t)

	if orch == nil {
		t.Fatal("Expected non-nil orchestrator")
	}

	if orch.maxRetries != 3 {
		t.Errorf("Expected maxRetries 3, got %d", orch.maxRetries)
	}
}

func TestIsBlocked(t *testing.T) {
	orch, store, _ := setupTestOrchestrator(t)

	// Create two issues
	issue1, err := store.CreateIssue("", "", "Issue 1", "", 0, nil)
	if err != nil {
		t.Fatalf("Failed to create issue1: %v", err)
	}
	issue2, err := store.CreateIssue("", "", "Issue 2", "", 0, nil)
	if err != nil {
		t.Fatalf("Failed to create issue2: %v", err)
	}

	// Issue 1 is not blocked
	if orch.isBlocked(*issue1) {
		t.Error("Expected issue1 to not be blocked")
	}

	// Block issue1 by issue2
	store.UpdateIssue(issue1.ID, map[string]interface{}{
		"blocked_by": []string{issue2.Identifier},
	})

	issue1, _ = store.GetIssue(issue1.ID)

	// Issue 1 is now blocked (issue2 is not done)
	if !orch.isBlocked(*issue1) {
		t.Error("Expected issue1 to be blocked")
	}

	// Complete issue2
	store.UpdateIssueState(issue2.ID, kanban.StateDone)

	// Issue 1 is no longer blocked (blocker is done)
	if orch.isBlocked(*issue1) {
		t.Error("Expected issue1 to not be blocked when blocker is done")
	}
}

func TestIsActiveState(t *testing.T) {
	orch, _, _ := setupTestOrchestrator(t)

	tests := []struct {
		state    string
		expected bool
	}{
		{"ready", true},
		{"in_progress", true},
		{"in_review", true},
		{"backlog", false},
		{"done", false},
		{"cancelled", false},
	}

	for _, tt := range tests {
		if got := orch.isActiveState(tt.state); got != tt.expected {
			t.Errorf("isActiveState(%q) = %v, expected %v", tt.state, got, tt.expected)
		}
	}
}

func TestIsTerminalState(t *testing.T) {
	orch, _, _ := setupTestOrchestrator(t)

	tests := []struct {
		state    string
		expected bool
	}{
		{"done", true},
		{"cancelled", true},
		{"backlog", false},
		{"ready", false},
		{"in_progress", false},
	}

	for _, tt := range tests {
		if got := orch.isTerminalState(tt.state); got != tt.expected {
			t.Errorf("isTerminalState(%q) = %v, expected %v", tt.state, got, tt.expected)
		}
	}
}

func TestDispatch(t *testing.T) {
	orch, store, _ := setupTestOrchestrator(t)

	// Create issues in different states
	issue1, _ := store.CreateIssue("", "", "Ready Issue", "", 0, nil)
	store.UpdateIssueState(issue1.ID, kanban.StateReady)

	issue2, _ := store.CreateIssue("", "", "Backlog Issue", "", 0, nil)
	// Stays in backlog

	// Dispatch should pick up the ready issue
	err := orch.dispatch(context.Background())
	if err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}

	// Give it a moment to start (run completes very fast with 'echo')
	time.Sleep(200 * time.Millisecond)

	// The issue may have already completed, so we check the workspace was created
	// rather than checking if it's still running
	workspace, err := store.GetWorkspace(issue1.ID)
	if err != nil {
		t.Error("Expected workspace to be created for ready issue")
	} else if workspace.RunCount < 1 {
		t.Errorf("Expected run count >= 1, got %d", workspace.RunCount)
	}

	// Backlog issue should not have a workspace
	_, err = store.GetWorkspace(issue2.ID)
	if err == nil {
		t.Error("Expected no workspace for backlog issue")
	}
}

func TestMaxConcurrent(t *testing.T) {
	orch, store, _ := setupTestOrchestrator(t)

	// Create 5 ready issues
	for i := 0; i < 5; i++ {
		issue, _ := store.CreateIssue("", "", "Ready Issue", "", 0, nil)
		store.UpdateIssueState(issue.ID, kanban.StateReady)
	}

	// Dispatch
	orch.dispatch(context.Background())

	// Give it a moment
	time.Sleep(100 * time.Millisecond)

	// Should have at most max_concurrent (2) running
	orch.mu.RLock()
	activeCount := len(orch.activeRuns)
	orch.mu.RUnlock()

	if activeCount > 2 {
		t.Errorf("Expected at most 2 concurrent runs, got %d", activeCount)
	}
}

func TestStopRun(t *testing.T) {
	orch, store, _ := setupTestOrchestrator(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	store.UpdateIssueState(issue.ID, kanban.StateReady)

	// Start a run
	ctx := context.Background()
	orch.startRun(ctx, issue)

	// Give it a moment
	time.Sleep(50 * time.Millisecond)

	// Stop it
	orch.mu.Lock()
	orch.stopRun(issue.ID)
	orch.mu.Unlock()

	// Should be removed from active runs
	orch.mu.RLock()
	_, running := orch.activeRuns[issue.ID]
	orch.mu.RUnlock()

	if running {
		t.Error("Expected run to be stopped")
	}
}

func TestStatus(t *testing.T) {
	orch, _, _ := setupTestOrchestrator(t)

	status := orch.Status()

	if _, ok := status["active_runs"]; !ok {
		t.Error("Expected status to have active_runs")
	}
	if _, ok := status["max_concurrent"]; !ok {
		t.Error("Expected status to have max_concurrent")
	}
	if status["max_concurrent"] != 2 {
		t.Errorf("Expected max_concurrent 2, got %v", status["max_concurrent"])
	}
}

func TestRetryQueue(t *testing.T) {
	orch, store, _ := setupTestOrchestrator(t)

	// Create an issue and add it to the retry queue
	issue, _ := store.CreateIssue("", "", "Failed Issue", "", 0, nil)

	// Simulate a failed run by adding to retry queue
	orch.mu.Lock()
	orch.retryQueue[issue.ID] = 1
	orch.mu.Unlock()

	// Process retries - should move issue back to ready state
	orch.processRetries(context.Background())

	// Check issue is now in ready state
	updated, _ := store.GetIssue(issue.ID)
	if updated.State != kanban.StateReady {
		t.Errorf("Expected state 'ready', got %s", updated.State)
	}

	// Retry count should still be 1 (not incremented by processRetries)
	orch.mu.RLock()
	count := orch.retryQueue[issue.ID]
	orch.mu.RUnlock()

	if count != 1 {
		t.Errorf("Expected retry count 1, got %d", count)
	}
}

func TestMaxRetriesExceeded(t *testing.T) {
	orch, store, _ := setupTestOrchestrator(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)

	// Set max retries exceeded
	orch.retryQueue[issue.ID] = 3

	// Process retries
	orch.processRetries(context.Background())

	// Should be moved to backlog
	updated, _ := store.GetIssue(issue.ID)
	if updated.State != kanban.StateBacklog {
		t.Errorf("Expected state 'backlog', got %s", updated.State)
	}

	// Should be removed from retry queue
	if _, exists := orch.retryQueue[issue.ID]; exists {
		t.Error("Expected issue to be removed from retry queue")
	}
}
