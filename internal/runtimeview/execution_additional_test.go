package runtimeview

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type executionTestProvider struct {
	snapshot observability.Snapshot
	live     map[string]interface{}
	pending  *agentruntime.PendingInteraction
}

func (p executionTestProvider) Snapshot() observability.Snapshot {
	return p.snapshot
}

func (p executionTestProvider) LiveSessions() map[string]interface{} {
	if p.live == nil {
		return map[string]interface{}{"sessions": map[string]interface{}{}}
	}
	return p.live
}

func (p executionTestProvider) PendingInterruptForIssue(issueID, identifier string) (*agentruntime.PendingInteraction, bool) {
	if p.pending == nil {
		return nil, false
	}
	if p.pending.IssueID != issueID && p.pending.IssueIdentifier != identifier {
		return nil, false
	}
	cloned := p.pending.Clone()
	return &cloned, true
}

func TestIssueExecutionPayloadIncludesPersistedSessionAndPlanFields(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Runtime payload", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApprovalWithContext(issue, "Plan the rollout", now, 3, "thread-1", "turn-3"); err != nil {
		t.Fatalf("SetIssuePendingPlanApprovalWithContext: %v", err)
	}
	if err := store.SetIssuePendingPlanRevision(issue.ID, "Revise the rollout", now); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision: %v", err)
	}

	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "review",
		Attempt:    3,
		RunKind:    "run_failed",
		Error:      "turn_input_required",
		UpdatedAt:  now,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-1-turn-3",
			ThreadID:        "thread-1",
			TurnID:          "turn-3",
			LastEvent:       "turn.completed",
			Terminal:        true,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	provider := executionTestProvider{
		snapshot: observability.Snapshot{
			GeneratedAt: now,
			Paused: []observability.PausedEntry{{
				IssueID:             issue.ID,
				Identifier:          issue.Identifier,
				Phase:               "review",
				Attempt:             3,
				PausedAt:            now,
				Error:               "turn_input_required",
				ConsecutiveFailures: 2,
				PauseThreshold:      4,
			}},
		},
		pending: &agentruntime.PendingInteraction{
			ID:              "pending-1",
			Kind:            agentruntime.PendingInteractionKindApproval,
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			RequestedAt:     now,
		},
	}

	payload, err := IssueExecutionPayload(store, provider, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}
	if payload["session_source"] != "persisted" {
		t.Fatalf("expected persisted session source, got %#v", payload["session_source"])
	}
	if payload["retry_state"] != "paused" {
		t.Fatalf("expected paused retry state, got %#v", payload["retry_state"])
	}
	if payload["phase"] != "review" {
		t.Fatalf("expected paused phase, got %#v", payload["phase"])
	}
	if payload["current_error"] != "turn_input_required" {
		t.Fatalf("unexpected current error: %#v", payload["current_error"])
	}
	if payload["failure_class"] != "turn_input_required" {
		t.Fatalf("unexpected failure class: %#v", payload["failure_class"])
	}
	if payload["paused_at"] != now.UTC().Format(time.RFC3339) {
		t.Fatalf("unexpected paused_at: %#v", payload["paused_at"])
	}
	if payload["pause_reason"] != "turn_input_required" {
		t.Fatalf("unexpected pause reason: %#v", payload["pause_reason"])
	}
	if payload["consecutive_failures"] != 2 || payload["pause_threshold"] != 4 {
		t.Fatalf("unexpected paused payload fields: %#v", payload)
	}
	if _, ok := payload["session"]; !ok {
		t.Fatalf("expected persisted session payload, got %#v", payload)
	}
	if _, ok := payload["pending_interrupt"]; !ok {
		t.Fatalf("expected pending interrupt payload, got %#v", payload)
	}
	if _, ok := payload["plan_approval"]; !ok {
		t.Fatalf("expected plan approval payload, got %#v", payload)
	}
	if _, ok := payload["plan_revision"]; !ok {
		t.Fatalf("expected plan revision payload, got %#v", payload)
	}
}

