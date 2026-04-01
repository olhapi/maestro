package dashboardapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/internal/testutil/inprocessserver"
)

func TestDashboardWorkEpicsAndRuntimeEndpointsExposeCurrentData(t *testing.T) {
	srv, project, epic := setupDashboardCoverageFixture(t)

	workResp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/work", nil)
	if workResp.StatusCode != http.StatusOK {
		t.Fatalf("expected work endpoint to return 200, got %d", workResp.StatusCode)
	}
	workPayload := decodeResponse(t, workResp)

	overview := workPayload["overview"].(map[string]interface{})
	snapshot := overview["snapshot"].(map[string]interface{})
	if len(snapshot["running"].([]interface{})) != 1 {
		t.Fatalf("expected one running session in work snapshot, got %#v", snapshot["running"])
	}
	if len(snapshot["retrying"].([]interface{})) != 1 {
		t.Fatalf("expected one retrying session in work snapshot, got %#v", snapshot["retrying"])
	}
	if len(snapshot["paused"].([]interface{})) != 1 {
		t.Fatalf("expected one paused session in work snapshot, got %#v", snapshot["paused"])
	}

	board := overview["board"].(map[string]interface{})
	if board["ready"].(float64) != 1 {
		t.Fatalf("expected ready issue count in work board, got %#v", board)
	}

	if len(workPayload["projects"].([]interface{})) != 1 {
		t.Fatalf("expected one project in work payload, got %#v", workPayload["projects"])
	}
	if len(workPayload["epics"].([]interface{})) != 1 {
		t.Fatalf("expected one epic in work payload, got %#v", workPayload["epics"])
	}
	issues := workPayload["issues"].(map[string]interface{})
	if issues["total"].(float64) != 1 {
		t.Fatalf("expected one issue in work payload, got %#v", issues)
	}
	if sessions, ok := workPayload["sessions"].(map[string]interface{}); !ok || len(sessions) != 1 {
		t.Fatalf("expected one live session in work payload, got %#v", workPayload["sessions"])
	}

	epicsResp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/epics?project_id="+project.ID, nil)
	if epicsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected epics endpoint to return 200, got %d", epicsResp.StatusCode)
	}
	epicsPayload := decodeResponse(t, epicsResp)
	items := epicsPayload["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected one epic from filtered epics endpoint, got %#v", epicsPayload)
	}
	epicItem := items[0].(map[string]interface{})
	if epicItem["id"].(string) != epic.ID || epicItem["project_id"].(string) != project.ID {
		t.Fatalf("unexpected epic payload: %#v", epicItem)
	}

	runtimeEventsResp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/runtime/events?since=0&limit=5", nil)
	if runtimeEventsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected runtime events endpoint to return 200, got %d", runtimeEventsResp.StatusCode)
	}
	runtimeEventsPayload := decodeResponse(t, runtimeEventsResp)
	events := runtimeEventsPayload["events"].([]interface{})
	if len(events) != 2 {
		t.Fatalf("expected two runtime events, got %#v", runtimeEventsPayload)
	}
	if events[0].(map[string]interface{})["kind"] != "run_started" || events[1].(map[string]interface{})["kind"] != "run_completed" {
		t.Fatalf("unexpected runtime events ordering: %#v", events)
	}

	runtimeSeriesResp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/runtime/series?hours=2", nil)
	if runtimeSeriesResp.StatusCode != http.StatusOK {
		t.Fatalf("expected runtime series endpoint to return 200, got %d", runtimeSeriesResp.StatusCode)
	}
	runtimeSeriesPayload := decodeResponse(t, runtimeSeriesResp)
	series := runtimeSeriesPayload["series"].([]interface{})
	if len(series) != 2 {
		t.Fatalf("expected two runtime series buckets, got %#v", runtimeSeriesPayload)
	}
	lastBucket := series[1].(map[string]interface{})
	if lastBucket["runs_started"].(float64) != 1 || lastBucket["runs_completed"].(float64) != 1 {
		t.Fatalf("expected current bucket to include run started/completed counts, got %#v", lastBucket)
	}
	if lastBucket["tokens"].(float64) == 0 {
		t.Fatalf("expected current bucket to accumulate tokens, got %#v", lastBucket)
	}

}

func TestDashboardOverviewEndpointsSurfaceStoreErrors(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mux := http.NewServeMux()
	NewServer(store, testProvider{}).Register(mux)
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
	t.Cleanup(srv.Close)

	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "bootstrap", path: "/api/v1/app/bootstrap"},
		{name: "work", path: "/api/v1/app/work"},
		{name: "epics", path: "/api/v1/app/epics"},
		{name: "runtime events", path: "/api/v1/app/runtime/events"},
		{name: "runtime series", path: "/api/v1/app/runtime/series"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := requestJSON(t, srv, http.MethodGet, tc.path, nil)
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("%s expected 500, got %d", tc.name, resp.StatusCode)
			}
			_ = decodeResponse(t, resp)
		})
	}
}

