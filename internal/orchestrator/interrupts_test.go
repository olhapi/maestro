package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
)

type sharedInterruptFixture struct {
	orch      *Orchestrator
	store     *kanban.Store
	project   *kanban.Project
	first     *kanban.Issue
	second    *kanban.Issue
	scopeRepo string
	dbPath    string
}

func setupSharedInterruptFixture(t *testing.T) sharedInterruptFixture {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	projectRepo := filepath.Join(t.TempDir(), "project-repo")
	if err := mkdirAll(projectRepo); err != nil {
		t.Fatalf("mkdirAll project repo: %v", err)
	}
	scopeRepo := filepath.Join(t.TempDir(), "scoped-repo")
	if err := mkdirAll(scopeRepo); err != nil {
		t.Fatalf("mkdirAll scope repo: %v", err)
	}

	project, err := store.CreateProject("Scoped project", "", projectRepo, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState: %v", err)
	}

	first, err := store.CreateIssue(project.ID, "", "First blocked issue", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue first: %v", err)
	}
	if err := store.UpdateIssueState(first.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState first: %v", err)
	}

	second, err := store.CreateIssue(project.ID, "", "Second blocked issue", "", 2, nil)
	if err != nil {
		t.Fatalf("CreateIssue second: %v", err)
	}
	if err := store.UpdateIssueState(second.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState second: %v", err)
	}

	return sharedInterruptFixture{
		orch:      NewSharedWithExtensions(store, nil, scopeRepo, ""),
		store:     store,
		project:   project,
		first:     first,
		second:    second,
		scopeRepo: scopeRepo,
		dbPath:    dbPath,
	}
}

func mkdirAll(path string) error {
	return os.MkdirAll(path, 0o755)
}

func registerRunningPendingInteraction(t *testing.T, orch *Orchestrator, issue *kanban.Issue, attempt int, phase kanban.WorkflowPhase, interaction appserver.PendingInteraction, responder appserver.InteractionResponder) {
	t.Helper()

	runningIssue := *issue
	runningIssue.State = kanban.StateInProgress
	runningIssue.WorkflowPhase = phase

	orch.mu.Lock()
	orch.running[issue.ID] = runningEntry{
		issue:   runningIssue,
		attempt: attempt,
		phase:   phase,
		cancel:  func() {},
	}
	orch.mu.Unlock()

	orch.registerPendingInteraction(issue.ID, &interaction, responder)
}

func TestSharedPendingInterruptsAppendsQueuedItemsBeforeDerivedAlertsInDispatchOrder(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)
	third, err := fixture.store.CreateIssue(fixture.project.ID, "", "Third blocked issue", "", 3, nil)
	if err != nil {
		t.Fatalf("CreateIssue third: %v", err)
	}
	if err := fixture.store.UpdateIssueState(third.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState third: %v", err)
	}

	queued := appserver.PendingInteraction{
		ID:              "queued-approval",
		Kind:            appserver.PendingInteractionKindApproval,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		Approval: &appserver.PendingApproval{
			Decisions: []appserver.PendingApprovalDecision{{
				Value: "approved",
				Label: "Approve once",
			}},
		},
	}

	registerRunningPendingInteraction(t, fixture.orch, fixture.first, 1, kanban.WorkflowPhaseImplementation, queued, nil)

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 3 {
		t.Fatalf("expected queued item plus two derived alerts, got %+v", snapshot.Items)
	}
	if snapshot.Items[0].ID != queued.ID {
		t.Fatalf("expected queued interaction first, got %+v", snapshot.Items)
	}
	if snapshot.Items[1].Kind != appserver.PendingInteractionKindAlert || snapshot.Items[1].IssueID != fixture.second.ID {
		t.Fatalf("expected first derived alert for highest-priority issue, got %+v", snapshot.Items[1])
	}
	if snapshot.Items[2].Kind != appserver.PendingInteractionKindAlert || snapshot.Items[2].IssueID != third.ID {
		t.Fatalf("expected second derived alert for next issue, got %+v", snapshot.Items[2])
	}
}

