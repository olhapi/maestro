package mcp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	mcpapi "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/internal/providers"
	"github.com/olhapi/maestro/internal/runtimeview"
)

type RuntimeProvider interface {
	observability.StatusProvider
	observability.SnapshotProvider
	observability.SessionProvider
	RequestProjectRefresh(projectID string) map[string]interface{}
	StopProjectRuns(projectID string) map[string]interface{}
	RetryIssueNow(ctx context.Context, identifier string) map[string]interface{}
	RunRecurringIssueNow(ctx context.Context, identifier string) map[string]interface{}
	PendingInterruptForIssue(issueID, identifier string) (*agentruntime.PendingInteraction, bool)
}

// Server implements the MCP server for the kanban board
// and optional extension tools.
type Server struct {
	store      *kanban.Store
	service    *providers.Service
	provider   RuntimeProvider
	server     *mcpserver.MCPServer
	tools      []mcpapi.Tool
	extensions *extensions.Registry
	instanceID string
}

const (
	mcpPaginationDefaultLimit = 200
	mcpPaginationMaxLimit     = 500
)

// NewServer creates a new MCP server.
func NewServer(store *kanban.Store) *Server {
	return NewServerWithProvider(store, nil)
}

// NewServerWithProvider creates a new MCP server with an optional runtime provider.
func NewServerWithProvider(store *kanban.Store, provider RuntimeProvider) *Server {
	return NewServerWithRegistry(store, provider, nil)
}

// NewServerWithExtensions creates a new MCP server and optionally loads extension tools from JSON file.
func NewServerWithExtensions(store *kanban.Store, provider RuntimeProvider, extensionsFile string) (*Server, error) {
	registry, err := extensions.LoadFile(extensionsFile)
	if err != nil {
		return nil, err
	}
	return NewServerWithRegistry(store, provider, registry), nil
}

func NewServerWithRegistry(store *kanban.Store, provider RuntimeProvider, registry *extensions.Registry) *Server {
	if registry == nil {
		registry = extensions.EmptyRegistry()
	}
	s := &Server{
		store:      store,
		service:    providers.NewService(store),
		provider:   provider,
		server:     mcpserver.NewMCPServer("maestro", "1.0.0", mcpserver.WithToolCapabilities(false)),
		extensions: registry,
		instanceID: generateServerInstanceID(),
	}

	s.registerTools()
	return s
}

