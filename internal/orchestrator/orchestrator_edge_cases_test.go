package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agent"
	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/pkg/config"
)

type failingCleanupRunner struct{}

func (failingCleanupRunner) RunAttempt(context.Context, *kanban.Issue, int) (*agent.RunResult, error) {
	return &agent.RunResult{Success: true}, nil
}

func TestClassifyOrphanedResumeSupportsClaudeStdio(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)

	issue, err := store.CreateIssue("", "", "Claude orphan", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	workflow := &config.Workflow{Config: config.DefaultConfig()}
	workflow.Config.Runtime.Default = "claude"

	threadID, mode := orch.classifyOrphanedResume(workflow, issue, &kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		RunKind:    "run_started",
		AppSession: agentruntime.Session{
			ThreadID:  "claude-session-1",
			SessionID: "claude-session-1",
		},
	})
	if threadID != "claude-session-1" || mode != "opportunistic" {
		t.Fatalf("expected claude stdio orphan to resume opportunistically, got thread=%q mode=%q", threadID, mode)
	}

	workflow.Config.Runtime.Default = "codex-stdio"
	threadID, mode = orch.classifyOrphanedResume(workflow, issue, &kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		RunKind:    "run_started",
		AppSession: agentruntime.Session{
			ThreadID:  "codex-thread-1",
			SessionID: "codex-thread-1",
		},
	})
	if threadID != "" || mode != "" {
		t.Fatalf("expected non-resumable codex stdio orphan to skip resume, got thread=%q mode=%q", threadID, mode)
	}
}

