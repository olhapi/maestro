package runtimeview

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type testProvider struct {
	snapshot          observability.Snapshot
	sessions          map[string]interface{}
	pendingInterrupts map[string]agentruntime.PendingInteraction
}

func (p testProvider) Snapshot() observability.Snapshot {
	return p.snapshot
}

func (p testProvider) LiveSessions() map[string]interface{} {
	return map[string]interface{}{"sessions": p.sessions}
}

func (p testProvider) PendingInterruptForIssue(issueID, identifier string) (*agentruntime.PendingInteraction, bool) {
	if interaction, ok := p.pendingInterrupts[issueID]; ok {
		cloned := interaction.Clone()
		return &cloned, true
	}
	if interaction, ok := p.pendingInterrupts[identifier]; ok {
		cloned := interaction.Clone()
		return &cloned, true
	}
	return nil, false
}

func TestIssueExecutionPayloadUsesLiveRuntimeWhenAvailable(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Live issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_failed",
		Error:      "approval_required",
		UpdatedAt:  time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-persisted-turn-persisted",
			LastEvent:       "turn.approval_required",
			LastTimestamp:   time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_failed", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
		"error":      "approval_required",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "review",
				Attempt:    4,
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: agentruntime.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-live-turn-live",
				ThreadID:        "thread-live",
				TurnID:          "turn-live",
			},
		},
	}, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["runtime_available"] != true || payload["active"] != true {
		t.Fatalf("unexpected runtime payload: %#v", payload)
	}
	if payload["session_source"] != "live" || payload["phase"] != "review" {
		t.Fatalf("expected live overlay, got %#v", payload)
	}
	if payload["attempt_number"].(int) != 4 {
		t.Fatalf("expected running attempt number, got %#v", payload["attempt_number"])
	}
}

func TestIssueExecutionPayloadPrefersPersistedSessionWhenLiveSessionIsTerminal(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Terminal live issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	persistedAt := time.Date(2026, 3, 9, 12, 15, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:           issue.ID,
		Identifier:        issue.Identifier,
		Phase:             "implementation",
		Attempt:           4,
		RunKind:           "run_completed",
		RuntimeName:       "claude",
		RuntimeProvider:   "claude",
		RuntimeTransport:  "stdio",
		RuntimeAuthSource: "OAuth",
		StopReason:        "end_turn",
		UpdatedAt:         persistedAt,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-persisted-turn-persisted",
			ThreadID:        "thread-persisted",
			TurnID:          "turn-persisted",
			LastEvent:       "turn.completed",
			LastTimestamp:   persistedAt,
			LastMessage:     "Persisted completion",
			Terminal:        true,
			TerminalReason:  "turn.completed",
			Metadata: map[string]interface{}{
				"provider":           "claude",
				"transport":          "stdio",
				"auth_source":        "OAuth",
				"claude_stop_reason": "end_turn",
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    4,
				SessionID:  "thread-live-turn-live",
				StartedAt:  persistedAt.Add(-time.Minute),
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: agentruntime.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-live-turn-live",
				ThreadID:        "thread-live",
				TurnID:          "turn-live",
				LastEvent:       "turn.completed",
				LastTimestamp:   persistedAt.Add(-time.Second),
				LastMessage:     "STREAM:" + issue.Identifier + ":live",
				Terminal:        true,
				TerminalReason:  "turn.completed",
				Metadata: map[string]interface{}{
					"provider":           "claude",
					"transport":          "stdio",
					"auth_source":        "OAuth",
					"claude_stop_reason": "end_turn",
				},
			},
		},
	}, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["active"] != false {
		t.Fatalf("expected terminal live session to be inactive, got %#v", payload["active"])
	}
	if payload["session_source"] != "persisted" {
		t.Fatalf("expected persisted session source, got %#v", payload["session_source"])
	}
	if payload["stop_reason"] != "end_turn" {
		t.Fatalf("expected persisted stop reason, got %#v", payload["stop_reason"])
	}
	if got := payload["session"].(agentruntime.Session); got.SessionID != "thread-persisted-turn-persisted" || !got.Terminal {
		t.Fatalf("expected persisted session payload, got %#v", payload["session"])
	}
}

