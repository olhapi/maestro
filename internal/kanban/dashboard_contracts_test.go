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
	if projectSummaries[0].TotalCount != 2 || projectSummaries[0].ActiveCount != 1 || projectSummaries[0].TerminalCount != 1 {
		t.Fatalf("unexpected aggregated project counts: %#v", projectSummaries[0])
	}
	if projectSummaries[0].State != ProjectStateStopped {
		t.Fatalf("expected stopped project state in summary, got %#v", projectSummaries[0].State)
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
	if epicSummaries[0].TotalCount != 2 || epicSummaries[0].ActiveCount != 1 || epicSummaries[0].TerminalCount != 1 {
		t.Fatalf("unexpected aggregated epic counts: %#v", epicSummaries[0])
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
	if detail.BranchName != "feature/runtime" || detail.PRURL != "https://example.com/pr/42" {
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
	if snapshot.AppSession.SessionID != "thread-runtime-turn-runtime" || len(snapshot.AppSession.History) != 0 {
		t.Fatalf("unexpected execution snapshot payload: %#v", snapshot)
	}
}

func TestDashboardPaginationHelpersUsePageQueries(t *testing.T) {
	store := setupTestStore(t)

	alpha, err := store.CreateProject("Alpha", "", t.TempDir(), "")
	if err != nil {
		t.Fatalf("CreateProject alpha: %v", err)
	}
	bravo, err := store.CreateProject("Bravo", "", t.TempDir(), "")
	if err != nil {
		t.Fatalf("CreateProject bravo: %v", err)
	}
	charlie, err := store.CreateProject("Charlie", "", t.TempDir(), "")
	if err != nil {
		t.Fatalf("CreateProject charlie: %v", err)
	}

	alphaIssue, err := store.CreateIssue(alpha.ID, "", "Alpha issue", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue alpha: %v", err)
	}
	if err := store.UpdateIssueState(alphaIssue.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState alpha: %v", err)
	}
	if err := store.AddIssueTokenSpend(alphaIssue.ID, 3); err != nil {
		t.Fatalf("AddIssueTokenSpend alpha: %v", err)
	}
	bravoIssue, err := store.CreateIssue(bravo.ID, "", "Bravo issue", "", 2, nil)
	if err != nil {
		t.Fatalf("CreateIssue bravo: %v", err)
	}
	if err := store.UpdateIssueState(bravoIssue.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState bravo: %v", err)
	}
	if err := store.AddIssueTokenSpend(bravoIssue.ID, 5); err != nil {
		t.Fatalf("AddIssueTokenSpend bravo: %v", err)
	}
	if _, err := store.CreateIssue(charlie.ID, "", "Charlie issue", "", 0, nil); err != nil {
		t.Fatalf("CreateIssue charlie: %v", err)
	}

	epic1, err := store.CreateEpic(alpha.ID, "Alpha epic 1", "")
	if err != nil {
		t.Fatalf("CreateEpic alpha 1: %v", err)
	}
	epic2, err := store.CreateEpic(alpha.ID, "Alpha epic 2", "")
	if err != nil {
		t.Fatalf("CreateEpic alpha 2: %v", err)
	}
	epic3, err := store.CreateEpic(alpha.ID, "Alpha epic 3", "")
	if err != nil {
		t.Fatalf("CreateEpic alpha 3: %v", err)
	}
	if issue, err := store.CreateIssue(alpha.ID, epic1.ID, "Epic issue 1", "", 0, nil); err != nil {
		t.Fatalf("CreateIssue epic 1: %v", err)
	} else if err := store.UpdateIssueState(issue.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState epic 1: %v", err)
	}
	if issue, err := store.CreateIssue(alpha.ID, epic2.ID, "Epic issue 2", "", 0, nil); err != nil {
		t.Fatalf("CreateIssue epic 2: %v", err)
	} else if err := store.UpdateIssueState(issue.ID, StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState epic 2: %v", err)
	}
	if issue, err := store.CreateIssue(alpha.ID, epic3.ID, "Epic issue 3", "", 0, nil); err != nil {
		t.Fatalf("CreateIssue epic 3: %v", err)
	} else if err := store.UpdateIssueState(issue.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState epic 3: %v", err)
	}

	commentIssue, err := store.CreateIssue(alpha.ID, "", "Comment issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue comment: %v", err)
	}
	root1Body := "Root 1"
	root1, err := store.CreateIssueComment(commentIssue.ID, IssueCommentInput{Body: &root1Body})
	if err != nil {
		t.Fatalf("CreateIssueComment root1: %v", err)
	}
	replyBody := "Reply 1"
	if _, err := store.CreateIssueComment(commentIssue.ID, IssueCommentInput{Body: &replyBody, ParentCommentID: root1.ID}); err != nil {
		t.Fatalf("CreateIssueComment reply: %v", err)
	}
	for _, body := range []string{"Root 2", "Root 3"} {
		body := body
		if _, err := store.CreateIssueComment(commentIssue.ID, IssueCommentInput{Body: &body}); err != nil {
			t.Fatalf("CreateIssueComment %s: %v", body, err)
		}
	}

	projectsPage1, totalProjects, err := store.ListProjectSummariesPage(2, 0)
	if err != nil {
		t.Fatalf("ListProjectSummariesPage 1: %v", err)
	}
	if totalProjects != 3 || len(projectsPage1) != 2 {
		t.Fatalf("unexpected first project page: total=%d items=%#v", totalProjects, projectsPage1)
	}
	if projectsPage1[0].Name != "Alpha" || projectsPage1[1].Name != "Bravo" {
		t.Fatalf("expected name-ordered project page, got %#v", projectsPage1)
	}
	if projectsPage1[0].Counts.Backlog != 1 || projectsPage1[0].Counts.Ready != 2 || projectsPage1[0].Counts.InProgress != 1 || projectsPage1[0].Counts.Done != 1 || projectsPage1[0].TotalTokensSpent != 3 {
		t.Fatalf("expected project aggregates to survive paging, got %#v", projectsPage1[0])
	}
	projectsPage2, totalProjects2, err := store.ListProjectSummariesPage(2, 2)
	if err != nil {
		t.Fatalf("ListProjectSummariesPage 2: %v", err)
	}
	if totalProjects2 != 3 || len(projectsPage2) != 1 || projectsPage2[0].Name != "Charlie" {
		t.Fatalf("unexpected second project page: total=%d items=%#v", totalProjects2, projectsPage2)
	}

	epicsPage1, totalEpics, err := store.ListEpicSummariesPage(alpha.ID, 2, 0)
	if err != nil {
		t.Fatalf("ListEpicSummariesPage 1: %v", err)
	}
	if totalEpics != 3 || len(epicsPage1) != 2 {
		t.Fatalf("unexpected first epic page: total=%d items=%#v", totalEpics, epicsPage1)
	}
	if epicsPage1[0].Name != "Alpha epic 1" || epicsPage1[0].ProjectName != alpha.Name {
		t.Fatalf("expected epic page to preserve project context, got %#v", epicsPage1[0])
	}
	epicsPage2, totalEpics2, err := store.ListEpicSummariesPage(alpha.ID, 2, 2)
	if err != nil {
		t.Fatalf("ListEpicSummariesPage 2: %v", err)
	}
	if totalEpics2 != 3 || len(epicsPage2) != 1 || epicsPage2[0].Name != "Alpha epic 3" {
		t.Fatalf("unexpected second epic page: total=%d items=%#v", totalEpics2, epicsPage2)
	}

	commentsPage1, totalComments, err := store.ListIssueCommentsPage(commentIssue.ID, 1, 0)
	if err != nil {
		t.Fatalf("ListIssueCommentsPage 1: %v", err)
	}
	if totalComments != 3 || len(commentsPage1) != 1 {
		t.Fatalf("unexpected first comment page: total=%d items=%#v", totalComments, commentsPage1)
	}
	if commentsPage1[0].Body != "Root 1" || len(commentsPage1[0].Replies) != 1 || commentsPage1[0].Replies[0].Body != "Reply 1" {
		t.Fatalf("expected replies to stay attached, got %#v", commentsPage1[0])
	}
	commentsPage2, totalComments2, err := store.ListIssueCommentsPage(commentIssue.ID, 1, 1)
	if err != nil {
		t.Fatalf("ListIssueCommentsPage 2: %v", err)
	}
	if totalComments2 != 3 || len(commentsPage2) != 1 || commentsPage2[0].Body != "Root 2" {
		t.Fatalf("unexpected second comment page: total=%d items=%#v", totalComments2, commentsPage2)
	}
	commentsPage3, totalComments3, err := store.ListIssueCommentsPage(commentIssue.ID, 1, 2)
	if err != nil {
		t.Fatalf("ListIssueCommentsPage 3: %v", err)
	}
	if totalComments3 != 3 || len(commentsPage3) != 1 || commentsPage3[0].Body != "Root 3" {
		t.Fatalf("unexpected third comment page: total=%d items=%#v", totalComments3, commentsPage3)
	}
}
