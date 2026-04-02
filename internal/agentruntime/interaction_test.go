package agentruntime

import (
	"testing"
	"time"
)

func TestPendingInteractionCloneDeepCopiesNestedFields(t *testing.T) {
	lastActivity := time.Now().UTC().Truncate(time.Second)
	interaction := PendingInteraction{
		ID:             "int-1",
		Kind:           PendingInteractionKindApproval,
		RequestedAt:    lastActivity,
		LastActivityAt: &lastActivity,
		Actions:        []PendingInteractionAction{{Kind: PendingInteractionActionAcknowledge, Label: "Acknowledge"}},
		Metadata:       map[string]interface{}{"provider": "codex"},
		Approval: &PendingApproval{
			Command: "go test ./...",
			Decisions: []PendingApprovalDecision{{
				Value: "approve",
				Label: "Approve",
				DecisionPayload: map[string]interface{}{
					"mode": "auto",
				},
			}},
		},
		UserInput: &PendingUserInput{
			Questions: []PendingUserInputQuestion{{
				ID:       "q1",
				Question: "Continue?",
				Options:  []PendingUserInputOption{{Label: "Yes"}},
			}},
		},
		Elicitation: &PendingElicitation{
			ServerName: "mcp",
			Message:    "Need input",
			RequestedSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"profile": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
			},
			Meta: map[string]interface{}{
				"channel": "browser",
			},
		},
		Alert: &PendingAlert{
			Code:     "warn",
			Severity: PendingAlertSeverityWarning,
			Title:    "Warning",
		},
	}

	cloned := interaction.Clone()
	cloned.Actions[0].Label = "Changed"
	cloned.Metadata["provider"] = "other"
	cloned.Approval.Decisions[0].DecisionPayload["mode"] = "manual"
	cloned.UserInput.Questions[0].Options[0].Label = "No"
	cloned.Elicitation.RequestedSchema["type"] = "array"
	cloned.Elicitation.Meta.(map[string]interface{})["channel"] = "cli"
	cloned.Alert.Title = "Changed"

	if !interaction.HasAction(PendingInteractionActionAcknowledge) {
		t.Fatal("expected interaction to report configured action")
	}
	if interaction.Actions[0].Label != "Acknowledge" {
		t.Fatalf("expected actions slice to be cloned, got %+v", interaction.Actions)
	}
	if interaction.Metadata["provider"] != "codex" {
		t.Fatalf("expected metadata clone to be independent, got %+v", interaction.Metadata)
	}
	if interaction.Approval.Decisions[0].DecisionPayload["mode"] != "auto" {
		t.Fatalf("expected approval payload clone to be independent, got %+v", interaction.Approval)
	}
	if interaction.UserInput.Questions[0].Options[0].Label != "Yes" {
		t.Fatalf("expected user input clone to be independent, got %+v", interaction.UserInput)
	}
	if interaction.Elicitation.RequestedSchema["type"] != "object" {
		t.Fatalf("expected elicitation schema clone to be independent, got %+v", interaction.Elicitation)
	}
	properties, ok := interaction.Elicitation.RequestedSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected requested schema properties to stay an object, got %#v", interaction.Elicitation.RequestedSchema["properties"])
	}
	profile, ok := properties["profile"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested requested schema property to stay an object, got %#v", properties["profile"])
	}
	nestedProperties, ok := profile["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested empty properties object to survive clone, got %#v", profile["properties"])
	}
	if len(nestedProperties) != 0 {
		t.Fatalf("expected nested properties to stay empty, got %#v", nestedProperties)
	}
	if interaction.Elicitation.Meta.(map[string]interface{})["channel"] != "browser" {
		t.Fatalf("expected elicitation meta clone to be independent, got %+v", interaction.Elicitation.Meta)
	}
	if interaction.Alert.Title != "Warning" {
		t.Fatalf("expected alert clone to be independent, got %+v", interaction.Alert)
	}
}
