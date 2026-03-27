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

func TestSharedPendingInterruptsAppendsDerivedAlertsAfterQueuedItemsInDispatchOrder(t *testing.T) {
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

	fixture.orch.mu.Lock()
	fixture.orch.pendingInteractions[queued.ID] = pendingInteractionEntry{interaction: queued}
	fixture.orch.pendingInteractionOrder = []string{queued.ID}
	fixture.orch.mu.Unlock()

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 3 {
		t.Fatalf("expected queued item plus two derived alerts, got %+v", snapshot.Items)
	}
	if snapshot.Items[0].ID != queued.ID {
		t.Fatalf("expected queued interaction first, got %+v", snapshot.Items)
	}
	if snapshot.Items[1].Kind != appserver.PendingInteractionKindAlert || snapshot.Items[1].IssueID != fixture.first.ID {
		t.Fatalf("expected first derived alert for highest-priority issue, got %+v", snapshot.Items[1])
	}
	if snapshot.Items[2].Kind != appserver.PendingInteractionKindAlert || snapshot.Items[2].IssueID != fixture.second.ID {
		t.Fatalf("expected second derived alert for next issue, got %+v", snapshot.Items[2])
	}
}

func TestSharedPendingInterruptsOrdersPlanApprovalsAfterEarlierAlerts(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	requestedAt := time.Now().UTC().Add(1 * time.Hour)
	if err := fixture.store.SetIssuePendingPlanApproval(fixture.second.ID, "Review the proposed plan.", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 2 {
		t.Fatalf("expected one derived alert and one plan approval, got %+v", snapshot.Items)
	}
	if snapshot.Items[0].Kind != appserver.PendingInteractionKindAlert || snapshot.Items[0].IssueID != fixture.first.ID {
		t.Fatalf("expected earlier alert first, got %+v", snapshot.Items[0])
	}
	if snapshot.Items[1].Kind != appserver.PendingInteractionKindApproval || snapshot.Items[1].IssueID != fixture.second.ID {
		t.Fatalf("expected later plan approval second, got %+v", snapshot.Items[1])
	}
	if !snapshot.Items[0].RequestedAt.Before(snapshot.Items[1].RequestedAt) {
		t.Fatalf("expected alert requested_at to be earlier than plan approval, got %+v", snapshot.Items)
	}
}

func TestPendingInterruptForIssuePrefersQueuedInteractionOverDerivedAlert(t *testing.T) {
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

	fixture.orch.mu.Lock()
	fixture.orch.pendingInteractions[queued.ID] = pendingInteractionEntry{interaction: queued}
	fixture.orch.pendingInteractionOrder = []string{queued.ID}
	fixture.orch.mu.Unlock()

	interaction, ok := fixture.orch.PendingInterruptForIssue(fixture.first.ID, fixture.first.Identifier)
	if !ok {
		t.Fatal("expected pending interrupt for issue")
	}
	if interaction.ID != queued.ID || interaction.Kind != appserver.PendingInteractionKindUserInput {
		t.Fatalf("expected queued interaction to win over derived alert, got %+v", interaction)
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

	fixture.orch.mu.Lock()
	fixture.orch.pendingInteractions[queued.ID] = pendingInteractionEntry{interaction: queued}
	fixture.orch.pendingInteractionOrder = []string{queued.ID}
	fixture.orch.mu.Unlock()

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

	fixture.orch.mu.Lock()
	fixture.orch.pendingInteractions[queued.ID] = pendingInteractionEntry{interaction: queued}
	fixture.orch.pendingInteractionOrder = []string{queued.ID}
	fixture.orch.mu.Unlock()

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

	fixture.orch.scopedRepoPath = filepath.Join(t.TempDir(), "different-scope")
	if err := mkdirAll(fixture.orch.scopedRepoPath); err != nil {
		t.Fatalf("mkdirAll new scope repo: %v", err)
	}

	snapshot = fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 2 {
		t.Fatalf("expected fingerprint change to restore both alerts, got %+v", snapshot.Items)
	}
	if snapshot.Items[0].ID == acknowledgedID {
		t.Fatalf("expected changed fingerprint to create a fresh alert id, got %+v", snapshot.Items[0])
	}
}

func TestAcknowledgeInterruptReappearsWhenResolvedBlockerReturns(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

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

	returnedRepo := filepath.Join(t.TempDir(), "project-repo-returned")
	if err := mkdirAll(returnedRepo); err != nil {
		t.Fatalf("mkdirAll returned repo: %v", err)
	}
	if err := fixture.store.UpdateProject(fixture.project.ID, fixture.project.Name, fixture.project.Description, returnedRepo, ""); err != nil {
		t.Fatalf("UpdateProject out of scope again: %v", err)
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

	fixture.orch.mu.Lock()
	fixture.orch.pendingInteractions[first.ID] = pendingInteractionEntry{
		interaction: first,
		respond: func(ctx context.Context, interactionID string, response appserver.PendingInteractionResponse) error {
			return nil
		},
	}
	fixture.orch.pendingInteractions[second.ID] = pendingInteractionEntry{
		interaction: second,
		respond: func(ctx context.Context, interactionID string, response appserver.PendingInteractionResponse) error {
			return nil
		},
	}
	fixture.orch.pendingInteractionOrder = []string{first.ID, second.ID}
	fixture.orch.mu.Unlock()

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

	fixture.orch.mu.Lock()
	fixture.orch.pendingInteractions[queued.ID] = pendingInteractionEntry{interaction: queued}
	fixture.orch.pendingInteractionOrder = []string{queued.ID}
	fixture.orch.mu.Unlock()

	err := fixture.orch.AcknowledgeInterrupt(context.Background(), queued.ID)
	if !errors.Is(err, appserver.ErrInvalidInteractionResponse) {
		t.Fatalf("expected invalid interaction response error, got %v", err)
	}
}
