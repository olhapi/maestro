package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agent"
	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/pkg/config"
)

func setupTestOrchestrator(t *testing.T, command string) (*Orchestrator, *kanban.Store, *config.Manager, string) {
	return setupTestOrchestratorWithConcurrency(t, command, 2)
}

func setupTestOrchestratorWithConcurrency(t *testing.T, command string, maxConcurrent int) (*Orchestrator, *kanban.Store, *config.Manager, string) {
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
  max_concurrent_agents: ` + fmt.Sprintf("%d", maxConcurrent) + `
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

func enablePhaseWorkflow(t *testing.T, manager *config.Manager, workspaceRoot string) {
	t.Helper()
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
  max_concurrent_agents: 1
  max_turns: 2
  max_retry_backoff_ms: 100
  mode: stdio
codex:
  command: cat
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 500
  turn_timeout_ms: 1000
phases:
  review:
    enabled: true
    prompt: |
      Review {{ issue.identifier }} in {{ phase }}
  done:
    enabled: true
    prompt: |
      Finalize {{ issue.identifier }} in {{ phase }}
---
Implement {{ issue.identifier }} in {{ phase }}
`
	if err := os.WriteFile(manager.Path(), []byte(workflowContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}
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

func waitForRunningCount(t *testing.T, orch *Orchestrator, expected int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		orch.mu.RLock()
		running := len(orch.running)
		orch.mu.RUnlock()
		if running == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for running count %d", expected)
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

func TestImplementationSuccessTransitionsToReviewPhase(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{store: store}

	issue, _ := store.CreateIssue("", "", "Needs review", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != kanban.StateInReview || updated.WorkflowPhase != kanban.WorkflowPhaseReview {
		t.Fatalf("expected in_review/review, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	retry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if retry.Phase != string(kanban.WorkflowPhaseReview) {
		t.Fatalf("expected review retry, got %+v", retry)
	}
}

func TestImplementationSuccessCanSkipReviewAndQueueDonePhase(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseImplementation: func(issue *kanban.Issue) (*agent.RunResult, error) {
				if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
					return nil, err
				}
				return &agent.RunResult{Success: true}, nil
			},
		},
	}

	issue, _ := store.CreateIssue("", "", "Skip review", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != kanban.StateDone || updated.WorkflowPhase != kanban.WorkflowPhaseDone {
		t.Fatalf("expected done/done, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	retry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if retry.Phase != string(kanban.WorkflowPhaseDone) {
		t.Fatalf("expected done retry, got %+v", retry)
	}
}

func TestReviewFailureMovesIssueBackToImplementation(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseReview: func(issue *kanban.Issue) (*agent.RunResult, error) {
				return nil, fmt.Errorf("review failed")
			},
		},
	}

	issue, _ := store.CreateIssue("", "", "Review failure", "", 0, nil)
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateInReview, kanban.WorkflowPhaseReview); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != kanban.StateInProgress || updated.WorkflowPhase != kanban.WorkflowPhaseImplementation {
		t.Fatalf("expected in_progress/implementation, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	retry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if retry.Phase != string(kanban.WorkflowPhaseImplementation) || retry.DelayType != "failure" {
		t.Fatalf("expected implementation failure retry, got %+v", retry)
	}
}

func TestReviewSuccessTransitionsToDonePhase(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{store: store}

	issue, _ := store.CreateIssue("", "", "Review success", "", 0, nil)
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateInReview, kanban.WorkflowPhaseReview); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != kanban.StateDone || updated.WorkflowPhase != kanban.WorkflowPhaseDone {
		t.Fatalf("expected done/done, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	retry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if retry.Phase != string(kanban.WorkflowPhaseDone) {
		t.Fatalf("expected done retry, got %+v", retry)
	}
}

func TestDoneFailureRetriesInDonePhase(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseDone: func(issue *kanban.Issue) (*agent.RunResult, error) {
				return nil, fmt.Errorf("finalization failed")
			},
		},
	}

	issue, _ := store.CreateIssue("", "", "Done failure", "", 0, nil)
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateDone, kanban.WorkflowPhaseDone); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != kanban.StateDone || updated.WorkflowPhase != kanban.WorkflowPhaseDone {
		t.Fatalf("expected done/done, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	retry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if retry.Phase != string(kanban.WorkflowPhaseDone) || retry.DelayType != "failure" {
		t.Fatalf("expected done failure retry, got %+v", retry)
	}
}

func TestReconcileDoesNotKillImplementationRunThatMovedIssueToDone(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	runner := newTerminalTransitionRunner(store)
	orch.runner = runner

	issue, _ := store.CreateIssue("", "", "Skip review race", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.waitForMovedToDone(t, time.Second)

	orch.reconcile(context.Background())

	orch.mu.RLock()
	_, stillRunning := orch.running[issue.ID]
	orch.mu.RUnlock()
	if !stillRunning {
		t.Fatal("expected run to remain active after reconcile")
	}

	runner.complete()
	waitForNoRunning(t, orch, time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != kanban.StateDone || updated.WorkflowPhase != kanban.WorkflowPhaseDone {
		t.Fatalf("expected done/done after implementation completion, got %s/%s", updated.State, updated.WorkflowPhase)
	}
}

func TestDoneSuccessMarksIssueCompleteAndAllowsCleanup(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{store: store}

	issue, _ := store.CreateIssue("", "", "Done success", "", 0, nil)
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateDone, kanban.WorkflowPhaseDone); err != nil {
		t.Fatal(err)
	}
	wsPath := filepath.Join(workspaceRoot, issue.Identifier)
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(issue.ID, wsPath); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WorkflowPhase != kanban.WorkflowPhaseComplete {
		t.Fatalf("expected complete phase, got %s", updated.WorkflowPhase)
	}
	orch.cleanupTerminalWorkspaces(context.Background())
	if _, err := store.GetWorkspace(issue.ID); err == nil {
		t.Fatal("expected workspace cleanup after done phase completion")
	}
}

func TestLiveSessionsTracksOnlyActiveRuns(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fake-codex.sh")
	script := `#!/bin/sh
count=0
while IFS= read -r line; do
  count=$((count + 1))
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-live"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-live"}}}'
      sleep 1
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-live","turnId":"turn-live"}}'
      exit 0
      ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
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
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 100
  mode: app_server
