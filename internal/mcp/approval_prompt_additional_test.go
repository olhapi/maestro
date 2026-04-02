package mcp

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
)

type approvalPromptRejectingProvider struct {
	testRuntimeProvider
}

func (p approvalPromptRejectingProvider) RegisterPendingInteraction(issueID string, interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder) bool {
	return false
}

func (p approvalPromptRejectingProvider) ClearPendingInteraction(issueID string, interactionID string) {
}

type approvalPromptSilentProvider struct {
	testRuntimeProvider
}

func (p approvalPromptSilentProvider) RegisterPendingInteraction(issueID string, interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder) bool {
	return true
}

func (p approvalPromptSilentProvider) ClearPendingInteraction(issueID string, interactionID string) {}

func TestApprovalPromptHelperBranches(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")

	cases := []struct {
		name               string
		toolName           string
		input              map[string]interface{}
		wantTarget         string
		wantClassification string
		wantCommand        string
		wantReason         string
		wantProtected      bool
	}{
		{
			name:               "bash command",
			toolName:           "Bash",
			input:              map[string]interface{}{"command": "pwd && git status --short"},
			wantClassification: "command",
			wantCommand:        "pwd && git status --short",
			wantReason:         "Claude requested command approval: pwd && git status --short",
		},
		{
			name:               "bash empty command",
			toolName:           "Bash",
			input:              map[string]interface{}{},
			wantClassification: "command",
			wantCommand:        "",
			wantReason:         "Claude requested command approval.",
		},
		{
			name:               "write regular file_path",
			toolName:           "Write",
			input:              map[string]interface{}{"file_path": filepath.Join(workspace, "notes.txt")},
			wantTarget:         filepath.Join(workspace, "notes.txt"),
			wantClassification: "file_write",
			wantCommand:        "Write " + filepath.Join(workspace, "notes.txt"),
			wantReason:         "Claude requested a file write: " + filepath.Join(workspace, "notes.txt"),
		},
		{
			name:               "write protected path",
			toolName:           "Write",
			input:              map[string]interface{}{"path": filepath.Join(workspace, ".git", "config")},
			wantTarget:         filepath.Join(workspace, ".git", "config"),
			wantClassification: "protected_directory_write",
			wantCommand:        "Write " + filepath.Join(workspace, ".git", "config"),
			wantReason:         "Claude requested a protected-directory write: " + filepath.Join(workspace, ".git", "config"),
			wantProtected:      true,
		},
		{
			name:               "edit protected target_path",
			toolName:           "Edit",
			input:              map[string]interface{}{"target_path": filepath.Join(workspace, ".git", "hooks", "pre-commit")},
			wantTarget:         filepath.Join(workspace, ".git", "hooks", "pre-commit"),
			wantClassification: "protected_directory_write",
			wantCommand:        "Edit " + filepath.Join(workspace, ".git", "hooks", "pre-commit"),
			wantReason:         "Claude requested a protected-directory edit: " + filepath.Join(workspace, ".git", "hooks", "pre-commit"),
			wantProtected:      true,
		},
		{
			name:               "multiedit regular",
			toolName:           "MultiEdit",
			input:              map[string]interface{}{"file_path": filepath.Join(workspace, "plan.md")},
			wantTarget:         filepath.Join(workspace, "plan.md"),
			wantClassification: "file_edit",
			wantCommand:        "MultiEdit " + filepath.Join(workspace, "plan.md"),
			wantReason:         "Claude requested a file edit: " + filepath.Join(workspace, "plan.md"),
		},
		{
			name:               "default with target",
			toolName:           "CustomTool",
			input:              map[string]interface{}{"file_path": "/tmp/target"},
			wantTarget:         "/tmp/target",
			wantClassification: "approval",
			wantCommand:        "CustomTool /tmp/target",
			wantReason:         "Claude requested approval for /tmp/target",
		},
		{
			name:               "default without target",
			toolName:           "CustomTool",
			input:              map[string]interface{}{},
			wantClassification: "approval",
			wantCommand:        "CustomTool",
			wantReason:         "Claude requested approval.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := approvalPromptTargetPath(tc.input)
			if target != tc.wantTarget {
				t.Fatalf("unexpected target path: got %q want %q", target, tc.wantTarget)
			}
			if got := approvalPromptProtectedPath(target); got != tc.wantProtected {
				t.Fatalf("unexpected protected-path classification: got %t want %t", got, tc.wantProtected)
			}

			classification := classifyApprovalPrompt(tc.toolName, tc.input)
			if classification != tc.wantClassification {
				t.Fatalf("unexpected classification: got %q want %q", classification, tc.wantClassification)
			}

			if got := approvalPromptCommand(tc.toolName, tc.input, target); got != tc.wantCommand {
				t.Fatalf("unexpected command: got %q want %q", got, tc.wantCommand)
			}
			if got := approvalPromptReason(tc.toolName, classification, target, tc.input); got != tc.wantReason {
				t.Fatalf("unexpected reason: got %q want %q", got, tc.wantReason)
			}

			command, reason, markdown := summarizeApprovalPrompt(tc.toolName, classification, tc.input, workspace)
			if command != tc.wantCommand {
				t.Fatalf("unexpected summarized command: got %q want %q", command, tc.wantCommand)
			}
			if reason != tc.wantReason {
				t.Fatalf("unexpected summarized reason: got %q want %q", reason, tc.wantReason)
			}
			if !strings.Contains(markdown, "### Claude permission prompt") {
				t.Fatalf("expected markdown heading, got %q", markdown)
			}
			if !strings.Contains(markdown, "- Tool: `"+tc.toolName+"`") {
				t.Fatalf("expected markdown to include the tool name, got %q", markdown)
			}
			if !strings.Contains(markdown, "- Classification: `"+classification+"`") {
				t.Fatalf("expected markdown to include the classification, got %q", markdown)
			}
			if !strings.Contains(markdown, "- Workspace: `"+workspace+"`") {
				t.Fatalf("expected markdown to include the workspace, got %q", markdown)
			}
		})
	}

	if got := approvalPromptTargetPath(nil); got != "" {
		t.Fatalf("expected nil input to return empty target path, got %q", got)
	}
	if got := approvalPromptProtectedPath(""); got {
		t.Fatalf("expected empty path to be treated as non-protected")
	}
}

