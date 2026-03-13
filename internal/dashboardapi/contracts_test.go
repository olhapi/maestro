package dashboardapi

import (
	"bytes"
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

type retryTrackingProvider struct {
	testProvider
	retried []string
	runNow  []string
}

func (p *retryTrackingProvider) RetryIssueNow(identifier string) map[string]interface{} {
	p.retried = append(p.retried, identifier)
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func (p *retryTrackingProvider) RunRecurringIssueNow(identifier string) map[string]interface{} {
	p.runNow = append(p.runNow, identifier)
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func requestJSON(t *testing.T, srv *httptest.Server, method, path string, body interface{}) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}
	return resp
}

func decodeResponse(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	defer resp.Body.Close()
	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func TestBootstrapContractsExposePortfolioAndRuntimeOverview(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProject("Maestro", "Main repo", "/repo", "/repo/WORKFLOW.md")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Runtime", "Observability")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Expose dashboard bootstrap", "desc", 2, []string{"api"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":     issue.ID,
		"identifier":   issue.Identifier,
		"title":        issue.Title,
		"phase":        "implementation",
		"total_tokens": 17,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent run_started: %v", err)
	}
	if err := store.AppendRuntimeEvent("retry_scheduled", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"error":      "approval_required",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent retry_scheduled: %v", err)
	}

	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	provider := testProvider{
		snapshot: observability.Snapshot{
			GeneratedAt: now,
			Running: []observability.RunningEntry{{
				IssueID:     issue.ID,
				Identifier:  issue.Identifier,
				State:       "in_progress",
				Phase:       "implementation",
				SessionID:   "thread-1-turn-1",
				TurnCount:   1,
				LastEvent:   "turn.started",
				LastMessage: "working",
				StartedAt:   now.Add(-15 * time.Second),
				Tokens:      observability.TokenTotals{InputTokens: 5, OutputTokens: 7, TotalTokens: 12, SecondsRunning: 15},
			}},
			Retrying: []observability.RetryEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    1,
				DueAt:      now.Add(2 * time.Minute),
				DueInMs:    120000,
				Error:      "approval_required",
				DelayType:  "failure",
			}},
		},
		sessions: map[string]interface{}{
			issue.ID: appserver.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-1-turn-1",
				ThreadID:        "thread-1",
				TurnID:          "turn-1",
				LastEvent:       "turn.started",
				LastTimestamp:   now,
			},
		},
	}

	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/bootstrap", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	payload := decodeResponse(t, resp)

	overview := payload["overview"].(map[string]interface{})
	if overview["project_count"].(float64) != 1 || overview["epic_count"].(float64) != 1 || overview["issue_count"].(float64) != 1 {
		t.Fatalf("unexpected overview counts: %#v", overview)
	}
	if len(payload["projects"].([]interface{})) != 1 {
		t.Fatalf("expected projects payload")
	}
	if len(payload["epics"].([]interface{})) != 1 {
		t.Fatalf("expected epics payload")
	}
	issues := payload["issues"].(map[string]interface{})
	if issues["total"].(float64) != 1 {
		t.Fatalf("unexpected issues payload: %#v", issues)
	}
	if _, ok := payload["sessions"].(map[string]interface{}); !ok {
		t.Fatalf("expected sessions payload: %#v", payload["sessions"])
	}
}

