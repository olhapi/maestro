package dashboardapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func (p testProvider) Status() map[string]interface{} {
	return map[string]interface{}{"active_runs": len(p.snapshot.Running)}
}

func (p testProvider) Snapshot() observability.Snapshot {
	return p.snapshot
}

func (p testProvider) LiveSessions() map[string]interface{} {
	if p.sessions == nil {
		return map[string]interface{}{"sessions": map[string]interface{}{}}
	}
	return map[string]interface{}{"sessions": p.sessions}
}

func (p testProvider) Events(since int64, limit int) map[string]interface{} {
	return map[string]interface{}{"since": since, "last_seq": 0, "events": []interface{}{}}
}

func (p testProvider) RequestRefresh() map[string]interface{} {
	return map[string]interface{}{"status": "accepted"}
}

func (p testProvider) RetryIssueNow(identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func setupDashboardServerTest(t *testing.T, provider Provider) (*kanban.Store, *httptest.Server) {
	t.Helper()
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return store, srv
}

func TestIssueExecutionEndpointReturnsLiveSession(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	issue, err := store.CreateIssue("", "", "Live execution", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	provider := testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:     issue.ID,
				Identifier:  issue.Identifier,
				State:       "in_progress",
				Phase:       "implementation",
				Attempt:     2,
				SessionID:   "thread-live-turn-live",
				TurnCount:   3,
				LastEvent:   "turn.started",
				LastMessage: "Working",
				StartedAt:   now.Add(-30 * time.Second),
				Tokens:      observability.TokenTotals{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, SecondsRunning: 30},
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: appserver.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-live-turn-live",
				ThreadID:        "thread-live",
				TurnID:          "turn-live",
				LastEvent:       "turn.started",
				LastTimestamp:   now,
				LastMessage:     "Working",
				TotalTokens:     30,
				TurnsStarted:    3,
				History: []appserver.Event{
					{Type: "turn.started", Message: "Started"},
					{Type: "tool_call_completed", Message: "Ran tests"},
				},
			},
		},
	}
	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent failed: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/v1/app/issues/" + issue.Identifier + "/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}
	if payload["session_source"] != "live" || payload["active"] != true {
		t.Fatalf("unexpected live payload: %#v", payload)
	}
	if payload["attempt_number"].(float64) != 2 {
		t.Fatalf("expected attempt 2, got %#v", payload["attempt_number"])
	}
}

func TestIssueExecutionEndpointReturnsPersistedSessionAndRetryMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	issue, err := store.CreateIssue("", "", "Persisted execution", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	provider := testProvider{
		snapshot: observability.Snapshot{
			Retrying: []observability.RetryEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    3,
				DueAt:      now.Add(2 * time.Minute),
				DueInMs:    120000,
				Error:      "stall_timeout",
				DelayType:  "failure",
			}},
		},
	}
	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_failed",
		Error:      "approval_required",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-persisted-turn-persisted",
			LastEvent:       "turn.approval_required",
			LastTimestamp:   now,
			LastMessage:     "Waiting for approval",
			History: []appserver.Event{
				{Type: "turn.started", Message: "Started"},
				{Type: "turn.approval_required", Message: "Waiting for approval"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}
	for _, kind := range []string{"run_started", "tick", "run_failed", "retry_scheduled"} {
		if err := store.AppendRuntimeEvent(kind, map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    2,
			"error":      "approval_required",
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s) failed: %v", kind, err)
		}
	}

	resp, err := http.Get(srv.URL + "/api/v1/app/issues/" + issue.Identifier + "/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}
	if payload["session_source"] != "persisted" || payload["retry_state"] != "scheduled" {
		t.Fatalf("unexpected persisted payload: %#v", payload)
	}
	if payload["failure_class"] != "stall_timeout" {
		t.Fatalf("expected retry-derived failure class, got %#v", payload["failure_class"])
	}
	events, ok := payload["runtime_events"].([]interface{})
	if !ok || len(events) != 3 {
		t.Fatalf("expected 3 filtered runtime events, got %#v", payload["runtime_events"])
	}
	first := events[0].(map[string]interface{})
	last := events[len(events)-1].(map[string]interface{})
	if first["kind"] != "run_started" || last["kind"] != "retry_scheduled" {
		t.Fatalf("expected oldest-to-newest execution events, got %#v", events)
	}
}

func TestIssueExecutionEndpointReturnsNotFoundForMissingIssue(t *testing.T) {
	_, srv := setupDashboardServerTest(t, testProvider{})
	resp, err := http.Get(srv.URL + "/api/v1/app/issues/ISS-404/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
