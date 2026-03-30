package codex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
)

func TestStartRejectsUnsupportedProviderAndTransport(t *testing.T) {
	if _, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:  "other",
		Transport: agentruntime.TransportStdio,
	}, agentruntime.Observers{}); !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
	if _, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:  agentruntime.ProviderCodex,
		Transport: "weird",
	}, agentruntime.Observers{}); !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
		t.Fatalf("expected unsupported transport error, got %v", err)
	}
}

func TestInputToAppServerAndTextInput(t *testing.T) {
	input, err := inputToAppServer([]agentruntime.InputItem{
		{Kind: agentruntime.InputItemText, Text: "fix it"},
		{Kind: agentruntime.InputItemLocalImage, Path: "/tmp/shot.png", Name: "shot"},
	})
	if err != nil {
		t.Fatalf("inputToAppServer: %v", err)
	}
	if len(input) != 2 || input[0].Type != gen.Text || input[1].Type != gen.LocalImage {
		t.Fatalf("unexpected app-server input: %#v", input)
	}

	text, err := textInput([]agentruntime.InputItem{
		{Kind: agentruntime.InputItemText, Text: "first"},
		{Kind: agentruntime.InputItemText, Text: "second"},
	})
	if err != nil {
		t.Fatalf("textInput: %v", err)
	}
	if text != "first\n\nsecond" {
		t.Fatalf("unexpected joined text input %q", text)
	}

	if _, err := inputToAppServer([]agentruntime.InputItem{{Kind: "unknown"}}); !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
		t.Fatalf("expected unsupported input kind, got %v", err)
	}
	if _, err := textInput([]agentruntime.InputItem{{Kind: agentruntime.InputItemLocalImage, Path: "/tmp/shot.png"}}); !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
		t.Fatalf("expected local image rejection, got %v", err)
	}
}

func TestRuntimeMetadataAndCloneHelpers(t *testing.T) {
	metadata := runtimeMetadata(map[string]interface{}{
		"nested": map[string]interface{}{
			"transport": "app_server",
		},
	}, agentruntime.TransportAppServer)
	if metadata["provider"] != string(agentruntime.ProviderCodex) || metadata["transport"] != string(agentruntime.TransportAppServer) {
		t.Fatalf("unexpected runtime metadata: %#v", metadata)
	}

	answers := cloneAnswers(map[string][]string{
		"q1": []string{"a", "b"},
	})
	answers["q1"][0] = "changed"
	if got := cloneAnswers(nil); got != nil {
		t.Fatalf("expected nil answers clone to stay nil, got %#v", got)
	}
	if answers["q1"][0] != "changed" {
		t.Fatalf("expected clone answers to be mutable, got %#v", answers)
	}

	clone := cloneMap(map[string]interface{}{
		"provider": "codex",
		"nested": map[string]interface{}{
			"transport": "app_server",
		},
	})
	clone["provider"] = "other"
	if clone["provider"] != "other" {
		t.Fatalf("expected clone map mutation, got %#v", clone)
	}
	if cloneMap(nil) != nil {
		t.Fatal("expected nil map clone to stay nil")
	}
}

