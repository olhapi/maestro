package kanban

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
)

func setupTestStore(t *testing.T) *Store {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	t.Cleanup(func() {
		store.Close()
	})

	return store
}

func TestDefaultDBPathUsesHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := DefaultDBPath()
	want := filepath.Join(home, ".maestro", "maestro.db")
	if got != want {
		t.Fatalf("DefaultDBPath() = %q, want %q", got, want)
	}
}

func TestResolveDBPathUsesDefaultWhenEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := ResolveDBPath("")
	want := filepath.Join(home, ".maestro", "maestro.db")
	if got != want {
		t.Fatalf("ResolveDBPath(\"\") = %q, want %q", got, want)
	}
}

func TestResolveDBPathPreservesExplicitPath(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom.db")
	if got := ResolveDBPath(want); got != want {
		t.Fatalf("ResolveDBPath(%q) = %q", want, got)
	}
}

func TestNewStoreConfiguresSQLitePragmas(t *testing.T) {
	store := setupTestStore(t)

	checkString := func(name, query, want string) {
		t.Helper()
		var got string
		if err := store.db.QueryRow(query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.ToLower(got) != strings.ToLower(want) {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	checkInt := func(name, query string, want int) {
		t.Helper()
		var got int
		if err := store.db.QueryRow(query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Fatalf("%s = %d, want %d", name, got, want)
		}
	}

	checkString("journal_mode", `PRAGMA journal_mode`, "wal")
	checkInt("busy_timeout", `PRAGMA busy_timeout`, 10000)
	checkInt("foreign_keys", `PRAGMA foreign_keys`, 1)
	checkInt("synchronous", `PRAGMA synchronous`, 1)

	stats := store.db.Stats()
	if stats.MaxOpenConnections != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", stats.MaxOpenConnections)
	}
}

func TestCreateIssueAgentCommandWithRuntimeEventRollsBackOnEventFailure(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Follow-up", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	_, err = store.CreateIssueAgentCommandWithRuntimeEvent(
		issue.ID,
		"Retry after failure.",
		IssueAgentCommandPending,
		"manual_command_submitted",
		map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      string(issue.WorkflowPhase),
			"bad":        func() {},
		},
	)
	if err == nil {
		t.Fatal("expected event payload error")
	}

	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAgentCommands: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("expected rollback to remove command, got %+v", commands)
	}
}

func TestIssueAgentCommandLifecycle(t *testing.T) {
	store := setupTestStore(t)

	blocker, err := store.CreateIssue("", "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	issue, err := store.CreateIssue("", "", "Follow-up", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue issue: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState issue: %v", err)
	}
	if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}

	unresolved, err := store.UnresolvedBlockersForIssue(issue.ID)
	if err != nil {
		t.Fatalf("UnresolvedBlockersForIssue: %v", err)
	}
	if len(unresolved) != 1 || unresolved[0] != blocker.Identifier {
		t.Fatalf("expected unresolved blocker %q, got %#v", blocker.Identifier, unresolved)
	}

	submitted, err := store.CreateIssueAgentCommandWithRuntimeEvent(
		issue.ID,
		"Resume implementation after unblock.",
		IssueAgentCommandPending,
		"manual_command_submitted",
		map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      string(issue.WorkflowPhase),
		},
	)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommandWithRuntimeEvent: %v", err)
	}
	waiting, err := store.CreateIssueAgentCommand(issue.ID, "Run the final check.", IssueAgentCommandWaitingForUnblock)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand waiting: %v", err)
	}
	if err := store.UpdateIssueAgentCommandStatus(submitted.ID, IssueAgentCommandWaitingForUnblock); err != nil {
		t.Fatalf("UpdateIssueAgentCommandStatus: %v", err)
	}

	pending, err := store.ListPendingIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListPendingIssueAgentCommands while blocked: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending commands while blocked, got %#v", pending)
	}

	if err := store.UpdateIssueState(blocker.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState blocker done: %v", err)
	}
	if err := store.ActivateIssueAgentCommandsIfDispatchable(issue.ID); err != nil {
		t.Fatalf("ActivateIssueAgentCommandsIfDispatchable: %v", err)
	}

	pending, err = store.ListPendingIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListPendingIssueAgentCommands after unblock: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected two pending commands after unblock, got %#v", pending)
	}
	if pending[0].ID != submitted.ID || pending[1].ID != waiting.ID {
		t.Fatalf("expected oldest-first pending ordering, got %#v", pending)
	}

	beforeDeliveredChange, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq before delivered change: %v", err)
	}
	if err := store.MarkIssueAgentCommandsDelivered(issue.ID, []string{submitted.ID, waiting.ID}, "same_thread", "thread-live", 2); err != nil {
		t.Fatalf("MarkIssueAgentCommandsDelivered: %v", err)
	}
	afterDeliveredChange, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq after delivered change: %v", err)
	}
	if afterDeliveredChange <= beforeDeliveredChange {
		t.Fatalf("expected delivered change event to advance seq: before=%d after=%d", beforeDeliveredChange, afterDeliveredChange)
	}

	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAgentCommands: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("expected two commands, got %#v", commands)
	}
	for _, command := range commands {
		if command.Status != IssueAgentCommandDelivered {
			t.Fatalf("expected delivered status, got %+v", command)
		}
		if command.DeliveryMode != "same_thread" || command.DeliveryThreadID != "thread-live" || command.DeliveryAttempt != 2 {
			t.Fatalf("unexpected delivery metadata: %+v", command)
		}
		if command.DeliveredAt == nil || command.DeliveredAt.IsZero() {
			t.Fatalf("expected delivered timestamp, got %+v", command)
		}
	}

	events, err := store.ListRuntimeEvents(0, 20)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	foundSubmission := false
	for _, event := range events {
		switch event.Kind {
		case "manual_command_submitted":
			if event.Payload["command_id"] != submitted.ID || event.Payload["command"] != submitted.Command {
				t.Fatalf("unexpected submitted payload: %+v", event)
			}
			foundSubmission = true
		}
	}
	if !foundSubmission {
		t.Fatal("expected manual_command_submitted runtime event")
	}
}

