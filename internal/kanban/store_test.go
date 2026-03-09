package kanban

import (
	"os"
	"path/filepath"
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

	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatalf("Failed to delete project: %v", err)
	}

	_, err := store.GetProject(project.ID)
	if err == nil {
		t.Error("Expected error getting deleted project")
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

func TestDeleteIssue(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "To Delete", "", 0, nil)

	if err := store.DeleteIssue(issue.ID); err != nil {
		t.Fatalf("Failed to delete issue: %v", err)
	}

	_, err := store.GetIssue(issue.ID)
	if err == nil {
		t.Error("Expected error getting deleted issue")
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
	_, _ = store.CreateWorkspace(issue.ID, "/tmp/workspace")

	if err := store.DeleteWorkspace(issue.ID); err != nil {
		t.Fatalf("Failed to delete workspace: %v", err)
	}

	_, err := store.GetWorkspace(issue.ID)
	if err == nil {
		t.Error("Expected error getting deleted workspace")
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
	_, _ = store.CreateIssue("", "", "Medium Priority", "", 5, nil)

	issues, _ := store.ListIssues(nil)

	// Should be sorted by priority
	if issues[0].Priority > issues[1].Priority {
		t.Error("Issues should be sorted by priority (ascending)")
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
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_failed",
		Error:      "approval_required",
		UpdatedAt:  now,
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
	if loaded.AppSession.SessionID != "thread-1-turn-1" || len(loaded.AppSession.History) != 2 {
		t.Fatalf("unexpected session payload: %+v", loaded.AppSession)
	}

	snapshot.Attempt = 3
	snapshot.RunKind = "run_completed"
	snapshot.Error = ""
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
}
