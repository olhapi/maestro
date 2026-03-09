package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
)

// Server implements the MCP server for the kanban board
// and optional extension tools.
type Server struct {
	store      *kanban.Store
	server     server.MCPServer
	tools      []mcp.Tool
	extensions *extensions.Registry
	instanceID string
}

// NewServer creates a new MCP server.
func NewServer(store *kanban.Store) *Server {
	return NewServerWithExtensions(store, "")
}

// NewServerWithExtensions creates a new MCP server and optionally loads extension tools from JSON file.
func NewServerWithExtensions(store *kanban.Store, extensionsFile string) *Server {
	registry, _ := extensions.LoadFile(extensionsFile)
	return NewServerWithRegistry(store, registry)
}

func NewServerWithRegistry(store *kanban.Store, registry *extensions.Registry) *Server {
	if registry == nil {
		registry = extensions.EmptyRegistry()
	}
	s := &Server{
		store:      store,
		server:     server.NewDefaultServer("maestro", "1.0.0"),
		extensions: registry,
		instanceID: generateServerInstanceID(),
	}

	s.registerTools()
	return s
}

func (s *Server) registerTools() {
	// Define tools
	s.tools = []mcp.Tool{
		{
			Name:        "server_info",
			Description: "Get Maestro MCP server identity and store metadata",
			InputSchema: mcp.ToolInputSchema{Type: "object"},
		},
		{
			Name:        "create_project",
			Description: "Create a new project",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name":          map[string]interface{}{"type": "string", "description": "Project name"},
					"description":   map[string]interface{}{"type": "string", "description": "Project description"},
					"repo_path":     map[string]interface{}{"type": "string", "description": "Absolute path to the repo this project orchestrates"},
					"workflow_path": map[string]interface{}{"type": "string", "description": "Optional workflow path override"},
				},
			},
		},
		{
			Name:        "update_project",
			Description: "Update an existing project",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"id":            map[string]interface{}{"type": "string", "description": "Project ID"},
					"name":          map[string]interface{}{"type": "string", "description": "Project name"},
					"description":   map[string]interface{}{"type": "string", "description": "Project description"},
					"repo_path":     map[string]interface{}{"type": "string", "description": "Absolute path to the repo this project orchestrates"},
					"workflow_path": map[string]interface{}{"type": "string", "description": "Optional workflow path override"},
				},
			},
		},
		{
			Name:        "list_projects",
			Description: "List all projects",
			InputSchema: mcp.ToolInputSchema{Type: "object"},
		},
		{
			Name:        "delete_project",
			Description: "Delete a project",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Project ID"},
				},
			},
		},
		{
			Name:        "create_epic",
			Description: "Create a new epic within a project",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"project_id":  map[string]interface{}{"type": "string", "description": "Project ID"},
					"name":        map[string]interface{}{"type": "string", "description": "Epic name"},
					"description": map[string]interface{}{"type": "string", "description": "Epic description"},
				},
			},
		},
		{
			Name:        "list_epics",
			Description: "List epics, optionally filtered by project",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"project_id": map[string]interface{}{"type": "string", "description": "Filter by project ID"},
				},
			},
		},
		{
			Name:        "delete_epic",
			Description: "Delete an epic",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Epic ID"},
				},
			},
		},
		{
			Name:        "create_issue",
			Description: "Create a new issue",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"title":       map[string]interface{}{"type": "string", "description": "Issue title"},
					"description": map[string]interface{}{"type": "string", "description": "Issue description"},
					"project_id":  map[string]interface{}{"type": "string", "description": "Project ID"},
					"epic_id":     map[string]interface{}{"type": "string", "description": "Epic ID"},
					"priority":    map[string]interface{}{"type": "number", "description": "Priority (lower = higher)"},
					"labels":      map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
				},
			},
		},
		{
			Name:        "get_issue",
			Description: "Get an issue by ID or identifier (e.g., PROJ-123)",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"identifier": map[string]interface{}{"type": "string", "description": "Issue ID or identifier"},
				},
			},
		},
		{
			Name:        "list_issues",
			Description: "List issues with optional filters",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"project_id": map[string]interface{}{"type": "string", "description": "Filter by project ID"},
					"epic_id":    map[string]interface{}{"type": "string", "description": "Filter by epic ID"},
					"state":      map[string]interface{}{"type": "string", "description": "Filter by state: backlog, ready, in_progress, in_review, done, cancelled"},
				},
			},
		},
		{
			Name:        "update_issue",
			Description: "Update an issue",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"identifier":  map[string]interface{}{"type": "string", "description": "Issue ID or identifier"},
					"title":       map[string]interface{}{"type": "string", "description": "New title"},
					"description": map[string]interface{}{"type": "string", "description": "New description"},
					"priority":    map[string]interface{}{"type": "number", "description": "New priority"},
					"labels":      map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
					"branch_name": map[string]interface{}{"type": "string", "description": "Branch name"},
					"pr_number":   map[string]interface{}{"type": "number", "description": "PR number"},
					"pr_url":      map[string]interface{}{"type": "string", "description": "PR URL"},
				},
			},
		},
		{
			Name:        "set_issue_state",
			Description: "Change an issue's state",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"identifier": map[string]interface{}{"type": "string", "description": "Issue ID or identifier"},
					"state":      map[string]interface{}{"type": "string", "description": "New state: backlog, ready, in_progress, in_review, done, cancelled"},
				},
			},
		},
		{
			Name:        "delete_issue",
			Description: "Delete an issue",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"identifier": map[string]interface{}{"type": "string", "description": "Issue ID or identifier"},
				},
			},
		},
		{
			Name:        "board_overview",
			Description: "Get a kanban board overview showing issue counts by state",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"project_id": map[string]interface{}{"type": "string", "description": "Filter by project ID"},
				},
			},
		},
		{
			Name:        "set_blockers",
			Description: "Set blockers for an issue",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"identifier": map[string]interface{}{"type": "string", "description": "Issue ID or identifier"},
					"blocked_by": map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}, "description": "List of issue identifiers that block this issue"},
				},
			},
		},
	}

	for _, spec := range s.extensions.Specs() {
		name, _ := spec["name"].(string)
		description, _ := spec["description"].(string)
		inputSchema, _ := spec["inputSchema"].(map[string]interface{})
		properties, _ := inputSchema["properties"].(map[string]interface{})
		s.tools = append(s.tools, mcp.Tool{
			Name:        name,
			Description: description,
			InputSchema: mcp.ToolInputSchema{
				Type:       "object",
				Properties: properties,
			},
		})
	}

	// Register tool list handler
	s.server.HandleListTools(func(ctx context.Context, cursor *string) (*mcp.ListToolsResult, error) {
		return &mcp.ListToolsResult{Tools: s.tools}, nil
	})

	// Register tool call handler
	s.server.HandleCallTool(s.handleCallTool)
}