func TestStateValidation(t *testing.T) {
	tests := []struct {
		state    State
		expected bool
	}{
		{StateBacklog, true},
		{StateReady, true},
		{StateInProgress, true},
		{StateInReview, true},
		{StateDone, true},
		{StateCancelled, true},
		{State("invalid"), false},
		{State(""), false},
	}

	for _, tt := range tests {
		if got := tt.state.IsValid(); got != tt.expected {
			t.Errorf("State(%q).IsValid() = %v, expected %v", tt.state, got, tt.expected)
		}
	}
}

func TestActiveStates(t *testing.T) {
	states := ActiveStates()
	if len(states) != 3 {
		t.Errorf("Expected 3 active states, got %d", len(states))
	}
}

func TestTerminalStates(t *testing.T) {
	states := TerminalStates()
	if len(states) != 2 {
		t.Errorf("Expected 2 terminal states, got %d", len(states))
	}
}

// Project tests

func TestCreateProject(t *testing.T) {
	store := setupTestStore(t)

	project, err := store.CreateProject("Test Project", "A test project", "", "")
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}

	if project.Name != "Test Project" {
		t.Errorf("Expected name 'Test Project', got %s", project.Name)
	}
	if project.Description != "A test project" {
		t.Errorf("Expected description 'A test project', got %s", project.Description)
	}
	if project.ID == "" {
		t.Error("Expected non-empty ID")
	}
}

func TestGetProject(t *testing.T) {
	store := setupTestStore(t)

	created, err := store.CreateProject("Test", "Desc", "", "")
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}

	project, err := store.GetProject(created.ID)
	if err != nil {
		t.Fatalf("Failed to get project: %v", err)
	}

	if project.Name != "Test" {
		t.Errorf("Expected name 'Test', got %s", project.Name)
	}
}

func TestListProjects(t *testing.T) {
	store := setupTestStore(t)

	_, _ = store.CreateProject("Project A", "", "", "")
	_, _ = store.CreateProject("Project B", "", "", "")

	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("Failed to list projects: %v", err)
	}

	if len(projects) != 2 {
		t.Errorf("Expected 2 projects, got %d", len(projects))
	}
}

func TestDeleteProject(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("To Delete", "", "", "")
	epic, err := store.CreateEpic(project.ID, "Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Issue in project", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	workspacePath := filepath.Join(t.TempDir(), issue.Identifier)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatalf("Failed to delete project: %v", err)
	}

	_, err = store.GetProject(project.ID)
	if err == nil {
		t.Error("Expected error getting deleted project")
	}
	if _, err := store.GetIssue(issue.ID); err == nil {
		t.Error("Expected project issue to be deleted")
	}
	if _, err := store.GetEpic(epic.ID); err == nil {
		t.Error("Expected project epic to be deleted")
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path to be removed, got err=%v", err)
	}
}

func TestDeleteProjectReturnsNotFound(t *testing.T) {
	store := setupTestStore(t)

	err := store.DeleteProject("missing-project")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected not found error, got %v", err)
	}
}

// Epic tests

func TestCreateEpic(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")

	epic, err := store.CreateEpic(project.ID, "Epic 1", "Epic description")
	if err != nil {
		t.Fatalf("Failed to create epic: %v", err)
	}

	if epic.Name != "Epic 1" {
		t.Errorf("Expected name 'Epic 1', got %s", epic.Name)
	}
	if epic.ProjectID != project.ID {
		t.Error("Epic project ID mismatch")
	}
}

func TestListEpics(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	_, _ = store.CreateEpic(project.ID, "Epic 1", "")
	_, _ = store.CreateEpic(project.ID, "Epic 2", "")

	epics, err := store.ListEpics(project.ID)
	if err != nil {
		t.Fatalf("Failed to list epics: %v", err)
	}

	if len(epics) != 2 {
		t.Errorf("Expected 2 epics, got %d", len(epics))
	}
}

// Issue tests

func TestCreateIssue(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("MyApp", "", "", "")
	labels := []string{"bug", "urgent"}

	issue, err := store.CreateIssue(project.ID, "", "Fix login bug", "Description here", 1, labels)
	if err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	if issue.Title != "Fix login bug" {
		t.Errorf("Expected title 'Fix login bug', got %s", issue.Title)
	}
	if issue.State != StateBacklog {
		t.Errorf("Expected initial state 'backlog', got %s", issue.State)
	}
	if issue.Identifier == "" {
		t.Error("Expected non-empty identifier")
	}
	if len(issue.Labels) != 2 {
		t.Errorf("Expected 2 labels, got %d", len(issue.Labels))
	}
}

