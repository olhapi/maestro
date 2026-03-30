package dashboardapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/providers"
)

type coverageIssueProvider struct {
	issue             *kanban.Issue
	listErr           error
	getErr            error
	updateErr         error
	deleteErr         error
	stateErr          error
	commentsErr       error
	createCommentErr  error
	updateCommentErr  error
	deleteCommentErr  error
	attachmentErr     error
	capabilities      kanban.ProviderCapabilities
}

func (p *coverageIssueProvider) Kind() string {
	return "stub"
}

func (p *coverageIssueProvider) Capabilities() kanban.ProviderCapabilities {
	if p.capabilities == (kanban.ProviderCapabilities{}) {
		return kanban.ProviderCapabilities{
			Projects:         true,
			Epics:            true,
			Issues:           true,
			IssueStateUpdate: true,
			IssueDelete:      true,
		}
	}
	return p.capabilities
}

func (p *coverageIssueProvider) ValidateProject(context.Context, *kanban.Project) error {
	return nil
}

func (p *coverageIssueProvider) ListIssues(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error) {
	if p.listErr != nil {
		return nil, p.listErr
	}
	if p.issue == nil {
		return []kanban.Issue{}, nil
	}
	return []kanban.Issue{*p.issue}, nil
}

func (p *coverageIssueProvider) GetIssue(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	if p.issue == nil {
		return &kanban.Issue{Identifier: "stub-issue", ProviderKind: p.Kind(), ProviderIssueRef: "stub-issue"}, nil
	}
	cp := *p.issue
	return &cp, nil
}

func (p *coverageIssueProvider) CreateIssue(context.Context, *kanban.Project, providers.IssueCreateInput) (*kanban.Issue, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	if p.issue == nil {
		return &kanban.Issue{Identifier: "stub-issue", ProviderKind: p.Kind(), ProviderIssueRef: "stub-issue"}, nil
	}
	cp := *p.issue
	return &cp, nil
}

func (p *coverageIssueProvider) UpdateIssue(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error) {
	if p.updateErr != nil {
		return nil, p.updateErr
	}
	if p.issue == nil {
		return &kanban.Issue{Identifier: "stub-issue", ProviderKind: p.Kind(), ProviderIssueRef: "stub-issue"}, nil
	}
	cp := *p.issue
	return &cp, nil
}

func (p *coverageIssueProvider) DeleteIssue(context.Context, *kanban.Project, *kanban.Issue) error {
	return p.deleteErr
}

func (p *coverageIssueProvider) SetIssueState(context.Context, *kanban.Project, *kanban.Issue, string) (*kanban.Issue, error) {
	if p.stateErr != nil {
		return nil, p.stateErr
	}
	if p.issue == nil {
		return &kanban.Issue{Identifier: "stub-issue", ProviderKind: p.Kind(), ProviderIssueRef: "stub-issue"}, nil
	}
	cp := *p.issue
	return &cp, nil
}

func (p *coverageIssueProvider) ListIssueComments(context.Context, *kanban.Project, *kanban.Issue) ([]kanban.IssueComment, error) {
	if p.commentsErr != nil {
		return nil, p.commentsErr
	}
	return []kanban.IssueComment{}, nil
}

func (p *coverageIssueProvider) CreateIssueComment(context.Context, *kanban.Project, *kanban.Issue, providers.IssueCommentInput) (*kanban.IssueComment, error) {
	if p.createCommentErr != nil {
		return nil, p.createCommentErr
	}
	return &kanban.IssueComment{ID: "comment-1"}, nil
}

func (p *coverageIssueProvider) UpdateIssueComment(context.Context, *kanban.Project, *kanban.Issue, string, providers.IssueCommentInput) (*kanban.IssueComment, error) {
	if p.updateCommentErr != nil {
		return nil, p.updateCommentErr
	}
	return &kanban.IssueComment{ID: "comment-1"}, nil
}

func (p *coverageIssueProvider) DeleteIssueComment(context.Context, *kanban.Project, *kanban.Issue, string) error {
	return p.deleteCommentErr
}

