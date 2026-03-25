package observability

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testProvider struct{}

func (testProvider) Status() map[string]interface{} {
	return map[string]interface{}{"active_runs": 1}
}

func (testProvider) LiveSessions() map[string]interface{} {
	return map[string]interface{}{"sessions": map[string]interface{}{"iss-1": map[string]interface{}{"issue_id": "issue-1", "issue_identifier": "iss-1", "session_id": "th-tu", "terminal": true}}}
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

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	if _, err := Start(ctx, addr, testProvider{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://" + addr + "/api/v1/state")
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

	resp2, err := http.Get("http://" + addr + "/api/v1/sessions")
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

	resp3, err := http.Get("http://" + addr + "/api/v1/sessions?issue=iss-1")
	if err != nil {
		t.Fatalf("failed GET session by issue: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp3.StatusCode)
	}

	resp4, err := http.Get("http://" + addr + "/api/v1/sessions?issue=missing")
	if err != nil {
		t.Fatalf("failed GET missing issue: %v", err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp4.StatusCode)
	}

	resp5, err := http.Get("http://" + addr + "/api/v1/events?since=1&limit=10")
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

	resp6, err := http.Get("http://" + addr + "/api/v1/dashboard")
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

	resp7, err := http.Get("http://" + addr + "/api/v1/iss-1")
	if err != nil {
		t.Fatalf("failed GET issue payload: %v", err)
	}
	defer resp7.Body.Close()
	if resp7.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for issue payload, got %d", resp7.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/v1/refresh", nil)
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

func TestRegisterRoutesRejectsNonGetRequests(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, testProvider{})

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/health"},
		{method: http.MethodPost, path: "/api/v1/state"},
		{method: http.MethodPost, path: "/api/v1/sessions"},
		{method: http.MethodPost, path: "/api/v1/events"},
		{method: http.MethodPost, path: "/api/v1/dashboard"},
		{method: http.MethodPost, path: "/api/v1/iss-1"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s: expected 405, got %d", tc.method, tc.path, rec.Code)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatalf("%s %s: decode response: %v", tc.method, tc.path, err)
		}
		if payload["error"] != "method_not_allowed" {
			t.Fatalf("%s %s: unexpected payload %#v", tc.method, tc.path, payload)
		}
	}
}

func TestStartFailsWhenPortIsOccupied(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	if _, err := Start(ctx, ln.Addr().String(), testProvider{}); err == nil {
		t.Fatal("expected Start to fail on an occupied port")
	}
}