func TestOrchestratorCoverageRecurringRerunBranches(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)

	if got := orch.processPendingRecurringRerunIgnoringRunning(nil, ""); got {
		t.Fatal("expected nil recurring issue to be ignored")
	}

	enabled := true
	enabledIssue, err := store.CreateIssueWithOptions("", "", "Recurring rerun", "", 0, nil, kanban.IssueCreateOptions{
		IssueType: kanban.IssueTypeRecurring,
		Cron:      "0 * * * *",
		Enabled:   &enabled,
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions enabled: %v", err)
	}
	if err := store.MarkRecurringPendingRerun(enabledIssue.ID, true); err != nil {
		t.Fatalf("MarkRecurringPendingRerun enabled: %v", err)
	}
	enabledLoaded, err := store.GetIssue(enabledIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue enabled: %v", err)
	}
	enabledLoaded.PendingRerun = true
	if got := orch.processPendingRecurringRerunIgnoringRunning(enabledLoaded, ""); !got {
		t.Fatal("expected enabled pending rerun to enqueue")
	}
	enabledReloaded, err := store.GetIssue(enabledIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue enabled reloaded: %v", err)
	}
	if enabledReloaded.PendingRerun || enabledReloaded.State != kanban.StateReady {
		t.Fatalf("expected enabled rerun to clear pending flag and move to ready, got %#v", enabledReloaded)
	}

	disabled := false
	disabledIssue, err := store.CreateIssueWithOptions("", "", "Recurring disabled rerun", "", 0, nil, kanban.IssueCreateOptions{
		IssueType: kanban.IssueTypeRecurring,
		Cron:      "0 * * * *",
		Enabled:   &disabled,
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions disabled: %v", err)
	}
	if err := store.MarkRecurringPendingRerun(disabledIssue.ID, true); err != nil {
		t.Fatalf("MarkRecurringPendingRerun disabled: %v", err)
	}
	disabledLoaded, err := store.GetIssue(disabledIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue disabled: %v", err)
	}
	disabledLoaded.PendingRerun = true
	if got := orch.processPendingRecurringRerunIgnoringRunning(disabledLoaded, ""); got {
		t.Fatal("expected disabled pending rerun to clear without enqueueing")
	}
	disabledReloaded, err := store.GetIssue(disabledIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue disabled reloaded: %v", err)
	}
	if disabledReloaded.PendingRerun {
		t.Fatalf("expected disabled rerun to clear the pending flag, got %#v", disabledReloaded)
	}
}

func TestOrchestratorCoverageAdditionalBranches(t *testing.T) {
	t.Run("cleanup terminal workspaces", func(t *testing.T) {
		orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
		enablePhaseWorkflow(t, manager, workspaceRoot)
		runner := newRetryTestRunner()
		orch.runner = runner

		terminalIssue, err := store.CreateIssue("", "", "Terminal cleanup", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue terminal: %v", err)
		}
		if err := store.UpdateIssueState(terminalIssue.ID, kanban.StateDone); err != nil {
			t.Fatalf("UpdateIssueState terminal: %v", err)
		}
		terminalWorkspace := filepath.Join(t.TempDir(), "terminal")
		if _, err := store.CreateWorkspace(terminalIssue.ID, terminalWorkspace); err != nil {
			t.Fatalf("CreateWorkspace terminal: %v", err)
		}

		activeIssue, err := store.CreateIssue("", "", "Active cleanup", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue active: %v", err)
		}
		if err := store.UpdateIssueState(activeIssue.ID, kanban.StateInProgress); err != nil {
			t.Fatalf("UpdateIssueState active: %v", err)
		}
		activeWorkspace := filepath.Join(t.TempDir(), "active")
		if _, err := store.CreateWorkspace(activeIssue.ID, activeWorkspace); err != nil {
			t.Fatalf("CreateWorkspace active: %v", err)
		}

		orch.cleanupTerminalWorkspaces(context.Background())

		if got := waitForCleanupCall(t, runner.cleanupCalls, time.Second); got != terminalIssue.Identifier {
			t.Fatalf("expected terminal cleanup for %s, got %s", terminalIssue.Identifier, got)
		}
		select {
		case got := <-runner.cleanupCalls:
			t.Fatalf("unexpected cleanup for %s", got)
		default:
		}
	})

	t.Run("schedule automatic retry branches", func(t *testing.T) {
		orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
		enablePhaseWorkflow(t, manager, workspaceRoot)
		workflow, err := manager.Current()
		if err != nil {
			t.Fatalf("manager.Current: %v", err)
		}

		retryIssue, err := store.CreateIssue("", "", "Retry branch", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue retry: %v", err)
		}
		dueAt := time.Now().UTC().Add(time.Minute)
		if got := orch.scheduleAutomaticRetryLockedWithResume(workflow, retryIssue, 1, kanban.WorkflowPhaseImplementation, "failure", "retry me", 1000, &dueAt, "thread-resume"); !got {
			t.Fatal("expected retry scheduling with dueAt to succeed")
		}
		orch.mu.RLock()
		entry, ok := orch.retries[retryIssue.ID]
		orch.mu.RUnlock()
		if !ok || entry.ResumeThreadID != "thread-resume" {
			t.Fatalf("expected retry entry to record resume thread, got %#v ok=%v", entry, ok)
		}

		if got := orch.scheduleAutomaticRetryLockedWithResume(workflow, nil, 1, kanban.WorkflowPhaseImplementation, "failure", "retry me", 1000, nil, ""); got {
			t.Fatal("expected nil issue retry scheduling to fail closed")
		}

		closedStore, err := kanban.NewStore(filepath.Join(t.TempDir(), "closed.db"))
		if err != nil {
			t.Fatalf("kanban.NewStore closed: %v", err)
		}
		if err := closedStore.Close(); err != nil {
			t.Fatalf("Close closed store: %v", err)
		}
		closedOrch := &Orchestrator{
			store:   closedStore,
			claimed: map[string]struct{}{},
			retries: map[string]retryEntry{},
			paused:  map[string]pausedEntry{},
		}
		if got := closedOrch.scheduleAutomaticRetryLockedWithResume(workflow, retryIssue, 2, kanban.WorkflowPhaseImplementation, "failure", "retry me again", 1000, nil, ""); got {
			t.Fatal("expected retry scheduling to fail when retry count lookup fails")
		}
	})
}

func TestOrchestratorCoveragePreviewAndSharedCleanupBranches(t *testing.T) {
	t.Run("publish issue preview comment", func(t *testing.T) {
		orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
		enablePhaseWorkflow(t, manager, workspaceRoot)

		issue, err := store.CreateIssue("", "", "Preview publication issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
			t.Fatalf("UpdateIssueState: %v", err)
		}

		workspacePath := filepath.Join(t.TempDir(), "preview-workspace")
		previewDir := filepath.Join(workspacePath, ".maestro", "review-preview")
		if err := os.MkdirAll(previewDir, 0o755); err != nil {
			t.Fatalf("MkdirAll preview dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(previewDir, "preview.mp4"), []byte("preview video"), 0o644); err != nil {
			t.Fatalf("WriteFile preview video: %v", err)
		}
		if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}

		orch.publishIssuePreviewAsync(issue, kanban.WorkflowPhaseDone, &agent.RunResult{Output: "Done pass complete"})
		waitForCondition(t, 5*time.Second, func() bool {
			comments, err := store.ListIssueComments(issue.ID)
			return err == nil && len(comments) == 1 && len(comments[0].Attachments) == 1
		})

		comments, err := store.ListIssueComments(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueComments: %v", err)
		}
		if len(comments) != 1 || len(comments[0].Attachments) != 1 {
			t.Fatalf("expected published preview comment with one attachment, got %#v", comments)
		}
		if !strings.Contains(comments[0].Body, "Automated reviewer preview") || !strings.Contains(comments[0].Body, "Preview file") {
			t.Fatalf("unexpected preview comment body: %q", comments[0].Body)
		}

		if err := store.DeleteIssueComment(issue.ID, comments[0].ID); err != nil {
			t.Fatalf("DeleteIssueComment cleanup: %v", err)
		}
	})

	t.Run("cleanup terminal workspaces in shared mode", func(t *testing.T) {
		store, err := kanban.NewStore(filepath.Join(t.TempDir(), "shared.db"))
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		scopedRepo := t.TempDir()
		project, err := store.CreateProject("Shared cleanup project", "", scopedRepo, "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		issue, err := store.CreateIssue(project.ID, "", "Shared terminal issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
			t.Fatalf("UpdateIssueState: %v", err)
		}

		workspacePath := filepath.Join(t.TempDir(), "shared-workspace")
		if err := os.MkdirAll(workspacePath, 0o755); err != nil {
			t.Fatalf("MkdirAll workspace: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workspacePath, "note.txt"), []byte("workspace"), 0o644); err != nil {
			t.Fatalf("WriteFile workspace note: %v", err)
		}
		if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}

		runner := newRetryTestRunner()
		shared := NewSharedWithExtensions(store, nil, scopedRepo, "")
		shared.runnerFactory = func(*config.Manager) runnerExecutor {
			return runner
		}

		shared.cleanupTerminalWorkspaces(context.Background())
		if got := waitForCleanupCall(t, runner.cleanupCalls, time.Second); got != issue.Identifier {
			t.Fatalf("expected shared cleanup for %s, got %s", issue.Identifier, got)
		}
	})

	t.Run("helper no-op branches", func(t *testing.T) {
		orch, store, _, _ := setupTestOrchestrator(t, "cat")
		issue, err := store.CreateIssue("", "", "Live session no-op", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}

		orch.updateLiveSession(issue.ID, nil)
		orch.updateLiveSession("missing", &agentruntime.Session{SessionID: "ignored"})
		orch.restoreIssueTokenSpend(issue.ID, 0)
		orch.restoreIssueTokenSpend(issue.ID, -1)
	})
}

func (failingCleanupRunner) CleanupWorkspace(context.Context, *kanban.Issue) error {
	return errors.New("cleanup failed")
}

func TestOrchestratorCoverageHelpers(t *testing.T) {
	store := func() *kanban.Store {
		t.Helper()
		s, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}()

	t.Run("issue workspace path", func(t *testing.T) {
		s := store
		issue, err := s.CreateIssue("", "", "Workspace issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		workspacePath := filepath.Join(t.TempDir(), "workspaces", issue.Identifier)
		if _, err := s.CreateWorkspace(issue.ID, workspacePath); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		orch := &Orchestrator{store: s}
		if got := orch.IssueWorkspacePath(issue.Identifier); got != workspacePath {
			t.Fatalf("IssueWorkspacePath = %q, want %q", got, workspacePath)
		}
		if got := orch.IssueWorkspacePath(""); got != "" {
			t.Fatalf("IssueWorkspacePath blank = %q", got)
		}
		if got := orch.IssueWorkspacePath("missing"); got != "" {
			t.Fatalf("IssueWorkspacePath missing = %q", got)
		}
	})

	t.Run("dispatch and recurring helpers", func(t *testing.T) {
		if got := dispatchMode(nil); got != config.DispatchModeParallel {
			t.Fatalf("dispatchMode(nil) = %q", got)
		}
		if got := dispatchMode(&config.Workflow{}); got != config.DispatchModeParallel {
			t.Fatalf("dispatchMode blank = %q", got)
		}
		if got := dispatchMode(&config.Workflow{Config: config.Config{Agent: config.AgentConfig{DispatchMode: config.DispatchModePerProjectSerial}}}); got != config.DispatchModePerProjectSerial {
			t.Fatalf("dispatchMode explicit = %q", got)
		}

		if got := recurringScheduleEventKind(nil, time.Now()); got != "recurring_enqueued" {
			t.Fatalf("recurringScheduleEventKind(nil) = %q", got)
		}
		if got := recurringScheduleEventKind(&kanban.Issue{}, time.Now()); got != "recurring_enqueued" {
			t.Fatalf("recurringScheduleEventKind without next run = %q", got)
		}
		past := time.Now().Add(-2 * time.Minute)
		if got := recurringScheduleEventKind(&kanban.Issue{NextRunAt: &past}, time.Now()); got != "recurring_catch_up_enqueued" {
			t.Fatalf("recurringScheduleEventKind catch-up = %q", got)
		}
		future := time.Now().Add(30 * time.Second)
		if got := recurringScheduleEventKind(&kanban.Issue{NextRunAt: &future}, time.Now()); got != "recurring_enqueued" {
			t.Fatalf("recurringScheduleEventKind future = %q", got)
		}
	})

	t.Run("recurring due and state updates", func(t *testing.T) {
		s := store
		enabled := true
		issue, err := s.CreateIssueWithOptions("", "", "Recurring issue", "", 0, nil, kanban.IssueCreateOptions{
			IssueType: kanban.IssueTypeRecurring,
			Cron:      "0 * * * *",
			Enabled:   &enabled,
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions: %v", err)
		}

		orch := &Orchestrator{store: s}
		base := 24 * time.Hour
		delay := orch.nextWakeDelay(base)
		if delay <= 0 || delay >= base {
			t.Fatalf("nextWakeDelay = %s, want value below base timeout", delay)
		}
		if got := orch.recordRecurringPendingRerun(issue, "manual retry"); !got {
			t.Fatal("expected recurring pending rerun to be recorded")
		}
		if got := orch.recordRecurringPendingRerun(issue, "manual retry"); got {
			t.Fatal("expected duplicate recurring pending rerun to be ignored")
		}

		loaded, err := s.GetIssueByIdentifier(issue.Identifier)
		if err != nil {
			t.Fatalf("GetIssueByIdentifier: %v", err)
		}
		if !loaded.PendingRerun {
			t.Fatal("expected pending rerun flag to persist")
		}

		stateIssue, err := s.CreateIssue("", "", "State issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue state: %v", err)
		}
		orch.updateIssueStatePhase(stateIssue, kanban.StateDone, kanban.WorkflowPhaseReview)
		if stateIssue.State != kanban.StateDone || stateIssue.WorkflowPhase != kanban.WorkflowPhaseReview {
			t.Fatalf("unexpected updated issue state: %#v", stateIssue)
		}

		orch.updateIssueStatePhase(&kanban.Issue{ID: "missing", Identifier: "ISS-missing"}, kanban.StateDone, kanban.WorkflowPhaseDone)
	})
}

func TestOrchestratorCoverageEdgeBranches(t *testing.T) {
	t.Run("runtime resolution guards", func(t *testing.T) {
		store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })

		scopedRepo := t.TempDir()
		shared := NewSharedWithExtensions(store, nil, scopedRepo, "")
		shared.runnerFactory = func(*config.Manager) runnerExecutor { return newRetryTestRunner() }

		if _, err := shared.runtimeForProject(nil); err == nil || err.Error() != "project_not_found" {
			t.Fatalf("expected missing project error, got %v", err)
		}
		if _, err := shared.runtimeForProject(&kanban.Project{ID: "proj-blank"}); err == nil || err.Error() != "project_missing_repo_path" {
			t.Fatalf("expected blank repo path error, got %v", err)
		}

		outOfScope, err := store.CreateProject("Out of scope", "", t.TempDir(), "")
		if err != nil {
			t.Fatalf("CreateProject out-of-scope: %v", err)
		}
		if _, err := shared.runtimeForProject(outOfScope); err == nil || err.Error() != "project_out_of_scope" {
			t.Fatalf("expected out-of-scope error, got %v", err)
		}

		project, err := store.CreateProject("Scoped", "", scopedRepo, "")
		if err != nil {
			t.Fatalf("CreateProject scoped: %v", err)
		}
		runtime, err := shared.runtimeForProject(project)
		if err != nil {
			t.Fatalf("runtimeForProject: %v", err)
		}
		cached, err := shared.runtimeForProject(project)
		if err != nil {
			t.Fatalf("runtimeForProject cached: %v", err)
		}
		if cached != runtime {
			t.Fatal("expected project runtime to be cached")
		}

		emptyScoped := NewSharedWithExtensions(store, nil, "", "")
		if _, err := emptyScoped.runtimeForScopedIssue(); err == nil || err.Error() != "issue_missing_project" {
			t.Fatalf("expected missing scoped issue error, got %v", err)
		}

		issue, err := store.CreateIssue("", "", "Scoped issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue scoped: %v", err)
		}
		resolvedRuntime, workflow, err := shared.runtimeForIssue(issue)
		if err != nil {
			t.Fatalf("runtimeForIssue scoped: %v", err)
		}
		if resolvedRuntime == nil || workflow == nil {
			t.Fatalf("expected scoped runtime resolution, got runtime=%v workflow=%v", resolvedRuntime, workflow)
		}
		if _, _, err := shared.runtimeForIssue(nil); err == nil || err.Error() != "issue_missing_project" {
			t.Fatalf("expected missing issue error, got %v", err)
		}
	})

	t.Run("dispatch and lifecycle helpers", func(t *testing.T) {
		orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
		enablePhaseWorkflow(t, manager, workspaceRoot)

		closedStore, err := kanban.NewStore(filepath.Join(t.TempDir(), "closed.db"))
		if err != nil {
			t.Fatalf("NewStore closed: %v", err)
		}
		if err := closedStore.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got := (&Orchestrator{store: closedStore}).nextWakeDelay(5 * time.Second); got != 5*time.Second {
			t.Fatalf("expected fallback wake delay after store error, got %s", got)
		}

		project, err := store.CreateProject("Dispatch", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject dispatch: %v", err)
		}
		if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
			t.Fatalf("UpdateProjectState dispatch: %v", err)
		}
		issue, err := store.CreateIssue(project.ID, "", "Dispatch issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue dispatch: %v", err)
		}

		if allowed, reason := orch.projectAllowsDispatch(nil, nil); !allowed || reason != "" {
			t.Fatalf("expected nil issue to allow dispatch, got %v %q", allowed, reason)
		}
		if allowed, reason := orch.projectAllowsDispatch(&kanban.Issue{ID: issue.ID}, nil); !allowed || reason != "" {
			t.Fatalf("expected blank project issue to allow dispatch, got %v %q", allowed, reason)
		}
		if allowed, reason := orch.projectAllowsDispatch(issue, &kanban.IssueDispatchState{ProjectExists: false}); allowed || reason != "project_missing" {
			t.Fatalf("expected missing project dispatch to fail, got %v %q", allowed, reason)
		}
		if allowed, reason := orch.projectAllowsDispatch(issue, &kanban.IssueDispatchState{ProjectExists: true, ProjectState: kanban.ProjectStateStopped}); allowed || reason != "project_stopped" {
			t.Fatalf("expected stopped project dispatch to fail, got %v %q", allowed, reason)
		}
		if allowed, reason := orch.projectAllowsDispatch(issue, &kanban.IssueDispatchState{ProjectExists: true, ProjectState: kanban.ProjectStateRunning}); !allowed || reason != "" {
			t.Fatalf("expected running project dispatch to allow, got %v %q", allowed, reason)
		}

		if _, err := orch.issueDispatchState(nil); err == nil || err.Error() != "issue is required" {
			t.Fatalf("expected missing issue dispatch lookup to fail, got %v", err)
		}
		dispatchState, err := orch.issueDispatchState(issue)
		if err != nil {
			t.Fatalf("issueDispatchState: %v", err)
		}
		if dispatchState == nil || !dispatchState.ProjectExists || dispatchState.ProjectState != kanban.ProjectStateRunning {
			t.Fatalf("unexpected dispatch state: %#v", dispatchState)
		}

		if blocked, err := orch.isBlocked(issue, &kanban.IssueDispatchState{HasUnresolvedBlockers: true}); err != nil || !blocked {
			t.Fatalf("expected blocked state to propagate, got blocked=%v err=%v", blocked, err)
		}
		if blocked, err := orch.isBlocked(issue, &kanban.IssueDispatchState{HasUnresolvedBlockers: false}); err != nil || blocked {
			t.Fatalf("expected unblocked state to propagate, got blocked=%v err=%v", blocked, err)
		}

		if paused := pausedLifecycleReset(nil, pausedEntry{}); paused {
			t.Fatal("expected nil issue to not reset paused lifecycle")
		}
		if paused := pausedLifecycleReset(issue, pausedEntry{IssueState: string(issue.State), Phase: string(issue.WorkflowPhase)}); paused {
			t.Fatal("expected matching paused lifecycle to stay active")
		}
		if paused := pausedLifecycleReset(&kanban.Issue{State: kanban.StateDone, WorkflowPhase: kanban.WorkflowPhaseComplete}, pausedEntry{IssueState: string(kanban.StateReady)}); !paused {
			t.Fatal("expected state mismatch to reset paused lifecycle")
		}
	})

	t.Run("recurring and workspace helpers", func(t *testing.T) {
		orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
		enablePhaseWorkflow(t, manager, workspaceRoot)

		falseValue := false
		disabledRecurring, err := store.CreateIssueWithOptions("", "", "Disabled recurring", "", 0, nil, kanban.IssueCreateOptions{
			IssueType: kanban.IssueTypeRecurring,
			Cron:      "0 * * * *",
			Enabled:   &falseValue,
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions disabled: %v", err)
		}
		if err := store.MarkRecurringPendingRerun(disabledRecurring.ID, true); err != nil {
			t.Fatalf("MarkRecurringPendingRerun disabled: %v", err)
		}
		disabledLoaded, err := store.GetIssueByIdentifier(disabledRecurring.Identifier)
		if err != nil {
			t.Fatalf("GetIssueByIdentifier disabled: %v", err)
		}
		disabledLoaded.Enabled = false
		if got := orch.processPendingRecurringRerunIgnoringRunning(disabledLoaded, ""); got {
			t.Fatal("expected disabled recurring issue to be skipped")
		}
		disabledRecurrence, err := store.GetIssueRecurrence(disabledRecurring.ID)
		if err != nil {
			t.Fatalf("GetIssueRecurrence disabled: %v", err)
		}
		if disabledRecurrence == nil || disabledRecurrence.PendingRerun {
			t.Fatalf("expected disabled pending rerun to be cleared, got %#v", disabledRecurrence)
		}

		ignoredRecurring, err := store.CreateIssueWithOptions("", "", "Ignored recurring", "", 0, nil, kanban.IssueCreateOptions{
			IssueType: kanban.IssueTypeRecurring,
			Cron:      "*/10 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions ignored: %v", err)
		}
		if err := store.MarkRecurringPendingRerun(ignoredRecurring.ID, true); err != nil {
			t.Fatalf("MarkRecurringPendingRerun ignored: %v", err)
		}
		ignoredLoaded, err := store.GetIssueByIdentifier(ignoredRecurring.Identifier)
		if err != nil {
			t.Fatalf("GetIssueByIdentifier ignored: %v", err)
		}
		orch.mu.Lock()
		orch.running[ignoredLoaded.ID] = runningEntry{issue: *ignoredLoaded, cancel: func() {}}
		orch.mu.Unlock()
		if got := orch.processPendingRecurringRerunIgnoringRunning(ignoredLoaded, ignoredLoaded.ID); !got {
			t.Fatal("expected running recurring issue to be enqueued when ignored")
		}

		project, err := store.CreateProject("Recurring project", "", filepath.Dir(manager.Path()), manager.Path())
		if err != nil {
			t.Fatalf("CreateProject recurring: %v", err)
		}
		if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
			t.Fatalf("UpdateProjectState recurring: %v", err)
		}
		pendingRecurring, err := store.CreateIssueWithOptions(project.ID, "", "Pending recurring", "", 0, nil, kanban.IssueCreateOptions{
			IssueType: kanban.IssueTypeRecurring,
			Cron:      "*/15 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions pending: %v", err)
		}
		if err := store.MarkRecurringPendingRerun(pendingRecurring.ID, true); err != nil {
			t.Fatalf("MarkRecurringPendingRerun pending: %v", err)
		}
		orch.processPendingRecurringReruns(context.Background())
		pendingLoaded, err := store.GetIssueByIdentifier(pendingRecurring.Identifier)
		if err != nil {
			t.Fatalf("GetIssueByIdentifier pending: %v", err)
		}
		if pendingLoaded.PendingRerun {
			t.Fatal("expected pending recurring rerun to be processed")
		}

		if result := orch.RunRecurringIssueNow(context.Background(), "missing"); result["status"] != "not_found" {
			t.Fatalf("expected missing recurring issue to report not_found, got %#v", result)
		}
		plainIssue, err := store.CreateIssue("", "", "Plain issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue plain: %v", err)
		}
		if result := orch.RunRecurringIssueNow(context.Background(), plainIssue.Identifier); result["status"] != "not_recurring" {
			t.Fatalf("expected plain issue to report not_recurring, got %#v", result)
		}

		issue, err := store.CreateIssue("", "", "Cleanup issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue cleanup: %v", err)
		}
		orch.cleanupTerminalIssueWorkspace(context.Background(), nil, issue, 1, "ignored")
		orch.cleanupTerminalIssueWorkspace(context.Background(), failingCleanupRunner{}, issue, 1, "terminal_state")
		successRunner := newRetryTestRunner()
		orch.cleanupTerminalIssueWorkspace(context.Background(), successRunner, issue, 1, "terminal_state")
		if got := waitForCleanupCall(t, successRunner.cleanupCalls, time.Second); got != issue.Identifier {
			t.Fatalf("expected cleanup callback for %s, got %s", issue.Identifier, got)
		}

		previewDir := t.TempDir()
		missingPath := filepath.Join(previewDir, "missing.png")
		previewPath := filepath.Join(previewDir, "preview.png")
		if err := os.WriteFile(previewPath, []byte("preview"), 0o644); err != nil {
			t.Fatalf("WriteFile preview: %v", err)
		}

		successTmp := t.TempDir()
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "not-a-directory"))
		if _, _, err := stageReviewPreviewAttachment(""); err == nil {
			t.Fatal("expected blank preview path to fail")
		}
		if _, _, err := stageReviewPreviewAttachment(missingPath); err == nil {
			t.Fatal("expected missing preview file to fail")
		}
		t.Setenv("TMPDIR", successTmp)
		staged, cleanup, err := stageReviewPreviewAttachment(previewPath)
		if err != nil {
			t.Fatalf("stageReviewPreviewAttachment: %v", err)
		}
		if staged == "" {
			t.Fatal("expected staged preview path")
		}
		if cleanup != nil {
			cleanup()
		}
	})

	t.Run("pure helper branches", func(t *testing.T) {
		issueWithRevision := &kanban.Issue{
			PendingPlanRevisionMarkdown:    "Revise the rollout",
			PendingPlanRevisionRequestedAt: func() *time.Time { v := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC); return &v }(),
		}
		if issueHasPendingPlanRevision(nil) {
			t.Fatal("expected nil issue to not have a pending plan revision")
		}
		if !issueHasPendingPlanRevision(issueWithRevision) {
			t.Fatal("expected revision markdown with request timestamp to be detected")
		}

		if got := payloadString(nil, "value"); got != "" {
			t.Fatalf("expected nil payload string to be empty, got %q", got)
		}
		if got := payloadString(map[string]interface{}{"value": "  trimmed  "}, "value"); got != "trimmed" {
			t.Fatalf("expected payload string to be trimmed, got %q", got)
		}
		if got := payloadTime(nil, "value"); !got.IsZero() {
			t.Fatalf("expected nil payload time to be zero, got %v", got)
		}
		if got := payloadTime(map[string]interface{}{"value": "not-a-time"}, "value"); !got.IsZero() {
			t.Fatalf("expected invalid payload time to be zero, got %v", got)
		}
		pausedAt := time.Date(2026, 3, 18, 12, 15, 0, 0, time.UTC)
		paused := pausedEntryFromRuntimeEvent(kanban.RuntimeEvent{
			Attempt: 3,
			Phase:   "implementation",
			Error:   "stall_timeout",
			TS:      pausedAt,
			Kind:    "retry_paused",
			Payload: map[string]interface{}{
				"issue_state":          " ready ",
				"paused_at":            pausedAt.Format(time.RFC3339),
				"consecutive_failures": 4,
				"pause_threshold":      5,
			},
		})
		if paused.PausedAt != pausedAt || paused.ConsecutiveFailures != 4 || paused.PauseThreshold != 5 || paused.IssueState != "ready" {
			t.Fatalf("unexpected paused entry: %#v", paused)
		}
		fallbackPausedAt := time.Date(2026, 3, 18, 12, 20, 0, 0, time.UTC)
		fallbackPaused := pausedEntryFromRuntimeEvent(kanban.RuntimeEvent{
			Attempt: 1,
			Phase:   "review",
			Error:   "stall_timeout",
			TS:      fallbackPausedAt,
			Kind:    "retry_paused",
			Payload: map[string]interface{}{
				"issue_state":          "done",
				"consecutive_failures": 1,
				"pause_threshold":      3,
			},
		})
		if fallbackPaused.PausedAt != fallbackPausedAt || fallbackPaused.IssueState != "done" {
			t.Fatalf("expected paused event to fall back to event timestamp, got %#v", fallbackPaused)
		}

		retryEvents := []kanban.RuntimeEvent{
			{Kind: "run_completed", Payload: map[string]interface{}{"next_retry": 1}},
			{Kind: "retry_scheduled", DelayType: "failure"},
			{Kind: "retry_scheduled", DelayType: "continuation"},
		}
		if got := automaticRetryCount(retryEvents); got != 2 {
			t.Fatalf("unexpected automatic retry count: %d", got)
		}
		streakEvents := []kanban.RuntimeEvent{
			{Kind: "retry_scheduled", DelayType: "failure"},
			{Kind: "run_interrupted"},
			{Kind: "run_failed", Error: "stall_timeout"},
		}
		if got := interruptedFailureStreak(streakEvents); got != 2 {
			t.Fatalf("unexpected interrupted failure streak: %d", got)
		}

		orch := &Orchestrator{}
		orch.runWG.Add(1)
		if got := orch.waitForActiveRuns(time.Millisecond); got {
			t.Fatal("expected waitForActiveRuns to time out while the waitgroup is held")
		}
		orch.runWG.Done()
		if got := orch.waitForActiveRuns(0); !got {
			t.Fatal("expected waitForActiveRuns(0) to succeed once released")
		}

		workflow := &config.Workflow{}
		workflow.Config.Agent.MaxConcurrentAgents = 1
		orch.mu.Lock()
		orch.running = map[string]runningEntry{
			"running-1": {issue: kanban.Issue{ProjectID: "proj-1"}},
		}
		orch.mu.Unlock()
		if got := orch.hasProjectCapacity(workflow, "proj-1"); got {
			t.Fatal("expected project capacity to be exhausted")
		}
		workflow.Config.Agent.MaxConcurrentAgents = 2
		if got := orch.hasProjectCapacity(workflow, "proj-1"); !got {
			t.Fatal("expected project capacity to allow another run")
		}

		orch.mu.Lock()
		orch.retries = map[string]retryEntry{
			"retry-1": {DueAt: time.Now().UTC().Add(-time.Minute)},
		}
		orch.mu.Unlock()
		if entry, ok := orch.dueRetryEntry("retry-1", time.Now().UTC()); !ok || entry.DueAt.IsZero() {
			t.Fatalf("expected due retry entry to be returned, got entry=%#v ok=%v", entry, ok)
		}
		if entry, ok := orch.dueRetryEntry("missing", time.Now().UTC()); ok || entry.DueAt != (time.Time{}) {
			t.Fatalf("expected missing retry entry to be ignored, got entry=%#v ok=%v", entry, ok)
		}

		orch.mu.Lock()
		orch.pendingInteractions = map[string]pendingInteractionEntry{
			"interaction-1": {interaction: agentruntime.PendingInteraction{ID: "interaction-1", IssueID: "ISS-1"}},
		}
		orch.pendingInteractionOrder = []string{"interaction-1"}
		orch.liveSessions = map[string]*agentruntime.Session{
			"issue-1": {
				SessionID:       "session-1",
				ThreadID:        "thread-1",
				LastMessage:     "Hello",
				IssueID:         "issue-1",
				IssueIdentifier: "ISS-1",
			},
		}
		orch.running = map[string]runningEntry{
			"issue-1": {issue: kanban.Issue{Identifier: "ISS-1"}},
		}
		pending := orch.currentPendingInteractionLocked()
		liveSessions := orch.copyLiveSessionsLocked()
		orch.mu.Unlock()
		if pending == nil || pending.ID != "interaction-1" {
			t.Fatalf("unexpected current pending interaction: %#v", pending)
		}
		if session, ok := liveSessions["ISS-1"]; !ok || session == nil || session.SessionID != "session-1" {
			t.Fatalf("unexpected copied live sessions: %#v", liveSessions)
		}
		if got := filterPendingInteractionOrder([]string{"a", "b", "c"}, "b"); strings.Join(got, ",") != "a,c" {
			t.Fatalf("unexpected filtered interaction order: %#v", got)
		}

		if got := issuePreviewSummary(nil); got != "" {
			t.Fatalf("expected nil preview summary to be empty, got %q", got)
		}
		if got := issuePreviewSummary(&agent.RunResult{Output: " output from run "}); got != "output from run" {
			t.Fatalf("unexpected preview summary output: %q", got)
		}
		if got := issuePreviewSummary(&agent.RunResult{AppSession: &agentruntime.Session{LastMessage: " message "}}); got != "message" {
			t.Fatalf("unexpected preview summary message: %q", got)
		}
	})

	t.Run("session persistence, interactions, and retries", func(t *testing.T) {
		orch, store, _, _ := setupTestOrchestrator(t, "cat")
		issue, err := store.CreateIssue("", "", "Coverage issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}

		if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
			t.Fatalf("UpdateIssueState: %v", err)
		}

		if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
			IssueID:    issue.ID,
			Identifier: issue.Identifier,
			Phase:      string(kanban.WorkflowPhaseImplementation),
			Attempt:    1,
			RunKind:    "run_started",
			UpdatedAt:  time.Now().UTC(),
			AppSession: agentruntime.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "session-1",
				ThreadID:        "thread-1",
				ProcessID:       1234,
				LastMessage:     "initial message",
			},
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession seed: %v", err)
		}

		if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
			IssueID:    issue.ID,
			Identifier: issue.Identifier,
			Phase:      string(kanban.WorkflowPhaseImplementation),
			Attempt:    1,
			RunKind:    "run_started",
			UpdatedAt:  time.Now().UTC(),
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession seed reuse: %v", err)
		}

		orch.persistExecutionSession(nil, kanban.WorkflowPhaseImplementation, 1, "run_started", "", false, "", nil)
		orch.persistExecutionSession(issue, kanban.WorkflowPhaseImplementation, 1, "run_started", "", false, "", &agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "session-2",
			ThreadID:        "thread-2",
			ProcessID:       4321,
			LastMessage:     "latest message",
		})
		snapshot, err := store.GetIssueExecutionSession(issue.ID)
		if err != nil {
			t.Fatalf("GetIssueExecutionSession after persist: %v", err)
		}
		if snapshot.AppSession.ProcessID != 4321 || snapshot.AppSession.SessionID != "session-2" {
			t.Fatalf("expected persisted session to be stored, got %#v", snapshot.AppSession)
		}

		orch.markAppServerRetired(issue.ID)
		orch.persistExecutionSession(issue, kanban.WorkflowPhaseImplementation, 2, "run_started", "", false, "", &agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "session-3",
			ThreadID:        "thread-3",
			ProcessID:       9999,
		})
		snapshot, err = store.GetIssueExecutionSession(issue.ID)
		if err != nil {
			t.Fatalf("GetIssueExecutionSession after retire: %v", err)
		}
		if snapshot.AppSession.ProcessID != 0 || snapshot.ResumeEligible {
			t.Fatalf("expected retired app server to zero process persistence, got %#v", snapshot.AppSession)
		}
		orch.unmarkAppServerRetired(issue.ID)

		orch.updateIssueActivity("missing", agentruntime.ActivityEvent{Type: "turn.started"})
		orch.mu.Lock()
		orch.running[issue.ID] = runningEntry{
			issue:   *issue,
			phase:   kanban.WorkflowPhaseImplementation,
			attempt: 2,
			cancel:  func() {},
		}
		orch.liveSessions[issue.ID] = &agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			ThreadID:        "thread-activity",
			LastMessage:     "most recent message",
			LastTimestamp:   time.Date(2026, 3, 18, 13, 0, 0, 0, time.UTC),
		}
		orch.mu.Unlock()
		orch.updateIssueActivity(issue.ID, agentruntime.ActivityEvent{
			Type:     "turn.started",
			ThreadID: "thread-activity",
			TurnID:   "turn-activity",
		})
		entries, err := store.ListIssueActivityEntries(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueActivityEntries after update: %v", err)
		}
		if len(entries) == 0 {
			t.Fatal("expected activity event to be persisted for running issue")
		}

		orch.mu.Lock()
		orch.pendingInteractions = map[string]pendingInteractionEntry{}
		orch.pendingInteractionOrder = nil
		orch.mu.Unlock()
		orch.registerPendingInteraction("", nil, nil)
		interaction := &agentruntime.PendingInteraction{
			ID:      "interaction-1",
			Kind:    agentruntime.PendingInteractionKindUserInput,
			IssueID: issue.ID,
		}
		orch.registerPendingInteraction(issue.ID, interaction, nil)
		orch.mu.RLock()
		registered, ok := orch.pendingInteractions[interaction.ID]
		orch.mu.RUnlock()
		if !ok {
			t.Fatal("expected pending interaction to be registered")
		}
		if registered.interaction.IssueID != issue.ID || registered.interaction.IssueIdentifier != issue.Identifier || registered.interaction.Phase != string(kanban.WorkflowPhaseImplementation) {
			t.Fatalf("expected pending interaction to be enriched, got %#v", registered.interaction)
		}
		if registered.interaction.LastActivity != "most recent message" || registered.interaction.LastActivityAt == nil {
			t.Fatalf("expected pending interaction to inherit latest session details, got %#v", registered.interaction)
		}

		workflow := &config.Workflow{Config: config.DefaultConfig()}
		workflow.Config.Agent.MaxAutomaticRetries = 1
		retryIssue, err := store.CreateIssue("", "", "Retry issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue retry: %v", err)
		}
		if got := orch.scheduleAutomaticRetryLockedWithResume(workflow, retryIssue, 1, kanban.WorkflowPhaseImplementation, "failure", "retry me", 1000, nil, ""); !got {
			t.Fatal("expected retry scheduling to succeed before limit")
		}
		if _, ok := orch.retries[retryIssue.ID]; !ok {
			t.Fatal("expected retry entry to be recorded")
		}
		if got := orch.scheduleAutomaticRetryLockedWithResume(workflow, retryIssue, 2, kanban.WorkflowPhaseImplementation, "failure", "retry me again", 1000, nil, ""); got {
			t.Fatal("expected retry scheduling to pause after reaching the limit")
		}
		orch.mu.RLock()
		_, retrying := orch.retries[retryIssue.ID]
		pausedEntry, paused := orch.paused[retryIssue.ID]
		orch.mu.RUnlock()
		if retrying || !paused || pausedEntry.Error != "retry_limit_reached" {
			t.Fatalf("expected retry limit to pause the issue, got retrying=%v paused=%v entry=%#v", retrying, paused, pausedEntry)
		}
	})

	t.Run("interruption and plan approval helpers", func(t *testing.T) {
		store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatalf("kanban.NewStore: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		orch := &Orchestrator{
			store:               store,
			claimed:             map[string]struct{}{},
			paused:              map[string]pausedEntry{},
			retries:             map[string]retryEntry{},
			sessionWrites:       map[string]sessionPersistenceState{},
			liveSessions:        map[string]*agentruntime.Session{},
			pendingInteractions: map[string]pendingInteractionEntry{},
		}

		interruptedIssue, err := store.CreateIssue("", "", "Interrupted issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue interrupted: %v", err)
		}
		interrupted := &kanban.Issue{
			ID:            interruptedIssue.ID,
			Identifier:    interruptedIssue.Identifier,
			State:         kanban.StateReady,
			WorkflowPhase: kanban.WorkflowPhaseImplementation,
		}
		next, paused := orch.handleInterruptedRunLocked(interrupted, kanban.WorkflowPhaseImplementation, 1, &agentruntime.Session{ThreadID: "thread-1"}, "workspace_bootstrap_failed", "", false)
		if next != 2 || !paused {
			t.Fatalf("expected workspace bootstrap interruption to pause retries, got next=%d paused=%v", next, paused)
		}
		orch.mu.RLock()
		pausedEntry, ok := orch.paused[interruptedIssue.ID]
		orch.mu.RUnlock()
		if !ok || pausedEntry.Error != "workspace_bootstrap_failed" {
			t.Fatalf("expected paused state to be recorded, got %#v", pausedEntry)
		}

		retryIssue, err := store.CreateIssue("", "", "Retry issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue retry: %v", err)
		}
		retryRun := &kanban.Issue{
			ID:            retryIssue.ID,
			Identifier:    retryIssue.Identifier,
			State:         kanban.StateReady,
			WorkflowPhase: kanban.WorkflowPhaseImplementation,
		}
		next, paused = orch.handleInterruptedRunLocked(retryRun, kanban.WorkflowPhaseImplementation, 1, &agentruntime.Session{ThreadID: "thread-2"}, "stall_timeout", "", false)
		if next != 2 || paused {
			t.Fatalf("expected interrupted retry to schedule instead of pausing, got next=%d paused=%v", next, paused)
		}

		planIssue, err := store.CreateIssue("", "", "Plan issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue plan: %v", err)
		}
		requestedAt := time.Date(2026, 3, 18, 12, 30, 0, 0, time.UTC)
		if err := store.SetIssuePendingPlanApproval(planIssue.ID, "Approve the plan", requestedAt); err != nil {
			t.Fatalf("SetIssuePendingPlanApproval: %v", err)
		}
		loadedPlan, err := store.GetIssue(planIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue plan: %v", err)
		}
		retry, ok := orch.planApprovalRetryEntry(loadedPlan)
		if !ok || retry.Attempt != 1 || retry.DelayType != "manual" || retry.ResumeThreadID != "" {
			t.Fatalf("expected plan approval retry entry without session, got %#v ok=%v", retry, ok)
		}

		session := kanban.ExecutionSessionSnapshot{
			IssueID:    planIssue.ID,
			Identifier: planIssue.Identifier,
			Phase:      "invalid-phase",
			Attempt:    0,
			RunKind:    "retry_paused",
			StopReason: planApprovalStopReason,
			UpdatedAt:  time.Now().UTC(),
			AppSession: agentruntime.Session{
				ThreadID: "thread-approval",
			},
		}
		if err := store.UpsertIssueExecutionSession(session); err != nil {
			t.Fatalf("UpsertIssueExecutionSession: %v", err)
		}
		retry, ok = orch.planApprovalRetryEntry(loadedPlan)
		if !ok || retry.Attempt != 1 || retry.ResumeThreadID != "thread-approval" || retry.Error != planApprovalStopReason {
			t.Fatalf("expected plan approval retry entry from snapshot, got %#v ok=%v", retry, ok)
		}
	})

	t.Run("maintenance and helper branches", func(t *testing.T) {
		orch, store, _, _ := setupTestOrchestrator(t, "cat")

		recent := time.Now().UTC()
		orch.mu.Lock()
		orch.lastMaintenanceAt = recent
		orch.lastCheckpointAt = time.Time{}
		orch.lastCheckpointResult = ""
		orch.mu.Unlock()
		orch.runMaintenanceIfDue()
		orch.mu.RLock()
		if !orch.lastMaintenanceAt.Equal(recent) || !orch.lastCheckpointAt.IsZero() || orch.lastCheckpointResult != "" {
			orch.mu.RUnlock()
			t.Fatalf("expected recent maintenance call to be a no-op, got lastMaintenanceAt=%v lastCheckpointAt=%v result=%q", orch.lastMaintenanceAt, orch.lastCheckpointAt, orch.lastCheckpointResult)
		}
		orch.mu.RUnlock()

		closedStore, err := kanban.NewStore(filepath.Join(t.TempDir(), "closed.db"))
		if err != nil {
			t.Fatalf("NewStore closed: %v", err)
		}
		if err := closedStore.Close(); err != nil {
			t.Fatalf("Close closed store: %v", err)
		}
		(&Orchestrator{store: closedStore}).runMaintenanceIfDue()

		if orch.appServerRetired(" ") {
			t.Fatal("expected blank issue id to never be retired")
		}
		orch.markAppServerRetired(" ")
		orch.unmarkAppServerRetired(" ")

		issue, err := store.CreateIssue("", "", "Helper branch issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue helper: %v", err)
		}
		orch.markAppServerRetired(issue.ID)
		if !orch.appServerRetired(issue.ID) {
			t.Fatal("expected issue to be marked retired")
		}
		orch.unmarkAppServerRetired(issue.ID)
		if orch.appServerRetired(issue.ID) {
			t.Fatal("expected issue retirement mark to be cleared")
		}

		noSessionIssue, err := store.CreateIssue("", "", "No session cleanup issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue no session: %v", err)
		}
		orch.cleanupTerminalAppServerProcess(nil)
		orch.cleanupTerminalAppServerProcess(noSessionIssue)

		cleanupIssue, err := store.CreateIssue("", "", "Cleanup failure issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue cleanup: %v", err)
		}
		if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
			IssueID:        cleanupIssue.ID,
			Identifier:     cleanupIssue.Identifier,
			Phase:          string(kanban.WorkflowPhaseImplementation),
			Attempt:        1,
			RunKind:        "run_started",
			ResumeEligible: true,
			UpdatedAt:      time.Now().UTC(),
			AppSession: agentruntime.Session{
				IssueID:         cleanupIssue.ID,
				IssueIdentifier: cleanupIssue.Identifier,
				SessionID:       "session-cleanup",
				ThreadID:        "thread-cleanup",
				ProcessID:       4242,
			},
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession cleanup: %v", err)
		}
		orch.liveSessions[cleanupIssue.ID] = &agentruntime.Session{
			IssueID:         cleanupIssue.ID,
			IssueIdentifier: cleanupIssue.Identifier,
			ProcessID:       4242,
		}
		orch.testHooks.cleanupLingeringAppServerProcess = func(int) error {
			return errors.New("cleanup failed")
		}
		orch.cleanupTerminalAppServerProcess(cleanupIssue)
		if orch.appServerRetired(cleanupIssue.ID) {
			t.Fatal("expected cleanup failure to unmark app-server retirement")
		}
		cleanupSnapshot, err := store.GetIssueExecutionSession(cleanupIssue.ID)
		if err != nil {
			t.Fatalf("GetIssueExecutionSession cleanup: %v", err)
		}
		if cleanupSnapshot.AppSession.ProcessID != 4242 || !cleanupSnapshot.ResumeEligible {
			t.Fatalf("expected cleanup failure to leave execution session unchanged, got %#v", cleanupSnapshot.AppSession)
		}

		interactionIssue, err := store.CreateIssue("", "", "Interaction helper issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue interaction: %v", err)
		}
		orch.mu.Lock()
		orch.pendingInteractions = map[string]pendingInteractionEntry{
			"present": {interaction: agentruntime.PendingInteraction{ID: "present", IssueID: interactionIssue.ID}},
		}
		orch.pendingInteractionOrder = []string{"missing"}
		if got := orch.currentPendingInteractionLocked(); got != nil {
			orch.mu.Unlock()
			t.Fatalf("expected missing pending interaction order to return nil, got %#v", got)
		}
		orch.pendingInteractionOrder = []string{"missing", "present"}
		current := orch.currentPendingInteractionLocked()
		orch.running = map[string]runningEntry{
			interactionIssue.ID: {issue: *interactionIssue, phase: kanban.WorkflowPhaseImplementation, attempt: 1, cancel: func() {}},
			"missing-run":       {issue: kanban.Issue{Identifier: "ISS-missing"}, cancel: func() {}},
		}
		orch.liveSessions = map[string]*agentruntime.Session{
			interactionIssue.ID: {IssueID: interactionIssue.ID, IssueIdentifier: interactionIssue.Identifier, SessionID: "session-1"},
		}
		copied := orch.copyLiveSessionsLocked()
		orch.mu.Unlock()
		if current == nil || current.ID != "present" {
			t.Fatalf("expected current pending interaction to skip missing entries, got %#v", current)
		}
		if got := filterPendingInteractionOrder(nil, "missing"); got != nil {
			t.Fatalf("expected nil interaction order to stay nil, got %#v", got)
		}
		if got := filterPendingInteractionOrder([]string{"a", "b"}, "missing"); strings.Join(got, ",") != "a,b" {
			t.Fatalf("expected no-match filter to preserve order, got %#v", got)
		}
		if got := filterPendingInteractionOrder([]string{"a", "b"}, "a"); strings.Join(got, ",") != "b" {
			t.Fatalf("expected matching filter to remove the requested id, got %#v", got)
		}
		if len(copied) != 1 || copied[interactionIssue.Identifier] == nil {
			t.Fatalf("expected only the live session with a matching running entry to be copied, got %#v", copied)
		}

		retryIssue, err := store.CreateIssue("", "", "Retry counter issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue retry: %v", err)
		}
		if err := store.AppendRuntimeEvent("retry_scheduled", map[string]interface{}{
			"issue_id":     retryIssue.ID,
			"identifier":   retryIssue.Identifier,
			"delay_type":   "failure",
			"total_tokens": 1,
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent retry_scheduled: %v", err)
		}
		if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
			"issue_id":   retryIssue.ID,
			"identifier": retryIssue.Identifier,
			"next_retry": 1,
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent run_completed: %v", err)
		}
		if count, err := orch.automaticRetryCountLocked(retryIssue.ID); err != nil || count != 1 {
			t.Fatalf("expected automatic retry count to be 1, got count=%d err=%v", count, err)
		}
		if _, err := (&Orchestrator{store: closedStore}).automaticRetryCountLocked(retryIssue.ID); err == nil {
			t.Fatal("expected automaticRetryCountLocked on closed store to fail")
		}

		streakIssue, err := store.CreateIssue("", "", "Interrupted streak issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue streak: %v", err)
		}
		if err := store.AppendRuntimeEvent("run_interrupted", map[string]interface{}{
			"issue_id":   streakIssue.ID,
			"identifier": streakIssue.Identifier,
			"error":      "run_interrupted",
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent run_interrupted: %v", err)
		}
		if err := store.AppendRuntimeEvent("run_failed", map[string]interface{}{
			"issue_id":   streakIssue.ID,
			"identifier": streakIssue.Identifier,
			"error":      "stall_timeout",
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent run_failed: %v", err)
		}
		if streak, err := orch.interruptedFailureStreak(streakIssue.ID, 20); err != nil || streak != 2 {
			t.Fatalf("expected interrupted failure streak to be 2, got streak=%d err=%v", streak, err)
		}
		if _, err := (&Orchestrator{store: closedStore}).interruptedFailureStreak(streakIssue.ID, 20); err == nil {
			t.Fatal("expected interruptedFailureStreak on closed store to fail")
		}

		retryNowIssue, err := store.CreateIssue("", "", "Manual retry branch", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue retry-now: %v", err)
		}
		orch.mu.Lock()
		orch.retries[retryNowIssue.ID] = retryEntry{
			Attempt:   1,
			Phase:     string(kanban.WorkflowPhaseImplementation),
			DueAt:     time.Now().UTC().Add(time.Minute),
			DelayType: "failure",
		}
		orch.mu.Unlock()
		if result := orch.RetryIssueNow(context.Background(), retryNowIssue.Identifier); result["status"] != "queued_now" {
			t.Fatalf("expected retry map branch to queue immediately, got %#v", result)
		}

		if retry, ok := orch.planApprovalRetryEntry(nil); ok || retry != (retryEntry{}) {
			t.Fatalf("expected nil issue to not produce a plan approval retry, got %#v ok=%v", retry, ok)
		}
		if retry, ok := orch.planApprovalRetryEntry(&kanban.Issue{ID: "plain", State: kanban.StateReady, WorkflowPhase: kanban.WorkflowPhaseImplementation}); ok || retry != (retryEntry{}) {
			t.Fatalf("expected issue without pending approval to skip retry entry, got %#v ok=%v", retry, ok)
		}
	})

	t.Run("persistence fallbacks and reconciliation guards", func(t *testing.T) {
		orch, store, _, _ := setupTestOrchestrator(t, "cat")
		now := time.Now().UTC()

		existingSessionIssue, err := store.CreateIssue("", "", "Existing session issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue existing session: %v", err)
		}
		if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
			IssueID:    existingSessionIssue.ID,
			Identifier: existingSessionIssue.Identifier,
			Phase:      string(kanban.WorkflowPhaseImplementation),
			Attempt:    1,
			RunKind:    "run_started",
			UpdatedAt:  now,
			AppSession: agentruntime.Session{
				IssueID:         existingSessionIssue.ID,
				IssueIdentifier: existingSessionIssue.Identifier,
				SessionID:       "existing-session",
				ThreadID:        "thread-existing",
				LastEvent:       "turn.started",
				LastTimestamp:   now,
			},
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession existing: %v", err)
		}
		orch.persistExecutionSession(existingSessionIssue, kanban.WorkflowPhaseImplementation, 2, "run_started", "", false, "", nil)
		snapshot, err := store.GetIssueExecutionSession(existingSessionIssue.ID)
		if err != nil {
			t.Fatalf("GetIssueExecutionSession existing: %v", err)
		}
		if snapshot.AppSession.SessionID != "existing-session" {
			t.Fatalf("expected nil session persist to reuse stored session, got %#v", snapshot.AppSession)
		}

		if got := orch.shouldPersistLiveSessionLocked(existingSessionIssue.ID, &agentruntime.Session{
			SessionID:     "existing-session",
			LastEvent:     "turn.started",
			LastTimestamp: now,
		}); got {
			t.Fatal("expected identical live session state to skip persistence")
		}
		if got := orch.shouldPersistLiveSessionLocked(existingSessionIssue.ID, &agentruntime.Session{
			SessionID:     "different-session",
			LastEvent:     "turn.started",
			LastTimestamp: now,
		}); !got {
			t.Fatal("expected session id changes to trigger persistence")
		}
		if got := orch.shouldPersistLiveSessionLocked(existingSessionIssue.ID, &agentruntime.Session{
			SessionID:      "existing-session",
			LastEvent:      "turn.completed",
			LastTimestamp:  now,
			Terminal:       true,
			TerminalReason: "done",
		}); !got {
			t.Fatal("expected terminal state change to trigger persistence")
		}

		intervalIssue, err := store.CreateIssue("", "", "Interval issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue interval: %v", err)
		}
		orch.sessionWriteMu.Lock()
		orch.sessionWrites[intervalIssue.ID] = sessionPersistenceState{
			LastPersistedAt: now.Add(-3 * liveSessionPersistInterval),
			SessionID:       "interval-session",
			LastEvent:       "turn.started",
			LastTimestamp:   now.Add(-time.Minute),
		}
		orch.sessionWriteMu.Unlock()
		if got := orch.shouldPersistLiveSessionLocked(intervalIssue.ID, &agentruntime.Session{
			SessionID:     "interval-session",
			LastEvent:     "turn.completed",
			LastTimestamp: now,
		}); !got {
			t.Fatal("expected stale live session state to trigger persistence")
		}

		closedStore, err := kanban.NewStore(filepath.Join(t.TempDir(), "closed.db"))
		if err != nil {
			t.Fatalf("NewStore closed: %v", err)
		}
		if err := closedStore.Close(); err != nil {
			t.Fatalf("Close closed store: %v", err)
		}
		closedOrch := &Orchestrator{
			store:                  closedStore,
			claimed:                map[string]struct{}{},
			paused:                 map[string]pausedEntry{},
			retries:                map[string]retryEntry{},
			running:                map[string]runningEntry{},
			liveSessions:           map[string]*agentruntime.Session{},
			sessionWrites:          map[string]sessionPersistenceState{},
			tokenSpends:            map[string]issueTokenSpendState{},
			retiredAppServerIssues: map[string]struct{}{},
		}

		closedOrch.reconcileWithProviderSync(context.Background(), false)
		closedOrch.processPendingRecurringReruns(context.Background())
		closedOrch.processDueRecurringIssues(context.Background())

		closedOrch.persistExecutionSession(existingSessionIssue, kanban.WorkflowPhaseImplementation, 3, "run_started", "", false, "", &agentruntime.Session{
			IssueID:         existingSessionIssue.ID,
			IssueIdentifier: existingSessionIssue.Identifier,
			SessionID:       "closed-session",
			ThreadID:        "thread-closed",
			LastEvent:       "turn.started",
			LastTimestamp:   now,
		})

		closedOrch.tokenSpendMu.Lock()
		closedOrch.tokenSpends[existingSessionIssue.ID] = issueTokenSpendState{
			PendingDelta: 5,
			PendingSince: now.Add(-time.Hour),
		}
		closedOrch.tokenSpendMu.Unlock()
		if got := closedOrch.flushIssueTokenSpend(existingSessionIssue.ID, true); got {
			t.Fatal("expected flushIssueTokenSpend to fail against closed store")
		}
		closedOrch.tokenSpendMu.Lock()
		restoredSpend := closedOrch.tokenSpends[existingSessionIssue.ID]
		closedOrch.tokenSpendMu.Unlock()
		if restoredSpend.PendingDelta != 5 {
			t.Fatalf("expected failed flush to restore pending tokens, got %#v", restoredSpend)
		}

		recurringIssue := &kanban.Issue{
			ID:         "recurring-issue",
			Identifier: "ISS-recurring",
			IssueType:  kanban.IssueTypeRecurring,
			Cron:       "not a cron",
			Enabled:    true,
		}
		if got := closedOrch.recordRecurringPendingRerun(recurringIssue, "manual retry"); got {
			t.Fatal("expected closed store recurring rerun recording to fail")
		}
		if got := closedOrch.enqueueRecurringIssue(recurringIssue, "recurring_catch_up_enqueued", false); got {
			t.Fatal("expected invalid recurring cron to fail enqueueing")
		}
	})
}
