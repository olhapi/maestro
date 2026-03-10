package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/appserver/protocol"
)

// Event is a minimal app-server event envelope.
type Event struct {
	Type         string `json:"type"`
	ThreadID     string `json:"thread_id"`
	TurnID       string `json:"turn_id"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
	Message      string `json:"message"`
}

// Session tracks live app-server session metadata.
type Session struct {
	IssueID         string    `json:"issue_id,omitempty"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	SessionID       string    `json:"session_id"`
	ThreadID        string    `json:"thread_id"`
	TurnID          string    `json:"turn_id"`
	AppServerPID    int       `json:"codex_app_server_pid,omitempty"`
	LastEvent       string    `json:"last_event"`
	LastTimestamp   time.Time `json:"last_timestamp"`
	LastMessage     string    `json:"last_message,omitempty"`
	InputTokens     int       `json:"input_tokens"`
	OutputTokens    int       `json:"output_tokens"`
	TotalTokens     int       `json:"total_tokens"`
	EventsProcessed int       `json:"events_processed"`
	TurnsStarted    int       `json:"turns_started"`
	TurnsCompleted  int       `json:"turns_completed"`
	Terminal        bool      `json:"terminal"`
	TerminalReason  string    `json:"terminal_reason,omitempty"`
	History         []Event   `json:"history,omitempty"`
	MaxHistory      int       `json:"-"`
}

func (s *Session) ApplyEvent(e Event) {
	if s.MaxHistory <= 0 {
		s.MaxHistory = 50
	}
	s.LastEvent = e.Type
	s.LastTimestamp = time.Now().UTC()
	if e.Message != "" {
		s.LastMessage = e.Message
	}
	if e.ThreadID != "" {
		s.ThreadID = e.ThreadID
	}
	if e.TurnID != "" {
		s.TurnID = e.TurnID
	}
	if s.ThreadID != "" && s.TurnID != "" {
		s.SessionID = s.ThreadID + "-" + s.TurnID
	}
	if e.InputTokens > 0 {
		s.InputTokens = e.InputTokens
	}
	if e.OutputTokens > 0 {
		s.OutputTokens = e.OutputTokens
	}
	if e.TotalTokens > 0 {
		s.TotalTokens = e.TotalTokens
	}
	s.EventsProcessed++

	switch e.Type {
	case "turn.started":
		s.TurnsStarted++
	case "turn.completed":
		s.TurnsCompleted++
		s.Terminal = true
		s.TerminalReason = e.Type
	case "session.completed", "run.completed", "run.failed", "error":
		s.Terminal = true
		s.TerminalReason = e.Type
	}

	s.History = append(s.History, e)
	if len(s.History) > s.MaxHistory {
		s.History = s.History[len(s.History)-s.MaxHistory:]
	}
}

// ParseEventLine attempts to parse one JSON line as an app-server event.
func ParseEventLine(line string) (Event, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return Event{}, false
	}

	var root map[string]interface{}
	if err := json.Unmarshal([]byte(line), &root); err != nil {
		return Event{}, false
	}

	candidates := []map[string]interface{}{root}
	for _, k := range []string{"event", "data", "payload"} {
		if m, ok := asMap(root[k]); ok {
			candidates = append(candidates, m)
			if inner, ok := asMap(m["event"]); ok {
				candidates = append(candidates, inner)
			}
			if inner, ok := asMap(m["data"]); ok {
				candidates = append(candidates, inner)
			}
		}
	}
	if m, ok := asMap(root["params"]); ok {
		candidates = append(candidates, m)
		if inner, ok := asMap(m["event"]); ok {
			candidates = append(candidates, inner)
		}
		if inner, ok := asMap(m["data"]); ok {
			candidates = append(candidates, inner)
		}
	}

	for _, c := range candidates {
		e := eventFromMap(c, root)
		if e.Type != "" {
			return e, true
		}
	}
	return Event{}, false
}

func EventFromMessage(msg protocol.Message) (Event, bool) {
	switch msg.Method {
	case protocol.MethodThreadStarted:
		var payload struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := msg.UnmarshalParams(&payload); err != nil {
			return Event{}, false
		}
		return Event{
			Type:     normalizeEventType(msg.Method),
			ThreadID: payload.Thread.ID,
		}, payload.Thread.ID != ""
	case protocol.MethodTurnStarted, protocol.MethodTurnCompleted:
		var payload struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if err := msg.UnmarshalParams(&payload); err != nil {
			return Event{}, false
		}
		return Event{
			Type:     normalizeEventType(msg.Method),
			ThreadID: payload.ThreadID,
			TurnID:   payload.Turn.ID,
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
			return Event{}, false
		}
		input, output, total := tokenUsageTotals(payload.TokenUsage.Total.InputTokens, payload.TokenUsage.Total.OutputTokens, payload.TokenUsage.Total.TotalTokens)
		if total == 0 {
			input, output, total = tokenUsageTotals(payload.TokenUsage.Last.InputTokens, payload.TokenUsage.Last.OutputTokens, payload.TokenUsage.Last.TotalTokens)
		}
		return Event{
			Type:         normalizeEventType(msg.Method),
			ThreadID:     payload.ThreadID,
			TurnID:       payload.TurnID,
			InputTokens:  input,
			OutputTokens: output,
			TotalTokens:  total,
		}, payload.ThreadID != "" || payload.TurnID != "" || total > 0
	default:
		return Event{}, false
	}
}