func TestExecutionHelperBranches(t *testing.T) {
	session := agentruntime.Session{
		SessionID: "thread-1-turn-1",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
	}
	live := map[string]interface{}{
		"sessions": map[string]interface{}{
			"ISS-1": session,
		},
	}
	got, ok := findLiveSession(live, "ISS-1")
	if !ok || got.SessionID != session.SessionID {
		t.Fatalf("expected live session lookup, got %+v", got)
	}
	if _, ok := findLiveSession(map[string]interface{}{"sessions": "invalid"}, "ISS-1"); ok {
		t.Fatal("expected invalid live session map to fail")
	}
	if !isPersistedPauseResetEvent("run_completed") || !isPersistedPauseResetEvent("retry_scheduled") {
		t.Fatal("expected reset events to be recognized")
	}
	if isPersistedPauseResetEvent("retry_paused") {
		t.Fatal("expected retry_paused to not be a reset event")
	}
	if eventPayloadString(nil, "status") != "" {
		t.Fatal("expected nil payload to return empty string")
	}
	if got := eventPayloadString(map[string]interface{}{"status": "recovering"}, "status"); got != "recovering" {
		t.Fatalf("unexpected payload string: %q", got)
	}
	if got := eventPayloadString(map[string]interface{}{"status": 1}, "status"); got != "" {
		t.Fatalf("expected non-string payload to be ignored, got %q", got)
	}
	if asPayloadInt(nil) != 0 || asPayloadInt(int64(3)) != 3 || asPayloadInt(float64(4)) != 4 {
		t.Fatal("unexpected payload int conversion")
	}
	if normalizeFailureErrorClass("  ") != "" {
		t.Fatal("expected blank error class to stay blank")
	}
	if normalizeFailureKind("workspace_bootstrap_failed") != "workspace_bootstrap" {
		t.Fatal("expected workspace bootstrap normalization")
	}
	if got := workspaceRecoveryFromEvent(kanban.RuntimeEvent{
		Kind:    "workspace_bootstrap_recovery",
		Payload: map[string]interface{}{},
	}, "recovering"); got == nil || got.Status != "recovering" || got.Message != "Workspace recovery is in progress." {
		t.Fatalf("unexpected recovering workspace payload: %#v", got)
	}
	if got := workspaceRecoveryFromEvent(kanban.RuntimeEvent{
		Kind: "workspace_bootstrap_failed",
		Payload: map[string]interface{}{
			"status":  "required",
			"message": "  ",
		},
	}, "recovering"); got == nil || got.Status != "required" || got.Message != "Workspace bootstrap failed. Review the blocker and retry once it is resolved." {
		t.Fatalf("unexpected required workspace payload: %#v", got)
	}
	if got := workspaceRecoveryFromEvent(kanban.RuntimeEvent{
		Kind:    "workspace_bootstrap_failed",
		Error:   "workspace blocked",
		Payload: map[string]interface{}{"message": "custom"},
	}, "required"); got == nil || got.Message != "custom" {
		t.Fatalf("unexpected custom workspace payload: %#v", got)
	}
	if planApprovalPendingErrorText(&observability.RetryEntry{Error: "plan_approval_pending: question"}, nil, nil) == "" {
		t.Fatal("expected retry plan approval pending text to be detected")
	}
	if planApprovalPendingErrorText(nil, &observability.PausedEntry{Error: "plan_approval_pending"}, nil) == "" {
		t.Fatal("expected paused plan approval pending text to be detected")
	}
	if planApprovalPendingErrorText(nil, nil, &kanban.ExecutionSessionSnapshot{Error: "plan_approval_pending"}) == "" {
		t.Fatal("expected persisted plan approval pending text to be detected")
	}
	if planApprovalPendingErrorText(nil, nil, nil) != "" {
		t.Fatal("expected missing plan approval pending text to remain blank")
	}
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "", want: ""},
		{input: "workspace_bootstrap_recovery", want: "workspace_bootstrap"},
		{input: "approval_required", want: "approval_required"},
		{input: "turn_input_required", want: "turn_input_required"},
		{input: "stall_timeout", want: "stall_timeout"},
		{input: "run_unsuccessful", want: "run_unsuccessful"},
		{input: "run_failed", want: "run_failed"},
		{input: "unknown", want: ""},
	} {
		if got := normalizeFailureKind(tc.input); got != tc.want {
			t.Fatalf("normalizeFailureKind(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
