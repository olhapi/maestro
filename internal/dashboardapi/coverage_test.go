package dashboardapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type webhookStatusProvider struct {
	testProvider
	retryResult map[string]interface{}
}

func (p *webhookStatusProvider) RetryIssueNow(ctx context.Context, identifier string) map[string]interface{} {
	if p.retryResult != nil {
		return p.retryResult
	}
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func TestWebhookResponseStatusMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		result map[string]interface{}
		want   int
	}{
		{name: "accepted", result: map[string]interface{}{"status": "accepted"}, want: http.StatusAccepted},
		{name: "queued_now", result: map[string]interface{}{"status": "queued_now"}, want: http.StatusAccepted},
		{name: "refresh_requested", result: map[string]interface{}{"status": "refresh_requested"}, want: http.StatusAccepted},
		{name: "stopped", result: map[string]interface{}{"status": "stopped"}, want: http.StatusAccepted},
		{name: "pending_rerun_recorded", result: map[string]interface{}{"status": "pending_rerun_recorded"}, want: http.StatusAccepted},
		{name: "pending_rerun_already_set", result: map[string]interface{}{"status": "pending_rerun_already_set"}, want: http.StatusAccepted},
		{name: "not_found", result: map[string]interface{}{"status": "not_found"}, want: http.StatusNotFound},
		{name: "not_recurring", result: map[string]interface{}{"status": "not_recurring"}, want: http.StatusConflict},
		{name: "error", result: map[string]interface{}{"status": "error"}, want: http.StatusInternalServerError},
		{name: "default", result: map[string]interface{}{"status": "something_else"}, want: http.StatusOK},
		{name: "missing", result: map[string]interface{}{}, want: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := webhookResponseStatus(tc.result); got != tc.want {
				t.Fatalf("webhookResponseStatus(%#v) = %d, want %d", tc.result, got, tc.want)
			}
		})
	}
}