func TestIssueExecutionPayloadIncludesPendingInterruptMetadata(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Waiting issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	payload, err := IssueExecutionPayload(store, testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    1,
			}},
		},
		pendingInterrupts: map[string]agentruntime.PendingInteraction{
			issue.ID: {
				ID:              "interrupt-1",
				Kind:            agentruntime.PendingInteractionKindApproval,
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				RequestedAt:     time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC),
			},
		},
	}, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	pending, ok := payload["pending_interrupt"].(*agentruntime.PendingInteraction)
	if !ok || pending.ID != "interrupt-1" {
		t.Fatalf("expected pending interrupt payload, got %#v", payload["pending_interrupt"])
	}
}

func TestIssueExecutionPayloadIncludesPlanApprovalMetadata(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Plan approval issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	requestedAt := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApprovalWithContext(issue, "Check the repo, then continue.", requestedAt, 5, "thread-plan", "turn-plan"); err != nil {
		t.Fatalf("SetIssuePendingPlanApprovalWithContext: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    5,
		RunKind:    "run_completed",
		UpdatedAt:  requestedAt,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-plan-turn-plan",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}
	approval, ok := payload["plan_approval"].(kanban.IssuePlanApproval)
	if !ok {
		t.Fatalf("expected plan_approval payload, got %#v", payload["plan_approval"])
	}
	if approval.Markdown != "Check the repo, then continue." || approval.Attempt != 5 {
		t.Fatalf("unexpected plan approval payload: %+v", approval)
	}
	if !approval.RequestedAt.Equal(requestedAt) {
		t.Fatalf("unexpected plan approval requested_at: %+v", approval.RequestedAt)
	}
}

func TestIssueExecutionPayloadIncludesPlanRevisionMetadata(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Plan revision issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	requestedAt := time.Date(2026, 3, 18, 12, 5, 0, 0, time.UTC)
	revisionRequestedAt := requestedAt.Add(5 * time.Minute)
	if err := store.SetIssuePendingPlanApprovalWithContext(issue, "Check the repo, then continue.", requestedAt, 5, "thread-plan", "turn-plan"); err != nil {
		t.Fatalf("SetIssuePendingPlanApprovalWithContext: %v", err)
	}
	if err := store.SetIssuePendingPlanRevision(issue.ID, "Tighten the rollout and add a rollback check.", revisionRequestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    5,
		RunKind:    "run_completed",
		UpdatedAt:  revisionRequestedAt,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-plan-turn-plan",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}
	revision, ok := payload["plan_revision"].(kanban.IssuePlanRevision)
	if !ok {
		t.Fatalf("expected plan_revision payload, got %#v", payload["plan_revision"])
	}
	if revision.Markdown != "Tighten the rollout and add a rollback check." || revision.Attempt != 5 {
		t.Fatalf("unexpected plan revision payload: %+v", revision)
	}
	if !revision.RequestedAt.Equal(revisionRequestedAt) {
		t.Fatalf("unexpected plan revision requested_at: %+v", revision.RequestedAt)
	}
}

func TestIssueExecutionPayloadFallsBackToPersistedDataWithoutProvider(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Persisted issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    3,
		RunKind:    "run_unsuccessful",
		Error:      "turn_input_required",
		UpdatedAt:  time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-persisted-turn-persisted",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_failed", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    3,
		"error":      "turn_input_required",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["runtime_available"] != false || payload["session_source"] != "persisted" {
		t.Fatalf("unexpected persisted-only payload: %#v", payload)
	}
	if payload["active"] != false {
		t.Fatalf("expected inactive persisted-only payload: %#v", payload)
	}
	if payload["failure_class"] != "turn_input_required" {
		t.Fatalf("unexpected failure class: %#v", payload["failure_class"])
	}
	if payload["attempt_number"].(int) != 3 {
		t.Fatalf("expected persisted attempt number, got %#v", payload["attempt_number"])
	}
}

func TestIssueExecutionPayloadMarksStaleRunStartedSnapshotAsInterrupted(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Interrupted issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 12, 5, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-stale-turn-stale",
			LastEvent:       "turn.started",
			LastTimestamp:   now,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["active"] != false || payload["session_source"] != "persisted" {
		t.Fatalf("unexpected interrupted payload: %#v", payload)
	}
	if payload["failure_class"] != "run_interrupted" {
		t.Fatalf("expected run_interrupted failure class, got %#v", payload["failure_class"])
	}
}

func TestIssueExecutionPayloadClearsHistoricalFailureForActiveRecoveredRun(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Recovered issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 12, 10, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-recovered-turn-recovered",
			LastEvent:       "item.started",
			LastTimestamp:   now,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	for _, event := range []struct {
		kind    string
		attempt int
		error   string
	}{
		{kind: "run_interrupted", attempt: 1, error: "run_interrupted"},
		{kind: "retry_scheduled", attempt: 2, error: "run_interrupted"},
		{kind: "run_started", attempt: 2},
	} {
		payload := map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    event.attempt,
		}
		if event.error != "" {
			payload["error"] = event.error
		}
		if err := store.AppendRuntimeEvent(event.kind, payload); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s): %v", event.kind, err)
		}
	}

	payload, err := IssueExecutionPayload(store, testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    2,
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: agentruntime.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-live-turn-live",
				LastEvent:       "item.started",
				LastTimestamp:   now,
			},
		},
	}, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["active"] != true || payload["retry_state"] != "active" {
		t.Fatalf("expected active recovered payload, got %#v", payload)
	}
	if payload["failure_class"] != "" {
		t.Fatalf("expected cleared failure class for active recovered run, got %#v", payload["failure_class"])
	}
	if payload["current_error"] != "" {
		t.Fatalf("expected cleared current error for active recovered run, got %#v", payload["current_error"])
	}
}

