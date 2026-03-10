package dashboardapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type testProvider struct {
	snapshot         observability.Snapshot
	sessions         map[string]interface{}
	status           map[string]interface{}
	projectRefreshes []string
	projectStops     []string
}

func (p testProvider) Status() map[string]interface{} {
	if p.status != nil {
		return p.status
	}
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

func (p testProvider) RequestProjectRefresh(projectID string) map[string]interface{} {
	return map[string]interface{}{"status": "accepted", "project_id": projectID}
}

func (p testProvider) StopProjectRuns(projectID string) map[string]interface{} {
	return map[string]interface{}{"status": "stopped", "project_id": projectID, "stopped_runs": 0}
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

func TestSessionsEndpointReturnsMergedEntriesAndPrefersLive(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	liveIssue, err := store.CreateIssue("", "", "Live issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue live failed: %v", err)
	}
	pausedIssue, err := store.CreateIssue("", "", "Paused issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue paused failed: %v", err)
	}
	completedIssue, err := store.CreateIssue("", "", "Completed issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue completed failed: %v", err)
	}
	interruptedIssue, err := store.CreateIssue("", "", "Interrupted issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue interrupted failed: %v", err)
	}
	failedIssue, err := store.CreateIssue("", "", "Failed issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed failed: %v", err)
	}

	for _, snapshot := range []kanban.ExecutionSessionSnapshot{
		{
			IssueID:    liveIssue.ID,
			Identifier: liveIssue.Identifier,
			Phase:      "implementation",
			Attempt:    2,
			RunKind:    "run_failed",
			Error:      "approval_required",
			UpdatedAt:  now.Add(-1 * time.Minute),
			AppSession: appserver.Session{
				IssueID:         liveIssue.ID,
				IssueIdentifier: liveIssue.Identifier,
				SessionID:       "thread-live-old-turn-live-old",
				LastEvent:       "turn.approval_required",
				LastTimestamp:   now.Add(-1 * time.Minute),
				LastMessage:     "Old persisted state",
			},
		},
		{
			IssueID:    pausedIssue.ID,
			Identifier: pausedIssue.Identifier,
			Phase:      "review",
			Attempt:    3,
			RunKind:    "retry_paused",
			Error:      "stall_timeout",
			UpdatedAt:  now.Add(-2 * time.Minute),
			AppSession: appserver.Session{
				IssueID:         pausedIssue.ID,
				IssueIdentifier: pausedIssue.Identifier,
				SessionID:       "thread-paused-turn-paused",
				LastEvent:       "run.failed",
				LastTimestamp:   now.Add(-2 * time.Minute),
				LastMessage:     "Paused after repeated failures",
			},
		},
		{
			IssueID:    completedIssue.ID,
			Identifier: completedIssue.Identifier,
			Phase:      "review",
			Attempt:    1,
			RunKind:    "run_completed",
			UpdatedAt:  now.Add(-3 * time.Minute),
			AppSession: appserver.Session{
				IssueID:         completedIssue.ID,
				IssueIdentifier: completedIssue.Identifier,
				SessionID:       "thread-complete-turn-complete",
				LastEvent:       "turn.completed",
				LastTimestamp:   now.Add(-3 * time.Minute),
				LastMessage:     "Completed cleanly",
				Terminal:        true,
				TerminalReason:  "turn.completed",
			},
		},
		{
			IssueID:    interruptedIssue.ID,
			Identifier: interruptedIssue.Identifier,
			Phase:      "implementation",
			Attempt:    4,
			RunKind:    "run_started",
			UpdatedAt:  now.Add(-4 * time.Minute),
			AppSession: appserver.Session{
				IssueID:         interruptedIssue.ID,
				IssueIdentifier: interruptedIssue.Identifier,
				SessionID:       "thread-interrupted-turn-interrupted",
				LastEvent:       "turn.started",
				LastTimestamp:   now.Add(-4 * time.Minute),
				LastMessage:     "Lost live heartbeat",
			},
		},
		{
			IssueID:    failedIssue.ID,
			Identifier: failedIssue.Identifier,
			Phase:      "implementation",
			Attempt:    1,
			RunKind:    "run_failed",
			Error:      "approval_required",
			UpdatedAt:  now.Add(-5 * time.Minute),
			AppSession: appserver.Session{
				IssueID:         failedIssue.ID,
				IssueIdentifier: failedIssue.Identifier,
				SessionID:       "thread-failed-turn-failed",
				LastEvent:       "turn.approval_required",
				LastTimestamp:   now.Add(-5 * time.Minute),
				LastMessage:     "Waiting on approval",
			},
		},
	} {
		if err := store.UpsertIssueExecutionSession(snapshot); err != nil {
			t.Fatalf("UpsertIssueExecutionSession(%s) failed: %v", snapshot.Identifier, err)
		}
	}

	provider := testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:     liveIssue.ID,
				Identifier:  liveIssue.Identifier,
				State:       "in_progress",
				Phase:       "implementation",
				Attempt:     7,
				SessionID:   "thread-live-turn-live",
				TurnCount:   5,
				LastEvent:   "turn.started",
				LastMessage: "Applying changes",
				StartedAt:   now.Add(-30 * time.Second),
				Tokens:      observability.TokenTotals{TotalTokens: 33},
			}},
			Paused: []observability.PausedEntry{{
				IssueID:             pausedIssue.ID,
				Identifier:          pausedIssue.Identifier,
				Phase:               "review",
				Attempt:             3,
				PausedAt:            now.Add(-2 * time.Minute),
				Error:               "stall_timeout",
				ConsecutiveFailures: 3,
				PauseThreshold:      3,
			}},
		},
		sessions: map[string]interface{}{
			liveIssue.Identifier: appserver.Session{
				IssueID:         liveIssue.ID,
				IssueIdentifier: liveIssue.Identifier,
				SessionID:       "thread-live-turn-live",
				ThreadID:        "thread-live",
				TurnID:          "turn-live",
				LastEvent:       "turn.started",
				LastTimestamp:   now,
				LastMessage:     "Applying changes",
				TotalTokens:     33,
				EventsProcessed: 6,
				TurnsStarted:    5,
				TurnsCompleted:  4,
				History: []appserver.Event{
					{Type: "turn.started", Message: "Applying changes"},
				},
			},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/app/sessions", nil)
	NewServer(store, provider).handleSessions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode sessions payload: %v", err)
	}

	entries, ok := payload["entries"].([]interface{})
	if !ok || len(entries) != 5 {
		t.Fatalf("expected 5 merged entries, got %#v", payload["entries"])
	}
	if entries[0].(map[string]interface{})["issue_identifier"] != liveIssue.Identifier {
		t.Fatalf("expected live entry first, got %#v", entries[0])
	}
	if got := findSessionFeedEntry(t, entries, liveIssue.Identifier)["source"]; got != "live" {
		t.Fatalf("expected live source for duplicate issue, got %#v", got)
	}
	if got := findSessionFeedEntry(t, entries, pausedIssue.Identifier)["status"]; got != "paused" {
		t.Fatalf("expected paused status, got %#v", got)
	}
	if got := findSessionFeedEntry(t, entries, completedIssue.Identifier)["status"]; got != "completed" {
		t.Fatalf("expected completed status, got %#v", got)
	}
	if got := findSessionFeedEntry(t, entries, interruptedIssue.Identifier)["status"]; got != "interrupted" {
		t.Fatalf("expected interrupted status, got %#v", got)
	}
	failed := findSessionFeedEntry(t, entries, failedIssue.Identifier)
	if failed["status"] != "failed" || failed["failure_class"] != "approval_required" {
		t.Fatalf("expected failed approval_required entry, got %#v", failed)
	}
}

