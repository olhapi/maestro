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

func setupTestOrchestrator(t *testing.T, command string) (*Orchestrator, *kanban.Store, *config.Manager, string) {
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
  active_states:
    - ready
    - in_progress
    - in_review
  terminal_states:
    - done
    - cancelled
polling:
  interval_ms: 50
workspace:
  root: ` + workspaceRoot + `
hooks:
  timeout_ms: 1000
agent:
  max_concurrent_agents: 2
  max_turns: 2
  max_retry_backoff_ms: 100
  mode: stdio
codex:
  command: ` + command + `
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 500
  turn_timeout_ms: 1000
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0o644); err != nil {
		t.Fatalf("Failed to write workflow: %v", err)
	}

	manager, err := config.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}

	orch := New(store, manager)
	t.Cleanup(func() {
		orch.stopAllRuns()
		waitForNoRunning(t, orch, time.Second)
	})
	t.Cleanup(func() { _ = store.Close() })
	return orch, store, manager, workspaceRoot
}

func waitForNoRunning(t *testing.T, orch *Orchestrator, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		orch.mu.RLock()
		running := len(orch.running)
		orch.mu.RUnlock()
		if running == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for running orchestrator jobs to stop")
}

func TestDispatchCreatesWorkspace(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	issue, _ := store.CreateIssue("", "", "Ready Issue", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	workspace, err := store.GetWorkspace(issue.ID)
	if err != nil {
		t.Fatalf("Expected workspace: %v", err)
	}
	if workspace.RunCount < 1 {
		t.Fatalf("expected run count >= 1, got %d", workspace.RunCount)
	}
	waitForNoRunning(t, orch, time.Second)
}

func TestFailureRetryScheduling(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "false")
	issue, _ := store.CreateIssue("", "", "Fails", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	orch.mu.RLock()
	retry, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected retry entry")
	}
	if retry.Attempt != 1 {
		t.Fatalf("expected retry attempt 1, got %d", retry.Attempt)
	}
	if retry.DelayType != "failure" {
		t.Fatalf("expected failure retry, got %s", retry.DelayType)
	}
	waitForNoRunning(t, orch, time.Second)
}

func TestContinuationRetryAfterSuccess(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	issue, _ := store.CreateIssue("", "", "Succeeds", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	orch.mu.RLock()
	retry, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected continuation retry entry")
	}
	if retry.DelayType != "continuation" {
		t.Fatalf("expected continuation retry, got %s", retry.DelayType)
	}
	waitForNoRunning(t, orch, time.Second)
}

func TestReconcileStopsTerminalRunsAndCleansWorkspace(t *testing.T) {
	sleepScript := filepath.Join(t.TempDir(), "sleep.sh")
	if err := os.WriteFile(sleepScript, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orch, store, _, _ := setupTestOrchestrator(t, sleepScript)
	issue, _ := store.CreateIssue("", "", "Sleep", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		t.Fatal(err)
	}

	orch.reconcile(context.Background())
	time.Sleep(50 * time.Millisecond)

	if _, err := store.GetWorkspace(issue.ID); err == nil {
		t.Fatal("expected workspace to be removed after terminal reconciliation")
	}
}

func TestCleanupTerminalWorkspacesOnStartup(t *testing.T) {
	orch, store, _, workspaceRoot := setupTestOrchestrator(t, "cat")
	issue, _ := store.CreateIssue("", "", "Done", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateDone)
	wsPath := filepath.Join(workspaceRoot, issue.Identifier)
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(issue.ID, wsPath); err != nil {
		t.Fatal(err)
	}

	orch.cleanupTerminalWorkspaces(context.Background())

	if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path removed, got %v", err)
	}
}

func TestDispatchBlockedByInvalidWorkflowReloadKeepsLastGood(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestrator(t, "cat")
	issue, _ := store.CreateIssue("", "", "Ready", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := os.WriteFile(manager.Path(), []byte("---\ntracker:\n  kind: linear\n---\nlegacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.LastError() == nil {
		t.Fatal("expected workflow reload error to be retained")
	}
	waitForNoRunning(t, orch, time.Second)
}

func TestStatusIncludesWorkflowAndRetryFields(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t, "cat")
	status := orch.Status()
	for _, key := range []string{"active_runs", "max_concurrent", "started_at", "uptime_seconds", "poll_interval_ms", "retry_queue", "run_metrics"} {
		if _, ok := status[key]; !ok {
			t.Fatalf("Expected status to have key %s", key)
		}
	}
}
