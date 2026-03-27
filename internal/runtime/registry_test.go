package runtime

import (
	"strings"
	"testing"
)

func TestResolveSelectionUsesPrecedence(t *testing.T) {
	registry := DefaultRegistry()

	selection, err := registry.ResolveSelection(SelectionInput{
		IssueRuntime:         string(BackendCodex),
		ProjectRuntime:       string(BackendClaude),
		WorkflowRuntime:      string(BackendClaude),
		ProjectAccessProfile: string(AccessProfileFullAccess),
		WorkflowStartupMode:  string(StartupModePlan),
	})
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if selection.Backend != BackendCodex {
		t.Fatalf("expected issue runtime to win, got %q", selection.Backend)
	}
	if selection.Policy.AccessProfile != AccessProfileFullAccess {
		t.Fatalf("expected project access profile to win, got %q", selection.Policy.AccessProfile)
	}
	if selection.Policy.StartupMode != StartupModePlan {
		t.Fatalf("expected workflow startup mode to apply, got %q", selection.Policy.StartupMode)
	}
}

func TestResolveSelectionUsesIssueOverrides(t *testing.T) {
	registry := DefaultRegistry()

	selection, err := registry.ResolveSelection(SelectionInput{
		ProjectRuntime:       string(BackendClaude),
		WorkflowRuntime:      string(BackendCodex),
		IssueAccessProfile:   string(AccessProfilePlanThenFullAccess),
		ProjectAccessProfile: string(AccessProfileFullAccess),
		IssueStartupMode:     string(StartupModePlan),
		WorkflowStartupMode:  string(StartupModeDefault),
	})
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if selection.Backend != BackendClaude {
		t.Fatalf("expected project runtime to apply, got %q", selection.Backend)
	}
	if selection.Policy.AccessProfile != AccessProfilePlanThenFullAccess {
		t.Fatalf("expected issue access profile to win, got %q", selection.Policy.AccessProfile)
	}
	if selection.Policy.StartupMode != StartupModePlan {
		t.Fatalf("expected issue startup mode to win, got %q", selection.Policy.StartupMode)
	}
}

func TestResolveSelectionRejectsUnknownRuntime(t *testing.T) {
	registry := DefaultRegistry()

	_, err := registry.ResolveSelection(SelectionInput{
		IssueRuntime: "definitely-not-a-runtime",
	})
	if err == nil {
		t.Fatal("expected unknown runtime to fail")
	}
	if !strings.Contains(err.Error(), "unknown runtime") {
		t.Fatalf("expected unknown runtime error, got %v", err)
	}
}

func TestResolveEffectivePolicyRejectsUnsupportedApprovalSurface(t *testing.T) {
	spec := DefaultClaudeBackend()
	spec.Name = Backend("custom")
	spec.DefaultPolicy.ApprovalSurface = ApprovalSurfaceUserInput
	spec.Capabilities = Capabilities{CapabilityStructuredApprovals}

	_, err := ResolveEffectivePolicy(spec, SelectionInput{})
	if err == nil {
		t.Fatal("expected unsupported approval surface to fail")
	}
	if !strings.Contains(err.Error(), "approval surface") {
		t.Fatalf("expected approval surface error, got %v", err)
	}
}

func TestResolveEffectivePolicyRejectsPlanModeWithoutCapability(t *testing.T) {
	spec := DefaultClaudeBackend()
	spec.Name = Backend("custom")
	spec.Capabilities = Capabilities{
		CapabilityStructuredApprovals,
	}

	_, err := ResolveEffectivePolicy(spec, SelectionInput{
		IssueStartupMode: string(StartupModePlan),
	})
	if err == nil {
		t.Fatal("expected unsupported plan mode to fail")
	}
	if !strings.Contains(err.Error(), "does not support plan mode") {
		t.Fatalf("expected plan mode error, got %v", err)
	}
}