func TestBootstrapContractsMarkProjectsOutOfScopeForScopedServer(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.CreateProject("PhotoPal", "UX work", "/repo/photopal", "/repo/photopal/WORKFLOW.md"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	provider := testProvider{
		status: map[string]interface{}{
			"active_runs":      0,
			"scoped_repo_path": "/repo/maestro",
		},
	}
	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/bootstrap", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	payload := decodeResponse(t, resp)

	projects := payload["projects"].([]interface{})
	if len(projects) != 1 {
		t.Fatalf("expected one project, got %#v", projects)
	}
	project := projects[0].(map[string]interface{})
	if project["dispatch_ready"] != false {
		t.Fatalf("expected dispatch_ready false, got %#v", project["dispatch_ready"])
	}
	if project["dispatch_error"] != "Project repo is outside the current server scope (/repo/maestro)" {
		t.Fatalf("unexpected dispatch_error: %#v", project["dispatch_error"])
	}
}

func TestProjectAndEpicEndpointsSupportCRUDContracts(t *testing.T) {
	provider := testProvider{}
	store, srv := setupDashboardServerTest(t, provider)

	badProject := requestJSON(t, srv, http.MethodPost, "/api/v1/app/projects", map[string]interface{}{
		"name": "Invalid",
	})
	if badProject.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing repo_path, got %d", badProject.StatusCode)
	}
	_ = decodeResponse(t, badProject)

	createProject := requestJSON(t, srv, http.MethodPost, "/api/v1/app/projects", map[string]interface{}{
		"name":          "CLI",
		"description":   "desc",
		"repo_path":     "/repo",
		"workflow_path": "/repo/WORKFLOW.md",
	})
	if createProject.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createProject.StatusCode)
	}
	projectPayload := decodeResponse(t, createProject)
	projectID := projectPayload["id"].(string)

	listProjects := requestJSON(t, srv, http.MethodGet, "/api/v1/app/projects", nil)
	if listProjects.StatusCode != http.StatusOK {
		t.Fatalf("list projects expected 200, got %d", listProjects.StatusCode)
	}
	if len(decodeResponse(t, listProjects)["items"].([]interface{})) != 1 {
		t.Fatal("expected one project in list")
	}

	createEpic := requestJSON(t, srv, http.MethodPost, "/api/v1/app/epics", map[string]interface{}{
		"project_id":  projectID,
		"name":        "Epic One",
		"description": "epic desc",
	})
	if createEpic.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createEpic.StatusCode)
	}
	epicID := decodeResponse(t, createEpic)["id"].(string)

	getProject := requestJSON(t, srv, http.MethodGet, "/api/v1/app/projects/"+projectID, nil)
	if getProject.StatusCode != http.StatusOK {
		t.Fatalf("get project expected 200, got %d", getProject.StatusCode)
	}
	projectDetail := decodeResponse(t, getProject)
	if projectDetail["project"].(map[string]interface{})["id"].(string) != projectID {
		t.Fatalf("unexpected project detail: %#v", projectDetail)
	}

	getEpic := requestJSON(t, srv, http.MethodGet, "/api/v1/app/epics/"+epicID, nil)
	if getEpic.StatusCode != http.StatusOK {
		t.Fatalf("get epic expected 200, got %d", getEpic.StatusCode)
	}
	epicDetail := decodeResponse(t, getEpic)
	if epicDetail["epic"].(map[string]interface{})["id"].(string) != epicID {
		t.Fatalf("unexpected epic detail: %#v", epicDetail)
	}

	updateProject := requestJSON(t, srv, http.MethodPatch, "/api/v1/app/projects/"+projectID, map[string]interface{}{
		"name":          "CLI Updated",
		"description":   "updated",
		"repo_path":     "/repo-updated",
		"workflow_path": "/repo-updated/WORKFLOW.md",
	})
	if updateProject.StatusCode != http.StatusOK {
		t.Fatalf("update project expected 200, got %d", updateProject.StatusCode)
	}
	if decodeResponse(t, updateProject)["name"].(string) != "CLI Updated" {
		t.Fatal("expected updated project name")
	}

	updateEpic := requestJSON(t, srv, http.MethodPatch, "/api/v1/app/epics/"+epicID, map[string]interface{}{
		"project_id":  projectID,
		"name":        "Epic Updated",
		"description": "updated",
	})
	if updateEpic.StatusCode != http.StatusOK {
		t.Fatalf("update epic expected 200, got %d", updateEpic.StatusCode)
	}
	if decodeResponse(t, updateEpic)["name"].(string) != "Epic Updated" {
		t.Fatal("expected updated epic name")
	}

	deleteEpic := requestJSON(t, srv, http.MethodDelete, "/api/v1/app/epics/"+epicID, nil)
	if deleteEpic.StatusCode != http.StatusOK {
		t.Fatalf("delete epic expected 200, got %d", deleteEpic.StatusCode)
	}
	_ = decodeResponse(t, deleteEpic)

	deleteProject := requestJSON(t, srv, http.MethodDelete, "/api/v1/app/projects/"+projectID, nil)
	if deleteProject.StatusCode != http.StatusOK {
		t.Fatalf("delete project expected 200, got %d", deleteProject.StatusCode)
	}
	_ = decodeResponse(t, deleteProject)

	if _, err := store.GetProject(projectID); err == nil {
		t.Fatal("expected deleted project to be missing")
	}
}

