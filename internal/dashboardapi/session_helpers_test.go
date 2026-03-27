package dashboardapi

import (
	"context"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

func TestIssueForInterruptResolvesByIdentifierAndID(t *testing.T) {
	store, _ := setupDashboardServerTest(t, testProvider{})
	server := NewServer(store, testProvider{})

	issue, err := store.CreateIssue("", "", "Interrupt target", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	byIdentifier, err := server.issueForInterrupt(context.Background(), &appserver.PendingInteraction{
		IssueIdentifier: issue.Identifier,
	})
	if err != nil {
		t.Fatalf("issueForInterrupt by identifier: %v", err)
	}
	if byIdentifier.ID != issue.ID || byIdentifier.Identifier != issue.Identifier {
		t.Fatalf("unexpected issue resolved by identifier: %+v", byIdentifier)
	}

	byID, err := server.issueForInterrupt(context.Background(), &appserver.PendingInteraction{
		IssueID: issue.ID,
	})
	if err != nil {
		t.Fatalf("issueForInterrupt by id: %v", err)
	}
	if byID.ID != issue.ID || byID.Identifier != issue.Identifier {
		t.Fatalf("unexpected issue resolved by id: %+v", byID)
	}

	if _, err := server.issueForInterrupt(context.Background(), nil); err == nil {
		t.Fatal("expected nil interaction to fail")
	}
	if _, err := server.issueForInterrupt(context.Background(), &appserver.PendingInteraction{}); err == nil {
		t.Fatal("expected missing issue reference to fail")
	}
}

func TestBuildPersistedSessionFeedEntryMarksPlanApprovalWaiting(t *testing.T) {
	now := time.Date(2026, 3, 18, 13, 30, 0, 0, time.UTC)
	snapshot := kanban.ExecutionSessionSnapshot{
		IssueID:    "issue-1",
		Identifier: "ISS-1",
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "retry_paused",
		Error:      "plan_approval_pending",
		StopReason: "plan_approval_pending",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         "issue-1",
			IssueIdentifier: "ISS-1",
			LastTimestamp:   now.Add(-30 * time.Second),
			LastEvent:       "turn.paused",
			LastMessage:     "Waiting for operator approval",
			TotalTokens:     42,
			TurnsStarted:    3,
			TurnsCompleted:  1,
		},
	}

	entry := buildPersistedSessionFeedEntry(
		snapshot,
		observability.RetryEntry{Attempt: 2, Phase: "implementation", Error: "plan_approval_pending"},
		observability.PausedEntry{Attempt: 2, Phase: "implementation", Error: "plan_approval_pending"},
		nil,
		"Plan approval issue",
	)

	if entry.Status != "waiting" {
		t.Fatalf("expected waiting status, got %+v", entry)
	}
	if entry.Error != "" || entry.FailureClass != "" {
		t.Fatalf("expected plan approval errors to be cleared, got %+v", entry)
	}
	if entry.IssueIdentifier != "ISS-1" || entry.IssueTitle != "Plan approval issue" {
		t.Fatalf("unexpected entry identity fields: %+v", entry)
	}
	if entry.TotalTokens != 42 || entry.TurnsStarted != 3 || entry.TurnsCompleted != 1 {
		t.Fatalf("unexpected session counters: %+v", entry)
	}
}

func TestBuildPersistedSessionFeedEntryMarksQueuedPlanRevision(t *testing.T) {
	now := time.Date(2026, 3, 18, 13, 30, 0, 0, time.UTC)
	revisionRequestedAt := now.Add(2 * time.Minute)
	snapshot := kanban.ExecutionSessionSnapshot{
		IssueID:    "issue-1",
		Identifier: "ISS-1",
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "retry_paused",
		Error:      "plan_approval_pending",
		StopReason: "plan_approval_pending",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         "issue-1",
			IssueIdentifier: "ISS-1",
			LastTimestamp:   now.Add(-30 * time.Second),
			LastEvent:       "turn.paused",
			LastMessage:     "Waiting for operator approval",
			TotalTokens:     42,
			TurnsStarted:    3,
			TurnsCompleted:  1,
		},
	}

	entry := buildPersistedSessionFeedEntry(
		snapshot,
		observability.RetryEntry{Attempt: 2, Phase: "implementation", Error: "plan_approval_pending"},
		observability.PausedEntry{Attempt: 2, Phase: "implementation", Error: "plan_approval_pending"},
		&kanban.Issue{
			ID:                             "issue-1",
			Identifier:                     "ISS-1",
			PendingPlanRevisionMarkdown:    "Tighten the rollout and keep the rollback explicit.",
			PendingPlanRevisionRequestedAt: &revisionRequestedAt,
		},
		"Plan approval issue",
	)

	if entry.Status != "revision_queued" {
		t.Fatalf("expected revision_queued status, got %+v", entry)
	}
	if entry.LastMessage != queuedPlanRevisionText {
		t.Fatalf("expected queued revision summary, got %+v", entry)
	}
	if !entry.UpdatedAt.Equal(revisionRequestedAt) {
		t.Fatalf("expected queued revision timestamp, got %+v", entry)
	}
	if entry.Error != "" || entry.FailureClass != "" {
		t.Fatalf("expected queued revision to clear waiting errors, got %+v", entry)
	}
}