func TestApprovalPromptSummarizeFallbackAndDecisionMessages(t *testing.T) {
	commandInput := map[string]interface{}{
		"command": "pwd",
		"bad":     make(chan int),
	}
	command, reason, markdown := summarizeApprovalPrompt("Bash", "command", commandInput, "/tmp/workspace")
	if command != "pwd" {
		t.Fatalf("unexpected summarized command: got %q want %q", command, "pwd")
	}
	if reason != "Claude requested command approval: pwd" {
		t.Fatalf("unexpected summarized reason: got %q", reason)
	}
	if !strings.Contains(markdown, "map[string]interface {}") {
		t.Fatalf("expected marshal fallback markdown to use fmt formatting, got %q", markdown)
	}
	if !strings.Contains(markdown, "chan int") {
		t.Fatalf("expected marshal fallback markdown to include the channel type, got %q", markdown)
	}

	cases := []struct {
		name        string
		behavior    string
		interaction agentruntime.PendingInteraction
		response    agentruntime.PendingInteractionResponse
		wantMessage string
	}{
		{
			name:        "deny note wins",
			behavior:    "deny",
			interaction: agentruntime.PendingInteraction{Approval: &agentruntime.PendingApproval{Reason: "approval reason"}},
			response:    agentruntime.PendingInteractionResponse{Note: "custom deny"},
			wantMessage: "custom deny",
		},
		{
			name:        "deny approval reason fallback",
			behavior:    "deny",
			interaction: agentruntime.PendingInteraction{Approval: &agentruntime.PendingApproval{Reason: "approval reason"}},
			response:    agentruntime.PendingInteractionResponse{},
			wantMessage: "Maestro denied the request: approval reason",
		},
		{
			name:        "ask last activity fallback",
			behavior:    "ask",
			interaction: agentruntime.PendingInteraction{LastActivity: "last activity"},
			response:    agentruntime.PendingInteractionResponse{},
			wantMessage: "Maestro needs clarification before approving this request: last activity",
		},
		{
			name:        "deny generic fallback",
			behavior:    "deny",
			interaction: agentruntime.PendingInteraction{},
			response:    agentruntime.PendingInteractionResponse{},
			wantMessage: "Maestro denied the request.",
		},
		{
			name:        "ask generic fallback",
			behavior:    "ask",
			interaction: agentruntime.PendingInteraction{},
			response:    agentruntime.PendingInteractionResponse{},
			wantMessage: "Maestro needs clarification before approving this request.",
		},
		{
			name:        "unsupported behavior",
			behavior:    "maybe",
			interaction: agentruntime.PendingInteraction{},
			response:    agentruntime.PendingInteractionResponse{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			switch tc.behavior {
			case "deny", "ask":
				got := approvalPromptDecisionMessage(tc.behavior, tc.interaction, tc.response)
				if got != tc.wantMessage {
					t.Fatalf("unexpected decision message: got %q want %q", got, tc.wantMessage)
				}
			default:
				if got := approvalPromptDecisionMessage(tc.behavior, tc.interaction, tc.response); got != "" {
					t.Fatalf("expected unsupported behavior to return empty message, got %q", got)
				}
			}
		})
	}

	buildCases := []struct {
		name         string
		interaction  agentruntime.PendingInteraction
		response     agentruntime.PendingInteractionResponse
		wantBehavior string
		wantUpdated  map[string]interface{}
		wantMessage  string
		wantError    bool
	}{
		{
			name: "allow decision clones input",
			interaction: agentruntime.PendingInteraction{
				Metadata: map[string]interface{}{
					"input": map[string]interface{}{
						"command": "pwd",
						"nested": []interface{}{
							map[string]interface{}{"k": "v"},
						},
					},
				},
			},
			response:     agentruntime.PendingInteractionResponse{Decision: "allow"},
			wantBehavior: "allow",
			wantUpdated:  map[string]interface{}{"command": "pwd", "nested": []interface{}{map[string]interface{}{"k": "v"}}},
		},
		{
			name: "allow action fallback",
			interaction: agentruntime.PendingInteraction{
				Metadata: map[string]interface{}{
					"input": map[string]interface{}{"command": "whoami"},
				},
			},
			response:     agentruntime.PendingInteractionResponse{Action: "allow"},
			wantBehavior: "allow",
			wantUpdated:  map[string]interface{}{"command": "whoami"},
		},
		{
			name: "deny uses decision message",
			interaction: agentruntime.PendingInteraction{
				Approval: &agentruntime.PendingApproval{Reason: "approval reason"},
				Metadata: map[string]interface{}{"input": map[string]interface{}{"command": "pwd"}},
			},
			response:     agentruntime.PendingInteractionResponse{Decision: "deny"},
			wantBehavior: "deny",
			wantMessage:  "Maestro denied the request: approval reason",
		},
		{
			name: "ask uses decision message",
			interaction: agentruntime.PendingInteraction{
				LastActivity: "last activity",
				Metadata:     map[string]interface{}{"input": map[string]interface{}{"command": "pwd"}},
			},
			response:     agentruntime.PendingInteractionResponse{Decision: "ask"},
			wantBehavior: "ask",
			wantMessage:  "Maestro needs clarification before approving this request: last activity",
		},
		{
			name:        "unsupported decision",
			interaction: agentruntime.PendingInteraction{},
			response:    agentruntime.PendingInteractionResponse{Decision: "maybe"},
			wantError:   true,
		},
	}

	for _, tc := range buildCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := buildClaudePermissionPromptResult(tc.interaction, tc.response)
			if tc.wantError {
				if err == nil {
					t.Fatal("expected unsupported decision to return an error")
				}
				if !strings.Contains(err.Error(), "unsupported approval decision") {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildClaudePermissionPromptResult failed: %v", err)
			}
			if result.Behavior != tc.wantBehavior {
				t.Fatalf("unexpected behavior: got %q want %q", result.Behavior, tc.wantBehavior)
			}
			if tc.wantBehavior == "allow" {
				updatedInput, ok := result.UpdatedInput.(map[string]interface{})
				if !ok {
					t.Fatalf("expected allow result to include updated input, got %#v", result.UpdatedInput)
				}
				if !reflect.DeepEqual(updatedInput, tc.wantUpdated) {
					t.Fatalf("unexpected updated input: got %#v want %#v", updatedInput, tc.wantUpdated)
				}
			}
			if tc.wantBehavior == "deny" || tc.wantBehavior == "ask" {
				if result.Message != tc.wantMessage {
					t.Fatalf("unexpected message: got %q want %q", result.Message, tc.wantMessage)
				}
			}
		})
	}

	if _, err := encodeApprovalPromptResult(nil, agentruntime.PendingInteractionResponse{Decision: "allow"}); err == nil || !strings.Contains(err.Error(), "invalid approval prompt payload") {
		t.Fatalf("expected nil interaction to fail encoding, got %v", err)
	}

	if got := approvalPromptFallbackToolUseID(approvalPromptIssueContext{}, "  Bash  ", map[string]interface{}{"bad": make(chan int)}); got != "Bash" {
		t.Fatalf("expected marshal failure to fall back to trimmed tool name, got %q", got)
	}
}