func (s *Server) registerTools() {
	s.tools = []mcpapi.Tool{
		objectTool("server_info", "Get Maestro MCP server identity and store metadata", nil),
		objectTool("create_project", "Create a new project", map[string]interface{}{
			"name":          stringProperty("Project name"),
			"description":   stringProperty("Project description"),
			"repo_path":     stringProperty("Absolute path to the repo this project orchestrates"),
			"workflow_path": stringProperty("Optional workflow path override"),
		}),
		objectTool("update_project", "Update an existing project", map[string]interface{}{
			"id":            stringProperty("Project ID"),
			"name":          stringProperty("Project name"),
			"description":   stringProperty("Project description"),
			"repo_path":     stringProperty("Absolute path to the repo this project orchestrates"),
			"workflow_path": stringProperty("Optional workflow path override"),
		}),
		objectTool("list_projects", "List projects with pagination; use pagination.next_request to continue", map[string]interface{}{
			"limit":  numberProperty("Maximum projects to return"),
			"offset": numberProperty("Number of projects to skip"),
		}),
		objectTool("delete_project", "Delete a project", map[string]interface{}{
			"id": stringProperty("Project ID"),
		}),
		objectTool("create_epic", "Create a new epic within a project", map[string]interface{}{
			"project_id":  stringProperty("Project ID"),
			"name":        stringProperty("Epic name"),
			"description": stringProperty("Epic description"),
		}),
		objectTool("update_epic", "Update an existing epic", map[string]interface{}{
			"id":          stringProperty("Epic ID"),
			"project_id":  stringProperty("Project ID"),
			"name":        stringProperty("Epic name"),
			"description": stringProperty("Epic description"),
		}),
		objectTool("list_epics", "List epics, optionally filtered by project, with pagination; use pagination.next_request to continue", map[string]interface{}{
			"project_id": stringProperty("Filter by project ID"),
			"limit":      numberProperty("Maximum epics to return"),
			"offset":     numberProperty("Number of epics to skip"),
		}),
		objectTool("delete_epic", "Delete an epic", map[string]interface{}{
			"id": stringProperty("Epic ID"),
		}),
		objectTool("create_issue", "Create a new issue", map[string]interface{}{
			"title":       stringProperty("Issue title"),
			"description": stringProperty("Issue description"),
			"project_id":  stringProperty("Project ID"),
			"epic_id":     stringProperty("Epic ID"),
			"issue_type":  stringProperty("Issue type: standard or recurring"),
			"cron":        stringProperty("Cron schedule for recurring issues"),
			"enabled":     booleanProperty("Enable recurring scheduling"),
			"priority":    numberProperty("Priority (lower = higher)"),
			"labels":      stringArrayProperty("Issue labels"),
			"state":       stringProperty("Initial state: backlog, ready, in_progress, in_review, done, cancelled"),
			"blocked_by":  stringArrayProperty("Issue identifiers that block this issue"),
			"branch_name": stringProperty("Branch name"),
			"pr_url":      stringProperty("Pull request URL"),
		}),
		objectTool("get_issue", "Get an issue by ID or identifier (for example PROJ-123)", map[string]interface{}{
			"identifier": stringProperty("Issue ID or identifier"),
		}),
		objectTool("list_issue_comments", "List comments for an issue with pagination over root threads; use pagination.next_request to continue", map[string]interface{}{
			"identifier": stringProperty("Issue ID or identifier"),
			"limit":      numberProperty("Maximum top-level comment threads to return"),
			"offset":     numberProperty("Number of top-level comment threads to skip"),
		}),
		objectTool("list_issues", "List issues with filters, search, sort, and pagination; use pagination.next_request to continue", map[string]interface{}{
			"project_id": stringProperty("Filter by project ID"),
			"epic_id":    stringProperty("Filter by epic ID"),
			"state":      stringProperty("Filter by state: backlog, ready, in_progress, in_review, done, cancelled"),
			"issue_type": stringProperty("Filter by issue type: standard or recurring"),
			"search":     stringProperty("Search identifier, title, or description"),
			"sort":       stringProperty("Sort order: updated_desc, created_asc, priority_asc, identifier_asc, state_asc"),
			"limit":      numberProperty("Maximum issues to return"),
			"offset":     numberProperty("Number of issues to skip"),
		}),
		objectTool("update_issue", "Update an issue", map[string]interface{}{
			"identifier":         stringProperty("Issue ID or identifier"),
			"project_id":         stringProperty("Project ID"),
			"epic_id":            stringProperty("Epic ID"),
			"title":              stringProperty("New title"),
			"description":        stringProperty("New description"),
			"permission_profile": stringProperty("Issue permission profile: default, full-access, plan-then-full-access"),
			"issue_type":         stringProperty("Issue type: standard or recurring"),
			"cron":               stringProperty("Cron schedule for recurring issues"),
			"enabled":            booleanProperty("Enable recurring scheduling"),
			"priority":           numberProperty("New priority"),
			"labels":             stringArrayProperty("New labels"),
			"blocked_by":         stringArrayProperty("Issue identifiers that block this issue"),
			"branch_name":        stringProperty("Branch name"),
			"pr_url":             stringProperty("Pull request URL"),
		}),
		objectTool("attach_issue_asset", "Attach a local asset to an issue from a file path", map[string]interface{}{
			"identifier": stringProperty("Issue ID or identifier"),
			"path":       stringProperty("Absolute or relative local file path"),
		}),
		objectTool("create_issue_comment", "Create a comment on an issue", map[string]interface{}{
			"identifier":        stringProperty("Issue ID or identifier"),
			"body":              stringProperty("Comment body"),
			"parent_comment_id": stringProperty("Parent comment ID"),
			"attachment_paths":  stringArrayProperty("Attachment file paths"),
		}),
		objectTool("update_issue_comment", "Update an existing issue comment", map[string]interface{}{
			"identifier":            stringProperty("Issue ID or identifier"),
			"comment_id":            stringProperty("Comment ID"),
			"body":                  stringProperty("Updated comment body"),
			"attachment_paths":      stringArrayProperty("Attachment file paths"),
			"remove_attachment_ids": stringArrayProperty("Attachment IDs to remove"),
		}),
		objectTool("delete_issue_comment", "Delete an issue comment", map[string]interface{}{
			"identifier": stringProperty("Issue ID or identifier"),
			"comment_id": stringProperty("Comment ID"),
		}),
		objectTool("delete_issue_asset", "Delete an attached issue asset", map[string]interface{}{
			"identifier": stringProperty("Issue ID or identifier"),
			"asset_id":   stringProperty("Issue asset ID"),
		}),
		objectTool("set_issue_state", "Change an issue state", map[string]interface{}{
			"identifier": stringProperty("Issue ID or identifier"),
			"state":      stringProperty("New state: backlog, ready, in_progress, in_review, done, cancelled"),
		}),
		objectTool("set_issue_workflow_phase", "Change an issue workflow phase", map[string]interface{}{
			"identifier":     stringProperty("Issue ID or identifier"),
			"workflow_phase": stringProperty("New workflow phase: implementation, review, done, complete"),
		}),
		objectTool("delete_issue", "Delete an issue", map[string]interface{}{
			"identifier": stringProperty("Issue ID or identifier"),
		}),
		objectTool("run_project", "Request live orchestration for a project", map[string]interface{}{
			"id": stringProperty("Project ID"),
		}),
		objectTool("stop_project", "Stop live runs for a project", map[string]interface{}{
			"id": stringProperty("Project ID"),
		}),
		objectTool("get_issue_execution", "Get execution details for a single issue", map[string]interface{}{
			"identifier": stringProperty("Issue ID or identifier"),
		}),
		objectTool("retry_issue", "Request an immediate retry for an issue", map[string]interface{}{
			"identifier": stringProperty("Issue identifier"),
		}),
		objectTool("run_issue_now", "Trigger a recurring issue immediately", map[string]interface{}{
			"identifier": stringProperty("Recurring issue identifier"),
		}),
		objectTool("board_overview", "Get a kanban board overview showing issue counts by state", map[string]interface{}{
			"project_id": stringProperty("Filter by project ID"),
		}),
		objectTool("set_blockers", "Set blockers for an issue", map[string]interface{}{
			"identifier": stringProperty("Issue ID or identifier"),
			"blocked_by": stringArrayProperty("List of issue identifiers that block this issue"),
		}),
		objectTool("list_runtime_events", "List persisted runtime events", map[string]interface{}{
			"since": numberProperty("Only return events with seq greater than this value"),
			"limit": numberProperty("Maximum events to return"),
		}),
		objectTool("get_runtime_snapshot", "Get the live Maestro runtime snapshot", nil),
		objectTool("list_sessions", "List live Maestro sessions or fetch one issue session", map[string]interface{}{
			"identifier": stringProperty("Issue identifier to fetch a single live session"),
		}),
	}

	for _, spec := range s.extensions.Specs() {
		s.tools = append(s.tools, mcpapi.Tool{
			Name:        asString(spec["name"]),
			Description: asString(spec["description"]),
			InputSchema: extensionToolInputSchema(spec),
		})
	}

	for _, tool := range s.tools {
		tool := tool
		s.server.AddTool(tool, func(ctx context.Context, request mcpapi.CallToolRequest) (*mcpapi.CallToolResult, error) {
			args := map[string]interface{}{}
			if request.Params.Arguments != nil {
				if typed, ok := request.Params.Arguments.(map[string]interface{}); ok {
					args = typed
				}
			}
			return s.handleCallTool(ctx, tool.Name, args)
		})
	}
}

