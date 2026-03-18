package dashboardapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
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

type webhookTrackingProvider struct {
	testProvider
	retried          []string
	runNow           []string
	projectRefreshes []string
	projectStops     []string
}

func (p *webhookTrackingProvider) RetryIssueNow(identifier string) map[string]interface{} {
	p.retried = append(p.retried, identifier)
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func (p *webhookTrackingProvider) RunRecurringIssueNow(identifier string) map[string]interface{} {
	p.runNow = append(p.runNow, identifier)
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func (p *webhookTrackingProvider) RequestProjectRefresh(projectID string) map[string]interface{} {
	p.projectRefreshes = append(p.projectRefreshes, projectID)
	return map[string]interface{}{"status": "accepted", "project_id": projectID, "state": "running"}
}

func (p *webhookTrackingProvider) StopProjectRuns(projectID string) map[string]interface{} {
	p.projectStops = append(p.projectStops, projectID)
	return map[string]interface{}{"status": "stopped", "project_id": projectID, "state": "stopped", "stopped_runs": 0}
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

func requestWebhookJSON(t *testing.T, srv *httptest.Server, token string, body interface{}) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal webhook body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks", reader)
	if err != nil {
		t.Fatalf("new webhook request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do webhook request: %v", err)
	}
	return resp
}

func requestMultipart(t *testing.T, srv *httptest.Server, method, path, fieldName, filename string, content []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(method, srv.URL+path, &body)
	if err != nil {
		t.Fatalf("new multipart request %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do multipart request %s %s: %v", method, path, err)
	}
	return resp
}

func contractSamplePNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
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

func TestIssueImageUploadRejectsOversizedMultipartBodies(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Oversized image", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	mux := http.NewServeMux()
	NewServer(store, testProvider{}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	oversized := append(contractSamplePNGBytes(), bytes.Repeat([]byte{0}, int(kanban.MaxIssueImageBytes))...)
	resp := requestMultipart(t, srv, http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/images", "file", "too-large.png", oversized)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized upload, got %d", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	if !strings.Contains(body["error"].(string), "too large") && !strings.Contains(body["error"].(string), "exceeds") {
		t.Fatalf("expected size validation error, got %#v", body)
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
	if projectDetail["project"].(map[string]interface{})["permission_profile"].(string) != "default" {
		t.Fatalf("expected default permission profile, got %#v", projectDetail["project"])
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

	updatePermissions := requestJSON(t, srv, http.MethodPost, "/api/v1/app/projects/"+projectID+"/permissions", map[string]interface{}{
		"permission_profile": "full-access",
	})
	if updatePermissions.StatusCode != http.StatusOK {
		t.Fatalf("update permissions expected 200, got %d", updatePermissions.StatusCode)
	}
	if decodeResponse(t, updatePermissions)["permission_profile"].(string) != "full-access" {
		t.Fatal("expected updated permission profile")
	}

	invalidPermissions := requestJSON(t, srv, http.MethodPost, "/api/v1/app/projects/"+projectID+"/permissions", map[string]interface{}{
		"permission_profile": "admin-mode",
	})
	if invalidPermissions.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid permission profile expected 400, got %d", invalidPermissions.StatusCode)
	}
	if !strings.Contains(decodeResponse(t, invalidPermissions)["error"].(string), "invalid permission profile") {
		t.Fatal("expected validation error for invalid permission profile")
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

func TestDeleteProjectEndpointRemovesProjectsWithIssueActivityHistory(t *testing.T) {
	provider := testProvider{}
	store, srv := setupDashboardServerTest(t, provider)

	project, err := store.CreateProject("Runtime", "", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Tracked issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, appserver.ActivityEvent{
		Type:      "item.completed",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "msg-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item: map[string]interface{}{
			"id":    "msg-1",
			"type":  "agentMessage",
			"phase": "commentary",
			"text":  "Finished summary",
		},
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent: %v", err)
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries before delete: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one persisted activity entry, got %#v", entries)
	}

	deleteProject := requestJSON(t, srv, http.MethodDelete, "/api/v1/app/projects/"+project.ID, nil)
	if deleteProject.StatusCode != http.StatusOK {
		t.Fatalf("delete project expected 200, got %d", deleteProject.StatusCode)
	}
	_ = decodeResponse(t, deleteProject)

	if _, err := store.GetProject(project.ID); err == nil {
		t.Fatal("expected deleted project to be missing")
	}
	entries, err = store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries after delete: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected activity history to be removed, got %#v", entries)
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
		"project_id":   project.ID,
		"epic_id":      epic.ID,
		"title":        "Track issue",
		"description":  "desc",
		"priority":     3,
		"labels":       []string{"api", "runtime"},
		"agent_name":   "marketing",
		"agent_prompt": "Review messaging before implementation.",
		"state":        "ready",
		"blocked_by":   []string{},
		"branch_name":  "feature/track",
		"pr_url":       "https://example.com/pr/12",
	})
	if createIssue.StatusCode != http.StatusCreated {
		t.Fatalf("create issue expected 201, got %d", createIssue.StatusCode)
	}
	created := decodeResponse(t, createIssue)
	identifier := created["identifier"].(string)
	issueID := created["id"].(string)

	patchIssue := requestJSON(t, srv, http.MethodPatch, "/api/v1/app/issues/"+identifier, map[string]interface{}{
		"project_id":   project.ID,
		"epic_id":      epic.ID,
		"title":        "Track issue updated",
		"description":  "updated",
		"priority":     5,
		"labels":       []string{"updated"},
		"agent_name":   "designer",
		"agent_prompt": "Focus on tone and visual hierarchy.",
		"blocked_by":   []string{},
		"branch_name":  "feature/updated",
		"pr_url":       "https://example.com/pr/99",
	})
	if patchIssue.StatusCode != http.StatusOK {
		t.Fatalf("patch issue expected 200, got %d", patchIssue.StatusCode)
	}
	patched := decodeResponse(t, patchIssue)
	if patched["title"].(string) != "Track issue updated" {
		t.Fatal("expected patched issue title")
	}
	if patched["agent_name"].(string) != "designer" || patched["agent_prompt"].(string) != "Focus on tone and visual hierarchy." {
		t.Fatalf("expected patched agent metadata, got %#v", patched)
	}

	patchWithoutAgentFields := requestJSON(t, srv, http.MethodPatch, "/api/v1/app/issues/"+identifier, map[string]interface{}{
		"project_id":  project.ID,
		"epic_id":     epic.ID,
		"title":       "Track issue retitled",
		"description": "retitled without touching agent metadata",
		"priority":    2,
		"labels":      []string{"retitled"},
		"blocked_by":  []string{},
		"branch_name": "feature/retitled",
		"pr_url":      "https://example.com/pr/100",
	})
	if patchWithoutAgentFields.StatusCode != http.StatusOK {
		t.Fatalf("patch without agent metadata expected 200, got %d", patchWithoutAgentFields.StatusCode)
	}
	patchedWithoutAgentFields := decodeResponse(t, patchWithoutAgentFields)
	if patchedWithoutAgentFields["agent_name"].(string) != "designer" || patchedWithoutAgentFields["agent_prompt"].(string) != "Focus on tone and visual hierarchy." {
		t.Fatalf("expected patch without agent fields to preserve metadata, got %#v", patchedWithoutAgentFields)
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
	issuePayload := decodeResponse(t, getIssue)
	if issuePayload["identifier"].(string) != identifier {
		t.Fatal("expected issue detail payload")
	}
	if issuePayload["permission_profile"].(string) != "default" {
		t.Fatalf("expected default issue permission profile, got %#v", issuePayload)
	}
	if issuePayload["project_permission_profile"].(string) != "default" {
		t.Fatalf("expected default project permission profile in issue detail, got %#v", issuePayload)
	}

	updateIssuePermissions := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+identifier+"/permissions", map[string]interface{}{
		"permission_profile": "full-access",
	})
	if updateIssuePermissions.StatusCode != http.StatusOK {
		t.Fatalf("update issue permissions expected 200, got %d", updateIssuePermissions.StatusCode)
	}
	if decodeResponse(t, updateIssuePermissions)["permission_profile"].(string) != "full-access" {
		t.Fatal("expected updated issue permission profile")
	}

	invalidIssuePermissions := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+identifier+"/permissions", map[string]interface{}{
		"permission_profile": "admin-mode",
	})
	if invalidIssuePermissions.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid issue permission profile expected 400, got %d", invalidIssuePermissions.StatusCode)
	}
	if !strings.Contains(decodeResponse(t, invalidIssuePermissions)["error"].(string), "invalid permission profile") {
		t.Fatal("expected validation error for invalid issue permission profile")
	}

	uploadImage := requestMultipart(t, srv, http.MethodPost, "/api/v1/app/issues/"+identifier+"/images", "file", "runtime.png", contractSamplePNGBytes())
	if uploadImage.StatusCode != http.StatusCreated {
		t.Fatalf("upload image expected 201, got %d", uploadImage.StatusCode)
	}
	imagePayload := decodeResponse(t, uploadImage)
	imageID := imagePayload["id"].(string)
	if imagePayload["content_type"].(string) != "image/png" {
		t.Fatalf("unexpected uploaded image payload: %#v", imagePayload)
	}

	getIssueWithImage := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+identifier, nil)
	if getIssueWithImage.StatusCode != http.StatusOK {
		t.Fatalf("get issue with image expected 200, got %d", getIssueWithImage.StatusCode)
	}
	issueWithImage := decodeResponse(t, getIssueWithImage)
	images := issueWithImage["images"].([]interface{})
	if len(images) != 1 {
		t.Fatalf("expected one image in issue detail, got %#v", issueWithImage["images"])
	}

	getImageContent := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+identifier+"/images/"+imageID+"/content", nil)
	if getImageContent.StatusCode != http.StatusOK {
		t.Fatalf("image content expected 200, got %d", getImageContent.StatusCode)
	}
	imageBytes, err := io.ReadAll(getImageContent.Body)
	if err != nil {
		t.Fatalf("read image content: %v", err)
	}
	_ = getImageContent.Body.Close()
	if !bytes.Equal(imageBytes, contractSamplePNGBytes()) {
		t.Fatalf("unexpected image content: got %d bytes", len(imageBytes))
	}
	if contentType := getImageContent.Header.Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("unexpected image content type %q", contentType)
	}

	deleteImage := requestJSON(t, srv, http.MethodDelete, "/api/v1/app/issues/"+identifier+"/images/"+imageID, nil)
	if deleteImage.StatusCode != http.StatusOK {
		t.Fatalf("delete image expected 200, got %d", deleteImage.StatusCode)
	}
	_ = decodeResponse(t, deleteImage)

	getIssueAfterDelete := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+identifier, nil)
	if getIssueAfterDelete.StatusCode != http.StatusOK {
		t.Fatalf("get issue after image delete expected 200, got %d", getIssueAfterDelete.StatusCode)
	}
	if images := decodeResponse(t, getIssueAfterDelete)["images"].([]interface{}); len(images) != 0 {
		t.Fatalf("expected no images after delete, got %#v", images)
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
		"cron":       "0 * * * *",
		"enabled":    true,
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

func TestIssueApprovePlanContractsPromoteAndRedispatch(t *testing.T) {
	provider := &retryTrackingProvider{}
	store, srv := setupDashboardServerTest(t, provider)

	project, err := store.CreateProject("Maestro", "", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Approve plan", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssuePermissionProfile(issue.ID, kanban.PermissionProfilePlanThenFullAccess); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile: %v", err)
	}
	requestedAt := time.Date(2026, 3, 18, 12, 30, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Plan body", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/approve-plan", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve plan expected 200, got %d", resp.StatusCode)
	}
	payload := decodeResponse(t, resp)
	if payload["ok"] != true {
		t.Fatalf("expected ok response, got %#v", payload)
	}
	if len(provider.retried) != 1 || provider.retried[0] != issue.Identifier {
		t.Fatalf("expected redispatch for %s, got %v", issue.Identifier, provider.retried)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.PermissionProfile != kanban.PermissionProfileFullAccess {
		t.Fatalf("expected full-access after approval, got %q", updated.PermissionProfile)
	}
	if updated.CollaborationModeOverride != kanban.CollaborationModeOverrideDefault {
		t.Fatalf("expected default collaboration override, got %q", updated.CollaborationModeOverride)
	}
	if updated.PlanApprovalPending {
		t.Fatalf("expected pending plan approval to clear, got %+v", updated)
	}
}

func TestIssueApprovePlanContractsRejectWhenNoPendingPlanExists(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})

	issue, err := store.CreateIssue("", "", "Nothing to approve", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/approve-plan", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("approve plan without pending request expected 409, got %d", resp.StatusCode)
	}
	if decodeResponse(t, resp)["error"].(string) != "no pending plan approval" {
		t.Fatal("expected no pending plan approval error")
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

func TestWebhooksRequireBearerTokenConfigurationAndAuthorization(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mux := http.NewServeMux()
	NewServer(store, &webhookTrackingProvider{}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	disabled := requestWebhookJSON(t, srv, "", map[string]interface{}{
		"event":   "issue.retry",
		"payload": map[string]interface{}{"issue_identifier": "ISS-1"},
	})
	if disabled.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled webhooks expected 503, got %d", disabled.StatusCode)
	}
	disabledBody := decodeResponse(t, disabled)
	if !strings.Contains(disabledBody["error"].(string), webhookBearerTokenEnv) {
		t.Fatalf("expected disabled config guidance, got %#v", disabledBody)
	}

	t.Setenv(webhookBearerTokenEnv, "test-webhook-token")
	mux = http.NewServeMux()
	NewServer(store, &webhookTrackingProvider{}).Register(mux)
	srvWithAuth := httptest.NewServer(mux)
	t.Cleanup(srvWithAuth.Close)

	unauthorized := requestWebhookJSON(t, srvWithAuth, "wrong-token", map[string]interface{}{
		"event":   "issue.retry",
		"payload": map[string]interface{}{"issue_identifier": "ISS-1"},
	})
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized webhooks expected 401, got %d", unauthorized.StatusCode)
	}
	unauthorizedBody := decodeResponse(t, unauthorized)
	if unauthorizedBody["error"] != "unauthorized" {
		t.Fatalf("unexpected unauthorized response: %#v", unauthorizedBody)
	}
}

