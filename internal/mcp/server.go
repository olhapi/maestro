package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/olhapi/symphony-go/internal/kanban"
)

// Server implements the MCP server for the kanban board
type ExtensionTool struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Command      string `json:"command"`
	TimeoutSec   int    `json:"timeout_sec,omitempty"`
	Allowed      *bool  `json:"allowed,omitempty"`
	WorkingDir   string `json:"working_dir,omitempty"`
	RequireArgs  bool   `json:"require_args,omitempty"`
	DenyEnvPassthrough bool `json:"deny_env_passthrough,omitempty"`
}

// Server implements the MCP server for the kanban board
// and optional extension tools.
type Server struct {
	store          *kanban.Store
	server         server.MCPServer
	tools          []mcp.Tool
	extensionTools map[string]ExtensionTool
}

// NewServer creates a new MCP server.
func NewServer(store *kanban.Store) *Server {
	return NewServerWithExtensions(store, "")
}

// NewServerWithExtensions creates a new MCP server and optionally loads extension tools from JSON file.
func NewServerWithExtensions(store *kanban.Store, extensionsFile string) *Server {
	s := &Server{
		store:          store,
		server:         server.NewDefaultServer("symphony", "1.0.0"),
		extensionTools: map[string]ExtensionTool{},
	}

	s.registerTools()
	if extensionsFile != "" {
		_ = s.loadExtensions(extensionsFile)
	}
	return s
}

func (s *Server) registerTools() {
	// Define tools
	s.tools = []mcp.Tool{
		{
			Name:        "create_project",
			Description: "Create a new project",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "description": "Project name"},
					"description": map[string]interface{}{"type": "string", "description": "Project description"},
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

	// Register tool list handler
	s.server.HandleListTools(func(ctx context.Context, cursor *string) (*mcp.ListToolsResult, error) {
		return &mcp.ListToolsResult{Tools: s.tools}, nil
	})

	// Register tool call handler
	s.server.HandleCallTool(s.handleCallTool)
}

func extensionToolSchema() mcp.ToolInputSchema {
	return mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"args": map[string]interface{}{
				"type":        "object",
				"description": "Extension arguments object; passed as JSON to command via SYMPHONY_ARGS_JSON",
			},
		},
	}
}

func (s *Server) loadExtensions(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var defs []ExtensionTool
	if err := json.Unmarshal(data, &defs); err != nil {
		return err
	}
	for _, d := range defs {
		name := strings.TrimSpace(d.Name)
		if name == "" || strings.TrimSpace(d.Command) == "" {
			continue
		}
		if d.TimeoutSec <= 0 {
			d.TimeoutSec = 15
		}
		d.Name = name
		s.extensionTools[name] = d
		s.tools = append(s.tools, mcp.Tool{
			Name:        name,
			Description: d.Description,
			InputSchema: extensionToolSchema(),
		})
	}
	return nil
}

// handleCallTool routes tool calls to appropriate handlers
func (s *Server) handleCallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	switch name {
	case "create_project":
		return s.handleCreateProject(ctx, args)
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
		if _, ok := s.extensionTools[name]; ok {
			return s.handleExtensionTool(ctx, name, args)
		}
		return toolError(fmt.Sprintf("Unknown tool: %s", name)), nil
	}
}

// toolResult creates a successful tool result with text
func toolResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.TextContent{
			Type: "text",
			Text: text,
		}},
	}
}

// toolError creates an error tool result
func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{mcp.TextContent{
			Type: "text",
			Text: text,
		}},
	}
}

func (s *Server) handleExtensionTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	ext, ok := s.extensionTools[name]
	if !ok {
		return toolError(fmt.Sprintf("Unknown extension tool: %s", name)), nil
	}
	if ext.Allowed != nil && !*ext.Allowed {
		return toolError(fmt.Sprintf("extension tool %s is disabled by policy", name)), nil
	}
	if ext.RequireArgs {
		if _, ok := args["args"]; !ok {
			return toolError(fmt.Sprintf("extension tool %s requires args object", name)), nil
		}
	}

	argsJSON, _ := json.Marshal(args)
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(ext.TimeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", ext.Command)
	if ext.WorkingDir != "" {
		if wd, err := filepath.Abs(ext.WorkingDir); err == nil {
			cmd.Dir = wd
		}
	}
	if ext.DenyEnvPassthrough {
		cmd.Env = []string{"SYMPHONY_ARGS_JSON=" + string(argsJSON), "SYMPHONY_TOOL_NAME=" + name}
	} else {
		cmd.Env = append(os.Environ(), "SYMPHONY_ARGS_JSON="+string(argsJSON), "SYMPHONY_TOOL_NAME="+name)
	}

	out, err := cmd.CombinedOutput()
	if runCtx.Err() == context.DeadlineExceeded {
		return toolError(fmt.Sprintf("extension tool %s timed out after %ds", name, ext.TimeoutSec)), nil
	}
	if err != nil {
		return toolError(fmt.Sprintf("extension tool %s failed: %v\n%s", name, err, string(out))), nil
	}
	return toolResult(strings.TrimSpace(string(out))), nil
}

