package orchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agent"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/pkg/config"
)

type retryTestRunner struct {
	runCalls     chan string
	cleanupCalls chan string
	release      chan struct{}
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
	orch.retries[runIssue.ID] = retryEntry{Attempt: 2, Phase: string(kanban.WorkflowPhaseImplementation), DueAt: now, DelayType: "failure"}
	orch.retries[terminalIssue.ID] = retryEntry{Attempt: 1, Phase: string(kanban.WorkflowPhaseComplete), DueAt: now, DelayType: "failure"}
	orch.retries[blockedIssue.ID] = retryEntry{Attempt: 1, Phase: string(kanban.WorkflowPhaseImplementation), DueAt: now, DelayType: "failure"}
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

	events := shared.Events(10, 0)
	if events["since"].(int64) != 10 {
		t.Fatalf("unexpected empty events payload: %#v", events)
	}
}