// handleCallTool routes tool calls to appropriate handlers
func (s *Server) handleCallTool(ctx context.Context, name string, args map[string]interface{}) (result *mcp.CallToolResult, err error) {
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
	case "list_epics":
		return s.handleListEpics(ctx, args)
	case "delete_epic":
		return s.handleDeleteEpic(ctx, args)
	case "create_issue":
		return s.handleCreateIssue(ctx, args)
	case "get_issue":
		return s.handleGetIssue(ctx, args)
	case "list_issues":
		return s.handleListIssues(ctx, args)
	case "update_issue":
		return s.handleUpdateIssue(ctx, args)
	case "set_issue_state":
		return s.handleSetIssueState(ctx, args)
	case "delete_issue":
		return s.handleDeleteIssue(ctx, args)
	case "board_overview":
		return s.handleBoardOverview(ctx, args)
	case "set_blockers":
		return s.handleSetBlockers(ctx, args)
	default:
		for _, toolName := range s.extensions.Names() {
			if toolName == name {
				return s.handleExtensionTool(ctx, name, args)
			}
		}
		return s.toolError(name, fmt.Sprintf("Unknown tool: %s", name)), nil
	}
}

func generateServerInstanceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "mcp_" + hex.EncodeToString(b)
}

func (s *Server) handleExtensionTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if s.extensions == nil {
		return s.toolError(name, fmt.Sprintf("Unknown extension tool: %s", name)), nil
	}
	out, err := s.extensions.Execute(ctx, name, args)
	if err != nil {
		return s.toolError(name, err.Error()), nil
	}
	return s.toolResult(name, map[string]interface{}{"output": out}), nil
}

