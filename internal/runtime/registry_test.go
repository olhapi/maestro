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

func TestResolveSelectionUsesWorkflowRuntimeAndOverrides(t *testing.T) {
	registry := DefaultRegistry()

	selection, err := registry.ResolveSelection(SelectionInput{
		WorkflowRuntime:       "claude-code",
		WorkflowAccessProfile: "plan then full access",
		WorkflowStartupMode:   "plan",
	})
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if selection.Backend != BackendClaude {
		t.Fatalf("expected workflow runtime to resolve to claude, got %q", selection.Backend)
	}
	if selection.Policy.AccessProfile != AccessProfilePlanThenFullAccess {
		t.Fatalf("expected workflow access profile to apply, got %q", selection.Policy.AccessProfile)
	}
	if selection.Policy.StartupMode != StartupModePlan {
		t.Fatalf("expected workflow startup mode to apply, got %q", selection.Policy.StartupMode)
	}
}

func TestResolveEffectivePolicyUsesWorkflowOverrides(t *testing.T) {
	spec := DefaultClaudeBackend()
	spec.Name = Backend("custom")
	spec.DefaultPolicy = EffectivePolicy{
		AccessProfile:   AccessProfileDefault,
		StartupMode:     StartupModeDefault,
		ApprovalSurface: ApprovalSurfaceUnknown,
	}

	policy, err := ResolveEffectivePolicy(spec, SelectionInput{
		WorkflowAccessProfile: "plan then full access",
		WorkflowStartupMode:   "plan",
	})
	if err != nil {
		t.Fatalf("ResolveEffectivePolicy: %v", err)
	}
	if policy.AccessProfile != AccessProfilePlanThenFullAccess {
		t.Fatalf("expected workflow access profile to apply, got %q", policy.AccessProfile)
	}
	if policy.StartupMode != StartupModePlan {
		t.Fatalf("expected workflow startup mode to apply, got %q", policy.StartupMode)
	}
	if policy.ApprovalSurface != ApprovalSurfaceUnknown {
		t.Fatalf("expected approval surface to remain unknown, got %q", policy.ApprovalSurface)
	}
}

func TestBackendSpecSupportPredicates(t *testing.T) {
	richSpec := BackendSpec{
		Capabilities: Capabilities{
			CapabilityPlanMode,
			CapabilityStructuredApprovals,
			CapabilityUserInputRequests,
		},
		SupportedAccessProfiles: map[AccessProfile]struct{}{
			AccessProfileDefault:    {},
			AccessProfileFullAccess: {},
		},
		SupportedStartupModes: map[StartupMode]struct{}{
			StartupModeDefault: {},
			StartupModePlan:    {},
		},
	}
	if !richSpec.supportsAccessProfile(AccessProfileDefault) {
		t.Fatal("expected default access profile to be supported")
	}
	if !richSpec.supportsAccessProfile(AccessProfileFullAccess) {
		t.Fatal("expected full access profile to be supported")
	}
	if richSpec.supportsAccessProfile(AccessProfilePlanThenFullAccess) {
		t.Fatal("expected plan-then-full-access to be rejected")
	}
	if !richSpec.supportsStartupMode(StartupModeDefault) {
		t.Fatal("expected default startup mode to be supported")
	}
	if !richSpec.supportsStartupMode(StartupModePlan) {
		t.Fatal("expected plan startup mode to be supported")
	}
	if richSpec.supportsStartupMode(StartupMode("burst")) {
		t.Fatal("expected unknown startup mode to be rejected")
	}
	if !richSpec.supportsApprovalSurface(ApprovalSurfaceUnknown) {
		t.Fatal("expected unknown approval surface to be allowed")
	}
	if !richSpec.supportsApprovalSurface(ApprovalSurfaceCommand) {
		t.Fatal("expected command approval to be supported")
	}
	if !richSpec.supportsApprovalSurface(ApprovalSurfaceFileEdit) {
		t.Fatal("expected file edit approval to be supported")
	}
	if !richSpec.supportsApprovalSurface(ApprovalSurfaceProtectedWrite) {
		t.Fatal("expected protected write approval to be supported")
	}
	if !richSpec.supportsApprovalSurface(ApprovalSurfaceUserInput) {
		t.Fatal("expected user input approval to be supported")
	}
	if !richSpec.supportsApprovalSurface(ApprovalSurfacePlanCheckpoint) {
		t.Fatal("expected plan checkpoint approval to be supported")
	}
	if richSpec.supportsApprovalSurface(ApprovalSurface("weird")) {
		t.Fatal("expected unknown approval surface to be rejected")
	}

	structuredOnly := BackendSpec{
		Capabilities: Capabilities{CapabilityStructuredApprovals},
	}
	if structuredOnly.supportsApprovalSurface(ApprovalSurfaceUserInput) {
		t.Fatal("expected user input approval to require a dedicated capability")
	}
	if structuredOnly.supportsApprovalSurface(ApprovalSurfacePlanCheckpoint) {
		t.Fatal("expected plan checkpoint approval to require a plan capability")
	}

	checkpointArtifacts := BackendSpec{
		Capabilities: Capabilities{CapabilityPlanCheckpointArtifacts},
	}
	if !checkpointArtifacts.supportsApprovalSurface(ApprovalSurfacePlanCheckpoint) {
		t.Fatal("expected plan checkpoint artifacts to satisfy plan checkpoint approval")
	}

	emptySpec := BackendSpec{}
	if !emptySpec.supportsAccessProfile(AccessProfilePlanThenFullAccess) {
		t.Fatal("expected empty access profile list to accept any profile")
	}
	if !emptySpec.supportsStartupMode(StartupModePlan) {
		t.Fatal("expected empty startup mode list to accept any mode")
	}
}

