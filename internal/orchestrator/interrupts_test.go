package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
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

	queued := agentruntime.PendingInteraction{
		ID:              "queued-approval",
		Kind:            agentruntime.PendingInteractionKindApproval,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		Approval: &agentruntime.PendingApproval{
			Decisions: []agentruntime.PendingApprovalDecision{{
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
	if snapshot.Items[1].Kind != agentruntime.PendingInteractionKindAlert || snapshot.Items[1].IssueID != fixture.first.ID {
		t.Fatalf("expected first derived alert for highest-priority issue, got %+v", snapshot.Items[1])
	}
	if snapshot.Items[2].Kind != agentruntime.PendingInteractionKindAlert || snapshot.Items[2].IssueID != fixture.second.ID {
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
	if snapshot.Items[0].Kind != agentruntime.PendingInteractionKindAlert || snapshot.Items[0].IssueID != fixture.first.ID {
		t.Fatalf("expected earlier alert first, got %+v", snapshot.Items[0])
	}
	if snapshot.Items[1].Kind != agentruntime.PendingInteractionKindApproval || snapshot.Items[1].IssueID != fixture.second.ID {
		t.Fatalf("expected later plan approval second, got %+v", snapshot.Items[1])
	}
	if !snapshot.Items[0].RequestedAt.Before(snapshot.Items[1].RequestedAt) {
		t.Fatalf("expected alert requested_at to be earlier than plan approval, got %+v", snapshot.Items)
	}
}

func TestSharedPendingInterruptsSuppressPlanApprovalWhileRevisionIsQueued(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	requestedAt := time.Now().UTC().Add(1 * time.Hour)
	if err := fixture.store.SetIssuePendingPlanApproval(fixture.second.ID, "Review the proposed plan.", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}
	if err := fixture.store.SetIssuePendingPlanRevision(
		fixture.second.ID,
		"Tighten the rollout and keep the rollback explicit.",
		requestedAt.Add(2*time.Minute),
	); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision: %v", err)
	}

	snapshot := fixture.orch.PendingInterrupts()
	if len(snapshot.Items) != 1 {
		t.Fatalf("expected only the earlier derived alert while revision is queued, got %+v", snapshot.Items)
	}
	if snapshot.Items[0].Kind != agentruntime.PendingInteractionKindAlert || snapshot.Items[0].IssueID != fixture.first.ID {
		t.Fatalf("expected derived alert to remain visible, got %+v", snapshot.Items[0])
	}
}

func TestPendingInterruptForIssuePrefersQueuedInteractionOverDerivedAlert(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	queued := agentruntime.PendingInteraction{
		ID:              "queued-input",
		Kind:            agentruntime.PendingInteractionKindUserInput,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		UserInput: &agentruntime.PendingUserInput{
			Questions: []agentruntime.PendingUserInputQuestion{{
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
	if interaction.ID != queued.ID || interaction.Kind != agentruntime.PendingInteractionKindUserInput {
		t.Fatalf("expected queued interaction to win over derived alert, got %+v", interaction)
	}
}

func TestPendingInterruptsKeepsQueuedItemsWhenDerivedAlertLookupFails(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	queued := agentruntime.PendingInteraction{
		ID:              "queued-approval",
		Kind:            agentruntime.PendingInteractionKindApproval,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		Approval: &agentruntime.PendingApproval{
			Decisions: []agentruntime.PendingApprovalDecision{{
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

	queued := agentruntime.PendingInteraction{
		ID:              "queued-input",
		Kind:            agentruntime.PendingInteractionKindUserInput,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		UserInput: &agentruntime.PendingUserInput{
			Questions: []agentruntime.PendingUserInputQuestion{{
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
	if interaction.ID != queued.ID || interaction.Kind != agentruntime.PendingInteractionKindUserInput {
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

	err := fixture.orch.RespondToInterrupt(context.Background(), snapshot.Items[0].ID, agentruntime.PendingInteractionResponse{
		Decision: "approved",
	})
	if !errors.Is(err, agentruntime.ErrInvalidInteractionResponse) {
		t.Fatalf("expected invalid interaction response error, got %v", err)
	}
}

func TestRespondToInterruptRejectsQueuedItemsBehindQueueHead(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	first := agentruntime.PendingInteraction{
		ID:              "queued-approval-1",
		Kind:            agentruntime.PendingInteractionKindApproval,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		Approval: &agentruntime.PendingApproval{
			Decisions: []agentruntime.PendingApprovalDecision{{
				Value: "approved",
				Label: "Approve once",
			}},
		},
	}
	second := agentruntime.PendingInteraction{
		ID:              "queued-approval-2",
		Kind:            agentruntime.PendingInteractionKindApproval,
		IssueID:         fixture.second.ID,
		IssueIdentifier: fixture.second.Identifier,
		IssueTitle:      fixture.second.Title,
		RequestedAt:     time.Now().UTC(),
		Approval: &agentruntime.PendingApproval{
			Decisions: []agentruntime.PendingApprovalDecision{{
				Value: "approved",
				Label: "Approve once",
			}},
		},
	}

	fixture.orch.mu.Lock()
	fixture.orch.pendingInteractions[first.ID] = pendingInteractionEntry{
		interaction: first,
		respond: func(ctx context.Context, interactionID string, response agentruntime.PendingInteractionResponse) error {
			return nil
		},
	}
	fixture.orch.pendingInteractions[second.ID] = pendingInteractionEntry{
		interaction: second,
		respond: func(ctx context.Context, interactionID string, response agentruntime.PendingInteractionResponse) error {
			return nil
		},
	}
	fixture.orch.pendingInteractionOrder = []string{first.ID, second.ID}
	fixture.orch.mu.Unlock()

	err := fixture.orch.RespondToInterrupt(context.Background(), second.ID, agentruntime.PendingInteractionResponse{
		Decision: "approved",
	})
	if !errors.Is(err, agentruntime.ErrPendingInteractionConflict) {
		t.Fatalf("expected conflict for queued non-head interaction, got %v", err)
	}
}

func TestAcknowledgeInterruptRejectsQueuedApprovals(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	queued := agentruntime.PendingInteraction{
		ID:              "queued-approval",
		Kind:            agentruntime.PendingInteractionKindApproval,
		IssueID:         fixture.first.ID,
		IssueIdentifier: fixture.first.Identifier,
		IssueTitle:      fixture.first.Title,
		RequestedAt:     time.Now().UTC(),
		Approval: &agentruntime.PendingApproval{
			Decisions: []agentruntime.PendingApprovalDecision{{
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
	if !errors.Is(err, agentruntime.ErrInvalidInteractionResponse) {
		t.Fatalf("expected invalid interaction response error, got %v", err)
	}
}

func TestAcknowledgeInterruptReturnsNotFoundForMissingItem(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	if err := fixture.orch.AcknowledgeInterrupt(context.Background(), "missing-interrupt"); !errors.Is(err, agentruntime.ErrPendingInteractionNotFound) {
		t.Fatalf("expected missing interrupt to return not found, got %v", err)
	}
}

func TestInterruptHelperBranches(t *testing.T) {
	t.Run("sort and lookup helpers", func(t *testing.T) {
		firstTime := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
		items := []agentruntime.PendingInteraction{
			{ID: "b", RequestedAt: firstTime},
			{ID: "a", RequestedAt: firstTime},
			{ID: "c", RequestedAt: firstTime.Add(1 * time.Minute)},
		}
		sortPendingInteractionsByRequestedAt(items)
		if items[0].ID != "a" || items[1].ID != "b" || items[2].ID != "c" {
			t.Fatalf("unexpected sorted order: %#v", items)
		}

		fixture := setupSharedInterruptFixture(t)
		queued := agentruntime.PendingInteraction{
			ID:              "queued-input",
			Kind:            agentruntime.PendingInteractionKindUserInput,
			IssueID:         fixture.first.ID,
			IssueIdentifier: fixture.first.Identifier,
			IssueTitle:      fixture.first.Title,
			RequestedAt:     time.Now().UTC(),
			UserInput: &agentruntime.PendingUserInput{
				Questions: []agentruntime.PendingUserInputQuestion{{ID: "path"}},
			},
		}
		fixture.orch.mu.Lock()
		fixture.orch.pendingInteractions[queued.ID] = pendingInteractionEntry{interaction: queued}
		fixture.orch.pendingInteractionOrder = []string{queued.ID}
		fixture.orch.mu.Unlock()

		interaction, found, err := fixture.orch.pendingInteractionByID("")
		if err != nil || found || interaction != nil {
			t.Fatalf("expected blank interaction lookup to be ignored, got interaction=%#v found=%v err=%v", interaction, found, err)
		}
		interaction, found, err = fixture.orch.pendingInteractionByID(queued.ID)
		if err != nil || !found || interaction == nil || interaction.ID != queued.ID {
			t.Fatalf("expected queued interaction lookup to succeed, got interaction=%#v found=%v err=%v", interaction, found, err)
		}

		closedStore, err := kanban.NewStore(filepath.Join(t.TempDir(), "closed.db"))
		if err != nil {
			t.Fatalf("kanban.NewStore: %v", err)
		}
		if err := closedStore.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		closedOrch := NewSharedWithExtensions(closedStore, nil, filepath.Join(t.TempDir(), "scope"), "")
		if interaction, found, err := closedOrch.pendingInteractionByID("missing"); err == nil || found || interaction != nil {
			t.Fatalf("expected closed-store lookup to fail, got interaction=%#v found=%v err=%v", interaction, found, err)
		}
	})

	t.Run("derived alert helpers", func(t *testing.T) {
		fixture := setupSharedInterruptFixture(t)
		issue := kanban.Issue{
			ID:         fixture.first.ID,
			Identifier: fixture.first.Identifier,
			Title:      fixture.first.Title,
			UpdatedAt:  time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC),
			State:      kanban.StateReady,
		}
		dispatchState := &kanban.IssueDispatchState{
			ProjectExists:         true,
			ProjectState:          kanban.ProjectStateRunning,
			HasUnresolvedBlockers: false,
		}
		if got := fixture.orch.issueEligibleForDerivedAlert(&issue, dispatchState); !got {
			t.Fatal("expected ready running issue to be eligible for a derived alert")
		}
		if got := fixture.orch.issueEligibleForDerivedAlert(nil, dispatchState); got {
			t.Fatal("expected nil issue to be ineligible")
		}
		if got := fixture.orch.issueEligibleForDerivedAlert(&issue, nil); got {
			t.Fatal("expected missing dispatch state to be ineligible")
		}
		if got := fixture.orch.issueEligibleForDerivedAlert(&issue, &kanban.IssueDispatchState{ProjectExists: false}); got {
			t.Fatal("expected missing project to be ineligible")
		}

		if got := projectScopeDispatchError("", ""); got != "" {
			t.Fatalf("expected blank scope error to be empty, got %q", got)
		}
		if got := projectScopeDispatchError("/repo/a", "/repo/a"); got != "" {
			t.Fatalf("expected identical paths to be empty, got %q", got)
		}
		if got := projectScopeDispatchError("/repo/a", "/repo/b"); !strings.Contains(got, "/repo/b") {
			t.Fatalf("expected mismatched scope error, got %q", got)
		}

		alert := buildIssueProjectDispatchBlockedAlert(issue, &kanban.Project{ID: fixture.project.ID, Name: "Platform"}, "scope mismatch")
		if alert.ID == "" || alert.Kind != agentruntime.PendingInteractionKindAlert || alert.Method != issueProjectDispatchBlockedAlertMethod {
			t.Fatalf("unexpected alert identity: %#v", alert)
		}
		if alert.Alert == nil || alert.Alert.Code != issueProjectDispatchBlockedAlertCode || alert.Alert.Severity != agentruntime.PendingAlertSeverityError {
			t.Fatalf("unexpected alert payload: %#v", alert.Alert)
		}
		if alert.ProjectID != fixture.project.ID || alert.ProjectName != "Platform" || alert.LastActivity != "scope mismatch" {
			t.Fatalf("unexpected project alert metadata: %#v", alert)
		}
		if alert.LastActivityAt == nil || alert.LastActivityAt.IsZero() {
			t.Fatalf("expected alert activity timestamp, got %#v", alert.LastActivityAt)
		}
	})
}

func TestDerivedInterruptHelperBranches(t *testing.T) {
	if got := projectScopeDispatchError("", ""); got != "" {
		t.Fatalf("expected blank scope inputs to produce no error, got %q", got)
	}
	if got := projectScopeDispatchError("/repo", "/repo"); got != "" {
		t.Fatalf("expected matching scope paths to produce no error, got %q", got)
	}
	scopeError := projectScopeDispatchError("/repo", "/scope")
	if scopeError == "" {
		t.Fatal("expected mismatched scope paths to produce an error message")
	}

	issue := kanban.Issue{
		ID:        "issue-1",
		Title:     "Fallback issue title",
		UpdatedAt: time.Time{},
	}
	project := &kanban.Project{ID: "proj-1", Name: "Platform", RepoPath: "/repo"}
	alert := buildIssueProjectDispatchBlockedAlert(issue, nil, scopeError)
	if alert.ProjectName != "Project" || alert.ProjectID != "" || alert.Alert == nil || alert.Alert.Message != scopeError {
		t.Fatalf("unexpected fallback dispatch alert: %#v", alert)
	}
	alert = buildIssueProjectDispatchBlockedAlert(issue, project, scopeError)
	if alert.ProjectName != "Platform" || alert.ProjectID != "proj-1" || alert.Alert == nil || alert.Alert.Message != scopeError {
		t.Fatalf("unexpected project dispatch alert: %#v", alert)
	}

	fixture := setupSharedInterruptFixture(t)
	loadedIssue, err := fixture.store.GetIssue(fixture.first.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	dispatchState, err := fixture.store.GetIssueDispatchState(fixture.first.ID)
	if err != nil {
		t.Fatalf("GetIssueDispatchState: %v", err)
	}

	if fixture.orch.issueEligibleForDerivedAlert(nil, nil) {
		t.Fatal("expected nil inputs to be ineligible for derived alerts")
	}
	if fixture.orch.issueEligibleForDerivedAlert(loadedIssue, nil) {
		t.Fatal("expected nil dispatch state to be ineligible for derived alerts")
	}
	if fixture.orch.issueEligibleForDerivedAlert(loadedIssue, &kanban.IssueDispatchState{ProjectExists: false}) {
		t.Fatal("expected missing project to be ineligible for derived alerts")
	}
	if fixture.orch.issueEligibleForDerivedAlert(loadedIssue, &kanban.IssueDispatchState{ProjectExists: true, ProjectState: kanban.ProjectStateStopped}) {
		t.Fatal("expected stopped project to be ineligible for derived alerts")
	}
	if fixture.orch.issueEligibleForDerivedAlert(loadedIssue, &kanban.IssueDispatchState{ProjectExists: true, ProjectState: kanban.ProjectStateRunning, HasUnresolvedBlockers: true}) {
		t.Fatal("expected blocked issue to be ineligible for derived alerts")
	}
	planApprovalIssue := *loadedIssue
	planApprovalIssue.PlanApprovalPending = true
	if fixture.orch.issueEligibleForDerivedAlert(&planApprovalIssue, &kanban.IssueDispatchState{ProjectExists: true, ProjectState: kanban.ProjectStateRunning}) {
		t.Fatal("expected plan approval pending issue to be ineligible for derived alerts")
	}
	completeIssue := *loadedIssue
	completeIssue.WorkflowPhase = kanban.WorkflowPhaseComplete
	if fixture.orch.issueEligibleForDerivedAlert(&completeIssue, &kanban.IssueDispatchState{ProjectExists: true, ProjectState: kanban.ProjectStateRunning}) {
		t.Fatal("expected complete issue to be ineligible for derived alerts")
	}

	fixture.orch.mu.Lock()
	fixture.orch.running[loadedIssue.ID] = runningEntry{}
	fixture.orch.mu.Unlock()
	if fixture.orch.issueEligibleForDerivedAlert(loadedIssue, &dispatchState) {
		t.Fatal("expected running issue to be ineligible for derived alerts")
	}
	fixture.orch.mu.Lock()
	delete(fixture.orch.running, loadedIssue.ID)
	fixture.orch.paused[loadedIssue.ID] = pausedEntry{}
	fixture.orch.mu.Unlock()
	if fixture.orch.issueEligibleForDerivedAlert(loadedIssue, &dispatchState) {
		t.Fatal("expected paused issue to be ineligible for derived alerts")
	}
	fixture.orch.mu.Lock()
	delete(fixture.orch.paused, loadedIssue.ID)
	fixture.orch.mu.Unlock()
	if !fixture.orch.issueEligibleForDerivedAlert(loadedIssue, &dispatchState) {
		t.Fatal("expected ready running issue to be eligible for derived alerts")
	}
}

func TestPendingInteractionLookupBranches(t *testing.T) {
	fixture := setupSharedInterruptFixture(t)

	if interaction, found, err := fixture.orch.pendingInteractionByID(""); err != nil || found || interaction != nil {
		t.Fatalf("expected blank interaction lookup to return no result, got interaction=%#v found=%v err=%v", interaction, found, err)
	}
	if interaction, found, err := fixture.orch.pendingInteractionByID("missing"); err != nil || found || interaction != nil {
		t.Fatalf("expected missing interaction lookup to return no result, got interaction=%#v found=%v err=%v", interaction, found, err)
	}

	if err := fixture.store.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}
	if interaction, found, err := fixture.orch.pendingInteractionByID("missing"); err == nil || found || interaction != nil {
		t.Fatalf("expected closed-store lookup to fail, got interaction=%#v found=%v err=%v", interaction, found, err)
	}
}