// handleCallTool routes tool calls to appropriate handlers.
func (s *Server) handleCallTool(ctx context.Context, name string, args map[string]interface{}) (result *mcpapi.CallToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = s.toolError(name, fmt.Sprintf("panic recovered: %v", recovered))
			err = nil
		}
	}()

	switch name {
	case "server_info":
		return s.handleServerInfo(ctx, args)
	case "create_project":
		return s.handleCreateProject(ctx, args)
	case "update_project":
		return s.handleUpdateProject(ctx, args)
	case "list_projects":
		return s.handleListProjects(ctx, args)
	case "delete_project":
		return s.handleDeleteProject(ctx, args)
	case "create_epic":
		return s.handleCreateEpic(ctx, args)
	case "update_epic":
		return s.handleUpdateEpic(ctx, args)
	case "list_epics":
		return s.handleListEpics(ctx, args)
	case "delete_epic":
		return s.handleDeleteEpic(ctx, args)
	case "create_issue":
		return s.handleCreateIssue(ctx, args)
	case "get_issue":
		return s.handleGetIssue(ctx, args)
	case "list_issue_comments":
		return s.handleListIssueComments(ctx, args)
	case "list_issues":
		return s.handleListIssues(ctx, args)
	case "update_issue":
		return s.handleUpdateIssue(ctx, args)
	case "attach_issue_asset":
		return s.handleAttachIssueAsset(ctx, args)
	case "create_issue_comment":
		return s.handleCreateIssueComment(ctx, args)
	case "update_issue_comment":
		return s.handleUpdateIssueComment(ctx, args)
	case "delete_issue_comment":
		return s.handleDeleteIssueComment(ctx, args)
	case "delete_issue_asset":
		return s.handleDeleteIssueAsset(ctx, args)
	case "set_issue_state":
		return s.handleSetIssueState(ctx, args)
	case "set_issue_workflow_phase":
		return s.handleSetIssueWorkflowPhase(ctx, args)
	case "delete_issue":
		return s.handleDeleteIssue(ctx, args)
	case "run_project":
		return s.handleRunProject(ctx, args)
	case "stop_project":
		return s.handleStopProject(ctx, args)
	case "get_issue_execution":
		return s.handleGetIssueExecution(ctx, args)
	case "retry_issue":
		return s.handleRetryIssue(ctx, args)
	case "run_issue_now":
		return s.handleRunIssueNow(ctx, args)
	case "board_overview":
		return s.handleBoardOverview(ctx, args)
	case "set_blockers":
		return s.handleSetBlockers(ctx, args)
	case "list_runtime_events":
		return s.handleListRuntimeEvents(ctx, args)
	case "get_runtime_snapshot":
		return s.handleGetRuntimeSnapshot(ctx, args)
	case "list_sessions":
		return s.handleListSessions(ctx, args)
	default:
		for _, toolName := range s.extensions.Names() {
			if toolName == name {
				return s.handleExtensionTool(ctx, name, args)
			}
		}
		return s.toolError(name, fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

func generateServerInstanceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "mcp_" + hex.EncodeToString(b)
}

func (s *Server) handleExtensionTool(ctx context.Context, name string, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	if s.extensions == nil {
		return s.toolError(name, fmt.Sprintf("unknown extension tool: %s", name)), nil
	}
	out, err := s.extensions.Execute(ctx, name, args)
	if err != nil {
		return s.toolError(name, err.Error()), nil
	}
	return s.toolResult(name, map[string]interface{}{"output": out}), nil
}

// ServeStdio runs the MCP server over stdin/stdout.
func (s *Server) ServeStdio() error {
	return mcpserver.ServeStdio(s.server)
}

// StreamableHTTPHandler exposes the MCP server over Streamable HTTP.
func (s *Server) StreamableHTTPHandler() http.Handler {
	return mcpserver.NewStreamableHTTPServer(s.server)
}

func (s *Server) handleServerInfo(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	projects, err := s.store.ListProjects()
	if err != nil {
		return s.toolError("server_info", fmt.Sprintf("Failed to list projects: %v", err)), nil
	}
	issues, err := s.store.ListIssues(nil)
	if err != nil {
		return s.toolError("server_info", fmt.Sprintf("Failed to list issues: %v", err)), nil
	}
	return s.toolResult("server_info", map[string]interface{}{
		"project_count":     len(projects),
		"issue_count":       len(issues),
		"runtime_available": s.provider != nil,
	}), nil
}

func (s *Server) handleCreateProject(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	repoPath := asString(args["repo_path"])
	if err := s.validateScopedRepoPath(repoPath); err != nil {
		return s.toolError("create_project", err.Error()), nil
	}
	runtimeName := strings.TrimSpace(asString(args["runtime_name"]))
	createArgs := []string{}
	if runtimeName != "" {
		createArgs = append(createArgs, runtimeName)
	}
	project, err := s.service.CreateProject(
		ctx,
		asString(args["name"]),
		asString(args["description"]),
		repoPath,
		asString(args["workflow_path"]),
		kanban.ProviderKindKanban,
		"",
		nil,
		createArgs...,
	)
	if err != nil {
		return s.toolError("create_project", fmt.Sprintf("Failed to create project: %v", err)), nil
	}
	s.decorateProject(project)
	return s.toolResult("create_project", project), nil
}

func (s *Server) handleUpdateProject(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	id := asString(args["id"])
	repoPath := asString(args["repo_path"])
	if strings.TrimSpace(repoPath) == "" {
		return s.toolError("update_project", "repo_path is required"), nil
	}
	if err := s.validateScopedRepoPath(repoPath); err != nil {
		return s.toolError("update_project", err.Error()), nil
	}
	runtimeName := strings.TrimSpace(asString(args["runtime_name"]))
	updateArgs := []string{}
	if runtimeName != "" {
		updateArgs = append(updateArgs, runtimeName)
	}
	if err := s.service.UpdateProject(ctx, id, asString(args["name"]), asString(args["description"]), repoPath, asString(args["workflow_path"]), kanban.ProviderKindKanban, "", nil, updateArgs...); err != nil {
		return s.toolError("update_project", fmt.Sprintf("Failed to update project: %v", err)), nil
	}
	project, err := s.store.GetProject(id)
	if err != nil {
		return s.toolError("update_project", fmt.Sprintf("Failed to reload project: %v", err)), nil
	}
	s.decorateProject(project)
	return s.toolResult("update_project", project), nil
}

func (s *Server) handleListProjects(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	projects, err := s.service.ListProjectSummaries()
	if err != nil {
		return s.toolError("list_projects", fmt.Sprintf("Failed to list projects: %v", err)), nil
	}
	limit, offset := normalizePaginationArgs(args)
	page, total, _ := paginateItems(projects, limit, offset)
	s.decorateProjectSummaries(page)
	return s.toolResult("list_projects", map[string]interface{}{
		"items":      page,
		"total":      total,
		"limit":      limit,
		"offset":     offset,
		"pagination": s.paginationPayload("list_projects", nil, total, limit, offset, len(page)),
	}), nil
}

func (s *Server) handleDeleteProject(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	id := asString(args["id"])
	if err := s.store.DeleteProject(id); err != nil {
		return s.toolError("delete_project", fmt.Sprintf("Failed to delete project: %v", err)), nil
	}
	return s.toolResult("delete_project", map[string]interface{}{"id": id}), nil
}

func (s *Server) handleCreateEpic(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	epic, err := s.service.CreateEpic(asString(args["project_id"]), asString(args["name"]), asString(args["description"]))
	if err != nil {
		return s.toolError("create_epic", fmt.Sprintf("Failed to create epic: %v", err)), nil
	}
	return s.toolResult("create_epic", epic), nil
}

func (s *Server) handleUpdateEpic(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	id := asString(args["id"])
	if err := s.service.UpdateEpic(id, asString(args["project_id"]), asString(args["name"]), asString(args["description"])); err != nil {
		return s.toolError("update_epic", fmt.Sprintf("Failed to update epic: %v", err)), nil
	}
	epic, err := s.store.GetEpic(id)
	if err != nil {
		return s.toolError("update_epic", fmt.Sprintf("Failed to reload epic: %v", err)), nil
	}
	return s.toolResult("update_epic", epic), nil
}

func (s *Server) handleListEpics(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	projectID := asString(args["project_id"])
	epics, err := s.service.ListEpicSummaries(projectID)
	if err != nil {
		return s.toolError("list_epics", fmt.Sprintf("Failed to list epics: %v", err)), nil
	}
	limit, offset := normalizePaginationArgs(args)
	page, total, _ := paginateItems(epics, limit, offset)
	baseArgs := map[string]interface{}{}
	if strings.TrimSpace(projectID) != "" {
		baseArgs["project_id"] = projectID
	}
	return s.toolResult("list_epics", map[string]interface{}{
		"items":      page,
		"total":      total,
		"limit":      limit,
		"offset":     offset,
		"pagination": s.paginationPayload("list_epics", baseArgs, total, limit, offset, len(page)),
	}), nil
}

func (s *Server) handleDeleteEpic(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	id := asString(args["id"])
	if err := s.service.DeleteEpic(id); err != nil {
		return s.toolError("delete_epic", fmt.Sprintf("Failed to delete epic: %v", err)), nil
	}
	return s.toolResult("delete_epic", map[string]interface{}{"id": id}), nil
}

func (s *Server) handleCreateIssue(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	enabled, _ := boolPointerArg(args, "enabled")
	detail, err := s.service.CreateIssue(ctx, providers.IssueCreateInput{
		ProjectID:   asString(args["project_id"]),
		EpicID:      asString(args["epic_id"]),
		Title:       asString(args["title"]),
		Description: asString(args["description"]),
		IssueType:   kanban.IssueType(asString(args["issue_type"])),
		Cron:        asString(args["cron"]),
		Enabled:     enabled,
		Priority:    intArg(args, "priority", 0),
		Labels:      stringListArg(args, "labels"),
		RuntimeName: strings.TrimSpace(asString(args["runtime_name"])),
		State:       asString(args["state"]),
		BlockedBy:   stringListArg(args, "blocked_by"),
		BranchName:  asString(args["branch_name"]),
		PRURL:       asString(args["pr_url"]),
	})
	if err != nil {
		return s.toolError("create_issue", fmt.Sprintf("Failed to create issue: %v", err)), nil
	}
	return s.toolResult("create_issue", detail), nil
}

func (s *Server) handleGetIssue(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("get_issue", err.Error()), nil
	}
	detail, err := s.service.GetIssueDetailByIdentifier(ctx, issue.Identifier)
	if err != nil {
		return s.toolError("get_issue", fmt.Sprintf("Failed to load issue detail: %v", err)), nil
	}
	return s.toolResult("get_issue", detail), nil
}

