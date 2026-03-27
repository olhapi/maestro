package runtime

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func normalizeToken(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(raw))
	lastSeparator := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSeparator = false
		case r == '_' || r == '-' || r == '.' || r == ' ' || r == '/':
			if b.Len() == 0 || lastSeparator {
				continue
			}
			b.WriteByte('_')
			lastSeparator = true
		}
	}

	return strings.Trim(b.String(), "_")
}

func normalizeEnum(raw string, fallback string, values map[string]string) string {
	if normalized, ok := values[normalizeToken(raw)]; ok {
		return normalized
	}
	return fallback
}

func parseEnum(raw string, fallback string, values map[string]string, kind string) (string, error) {
	token := normalizeToken(raw)
	if token == "" {
		return fallback, nil
	}
	if normalized, ok := values[token]; ok {
		return normalized, nil
	}
	return fallback, fmt.Errorf("unsupported %s %q", kind, strings.TrimSpace(raw))
}

type Backend string

const (
	BackendUnknown Backend = ""
	BackendCodex   Backend = "codex"
	BackendClaude  Backend = "claude"
)

var backendValues = map[string]string{
	"codex":       string(BackendCodex),
	"claude":      string(BackendClaude),
	"claude_code": string(BackendClaude),
}

func NormalizeBackend(raw string) Backend {
	return Backend(normalizeEnum(raw, string(BackendUnknown), backendValues))
}

func ParseBackend(raw string) (Backend, error) {
	normalized, err := parseEnum(raw, string(BackendUnknown), backendValues, "backend")
	return Backend(normalized), err
}

type Capability string

const (
	CapabilityUnknown                 Capability = ""
	CapabilityStreamingEvents         Capability = "streaming_events"
	CapabilityResumableSessions       Capability = "resumable_sessions"
	CapabilityPlanMode                Capability = "plan_mode"
	CapabilityPlanCheckpointArtifacts Capability = "plan_checkpoint_artifacts"
	CapabilityStructuredApprovals     Capability = "structured_approvals"
	CapabilityUserInputRequests       Capability = "user_input_requests"
	CapabilityDynamicPolicyUpdate     Capability = "dynamic_policy_update"
	CapabilityNativeRemoteControl     Capability = "native_remote_control"
	CapabilityWorktreeOwnership       Capability = "worktree_ownership"
	CapabilityAuthSourceObservability Capability = "auth_source_observability"
)

var capabilityValues = map[string]Capability{
	"streaming_events":          CapabilityStreamingEvents,
	"resumable_sessions":        CapabilityResumableSessions,
	"plan_mode":                 CapabilityPlanMode,
	"plan_checkpoint_artifacts": CapabilityPlanCheckpointArtifacts,
	"structured_approvals":      CapabilityStructuredApprovals,
	"user_input_requests":       CapabilityUserInputRequests,
	"dynamic_policy_update":     CapabilityDynamicPolicyUpdate,
	"native_remote_control":     CapabilityNativeRemoteControl,
	"worktree_ownership":        CapabilityWorktreeOwnership,
	"auth_source_observability": CapabilityAuthSourceObservability,
}

func NormalizeCapability(raw string) Capability {
	if value, ok := capabilityValues[normalizeToken(raw)]; ok {
		return value
	}
	return CapabilityUnknown
}

func ParseCapability(raw string) (Capability, error) {
	token := normalizeToken(raw)
	if token == "" {
		return CapabilityUnknown, nil
	}
	if value, ok := capabilityValues[token]; ok {
		return value, nil
	}
	return CapabilityUnknown, fmt.Errorf("unsupported capability %q", strings.TrimSpace(raw))
}

type Capabilities []Capability

