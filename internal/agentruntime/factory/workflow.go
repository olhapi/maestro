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
	RuntimeName     string
	RuntimeConfig   config.RuntimeConfig
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
	runtimeConfig := workflow.Config.Codex
	if strings.TrimSpace(request.RuntimeName) != "" {
		runtimeConfig = mergeRuntimeConfig(request.RuntimeConfig, workflow.Config.Codex)
	}
	transport := agentruntime.Transport(strings.ToLower(strings.TrimSpace(runtimeConfig.Transport)))
	provider := agentruntime.Provider(strings.TrimSpace(runtimeConfig.Provider))
	if provider == "" {
		provider = agentruntime.ProviderCodex
	}
	return agentruntime.RuntimeSpec{
		Provider:        provider,
		Transport:       transport,
		Command:         runtimeConfig.Command,
		ExpectedVersion: runtimeConfig.ExpectedVersion,
		Workspace:       request.WorkspacePath,
		WorkspaceRoot:   workflow.Config.Workspace.Root,
		IssueID:         request.IssueID,
		IssueIdentifier: request.IssueIdentifier,
		Env:             env,
		ReadTimeout:     time.Duration(runtimeConfig.ReadTimeoutMs) * time.Millisecond,
		TurnTimeout:     time.Duration(runtimeConfig.TurnTimeoutMs) * time.Millisecond,
		StallTimeout:    time.Duration(runtimeConfig.StallTimeoutMs) * time.Millisecond,
		Permissions:     clonePermissionConfig(request.Permissions),
		DynamicTools:    cloneToolSpecs(request.DynamicTools),
		ToolExecutor:    request.ToolExecutor,
		ResumeToken:     strings.TrimSpace(request.ResumeToken),
		Metadata:        cloneJSONMap(request.Metadata),
	}, nil
}

func mergeRuntimeConfig(cfg, fallback config.RuntimeConfig) config.RuntimeConfig {
	if strings.TrimSpace(cfg.Provider) == "" {
		cfg.Provider = fallback.Provider
	}
	if strings.TrimSpace(cfg.Transport) == "" {
		cfg.Transport = fallback.Transport
	}
	if strings.TrimSpace(cfg.Command) == "" {
		cfg.Command = fallback.Command
	}
	if strings.TrimSpace(cfg.ExpectedVersion) == "" {
		cfg.ExpectedVersion = fallback.ExpectedVersion
	}
	if cfg.ReadTimeoutMs <= 0 {
		cfg.ReadTimeoutMs = fallback.ReadTimeoutMs
	}
	if cfg.TurnTimeoutMs <= 0 {
		cfg.TurnTimeoutMs = fallback.TurnTimeoutMs
	}
	if cfg.StallTimeoutMs <= 0 {
		cfg.StallTimeoutMs = fallback.StallTimeoutMs
	}
	return cfg
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
