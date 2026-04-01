package kanban

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadIssuePlanningMapByIDsHandlesEmptyInputAndOpenAndClosedSessions(t *testing.T) {
	store := setupTestStore(t)

	if got, err := store.loadIssuePlanningMapByIDs([]string{" ", "\t"}); err != nil || len(got) != 0 {
		t.Fatalf("loadIssuePlanningMapByIDs(empty) = %#v, %v", got, err)
	}

	openIssue, err := store.CreateIssue("", "", "Open planning issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue open: %v", err)
	}
	closedIssue, err := store.CreateIssue("", "", "Closed planning issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue closed: %v", err)
	}

	requestedAt := time.Date(2026, 3, 20, 11, 0, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApprovalWithContext(openIssue, "Draft the open plan", requestedAt, 1, "thread-open", "turn-open"); err != nil {
		t.Fatalf("SetIssuePendingPlanApprovalWithContext open: %v", err)
	}
	if err := store.SetIssuePendingPlanApprovalWithContext(openIssue, "Draft the open plan v2", requestedAt.Add(15*time.Minute), 2, "thread-open-2", "turn-open-2"); err != nil {
		t.Fatalf("SetIssuePendingPlanApprovalWithContext open v2: %v", err)
	}
	if err := store.SetIssuePendingPlanRevision(openIssue.ID, "Tighten the open plan", requestedAt.Add(time.Minute)); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision open: %v", err)
	}

	if err := store.SetIssuePendingPlanApprovalWithContext(closedIssue, "Draft the closed plan", requestedAt, 1, "thread-closed", "turn-closed"); err != nil {
		t.Fatalf("SetIssuePendingPlanApprovalWithContext closed: %v", err)
	}
	if err := store.ClearIssuePendingPlanApproval(closedIssue.ID, "manual_retry"); err != nil {
		t.Fatalf("ClearIssuePendingPlanApproval closed: %v", err)
	}

	noSessionIssue, err := store.CreateIssue("", "", "No planning session issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue no session: %v", err)
	}
	if got, err := store.loadIssuePlanningMapByIDs([]string{noSessionIssue.ID}); err != nil || len(got) != 0 {
		t.Fatalf("loadIssuePlanningMapByIDs(no sessions) = %#v, %v", got, err)
	}

	planningByIssue, err := store.loadIssuePlanningMapByIDs([]string{"", openIssue.ID, openIssue.ID, closedIssue.ID, "missing"})
	if err != nil {
		t.Fatalf("loadIssuePlanningMapByIDs: %v", err)
	}
	if len(planningByIssue) != 2 {
		t.Fatalf("expected two planning sessions, got %#v", planningByIssue)
	}

	openPlanning := planningByIssue[openIssue.ID]
	if openPlanning == nil {
		t.Fatalf("expected open planning for %s", openIssue.ID)
	}
	if openPlanning.Status != IssuePlanningStatusRevisionRequested {
		t.Fatalf("expected open planning to remain revision requested, got %#v", openPlanning)
	}
	if openPlanning.ClosedAt != nil {
		t.Fatalf("expected open planning to stay open, got %#v", openPlanning)
	}
	if openPlanning.CurrentVersion == nil || openPlanning.CurrentVersionNumber != 2 || openPlanning.CurrentVersion.Markdown != "Draft the open plan v2" {
		t.Fatalf("unexpected open planning version state: %#v", openPlanning)
	}
	if openPlanning.PendingRevisionNote != "Tighten the open plan" {
		t.Fatalf("unexpected open planning revision note: %#v", openPlanning)
	}
	if len(openPlanning.Versions) != 2 {
		t.Fatalf("expected both open planning versions to be preserved, got %#v", openPlanning)
	}

	closedPlanning := planningByIssue[closedIssue.ID]
	if closedPlanning == nil {
		t.Fatalf("expected closed planning for %s", closedIssue.ID)
	}
	if closedPlanning.ClosedAt == nil || closedPlanning.ClosedReason != "manual_retry" {
		t.Fatalf("unexpected closed planning state: %#v", closedPlanning)
	}
	if closedPlanning.CurrentVersion == nil || len(closedPlanning.Versions) == 0 {
		t.Fatalf("expected closed planning versions to be preserved, got %#v", closedPlanning)
	}
}

func TestLoadIssuePlanningMapByIDsSurfacesQueryFailures(t *testing.T) {
	t.Run("session query failure", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "planning-query.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Faulty planning map query", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}
		store := openFaultySQLiteStoreAt(t, dbPath, "from issue_plan_sessions")
		if err := store.configureConnection(); err != nil {
			t.Fatalf("configureConnection: %v", err)
		}
		if _, err := store.loadIssuePlanningMapByIDs([]string{issue.ID}); err == nil {
			t.Fatal("expected loadIssuePlanningMapByIDs to fail on session query")
		}
	})

	t.Run("version query failure", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "planning-version.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Faulty planning map version query", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		requestedAt := time.Date(2026, 3, 20, 12, 30, 0, 0, time.UTC)
		if err := base.SetIssuePendingPlanApprovalWithContext(issue, "Draft the version query failure", requestedAt, 1, "thread-version", "turn-version"); err != nil {
			t.Fatalf("SetIssuePendingPlanApprovalWithContext: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}
		store := openFaultySQLiteStoreAt(t, dbPath, "from issue_plan_versions")
		if err := store.configureConnection(); err != nil {
			t.Fatalf("configureConnection: %v", err)
		}
		if _, err := store.loadIssuePlanningMapByIDs([]string{issue.ID}); err == nil {
			t.Fatal("expected loadIssuePlanningMapByIDs to fail on version query")
		}
	})
}
