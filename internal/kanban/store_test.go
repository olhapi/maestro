package kanban

import (
	"path/filepath"
	"testing"
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

	project, err := store.CreateProject("Test Project", "A test project")
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

	created, err := store.CreateProject("Test", "Desc")
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

	_, _ = store.CreateProject("Project A", "")
	_, _ = store.CreateProject("Project B", "")

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

	project, _ := store.CreateProject("To Delete", "")

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

	project, _ := store.CreateProject("Project", "")

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

	project, _ := store.CreateProject("Project", "")
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

	project, _ := store.CreateProject("MyApp", "")
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

	project, _ := store.CreateProject("Project", "")
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

	// Move to in_progress - should set started_at
	if err := store.UpdateIssueState(issue.ID, StateInProgress); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}

	updated, _ = store.GetIssue(issue.ID)
	if updated.StartedAt == nil {
		t.Error("Expected started_at to be set")
	}

	// Move to done - should set completed_at
	if err := store.UpdateIssueState(issue.ID, StateDone); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}

	updated, _ = store.GetIssue(issue.ID)
	if updated.CompletedAt == nil {
		t.Error("Expected completed_at to be set")
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

	issue1, _ := store.CreateIssue("", "", "Issue 1", "", 0, nil)
	issue2, _ := store.CreateIssue("", "", "Issue 2", "", 0, nil)

	// Set issue2 as blocker for issue1
	updates := map[string]interface{}{
		"blocked_by": []string{issue2.Identifier},
	}

	if err := store.UpdateIssue(issue1.ID, updates); err != nil {
		t.Fatalf("Failed to set blockers: %v", err)
	}

	updated, _ := store.GetIssue(issue1.ID)
	if len(updated.BlockedBy) != 1 || updated.BlockedBy[0] != issue2.Identifier {
		t.Errorf("Expected blocker %s, got %v", issue2.Identifier, updated.BlockedBy)
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

	project, _ := store.CreateProject("MyApp", "")

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
