package factory

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	codexruntime "github.com/olhapi/maestro/internal/agentruntime/codex"
	"github.com/olhapi/maestro/pkg/config"
)

type WorkflowStartRequest struct {
	Workflow        *config.Workflow
	WorkspacePath   string
	IssueID         string
	IssueIdentifier string
	Env             []string
	Permissions     agentruntime.PermissionConfig
	DynamicTools    []map[string]interface{}
	ToolExecutor    agentruntime.ToolExecutor
	ResumeToken     string
	Metadata        map[string]interface{}
}

type WorkflowStarter func(ctx context.Context, request WorkflowStartRequest, observers agentruntime.Observers) (agentruntime.Client, error)

func StartWorkflow(ctx context.Context, request WorkflowStartRequest, observers agentruntime.Observers) (agentruntime.Client, error) {
	spec, err := RuntimeSpecFromWorkflow(request)
	if err != nil {
		return nil, err
	}
	switch spec.Provider {
	case "", agentruntime.ProviderCodex:
		return codexruntime.Start(ctx, spec, observers)
	default:
		return nil, fmt.Errorf("%w: provider %q", agentruntime.ErrUnsupportedCapability, spec.Provider)
	}
}

func RuntimeSpecFromWorkflow(request WorkflowStartRequest) (agentruntime.RuntimeSpec, error) {
	if request.Workflow == nil {
		return agentruntime.RuntimeSpec{}, fmt.Errorf("workflow is required")
	}
	workflow := request.Workflow
	env := append([]string(nil), request.Env...)
	if len(env) == 0 {
		env = os.Environ()
	}
	transport := agentruntime.Transport(strings.ToLower(strings.TrimSpace(workflow.Config.Agent.Mode)))
	provider := agentruntime.Provider(strings.TrimSpace(workflow.Config.Codex.Provider))
	if provider == "" {
		provider = agentruntime.ProviderCodex
	}
	return agentruntime.RuntimeSpec{
		Provider:        provider,
		Transport:       transport,
		Command:         workflow.Config.Codex.Command,
		ExpectedVersion: workflow.Config.Codex.ExpectedVersion,
		Workspace:       request.WorkspacePath,
		WorkspaceRoot:   workflow.Config.Workspace.Root,
		IssueID:         request.IssueID,
		IssueIdentifier: request.IssueIdentifier,
		Env:             env,
		ReadTimeout:     time.Duration(workflow.Config.Codex.ReadTimeoutMs) * time.Millisecond,
		TurnTimeout:     time.Duration(workflow.Config.Codex.TurnTimeoutMs) * time.Millisecond,
		StallTimeout:    time.Duration(workflow.Config.Codex.StallTimeoutMs) * time.Millisecond,
		Permissions:     clonePermissionConfig(request.Permissions),
		DynamicTools:    cloneToolSpecs(request.DynamicTools),
		ToolExecutor:    request.ToolExecutor,
		ResumeToken:     strings.TrimSpace(request.ResumeToken),
		Metadata:        cloneJSONMap(request.Metadata),
	}, nil
}

func clonePermissionConfig(config agentruntime.PermissionConfig) agentruntime.PermissionConfig {
	return agentruntime.PermissionConfig{
		ApprovalPolicy:    cloneJSONValue(config.ApprovalPolicy),
		ThreadSandbox:     config.ThreadSandbox,
		TurnSandboxPolicy: cloneJSONMap(config.TurnSandboxPolicy),
		CollaborationMode: config.CollaborationMode,
		Metadata:          cloneJSONMap(config.Metadata),
	}
}

func cloneJSONValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneJSONMap(typed)
	case []interface{}:
		cloned := make([]interface{}, len(typed))
		for i := range typed {
			cloned[i] = cloneJSONValue(typed[i])
		}
		return cloned
	default:
		return typed
	}
}

func cloneJSONMap(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneToolSpecs(specs []map[string]interface{}) []map[string]interface{} {
	if len(specs) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(specs))
	for _, spec := range specs {
		out = append(out, cloneJSONMap(spec))
	}
	return out
}
