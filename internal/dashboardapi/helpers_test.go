package dashboardapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

func TestDashboardAPIHelpers(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := NewServer(store, testProvider{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if !srv.upgrader.CheckOrigin(req) {
		t.Fatal("expected websocket upgrader to allow origins")
	}

	req = httptest.NewRequest(http.MethodGet, "/?hours=12&bad=nope", nil)
	if got := queryInt(req, "hours", 24); got != 12 {
		t.Fatalf("unexpected query int value: %d", got)
	}
	if got := queryInt(req, "bad", 7); got != 7 {
		t.Fatalf("expected fallback query int value, got %d", got)
	}

	rec := httptest.NewRecorder()
	writeError(rec, http.StatusTeapot, errors.New("boom"))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("unexpected writeError status: %d", rec.Code)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode writeError payload: %v", err)
	}
	if payload["error"] != "boom" {
		t.Fatalf("unexpected writeError payload: %#v", payload)
	}
}

func TestDashboardAPIHandlerDecodeAndNotFoundPaths(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProject("Project", "", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Issue", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	server := NewServer(store, testProvider{})
	badJSON := bytes.NewBufferString("{")
	for _, tc := range []struct {
		name   string
		method string
		target string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{"projects post", http.MethodPost, "/api/v1/app/projects", server.handleProjects},
		{"project patch", http.MethodPatch, "/api/v1/app/projects/" + project.ID, server.handleProject},
		{"epics post", http.MethodPost, "/api/v1/app/epics", server.handleEpics},
		{"epic patch", http.MethodPatch, "/api/v1/app/epics/" + epic.ID, server.handleEpic},
		{"issues post", http.MethodPost, "/api/v1/app/issues", server.handleIssues},
		{"issue patch", http.MethodPatch, "/api/v1/app/issues/" + issue.Identifier, server.handleIssue},
		{"issue state", http.MethodPost, "/api/v1/app/issues/" + issue.Identifier + "/state", server.handleIssue},
		{"issue blockers", http.MethodPost, "/api/v1/app/issues/" + issue.Identifier + "/blockers", server.handleIssue},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.target, bytes.NewReader(badJSON.Bytes()))
		req.Header.Set("Content-Type", "application/json")
		tc.call(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d body=%q", tc.name, rec.Code, rec.Body.String())
		}
	}

	for _, tc := range []struct {
		name   string
		method string
		target string
		call   func(http.ResponseWriter, *http.Request)
		status int
		want   string
	}{
		{"missing project", http.MethodGet, "/api/v1/app/projects/missing", server.handleProject, http.StatusNotFound, "sql: no rows"},
		{"missing epic", http.MethodGet, "/api/v1/app/epics/missing", server.handleEpic, http.StatusNotFound, "sql: no rows"},
		{"missing issue", http.MethodGet, "/api/v1/app/issues/ISS-404", server.handleIssue, http.StatusNotFound, "sql: no rows"},
		{"missing execution", http.MethodGet, "/api/v1/app/issues/ISS-404/execution", server.handleIssue, http.StatusNotFound, "sql: no rows"},
		{"missing state issue", http.MethodPost, "/api/v1/app/issues/ISS-404/state", server.handleIssue, http.StatusNotFound, "sql: no rows"},
		{"missing blockers issue", http.MethodPost, "/api/v1/app/issues/ISS-404/blockers", server.handleIssue, http.StatusNotFound, "sql: no rows"},
		{"unknown issue action", http.MethodPost, "/api/v1/app/issues/" + issue.Identifier + "/unknown", server.handleIssue, http.StatusNotFound, "404 page not found"},
		{"project nested path", http.MethodGet, "/api/v1/app/projects/" + project.ID + "/nested", server.handleProject, http.StatusNotFound, "404 page not found"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		tc.call(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s: expected %d, got %d body=%q", tc.name, tc.status, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s: expected %q in body %q", tc.name, tc.want, rec.Body.String())
		}
	}
}

func TestDashboardAPIWebSocketInvalidatesOnStoreChanges(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mux := http.NewServeMux()
	NewServer(store, testProvider{}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	var connected map[string]interface{}
	if err := conn.ReadJSON(&connected); err != nil {
		t.Fatalf("read connected frame: %v", err)
	}
	if connected["type"] != "connected" {
		t.Fatalf("unexpected connected payload: %#v", connected)
	}

	if _, err := store.CreateProject("Realtime", "", "/repo", ""); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var invalidate map[string]interface{}
	if err := conn.ReadJSON(&invalidate); err != nil {
		t.Fatalf("read invalidate frame: %v", err)
	}
	if invalidate["type"] != "invalidate" {
		t.Fatalf("unexpected invalidate payload: %#v", invalidate)
	}
	if invalidate["seq"].(float64) <= connected["seq"].(float64) {
		t.Fatalf("expected invalidate seq to advance: connected=%#v invalidate=%#v", connected, invalidate)
	}
}

func TestIssueExecutionHelperSelectionPaths(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Selection paths", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    3,
		RunKind:    "run_failed",
		Error:      "turn_input_required",
		UpdatedAt:  now,
		AppSession: appserver.Session{IssueID: issue.ID, IssueIdentifier: issue.Identifier, SessionID: "persisted"},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_failed", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
		"error":      "approval_required",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	provider := testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				State:      "in_progress",
				Phase:      "review",
				Attempt:    4,
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: map[string]interface{}{
				"issue_id":         issue.ID,
				"issue_identifier": issue.Identifier,
				"session_id":       "live",
				"thread_id":        "thread-live",
				"turn_id":          "turn-live",
			},
		},
	}

	server := NewServer(store, provider)
	payload, err := server.issueExecutionPayload(issue)
	if err != nil {
		t.Fatalf("issueExecutionPayload: %v", err)
	}
	if payload["active"] != true || payload["attempt_number"].(int) != 4 {
		t.Fatalf("unexpected execution payload: %#v", payload)
	}
	if payload["session_source"] != "live" || payload["retry_state"] != "active" {
		t.Fatalf("unexpected execution source state: %#v", payload)
	}

	provider.snapshot.Running = nil
	provider.snapshot.Retrying = []observability.RetryEntry{{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    5,
		DueAt:      now.Add(time.Minute),
		Error:      "approval_required",
	}}
	provider.sessions = nil
	server = NewServer(store, provider)
	payload, err = server.issueExecutionPayload(issue)
	if err != nil {
		t.Fatalf("issueExecutionPayload retry: %v", err)
	}
	if payload["retry_state"] != "scheduled" || payload["session_source"] != "persisted" {
		t.Fatalf("unexpected persisted execution payload: %#v", payload)
	}

	fallbackIssue, err := store.CreateIssue("", "", "Fallback issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue fallback: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_failed", map[string]interface{}{
		"issue_id":   fallbackIssue.ID,
		"identifier": fallbackIssue.Identifier,
		"phase":      "implementation",
		"attempt":    6,
		"error":      "approval_required",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent fallback: %v", err)
	}
	server = NewServer(store, testProvider{})
	payload, err = server.issueExecutionPayload(fallbackIssue)
	if err != nil {
		t.Fatalf("issueExecutionPayload fallback: %v", err)
	}
	if payload["attempt_number"].(int) != 6 || payload["current_error"] != "approval_required" {
		t.Fatalf("unexpected fallback execution payload: %#v", payload)
	}
}

func TestDashboardAPIAdditionalSuccessPaths(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProject("Project", "", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	provider := testProvider{
		sessions: map[string]interface{}{
			"sessions": map[string]interface{}{},
		},
	}
	server := NewServer(store, provider)

	epicBody := bytes.NewBufferString(`{"project_id":"` + project.ID + `","name":"Epic","description":"desc"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/app/epics", epicBody)
	req.Header.Set("Content-Type", "application/json")
	server.handleEpics(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create epic: expected 201, got %d body=%q", rec.Code, rec.Body.String())
	}
	var createdEpic map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &createdEpic); err != nil {
		t.Fatalf("decode epic: %v", err)
	}
	epicID := createdEpic["id"].(string)

	issueBody := bytes.NewBufferString(`{"project_id":"` + project.ID + `","epic_id":"` + epicID + `","title":"Issue","description":"desc","priority":2,"labels":["api"],"state":"backlog","blocked_by":[],"branch_name":"feature/api","pr_number":3,"pr_url":"https://example.com/pr/3"}`)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/app/issues", issueBody)
	req.Header.Set("Content-Type", "application/json")
	server.handleIssues(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: expected 201, got %d body=%q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/app/epics?project_id="+project.ID, nil)
	server.handleEpics(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"items"`) {
		t.Fatalf("list epics failed: %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/app/projects/"+project.ID, nil)
	server.handleProject(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"project"`) || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("project detail failed: %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/app/issues?project_id="+project.ID+"&epic_id="+epicID+"&search=Issue&sort=updated_desc&limit=10&offset=0", nil)
	server.handleIssues(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("list issues failed: %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/app/bootstrap", nil)
	server.handleBootstrap(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"project_count":1`) {
		t.Fatalf("bootstrap failed: %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/app/sessions", nil)
	server.handleSessions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions get: expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/app/sessions", nil)
	server.handleSessions(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("sessions post: expected 405, got %d", rec.Code)
	}

	standaloneEpic, err := store.CreateEpic("", "Standalone", "")
	if err != nil {
		t.Fatalf("CreateEpic standalone: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/app/epics/"+standaloneEpic.ID, nil)
	server.handleEpic(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"project":null`) {
		t.Fatalf("standalone epic detail failed: %d %q", rec.Code, rec.Body.String())
	}
}

func TestDashboardAPIMethodAndValidationBranches(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProject("Project", "", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	server := NewServer(store, testProvider{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/app/projects/"+project.ID, strings.NewReader(`{"name":"still bad"}`))
	req.Header.Set("Content-Type", "application/json")
	server.handleProject(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "repo_path is required") {
		t.Fatalf("expected project patch validation error, got %d %q", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		name   string
		method string
		target string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{"projects", http.MethodPut, "/api/v1/app/projects", server.handleProjects},
		{"project", http.MethodPost, "/api/v1/app/projects/" + project.ID, server.handleProject},
		{"epics", http.MethodPut, "/api/v1/app/epics", server.handleEpics},
		{"epic", http.MethodPost, "/api/v1/app/epics/" + epic.ID, server.handleEpic},
		{"issues", http.MethodPut, "/api/v1/app/issues", server.handleIssues},
		{"issue execution method", http.MethodPost, "/api/v1/app/issues/" + issue.Identifier + "/execution", server.handleIssue},
	} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(tc.method, tc.target, nil)
		tc.call(rec, req)
		expected := http.StatusMethodNotAllowed
		if tc.name == "issue execution method" {
			expected = http.StatusNotFound
		}
		if rec.Code != expected {
			t.Fatalf("%s: expected %d, got %d body=%q", tc.name, expected, rec.Code, rec.Body.String())
		}
	}
}

func TestDashboardAPIClosedStoreErrorBranches(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	project, err := store.CreateProject("Project", "", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	server := NewServer(store, testProvider{})
	for _, tc := range []struct {
		name   string
		target string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{"bootstrap", "/api/v1/app/bootstrap", server.handleBootstrap},
		{"projects", "/api/v1/app/projects", server.handleProjects},
		{"project", "/api/v1/app/projects/" + project.ID, server.handleProject},
		{"epics", "/api/v1/app/epics", server.handleEpics},
		{"epic", "/api/v1/app/epics/" + epic.ID, server.handleEpic},
		{"issues", "/api/v1/app/issues", server.handleIssues},
		{"issue", "/api/v1/app/issues/" + issue.Identifier, server.handleIssue},
		{"runtime events", "/api/v1/app/runtime/events", server.handleRuntimeEvents},
		{"runtime series", "/api/v1/app/runtime/series", server.handleRuntimeSeries},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		tc.call(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: expected 500, got %d body=%q", tc.name, rec.Code, rec.Body.String())
		}
	}
}
