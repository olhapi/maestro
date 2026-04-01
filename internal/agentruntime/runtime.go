package agentruntime

import (
	"context"
	"errors"
	"time"
)

var ErrUnsupportedCapability = errors.New("unsupported_runtime_capability")

type Provider string

const (
	ProviderCodex Provider = "codex"
)

type Transport string

const (
	TransportAppServer Transport = "app_server"
	TransportStdio     Transport = "stdio"
)

type Capabilities struct {
	Resume                   bool
	QueuedInteractions       bool
	PlanGating               bool
	LocalImageInput          bool
	DynamicTools             bool
	RuntimePermissionUpdates bool
}

type PermissionConfig struct {
	ApprovalPolicy    interface{}            `json:"approval_policy,omitempty"`
	ThreadSandbox     string                 `json:"thread_sandbox,omitempty"`
	TurnSandboxPolicy map[string]interface{} `json:"turn_sandbox_policy,omitempty"`
	CollaborationMode string                 `json:"collaboration_mode,omitempty"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

type RuntimeSpec struct {
	Provider        Provider
	Transport       Transport
	Command         string
	ExpectedVersion string
	Workspace       string
	WorkspaceRoot   string
	IssueID         string
	IssueIdentifier string
	Env             []string
	ReadTimeout     time.Duration
	TurnTimeout     time.Duration
	StallTimeout    time.Duration
	Permissions     PermissionConfig
	DynamicTools    []map[string]interface{}
	ToolExecutor    ToolExecutor
	// ResumeToken is the provider-specific durable continuation token used to resume a run across processes.
	ResumeToken string
	Metadata    map[string]interface{}
}

type InputItemKind string

const (
	InputItemText       InputItemKind = "text"
	InputItemLocalImage InputItemKind = "local_image"
)

type InputItem struct {
	Kind     InputItemKind          `json:"kind"`
	Text     string                 `json:"text,omitempty"`
	Path     string                 `json:"path,omitempty"`
	Name     string                 `json:"name,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type TurnRequest struct {
	Title    string                 `json:"title,omitempty"`
	Input    []InputItem            `json:"input,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type ToolExecutor func(ctx context.Context, name string, arguments interface{}) map[string]interface{}

type Observers struct {
	OnSessionUpdate          func(*Session)
	OnActivityEvent          func(ActivityEvent)
	OnPendingInteraction     func(*PendingInteraction, InteractionResponder)
	OnPendingInteractionDone func(string)
}

type Client interface {
	Capabilities() Capabilities
	RunTurn(ctx context.Context, request TurnRequest, onStarted func(*Session)) error
	UpdatePermissions(PermissionConfig)
	RespondToInteraction(ctx context.Context, interactionID string, response PendingInteractionResponse) error
	Session() *Session
	Output() string
	Close() error
}
