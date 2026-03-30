package orchestrator

import (
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
)

func TestOrchestratorHelperBranches(t *testing.T) {
	t.Run("priority and payload helpers", func(t *testing.T) {
		if priorityBucket(1) != 0 || priorityBucket(0) != 1 || priorityBucket(-1) != 1 {
			t.Fatal("unexpected priority bucket results")
		}

		left := &kanban.Issue{Priority: 1, CreatedAt: time.Unix(1, 0).UTC(), Identifier: "ISS-1"}
		right := &kanban.Issue{Priority: 2, CreatedAt: time.Unix(2, 0).UTC(), Identifier: "ISS-2"}
		if !issuePriorityLess(left, right) {
			t.Fatal("expected smaller positive priority to sort first")
		}
		left = &kanban.Issue{Priority: 1, CreatedAt: time.Unix(2, 0).UTC(), Identifier: "ISS-1"}
		right = &kanban.Issue{Priority: 1, CreatedAt: time.Unix(1, 0).UTC(), Identifier: "ISS-2"}
		if issuePriorityLess(left, right) {
			t.Fatal("expected earlier created issue to sort first when priorities match")
		}
		left = &kanban.Issue{Priority: 0, CreatedAt: time.Unix(1, 0).UTC(), Identifier: "ISS-1"}
		right = &kanban.Issue{Priority: 0, CreatedAt: time.Unix(1, 0).UTC(), Identifier: "ISS-2"}
		if !issuePriorityLess(left, right) {
			t.Fatal("expected identifier tie-breaker to be stable")
		}

		payload := map[string]interface{}{
			"int":    4,
			"int64":  int64(5),
			"float":  6.0,
			"string": " 7 ",
			"time":   "2026-03-09T12:00:00Z",
		}
		if payloadInt(payload, "int") != 4 || payloadInt(payload, "int64") != 5 || payloadInt(payload, "float") != 6 || payloadInt(nil, "missing") != 0 {
			t.Fatal("unexpected payloadInt results")
		}
		if payloadString(payload, "string") != "7" || payloadString(payload, "missing") != "" {
			t.Fatal("unexpected payloadString results")
		}
		if got := payloadTime(payload, "time"); got.IsZero() || got.UTC().Format(time.RFC3339) != "2026-03-09T12:00:00Z" {
			t.Fatalf("unexpected payloadTime result: %v", got)
		}
		if !payloadTime(payload, "missing").IsZero() {
			t.Fatal("expected missing payload time to be zero")
		}
	})

	t.Run("running and clearing helpers", func(t *testing.T) {
		orch := &Orchestrator{
			running: map[string]runningEntry{
				"ISS-1": {},
			},
			pendingInteractions: map[string]pendingInteractionEntry{
				"alert-1": {interaction: agentruntime.PendingInteraction{IssueID: "ISS-1", ID: "alert-1"}},
				"alert-2": {interaction: agentruntime.PendingInteraction{IssueID: "ISS-2", ID: "alert-2"}},
			},
			pendingInteractionOrder: []string{"alert-1", "alert-2"},
		}
		if !orch.issueRunning("ISS-1") || orch.issueRunning("") || orch.issueRunning("missing") {
			t.Fatal("unexpected issueRunning results")
		}
		orch.clearPendingInteractionsForIssue("")
		orch.clearPendingInteractionsForIssue("ISS-1")
		if _, ok := orch.pendingInteractions["alert-1"]; ok {
			t.Fatal("expected matching pending interaction to be cleared")
		}
		if _, ok := orch.pendingInteractions["alert-2"]; !ok {
			t.Fatal("expected unrelated pending interaction to remain")
		}
		if len(orch.pendingInteractionOrder) != 1 || orch.pendingInteractionOrder[0] != "alert-2" {
			t.Fatalf("unexpected pending interaction order: %#v", orch.pendingInteractionOrder)
		}
	})

	t.Run("plan approval interrupt", func(t *testing.T) {
		requestedAt := time.Date(2026, 3, 9, 11, 0, 0, 0, time.UTC)
		updatedAt := time.Date(2026, 3, 9, 11, 30, 0, 0, time.UTC)
		snapshot := &kanban.ExecutionSessionSnapshot{
			Phase:   "implementation",
			Attempt: 2,
			AppSession: agentruntime.Session{
				SessionID:       "session-1",
				ThreadID:        "thread-1",
				TurnID:          "turn-1",
				LastEvent:       "turn.started",
				LastTimestamp:   updatedAt,
				IssueIdentifier: "ISS-1",
			},
			UpdatedAt: updatedAt,
		}
		planning := &kanban.IssuePlanning{
			SessionID:            "plan-1",
			Status:               kanban.IssuePlanningStatusAwaitingApproval,
			CurrentVersionNumber: 3,
			CurrentVersion: &kanban.IssuePlanVersion{
				Markdown: "Plan markdown",
			},
			PendingRevisionNote: "Needs another pass",
			UpdatedAt:           updatedAt,
		}
		interaction := buildPlanApprovalPendingInterrupt(
			kanban.Issue{
				ID:                            "issue-1",
				Identifier:                    "ISS-1",
				Title:                         "Plan issue",
				ProjectID:                     "proj-1",
				PendingPlanMarkdown:           "Draft markdown",
				PendingPlanRequestedAt:        &requestedAt,
				PendingPlanRevisionMarkdown:   "Revision markdown",
				PendingPlanRevisionRequestedAt: &requestedAt,
			},
			&kanban.Project{ID: "proj-1", Name: "Platform"},
			snapshot,
			planning,
		)
		if interaction.ID != "plan-approval-issue-1" || interaction.Kind != agentruntime.PendingInteractionKindApproval {
			t.Fatalf("unexpected plan approval interaction identity: %#v", interaction)
		}
		if interaction.ProjectID != "proj-1" || interaction.ProjectName != "Platform" {
			t.Fatalf("expected project metadata to be preserved, got %#v", interaction)
		}
		if interaction.RequestedAt.UTC() != requestedAt {
			t.Fatalf("expected requestedAt override, got %#v", interaction.RequestedAt)
		}
		if interaction.Phase != "implementation" || interaction.Attempt != 2 || interaction.SessionID != "session-1" || interaction.ThreadID != "thread-1" || interaction.TurnID != "turn-1" {
			t.Fatalf("unexpected snapshot metadata: %#v", interaction)
		}
		if interaction.Approval == nil || interaction.Approval.Markdown != "Plan markdown" || interaction.Approval.PlanStatus != string(kanban.IssuePlanningStatusAwaitingApproval) || interaction.Approval.PlanVersionNumber != 3 {
			t.Fatalf("unexpected approval payload: %#v", interaction.Approval)
		}
		if interaction.LastActivityAt == nil || !interaction.LastActivityAt.Equal(updatedAt) {
			t.Fatalf("expected updatedAt to propagate, got %#v", interaction.LastActivityAt)
		}
		if interaction.LastActivity != "Plan v3 ready for approval." {
			t.Fatalf("unexpected last activity message: %q", interaction.LastActivity)
		}
	})
}