func TestProjectEndpointsRejectOutOfScopeRepoPaths(t *testing.T) {
	provider := testProvider{
		status: map[string]interface{}{
			"active_runs":      0,
			"scoped_repo_path": "/repo/maestro",
		},
	}
	store, srv := setupDashboardServerTest(t, provider)

	createProject := requestJSON(t, srv, http.MethodPost, "/api/v1/app/projects", map[string]interface{}{
		"name":          "PhotoPal",
		"description":   "desc",
		"repo_path":     "/repo/photopal",
		"workflow_path": "/repo/photopal/WORKFLOW.md",
	})
	if createProject.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", createProject.StatusCode)
	}
	if !strings.Contains(decodeResponse(t, createProject)["error"].(string), "repo_path must match the current server scope") {
		t.Fatalf("unexpected create error payload")
	}

	project, err := store.CreateProject("CLI", "desc", "/repo/maestro", "/repo/maestro/WORKFLOW.md")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	updateProject := requestJSON(t, srv, http.MethodPatch, "/api/v1/app/projects/"+project.ID, map[string]interface{}{
		"name":          "CLI Updated",
		"description":   "updated",
		"repo_path":     "/repo/photopal",
		"workflow_path": "/repo/photopal/WORKFLOW.md",
	})
	if updateProject.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", updateProject.StatusCode)
	}
	if !strings.Contains(decodeResponse(t, updateProject)["error"].(string), "repo_path must match the current server scope") {
		t.Fatalf("unexpected update error payload")
	}
}