// ServeStdio runs the MCP server over stdin/stdout
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.server)
}

// Handlers

func (s *Server) handleServerInfo(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	projects, err := s.store.ListProjects()
	if err != nil {
		return s.toolError("server_info", fmt.Sprintf("Failed to list projects: %v", err)), nil
	}
	issues, err := s.store.ListIssues(nil)
	if err != nil {
		return s.toolError("server_info", fmt.Sprintf("Failed to list issues: %v", err)), nil
	}
	return s.toolResult("server_info", map[string]interface{}{
		"project_count": len(projects),
		"issue_count":   len(issues),
	}), nil
}

func (s *Server) handleCreateProject(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)
	repoPath, _ := args["repo_path"].(string)
	workflowPath, _ := args["workflow_path"].(string)
	if strings.TrimSpace(repoPath) == "" {
		return s.toolError("create_project", "repo_path is required"), nil
	}

	project, err := s.store.CreateProject(name, description, repoPath, workflowPath)
	if err != nil {
		return s.toolError("create_project", fmt.Sprintf("Failed to create project: %v", err)), nil
	}

	return s.toolResult("create_project", project), nil
}

func (s *Server) handleUpdateProject(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	id, _ := args["id"].(string)
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)
	repoPath, _ := args["repo_path"].(string)
	workflowPath, _ := args["workflow_path"].(string)
	if strings.TrimSpace(repoPath) == "" {
		return s.toolError("update_project", "repo_path is required"), nil
	}

	if err := s.store.UpdateProject(id, name, description, repoPath, workflowPath); err != nil {
		return s.toolError("update_project", fmt.Sprintf("Failed to update project: %v", err)), nil
	}
	project, err := s.store.GetProject(id)
	if err != nil {
		return s.toolError("update_project", fmt.Sprintf("Failed to reload project: %v", err)), nil
	}
	return s.toolResult("update_project", project), nil
}

func (s *Server) handleListProjects(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	projects, err := s.store.ListProjects()
	if err != nil {
		return s.toolError("list_projects", fmt.Sprintf("Failed to list projects: %v", err)), nil
	}
	return s.toolResult("list_projects", map[string]interface{}{"items": projects}), nil
}

func (s *Server) handleDeleteProject(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	id, _ := args["id"].(string)
	if err := s.store.DeleteProject(id); err != nil {
		return s.toolError("delete_project", fmt.Sprintf("Failed to delete project: %v", err)), nil
	}
	return s.toolResult("delete_project", map[string]interface{}{"id": id}), nil
}

func (s *Server) handleCreateEpic(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	projectID, _ := args["project_id"].(string)
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)

	epic, err := s.store.CreateEpic(projectID, name, description)
	if err != nil {
		return s.toolError("create_epic", fmt.Sprintf("Failed to create epic: %v", err)), nil
	}

	return s.toolResult("create_epic", epic), nil
}

func (s *Server) handleListEpics(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	projectID, _ := args["project_id"].(string)

	epics, err := s.store.ListEpics(projectID)
	if err != nil {
		return s.toolError("list_epics", fmt.Sprintf("Failed to list epics: %v", err)), nil
	}
	return s.toolResult("list_epics", map[string]interface{}{"items": epics}), nil
}

