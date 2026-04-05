package factory

import (
	"context"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/pkg/config"
)

func TestRuntimeSpecFromWorkflowRejectsMissingWorkflow(t *testing.T) {
	_, err := RuntimeSpecFromWorkflow(WorkflowStartRequest{})
	if err == nil || !strings.Contains(err.Error(), "workflow is required") {
		t.Fatalf("expected missing workflow error, got %v", err)
	}
}

func TestRuntimeSpecFromWorkflowUsesProcessEnvWhenRequestEnvMissing(t *testing.T) {
	workflow := &config.Workflow{Config: config.DefaultConfig()}
	selectedRuntime := workflow.Config.SelectedRuntimeConfig()
	selectedRuntime.Command = "codex app-server"
	workflow.Config.Runtime.Entries[workflow.Config.Runtime.Default] = selectedRuntime

	spec, err := RuntimeSpecFromWorkflow(WorkflowStartRequest{
		Workflow: workflow,
	})
	if err != nil {
		t.Fatalf("RuntimeSpecFromWorkflow: %v", err)
	}
	if spec.Provider != agentruntime.ProviderCodex {
		t.Fatalf("expected codex provider, got %q", spec.Provider)
	}
	if len(spec.Env) == 0 {
		t.Fatal("expected runtime spec to default to process environment")
	}
}

func TestRuntimeSpecFromWorkflowClonesMutableInputs(t *testing.T) {
	workflow := &config.Workflow{Config: config.DefaultConfig()}
	workflow.Config.Agent.Mode = config.AgentModeAppServer
	workflow.Config.Workspace.Root = "/repo/root"

	permissions := agentruntime.PermissionConfig{
		ApprovalPolicy: map[string]interface{}{
			"granular": map[string]interface{}{"rules": true},
		},
		TurnSandboxPolicy: map[string]interface{}{
			"type": "workspaceWrite",
		},
		Metadata: map[string]interface{}{
			"provider": "codex",
		},
	}
	tools := []map[string]interface{}{
		{
			"name": "tool-1",
			"config": map[string]interface{}{
				"mode": "safe",
			},
		},
	}
	request := WorkflowStartRequest{
		Workflow:        workflow,
		WorkspacePath:   "/tmp/workspace",
		IssueID:         "iss-1",
		IssueIdentifier: "ISS-1",
		Env:             []string{"FOO=bar"},
		Permissions:     permissions,
		DynamicTools:    tools,
		ResumeToken:     " thread-1 ",
		Metadata: map[string]interface{}{
			"source": "test",
		},
	}

	spec, err := RuntimeSpecFromWorkflow(request)
	if err != nil {
		t.Fatalf("RuntimeSpecFromWorkflow: %v", err)
	}
	request.Env[0] = "FOO=changed"
	request.Permissions.TurnSandboxPolicy["type"] = "dangerFullAccess"
	request.Permissions.Metadata["provider"] = "changed"
	request.DynamicTools[0]["name"] = "tool-2"
	request.Metadata["source"] = "changed"

	if spec.ResumeToken != "thread-1" {
		t.Fatalf("expected resume token to be trimmed, got %q", spec.ResumeToken)
	}
	if spec.Permissions.TurnSandboxPolicy["type"] != "workspaceWrite" {
		t.Fatalf("expected sandbox policy clone, got %#v", spec.Permissions.TurnSandboxPolicy)
	}
	if spec.Permissions.Metadata["provider"] != "codex" {
		t.Fatalf("expected permission metadata clone, got %#v", spec.Permissions.Metadata)
	}
	if spec.DynamicTools[0]["name"] != "tool-1" {
		t.Fatalf("expected dynamic tool clone, got %#v", spec.DynamicTools)
	}
	if spec.Metadata["source"] != "test" {
		t.Fatalf("expected request metadata clone, got %#v", spec.Metadata)
	}
}

func TestCloneHelpersCopyNestedValues(t *testing.T) {
	permissions := agentruntime.PermissionConfig{
		ApprovalPolicy: map[string]interface{}{
			"granular": map[string]interface{}{
				"rules": true,
			},
		},
		TurnSandboxPolicy: map[string]interface{}{
			"type": "workspaceWrite",
		},
		Metadata: map[string]interface{}{
			"nested": map[string]interface{}{
				"transport": "app_server",
			},
		},
	}

	cloned := clonePermissionConfig(permissions)
	cloned.TurnSandboxPolicy["type"] = "dangerFullAccess"
	cloned.Metadata["nested"].(map[string]interface{})["transport"] = "stdio"
	if permissions.TurnSandboxPolicy["type"] != "workspaceWrite" {
		t.Fatalf("expected original sandbox policy to remain unchanged, got %#v", permissions.TurnSandboxPolicy)
	}
	if permissions.Metadata["nested"].(map[string]interface{})["transport"] != "app_server" {
		t.Fatalf("expected original metadata to remain unchanged, got %#v", permissions.Metadata)
	}

	specs := cloneToolSpecs([]map[string]interface{}{
		{
			"name": "tool-1",
			"config": map[string]interface{}{
				"mode": "safe",
			},
		},
	})
	specs[0]["name"] = "tool-2"
	if specs[0]["config"].(map[string]interface{})["mode"] != "safe" {
		t.Fatalf("expected cloned tool config to survive mutation, got %#v", specs)
	}

	if cloneJSONMap(nil) != nil {
		t.Fatal("expected nil map clone to stay nil")
	}
	clone := cloneJSONMap(map[string]interface{}{
		"nested": map[string]interface{}{
			"mode": "safe",
		},
	})
	clone["nested"].(map[string]interface{})["mode"] = "unsafe"
	if clone["nested"].(map[string]interface{})["mode"] != "unsafe" {
		t.Fatalf("expected cloned nested map to be mutable, got %#v", clone)
	}
	if cloneJSONValue([]interface{}{map[string]interface{}{"mode": "safe"}}) == nil {
		t.Fatal("expected cloneJSONValue to preserve slices")
	}
}

func TestStartWorkflowUsesCodexRuntime(t *testing.T) {
	workflow := &config.Workflow{Config: config.DefaultConfig()}
	runtime := workflow.Config.Runtime.Entries["codex-stdio"]
	runtime.Command = "cat"

	client, err := StartWorkflow(context.Background(), WorkflowStartRequest{
		Workflow:        workflow,
		RuntimeName:     "codex-stdio",
		RuntimeConfig:   runtime,
		IssueID:         "iss-1",
		IssueIdentifier: "ISS-1",
	}, agentruntime.Observers{})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	if client == nil {
		t.Fatal("expected runtime client")
	}
	_ = client.Close()
}