func TestApprovalPromptToolUseIDFromMetaPrecedence(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]interface{}
		want string
	}{
		{
			name: "claudecode wins",
			meta: map[string]interface{}{
				"claudecode/toolUseId": "  claudecode-id  ",
				"claude/toolUseId":     "claude-id",
				"tool_use_id":          "legacy-id",
			},
			want: "claudecode-id",
		},
		{
			name: "claude fallback",
			meta: map[string]interface{}{
				"claude/toolUseId": "  claude-id  ",
				"tool_use_id":      "legacy-id",
			},
			want: "claude-id",
		},
		{
			name: "legacy fallback",
			meta: map[string]interface{}{
				"tool_use_id": "  legacy-id  ",
			},
			want: "legacy-id",
		},
		{
			name: "empty",
			meta: map[string]interface{}{},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := approvalPromptToolUseIDFromMeta(tc.meta); got != tc.want {
				t.Fatalf("unexpected tool use id: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestApprovalPromptParseCallBranches(t *testing.T) {
	_, _, project, issue, workspace := newApprovalPromptTestServer(t)

	baseMeta := map[string]interface{}{
		"maestro/issue_id":         issue.ID,
		"maestro/issue_identifier": issue.Identifier,
		"maestro/issue_title":      issue.Title,
		"maestro/project_id":       project.ID,
		"maestro/project_name":     project.Name,
		"maestro/workspace_path":   workspace,
	}

	t.Run("missing args", func(t *testing.T) {
		request := mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{Name: "approval_prompt"},
		}
		if _, err := parseApprovalPromptCall(request); err == nil || !strings.Contains(err.Error(), "non-empty arguments object") {
			t.Fatalf("expected missing args error, got %v", err)
		}
	})

	t.Run("missing tool name", func(t *testing.T) {
		request := mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{
				Name:      "approval_prompt",
				Arguments: map[string]interface{}{"input": map[string]interface{}{"command": "pwd"}},
				Meta:      mcpapi.NewMetaFromMap(baseMeta),
			},
		}
		if _, err := parseApprovalPromptCall(request); err == nil || !strings.Contains(err.Error(), "requires tool_name") {
			t.Fatalf("expected missing tool name error, got %v", err)
		}
	})

	t.Run("missing input", func(t *testing.T) {
		request := mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{
				Name:      "approval_prompt",
				Arguments: map[string]interface{}{"tool_name": "Bash"},
				Meta:      mcpapi.NewMetaFromMap(baseMeta),
			},
		}
		if _, err := parseApprovalPromptCall(request); err == nil || !strings.Contains(err.Error(), "requires input") {
			t.Fatalf("expected missing input error, got %v", err)
		}
	})

	t.Run("missing issue metadata", func(t *testing.T) {
		meta := map[string]interface{}{
			"maestro/project_id":     project.ID,
			"maestro/project_name":   project.Name,
			"maestro/workspace_path": workspace,
		}
		request := mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{
				Name:      "approval_prompt",
				Arguments: map[string]interface{}{"tool_name": "Bash", "input": map[string]interface{}{"command": "pwd"}},
				Meta:      mcpapi.NewMetaFromMap(meta),
			},
		}
		if _, err := parseApprovalPromptCall(request); err == nil || !strings.Contains(err.Error(), "requires maestro/issue_id metadata") {
			t.Fatalf("expected missing issue metadata error, got %v", err)
		}
	})

	t.Run("missing workspace metadata", func(t *testing.T) {
		meta := map[string]interface{}{
			"maestro/issue_id":         issue.ID,
			"maestro/issue_identifier": issue.Identifier,
			"maestro/issue_title":      issue.Title,
			"maestro/project_id":       project.ID,
			"maestro/project_name":     project.Name,
		}
		request := mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{
				Name:      "approval_prompt",
				Arguments: map[string]interface{}{"tool_name": "Bash", "input": map[string]interface{}{"command": "pwd"}},
				Meta:      mcpapi.NewMetaFromMap(meta),
			},
		}
		if _, err := parseApprovalPromptCall(request); err == nil || !strings.Contains(err.Error(), "requires maestro/workspace_path metadata") {
			t.Fatalf("expected missing workspace metadata error, got %v", err)
		}
	})

	t.Run("explicit tool use id mismatch with meta claudecode", func(t *testing.T) {
		meta := cloneJSONMap(baseMeta)
		meta["claudecode/toolUseId"] = "meta-id"
		request := mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{
				Name:      "approval_prompt",
				Arguments: map[string]interface{}{"tool_name": "Bash", "input": map[string]interface{}{"command": "pwd"}, "tool_use_id": "arg-id"},
				Meta:      mcpapi.NewMetaFromMap(meta),
			},
		}
		if _, err := parseApprovalPromptCall(request); err == nil || !strings.Contains(err.Error(), "tool_use_id mismatch") {
			t.Fatalf("expected explicit/meta mismatch error, got %v", err)
		}
	})

	t.Run("explicit tool use id mismatch with meta claude", func(t *testing.T) {
		meta := cloneJSONMap(baseMeta)
		meta["claudecode/toolUseId"] = "arg-id"
		meta["claude/toolUseId"] = "meta-id"
		request := mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{
				Name:      "approval_prompt",
				Arguments: map[string]interface{}{"tool_name": "Bash", "input": map[string]interface{}{"command": "pwd"}, "tool_use_id": "arg-id"},
				Meta:      mcpapi.NewMetaFromMap(meta),
			},
		}
		if _, err := parseApprovalPromptCall(request); err == nil || !strings.Contains(err.Error(), "tool_use_id mismatch") {
			t.Fatalf("expected legacy tool-use mismatch error, got %v", err)
		}
	})

	t.Run("explicit tool use id clones request metadata", func(t *testing.T) {
		nested := map[string]interface{}{
			"nested": map[string]interface{}{"slice": []interface{}{"a", map[string]interface{}{"b": "c"}}},
		}
		input := map[string]interface{}{"command": "pwd"}
		meta := cloneJSONMap(baseMeta)
		meta["extra"] = nested

		request := mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{
				Name:      "approval_prompt",
				Arguments: map[string]interface{}{"tool_name": "Bash", "input": input, "tool_use_id": "arg-id"},
				Meta:      mcpapi.NewMetaFromMap(meta),
			},
		}

		parsed, err := parseApprovalPromptCall(request)
		if err != nil {
			t.Fatalf("parseApprovalPromptCall failed: %v", err)
		}
		if parsed.ToolUseID != "arg-id" {
			t.Fatalf("unexpected tool use id: got %q want %q", parsed.ToolUseID, "arg-id")
		}
		if got := parsed.RequestMeta["claudecode/toolUseId"]; got != "arg-id" {
			t.Fatalf("expected claudecode tool-use id to be injected, got %#v", got)
		}
		if got := parsed.RequestMeta["claude/toolUseId"]; got != "arg-id" {
			t.Fatalf("expected legacy tool-use id to be injected, got %#v", got)
		}
		if got := parsed.RequestMeta["extra"]; !reflect.DeepEqual(got, nested) {
			t.Fatalf("unexpected nested request metadata: got %#v want %#v", got, nested)
		}

		input["command"] = "mutated"
		nested["nested"].(map[string]interface{})["slice"].([]interface{})[0] = "changed"
		if got := parsed.Input["command"]; got != "pwd" {
			t.Fatalf("expected parsed input to be cloned, got %#v", got)
		}
		if got := parsed.RequestMeta["extra"]; !reflect.DeepEqual(got, map[string]interface{}{"nested": map[string]interface{}{"slice": []interface{}{"a", map[string]interface{}{"b": "c"}}}}) {
			t.Fatalf("expected parsed meta to be cloned, got %#v", got)
		}
	})

	t.Run("meta claudecode tool use id", func(t *testing.T) {
		meta := cloneJSONMap(baseMeta)
		meta["claudecode/toolUseId"] = "claudecode-id"
		request := mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{
				Name:      "approval_prompt",
				Arguments: map[string]interface{}{"tool_name": "Bash", "input": map[string]interface{}{"command": "pwd"}},
				Meta:      mcpapi.NewMetaFromMap(meta),
			},
		}
		parsed, err := parseApprovalPromptCall(request)
		if err != nil {
			t.Fatalf("parseApprovalPromptCall failed: %v", err)
		}
		if parsed.ToolUseID != "claudecode-id" {
			t.Fatalf("unexpected tool use id: got %q want %q", parsed.ToolUseID, "claudecode-id")
		}
	})

	t.Run("meta claude tool use id fallback", func(t *testing.T) {
		meta := cloneJSONMap(baseMeta)
		meta["claude/toolUseId"] = "claude-id"
		meta["tool_use_id"] = "legacy-id"
		request := mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{
				Name:      "approval_prompt",
				Arguments: map[string]interface{}{"tool_name": "Bash", "input": map[string]interface{}{"command": "pwd"}},
				Meta:      mcpapi.NewMetaFromMap(meta),
			},
		}
		parsed, err := parseApprovalPromptCall(request)
		if err != nil {
			t.Fatalf("parseApprovalPromptCall failed: %v", err)
		}
		if parsed.ToolUseID != "claude-id" {
			t.Fatalf("unexpected tool use id: got %q want %q", parsed.ToolUseID, "claude-id")
		}
	})
}