codex:
  command: sh ` + scriptPath + `
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 500
  turn_timeout_ms: 3000
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(manager.Path(), []byte(workflowContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}

	issue, _ := store.CreateIssue("", "", "Live Session", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sessions := orch.LiveSessions()["sessions"].(map[string]interface{})
		if session, ok := sessions[issue.Identifier].(appserver.Session); ok {
			if session.SessionID == "thread-live-turn-live" && session.TurnsStarted == 1 && session.IssueID == issue.ID && session.IssueIdentifier == issue.Identifier {
				goto completed
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected live session while run is active")

completed:
	waitForNoRunning(t, orch, 3*time.Second)
	sessions := orch.LiveSessions()["sessions"].(map[string]interface{})
	if len(sessions) != 0 {
		t.Fatalf("expected no live sessions after run completion, got %#v", sessions)
	}
}

func TestInterruptedRunPersistsLatestExecutionSessionSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "approval-required.sh")
	script := `#!/bin/sh
count=0
while IFS= read -r line; do
  count=$((count + 1))
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-approval"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-approval"}}}'
      printf '%s\n' '{"id":99,"method":"item/commandExecution/requestApproval","params":{"command":"gh pr view"}}'
      ;;
    *) sleep 1 ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
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
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 100
  mode: app_server
codex:
  command: sh ` + scriptPath + `
  approval_policy: on_request
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 500
  turn_timeout_ms: 3000
  stall_timeout_ms: 3000
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(manager.Path(), []byte(workflowContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}

	issue, _ := store.CreateIssue("", "", "Approval snapshot", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, 3*time.Second)

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession failed: %v", err)
	}
	if snapshot.RunKind != "run_unsuccessful" || snapshot.Error != "approval_required" || snapshot.Attempt != 0 {
		t.Fatalf("unexpected execution snapshot: %+v", snapshot)
	}
	if snapshot.AppSession.SessionID != "thread-approval-turn-approval" {
		t.Fatalf("unexpected persisted session id: %+v", snapshot.AppSession)
	}
	if len(snapshot.AppSession.History) == 0 {
		t.Fatalf("expected persisted session history, got %+v", snapshot.AppSession)
	}
	sessions := orch.LiveSessions()["sessions"].(map[string]interface{})
	if len(sessions) != 0 {
		t.Fatalf("expected no live sessions after interrupted run, got %#v", sessions)
	}
}

func TestStalledRunPersistsLatestExecutionSessionSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "stall.sh")
	script := `#!/bin/sh
count=0
while IFS= read -r _line; do
  count=$((count + 1))
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-stall"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-stall"}}}'
      sleep 4
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-stall","turnId":"turn-stall"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
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
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 100
  mode: app_server