func TestNormalizeFailureClass(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "blank", input: "", want: ""},
		{name: "bootstrap", input: "  Workspace_Bootstrap_Recovery  ", want: "workspace_bootstrap"},
		{name: "approval", input: "approval_required: waiting for input", want: "approval_required"},
		{name: "turn input", input: "turn_input_required", want: "turn_input_required"},
		{name: "stall timeout", input: "stall_timeout", want: "stall_timeout"},
		{name: "unsuccessful", input: "run_unsuccessful: exit 1", want: "run_unsuccessful"},
		{name: "failed", input: "run_failed: exit 1", want: "run_failed"},
		{name: "interrupted", input: "interrupted by user", want: "run_interrupted"},
		{name: "default lower", input: "Custom Failure", want: "custom failure"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeFailureClass(tc.input); got != tc.want {
				t.Fatalf("normalizeFailureClass(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSessionHelperUtilities(t *testing.T) {
	t.Parallel()

	if got := maxInt(4, 2); got != 4 {
		t.Fatalf("maxInt(4, 2) = %d, want 4", got)
	}
	if got := maxInt(2, 7); got != 7 {
		t.Fatalf("maxInt(2, 7) = %d, want 7", got)
	}

	if got := sessionFeedSortKey("  Beta ", "alpha"); got != "beta" {
		t.Fatalf("sessionFeedSortKey(title) = %q, want beta", got)
	}
	if got := sessionFeedSortKey("", "Alpha"); got != "alpha" {
		t.Fatalf("sessionFeedSortKey(identifier) = %q, want alpha", got)
	}

	issueID := "iss-1"
	identifier := "ISS-1"
	byIssueID := map[string]appserver.PendingInteraction{
		issueID: {
			ID:              "issue-first",
			IssueID:         issueID,
			IssueIdentifier: "wrong",
		},
	}
	byIdentifier := map[string]appserver.PendingInteraction{
		identifier: {
			ID:              "identifier-fallback",
			IssueIdentifier: identifier,
		},
	}
	interaction := pendingInterruptForSession(issueID, identifier, byIssueID, byIdentifier)
	if interaction == nil || interaction.ID != "issue-first" {
		t.Fatalf("expected issue-id interrupt to win, got %#v", interaction)
	}
	interaction.ID = "changed"
	if byIssueID[issueID].ID != "issue-first" {
		t.Fatalf("expected pending interrupt to be cloned, got %#v", byIssueID[issueID])
	}
	if got := pendingInterruptForSession("", "", byIssueID, byIdentifier); got != nil {
		t.Fatalf("expected empty lookup to return nil, got %#v", got)
	}
}

func TestBuildPersistedSessionFeedEntryStatusResolution(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		snapshot    kanban.ExecutionSessionSnapshot
		retry       observability.RetryEntry
		paused      observability.PausedEntry
		wantStatus  string
		wantPhase   string
		wantAttempt int
		wantFailure string
		wantUpdated time.Time
	}{
		{
			name: "paused",
			snapshot: kanban.ExecutionSessionSnapshot{
				IssueID:    "iss-1",
				Identifier: "ISS-1",
				Phase:      "implementation",
				Attempt:    1,
				RunKind:    "run_failed",
				UpdatedAt:  base,
				AppSession: appserver.Session{
					IssueID:         "iss-1",
					IssueIdentifier: "ISS-1",
					LastEvent:       "run.failed",
					LastMessage:     "Paused",
					TotalTokens:     9,
				},
			},
			paused: observability.PausedEntry{
				IssueID:    "iss-1",
				Identifier: "ISS-1",
				Phase:      "review",
				Attempt:    4,
				Error:      "stall_timeout: waiting",
			},
			wantStatus:  "paused",
			wantPhase:   "review",
			wantAttempt: 4,
			wantFailure: "stall_timeout",
			wantUpdated: base,
		},
		{
			name: "completed from run kind",
			snapshot: kanban.ExecutionSessionSnapshot{
				IssueID:    "iss-2",
				Identifier: "ISS-2",
				Phase:      "done",
				Attempt:    2,
				RunKind:    "run_completed",
				UpdatedAt:  base.Add(time.Minute),
				AppSession: appserver.Session{
					IssueID:         "iss-2",
					IssueIdentifier: "ISS-2",
					LastTimestamp:   base.Add(30 * time.Second),
				},
			},
			wantStatus:  "completed",
			wantPhase:   "done",
			wantAttempt: 2,
			wantFailure: "run_completed",
			wantUpdated: base.Add(30 * time.Second),
		},
		{
			name: "interrupted from run started",
			snapshot: kanban.ExecutionSessionSnapshot{
				IssueID:    "iss-3",
				Identifier: "ISS-3",
				Attempt:    0,
				RunKind:    "run_started",
				Error:      "run_failed: exit 1",
				UpdatedAt:  base.Add(2 * time.Minute),
				AppSession: appserver.Session{
					IssueID:         "iss-3",
					IssueIdentifier: "ISS-3",
					LastEvent:       "turn.started",
					LastMessage:     "Working",
				},
			},
			wantStatus:  "interrupted",
			wantPhase:   "",
			wantAttempt: 0,
			wantFailure: "run_failed",
			wantUpdated: base.Add(2 * time.Minute),
		},
		{
			name: "completed from terminal reason",
			snapshot: kanban.ExecutionSessionSnapshot{
				IssueID:    "iss-4",
				Identifier: "ISS-4",
				Attempt:    0,
				UpdatedAt:  base.Add(3 * time.Minute),
				AppSession: appserver.Session{
					IssueID:         "iss-4",
					IssueIdentifier: "ISS-4",
					Terminal:        true,
					TerminalReason:  "run finished completed successfully",
				},
			},
			retry: observability.RetryEntry{
				Phase:   "done",
				Attempt: 2,
			},
			wantStatus:  "completed",
			wantPhase:   "done",
			wantAttempt: 2,
			wantUpdated: base.Add(3 * time.Minute),
		},
		{
			name: "failed terminal",
			snapshot: kanban.ExecutionSessionSnapshot{
				IssueID:    "iss-5",
				Identifier: "ISS-5",
				UpdatedAt:  base.Add(4 * time.Minute),
				AppSession: appserver.Session{
					IssueID:         "iss-5",
					IssueIdentifier: "ISS-5",
					Terminal:        true,
					TerminalReason:  "exit 1",
				},
			},
			wantStatus:  "failed",
			wantPhase:   "",
			wantAttempt: 0,
			wantUpdated: base.Add(4 * time.Minute),
		},
		{
			name: "default interrupted",
			snapshot: kanban.ExecutionSessionSnapshot{
				IssueID:    "iss-6",
				Identifier: "ISS-6",
				UpdatedAt:  base.Add(5 * time.Minute),
				AppSession: appserver.Session{
					IssueID:         "iss-6",
					IssueIdentifier: "ISS-6",
				},
			},
			wantStatus:  "interrupted",
			wantPhase:   "",
			wantAttempt: 0,
			wantUpdated: base.Add(5 * time.Minute),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildPersistedSessionFeedEntry(tc.snapshot, tc.retry, tc.paused, "Title")
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Phase != tc.wantPhase {
				t.Fatalf("phase = %q, want %q", got.Phase, tc.wantPhase)
			}
			if got.Attempt != tc.wantAttempt {
				t.Fatalf("attempt = %d, want %d", got.Attempt, tc.wantAttempt)
			}
			if got.FailureClass != tc.wantFailure {
				t.Fatalf("failure class = %q, want %q", got.FailureClass, tc.wantFailure)
			}
			if !got.UpdatedAt.Equal(tc.wantUpdated) {
				t.Fatalf("updated at = %s, want %s", got.UpdatedAt, tc.wantUpdated)
			}
			if got.Source != "persisted" || got.Active {
				t.Fatalf("expected persisted inactive entry, got %#v", got)
			}
		})
	}
}

func TestWebhookDispatchStatusMapping(t *testing.T) {
	t.Setenv(webhookBearerTokenEnv, "test-webhook-token")

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cases := []struct {
		name       string
		status     string
		wantStatus int
	}{
		{name: "accepted", status: "accepted", wantStatus: http.StatusAccepted},
		{name: "not found", status: "not_found", wantStatus: http.StatusNotFound},
		{name: "conflict", status: "not_recurring", wantStatus: http.StatusConflict},
		{name: "error", status: "error", wantStatus: http.StatusInternalServerError},
		{name: "default", status: "custom", wantStatus: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &webhookStatusProvider{
				retryResult: map[string]interface{}{
					"status": tc.status,
					"issue":  "ISS-1",
				},
			}
			mux := http.NewServeMux()
			NewServer(store, provider).Register(mux)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			resp := requestWebhookJSON(t, srv, "test-webhook-token", map[string]interface{}{
				"event":       "issue.retry",
				"delivery_id": "delivery-1",
				"payload":     map[string]interface{}{"issue_identifier": "ISS-1"},
			})
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			body := decodeResponse(t, resp)
			result := body["result"].(map[string]interface{})
			if result["status"] != tc.status {
				t.Fatalf("result status = %#v, want %q", result["status"], tc.status)
			}
		})
	}
}