func TestWebhooksDispatchSupportedEvents(t *testing.T) {
	t.Setenv(webhookBearerTokenEnv, "test-webhook-token")

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	repoDir := t.TempDir()
	workflowPath := filepath.Join(repoDir, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte("agent:\n  command: codex app-server\n"), 0o644); err != nil {
		t.Fatalf("WriteFile WORKFLOW.md: %v", err)
	}

	project, err := store.CreateProject("Webhook project", "", repoDir, workflowPath)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	recurring, err := store.CreateIssue(project.ID, "", "Recurring webhook issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue recurring: %v", err)
	}
	if err := store.UpdateIssue(recurring.ID, map[string]interface{}{
		"issue_type": string(kanban.IssueTypeRecurring),
		"cron":       "0 * * * *",
		"enabled":    true,
	}); err != nil {
		t.Fatalf("UpdateIssue recurring: %v", err)
	}

	provider := &webhookTrackingProvider{}
	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	testCases := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
		check      func(t *testing.T)
	}{
		{
			name: "issue retry",
			body: map[string]interface{}{
				"event":       "issue.retry",
				"delivery_id": "retry-1",
				"payload":     map[string]interface{}{"issue_identifier": recurring.Identifier},
			},
			wantStatus: http.StatusAccepted,
			check: func(t *testing.T) {
				if len(provider.retried) != 1 || provider.retried[0] != recurring.Identifier {
					t.Fatalf("expected retry dispatch for %s, got %v", recurring.Identifier, provider.retried)
				}
			},
		},
		{
			name: "issue run now",
			body: map[string]interface{}{
				"event":   "issue.run_now",
				"payload": map[string]interface{}{"issue_identifier": recurring.Identifier},
			},
			wantStatus: http.StatusAccepted,
			check: func(t *testing.T) {
				if len(provider.runNow) != 1 || provider.runNow[0] != recurring.Identifier {
					t.Fatalf("expected run-now dispatch for %s, got %v", recurring.Identifier, provider.runNow)
				}
			},
		},
		{
			name: "project run",
			body: map[string]interface{}{
				"event":   "project.run",
				"payload": map[string]interface{}{"project_id": project.ID},
			},
			wantStatus: http.StatusAccepted,
			check: func(t *testing.T) {
				if len(provider.projectRefreshes) != 1 || provider.projectRefreshes[0] != project.ID {
					t.Fatalf("expected project refresh for %s, got %v", project.ID, provider.projectRefreshes)
				}
			},
		},
		{
			name: "project stop",
			body: map[string]interface{}{
				"event":   "project.stop",
				"payload": map[string]interface{}{"project_id": project.ID},
			},
			wantStatus: http.StatusAccepted,
			check: func(t *testing.T) {
				if len(provider.projectStops) != 1 || provider.projectStops[0] != project.ID {
					t.Fatalf("expected project stop for %s, got %v", project.ID, provider.projectStops)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := requestWebhookJSON(t, srv, "test-webhook-token", tc.body)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected %d, got %d", tc.wantStatus, resp.StatusCode)
			}
			body := decodeResponse(t, resp)
			if body["event"] != tc.body["event"] {
				t.Fatalf("expected event echo %v, got %#v", tc.body["event"], body)
			}
			if _, ok := body["received_at"].(string); !ok {
				t.Fatalf("expected received_at in response, got %#v", body)
			}
			result := body["result"].(map[string]interface{})
			if result["status"] == nil {
				t.Fatalf("expected result status, got %#v", result)
			}
			tc.check(t)
		})
	}
}

