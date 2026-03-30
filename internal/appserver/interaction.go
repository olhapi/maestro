package appserver

import "github.com/olhapi/maestro/internal/agentruntime"

var (
	ErrPendingInteractionNotFound = agentruntime.ErrPendingInteractionNotFound
	ErrPendingInteractionConflict = agentruntime.ErrPendingInteractionConflict
	ErrInvalidInteractionResponse = agentruntime.ErrInvalidInteractionResponse
)

type PendingInteractionKind = agentruntime.PendingInteractionKind

const (
	PendingInteractionKindApproval    = agentruntime.PendingInteractionKindApproval
	PendingInteractionKindUserInput   = agentruntime.PendingInteractionKindUserInput
	PendingInteractionKindElicitation = agentruntime.PendingInteractionKindElicitation
	PendingInteractionKindAlert       = agentruntime.PendingInteractionKindAlert
)

type PendingAlertSeverity = agentruntime.PendingAlertSeverity

const (
	PendingAlertSeverityInfo    = agentruntime.PendingAlertSeverityInfo
	PendingAlertSeverityWarning = agentruntime.PendingAlertSeverityWarning
	PendingAlertSeverityError   = agentruntime.PendingAlertSeverityError
)

type PendingInteractionActionKind = agentruntime.PendingInteractionActionKind

const (
	PendingInteractionActionAcknowledge = agentruntime.PendingInteractionActionAcknowledge
)

type PendingInteraction = agentruntime.PendingInteraction
type PendingInteractionAction = agentruntime.PendingInteractionAction
type PendingApproval = agentruntime.PendingApproval
type PendingApprovalDecision = agentruntime.PendingApprovalDecision
type PendingUserInput = agentruntime.PendingUserInput
type PendingUserInputQuestion = agentruntime.PendingUserInputQuestion
type PendingUserInputOption = agentruntime.PendingUserInputOption
type PendingElicitation = agentruntime.PendingElicitation
type PendingAlert = agentruntime.PendingAlert
type PendingInteractionResponse = agentruntime.PendingInteractionResponse
type PendingInteractionSnapshot = agentruntime.PendingInteractionSnapshot
type InteractionResponder = agentruntime.InteractionResponder

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