func TestConversionHelpers(t *testing.T) {
	issue := &appserver.Session{
		IssueID:         "iss-1",
		IssueIdentifier: "ISS-1",
		SessionID:       "thread-1-turn-1",
		ThreadID:        "thread-1",
		TurnID:          "turn-1",
		LastEvent:       "turn.completed",
		LastTimestamp:   time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC),
		Metadata: map[string]interface{}{
			"provider": "codex",
		},
	}
	if got := sessionFromAppServer(issue); got.SessionID != issue.SessionID || got.Metadata["provider"] != string(agentruntime.ProviderCodex) {
		t.Fatalf("unexpected session conversion: %+v", got)
	}
	if got := sessionFromAppServer(nil); got.IssueID != "" || got.SessionID != "" || got.Metadata != nil {
		t.Fatalf("expected nil session conversion to return zero value, got %+v", got)
	}

	event := appserver.Event{
		Type:         "item.completed",
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		ItemID:       "item-1",
		ItemType:     "agentMessage",
		ItemPhase:    "final_answer",
		Message:      "done",
		InputTokens:  3,
		OutputTokens: 4,
		TotalTokens:  7,
	}
	if got := eventFromAppServer(event); got.Message != "done" || got.TotalTokens != 7 {
		t.Fatalf("unexpected event conversion: %+v", got)
	}
	if got := eventSliceFromAppServer([]appserver.Event{event}); len(got) != 1 || got[0].Type != "item.completed" {
		t.Fatalf("unexpected event slice conversion: %+v", got)
	}

	activity := appserver.ActivityEvent{
		Type:      "turn.completed",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "item-1",
		ItemType:  "agentMessage",
		ItemPhase: "final_answer",
		Raw: map[string]interface{}{
			"status": "ok",
		},
		Metadata: map[string]interface{}{
			"transport": "stdio",
		},
	}
	convertedActivity := activityFromAppServer(activity)
	if convertedActivity.Metadata["provider"] != string(agentruntime.ProviderCodex) {
		t.Fatalf("unexpected activity metadata: %+v", convertedActivity.Metadata)
	}
	if convertedActivity.Raw["status"] != "ok" {
		t.Fatalf("unexpected activity payload: %+v", convertedActivity)
	}

	interaction := appserver.PendingInteraction{
		ID:              "int-1",
		Kind:            appserver.PendingInteractionKindApproval,
		IssueID:         "iss-1",
		IssueIdentifier: "ISS-1",
		RequestedAt:     time.Date(2026, 3, 29, 12, 5, 0, 0, time.UTC),
		LastActivityAt:  ptrTime(time.Date(2026, 3, 29, 12, 10, 0, 0, time.UTC)),
		Actions: []appserver.PendingInteractionAction{{
			Kind:  appserver.PendingInteractionActionAcknowledge,
			Label: "Acknowledge",
		}},
		Approval: &appserver.PendingApproval{
			Command: "go test ./...",
			Decisions: []appserver.PendingApprovalDecision{{
				Value: "approve",
				Label: "Approve",
				DecisionPayload: map[string]interface{}{
					"mode": "auto",
				},
			}},
		},
		UserInput: &appserver.PendingUserInput{
			Questions: []appserver.PendingUserInputQuestion{{
				ID:       "q1",
				Question: "Pick one",
				Options: []appserver.PendingUserInputOption{{
					Label: "One",
				}},
			}},
		},
		Elicitation: &appserver.PendingElicitation{
			ServerName:    "server",
			Message:       "Need more detail",
			Mode:          "manual",
			ElicitationID: "elicitation-1",
		},
		Alert: &appserver.PendingAlert{
			Code:     "alert-1",
			Severity: appserver.PendingAlertSeverityWarning,
			Title:    "Warning",
			Message:  "Take care",
		},
	}
	convertedInteraction := interactionFromAppServer(&interaction)
	if convertedInteraction.ID != "int-1" || len(convertedInteraction.Actions) != 1 {
		t.Fatalf("unexpected interaction conversion: %+v", convertedInteraction)
	}
	if convertedInteraction.Approval == nil || convertedInteraction.Approval.Decisions[0].DecisionPayload["mode"] != "auto" {
		t.Fatalf("expected nested approval payload, got %+v", convertedInteraction.Approval)
	}
	if convertedInteraction.LastActivityAt == nil || !convertedInteraction.LastActivityAt.Equal(time.Date(2026, 3, 29, 12, 10, 0, 0, time.UTC)) {
		t.Fatalf("expected last activity timestamp to be preserved, got %+v", convertedInteraction.LastActivityAt)
	}
	if convertedInteraction.UserInput == nil || len(convertedInteraction.UserInput.Questions) != 1 {
		t.Fatalf("expected nested user input, got %+v", convertedInteraction.UserInput)
	}
	if convertedInteraction.Elicitation == nil || convertedInteraction.Elicitation.ServerName != "server" {
		t.Fatalf("expected elicitation payload, got %+v", convertedInteraction.Elicitation)
	}
	if convertedInteraction.Alert == nil || convertedInteraction.Alert.Severity != agentruntime.PendingAlertSeverityWarning {
		t.Fatalf("expected alert payload, got %+v", convertedInteraction.Alert)
	}

	response := interactionResponseToAppServer(agentruntime.PendingInteractionResponse{
		Decision: "accept",
		Answers: map[string][]string{
			"q1": []string{"a"},
		},
		DecisionPayload: map[string]interface{}{
			"mode": "auto",
		},
	})
	if response.Decision != "accept" || response.DecisionPayload["mode"] != "auto" || response.Answers["q1"][0] != "a" {
		t.Fatalf("unexpected response conversion: %+v", response)
	}
}

