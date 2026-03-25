package orchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agent"
	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/pkg/config"
)

type retryTestRunner struct {
	runCalls     chan string
	cleanupCalls chan string
	release      chan struct{}
}

type interruptedFailureRunner struct {
	store *kanban.Store
	calls int
}

func newRetryTestRunner() *retryTestRunner {
	return &retryTestRunner{
		runCalls:     make(chan string, 8),
		cleanupCalls: make(chan string, 8),
		release:      make(chan struct{}),
	}
}

func (r *retryTestRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	r.runCalls <- issue.Identifier
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.release:
		return &agent.RunResult{Success: true}, nil
	}
}

func (r *retryTestRunner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	r.cleanupCalls <- issue.Identifier
	return nil
}

func (r *interruptedFailureRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	r.calls++
	if issue.State == kanban.StateReady {
		if err := r.store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
			return nil, err
		}
	}
	return &agent.RunResult{
		Success: false,
		Error:   errors.New("stall_timeout"),
		AppSession: &appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-stall-turn-stall",
			ThreadID:        "thread-stall",
			TurnID:          "turn-stall",
			LastEvent:       "item.started",
			LastTimestamp:   time.Now().UTC(),
		},
	}, nil
}

func (r *interruptedFailureRunner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	return nil
}

func waitForRunCall(t *testing.T, ch <-chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case identifier := <-ch:
		return identifier
	case <-time.After(timeout):
		t.Fatal("timed out waiting for run call")
		return ""
	}
}

func waitForCleanupCall(t *testing.T, ch <-chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case identifier := <-ch:
		return identifier
	case <-time.After(timeout):
		t.Fatal("timed out waiting for cleanup call")
		return ""
	}
}

func TestProcessRetriesAndRunLoopHelpers(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	runner := newRetryTestRunner()
	orch.runner = runner

	runIssue, err := store.CreateIssue("", "", "Retry run", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue run: %v", err)
	}
	if err := store.UpdateIssueState(runIssue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState run: %v", err)
	}

	terminalIssue, err := store.CreateIssue("", "", "Retry cleanup", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue terminal: %v", err)
	}
	if err := store.UpdateIssueStateAndPhase(terminalIssue.ID, kanban.StateDone, kanban.WorkflowPhaseComplete); err != nil {
		t.Fatalf("UpdateIssueStateAndPhase terminal: %v", err)
	}

	blockedIssue, err := store.CreateIssue("", "", "Retry blocked", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	if err := store.UpdateIssueState(blockedIssue.ID, kanban.StateBacklog); err != nil {
		t.Fatalf("UpdateIssueState blocked: %v", err)
	}

	now := time.Now().Add(-time.Second)
	orch.claimed[runIssue.ID] = struct{}{}
	orch.claimed[terminalIssue.ID] = struct{}{}
	orch.claimed[blockedIssue.ID] = struct{}{}
	orch.claimed["missing"] = struct{}{}
	orch.retries[runIssue.ID] = retryEntry{Attempt: 2, Identifier: runIssue.Identifier, Phase: string(kanban.WorkflowPhaseImplementation), DueAt: now, DelayType: "failure"}
	orch.retries[terminalIssue.ID] = retryEntry{Attempt: 1, Identifier: terminalIssue.Identifier, Phase: string(kanban.WorkflowPhaseComplete), DueAt: now, DelayType: "failure"}
	orch.retries[blockedIssue.ID] = retryEntry{Attempt: 1, Identifier: blockedIssue.Identifier, Phase: string(kanban.WorkflowPhaseImplementation), DueAt: now, DelayType: "failure"}
	orch.retries["missing"] = retryEntry{Attempt: 1, Phase: string(kanban.WorkflowPhaseImplementation), DueAt: now, DelayType: "failure"}

	orch.processRetries(context.Background())

	if got := waitForRunCall(t, runner.runCalls, time.Second); got != runIssue.Identifier {
		t.Fatalf("expected run retry for %s, got %s", runIssue.Identifier, got)
	}
	if got := waitForCleanupCall(t, runner.cleanupCalls, time.Second); got != terminalIssue.Identifier {
		t.Fatalf("expected cleanup retry for %s, got %s", terminalIssue.Identifier, got)
	}
	if _, ok := orch.retries["missing"]; ok {
		t.Fatal("expected missing retry to be dropped")
	}
	if _, ok := orch.retries[blockedIssue.ID]; ok {
		t.Fatal("expected blocked retry to be dropped")
	}
	if _, ok := orch.running[runIssue.ID]; !ok {
		t.Fatal("expected due retry to start running issue")
	}

	close(runner.release)
	waitForNoRunning(t, orch, time.Second)

	events := orch.Events(0, 2)
	if events["last_seq"].(int64) == 0 {
		t.Fatalf("expected event sequence in payload: %#v", events)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(120 * time.Millisecond)
		cancel()
	}()
	if err := orch.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected Run to exit on cancellation, got %v", err)
	}
	if orch.lastTickAt.IsZero() {
		t.Fatal("expected Run to execute at least one tick")
	}
}

