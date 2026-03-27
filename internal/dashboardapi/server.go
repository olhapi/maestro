package dashboardapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/internal/providers"
	"github.com/olhapi/maestro/internal/runtimeview"
)

type Provider interface {
	observability.StatusProvider
	observability.SnapshotProvider
	observability.SessionProvider
	observability.EventProvider
	observability.RefreshProvider
	PendingInterrupts() appserver.PendingInteractionSnapshot
	PendingInterruptForIssue(issueID, identifier string) (*appserver.PendingInteraction, bool)
	RespondToInterrupt(ctx context.Context, interactionID string, response appserver.PendingInteractionResponse) error
	AcknowledgeInterrupt(ctx context.Context, interactionID string) error
	RetryIssueNow(ctx context.Context, identifier string) map[string]interface{}
	RunRecurringIssueNow(ctx context.Context, identifier string) map[string]interface{}
	RequestProjectRefresh(projectID string) map[string]interface{}
	StopProjectRuns(projectID string) map[string]interface{}
}

type Server struct {
	store    *kanban.Store
	service  *providers.Service
	provider Provider
	webhook  webhookAuthConfig
	upgrader websocket.Upgrader
}

const (
	dashboardWSReadTimeout  = 60 * time.Second
	dashboardWSWriteTimeout = 5 * time.Second
	dashboardWSPingInterval = 30 * time.Second
)

var inlineRenderableContentTypes = map[string]struct{}{
	"image/apng":               {},
	"image/avif":               {},
	"image/bmp":                {},
	"image/gif":                {},
	"image/jpeg":               {},
	"image/png":                {},
	"image/vnd.microsoft.icon": {},
	"image/webp":               {},
	"image/x-icon":             {},
}