func TestIssueExecutionPayloadIncludesInterruptedStopReason(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Interrupted issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_interrupted",
		Error:      "run_interrupted",
		StopReason: "run_interrupted",
		UpdatedAt:  now,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "claude-session-1",
			ThreadID:        "claude-session-1",
			LastEvent:       "turn.cancelled",
			LastTimestamp:   now,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_interrupted", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
		"error":      "run_interrupted",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["failure_class"] != "run_interrupted" {
		t.Fatalf("expected run_interrupted failure class, got %#v", payload["failure_class"])
	}
	if payload["stop_reason"] != "run_interrupted" {
		t.Fatalf("expected run_interrupted stop reason, got %#v", payload["stop_reason"])
	}
}

func TestIssueExecutionPayloadClearsHistoricalFailureAfterRunCompletedSuccess(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Succeeded after failure", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	for _, event := range []struct {
		kind    string
		attempt int
		error   string
	}{
		{kind: "run_started", attempt: 1},
		{kind: "run_failed", attempt: 1, error: "approval_required"},
		{kind: "retry_scheduled", attempt: 2, error: "approval_required"},
		{kind: "run_started", attempt: 2},
		{kind: "run_completed", attempt: 2},
	} {
		payload := map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    event.attempt,
		}
		if event.error != "" {
			payload["error"] = event.error
		}
		if err := store.AppendRuntimeEvent(event.kind, payload); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s): %v", event.kind, err)
		}
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["failure_class"] != "" {
		t.Fatalf("expected success boundary to clear failure class, got %#v", payload["failure_class"])
	}
	if payload["current_error"] != "" {
		t.Fatalf("expected success boundary to clear current error, got %#v", payload["current_error"])
	}
	if payload["attempt_number"].(int) != 2 {
		t.Fatalf("expected latest successful attempt number, got %#v", payload["attempt_number"])
	}
}

func TestIssueExecutionPayloadClearsHistoricalBootstrapFailureAfterSuccess(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Bootstrap succeeded after failure", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	for _, event := range []struct {
		kind  string
		error string
	}{
		{kind: "workspace_bootstrap_recovery", error: "workspace recovery required: Git blocked the branch switch while rebasing"},
		{kind: "workspace_bootstrap_created"},
	} {
		payload := map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    1,
		}
		if event.error != "" {
			payload["error"] = event.error
		}
		if err := store.AppendRuntimeEvent(event.kind, payload); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s): %v", event.kind, err)
		}
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["failure_class"] != "" {
		t.Fatalf("expected bootstrap success boundary to clear failure class, got %#v", payload["failure_class"])
	}
	if payload["current_error"] != "" {
		t.Fatalf("expected bootstrap success boundary to clear current error, got %#v", payload["current_error"])
	}
	if payload["workspace_recovery"] != nil {
		t.Fatalf("expected successful bootstrap boundary to clear recovery metadata, got %#v", payload["workspace_recovery"])
	}
}