func TestIssueRuntimeAndSessionEndpointsExposeContracts(t *testing.T) {
	provider := &retryTrackingProvider{
		testProvider: testProvider{
			sessions: map[string]interface{}{
				"ISS-1": appserver.Session{
					IssueID:         "issue-1",
					IssueIdentifier: "ISS-1",
					SessionID:       "thread-1-turn-1",
					ThreadID:        "thread-1",
					TurnID:          "turn-1",
					LastEvent:       "turn.started",
					LastTimestamp:   time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
				},
			},
		},
	}
	store, srv := setupDashboardServerTest(t, provider)

	project, err := store.CreateProject("Project", "", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}

	createIssue := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues", map[string]interface{}{
		"project_id":  project.ID,
		"epic_id":     epic.ID,
		"title":       "Track issue",
		"description": "desc",
		"priority":    3,
		"labels":      []string{"api", "runtime"},
		"state":       "ready",
		"blocked_by":  []string{},
		"branch_name": "feature/track",
		"pr_number":   12,
		"pr_url":      "https://example.com/pr/12",
	})
	if createIssue.StatusCode != http.StatusCreated {
		t.Fatalf("create issue expected 201, got %d", createIssue.StatusCode)
	}
	created := decodeResponse(t, createIssue)
	identifier := created["identifier"].(string)
	issueID := created["id"].(string)

	patchIssue := requestJSON(t, srv, http.MethodPatch, "/api/v1/app/issues/"+identifier, map[string]interface{}{
		"project_id":  project.ID,
		"epic_id":     epic.ID,
		"title":       "Track issue updated",
		"description": "updated",
		"priority":    5,
		"labels":      []string{"updated"},
		"blocked_by":  []string{},
		"branch_name": "feature/updated",
		"pr_number":   99,
		"pr_url":      "https://example.com/pr/99",
	})
	if patchIssue.StatusCode != http.StatusOK {
		t.Fatalf("patch issue expected 200, got %d", patchIssue.StatusCode)
	}
	if decodeResponse(t, patchIssue)["title"].(string) != "Track issue updated" {
		t.Fatal("expected patched issue title")
	}

	for _, req := range []struct {
		path string
		body interface{}
	}{
		{path: "/api/v1/app/issues/" + identifier + "/state", body: map[string]interface{}{"state": "in_progress"}},
		{path: "/api/v1/app/issues/" + identifier + "/blockers", body: map[string]interface{}{"blocked_by": []string{}}},
	} {
		resp := requestJSON(t, srv, http.MethodPost, req.path, req.body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s expected 200, got %d", req.path, resp.StatusCode)
		}
		_ = decodeResponse(t, resp)
	}

	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issueID,
		Identifier: identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_completed",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issueID,
			IssueIdentifier: identifier,
			SessionID:       "thread-1-turn-1",
			ThreadID:        "thread-1",
			TurnID:          "turn-1",
			LastEvent:       "turn.completed",
			LastTimestamp:   now,
			Terminal:        true,
			TerminalReason:  "turn.completed",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	for _, kind := range []string{"run_started", "run_completed", "retry_scheduled"} {
		if err := store.AppendRuntimeEvent(kind, map[string]interface{}{
			"issue_id":     issueID,
			"identifier":   identifier,
			"phase":        "implementation",
			"total_tokens": 9,
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent %s: %v", kind, err)
		}
	}

	listIssues := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues?project_id="+project.ID+"&limit=20", nil)
	if listIssues.StatusCode != http.StatusOK {
		t.Fatalf("list issues expected 200, got %d", listIssues.StatusCode)
	}
	issuesPayload := decodeResponse(t, listIssues)
	if issuesPayload["total"].(float64) != 1 {
		t.Fatalf("unexpected issues payload: %#v", issuesPayload)
	}

	getIssue := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+identifier, nil)
	if getIssue.StatusCode != http.StatusOK {
		t.Fatalf("get issue expected 200, got %d", getIssue.StatusCode)
	}
	if decodeResponse(t, getIssue)["identifier"].(string) != identifier {
		t.Fatal("expected issue detail payload")
	}

	getExecution := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+identifier+"/execution", nil)
	if getExecution.StatusCode != http.StatusOK {
		t.Fatalf("issue execution expected 200, got %d", getExecution.StatusCode)
	}
	if decodeResponse(t, getExecution)["session_source"].(string) == "none" {
		t.Fatal("expected execution session payload")
	}

	getEvents := requestJSON(t, srv, http.MethodGet, "/api/v1/app/runtime/events?limit=10", nil)
	if getEvents.StatusCode != http.StatusOK {
		t.Fatalf("runtime events expected 200, got %d", getEvents.StatusCode)
	}
	if len(decodeResponse(t, getEvents)["events"].([]interface{})) == 0 {
		t.Fatal("expected runtime events payload")
	}

	getSeries := requestJSON(t, srv, http.MethodGet, "/api/v1/app/runtime/series?hours=12", nil)
	if getSeries.StatusCode != http.StatusOK {
		t.Fatalf("runtime series expected 200, got %d", getSeries.StatusCode)
	}
	if len(decodeResponse(t, getSeries)["series"].([]interface{})) != 12 {
		t.Fatal("expected 12 runtime series buckets")
	}

	getSessions := requestJSON(t, srv, http.MethodGet, "/api/v1/app/sessions", nil)
	if getSessions.StatusCode != http.StatusOK {
		t.Fatalf("sessions expected 200, got %d", getSessions.StatusCode)
	}
	sessionsPayload := decodeResponse(t, getSessions)
	if _, ok := sessionsPayload["sessions"]; !ok {
		t.Fatal("expected sessions payload")
	}
	if _, ok := sessionsPayload["entries"]; !ok {
		t.Fatal("expected session feed entries payload")
	}

	retryIssue := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+identifier+"/retry", nil)
	if retryIssue.StatusCode != http.StatusOK {
		t.Fatalf("retry issue expected 200, got %d", retryIssue.StatusCode)
	}
	if len(provider.retried) != 1 || provider.retried[0] != identifier {
		t.Fatalf("expected retry callback for %s, got %v", identifier, provider.retried)
	}

	deleteIssue := requestJSON(t, srv, http.MethodDelete, "/api/v1/app/issues/"+identifier, nil)
	if deleteIssue.StatusCode != http.StatusOK {
		t.Fatalf("delete issue expected 200, got %d", deleteIssue.StatusCode)
	}
	_ = decodeResponse(t, deleteIssue)
}