func NewServer(store *kanban.Store, provider Provider) *Server {
	return &Server{
		store:    store,
		service:  providers.NewService(store),
		provider: provider,
		webhook:  loadWebhookAuthConfig(),
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
}

func scopedRepoPathFromStatus(status map[string]interface{}) string {
	if status == nil {
		return ""
	}
	value, _ := status["scoped_repo_path"].(string)
	return strings.TrimSpace(value)
}

func projectScopeError(projectRepoPath, scopedRepoPath string) string {
	projectRepoPath = strings.TrimSpace(projectRepoPath)
	scopedRepoPath = strings.TrimSpace(scopedRepoPath)
	if projectRepoPath == "" || scopedRepoPath == "" {
		return ""
	}
	if filepath.Clean(projectRepoPath) == filepath.Clean(scopedRepoPath) {
		return ""
	}
	return "Project repo is outside the current server scope (" + scopedRepoPath + ")"
}

func decorateProject(project *kanban.Project, scopedRepoPath string) {
	if project == nil {
		return
	}
	project.DispatchReady = project.OrchestrationReady
	project.DispatchError = ""
	if scopeError := projectScopeError(project.RepoPath, scopedRepoPath); scopeError != "" {
		project.DispatchReady = false
		project.DispatchError = scopeError
	}
}

func decorateProjectSummaries(projects []kanban.ProjectSummary, scopedRepoPath string) []kanban.ProjectSummary {
	for i := range projects {
		decorateProject(&projects[i].Project, scopedRepoPath)
	}
	return projects
}

func validateScopedRepoPath(repoPath, scopedRepoPath string) error {
	if strings.TrimSpace(scopedRepoPath) == "" {
		return nil
	}
	absRepoPath, err := filepath.Abs(strings.TrimSpace(repoPath))
	if err != nil {
		return err
	}
	if filepath.Clean(absRepoPath) == filepath.Clean(scopedRepoPath) {
		return nil
	}
	return fmt.Errorf("repo_path must match the current server scope (%s)", scopedRepoPath)
}

func contentDispositionHeader(filename, contentType string) string {
	disposition := "attachment"
	if isInlineRenderableContentType(contentType) {
		disposition = "inline"
	}
	return mime.FormatMediaType(disposition, map[string]string{"filename": filename})
}

func isInlineRenderableContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		mediaType = strings.TrimSpace(contentType)
	}
	_, ok := inlineRenderableContentTypes[strings.ToLower(mediaType)]
	return ok
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/webhooks", s.handleWebhook)
	mux.HandleFunc("/api/v1/app/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/v1/app/work", s.handleWork)
	mux.HandleFunc("/api/v1/app/projects", s.handleProjects)
	mux.HandleFunc("/api/v1/app/projects/", s.handleProject)
	mux.HandleFunc("/api/v1/app/epics", s.handleEpics)
	mux.HandleFunc("/api/v1/app/epics/", s.handleEpic)
	mux.HandleFunc("/api/v1/app/issues", s.handleIssues)
	mux.HandleFunc("/api/v1/app/issues/", s.handleIssue)
	mux.HandleFunc("/api/v1/app/runtime/events", s.handleRuntimeEvents)
	mux.HandleFunc("/api/v1/app/runtime/series", s.handleRuntimeSeries)
	mux.HandleFunc("/api/v1/app/interrupts", s.handleInterrupts)
	mux.HandleFunc("/api/v1/app/interrupts/", s.handleInterrupt)
	mux.HandleFunc("/api/v1/app/sessions", s.handleSessions)
	mux.HandleFunc("/api/v1/ws", s.handleWS)
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	status := s.provider.Status()
	scopedRepoPath := scopedRepoPathFromStatus(status)
	projects, err := s.service.ListProjectSummaries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	projects = decorateProjectSummaries(projects, scopedRepoPath)
	epics, err := s.service.ListEpicSummaries("")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	issues, total, err := s.service.ListIssueSummaries(r.Context(), kanban.IssueQuery{
		Limit:  100,
		Offset: 0,
		Sort:   "updated_desc",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	series, err := s.store.RuntimeSeries(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	runtimeEvents, err := s.store.ListRuntimeEvents(0, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	snapshot := s.provider.Snapshot()
	board := kanban.IssueStateCounts{}
	for _, issue := range issues {
		board.Add(issue.State)
	}

	writeJSON(w, map[string]interface{}{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"overview": map[string]interface{}{
			"status":        status,
			"snapshot":      snapshot,
			"board":         board,
			"project_count": len(projects),
			"epic_count":    len(epics),
			"issue_count":   total,
			"series":        series,
			"recent_events": runtimeEvents,
		},
		"projects": projects,
		"epics":    epics,
		"issues": map[string]interface{}{
			"items":  issues,
			"total":  total,
			"limit":  100,
			"offset": 0,
		},
		"sessions": s.provider.LiveSessions(),
	})
}

func (s *Server) handleWork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	status := s.provider.Status()
	scopedRepoPath := scopedRepoPathFromStatus(status)
	projects, err := s.service.ListProjectSummaries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	projects = decorateProjectSummaries(projects, scopedRepoPath)
	epics, err := s.service.ListEpicSummaries("")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	issues, total, err := s.service.ListIssueSummaries(r.Context(), kanban.IssueQuery{
		Limit:  100,
		Offset: 0,
		Sort:   "updated_desc",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	snapshot := s.provider.Snapshot()
	board := kanban.IssueStateCounts{}
	for _, issue := range issues {
		board.Add(issue.State)
	}

	writeJSON(w, map[string]interface{}{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"overview": map[string]interface{}{
			"snapshot": map[string]interface{}{
				"running":  snapshot.Running,
				"retrying": snapshot.Retrying,
				"paused":   snapshot.Paused,
			},
			"board": board,
		},
		"projects": projects,
		"epics":    epics,
		"issues": map[string]interface{}{
			"items":  issues,
			"total":  total,
			"limit":  100,
			"offset": 0,
		},
		"sessions": s.provider.LiveSessions(),
	})
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status := s.provider.Status()
		scopedRepoPath := scopedRepoPathFromStatus(status)
		projects, err := s.service.ListProjectSummaries()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		projects = decorateProjectSummaries(projects, scopedRepoPath)
		writeJSON(w, map[string]interface{}{"items": projects})
	case http.MethodPost:
		var body struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			RuntimeName  string `json:"runtime_name"`
			RepoPath     string `json:"repo_path"`
			WorkflowPath string `json:"workflow_path"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.RepoPath) == "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "repo_path is required"})
			return
		}
		if err := validateScopedRepoPath(body.RepoPath, scopedRepoPathFromStatus(s.provider.Status())); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		project, err := s.service.CreateProject(
			r.Context(),
			strings.TrimSpace(body.Name),
			strings.TrimSpace(body.Description),
			strings.TrimSpace(body.RepoPath),
			strings.TrimSpace(body.WorkflowPath),
			strings.TrimSpace(body.RuntimeName),
			kanban.ProviderKindKanban,
			"",
			nil,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		decorateProject(project, scopedRepoPathFromStatus(s.provider.Status()))
		writeJSONStatus(w, http.StatusCreated, project)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/app/projects/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	id, action, hasAction := strings.Cut(rest, "/")
	if id == "" || strings.Contains(id, "/") || (hasAction && strings.Contains(action, "/")) {
		http.NotFound(w, r)
		return
	}
	if hasAction {
		project, err := s.store.GetProject(id)
		if err != nil {
			writeErrorStatus(w, http.StatusNotFound, err)
			return
		}
		decorateProject(project, scopedRepoPathFromStatus(s.provider.Status()))
		switch {
		case r.Method == http.MethodPost && action == "run":
			if !project.DispatchReady {
				errText := project.DispatchError
				if strings.TrimSpace(errText) == "" {
					errText = "project is not dispatchable"
				}
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": errText})
				return
			}
			writeJSON(w, s.provider.RequestProjectRefresh(id))
			return
		case r.Method == http.MethodPost && action == "stop":
			writeJSON(w, s.provider.StopProjectRuns(id))
			return
		case r.Method == http.MethodPost && action == "permissions":
			var body struct {
				PermissionProfile string `json:"permission_profile"`
			}
			if !decodeJSON(w, r, &body) {
				return
			}
			profile, err := kanban.ParsePermissionProfile(body.PermissionProfile)
			if err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			if err := s.store.UpdateProjectPermissionProfile(id, profile); err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			updated, err := s.store.GetProject(id)
			if err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			decorateProject(updated, scopedRepoPathFromStatus(s.provider.Status()))
			writeJSON(w, updated)
			return
		default:
			http.NotFound(w, r)
			return
		}
	}
	switch r.Method {
	case http.MethodGet:
		status := s.provider.Status()
		scopedRepoPath := scopedRepoPathFromStatus(status)
		projectSummaries, err := s.service.ListProjectSummaries()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		projectSummaries = decorateProjectSummaries(projectSummaries, scopedRepoPath)
		var projectSummary *kanban.ProjectSummary
		for i := range projectSummaries {
			if projectSummaries[i].ID == id {
				projectSummary = &projectSummaries[i]
				break
			}
		}
		if projectSummary == nil {
			writeErrorStatus(w, http.StatusNotFound, sql.ErrNoRows)
			return
		}
		epics, err := s.service.ListEpicSummaries(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		issues, total, err := s.service.ListIssueSummaries(r.Context(), kanban.IssueQuery{
			ProjectID: id,
			Sort:      "updated_desc",
			Limit:     200,
			Offset:    0,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]interface{}{
			"project": projectSummary,
			"epics":   epics,
			"issues": map[string]interface{}{
				"items":  issues,
				"total":  total,
				"limit":  200,
				"offset": 0,
			},
		})
	case http.MethodPatch:
		var body struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			RuntimeName  string `json:"runtime_name"`
			RepoPath     string `json:"repo_path"`
			WorkflowPath string `json:"workflow_path"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.RepoPath) == "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "repo_path is required"})
			return
		}
		if err := validateScopedRepoPath(body.RepoPath, scopedRepoPathFromStatus(s.provider.Status())); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.service.UpdateProject(
			r.Context(),
			id,
			strings.TrimSpace(body.Name),
			strings.TrimSpace(body.Description),
			strings.TrimSpace(body.RepoPath),
			strings.TrimSpace(body.WorkflowPath),
			strings.TrimSpace(body.RuntimeName),
			kanban.ProviderKindKanban,
			"",
			nil,
		); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		project, err := s.store.GetProject(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		decorateProject(project, scopedRepoPathFromStatus(s.provider.Status()))
		writeJSON(w, project)
	case http.MethodDelete:
		if err := s.store.DeleteProject(id); err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, map[string]interface{}{"deleted": true, "id": id})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleEpics(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projectID := r.URL.Query().Get("project_id")
		epics, err := s.service.ListEpicSummaries(projectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]interface{}{"items": epics})
	case http.MethodPost:
		var body struct {
			ProjectID   string `json:"project_id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		epic, err := s.service.CreateEpic(strings.TrimSpace(body.ProjectID), strings.TrimSpace(body.Name), strings.TrimSpace(body.Description))
		if err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, epic)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleEpic(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/app/epics/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		epicSummaries, err := s.service.ListEpicSummaries("")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		var epicSummary *kanban.EpicSummary
		for i := range epicSummaries {
			if epicSummaries[i].ID == id {
				epicSummary = &epicSummaries[i]
				break
			}
		}
		if epicSummary == nil {
			writeErrorStatus(w, http.StatusNotFound, sql.ErrNoRows)
			return
		}
		var project *kanban.Project
		if epicSummary.ProjectID != "" {
			project, err = s.store.GetProject(epicSummary.ProjectID)
			if err != nil && err != sql.ErrNoRows {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
		}
		siblingEpics, err := s.service.ListEpicSummaries(epicSummary.ProjectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		issues, total, err := s.service.ListIssueSummaries(r.Context(), kanban.IssueQuery{
			EpicID: id,
			Sort:   "updated_desc",
			Limit:  200,
			Offset: 0,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]interface{}{
			"epic":          epicSummary,
			"project":       project,
			"sibling_epics": siblingEpics,
			"issues": map[string]interface{}{
				"items":  issues,
				"total":  total,
				"limit":  200,
				"offset": 0,
			},
		})
	case http.MethodPatch:
		var body struct {
			ProjectID   string `json:"project_id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if err := s.service.UpdateEpic(id, strings.TrimSpace(body.ProjectID), strings.TrimSpace(body.Name), strings.TrimSpace(body.Description)); err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		epic, err := s.store.GetEpic(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, epic)
	case http.MethodDelete:
		if err := s.service.DeleteEpic(id); err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, map[string]interface{}{"deleted": true, "id": id})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		query := kanban.IssueQuery{
			ProjectID: r.URL.Query().Get("project_id"),
			EpicID:    r.URL.Query().Get("epic_id"),
			State:     r.URL.Query().Get("state"),
			IssueType: r.URL.Query().Get("issue_type"),
			Search:    r.URL.Query().Get("search"),
			Sort:      r.URL.Query().Get("sort"),
			Limit:     queryInt(r, "limit", 200),
			Offset:    queryInt(r, "offset", 0),
		}
		issues, total, err := s.service.ListIssueSummaries(r.Context(), query)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]interface{}{
			"items":  issues,
			"total":  total,
			"limit":  query.Limit,
			"offset": query.Offset,
		})
	case http.MethodPost:
		var body struct {
			ProjectID   string   `json:"project_id"`
			EpicID      string   `json:"epic_id"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			IssueType   string   `json:"issue_type"`
			Cron        string   `json:"cron"`
			Enabled     *bool    `json:"enabled"`
			Priority    int      `json:"priority"`
			Labels      []string `json:"labels"`
			RuntimeName string   `json:"runtime_name"`
			AgentName   string   `json:"agent_name"`
			AgentPrompt string   `json:"agent_prompt"`
			State       string   `json:"state"`
			BlockedBy   []string `json:"blocked_by"`
			BranchName  string   `json:"branch_name"`
			PRURL       string   `json:"pr_url"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		detail, err := s.service.CreateIssue(r.Context(), providers.IssueCreateInput{
			ProjectID:   body.ProjectID,
			EpicID:      body.EpicID,
			Title:       strings.TrimSpace(body.Title),
			Description: strings.TrimSpace(body.Description),
			IssueType:   kanban.IssueType(strings.TrimSpace(body.IssueType)),
			Cron:        strings.TrimSpace(body.Cron),
			Enabled:     body.Enabled,
			Priority:    body.Priority,
			Labels:      body.Labels,
			RuntimeName: strings.TrimSpace(body.RuntimeName),
			AgentName:   strings.TrimSpace(body.AgentName),
			AgentPrompt: strings.TrimSpace(body.AgentPrompt),
			State:       body.State,
			BlockedBy:   body.BlockedBy,
			BranchName:  body.BranchName,
			PRURL:       body.PRURL,
		})
		if err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, detail)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleIssue(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/app/issues/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	identifier := parts[0]
	if identifier == "" {
		http.NotFound(w, r)
		return
	}

	if len(parts) >= 2 && parts[1] == "assets" {
		s.handleIssueAssets(w, r, identifier, parts[2:])
		return
	}
	if len(parts) >= 2 && parts[1] == "comments" {
		s.handleIssueComments(w, r, identifier, parts[2:])
		return
	}
	if len(parts) >= 2 && parts[1] == "commands" {
		s.handleIssueCommands(w, r, identifier, parts[2:])
		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			detail, err := s.service.GetIssueDetailByIdentifier(r.Context(), identifier)
			if err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			writeJSON(w, detail)
		case http.MethodPatch:
			var body struct {
				ProjectID   string   `json:"project_id"`
				EpicID      string   `json:"epic_id"`
				Title       string   `json:"title"`
				Description string   `json:"description"`
				IssueType   *string  `json:"issue_type"`
				Cron        *string  `json:"cron"`
				Enabled     *bool    `json:"enabled"`
				Priority    int      `json:"priority"`
				Labels      []string `json:"labels"`
				RuntimeName string   `json:"runtime_name"`
				AgentName   *string  `json:"agent_name"`
				AgentPrompt *string  `json:"agent_prompt"`
				BlockedBy   []string `json:"blocked_by"`
				BranchName  string   `json:"branch_name"`
				PRURL       string   `json:"pr_url"`
			}
			if !decodeJSON(w, r, &body) {
				return
			}
			updates := map[string]interface{}{
				"project_id":   body.ProjectID,
				"epic_id":      body.EpicID,
				"title":        body.Title,
				"description":  body.Description,
				"priority":     body.Priority,
				"labels":       body.Labels,
				"blocked_by":   body.BlockedBy,
				"branch_name":  body.BranchName,
				"pr_url":       body.PRURL,
				"runtime_name": strings.TrimSpace(body.RuntimeName),
			}
			if body.AgentName != nil {
				updates["agent_name"] = strings.TrimSpace(*body.AgentName)
			}
			if body.AgentPrompt != nil {
				updates["agent_prompt"] = strings.TrimSpace(*body.AgentPrompt)
			}
			if body.IssueType != nil {
				updates["issue_type"] = strings.TrimSpace(*body.IssueType)
			}
			if body.Cron != nil {
				updates["cron"] = strings.TrimSpace(*body.Cron)
			}
			if body.Enabled != nil {
				updates["enabled"] = *body.Enabled
			}
			detail, err := s.service.UpdateIssue(r.Context(), identifier, updates)
			if err != nil {
				writeError(w, appErrorStatus(err), err)
				return
			}
			writeJSON(w, detail)
		case http.MethodDelete:
			issue, err := s.service.GetIssueByIdentifier(r.Context(), identifier)
			if err != nil {
				writeErrorStatus(w, http.StatusNotFound, err)
				return
			}
			if err := s.service.DeleteIssue(r.Context(), issue.Identifier); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, map[string]interface{}{"deleted": true, "identifier": identifier})
		default:
			methodNotAllowed(w)
		}
		return
	}

	if len(parts) == 2 && r.Method == http.MethodGet && parts[1] == "execution" {
		issue, err := s.service.GetIssueByIdentifier(r.Context(), identifier)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		payload, err := s.issueExecutionPayload(issue)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, payload)
		return
	}

	if len(parts) != 2 || r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	switch parts[1] {
	case "permissions":
		var body struct {
			PermissionProfile string `json:"permission_profile"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		profile, err := kanban.ParsePermissionProfile(body.PermissionProfile)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		issue, err := s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		if err := s.store.UpdateIssuePermissionProfile(issue.ID, profile); err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		detail, err := s.service.GetIssueDetailByIdentifier(r.Context(), identifier)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, detail)
	case "approve-plan":
		var body struct {
			Note string `json:"note"`
		}
		if !decodeOptionalJSON(w, r, &body) {
			return
		}
		note := strings.TrimSpace(body.Note)
		issue, err := s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		if !issue.PlanApprovalPending || strings.TrimSpace(issue.PendingPlanMarkdown) == "" {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no pending plan approval"})
			return
		}
		response, err := s.approveIssuePlan(r.Context(), issue, note)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, response)
	case "request-plan-revision":
		var body struct {
			Note string `json:"note"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		note := strings.TrimSpace(body.Note)
		if note == "" {
			writeErrorStatus(w, http.StatusBadRequest, appserver.ErrInvalidInteractionResponse)
			return
		}
		issue, err := s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		if !issue.PlanApprovalPending || strings.TrimSpace(issue.PendingPlanMarkdown) == "" {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no pending plan approval"})
			return
		}
		response, err := s.requestIssuePlanRevision(r.Context(), issue, note)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, response)
	case "state":
		var body struct {
			State string `json:"state"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		detail, err := s.service.SetIssueState(r.Context(), identifier, body.State)
		if err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "identifier": identifier, "state": detail.State})
	case "blockers":
		var body struct {
			BlockedBy []string `json:"blocked_by"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		detail, err := s.service.UpdateIssue(r.Context(), identifier, map[string]interface{}{"blocked_by": body.BlockedBy})
		if err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "identifier": identifier, "blocked_by": detail.BlockedBy})
	case "retry":
		writeJSON(w, s.provider.RetryIssueNow(r.Context(), identifier))
	case "run-now":
		writeJSON(w, s.provider.RunRecurringIssueNow(r.Context(), identifier))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleIssueCommands(w http.ResponseWriter, r *http.Request, identifier string, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodPost:
		var body struct {
			Command string `json:"command"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		issue, err := s.service.GetIssueByIdentifier(r.Context(), identifier)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		command, err := s.submitIssueCommand(r.Context(), issue, body.Command)
		if err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, map[string]interface{}{
			"ok":      true,
			"issue":   identifier,
			"command": command,
		})
	case len(rest) == 1:
		commandID := strings.TrimSpace(rest[0])
		if commandID == "" {
			http.NotFound(w, r)
			return
		}
		issue, err := s.service.GetIssueByIdentifier(r.Context(), identifier)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				Command string `json:"command"`
			}
			if !decodeJSON(w, r, &body) {
				return
			}
			command, err := s.store.UpdateIssueAgentCommand(issue.ID, commandID, body.Command)
			if err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			writeJSON(w, map[string]interface{}{
				"ok":      true,
				"issue":   identifier,
				"command": command,
			})
		case http.MethodDelete:
			if err := s.store.DeleteIssueAgentCommand(issue.ID, commandID); err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			writeJSON(w, map[string]interface{}{
				"ok":         true,
				"deleted":    true,
				"issue":      identifier,
				"command_id": commandID,
			})
		default:
			methodNotAllowed(w)
		}
	case len(rest) == 2 && rest[1] == "steer" && r.Method == http.MethodPost:
		commandID := strings.TrimSpace(rest[0])
		if commandID == "" {
			http.NotFound(w, r)
			return
		}
		issue, err := s.service.GetIssueByIdentifier(r.Context(), identifier)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		command, err := s.store.SteerIssueAgentCommand(issue.ID, commandID)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		if s.provider != nil && (!issue.PlanApprovalPending || strings.TrimSpace(issue.PendingPlanRevisionMarkdown) != "" && issue.PendingPlanRevisionRequestedAt != nil) {
			_ = s.provider.RetryIssueNow(r.Context(), identifier)
		}
		writeJSON(w, map[string]interface{}{
			"ok":      true,
			"issue":   identifier,
			"command": command,
		})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleIssueAssets(w http.ResponseWriter, r *http.Request, identifier string, rest []string) {
	if len(rest) == 0 {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, kanban.MaxIssueAssetBytes+(1<<20))
		file, filename, err := readIssueAssetUpload(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		defer file.Close()

		asset, err := s.service.AttachIssueAsset(r.Context(), identifier, filename, file)
		if err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, asset)
		return
	}

	if len(rest) == 2 && rest[1] == "content" && r.Method == http.MethodGet {
		asset, path, err := s.service.GetIssueAssetContent(r.Context(), identifier, rest[0])
		if err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		file, err := os.Open(path)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", asset.ContentType)
		w.Header().Set("Content-Disposition", contentDispositionHeader(asset.Filename, asset.ContentType))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.FormatInt(asset.ByteSize, 10))
		http.ServeContent(w, r, asset.Filename, asset.UpdatedAt, file)
		return
	}

	if len(rest) == 1 && r.Method == http.MethodDelete {
		if err := s.service.DeleteIssueAsset(r.Context(), identifier, rest[0]); err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, map[string]interface{}{"deleted": true, "identifier": identifier, "asset_id": rest[0]})
		return
	}

	methodNotAllowed(w)
}

func (s *Server) submitIssueCommand(ctx context.Context, issue *kanban.Issue, command string) (*kanban.IssueAgentCommand, error) {
	if issue == nil {
		return nil, fmt.Errorf("issue is required")
	}
	status := kanban.IssueAgentCommandPending
	if issue.State == kanban.StateInProgress {
		unresolved, err := s.store.UnresolvedBlockersForIssue(issue.ID)
		if err != nil {
			return nil, err
		}
		if len(unresolved) > 0 {
			status = kanban.IssueAgentCommandWaitingForUnblock
		}
	} else {
		if _, err := s.service.SetIssueState(ctx, issue.Identifier, string(kanban.StateInProgress)); err != nil {
			if kanban.IsBlockedTransition(err) {
				status = kanban.IssueAgentCommandWaitingForUnblock
				if issue.State == kanban.StateBacklog {
					if _, readyErr := s.service.SetIssueState(ctx, issue.Identifier, string(kanban.StateReady)); readyErr != nil && !kanban.IsBlockedTransition(readyErr) {
						return nil, readyErr
					}
				}
			} else {
				return nil, err
			}
		}
	}
	eventPayload := map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      string(issue.WorkflowPhase),
	}
	return s.store.CreateIssueAgentCommandWithRuntimeEvent(issue.ID, command, status, "manual_command_submitted", eventPayload)
}

func (s *Server) handleIssueComments(w http.ResponseWriter, r *http.Request, identifier string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			comments, err := s.service.ListIssueComments(r.Context(), identifier)
			if err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			writeJSON(w, map[string]interface{}{"items": comments})
		case http.MethodPost:
			input, cleanup, err := readIssueCommentMultipart(w, r, "UI")
			defer cleanup()
			if err != nil {
				writeErrorStatus(w, http.StatusBadRequest, err)
				return
			}
			comment, err := s.service.CreateIssueCommentWithResult(r.Context(), identifier, input)
			if err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			writeJSONStatus(w, http.StatusCreated, comment)
		default:
			methodNotAllowed(w)
		}
		return
	}

	if len(rest) == 4 && rest[1] == "attachments" && rest[2] != "" && rest[3] == "content" && r.Method == http.MethodGet {
		content, err := s.service.GetIssueCommentAttachmentContent(r.Context(), identifier, rest[0], rest[2])
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		defer content.Content.Close()
		w.Header().Set("Content-Type", content.Attachment.ContentType)
		w.Header().Set("Content-Disposition", contentDispositionHeader(content.Attachment.Filename, content.Attachment.ContentType))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if content.Attachment.ByteSize > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(content.Attachment.ByteSize, 10))
		}
		_, _ = io.Copy(w, content.Content)
		return
	}

	if len(rest) != 1 {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodPatch:
		input, cleanup, err := readIssueCommentMultipart(w, r, "UI")
		defer cleanup()
		if err != nil {
			writeErrorStatus(w, http.StatusBadRequest, err)
			return
		}
		comment, err := s.service.UpdateIssueComment(r.Context(), identifier, rest[0], input)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, comment)
	case http.MethodDelete:
		if err := s.service.DeleteIssueComment(r.Context(), identifier, rest[0]); err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, map[string]interface{}{"deleted": true, "identifier": identifier, "comment_id": rest[0]})
	default:
		methodNotAllowed(w)
	}
}

func readIssueAssetUpload(r *http.Request) (io.ReadCloser, string, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, "", err
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", err
		}
		if part.FormName() != "file" {
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
			continue
		}
		filename := strings.TrimSpace(part.FileName())
		if filename == "" {
			_ = part.Close()
			return nil, "", fmt.Errorf("file is required")
		}
		return part, filename, nil
	}
	return nil, "", fmt.Errorf("file is required")
}

func readIssueCommentMultipart(w http.ResponseWriter, r *http.Request, source string) (providers.IssueCommentInput, func(), error) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxIssueCommentMultipartBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return providers.IssueCommentInput{}, func() {}, err
	}

	tempDir, err := os.MkdirTemp("", "maestro-comment-*")
	if err != nil {
		return providers.IssueCommentInput{}, func() {}, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	input := providers.IssueCommentInput{
		Author: kanban.IssueCommentAuthor{
			Type: "source",
			Name: source,
		},
	}
	var bodySet bool

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return providers.IssueCommentInput{}, func() {}, err
		}
		name := strings.TrimSpace(part.FormName())
		filename := strings.TrimSpace(part.FileName())
		switch {
		case name == "body":
			data, err := io.ReadAll(part)
			_ = part.Close()
			if err != nil {
				cleanup()
				return providers.IssueCommentInput{}, func() {}, err
			}
			value := string(data)
			input.Body = &value
			bodySet = true
		case name == "parent_comment_id":
			data, err := io.ReadAll(part)
			_ = part.Close()
			if err != nil {
				cleanup()
				return providers.IssueCommentInput{}, func() {}, err
			}
			input.ParentCommentID = strings.TrimSpace(string(data))
		case name == "remove_attachment_ids":
			data, err := io.ReadAll(part)
			_ = part.Close()
			if err != nil {
				cleanup()
				return providers.IssueCommentInput{}, func() {}, err
			}
			if value := strings.TrimSpace(string(data)); value != "" {
				input.RemoveAttachmentIDs = append(input.RemoveAttachmentIDs, value)
			}
		case name == "files" && filename != "":
			partDir, err := os.MkdirTemp(tempDir, "attachment-*")
			if err != nil {
				_ = part.Close()
				cleanup()
				return providers.IssueCommentInput{}, func() {}, err
			}
			tempPath := filepath.Join(partDir, filepath.Base(filename))
			file, err := os.Create(tempPath)
			if err != nil {
				_ = part.Close()
				cleanup()
				return providers.IssueCommentInput{}, func() {}, err
			}
			if _, err := io.Copy(file, part); err != nil {
				_ = file.Close()
				_ = part.Close()
				cleanup()
				return providers.IssueCommentInput{}, func() {}, err
			}
			_ = file.Close()
			_ = part.Close()
			input.Attachments = append(input.Attachments, providers.IssueCommentAttachment{
				Path:        tempPath,
				ContentType: strings.TrimSpace(part.Header.Get("Content-Type")),
			})
		default:
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
		}
	}

	if !bodySet {
		input.Body = nil
	}
	return input, cleanup, nil
}

const MaxIssueCommentMultipartBytes int64 = 256 * 1024 * 1024

func (s *Server) handleRuntimeEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	events, err := s.store.ListRuntimeEvents(int64(queryInt(r, "since", 0)), queryInt(r, "limit", 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]interface{}{"events": events})
}

func (s *Server) handleRuntimeSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	series, err := s.store.RuntimeSeries(queryInt(r, "hours", 24))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]interface{}{"series": series})
}

func (s *Server) handleInterrupts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, s.provider.PendingInterrupts())
}

func (s *Server) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/app/interrupts/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	interactionID, action, hasAction := strings.Cut(rest, "/")
	if interactionID == "" || strings.Contains(interactionID, "/") || !hasAction {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if action == "acknowledge" {
		err := s.provider.AcknowledgeInterrupt(r.Context(), interactionID)
		switch {
		case err == nil:
			writeJSONStatus(w, http.StatusAccepted, map[string]interface{}{
				"id":     interactionID,
				"status": "accepted",
			})
		case errors.Is(err, appserver.ErrPendingInteractionNotFound):
			writeErrorStatus(w, http.StatusNotFound, err)
		case errors.Is(err, appserver.ErrInvalidInteractionResponse):
			writeErrorStatus(w, http.StatusBadRequest, err)
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	if action != "respond" {
		http.NotFound(w, r)
		return
	}

	var body struct {
		Decision        string                 `json:"decision"`
		DecisionPayload map[string]interface{} `json:"decision_payload"`
		Answers         map[string][]string    `json:"answers"`
		Note            string                 `json:"note"`
		Action          string                 `json:"action"`
		Content         interface{}            `json:"content"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	snapshot := s.provider.PendingInterrupts()
	interaction, found := pendingInterruptByID(snapshot, interactionID)
	var err error
	note := strings.TrimSpace(body.Note)
	decision := strings.TrimSpace(body.Decision)
	responseAction := strings.ToLower(strings.TrimSpace(body.Action))
	hasDecisionPayload := len(body.DecisionPayload) > 0
	hasAnswers := len(body.Answers) > 0
	hasNote := note != ""
	hasContent := body.Content != nil
	isPlanApproval := isPlanApprovalInteraction(interaction)
	if !found {
		err = s.provider.RespondToInterrupt(r.Context(), interactionID, appserver.PendingInteractionResponse{
			Decision:        decision,
			DecisionPayload: body.DecisionPayload,
			Answers:         body.Answers,
			Note:            note,
			Action:          responseAction,
			Content:         body.Content,
		})
		switch {
		case err == nil:
			writeJSONStatus(w, http.StatusAccepted, map[string]interface{}{
				"id":     interactionID,
				"status": "accepted",
			})
		case errors.Is(err, appserver.ErrPendingInteractionNotFound):
			writeErrorStatus(w, http.StatusNotFound, err)
		case errors.Is(err, appserver.ErrPendingInteractionConflict):
			writeErrorStatus(w, http.StatusConflict, err)
		case errors.Is(err, appserver.ErrInvalidInteractionResponse):
			writeErrorStatus(w, http.StatusBadRequest, err)
		default:
			writeErrorStatus(w, http.StatusInternalServerError, err)
		}
		return
	}
	if interaction.Kind == appserver.PendingInteractionKindElicitation {
		switch responseAction {
		case "":
			if !hasContent {
				writeErrorStatus(w, http.StatusBadRequest, appserver.ErrInvalidInteractionResponse)
				return
			}
			responseAction = "accept"
		case "accept":
			if !hasContent {
				writeErrorStatus(w, http.StatusBadRequest, appserver.ErrInvalidInteractionResponse)
				return
			}
		case "decline", "cancel":
			body.Content = nil
			hasContent = false
		default:
			writeErrorStatus(w, http.StatusBadRequest, appserver.ErrInvalidInteractionResponse)
			return
		}
	}
	if isPlanApproval {
		if firstRespondableInterruptID(snapshot) != interactionID {
			writeErrorStatus(w, http.StatusConflict, appserver.ErrPendingInteractionConflict)
			return
		}
		switch {
		case decision == "" && !hasDecisionPayload:
			if !hasNote {
				writeErrorStatus(w, http.StatusBadRequest, appserver.ErrInvalidInteractionResponse)
				return
			}
			issue, err := s.issueForInterrupt(r.Context(), interaction)
			if err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			if _, err := s.requestIssuePlanRevision(r.Context(), issue, note); err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			writeJSONStatus(w, http.StatusAccepted, map[string]interface{}{
				"id":     interactionID,
				"status": "accepted",
			})
		case strings.EqualFold(decision, "approved"):
			issue, err := s.issueForInterrupt(r.Context(), interaction)
			if err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			response, err := s.approveIssuePlan(r.Context(), issue, note)
			if err != nil {
				writeErrorStatus(w, appErrorStatus(err), err)
				return
			}
			writeJSONStatus(w, http.StatusAccepted, map[string]interface{}{
				"id":       interactionID,
				"status":   "accepted",
				"dispatch": response["dispatch"],
			})
		default:
			writeErrorStatus(w, http.StatusBadRequest, appserver.ErrInvalidInteractionResponse)
		}
		return
	}
	if hasNote && decision == "" && responseAction == "" && !hasDecisionPayload && !hasAnswers && !hasContent && interruptNoteCommandAllowed(interaction) {
		issue, err := s.issueForInterrupt(r.Context(), interaction)
		if err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		if _, err := s.submitIssueCommand(r.Context(), issue, note); err != nil {
			writeErrorStatus(w, appErrorStatus(err), err)
			return
		}
		writeJSONStatus(w, http.StatusAccepted, map[string]interface{}{
			"id":     interactionID,
			"status": "accepted",
		})
		return
	}
	err = s.provider.RespondToInterrupt(r.Context(), interactionID, appserver.PendingInteractionResponse{
		Decision:        decision,
		DecisionPayload: body.DecisionPayload,
		Answers:         body.Answers,
		Note:            note,
		Action:          responseAction,
		Content:         body.Content,
	})
	switch {
	case err == nil:
		if hasNote && interruptNoteCommandAllowed(interaction) {
			// Best effort only: the interrupt response has already been accepted.
			if issue, noteErr := s.issueForInterrupt(r.Context(), interaction); noteErr == nil {
				_, _ = s.submitIssueCommand(r.Context(), issue, note)
			}
		}
		writeJSONStatus(w, http.StatusAccepted, map[string]interface{}{
			"id":     interactionID,
			"status": "accepted",
		})
	case errors.Is(err, appserver.ErrPendingInteractionNotFound):
		writeErrorStatus(w, http.StatusNotFound, err)
	case errors.Is(err, appserver.ErrPendingInteractionConflict):
		writeErrorStatus(w, http.StatusConflict, err)
	case errors.Is(err, appserver.ErrInvalidInteractionResponse):
		writeErrorStatus(w, http.StatusBadRequest, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

func pendingInterruptByID(snapshot appserver.PendingInteractionSnapshot, interactionID string) (*appserver.PendingInteraction, bool) {
	for i := range snapshot.Items {
		if snapshot.Items[i].ID != interactionID {
			continue
		}
		cloned := snapshot.Items[i].Clone()
		return &cloned, true
	}
	return nil, false
}

func firstRespondableInterruptID(snapshot appserver.PendingInteractionSnapshot) string {
	for i := range snapshot.Items {
		if snapshot.Items[i].Kind == appserver.PendingInteractionKindAlert {
			continue
		}
		if id := strings.TrimSpace(snapshot.Items[i].ID); id != "" {
			return id
		}
	}
	return ""
}

func isPlanApprovalInteraction(interaction *appserver.PendingInteraction) bool {
	if interaction == nil || interaction.Kind != appserver.PendingInteractionKindApproval || interaction.Approval == nil {
		return false
	}
	return strings.TrimSpace(interaction.Approval.Markdown) != ""
}

func interruptNoteCommandAllowed(interaction *appserver.PendingInteraction) bool {
	return interaction != nil && interaction.Kind != appserver.PendingInteractionKindElicitation
}

func (s *Server) issueForInterrupt(ctx context.Context, interaction *appserver.PendingInteraction) (*kanban.Issue, error) {
	if interaction == nil {
		return nil, fmt.Errorf("pending interaction is required")
	}
	if identifier := strings.TrimSpace(interaction.IssueIdentifier); identifier != "" {
		return s.service.GetIssueByIdentifier(ctx, identifier)
	}
	if strings.TrimSpace(interaction.IssueID) != "" {
		return s.store.GetIssue(interaction.IssueID)
	}
	return nil, fmt.Errorf("pending interaction is missing issue reference")
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, s.sessionsPayload())
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	updates, unsubscribe := observability.Subscribe()
	defer unsubscribe()
	lastSeq, _ := s.store.LatestChangeSeq()
	writeJSON := func(payload map[string]interface{}) error {
		if err := conn.SetWriteDeadline(time.Now().Add(dashboardWSWriteTimeout)); err != nil {
			return err
		}
		return conn.WriteJSON(payload)
	}
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(dashboardWSReadTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(dashboardWSReadTimeout))
	})

	if err := writeJSON(map[string]interface{}{
		"type": "connected",
		"at":   time.Now().UTC().Format(time.RFC3339),
		"seq":  lastSeq,
	}); err != nil {
		return
	}

	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	pingTicker := time.NewTicker(dashboardWSPingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-updates:
			seq, err := s.store.LatestChangeSeq()
			if err != nil {
				continue
			}
			runtimeOnly := seq <= lastSeq
			if seq > lastSeq {
				lastSeq = seq
			}
			if err := writeJSON(map[string]interface{}{
				"type":         "invalidate",
				"at":           time.Now().UTC().Format(time.RFC3339),
				"seq":          lastSeq,
				"runtime_only": runtimeOnly,
			}); err != nil {
				return
			}
		case <-pingTicker.C:
			if err := conn.SetWriteDeadline(time.Now().Add(dashboardWSWriteTimeout)); err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out interface{}) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, out interface{}) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		if err == io.EOF {
			return true
		}
		writeErrorStatus(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func (s *Server) issueExecutionPayload(issue *kanban.Issue) (map[string]interface{}, error) {
	return runtimeview.IssueExecutionPayload(s.store, s.provider, issue)
}

func (s *Server) planApprovalNoteCommandStatus(issue *kanban.Issue) (kanban.IssueAgentCommandStatus, error) {
	if issue == nil {
		return kanban.IssueAgentCommandPending, fmt.Errorf("issue is required")
	}
	unresolved, err := s.store.UnresolvedBlockersForIssue(issue.ID)
	if err != nil {
		return "", err
	}
	if len(unresolved) > 0 {
		return kanban.IssueAgentCommandWaitingForUnblock, nil
	}
	return kanban.IssueAgentCommandPending, nil
}

func (s *Server) approveIssuePlan(ctx context.Context, issue *kanban.Issue, note string) (map[string]interface{}, error) {
	if issue == nil {
		return nil, fmt.Errorf("issue is required")
	}
	if !issue.PlanApprovalPending || strings.TrimSpace(issue.PendingPlanMarkdown) == "" {
		return nil, kanban.ErrBlockedTransition
	}

	note = strings.TrimSpace(note)
	noteStatus := kanban.IssueAgentCommandPending
	var err error
	if note != "" {
		noteStatus, err = s.planApprovalNoteCommandStatus(issue)
		if err != nil {
			return nil, err
		}
	}

	approvedAt := time.Now().UTC()
	if _, err := s.store.ApproveIssuePlanWithNote(issue, approvedAt, note, noteStatus); err != nil {
		return nil, err
	}

	response := map[string]interface{}{
		"ok":       true,
		"dispatch": s.provider.RetryIssueNow(ctx, issue.Identifier),
	}
	if detail, err := s.service.GetIssueDetailByIdentifier(ctx, issue.Identifier); err == nil {
		response["issue"] = detail
	}
	return response, nil
}

func (s *Server) requestIssuePlanRevision(ctx context.Context, issue *kanban.Issue, note string) (map[string]interface{}, error) {
	if issue == nil {
		return nil, fmt.Errorf("issue is required")
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return nil, appserver.ErrInvalidInteractionResponse
	}
	if !issue.PlanApprovalPending || strings.TrimSpace(issue.PendingPlanMarkdown) == "" {
		return nil, kanban.ErrBlockedTransition
	}
	requestedAt := time.Now().UTC()
	if err := s.store.SetIssuePendingPlanRevision(issue.ID, note, requestedAt); err != nil {
		return nil, err
	}
	rollbackRevision := func(reason string, cause error) error {
		if clearErr := s.store.ClearIssuePendingPlanRevision(issue.ID, reason); clearErr != nil {
			return errors.Join(cause, fmt.Errorf("clear pending plan revision: %w", clearErr))
		}
		return cause
	}
	dispatch := s.provider.RetryIssueNow(ctx, issue.Identifier)
	if status, _ := dispatch["status"].(string); status == "error" {
		return nil, rollbackRevision("dispatch_failed", fmt.Errorf("%v", dispatch["error"]))
	}
	s.recordPlanRevisionRuntimeEvent(issue, "plan_revision_requested", requestedAt, note, dispatch)
	response := map[string]interface{}{
		"ok":       true,
		"dispatch": dispatch,
	}
	if detail, err := s.service.GetIssueDetailByIdentifier(ctx, issue.Identifier); err == nil {
		response["issue"] = detail
	}
	return response, nil
}

func (s *Server) recordPlanRevisionRuntimeEvent(issue *kanban.Issue, kind string, requestedAt time.Time, note string, dispatch map[string]interface{}) {
	if s.store == nil || issue == nil {
		return
	}
	payload := map[string]interface{}{
		"issue_id":     issue.ID,
		"identifier":   issue.Identifier,
		"title":        issue.Title,
		"phase":        string(issue.WorkflowPhase),
		"requested_at": requestedAt.UTC().Format(time.RFC3339),
		"markdown":     note,
	}
	if status, _ := dispatch["status"].(string); status != "" {
		payload["dispatch_status"] = status
	}
	if snapshot, err := s.store.GetIssueExecutionSession(issue.ID); err == nil && snapshot != nil {
		if snapshot.Attempt > 0 {
			payload["attempt"] = snapshot.Attempt
		}
		if strings.TrimSpace(snapshot.Phase) != "" {
			payload["phase"] = snapshot.Phase
		}
	}
	_ = s.store.AppendRuntimeEventOnly(kind, payload)
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func writeJSON(w http.ResponseWriter, payload interface{}) {
	writeJSONStatus(w, http.StatusOK, payload)
}

func writeJSONStatus(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeErrorStatus(w, status, err)
}

func appErrorStatus(err error) int {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return http.StatusBadRequest
	}
	if kanban.IsNotFound(err) {
		return http.StatusNotFound
	}
	if kanban.IsBlockedTransition(err) {
		return http.StatusConflict
	}
	if kanban.IsValidation(err) || providers.IsUnsupported(err) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func writeErrorStatus(w http.ResponseWriter, status int, err error) {
	writeJSONStatus(w, status, map[string]interface{}{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
}