func TestPendingInterruptForIssueReturnsQueuedInteractionForRunningIssue(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	queued := appserver.PendingInteraction{
		ID:              "queued-input",
		Kind:            appserver.PendingInteractionKindUserInput,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		UserInput: &appserver.PendingUserInput{
			Questions: []appserver.PendingUserInputQuestion{{
				ID: "path",
			}},
		},
	}

	registerRunningPendingInteraction(t, fixture.orch, fixture.first, 1, kanban.WorkflowPhaseImplementation, queued, nil)

	interaction, ok := fixture.orch.PendingInterruptForIssue(fixture.first.ID, fixture.first.Identifier)
	if !ok {
		t.Fatal("expected pending interrupt for issue")
	}
	if interaction.ID != queued.ID || interaction.Kind != appserver.PendingInteractionKindUserInput {
		t.Fatalf("expected queued interaction to win over derived alert, got %+v", interaction)
	}
}

func TestPendingInterruptForIssueReturnsDerivedAlertForBlockedIssue(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	interaction, ok := fixture.orch.PendingInterruptForIssue(fixture.first.ID, fixture.first.Identifier)
	if !ok {
		t.Fatal("expected pending interrupt for blocked issue")
	}
	if interaction.Kind != appserver.PendingInteractionKindAlert || interaction.IssueID != fixture.first.ID {
		t.Fatalf("expected derived alert for blocked issue, got %+v", interaction)
	}
}

func TestPendingInterruptsKeepsQueuedItemsWhenDerivedAlertLookupFails(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	queued := appserver.PendingInteraction{
		ID:              "queued-approval",
		Kind:            appserver.PendingInteractionKindApproval,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		Approval: &appserver.PendingApproval{
			Decisions: []appserver.PendingApprovalDecision{{
				Value: "approved",
				Label: "Approve once",
			}},
		},
	}

	registerRunningPendingInteraction(t, fixture.orch, fixture.first, 1, kanban.WorkflowPhaseImplementation, queued, nil)

	db, err := sql.Open("sqlite3", fixture.dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE interrupt_acknowledgements`); err != nil {
		t.Fatalf("DROP TABLE interrupt_acknowledgements: %v", err)
	}

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 1 || snapshot.Items[0].ID != queued.ID {
		t.Fatalf("expected queued item to remain visible after derived alert failure, got %+v", snapshot.Items)
	}
}

func TestPendingInterruptForIssueKeepsQueuedItemsWhenDerivedAlertLookupFails(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	queued := appserver.PendingInteraction{
		ID:              "queued-input",
		Kind:            appserver.PendingInteractionKindUserInput,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		UserInput: &appserver.PendingUserInput{
			Questions: []appserver.PendingUserInputQuestion{{
				ID: "path",
			}},
		},
	}

	registerRunningPendingInteraction(t, fixture.orch, fixture.first, 1, kanban.WorkflowPhaseImplementation, queued, nil)

	db, err := sql.Open("sqlite3", fixture.dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE interrupt_acknowledgements`); err != nil {
		t.Fatalf("DROP TABLE interrupt_acknowledgements: %v", err)
	}

	interaction, ok := fixture.orch.PendingInterruptForIssue(fixture.first.ID, fixture.first.Identifier)
	if !ok {
		t.Fatal("expected queued pending interrupt after derived alert failure")
	}
	if interaction.ID != queued.ID || interaction.Kind != appserver.PendingInteractionKindUserInput {
		t.Fatalf("expected queued interaction to remain available, got %+v", interaction)
	}
}