func (s *Server) handleListIssueComments(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("list_issue_comments", err.Error()), nil
	}
	comments, err := s.service.ListIssueComments(ctx, issue.Identifier)
	if err != nil {
		return s.toolError("list_issue_comments", fmt.Sprintf("Failed to list issue comments: %v", err)), nil
	}
	limit, offset := normalizePaginationArgs(args)
	page, total, _ := paginateItems(comments, limit, offset)
	return s.toolResult("list_issue_comments", map[string]interface{}{
		"identifier": issue.Identifier,
		"items":      page,
		"total":      total,
		"limit":      limit,
		"offset":     offset,
		"pagination": s.paginationPayload("list_issue_comments", map[string]interface{}{"identifier": issue.Identifier}, total, limit, offset, len(page)),
	}), nil
}

func (s *Server) handleListIssues(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	limit, offset := normalizePaginationArgs(args)
	query := kanban.IssueQuery{
		ProjectID: asString(args["project_id"]),
		EpicID:    asString(args["epic_id"]),
		State:     asString(args["state"]),
		IssueType: asString(args["issue_type"]),
		Search:    asString(args["search"]),
		Sort:      asString(args["sort"]),
		Limit:     limit,
		Offset:    offset,
	}
	if query.Sort == "" {
		query.Sort = "updated_desc"
	}

	items, total, err := s.service.ListIssueSummaries(ctx, query)
	if err != nil {
		return s.toolError("list_issues", fmt.Sprintf("Failed to list issues: %v", err)), nil
	}
	baseArgs := map[string]interface{}{}
	if projectID := strings.TrimSpace(query.ProjectID); projectID != "" {
		baseArgs["project_id"] = projectID
	}
	if epicID := strings.TrimSpace(query.EpicID); epicID != "" {
		baseArgs["epic_id"] = epicID
	}
	if state := strings.TrimSpace(query.State); state != "" {
		baseArgs["state"] = state
	}
	if issueType := strings.TrimSpace(query.IssueType); issueType != "" {
		baseArgs["issue_type"] = issueType
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		baseArgs["search"] = search
	}
	if sort := strings.TrimSpace(query.Sort); sort != "" {
		baseArgs["sort"] = sort
	}
	return s.toolResult("list_issues", map[string]interface{}{
		"items":      items,
		"total":      total,
		"limit":      query.Limit,
		"offset":     query.Offset,
		"pagination": s.paginationPayload("list_issues", baseArgs, total, query.Limit, query.Offset, len(items)),
	}), nil
}