func TestBackendSpecValidatePolicyRejectsUnsupportedPolicies(t *testing.T) {
	baseSpec := BackendSpec{
		Name: BackendCodex,
		DefaultPolicy: EffectivePolicy{
			AccessProfile:   AccessProfileDefault,
			StartupMode:     StartupModeDefault,
			ApprovalSurface: ApprovalSurfaceCommand,
		},
		SupportedAccessProfiles: map[AccessProfile]struct{}{
			AccessProfileDefault: {},
		},
		SupportedStartupModes: map[StartupMode]struct{}{
			StartupModeDefault: {},
		},
		Capabilities: Capabilities{
			CapabilityStructuredApprovals,
		},
	}

	tests := []struct {
		name   string
		spec   BackendSpec
		policy EffectivePolicy
		want   string
	}{
		{
			name:   "access profile",
			spec:   baseSpec,
			policy: EffectivePolicy{AccessProfile: AccessProfileFullAccess, StartupMode: StartupModeDefault, ApprovalSurface: ApprovalSurfaceCommand},
			want:   "access profile",
		},
		{
			name: "startup mode",
			spec: func() BackendSpec {
				spec := baseSpec
				spec.Capabilities = Capabilities{CapabilityStructuredApprovals, CapabilityPlanMode}
				return spec
			}(),
			policy: EffectivePolicy{AccessProfile: AccessProfileDefault, StartupMode: StartupModePlan, ApprovalSurface: ApprovalSurfaceCommand},
			want:   "startup mode",
		},
		{
			name: "approval surface",
			spec: func() BackendSpec {
				spec := baseSpec
				spec.Capabilities = Capabilities{}
				return spec
			}(),
			policy: EffectivePolicy{AccessProfile: AccessProfileDefault, StartupMode: StartupModeDefault, ApprovalSurface: ApprovalSurfaceUserInput},
			want:   "approval surface",
		},
		{
			name: "plan mode",
			spec: func() BackendSpec {
				spec := baseSpec
				spec.Capabilities = Capabilities{CapabilityStructuredApprovals}
				return spec
			}(),
			policy: EffectivePolicy{AccessProfile: AccessProfileDefault, StartupMode: StartupModePlan, ApprovalSurface: ApprovalSurfaceCommand},
			want:   "does not support plan mode",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.ValidatePolicy(tc.policy)
			if err == nil {
				t.Fatalf("expected policy validation to fail for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestRegistryRegisterLookupAndResolveBackend(t *testing.T) {
	var nilRegistry *Registry
	if err := nilRegistry.Register(BackendSpec{Name: BackendCodex}); err == nil {
		t.Fatal("expected nil registry registration to fail")
	}
	if _, ok := nilRegistry.Lookup("codex"); ok {
		t.Fatal("expected nil registry lookup to fail")
	}

	registry := &Registry{}
	if err := registry.Register(BackendSpec{}); err == nil {
		t.Fatal("expected empty backend name to fail")
	}

	if err := registry.Register(BackendSpec{
		Name: Backend("  CLAUDE-Code "),
		DefaultPolicy: EffectivePolicy{
			AccessProfile:   AccessProfileFullAccess,
			StartupMode:     StartupModePlan,
			ApprovalSurface: ApprovalSurfaceFileEdit,
		},
		Capabilities: Capabilities{
			CapabilityStructuredApprovals,
			CapabilityUserInputRequests,
			CapabilityUserInputRequests,
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := registry.Lookup("claude code")
	if !ok {
		t.Fatal("expected lookup to succeed for registered backend")
	}
	if got.Name != BackendClaude {
		t.Fatalf("expected normalized backend name, got %q", got.Name)
	}
	if got.DefaultPolicy.AccessProfile != AccessProfileFullAccess {
		t.Fatalf("expected normalized access profile, got %q", got.DefaultPolicy.AccessProfile)
	}
	if got.DefaultPolicy.StartupMode != StartupModePlan {
		t.Fatalf("expected normalized startup mode, got %q", got.DefaultPolicy.StartupMode)
	}
	if got.DefaultPolicy.ApprovalSurface != ApprovalSurfaceFileEdit {
		t.Fatalf("expected normalized approval surface, got %q", got.DefaultPolicy.ApprovalSurface)
	}
	if !got.Capabilities.Has(CapabilityStructuredApprovals) || !got.Capabilities.Has(CapabilityUserInputRequests) {
		t.Fatalf("expected capabilities to be normalized and deduplicated, got %+v", got.Capabilities)
	}
	if _, ok := got.SupportedAccessProfiles[AccessProfileDefault]; !ok {
		t.Fatalf("expected default access profile support to be synthesized, got %+v", got.SupportedAccessProfiles)
	}
	if _, ok := got.SupportedStartupModes[StartupModeDefault]; !ok {
		t.Fatalf("expected default startup mode support to be synthesized, got %+v", got.SupportedStartupModes)
	}
	if _, ok := registry.Lookup("definitely-missing"); ok {
		t.Fatal("expected unknown backend lookup to fail")
	}

	fallbackRegistry := NewRegistry(DefaultCodexBackend())
	fallback, err := fallbackRegistry.ResolveBackend("", "", "")
	if err != nil {
		t.Fatalf("ResolveBackend fallback: %v", err)
	}
	if fallback.Name != BackendCodex {
		t.Fatalf("expected fallback backend to be codex, got %q", fallback.Name)
	}

	if _, err := fallbackRegistry.ResolveBackend("not-a-runtime", "", ""); err == nil || !strings.Contains(err.Error(), "unknown runtime") {
		t.Fatalf("expected parse failure for invalid runtime, got %v", err)
	}

	if _, err := fallbackRegistry.ResolveBackend("claude", "", ""); err == nil || !strings.Contains(err.Error(), "unknown runtime") {
		t.Fatalf("expected lookup failure for unregistered runtime, got %v", err)
	}
}

func TestNewVerifyResult(t *testing.T) {
	result := NewVerifyResult()
	if !result.OK {
		t.Fatal("expected verify result to start as OK")
	}
	if result.Checks == nil || result.Remediation == nil {
		t.Fatalf("expected verify result maps to be initialized: %+v", result)
	}
	if len(result.Checks) != 0 || len(result.Remediation) != 0 {
		t.Fatalf("expected verify result maps to be empty: %+v", result)
	}
}
