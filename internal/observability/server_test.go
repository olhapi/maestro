package observability

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/testutil/inprocessserver"
)

type testProvider struct{}

func (testProvider) Status() map[string]interface{} {
	return map[string]interface{}{"active_runs": 1}
}

func (testProvider) LiveSessions() map[string]interface{} {
	return map[string]interface{}{"sessions": map[string]interface{}{"iss-1": map[string]interface{}{"issue_id": "issue-1", "issue_identifier": "iss-1", "session_id": "th-tu", "terminal": true}}}
}

func (testProvider) IssueWorkspacePath(issueIdentifier string) string {
	if issueIdentifier == "iss-1" {
		return "/tmp/worktrees/platform/iss-1"
	}
	return ""
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
			IssueID:       "issue-1",
			Identifier:    "iss-1",
			WorkspacePath: "/tmp/worktrees/platform/iss-1",
			State:         "running",
			SessionID:     "th-tu",
			TurnCount:     2,
			LastEvent:     "turn.completed",
			LastMessage:   "done",
			StartedAt:     now.Add(-10 * time.Second),
			Tokens:        TokenTotals{InputTokens: 3, OutputTokens: 4, TotalTokens: 7, SecondsRunning: 10},
		}},
		Retrying: []RetryEntry{{
			IssueID:       "issue-2",
			Identifier:    "iss-2",
			WorkspacePath: "/tmp/worktrees/platform/iss-2",
			Attempt:       3,
			DueAt:         dueAt,
			DueInMs:       5000,
			Error:         "retry later",
		}},
		CodexTotals: TokenTotals{InputTokens: 3, OutputTokens: 4, TotalTokens: 7, SecondsRunning: 10},
	}
}

func (testProvider) RequestRefresh() map[string]interface{} {
	return map[string]interface{}{"requested_at": time.Now().UTC().Format(time.RFC3339), "status": "accepted"}
}

type statusOnlyProvider struct{}

func (statusOnlyProvider) Status() map[string]interface{} {
	return map[string]interface{}{"mode": "status-only"}
}

type fakeListener struct {
	addr net.Addr
}

func (l *fakeListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }

func (l *fakeListener) Close() error { return nil }

func (l *fakeListener) Addr() net.Addr { return l.addr }

func TestServerStartsAndServesState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := nextFakeAddr()
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
	runningEntries, ok := payload["running"].([]interface{})
	if !ok || len(runningEntries) != 1 {
		t.Fatalf("unexpected running payload: %#v", payload["running"])
	}
	running, ok := runningEntries[0].(map[string]interface{})
	if !ok || running["workspace_path"] != "/tmp/worktrees/platform/iss-1" {
		t.Fatalf("expected running workspace path in state payload, got %#v", payload["running"])
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
	var issuePayload map[string]interface{}
	if err := json.NewDecoder(resp7.Body).Decode(&issuePayload); err != nil {
		t.Fatalf("decode issue payload: %v", err)
	}
	workspace, ok := issuePayload["workspace"].(map[string]interface{})
	if !ok || workspace["path"] != "/tmp/worktrees/platform/iss-1" {
		t.Fatalf("expected issue payload to use stored workspace path, got %#v", issuePayload)
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

	cancel()
	time.Sleep(100 * time.Millisecond)
}

func TestStartFailsWhenPortIsOccupied(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := nextFakeAddr()
	occupied, err := inprocessserver.NewWithURL("http://"+addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if err != nil {
		t.Fatalf("occupy fake addr: %v", err)
	}
	defer occupied.Close()

	if _, err := Start(ctx, addr, testProvider{}); err == nil {
		t.Fatal("expected Start to fail on an occupied port")
	}
}

func TestStartNormalMode(t *testing.T) {
	t.Setenv(inProcessServerEnv, "")

	origListen := listenFunc
	t.Cleanup(func() {
		listenFunc = origListen
	})
	listenFunc = func(network, address string) (net.Listener, error) {
		return &fakeListener{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 29099}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, err := Start(ctx, "127.0.0.1:29099", statusOnlyProvider{})
	if err != nil {
		t.Fatalf("Start normal mode: %v", err)
	}
	if server == nil || server.http == nil {
		t.Fatal("expected server handle")
	}
	if server.http.Addr != "127.0.0.1:29099" {
		t.Fatalf("unexpected server addr: %q", server.http.Addr)
	}
	_ = server.http.Close()
}

func TestIssuePayloadFallsBackToProviderWorkspacePath(t *testing.T) {
	payload, found := IssuePayload(testProvider{}, "iss-1")
	if !found {
		t.Fatal("expected issue payload")
	}
	workspace, ok := payload["workspace"].(map[string]interface{})
	if !ok || workspace["path"] != "/tmp/worktrees/platform/iss-1" {
		t.Fatalf("expected provider workspace lookup, got %#v", payload)
	}
}

func TestRegisterRoutesFallbackBranches(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, statusOnlyProvider{})
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server: %v", err)
	}
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/state")
	if err != nil {
		t.Fatalf("GET state: %v", err)
	}
	defer resp.Body.Close()
	var statePayload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&statePayload); err != nil {
		t.Fatalf("decode state payload: %v", err)
	}
	if statePayload["mode"] != "status-only" {
		t.Fatalf("expected direct status payload, got %#v", statePayload)
	}

	resp, err = http.Get(srv.URL + "/api/v1/sessions")
	if err != nil {
		t.Fatalf("GET sessions: %v", err)
	}
	defer resp.Body.Close()
	var sessionsPayload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&sessionsPayload); err != nil {
		t.Fatalf("decode sessions payload: %v", err)
	}
	if _, ok := sessionsPayload["sessions"]; !ok {
		t.Fatalf("expected empty sessions payload, got %#v", sessionsPayload)
	}

	resp, err = http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	var eventsPayload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&eventsPayload); err != nil {
		t.Fatalf("decode events payload: %v", err)
	}
	if eventsPayload["since"] != float64(0) || eventsPayload["last_seq"] != float64(0) {
		t.Fatalf("expected empty events payload, got %#v", eventsPayload)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/refresh", nil)
	if err != nil {
		t.Fatalf("new refresh request: %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST refresh: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected accepted refresh, got %d", resp.StatusCode)
	}
}

func TestStartUsesNetworkListenerWhenInProcessModeIsDisabled(t *testing.T) {
	t.Setenv(inProcessServerEnv, "")

	origListen := listenFunc
	t.Cleanup(func() {
		listenFunc = origListen
	})
	addr := "127.0.0.1:29100"
	listenFunc = func(network, address string) (net.Listener, error) {
		if network != "tcp" || address != addr {
			t.Fatalf("unexpected listen request: %s %s", network, address)
		}
		return &fakeListener{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 29100}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, err := Start(ctx, addr, testProvider{})
	if err != nil {
		t.Fatalf("Start network mode: %v", err)
	}
	if server == nil || server.http == nil {
		t.Fatal("expected server handle")
	}
	if server.http.Addr != addr {
		t.Fatalf("unexpected server addr: %q", server.http.Addr)
	}

	cancel()
	time.Sleep(100 * time.Millisecond)
}