func (s *Server) handleUpdateIssue(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("update_issue", err.Error()), nil
	}

	updates := issueMutationArgs(args, true)
	if len(updates) == 0 {
		return s.toolResult("update_issue", map[string]interface{}{"identifier": issue.Identifier, "updated": false}), nil
	}
	detail, err := s.service.UpdateIssue(ctx, issue.Identifier, updates)
	if err != nil {
		return s.toolError("update_issue", fmt.Sprintf("Failed to update issue: %v", err)), nil
	}
	return s.toolResult("update_issue", detail), nil
}

func (s *Server) handleAttachIssueAsset(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("attach_issue_asset", err.Error()), nil
	}
	asset, err := s.service.AttachIssueAssetPath(ctx, issue.Identifier, asString(args["path"]))
	if err != nil {
		return s.toolError("attach_issue_asset", fmt.Sprintf("Failed to attach issue asset: %v", err)), nil
	}
	detail, err := s.service.GetIssueDetailByIdentifier(ctx, issue.Identifier)
	if err != nil {
		return s.toolError("attach_issue_asset", fmt.Sprintf("Failed to reload issue detail: %v", err)), nil
	}
	return s.toolResult("attach_issue_asset", map[string]interface{}{
		"asset": asset,
		"issue": detail,
	}), nil
}

func (s *Server) handleCreateIssueComment(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("create_issue_comment", err.Error()), nil
	}
	input := issueCommentMutationArgs(args)
	input.Author = kanban.IssueCommentAuthor{Type: "source", Name: "MCP"}
	comment, err := s.service.CreateIssueCommentWithResult(ctx, issue.Identifier, input)
	if err != nil {
		return s.toolError("create_issue_comment", fmt.Sprintf("Failed to create issue comment: %v", err)), nil
	}
	return s.toolResult("create_issue_comment", comment), nil
}

func (s *Server) handleUpdateIssueComment(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("update_issue_comment", err.Error()), nil
	}
	commentID := asString(args["comment_id"])
	input := issueCommentMutationArgs(args)
	input.Author = kanban.IssueCommentAuthor{Type: "source", Name: "MCP"}
	comment, err := s.service.UpdateIssueComment(ctx, issue.Identifier, commentID, input)
	if err != nil {
		return s.toolError("update_issue_comment", fmt.Sprintf("Failed to update issue comment: %v", err)), nil
	}
	return s.toolResult("update_issue_comment", comment), nil
}

func (s *Server) handleDeleteIssueComment(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("delete_issue_comment", err.Error()), nil
	}
	commentID := asString(args["comment_id"])
	if err := s.service.DeleteIssueComment(ctx, issue.Identifier, commentID); err != nil {
		return s.toolError("delete_issue_comment", fmt.Sprintf("Failed to delete issue comment: %v", err)), nil
	}
	return s.toolResult("delete_issue_comment", map[string]interface{}{
		"deleted":    true,
		"identifier": issue.Identifier,
		"comment_id": commentID,
	}), nil
}

func (s *Server) handleDeleteIssueAsset(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("delete_issue_asset", err.Error()), nil
	}
	assetID := asString(args["asset_id"])
	if err := s.service.DeleteIssueAsset(ctx, issue.Identifier, assetID); err != nil {
		return s.toolError("delete_issue_asset", fmt.Sprintf("Failed to delete issue asset: %v", err)), nil
	}
	detail, err := s.service.GetIssueDetailByIdentifier(ctx, issue.Identifier)
	if err != nil {
		return s.toolError("delete_issue_asset", fmt.Sprintf("Failed to reload issue detail: %v", err)), nil
	}
	return s.toolResult("delete_issue_asset", map[string]interface{}{
		"deleted":  true,
		"asset_id": assetID,
		"issue":    detail,
	}), nil
}

