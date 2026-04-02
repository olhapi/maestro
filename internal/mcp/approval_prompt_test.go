package mcp

import (
	"context"
	"encoding/json"
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

func TestApprovalPromptHandleCallToolRoutesDecisionPayloads(t *testing.T) {
	server, provider, project, issue, workspace := newApprovalPromptTestServer(t)

	cases := []struct {
		name               string
		toolName           string
		input              map[string]interface{}
		decision           string
		wantClassification string
		wantCommand        string
		wantReason         string
		wantBehavior       string
		wantMessage        string
	}{
		{
			name:               "command allow",
			toolName:           "Bash",
			input:              map[string]interface{}{"command": "pwd && git status --short", "description": "Run pwd and git status --short"},
			decision:           "allow",
			wantClassification: "command",
			wantCommand:        "pwd && git status --short",
			wantReason:         "Claude requested command approval: pwd && git status --short",
			wantBehavior:       "allow",
		},
		{
			name:               "file write deny",
			toolName:           "Write",
			input:              map[string]interface{}{"file_path": filepath.Join(workspace, "notes.txt"), "content": "file-change-ok\n"},
			decision:           "deny",
			wantClassification: "file_write",
			wantCommand:        "Write " + filepath.Join(workspace, "notes.txt"),
			wantReason:         "Claude requested a file write: " + filepath.Join(workspace, "notes.txt"),
			wantBehavior:       "deny",
			wantMessage:        "Maestro denied the request: Claude requested a file write: " + filepath.Join(workspace, "notes.txt"),
		},
		{
			name:               "protected edit ask",
			toolName:           "Edit",
			input:              map[string]interface{}{"file_path": filepath.Join(workspace, ".git", "config"), "old_string": "[core]", "new_string": "# spike\n[core]", "replace_all": false},
			decision:           "ask",
			wantClassification: "protected_directory_write",
			wantCommand:        "Edit " + filepath.Join(workspace, ".git", "config"),
			wantReason:         "Claude requested a protected-directory edit: " + filepath.Join(workspace, ".git", "config"),
			wantBehavior:       "ask",
			wantMessage:        "Maestro needs clarification before approving this request: Claude requested a protected-directory edit: " + filepath.Join(workspace, ".git", "config"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolUseID := "toolu_" + strings.ReplaceAll(strings.ToLower(strings.ReplaceAll(tc.name, " ", "_")), "-", "_")
			request := approvalPromptCallRequest(t, project, issue, workspace, toolUseID, tc.toolName, tc.input)

			resultCh := make(chan *mcpapi.CallToolResult, 1)
			errCh := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			go func() {
				result, err := server.handleCallToolRequest(ctx, request)
				if err != nil {
					errCh <- err
					return
				}
				resultCh <- result
			}()

			interaction := <-provider.pendingCh
			responder := <-provider.responderCh

			if interaction.Kind != agentruntime.PendingInteractionKindApproval {
				t.Fatalf("expected approval interaction, got %q", interaction.Kind)
			}
			if interaction.ID != approvalPromptInteractionID(toolUseID) {
				t.Fatalf("unexpected interaction id: got %q want %q", interaction.ID, approvalPromptInteractionID(toolUseID))
			}
			if interaction.RequestID != toolUseID || interaction.ItemID != toolUseID {
				t.Fatalf("unexpected tool-use correlation fields: %+v", interaction)
			}
			if interaction.IssueID != issue.ID || interaction.IssueIdentifier != issue.Identifier {
				t.Fatalf("unexpected issue metadata: %+v", interaction)
			}
			if interaction.ProjectID != project.ID || interaction.ProjectName != project.Name {
				t.Fatalf("unexpected project metadata: %+v", interaction)
			}
			if interaction.Approval == nil {
				t.Fatal("expected approval payload")
			}
			if got := interaction.Approval.Command; got != tc.wantCommand {
				t.Fatalf("unexpected approval command: got %q want %q", got, tc.wantCommand)
			}
			if got := interaction.Approval.CWD; got != workspace {
				t.Fatalf("unexpected approval cwd: got %q want %q", got, workspace)
			}
			if got := interaction.Approval.Reason; got != tc.wantReason {
				t.Fatalf("unexpected approval reason: got %q want %q", got, tc.wantReason)
			}
			if got := interaction.Metadata["source"]; got != "claude_permission_prompt" {
				t.Fatalf("unexpected source metadata: %#v", got)
			}
			if got := interaction.Metadata["classification"]; got != tc.wantClassification {
				t.Fatalf("unexpected classification metadata: %#v", got)
			}
			if got := interaction.Metadata["tool_name"]; got != tc.toolName {
				t.Fatalf("unexpected tool_name metadata: %#v", got)
			}
			if got := interaction.Metadata["tool_use_id"]; got != toolUseID {
				t.Fatalf("unexpected tool_use_id metadata: %#v", got)
			}
			if got := interaction.Metadata["workspace_path"]; got != workspace {
				t.Fatalf("unexpected workspace_path metadata: %#v", got)
			}
			if got, ok := interaction.Metadata["request_meta"].(map[string]interface{}); !ok || got["claudecode/toolUseId"] != toolUseID {
				t.Fatalf("expected request_meta to preserve tool-use correlation, got %#v", interaction.Metadata["request_meta"])
			}
			if len(interaction.Approval.Decisions) != 3 {
				t.Fatalf("expected 3 approval decisions, got %#v", interaction.Approval.Decisions)
			}
			decisionValues := []string{
				interaction.Approval.Decisions[0].Value,
				interaction.Approval.Decisions[1].Value,
				interaction.Approval.Decisions[2].Value,
			}
			if !reflect.DeepEqual(decisionValues, []string{"allow", "deny", "ask"}) {
				t.Fatalf("unexpected approval decisions: %#v", decisionValues)
			}

			if err := responder(context.Background(), interaction.ID, agentruntime.PendingInteractionResponse{Decision: tc.decision}); err != nil {
				t.Fatalf("responder failed: %v", err)
			}

			result := awaitApprovalPromptResult(t, resultCh, errCh)
			if result.IsError {
				t.Fatalf("expected approval prompt result to succeed, got %#v", result)
			}

			payload := decodeApprovalPromptResult(t, result)
			if got := payload["behavior"]; got != tc.wantBehavior {
				t.Fatalf("unexpected behavior: got %#v want %q", got, tc.wantBehavior)
			}
			switch tc.wantBehavior {
			case "allow":
				updatedInput, ok := payload["updatedInput"].(map[string]interface{})
				if !ok {
					t.Fatalf("expected allow response to include updatedInput, got %#v", payload)
				}
				if !reflect.DeepEqual(updatedInput, tc.input) {
					t.Fatalf("unexpected allow payload: got %#v want %#v", updatedInput, tc.input)
				}
			case "deny", "ask":
				if got := payload["message"]; got != tc.wantMessage {
					t.Fatalf("unexpected message: got %#v want %q", got, tc.wantMessage)
				}
				if _, ok := payload["updatedInput"]; ok {
					t.Fatalf("did not expect updatedInput in %s response, got %#v", tc.wantBehavior, payload)
				}
			}

			clears := drainStringChannel(provider.clearedCh)
			if len(clears) == 0 {
				t.Fatal("expected pending interaction to be cleared")
			}
			for _, cleared := range clears {
				if cleared != interaction.ID {
					t.Fatalf("unexpected cleared interaction id: %q", cleared)
				}
			}
		})
	}
}

func TestApprovalPromptHandleCallToolTimesOutDeterministically(t *testing.T) {
	server, provider, project, issue, workspace := newApprovalPromptTestServer(t)

	toolUseID := "toolu_timeout_case"
	request := approvalPromptCallRequest(t, project, issue, workspace, toolUseID, "Bash", map[string]interface{}{
		"command": "sleep 60",
	})

	resultCh := make(chan *mcpapi.CallToolResult, 1)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	go func() {
		result, err := server.handleCallToolRequest(ctx, request)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	interaction := <-provider.pendingCh
	if interaction.ID != approvalPromptInteractionID(toolUseID) {
		t.Fatalf("unexpected timeout interaction id: %q", interaction.ID)
	}

	result := awaitApprovalPromptResult(t, resultCh, errCh)
	if !result.IsError {
		t.Fatalf("expected timeout result to be an error envelope, got %#v", result)
	}

	envelope := decodeEnvelope(t, result)
	errorPayload, ok := envelope["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected timeout envelope error payload, got %#v", envelope)
	}
	if got := asString(errorPayload["message"]); !strings.Contains(got, context.DeadlineExceeded.Error()) {
		t.Fatalf("expected deadline exceeded timeout message, got %q", got)
	}

	clears := drainStringChannel(provider.clearedCh)
	if len(clears) != 1 {
		t.Fatalf("expected exactly one clear on timeout, got %#v", clears)
	}
	if clears[0] != interaction.ID {
		t.Fatalf("unexpected cleared interaction id: %q", clears[0])
	}
}

func TestApprovalPromptHandleCallToolUsesFallbackToolUseIDWhenMissing(t *testing.T) {
	server, provider, project, issue, workspace := newApprovalPromptTestServer(t)

	input := map[string]interface{}{"command": "whoami"}
	request := mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{
			Name: "approval_prompt",
			Arguments: map[string]interface{}{
				"tool_name": "Bash",
				"input":     input,
			},
			Meta: mcpapi.NewMetaFromMap(map[string]interface{}{
				"maestro/issue_id":         issue.ID,
				"maestro/issue_identifier": issue.Identifier,
				"maestro/issue_title":      issue.Title,
				"maestro/project_id":       project.ID,
				"maestro/project_name":     project.Name,
				"maestro/workspace_path":   workspace,
			}),
		},
	}

	resultCh := make(chan *mcpapi.CallToolResult, 1)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		result, err := server.handleCallToolRequest(ctx, request)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	interaction := <-provider.pendingCh
	responder := <-provider.responderCh

	expectedToolUseID := approvalPromptFallbackToolUseID(approvalPromptIssueContext{
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		IssueTitle:      issue.Title,
		ProjectID:       project.ID,
		ProjectName:     project.Name,
		WorkspacePath:   workspace,
	}, "Bash", input)
	if interaction.ID != approvalPromptInteractionID(expectedToolUseID) {
		t.Fatalf("unexpected fallback interaction id: got %q want %q", interaction.ID, approvalPromptInteractionID(expectedToolUseID))
	}
	if interaction.RequestID != expectedToolUseID || interaction.ItemID != expectedToolUseID {
		t.Fatalf("unexpected fallback tool-use correlation fields: %+v", interaction)
	}
	if got := interaction.Metadata["tool_use_id"]; got != expectedToolUseID {
		t.Fatalf("expected fallback tool-use id metadata, got %#v", got)
	}

	if err := responder(context.Background(), interaction.ID, agentruntime.PendingInteractionResponse{Decision: "allow"}); err != nil {
		t.Fatalf("responder failed: %v", err)
	}
	if result := awaitApprovalPromptResult(t, resultCh, errCh); result == nil {
		t.Fatal("expected a result")
	}
}

