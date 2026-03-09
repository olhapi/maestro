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
	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
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

func writeAppServerWorkflow(t *testing.T, manager *config.Manager, workspaceRoot, command, approvalPolicy string, turnTimeoutMs, stallTimeoutMs int) {
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
  max_turns: 1
  max_retry_backoff_ms: 100
  mode: app_server
codex:
  command: ` + command + `
  approval_policy: ` + approvalPolicy + `
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 500
  turn_timeout_ms: ` + fmt.Sprintf("%d", turnTimeoutMs) + `
  stall_timeout_ms: ` + fmt.Sprintf("%d", stallTimeoutMs) + `
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(manager.Path(), []byte(workflowContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}
}

func waitForLiveSession(t *testing.T, orch *Orchestrator, identifier string, timeout time.Duration) appserver.Session {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sessions := orch.LiveSessions()["sessions"].(map[string]interface{})
		if session, ok := sessions[identifier].(appserver.Session); ok {
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for live session %s", identifier)
	return appserver.Session{}
}

func waitForWorkspaceRemoval(t *testing.T, store *kanban.Store, issueID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := store.GetWorkspace(issueID); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for workspace %s removal", issueID)
}

func waitForExecutionSnapshot(t *testing.T, store *kanban.Store, issueID string, timeout time.Duration) *kanban.ExecutionSessionSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot, err := store.GetIssueExecutionSession(issueID)
		if err == nil && snapshot.RunKind != "run_started" {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for execution snapshot %s", issueID)
	return nil
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
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, release := fakeappserver.CommandString(t, func() fakeappserver.Scenario {
		scenario := fakeappserver.Scenario{
			Steps: []fakeappserver.Step{
				{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
				{Match: fakeappserver.Match{Method: "initialized"}},
				{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-live"}}}}}},
				{
					Match:          fakeappserver.Match{Method: "turn/start"},
					Emit:           []fakeappserver.Output{{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-live"}}}}},
					WaitForRelease: "complete",
					EmitAfterRelease: []fakeappserver.Output{{
						JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-live", "turnId": "turn-live"}},
					}},
					ExitCode: fakeappserver.Int(0),
				},
			},
		}
		return scenario
	}())
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 3000, 0)

	issue, _ := store.CreateIssue("", "", "Live Session", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}

	session := waitForLiveSession(t, orch, issue.Identifier, 2*time.Second)
	if session.SessionID != "thread-live-turn-live" || session.TurnsStarted != 1 || session.IssueID != issue.ID || session.IssueIdentifier != issue.Identifier {
		t.Fatalf("unexpected live session: %+v", session)
	}
	release("complete")
	waitForNoRunning(t, orch, 3*time.Second)
	sessions := orch.LiveSessions()["sessions"].(map[string]interface{})
	if len(sessions) != 0 {
		t.Fatalf("expected no live sessions after run completion, got %#v", sessions)
	}
}

func TestInterruptedRunPersistsLatestExecutionSessionSnapshot(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-approval"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-approval"}}}},
				{JSON: map[string]interface{}{"id": 99, "method": "item/commandExecution/requestApproval", "params": map[string]interface{}{"command": "gh pr view"}}},
			}},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "on_request", 3000, 3000)

	issue, _ := store.CreateIssue("", "", "Approval snapshot", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := waitForExecutionSnapshot(t, store, issue.ID, 3*time.Second)
	waitForNoRunning(t, orch, 3*time.Second)

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
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-stall"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-stall"}}}}}, WaitForRelease: "never"},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 6000, 2500)

	issue, _ := store.CreateIssue("", "", "Stall snapshot", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := waitForExecutionSnapshot(t, store, issue.ID, 5*time.Second)
	waitForNoRunning(t, orch, 5*time.Second)

	if snapshot.Error != "stall_timeout" || snapshot.RunKind != "run_unsuccessful" || snapshot.Attempt != 0 {
		t.Fatalf("unexpected stall snapshot: %+v", snapshot)
	}
	if snapshot.AppSession.SessionID != "thread-stall-turn-stall" {
		t.Fatalf("unexpected persisted stall session: %+v", snapshot.AppSession)
	}
}

func TestCompletedRunPersistsLatestExecutionSessionSnapshot(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-complete"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-complete"}}}},
				{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-complete", "turnId": "turn-complete"}}},
			}, ExitCode: fakeappserver.Int(0)},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 3000, 3000)

	issue, _ := store.CreateIssue("", "", "Completion snapshot", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := waitForExecutionSnapshot(t, store, issue.ID, 3*time.Second)
	waitForNoRunning(t, orch, 3*time.Second)

	if snapshot.RunKind != "run_completed" || snapshot.Error != "" {
		t.Fatalf("unexpected completion snapshot: %+v", snapshot)
	}
	if snapshot.AppSession.SessionID != "thread-complete-turn-complete" || snapshot.AppSession.TerminalReason != "turn.completed" {
		t.Fatalf("unexpected persisted completed session: %+v", snapshot.AppSession)
	}
}

