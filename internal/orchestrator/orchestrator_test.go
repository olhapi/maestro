package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/olhapi/symphony-go/internal/agent"
	"github.com/olhapi/symphony-go/internal/appserver"
	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/pkg/config"
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

type runnerEvent struct {
	kind       string
	identifier string
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