func TestGetIssueByIdentifier(t *testing.T) {
	store := setupTestStore(t)

	created, _ := store.CreateIssue("", "", "Test Issue", "", 0, nil)

	issue, err := store.GetIssueByIdentifier(created.Identifier)
	if err != nil {
		t.Fatalf("Failed to get issue by identifier: %v", err)
	}

	if issue.ID != created.ID {
		t.Error("Issue ID mismatch")
	}
}

func TestAddIssueTokenSpendIncrementsWithoutTouchingUpdatedAt(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Token spend", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	before, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue before increment: %v", err)
	}

	if err := store.AddIssueTokenSpend(issue.ID, 12); err != nil {
		t.Fatalf("AddIssueTokenSpend first increment: %v", err)
	}
	if err := store.AddIssueTokenSpend(issue.ID, 5); err != nil {
		t.Fatalf("AddIssueTokenSpend second increment: %v", err)
	}

	after, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after increment: %v", err)
	}
	if after.TotalTokensSpent != 17 {
		t.Fatalf("TotalTokensSpent = %d, want 17", after.TotalTokensSpent)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("UpdatedAt changed from %s to %s", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestListIssues(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	_, _ = store.CreateIssue(project.ID, "", "Issue 1", "", 0, nil)
	_, _ = store.CreateIssue(project.ID, "", "Issue 2", "", 0, nil)
	_, _ = store.CreateIssue("", "", "Issue 3 (no project)", "", 0, nil)

	// List all
	issues, err := store.ListIssues(nil)
	if err != nil {
		t.Fatalf("Failed to list issues: %v", err)
	}
	if len(issues) != 3 {
		t.Errorf("Expected 3 issues, got %d", len(issues))
	}

	// Filter by project
	projectIssues, err := store.ListIssues(map[string]interface{}{"project_id": project.ID})
	if err != nil {
		t.Fatalf("Failed to list project issues: %v", err)
	}
	if len(projectIssues) != 2 {
		t.Errorf("Expected 2 project issues, got %d", len(projectIssues))
	}
}

func TestUpdateIssueState(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)

	// Move to ready
	if err := store.UpdateIssueState(issue.ID, StateReady); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}

	updated, _ := store.GetIssue(issue.ID)
	if updated.State != StateReady {
		t.Errorf("Expected state 'ready', got %s", updated.State)
	}
	if updated.WorkflowPhase != WorkflowPhaseImplementation {
		t.Fatalf("expected implementation phase for ready issue, got %s", updated.WorkflowPhase)
	}

	// Move to in_progress - should set started_at
	if err := store.UpdateIssueState(issue.ID, StateInProgress); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}

	updated, _ = store.GetIssue(issue.ID)
	if updated.StartedAt == nil {
		t.Error("Expected started_at to be set")
	}
	if updated.WorkflowPhase != WorkflowPhaseImplementation {
		t.Fatalf("expected implementation phase for in_progress issue, got %s", updated.WorkflowPhase)
	}

	if err := store.UpdateIssueState(issue.ID, StateInReview); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}
	updated, _ = store.GetIssue(issue.ID)
	if updated.WorkflowPhase != WorkflowPhaseImplementation {
		t.Fatalf("expected manual in_review move to stay in implementation phase, got %s", updated.WorkflowPhase)
	}

	// Move to done - should set completed_at
	if err := store.UpdateIssueState(issue.ID, StateDone); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}

	updated, _ = store.GetIssue(issue.ID)
	if updated.CompletedAt == nil {
		t.Error("Expected completed_at to be set")
	}
	if updated.WorkflowPhase != WorkflowPhaseComplete {
		t.Fatalf("expected complete phase for done issue, got %s", updated.WorkflowPhase)
	}
}

