package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
)

type rejectingApprovalPromptProvider struct {
	testRuntimeProvider
}

func (p rejectingApprovalPromptProvider) RegisterPendingInteraction(issueID string, interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder) bool {
	_ = issueID
	_ = interaction
	_ = responder
	return false
}

func (p rejectingApprovalPromptProvider) ClearPendingInteraction(issueID string, interactionID string) {
	_ = issueID
	_ = interactionID
}

func TestApprovalPromptParseAndDecisionBranches(t *testing.T) {
	_, project, issue, workspace := approvalPromptFixture(t)

	baseArgs := map[string]interface{}{
		"tool_name": "Bash",
		"input": map[string]interface{}{
			"command": "pwd",
		},
	}
	baseMeta := approvalPromptMeta(project, issue, workspace)

	t.Run("parse validation", func(t *testing.T) {
		cases := []struct {
			name    string
			args    map[string]interface{}
			meta    map[string]interface{}
			wantErr string
		}{
			{
				name:    "empty arguments",
				args:    map[string]interface{}{},
				meta:    baseMeta,
				wantErr: "approval_prompt requires a non-empty arguments object",
			},
			{
				name: "missing tool name",
				args: map[string]interface{}{
					"input": map[string]interface{}{"command": "pwd"},
				},
				meta:    baseMeta,
				wantErr: "approval_prompt requires tool_name",
			},
			{
				name: "missing input",
				args: map[string]interface{}{
					"tool_name": "Bash",
				},
				meta:    baseMeta,
				wantErr: "approval_prompt requires input",
			},
			{
				name:    "missing issue metadata",
				args:    baseArgs,
				meta:    map[string]interface{}{"maestro/workspace_path": workspace},
				wantErr: "approval_prompt requires maestro/issue_id metadata",
			},
			{
				name:    "missing workspace metadata",
				args:    baseArgs,
				meta:    map[string]interface{}{"maestro/issue_id": issue.ID},
				wantErr: "approval_prompt requires maestro/workspace_path metadata",
			},
			{
				name: "explicit and metadata tool ids differ",
				args: map[string]interface{}{
					"tool_name":   "Bash",
					"input":       map[string]interface{}{"command": "pwd"},
					"tool_use_id": "explicit-tool-id",
				},
				meta: func() map[string]interface{} {
					meta := approvalPromptMeta(project, issue, workspace)
					meta["claudecode/toolUseId"] = "metadata-tool-id"
					return meta
				}(),
				wantErr: "approval_prompt tool_use_id mismatch",
			},
			{
				name: "metadata claudecode mismatches claude id",
				args: map[string]interface{}{
					"tool_name": "Bash",
					"input":     map[string]interface{}{"command": "pwd"},
				},
				meta: func() map[string]interface{} {
					meta := approvalPromptMeta(project, issue, workspace)
					meta["claudecode/toolUseId"] = "legacy-tool-id"
					meta["claude/toolUseId"] = "different-tool-id"
					return meta
				}(),
				wantErr: "approval_prompt tool_use_id mismatch",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				request := mcpapi.CallToolRequest{
					Params: mcpapi.CallToolParams{
						Name:      "approval_prompt",
						Arguments: tc.args,
						Meta:      mcpapi.NewMetaFromMap(tc.meta),
					},
				}

				parsed, err := parseApprovalPromptCall(request)
				if err == nil {
					t.Fatalf("expected parse error for %s, got %#v", tc.name, parsed)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("unexpected parse error: %v", err)
				}
			})
		}

		t.Run("fallback tool id is derived deterministically", func(t *testing.T) {
			request := mcpapi.CallToolRequest{
				Params: mcpapi.CallToolParams{
					Name: "approval_prompt",
					Arguments: map[string]interface{}{
						"tool_name": "Bash",
						"input": map[string]interface{}{
							"command": "pwd && git status --short",
							"extra": map[string]interface{}{
								"nested": []interface{}{"value", 2},
							},
						},
					},
					Meta: mcpapi.NewMetaFromMap(baseMeta),
				},
			}

			parsed, err := parseApprovalPromptCall(request)
			if err != nil {
				t.Fatalf("parseApprovalPromptCall failed: %v", err)
			}
			expected := approvalPromptFallbackToolUseID(parsed.Issue, parsed.ToolName, parsed.Input)
			if parsed.ToolUseID != expected {
				t.Fatalf("unexpected fallback tool use id: got %q want %q", parsed.ToolUseID, expected)
			}
			if got := parsed.RequestMeta["claudecode/toolUseId"]; got != expected {
				t.Fatalf("expected claudecode/toolUseId to be injected, got %#v", got)
			}
			if got := parsed.RequestMeta["claude/toolUseId"]; got != expected {
				t.Fatalf("expected claude/toolUseId to be injected, got %#v", got)
			}
		})
	})

	t.Run("tool use id and prompt helper branches", func(t *testing.T) {
		if got := approvalPromptToolUseIDFromMeta(map[string]interface{}{
			"claudecode/toolUseId": "  ",
			"claude/toolUseId":     "legacy",
			"tool_use_id":          "fallback",
		}); got != "legacy" {
			t.Fatalf("unexpected tool use id precedence: %q", got)
		}
		if got := approvalPromptToolUseIDFromMeta(map[string]interface{}{"tool_use_id": "fallback"}); got != "fallback" {
			t.Fatalf("unexpected fallback meta tool use id: %q", got)
		}
		if got := approvalPromptToolUseIDFromMeta(nil); got != "" {
			t.Fatalf("expected nil meta to yield an empty id, got %q", got)
		}

		if got := approvalPromptFallbackToolUseID(approvalPromptIssueContext{IssueID: issue.ID, IssueIdentifier: issue.Identifier}, "Bash", map[string]interface{}{"command": "pwd"}); got == "" {
			t.Fatal("expected deterministic fallback tool use id")
		}
		if got := approvalPromptFallbackToolUseID(approvalPromptIssueContext{IssueID: issue.ID, IssueIdentifier: issue.Identifier}, "Bash", map[string]interface{}{"bad": make(chan int)}); got != "Bash" {
			t.Fatalf("expected marshal failure fallback to tool name, got %q", got)
		}

		if got := approvalPromptTargetPath(nil); got != "" {
			t.Fatalf("expected empty target path for nil input, got %q", got)
		}
		if got := approvalPromptTargetPath(map[string]interface{}{"path": "first", "target_path": "second"}); got != "first" {
			t.Fatalf("expected file path to win, got %q", got)
		}
		if got := approvalPromptTargetPath(map[string]interface{}{"target_path": "second"}); got != "second" {
			t.Fatalf("expected target_path fallback, got %q", got)
		}

		if got := approvalPromptProtectedPath(""); got {
			t.Fatal("expected empty path to be unprotected")
		}
		if got := approvalPromptProtectedPath(filepath.Join(workspace, ".git", "config")); !got {
			t.Fatalf("expected .git path to be protected")
		}

		cases := []struct {
			name     string
			toolName string
			input    map[string]interface{}
			class    string
			command  string
			reason   string
		}{
			{
				name:     "bash command",
				toolName: "Bash",
				input: map[string]interface{}{
					"command": "pwd",
				},
				class:   "command",
				command: "pwd",
				reason:  "Claude requested command approval: pwd",
			},
			{
				name:     "write uses path key",
				toolName: "Write",
				input: map[string]interface{}{
					"path": "notes.txt",
				},
				class:   "file_write",
				command: "Write notes.txt",
				reason:  "Claude requested a file write: notes.txt",
			},
			{
				name:     "edit uses target path key",
				toolName: "Edit",
				input: map[string]interface{}{
					"target_path": "draft.md",
				},
				class:   "file_edit",
				command: "Edit draft.md",
				reason:  "Claude requested a file edit: draft.md",
			},
			{
				name:     "multiedit is recognized",
				toolName: "MultiEdit",
				input: map[string]interface{}{
					"file_path": "draft.md",
				},
				class:   "file_edit",
				command: "MultiEdit draft.md",
				reason:  "Claude requested a file edit: draft.md",
			},
			{
				name:     "protected write",
				toolName: "Write",
				input: map[string]interface{}{
					"file_path": filepath.Join(workspace, ".git", "config"),
				},
				class:   "protected_directory_write",
				command: "Write " + filepath.Join(workspace, ".git", "config"),
				reason:  "Claude requested a protected-directory write: " + filepath.Join(workspace, ".git", "config"),
			},
			{
				name:     "default approval",
				toolName: "Search",
				input:    map[string]interface{}{},
				class:    "approval",
				command:  "Search",
				reason:   "Claude requested approval.",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				classification := classifyApprovalPrompt(tc.toolName, tc.input)
				if classification != tc.class {
					t.Fatalf("unexpected classification: got %q want %q", classification, tc.class)
				}
				command := approvalPromptCommand(tc.toolName, tc.input, approvalPromptTargetPath(tc.input))
				if command != tc.command {
					t.Fatalf("unexpected command: got %q want %q", command, tc.command)
				}
				reason := approvalPromptReason(tc.toolName, classification, approvalPromptTargetPath(tc.input), tc.input)
				if reason != tc.reason {
					t.Fatalf("unexpected reason: got %q want %q", reason, tc.reason)
				}
			})
		}

		if got := approvalPromptDecisionMessage("allow", agentruntime.PendingInteraction{}, agentruntime.PendingInteractionResponse{}); got != "" {
			t.Fatalf("expected allow to produce no decision message, got %q", got)
		}
		if got := approvalPromptDecisionMessage("deny", agentruntime.PendingInteraction{
			Approval:    &agentruntime.PendingApproval{Reason: "review required"},
			LastActivity: "fallback",
		}, agentruntime.PendingInteractionResponse{Note: "explicit note"}); got != "explicit note" {
			t.Fatalf("expected note to win, got %q", got)
		}
		if got := approvalPromptDecisionMessage("deny", agentruntime.PendingInteraction{
			Approval:    &agentruntime.PendingApproval{Reason: "review required"},
			LastActivity: "fallback",
		}, agentruntime.PendingInteractionResponse{}); got != "Maestro denied the request: review required" {
			t.Fatalf("unexpected deny message: %q", got)
		}
		if got := approvalPromptDecisionMessage("ask", agentruntime.PendingInteraction{
			LastActivity: "fallback",
		}, agentruntime.PendingInteractionResponse{}); got != "Maestro needs clarification before approving this request: fallback" {
			t.Fatalf("unexpected ask message: %q", got)
		}

		interaction := agentruntime.PendingInteraction{
			Metadata: map[string]interface{}{
				"input": map[string]interface{}{
					"command": "pwd",
					"nested": map[string]interface{}{
						"values": []interface{}{"a", map[string]interface{}{"b": "c"}},
					},
				},
			},
		}
		allowResult, err := buildClaudePermissionPromptResult(interaction, agentruntime.PendingInteractionResponse{Decision: "allow"})
		if err != nil {
			t.Fatalf("buildClaudePermissionPromptResult(allow) failed: %v", err)
		}
		if allowResult.Behavior != "allow" {
			t.Fatalf("unexpected allow behavior: %#v", allowResult)
		}
		updatedInput, ok := allowResult.UpdatedInput.(map[string]interface{})
		if !ok {
			t.Fatalf("expected allow response to clone the original input, got %#v", allowResult.UpdatedInput)
		}
		originalInput := interaction.Metadata["input"].(map[string]interface{})
		originalInput["command"] = "changed"
		nested := updatedInput["nested"].(map[string]interface{})
		values := nested["values"].([]interface{})
		if values[1].(map[string]interface{})["b"] != "c" {
			t.Fatalf("expected deep clone of nested input, got %#v", allowResult.UpdatedInput)
		}

		denyResult, err := buildClaudePermissionPromptResult(interaction, agentruntime.PendingInteractionResponse{Decision: "deny", Note: "stop"})
		if err != nil {
			t.Fatalf("buildClaudePermissionPromptResult(deny) failed: %v", err)
		}
		if denyResult.Behavior != "deny" || denyResult.Message != "stop" {
			t.Fatalf("unexpected deny result: %#v", denyResult)
		}

		interactionWithActivity := interaction
		interactionWithActivity.LastActivity = "fallback"
		askResult, err := buildClaudePermissionPromptResult(interactionWithActivity, agentruntime.PendingInteractionResponse{Decision: "ask"})
		if err != nil {
			t.Fatalf("buildClaudePermissionPromptResult(ask) failed: %v", err)
		}
		if askResult.Behavior != "ask" || askResult.Message != "Maestro needs clarification before approving this request: fallback" {
			t.Fatalf("unexpected ask result: %#v", askResult)
		}

		if _, err := buildClaudePermissionPromptResult(interaction, agentruntime.PendingInteractionResponse{Decision: "maybe"}); !errors.Is(err, agentruntime.ErrInvalidInteractionResponse) {
			t.Fatalf("expected unsupported decision error, got %v", err)
		}

		if got := buildApprovalPromptInteraction(nil); got != nil {
			t.Fatalf("expected nil approval prompt call to return nil interaction, got %#v", got)
		}

		if got := cloneJSONMap(nil); got != nil {
			t.Fatalf("expected nil JSON map clone to remain nil, got %#v", got)
		}
	})
}

