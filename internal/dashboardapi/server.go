package dashboardapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

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
	RetryIssueNow(identifier string) map[string]interface{}
	RunRecurringIssueNow(identifier string) map[string]interface{}
	RequestProjectRefresh(projectID string) map[string]interface{}
	StopProjectRuns(projectID string) map[string]interface{}
}

type Server struct {
	store    *kanban.Store
	service  *providers.Service
	provider Provider
	upgrader websocket.Upgrader
}

const (
	dashboardWSReadTimeout  = 60 * time.Second
	dashboardWSWriteTimeout = 5 * time.Second
	dashboardWSPingInterval = 30 * time.Second
)

func NewServer(store *kanban.Store, provider Provider) *Server {
	return &Server{
		store:    store,
		service:  providers.NewService(store),
		provider: provider,
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

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/app/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/v1/app/projects", s.handleProjects)
	mux.HandleFunc("/api/v1/app/projects/", s.handleProject)
	mux.HandleFunc("/api/v1/app/epics", s.handleEpics)
	mux.HandleFunc("/api/v1/app/epics/", s.handleEpic)
	mux.HandleFunc("/api/v1/app/issues", s.handleIssues)
	mux.HandleFunc("/api/v1/app/issues/", s.handleIssue)
	mux.HandleFunc("/api/v1/app/runtime/events", s.handleRuntimeEvents)
	mux.HandleFunc("/api/v1/app/runtime/series", s.handleRuntimeSeries)
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
			Name               string                 `json:"name"`
			Description        string                 `json:"description"`
			RepoPath           string                 `json:"repo_path"`
			WorkflowPath       string                 `json:"workflow_path"`
			ProviderKind       string                 `json:"provider_kind"`
			ProviderProjectRef string                 `json:"provider_project_ref"`
			ProviderConfig     map[string]interface{} `json:"provider_config"`
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
			strings.TrimSpace(body.ProviderKind),
			strings.TrimSpace(body.ProviderProjectRef),
			body.ProviderConfig,
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
			Name               string                 `json:"name"`
			Description        string                 `json:"description"`
			RepoPath           string                 `json:"repo_path"`
			WorkflowPath       string                 `json:"workflow_path"`
			ProviderKind       string                 `json:"provider_kind"`
			ProviderProjectRef string                 `json:"provider_project_ref"`
			ProviderConfig     map[string]interface{} `json:"provider_config"`
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
			strings.TrimSpace(body.ProviderKind),
			strings.TrimSpace(body.ProviderProjectRef),
			body.ProviderConfig,
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
			writeError(w, http.StatusInternalServerError, err)
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
			writeError(w, http.StatusInternalServerError, err)
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

	if len(parts) >= 2 && parts[1] == "images" {
		s.handleIssueImages(w, r, identifier, parts[2:])
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
				BlockedBy   []string `json:"blocked_by"`
				BranchName  string   `json:"branch_name"`
				PRURL       string   `json:"pr_url"`
			}
			if !decodeJSON(w, r, &body) {
				return
			}
			updates := map[string]interface{}{
				"project_id":  body.ProjectID,
				"epic_id":     body.EpicID,
				"title":       body.Title,
				"description": body.Description,
				"priority":    body.Priority,
				"labels":      body.Labels,
				"blocked_by":  body.BlockedBy,
				"branch_name": body.BranchName,
				"pr_url":      body.PRURL,
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
		issue, err := s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			writeErrorStatus(w, http.StatusNotFound, err)
			return
		}
		if err := s.store.UpdateIssue(issue.ID, map[string]interface{}{"blocked_by": body.BlockedBy}); err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "identifier": identifier, "blocked_by": body.BlockedBy})
	case "retry":
		writeJSON(w, s.provider.RetryIssueNow(identifier))
	case "run-now":
		writeJSON(w, s.provider.RunRecurringIssueNow(identifier))
	case "commands":
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
		status := kanban.IssueAgentCommandPending
		if issue.State == kanban.StateInProgress {
			unresolved, err := s.store.UnresolvedBlockersForIssue(issue.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			if len(unresolved) > 0 {
				status = kanban.IssueAgentCommandWaitingForUnblock
			}
		} else {
			if _, err := s.service.SetIssueState(r.Context(), identifier, string(kanban.StateInProgress)); err != nil {
				if kanban.IsBlockedTransition(err) {
					status = kanban.IssueAgentCommandWaitingForUnblock
				} else {
					writeError(w, appErrorStatus(err), err)
					return
				}
			}
		}
		eventPayload := map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      string(issue.WorkflowPhase),
		}
		command, err := s.store.CreateIssueAgentCommandWithRuntimeEvent(issue.ID, body.Command, status, "manual_command_submitted", eventPayload)
		if err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, map[string]interface{}{
			"ok":      true,
			"issue":   identifier,
			"command": command,
		})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleIssueImages(w http.ResponseWriter, r *http.Request, identifier string, rest []string) {
	if len(rest) == 0 {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, kanban.MaxIssueImageBytes+(1<<20))
		file, filename, err := readIssueImageUpload(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		defer file.Close()

		image, err := s.service.AttachIssueImage(r.Context(), identifier, filename, file)
		if err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, image)
		return
	}

	if len(rest) == 2 && rest[1] == "content" && r.Method == http.MethodGet {
		image, path, err := s.service.GetIssueImageContent(r.Context(), identifier, rest[0])
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
		w.Header().Set("Content-Type", image.ContentType)
		w.Header().Set("Content-Length", strconv.FormatInt(image.ByteSize, 10))
		http.ServeContent(w, r, image.Filename, image.UpdatedAt, file)
		return
	}

	if len(rest) == 1 && r.Method == http.MethodDelete {
		if err := s.service.DeleteIssueImage(r.Context(), identifier, rest[0]); err != nil {
			writeError(w, appErrorStatus(err), err)
			return
		}
		writeJSON(w, map[string]interface{}{"deleted": true, "identifier": identifier, "image_id": rest[0]})
		return
	}

	methodNotAllowed(w)
}

func readIssueImageUpload(r *http.Request) (io.ReadCloser, string, error) {
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
			if err != nil || seq <= lastSeq {
				continue
			}
			lastSeq = seq
			if err := writeJSON(map[string]interface{}{
				"type": "invalidate",
				"at":   time.Now().UTC().Format(time.RFC3339),
				"seq":  seq,
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

func (s *Server) issueExecutionPayload(issue *kanban.Issue) (map[string]interface{}, error) {
	return runtimeview.IssueExecutionPayload(s.store, s.provider, issue)
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
