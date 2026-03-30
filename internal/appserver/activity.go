package appserver

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/appserver/protocol"
)

type ActivityEvent = agentruntime.ActivityEvent

func ActivityEventFromMessage(msg protocol.Message) (ActivityEvent, bool) {
	method := strings.TrimSpace(msg.Method)
	if method == "" {
		return ActivityEvent{}, false
	}

	switch method {
	case protocol.MethodTurnStarted, protocol.MethodTurnCompleted, protocol.MethodTurnFailed, protocol.MethodTurnCancelled:
		var payload struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if err := msg.UnmarshalParams(&payload); err != nil {
			return ActivityEvent{}, false
		}
		return ActivityEvent{
			Type:     normalizeEventType(method),
			ThreadID: strings.TrimSpace(payload.ThreadID),
			TurnID:   strings.TrimSpace(payload.Turn.ID),
			Raw:      cloneRawMap(msg.Raw),
		}, payload.ThreadID != "" || payload.Turn.ID != ""
	case protocol.MethodThreadTokenUsageUpdated:
		var payload struct {
			ThreadID   string `json:"threadId"`
			TurnID     string `json:"turnId"`
			TokenUsage struct {
				Total struct {
					InputTokens  int `json:"inputTokens"`
					OutputTokens int `json:"outputTokens"`
					TotalTokens  int `json:"totalTokens"`
				} `json:"total"`
				Last struct {
					InputTokens  int `json:"inputTokens"`
					OutputTokens int `json:"outputTokens"`
					TotalTokens  int `json:"totalTokens"`
				} `json:"last"`
			} `json:"tokenUsage"`
		}
		if err := msg.UnmarshalParams(&payload); err != nil {
			return ActivityEvent{}, false
		}
		input, output, total := tokenUsageTotals(
			payload.TokenUsage.Total.InputTokens,
			payload.TokenUsage.Total.OutputTokens,
			payload.TokenUsage.Total.TotalTokens,
		)
		if total == 0 {
			input, output, total = tokenUsageTotals(
				payload.TokenUsage.Last.InputTokens,
				payload.TokenUsage.Last.OutputTokens,
				payload.TokenUsage.Last.TotalTokens,
			)
		}
		return ActivityEvent{
			Type:         normalizeEventType(method),
			ThreadID:     strings.TrimSpace(payload.ThreadID),
			TurnID:       strings.TrimSpace(payload.TurnID),
			InputTokens:  input,
			OutputTokens: output,
			TotalTokens:  total,
			Raw:          cloneRawMap(msg.Raw),
		}, payload.ThreadID != "" || payload.TurnID != "" || total > 0
	case "item/started", "item/completed":
		params, ok := messageParamsMap(msg)
		if !ok {
			return ActivityEvent{}, false
		}
		item, _ := asMap(params["item"])
		event := activityEventFromParams(normalizeEventType(method), requestIDString(msg), params, item, msg.Raw)
		return event, event.ThreadID != "" || event.TurnID != "" || event.ItemID != ""
	case "item/agentMessage/delta":
		var payload struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if err := msg.UnmarshalParams(&payload); err != nil {
			return ActivityEvent{}, false
		}
		return ActivityEvent{
			Type:     normalizeEventType(method),
			ThreadID: strings.TrimSpace(payload.ThreadID),
			TurnID:   strings.TrimSpace(payload.TurnID),
			ItemID:   strings.TrimSpace(payload.ItemID),
			ItemType: "agentMessage",
			Delta:    payload.Delta,
			Raw:      cloneRawMap(msg.Raw),
		}, payload.ThreadID != "" || payload.TurnID != "" || payload.ItemID != ""
	case "item/plan/delta":
		var payload struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if err := msg.UnmarshalParams(&payload); err != nil {
			return ActivityEvent{}, false
		}
		return ActivityEvent{
			Type:     normalizeEventType(method),
			ThreadID: strings.TrimSpace(payload.ThreadID),
			TurnID:   strings.TrimSpace(payload.TurnID),
			ItemID:   strings.TrimSpace(payload.ItemID),
			ItemType: "plan",
			Delta:    payload.Delta,
			Raw:      cloneRawMap(msg.Raw),
		}, payload.ThreadID != "" || payload.TurnID != "" || payload.ItemID != ""
	case "item/commandExecution/outputDelta":
		params, ok := messageParamsMap(msg)
		if !ok {
			return ActivityEvent{}, false
		}
		return ActivityEvent{
				Type:     normalizeEventType(method),
				ThreadID: strings.TrimSpace(firstStr(params, "threadId", "thread_id")),
				TurnID:   strings.TrimSpace(firstStr(params, "turnId", "turn_id")),
				ItemID:   strings.TrimSpace(firstStr(params, "itemId", "item_id", "callId", "call_id")),
				ItemType: "commandExecution",
				Delta:    firstStr(params, "delta", "chunk"),
				Command:  strings.TrimSpace(firstStr(params, "command")),
				CWD:      strings.TrimSpace(firstStr(params, "cwd")),
				Raw:      cloneRawMap(msg.Raw),
			}, firstStr(params, "threadId", "thread_id") != "" ||
				firstStr(params, "turnId", "turn_id") != "" ||
				firstStr(params, "itemId", "item_id", "callId", "call_id") != ""
	case "item/commandExecution/terminalInteraction":
		var payload struct {
			ThreadID  string `json:"threadId"`
			TurnID    string `json:"turnId"`
			ItemID    string `json:"itemId"`
			ProcessID string `json:"processId"`
			Stdin     string `json:"stdin"`
		}
		if err := msg.UnmarshalParams(&payload); err != nil {
			return ActivityEvent{}, false
		}
		return ActivityEvent{
			Type:      normalizeEventType(method),
			ThreadID:  strings.TrimSpace(payload.ThreadID),
			TurnID:    strings.TrimSpace(payload.TurnID),
			ItemID:    strings.TrimSpace(payload.ItemID),
			ItemType:  "commandExecution",
			ProcessID: strings.TrimSpace(payload.ProcessID),
			Stdin:     payload.Stdin,
			Raw:       cloneRawMap(msg.Raw),
		}, payload.ThreadID != "" || payload.TurnID != "" || payload.ItemID != ""
	case protocol.MethodItemCommandExecutionApproval, protocol.MethodItemFileChangeApproval, protocol.MethodExecCommandApproval, protocol.MethodApplyPatchApproval, protocol.MethodToolRequestUserInput, protocol.MethodMCPServerElicitationRequest:
		params, ok := messageParamsMap(msg)
		if !ok {
			return ActivityEvent{}, false
		}
		event := activityEventFromParams(normalizeEventType(method), requestIDString(msg), params, nil, msg.Raw)
		if method == protocol.MethodMCPServerElicitationRequest {
			event.Reason = strings.TrimSpace(firstStr(params, "message", "url"))
		}
		return event, event.ThreadID != "" || event.TurnID != "" || event.ItemID != "" || event.RequestID != ""
	default:
		return ActivityEvent{}, false
	}
}