func TestIssueExecutionPayloadReturnsWorkspaceRecoveryMetadata(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Recovery issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AppendRuntimeEvent("workspace_bootstrap_recovery", map[string]interface{}{
		"issue_id":        issue.ID,
		"identifier":      issue.Identifier,
		"phase":           "implementation",
		"attempt":         1,
		"status":          "recovering",
		"message":         "Workspace recovery note:\n\n- Maestro found an active Git rebase in this workspace.",
		"recovery_reason": "branch_switch_blocked",
		"error":           "workspace recovery required: Git blocked the branch switch while rebasing",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	recovery, ok := payload["workspace_recovery"].(*kanban.WorkspaceRecovery)
	if !ok {
		t.Fatalf("expected workspace_recovery payload, got %#v", payload["workspace_recovery"])
	}
	if recovery.Status != "recovering" {
		t.Fatalf("unexpected workspace recovery status: %+v", recovery)
	}
	if !strings.Contains(recovery.Message, "Workspace recovery note:") {
		t.Fatalf("unexpected workspace recovery message: %+v", recovery)
	}
	if payload["failure_class"] != "workspace_bootstrap" {
		t.Fatalf("expected workspace bootstrap failure class, got %#v", payload["failure_class"])
	}
}

func TestIssueExecutionPayloadIgnoresSuccessfulWorkspaceBootstrapEvents(t *testing.T) {
	for _, kind := range []string{
		"workspace_bootstrap_created",
		"workspace_bootstrap_reused",
		"workspace_bootstrap_preserved",
	} {
		t.Run(kind, func(t *testing.T) {
			store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })

			issue, err := store.CreateIssue("", "", "Bootstrap issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			if err := store.AppendRuntimeEvent(kind, map[string]interface{}{
				"issue_id":   issue.ID,
				"identifier": issue.Identifier,
				"phase":      "implementation",
				"attempt":    1,
			}); err != nil {
				t.Fatalf("AppendRuntimeEvent: %v", err)
			}

			payload, err := IssueExecutionPayload(store, nil, issue)
			if err != nil {
				t.Fatalf("IssueExecutionPayload: %v", err)
			}

			if payload["failure_class"] != "" {
				t.Fatalf("expected successful bootstrap event to remain clear, got %#v", payload["failure_class"])
			}
			if payload["workspace_recovery"] != nil {
				t.Fatalf("expected no workspace recovery metadata, got %#v", payload["workspace_recovery"])
			}
		})
	}
}

