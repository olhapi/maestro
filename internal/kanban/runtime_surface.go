package kanban

import (
	"fmt"
	"strings"

	"github.com/olhapi/maestro/internal/agentruntime"
)

type RuntimeSurface struct {
	RuntimeName             string `json:"runtime_name,omitempty"`
	RuntimeProvider         string `json:"runtime_provider,omitempty"`
	RuntimeTransport        string `json:"runtime_transport,omitempty"`
	RuntimeAuthSource       string `json:"runtime_auth_source,omitempty"`
	PendingInteractionState string `json:"pending_interaction_state,omitempty"`
	StopReason              string `json:"stop_reason,omitempty"`
}

func ResolveRuntimeSurface(store *Store, issue *Issue, snapshot *ExecutionSessionSnapshot, session *agentruntime.Session, pending *agentruntime.PendingInteraction, planning *IssuePlanning) RuntimeSurface {
	surface := RuntimeSurface{}

	if snapshot != nil {
		surface.RuntimeName = strings.TrimSpace(snapshot.RuntimeName)
		surface.RuntimeProvider = strings.TrimSpace(snapshot.RuntimeProvider)
		surface.RuntimeTransport = strings.TrimSpace(snapshot.RuntimeTransport)
		surface.RuntimeAuthSource = strings.TrimSpace(snapshot.RuntimeAuthSource)
		surface.StopReason = strings.TrimSpace(snapshot.StopReason)
	}

	if issue != nil {
		if runtimeName := strings.TrimSpace(issue.RuntimeName); runtimeName != "" && surface.RuntimeName == "" {
			surface.RuntimeName = runtimeName
		} else if surface.RuntimeName == "" && store != nil && strings.TrimSpace(issue.ProjectID) != "" {
			if project, err := store.GetProject(issue.ProjectID); err == nil && project != nil {
				if runtimeName := strings.TrimSpace(project.RuntimeName); runtimeName != "" {
					surface.RuntimeName = runtimeName
				}
			}
		}
	}

	if session != nil {
		if runtimeName := metadataString(session.Metadata, "runtime_name"); runtimeName != "" && surface.RuntimeName == "" {
			surface.RuntimeName = runtimeName
		}
		if provider := metadataString(session.Metadata, "provider"); provider != "" && surface.RuntimeProvider == "" {
			surface.RuntimeProvider = provider
		}
		if transport := metadataString(session.Metadata, "transport"); transport != "" && surface.RuntimeTransport == "" {
			surface.RuntimeTransport = transport
		}
		if authSource := metadataString(session.Metadata, "auth_source"); authSource != "" && surface.RuntimeAuthSource == "" {
			surface.RuntimeAuthSource = authSource
		}
		if surface.StopReason == "" {
			if stopReason := metadataString(session.Metadata, "stop_reason"); stopReason != "" {
				surface.StopReason = stopReason
			} else if stopReason := metadataString(session.Metadata, "claude_stop_reason"); stopReason != "" {
				surface.StopReason = stopReason
			} else if stopReason := strings.TrimSpace(session.TerminalReason); stopReason != "" {
				surface.StopReason = stopReason
			}
		}
	}

	if surface.PendingInteractionState == "" {
		surface.PendingInteractionState = pendingInteractionState(pending, planning, surface.StopReason)
	}

	return surface
}

func metadataString(metadata map[string]interface{}, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func pendingInteractionState(pending *agentruntime.PendingInteraction, planning *IssuePlanning, stopReason string) string {
	if pending != nil {
		switch pending.Kind {
		case agentruntime.PendingInteractionKindApproval:
			return "approval"
		case agentruntime.PendingInteractionKindUserInput:
			return "user_input"
		case agentruntime.PendingInteractionKindElicitation:
			return "elicitation"
		case agentruntime.PendingInteractionKindAlert:
			return "alert"
		}
	}

	if planning != nil {
		switch planning.Status {
		case IssuePlanningStatusDrafting:
			return "planning"
		case IssuePlanningStatusAwaitingApproval:
			return "approval"
		case IssuePlanningStatusRevisionRequested:
			return "revision_requested"
		case IssuePlanningStatusAbandoned:
			return "abandoned"
		}
	}

	switch strings.ToLower(strings.TrimSpace(stopReason)) {
	case "plan_approval_pending", "approval_pending", "approval_required":
		return "approval"
	case "turn_input_required", "user_input_required":
		return "user_input"
	case "elicitation_required":
		return "elicitation"
	case "alert":
		return "alert"
	}

	return ""
}