func TestUpdateIssueStateRejectsInvalidState(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	err := store.UpdateIssueState(issue.ID, State("invalid"))
	if err == nil {
		t.Fatal("expected invalid state error")
	}
	if !IsValidation(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestUpdateIssueStateRejectsBlockedInProgress(t *testing.T) {
	store := setupTestStore(t)

	blockerB, _ := store.CreateIssue("", "", "Blocker B", "", 0, nil)
	blockerA, _ := store.CreateIssue("", "", "Blocker A", "", 0, nil)
	blocked, _ := store.CreateIssue("", "", "Blocked", "", 0, nil)

	if err := store.UpdateIssueState(blockerA.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blockerA: %v", err)
	}
	if err := store.UpdateIssueState(blockerB.ID, StateInReview); err != nil {
		t.Fatalf("UpdateIssueState blockerB: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{blockerA.Identifier, blockerB.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}

	err := store.UpdateIssueState(blocked.ID, StateInProgress)
	if err == nil {
		t.Fatal("expected blocked transition error")
	}
	if !IsBlockedTransition(err) {
		t.Fatalf("expected blocked transition error, got %v", err)
	}
	if !IsValidation(err) {
		t.Fatalf("expected validation classification, got %v", err)
	}
	blockers := []string{blockerA.Identifier, blockerB.Identifier}
	sort.Strings(blockers)
	want := "cannot move issue to in_progress: blocked by " + strings.Join(blockers, ", ") + ". Move those blockers to done or cancelled, or remove them from blocked_by first"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected blocker message %q, got %q", want, err.Error())
	}

	reloaded, err := store.GetIssue(blocked.ID)
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}
	if reloaded.State != StateBacklog {
		t.Fatalf("expected blocked issue to stay in backlog, got %s", reloaded.State)
	}
	if reloaded.StartedAt != nil {
		t.Fatal("expected blocked issue to remain unstarted")
	}

	if err := store.UpdateIssueState(blocked.ID, StateReady); err != nil {
		t.Fatalf("expected non-in_progress transition to succeed, got %v", err)
	}
}

func TestUpdateIssueStateAllowsTerminalBlockers(t *testing.T) {
	store := setupTestStore(t)

	doneBlocker, _ := store.CreateIssue("", "", "Done blocker", "", 0, nil)
	cancelledBlocker, _ := store.CreateIssue("", "", "Cancelled blocker", "", 0, nil)
	blocked, _ := store.CreateIssue("", "", "Blocked", "", 0, nil)

	if err := store.UpdateIssueState(doneBlocker.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState doneBlocker: %v", err)
	}
	if err := store.UpdateIssueState(cancelledBlocker.ID, StateCancelled); err != nil {
		t.Fatalf("UpdateIssueState cancelledBlocker: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{doneBlocker.Identifier, cancelledBlocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}

	if err := store.UpdateIssueState(blocked.ID, StateInProgress); err != nil {
		t.Fatalf("expected terminal blockers to allow in_progress, got %v", err)
	}
}

func TestUpdateIssue(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Original Title", "", 0, nil)

	updates := map[string]interface{}{
		"title":       "Updated Title",
		"description": "New description",
		"priority":    5,
		"labels":      []string{"new-label"},
	}

	if err := store.UpdateIssue(issue.ID, updates); err != nil {
		t.Fatalf("Failed to update issue: %v", err)
	}

	updated, _ := store.GetIssue(issue.ID)
	if updated.Title != "Updated Title" {
		t.Errorf("Expected title 'Updated Title', got %s", updated.Title)
	}
	if updated.Priority != 5 {
		t.Errorf("Expected priority 5, got %d", updated.Priority)
	}
	if len(updated.Labels) != 1 || updated.Labels[0] != "new-label" {
		t.Errorf("Expected labels ['new-label'], got %v", updated.Labels)
	}
}

func TestUpdateIssueResolvesEpicProjectConsistency(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	epic, _ := store.CreateEpic(project.ID, "Epic", "")
	issue, _ := store.CreateIssue("", "", "Original Title", "", 0, nil)

	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"epic_id": epic.ID}); err != nil {
		t.Fatalf("UpdateIssue failed: %v", err)
	}

	updated, _ := store.GetIssue(issue.ID)
	if updated.EpicID != epic.ID {
		t.Fatalf("expected epic %s, got %s", epic.ID, updated.EpicID)
	}
	if updated.ProjectID != project.ID {
		t.Fatalf("expected project %s, got %s", project.ID, updated.ProjectID)
	}
}

func TestUpdateIssueRejectsMismatchedProjectAndEpic(t *testing.T) {
	store := setupTestStore(t)

	projectA, _ := store.CreateProject("Project A", "", "", "")
	projectB, _ := store.CreateProject("Project B", "", "", "")
	epic, _ := store.CreateEpic(projectA.ID, "Epic", "")
	issue, _ := store.CreateIssue(projectB.ID, "", "Original Title", "", 0, nil)

	err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"project_id": projectB.ID,
		"epic_id":    epic.ID,
	})
	if err == nil {
		t.Fatal("expected mismatched project/epic validation error")
	}
	if !IsValidation(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestUpdateIssueClearingProjectAlsoClearsEpic(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	epic, _ := store.CreateEpic(project.ID, "Epic", "")
	issue, _ := store.CreateIssue(project.ID, epic.ID, "Original Title", "", 0, nil)

	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"project_id": ""}); err != nil {
		t.Fatalf("UpdateIssue failed: %v", err)
	}

	updated, _ := store.GetIssue(issue.ID)
	if updated.ProjectID != "" || updated.EpicID != "" {
		t.Fatalf("expected cleared project/epic, got project=%q epic=%q", updated.ProjectID, updated.EpicID)
	}
}

func TestDeleteIssue(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "To Delete", "", 0, nil)
	workspacePath := filepath.Join(t.TempDir(), issue.Identifier)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if err := store.DeleteIssue(issue.ID); err != nil {
		t.Fatalf("Failed to delete issue: %v", err)
	}

	_, err := store.GetIssue(issue.ID)
	if err == nil {
		t.Error("Expected error getting deleted issue")
	}
	if _, err := store.GetWorkspace(issue.ID); err == nil {
		t.Error("Expected workspace record to be deleted")
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path to be removed, got err=%v", err)
	}
}