func TestAcknowledgeInterruptFiltersOnlyTargetedDerivedAlert(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 2 {
		t.Fatalf("expected two derived alerts, got %+v", snapshot.Items)
	}

	firstAlertID := snapshot.Items[0].ID
	if err := fixture.orch.AcknowledgeInterrupt(context.Background(), firstAlertID); err != nil {
		t.Fatalf("AcknowledgeInterrupt: %v", err)
	}

	acknowledged, err := fixture.store.InterruptAcknowledged(firstAlertID)
	if err != nil {
		t.Fatalf("InterruptAcknowledged: %v", err)
	}
	if !acknowledged {
		t.Fatalf("expected %s to be persisted as acknowledged", firstAlertID)
	}

	snapshot = fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 1 {
		t.Fatalf("expected only one derived alert after acknowledgement, got %+v", snapshot.Items)
	}
	if snapshot.Items[0].IssueID != fixture.second.ID {
		t.Fatalf("expected second issue alert to remain visible, got %+v", snapshot.Items[0])
	}
}

func TestAcknowledgeInterruptReappearsWhenDerivedFingerprintChanges(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 2 {
		t.Fatalf("expected derived alerts, got %+v", snapshot.Items)
	}
	acknowledgedID := snapshot.Items[0].ID
	if err := fixture.orch.AcknowledgeInterrupt(context.Background(), acknowledgedID); err != nil {
		t.Fatalf("AcknowledgeInterrupt: %v", err)
	}

	differentRepo := filepath.Join(t.TempDir(), "different-project-repo")
	if err := mkdirAll(differentRepo); err != nil {
		t.Fatalf("mkdirAll different project repo: %v", err)
	}
	if err := fixture.store.UpdateProject(fixture.project.ID, fixture.project.Name, fixture.project.Description, differentRepo, ""); err != nil {
		t.Fatalf("UpdateProject different repo: %v", err)
	}

	snapshot = fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 2 {
		t.Fatalf("expected fingerprint change to restore both alerts, got %+v", snapshot.Items)
	}
	if snapshot.Items[0].ID == acknowledgedID {
		t.Fatalf("expected changed fingerprint to create a fresh alert id, got %+v", snapshot.Items[0])
	}
}

func TestAcknowledgeInterruptStaysHiddenWhenIssueUpdates(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 2 {
		t.Fatalf("expected derived alerts, got %+v", snapshot.Items)
	}
	acknowledgedID := snapshot.Items[0].ID
	if err := fixture.orch.AcknowledgeInterrupt(context.Background(), acknowledgedID); err != nil {
		t.Fatalf("AcknowledgeInterrupt: %v", err)
	}

	if err := fixture.store.UpdateIssue(fixture.first.ID, map[string]interface{}{
		"title": "First blocked issue updated",
	}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	snapshot = fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 1 {
		t.Fatalf("expected acknowledged alert to stay hidden after issue update, got %+v", snapshot.Items)
	}
	if snapshot.Items[0].IssueID != fixture.second.ID {
		t.Fatalf("expected only the second issue alert to remain visible, got %+v", snapshot.Items[0])
	}
}

func TestAcknowledgeInterruptReappearsWhenResolvedBlockerReturns(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)
	originalRepoPath := fixture.project.RepoPath

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 2 {
		t.Fatalf("expected derived alerts, got %+v", snapshot.Items)
	}
	acknowledgedID := snapshot.Items[0].ID
	if err := fixture.orch.AcknowledgeInterrupt(context.Background(), acknowledgedID); err != nil {
		t.Fatalf("AcknowledgeInterrupt: %v", err)
	}

	if err := fixture.store.UpdateProject(fixture.project.ID, fixture.project.Name, fixture.project.Description, fixture.scopeRepo, ""); err != nil {
		t.Fatalf("UpdateProject in scope: %v", err)
	}
	snapshot = fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 0 {
		t.Fatalf("expected resolved blocker to remove derived alerts, got %+v", snapshot.Items)
	}
	if acknowledged, err := fixture.store.InterruptAcknowledged(acknowledgedID); err != nil {
		t.Fatalf("InterruptAcknowledged: %v", err)
	} else if acknowledged {
		t.Fatalf("expected resolved blocker to prune acknowledgement for %s", acknowledgedID)
	}

	if err := fixture.store.UpdateProject(fixture.project.ID, fixture.project.Name, fixture.project.Description, originalRepoPath, ""); err != nil {
		t.Fatalf("UpdateProject restore original repo: %v", err)
	}

	snapshot = fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 2 {
		t.Fatalf("expected recurring blocker to restore alerts, got %+v", snapshot.Items)
	}
	if snapshot.Items[0].ID != acknowledgedID {
		t.Fatalf("expected recurring blocker to restore original alert id %s, got %+v", acknowledgedID, snapshot.Items[0])
	}
}