func TestStartRunPersistsExecutionSessionImmediately(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	runner := newRetryTestRunner()
	orch.runner = runner

	issue, err := store.CreateIssue("", "", "Immediate snapshot", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := waitForRunCall(t, runner.runCalls, time.Second); got != issue.Identifier {
		t.Fatalf("expected run call for %s, got %s", issue.Identifier, got)
	}

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.RunKind != "run_started" || snapshot.Phase != "implementation" || snapshot.Attempt != 0 {
		t.Fatalf("unexpected snapshot metadata: %#v", snapshot)
	}
	if snapshot.AppSession.IssueID != issue.ID || snapshot.AppSession.IssueIdentifier != issue.Identifier {
		t.Fatalf("expected issue metadata in snapshot: %#v", snapshot.AppSession)
	}

	close(runner.release)
	waitForNoRunning(t, orch, time.Second)
}

func TestUpdateLiveSessionPersistsWhileRunIsActive(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	runner := newRetryTestRunner()
	orch.runner = runner

	issue, err := store.CreateIssue("", "", "Live snapshot", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	waitForRunCall(t, runner.runCalls, time.Second)

	now := time.Now().UTC().Truncate(time.Second)
	orch.updateLiveSession(issue.ID, &appserver.Session{
		SessionID:     "thread-live-turn-live",
		ThreadID:      "thread-live",
		TurnID:        "turn-live",
		LastEvent:     "turn.started",
		LastTimestamp: now,
		LastMessage:   "Working",
	})

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.RunKind != "run_started" || snapshot.AppSession.SessionID != "thread-live-turn-live" {
		t.Fatalf("unexpected live snapshot payload: %#v", snapshot)
	}
	if snapshot.AppSession.LastEvent != "turn.started" || snapshot.AppSession.LastMessage != "Working" {
		t.Fatalf("expected latest session fields to persist: %#v", snapshot.AppSession)
	}

	close(runner.release)
	waitForNoRunning(t, orch, time.Second)
}

func TestReconcileRecoversOrphanedRunWithBackoffRetry(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)

	issue, err := store.CreateIssue("", "", "Orphaned retry", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-stale-turn-stale",
			LastEvent:       "turn.started",
			LastTimestamp:   now,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	orch.reconcile(context.Background())

	orch.mu.RLock()
	retry, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected orphaned run retry to be scheduled")
	}
	if retry.Attempt != 3 || retry.Phase != "implementation" || retry.Error != "run_interrupted" || retry.DelayType != "failure" {
		t.Fatalf("unexpected retry payload: %+v", retry)
	}
	if retry.DueAt.Before(time.Now().UTC().Add(9 * time.Second)) {
		t.Fatalf("expected failure backoff retry scheduling, got due_at=%v", retry.DueAt)
	}

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.RunKind != "run_interrupted" || snapshot.Error != "run_interrupted" {
		t.Fatalf("expected interrupted snapshot, got %#v", snapshot)
	}

	events, err := store.ListIssueRuntimeEvents(issue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	foundInterrupted := false
	for _, event := range events {
		if event.Kind == "run_interrupted" {
			foundInterrupted = true
			break
		}
	}
	if !foundInterrupted {
		t.Fatalf("expected run_interrupted event in %#v", events)
	}
}

func TestInterruptedFailuresPauseAutomaticRetriesAfterThreshold(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	runner := &interruptedFailureRunner{store: store}
	orch.runner = runner

	issue, err := store.CreateIssue("", "", "Pause retries", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	waitForNoRunning(t, orch, time.Second)

	for i := 0; i < 2; i++ {
		orch.mu.Lock()
		entry := orch.retries[issue.ID]
		entry.DueAt = time.Now().UTC()
		orch.retries[issue.ID] = entry
		orch.mu.Unlock()
		orch.processRetries(context.Background())
		waitForNoRunning(t, orch, time.Second)
	}

	orch.mu.RLock()
	paused, pausedOK := orch.paused[issue.ID]
	_, retryScheduled := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !pausedOK {
		t.Fatal("expected issue to be paused after three interrupted failures")
	}
	if retryScheduled {
		t.Fatal("expected retries to be cleared after pause")
	}
	if paused.Attempt != 3 || paused.ConsecutiveFailures != 3 || paused.PauseThreshold != 3 || paused.Error != "stall_timeout" {
		t.Fatalf("unexpected paused payload: %+v", paused)
	}
	if runner.calls != 3 {
		t.Fatalf("expected three interrupted runs before pause, got %d", runner.calls)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch while paused: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if runner.calls != 3 {
		t.Fatalf("expected paused issue not to redispatch, got %d calls", runner.calls)
	}

	resp := orch.RetryIssueNow(context.Background(), issue.Identifier)
	if resp["status"] != "queued_now" {
		t.Fatalf("unexpected retry response: %#v", resp)
	}
	orch.processRetries(context.Background())
	waitForNoRunning(t, orch, time.Second)
	if runner.calls != 4 {
		t.Fatalf("expected manual retry to resume execution, got %d calls", runner.calls)
	}
}

func TestReconcileRestoresPausedRunStateFromPersistedEvents(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)

	issue, err := store.CreateIssue("", "", "Paused restore", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	pausedAt := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    3,
		RunKind:    "retry_paused",
		Error:      "stall_timeout",
		UpdatedAt:  pausedAt,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-stall-turn-stall",
			LastEvent:       "item.started",
			LastTimestamp:   pausedAt,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
		"issue_id":             issue.ID,
		"identifier":           issue.Identifier,
		"phase":                "implementation",
		"attempt":              3,
		"paused_at":            pausedAt.Format(time.RFC3339),
		"error":                "stall_timeout",
		"consecutive_failures": 3,
		"pause_threshold":      3,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent retry_paused: %v", err)
	}

	orch.reconcile(context.Background())

	orch.mu.RLock()
	paused, ok := orch.paused[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected paused state to be restored from persisted events")
	}
	if paused.Attempt != 3 || paused.ConsecutiveFailures != 3 {
		t.Fatalf("unexpected restored paused payload: %+v", paused)
	}
}

func TestUpdateLiveSessionCoalescesPersistenceWhileRunIsActive(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	runner := newRetryTestRunner()
	orch.runner = runner

	issue, err := store.CreateIssue("", "", "Coalesced live snapshot", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	waitForRunCall(t, runner.runCalls, time.Second)

	now := time.Now().UTC().Truncate(time.Second)
	orch.updateLiveSession(issue.ID, &appserver.Session{
		SessionID:     "thread-live-turn-live",
		ThreadID:      "thread-live",
		TurnID:        "turn-live",
		LastEvent:     "turn.started",
		LastTimestamp: now,
		LastMessage:   "first persist",
	})
	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.AppSession.LastMessage != "first persist" {
		t.Fatalf("expected first live message to persist, got %#v", snapshot.AppSession)
	}

	orch.updateLiveSession(issue.ID, &appserver.Session{
		SessionID:     "thread-live-turn-live",
		ThreadID:      "thread-live",
		TurnID:        "turn-live",
		LastEvent:     "item.updated",
		LastTimestamp: now.Add(time.Second),
		LastMessage:   "coalesced away",
	})
	snapshot, err = store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession second read: %v", err)
	}
	if snapshot.AppSession.LastMessage != "first persist" {
		t.Fatalf("expected coalesced update not to persist immediately, got %#v", snapshot.AppSession)
	}

	orch.sessionWriteMu.Lock()
	state := orch.sessionWrites[issue.ID]
	state.LastPersistedAt = time.Now().UTC().Add(-3 * time.Second)
	orch.sessionWrites[issue.ID] = state
	orch.sessionWriteMu.Unlock()

	orch.updateLiveSession(issue.ID, &appserver.Session{
		SessionID:     "thread-live-turn-live",
		ThreadID:      "thread-live",
		TurnID:        "turn-live",
		LastEvent:     "item.updated",
		LastTimestamp: now.Add(3 * time.Second),
		LastMessage:   "persisted later",
	})
	snapshot, err = store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession third read: %v", err)
	}
	if snapshot.AppSession.LastMessage != "persisted later" {
		t.Fatalf("expected delayed live update to persist, got %#v", snapshot.AppSession)
	}

	close(runner.release)
	waitForNoRunning(t, orch, time.Second)
}

func TestReconcileRecoversBlockedOrphanWithoutRetry(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)

	blocker, err := store.CreateIssue("", "", "Blocking issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}

	issue, err := store.CreateIssue("", "", "Blocked orphan", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState blocked: %v", err)
	}
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"blocked_by": []string{blocker.Identifier}}); err != nil {
		t.Fatalf("UpdateIssue blocked_by: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-blocked-turn-stale",
			LastEvent:       "turn.started",
			LastTimestamp:   now,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    1,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	orch.reconcile(context.Background())

	orch.mu.RLock()
	_, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if ok {
		t.Fatal("expected blocked orphan not to schedule retry")
	}

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.RunKind != "run_interrupted" || snapshot.Error != "run_interrupted" {
		t.Fatalf("expected interrupted snapshot without retry, got %#v", snapshot)
	}
}

func TestAdvanceIssueAfterSuccessMatrix(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	workflow, err := manager.Current()
	if err != nil {
		t.Fatalf("manager.Current: %v", err)
	}

	makeIssue := func(state kanban.State, phase kanban.WorkflowPhase) *kanban.Issue {
		t.Helper()
		issue, err := store.CreateIssue("", "", string(state)+"-"+string(phase), "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.UpdateIssueStateAndPhase(issue.ID, state, phase); err != nil {
			t.Fatalf("UpdateIssueStateAndPhase: %v", err)
		}
		current, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		return current
	}

	tests := []struct {
		name      string
		state     kanban.State
		phase     kanban.WorkflowPhase
		wantPhase kanban.WorkflowPhase
		wantState kanban.State
		cont      bool
	}{
		{"implementation ready to review", kanban.StateReady, kanban.WorkflowPhaseImplementation, kanban.WorkflowPhaseReview, kanban.StateInReview, true},
		{"implementation done to done phase", kanban.StateDone, kanban.WorkflowPhaseImplementation, kanban.WorkflowPhaseDone, kanban.StateDone, true},
		{"implementation cancelled completes", kanban.StateCancelled, kanban.WorkflowPhaseImplementation, kanban.WorkflowPhaseComplete, kanban.StateCancelled, false},
		{"review in review to done phase", kanban.StateInReview, kanban.WorkflowPhaseReview, kanban.WorkflowPhaseDone, kanban.StateDone, true},
		{"review done stays done phase", kanban.StateDone, kanban.WorkflowPhaseReview, kanban.WorkflowPhaseDone, kanban.StateDone, true},
		{"done in review back to review", kanban.StateInReview, kanban.WorkflowPhaseDone, kanban.WorkflowPhaseReview, kanban.StateInReview, true},
		{"done terminal completes", kanban.StateDone, kanban.WorkflowPhaseDone, kanban.WorkflowPhaseComplete, kanban.StateDone, false},
	}

	for _, tc := range tests {
		issue := makeIssue(tc.state, tc.phase)
		nextPhase, cont := orch.advanceIssueAfterSuccess(workflow, issue, tc.phase)
		if nextPhase != tc.wantPhase || cont != tc.cont {
			t.Fatalf("%s: got (%s,%v), want (%s,%v)", tc.name, nextPhase, cont, tc.wantPhase, tc.cont)
		}
		current, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("%s: GetIssue: %v", tc.name, err)
		}
		if current.WorkflowPhase != tc.wantPhase || current.State != tc.wantState {
			t.Fatalf("%s: got state=%s phase=%s, want state=%s phase=%s", tc.name, current.State, current.WorkflowPhase, tc.wantState, tc.wantPhase)
		}
	}
}

func TestAdvanceIssueAfterSuccessWithoutReviewOrDoneKeepsImplementationOpen(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	makeIssue := func(state kanban.State) *kanban.Issue {
		t.Helper()
		issue, err := store.CreateIssue("", "", string(state)+"-implementation", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.UpdateIssueStateAndPhase(issue.ID, state, kanban.WorkflowPhaseImplementation); err != nil {
			t.Fatalf("UpdateIssueStateAndPhase: %v", err)
		}
		current, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		return current
	}

	workflow := &config.Workflow{Config: config.DefaultConfig()}
	workflow.Config.Phases.Review.Enabled = false
	workflow.Config.Phases.Review.Prompt = ""
	workflow.Config.Phases.Done.Enabled = false
	workflow.Config.Phases.Done.Prompt = ""

	for _, state := range []kanban.State{kanban.StateReady, kanban.StateInProgress} {
		issue := makeIssue(state)
		nextPhase, cont := orch.advanceIssueAfterSuccess(workflow, issue, kanban.WorkflowPhaseImplementation)
		if nextPhase != kanban.WorkflowPhaseImplementation || !cont {
			t.Fatalf("%s: got (%s,%v), want (%s,%v)", state, nextPhase, cont, kanban.WorkflowPhaseImplementation, true)
		}
		current, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("%s: GetIssue: %v", state, err)
		}
		if current.WorkflowPhase != kanban.WorkflowPhaseImplementation || current.State != state {
			t.Fatalf("%s: got state=%s phase=%s, want state=%s phase=%s", state, current.State, current.WorkflowPhase, state, kanban.WorkflowPhaseImplementation)
		}
	}
}

func TestRuntimeResolutionAndUtilityHelpers(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	repoPath := t.TempDir()
	project, err := store.CreateProject("Shared", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	shared := NewSharedWithExtensions(store, nil, "", "")
	shared.runnerFactory = func(*config.Manager) runnerExecutor { return newRetryTestRunner() }

	runtime, err := shared.runtimeForProject(project)
	if err != nil {
		t.Fatalf("runtimeForProject: %v", err)
	}
	if runtime.repoPath != repoPath || runtime.workflow == nil || runtime.runner == nil {
		t.Fatalf("unexpected runtime: %+v", runtime)
	}
	cached, err := shared.runtimeForProject(project)
	if err != nil {
		t.Fatalf("runtimeForProject cached: %v", err)
	}
	if cached != runtime {
		t.Fatal("expected cached project runtime")
	}

	scopedRepo := t.TempDir()
	scoped := NewSharedWithExtensions(store, nil, scopedRepo, "")
	scoped.runnerFactory = func(*config.Manager) runnerExecutor { return newRetryTestRunner() }
	issue, err := store.CreateIssue("", "", "Scoped", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue scoped: %v", err)
	}
	resolvedRuntime, workflow, err := scoped.runtimeForIssue(issue)
	if err != nil {
		t.Fatalf("runtimeForIssue scoped: %v", err)
	}
	if resolvedRuntime == nil || workflow == nil {
		t.Fatalf("expected scoped runtime resolution, got runtime=%v workflow=%v", resolvedRuntime, workflow)
	}

	if got := failureRetryDelay(0, 0); got != 10*time.Second {
		t.Fatalf("unexpected default retry delay: %v", got)
	}
	if got := failureRetryDelay(10, 1000); got != time.Second {
		t.Fatalf("expected capped retry delay, got %v", got)
	}
	if got := failureRetryDelay(210, 60000); got != time.Minute {
		t.Fatalf("expected saturating retry delay, got %v", got)
	}
	if got := failureRetryDelay(210, 1); got != time.Millisecond {
		t.Fatalf("expected millisecond cap, got %v", got)
	}
	if got := automaticRetryCount([]kanban.RuntimeEvent{
		{Kind: "retry_scheduled", DelayType: "failure"},
		{Kind: "retry_scheduled", DelayType: "continuation"},
		{Kind: "manual_retry_requested"},
		{Kind: "retry_scheduled", DelayType: "failure"},
	}); got != 1 {
		t.Fatalf("expected retry count after manual reset to be 1, got %d", got)
	}
	if got := automaticRetryCount([]kanban.RuntimeEvent{
		{Kind: "retry_scheduled", DelayType: "failure"},
		{Kind: "run_completed", Payload: map[string]interface{}{"next_retry": 2}},
		{Kind: "retry_scheduled", DelayType: "continuation"},
		{Kind: "retry_paused"},
	}); got != 0 {
		t.Fatalf("expected paused lifecycle to reset retry count, got %d", got)
	}
	if got := interruptedFailureStreak([]kanban.RuntimeEvent{
		{Kind: "workspace_bootstrap_created"},
		{Kind: "run_started"},
		{Kind: "run_unsuccessful", Error: "stall_timeout"},
		{Kind: "retry_scheduled", DelayType: "failure"},
		{Kind: "workspace_bootstrap_reused"},
		{Kind: "run_started"},
		{Kind: "run_unsuccessful", Error: "stall_timeout"},
		{Kind: "retry_scheduled", DelayType: "failure"},
		{Kind: "workspace_bootstrap_recovery"},
		{Kind: "run_started"},
		{Kind: "run_unsuccessful", Error: "stall_timeout"},
	}); got != 3 {
		t.Fatalf("expected interrupted failure streak to ignore workspace bootstrap events, got %d", got)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}
	if !shared.shouldPauseRunLocked("missing-issue", "stall_timeout") {
		t.Fatal("expected interrupted retry pause to fail closed when streak lookup fails")
	}

	events := shared.Events(10, 0)
	if events["since"].(int64) != 10 {
		t.Fatalf("unexpected empty events payload: %#v", events)
	}
}

func TestUpdateLiveSessionFlushesTokenSpendAfterDebounceAndBroadcasts(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	runner := newRetryTestRunner()
	orch.runner = runner

	issue, err := store.CreateIssue("", "", "Token delta", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	waitForRunCall(t, runner.runCalls, time.Second)

	updates, unsubscribe := observability.Subscribe()
	defer unsubscribe()

	now := time.Now().UTC().Truncate(time.Second)
	orch.updateLiveSession(issue.ID, &appserver.Session{
		ThreadID:      "thread-token",
		SessionID:     "thread-token-turn-1",
		TurnID:        "turn-1",
		LastEvent:     "thread.tokenUsage.updated",
		LastTimestamp: now,
		TotalTokens:   10,
	})
	current, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after first token update: %v", err)
	}
	if current.TotalTokensSpent != 0 {
		t.Fatalf("TotalTokensSpent after first update = %d, want 0", current.TotalTokensSpent)
	}

	orch.updateLiveSession(issue.ID, &appserver.Session{
		ThreadID:      "thread-token",
		SessionID:     "thread-token-turn-1",
		TurnID:        "turn-1",
		LastEvent:     "thread.tokenUsage.updated",
		LastTimestamp: now.Add(time.Second),
		TotalTokens:   10,
	})
	current, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after repeated token update: %v", err)
	}
	if current.TotalTokensSpent != 0 {
		t.Fatalf("TotalTokensSpent after repeated update = %d, want 0", current.TotalTokensSpent)
	}

	orch.tokenSpendMu.Lock()
	state := orch.tokenSpends[issue.ID]
	state.PendingSince = time.Now().Add(-(liveTokenSpendFlushInterval + time.Second))
	orch.tokenSpends[issue.ID] = state
	orch.tokenSpendMu.Unlock()

	orch.updateLiveSession(issue.ID, &appserver.Session{
		ThreadID:      "thread-token",
		SessionID:     "thread-token-turn-2",
		TurnID:        "turn-2",
		LastEvent:     "thread.tokenUsage.updated",
		LastTimestamp: now.Add(2 * time.Second),
		TotalTokens:   18,
	})

	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dashboard broadcast after live token spend flush")
	}

	current, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after flushed token update: %v", err)
	}
	if current.TotalTokensSpent != 18 {
		t.Fatalf("TotalTokensSpent after flushed token update = %d, want 18", current.TotalTokensSpent)
	}

	orch.updateLiveSession(issue.ID, &appserver.Session{
		ThreadID:      "thread-token",
		SessionID:     "thread-token-turn-3",
		TurnID:        "turn-3",
		LastEvent:     "thread.tokenUsage.updated",
		LastTimestamp: now.Add(3 * time.Second),
		TotalTokens:   25,
	})
	current, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after pending token update: %v", err)
	}
	if current.TotalTokensSpent != 18 {
		t.Fatalf("TotalTokensSpent after pending token update = %d, want 18", current.TotalTokensSpent)
	}

	orch.persistFinalIssueTokenSpend(issue.ID, &appserver.Session{
		ThreadID:      "thread-token",
		SessionID:     "thread-token-turn-3",
		TurnID:        "turn-3",
		LastEvent:     "thread.tokenUsage.updated",
		LastTimestamp: now.Add(3 * time.Second),
		TotalTokens:   25,
	})

	current, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after final token flush: %v", err)
	}
	if current.TotalTokensSpent != 25 {
		t.Fatalf("TotalTokensSpent after final token flush = %d, want 25", current.TotalTokensSpent)
	}

	close(runner.release)
	waitForNoRunning(t, orch, time.Second)
}

