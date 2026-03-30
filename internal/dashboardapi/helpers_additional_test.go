package dashboardapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type retryErrorProvider struct {
	testProvider
}

func (p retryErrorProvider) RetryIssueNow(ctx context.Context, identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "error", "error": "dispatch failed"}
}

func TestDashboardSessionFeedHelperBranches(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProject("Platform", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Session feed issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	server := NewServer(store, testProvider{})
	issue = markLegacyPendingPlan(t, store, issue.ID, "Build the first pass")
	if _, err := server.requestIssuePlanRevision(context.Background(), issue, "Tighten the plan"); err != nil {
		t.Fatalf("requestIssuePlanRevision: %v", err)
	}
	planning, err := store.GetIssuePlanning(issue)
	if err != nil {
		t.Fatalf("GetIssuePlanning: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	liveSessions := map[string]interface{}{
		issue.Identifier: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			LastMessage:     "Session is running",
			LastTimestamp:   now,
			TotalTokens:     0,
			TurnsStarted:    1,
		},
	}

	alert := agentruntime.PendingInteraction{
		ID:              "alert-1",
		Kind:            agentruntime.PendingInteractionKindAlert,
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		LastActivityAt:  &now,
		LastActivity:    "Blocked by a repo alert",
	}
	provider := testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:       issue.ID,
				Identifier:    issue.Identifier,
				State:         "in_progress",
				Phase:         "implementation",
				Attempt:       2,
				SessionID:     "session-1",
				TurnCount:     3,
				LastEvent:     "turn.started",
				LastMessage:   "Working",
				StartedAt:     now.Add(-5 * time.Minute),
				LastEventAt:   &now,
				Tokens:        observability.TokenTotals{TotalTokens: 7},
				WorkspacePath: filepath.Join(t.TempDir(), "workspace"),
			}},
		},
		pendingInterruptsByIssue: map[string]agentruntime.PendingInteraction{
			issue.ID: alert,
		},
	}

	entries, err := buildSessionFeedEntries(store, provider, liveSessions)
	if err != nil {
		t.Fatalf("buildSessionFeedEntries blocked: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one live entry, got %#v", entries)
	}
	if entries[0].Source != "live" || entries[0].Status != "blocked" {
		t.Fatalf("expected blocked live entry, got %#v", entries[0])
	}
	if entries[0].IssueTitle != issue.Title || entries[0].Planning == nil {
		t.Fatalf("expected issue title and planning summary to be populated, got %#v", entries[0])
	}
	if entries[0].PendingInterrupt == nil || entries[0].PendingInterrupt.ID != alert.ID {
		t.Fatalf("expected pending interrupt to be included, got %#v", entries[0].PendingInterrupt)
	}

	provider.pendingInterruptsByIssue = nil
	entries, err = buildSessionFeedEntries(store, provider, liveSessions)
	if err != nil {
		t.Fatalf("buildSessionFeedEntries revision queued: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one live entry on revision queued path, got %#v", entries)
	}
	if entries[0].Status != "revision_queued" || entries[0].LastMessage != queuedPlanRevisionText {
		t.Fatalf("expected revision queued live entry, got %#v", entries[0])
	}

	draftingAt := now.Add(-time.Minute)
	awaitingAt := now.Add(-2 * time.Minute)
	revisionAt := now.Add(-3 * time.Minute)
	for _, tc := range []struct {
		name       string
		planning   *kanban.IssuePlanning
		wantStatus string
		wantMsg    string
	}{
		{
			name: "drafting",
			planning: &kanban.IssuePlanning{
				Status:    kanban.IssuePlanningStatusDrafting,
				UpdatedAt: draftingAt,
				OpenedAt:  draftingAt,
				ClosedAt:  nil,
			},
			wantStatus: "active",
			wantMsg:    "Revising the plan.",
		},
		{
			name: "awaiting approval",
			planning: &kanban.IssuePlanning{
				Status:    kanban.IssuePlanningStatusAwaitingApproval,
				UpdatedAt: awaitingAt,
				OpenedAt:  awaitingAt,
				ClosedAt:  nil,
			},
			wantStatus: "waiting",
			wantMsg:    "Plan ready for approval.",
		},
		{
			name: "revision requested",
			planning: &kanban.IssuePlanning{
				Status:    kanban.IssuePlanningStatusRevisionRequested,
				UpdatedAt: revisionAt,
				OpenedAt:  revisionAt,
				ClosedAt:  nil,
			},
			wantStatus: "revision_queued",
			wantMsg:    queuedPlanRevisionText,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, msg, updatedAt, ok := openPlanningFeedState(tc.planning)
			if !ok || status != tc.wantStatus || msg != tc.wantMsg || updatedAt == nil {
				t.Fatalf("openPlanningFeedState(%s) = %q %q %v %v", tc.name, status, msg, updatedAt, ok)
			}
		})
	}

	persistedIssue := *issue
	persistedIssue.PendingPlanRevisionMarkdown = "Please revise"
	persistedIssue.PendingPlanRevisionRequestedAt = &now
	pausedAt := now.Add(-10 * time.Minute)
	snapshot := kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    4,
		RunKind:    "run_failed",
		Error:      "run_failed",
		UpdatedAt:  now,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			LastEvent:       "turn.completed",
			LastMessage:     "Finished",
			TotalTokens:     12,
			TurnsStarted:    2,
			TurnsCompleted:  1,
			Terminal:        true,
			TerminalReason:  "turn.completed",
		},
	}
	completedSnapshot := snapshot
	completedSnapshot.RunKind = "run_completed"
	completedSnapshot.Error = ""
	cases := []struct {
		name     string
		snapshot kanban.ExecutionSessionSnapshot
		retry    observability.RetryEntry
		paused   observability.PausedEntry
		issue    *kanban.Issue
		planning *kanban.IssuePlanning
		want     string
	}{
		{
			name:     "paused wins",
			snapshot: snapshot,
			paused: observability.PausedEntry{
				Identifier: issue.Identifier,
				Error:      "plan_approval_pending",
				Phase:      "implementation",
				Attempt:    5,
				PausedAt:   pausedAt,
			},
			issue:    &kanban.Issue{},
			planning: nil,
			want:     "waiting",
		},
		{
			name:     "revision wins",
			snapshot: snapshot,
			issue:    &persistedIssue,
			planning: planning,
			want:     "revision_queued",
		},
		{
			name:     "completed wins",
			snapshot: completedSnapshot,
			retry: observability.RetryEntry{
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    6,
			},
			issue:    &kanban.Issue{},
			planning: nil,
			want:     "completed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := buildPersistedSessionFeedEntry(tc.snapshot, tc.retry, tc.paused, tc.issue, tc.planning, issue.Title)
			if entry.Status != tc.want {
				t.Fatalf("expected %s status, got %#v", tc.want, entry)
			}
		})
	}

	t.Run("persisted helper fallbacks", func(t *testing.T) {
		fallbackSnapshot := snapshot
		fallbackSnapshot.RunKind = ""
		fallbackSnapshot.Error = ""
		fallbackSnapshot.AppSession.Terminal = false
		fallbackSnapshot.AppSession.TerminalReason = ""

		tests := []struct {
			name     string
			snapshot kanban.ExecutionSessionSnapshot
			retry    observability.RetryEntry
			paused   observability.PausedEntry
			want     string
		}{
			{
				name:     "paused branch",
				snapshot: fallbackSnapshot,
				paused:   observability.PausedEntry{Identifier: issue.Identifier, Phase: "implementation", Attempt: 7},
				want:     "paused",
			},
			{
				name:     "run started branch",
				snapshot: func() kanban.ExecutionSessionSnapshot { s := fallbackSnapshot; s.RunKind = "run_started"; return s }(),
				want:     "interrupted",
			},
			{
				name:     "failure branch",
				snapshot: func() kanban.ExecutionSessionSnapshot { s := fallbackSnapshot; s.Error = "run_failed"; return s }(),
				want:     "failed",
			},
			{
				name: "terminal failed branch",
				snapshot: func() kanban.ExecutionSessionSnapshot {
					s := fallbackSnapshot
					s.AppSession.Terminal = true
					s.AppSession.TerminalReason = "stopped"
					return s
				}(),
				want: "failed",
			},
			{
				name:     "default branch",
				snapshot: fallbackSnapshot,
				want:     "interrupted",
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				entry := buildPersistedSessionFeedEntry(tc.snapshot, tc.retry, tc.paused, &kanban.Issue{}, nil, issue.Title)
				if entry.Status != tc.want {
					t.Fatalf("expected %s status, got %#v", tc.want, entry)
				}
			})
		}
	})

	t.Run("loading helper fallbacks", func(t *testing.T) {
		errorStore, err := kanban.NewStore(filepath.Join(t.TempDir(), "error.db"))
		if err != nil {
			t.Fatalf("NewStore error store: %v", err)
		}
		issueForLookup, err := errorStore.CreateIssue("", "", "Lookup issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue lookup: %v", err)
		}
		if err := errorStore.Close(); err != nil {
			t.Fatalf("Close error store: %v", err)
		}
		live := map[string]agentruntime.Session{
			"thread-fallback": {IssueIdentifier: issueForLookup.Identifier},
		}
		if got := loadIssueTitlesByIdentifier(errorStore, live, nil); len(got) != 0 {
			t.Fatalf("expected lookup error to return empty map, got %#v", got)
		}
		if got := loadPlanningByIdentifier(errorStore, map[string]*kanban.Issue{issueForLookup.Identifier: issueForLookup, "missing": nil}); len(got) != 0 {
			t.Fatalf("expected planning lookup error to return empty map, got %#v", got)
		}
	})

	t.Run("recent blank identifiers are skipped", func(t *testing.T) {
		skippedIssue, err := store.CreateIssue(project.ID, "", "Skipped recent", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue skipped recent: %v", err)
		}
		if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
			IssueID:    skippedIssue.ID,
			Identifier: "",
			UpdatedAt:   now,
			AppSession: agentruntime.Session{
				IssueID:       skippedIssue.ID,
				LastTimestamp: now,
			},
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession: %v", err)
		}
		entries, err := buildSessionFeedEntries(store, provider, map[string]interface{}{})
		if err != nil {
			t.Fatalf("buildSessionFeedEntries blank recent: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("expected blank recent identifiers to be skipped, got %#v", entries)
		}
	})

	t.Run("live identifier fallback and sort tie-break", func(t *testing.T) {
		firstIssue, err := store.CreateIssue(project.ID, "", "Shared title", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue first: %v", err)
		}
		secondIssue, err := store.CreateIssue(project.ID, "", "Shared title", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue second: %v", err)
		}
		titles := loadIssueTitlesByIdentifier(store, map[string]agentruntime.Session{
			"": {},
			"first": {
				IssueIdentifier: firstIssue.Identifier,
			},
		}, []kanban.ExecutionSessionSnapshot{
			{
				IssueID:    secondIssue.ID,
				Identifier: "",
				AppSession: agentruntime.Session{
					IssueID:         secondIssue.ID,
					IssueIdentifier: "",
				},
			},
		})
		if got := titles[firstIssue.Identifier]; got != firstIssue.Title {
			t.Fatalf("expected identifier lookup to resolve title, got %#v", titles)
		}
		if _, ok := titles[""]; ok {
			t.Fatalf("expected blank live identifier to be skipped, got %#v", titles)
		}

		entries, err := buildSessionFeedEntries(store, testProvider{
			snapshot: observability.Snapshot{
				Running: []observability.RunningEntry{
					{
						IssueID:    firstIssue.ID,
						Identifier: firstIssue.Identifier,
						StartedAt:  now.Add(-time.Minute),
					},
					{
						IssueID:    secondIssue.ID,
						Identifier: secondIssue.Identifier,
						StartedAt:  now.Add(-2 * time.Minute),
					},
				},
			},
		}, map[string]interface{}{
			firstIssue.Identifier: map[string]interface{}{"issue_identifier": firstIssue.Identifier},
			secondIssue.Identifier: map[string]interface{}{"issue_identifier": secondIssue.Identifier},
		})
		if err != nil {
			t.Fatalf("buildSessionFeedEntries tie-break: %v", err)
		}
		if len(entries) != 2 || entries[0].IssueIdentifier != firstIssue.Identifier || entries[1].IssueIdentifier != secondIssue.Identifier {
			t.Fatalf("expected title tie-break to sort by identifier, got %#v", entries)
		}
	})

	t.Run("completed terminal fallback fields", func(t *testing.T) {
		entry := buildPersistedSessionFeedEntry(
			kanban.ExecutionSessionSnapshot{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				UpdatedAt:  now,
				AppSession: agentruntime.Session{
					IssueID:         issue.ID,
					IssueIdentifier: issue.Identifier,
					Terminal:        true,
					TerminalReason:  "turn.completed",
				},
			},
			observability.RetryEntry{Phase: "review", Attempt: 6},
			observability.PausedEntry{},
			&kanban.Issue{},
			nil,
			issue.Title,
		)
		if entry.Status != "completed" || entry.Phase != "review" || entry.Attempt != 6 {
			t.Fatalf("expected completed fallback fields to be preserved, got %#v", entry)
		}
		if !entry.Terminal || entry.TerminalReason != "turn.completed" {
			t.Fatalf("expected terminal session metadata to be preserved, got %#v", entry)
		}
	})

	t.Run("planning default state", func(t *testing.T) {
		if status, message, updatedAt, ok := openPlanningFeedState(&kanban.IssuePlanning{Status: "other", UpdatedAt: now}); ok || status != "" || message != "" || updatedAt != nil {
			t.Fatalf("expected unknown planning status to be ignored, got %q %q %v %v", status, message, updatedAt, ok)
		}
	})
}