func TestApprovalPromptShouldAutoApproveBranches(t *testing.T) {
	defaultServer, _, _, defaultIssue, _ := newApprovalPromptTestServer(t)

	issueFullAccessServer, _, _, issueFullAccessIssue, _ := newApprovalPromptTestServer(t)
	if err := issueFullAccessServer.store.UpdateIssuePermissionProfile(issueFullAccessIssue.ID, kanban.PermissionProfileFullAccess); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile: %v", err)
	}

	projectFullAccessServer, _, projectFullAccessProject, projectFullAccessIssue, _ := newApprovalPromptTestServer(t)
	if err := projectFullAccessServer.store.UpdateProjectPermissionProfile(projectFullAccessProject.ID, kanban.PermissionProfileFullAccess); err != nil {
		t.Fatalf("UpdateProjectPermissionProfile: %v", err)
	}

	blankProjectServer, _, _, blankProjectIssue, _ := newApprovalPromptTestServer(t)
	if err := blankProjectServer.store.UpdateIssue(blankProjectIssue.ID, map[string]interface{}{"project_id": ""}); err != nil {
		t.Fatalf("UpdateIssue clear project_id: %v", err)
	}

	cases := []struct {
		name   string
		server *Server
		call   *approvalPromptRequest
		want   bool
	}{
		{
			name:   "nil server",
			server: nil,
			call:   &approvalPromptRequest{Issue: approvalPromptIssueContext{IssueID: defaultIssue.ID}},
			want:   false,
		},
		{
			name:   "nil store",
			server: &Server{},
			call:   &approvalPromptRequest{Issue: approvalPromptIssueContext{IssueID: defaultIssue.ID}},
			want:   false,
		},
		{
			name:   "nil call",
			server: defaultServer,
			call:   nil,
			want:   false,
		},
		{
			name:   "blank issue id",
			server: defaultServer,
			call:   &approvalPromptRequest{},
			want:   false,
		},
		{
			name:   "missing issue",
			server: defaultServer,
			call:   &approvalPromptRequest{Issue: approvalPromptIssueContext{IssueID: "missing"}},
			want:   false,
		},
		{
			name:   "issue full access",
			server: issueFullAccessServer,
			call:   &approvalPromptRequest{Issue: approvalPromptIssueContext{IssueID: issueFullAccessIssue.ID}},
			want:   true,
		},
		{
			name:   "project full access",
			server: projectFullAccessServer,
			call:   &approvalPromptRequest{Issue: approvalPromptIssueContext{IssueID: projectFullAccessIssue.ID}},
			want:   true,
		},
		{
			name:   "project id cleared",
			server: blankProjectServer,
			call:   &approvalPromptRequest{Issue: approvalPromptIssueContext{IssueID: blankProjectIssue.ID}},
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.server.shouldAutoApproveApprovalPrompt(tc.call); got != tc.want {
				t.Fatalf("unexpected auto-approve decision: got %t want %t", got, tc.want)
			}
		})
	}
}