func TestDeleteIssueReturnsNotFound(t *testing.T) {
	store := setupTestStore(t)

	err := store.DeleteIssue("missing-issue")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestIssueBlockers(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	issue1, _ := store.CreateIssue(project.ID, "", "Issue 1", "", 0, nil)
	issue2, _ := store.CreateIssue(project.ID, "", "Issue 2", "", 0, nil)

	if _, err := store.SetIssueBlockers(issue1.ID, []string{issue2.Identifier}); err != nil {
		t.Fatalf("Failed to set blockers: %v", err)
	}

	updated, _ := store.GetIssue(issue1.ID)
	if len(updated.BlockedBy) != 1 || updated.BlockedBy[0] != issue2.Identifier {
		t.Errorf("Expected blocker %s, got %v", issue2.Identifier, updated.BlockedBy)
	}
}

func TestSetIssueBlockersNormalizesDuplicates(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	issue1, _ := store.CreateIssue(project.ID, "", "Issue 1", "", 0, nil)
	issue2, _ := store.CreateIssue(project.ID, "", "Issue 2", "", 0, nil)

	blockers, err := store.SetIssueBlockers(issue1.ID, []string{issue2.Identifier, " ", issue2.Identifier})
	if err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}
	if len(blockers) != 1 || blockers[0] != issue2.Identifier {
		t.Fatalf("expected normalized blocker set [%s], got %v", issue2.Identifier, blockers)
	}
}

func TestSetIssueBlockersRejectsCrossProjectAndRollsBack(t *testing.T) {
	store := setupTestStore(t)

	projectA, _ := store.CreateProject("Project A", "", "", "")
	projectB, _ := store.CreateProject("Project B", "", "", "")
	issueA, _ := store.CreateIssue(projectA.ID, "", "Issue A", "", 0, nil)
	issueB, _ := store.CreateIssue(projectB.ID, "", "Issue B", "", 0, nil)

	if _, err := store.SetIssueBlockers(issueA.ID, []string{issueB.Identifier}); err == nil {
		t.Fatal("expected cross-project blocker validation error")
	}

	updated, _ := store.GetIssue(issueA.ID)
	if len(updated.BlockedBy) != 0 {
		t.Fatalf("expected blocker set to remain empty, got %v", updated.BlockedBy)
	}
}

func TestUpdateIssueWithInvalidBlockerRollsBackIssueFields(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	issue, _ := store.CreateIssue(project.ID, "", "Original", "", 0, nil)

	err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"title":       "Changed",
		"description": "Changed description",
		"blocked_by":  []string{"MISSING-1"},
	})
	if err == nil {
		t.Fatal("expected invalid blocker update to fail")
	}

	updated, _ := store.GetIssue(issue.ID)
	if updated.Title != "Original" {
		t.Fatalf("expected title rollback, got %q", updated.Title)
	}
	if updated.Description != "" {
		t.Fatalf("expected description rollback, got %q", updated.Description)
	}
	if len(updated.BlockedBy) != 0 {
		t.Fatalf("expected blockers rollback, got %v", updated.BlockedBy)
	}
}

// Workspace tests

func TestCreateWorkspace(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)

	workspace, err := store.CreateWorkspace(issue.ID, "/tmp/workspace")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	if workspace.IssueID != issue.ID {
		t.Error("Workspace issue ID mismatch")
	}
	if workspace.RunCount != 0 {
		t.Errorf("Expected run count 0, got %d", workspace.RunCount)
	}
}

func TestUpdateWorkspaceRun(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	_, _ = store.CreateWorkspace(issue.ID, "/tmp/workspace")

	if err := store.UpdateWorkspaceRun(issue.ID); err != nil {
		t.Fatalf("Failed to update workspace run: %v", err)
	}

	workspace, _ := store.GetWorkspace(issue.ID)
	if workspace.RunCount != 1 {
		t.Errorf("Expected run count 1, got %d", workspace.RunCount)
	}
	if workspace.LastRunAt == nil {
		t.Error("Expected last_run_at to be set")
	}
}

func TestDeleteWorkspace(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	_, _ = store.CreateWorkspace(issue.ID, workspacePath)

	if err := store.DeleteWorkspace(issue.ID); err != nil {
		t.Fatalf("Failed to delete workspace: %v", err)
	}

	_, err := store.GetWorkspace(issue.ID)
	if err == nil {
		t.Error("Expected error getting deleted workspace")
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path to be removed, got err=%v", err)
	}
}

func TestGenerateIdentifier(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("MyApp", "", "", "")

	issue1, _ := store.CreateIssue(project.ID, "", "First", "", 0, nil)
	issue2, _ := store.CreateIssue(project.ID, "", "Second", "", 0, nil)

	// Identifiers should be unique
	if issue1.Identifier == issue2.Identifier {
		t.Error("Expected unique identifiers")
	}

	// Identifier should have a prefix
	if len(issue1.Identifier) < 5 {
		t.Errorf("Identifier too short: %s", issue1.Identifier)
	}
}

