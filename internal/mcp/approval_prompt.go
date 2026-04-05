package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
)

type pendingInteractionRegistrar interface {
	RegisterPendingInteraction(issueID string, interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder) bool
	ClearPendingInteraction(issueID string, interactionID string)
}

type approvalPromptIssueContext struct {
	IssueID         string
	IssueIdentifier string
	IssueTitle      string
	ProjectID       string
	ProjectName     string
	WorkspacePath   string
}

type approvalPromptRequest struct {
	ToolName    string
	ToolUseID   string
	Input       map[string]interface{}
	RequestMeta map[string]interface{}
	Issue       approvalPromptIssueContext
}

type claudePermissionPromptResult struct {
	Behavior     string      `json:"behavior"`
	UpdatedInput interface{} `json:"updatedInput,omitempty"`
	Message      string      `json:"message,omitempty"`
}

func (s *Server) handleApprovalPrompt(ctx context.Context, request mcpapi.CallToolRequest) (*mcpapi.CallToolResult, error) {
	call, err := parseApprovalPromptCall(request)
	if err != nil {
		return s.toolError("approval_prompt", err.Error()), nil
	}

	interaction := buildApprovalPromptInteraction(call)
	if interaction == nil {
		return s.toolError("approval_prompt", "invalid approval prompt payload"), nil
	}
	if s.shouldAutoApproveApprovalPrompt(call) {
		return encodeApprovalPromptResult(interaction, agentruntime.PendingInteractionResponse{Decision: "allow"})
	}

	registrar, ok := s.provider.(pendingInteractionRegistrar)
	if !ok || registrar == nil {
		return s.toolError("approval_prompt", "runtime_unavailable: this Maestro MCP server was started without pending-interaction support"), nil
	}

	resultCh := make(chan claudePermissionPromptResult, 1)
	responder := func(respCtx context.Context, interactionID string, response agentruntime.PendingInteractionResponse) error {
		_ = respCtx
		registrar.ClearPendingInteraction(call.Issue.IssueID, interactionID)
		result, err := buildClaudePermissionPromptResult(*interaction, response)
		if err != nil {
			return err
		}
		select {
		case resultCh <- result:
		default:
		}
		return nil
	}

	if !registrar.RegisterPendingInteraction(call.Issue.IssueID, interaction, responder) {
		return s.toolError("approval_prompt", "runtime_unavailable: Maestro could not register the pending approval interaction"), nil
	}
	defer registrar.ClearPendingInteraction(call.Issue.IssueID, interaction.ID)

	select {
	case result := <-resultCh:
		body, err := json.Marshal(result)
		if err != nil {
			return s.toolError("approval_prompt", fmt.Sprintf("failed to encode approval response: %v", err)), nil
		}
		return &mcpapi.CallToolResult{
			Content: []mcpapi.Content{mcpapi.TextContent{
				Type: "text",
				Text: string(body),
			}},
		}, nil
	case <-ctx.Done():
		return s.toolError("approval_prompt", ctx.Err().Error()), nil
	}
}

func (s *Server) shouldAutoApproveApprovalPrompt(call *approvalPromptRequest) bool {
	if s == nil || s.store == nil || call == nil || strings.TrimSpace(call.Issue.IssueID) == "" {
		return false
	}

	issue, err := s.store.GetIssue(call.Issue.IssueID)
	if err != nil || issue == nil {
		return false
	}

	profile := kanban.NormalizePermissionProfile(string(issue.PermissionProfile))
	if profile == kanban.PermissionProfileFullAccess {
		return true
	}
	projectID := strings.TrimSpace(issue.ProjectID)
	if projectID == "" {
		return false
	}
	project, err := s.store.GetProject(projectID)
	if err != nil || project == nil {
		return false
	}
	return kanban.NormalizePermissionProfile(string(project.PermissionProfile)) == kanban.PermissionProfileFullAccess
}

