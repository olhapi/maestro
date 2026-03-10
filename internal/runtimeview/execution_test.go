package runtimeview

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type testProvider struct {
	snapshot observability.Snapshot
	sessions map[string]interface{}
}

func (p testProvider) Snapshot() observability.Snapshot {
	return p.snapshot
}

func (p testProvider) LiveSessions() map[string]interface{} {
	return map[string]interface{}{"sessions": p.sessions}
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
		AppSession: appserver.Session{
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
			issue.Identifier: appserver.Session{
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
		AppSession: appserver.Session{
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
		AppSession: appserver.Session{
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
		AppSession: appserver.Session{
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
			issue.Identifier: appserver.Session{
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
		AppSession: appserver.Session{
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
