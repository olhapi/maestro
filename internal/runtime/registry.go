package runtime

import (
	"fmt"
	"strings"
	"sync"
)

type Selection struct {
	Backend Backend
	Spec    BackendSpec
	Policy  EffectivePolicy
}

type SelectionInput struct {
	IssueRuntime    string
	ProjectRuntime  string
	WorkflowRuntime string

	IssueAccessProfile    string
	ProjectAccessProfile  string
	WorkflowAccessProfile string
	IssueStartupMode      string
	WorkflowStartupMode   string
}

type Registry struct {
	mu       sync.RWMutex
	backends map[Backend]BackendSpec
}

func NewRegistry(specs ...BackendSpec) *Registry {
	r := &Registry{backends: make(map[Backend]BackendSpec, len(specs))}
	for _, spec := range specs {
		_ = r.Register(spec)
	}
	return r
}

func DefaultRegistry() *Registry {
	return NewRegistry(
		DefaultCodexBackend(),
		DefaultClaudeBackend(),
	)
}

func (r *Registry) Register(spec BackendSpec) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	spec.Name = NormalizeBackend(string(spec.Name))
	if spec.Name == BackendUnknown {
		return fmt.Errorf("backend name is required")
	}
	spec.DefaultPolicy = spec.DefaultPolicy.Normalize()
	spec.Capabilities = spec.Capabilities.Normalize()
	if len(spec.SupportedAccessProfiles) == 0 {
		spec.SupportedAccessProfiles = map[AccessProfile]struct{}{
			AccessProfileDefault: {},
		}
	}
	if len(spec.SupportedStartupModes) == 0 {
		spec.SupportedStartupModes = map[StartupMode]struct{}{
			StartupModeDefault: {},
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.backends == nil {
		r.backends = make(map[Backend]BackendSpec)
	}
	r.backends[spec.Name] = spec
	return nil
}

func (r *Registry) Lookup(raw string) (BackendSpec, bool) {
	if r == nil {
		return BackendSpec{}, false
	}
	name := NormalizeBackend(raw)
	if name == BackendUnknown {
		return BackendSpec{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.backends[name]
	return spec, ok
}

func (r *Registry) ResolveBackend(issueRuntime, projectRuntime, workflowRuntime string) (BackendSpec, error) {
	for _, raw := range []string{issueRuntime, projectRuntime, workflowRuntime} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name, err := ParseBackend(raw)
		if err != nil {
			return BackendSpec{}, fmt.Errorf("unknown runtime %q: %w", raw, err)
		}
		spec, ok := r.Lookup(string(name))
		if !ok {
			return BackendSpec{}, fmt.Errorf("unknown runtime %q", raw)
		}
		return spec, nil
	}
	spec, ok := r.Lookup(string(BackendCodex))
	if !ok {
		return BackendSpec{}, fmt.Errorf("unknown runtime %q", BackendCodex)
	}
	return spec, nil
}

func (r *Registry) ResolveSelection(input SelectionInput) (Selection, error) {
	spec, err := r.ResolveBackend(input.IssueRuntime, input.ProjectRuntime, input.WorkflowRuntime)
	if err != nil {
		return Selection{}, err
	}
	policy, err := ResolveEffectivePolicy(spec, input)
	if err != nil {
		return Selection{}, err
	}
	return Selection{
		Backend: spec.Name,
		Spec:    spec,
		Policy:  policy,
	}, nil
}

func ResolveEffectivePolicy(spec BackendSpec, input SelectionInput) (EffectivePolicy, error) {
	policy := spec.DefaultPolicy.Normalize()

	if raw := strings.TrimSpace(input.IssueAccessProfile); raw != "" {
		value, err := ParseAccessProfile(raw)
		if err != nil {
			return EffectivePolicy{}, fmt.Errorf("unsupported policy for runtime %q: %w", spec.Name, err)
		}
		policy.AccessProfile = value
	} else if raw := strings.TrimSpace(input.ProjectAccessProfile); raw != "" {
		value, err := ParseAccessProfile(raw)
		if err != nil {
			return EffectivePolicy{}, fmt.Errorf("unsupported policy for runtime %q: %w", spec.Name, err)
		}
		policy.AccessProfile = value
	} else if raw := strings.TrimSpace(input.WorkflowAccessProfile); raw != "" {
		value, err := ParseAccessProfile(raw)
		if err != nil {
			return EffectivePolicy{}, fmt.Errorf("unsupported policy for runtime %q: %w", spec.Name, err)
		}
		policy.AccessProfile = value
	}

	if raw := strings.TrimSpace(input.IssueStartupMode); raw != "" {
		value, err := ParseStartupMode(raw)
		if err != nil {
			return EffectivePolicy{}, fmt.Errorf("unsupported policy for runtime %q: %w", spec.Name, err)
		}
		policy.StartupMode = value
	} else if raw := strings.TrimSpace(input.WorkflowStartupMode); raw != "" {
		value, err := ParseStartupMode(raw)
		if err != nil {
			return EffectivePolicy{}, fmt.Errorf("unsupported policy for runtime %q: %w", spec.Name, err)
		}
		policy.StartupMode = value
	}

	if policy.ApprovalSurface == ApprovalSurfaceUnknown {
		policy.ApprovalSurface = spec.DefaultPolicy.ApprovalSurface
	}

	if err := spec.ValidatePolicy(policy); err != nil {
		return EffectivePolicy{}, err
	}
	return policy.Normalize(), nil
}

func (s BackendSpec) ValidatePolicy(policy EffectivePolicy) error {
	policy = policy.Normalize()
	if s.Name == BackendUnknown {
		return fmt.Errorf("backend name is required")
	}
	if s.isPlanModeRequested(policy) && !s.Capabilities.Has(CapabilityPlanMode) {
		return fmt.Errorf("runtime %q does not support plan mode", s.Name)
	}
	if !s.supportsAccessProfile(policy.AccessProfile) {
		return fmt.Errorf("unsupported policy for runtime %q: access profile %q", s.Name, policy.AccessProfile)
	}
	if !s.supportsStartupMode(policy.StartupMode) {
		return fmt.Errorf("unsupported policy for runtime %q: startup mode %q", s.Name, policy.StartupMode)
	}
	if !s.supportsApprovalSurface(policy.ApprovalSurface) {
		return fmt.Errorf("approval surface %q not supported by runtime %q", policy.ApprovalSurface, s.Name)
	}
	return nil
}

func (s BackendSpec) isPlanModeRequested(policy EffectivePolicy) bool {
	return policy.StartupMode == StartupModePlan || policy.ApprovalSurface == ApprovalSurfacePlanCheckpoint
}

func (s BackendSpec) supportsAccessProfile(profile AccessProfile) bool {
	if len(s.SupportedAccessProfiles) == 0 {
		return true
	}
	_, ok := s.SupportedAccessProfiles[profile]
	return ok
}

func (s BackendSpec) supportsStartupMode(mode StartupMode) bool {
	if len(s.SupportedStartupModes) == 0 {
		return true
	}
	_, ok := s.SupportedStartupModes[mode]
	return ok
}

func (s BackendSpec) supportsApprovalSurface(surface ApprovalSurface) bool {
	switch surface {
	case ApprovalSurfaceUnknown:
		return true
	case ApprovalSurfaceCommand, ApprovalSurfaceFileEdit, ApprovalSurfaceProtectedWrite:
		return s.Capabilities.Has(CapabilityStructuredApprovals)
	case ApprovalSurfaceUserInput:
		return s.Capabilities.Has(CapabilityUserInputRequests)
	case ApprovalSurfacePlanCheckpoint:
		return s.Capabilities.Has(CapabilityPlanMode) || s.Capabilities.Has(CapabilityPlanCheckpointArtifacts)
	default:
		return false
	}
}

func DefaultCodexBackend() BackendSpec {
	return BackendSpec{
		Name: BackendCodex,
		DefaultPolicy: EffectivePolicy{
			AccessProfile:   AccessProfileDefault,
			StartupMode:     StartupModeDefault,
			ApprovalSurface: ApprovalSurfaceCommand,
		},
		SupportedAccessProfiles: map[AccessProfile]struct{}{
			AccessProfileDefault:            {},
			AccessProfileFullAccess:         {},
			AccessProfilePlanThenFullAccess: {},
		},
		SupportedStartupModes: map[StartupMode]struct{}{
			StartupModeDefault: {},
			StartupModePlan:    {},
		},
		Capabilities: Capabilities{
			CapabilityAuthSourceObservability,
			CapabilityDynamicPolicyUpdate,
			CapabilityNativeRemoteControl,
			CapabilityPlanCheckpointArtifacts,
			CapabilityPlanMode,
			CapabilityResumableSessions,
			CapabilityStructuredApprovals,
			CapabilityStreamingEvents,
			CapabilityUserInputRequests,
			CapabilityWorktreeOwnership,
		},
	}
}

func DefaultClaudeBackend() BackendSpec {
	return BackendSpec{
		Name: BackendClaude,
		DefaultPolicy: EffectivePolicy{
			AccessProfile:   AccessProfileDefault,
			StartupMode:     StartupModeDefault,
			ApprovalSurface: ApprovalSurfaceCommand,
		},
		SupportedAccessProfiles: map[AccessProfile]struct{}{
			AccessProfileDefault:            {},
			AccessProfileFullAccess:         {},
			AccessProfilePlanThenFullAccess: {},
		},
		SupportedStartupModes: map[StartupMode]struct{}{
			StartupModeDefault: {},
			StartupModePlan:    {},
		},
		Capabilities: Capabilities{
			CapabilityAuthSourceObservability,
			CapabilityNativeRemoteControl,
			CapabilityPlanCheckpointArtifacts,
			CapabilityPlanMode,
			CapabilityResumableSessions,
			CapabilityStructuredApprovals,
			CapabilityStreamingEvents,
			CapabilityUserInputRequests,
			CapabilityWorktreeOwnership,
		},
	}
}