func TestPersistFinalIssueTokenSpendDeduplicatesAcrossLiveThreadSwitches(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	issue, err := store.CreateIssue("", "", "Token thread switches", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	orch.observeIssueTokenSpend(issue.ID, &appserver.Session{
		ThreadID:    "thread-a",
		SessionID:   "thread-a-turn-1",
		TotalTokens: 10,
	})
	orch.observeIssueTokenSpend(issue.ID, &appserver.Session{
		ThreadID:    "thread-b",
		SessionID:   "thread-b-turn-1",
		TotalTokens: 4,
	})
	orch.observeIssueTokenSpend(issue.ID, &appserver.Session{
		ThreadID:    "thread-a",
		SessionID:   "thread-a-turn-2",
		TotalTokens: 12,
	})
	orch.observeIssueTokenSpend(issue.ID, &appserver.Session{
		ThreadID:    "thread-b",
		SessionID:   "thread-b-turn-2",
		TotalTokens: 7,
	})

	current, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue before final flush: %v", err)
	}
	if current.TotalTokensSpent != 0 {
		t.Fatalf("TotalTokensSpent before final flush = %d, want 0", current.TotalTokensSpent)
	}

	orch.persistFinalIssueTokenSpend(issue.ID, &appserver.Session{
		ThreadID:    "thread-a",
		SessionID:   "thread-a-turn-2",
		TotalTokens: 12,
	})

	current, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after final flush: %v", err)
	}
	if current.TotalTokensSpent != 19 {
		t.Fatalf("TotalTokensSpent after final flush = %d, want 19", current.TotalTokensSpent)
	}
}