// ServeStdio runs the MCP server over stdin/stdout
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.server)
}

// Handlers

func (s *Server) handleCreateProject(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)

	project, err := s.store.CreateProject(name, description)
	if err != nil {
		return toolError(fmt.Sprintf("Failed to create project: %v", err)), nil
	}

	return toolResult(fmt.Sprintf("Created project %s (ID: %s)", project.Name, project.ID)), nil
}

func (s *Server) handleListProjects(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	projects, err := s.store.ListProjects()
	if err != nil {
		return toolError(fmt.Sprintf("Failed to list projects: %v", err)), nil
	}

	if len(projects) == 0 {
		return toolResult("No projects found. Create one with create_project."), nil
	}

	var sb strings.Builder
	sb.WriteString("Projects:\n")
	for _, p := range projects {
		sb.WriteString(fmt.Sprintf("- %s (ID: %s)\n", p.Name, p.ID))
		if p.Description != "" {
			sb.WriteString(fmt.Sprintf("  %s\n", p.Description))
		}
	}
	return toolResult(sb.String()), nil
}

func (s *Server) handleDeleteProject(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	id, _ := args["id"].(string)
	if err := s.store.DeleteProject(id); err != nil {
		return toolError(fmt.Sprintf("Failed to delete project: %v", err)), nil
	}
	return toolResult("Project deleted"), nil
}

func (s *Server) handleCreateEpic(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	projectID, _ := args["project_id"].(string)
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)

	epic, err := s.store.CreateEpic(projectID, name, description)
	if err != nil {
		return toolError(fmt.Sprintf("Failed to create epic: %v", err)), nil
	}

	return toolResult(fmt.Sprintf("Created epic %s (ID: %s)", epic.Name, epic.ID)), nil
}

func (s *Server) handleListEpics(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	projectID, _ := args["project_id"].(string)

	epics, err := s.store.ListEpics(projectID)
	if err != nil {
		return toolError(fmt.Sprintf("Failed to list epics: %v", err)), nil
	}

	if len(epics) == 0 {
		return toolResult("No epics found."), nil
	}

	var sb strings.Builder
	sb.WriteString("Epics:\n")
	for _, e := range epics {
		sb.WriteString(fmt.Sprintf("- %s (ID: %s, Project: %s)\n", e.Name, e.ID, e.ProjectID))
	}
	return toolResult(sb.String()), nil
}

func (s *Server) handleDeleteEpic(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	id, _ := args["id"].(string)
	if err := s.store.DeleteEpic(id); err != nil {
		return toolError(fmt.Sprintf("Failed to delete epic: %v", err)), nil
	}
	return toolResult("Epic deleted"), nil
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
		return toolError(fmt.Sprintf("Failed to create issue: %v", err)), nil
	}

	return toolResult(fmt.Sprintf("Created issue %s: %s", issue.Identifier, issue.Title)), nil
}

func (s *Server) handleGetIssue(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	identifier, _ := args["identifier"].(string)

	issue, err := s.store.GetIssue(identifier)
	if err != nil {
		// Try by identifier
		issue, err = s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			return toolError(fmt.Sprintf("Issue not found: %s", identifier)), nil
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**%s**: %s\n", issue.Identifier, issue.Title))
	sb.WriteString(fmt.Sprintf("State: %s | Priority: %d\n", issue.State, issue.Priority))
	if issue.Description != "" {
		sb.WriteString(fmt.Sprintf("\n%s\n", issue.Description))
	}
	if len(issue.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("\nLabels: %s\n", strings.Join(issue.Labels, ", ")))
	}
	if issue.PRURL != "" {
		sb.WriteString(fmt.Sprintf("\nPR: %s\n", issue.PRURL))
	}
	if len(issue.BlockedBy) > 0 {
		sb.WriteString(fmt.Sprintf("\nBlocked by: %s\n", strings.Join(issue.BlockedBy, ", ")))
	}

	return toolResult(sb.String()), nil
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
		return toolError(fmt.Sprintf("Failed to list issues: %v", err)), nil
	}

	if len(issues) == 0 {
		return toolResult("No issues found."), nil
	}

	var sb strings.Builder
	sb.WriteString("Issues:\n")
	for _, i := range issues {
		sb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", i.State, i.Identifier, i.Title))
	}
	return toolResult(sb.String()), nil
}