func (s *Server) handleSetIssueState(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("set_issue_state", err.Error()), nil
	}
	detail, err := s.service.SetIssueState(ctx, issue.Identifier, asString(args["state"]))
	if err != nil {
		return s.issueTransitionToolError("set_issue_state", "Failed to update issue state", err), nil
	}
	return s.toolResult("set_issue_state", detail), nil
}

func (s *Server) handleSetIssueWorkflowPhase(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("set_issue_workflow_phase", err.Error()), nil
	}
	phase := kanban.WorkflowPhase(asString(args["workflow_phase"]))
	if !phase.IsValid() {
		return s.toolError("set_issue_workflow_phase", fmt.Sprintf("Invalid workflow phase: %s. Valid phases: implementation, review, done, complete", phase)), nil
	}
	if err := s.store.UpdateIssueWorkflowPhase(issue.ID, phase); err != nil {
		return s.toolError("set_issue_workflow_phase", fmt.Sprintf("Failed to update issue workflow phase: %v", err)), nil
	}
	detail, err := s.store.GetIssueDetailByIdentifier(issue.Identifier)
	if err != nil {
		return s.toolError("set_issue_workflow_phase", fmt.Sprintf("Failed to reload issue: %v", err)), nil
	}
	return s.toolResult("set_issue_workflow_phase", detail), nil
}

func (s *Server) handleDeleteIssue(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("delete_issue", err.Error()), nil
	}
	if err := s.service.DeleteIssue(ctx, issue.Identifier); err != nil {
		return s.toolError("delete_issue", fmt.Sprintf("Failed to delete issue: %v", err)), nil
	}
	return s.toolResult("delete_issue", map[string]interface{}{"identifier": issue.Identifier, "id": issue.ID}), nil
}

func (s *Server) handleRunProject(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	if s.provider == nil {
		return s.runtimeUnavailable("run_project"), nil
	}
	project, err := s.store.GetProject(asString(args["id"]))
	if err != nil {
		return s.toolError("run_project", fmt.Sprintf("Failed to load project: %v", err)), nil
	}
	s.decorateProject(project)
	if !project.DispatchReady {
		errText := strings.TrimSpace(project.DispatchError)
		if errText == "" {
			errText = "project is not dispatchable"
		}
		return s.toolError("run_project", errText), nil
	}
	return s.toolResult("run_project", s.provider.RequestProjectRefresh(project.ID)), nil
}

func (s *Server) handleStopProject(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	if s.provider == nil {
		return s.runtimeUnavailable("stop_project"), nil
	}
	project, err := s.store.GetProject(asString(args["id"]))
	if err != nil {
		return s.toolError("stop_project", fmt.Sprintf("Failed to load project: %v", err)), nil
	}
	return s.toolResult("stop_project", s.provider.StopProjectRuns(project.ID)), nil
}

func (s *Server) handleGetIssueExecution(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("get_issue_execution", err.Error()), nil
	}
	payload, err := runtimeview.IssueExecutionPayload(s.store, s.provider, issue)
	if err != nil {
		return s.toolError("get_issue_execution", fmt.Sprintf("Failed to build execution payload: %v", err)), nil
	}
	return s.toolResult("get_issue_execution", payload), nil
}

func (s *Server) handleRetryIssue(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	if s.provider == nil {
		return s.runtimeUnavailable("retry_issue"), nil
	}
	return s.toolResult("retry_issue", s.provider.RetryIssueNow(ctx, asString(args["identifier"]))), nil
}

func (s *Server) handleRunIssueNow(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	if s.provider == nil {
		return s.runtimeUnavailable("run_issue_now"), nil
	}
	return s.toolResult("run_issue_now", s.provider.RunRecurringIssueNow(ctx, asString(args["identifier"]))), nil
}

func (s *Server) handleBoardOverview(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	counts, err := s.service.BoardOverview(ctx, asString(args["project_id"]))
	if err != nil {
		return s.toolError("board_overview", fmt.Sprintf("Failed to get board overview: %v", err)), nil
	}
	return s.toolResult("board_overview", map[string]int{
		string(kanban.StateBacklog):    counts[kanban.StateBacklog],
		string(kanban.StateReady):      counts[kanban.StateReady],
		string(kanban.StateInProgress): counts[kanban.StateInProgress],
		string(kanban.StateInReview):   counts[kanban.StateInReview],
		string(kanban.StateDone):       counts[kanban.StateDone],
		string(kanban.StateCancelled):  counts[kanban.StateCancelled],
	}), nil
}

func (s *Server) handleSetBlockers(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	issue, err := s.lookupIssue(ctx, asString(args["identifier"]))
	if err != nil {
		return s.toolError("set_blockers", err.Error()), nil
	}
	persisted, err := s.store.SetIssueBlockers(issue.ID, stringListArg(args, "blocked_by"))
	if err != nil {
		return s.toolError("set_blockers", fmt.Sprintf("Failed to set blockers: %v", err)), nil
	}
	detail, err := s.store.GetIssueDetailByIdentifier(issue.Identifier)
	if err != nil {
		return s.toolError("set_blockers", fmt.Sprintf("Failed to reload issue: %v", err)), nil
	}
	return s.toolResult("set_blockers", map[string]interface{}{
		"identifier": issue.Identifier,
		"blocked_by": persisted,
		"issue":      detail,
	}), nil
}

func (s *Server) handleListRuntimeEvents(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	since := int64(intArg(args, "since", 0))
	limit := intArg(args, "limit", 100)
	events, err := s.store.ListRuntimeEvents(since, limit)
	if err != nil {
		return s.toolError("list_runtime_events", fmt.Sprintf("Failed to list runtime events: %v", err)), nil
	}
	var lastSeq int64
	if len(events) > 0 {
		lastSeq = events[len(events)-1].Seq
	}
	return s.toolResult("list_runtime_events", map[string]interface{}{
		"since":    since,
		"last_seq": lastSeq,
		"events":   events,
	}), nil
}