func TestDashboardWebhookAndQueryHelperBranches(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "WORKFLOW.md"), []byte("# workflow"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	project, err := store.CreateProject("Webhook Project", "", repoRoot, filepath.Join(repoRoot, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	otherRepoRoot := t.TempDir()
	otherProject, err := store.CreateProject("Webhook Other", "", otherRepoRoot, "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	issue, err := store.CreateIssue("", "", "Webhook issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	provider := &testProvider{
		status: map[string]interface{}{"scoped_repo_path": repoRoot},
	}
	server := NewServer(store, provider)

	for _, tc := range []struct {
		name string
		key  string
		val  interface{}
		want string
	}{
		{name: "missing", key: "issue_identifier", val: nil, want: "issue_identifier is required"},
		{name: "wrong type", key: "issue_identifier", val: 7, want: "issue_identifier must be a string"},
		{name: "blank", key: "issue_identifier", val: "   ", want: "issue_identifier is required"},
		{name: "valid", key: "issue_identifier", val: issue.Identifier, want: issue.Identifier},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]interface{}{}
			if tc.val != nil {
				payload[tc.key] = tc.val
			}
			got, err := webhookString(payload, tc.key)
			if tc.name == "valid" {
				if err != nil || got != tc.want {
					t.Fatalf("webhookString valid = %q err=%v", got, err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("webhookString %s = %q err=%v", tc.name, got, err)
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/?limit=7&bad=not-a-number", nil)
	if got := queryInt(req, "limit", 3); got != 7 {
		t.Fatalf("queryInt valid = %d", got)
	}
	if got := queryInt(req, "bad", 3); got != 3 {
		t.Fatalf("queryInt invalid = %d", got)
	}
	if got := queryInt(req, "missing", 3); got != 3 {
		t.Fatalf("queryInt missing = %d", got)
	}

	dispatchCases := []struct {
		name    string
		body    webhookRequest
		want    int
		wantErr string
	}{
		{name: "issue retry", body: webhookRequest{Event: "issue.retry", Payload: map[string]interface{}{"issue_identifier": issue.Identifier}}, want: http.StatusAccepted},
		{name: "issue retry missing", body: webhookRequest{Event: "issue.retry", Payload: map[string]interface{}{}}, want: http.StatusBadRequest, wantErr: "issue_identifier is required"},
		{name: "issue run now", body: webhookRequest{Event: "issue.run_now", Payload: map[string]interface{}{"issue_identifier": issue.Identifier}}, want: http.StatusAccepted},
		{name: "issue run now missing", body: webhookRequest{Event: "issue.run_now", Payload: map[string]interface{}{}}, want: http.StatusBadRequest, wantErr: "issue_identifier is required"},
		{name: "project stop", body: webhookRequest{Event: "project.stop", Payload: map[string]interface{}{"project_id": project.ID}}, want: http.StatusAccepted},
		{name: "project stop missing", body: webhookRequest{Event: "project.stop", Payload: map[string]interface{}{}}, want: http.StatusBadRequest, wantErr: "project_id is required"},
		{name: "project run", body: webhookRequest{Event: "project.run", Payload: map[string]interface{}{"project_id": project.ID}}, want: http.StatusAccepted},
		{name: "project run missing", body: webhookRequest{Event: "project.run", Payload: map[string]interface{}{}}, want: http.StatusBadRequest, wantErr: "project_id is required"},
		{name: "project run missing project", body: webhookRequest{Event: "project.run", Payload: map[string]interface{}{"project_id": "missing"}}, want: http.StatusNotFound},
		{name: "project run out of scope", body: webhookRequest{Event: "project.run", Payload: map[string]interface{}{"project_id": otherProject.ID}}, want: http.StatusBadRequest},
		{name: "unsupported", body: webhookRequest{Event: "missing.event"}, want: http.StatusBadRequest},
	}
	for _, tc := range dispatchCases {
		t.Run(tc.name, func(t *testing.T) {
			status, result, err := server.dispatchWebhook(context.Background(), tc.body)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("expected %s to fail with %q, got result=%#v err=%v", tc.name, tc.wantErr, result, err)
				}
			} else if tc.want == http.StatusBadRequest && err == nil {
				t.Fatalf("expected %s to fail", tc.name)
			}
			if status != tc.want {
				t.Fatalf("unexpected status for %s: %d result=%#v err=%v", tc.name, status, result, err)
			}
		})
	}

	bareServer := NewServer(store, testProvider{})
	status, _, err := bareServer.dispatchWebhook(context.Background(), webhookRequest{
		Event:   "project.run",
		Payload: map[string]interface{}{"project_id": otherProject.ID},
	})
	if status != http.StatusBadRequest || err == nil || err.Error() != "project is not dispatchable" {
		t.Fatalf("expected non-dispatchable project fallback, got status=%d err=%v", status, err)
	}

	server.webhook = webhookAuthConfig{bearerToken: "secret"}
	rr := httptest.NewRecorder()
	reqBody := httptest.NewRequest(http.MethodGet, "/api/v1/webhooks", nil)
	reqBody.Header.Set("Authorization", "Bearer secret")
	server.handleWebhook(rr, reqBody)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected GET webhook to return 405, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	reqBody = httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", strings.NewReader("{"))
	reqBody.Header.Set("Authorization", "Bearer secret")
	server.handleWebhook(rr, reqBody)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid JSON webhook to return 400, got %d", rr.Code)
	}

	server.webhook = webhookAuthConfig{}
	rr = httptest.NewRecorder()
	reqBody = httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", strings.NewReader(`{"event":"issue.retry","payload":{"issue_identifier":"`+issue.Identifier+`"}}`))
	server.handleWebhook(rr, reqBody)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected disabled webhook to return 503, got %d", rr.Code)
	}

	server.webhook = webhookAuthConfig{bearerToken: "secret"}
	rr = httptest.NewRecorder()
	reqBody = httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", strings.NewReader(`{"event":"issue.retry","payload":{"issue_identifier":"`+issue.Identifier+`"}}`))
	server.handleWebhook(rr, reqBody)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized webhook to return 401, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	reqBody = httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", strings.NewReader(`{"payload":{}}`))
	reqBody.Header.Set("Authorization", "Bearer secret")
	server.handleWebhook(rr, reqBody)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected missing event webhook to return 400, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	reqBody = httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", strings.NewReader(`{"event":"issue.retry","payload":{"issue_identifier":"`+issue.Identifier+`"}}`))
	reqBody.Header.Set("Authorization", "Bearer secret")
	server.handleWebhook(rr, reqBody)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected authorized webhook to return 202, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal webhook response: %v", err)
	}
	if payload["event"] != "issue.retry" {
		t.Fatalf("unexpected webhook payload: %#v", payload)
	}

	errorServer := NewServer(store, retryErrorProvider{})
	errorServer.webhook = webhookAuthConfig{bearerToken: "secret"}
	rr = httptest.NewRecorder()
	reqBody = httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", strings.NewReader(`{"event":"issue.retry","payload":{"issue_identifier":"`+issue.Identifier+`"}}`))
	reqBody.Header.Set("Authorization", "Bearer secret")
	errorServer.handleWebhook(rr, reqBody)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected webhook dispatch error to return 500, got %d body=%s", rr.Code, rr.Body.String())
	}
}