func encodeApprovalPromptResult(interaction *agentruntime.PendingInteraction, response agentruntime.PendingInteractionResponse) (*mcpapi.CallToolResult, error) {
	if interaction == nil {
		return nil, fmt.Errorf("invalid approval prompt payload")
	}
	result, err := buildClaudePermissionPromptResult(*interaction, response)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to encode approval response: %w", err)
	}
	return &mcpapi.CallToolResult{
		Content: []mcpapi.Content{mcpapi.TextContent{
			Type: "text",
			Text: string(body),
		}},
	}, nil
}

func parseApprovalPromptCall(request mcpapi.CallToolRequest) (*approvalPromptRequest, error) {
	args := request.GetArguments()
	if len(args) == 0 {
		return nil, fmt.Errorf("approval_prompt requires a non-empty arguments object")
	}

	toolName := strings.TrimSpace(asString(args["tool_name"]))
	if toolName == "" {
		return nil, fmt.Errorf("approval_prompt requires tool_name")
	}

	input := objectArg(args, "input")
	if len(input) == 0 {
		return nil, fmt.Errorf("approval_prompt requires input")
	}

	meta := map[string]interface{}{}
	if request.Params.Meta != nil && len(request.Params.Meta.AdditionalFields) > 0 {
		meta = cloneJSONMap(request.Params.Meta.AdditionalFields)
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	issue := approvalPromptIssueContext{
		IssueID:         strings.TrimSpace(asString(meta["maestro/issue_id"])),
		IssueIdentifier: strings.TrimSpace(asString(meta["maestro/issue_identifier"])),
		IssueTitle:      strings.TrimSpace(asString(meta["maestro/issue_title"])),
		ProjectID:       strings.TrimSpace(asString(meta["maestro/project_id"])),
		ProjectName:     strings.TrimSpace(asString(meta["maestro/project_name"])),
		WorkspacePath:   strings.TrimSpace(asString(meta["maestro/workspace_path"])),
	}
	if issue.IssueID == "" {
		return nil, fmt.Errorf("approval_prompt requires maestro/issue_id metadata")
	}
	if issue.WorkspacePath == "" {
		return nil, fmt.Errorf("approval_prompt requires maestro/workspace_path metadata")
	}
	explicitToolUseID := strings.TrimSpace(asString(args["tool_use_id"]))
	metaToolUseID := approvalPromptToolUseIDFromMeta(meta)
	if explicitToolUseID != "" && metaToolUseID != "" && metaToolUseID != explicitToolUseID {
		return nil, fmt.Errorf("approval_prompt tool_use_id mismatch: arguments=%q meta=%q", explicitToolUseID, metaToolUseID)
	}
	toolUseID := explicitToolUseID
	if toolUseID == "" {
		toolUseID = metaToolUseID
	}
	if toolUseID == "" {
		toolUseID = approvalPromptFallbackToolUseID(issue, toolName, input)
	}
	if metaToolUseID := strings.TrimSpace(asString(meta["claudecode/toolUseId"])); metaToolUseID != "" && metaToolUseID != toolUseID {
		return nil, fmt.Errorf("approval_prompt tool_use_id mismatch: arguments=%q meta=%q", toolUseID, metaToolUseID)
	}
	if metaToolUseID := strings.TrimSpace(asString(meta["claude/toolUseId"])); metaToolUseID != "" && metaToolUseID != toolUseID {
		return nil, fmt.Errorf("approval_prompt tool_use_id mismatch: arguments=%q meta=%q", toolUseID, metaToolUseID)
	}
	if _, ok := meta["claudecode/toolUseId"]; !ok {
		meta["claudecode/toolUseId"] = toolUseID
	}
	if _, ok := meta["claude/toolUseId"]; !ok {
		meta["claude/toolUseId"] = toolUseID
	}

	return &approvalPromptRequest{
		ToolName:    toolName,
		ToolUseID:   toolUseID,
		Input:       cloneJSONMap(input),
		RequestMeta: meta,
		Issue:       issue,
	}, nil
}

func approvalPromptToolUseIDFromMeta(meta map[string]interface{}) string {
	if len(meta) == 0 {
		return ""
	}
	for _, key := range []string{"claudecode/toolUseId", "claude/toolUseId", "tool_use_id"} {
		if value := strings.TrimSpace(asString(meta[key])); value != "" {
			return value
		}
	}
	return ""
}

func approvalPromptFallbackToolUseID(issue approvalPromptIssueContext, toolName string, input map[string]interface{}) string {
	payload := map[string]interface{}{
		"issue_id":         issue.IssueID,
		"issue_identifier": issue.IssueIdentifier,
		"tool_name":        toolName,
		"input":            cloneJSONValue(input),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return strings.TrimSpace(toolName)
	}
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum[:8])
}