func findSessionFeedEntry(t *testing.T, entries []interface{}, identifier string) map[string]interface{} {
	t.Helper()
	for _, raw := range entries {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(entry["issue_identifier"])) == identifier {
			return entry
		}
	}
	t.Fatalf("missing session feed entry for %s", identifier)
	return nil
}

func asString(value interface{}) string {
	text, _ := value.(string)
	return text
}

func TestIssueExecutionEndpointReturnsPausedRetryMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	issue, err := store.CreateIssue("", "", "Paused execution", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	mux := http.NewServeMux()
	NewServer(store, testProvider{}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    3,
		RunKind:    "retry_paused",
		Error:      "stall_timeout",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-paused-turn-paused",
			LastEvent:       "item.started",
			LastTimestamp:   now,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}
	if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
		"issue_id":             issue.ID,
		"identifier":           issue.Identifier,
		"phase":                "implementation",
		"attempt":              3,
		"paused_at":            now.Format(time.RFC3339),
		"error":                "stall_timeout",
		"consecutive_failures": 3,
		"pause_threshold":      3,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent retry_paused failed: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/v1/app/issues/" + issue.Identifier + "/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}
	if payload["retry_state"] != "paused" || payload["pause_reason"] != "stall_timeout" {
		t.Fatalf("unexpected paused execution payload: %#v", payload)
	}
	if payload["consecutive_failures"].(float64) != 3 || payload["pause_threshold"].(float64) != 3 {
		t.Fatalf("unexpected paused streak payload: %#v", payload)
	}
}

func TestIssueExecutionEndpointReturnsRetryLimitPauseReason(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	issue, err := store.CreateIssue("", "", "Retry limit execution", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	mux := http.NewServeMux()
	NewServer(store, testProvider{}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
		"issue_id":        issue.ID,
		"identifier":      issue.Identifier,
		"issue_state":     "in_progress",
		"phase":           "implementation",
		"attempt":         4,
		"paused_at":       now.Format(time.RFC3339),
		"error":           "retry_limit_reached",
		"pause_threshold": 8,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent retry_paused failed: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/v1/app/issues/" + issue.Identifier + "/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}
	if payload["retry_state"] != "paused" || payload["pause_reason"] != "retry_limit_reached" {
		t.Fatalf("unexpected retry limit execution payload: %#v", payload)
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