func TestIssueExecutionPayloadIncludesAgentCommands(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Commanded issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	command, err := store.CreateIssueAgentCommand(issue.ID, "Merge the branch to master.", kanban.IssueAgentCommandPending)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand: %v", err)
	}
	if err := store.MarkIssueAgentCommandsDelivered(issue.ID, []string{command.ID}, "same_thread", "thread-live", 2); err != nil {
		t.Fatalf("MarkIssueAgentCommandsDelivered: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	commands, ok := payload["agent_commands"].([]kanban.IssueAgentCommand)
	if !ok {
		t.Fatalf("expected typed agent command list, got %#v", payload["agent_commands"])
	}
	if len(commands) != 1 {
		t.Fatalf("expected one command, got %#v", commands)
	}
	if commands[0].Status != kanban.IssueAgentCommandDelivered || commands[0].DeliveryThreadID != "thread-live" {
		t.Fatalf("unexpected command payload: %+v", commands[0])
	}
}

func TestIssueExecutionPayloadReturnsPausedRetryMetadata(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Paused issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	pausedAt := time.Date(2026, 3, 9, 12, 15, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    3,
		RunKind:    "retry_paused",
		Error:      "stall_timeout",
		UpdatedAt:  pausedAt,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-paused-turn-paused",
			LastEvent:       "item.started",
			LastTimestamp:   pausedAt,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
		"issue_id":             issue.ID,
		"identifier":           issue.Identifier,
		"phase":                "implementation",
		"attempt":              3,
		"paused_at":            pausedAt.Format(time.RFC3339),
		"error":                "stall_timeout",
		"consecutive_failures": 3,
		"pause_threshold":      3,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["retry_state"] != "paused" {
		t.Fatalf("expected paused retry state, got %#v", payload["retry_state"])
	}
	if payload["pause_reason"] != "stall_timeout" || payload["failure_class"] != "stall_timeout" {
		t.Fatalf("unexpected paused failure metadata: %#v", payload)
	}
	if payload["consecutive_failures"].(int) != 3 || payload["pause_threshold"].(int) != 3 {
		t.Fatalf("unexpected paused streak metadata: %#v", payload)
	}
	if payload["paused_at"] != pausedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected paused_at: %#v", payload["paused_at"])
	}
}

func TestIssueExecutionPayloadReturnsRetryLimitPauseReason(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Retry limited issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	pausedAt := time.Date(2026, 3, 9, 12, 20, 0, 0, time.UTC)
	if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
		"issue_id":        issue.ID,
		"identifier":      issue.Identifier,
		"issue_state":     "in_progress",
		"phase":           "implementation",
		"attempt":         4,
		"paused_at":       pausedAt.Format(time.RFC3339),
		"error":           "retry_limit_reached",
		"pause_threshold": 8,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}
	if err := store.AppendRuntimeEvent("plan_revision_requested", map[string]interface{}{
		"issue_id":     issue.ID,
		"identifier":   issue.Identifier,
		"phase":        "implementation",
		"attempt":      0,
		"markdown":     "Tighten the rollout and add a rollback check.",
		"requested_at": pausedAt.Add(time.Minute).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent plan_revision_requested: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["retry_state"] != "paused" || payload["pause_reason"] != "retry_limit_reached" {
		t.Fatalf("unexpected retry limit payload: %#v", payload)
	}
	if payload["failure_class"] != "retry_limit_reached" {
		t.Fatalf("unexpected failure class: %#v", payload["failure_class"])
	}
	if payload["attempt_number"].(int) != 4 {
		t.Fatalf("expected attempt number to remain anchored to the paused retry, got %#v", payload["attempt_number"])
	}
}

func TestIssueExecutionPayloadClearsPersistedPauseAfterRunCompletes(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Recovered retry issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	pausedAt := time.Date(2026, 3, 9, 12, 20, 0, 0, time.UTC)
	for _, event := range []struct {
		kind    string
		attempt int
		fields  map[string]interface{}
	}{
		{
			kind:    "retry_paused",
			attempt: 4,
			fields: map[string]interface{}{
				"paused_at":       pausedAt.Format(time.RFC3339),
				"error":           "retry_limit_reached",
				"pause_threshold": 8,
			},
		},
		{
			kind:    "plan_revision_requested",
			attempt: 0,
			fields: map[string]interface{}{
				"markdown":     "Tighten the rollout and add a rollback check.",
				"requested_at": pausedAt.Add(time.Minute).Format(time.RFC3339),
			},
		},
		{
			kind:    "run_started",
			attempt: 4,
			fields:  map[string]interface{}{},
		},
		{
			kind:    "run_completed",
			attempt: 4,
			fields:  map[string]interface{}{},
		},
	} {
		payload := map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    event.attempt,
		}
		for key, value := range event.fields {
			payload[key] = value
		}
		if err := store.AppendRuntimeEvent(event.kind, payload); err != nil {
			t.Fatalf("AppendRuntimeEvent %s: %v", event.kind, err)
		}
	}

	result, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if result["retry_state"] != "none" {
		t.Fatalf("expected completed run to clear persisted pause metadata, got %#v", result)
	}
	if _, ok := result["pause_reason"]; ok {
		t.Fatalf("did not expect pause_reason once a later run completed, got %#v", result["pause_reason"])
	}
}

func TestIssueExecutionPayloadGroupsPersistentActivityByAttempt(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Attempt timeline issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    1,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent run_started: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    1,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent run_completed: %v", err)
	}

	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:      "item.started",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "agent-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-1", "type": "agentMessage", "phase": "commentary", "text": "Plan"},
	})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:     "item.agentMessage.delta",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "agent-1",
		Delta:    "ning the fix",
	})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:      "item.completed",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "agent-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-1", "type": "agentMessage", "phase": "commentary", "text": "Planning the fix"},
	})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:     "item.started",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "cmd-1",
		ItemType: "commandExecution",
		Command:  "npm test",
		CWD:      "/repo",
		Item: map[string]interface{}{
			"id":      "cmd-1",
			"type":    "commandExecution",
			"command": "npm test",
			"cwd":     "/repo",
		},
	})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:     "item.commandExecution.outputDelta",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "cmd-1",
		Delta:    "all checks green",
	})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:             "item.completed",
		ThreadID:         "thread-1",
		TurnID:           "turn-1",
		ItemID:           "cmd-1",
		ItemType:         "commandExecution",
		Command:          "npm test",
		CWD:              "/repo",
		AggregatedOutput: "all checks green",
		ExitCode:         intPtr(0),
		Status:           "completed",
		Item: map[string]interface{}{
			"id":               "cmd-1",
			"type":             "commandExecution",
			"command":          "npm test",
			"cwd":              "/repo",
			"aggregatedOutput": "all checks green",
			"exitCode":         0,
			"status":           "completed",
		},
	})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{Type: "turn.completed", ThreadID: "thread-1", TurnID: "turn-1"})

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	groups, ok := payload["activity_groups"].([]kanban.ActivityGroup)
	if !ok {
		t.Fatalf("expected activity groups, got %#v", payload["activity_groups"])
	}
	if len(groups) != 1 {
		t.Fatalf("expected one attempt group, got %#v", groups)
	}
	if groups[0].Attempt != 1 || groups[0].Status != "completed" || groups[0].Phase != "implementation" {
		t.Fatalf("unexpected group metadata: %#v", groups[0])
	}
	if len(groups[0].Entries) != 2 {
		t.Fatalf("expected compacted successful activity to keep the latest substantive row and completion status, got %#v", groups[0].Entries)
	}
	if groups[0].Entries[0].Kind != "command" || groups[0].Entries[0].Title != "Command completed" {
		t.Fatalf("expected compacted command row to remain visible, got %#v", groups[0].Entries[0])
	}
	if groups[0].Entries[1].Kind != "status" || groups[0].Entries[1].Title != "Turn Completed" {
		t.Fatalf("expected compacted completion status row, got %#v", groups[0].Entries[1])
	}
}