func buildApprovalPromptInteraction(call *approvalPromptRequest) *agentruntime.PendingInteraction {
	if call == nil {
		return nil
	}
	classification := classifyApprovalPrompt(call.ToolName, call.Input)
	command, reason, markdown := summarizeApprovalPrompt(call.ToolName, classification, call.Input, call.Issue.WorkspacePath)
	requestedAt := time.Now().UTC()
	interaction := &agentruntime.PendingInteraction{
		ID:              approvalPromptInteractionID(call.ToolUseID),
		Kind:            agentruntime.PendingInteractionKindApproval,
		Method:          "approval_prompt",
		RequestID:       call.ToolUseID,
		ItemID:          call.ToolUseID,
		IssueID:         call.Issue.IssueID,
		IssueIdentifier: call.Issue.IssueIdentifier,
		IssueTitle:      call.Issue.IssueTitle,
		RequestedAt:     requestedAt,
		LastActivityAt:  &requestedAt,
		LastActivity:    reason,
		ProjectID:       call.Issue.ProjectID,
		ProjectName:     call.Issue.ProjectName,
		Approval: &agentruntime.PendingApproval{
			Command:  command,
			CWD:      call.Issue.WorkspacePath,
			Reason:   reason,
			Markdown: markdown,
			Decisions: []agentruntime.PendingApprovalDecision{
				{
					Value:       "allow",
					Label:       "Allow once",
					Description: "Allow this request once and continue the turn.",
				},
				{
					Value:       "deny",
					Label:       "Deny",
					Description: "Deny this request and let the turn continue without it.",
				},
				{
					Value:       "ask",
					Label:       "Ask for clarification",
					Description: "Pause the turn and ask Claude for more information.",
				},
			},
		},
		Metadata: map[string]interface{}{
			"source":         "claude_permission_prompt",
			"tool_name":      call.ToolName,
			"tool_use_id":    call.ToolUseID,
			"classification": classification,
			"workspace_path": call.Issue.WorkspacePath,
			"input":          cloneJSONValue(call.Input),
			"request_meta":   cloneJSONMap(call.RequestMeta),
		},
	}
	return interaction
}

func buildClaudePermissionPromptResult(interaction agentruntime.PendingInteraction, response agentruntime.PendingInteractionResponse) (claudePermissionPromptResult, error) {
	decision := strings.ToLower(strings.TrimSpace(response.Decision))
	if decision == "" {
		decision = strings.ToLower(strings.TrimSpace(response.Action))
	}

	switch decision {
	case "allow":
		return claudePermissionPromptResult{
			Behavior:     "allow",
			UpdatedInput: cloneJSONValue(interaction.Metadata["input"]),
		}, nil
	case "deny":
		return claudePermissionPromptResult{
			Behavior: "deny",
			Message:  approvalPromptDecisionMessage("deny", interaction, response),
		}, nil
	case "ask":
		return claudePermissionPromptResult{
			Behavior: "ask",
			Message:  approvalPromptDecisionMessage("ask", interaction, response),
		}, nil
	default:
		return claudePermissionPromptResult{}, fmt.Errorf("%w: unsupported approval decision %q", agentruntime.ErrInvalidInteractionResponse, response.Decision)
	}
}

