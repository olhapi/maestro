package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type testProvider struct{}

func (testProvider) Status() map[string]interface{} {
	return map[string]interface{}{"active_runs": 1}
}

func (testProvider) LiveSessions() map[string]interface{} {
	return map[string]interface{}{"sessions": map[string]interface{}{"iss-1": map[string]interface{}{"session_id": "th-tu", "terminal": true}}}
}

func (testProvider) Events(since int64, limit int) map[string]interface{} {
	return map[string]interface{}{"since": since, "last_seq": 2, "events": []map[string]interface{}{{"seq": int64(1), "kind": "tick"}, {"seq": int64(2), "kind": "run_started"}}}
}

func (testProvider) Snapshot() Snapshot {
	now := time.Now().UTC()
	dueAt := now.Add(5 * time.Second)
	return Snapshot{
		GeneratedAt:   now,
		WorkspaceRoot: "/tmp/workspaces",
		Running: []RunningEntry{{
			IssueID:     "issue-1",
			Identifier:  "iss-1",
			State:       "running",
			SessionID:   "th-tu",
			TurnCount:   2,
			LastEvent:   "turn.completed",
			LastMessage: "done",
			StartedAt:   now.Add(-10 * time.Second),
			Tokens:      TokenTotals{InputTokens: 3, OutputTokens: 4, TotalTokens: 7, SecondsRunning: 10},
		}},
		Retrying: []RetryEntry{{
			IssueID:    "issue-2",
			Identifier: "iss-2",
			Attempt:    3,
			DueAt:      dueAt,
			DueInMs:    5000,
			Error:      "retry later",
		}},
		CodexTotals: TokenTotals{InputTokens: 3, OutputTokens: 4, TotalTokens: 7, SecondsRunning: 10},
	}
}

func (testProvider) RequestRefresh() map[string]interface{} {
	return map[string]interface{}{"requested_at": time.Now().UTC().Format(time.RFC3339), "status": "accepted"}
}

func TestServerStartsAndServesState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Start(ctx, ":18987", testProvider{})
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:18987/api/v1/state")
	if err != nil {
		t.Fatalf("failed GET state: %v", err)
	}
	defer resp.Body.Close()

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	counts, ok := payload["counts"].(map[string]interface{})
	if !ok || counts["running"].(float64) != 1 {
		t.Fatalf("unexpected state payload: %#v", payload)
	}

	resp2, err := http.Get("http://127.0.0.1:18987/api/v1/sessions")
	if err != nil {
		t.Fatalf("failed GET sessions: %v", err)
	}
	defer resp2.Body.Close()
	var payload2 map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&payload2); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if _, ok := payload2["sessions"]; !ok {
		t.Fatalf("unexpected sessions payload: %#v", payload2)
	}

	resp3, err := http.Get("http://127.0.0.1:18987/api/v1/sessions?issue=iss-1")
	if err != nil {
		t.Fatalf("failed GET session by issue: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp3.StatusCode)
	}

	resp4, err := http.Get("http://127.0.0.1:18987/api/v1/sessions?issue=missing")
	if err != nil {
		t.Fatalf("failed GET missing issue: %v", err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp4.StatusCode)
	}

	resp5, err := http.Get("http://127.0.0.1:18987/api/v1/events?since=1&limit=10")
	if err != nil {
		t.Fatalf("failed GET events: %v", err)
	}
	defer resp5.Body.Close()
	var payload5 map[string]interface{}
	if err := json.NewDecoder(resp5.Body).Decode(&payload5); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if payload5["last_seq"].(float64) != 2 {
		t.Fatalf("unexpected events payload: %#v", payload5)
	}

	resp6, err := http.Get("http://127.0.0.1:18987/api/v1/dashboard")
	if err != nil {
		t.Fatalf("failed GET dashboard: %v", err)
	}
	defer resp6.Body.Close()
	var payload6 map[string]interface{}
	if err := json.NewDecoder(resp6.Body).Decode(&payload6); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if _, ok := payload6["state"]; !ok {
		t.Fatalf("dashboard missing state: %#v", payload6)
	}
	if _, ok := payload6["events"]; !ok {
		t.Fatalf("dashboard missing events: %#v", payload6)
	}

	resp7, err := http.Get("http://127.0.0.1:18987/api/v1/iss-1")
	if err != nil {
		t.Fatalf("failed GET issue payload: %v", err)
	}
	defer resp7.Body.Close()
	if resp7.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for issue payload, got %d", resp7.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:18987/api/v1/refresh", nil)
	if err != nil {
		t.Fatalf("request refresh: %v", err)
	}
	resp8, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed POST refresh: %v", err)
	}
	defer resp8.Body.Close()
	if resp8.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp8.StatusCode)
	}
}