func (s *Server) handleDeleteEpic(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	id, _ := args["id"].(string)
	if err := s.store.DeleteEpic(id); err != nil {
		return s.toolError("delete_epic", fmt.Sprintf("Failed to delete epic: %v", err)), nil
	}
	return s.toolResult("delete_epic", map[string]interface{}{"id": id}), nil
}

func (s *Server) handleCreateIssue(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	title, _ := args["title"].(string)
	description, _ := args["description"].(string)
	projectID, _ := args["project_id"].(string)
	epicID, _ := args["epic_id"].(string)
	priority, _ := args["priority"].(float64)

	var labels []string
	if l, ok := args["labels"].([]interface{}); ok {
		for _, label := range l {
			if str, ok := label.(string); ok {
				labels = append(labels, str)
			}
		}
	}

	issue, err := s.store.CreateIssue(projectID, epicID, title, description, int(priority), labels)
	if err != nil {
		return s.toolError("create_issue", fmt.Sprintf("Failed to create issue: %v", err)), nil
	}

	return s.toolResult("create_issue", issue), nil
}

func (s *Server) handleGetIssue(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	identifier, _ := args["identifier"].(string)

	issue, err := s.store.GetIssue(identifier)
	if err != nil {
		// Try by identifier
		issue, err = s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			return s.toolError("get_issue", fmt.Sprintf("Issue not found: %s", identifier)), nil
		}
	}
	return s.toolResult("get_issue", issue), nil
}

func (s *Server) handleListIssues(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	filter := make(map[string]interface{})
	if projectID, ok := args["project_id"].(string); ok {
		filter["project_id"] = projectID
	}
	if epicID, ok := args["epic_id"].(string); ok {
		filter["epic_id"] = epicID
	}
	if state, ok := args["state"].(string); ok {
		filter["state"] = state
	}

	issues, err := s.store.ListIssues(filter)
	if err != nil {
		return s.toolError("list_issues", fmt.Sprintf("Failed to list issues: %v", err)), nil
	}
	return s.toolResult("list_issues", map[string]interface{}{"items": issues}), nil
}

func (s *Server) handleUpdateIssue(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	identifier, _ := args["identifier"].(string)

	issue, err := s.store.GetIssue(identifier)
	if err != nil {
		issue, err = s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			return s.toolError("update_issue", fmt.Sprintf("Issue not found: %s", identifier)), nil
		}
	}

	updates := make(map[string]interface{})
	if title, ok := args["title"].(string); ok {
		updates["title"] = title
	}
	if desc, ok := args["description"].(string); ok {
		updates["description"] = desc
	}
	if priority, ok := args["priority"].(float64); ok {
		updates["priority"] = int(priority)
	}
	if branch, ok := args["branch_name"].(string); ok {
		updates["branch_name"] = branch
	}
	if prNum, ok := args["pr_number"].(float64); ok {
		updates["pr_number"] = int(prNum)
	}
	if prURL, ok := args["pr_url"].(string); ok {
		updates["pr_url"] = prURL
	}
	if labels, ok := args["labels"].([]interface{}); ok {
		var labelStrs []string
		for _, l := range labels {
			if str, ok := l.(string); ok {
				labelStrs = append(labelStrs, str)
			}
		}
		updates["labels"] = labelStrs
	}

	if len(updates) == 0 {
		return s.toolResult("update_issue", map[string]interface{}{"identifier": issue.Identifier, "updated": false}), nil
	}

	if err := s.store.UpdateIssue(issue.ID, updates); err != nil {
		return s.toolError("update_issue", fmt.Sprintf("Failed to update issue: %v", err)), nil
	}
	updated, err := s.store.GetIssue(issue.ID)
	if err != nil {
		return s.toolError("update_issue", fmt.Sprintf("Failed to reload issue: %v", err)), nil
	}
	return s.toolResult("update_issue", updated), nil
}

