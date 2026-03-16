package appserver

import (
	"context"
	"errors"
	"time"
)

var (
	ErrPendingInteractionNotFound = errors.New("pending_interaction_not_found")
	ErrPendingInteractionConflict = errors.New("pending_interaction_conflict")
	ErrInvalidInteractionResponse = errors.New("invalid_interaction_response")
)

type PendingInteractionKind string

const (
	PendingInteractionKindApproval  PendingInteractionKind = "approval"
	PendingInteractionKindUserInput PendingInteractionKind = "user_input"
)

type PendingInteraction struct {
	ID                string                 `json:"id"`
	RequestID         string                 `json:"request_id,omitempty"`
	Kind              PendingInteractionKind `json:"kind"`
	Method            string                 `json:"method,omitempty"`
	IssueID           string                 `json:"issue_id,omitempty"`
	IssueIdentifier   string                 `json:"issue_identifier,omitempty"`
	IssueTitle        string                 `json:"issue_title,omitempty"`
	Phase             string                 `json:"phase,omitempty"`
	Attempt           int                    `json:"attempt,omitempty"`
	SessionID         string                 `json:"session_id,omitempty"`
	ThreadID          string                 `json:"thread_id,omitempty"`
	TurnID            string                 `json:"turn_id,omitempty"`
	ItemID            string                 `json:"item_id,omitempty"`
	RequestedAt       time.Time              `json:"requested_at"`
	LastActivityAt    *time.Time             `json:"last_activity_at,omitempty"`
	LastActivity      string                 `json:"last_activity,omitempty"`
	CollaborationMode string                 `json:"collaboration_mode,omitempty"`
	Approval          *PendingApproval       `json:"approval,omitempty"`
	UserInput         *PendingUserInput      `json:"user_input,omitempty"`
}

type PendingApproval struct {
	Command   string                    `json:"command,omitempty"`
	CWD       string                    `json:"cwd,omitempty"`
	Reason    string                    `json:"reason,omitempty"`
	Decisions []PendingApprovalDecision `json:"decisions"`
}

type PendingApprovalDecision struct {
	Value           string                 `json:"value"`
	Label           string                 `json:"label"`
	Description     string                 `json:"description,omitempty"`
	DecisionPayload map[string]interface{} `json:"decision_payload,omitempty"`
}

type PendingUserInput struct {
	Questions []PendingUserInputQuestion `json:"questions"`
}

type PendingUserInputQuestion struct {
	Header   string                   `json:"header,omitempty"`
	ID       string                   `json:"id"`
	Question string                   `json:"question,omitempty"`
	Options  []PendingUserInputOption `json:"options,omitempty"`
	IsOther  bool                     `json:"is_other,omitempty"`
	IsSecret bool                     `json:"is_secret,omitempty"`
}

type PendingUserInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type PendingInteractionResponse struct {
	Decision        string                 `json:"decision,omitempty"`
	DecisionPayload map[string]interface{} `json:"decision_payload,omitempty"`
	Answers         map[string][]string    `json:"answers,omitempty"`
}

type PendingInteractionSnapshot struct {
	Count   int                 `json:"count"`
	Current *PendingInteraction `json:"current,omitempty"`
}

type InteractionResponder func(ctx context.Context, interactionID string, response PendingInteractionResponse) error

func (interaction PendingInteraction) Clone() PendingInteraction {
	cloned := interaction
	if interaction.Approval != nil {
		approval := *interaction.Approval
		approval.Decisions = append([]PendingApprovalDecision(nil), approval.Decisions...)
		for i := range approval.Decisions {
			approval.Decisions[i].DecisionPayload = cloneJSONMap(approval.Decisions[i].DecisionPayload)
		}
		cloned.Approval = &approval
	}
	if interaction.UserInput != nil {
		userInput := *interaction.UserInput
		userInput.Questions = append([]PendingUserInputQuestion(nil), userInput.Questions...)
		for i := range userInput.Questions {
			userInput.Questions[i].Options = append([]PendingUserInputOption(nil), userInput.Questions[i].Options...)
		}
		cloned.UserInput = &userInput
	}
	if interaction.LastActivityAt != nil {
		ts := interaction.LastActivityAt.UTC()
		cloned.LastActivityAt = &ts
	}
	return cloned
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
