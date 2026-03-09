package dashboardapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/internal/runtimeview"
)

type Provider interface {
	observability.StatusProvider
	observability.SnapshotProvider
	observability.SessionProvider
	observability.EventProvider
	observability.RefreshProvider
	RetryIssueNow(identifier string) map[string]interface{}
}

type Server struct {
	store    *kanban.Store
	provider Provider
	upgrader websocket.Upgrader
}

func NewServer(store *kanban.Store, provider Provider) *Server {
	return &Server{
		store:    store,
		provider: provider,
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
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

	projects, err := s.store.ListProjectSummaries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	epics, err := s.store.ListEpicSummaries("")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	issues, total, err := s.store.ListIssueSummaries(kanban.IssueQuery{
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
			"status":        s.provider.Status(),
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
		projects, err := s.store.ListProjectSummaries()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]interface{}{"items": projects})
	case http.MethodPost:
		var body struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
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
		project, err := s.store.CreateProject(
			strings.TrimSpace(body.Name),
			strings.TrimSpace(body.Description),
			strings.TrimSpace(body.RepoPath),
			strings.TrimSpace(body.WorkflowPath),
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, project)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/app/projects/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		projectSummaries, err := s.store.ListProjectSummaries()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
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
		epics, err := s.store.ListEpicSummaries(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		issues, total, err := s.store.ListIssueSummaries(kanban.IssueQuery{
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
		if err := s.store.UpdateProject(
			id,
			strings.TrimSpace(body.Name),
			strings.TrimSpace(body.Description),
			strings.TrimSpace(body.RepoPath),
			strings.TrimSpace(body.WorkflowPath),
		); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		project, err := s.store.GetProject(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
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
		epics, err := s.store.ListEpicSummaries(projectID)
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
		epic, err := s.store.CreateEpic(strings.TrimSpace(body.ProjectID), strings.TrimSpace(body.Name), strings.TrimSpace(body.Description))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
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
		epicSummaries, err := s.store.ListEpicSummaries("")
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
		siblingEpics, err := s.store.ListEpicSummaries(epicSummary.ProjectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		issues, total, err := s.store.ListIssueSummaries(kanban.IssueQuery{
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
		if err := s.store.UpdateEpic(id, strings.TrimSpace(body.ProjectID), strings.TrimSpace(body.Name), strings.TrimSpace(body.Description)); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		epic, err := s.store.GetEpic(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, epic)
	case http.MethodDelete:
		if err := s.store.DeleteEpic(id); err != nil {
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
			Search:    r.URL.Query().Get("search"),
			Sort:      r.URL.Query().Get("sort"),
			Limit:     queryInt(r, "limit", 200),
			Offset:    queryInt(r, "offset", 0),
		}
		issues, total, err := s.store.ListIssueSummaries(query)
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
			Priority    int      `json:"priority"`
			Labels      []string `json:"labels"`
			State       string   `json:"state"`
			BlockedBy   []string `json:"blocked_by"`
			BranchName  string   `json:"branch_name"`
			PRNumber    int      `json:"pr_number"`
			PRURL       string   `json:"pr_url"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		issue, err := s.store.CreateIssue(body.ProjectID, body.EpicID, strings.TrimSpace(body.Title), strings.TrimSpace(body.Description), body.Priority, body.Labels)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		updates := map[string]interface{}{
			"blocked_by":  body.BlockedBy,
			"branch_name": body.BranchName,
			"pr_number":   body.PRNumber,
			"pr_url":      body.PRURL,
		}
		if err := s.store.UpdateIssue(issue.ID, updates); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if body.State != "" && body.State != string(kanban.StateBacklog) {
			if err := s.store.UpdateIssueState(issue.ID, kanban.State(body.State)); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
		}
		detail, err := s.store.GetIssueDetailByIdentifier(issue.Identifier)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
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

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			detail, err := s.store.GetIssueDetailByIdentifier(identifier)
			if err != nil {
				status := http.StatusInternalServerError
				if err == sql.ErrNoRows {
					status = http.StatusNotFound
				}
				writeErrorStatus(w, status, err)
				return
			}
			writeJSON(w, detail)
		case http.MethodPatch:
			issue, err := s.store.GetIssueByIdentifier(identifier)
			if err != nil {
				writeErrorStatus(w, http.StatusNotFound, err)
				return
			}
			var body struct {
				ProjectID   string   `json:"project_id"`
				EpicID      string   `json:"epic_id"`
				Title       string   `json:"title"`
				Description string   `json:"description"`
				Priority    int      `json:"priority"`
				Labels      []string `json:"labels"`
				BlockedBy   []string `json:"blocked_by"`
				BranchName  string   `json:"branch_name"`
				PRNumber    int      `json:"pr_number"`
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
				"pr_number":   body.PRNumber,
				"pr_url":      body.PRURL,
			}
			if err := s.store.UpdateIssue(issue.ID, updates); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			detail, err := s.store.GetIssueDetailByIdentifier(identifier)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, detail)
		case http.MethodDelete:
			issue, err := s.store.GetIssueByIdentifier(identifier)
			if err != nil {
				writeErrorStatus(w, http.StatusNotFound, err)
				return
			}
			if err := s.store.DeleteIssue(issue.ID); err != nil {
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
		issue, err := s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			status := http.StatusInternalServerError
			if err == sql.ErrNoRows {
				status = http.StatusNotFound
			}
			writeErrorStatus(w, status, err)
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
		issue, err := s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			writeErrorStatus(w, http.StatusNotFound, err)
			return
		}
		if err := s.store.UpdateIssueState(issue.ID, kanban.State(body.State)); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "identifier": identifier, "state": body.State})
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
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "identifier": identifier, "blocked_by": body.BlockedBy})
	case "retry":
		writeJSON(w, s.provider.RetryIssueNow(identifier))
	default:
		http.NotFound(w, r)
	}
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
	writeJSON(w, s.provider.LiveSessions())
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	lastSeq, _ := s.store.LatestChangeSeq()

	_ = conn.WriteJSON(map[string]interface{}{
		"type": "connected",
		"at":   time.Now().UTC().Format(time.RFC3339),
		"seq":  lastSeq,
	})

	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(750 * time.Millisecond):
			seq, err := s.store.LatestChangeSeq()
			if err != nil || seq <= lastSeq {
				continue
			}
			lastSeq = seq
			if err := conn.WriteJSON(map[string]interface{}{
				"type": "invalidate",
				"at":   time.Now().UTC().Format(time.RFC3339),
				"seq":  seq,
			}); err != nil {
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

func writeErrorStatus(w http.ResponseWriter, status int, err error) {
	writeJSONStatus(w, status, map[string]interface{}{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
}