func TestApprovalPromptHandleBranches(t *testing.T) {
	t.Run("parse error", func(t *testing.T) {
		server, _, _, _, _ := newApprovalPromptTestServer(t)
		result, err := server.handleApprovalPrompt(context.Background(), mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{Name: "approval_prompt"},
		})
		if err != nil {
			t.Fatalf("handleApprovalPrompt failed: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected parse error to return an error envelope, got %#v", result)
		}
		envelope := decodeEnvelope(t, result)
		errorPayload, ok := envelope["error"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected error payload, got %#v", envelope)
		}
		if got := asString(errorPayload["message"]); !strings.Contains(got, "non-empty arguments object") {
			t.Fatalf("unexpected parse error message: %q", got)
		}
	})

	t.Run("unsupported registrar", func(t *testing.T) {
		server, _, project, issue, workspace := newApprovalPromptTestServer(t)
		server = NewServerWithProvider(server.store, testRuntimeProvider{store: server.store})
		request := approvalPromptCallRequest(t, project, issue, workspace, "toolu_unsupported", "Bash", map[string]interface{}{"command": "pwd"})
		result, err := server.handleApprovalPrompt(context.Background(), request)
		if err != nil {
			t.Fatalf("handleApprovalPrompt failed: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected unsupported registrar to return an error envelope, got %#v", result)
		}
		envelope := decodeEnvelope(t, result)
		errorPayload, ok := envelope["error"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected error payload, got %#v", envelope)
		}
		if got := asString(errorPayload["message"]); !strings.Contains(got, "pending-interaction support") {
			t.Fatalf("unexpected registrar error message: %q", got)
		}
	})

	t.Run("register failure", func(t *testing.T) {
		server, _, project, issue, workspace := newApprovalPromptTestServer(t)
		server = NewServerWithProvider(server.store, approvalPromptRejectingProvider{testRuntimeProvider: testRuntimeProvider{store: server.store}})
		request := approvalPromptCallRequest(t, project, issue, workspace, "toolu_reject", "Bash", map[string]interface{}{"command": "pwd"})
		result, err := server.handleApprovalPrompt(context.Background(), request)
		if err != nil {
			t.Fatalf("handleApprovalPrompt failed: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected register failure to return an error envelope, got %#v", result)
		}
		envelope := decodeEnvelope(t, result)
		errorPayload, ok := envelope["error"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected error payload, got %#v", envelope)
		}
		if got := asString(errorPayload["message"]); !strings.Contains(got, "could not register") {
			t.Fatalf("unexpected register failure message: %q", got)
		}
	})

	t.Run("timeout waiting for responder", func(t *testing.T) {
		server, _, project, issue, workspace := newApprovalPromptTestServer(t)
		server = NewServerWithProvider(server.store, approvalPromptSilentProvider{testRuntimeProvider: testRuntimeProvider{store: server.store}})
		request := approvalPromptCallRequest(t, project, issue, workspace, "toolu_timeout", "Bash", map[string]interface{}{"command": "pwd"})

		resultCh := make(chan *mcpapi.CallToolResult, 1)
		errCh := make(chan error, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		go func() {
			result, err := server.handleApprovalPrompt(ctx, request)
			if err != nil {
				errCh <- err
				return
			}
			resultCh <- result
		}()

		result := awaitApprovalPromptResult(t, resultCh, errCh)
		if !result.IsError {
			t.Fatalf("expected timeout to return an error envelope, got %#v", result)
		}
		envelope := decodeEnvelope(t, result)
		errorPayload, ok := envelope["error"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected error payload, got %#v", envelope)
		}
		if got := asString(errorPayload["message"]); !strings.Contains(got, context.DeadlineExceeded.Error()) {
			t.Fatalf("unexpected timeout message: %q", got)
		}
	})
}