func activityEventFromParams(eventType, requestID string, params map[string]interface{}, item map[string]interface{}, raw map[string]interface{}) ActivityEvent {
	event := ActivityEvent{
		Type:      eventType,
		RequestID: requestID,
		ThreadID:  strings.TrimSpace(firstStr(params, "threadId", "thread_id")),
		TurnID:    strings.TrimSpace(firstStr(params, "turnId", "turn_id")),
		ItemID:    strings.TrimSpace(firstStr(params, "itemId", "item_id")),
		Item:      cloneRawMap(item),
		Raw:       cloneRawMap(raw),
	}
	if item != nil {
		if event.ItemID == "" {
			event.ItemID = strings.TrimSpace(firstStr(item, "id"))
		}
		event.ItemType = strings.TrimSpace(firstStr(item, "type"))
		event.ItemPhase = strings.TrimSpace(firstStr(item, "phase"))
		event.Command = strings.TrimSpace(firstStr(item, "command"))
		event.CWD = strings.TrimSpace(firstStr(item, "cwd"))
		event.AggregatedOutput = firstStr(item, "aggregatedOutput")
		event.Status = strings.TrimSpace(firstStr(item, "status"))
		event.Reason = strings.TrimSpace(firstStr(item, "reason"))
		event.ExitCode = firstIntPtr(item, "exitCode", "exit_code")
	} else {
		event.Command = strings.TrimSpace(firstStr(params, "command"))
		event.CWD = strings.TrimSpace(firstStr(params, "cwd"))
		event.Reason = strings.TrimSpace(firstStr(params, "reason"))
	}
	return event
}

func messageParamsMap(msg protocol.Message) (map[string]interface{}, bool) {
	if len(msg.Params) == 0 || string(msg.Params) == "null" {
		return nil, false
	}
	var params map[string]interface{}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return nil, false
	}
	return params, true
}

func requestIDString(msg protocol.Message) string {
	if !msg.HasID() {
		return ""
	}
	var raw interface{}
	if err := json.Unmarshal(msg.ID, &raw); err != nil {
		return strings.TrimSpace(string(msg.ID))
	}
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.FormatInt(int64(value), 10)
	default:
		return strings.TrimSpace(string(msg.ID))
	}
}

func cloneRawMap(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