func (s *Server) handleUpdateIssue(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	identifier, _ := args["identifier"].(string)

	issue, err := s.store.GetIssue(identifier)
	if err != nil {
		issue, err = s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			return toolError(fmt.Sprintf("Issue not found: %s", identifier)), nil
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
		return toolResult("No updates provided"), nil
	}

	if err := s.store.UpdateIssue(issue.ID, updates); err != nil {
		return toolError(fmt.Sprintf("Failed to update issue: %v", err)), nil
	}

	return toolResult(fmt.Sprintf("Updated issue %s", issue.Identifier)), nil
}

func (s *Server) handleSetIssueState(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	identifier, _ := args["identifier"].(string)
	state, _ := args["state"].(string)

	issue, err := s.store.GetIssue(identifier)
	if err != nil {
		issue, err = s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			return toolError(fmt.Sprintf("Issue not found: %s", identifier)), nil
		}
	}

	if !kanban.State(state).IsValid() {
		return toolError(fmt.Sprintf("Invalid state: %s. Valid states: backlog, ready, in_progress, in_review, done, cancelled", state)), nil
	}

	if err := s.store.UpdateIssueState(issue.ID, kanban.State(state)); err != nil {
		return toolError(fmt.Sprintf("Failed to update issue state: %v", err)), nil
	}

	return toolResult(fmt.Sprintf("Issue %s state changed to %s", issue.Identifier, state)), nil
}

func (s *Server) handleDeleteIssue(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	identifier, _ := args["identifier"].(string)

	issue, err := s.store.GetIssue(identifier)
	if err != nil {
		issue, err = s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			return toolError(fmt.Sprintf("Issue not found: %s", identifier)), nil
		}
	}

	if err := s.store.DeleteIssue(issue.ID); err != nil {
		return toolError(fmt.Sprintf("Failed to delete issue: %v", err)), nil
	}

	return toolResult(fmt.Sprintf("Deleted issue %s", issue.Identifier)), nil
}

func (s *Server) handleBoardOverview(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	filter := make(map[string]interface{})
	if projectID, ok := args["project_id"].(string); ok {
		filter["project_id"] = projectID
	}

	issues, err := s.store.ListIssues(filter)
	if err != nil {
		return toolError(fmt.Sprintf("Failed to get board overview: %v", err)), nil
	}

	counts := make(map[kanban.State]int)
	for _, i := range issues {
		counts[i.State]++
	}

	result := fmt.Sprintf("Kanban Board Overview:\n\n[Backlog]     %d\n[Ready]       %d\n[In Progress] %d\n[In Review]   %d\n[Done]        %d\n[Cancelled]   %d\n",
		counts[kanban.StateBacklog],
		counts[kanban.StateReady],
		counts[kanban.StateInProgress],
		counts[kanban.StateInReview],
		counts[kanban.StateDone],
		counts[kanban.StateCancelled])

	return toolResult(result), nil
}

func (s *Server) handleSetBlockers(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
	identifier, _ := args["identifier"].(string)

	issue, err := s.store.GetIssue(identifier)
	if err != nil {
		issue, err = s.store.GetIssueByIdentifier(identifier)
		if err != nil {
			return toolError(fmt.Sprintf("Issue not found: %s", identifier)), nil
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

	updates := map[string]interface{}{"blocked_by": blockers}
	if err := s.store.UpdateIssue(issue.ID, updates); err != nil {
		return toolError(fmt.Sprintf("Failed to set blockers: %v", err)), nil
	}

	if len(blockers) == 0 {
		return toolResult(fmt.Sprintf("Cleared blockers for %s", issue.Identifier)), nil
	}
	return toolResult(fmt.Sprintf("Set blockers for %s: %s", issue.Identifier, strings.Join(blockers, ", "))), nil
}