func TestPersistFinalIssueTokenSpendUsesFinalizedRunTotals(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	issue, err := store.CreateIssue("", "", "Token terminal", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	orch.persistFinalIssueTokenSpend(issue.ID, &appserver.Session{ThreadID: "thread-a", TotalTokens: 18})

	current, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after first finalized total: %v", err)
	}
	if current.TotalTokensSpent != 18 {
		t.Fatalf("TotalTokensSpent after first finalized total = %d, want 18", current.TotalTokensSpent)
	}

	orch.persistFinalIssueTokenSpend(issue.ID, &appserver.Session{ThreadID: "thread-a", TotalTokens: 45})
	current, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after second finalized total: %v", err)
	}
	if current.TotalTokensSpent != 45 {
		t.Fatalf("TotalTokensSpent after second finalized total = %d, want 45", current.TotalTokensSpent)
	}

	orch.persistFinalIssueTokenSpend(issue.ID, &appserver.Session{ThreadID: "thread-b", TotalTokens: 6})
	current, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after third finalized total: %v", err)
	}
	if current.TotalTokensSpent != 51 {
		t.Fatalf("TotalTokensSpent after third finalized total = %d, want 51", current.TotalTokensSpent)
	}
}

func TestIssueTokenSpendHelpersTrackRunKeysAndResetState(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	issue, err := store.CreateIssue("", "", "Token helper state", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if got := issueTokenSpendRunKey(nil); got != "" {
		t.Fatalf("issueTokenSpendRunKey(nil) = %q, want empty", got)
	}
	if got := issueTokenSpendRunKey(&appserver.Session{ThreadID: " thread-a ", SessionID: "session-a"}); got != "thread:thread-a" {
		t.Fatalf("issueTokenSpendRunKey(thread) = %q, want thread:thread-a", got)
	}
	if got := issueTokenSpendRunKey(&appserver.Session{SessionID: " session-b "}); got != "session:session-b" {
		t.Fatalf("issueTokenSpendRunKey(session) = %q, want session:session-b", got)
	}

	orch.observeIssueTokenSpend(issue.ID, &appserver.Session{TotalTokens: 2})
	orch.observeIssueTokenSpend(issue.ID, &appserver.Session{TotalTokens: 7})
	orch.observeIssueTokenSpend(issue.ID, &appserver.Session{TotalTokens: 5})
	orch.restoreIssueTokenSpend(issue.ID, 4)

	orch.tokenSpendMu.Lock()
	state := orch.tokenSpends[issue.ID]
	orch.tokenSpendMu.Unlock()
	if state.PendingDelta != 11 {
		t.Fatalf("PendingDelta = %d, want 11", state.PendingDelta)
	}
	if state.LastUnnamedTotal != 7 {
		t.Fatalf("LastUnnamedTotal = %d, want 7", state.LastUnnamedTotal)
	}
	if state.PendingSince.IsZero() {
		t.Fatal("expected restoreIssueTokenSpend to restart the pending window")
	}
	if !state.LastFlushedAt.IsZero() {
		t.Fatalf("LastFlushedAt = %v, want zero", state.LastFlushedAt)
	}

	orch.clearIssueTokenSpendState(issue.ID)
	orch.tokenSpendMu.Lock()
	_, ok := orch.tokenSpends[issue.ID]
	orch.tokenSpendMu.Unlock()
	if ok {
		t.Fatal("expected clearIssueTokenSpendState to remove the tracked issue")
	}

	if got := issueTokenSpendRunKey(&appserver.Session{}); got != "" {
		t.Fatalf("issueTokenSpendRunKey(empty session) = %q, want empty", got)
	}
}
