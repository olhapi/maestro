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

type stateProvider struct {
	snapshot Snapshot
}

func (p stateProvider) Status() map[string]interface{} {
	return map[string]interface{}{"active_runs": len(p.snapshot.Running)}
}

func (p stateProvider) Snapshot() Snapshot {
	return p.snapshot
}

type lookupStateProvider struct {
	stateProvider
	workspacePath string
}

func (p lookupStateProvider) IssueWorkspacePath(issueIdentifier string) string {
	return p.workspacePath
}

func TestStatePayloadAndMissingIssuePayload(t *testing.T) {
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	provider := stateProvider{
		snapshot: Snapshot{
			GeneratedAt: now,
			Running: []RunningEntry{{
				IssueID:       "issue-1",
				Identifier:    "ISS-1",
				WorkspacePath: "/workspaces/ISS-1",
				State:         "running",
				SessionID:     "session-1",
			}},
			Retrying: []RetryEntry{{
				IssueID:       "issue-2",
				Identifier:    "ISS-2",
				WorkspacePath: "/workspaces/ISS-2",
				Attempt:       2,
				DueAt:         now.Add(5 * time.Minute),
				Error:         "retry later",
			}},
			Paused: []PausedEntry{{
				IssueID:             "issue-3",
				Identifier:          "ISS-3",
				WorkspacePath:       "/workspaces/ISS-3",
				Attempt:             3,
				PausedAt:            now.Add(-5 * time.Minute),
				Error:               "pause later",
				ConsecutiveFailures: 2,
				PauseThreshold:      4,
			}},
			CodexTotals: TokenTotals{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
			RateLimits: map[string]interface{}{
				"primary": "ok",
			},
		},
	}

	state := StatePayload(provider)
	counts, ok := state["counts"].(map[string]int)
	if !ok || counts["running"] != 1 || counts["retrying"] != 1 || counts["paused"] != 1 {
		t.Fatalf("unexpected state counts: %#v", state["counts"])
	}
	if _, ok := state["generated_at"].(string); !ok {
		t.Fatalf("expected generated_at timestamp, got %#v", state["generated_at"])
	}
	if running, ok := state["running"].([]map[string]interface{}); !ok || len(running) != 1 {
		t.Fatalf("expected running payload slice, got %#v", state["running"])
	}
	if _, found := IssuePayload(provider, "missing"); found {
		t.Fatal("expected missing issue payload to return false")
	}
}

func TestStartInProcessMode(t *testing.T) {
	t.Setenv(inProcessServerEnv, "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := nextFakeAddr()
	server, err := Start(ctx, addr, testProvider{})
	if err != nil {
		t.Fatalf("Start in-process: %v", err)
	}
	if server == nil {
		t.Fatal("expected server handle")
	}

	resp, err := http.Get("http://" + addr + "/api/v1/state")
	if err != nil {
		t.Fatalf("GET in-process state: %v", err)
	}
	defer resp.Body.Close()

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode state payload: %v", err)
	}
	if _, ok := payload["counts"]; !ok {
		t.Fatalf("expected state payload counts, got %#v", payload)
	}

	cancel()
	time.Sleep(100 * time.Millisecond)
}

func TestIssueWorkspaceAndRecentEventBranches(t *testing.T) {
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	provider := stateProvider{
		snapshot: Snapshot{
			GeneratedAt:   now,
			WorkspaceRoot: "/workspaces",
		},
	}

	running := RunningEntry{WorkspacePath: "  /explicit/path  "}
	if got := issueWorkspacePath(provider, provider.snapshot, "ISS-1", &running, nil, nil); got != "/explicit/path" {
		t.Fatalf("expected running workspace path to win, got %q", got)
	}
	if got := issueWorkspacePath(provider, provider.snapshot, "ISS-1", nil, nil, nil); got != "/workspaces/ISS-1" {
		t.Fatalf("expected workspace root fallback, got %q", got)
	}
	if got := issueWorkspacePath(lookupStateProvider{workspacePath: "/looked/up"}, Snapshot{}, "ISS-2", nil, nil, nil); got != "/looked/up" {
		t.Fatalf("expected provider workspace lookup to win, got %q", got)
	}

	runningPayload := runningEntryPayload(RunningEntry{
		IssueID:       "issue-1",
		Identifier:    "ISS-1",
		State:         "running",
		Phase:         "execution",
		Attempt:       2,
		SessionID:     "session-1",
		TurnCount:     3,
		LastEvent:     "turn.completed",
		LastMessage:   "  hello\\nworld  ",
		StartedAt:     now,
		LastEventAt:   &now,
		WorkspacePath: "/workspaces/ISS-1",
	})
	if runningPayload["last_message"] != "hello world" {
		t.Fatalf("expected message sanitization, got %#v", runningPayload["last_message"])
	}
	if runningPayload["workspace_path"] != "/workspaces/ISS-1" {
		t.Fatalf("expected workspace path to be preserved, got %#v", runningPayload["workspace_path"])
	}
	if runningPayload["last_event_at"] != now.UTC().Format(time.RFC3339) {
		t.Fatalf("unexpected last_event_at: %#v", runningPayload["last_event_at"])
	}

	if events := recentEventsPayload(RunningEntry{}); len(events) != 0 {
		t.Fatalf("expected empty recent events when no timestamp exists, got %#v", events)
	}
	events := recentEventsPayload(RunningEntry{
		LastEventAt: &now,
		LastEvent:   "turn.completed",
		LastMessage: "  hello\\nworld  ",
	})
	if len(events) != 1 || events[0]["message"] != "hello world" {
		t.Fatalf("unexpected recent events payload: %#v", events)
	}
}

func TestStartInProcessRejectsInvalidAddress(t *testing.T) {
	t.Setenv(inProcessServerEnv, "1")
	if _, err := Start(context.Background(), "not-a-listener", testProvider{}); err == nil {
		t.Fatal("expected in-process start to reject invalid address")
	}
}

func TestStartUsesTCPListenerWhenInProcessModeIsDisabled(t *testing.T) {
	t.Setenv(inProcessServerEnv, "")

	origListen := listenFunc
	t.Cleanup(func() {
		listenFunc = origListen
	})
	addr := nextFakeAddr()
	listenFunc = func(network, address string) (net.Listener, error) {
		if network != "tcp" || address != addr {
			t.Fatalf("unexpected listen request: %s %s", network, address)
		}
		return &fakeListener{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 29101}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, err := Start(ctx, addr, testProvider{})
	if err != nil {
		t.Fatalf("Start TCP mode: %v", err)
	}
	if server == nil {
		t.Fatal("expected server handle")
	}
	if server.http.Addr != addr {
		t.Fatalf("unexpected server addr: %q", server.http.Addr)
	}
}

func TestRegisterRoutesMethodAndPathBranches(t *testing.T) {
	RegisterRoutes(nil, stateProvider{})

	mux := http.NewServeMux()
	RegisterRoutes(mux, stateProvider{})
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server: %v", err)
	}
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/refresh")
	if err != nil {
		t.Fatalf("GET refresh: %v", err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected GET refresh to be rejected, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/api/v1/issues/extra/path")
	if err != nil {
		t.Fatalf("GET nested issue path: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected nested issue path to 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/api/v1/events?since=bad&limit=bad")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode events payload: %v", err)
	}
	if payload["since"] != float64(0) || payload["last_seq"] != float64(0) {
		t.Fatalf("expected bad query parameters to fall back to defaults, got %#v", payload)
	}
}
