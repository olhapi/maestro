package orchestrator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

func TestProjectRunAndStopControls(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	project, err := store.CreateProject("Scoped", "", filepath.Join(t.TempDir(), "repo"), "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Project issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	otherIssue, err := store.CreateIssue("", "", "Other issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}

	cancelled := false
	otherCancelled := false
	orch.mu.Lock()
	orch.running[issue.ID] = runningEntry{
		cancel:    func() { cancelled = true },
		issue:     *issue,
		phase:     kanban.WorkflowPhaseImplementation,
		attempt:   1,
		startedAt: time.Now().UTC(),
	}
	orch.running[otherIssue.ID] = runningEntry{
		cancel:    func() { otherCancelled = true },
		issue:     *otherIssue,
		phase:     kanban.WorkflowPhaseImplementation,
		attempt:   1,
		startedAt: time.Now().UTC(),
	}
	orch.mu.Unlock()

	refresh := orch.RequestProjectRefresh(project.ID)
	if refresh["status"] != "accepted" || refresh["state"] != string(kanban.ProjectStateRunning) {
		t.Fatalf("unexpected project refresh payload: %#v", refresh)
	}
	reloaded, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject after refresh: %v", err)
	}
	if reloaded.State != kanban.ProjectStateRunning {
		t.Fatalf("expected running project state, got %#v", reloaded.State)
	}

	stop := orch.StopProjectRuns(project.ID)
	if stop["status"] != "stopped" || stop["stopped_runs"] != 1 || stop["state"] != string(kanban.ProjectStateStopped) {
		t.Fatalf("unexpected project stop payload: %#v", stop)
	}
	reloaded, err = store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject after stop: %v", err)
	}
	if reloaded.State != kanban.ProjectStateStopped {
		t.Fatalf("expected stopped project state, got %#v", reloaded.State)
	}
	if !cancelled {
		t.Fatal("expected project run to be cancelled")
	}
	if otherCancelled {
		t.Fatal("expected non-project run to remain active")
	}

	orch.mu.RLock()
	defer orch.mu.RUnlock()
	if _, ok := orch.running[issue.ID]; ok {
		t.Fatal("expected project run to be removed from running map")
	}
	if _, ok := orch.running[otherIssue.ID]; !ok {
		t.Fatal("expected unrelated run to remain in running map")
	}
}

func TestProjectControlsHandleMissingProjects(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t, "cat")

	if got := orch.RequestProjectRefresh("missing"); got["status"] != "not_found" {
		t.Fatalf("expected not_found refresh result, got %#v", got)
	}
	if got := orch.StopProjectRuns("missing"); got["status"] != "not_found" {
		t.Fatalf("expected not_found stop result, got %#v", got)
	}
}

func TestStoppedProjectsDoNotDispatchUntilStarted(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	runner := &blockingRunner{
		started:      make(chan struct{}),
		ctxCancelled: make(chan struct{}),
		release:      make(chan struct{}),
	}
	orch.runner = runner

	project, err := store.CreateProject("Scoped", "", t.TempDir(), "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Project issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch while stopped: %v", err)
	}
	select {
	case <-runner.started:
		t.Fatal("expected stopped project not to dispatch")
	case <-time.After(100 * time.Millisecond):
	}

	if got := orch.RequestProjectRefresh(project.ID); got["state"] != string(kanban.ProjectStateRunning) {
		t.Fatalf("unexpected start payload: %#v", got)
	}
	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch after start: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("expected started project to dispatch")
	}

	if got := orch.StopProjectRuns(project.ID); got["state"] != string(kanban.ProjectStateStopped) {
		t.Fatalf("unexpected stop payload: %#v", got)
	}
	select {
	case <-runner.ctxCancelled:
	case <-time.After(time.Second):
		t.Fatal("expected stop to cancel running project issue")
	}
	close(runner.release)
}