func TestWebhooksRejectInvalidPayloadsAndDispatchFailures(t *testing.T) {
	t.Setenv(webhookBearerTokenEnv, "test-webhook-token")

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProject("Scoped project", "", "/repo/outside", "/repo/outside/WORKFLOW.md")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	provider := &webhookTrackingProvider{
		testProvider: testProvider{
			status: map[string]interface{}{"scoped_repo_path": "/repo/inside"},
		},
	}
	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	unsupported := requestWebhookJSON(t, srv, "test-webhook-token", map[string]interface{}{
		"event":   "issue.unknown",
		"payload": map[string]interface{}{"issue_identifier": "ISS-1"},
	})
	if unsupported.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported event expected 400, got %d", unsupported.StatusCode)
	}
	unsupportedBody := decodeResponse(t, unsupported)
	if !strings.Contains(unsupportedBody["error"].(string), "unsupported event") {
		t.Fatalf("unexpected unsupported response: %#v", unsupportedBody)
	}

	missingIdentifier := requestWebhookJSON(t, srv, "test-webhook-token", map[string]interface{}{
		"event":   "issue.retry",
		"payload": map[string]interface{}{},
	})
	if missingIdentifier.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing issue identifier expected 400, got %d", missingIdentifier.StatusCode)
	}

	dispatchBlocked := requestWebhookJSON(t, srv, "test-webhook-token", map[string]interface{}{
		"event":   "project.run",
		"payload": map[string]interface{}{"project_id": project.ID},
	})
	if dispatchBlocked.StatusCode != http.StatusBadRequest {
		t.Fatalf("blocked project dispatch expected 400, got %d", dispatchBlocked.StatusCode)
	}
	dispatchBlockedBody := decodeResponse(t, dispatchBlocked)
	if !strings.Contains(dispatchBlockedBody["error"].(string), "outside the current server scope") {
		t.Fatalf("unexpected blocked dispatch response: %#v", dispatchBlockedBody)
	}

	missingProjectStop := requestWebhookJSON(t, srv, "test-webhook-token", map[string]interface{}{
		"event":   "project.stop",
		"payload": map[string]interface{}{"project_id": "proj_missing"},
	})
	if missingProjectStop.StatusCode != http.StatusNotFound {
		t.Fatalf("missing project stop expected 404, got %d", missingProjectStop.StatusCode)
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