func NormalizeCapabilities(raw []string) Capabilities {
	if len(raw) == 0 {
		return nil
	}
	out := make(Capabilities, 0, len(raw))
	seen := make(map[Capability]struct{}, len(raw))
	for _, item := range raw {
		capability := NormalizeCapability(item)
		if capability == CapabilityUnknown {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

func ParseCapabilities(raw []string) (Capabilities, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(Capabilities, 0, len(raw))
	seen := make(map[Capability]struct{}, len(raw))
	for _, item := range raw {
		token := normalizeToken(item)
		if token == "" {
			continue
		}
		capability, ok := capabilityValues[token]
		if !ok {
			return nil, fmt.Errorf("unsupported capability %q", strings.TrimSpace(item))
		}
		if _, dup := seen[capability]; dup {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (c Capabilities) Normalize() Capabilities {
	if len(c) == 0 {
		return nil
	}
	raw := make([]string, 0, len(c))
	for _, capability := range c {
		raw = append(raw, string(capability))
	}
	return NormalizeCapabilities(raw)
}

func (c Capabilities) Has(target Capability) bool {
	for _, capability := range c {
		if capability == target {
			return true
		}
	}
	return false
}

type AccessProfile string

const (
	AccessProfileUnknown            AccessProfile = ""
	AccessProfileDefault            AccessProfile = "default"
	AccessProfileFullAccess         AccessProfile = "full-access"
	AccessProfilePlanThenFullAccess AccessProfile = "plan-then-full-access"
)

var accessProfileValues = map[string]string{
	"default":               string(AccessProfileDefault),
	"full_access":           string(AccessProfileFullAccess),
	"fullaccess":            string(AccessProfileFullAccess),
	"plan_then_full_access": string(AccessProfilePlanThenFullAccess),
	"planthenfullaccess":    string(AccessProfilePlanThenFullAccess),
}

func NormalizeAccessProfile(raw string) AccessProfile {
	return AccessProfile(normalizeEnum(raw, string(AccessProfileDefault), accessProfileValues))
}

func ParseAccessProfile(raw string) (AccessProfile, error) {
	normalized, err := parseEnum(raw, string(AccessProfileDefault), accessProfileValues, "access profile")
	return AccessProfile(normalized), err
}

type StartupMode string

const (
	StartupModeUnknown StartupMode = ""
	StartupModeDefault StartupMode = "default"
	StartupModePlan    StartupMode = "plan"
)

var startupModeValues = map[string]string{
	"default": string(StartupModeDefault),
	"plan":    string(StartupModePlan),
}

func NormalizeStartupMode(raw string) StartupMode {
	return StartupMode(normalizeEnum(raw, string(StartupModeDefault), startupModeValues))
}

func ParseStartupMode(raw string) (StartupMode, error) {
	normalized, err := parseEnum(raw, string(StartupModeDefault), startupModeValues, "startup mode")
	return StartupMode(normalized), err
}

type ApprovalSurface string

const (
	ApprovalSurfaceUnknown        ApprovalSurface = ""
	ApprovalSurfaceCommand        ApprovalSurface = "command"
	ApprovalSurfaceFileEdit       ApprovalSurface = "file_edit"
	ApprovalSurfaceProtectedWrite ApprovalSurface = "protected_write"
	ApprovalSurfaceUserInput      ApprovalSurface = "user_input"
	ApprovalSurfacePlanCheckpoint ApprovalSurface = "plan_checkpoint"
)

var approvalSurfaceValues = map[string]string{
	"command":         string(ApprovalSurfaceCommand),
	"file_edit":       string(ApprovalSurfaceFileEdit),
	"filechange":      string(ApprovalSurfaceFileEdit),
	"file_change":     string(ApprovalSurfaceFileEdit),
	"protected_write": string(ApprovalSurfaceProtectedWrite),
	"user_input":      string(ApprovalSurfaceUserInput),
	"plan_checkpoint": string(ApprovalSurfacePlanCheckpoint),
}

func NormalizeApprovalSurface(raw string) ApprovalSurface {
	return ApprovalSurface(normalizeEnum(raw, string(ApprovalSurfaceUnknown), approvalSurfaceValues))
}

func ParseApprovalSurface(raw string) (ApprovalSurface, error) {
	normalized, err := parseEnum(raw, string(ApprovalSurfaceUnknown), approvalSurfaceValues, "approval surface")
	return ApprovalSurface(normalized), err
}

type EventKind string

const (
	EventKindUnknown              EventKind = ""
	EventKindSessionStarted       EventKind = "session_started"
	EventKindSessionResumed       EventKind = "session_resumed"
	EventKindSessionStopped       EventKind = "session_stopped"
	EventKindTurnStarted          EventKind = "turn_started"
	EventKindTurnCompleted        EventKind = "turn_completed"
	EventKindTurnFailed           EventKind = "turn_failed"
	EventKindTurnCancelled        EventKind = "turn_cancelled"
	EventKindInteractionRequested EventKind = "interaction_requested"
	EventKindInteractionResolved  EventKind = "interaction_resolved"
	EventKindOutputDelta          EventKind = "output_delta"
)

var eventKindValues = map[string]string{
	"session_started":       string(EventKindSessionStarted),
	"session_resumed":       string(EventKindSessionResumed),
	"session_stopped":       string(EventKindSessionStopped),
	"turn_started":          string(EventKindTurnStarted),
	"turn_completed":        string(EventKindTurnCompleted),
	"turn_failed":           string(EventKindTurnFailed),
	"turn_cancelled":        string(EventKindTurnCancelled),
	"interaction_requested": string(EventKindInteractionRequested),
	"interaction_resolved":  string(EventKindInteractionResolved),
	"output_delta":          string(EventKindOutputDelta),
}

func NormalizeEventKind(raw string) EventKind {
	return EventKind(normalizeEnum(raw, string(EventKindUnknown), eventKindValues))
}

func ParseEventKind(raw string) (EventKind, error) {
	normalized, err := parseEnum(raw, string(EventKindUnknown), eventKindValues, "event kind")
	return EventKind(normalized), err
}

type InteractionRequestKind string

const (
	InteractionRequestKindUnknown               InteractionRequestKind = ""
	InteractionRequestKindApproveCommand        InteractionRequestKind = "approve_command"
	InteractionRequestKindApproveFileEdit       InteractionRequestKind = "approve_file_edit"
	InteractionRequestKindApproveProtectedWrite InteractionRequestKind = "approve_protected_write"
	InteractionRequestKindRequestUserInput      InteractionRequestKind = "request_user_input"
	InteractionRequestKindPlanCheckpoint        InteractionRequestKind = "plan_checkpoint"
)

var interactionRequestKindValues = map[string]string{
	"approve_command":          string(InteractionRequestKindApproveCommand),
	"command_approval":         string(InteractionRequestKindApproveCommand),
	"approve_file_edit":        string(InteractionRequestKindApproveFileEdit),
	"approve_file_change":      string(InteractionRequestKindApproveFileEdit),
	"file_edit_approval":       string(InteractionRequestKindApproveFileEdit),
	"approve_protected_write":   string(InteractionRequestKindApproveProtectedWrite),
	"protected_write_approval":  string(InteractionRequestKindApproveProtectedWrite),
	"request_user_input":        string(InteractionRequestKindRequestUserInput),
	"user_input_request":        string(InteractionRequestKindRequestUserInput),
	"plan_checkpoint":          string(InteractionRequestKindPlanCheckpoint),
}

func NormalizeInteractionRequestKind(raw string) InteractionRequestKind {
	return InteractionRequestKind(normalizeEnum(raw, string(InteractionRequestKindUnknown), interactionRequestKindValues))
}

func ParseInteractionRequestKind(raw string) (InteractionRequestKind, error) {
	normalized, err := parseEnum(raw, string(InteractionRequestKindUnknown), interactionRequestKindValues, "interaction request kind")
	return InteractionRequestKind(normalized), err
}

func (k InteractionRequestKind) ApprovalSurface() ApprovalSurface {
	switch k {
	case InteractionRequestKindApproveCommand:
		return ApprovalSurfaceCommand
	case InteractionRequestKindApproveFileEdit:
		return ApprovalSurfaceFileEdit
	case InteractionRequestKindApproveProtectedWrite:
		return ApprovalSurfaceProtectedWrite
	case InteractionRequestKindRequestUserInput:
		return ApprovalSurfaceUserInput
	case InteractionRequestKindPlanCheckpoint:
		return ApprovalSurfacePlanCheckpoint
	default:
		return ApprovalSurfaceUnknown
	}
}

func (s ApprovalSurface) RequestKind() InteractionRequestKind {
	switch s {
	case ApprovalSurfaceCommand:
		return InteractionRequestKindApproveCommand
	case ApprovalSurfaceFileEdit:
		return InteractionRequestKindApproveFileEdit
	case ApprovalSurfaceProtectedWrite:
		return InteractionRequestKindApproveProtectedWrite
	case ApprovalSurfaceUserInput:
		return InteractionRequestKindRequestUserInput
	case ApprovalSurfacePlanCheckpoint:
		return InteractionRequestKindPlanCheckpoint
	default:
		return InteractionRequestKindUnknown
	}
}

type InteractionDecision struct {
	Value       string                 `json:"value"`
	Label       string                 `json:"label"`
	Description string                 `json:"description,omitempty"`
	Payload     map[string]interface{} `json:"payload,omitempty"`
}

type InteractionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type InteractionQuestion struct {
	Header   string              `json:"header,omitempty"`
	ID       string              `json:"id"`
	Question string              `json:"question,omitempty"`
	Options  []InteractionOption `json:"options,omitempty"`
	IsOther  bool                `json:"is_other,omitempty"`
	IsSecret bool                `json:"is_secret,omitempty"`
}

type InteractionRequest struct {
	ID             string                 `json:"id,omitempty"`
	RequestID      string                 `json:"request_id,omitempty"`
	Kind           InteractionRequestKind `json:"kind"`
	SessionID      string                 `json:"session_id,omitempty"`
	TurnID         string                 `json:"turn_id,omitempty"`
	ItemID         string                 `json:"item_id,omitempty"`
	Surface        ApprovalSurface        `json:"surface,omitempty"`
	Command        string                 `json:"command,omitempty"`
	WorkingDir     string                 `json:"working_dir,omitempty"`
	Paths          []string               `json:"paths,omitempty"`
	Reason         string                 `json:"reason,omitempty"`
	Decisions      []InteractionDecision  `json:"decisions,omitempty"`
	Questions      []InteractionQuestion  `json:"questions,omitempty"`
	Plan           string                 `json:"plan,omitempty"`
	RequestedAt    time.Time              `json:"requested_at,omitempty"`
	LastActivityAt *time.Time             `json:"last_activity_at,omitempty"`
	LastActivity   string                 `json:"last_activity,omitempty"`
}

type InteractionResponse struct {
	Decision        string                 `json:"decision,omitempty"`
	DecisionPayload map[string]interface{} `json:"decision_payload,omitempty"`
	Answers         map[string][]string    `json:"answers,omitempty"`
	Note            string                 `json:"note,omitempty"`
}

type Event struct {
	Kind          EventKind              `json:"kind"`
	SessionID     string                 `json:"session_id,omitempty"`
	TurnID        string                 `json:"turn_id,omitempty"`
	RequestID     string                 `json:"request_id,omitempty"`
	InteractionID string                 `json:"interaction_id,omitempty"`
	Surface       ApprovalSurface        `json:"surface,omitempty"`
	Message       string                 `json:"message,omitempty"`
	Data          map[string]interface{} `json:"data,omitempty"`
	OccurredAt    time.Time              `json:"occurred_at,omitempty"`
}

type EffectivePolicy struct {
	AccessProfile   AccessProfile   `json:"access_profile"`
	StartupMode     StartupMode     `json:"startup_mode"`
	ApprovalSurface ApprovalSurface `json:"approval_surface"`
	Capabilities    Capabilities    `json:"capabilities,omitempty"`
}

func (p EffectivePolicy) Normalize() EffectivePolicy {
	p.AccessProfile = NormalizeAccessProfile(string(p.AccessProfile))
	p.StartupMode = NormalizeStartupMode(string(p.StartupMode))
	p.ApprovalSurface = NormalizeApprovalSurface(string(p.ApprovalSurface))
	p.Capabilities = p.Capabilities.Normalize()
	return p
}

type SessionInfo struct {
	Backend    Backend         `json:"backend"`
	SessionID  string          `json:"session_id,omitempty"`
	ThreadID   string          `json:"thread_id,omitempty"`
	TurnID     string          `json:"turn_id,omitempty"`
	Command    string          `json:"command,omitempty"`
	WorkingDir string          `json:"working_dir,omitempty"`
	Policy     *EffectivePolicy `json:"policy,omitempty"`
	StartedAt  time.Time       `json:"started_at,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at,omitempty"`
}

type Session struct {
	Info            SessionInfo `json:"info"`
	LastEvent       *Event      `json:"last_event,omitempty"`
	LastMessage     string      `json:"last_message,omitempty"`
	InputTokens     int         `json:"input_tokens"`
	OutputTokens    int         `json:"output_tokens"`
	TotalTokens     int         `json:"total_tokens"`
	EventsProcessed int         `json:"events_processed"`
	TurnsStarted    int         `json:"turns_started"`
	TurnsCompleted  int         `json:"turns_completed"`
	Terminal        bool        `json:"terminal"`
	TerminalReason  string      `json:"terminal_reason,omitempty"`
	History         []Event     `json:"history,omitempty"`
}

type VerifyResult struct {
	OK          bool              `json:"ok"`
	Checks      map[string]string `json:"checks"`
	Errors      []string          `json:"errors,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
	Remediation map[string]string `json:"remediation"`
}

func NewVerifyResult() VerifyResult {
	return VerifyResult{
		OK:          true,
		Checks:      map[string]string{},
		Remediation: map[string]string{},
	}
}

// BackendSpec describes one concrete backend entry in the registry.
type BackendSpec struct {
	Name                    Backend
	DefaultPolicy           EffectivePolicy
	SupportedAccessProfiles map[AccessProfile]struct{}
	SupportedStartupModes   map[StartupMode]struct{}
	Capabilities           Capabilities
}