func TestInteractionFromAppServerCoversOptionalFields(t *testing.T) {
	lastActivityAt := time.Date(2026, 3, 29, 12, 15, 0, 0, time.UTC)
	interaction := appserver.PendingInteraction{
		ID:                "int-2",
		RequestID:         "req-2",
		Kind:              appserver.PendingInteractionKindUserInput,
		Method:            "interactions/create",
		IssueID:           "iss-2",
		IssueIdentifier:   "ISS-2",
		IssueTitle:        "Need input",
		Phase:             "review",
		Attempt:           4,
		SessionID:         "thread-2-turn-1",
		ThreadID:          "thread-2",
		TurnID:            "turn-1",
		ItemID:            "item-1",
		RequestedAt:       time.Date(2026, 3, 29, 12, 10, 0, 0, time.UTC),
		LastActivityAt:    &lastActivityAt,
		LastActivity:      "waiting",
		CollaborationMode: "plan",
		ProjectID:         "proj-1",
		ProjectName:       "Project One",
		Actions: []appserver.PendingInteractionAction{{
			Kind:  appserver.PendingInteractionActionAcknowledge,
			Label: "Acknowledge",
		}},
		UserInput: &appserver.PendingUserInput{
			Questions: []appserver.PendingUserInputQuestion{{
				Header:   "Header",
				ID:       "q-1",
				Question: "What now?",
				IsOther:  true,
				IsSecret: true,
				Options: []appserver.PendingUserInputOption{{
					Label:       "Option",
					Description: "desc",
				}},
			}},
		},
		Elicitation: &appserver.PendingElicitation{
			ServerName:      "server",
			Message:         "Need more detail",
			Mode:            "form",
			RequestedSchema: map[string]interface{}{"title": "schema"},
			ElicitationID:   "eli-1",
			URL:             "https://example.com",
			Meta:            map[string]interface{}{"nested": []interface{}{"x"}},
		},
		Alert: &appserver.PendingAlert{
			Code:     "warning",
			Severity: appserver.PendingAlertSeverityWarning,
			Title:    "Attention",
			Message:  "check input",
			Detail:   "extra detail",
		},
		Metadata: map[string]interface{}{
			"nested": map[string]interface{}{"transport": "app_server"},
		},
	}

	converted := interactionFromAppServer(&interaction)
	if converted.ID != "int-2" || converted.RequestID != "req-2" || converted.Kind != agentruntime.PendingInteractionKindUserInput {
		t.Fatalf("unexpected converted interaction: %+v", converted)
	}
	if converted.LastActivityAt == nil || !converted.LastActivityAt.Equal(lastActivityAt) {
		t.Fatalf("expected last activity timestamp to be copied, got %+v", converted.LastActivityAt)
	}
	if converted.UserInput == nil || len(converted.UserInput.Questions) != 1 || !converted.UserInput.Questions[0].IsSecret {
		t.Fatalf("expected user input to be copied, got %+v", converted.UserInput)
	}
	if converted.Elicitation == nil || converted.Elicitation.RequestedSchema["title"] != "schema" {
		t.Fatalf("expected elicitation payload to be copied, got %+v", converted.Elicitation)
	}
	if converted.Alert == nil || converted.Alert.Severity != agentruntime.PendingAlertSeverityWarning {
		t.Fatalf("expected alert payload to be copied, got %+v", converted.Alert)
	}
	if converted.Metadata["provider"] != string(agentruntime.ProviderCodex) || converted.Metadata["transport"] != string(agentruntime.TransportAppServer) {
		t.Fatalf("expected metadata to be rewritten, got %+v", converted.Metadata)
	}
	if got := interactionFromAppServer(nil); got.ID != "" || got.RequestID != "" || got.Approval != nil || got.UserInput != nil || got.Alert != nil {
		t.Fatalf("expected nil interaction to return zero value, got %+v", got)
	}
}