func TestRecurringIssueContractsExposeRecurringFieldsAndRunNow(t *testing.T) {
	provider := &retryTrackingProvider{}
	store, srv := setupDashboardServerTest(t, provider)

	project, err := store.CreateProject("Automation", "", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	createIssue := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues", map[string]interface{}{
		"project_id":  project.ID,
		"title":       "Scan GitHub ready-to-work",
		"description": "Mirror ready-to-work GitHub issues into Maestro.",
		"issue_type":  "recurring",
		"cron":        "*/15 * * * *",
		"enabled":     false,
	})
	if createIssue.StatusCode != http.StatusCreated {
		t.Fatalf("create recurring issue expected 201, got %d", createIssue.StatusCode)
	}
	created := decodeResponse(t, createIssue)
	identifier := created["identifier"].(string)
	if created["issue_type"] != "recurring" || created["cron"] != "*/15 * * * *" || created["enabled"] != false {
		t.Fatalf("unexpected recurring create payload: %#v", created)
	}

	patchIssue := requestJSON(t, srv, http.MethodPatch, "/api/v1/app/issues/"+identifier, map[string]interface{}{
		"project_id": project.ID,
		"cron":    "0 * * * *",
		"enabled": true,
	})
	if patchIssue.StatusCode != http.StatusOK {
		t.Fatalf("patch recurring issue expected 200, got %d", patchIssue.StatusCode)
	}
	patched := decodeResponse(t, patchIssue)
	if patched["cron"] != "0 * * * *" || patched["enabled"] != true {
		t.Fatalf("unexpected recurring patch payload: %#v", patched)
	}

	listIssues := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues?issue_type=recurring&limit=20", nil)
	if listIssues.StatusCode != http.StatusOK {
		t.Fatalf("list recurring issues expected 200, got %d", listIssues.StatusCode)
	}
	items := decodeResponse(t, listIssues)["items"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["identifier"] != identifier {
		t.Fatalf("unexpected recurring issue list payload: %#v", items)
	}

	getIssue := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+identifier, nil)
	if getIssue.StatusCode != http.StatusOK {
		t.Fatalf("get recurring issue expected 200, got %d", getIssue.StatusCode)
	}
	got := decodeResponse(t, getIssue)
	if got["issue_type"] != "recurring" || got["cron"] != "0 * * * *" || got["enabled"] != true {
		t.Fatalf("unexpected recurring issue payload: %#v", got)
	}

	runNow := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+identifier+"/run-now", nil)
	if runNow.StatusCode != http.StatusOK {
		t.Fatalf("run-now expected 200, got %d", runNow.StatusCode)
	}
	if len(provider.runNow) != 1 || provider.runNow[0] != identifier {
		t.Fatalf("expected run-now callback for %s, got %v", identifier, provider.runNow)
	}
}

func TestCreateIssueAndEpicRequireProject(t *testing.T) {
	_, srv := setupDashboardServerTest(t, testProvider{})

	createIssue := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues", map[string]interface{}{
		"title": "Missing project",
	})
	if createIssue.StatusCode != http.StatusBadRequest {
		t.Fatalf("create issue without project expected 400, got %d", createIssue.StatusCode)
	}

	createEpic := requestJSON(t, srv, http.MethodPost, "/api/v1/app/epics", map[string]interface{}{
		"name": "Missing project",
	})
	if createEpic.StatusCode != http.StatusBadRequest {
		t.Fatalf("create epic without project expected 400, got %d", createEpic.StatusCode)
	}
}

func TestIssueStateEndpointRejectsBlockedInProgress(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})

	blocker, err := store.CreateIssue("", "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}
	blocked, err := store.CreateIssue("", "", "Blocked", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+blocked.Identifier+"/state", map[string]interface{}{"state": "in_progress"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	if !strings.Contains(body["error"].(string), "cannot move issue to in_progress: blocked by "+blocker.Identifier) {
		t.Fatalf("unexpected error payload: %#v", body)
	}

	reloaded, err := store.GetIssue(blocked.ID)
	if err != nil {
		t.Fatalf("GetIssue blocked: %v", err)
	}
	if reloaded.State != kanban.StateBacklog {
		t.Fatalf("expected blocked issue to stay in backlog, got %s", reloaded.State)
	}
}

func TestCreateIssueRejectsBlockedInProgressWithConflict(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})
	project, err := store.CreateProject("Project", "", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	blocker, err := store.CreateIssue(project.ID, "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, kanban.StateInReview); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues", map[string]interface{}{
		"project_id": project.ID,
		"title":      "Blocked create",
		"state":      "in_progress",
		"blocked_by": []string{blocker.Identifier},
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	if !strings.Contains(body["error"].(string), "cannot move issue to in_progress: blocked by "+blocker.Identifier) {
		t.Fatalf("unexpected error payload: %#v", body)
	}
}

func TestDashboardAPIReturnsMethodAndPathErrors(t *testing.T) {
	_, srv := setupDashboardServerTest(t, testProvider{})

	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodPost, path: "/api/v1/app/bootstrap", status: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: "/api/v1/app/runtime/events", status: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: "/api/v1/app/runtime/series", status: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: "/api/v1/app/projects/missing/nested", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/api/v1/app/issues/missing/execution/extra", status: http.StatusMethodNotAllowed},
	} {
		resp := requestJSON(t, srv, tc.method, tc.path, nil)
		if resp.StatusCode != tc.status {
			t.Fatalf("%s %s: expected %d, got %d", tc.method, tc.path, tc.status, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