func (p *coverageIssueProvider) GetIssueCommentAttachmentContent(context.Context, *kanban.Project, *kanban.Issue, string, string) (*providers.IssueCommentAttachmentContent, error) {
	if p.attachmentErr != nil {
		return nil, p.attachmentErr
	}
	return &providers.IssueCommentAttachmentContent{
		Attachment: kanban.IssueCommentAttachment{ID: "attachment-1", Filename: "file.txt", ContentType: "text/plain"},
		Content:    io.NopCloser(strings.NewReader("content")),
	}, nil
}

func invokeDashboardRoute(t *testing.T, handler func(http.ResponseWriter, *http.Request), method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func buildMultipartForm(t *testing.T, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write multipart field %s: %v", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, writer.FormDataContentType()
}

func TestDashboardRouteValidationBranches(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "validation.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(store, testProvider{})

	for _, tc := range []struct {
		name     string
		method   string
		path     string
		handler  func(http.ResponseWriter, *http.Request)
		wantCode int
	}{
		{name: "project empty", method: http.MethodGet, path: "/api/v1/app/projects/", handler: server.handleProject, wantCode: http.StatusNotFound},
		{name: "project nested", method: http.MethodGet, path: "/api/v1/app/projects/foo/bar", handler: server.handleProject, wantCode: http.StatusNotFound},
		{name: "epic empty", method: http.MethodGet, path: "/api/v1/app/epics/", handler: server.handleEpic, wantCode: http.StatusNotFound},
		{name: "epic nested", method: http.MethodGet, path: "/api/v1/app/epics/foo/bar", handler: server.handleEpic, wantCode: http.StatusNotFound},
		{name: "issue empty", method: http.MethodGet, path: "/api/v1/app/issues/", handler: server.handleIssue, wantCode: http.StatusNotFound},
		{name: "issue nested", method: http.MethodGet, path: "/api/v1/app/issues//", handler: server.handleIssue, wantCode: http.StatusNotFound},
		{name: "ws upgrade", method: http.MethodGet, path: "/api/v1/ws", handler: server.handleWS, wantCode: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := invokeDashboardRoute(t, tc.handler, tc.method, tc.path, nil, nil)
			if rr.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d", tc.wantCode, rr.Code)
			}
		})
	}

	t.Run("issue commands path validation", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/app/issues/ISS-1/commands/", nil)
		server.handleIssueCommands(rr, req, "ISS-1", []string{""})
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected blank command id to return 404, got %d", rr.Code)
		}

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/v1/app/issues/ISS-1/commands/missing/steer", nil)
		server.handleIssueCommands(rr, req, "ISS-1", []string{"", "steer"})
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected blank steer command id to return 404, got %d", rr.Code)
		}
	})

	t.Run("interrupt path validation", func(t *testing.T) {
		rr := invokeDashboardRoute(t, server.handleInterrupt, http.MethodPost, "/api/v1/app/interrupts/", nil, nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected missing interrupt id to return 404, got %d", rr.Code)
		}

		rr = invokeDashboardRoute(t, server.handleInterrupt, http.MethodGet, "/api/v1/app/interrupts/interrupt-1/respond", nil, nil)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected GET respond to return 405, got %d", rr.Code)
		}

		rr = invokeDashboardRoute(t, server.handleInterrupt, http.MethodPost, "/api/v1/app/interrupts/interrupt-1/unknown", bytes.NewReader([]byte(`{}`)), nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected unknown interrupt action to return 404, got %d", rr.Code)
		}
	})
}

