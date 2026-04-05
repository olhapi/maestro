package codex

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/appserver/protocol"
	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
)

var (
	appServerCapabilities = agentruntime.Capabilities{
		Resume:                   true,
		QueuedInteractions:       true,
		PlanGating:               true,
		LocalImageInput:          true,
		DynamicTools:             true,
		RuntimePermissionUpdates: true,
	}
	stdioCapabilities = agentruntime.Capabilities{
		PlanGating: true,
	}
)

func Start(ctx context.Context, spec agentruntime.RuntimeSpec, observers agentruntime.Observers) (agentruntime.Client, error) {
	if spec.Provider != "" && spec.Provider != agentruntime.ProviderCodex {
		return nil, fmt.Errorf("%w: provider %q", agentruntime.ErrUnsupportedCapability, spec.Provider)
	}
	switch spec.Transport {
	case agentruntime.TransportAppServer:
		return startAppServer(ctx, spec, observers)
	case agentruntime.TransportStdio:
		return startStdio(spec, observers), nil
	default:
		return nil, fmt.Errorf("%w: transport %q", agentruntime.ErrUnsupportedCapability, spec.Transport)
	}
}

func CleanupLingeringProcess(pid int) error {
	return appserver.CleanupLingeringAppServerProcess(pid)
}

type appServerClient struct {
	delegate *appserver.Client
}

func startAppServer(ctx context.Context, spec agentruntime.RuntimeSpec, observers agentruntime.Observers) (agentruntime.Client, error) {
	client := &appServerClient{}
	cfg := appserver.ClientConfig{
		Executable:               "sh",
		Args:                     []string{"-lc", spec.Command},
		Env:                      spec.Env,
		Workspace:                spec.Workspace,
		WorkspaceRoot:            spec.WorkspaceRoot,
		IssueID:                  spec.IssueID,
		IssueIdentifier:          spec.IssueIdentifier,
		CodexCommand:             spec.Command,
		ExpectedVersion:          spec.ExpectedVersion,
		ApprovalPolicy:           spec.Permissions.ApprovalPolicy,
		InitialCollaborationMode: spec.Permissions.CollaborationMode,
		ThreadSandbox:            spec.Permissions.ThreadSandbox,
		TurnSandboxPolicy:        spec.Permissions.TurnSandboxPolicy,
		ReadTimeout:              spec.ReadTimeout,
		TurnTimeout:              spec.TurnTimeout,
		StallTimeout:             spec.StallTimeout,
		DynamicTools:             spec.DynamicTools,
		ToolExecutor:             appserver.ToolExecutor(spec.ToolExecutor),
		ResumeThreadID:           spec.ResumeToken,
		ResumeSource:             "orphaned_run_recovery",
		OnSessionUpdate: func(session *appserver.Session) {
			if observers.OnSessionUpdate == nil || session == nil {
				return
			}
			converted := sessionFromAppServer(session)
			observers.OnSessionUpdate(&converted)
		},
		OnActivityEvent: func(event appserver.ActivityEvent) {
			if observers.OnActivityEvent == nil {
				return
			}
			observers.OnActivityEvent(activityFromAppServer(event))
		},
		OnPendingInteraction: func(interaction *appserver.PendingInteraction) {
			if observers.OnPendingInteraction == nil || interaction == nil {
				return
			}
			converted := interactionFromAppServer(interaction)
			observers.OnPendingInteraction(&converted, client.RespondToInteraction)
		},
		OnPendingInteractionDone: observers.OnPendingInteractionDone,
	}
	if len(cfg.Env) == 0 {
		cfg.Env = os.Environ()
	}
	delegate, err := appserver.Start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	client.delegate = delegate
	return client, nil
}

func (c *appServerClient) Capabilities() agentruntime.Capabilities {
	return appServerCapabilities
}

func (c *appServerClient) RunTurn(ctx context.Context, request agentruntime.TurnRequest, onStarted func(*agentruntime.Session)) error {
	input, err := inputToAppServer(request.Input)
	if err != nil {
		return err
	}
	return c.delegate.RunTurnWithInputsAndStartCallback(ctx, input, request.Title, func(session *appserver.Session) {
		if onStarted == nil || session == nil {
			return
		}
		converted := sessionFromAppServer(session)
		onStarted(&converted)
	})
}

func (c *appServerClient) UpdatePermissions(config agentruntime.PermissionConfig) {
	c.delegate.UpdatePermissionConfig(config.ApprovalPolicy, config.ThreadSandbox, config.TurnSandboxPolicy)
}

func (c *appServerClient) RespondToInteraction(ctx context.Context, interactionID string, response agentruntime.PendingInteractionResponse) error {
	return c.delegate.RespondToInteraction(ctx, interactionID, interactionResponseToAppServer(response))
}

