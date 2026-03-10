package kanban

import (
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
)

func TestDashboardScenarioShapesMatchPortfolioContracts(t *testing.T) {
	store := setupTestStore(t)

	project, err := store.CreateProject("Platform", "Main orchestration repo", "/repo", "/repo/WORKFLOW.md")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Observability", "Dashboard polish")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}

	readyIssue, err := store.CreateIssue(project.ID, epic.ID, "Runtime detail", "desc", 1, []string{"api"})
	if err != nil {
		t.Fatalf("CreateIssue ready: %v", err)
	}
	doneIssue, err := store.CreateIssue(project.ID, epic.ID, "Snapshot detail", "desc", 2, []string{"ui"})
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	if err := store.UpdateIssueState(readyIssue.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState ready: %v", err)
	}
	if err := store.UpdateIssueState(doneIssue.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState done: %v", err)
	}
	if err := store.AddIssueTokenSpend(readyIssue.ID, 11); err != nil {
		t.Fatalf("AddIssueTokenSpend ready: %v", err)
	}
	if err := store.AddIssueTokenSpend(doneIssue.ID, 7); err != nil {
		t.Fatalf("AddIssueTokenSpend done: %v", err)
	}

	workspace, err := store.CreateWorkspace(readyIssue.ID, "/tmp/workspaces/"+readyIssue.Identifier)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if workspace.Path == "" {
		t.Fatal("expected workspace path")
	}
	if err := store.UpdateWorkspaceRun(readyIssue.ID); err != nil {
		t.Fatalf("UpdateWorkspaceRun: %v", err)
	}
	if err := store.UpdateIssue(readyIssue.ID, map[string]interface{}{
		"blocked_by":  []string{doneIssue.Identifier},
		"branch_name": "feature/runtime",
		"pr_number":   42,
		"pr_url":      "https://example.com/pr/42",
	}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	for _, event := range []struct {
		kind  string
		error string
	}{
		{kind: "run_started"},
		{kind: "tick"},
		{kind: "run_completed"},
		{kind: "retry_scheduled", error: "approval_required"},
		{kind: "manual_retry_requested"},
	} {
		payload := map[string]interface{}{
			"issue_id":     readyIssue.ID,
			"identifier":   readyIssue.Identifier,
			"title":        readyIssue.Title,
			"phase":        "implementation",
			"attempt":      2,
			"total_tokens": 11,
		}
		if event.error != "" {
			payload["error"] = event.error
		}
		if err := store.AppendRuntimeEvent(event.kind, payload); err != nil {
			t.Fatalf("AppendRuntimeEvent %s: %v", event.kind, err)
		}
	}

	if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
		IssueID:    readyIssue.ID,
		Identifier: readyIssue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_completed",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         readyIssue.ID,
			IssueIdentifier: readyIssue.Identifier,
			SessionID:       "thread-runtime-turn-runtime",
			ThreadID:        "thread-runtime",
			TurnID:          "turn-runtime",
			LastEvent:       "turn.completed",
			LastTimestamp:   now,
			LastMessage:     "done",
			TotalTokens:     11,
			TurnsStarted:    1,
			TurnsCompleted:  1,
			Terminal:        true,
			TerminalReason:  "turn.completed",
			History: []appserver.Event{
				{Type: "turn.started", Message: "start"},
				{Type: "turn.completed", Message: "done"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	projectSummaries, err := store.ListProjectSummaries()
	if err != nil {
		t.Fatalf("ListProjectSummaries: %v", err)
	}
	if len(projectSummaries) != 1 || projectSummaries[0].Counts.Ready != 1 || projectSummaries[0].Counts.Done != 1 {
		t.Fatalf("unexpected project summaries: %#v", projectSummaries)
	}
	if projectSummaries[0].TotalTokensSpent != 18 {
		t.Fatalf("expected project token spend 18, got %#v", projectSummaries[0])
	}

	epicSummaries, err := store.ListEpicSummaries(project.ID)
	if err != nil {
		t.Fatalf("ListEpicSummaries: %v", err)
	}
	if len(epicSummaries) != 1 || epicSummaries[0].Counts.Ready != 1 || epicSummaries[0].Counts.Done != 1 {
		t.Fatalf("unexpected epic summaries: %#v", epicSummaries)
	}

	issueSummaries, total, err := store.ListIssueSummaries(IssueQuery{ProjectID: project.ID, Sort: "updated_desc", Limit: 10})
	if err != nil {
		t.Fatalf("ListIssueSummaries: %v", err)
	}
	if total != 2 || len(issueSummaries) != 2 {
		t.Fatalf("unexpected issue summaries: total=%d items=%d", total, len(issueSummaries))
	}
	if issueSummaries[0].WorkspaceRunCount < 1 {
		t.Fatalf("expected workspace run count on leading summary: %#v", issueSummaries[0])
	}

	detail, err := store.GetIssueDetailByIdentifier(readyIssue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier: %v", err)
	}
	if detail.ProjectDescription != project.Description || detail.EpicDescription != epic.Description {
		t.Fatalf("unexpected issue detail: %#v", detail)
	}
	if detail.BranchName != "feature/runtime" || detail.PRNumber != 42 {
		t.Fatalf("expected branch/pr metadata in detail: %#v", detail)
	}

	executionEvents, err := store.ListIssueRuntimeEvents(readyIssue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	if len(executionEvents) != 4 {
		t.Fatalf("expected 4 execution events, got %d", len(executionEvents))
	}
	if executionEvents[0].Kind != "run_started" || executionEvents[len(executionEvents)-1].Kind != "manual_retry_requested" {
		t.Fatalf("unexpected execution event ordering: %#v", executionEvents)
	}

	series, err := store.RuntimeSeries(6)
	if err != nil {
		t.Fatalf("RuntimeSeries: %v", err)
	}
	if len(series) != 6 {
		t.Fatalf("expected 6 series buckets, got %d", len(series))
	}

	snapshot, err := store.GetIssueExecutionSession(readyIssue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.AppSession.SessionID != "thread-runtime-turn-runtime" || len(snapshot.AppSession.History) != 2 {
		t.Fatalf("unexpected execution snapshot payload: %#v", snapshot)
	}
}
