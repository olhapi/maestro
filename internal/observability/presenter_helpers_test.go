package observability

import (
	"strings"
	"testing"
	"time"
)

type snapshotOnlyProvider struct {
	snapshot Snapshot
}

func (p snapshotOnlyProvider) Snapshot() Snapshot { return p.snapshot }

type refreshOnlyProvider struct{}

func (refreshOnlyProvider) RequestRefresh() map[string]interface{} {
	return map[string]interface{}{"requested_at": "2026-03-29T12:00:00Z", "status": "queued"}
}

func TestIssuePayloadUsesWorkspaceRootAndPausedEntries(t *testing.T) {
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	provider := snapshotOnlyProvider{
		snapshot: Snapshot{
			GeneratedAt:   now,
			WorkspaceRoot: "/workspaces",
			Retrying: []RetryEntry{{
				IssueID:       "issue-1",
				Identifier:    "ISS-1",
				WorkspacePath: "",
				Phase:         "review",
				Attempt:       2,
				DueAt:         now.Add(5 * time.Minute),
				Error:         "retry later",
			}},
			Paused: []PausedEntry{{
				IssueID:             "issue-2",
				Identifier:          "ISS-2",
				WorkspacePath:       "",
				Phase:               "done",
				Attempt:             3,
				PausedAt:            now.Add(-10 * time.Minute),
				Error:               "plan_approval_pending",
				ConsecutiveFailures: 2,
				PauseThreshold:      3,
			}},
		},
	}

	retryPayload, found := IssuePayload(provider, "ISS-1")
	if !found {
		t.Fatal("expected retry issue payload")
	}
	workspace, _ := retryPayload["workspace"].(map[string]interface{})
	if workspace["path"] != "/workspaces/ISS-1" {
		t.Fatalf("expected workspace root fallback, got %#v", workspace)
	}
	if retryPayload["status"] != "retrying" {
		t.Fatalf("expected retrying status, got %#v", retryPayload["status"])
	}
	if retryPayload["attempts"].(map[string]interface{})["current_retry_attempt"] != 2 {
		t.Fatalf("unexpected retry attempts payload: %#v", retryPayload["attempts"])
	}

	pausedPayload, found := IssuePayload(provider, "ISS-2")
	if !found {
		t.Fatal("expected paused issue payload")
	}
	if pausedPayload["status"] != "paused" {
		t.Fatalf("expected paused status, got %#v", pausedPayload["status"])
	}
	paused := pausedPayload["paused"].(map[string]interface{})
	if paused["consecutive_failures"] != 2 || paused["pause_threshold"] != 3 {
		t.Fatalf("unexpected paused payload: %#v", paused)
	}
	if pausedPayload["workspace"].(map[string]interface{})["path"] != "/workspaces/ISS-2" {
		t.Fatalf("expected paused workspace root fallback, got %#v", pausedPayload["workspace"])
	}
}

func TestRefreshPayloadAndDashboardHelpers(t *testing.T) {
	refresh := RefreshPayload(nil)
	if refresh["requested_at"] == "" {
		t.Fatalf("expected refresh timestamp, got %#v", refresh)
	}
	if got := RefreshPayload(refreshOnlyProvider{}); got["status"] != "queued" {
		t.Fatalf("expected provider refresh payload, got %#v", got)
	}
	if sanitizeMessage("  hello\\nworld\n again  ") != "hello world again" {
		t.Fatalf("expected sanitized message, got %q", sanitizeMessage("  hello\\nworld\n again  "))
	}
	if restartCount(nil) != 0 {
		t.Fatalf("expected nil retry entry restart count to be zero")
	}
	if restartCount(&RetryEntry{Attempt: 3}) != 2 {
		t.Fatalf("expected restart count from attempt, got %d", restartCount(&RetryEntry{Attempt: 3}))
	}

	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	out := FormatDashboard(Snapshot{
		GeneratedAt: now,
		Paused: []PausedEntry{{
			Identifier:          "ISS-9",
			Attempt:             4,
			PausedAt:            now.Add(-5 * time.Minute),
			Error:               "pause later",
			ConsecutiveFailures: 3,
			PauseThreshold:      5,
		}},
	}, DashboardOptions{Now: now})
	for _, want := range []string{"paused_queue:", "ISS-9", "failures=3/5"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in dashboard output, got %q", want, out)
		}
	}
}

func TestBroadcasterUnsubscribeAndNilReceiver(t *testing.T) {
	(*Broadcaster)(nil).BroadcastUpdate()

	broadcaster := NewBroadcaster()
	ch, unsubscribe := broadcaster.Subscribe()
	unsubscribe()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected unsubscribed channel to be closed")
		}
	default:
		t.Fatal("expected unsubscribed channel to be closed immediately")
	}
}
