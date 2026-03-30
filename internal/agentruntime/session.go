package agentruntime

import (
	"encoding/json"
	"strings"
	"time"
)

const DefaultSessionHistoryLimit = 64

type Event struct {
	Type         string `json:"type"`
	ThreadID     string `json:"thread_id"`
	TurnID       string `json:"turn_id"`
	CallID       string `json:"call_id,omitempty"`
	ItemID       string `json:"item_id,omitempty"`
	ItemType     string `json:"item_type,omitempty"`
	ItemPhase    string `json:"item_phase,omitempty"`
	Stream       string `json:"stream,omitempty"`
	Command      string `json:"command,omitempty"`
	CWD          string `json:"cwd,omitempty"`
	Chunk        string `json:"chunk,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
	Message      string `json:"message"`
}

type Session struct {
	IssueID         string                 `json:"issue_id,omitempty"`
	IssueIdentifier string                 `json:"issue_identifier,omitempty"`
	SessionID       string                 `json:"session_id"`
	ThreadID        string                 `json:"thread_id"`
	TurnID          string                 `json:"turn_id"`
	ProcessID       int                    `json:"codex_app_server_pid,omitempty"`
	LastEvent       string                 `json:"last_event"`
	LastTimestamp   time.Time              `json:"last_timestamp"`
	LastMessage     string                 `json:"last_message,omitempty"`
	InputTokens     int                    `json:"input_tokens"`
	OutputTokens    int                    `json:"output_tokens"`
	TotalTokens     int                    `json:"total_tokens"`
	EventsProcessed int                    `json:"events_processed"`
	TurnsStarted    int                    `json:"turns_started"`
	TurnsCompleted  int                    `json:"turns_completed"`
	Terminal        bool                   `json:"terminal"`
	TerminalReason  string                 `json:"terminal_reason,omitempty"`
	History         []Event                `json:"history,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	MaxHistory      int                    `json:"-"`
	startedTurnID   string
}

func (s *Session) Clone() Session {
	if s == nil {
		return Session{}
	}
	cp := *s
	cp.History = append([]Event(nil), s.History...)
	cp.Metadata = cloneJSONMap(s.Metadata)
	return cp
}

func (s *Session) Summary() Session {
	if s == nil {
		return Session{}
	}
	cp := *s
	cp.History = nil
	cp.Metadata = cloneJSONMap(s.Metadata)
	return cp
}

func (s *Session) ResetThreadState() {
	if s == nil {
		return
	}
	maxHistory := s.MaxHistory
	issueID := s.IssueID
	issueIdentifier := s.IssueIdentifier
	processID := s.ProcessID
	metadata := cloneJSONMap(s.Metadata)
	*s = Session{
		IssueID:         issueID,
		IssueIdentifier: issueIdentifier,
		ProcessID:       processID,
		Metadata:        metadata,
		MaxHistory:      maxHistory,
	}
}

func (s *Session) ResetTurnState() {
	if s == nil {
		return
	}
	s.TurnID = ""
	s.SessionID = ""
	s.Terminal = false
	s.TerminalReason = ""
	s.startedTurnID = ""
}

func SessionFromAny(value interface{}) (Session, bool) {
	switch session := value.(type) {
	case Session:
		return session, true
	case *Session:
		if session == nil {
			return Session{}, false
		}
		return *session, true
	case map[string]interface{}:
		return sessionFromMap(session)
	default:
		return Session{}, false
	}
}

func SessionsFromMap(raw map[string]interface{}) map[string]Session {
	out := make(map[string]Session, len(raw))
	for key, value := range raw {
		if session, ok := SessionFromAny(value); ok {
			out[key] = session
		}
	}
	return out
}

func (s *Session) ApplyEvent(e Event) {
	if s.MaxHistory <= 0 {
		s.MaxHistory = DefaultSessionHistoryLimit
	}
	s.LastEvent = e.Type
	s.LastTimestamp = time.Now().UTC()
	if message, ok := sessionSummaryMessage(e); ok {
		s.LastMessage = message
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
		s.startedTurnID = e.TurnID
		s.TurnsStarted++
	case "turn.completed":
		s.TurnsCompleted++
		s.Terminal = true
		s.TerminalReason = e.Type
	case "turn.failed", "turn.cancelled", "session.completed", "run.completed", "run.failed", "error":
		s.Terminal = true
		s.TerminalReason = e.Type
	}

	s.History = append(s.History, e)
	if len(s.History) > s.MaxHistory {
		s.History = s.History[len(s.History)-s.MaxHistory:]
	}
}

func (s *Session) HasStartedTurn(turnID string) bool {
	return s != nil && strings.TrimSpace(turnID) != "" && s.startedTurnID == turnID
}