func TestIssuePrioritySorting(t *testing.T) {
	store := setupTestStore(t)

	// Create issues with different priorities
	_, _ = store.CreateIssue("", "", "Low Priority", "", 10, nil)
	_, _ = store.CreateIssue("", "", "High Priority", "", 1, nil)
	_, _ = store.CreateIssue("", "", "Unprioritized", "", 0, nil)
	_, _ = store.CreateIssue("", "", "Medium Priority", "", 5, nil)

	issues, _ := store.ListIssues(nil)

	// Positive priorities should be sorted ascending before unprioritized (0).
	if len(issues) < 4 {
		t.Fatalf("expected 4 issues, got %d", len(issues))
	}
	if issues[0].Priority != 1 || issues[1].Priority != 5 || issues[2].Priority != 10 || issues[3].Priority != 0 {
		t.Fatalf("unexpected priority order: got [%d %d %d %d]", issues[0].Priority, issues[1].Priority, issues[2].Priority, issues[3].Priority)
	}
}

func TestListIssueSummariesPrioritySortTreatsZeroAsUnprioritized(t *testing.T) {
	store := setupTestStore(t)
	_, _ = store.CreateIssue("", "", "No priority", "", 0, nil)
	_, _ = store.CreateIssue("", "", "P3", "", 3, nil)
	_, _ = store.CreateIssue("", "", "P1", "", 1, nil)

	items, _, err := store.ListIssueSummaries(IssueQuery{
		Sort:  "priority_asc",
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListIssueSummaries failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 issues, got %d", len(items))
	}
	if items[0].Priority != 1 || items[1].Priority != 3 || items[2].Priority != 0 {
		t.Fatalf("unexpected priority_asc order: got [%d %d %d]", items[0].Priority, items[1].Priority, items[2].Priority)
	}
}

func TestListIssueSummariesSupportsBlockedAndProjectNameFilters(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Platform", "", "", "")
	other, _ := store.CreateProject("Other", "", "", "")
	blocker, _ := store.CreateIssue(project.ID, "", "Blocker", "", 0, nil)
	blocked, _ := store.CreateIssue(project.ID, "", "Blocked", "", 0, nil)
	_, _ = store.CreateIssue(other.ID, "", "Elsewhere", "", 0, nil)
	if _, err := store.SetIssueBlockers(blocked.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}

	blockedOnly := true
	items, total, err := store.ListIssueSummaries(IssueQuery{
		ProjectName: "platform",
		Blocked:     &blockedOnly,
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("ListIssueSummaries failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected one blocked platform issue, got total=%d items=%d", total, len(items))
	}
	if items[0].Identifier != blocked.Identifier {
		t.Fatalf("expected blocked identifier %s, got %s", blocked.Identifier, items[0].Identifier)
	}
}

func TestConcurrentAccess(t *testing.T) {
	store := setupTestStore(t)

	done := make(chan bool)

	// Concurrent creates - use mutex or sequential since identifier generation isn't thread-safe
	for i := 0; i < 10; i++ {
		go func(n int) {
			// Create issues sequentially within each goroutine to avoid race
			_, err := store.CreateIssue("", "", "Concurrent", "", 0, nil)
			if err != nil {
				// UNIQUE constraint on identifier is expected under concurrency
				// Try again
				_, err = store.CreateIssue("", "", "Concurrent", "", 0, nil)
			}
			if err != nil {
				t.Errorf("Concurrent create failed: %v", err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have at least some issues created
	issues, _ := store.ListIssues(nil)
	if len(issues) < 1 {
		t.Errorf("Expected at least 1 issue, got %d", len(issues))
	}
}

func TestCreateProjectNormalizesPathsAndReadiness(t *testing.T) {
	store := setupTestStore(t)
	repoDir := t.TempDir()
	workflowPath := filepath.Join(repoDir, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	project, err := store.CreateProject("Repo Project", "desc", repoDir, "")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if project.RepoPath != repoDir {
		t.Fatalf("expected repo path %q, got %q", repoDir, project.RepoPath)
	}
	if project.WorkflowPath != workflowPath {
		t.Fatalf("expected workflow path %q, got %q", workflowPath, project.WorkflowPath)
	}
	if !project.OrchestrationReady {
		t.Fatal("expected project to be orchestration ready")
	}
}

func TestLatestChangeSeqAdvancesOnMutations(t *testing.T) {
	store := setupTestStore(t)
	before, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq failed: %v", err)
	}

	if _, err := store.CreateProject("Tracked", "", "", ""); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	after, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq failed: %v", err)
	}
	if after <= before {
		t.Fatalf("expected change seq to increase, before=%d after=%d", before, after)
	}
}

func TestStoreIdentityStable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "identity.db")

	store1, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	identity1 := store1.Identity()
	if identity1.StoreID == "" {
		t.Fatal("expected non-empty store id")
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	store2, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store2.Close()

	identity2 := store2.Identity()
	if identity1.StoreID != identity2.StoreID {
		t.Fatalf("expected stable store id, got %q then %q", identity1.StoreID, identity2.StoreID)
	}
	absDBPath, _ := filepath.Abs(dbPath)
	if identity2.DBPath != absDBPath {
		t.Fatalf("expected db path %q, got %q", absDBPath, identity2.DBPath)
	}
}

func TestListIssueRuntimeEventsFiltersAndOrdersExecutionEvents(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Runtime issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	for _, kind := range []string{"run_started", "tick", "run_failed", "retry_scheduled", "manual_retry_requested"} {
		if err := store.AppendRuntimeEvent(kind, map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s) failed: %v", kind, err)
		}
	}

	events, err := store.ListIssueRuntimeEvents(issue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents failed: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 execution events, got %d", len(events))
	}
	if events[0].Kind != "run_started" || events[len(events)-1].Kind != "manual_retry_requested" {
		t.Fatalf("expected oldest-to-newest execution events, got %#v", events)
	}
	for _, event := range events {
		if event.Kind == "tick" {
			t.Fatalf("unexpected non-execution event returned: %#v", event)
		}
		if event.IssueID != issue.ID {
			t.Fatalf("unexpected issue id: %#v", event)
		}
	}
}

func TestIssueExecutionSessionSnapshotRoundTrip(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Session issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := ExecutionSessionSnapshot{
		IssueID:        issue.ID,
		Identifier:     issue.Identifier,
		Phase:          "implementation",
		Attempt:        2,
		RunKind:        "run_failed",
		Error:          "approval_required",
		ResumeEligible: true,
		StopReason:     "graceful_shutdown",
		UpdatedAt:      now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-1-turn-1",
			ThreadID:        "thread-1",
			TurnID:          "turn-1",
			LastEvent:       "turn.started",
			LastTimestamp:   now,
			LastMessage:     "Waiting for approval",
			TotalTokens:     42,
			TurnsStarted:    1,
			History: []appserver.Event{
				{Type: "turn.started", Message: "Start"},
				{Type: "turn.approval_required", Message: "Waiting for approval"},
			},
		},
	}

	if err := store.UpsertIssueExecutionSession(snapshot); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}

	loaded, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession failed: %v", err)
	}
	if loaded.Attempt != 2 || loaded.RunKind != "run_failed" || loaded.Error != "approval_required" {
		t.Fatalf("unexpected snapshot metadata: %+v", loaded)
	}
	if !loaded.ResumeEligible || loaded.StopReason != "graceful_shutdown" {
		t.Fatalf("expected resume metadata, got %+v", loaded)
	}
	if loaded.AppSession.SessionID != "thread-1-turn-1" || len(loaded.AppSession.History) != 2 {
		t.Fatalf("unexpected session payload: %+v", loaded.AppSession)
	}

	snapshot.Attempt = 3
	snapshot.RunKind = "run_completed"
	snapshot.Error = ""
	snapshot.ResumeEligible = false
	snapshot.StopReason = ""
	snapshot.AppSession.LastEvent = "turn.completed"
	if err := store.UpsertIssueExecutionSession(snapshot); err != nil {
		t.Fatalf("UpsertIssueExecutionSession update failed: %v", err)
	}
	loaded, err = store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession after update failed: %v", err)
	}
	if loaded.Attempt != 3 || loaded.RunKind != "run_completed" || loaded.AppSession.LastEvent != "turn.completed" {
		t.Fatalf("unexpected updated snapshot: %+v", loaded)
	}
	if loaded.ResumeEligible || loaded.StopReason != "" {
		t.Fatalf("expected cleared resume metadata, got %+v", loaded)
	}
}