func (s *Server) handleGetRuntimeSnapshot(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	if s.provider == nil {
		return s.runtimeUnavailable("get_runtime_snapshot"), nil
	}
	return s.toolResult("get_runtime_snapshot", observability.StatePayload(s.provider)), nil
}

func (s *Server) handleListSessions(ctx context.Context, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	if s.provider == nil {
		return s.runtimeUnavailable("list_sessions"), nil
	}
	all := s.provider.LiveSessions()
	identifier := asString(args["identifier"])
	if strings.TrimSpace(identifier) == "" {
		return s.toolResult("list_sessions", all), nil
	}
	sessions, ok := all["sessions"].(map[string]interface{})
	if !ok {
		return s.toolError("list_sessions", fmt.Sprintf("Live sessions are unavailable for issue: %s", identifier)), nil
	}
	session, ok := sessions[identifier]
	if !ok {
		return s.toolError("list_sessions", fmt.Sprintf("Session not found: %s", identifier)), nil
	}
	return s.toolResult("list_sessions", map[string]interface{}{
		"issue":   identifier,
		"session": session,
	}), nil
}

type responseEnvelope struct {
	OK    bool                 `json:"ok"`
	Tool  string               `json:"tool"`
	Meta  responseEnvelopeMeta `json:"meta"`
	Data  interface{}          `json:"data,omitempty"`
	Error *responseError       `json:"error,omitempty"`
}

type responseEnvelopeMeta struct {
	DBPath           string `json:"db_path"`
	StoreID          string `json:"store_id"`
	ServerInstanceID string `json:"server_instance_id"`
	ChangeSeq        int64  `json:"change_seq"`
}

type responseError struct {
	Message string `json:"message"`
}

func (s *Server) responseMeta() responseEnvelopeMeta {
	meta := responseEnvelopeMeta{
		ServerInstanceID: s.instanceID,
	}
	if s.store == nil {
		return meta
	}
	changeSeq, _ := s.store.LatestChangeSeq()
	identity := s.store.Identity()
	meta.DBPath = identity.DBPath
	meta.StoreID = identity.StoreID
	meta.ChangeSeq = changeSeq
	return meta
}

func (s *Server) toolResult(name string, data interface{}) *mcpapi.CallToolResult {
	return s.envelopeResult(name, data, "", false)
}

func (s *Server) toolError(name, message string) *mcpapi.CallToolResult {
	return s.envelopeResult(name, nil, message, true)
}