func sessionFromMap(raw map[string]interface{}) (Session, bool) {
	var session Session
	parsed := false

	if value, ok := stringValue(raw["issue_id"]); ok {
		session.IssueID = value
		parsed = true
	}
	if value, ok := stringValue(raw["issue_identifier"]); ok {
		session.IssueIdentifier = value
		parsed = true
	}
	if value, ok := stringValue(raw["session_id"]); ok {
		session.SessionID = value
		parsed = true
	}
	if value, ok := stringValue(raw["thread_id"]); ok {
		session.ThreadID = value
		parsed = true
	}
	if value, ok := stringValue(raw["turn_id"]); ok {
		session.TurnID = value
		parsed = true
	}
	if value, ok := intValue(raw["codex_app_server_pid"]); ok {
		session.ProcessID = value
		parsed = true
	}
	if value, ok := stringValue(raw["last_event"]); ok {
		session.LastEvent = value
		parsed = true
	}
	if value, ok := timeValue(raw["last_timestamp"]); ok {
		session.LastTimestamp = value
		parsed = true
	}
	if value, ok := stringValue(raw["last_message"]); ok {
		session.LastMessage = value
		parsed = true
	}
	if value, ok := intValue(raw["input_tokens"]); ok {
		session.InputTokens = value
		parsed = true
	}
	if value, ok := intValue(raw["output_tokens"]); ok {
		session.OutputTokens = value
		parsed = true
	}
	if value, ok := intValue(raw["total_tokens"]); ok {
		session.TotalTokens = value
		parsed = true
	}
	if value, ok := intValue(raw["events_processed"]); ok {
		session.EventsProcessed = value
		parsed = true
	}
	if value, ok := intValue(raw["turns_started"]); ok {
		session.TurnsStarted = value
		parsed = true
	}
	if value, ok := intValue(raw["turns_completed"]); ok {
		session.TurnsCompleted = value
		parsed = true
	}
	if value, ok := boolValue(raw["terminal"]); ok {
		session.Terminal = value
		parsed = true
	}
	if value, ok := stringValue(raw["terminal_reason"]); ok {
		session.TerminalReason = value
		parsed = true
	}
	if history, ok := eventsValue(raw["history"]); ok {
		session.History = history
		parsed = true
	}
	if metadata, ok := raw["metadata"].(map[string]interface{}); ok {
		session.Metadata = cloneJSONMap(metadata)
		parsed = true
	}

	return session, parsed
}

func eventsValue(value interface{}) ([]Event, bool) {
	switch typed := value.(type) {
	case []Event:
		return append([]Event(nil), typed...), true
	case []interface{}:
		out := make([]Event, 0, len(typed))
		parsed := false
		for _, item := range typed {
			event, ok := eventFromAny(item)
			if !ok {
				continue
			}
			out = append(out, event)
			parsed = true
		}
		return out, parsed
	default:
		return nil, false
	}
}

func eventFromAny(value interface{}) (Event, bool) {
	switch event := value.(type) {
	case Event:
		return event, true
	case *Event:
		if event == nil {
			return Event{}, false
		}
		return *event, true
	case map[string]interface{}:
		return eventRecordFromMap(event)
	default:
		return Event{}, false
	}
}

func eventRecordFromMap(raw map[string]interface{}) (Event, bool) {
	var event Event
	parsed := false

	if value, ok := stringValue(raw["type"]); ok {
		event.Type = value
		parsed = true
	}
	if value, ok := stringValue(raw["thread_id"]); ok {
		event.ThreadID = value
		parsed = true
	}
	if value, ok := stringValue(raw["turn_id"]); ok {
		event.TurnID = value
		parsed = true
	}
	if value, ok := stringValue(raw["call_id"]); ok {
		event.CallID = value
		parsed = true
	}
	if value, ok := stringValue(raw["item_id"]); ok {
		event.ItemID = value
		parsed = true
	}
	if value, ok := stringValue(raw["item_type"]); ok {
		event.ItemType = value
		parsed = true
	}
	if value, ok := stringValue(raw["item_phase"]); ok {
		event.ItemPhase = value
		parsed = true
	}
	if value, ok := stringValue(raw["stream"]); ok {
		event.Stream = value
		parsed = true
	}
	if value, ok := stringValue(raw["command"]); ok {
		event.Command = value
		parsed = true
	}
	if value, ok := stringValue(raw["cwd"]); ok {
		event.CWD = value
		parsed = true
	}
	if value, ok := stringValue(raw["chunk"]); ok {
		event.Chunk = value
		parsed = true
	}
	if value, ok := intValue(raw["exit_code"]); ok {
		event.ExitCode = &value
		parsed = true
	}
	if value, ok := intValue(raw["input_tokens"]); ok {
		event.InputTokens = value
		parsed = true
	}
	if value, ok := intValue(raw["output_tokens"]); ok {
		event.OutputTokens = value
		parsed = true
	}
	if value, ok := intValue(raw["total_tokens"]); ok {
		event.TotalTokens = value
		parsed = true
	}
	if value, ok := stringValue(raw["message"]); ok {
		event.Message = value
		parsed = true
	}

	return event, parsed
}

func stringValue(value interface{}) (string, bool) {
	typed, ok := value.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(typed), true
}

func intValue(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		n, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func boolValue(value interface{}) (bool, bool) {
	typed, ok := value.(bool)
	return typed, ok
}

func timeValue(value interface{}) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed, true
	case string:
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed))
		if err != nil {
			return time.Time{}, false
		}
		return parsed, true
	default:
		return time.Time{}, false
	}
}

func sessionSummaryMessage(e Event) (string, bool) {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		return "", false
	}

	switch e.Type {
	case "item.completed":
		if strings.EqualFold(strings.TrimSpace(e.ItemType), "agentMessage") {
			return message, true
		}
		return "", false
	case "turn.completed", "turn.failed", "turn.cancelled", "session.completed", "run.completed", "run.failed", "error":
		if messageDerivedFromChunkOnly(e, message) {
			return "", false
		}
		return message, true
	default:
		return "", false
	}
}

func messageDerivedFromChunkOnly(e Event, message string) bool {
	chunk := strings.TrimSpace(e.Chunk)
	return chunk != "" && message == chunk
}