codex:
  command: sh ` + scriptPath + `
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 500
  turn_timeout_ms: 6000
  stall_timeout_ms: 2500
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(manager.Path(), []byte(workflowContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}

	issue, _ := store.CreateIssue("", "", "Stall snapshot", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, 5*time.Second)

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession failed: %v", err)
	}
	if snapshot.Error != "stall_timeout" || snapshot.RunKind != "run_unsuccessful" || snapshot.Attempt != 0 {
		t.Fatalf("unexpected stall snapshot: %+v", snapshot)
	}
	if snapshot.AppSession.SessionID != "thread-stall-turn-stall" {
		t.Fatalf("unexpected persisted stall session: %+v", snapshot.AppSession)
	}
}

func TestCompletedRunPersistsLatestExecutionSessionSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "complete.sh")
	script := `#!/bin/sh
count=0
while IFS= read -r line; do
  count=$((count + 1))
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-complete"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-complete"}}}'
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-complete","turnId":"turn-complete"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
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
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 100
  mode: app_server
codex:
  command: sh ` + scriptPath + `
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 500
  turn_timeout_ms: 3000
  stall_timeout_ms: 3000
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(manager.Path(), []byte(workflowContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}

	issue, _ := store.CreateIssue("", "", "Completion snapshot", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, 3*time.Second)

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession failed: %v", err)
	}
	if snapshot.RunKind != "run_completed" || snapshot.Error != "" {
		t.Fatalf("unexpected completion snapshot: %+v", snapshot)
	}
	if snapshot.AppSession.SessionID != "thread-complete-turn-complete" || snapshot.AppSession.TerminalReason != "turn.completed" {
		t.Fatalf("unexpected persisted completed session: %+v", snapshot.AppSession)
	}
}

func TestReconcileStopsCancelledRunsAndCleansWorkspace(t *testing.T) {
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
	if err := store.UpdateIssueState(issue.ID, kanban.StateCancelled); err != nil {
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

func TestStatusLiveSessionsUseIssueIdentifiers(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	issue, _ := store.CreateIssue("", "", "Tracked session", "", 0, nil)
	orch.mu.Lock()
	orch.running[issue.ID] = runningEntry{
		cancel:    func() {},
		issue:     *issue,
		attempt:   1,
		startedAt: time.Now().UTC(),
	}
	orch.liveSessions[issue.ID] = &appserver.Session{
		SessionID:       "thread-turn",
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
	}
	orch.mu.Unlock()

	status := orch.Status()
	live, ok := status["live_sessions"].(map[string]*appserver.Session)
	if !ok {
		t.Fatalf("unexpected live_sessions payload: %#v", status["live_sessions"])
	}
	session := live[issue.Identifier]
	if session == nil {
		t.Fatalf("expected live session keyed by identifier, got %#v", live)
	}
	if session.IssueID != issue.ID || session.IssueIdentifier != issue.Identifier {
		t.Fatalf("unexpected session metadata: %+v", session)
	}
}

func TestSharedDispatchUsesScopedRuntimeForProjectlessIssue(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	repoPath := filepath.Join(tmpDir, "repo")
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("MkdirAll repo: %v", err)
	}

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
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
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 100
  mode: stdio
codex:
  command: cat
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
		t.Fatalf("WriteFile workflow: %v", err)
	}

	orch := NewSharedWithExtensions(store, nil, repoPath, workflowPath)
	runner := newControlledRunner(store)
	orch.runnerFactory = func(*config.Manager) runnerExecutor { return runner }
	t.Cleanup(func() {
		orch.stopAllRuns()
		waitForNoRunning(t, orch, time.Second)
	})

	issue, err := store.CreateIssue("", "", "Scoped issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState failed: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}

	starts := runner.waitForStarts(t, 1, time.Second)
	if starts[0] != issue.Identifier {
		t.Fatalf("expected start for %s, got %v", issue.Identifier, starts)
	}

	runner.complete(issue.Identifier)
	waitForNoRunning(t, orch, time.Second)
}

type runnerEvent struct {
	kind       string
	identifier string
}

type phaseRunHandler func(issue *kanban.Issue) (*agent.RunResult, error)

type phaseScriptRunner struct {
	store    *kanban.Store
	handlers map[kanban.WorkflowPhase]phaseRunHandler
}

func (r *phaseScriptRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	if issue.State == kanban.StateReady {
		if err := r.store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
			return nil, err
		}
	}
	if handler := r.handlers[issue.WorkflowPhase]; handler != nil {
		return handler(issue)
	}
	return &agent.RunResult{Success: true}, nil
}

func (r *phaseScriptRunner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	workspace, err := r.store.GetWorkspace(issue.ID)
	if err == nil && workspace != nil {
		_ = os.RemoveAll(workspace.Path)
	}
	return r.store.DeleteWorkspace(issue.ID)
}

type terminalTransitionRunner struct {
	store       *kanban.Store
	movedToDone chan struct{}
	release     chan struct{}
	once        sync.Once
}

func newTerminalTransitionRunner(store *kanban.Store) *terminalTransitionRunner {
	return &terminalTransitionRunner{
		store:       store,
		movedToDone: make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (r *terminalTransitionRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	if issue.State == kanban.StateReady {
		if err := r.store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
			return nil, err
		}
	}
	if err := r.store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		return nil, err
	}
	r.once.Do(func() { close(r.movedToDone) })

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.release:
	}
	return &agent.RunResult{Success: true}, nil
}

func (r *terminalTransitionRunner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	return nil
}

func (r *terminalTransitionRunner) waitForMovedToDone(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-r.movedToDone:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for issue to move to done")
	}
}

func (r *terminalTransitionRunner) complete() {
	close(r.release)
}

type controlledRunner struct {
	store   *kanban.Store
	mu      sync.Mutex
	starts  []string
	events  []runnerEvent
	waiters map[string]chan struct{}
}

func newControlledRunner(store *kanban.Store) *controlledRunner {
	return &controlledRunner{
		store:   store,
		waiters: make(map[string]chan struct{}),
	}
}

func (r *controlledRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	if issue.State == kanban.StateReady {
		_ = r.store.UpdateIssueState(issue.ID, kanban.StateInProgress)
	}
	r.mu.Lock()
	r.starts = append(r.starts, issue.Identifier)
	r.events = append(r.events, runnerEvent{kind: "start", identifier: issue.Identifier})
	ch, ok := r.waiters[issue.Identifier]
	if !ok {
		ch = make(chan struct{})
		r.waiters[issue.Identifier] = ch
	}
	r.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-ch:
	}

	if err := r.store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.events = append(r.events, runnerEvent{kind: "done", identifier: issue.Identifier})
	r.mu.Unlock()
	return &agent.RunResult{Success: true}, nil
}

func (r *controlledRunner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	return nil
}

func (r *controlledRunner) complete(identifier string) {
	r.mu.Lock()
	ch, ok := r.waiters[identifier]
	if !ok {
		ch = make(chan struct{})
		r.waiters[identifier] = ch
	}
	delete(r.waiters, identifier)
	r.mu.Unlock()
	close(ch)
}

func (r *controlledRunner) waitForStarts(t *testing.T, expected int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		starts := append([]string(nil), r.starts...)
		r.mu.Unlock()
		if len(starts) >= expected {
			return starts
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d starts", expected)
	return nil
}

func (r *controlledRunner) snapshotEvents() []runnerEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runnerEvent(nil), r.events...)
}

func assertBlockedExecution(t *testing.T, events []runnerEvent, blockers map[string][]string) {
	t.Helper()
	doneAt := map[string]int{}
	for idx, event := range events {
		switch event.kind {
		case "done":
			doneAt[event.identifier] = idx
		case "start":
			for _, blocker := range blockers[event.identifier] {
				doneIdx, ok := doneAt[blocker]
				if !ok || doneIdx >= idx {
					t.Fatalf("issue %s started before blocker %s completed; events=%v", event.identifier, blocker, events)
				}
			}
		}
	}
}

func createDependencyGraph(t *testing.T, store *kanban.Store) map[string]*kanban.Issue {
	t.Helper()
	project, err := store.CreateProject("Graph", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	makeIssue := func(title string, priority int) *kanban.Issue {
		issue, err := store.CreateIssue(project.ID, "", title, "", priority, nil)
		if err != nil {
			t.Fatalf("CreateIssue failed: %v", err)
		}
		if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
			t.Fatalf("UpdateIssueState failed: %v", err)
		}
		return issue
	}
	issues := map[string]*kanban.Issue{
		"A": makeIssue("A", 1),
		"B": makeIssue("B", 2),
		"C": makeIssue("C", 3),
		"D": makeIssue("D", 4),
		"E": makeIssue("E", 5),
	}
	if _, err := store.SetIssueBlockers(issues["B"].ID, []string{issues["A"].Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}
	if _, err := store.SetIssueBlockers(issues["C"].ID, []string{issues["A"].Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}
	if _, err := store.SetIssueBlockers(issues["D"].ID, []string{issues["B"].Identifier, issues["C"].Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}
	return issues
}

func TestDispatchRespectsDependencyOrderSerial(t *testing.T) {
	orch, store, _, _ := setupTestOrchestratorWithConcurrency(t, "cat", 1)
	runner := newControlledRunner(store)
	orch.runner = runner
	issues := createDependencyGraph(t, store)

	expected := []string{
		issues["A"].Identifier,
		issues["B"].Identifier,
		issues["C"].Identifier,
		issues["D"].Identifier,
		issues["E"].Identifier,
	}

	for idx, identifier := range expected {
		if err := orch.dispatch(context.Background()); err != nil {
			t.Fatalf("dispatch failed: %v", err)
		}
		starts := runner.waitForStarts(t, idx+1, time.Second)
		if starts[idx] != identifier {
			t.Fatalf("expected start %d to be %s, got %s (all starts=%v)", idx, identifier, starts[idx], starts)
		}
		runner.complete(identifier)
		waitForNoRunning(t, orch, time.Second)
	}

	assertBlockedExecution(t, runner.snapshotEvents(), map[string][]string{
		issues["B"].Identifier: {issues["A"].Identifier},
		issues["C"].Identifier: {issues["A"].Identifier},
		issues["D"].Identifier: {issues["B"].Identifier, issues["C"].Identifier},
	})
}

func TestDispatchRespectsDependencyOrderParallel(t *testing.T) {
	orch, store, _, _ := setupTestOrchestratorWithConcurrency(t, "cat", 2)
	runner := newControlledRunner(store)
	orch.runner = runner
	issues := createDependencyGraph(t, store)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	starts := runner.waitForStarts(t, 2, time.Second)
	if starts[0] != issues["A"].Identifier || starts[1] != issues["E"].Identifier {
		t.Fatalf("expected initial parallel starts A and E, got %v", starts)
	}

	runner.complete(issues["E"].Identifier)
	runner.complete(issues["A"].Identifier)
	waitForNoRunning(t, orch, time.Second)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	starts = runner.waitForStarts(t, 4, time.Second)
	if starts[2] != issues["B"].Identifier || starts[3] != issues["C"].Identifier {
		t.Fatalf("expected B and C after A, got %v", starts)
	}

	runner.complete(issues["B"].Identifier)
	waitForRunningCount(t, orch, 1, time.Second)
	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	starts = runner.waitForStarts(t, 4, time.Second)
	if len(starts) != 4 {
		t.Fatalf("expected D to stay blocked while C is running, got %v", starts)
	}

	runner.complete(issues["C"].Identifier)
	waitForNoRunning(t, orch, time.Second)
	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	starts = runner.waitForStarts(t, 5, time.Second)
	if starts[4] != issues["D"].Identifier {
		t.Fatalf("expected D after B and C, got %v", starts)
	}
	runner.complete(issues["D"].Identifier)
	waitForNoRunning(t, orch, time.Second)

	assertBlockedExecution(t, runner.snapshotEvents(), map[string][]string{
		issues["B"].Identifier: {issues["A"].Identifier},
		issues["C"].Identifier: {issues["A"].Identifier},
		issues["D"].Identifier: {issues["B"].Identifier, issues["C"].Identifier},
	})
}
