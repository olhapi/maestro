package factory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/pkg/config"
)

func TestRuntimeSpecFromWorkflowMapsWorkflowAndClonesMutableFields(t *testing.T) {
	workflow := &config.Workflow{Config: config.DefaultConfig()}
	workflow.Config.Workspace.Root = "/repo/root"
	workflow.Config.Codex.Command = "codex app-server"
	workflow.Config.Codex.ExpectedVersion = "1.2.3"
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
		DBPath:      "/tmp/maestro.db",
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
	if spec.Transport != agentruntime.TransportAppServer {
		t.Fatalf("expected app_server transport, got %q", spec.Transport)
	}
	if spec.Command != workflow.Config.Codex.Command || spec.ExpectedVersion != workflow.Config.Codex.ExpectedVersion {
		t.Fatalf("expected codex command/version to be copied, got %+v", spec)
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
	if spec.DBPath != "/tmp/maestro.db" {
		t.Fatalf("expected db path to be preserved, got %q", spec.DBPath)
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

func TestRuntimeSpecFromWorkflowUsesExplicitRuntimeSelection(t *testing.T) {
	workflow := &config.Workflow{Config: config.DefaultConfig()}
	workflow.Config.Agent.Mode = config.AgentModeAppServer
	workflow.Config.Workspace.Root = "/repo/root"
	workflow.Config.Codex.Command = "codex app-server"
	workflow.Config.Codex.ExpectedVersion = "1.2.3"

	request := WorkflowStartRequest{
		Workflow:        workflow,
		RuntimeName:     "codex-stdio",
		RuntimeConfig:   workflow.Config.Runtime.Entries["codex-stdio"],
		WorkspacePath:   "/tmp/workspaces/MAES-123",
		IssueID:         "iss_123",
		IssueIdentifier: "MAES-123",
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
	if spec.Command != workflow.Config.Runtime.Entries["codex-stdio"].Command {
		t.Fatalf("expected explicit runtime command, got %q", spec.Command)
	}
	if spec.ExpectedVersion != workflow.Config.Runtime.Entries["codex-stdio"].ExpectedVersion {
		t.Fatalf("expected explicit runtime version, got %q", spec.ExpectedVersion)
	}
	if workflow.Config.Codex.Command != "codex app-server" {
		t.Fatalf("expected workflow codex config to stay untouched, got %q", workflow.Config.Codex.Command)
	}
}

func TestRuntimeSpecFromWorkflowMergesExplicitRuntimeConfigFallbacks(t *testing.T) {
	workflow := &config.Workflow{Config: config.DefaultConfig()}
	workflow.Config.Workspace.Root = "/repo/root"
	workflow.Config.Codex.Provider = "codex"
	workflow.Config.Codex.Transport = config.AgentModeAppServer
	workflow.Config.Codex.Command = "codex app-server"
	workflow.Config.Codex.ExpectedVersion = "1.2.3"
	workflow.Config.Codex.ReadTimeoutMs = 11
	workflow.Config.Codex.TurnTimeoutMs = 22
	workflow.Config.Codex.StallTimeoutMs = 33

	spec, err := RuntimeSpecFromWorkflow(WorkflowStartRequest{
		Workflow:      workflow,
		RuntimeName:   "codex-custom",
		RuntimeConfig: config.RuntimeConfig{},
		WorkspacePath: "/tmp/workspaces/MAES-123",
	})
	if err != nil {
		t.Fatalf("RuntimeSpecFromWorkflow: %v", err)
	}

	if spec.Provider != agentruntime.ProviderCodex {
		t.Fatalf("expected fallback provider, got %q", spec.Provider)
	}
	if spec.Transport != agentruntime.TransportAppServer {
		t.Fatalf("expected fallback transport, got %q", spec.Transport)
	}
	if spec.Command != "codex app-server" || spec.ExpectedVersion != "1.2.3" {
		t.Fatalf("expected fallback command and version, got %+v", spec)
	}
	if spec.ReadTimeout != 11*time.Millisecond || spec.TurnTimeout != 22*time.Millisecond || spec.StallTimeout != 33*time.Millisecond {
		t.Fatalf("expected fallback timeouts, got %+v", spec)
	}
}

func TestStartWorkflowUsesClaudeRuntime(t *testing.T) {
	workflow := &config.Workflow{Config: config.DefaultConfig()}
	workflow.Config.Workspace.Root = "/repo/root"
	workflow.Config.Runtime.Default = "claude"
	workflow.Config.Runtime.Entries["claude"] = config.RuntimeConfig{
		Provider:                 "claude",
		Transport:                config.AgentModeStdio,
		Command:                  "cat",
		ApprovalPolicy:           "never",
		InitialCollaborationMode: config.InitialCollaborationModeDefault,
		TurnTimeoutMs:            1,
		ReadTimeoutMs:            1,
		StallTimeoutMs:           1,
	}
	workflow.Config.Codex = workflow.Config.Runtime.Entries["claude"]

	client, err := StartWorkflow(context.Background(), WorkflowStartRequest{
		Workflow:        workflow,
		IssueID:         "iss_1",
		IssueIdentifier: "MAES-1",
		DBPath:          filepath.Join(t.TempDir(), "maestro.db"),
	}, agentruntime.Observers{})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	session := client.Session()
	if session == nil {
		t.Fatal("expected runtime session")
	}
	if session.Metadata["provider"] != string(agentruntime.ProviderClaude) {
		t.Fatalf("expected claude provider metadata, got %+v", session.Metadata)
	}
	if session.Metadata["transport"] != string(agentruntime.TransportStdio) {
		t.Fatalf("expected stdio transport metadata, got %+v", session.Metadata)
	}
	if client.Output() != "" {
		t.Fatalf("expected empty output before any turn, got %q", client.Output())
	}
}