func TestInteractionFromAppServerBranches(t *testing.T) {
	if got := interactionFromAppServer(nil); got.ID != "" || got.Approval != nil || got.UserInput != nil || got.Alert != nil {
		t.Fatalf("expected nil interaction conversion to return zero value, got %+v", got)
	}

	lastActivityAt := time.Date(2026, 3, 29, 12, 30, 0, 0, time.UTC)
	interaction := &appserver.PendingInteraction{
		ID:                "int-2",
		RequestID:         "req-2",
		Kind:              appserver.PendingInteractionKindUserInput,
		Method:            "turn/start",
		IssueID:           "issue-2",
		IssueIdentifier:   "ISS-2",
		IssueTitle:        "Prompt me",
		Phase:             "planning",
		Attempt:           4,
		SessionID:         "session-2",
		ThreadID:          "thread-2",
		TurnID:            "turn-2",
		ItemID:            "item-2",
		RequestedAt:       time.Date(2026, 3, 29, 12, 15, 0, 0, time.UTC),
		LastActivityAt:    &lastActivityAt,
		LastActivity:      "Waiting for input",
		CollaborationMode: "default",
		ProjectID:         "proj-2",
		ProjectName:       "Project 2",
		Actions: []appserver.PendingInteractionAction{{
			Kind:  appserver.PendingInteractionActionAcknowledge,
			Label: "Acknowledge",
		}},
		Approval: &appserver.PendingApproval{
			Command:           "git status",
			CWD:               "/repo",
			Reason:            "Need approval",
			Markdown:          "Approve the plan",
			PlanStatus:        "pending",
			PlanVersionNumber: 3,
			PlanRevisionNote:  "revise plan",
			Decisions: []appserver.PendingApprovalDecision{{
				Value:           "approve",
				Label:           "Approve",
				Description:     "Approve the action",
				DecisionPayload: map[string]interface{}{"mode": "auto"},
			}},
		},
		UserInput: &appserver.PendingUserInput{
			Questions: []appserver.PendingUserInputQuestion{{
				Header:   "Question",
				ID:       "q1",
				Question: "What now?",
				IsOther:  true,
				IsSecret: false,
				Options: []appserver.PendingUserInputOption{{
					Label:       "Continue",
					Description: "Proceed with the turn",
				}},
			}},
		},
		Elicitation: &appserver.PendingElicitation{
			ServerName:      "server",
			Message:         "Need more context",
			Mode:            "plan",
			RequestedSchema: map[string]interface{}{"type": "object"},
			ElicitationID:   "elicitation-1",
			URL:             "https://example.com",
			Meta:            "meta",
		},
		Alert: &appserver.PendingAlert{
			Code:     "warning",
			Severity: appserver.PendingAlertSeverityWarning,
			Title:    "Heads up",
			Message:  "Take care",
			Detail:   "More detail",
		},
	}

	got := interactionFromAppServer(interaction)
	if got.ID != interaction.ID || got.RequestID != interaction.RequestID || got.LastActivityAt == nil || !got.LastActivityAt.Equal(lastActivityAt) {
		t.Fatalf("unexpected interaction conversion: %+v", got)
	}
	if got.Approval == nil || got.Approval.PlanVersionNumber != 3 || got.Approval.Decisions[0].DecisionPayload["mode"] != "auto" {
		t.Fatalf("expected approval payload to be preserved, got %+v", got.Approval)
	}
	if got.UserInput == nil || len(got.UserInput.Questions) != 1 || got.UserInput.Questions[0].Options[0].Label != "Continue" {
		t.Fatalf("expected user input payload to be preserved, got %+v", got.UserInput)
	}
	if got.Elicitation == nil || got.Elicitation.ElicitationID != "elicitation-1" {
		t.Fatalf("expected elicitation payload to be preserved, got %+v", got.Elicitation)
	}
	if got.Alert == nil || got.Alert.Severity != agentruntime.PendingAlertSeverityWarning {
		t.Fatalf("expected alert payload to be preserved, got %+v", got.Alert)
	}
}

func ptrTime(ti time.Time) *time.Time {
	return &ti
}
