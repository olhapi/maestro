package orchestrator

import (
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
	if refresh["status"] != "accepted" {
		t.Fatalf("unexpected project refresh payload: %#v", refresh)
	}

	stop := orch.StopProjectRuns(project.ID)
	if stop["status"] != "stopped" || stop["stopped_runs"] != 1 {
		t.Fatalf("unexpected project stop payload: %#v", stop)
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