func MergeEvents(primary, fallback Event) Event {
	out := primary
	if out.Type == "" {
		out.Type = fallback.Type
	}
	if out.ThreadID == "" {
		out.ThreadID = fallback.ThreadID
	}
	if out.TurnID == "" {
		out.TurnID = fallback.TurnID
	}
	if out.InputTokens == 0 {
		out.InputTokens = fallback.InputTokens
	}
	if out.OutputTokens == 0 {
		out.OutputTokens = fallback.OutputTokens
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = fallback.TotalTokens
	}
	if out.Message == "" {
		out.Message = fallback.Message
	}
	return out
}

func eventFromMap(m map[string]interface{}, root map[string]interface{}) Event {
	e := Event{}
	e.Type = str(m, "type")
	if e.Type == "" {
		e.Type = str(m, "event_type")
	}
	if e.Type == "" {
		e.Type = normalizeEventType(firstStr(m, "method"))
	}
	if e.Type == "" {
		e.Type = normalizeEventType(firstStr(root, "type", "method"))
	}
	e.ThreadID = firstStr(m, "thread_id", "threadId")
	e.TurnID = firstStr(m, "turn_id", "turnId")
	e.Message = firstStr(m, "message", "content", "reason", "command")
	if e.ThreadID == "" {
		e.ThreadID = firstStr(root, "thread_id", "threadId")
	}
	if e.TurnID == "" {
		e.TurnID = firstStr(root, "turn_id", "turnId")
	}
	if params, ok := asMap(root["params"]); ok {
		if e.ThreadID == "" {
			e.ThreadID = firstStr(params, "thread_id", "threadId")
		}
		if e.TurnID == "" {
			e.TurnID = firstStr(params, "turn_id", "turnId")
		}
		if e.Message == "" {
			e.Message = extractMessage(params)
		}
		if e.TotalTokens == 0 {
			e.InputTokens, e.OutputTokens, e.TotalTokens = tokenUsageFromMap(params)
		}
	}
	if e.Message == "" {
		e.Message = extractMessage(m)
	}
	if e.Message == "" {
		e.Message = extractMessage(root)
	}

	if usage, ok := asMap(m["usage"]); ok {
		e.InputTokens = firstInt(usage, "input_tokens", "prompt_tokens")
		e.OutputTokens = firstInt(usage, "output_tokens", "completion_tokens")
		e.TotalTokens = firstInt(usage, "total_tokens")
	}
	if e.TotalTokens == 0 {
		e.InputTokens, e.OutputTokens, e.TotalTokens = tokenUsageFromMap(m)
	}
	if e.InputTokens == 0 {
		e.InputTokens = firstInt(m, "input_tokens")
	}
	if e.OutputTokens == 0 {
		e.OutputTokens = firstInt(m, "output_tokens")
	}
	if e.TotalTokens == 0 {
		e.TotalTokens = firstInt(m, "total_tokens")
	}
	if e.TotalTokens == 0 && e.InputTokens > 0 && e.OutputTokens > 0 {
		e.TotalTokens = e.InputTokens + e.OutputTokens
	}
	return e
}

func tokenUsageFromMap(m map[string]interface{}) (int, int, int) {
	if m == nil {
		return 0, 0, 0
	}
	raw, ok := m["tokenUsage"]
	if !ok {
		raw, ok = m["token_usage"]
	}
	if !ok {
		return 0, 0, 0
	}
	usage, ok := asMap(raw)
	if !ok {
		return 0, 0, 0
	}
	for _, key := range []string{"total", "last"} {
		part, ok := asMap(usage[key])
		if !ok {
			continue
		}
		input, output, total := tokenUsageTotals(
			firstInt(part, "inputTokens", "input_tokens", "prompt_tokens"),
			firstInt(part, "outputTokens", "output_tokens", "completion_tokens"),
			firstInt(part, "totalTokens", "total_tokens"),
		)
		if total > 0 {
			return input, output, total
		}
	}
	return 0, 0, 0
}

func tokenUsageTotals(input, output, total int) (int, int, int) {
	if total == 0 && input > 0 && output > 0 {
		total = input + output
	}
	return input, output, total
}

func normalizeEventType(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ReplaceAll(s, "/", ".")
}

func asMap(v interface{}) (map[string]interface{}, bool) {
	m, ok := v.(map[string]interface{})
	return m, ok
}

func str(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func firstStr(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if s := str(m, k); s != "" {
			return s
		}
	}
	return ""
}

func firstInt(m map[string]interface{}, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch x := v.(type) {
			case float64:
				return int(x)
			case int:
				return x
			case int64:
				return int(x)
			case string:
				var out int
				_, _ = fmt.Sscanf(x, "%d", &out)
				if out > 0 {
					return out
				}
			}
		}
	}
	return 0
}

func extractMessage(m map[string]interface{}) string {
	if m == nil {
		return ""
	}
	if s := firstStr(m, "message", "content", "reason", "command", "delta", "text"); s != "" {
		return s
	}
	for _, key := range []string{"message", "content", "delta", "text"} {
		if s := extractMessageValue(m[key]); s != "" {
			return s
		}
	}
	return ""
}

func extractMessageValue(v interface{}) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case map[string]interface{}:
		if s := extractMessage(x); s != "" {
			return s
		}
	case []interface{}:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			if s := extractMessageValue(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	}
	return ""
}