func TestApprovalPromptCloneHelpers(t *testing.T) {
	if got := cloneJSONMap(nil); got != nil {
		t.Fatalf("expected nil map clone to stay nil, got %#v", got)
	}
	if got := cloneJSONMap(map[string]interface{}{}); got != nil {
		t.Fatalf("expected empty map clone to return nil, got %#v", got)
	}

	original := map[string]interface{}{
		"nested": map[string]interface{}{
			"slice": []interface{}{
				map[string]interface{}{"k": "v"},
			},
		},
	}
	cloned, ok := cloneJSONValue(original).(map[string]interface{})
	if !ok {
		t.Fatalf("expected cloned value to be a map, got %T", cloneJSONValue(original))
	}
	if !reflect.DeepEqual(cloned, original) {
		t.Fatalf("expected clone to match original, got %#v want %#v", cloned, original)
	}

	original["nested"].(map[string]interface{})["slice"].([]interface{})[0].(map[string]interface{})["k"] = "changed"
	if got := cloned["nested"].(map[string]interface{})["slice"].([]interface{})[0].(map[string]interface{})["k"]; got != "v" {
		t.Fatalf("expected nested clone to be isolated, got %#v", got)
	}

	list := []interface{}{"value", map[string]interface{}{"k": "v"}}
	clonedList, ok := cloneJSONValue(list).([]interface{})
	if !ok {
		t.Fatalf("expected cloned list to be a slice, got %T", cloneJSONValue(list))
	}
	if !reflect.DeepEqual(clonedList, list) {
		t.Fatalf("expected cloned list to match original, got %#v want %#v", clonedList, list)
	}
	list[1].(map[string]interface{})["k"] = "changed"
	if got := clonedList[1].(map[string]interface{})["k"]; got != "v" {
		t.Fatalf("expected cloned list to be isolated, got %#v", got)
	}

	if got := cloneJSONValue(nil); got != nil {
		t.Fatalf("expected nil clone value to stay nil, got %#v", got)
	}
}