func (s *Server) handleSetIssueState(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	identifier, _ := args["identifier"].(string)
	state, _ := args["state"].(string)

	issue, err := s.store.GetIssue(identifier)
	if err != nil {
		issue, err = s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			return s.toolError("set_issue_state", fmt.Sprintf("Issue not found: %s", identifier)), nil
		}
	}

	if !kanban.State(state).IsValid() {
		return s.toolError("set_issue_state", fmt.Sprintf("Invalid state: %s. Valid states: backlog, ready, in_progress, in_review, done, cancelled", state)), nil
	}

	if err := s.store.UpdateIssueState(issue.ID, kanban.State(state)); err != nil {
		return s.toolError("set_issue_state", fmt.Sprintf("Failed to update issue state: %v", err)), nil
	}
	updated, err := s.store.GetIssue(issue.ID)
	if err != nil {
		return s.toolError("set_issue_state", fmt.Sprintf("Failed to reload issue: %v", err)), nil
	}
	return s.toolResult("set_issue_state", updated), nil
}

func (s *Server) handleDeleteIssue(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	identifier, _ := args["identifier"].(string)

	issue, err := s.store.GetIssue(identifier)
	if err != nil {
		issue, err = s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			return s.toolError("delete_issue", fmt.Sprintf("Issue not found: %s", identifier)), nil
		}
	}

	if err := s.store.DeleteIssue(issue.ID); err != nil {
		return s.toolError("delete_issue", fmt.Sprintf("Failed to delete issue: %v", err)), nil
	}
	return s.toolResult("delete_issue", map[string]interface{}{"identifier": issue.Identifier, "id": issue.ID}), nil
}

func (s *Server) handleBoardOverview(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	filter := make(map[string]interface{})
	if projectID, ok := args["project_id"].(string); ok {
		filter["project_id"] = projectID
	}

	issues, err := s.store.ListIssues(filter)
	if err != nil {
		return s.toolError("board_overview", fmt.Sprintf("Failed to get board overview: %v", err)), nil
	}

	counts := make(map[kanban.State]int)
	for _, i := range issues {
		counts[i.State]++
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

func (s *Server) handleSetBlockers(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	identifier, _ := args["identifier"].(string)

	issue, err := s.store.GetIssue(identifier)
	if err != nil {
		issue, err = s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			return s.toolError("set_blockers", fmt.Sprintf("Issue not found: %s", identifier)), nil
		}
	}

	var blockers []string
	if b, ok := args["blocked_by"].([]interface{}); ok {
		for _, block := range b {
			if str, ok := block.(string); ok {
				blockers = append(blockers, str)
			}
		}
	}

	persisted, err := s.store.SetIssueBlockers(issue.ID, blockers)
	if err != nil {
		return s.toolError("set_blockers", fmt.Sprintf("Failed to set blockers: %v", err)), nil
	}
	updated, err := s.store.GetIssue(issue.ID)
	if err != nil {
		return s.toolError("set_blockers", fmt.Sprintf("Failed to reload issue: %v", err)), nil
	}
	return s.toolResult("set_blockers", map[string]interface{}{
		"identifier": issue.Identifier,
		"blocked_by": persisted,
		"issue":      updated,
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

func (s *Server) toolResult(name string, data interface{}) *mcp.CallToolResult {
	return s.envelopeResult(name, data, "", false)
}

func (s *Server) toolError(name, message string) *mcp.CallToolResult {
	return s.envelopeResult(name, nil, message, true)
}

func (s *Server) envelopeResult(name string, data interface{}, message string, isError bool) *mcp.CallToolResult {
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
	return &mcp.CallToolResult{
		IsError: isError,
		Content: []mcp.Content{mcp.TextContent{
			Type: "text",
			Text: string(body),
		}},
	}
}
