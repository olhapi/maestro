package dashboardapi

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
)

func markPendingPlan(t *testing.T, store *kanban.Store, issueID, markdown string) *kanban.Issue {
	t.Helper()
	requestedAt := time.Now().UTC().Truncate(time.Second)
	if err := store.SetIssuePendingPlanApproval(issueID, markdown, requestedAt); err != nil {
		t.Fatalf("set issue pending plan approval: %v", err)
	}
	issue, err := store.GetIssue(issueID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	return issue
}

func TestPlanApprovalHelpers(t *testing.T) {
	store, _ := setupDashboardServerTest(t, testProvider{})
	server := NewServer(store, testProvider{})

	t.Run("plan approval note status", func(t *testing.T) {
		issue, err := store.CreateIssue("", "", "Plan issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if _, err := server.planApprovalNoteCommandStatus(nil); err == nil {
			t.Fatal("expected nil issue to fail")
		}
		if status, err := server.planApprovalNoteCommandStatus(issue); err != nil || status != kanban.IssueAgentCommandPending {
			t.Fatalf("planApprovalNoteCommandStatus pending = %q, err=%v", status, err)
		}
		blocker, err := store.CreateIssue("", "", "Blocker", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocker: %v", err)
		}
		if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
			t.Fatalf("SetIssueBlockers: %v", err)
		}
		if status, err := server.planApprovalNoteCommandStatus(issue); err != nil || status != kanban.IssueAgentCommandWaitingForUnblock {
			t.Fatalf("planApprovalNoteCommandStatus waiting = %q, err=%v", status, err)
		}
	})

	t.Run("plan approval note store error", func(t *testing.T) {
		errorStore, err := kanban.NewStore(filepath.Join(t.TempDir(), "closed.db"))
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		issue, err := errorStore.CreateIssue("", "", "Plan issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := errorStore.Close(); err != nil {
			t.Fatalf("Close store: %v", err)
		}
		errorServer := NewServer(errorStore, testProvider{})
		if _, err := errorServer.planApprovalNoteCommandStatus(issue); err == nil {
			t.Fatal("expected planApprovalNoteCommandStatus to fail on closed store")
		}
	})

	t.Run("approve pending plan", func(t *testing.T) {
		issue, err := store.CreateIssue("", "", "Approvals", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		issue = markPendingPlan(t, store, issue.ID, "Investigate the rollout")

		response, err := server.approveIssuePlan(context.Background(), issue, "Please review the plan")
		if err != nil {
			t.Fatalf("approveIssuePlan: %v", err)
		}
		if ok, _ := response["ok"].(bool); !ok {
			t.Fatalf("expected ok response, got %#v", response)
		}
		dispatch, _ := response["dispatch"].(map[string]interface{})
		if status, _ := dispatch["status"].(string); status != "queued_now" {
			t.Fatalf("unexpected dispatch response: %#v", dispatch)
		}
		loaded, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue after approval: %v", err)
		}
		if _, err := store.GetIssuePlanning(loaded); err != nil {
			t.Fatalf("GetIssuePlanning after approval: %v", err)
		}
	})

	t.Run("approve and revise nil issue", func(t *testing.T) {
		if _, err := server.approveIssuePlan(context.Background(), nil, "note"); err == nil {
			t.Fatal("expected nil issue approval to fail")
		}
		if _, err := server.requestIssuePlanRevision(context.Background(), nil, "note"); err == nil {
			t.Fatal("expected nil issue revision request to fail")
		}
	})

	t.Run("reject invalid approval requests", func(t *testing.T) {
		issue, err := store.CreateIssue("", "", "Plain issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if _, err := server.approveIssuePlan(context.Background(), issue, "note"); !errors.Is(err, kanban.ErrBlockedTransition) {
			t.Fatalf("expected blocked transition error, got %v", err)
		}
		if _, err := server.requestIssuePlanRevision(context.Background(), issue, ""); !errors.Is(err, agentruntime.ErrInvalidInteractionResponse) {
			t.Fatalf("expected invalid interaction error, got %v", err)
		}
		if _, err := server.requestIssuePlanRevision(context.Background(), issue, "note"); !errors.Is(err, kanban.ErrBlockedTransition) {
			t.Fatalf("expected blocked transition on revision request, got %v", err)
		}
	})

	t.Run("request plan revision", func(t *testing.T) {
		issue, err := store.CreateIssue("", "", "Revision", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		issue = markPendingPlan(t, store, issue.ID, "Investigate the rollout")

		response, err := server.requestIssuePlanRevision(context.Background(), issue, "Tighten the rollout steps")
		if err != nil {
			t.Fatalf("requestIssuePlanRevision: %v", err)
		}
		if ok, _ := response["ok"].(bool); !ok {
			t.Fatalf("expected ok response, got %#v", response)
		}
		dispatch, _ := response["dispatch"].(map[string]interface{})
		if status, _ := dispatch["status"].(string); status != "queued_now" {
			t.Fatalf("unexpected dispatch response: %#v", dispatch)
		}
		loaded, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue after revision request: %v", err)
		}
		if _, err := store.GetIssuePlanning(loaded); err != nil {
			t.Fatalf("GetIssuePlanning after revision request: %v", err)
		}
	})

	t.Run("request plan revision rollback on dispatch error", func(t *testing.T) {
		errorStore, err := kanban.NewStore(filepath.Join(t.TempDir(), "rollback.db"))
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		t.Cleanup(func() { _ = errorStore.Close() })
		issue, err := errorStore.CreateIssue("", "", "Rollback", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		issue = markPendingPlan(t, errorStore, issue.ID, "Investigate the rollout")
		errorServer := NewServer(errorStore, retryErrorProvider{})
		if _, err := errorServer.requestIssuePlanRevision(context.Background(), issue, "Tighten the rollout steps"); err == nil {
			t.Fatal("expected requestIssuePlanRevision to fail when dispatch fails")
		}
		reloaded, err := errorStore.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue rollback: %v", err)
		}
		if reloaded.PendingPlanRevisionMarkdown != "" || reloaded.PendingPlanRevisionRequestedAt != nil {
			t.Fatalf("expected pending revision to be rolled back, got %#v", reloaded)
		}
	})
}