func (c *appServerClient) Session() *agentruntime.Session {
	if c == nil || c.delegate == nil {
		return nil
	}
	session := sessionFromAppServer(c.delegate.Session())
	return &session
}

func (c *appServerClient) Output() string {
	if c == nil || c.delegate == nil {
		return ""
	}
	return c.delegate.Output()
}

func (c *appServerClient) Close() error {
	if c == nil || c.delegate == nil {
		return nil
	}
	return c.delegate.Close()
}

func inputToAppServer(items []agentruntime.InputItem) ([]gen.UserInputElement, error) {
	out := make([]gen.UserInputElement, 0, len(items))
	for _, item := range items {
		switch item.Kind {
		case agentruntime.InputItemText:
			out = append(out, protocol.TextInput(item.Text))
		case agentruntime.InputItemLocalImage:
			out = append(out, protocol.LocalImageInput(item.Path, item.Name))
		default:
			return nil, fmt.Errorf("%w: input kind %q", agentruntime.ErrUnsupportedCapability, item.Kind)
		}
	}
	return out, nil
}

func sessionFromAppServer(session *appserver.Session) agentruntime.Session {
	if session == nil {
		return agentruntime.Session{}
	}
	return agentruntime.Session{
		IssueID:         session.IssueID,
		IssueIdentifier: session.IssueIdentifier,
		SessionID:       session.SessionID,
		ThreadID:        session.ThreadID,
		TurnID:          session.TurnID,
		ProcessID:       session.ProcessID,
		LastEvent:       session.LastEvent,
		LastTimestamp:   session.LastTimestamp,
		LastMessage:     session.LastMessage,
		InputTokens:     session.InputTokens,
		OutputTokens:    session.OutputTokens,
		TotalTokens:     session.TotalTokens,
		EventsProcessed: session.EventsProcessed,
		TurnsStarted:    session.TurnsStarted,
		TurnsCompleted:  session.TurnsCompleted,
		Terminal:        session.Terminal,
		TerminalReason:  session.TerminalReason,
		History:         append([]agentruntime.Event(nil), eventSliceFromAppServer(session.History)...),
		Metadata:        runtimeMetadata(session.Metadata, agentruntime.TransportAppServer),
		MaxHistory:      session.MaxHistory,
	}
}

func eventSliceFromAppServer(events []appserver.Event) []agentruntime.Event {
	out := make([]agentruntime.Event, 0, len(events))
	for _, event := range events {
		out = append(out, eventFromAppServer(event))
	}
	return out
}

func eventFromAppServer(event appserver.Event) agentruntime.Event {
	return agentruntime.Event{
		Type:         event.Type,
		ThreadID:     event.ThreadID,
		TurnID:       event.TurnID,
		CallID:       event.CallID,
		ItemID:       event.ItemID,
		ItemType:     event.ItemType,
		ItemPhase:    event.ItemPhase,
		Stream:       event.Stream,
		Command:      event.Command,
		CWD:          event.CWD,
		Chunk:        event.Chunk,
		ExitCode:     event.ExitCode,
		InputTokens:  event.InputTokens,
		OutputTokens: event.OutputTokens,
		TotalTokens:  event.TotalTokens,
		Message:      event.Message,
	}
}

func activityFromAppServer(event appserver.ActivityEvent) agentruntime.ActivityEvent {
	return agentruntime.ActivityEvent{
		Type:             event.Type,
		RequestID:        event.RequestID,
		ThreadID:         event.ThreadID,
		TurnID:           event.TurnID,
		ItemID:           event.ItemID,
		ItemType:         event.ItemType,
		ItemPhase:        event.ItemPhase,
		Delta:            event.Delta,
		Stdin:            event.Stdin,
		ProcessID:        event.ProcessID,
		Command:          event.Command,
		CWD:              event.CWD,
		AggregatedOutput: event.AggregatedOutput,
		Status:           event.Status,
		Reason:           event.Reason,
		InputTokens:      event.InputTokens,
		OutputTokens:     event.OutputTokens,
		TotalTokens:      event.TotalTokens,
		ExitCode:         event.ExitCode,
		Item:             cloneMap(event.Item),
		Raw:              cloneMap(event.Raw),
		Metadata:         runtimeMetadata(event.Metadata, agentruntime.TransportAppServer),
	}
}

