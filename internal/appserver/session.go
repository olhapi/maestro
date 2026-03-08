package appserver

import (
	"encoding/json"
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
	var e Event
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return Event{}, false
	}
	if e.Type == "" {
		return Event{}, false
	}
	return e, true
}
