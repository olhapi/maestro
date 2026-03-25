package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/appserver/protocol"
)

const defaultSessionHistoryLimit = 64

// Event is a minimal app-server event envelope.
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
	startedTurnID   string
}

func (s *Session) Clone() Session {
	if s == nil {
		return Session{}
	}
	cp := *s
	cp.History = append([]Event(nil), s.History...)
	return cp
}

func (s *Session) Summary() Session {
	if s == nil {
		return Session{}
	}
	cp := *s
	cp.History = nil
	return cp
}

func (s *Session) ResetThreadState() {
	if s == nil {
		return
	}
	maxHistory := s.MaxHistory
	issueID := s.IssueID
	issueIdentifier := s.IssueIdentifier
	appServerPID := s.AppServerPID
	*s = Session{
		IssueID:         issueID,
		IssueIdentifier: issueIdentifier,
		AppServerPID:    appServerPID,
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
		session.AppServerPID = value
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

func (s *Session) ApplyEvent(e Event) {
	if s.MaxHistory <= 0 {
		s.MaxHistory = defaultSessionHistoryLimit
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
	if out.CallID == "" {
		out.CallID = fallback.CallID
	}
	if out.ItemID == "" {
		out.ItemID = fallback.ItemID
	}
	if out.ItemType == "" {
		out.ItemType = fallback.ItemType
	}
	if out.ItemPhase == "" {
		out.ItemPhase = fallback.ItemPhase
	}
	if out.Stream == "" {
		out.Stream = fallback.Stream
	}
	if out.Command == "" {
		out.Command = fallback.Command
	}
	if out.CWD == "" {
		out.CWD = fallback.CWD
	}
	if out.Chunk == "" {
		out.Chunk = fallback.Chunk
	}
	if out.ExitCode == nil {
		out.ExitCode = fallback.ExitCode
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
	e.CallID = strings.TrimSpace(firstStr(m, "call_id", "callId"))
	e.ItemID = strings.TrimSpace(firstStr(m, "item_id", "itemId"))
	e.ItemType = strings.TrimSpace(firstStr(m, "item_type", "itemType"))
	e.ItemPhase = strings.TrimSpace(firstStr(m, "item_phase", "itemPhase"))
	e.Stream = strings.TrimSpace(firstStr(m, "stream"))
	e.Command = strings.TrimSpace(firstStr(m, "command"))
	e.CWD = strings.TrimSpace(firstStr(m, "cwd"))
	e.Chunk = firstStr(m, "chunk")
	e.ExitCode = firstIntPtr(m, "exit_code", "exitCode")
	e.Message = firstStr(m, "message", "content", "reason")
	applyNestedItemMetadata(&e, m)
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
		if e.CallID == "" {
			e.CallID = strings.TrimSpace(firstStr(params, "call_id", "callId"))
		}
		if e.ItemID == "" {
			e.ItemID = strings.TrimSpace(firstStr(params, "item_id", "itemId"))
		}
		if e.ItemType == "" {
			e.ItemType = strings.TrimSpace(firstStr(params, "item_type", "itemType"))
		}
		if e.ItemPhase == "" {
			e.ItemPhase = strings.TrimSpace(firstStr(params, "item_phase", "itemPhase"))
		}
		if e.Stream == "" {
			e.Stream = strings.TrimSpace(firstStr(params, "stream"))
		}
		if e.Command == "" {
			e.Command = strings.TrimSpace(firstStr(params, "command"))
		}
		if e.CWD == "" {
			e.CWD = strings.TrimSpace(firstStr(params, "cwd"))
		}
		if e.Chunk == "" {
			e.Chunk = firstStr(params, "chunk")
		}
		if e.ExitCode == nil {
			e.ExitCode = firstIntPtr(params, "exit_code", "exitCode")
		}
		if e.Message == "" {
			e.Message = extractMessage(params)
		}
		applyNestedItemMetadata(&e, params)
		if e.TotalTokens == 0 {
			e.InputTokens, e.OutputTokens, e.TotalTokens = tokenUsageFromMap(params)
		}
	}
	if e.CallID == "" {
		e.CallID = strings.TrimSpace(firstStr(root, "call_id", "callId"))
	}
	if e.ItemID == "" {
		e.ItemID = strings.TrimSpace(firstStr(root, "item_id", "itemId"))
	}
	if e.ItemType == "" {
		e.ItemType = strings.TrimSpace(firstStr(root, "item_type", "itemType"))
	}
	if e.ItemPhase == "" {
		e.ItemPhase = strings.TrimSpace(firstStr(root, "item_phase", "itemPhase"))
	}
	if e.Stream == "" {
		e.Stream = strings.TrimSpace(firstStr(root, "stream"))
	}
	if e.Command == "" {
		e.Command = strings.TrimSpace(firstStr(root, "command"))
	}
	if e.CWD == "" {
		e.CWD = strings.TrimSpace(firstStr(root, "cwd"))
	}
	if e.Chunk == "" {
		e.Chunk = firstStr(root, "chunk")
	}
	if e.ExitCode == nil {
		e.ExitCode = firstIntPtr(root, "exit_code", "exitCode")
	}
	if e.Message == "" {
		e.Message = extractMessage(m)
	}
	applyNestedItemMetadata(&e, root)
	if e.Message == "" {
		e.Message = extractMessage(root)
	}
	if e.Message == "" {
		switch {
		case strings.TrimSpace(e.Chunk) != "":
			e.Message = strings.TrimSpace(e.Chunk)
		case e.Command != "":
			e.Message = e.Command
		}
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

func applyNestedItemMetadata(event *Event, container map[string]interface{}) {
	if event == nil || container == nil {
		return
	}
	item, ok := asMap(container["item"])
	if !ok {
		return
	}
	if event.ItemID == "" {
		event.ItemID = strings.TrimSpace(firstStr(item, "id", "item_id", "itemId"))
	}
	if event.ItemType == "" {
		event.ItemType = strings.TrimSpace(firstStr(item, "type", "item_type", "itemType"))
	}
	if event.ItemPhase == "" {
		event.ItemPhase = strings.TrimSpace(firstStr(item, "phase", "item_phase", "itemPhase"))
	}
	if event.Message == "" {
		event.Message = extractMessage(item)
	}
	if event.Command == "" {
		event.Command = strings.TrimSpace(firstStr(item, "command"))
	}
	if event.CWD == "" {
		event.CWD = strings.TrimSpace(firstStr(item, "cwd"))
	}
	if event.ExitCode == nil {
		event.ExitCode = firstIntPtr(item, "exit_code", "exitCode")
	}
	if event.Chunk == "" && strings.EqualFold(strings.TrimSpace(event.ItemType), "commandExecution") {
		event.Chunk = firstStr(item, "aggregatedOutput", "aggregated_output")
	}
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
	if s := firstStr(m, "message", "content", "reason", "delta", "text", "chunk", "command"); s != "" {
		return s
	}
	for _, key := range []string{"message", "content", "delta", "text", "chunk"} {
		if s := extractMessageValue(m[key]); s != "" {
			return s
		}
	}
	return ""
}

func firstIntPtr(m map[string]interface{}, keys ...string) *int {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case float64:
			out := int(x)
			return &out
		case int:
			out := x
			return &out
		case int64:
			out := int(x)
			return &out
		case string:
			var out int
			if _, err := fmt.Sscanf(strings.TrimSpace(x), "%d", &out); err == nil {
				return &out
			}
		}
	}
	return nil
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