func TestDashboardHelperAndDecoderCoverage(t *testing.T) {
	scoped := "/repo/current"
	if got := scopedRepoPathFromStatus(nil); got != "" {
		t.Fatalf("expected empty scoped repo path for nil status, got %q", got)
	}
	if got := scopedRepoPathFromStatus(map[string]interface{}{"scoped_repo_path": "  " + scoped + "  "}); got != scoped {
		t.Fatalf("expected trimmed scoped repo path, got %q", got)
	}

	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := validateScopedRepoPath(repoPath, filepath.Clean(repoPath)); err != nil {
		t.Fatalf("expected matching repo path to pass validation, got %v", err)
	}
	if err := validateScopedRepoPath(filepath.Join(repoPath, "nested"), filepath.Clean(repoPath)); err == nil {
		t.Fatal("expected mismatched repo path to fail validation")
	}
	if err := validateScopedRepoPath(repoPath, ""); err != nil {
		t.Fatalf("expected empty scope to allow repo path, got %v", err)
	}

	if got := sessionFeedSortKey("  Zebra  ", "alpha"); got != "zebra" {
		t.Fatalf("expected title to win sort key, got %q", got)
	}
	if got := sessionFeedSortKey("", "  Alpha  "); got != "alpha" {
		t.Fatalf("expected identifier fallback for sort key, got %q", got)
	}

	items := []agentruntime.PendingInteraction{
		{
			ID:              "interrupt-1",
			IssueID:         "issue-1",
			IssueIdentifier: "ISS-1",
		},
		{
			ID:              "interrupt-duplicate",
			IssueID:         "issue-1",
			IssueIdentifier: "ISS-1",
		},
		{
			ID:              "interrupt-2",
			IssueIdentifier: "ISS-2",
		},
	}
	byIssueID, byIdentifier := indexPendingInterrupts(items)
	if byIssueID["issue-1"].ID != "interrupt-1" || byIdentifier["ISS-1"].ID != "interrupt-1" {
		t.Fatalf("expected first interrupt to win duplicate indexing, got issue=%#v identifier=%#v", byIssueID["issue-1"], byIdentifier["ISS-1"])
	}
	if byIdentifier["ISS-2"].ID != "interrupt-2" {
		t.Fatalf("expected identifier-only interrupt to be indexed, got %#v", byIdentifier["ISS-2"])
	}

	sessionInterrupt := pendingInterruptForSession("issue-1", "ISS-1", byIssueID, byIdentifier)
	if sessionInterrupt == nil || sessionInterrupt.ID != "interrupt-1" {
		t.Fatalf("expected pending interrupt lookup to resolve by issue id, got %#v", sessionInterrupt)
	}
	identifierInterrupt := pendingInterruptForSession("", "ISS-2", nil, byIdentifier)
	if identifierInterrupt == nil || identifierInterrupt.ID != "interrupt-2" {
		t.Fatalf("expected pending interrupt lookup to resolve by identifier, got %#v", identifierInterrupt)
	}
	if pendingInterruptForSession("", "", nil, nil) != nil {
		t.Fatal("expected missing interrupt lookups to return nil")
	}

	if got := firstNonEmpty("   ", "alpha", "beta"); got != "alpha" {
		t.Fatalf("expected first non-empty value, got %q", got)
	}
	if got := maxInt(3, 9); got != 9 {
		t.Fatalf("expected maxInt to return larger value, got %d", got)
	}
	if got := maxInt(9, 3); got != 9 {
		t.Fatalf("expected maxInt to preserve larger first value, got %d", got)
	}
	if !isPlanApprovalPendingError("  PLAN_APPROVAL_PENDING  ") {
		t.Fatal("expected plan approval pending detection to be case-insensitive")
	}
	if isPlanApprovalPendingError("approval_required") {
		t.Fatal("expected non-plan approval error to return false")
	}

	t.Run("decodeJSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"}`))
		var body struct {
			Name string `json:"name"`
		}
		if !decodeJSON(rec, req, &body) {
			t.Fatal("expected valid JSON body to decode")
		}
		if body.Name != "ok" {
			t.Fatalf("unexpected decoded body: %#v", body)
		}
	})

	t.Run("decodeJSON invalid", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{`))
		var body struct {
			Name string `json:"name"`
		}
		if decodeJSON(rec, req, &body) {
			t.Fatal("expected invalid JSON to fail")
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected decodeJSON to write 400, got %d", rec.Code)
		}
	})

	t.Run("decodeOptionalJSON empty", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
		var body struct {
			Name string `json:"name"`
		}
		if !decodeOptionalJSON(rec, req, &body) {
			t.Fatal("expected empty body to be accepted")
		}
	})

	t.Run("decodeOptionalJSON invalid", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{`))
		var body struct {
			Name string `json:"name"`
		}
		if decodeOptionalJSON(rec, req, &body) {
			t.Fatal("expected invalid optional JSON to fail")
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected decodeOptionalJSON to write 400, got %d", rec.Code)
		}
	})
}

func setupDashboardCoverageFixture(t *testing.T) (*inprocessserver.Server, *kanban.Project, *kanban.Epic) {
	t.Helper()

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProject("Platform", "", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Execution", "Track dashboard work")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Ship dashboard", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":     issue.ID,
		"identifier":   issue.Identifier,
		"phase":        "implementation",
		"attempt":      1,
		"thread_id":    "thread-dashboard",
		"total_tokens": 11,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent(run_started): %v", err)
	}
	if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
		"issue_id":     issue.ID,
		"identifier":   issue.Identifier,
		"phase":        "implementation",
		"attempt":      1,
		"thread_id":    "thread-dashboard",
		"total_tokens": 17,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent(run_completed): %v", err)
	}

	provider := testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:     issue.ID,
				Identifier:  issue.Identifier,
				State:       "in_progress",
				Phase:       "implementation",
				Attempt:     1,
				SessionID:   "thread-dashboard-turn-1",
				TurnCount:   2,
				LastEvent:   "turn.started",
				LastMessage: "Working on dashboard",
				StartedAt:   now.Add(-15 * time.Minute),
				Tokens:      observability.TokenTotals{TotalTokens: 11},
			}},
			Retrying: []observability.RetryEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    1,
				DueAt:      now.Add(5 * time.Minute),
				DueInMs:    300000,
				Error:      "approval_required",
				DelayType:  "failure",
			}},
			Paused: []observability.PausedEntry{{
				IssueID:             issue.ID,
				Identifier:          issue.Identifier,
				Phase:               "implementation",
				Attempt:             1,
				PausedAt:            now.Add(-2 * time.Minute),
				Error:               "stall_timeout",
				ConsecutiveFailures: 1,
				PauseThreshold:      3,
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: agentruntime.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-dashboard-turn-1",
				ThreadID:        "thread-dashboard",
				TurnID:          "turn-1",
				LastEvent:       "turn.started",
				LastTimestamp:   now,
				LastMessage:     "Working on dashboard",
				TotalTokens:     11,
				TurnsStarted:    2,
			},
		},
	}

	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
	t.Cleanup(srv.Close)

	return srv, project, epic
}

func TestDashboardScopeAndFeedHelpers(t *testing.T) {
	if got := projectScopeError("/repo", "/repo"); got != "" {
		t.Fatalf("expected matching scopes to be allowed, got %q", got)
	}
	if got := projectScopeError("/repo", "/scope"); got == "" {
		t.Fatal("expected scope mismatch to report an error")
	}

	project := &kanban.Project{
		RepoPath:           "/repo",
		OrchestrationReady: true,
	}
	decorateProject(project, "/repo")
	if !project.DispatchReady || project.DispatchError != "" {
		t.Fatalf("expected matching project scope to remain ready, got %#v", project)
	}
	decorateProject(project, "/scope")
	if project.DispatchReady || project.DispatchError == "" {
		t.Fatalf("expected mismatched project scope to be decorated, got %#v", project)
	}
	decorateProject(nil, "/scope")

	if err := validateScopedRepoPath("\x00", "/scope"); err == nil {
		t.Fatal("expected invalid repo path to fail validation")
	}

	for _, tc := range []struct {
		contentType string
		want        bool
	}{
		{contentType: "image/png", want: true},
		{contentType: "image/png; charset=utf-8", want: true},
		{contentType: "text/plain", want: false},
		{contentType: "text/plain; charset=\"broken", want: false},
	} {
		if got := isInlineRenderableContentType(tc.contentType); got != tc.want {
			t.Fatalf("isInlineRenderableContentType(%q) = %v, want %v", tc.contentType, got, tc.want)
		}
	}

	if got := firstRespondableInterruptID(agentruntime.PendingInteractionSnapshot{
		Items: []agentruntime.PendingInteraction{
			{
				ID:   "alert-1",
				Kind: agentruntime.PendingInteractionKindAlert,
			},
			{
				ID:   " ",
				Kind: agentruntime.PendingInteractionKindApproval,
			},
		},
	}); got != "" {
		t.Fatalf("expected alerts and blank IDs to be skipped, got %q", got)
	}

	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	if status, message, updatedAt, ok := openPlanningFeedState(nil); ok || status != "" || message != "" || updatedAt != nil {
		t.Fatalf("expected nil planning state to be inactive, got %q %q %#v %v", status, message, updatedAt, ok)
	}
	if status, message, updatedAt, ok := openPlanningFeedState(&kanban.IssuePlanning{Status: kanban.IssuePlanningStatusDrafting, UpdatedAt: now}); !ok || status != "active" || message != "Revising the plan." || updatedAt == nil || !updatedAt.Equal(now) {
		t.Fatalf("unexpected drafting planning state: %q %q %#v %v", status, message, updatedAt, ok)
	}
	if status, message, updatedAt, ok := openPlanningFeedState(&kanban.IssuePlanning{Status: kanban.IssuePlanningStatusRevisionRequested, UpdatedAt: now}); !ok || status != "revision_queued" || message != queuedPlanRevisionText || updatedAt == nil || !updatedAt.Equal(now) {
		t.Fatalf("unexpected revision planning state: %q %q %#v %v", status, message, updatedAt, ok)
	}

	requestedAt := now.Add(-time.Hour)
	alertAt := now.Add(-30 * time.Minute)
	planning := &kanban.IssuePlanning{
		SessionID:            "plan-session",
		Status:               kanban.IssuePlanningStatusAwaitingApproval,
		CurrentVersionNumber: 2,
		CurrentVersion: &kanban.IssuePlanVersion{
			Markdown: "Plan markdown",
		},
		UpdatedAt: now,
	}
	blocked := buildLiveSessionFeedEntry(
		"ISS-1",
		agentruntime.Session{
			IssueID:         "issue-1",
			IssueIdentifier: "ISS-1",
			LastTimestamp:   time.Time{},
			LastMessage:     "",
			TotalTokens:     0,
		},
		observability.RunningEntry{
			IssueID:    "issue-1",
			Identifier: "ISS-1",
			Phase:      "implementation",
			Attempt:    3,
			StartedAt:  now.Add(-2 * time.Hour),
			LastEventAt: func() *time.Time {
				t := now.Add(-90 * time.Minute)
				return &t
			}(),
			LastMessage: "running",
			TurnCount:   4,
			Tokens: observability.TokenTotals{
				TotalTokens: 11,
			},
		},
		&kanban.Issue{
			ID:                             "issue-1",
			Identifier:                     "ISS-1",
			Title:                          "Blocked issue",
			PendingPlanRevisionMarkdown:    "draft",
			PendingPlanRevisionRequestedAt: &requestedAt,
		},
		planning,
		"Blocked issue",
		&agentruntime.PendingInteraction{
			ID:              "alert-1",
			Kind:            agentruntime.PendingInteractionKindAlert,
			LastActivity:    "Blocked by policy",
			LastActivityAt:  &alertAt,
			RequestedAt:     now,
			IssueIdentifier: "ISS-1",
		},
	)
	if blocked.Status != "blocked" || blocked.LastMessage != "Blocked by policy" || !blocked.UpdatedAt.Equal(alertAt) || blocked.TotalTokens != 11 {
		t.Fatalf("unexpected blocked live session entry: %#v", blocked)
	}
	if blocked.Planning == nil || blocked.Planning.CurrentVersionNumber != 2 {
		t.Fatalf("expected planning summary to be preserved, got %#v", blocked.Planning)
	}

	revisionQueued := buildLiveSessionFeedEntry(
		"ISS-2",
		agentruntime.Session{
			IssueIdentifier: "ISS-2",
			LastMessage:     "running",
			TotalTokens:     4,
		},
		observability.RunningEntry{
			IssueID:    "issue-2",
			Identifier: "ISS-2",
			StartedAt:  now.Add(-time.Hour),
			TurnCount:  2,
		},
		&kanban.Issue{
			ID:         "issue-2",
			Identifier: "ISS-2",
			Title:      "Revision queued issue",
		},
		&kanban.IssuePlanning{
			Status:                     kanban.IssuePlanningStatusRevisionRequested,
			PendingRevisionNote:        "draft",
			PendingRevisionRequestedAt: &requestedAt,
			UpdatedAt:                  requestedAt,
		},
		"Revision queued issue",
		nil,
	)
	if revisionQueued.Status != "revision_queued" || revisionQueued.LastMessage != queuedPlanRevisionText || !revisionQueued.UpdatedAt.Equal(requestedAt) {
		t.Fatalf("unexpected revision queued entry: %#v", revisionQueued)
	}
	if revisionQueued.TotalTokens != 4 {
		t.Fatalf("expected running tokens to be used, got %#v", revisionQueued)
	}
}
