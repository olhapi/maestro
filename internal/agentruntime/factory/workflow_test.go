package factory

import (
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/pkg/config"
)

func TestRuntimeSpecFromWorkflowMapsWorkflowAndClonesMutableFields(t *testing.T) {
	workflow := &config.Workflow{Config: config.DefaultConfig()}
	workflow.Config.Agent.Mode = config.AgentModeStdio
	workflow.Config.Workspace.Root = "/repo/root"
	workflow.Config.Codex.Command = "cat"
	workflow.Config.Codex.ReadTimeoutMs = 11
	workflow.Config.Codex.TurnTimeoutMs = 22
	workflow.Config.Codex.StallTimeoutMs = 33

	request := WorkflowStartRequest{
		Workflow:        workflow,
		WorkspacePath:   "/tmp/workspaces/MAES-123",
		IssueID:         "iss_123",
		IssueIdentifier: "MAES-123",
		Env:             []string{"FOO=bar"},
		Permissions: agentruntime.PermissionConfig{
			ApprovalPolicy: map[string]interface{}{"mode": "never"},
			ThreadSandbox:  "workspace-write",
			TurnSandboxPolicy: map[string]interface{}{
				"type": "workspaceWrite",
			},
			CollaborationMode: "plan",
			Metadata: map[string]interface{}{
				"source": "test",
			},
		},
		DynamicTools: []map[string]interface{}{{
			"name": "tool-1",
			"config": map[string]interface{}{
				"mode": "safe",
			},
		}},
		ResumeToken: " thread-123 ",
		Metadata: map[string]interface{}{
			"provider_hint": "codex",
		},
	}

	spec, err := RuntimeSpecFromWorkflow(request)
	if err != nil {
		t.Fatalf("RuntimeSpecFromWorkflow: %v", err)
	}

	if spec.Provider != agentruntime.ProviderCodex {
		t.Fatalf("expected codex provider, got %q", spec.Provider)
	}
	if spec.Transport != agentruntime.TransportStdio {
		t.Fatalf("expected stdio transport, got %q", spec.Transport)
	}
	if spec.Command != workflow.Config.Codex.Command {
		t.Fatalf("expected codex command to be copied, got %+v", spec)
	}
	if spec.Workspace != request.WorkspacePath || spec.WorkspaceRoot != workflow.Config.Workspace.Root {
		t.Fatalf("expected workspace paths to be preserved, got %+v", spec)
	}
	if spec.IssueID != request.IssueID || spec.IssueIdentifier != request.IssueIdentifier {
		t.Fatalf("expected issue identity to be preserved, got %+v", spec)
	}
	if spec.ReadTimeout != 11*time.Millisecond || spec.TurnTimeout != 22*time.Millisecond || spec.StallTimeout != 33*time.Millisecond {
		t.Fatalf("expected workflow timeouts to be converted, got %+v", spec)
	}
	if spec.ResumeToken != "thread-123" {
		t.Fatalf("expected resume token to be trimmed, got %q", spec.ResumeToken)
	}
	if len(spec.Env) != 1 || spec.Env[0] != "FOO=bar" {
		t.Fatalf("expected env to be copied, got %#v", spec.Env)
	}
	if spec.Permissions.ThreadSandbox != "workspace-write" || spec.Permissions.CollaborationMode != "plan" {
		t.Fatalf("expected permissions to be copied, got %+v", spec.Permissions)
	}
	if spec.Permissions.Metadata["source"] != "test" {
		t.Fatalf("expected permission metadata to be copied, got %+v", spec.Permissions)
	}
	if len(spec.DynamicTools) != 1 || spec.DynamicTools[0]["name"] != "tool-1" {
		t.Fatalf("expected dynamic tools to be copied, got %#v", spec.DynamicTools)
	}
	if spec.Metadata["provider_hint"] != "codex" {
		t.Fatalf("expected request metadata to be copied, got %#v", spec.Metadata)
	}

	request.Env[0] = "FOO=changed"
	request.Permissions.TurnSandboxPolicy["type"] = "dangerFullAccess"
	request.Permissions.Metadata["source"] = "mutated"
	request.DynamicTools[0]["name"] = "tool-mutated"
	request.Metadata["provider_hint"] = "mutated"

	if spec.Env[0] != "FOO=bar" {
		t.Fatalf("expected env copy to be isolated, got %#v", spec.Env)
	}
	if spec.Permissions.TurnSandboxPolicy["type"] != "workspaceWrite" {
		t.Fatalf("expected sandbox policy clone to be isolated, got %#v", spec.Permissions.TurnSandboxPolicy)
	}
	if spec.Permissions.Metadata["source"] != "test" {
		t.Fatalf("expected permission metadata clone to be isolated, got %#v", spec.Permissions.Metadata)
	}
	if spec.DynamicTools[0]["name"] != "tool-1" {
		t.Fatalf("expected dynamic tool clone to be isolated, got %#v", spec.DynamicTools)
	}
	if spec.Metadata["provider_hint"] != "codex" {
		t.Fatalf("expected metadata clone to be isolated, got %#v", spec.Metadata)
	}
}