func TestIssueExecutionPayloadKeepsHistoricalAttemptsVisible(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "History issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	for _, event := range []struct {
		kind    string
		attempt int
	}{
		{kind: "run_completed", attempt: 1},
		{kind: "run_started", attempt: 2},
	} {
		if err := store.AppendRuntimeEvent(event.kind, map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    event.attempt,
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s): %v", event.kind, err)
		}
	}
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:      "item.completed",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "agent-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-1", "type": "agentMessage", "phase": "commentary", "text": "First attempt"},
	})
	mustApplyActivityEvent(t, store, issue, 2, agentruntime.ActivityEvent{Type: "turn.started", ThreadID: "thread-2", TurnID: "turn-2"})
	mustApplyActivityEvent(t, store, issue, 2, agentruntime.ActivityEvent{
		Type:      "item.completed",
		ThreadID:  "thread-2",
		TurnID:    "turn-2",
		ItemID:    "agent-2",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-2", "type": "agentMessage", "phase": "commentary", "text": "Second attempt"},
	})

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	groups := payload["activity_groups"].([]kanban.ActivityGroup)
	if len(groups) != 2 {
		t.Fatalf("expected both attempts to remain visible, got %#v", groups)
	}
	if groups[0].Entries[1].Summary != "First attempt" || groups[1].Entries[1].Summary != "Second attempt" {
		t.Fatalf("unexpected grouped history: %#v", groups)
	}
}