func TestApprovalPromptHandleErrorBranches(t *testing.T) {
	store, project, issue, workspace := approvalPromptFixture(t)
	request := makeApprovalPromptRequest(project, issue, workspace, "toolu-error-branch", "Bash", map[string]interface{}{
		"command": "pwd",
	})

	t.Run("runtime unavailable without registrar support", func(t *testing.T) {
		server := NewServerWithProvider(store, testRuntimeProvider{store: store})
		result, err := server.handleApprovalPrompt(context.Background(), request)
		if err != nil {
			t.Fatalf("handleApprovalPrompt returned error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected runtime unavailable response to be an error, got %#v", result)
		}
		envelope := decodeEnvelope(t, result)
		if got := envelope["error"].(map[string]interface{})["message"]; !strings.Contains(got.(string), "pending-interaction support") {
			t.Fatalf("unexpected runtime unavailable envelope: %#v", envelope)
		}
	})

	t.Run("register failure surfaces a runtime unavailable error", func(t *testing.T) {
		server := NewServerWithProvider(store, rejectingApprovalPromptProvider{testRuntimeProvider{store: store}})
		result, err := server.handleApprovalPrompt(context.Background(), request)
		if err != nil {
			t.Fatalf("handleApprovalPrompt returned error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected register failure to be an error, got %#v", result)
		}
		envelope := decodeEnvelope(t, result)
		if got := envelope["error"].(map[string]interface{})["message"]; !strings.Contains(got.(string), "could not register") {
			t.Fatalf("unexpected register failure envelope: %#v", envelope)
		}
	})

	t.Run("parse errors are returned as tool errors", func(t *testing.T) {
		server := NewServerWithProvider(store, testRuntimeProvider{store: store})
		result, err := server.handleApprovalPrompt(context.Background(), mcpapi.CallToolRequest{})
		if err != nil {
			t.Fatalf("handleApprovalPrompt returned error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected parse failure to be an error, got %#v", result)
		}
		envelope := decodeEnvelope(t, result)
		if got := envelope["error"].(map[string]interface{})["message"]; !strings.Contains(got.(string), "approval_prompt requires") {
			t.Fatalf("unexpected parse failure envelope: %#v", envelope)
		}
	})

	t.Run("serialization failure is reported back to the caller", func(t *testing.T) {
		server, provider, project, issue, workspace := newApprovalPromptTestServer(t)
		request := approvalPromptCallRequest(t, project, issue, workspace, "toolu-serialization", "Bash", map[string]interface{}{
			"command": "pwd",
			"bad":     make(chan int),
		})

		resultCh := make(chan *mcpapi.CallToolResult, 1)
		errCh := make(chan error, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go func() {
			result, err := server.handleApprovalPrompt(ctx, request)
			if err != nil {
				errCh <- err
				return
			}
			resultCh <- result
		}()

		interaction := <-provider.pendingCh
		responder := <-provider.responderCh
		if interaction.ID != approvalPromptInteractionID("toolu-serialization") {
			t.Fatalf("unexpected interaction id: %q", interaction.ID)
		}
		if err := responder(context.Background(), interaction.ID, agentruntime.PendingInteractionResponse{Decision: "allow"}); err != nil {
			t.Fatalf("responder failed: %v", err)
		}

		result := awaitApprovalPromptResult(t, resultCh, errCh)
		if !result.IsError {
			t.Fatalf("expected serialization failure to return an error envelope, got %#v", result)
		}
		envelope := decodeEnvelope(t, result)
		if got := envelope["error"].(map[string]interface{})["message"]; !strings.Contains(got.(string), "failed to encode approval response") {
			t.Fatalf("unexpected serialization failure envelope: %#v", envelope)
		}
	})
}

func approvalPromptFixture(t *testing.T) (*kanban.Store, *kanban.Project, *kanban.Issue, string) {
	t.Helper()

	store := testStore(t, "")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	project, err := store.CreateProject("Approval prompt project", "", workspace, filepath.Join(workspace, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Approval prompt issue", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	return store, project, issue, workspace
}

func approvalPromptMeta(project *kanban.Project, issue *kanban.Issue, workspace string) map[string]interface{} {
	return map[string]interface{}{
		"maestro/issue_id":         issue.ID,
		"maestro/issue_identifier": issue.Identifier,
		"maestro/issue_title":      issue.Title,
		"maestro/project_id":       project.ID,
		"maestro/project_name":     project.Name,
		"maestro/workspace_path":   workspace,
	}
}

func makeApprovalPromptRequest(project *kanban.Project, issue *kanban.Issue, workspace, toolUseID, toolName string, input map[string]interface{}) mcpapi.CallToolRequest {
	return mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{
			Name: "approval_prompt",
			Arguments: map[string]interface{}{
				"tool_name":   toolName,
				"input":       input,
				"tool_use_id": toolUseID,
			},
			Meta: mcpapi.NewMetaFromMap(func() map[string]interface{} {
				meta := approvalPromptMeta(project, issue, workspace)
				meta["claudecode/toolUseId"] = toolUseID
				return meta
			}()),
		},
	}
}

func TestApprovalPromptHandleUsesFallbackToolUseIDWithoutExplicitId(t *testing.T) {
	server, provider, project, issue, workspace := newApprovalPromptTestServer(t)
	request := mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{
			Name: "approval_prompt",
			Arguments: map[string]interface{}{
				"tool_name": "Bash",
				"input": map[string]interface{}{
					"command": "whoami",
				},
			},
			Meta: mcpapi.NewMetaFromMap(approvalPromptMeta(project, issue, workspace)),
		},
	}

	resultCh := make(chan *mcpapi.CallToolResult, 1)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		result, err := server.handleApprovalPrompt(ctx, request)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	interaction := <-provider.pendingCh
	responder := <-provider.responderCh
	expected := approvalPromptFallbackToolUseID(approvalPromptIssueContext{
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		IssueTitle:      issue.Title,
		ProjectID:       project.ID,
		ProjectName:     project.Name,
		WorkspacePath:   workspace,
	}, "Bash", map[string]interface{}{"command": "whoami"})
	if interaction.ID != approvalPromptInteractionID(expected) {
		t.Fatalf("unexpected fallback interaction id: got %q want %q", interaction.ID, approvalPromptInteractionID(expected))
	}
	if err := responder(context.Background(), interaction.ID, agentruntime.PendingInteractionResponse{Decision: "allow"}); err != nil {
		t.Fatalf("responder failed: %v", err)
	}

	result := awaitApprovalPromptResult(t, resultCh, errCh)
	if result.IsError {
		t.Fatalf("expected fallback prompt to succeed, got %#v", result)
	}
		payload := decodeApprovalPromptResult(t, result)
		if got := payload["behavior"]; got != "allow" {
			t.Fatalf("unexpected behavior: %#v", payload)
		}
	if got := interaction.Metadata["tool_use_id"]; got != expected {
		t.Fatalf("expected fallback tool-use id metadata, got %#v", got)
	}
	requestMeta, ok := interaction.Metadata["request_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected request_meta metadata, got %#v", interaction.Metadata["request_meta"])
	}
	if got := requestMeta["claudecode/toolUseId"]; got != expected {
		t.Fatalf("expected fallback claudecode/toolUseId metadata, got %#v", got)
	}
	if got := requestMeta["claude/toolUseId"]; got != expected {
		t.Fatalf("expected fallback claude/toolUseId metadata, got %#v", got)
	}
}

