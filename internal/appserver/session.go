package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
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
	SessionID        string    `json:"session_id"`
	ThreadID         string    `json:"thread_id"`
	TurnID           string    `json:"turn_id"`
	LastEvent        string    `json:"last_event"`
	LastTimestamp    time.Time `json:"last_timestamp"`
	LastMessage      string    `json:"last_message,omitempty"`
	InputTokens      int       `json:"input_tokens"`
	OutputTokens     int       `json:"output_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	EventsProcessed  int       `json:"events_processed"`
	TurnsStarted     int       `json:"turns_started"`
	TurnsCompleted   int       `json:"turns_completed"`
	Terminal         bool      `json:"terminal"`
	TerminalReason   string    `json:"terminal_reason,omitempty"`
	History          []Event   `json:"history,omitempty"`
	MaxHistory       int       `json:"-"`
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

	for _, c := range candidates {
		e := eventFromMap(c, root)
		if e.Type != "" {
			return e, true
		}
	}
	return Event{}, false
}

func eventFromMap(m map[string]interface{}, root map[string]interface{}) Event {
	e := Event{}
	e.Type = str(m, "type")
	if e.Type == "" {
		e.Type = str(m, "event_type")
	}
	if e.Type == "" {
		e.Type = str(root, "type")
	}
	e.ThreadID = firstStr(m, "thread_id", "threadId")
	e.TurnID = firstStr(m, "turn_id", "turnId")
	e.Message = firstStr(m, "message", "content")

	if usage, ok := asMap(m["usage"]); ok {
		e.InputTokens = firstInt(usage, "input_tokens", "prompt_tokens")
		e.OutputTokens = firstInt(usage, "output_tokens", "completion_tokens")
		e.TotalTokens = firstInt(usage, "total_tokens")
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
	return e
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