func TestIssueExecutionPayloadUsesCompletedItemAsAuthoritativeAgentText(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Agent authoritative text", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:      "item.started",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "agent-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-1", "type": "agentMessage", "phase": "commentary", "text": "Pl"},
	})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{Type: "item.agentMessage.delta", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "agent-1", Delta: "ann"})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{Type: "item.agentMessage.delta", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "agent-1", Delta: "ing the fi"})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:      "item.completed",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "agent-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-1", "type": "agentMessage", "phase": "commentary", "text": "Planning the fix"},
	})

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	groups := payload["activity_groups"].([]kanban.ActivityGroup)
	if len(groups) != 1 || len(groups[0].Entries) != 1 {
		t.Fatalf("expected a single authoritative agent row, got %#v", groups)
	}
	if groups[0].Entries[0].Summary != "Planning the fix" {
		t.Fatalf("expected completed text to replace partial stream, got %#v", groups[0].Entries[0])
	}
}

func TestIssueExecutionPayloadCollapsesCommandStreamingIntoOnePersistentRow(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Command streaming row", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:     "item.started",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "cmd-1",
		ItemType: "commandExecution",
		Command:  "npm run dev",
		CWD:      "/repo/apps/frontend",
		Item: map[string]interface{}{
			"id":      "cmd-1",
			"type":    "commandExecution",
			"command": "npm run dev",
			"cwd":     "/repo/apps/frontend",
		},
	})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{Type: "item.commandExecution.outputDelta", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "cmd-1", Delta: "ready line 1\n"})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{Type: "item.commandExecution.outputDelta", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "cmd-1", Delta: "ready line 2"})
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:             "item.completed",
		ThreadID:         "thread-1",
		TurnID:           "turn-1",
		ItemID:           "cmd-1",
		ItemType:         "commandExecution",
		Command:          "npm run dev",
		CWD:              "/repo/apps/frontend",
		AggregatedOutput: "ready line 1\nready line 2",
		ExitCode:         intPtr(1),
		Status:           "failed",
		Item: map[string]interface{}{
			"id":               "cmd-1",
			"type":             "commandExecution",
			"command":          "npm run dev",
			"cwd":              "/repo/apps/frontend",
			"aggregatedOutput": "ready line 1\nready line 2",
			"exitCode":         1,
			"status":           "failed",
		},
	})

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	groups := payload["activity_groups"].([]kanban.ActivityGroup)
	if len(groups) != 1 || len(groups[0].Entries) != 1 {
		t.Fatalf("expected a single command row, got %#v", groups)
	}
	command := groups[0].Entries[0]
	if command.Kind != "command" || command.Title != "Command failed (exit 1)" {
		t.Fatalf("expected failed command row, got %#v", command)
	}
	if !strings.Contains(command.Detail, "ready line 1") || !strings.Contains(command.Detail, "ready line 2") {
		t.Fatalf("expected accumulated output in command detail, got %#v", command)
	}
}

func TestIssueExecutionPayloadRoutesNonPrimaryItemsToDebugGroups(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Debug activity issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	mustApplyActivityEvent(t, store, issue, 1, agentruntime.ActivityEvent{
		Type:     "item.completed",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "plan-1",
		ItemType: "plan",
		Item:     map[string]interface{}{"id": "plan-1", "type": "plan", "text": "- inspect routes\n- patch contract"},
	})

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if groups := payload["activity_groups"].([]kanban.ActivityGroup); len(groups) != 0 {
		t.Fatalf("expected no primary activity groups, got %#v", groups)
	}
	debugGroups := payload["debug_activity_groups"].([]kanban.ActivityGroup)
	if len(debugGroups) != 1 || len(debugGroups[0].Entries) != 1 {
		t.Fatalf("expected plan item in debug groups, got %#v", debugGroups)
	}
	if debugGroups[0].Entries[0].ItemType != "plan" {
		t.Fatalf("expected plan item type preserved, got %#v", debugGroups[0].Entries[0])
	}
}

func mustApplyActivityEvent(t *testing.T, store *kanban.Store, issue *kanban.Issue, attempt int, event agentruntime.ActivityEvent) {
	t.Helper()
	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, attempt, event); err != nil {
		t.Fatalf("ApplyIssueActivityEvent(%s): %v", event.Type, err)
	}
}

func intPtr(value int) *int {
	return &value
}