func approvalPromptDecisionMessage(behavior string, interaction agentruntime.PendingInteraction, response agentruntime.PendingInteractionResponse) string {
	note := strings.TrimSpace(response.Note)
	if note != "" {
		return note
	}
	reason := ""
	if interaction.Approval != nil {
		reason = strings.TrimSpace(interaction.Approval.Reason)
	}
	if reason == "" {
		reason = strings.TrimSpace(interaction.LastActivity)
	}

	switch behavior {
	case "deny":
		if reason != "" {
			return "Maestro denied the request: " + reason
		}
		return "Maestro denied the request."
	case "ask":
		if reason != "" {
			return "Maestro needs clarification before approving this request: " + reason
		}
		return "Maestro needs clarification before approving this request."
	default:
		return ""
	}
}

func classifyApprovalPrompt(toolName string, input map[string]interface{}) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "bash":
		return "command"
	case "write":
		if approvalPromptProtectedPath(approvalPromptTargetPath(input)) {
			return "protected_directory_write"
		}
		return "file_write"
	case "edit", "multiedit":
		if approvalPromptProtectedPath(approvalPromptTargetPath(input)) {
			return "protected_directory_write"
		}
		return "file_edit"
	default:
		return "approval"
	}
}

func summarizeApprovalPrompt(toolName, classification string, input map[string]interface{}, workspacePath string) (string, string, string) {
	targetPath := strings.TrimSpace(approvalPromptTargetPath(input))
	command := strings.TrimSpace(approvalPromptCommand(toolName, input, targetPath))
	reason := strings.TrimSpace(approvalPromptReason(toolName, classification, targetPath, input))
	if reason == "" {
		reason = "Claude requested approval."
	}

	inputJSON, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		inputJSON = []byte(fmt.Sprintf("%#v", input))
	}

	lines := []string{
		"### Claude permission prompt",
		"",
		"- Tool: `" + toolName + "`",
		"- Classification: `" + classification + "`",
		"- Workspace: `" + workspacePath + "`",
	}
	if command != "" {
		lines = append(lines, "- Command: `"+command+"`")
	}
	lines = append(lines,
		"- Input:",
		"```json",
		string(inputJSON),
		"```",
	)
	return command, reason, strings.Join(lines, "\n")
}

func approvalPromptReason(toolName, classification, targetPath string, input map[string]interface{}) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "bash":
		command := strings.TrimSpace(asString(input["command"]))
		if command != "" {
			return "Claude requested command approval: " + command
		}
		return "Claude requested command approval."
	case "write":
		if targetPath != "" {
			if classification == "protected_directory_write" {
				return "Claude requested a protected-directory write: " + targetPath
			}
			return "Claude requested a file write: " + targetPath
		}
		return "Claude requested a file write."
	case "edit", "multiedit":
		if targetPath != "" {
			if classification == "protected_directory_write" {
				return "Claude requested a protected-directory edit: " + targetPath
			}
			return "Claude requested a file edit: " + targetPath
		}
		return "Claude requested a file edit."
	default:
		if targetPath != "" {
			return "Claude requested approval for " + targetPath
		}
		return "Claude requested approval."
	}
}

func approvalPromptCommand(toolName string, input map[string]interface{}, targetPath string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "bash":
		return strings.TrimSpace(asString(input["command"]))
	case "write":
		if targetPath != "" {
			return "Write " + targetPath
		}
		return "Write"
	case "edit", "multiedit":
		label := "Edit"
		if strings.EqualFold(toolName, "MultiEdit") {
			label = "MultiEdit"
		}
		if targetPath != "" {
			return label + " " + targetPath
		}
		return label
	default:
		if targetPath != "" {
			return strings.TrimSpace(toolName) + " " + targetPath
		}
		return strings.TrimSpace(toolName)
	}
}

func approvalPromptTargetPath(input map[string]interface{}) string {
	if input == nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "target_path"} {
		if path := strings.TrimSpace(asString(input[key])); path != "" {
			return path
		}
	}
	return ""
}

func approvalPromptProtectedPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	cleaned := filepath.Clean(path)
	parts := strings.Split(cleaned, string(filepath.Separator))
	for _, part := range parts {
		if part == ".git" {
			return true
		}
	}
	return false
}

func approvalPromptInteractionID(toolUseID string) string {
	return "approval-prompt-" + strings.TrimSpace(toolUseID)
}