func TestDerivedAlertsSkipPausedIssues(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	fixture.orch.mu.Lock()
	fixture.orch.paused[fixture.first.ID] = pausedEntry{
		IssueState: string(fixture.first.State),
		Attempt:    2,
		Identifier: fixture.first.Identifier,
		Phase:      string(kanban.WorkflowPhaseImplementation),
		PausedAt:   time.Now().UTC(),
		Error:      "run_paused",
	}
	fixture.orch.mu.Unlock()

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 1 {
		t.Fatalf("expected only one derived alert after pausing the first issue, got %+v", snapshot.Items)
	}
	if snapshot.Items[0].IssueID != fixture.second.ID {
		t.Fatalf("expected only the non-paused issue to surface a derived alert, got %+v", snapshot.Items[0])
	}
}

func TestRespondToInterruptRejectsDerivedAlerts(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) == 0 {
		t.Fatal("expected derived alert")
	}

	err := fixture.orch.RespondToInterrupt(context.Background(), snapshot.Items[0].ID, appserver.PendingInteractionResponse{
		Decision: "approved",
	})
	if !errors.Is(err, appserver.ErrInvalidInteractionResponse) {
		t.Fatalf("expected invalid interaction response error, got %v", err)
	}
}

func TestRespondToInterruptRejectsQueuedItemsBehindQueueHead(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	first := appserver.PendingInteraction{
		ID:              "queued-approval-1",
		Kind:            appserver.PendingInteractionKindApproval,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		Approval: &appserver.PendingApproval{
			Decisions: []appserver.PendingApprovalDecision{{
				Value: "approved",
				Label: "Approve once",
			}},
		},
	}
	second := appserver.PendingInteraction{
		ID:              "queued-approval-2",
		Kind:            appserver.PendingInteractionKindApproval,
		IssueID:         fixture.second.ID,
		IssueIdentifier: fixture.second.Identifier,
		IssueTitle:      fixture.second.Title,
		RequestedAt:     time.Now().UTC(),
		Approval: &appserver.PendingApproval{
			Decisions: []appserver.PendingApprovalDecision{{
				Value: "approved",
				Label: "Approve once",
			}},
		},
	}

	registerRunningPendingInteraction(t, fixture.orch, fixture.first, 1, kanban.WorkflowPhaseImplementation, first, func(ctx context.Context, interactionID string, response appserver.PendingInteractionResponse) error {
		return nil
	})
	registerRunningPendingInteraction(t, fixture.orch, fixture.second, 1, kanban.WorkflowPhaseImplementation, second, func(ctx context.Context, interactionID string, response appserver.PendingInteractionResponse) error {
		return nil
	})

	err := fixture.orch.RespondToInterrupt(context.Background(), second.ID, appserver.PendingInteractionResponse{
		Decision: "approved",
	})
	if !errors.Is(err, appserver.ErrPendingInteractionConflict) {
		t.Fatalf("expected conflict for queued non-head interaction, got %v", err)
	}
}

func TestAcknowledgeInterruptRejectsQueuedApprovals(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	queued := appserver.PendingInteraction{
		ID:              "queued-approval",
		Kind:            appserver.PendingInteractionKindApproval,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		Approval: &appserver.PendingApproval{
			Decisions: []appserver.PendingApprovalDecision{{
				Value: "approved",
				Label: "Approve once",
			}},
		},
	}

	registerRunningPendingInteraction(t, fixture.orch, fixture.first, 1, kanban.WorkflowPhaseImplementation, queued, nil)

	err := fixture.orch.AcknowledgeInterrupt(context.Background(), queued.ID)
	if !errors.Is(err, appserver.ErrInvalidInteractionResponse) {
		t.Fatalf("expected invalid interaction response error, got %v", err)
	}
}