func TestReconcileStopsCancelledRunsAndCleansWorkspace(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	runner := newControlledRunner(store)
	orch.runner = runner
	issue, _ := store.CreateIssue("", "", "Sleep", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.waitForStarts(t, 1, time.Second)
	if err := store.UpdateIssueState(issue.ID, kanban.StateCancelled); err != nil {
		t.Fatal(err)
	}

	orch.reconcile(context.Background())
	waitForWorkspaceRemoval(t, store, issue.ID, time.Second)
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

func TestSnapshotAndRetryNowExposeDashboardScenarioShape(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	runningIssue, err := store.CreateIssue("", "", "Running", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue running: %v", err)
	}
	if err := store.UpdateIssueState(runningIssue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState running: %v", err)
	}
	doneIssue, err := store.CreateIssue("", "", "Done", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	if err := store.UpdateIssueState(doneIssue.ID, kanban.StateDone); err != nil {
		t.Fatalf("UpdateIssueState done: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(doneIssue.ID, kanban.WorkflowPhaseDone); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase done: %v", err)
	}

	startedAt := time.Now().UTC().Add(-10 * time.Second)
	orch.mu.Lock()
	orch.running[runningIssue.ID] = runningEntry{
		cancel:    func() {},
		issue:     *runningIssue,
		attempt:   2,
		phase:     kanban.WorkflowPhaseImplementation,
		startedAt: startedAt,
	}
	orch.liveSessions[runningIssue.ID] = &appserver.Session{
		SessionID:       "thread-live-turn-live",
		ThreadID:        "thread-live",
		TurnID:          "turn-live",
		LastEvent:       "turn.started",
		LastMessage:     "Working",
		LastTimestamp:   startedAt.Add(8 * time.Second),
		InputTokens:     11,
		OutputTokens:    7,
		TotalTokens:     18,
		TurnsStarted:    2,
		TurnsCompleted:  1,
		IssueID:         runningIssue.ID,
		IssueIdentifier: runningIssue.Identifier,
	}
	orch.retries[doneIssue.ID] = retryEntry{
		Attempt:   3,
		Phase:     string(kanban.WorkflowPhaseDone),
		DueAt:     time.Now().UTC().Add(5 * time.Minute),
		Error:     "approval_required",
		DelayType: "failure",
	}
	orch.mu.Unlock()

	snapshot := orch.Snapshot()
	if len(snapshot.Running) != 1 || len(snapshot.Retrying) != 1 {
		t.Fatalf("unexpected snapshot shape: %+v", snapshot)
	}
	if snapshot.Running[0].Identifier != runningIssue.Identifier || snapshot.Running[0].Tokens.TotalTokens != 18 {
		t.Fatalf("unexpected running payload: %+v", snapshot.Running[0])
	}
	if snapshot.Retrying[0].Identifier != doneIssue.Identifier || snapshot.Retrying[0].DelayType != "failure" {
		t.Fatalf("unexpected retry payload: %+v", snapshot.Retrying[0])
	}

	live := orch.LiveSessions()["sessions"].(map[string]interface{})
	session, ok := live[runningIssue.Identifier].(appserver.Session)
	if !ok {
		t.Fatalf("expected live session for %s, got %#v", runningIssue.Identifier, live)
	}
	if session.SessionID != "thread-live-turn-live" || session.IssueIdentifier != runningIssue.Identifier {
		t.Fatalf("unexpected live session payload: %+v", session)
	}

	result := orch.RetryIssueNow(doneIssue.Identifier)
	if result["status"] != "queued_now" {
		t.Fatalf("unexpected retry-now result: %#v", result)
	}

	updated := orch.Snapshot()
	if updated.Retrying[0].DueInMs > 1000 {
		t.Fatalf("expected retry to be due immediately, got %+v", updated.Retrying[0])
	}

	events, err := store.ListRuntimeEvents(0, 10)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(events) == 0 || events[0].Kind != "manual_retry_requested" {
		t.Fatalf("expected manual retry event, got %#v", events)
	}
}

func TestRetryNowAndRefreshHandleAdditionalControlPaths(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	if result := orch.RetryIssueNow("ISS-404"); result["status"] != "not_found" {
		t.Fatalf("expected not_found retry result, got %#v", result)
	}

	readyIssue, err := store.CreateIssue("", "", "Ready", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue ready: %v", err)
	}
	if err := store.UpdateIssueState(readyIssue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState ready: %v", err)
	}
	if result := orch.RetryIssueNow(readyIssue.Identifier); result["status"] != "refresh_requested" {
		t.Fatalf("expected refresh_requested for ready issue, got %#v", result)
	}

	doneIssue, err := store.CreateIssue("", "", "Done", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	if err := store.UpdateIssueState(doneIssue.ID, kanban.StateDone); err != nil {
		t.Fatalf("UpdateIssueState done: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(doneIssue.ID, kanban.WorkflowPhaseDone); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase done: %v", err)
	}
	if result := orch.RetryIssueNow(doneIssue.Identifier); result["status"] != "queued_now" {
		t.Fatalf("expected queued_now for done issue, got %#v", result)
	}

	refresh := orch.RequestRefresh()
	if refresh["status"] != "accepted" {
		t.Fatalf("unexpected refresh payload: %#v", refresh)
	}
	events, err := store.ListRuntimeEvents(0, 10)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("expected retry/refresh events, got %#v", events)
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