func newApprovalPromptTestServer(t *testing.T) (*Server, *approvalPromptTestProvider, *kanban.Project, *kanban.Issue, string) {
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

	provider := &approvalPromptTestProvider{
		testRuntimeProvider: testRuntimeProvider{store: store},
		pendingCh:           make(chan *agentruntime.PendingInteraction, 1),
		responderCh:         make(chan agentruntime.InteractionResponder, 1),
		clearedCh:           make(chan string, 2),
	}

	return NewServerWithProvider(store, provider), provider, project, issue, workspace
}

func approvalPromptCallRequest(t *testing.T, project *kanban.Project, issue *kanban.Issue, workspace, toolUseID, toolName string, input map[string]interface{}) mcpapi.CallToolRequest {
	t.Helper()

	meta := map[string]interface{}{
		"claudecode/toolUseId":     toolUseID,
		"maestro/issue_id":         issue.ID,
		"maestro/issue_identifier": issue.Identifier,
		"maestro/issue_title":      issue.Title,
		"maestro/project_id":       project.ID,
		"maestro/project_name":     project.Name,
		"maestro/workspace_path":   workspace,
	}
	return mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{
			Name: "approval_prompt",
			Arguments: map[string]interface{}{
				"tool_name":   toolName,
				"input":       input,
				"tool_use_id": toolUseID,
			},
			Meta: mcpapi.NewMetaFromMap(meta),
		},
	}
}

func decodeApprovalPromptResult(t *testing.T, result *mcpapi.CallToolResult) map[string]interface{} {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected approval prompt content")
	}
	textContent, ok := result.Content[0].(mcpapi.TextContent)
	if !ok {
		t.Fatalf("unexpected approval prompt content type %T", result.Content[0])
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(textContent.Text), &decoded); err != nil {
		t.Fatalf("decode approval prompt payload %q: %v", textContent.Text, err)
	}
	return decoded
}

func awaitApprovalPromptResult(t *testing.T, resultCh <-chan *mcpapi.CallToolResult, errCh <-chan error) *mcpapi.CallToolResult {
	t.Helper()
	select {
	case err := <-errCh:
		t.Fatalf("approval prompt handler returned error: %v", err)
	case result := <-resultCh:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval prompt result")
	}
	return nil
}

func drainStringChannel(ch <-chan string) []string {
	var out []string
	for {
		select {
		case value := <-ch:
			out = append(out, value)
		default:
			return out
		}
	}
}