func TestApprovalPromptClassifyAndCloneHelpers(t *testing.T) {
	if got := cloneJSONValue(nil); got != nil {
		t.Fatalf("expected nil clone to remain nil, got %#v", got)
	}

	original := map[string]interface{}{
		"command": "pwd",
		"nested": map[string]interface{}{
			"list": []interface{}{"a", map[string]interface{}{"b": "c"}},
		},
	}
	cloned := cloneJSONValue(original).(map[string]interface{})
	original["command"] = "changed"
	originalNested := original["nested"].(map[string]interface{})
	originalNested["list"].([]interface{})[1].(map[string]interface{})["b"] = "changed"
	if cloned["command"] != "pwd" {
		t.Fatalf("expected clone to preserve original command, got %#v", cloned["command"])
	}
	clonedNested := cloned["nested"].(map[string]interface{})
	if clonedNested["list"].([]interface{})[1].(map[string]interface{})["b"] != "c" {
		t.Fatalf("expected deep clone to keep nested value, got %#v", clonedNested)
	}

	cloneMap := cloneJSONMap(map[string]interface{}{"list": []interface{}{1, map[string]interface{}{"value": "keep"}}})
	if !reflect.DeepEqual(cloneMap["list"].([]interface{})[1].(map[string]interface{}), map[string]interface{}{"value": "keep"}) {
		t.Fatalf("unexpected cloned map: %#v", cloneMap)
	}
	if got := cloneJSONMap(nil); got != nil {
		t.Fatalf("expected nil map clone to stay nil, got %#v", got)
	}

	_, _, markdown := summarizeApprovalPrompt("Bash", "command", map[string]interface{}{"command": "pwd"}, "workspace")
	if !strings.Contains(markdown, "### Claude permission prompt") {
		t.Fatalf("unexpected summary markdown: %s", markdown)
	}
}
