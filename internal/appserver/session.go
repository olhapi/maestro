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
	SessionID       string    `json:"session_id"`
	ThreadID        string    `json:"thread_id"`
	TurnID          string    `json:"turn_id"`
	LastEvent       string    `json:"last_event"`
	LastTimestamp   time.Time `json:"last_timestamp"`
	LastMessage     string    `json:"last_message,omitempty"`
	InputTokens     int       `json:"input_tokens"`
	OutputTokens    int       `json:"output_tokens"`
	TotalTokens     int       `json:"total_tokens"`
	EventsProcessed int       `json:"events_processed"`
}

func (s *Session) ApplyEvent(e Event) {
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