func normalizePaginationArgs(args map[string]interface{}) (int, int) {
	limit := intArg(args, "limit", mcpPaginationDefaultLimit)
	if limit <= 0 || limit > mcpPaginationMaxLimit {
		limit = mcpPaginationDefaultLimit
	}
	offset := intArg(args, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func paginateItems[T any](items []T, limit, offset int) ([]T, int, int) {
	total := len(items)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := make([]T, end-start)
	copy(page, items[start:end])
	return page, total, end
}

func (s *Server) paginationPayload(tool string, baseArgs map[string]interface{}, total, limit, offset, returned int) map[string]interface{} {
	nextOffset := offset + returned
	if nextOffset > total {
		nextOffset = total
	}
	pagination := map[string]interface{}{
		"has_more":    nextOffset < total,
		"next_offset": nextOffset,
		"next_hint":   "No additional results remain.",
	}
	if nextOffset < total {
		nextArgs := map[string]interface{}{}
		for key, value := range baseArgs {
			if value != nil {
				nextArgs[key] = value
			}
		}
		nextArgs["limit"] = limit
		nextArgs["offset"] = nextOffset
		pagination["next_request"] = map[string]interface{}{
			"tool":      tool,
			"arguments": nextArgs,
		}
		pagination["next_hint"] = "Use pagination.next_request to fetch the next batch."
	}
	return pagination
}

func (s *Server) scopedRepoPath() string {
	if s.provider == nil {
		return ""
	}
	status := s.provider.Status()
	if status == nil {
		return ""
	}
	value, _ := status["scoped_repo_path"].(string)
	return strings.TrimSpace(value)
}

func (s *Server) validateScopedRepoPath(repoPath string) error {
	scopedRepoPath := s.scopedRepoPath()
	if scopedRepoPath == "" {
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

func (s *Server) decorateProject(project *kanban.Project) {
	if project == nil {
		return
	}
	project.DispatchReady = project.OrchestrationReady
	project.DispatchError = ""

	scopedRepoPath := s.scopedRepoPath()
	if scopedRepoPath == "" || strings.TrimSpace(project.RepoPath) == "" {
		return
	}
	if filepath.Clean(project.RepoPath) == filepath.Clean(scopedRepoPath) {
		return
	}
	project.DispatchReady = false
	project.DispatchError = "Project repo is outside the current server scope (" + scopedRepoPath + ")"
}

func (s *Server) decorateProjects(projects []kanban.Project) {
	for i := range projects {
		s.decorateProject(&projects[i])
	}
}

func (s *Server) decorateProjectSummaries(projects []kanban.ProjectSummary) {
	for i := range projects {
		s.decorateProject(&projects[i].Project)
	}
}

func (s *Server) issueTransitionToolError(name, prefix string, err error) *mcpapi.CallToolResult {
	if kanban.IsBlockedTransition(err) {
		return s.toolError(name, err.Error())
	}
	return s.toolError(name, fmt.Sprintf("%s: %v", prefix, err))
}

func (s *Server) runtimeUnavailable(name string) *mcpapi.CallToolResult {
	return s.toolError(name, "runtime_unavailable: this Maestro MCP server was started without a live runtime provider")
}

func (s *Server) envelopeResult(name string, data interface{}, message string, isError bool) *mcpapi.CallToolResult {
	envelope := responseEnvelope{
		OK:   !isError,
		Tool: name,
		Meta: s.responseMeta(),
		Data: data,
	}
	if isError {
		envelope.Error = &responseError{Message: message}
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		body = []byte(fmt.Sprintf(`{"ok":false,"tool":%q,"error":{"message":"failed to encode response: %s"}}`, name, strings.ReplaceAll(err.Error(), `"`, `'`)))
		isError = true
	}
	return &mcpapi.CallToolResult{
		IsError: isError,
		Content: []mcpapi.Content{mcpapi.TextContent{
			Type: "text",
			Text: string(body),
		}},
	}
}

func decodeEnvelopeResult(result *mcpapi.CallToolResult) (*responseEnvelope, error) {
	if result == nil || len(result.Content) == 0 {
		return nil, fmt.Errorf("missing MCP content")
	}
	content, ok := result.Content[0].(mcpapi.TextContent)
	if !ok {
		return nil, fmt.Errorf("unexpected MCP content type %T", result.Content[0])
	}
	var envelope responseEnvelope
	if err := json.Unmarshal([]byte(content.Text), &envelope); err != nil {
		return nil, err
	}
	return &envelope, nil
}

func (s *Server) lookupIssue(ctx context.Context, identifier string) (*kanban.Issue, error) {
	issue, err := s.service.GetIssueByIdentifier(ctx, identifier)
	if err == nil {
		return issue, nil
	}
	if !errors.Is(err, kanban.ErrNotFound) && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	issue, fallbackErr := s.store.GetIssue(identifier)
	if fallbackErr == nil {
		return issue, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return nil, fmt.Errorf("Issue not found: %s", identifier)
}

func issueMutationArgs(args map[string]interface{}, includeProjectFields bool) map[string]interface{} {
	updates := make(map[string]interface{})
	if includeProjectFields {
		if value, ok := args["project_id"]; ok {
			updates["project_id"] = asString(value)
		}
		if value, ok := args["epic_id"]; ok {
			updates["epic_id"] = asString(value)
		}
	}
	if value, ok := args["title"]; ok {
		updates["title"] = asString(value)
	}
	if value, ok := args["description"]; ok {
		updates["description"] = asString(value)
	}
	if value, ok := args["permission_profile"]; ok {
		updates["permission_profile"] = asString(value)
	}
	if value, ok := args["issue_type"]; ok {
		updates["issue_type"] = asString(value)
	}
	if value, ok := args["cron"]; ok {
		updates["cron"] = asString(value)
	}
	if value, ok := boolPointerArg(args, "enabled"); value != nil && ok {
		updates["enabled"] = *value
	}
	if _, ok := args["priority"]; ok {
		updates["priority"] = intArg(args, "priority", 0)
	}
	if _, ok := args["labels"]; ok {
		updates["labels"] = stringListArg(args, "labels")
	}
	if _, ok := args["blocked_by"]; ok {
		updates["blocked_by"] = stringListArg(args, "blocked_by")
	}
	if value, ok := args["branch_name"]; ok {
		updates["branch_name"] = asString(value)
	}
	if value, ok := args["pr_url"]; ok {
		updates["pr_url"] = asString(value)
	}
	if value, ok := args["runtime_name"]; ok {
		updates["runtime_name"] = asString(value)
	}
	return updates
}

func issueCommentMutationArgs(args map[string]interface{}) providers.IssueCommentInput {
	input := providers.IssueCommentInput{
		ParentCommentID:     asString(args["parent_comment_id"]),
		RemoveAttachmentIDs: stringListArg(args, "remove_attachment_ids"),
	}
	if value, ok := args["body"]; ok {
		body := asString(value)
		input.Body = &body
	}
	for _, path := range stringListArg(args, "attachment_paths") {
		input.Attachments = append(input.Attachments, providers.IssueCommentAttachment{Path: path})
	}
	return input
}

func extensionToolInputSchema(spec map[string]interface{}) mcpapi.ToolInputSchema {
	inputSchema, _ := spec["inputSchema"].(map[string]interface{})
	properties, _ := inputSchema["properties"].(map[string]interface{})
	typ := asString(inputSchema["type"])
	if typ == "" {
		typ = "object"
	}
	return mcpapi.ToolInputSchema{
		Type:       typ,
		Properties: properties,
	}
}

func objectTool(name, description string, properties map[string]interface{}) mcpapi.Tool {
	return mcpapi.Tool{
		Name:        name,
		Description: description,
		InputSchema: mcpapi.ToolInputSchema{
			Type:       "object",
			Properties: properties,
		},
	}
}

func stringProperty(description string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": description}
}

func numberProperty(description string) map[string]interface{} {
	return map[string]interface{}{"type": "number", "description": description}
}

func objectProperty(description string) map[string]interface{} {
	return map[string]interface{}{"type": "object", "description": description}
}

func booleanProperty(description string) map[string]interface{} {
	return map[string]interface{}{"type": "boolean", "description": description}
}

func stringArrayProperty(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": description,
		"items":       map[string]interface{}{"type": "string"},
	}
}

func intArg(args map[string]interface{}, key string, fallback int) int {
	raw, ok := args[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	default:
		return fallback
	}
}

func stringListArg(args map[string]interface{}, key string) []string {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	switch items := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(items))
		out = append(out, items...)
		return out
	default:
		return nil
	}
}

func boolPointerArg(args map[string]interface{}, key string) (*bool, bool) {
	raw, ok := args[key]
	if !ok {
		return nil, false
	}
	switch value := raw.(type) {
	case bool:
		return &value, true
	default:
		return nil, false
	}
}

func objectArg(args map[string]interface{}, key string) map[string]interface{} {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	value, _ := raw.(map[string]interface{})
	return value
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