func interactionFromAppServer(interaction *appserver.PendingInteraction) agentruntime.PendingInteraction {
	if interaction == nil {
		return agentruntime.PendingInteraction{}
	}
	out := agentruntime.PendingInteraction{
		ID:                interaction.ID,
		RequestID:         interaction.RequestID,
		Kind:              agentruntime.PendingInteractionKind(interaction.Kind),
		Method:            interaction.Method,
		IssueID:           interaction.IssueID,
		IssueIdentifier:   interaction.IssueIdentifier,
		IssueTitle:        interaction.IssueTitle,
		Phase:             interaction.Phase,
		Attempt:           interaction.Attempt,
		SessionID:         interaction.SessionID,
		ThreadID:          interaction.ThreadID,
		TurnID:            interaction.TurnID,
		ItemID:            interaction.ItemID,
		RequestedAt:       interaction.RequestedAt,
		LastActivity:      interaction.LastActivity,
		CollaborationMode: interaction.CollaborationMode,
		ProjectID:         interaction.ProjectID,
		ProjectName:       interaction.ProjectName,
		Metadata:          runtimeMetadata(interaction.Metadata, agentruntime.TransportAppServer),
	}
	if interaction.LastActivityAt != nil {
		ts := interaction.LastActivityAt.UTC()
		out.LastActivityAt = &ts
	}
	for _, action := range interaction.Actions {
		out.Actions = append(out.Actions, agentruntime.PendingInteractionAction{
			Kind:  agentruntime.PendingInteractionActionKind(action.Kind),
			Label: action.Label,
		})
	}
	if interaction.Approval != nil {
		approval := &agentruntime.PendingApproval{
			Command:           interaction.Approval.Command,
			CWD:               interaction.Approval.CWD,
			Reason:            interaction.Approval.Reason,
			Markdown:          interaction.Approval.Markdown,
			PlanStatus:        interaction.Approval.PlanStatus,
			PlanVersionNumber: interaction.Approval.PlanVersionNumber,
			PlanRevisionNote:  interaction.Approval.PlanRevisionNote,
			Decisions:         make([]agentruntime.PendingApprovalDecision, 0, len(interaction.Approval.Decisions)),
		}
		for _, decision := range interaction.Approval.Decisions {
			approval.Decisions = append(approval.Decisions, agentruntime.PendingApprovalDecision{
				Value:           decision.Value,
				Label:           decision.Label,
				Description:     decision.Description,
				DecisionPayload: cloneMap(decision.DecisionPayload),
			})
		}
		out.Approval = approval
	}
	if interaction.UserInput != nil {
		userInput := &agentruntime.PendingUserInput{Questions: make([]agentruntime.PendingUserInputQuestion, 0, len(interaction.UserInput.Questions))}
		for _, question := range interaction.UserInput.Questions {
			converted := agentruntime.PendingUserInputQuestion{
				Header:   question.Header,
				ID:       question.ID,
				Question: question.Question,
				IsOther:  question.IsOther,
				IsSecret: question.IsSecret,
				Options:  make([]agentruntime.PendingUserInputOption, 0, len(question.Options)),
			}
			for _, option := range question.Options {
				converted.Options = append(converted.Options, agentruntime.PendingUserInputOption{
					Label:       option.Label,
					Description: option.Description,
				})
			}
			userInput.Questions = append(userInput.Questions, converted)
		}
		out.UserInput = userInput
	}
	if interaction.Elicitation != nil {
		out.Elicitation = &agentruntime.PendingElicitation{
			ServerName:      interaction.Elicitation.ServerName,
			Message:         interaction.Elicitation.Message,
			Mode:            interaction.Elicitation.Mode,
			RequestedSchema: cloneMap(interaction.Elicitation.RequestedSchema),
			ElicitationID:   interaction.Elicitation.ElicitationID,
			URL:             interaction.Elicitation.URL,
			Meta:            interaction.Elicitation.Meta,
		}
	}
	if interaction.Alert != nil {
		out.Alert = &agentruntime.PendingAlert{
			Code:     interaction.Alert.Code,
			Severity: agentruntime.PendingAlertSeverity(interaction.Alert.Severity),
			Title:    interaction.Alert.Title,
			Message:  interaction.Alert.Message,
			Detail:   interaction.Alert.Detail,
		}
	}
	return out
}

func interactionResponseToAppServer(response agentruntime.PendingInteractionResponse) appserver.PendingInteractionResponse {
	return appserver.PendingInteractionResponse{
		Decision:        response.Decision,
		DecisionPayload: cloneMap(response.DecisionPayload),
		Answers:         cloneAnswers(response.Answers),
		Note:            response.Note,
		Action:          response.Action,
		Content:         response.Content,
	}
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneAnswers(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func runtimeMetadata(existing map[string]interface{}, transport agentruntime.Transport) map[string]interface{} {
	metadata := cloneMap(existing)
	if metadata == nil {
		metadata = make(map[string]interface{}, 2)
	}
	metadata["provider"] = string(agentruntime.ProviderCodex)
	metadata["transport"] = string(transport)
	return metadata
}

func DetectVersion(command string) (appserver.CodexVersionStatus, error) {
	return appserver.DetectCodexVersion(command)
}

func startTime() time.Time {
	return time.Now().UTC()
}