func TestDashboardClosedStoreBranches(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	server := NewServer(store, testProvider{})
	if err := store.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	tests := []struct {
		name     string
		method   string
		path     string
		body     io.Reader
		wantCode int
		handler  func(http.ResponseWriter, *http.Request)
		headers  map[string]string
	}{
		{name: "bootstrap", method: http.MethodGet, path: "/api/v1/app/bootstrap", handler: server.handleBootstrap, wantCode: http.StatusInternalServerError},
		{name: "work", method: http.MethodGet, path: "/api/v1/app/work", handler: server.handleWork, wantCode: http.StatusInternalServerError},
		{name: "projects", method: http.MethodGet, path: "/api/v1/app/projects", handler: server.handleProjects, wantCode: http.StatusInternalServerError},
		{name: "project", method: http.MethodGet, path: "/api/v1/app/projects/proj-1", handler: server.handleProject, wantCode: http.StatusInternalServerError},
		{name: "epic", method: http.MethodGet, path: "/api/v1/app/epics/epic-1", handler: server.handleEpic, wantCode: http.StatusInternalServerError},
		{name: "issues", method: http.MethodGet, path: "/api/v1/app/issues", handler: server.handleIssues, wantCode: http.StatusInternalServerError},
		{name: "issue detail", method: http.MethodGet, path: "/api/v1/app/issues/iss-1", handler: server.handleIssue, wantCode: http.StatusInternalServerError},
		{name: "issue execution", method: http.MethodGet, path: "/api/v1/app/issues/iss-1/execution", handler: server.handleIssue, wantCode: http.StatusInternalServerError},
		{name: "issue commands", method: http.MethodPost, path: "/api/v1/app/issues/iss-1/commands", body: strings.NewReader(`{"command":"noop"}`), handler: server.handleIssue, wantCode: http.StatusInternalServerError, headers: map[string]string{"Content-Type": "application/json"}},
		{name: "issue assets get", method: http.MethodGet, path: "/api/v1/app/issues/iss-1/assets/asset-1/content", handler: server.handleIssue, wantCode: http.StatusInternalServerError},
		{name: "issue assets delete", method: http.MethodDelete, path: "/api/v1/app/issues/iss-1/assets/asset-1", handler: server.handleIssue, wantCode: http.StatusInternalServerError},
		{name: "issue comments get", method: http.MethodGet, path: "/api/v1/app/issues/iss-1/comments", handler: server.handleIssue, wantCode: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := invokeDashboardRoute(t, tc.handler, tc.method, tc.path, tc.body, tc.headers)
			if rr.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d", tc.wantCode, rr.Code)
			}
		})
	}

	t.Run("issue asset upload", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/app/issues/iss-1/assets", strings.NewReader("not multipart"))
		req.Header.Set("Content-Type", "multipart/form-data")
		server.handleIssueAssets(rr, req, "iss-1", nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected invalid multipart upload to return 400, got %d", rr.Code)
		}
	})

	t.Run("comment multipart upload", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/app/issues/iss-1/comments", strings.NewReader("not multipart"))
		req.Header.Set("Content-Type", "multipart/form-data")
		server.handleIssueComments(rr, req, "iss-1", nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected invalid comment multipart to return 400, got %d", rr.Code)
		}
	})
}