func TestIssueExecutionSessionMigrationAddsResumeColumns(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE issue_execution_sessions (
			issue_id TEXT PRIMARY KEY,
			identifier TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL DEFAULT '',
			attempt INTEGER NOT NULL DEFAULT 0,
			run_kind TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			session_json TEXT NOT NULL DEFAULT '{}'
		)`); err != nil {
		t.Fatalf("create legacy table failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db failed: %v", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	issue, err := store.CreateIssue("", "", "Migrated session", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
		IssueID:        issue.ID,
		Identifier:     issue.Identifier,
		Phase:          "implementation",
		Attempt:        1,
		RunKind:        "run_started",
		ResumeEligible: true,
		StopReason:     "graceful_shutdown",
		UpdatedAt:      time.Now().UTC(),
		AppSession:     appserver.Session{IssueID: issue.ID, IssueIdentifier: issue.Identifier, ThreadID: "thread-migrated"},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}

	loaded, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession failed: %v", err)
	}
	if !loaded.ResumeEligible || loaded.StopReason != "graceful_shutdown" {
		t.Fatalf("expected migrated resume metadata, got %+v", loaded)
	}
}

func TestIssueExecutionSessionUpsertDoesNotEmitChangeEvents(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Quiet session issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	before, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq failed: %v", err)
	}

	if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  time.Now().UTC(),
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-quiet-turn-quiet",
			LastEvent:       "item.started",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}

	after, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq failed: %v", err)
	}
	if after != before {
		t.Fatalf("expected execution session upsert not to emit change event, before=%d after=%d", before, after)
	}
}

func TestListRecentExecutionSessionsOrdersAndDecodesPayloads(t *testing.T) {
	store := setupTestStore(t)
	oldIssue, err := store.CreateIssue("", "", "Older session", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue old failed: %v", err)
	}
	newIssue, err := store.CreateIssue("", "", "Newer session", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue new failed: %v", err)
	}
	base := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	for _, snapshot := range []ExecutionSessionSnapshot{
		{
			IssueID:    oldIssue.ID,
			Identifier: oldIssue.Identifier,
			Phase:      "implementation",
			Attempt:    1,
			RunKind:    "run_failed",
			Error:      "approval_required",
			UpdatedAt:  base.Add(-2 * time.Hour),
			AppSession: appserver.Session{
				IssueID:         oldIssue.ID,
				IssueIdentifier: oldIssue.Identifier,
				SessionID:       "thread-old-turn-old",
				LastEvent:       "turn.approval_required",
				LastTimestamp:   base.Add(-2 * time.Hour),
				LastMessage:     "Waiting for approval",
				History:         []appserver.Event{{Type: "turn.approval_required", Message: "Waiting for approval"}},
			},
		},
		{
			IssueID:    newIssue.ID,
			Identifier: newIssue.Identifier,
			Phase:      "review",
			Attempt:    2,
			RunKind:    "run_completed",
			UpdatedAt:  base,
			AppSession: appserver.Session{
				IssueID:         newIssue.ID,
				IssueIdentifier: newIssue.Identifier,
				SessionID:       "thread-new-turn-new",
				LastEvent:       "turn.completed",
				LastTimestamp:   base,
				LastMessage:     "Finished review",
				History:         []appserver.Event{{Type: "turn.completed", Message: "Finished review"}},
			},
		},
	} {
		if err := store.UpsertIssueExecutionSession(snapshot); err != nil {
			t.Fatalf("UpsertIssueExecutionSession(%s) failed: %v", snapshot.Identifier, err)
		}
	}

	snapshots, err := store.ListRecentExecutionSessions(base.Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListRecentExecutionSessions failed: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snapshots))
	}
	if snapshots[0].IssueID != newIssue.ID || snapshots[1].IssueID != oldIssue.ID {
		t.Fatalf("expected newest-first ordering, got %#v", snapshots)
	}
	if snapshots[0].AppSession.LastMessage != "Finished review" || len(snapshots[0].AppSession.History) != 1 {
		t.Fatalf("expected decoded app session payload, got %+v", snapshots[0].AppSession)
	}

	filtered, err := store.ListRecentExecutionSessions(base.Add(-90*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListRecentExecutionSessions filtered failed: %v", err)
	}
	if len(filtered) != 1 || filtered[0].IssueID != newIssue.ID {
		t.Fatalf("expected recent filter to keep only newest snapshot, got %#v", filtered)
	}
}

func TestStoreAccessorsAndAdditionalCRUDPaths(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "extra.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if store.DBPath() == "" {
		t.Fatal("expected DBPath to be populated")
	}
	if store.StoreID() == "" {
		t.Fatal("expected StoreID to be populated")
	}

	repoPath := t.TempDir()
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	project, err := store.CreateProject("Project", "", repoPath, workflowPath)
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	updatedRepo := t.TempDir()
	updatedWorkflow := filepath.Join(updatedRepo, "ALT_WORKFLOW.md")
	if err := os.WriteFile(updatedWorkflow, []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
		t.Fatalf("write updated workflow: %v", err)
	}
	if err := store.UpdateProject(project.ID, "Project Updated", "desc", updatedRepo, updatedWorkflow); err != nil {
		t.Fatalf("UpdateProject failed: %v", err)
	}
	project, err = store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject after update failed: %v", err)
	}
	if project.Name != "Project Updated" || project.RepoPath != updatedRepo || project.WorkflowPath != updatedWorkflow {
		t.Fatalf("unexpected updated project: %+v", project)
	}

	epic, err := store.CreateEpic(project.ID, "Epic", "desc")
	if err != nil {
		t.Fatalf("CreateEpic failed: %v", err)
	}
	if err := store.UpdateEpic(epic.ID, project.ID, "Epic Updated", "updated"); err != nil {
		t.Fatalf("UpdateEpic failed: %v", err)
	}
	epic, err = store.GetEpic(epic.ID)
	if err != nil {
		t.Fatalf("GetEpic after update failed: %v", err)
	}
	if epic.Name != "Epic Updated" {
		t.Fatalf("unexpected updated epic: %+v", epic)
	}

	issue, err := store.CreateIssue(project.ID, epic.ID, "Tracked", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(issue.ID, WorkflowPhaseReview); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase failed: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after phase update failed: %v", err)
	}
	if issue.WorkflowPhase != WorkflowPhaseReview {
		t.Fatalf("expected review phase, got %s", issue.WorkflowPhase)
	}

	for _, kind := range []string{"run_started", "tick", "manual_retry_requested"} {
		if err := store.AppendRuntimeEvent(kind, map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    1,
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s) failed: %v", kind, err)
		}
	}
	events, err := store.ListRuntimeEvents(0, 10)
	if err != nil {
		t.Fatalf("ListRuntimeEvents failed: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("expected runtime events, got %d", len(events))
	}
	if events[len(events)-1].Kind != "manual_retry_requested" {
		t.Fatalf("unexpected runtime event ordering: %#v", events)
	}

	if err := store.DeleteEpic(epic.ID); err != nil {
		t.Fatalf("DeleteEpic failed: %v", err)
	}
	if _, err := store.GetEpic(epic.ID); err == nil {
		t.Fatal("expected deleted epic lookup to fail")
	}
}

func TestHelperUtilities(t *testing.T) {
	if min(2, 5) != 2 || min(9, 3) != 3 {
		t.Fatal("expected min helper to pick the smaller value")
	}
	if asInt(4) != 4 || asInt(int64(5)) != 5 || asInt(float64(3)) != 3 {
		t.Fatal("expected asInt helper to decode common numeric forms")
	}
}
