package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/appserver/protocol"
	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
)

// Re-export the app-server data shapes behind the runtime package boundary.
type Session = appserver.Session
type ActivityEvent = appserver.ActivityEvent
type PendingInteraction = appserver.PendingInteraction
type PendingInteractionResponse = appserver.PendingInteractionResponse
type InteractionResponder = appserver.InteractionResponder
type PendingInteractionSnapshot = appserver.PendingInteractionSnapshot
type ToolExecutor = appserver.ToolExecutor
type UserInputElement = gen.UserInputElement

const PlanApprovalStopReason = "plan_approval_pending"

type AppServerBackend interface {
	RunTurn(ctx context.Context, prompt, title string) error
	RunTurnWithStartCallback(ctx context.Context, prompt, title string, onStarted func(*Session)) error
	RunTurnWithInputs(ctx context.Context, input []UserInputElement, title string) error
	RunTurnWithInputsAndStartCallback(ctx context.Context, input []UserInputElement, title string, onStarted func(*Session)) error
	UpdatePermissionConfig(approvalPolicy interface{}, threadSandbox string, turnSandboxPolicy map[string]interface{})
	Session() *Session
	Output() string
	RespondToInteraction(ctx context.Context, interactionID string, response PendingInteractionResponse) error
	Close() error
}

type CodexBackendConfig struct {
	Command                  string
	Workspace                string
	WorkspaceRoot            string
	IssueID                  string
	IssueIdentifier          string
	ExpectedVersion          string
	ApprovalPolicy           interface{}
	InitialCollaborationMode string
	ThreadSandbox            string
	TurnSandboxPolicy        map[string]interface{}
	ReadTimeout              time.Duration
	TurnTimeout              time.Duration
	StallTimeout             time.Duration
	DynamicTools             []map[string]interface{}
	ToolExecutor             ToolExecutor
	Logger                   *slog.Logger
	OnSessionUpdate          func(*Session)
	OnActivityEvent          func(ActivityEvent)
	OnPendingInteraction     func(*PendingInteraction)
	OnPendingInteractionDone func(string)
	ResumeThreadID           string
	ResumeSource             string
	Env                      []string
}

type CodexBackend struct {
	client *appserver.Client
}

func NewCodexBackend(ctx context.Context, cfg CodexBackendConfig) (*CodexBackend, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("missing codex command")
	}

	env := cfg.Env
	if len(env) == 0 {
		env = os.Environ()
	}

	client, err := appserver.Start(ctx, appserver.ClientConfig{
		Executable:               "sh",
		Args:                     []string{"-lc", cfg.Command},
		Env:                      env,
		Workspace:                cfg.Workspace,
		WorkspaceRoot:            cfg.WorkspaceRoot,
		IssueID:                  cfg.IssueID,
		IssueIdentifier:          cfg.IssueIdentifier,
		CodexCommand:             cfg.Command,
		ExpectedVersion:          cfg.ExpectedVersion,
		ApprovalPolicy:           cfg.ApprovalPolicy,
		InitialCollaborationMode: cfg.InitialCollaborationMode,
		ThreadSandbox:            cfg.ThreadSandbox,
		TurnSandboxPolicy:        cfg.TurnSandboxPolicy,
		ReadTimeout:              cfg.ReadTimeout,
		TurnTimeout:              cfg.TurnTimeout,
		StallTimeout:             cfg.StallTimeout,
		DynamicTools:             cfg.DynamicTools,
		ToolExecutor:             cfg.ToolExecutor,
		Logger:                   cfg.Logger,
		OnSessionUpdate:          cfg.OnSessionUpdate,
		OnActivityEvent:          cfg.OnActivityEvent,
		OnPendingInteraction:     cfg.OnPendingInteraction,
		OnPendingInteractionDone: cfg.OnPendingInteractionDone,
		ResumeThreadID:           cfg.ResumeThreadID,
		ResumeSource:             cfg.ResumeSource,
	})
	if err != nil {
		return nil, err
	}
	return &CodexBackend{client: client}, nil
}

func (b *CodexBackend) RunTurn(ctx context.Context, prompt, title string) error {
	return b.client.RunTurn(ctx, prompt, title)
}

func (b *CodexBackend) RunTurnWithStartCallback(ctx context.Context, prompt, title string, onStarted func(*Session)) error {
	return b.client.RunTurnWithStartCallback(ctx, prompt, title, onStarted)
}

func (b *CodexBackend) RunTurnWithInputs(ctx context.Context, input []UserInputElement, title string) error {
	return b.client.RunTurnWithInputs(ctx, input, title)
}

func (b *CodexBackend) RunTurnWithInputsAndStartCallback(ctx context.Context, input []UserInputElement, title string, onStarted func(*Session)) error {
	return b.client.RunTurnWithInputsAndStartCallback(ctx, input, title, onStarted)
}

func (b *CodexBackend) UpdatePermissionConfig(approvalPolicy interface{}, threadSandbox string, turnSandboxPolicy map[string]interface{}) {
	b.client.UpdatePermissionConfig(approvalPolicy, threadSandbox, turnSandboxPolicy)
}

func (b *CodexBackend) Session() *Session {
	return b.client.Session()
}

func (b *CodexBackend) Output() string {
	return b.client.Output()
}

func (b *CodexBackend) RespondToInteraction(ctx context.Context, interactionID string, response PendingInteractionResponse) error {
	return b.client.RespondToInteraction(ctx, interactionID, response)
}

func (b *CodexBackend) Close() error {
	return b.client.Close()
}

func TextInput(text string) UserInputElement {
	return protocol.TextInput(text)
}

func LocalImageInput(path, name string) UserInputElement {
	return protocol.LocalImageInput(path, name)
}