func TestDashboardProviderBranches(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "provider.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	syncRepoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(syncRepoRoot, "WORKFLOW.md"), []byte("# workflow"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	syncProject, err := store.CreateProjectWithProvider("Sync Project", "", syncRepoRoot, filepath.Join(syncRepoRoot, "WORKFLOW.md"), "stub", "", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider sync project: %v", err)
	}
	syncEpic, err := store.CreateEpic(syncProject.ID, "Sync Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic sync epic: %v", err)
	}
	issueRepoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(issueRepoRoot, "WORKFLOW.md"), []byte("# workflow"), 0o644); err != nil {
		t.Fatalf("write issue workflow: %v", err)
	}
	issueProject, err := store.CreateProjectWithProvider("Issue Project", "", issueRepoRoot, filepath.Join(issueRepoRoot, "WORKFLOW.md"), "stub", "", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider issue project: %v", err)
	}
	issue, err := store.UpsertProviderIssue(issueProject.ID, &kanban.Issue{
		Identifier:       "STUB-1",
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Title:            "Stub issue",
		State:            kanban.StateBacklog,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	noProviderServer := NewServer(store, testProvider{})
	for _, tc := range []struct {
		name     string
		method   string
		path     string
		handler  func(http.ResponseWriter, *http.Request)
		wantCode int
	}{
		{name: "project list epics", method: http.MethodGet, path: "/api/v1/app/projects/" + syncProject.ID, handler: noProviderServer.handleProject, wantCode: http.StatusInternalServerError},
		{name: "epic project lookup", method: http.MethodGet, path: "/api/v1/app/epics/" + syncEpic.ID, handler: noProviderServer.handleEpic, wantCode: http.StatusInternalServerError},
		{name: "issue detail", method: http.MethodGet, path: "/api/v1/app/issues/" + issue.Identifier, handler: noProviderServer.handleIssue, wantCode: http.StatusBadRequest},
		{name: "issue execution", method: http.MethodGet, path: "/api/v1/app/issues/" + issue.Identifier + "/execution", handler: noProviderServer.handleIssue, wantCode: http.StatusBadRequest},
		{name: "issue delete", method: http.MethodDelete, path: "/api/v1/app/issues/" + issue.Identifier, handler: noProviderServer.handleIssue, wantCode: http.StatusNotFound},
		{name: "issue assets content", method: http.MethodGet, path: "/api/v1/app/issues/" + issue.Identifier + "/assets/asset-1/content", handler: noProviderServer.handleIssue, wantCode: http.StatusBadRequest},
		{name: "issue comments list", method: http.MethodGet, path: "/api/v1/app/issues/" + issue.Identifier + "/comments", handler: noProviderServer.handleIssue, wantCode: http.StatusBadRequest},
		{name: "issue commands", method: http.MethodPost, path: "/api/v1/app/issues/" + issue.Identifier + "/commands", handler: noProviderServer.handleIssue, wantCode: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := invokeDashboardRoute(t, tc.handler, tc.method, tc.path, strings.NewReader(`{"command":"noop"}`), map[string]string{"Content-Type": "application/json"})
			if rr.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d", tc.wantCode, rr.Code)
			}
		})
	}

	coverageProvider := &coverageIssueProvider{
		issue:            issue,
		listErr:          errors.New("list issues failed"),
		updateErr:        errors.New("update failed"),
		deleteErr:        errors.New("delete failed"),
		stateErr:         errors.New("state failed"),
		commentsErr:      errors.New("comments failed"),
		createCommentErr: errors.New("create comment failed"),
		updateCommentErr: errors.New("update comment failed"),
		deleteCommentErr: errors.New("delete comment failed"),
		attachmentErr:    errors.New("attachment failed"),
	}
	withProviderServer := NewServer(store, testProvider{})
	withProviderServer.service.RegisterProvider(coverageProvider)

	for _, tc := range []struct {
		name     string
		method   string
		path     string
		body     io.Reader
		headers  map[string]string
		handler  func(http.ResponseWriter, *http.Request)
		wantCode int
	}{
		{name: "bootstrap list issue sync", method: http.MethodGet, path: "/api/v1/app/bootstrap", handler: withProviderServer.handleBootstrap, wantCode: http.StatusInternalServerError},
		{name: "work list issue sync", method: http.MethodGet, path: "/api/v1/app/work", handler: withProviderServer.handleWork, wantCode: http.StatusInternalServerError},
		{name: "issues list issue sync", method: http.MethodGet, path: "/api/v1/app/issues", handler: withProviderServer.handleIssues, wantCode: http.StatusInternalServerError},
		{name: "project issue summary sync", method: http.MethodGet, path: "/api/v1/app/projects/" + syncProject.ID, handler: withProviderServer.handleProject, wantCode: http.StatusInternalServerError},
		{name: "epic issue summary sync", method: http.MethodGet, path: "/api/v1/app/epics/" + syncEpic.ID, handler: withProviderServer.handleEpic, wantCode: http.StatusInternalServerError},
		{name: "issue patch", method: http.MethodPatch, path: "/api/v1/app/issues/" + issue.Identifier, body: strings.NewReader(`{"title":"Changed"}`), headers: map[string]string{"Content-Type": "application/json"}, handler: withProviderServer.handleIssue, wantCode: http.StatusInternalServerError},
		{name: "issue state", method: http.MethodPost, path: "/api/v1/app/issues/" + issue.Identifier + "/state", body: strings.NewReader(`{"state":"ready"}`), headers: map[string]string{"Content-Type": "application/json"}, handler: withProviderServer.handleIssue, wantCode: http.StatusInternalServerError},
		{name: "issue blockers", method: http.MethodPost, path: "/api/v1/app/issues/" + issue.Identifier + "/blockers", body: strings.NewReader(`{"blocked_by":["foo"]}`), headers: map[string]string{"Content-Type": "application/json"}, handler: withProviderServer.handleIssue, wantCode: http.StatusInternalServerError},
		{name: "issue assets content", method: http.MethodGet, path: "/api/v1/app/issues/" + issue.Identifier + "/assets/asset-1/content", handler: withProviderServer.handleIssue, wantCode: http.StatusNotFound},
		{name: "issue assets delete", method: http.MethodDelete, path: "/api/v1/app/issues/" + issue.Identifier + "/assets/asset-1", handler: withProviderServer.handleIssue, wantCode: http.StatusNotFound},
		{name: "issue comments list", method: http.MethodGet, path: "/api/v1/app/issues/" + issue.Identifier + "/comments", handler: withProviderServer.handleIssue, wantCode: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := invokeDashboardRoute(t, tc.handler, tc.method, tc.path, tc.body, tc.headers)
			if rr.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d", tc.wantCode, rr.Code)
			}
		})
	}

	t.Run("issue commands submit error", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/commands", strings.NewReader(`{"command":"run"}`))
		req.Header.Set("Content-Type", "application/json")
		withProviderServer.handleIssueCommands(rr, req, issue.Identifier, nil)
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected command submission failure to return 500, got %d", rr.Code)
		}
	})

	t.Run("issue commands command update and delete", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/app/issues/"+issue.Identifier+"/commands/command-1", strings.NewReader(`{"command":"edit"}`))
		req.Header.Set("Content-Type", "application/json")
		withProviderServer.handleIssueCommands(rr, req, issue.Identifier, []string{"command-1"})
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected missing command update to return 404, got %d", rr.Code)
		}

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodDelete, "/api/v1/app/issues/"+issue.Identifier+"/commands/command-1", nil)
		withProviderServer.handleIssueCommands(rr, req, issue.Identifier, []string{"command-1"})
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected missing command delete to return 404, got %d", rr.Code)
		}

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/commands/command-1/steer", nil)
		withProviderServer.handleIssueCommands(rr, req, issue.Identifier, []string{"command-1", "steer"})
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected missing command steer to return 404, got %d", rr.Code)
		}
	})

	t.Run("issue comments multipart and attachments", func(t *testing.T) {
		body, contentType := buildMultipartForm(t, map[string]string{"body": "Hello"})
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/comments", bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", contentType)
		withProviderServer.handleIssueComments(rr, req, issue.Identifier, nil)
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected comment creation failure to return 500, got %d", rr.Code)
		}

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/api/v1/app/issues/"+issue.Identifier+"/comments/comment-1/attachments/attachment-1/content", nil)
		withProviderServer.handleIssueComments(rr, req, issue.Identifier, []string{"comment-1", "attachments", "attachment-1", "content"})
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected attachment content failure to return 500, got %d", rr.Code)
		}

		body, contentType = buildMultipartForm(t, map[string]string{"body": "Updated"})
		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPatch, "/api/v1/app/issues/"+issue.Identifier+"/comments/comment-1", bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", contentType)
		withProviderServer.handleIssueComments(rr, req, issue.Identifier, []string{"comment-1"})
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected comment update failure to return 500, got %d", rr.Code)
		}

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodDelete, "/api/v1/app/issues/"+issue.Identifier+"/comments/comment-1", nil)
		withProviderServer.handleIssueComments(rr, req, issue.Identifier, []string{"comment-1"})
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected comment delete failure to return 500, got %d", rr.Code)
		}
	})

	t.Run("issue asset upload and lookup", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/assets", strings.NewReader("not multipart"))
		req.Header.Set("Content-Type", "multipart/form-data")
		withProviderServer.handleIssueAssets(rr, req, issue.Identifier, nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected invalid asset multipart to return 400, got %d", rr.Code)
		}
	})
}
