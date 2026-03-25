package appserver

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver/protocol"
)

func TestParseEventLine(t *testing.T) {
	line := `{"type":"turn.completed","thread_id":"th1","turn_id":"tu1","input_tokens":10,"output_tokens":20,"total_tokens":30,"message":"ok"}`
	e, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if e.Type != "turn.completed" || e.ThreadID != "th1" || e.TurnID != "tu1" {
		t.Fatalf("unexpected event: %#v", e)
	}
}

func TestParseEventNestedEnvelope(t *testing.T) {
	line := `{"event":{"type":"turn.completed","threadId":"th1","turnId":"tu1","usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18},"content":"done"}}`
	e, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if e.Type != "turn.completed" || e.ThreadID != "th1" || e.TurnID != "tu1" {
		t.Fatalf("unexpected event: %#v", e)
	}
	if e.InputTokens != 11 || e.OutputTokens != 7 || e.TotalTokens != 18 {
		t.Fatalf("unexpected token usage: %#v", e)
	}
	if e.Message != "done" {
		t.Fatalf("unexpected message: %#v", e)
	}
}

func TestParseEventLineAgentMessageDeltaNotification(t *testing.T) {
	line := `{"method":"item/agentMessage/delta","params":{"itemId":"item-1","threadId":"th1","turnId":"tu1","delta":"Planning the next step"}}`
	e, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if e.Type != "item.agentMessage.delta" || e.ThreadID != "th1" || e.TurnID != "tu1" {
		t.Fatalf("unexpected event: %#v", e)
	}
	if e.Message != "Planning the next step" {
		t.Fatalf("expected delta text as message, got %#v", e)
	}
}

func TestParseEventLineAgentMessageContentDelta(t *testing.T) {
	line := `{"event":{"type":"agent_message_content_delta","thread_id":"th2","turn_id":"tu2","delta":"Writing the patch now"}}`
	e, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if e.Type != "agent_message_content_delta" || e.ThreadID != "th2" || e.TurnID != "tu2" {
		t.Fatalf("unexpected event: %#v", e)
	}
	if e.Message != "Writing the patch now" {
		t.Fatalf("expected content delta text as message, got %#v", e)
	}
}

func TestParseEventLineItemStartedPreservesAgentMessageMetadata(t *testing.T) {
	line := `{"method":"item/started","params":{"threadId":"th5","turnId":"tu5","item":{"id":"item-5","type":"agentMessage","phase":"commentary","text":"Thinking through the change"}}}`
	e, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if e.Type != "item.started" || e.ThreadID != "th5" || e.TurnID != "tu5" {
		t.Fatalf("unexpected event: %#v", e)
	}
	if e.ItemID != "item-5" || e.ItemType != "agentMessage" || e.ItemPhase != "commentary" {
		t.Fatalf("expected nested item metadata, got %#v", e)
	}
	if e.Message != "Thinking through the change" {
		t.Fatalf("expected nested item text as message, got %#v", e)
	}
}

func TestParseEventLineItemCompletedPreservesCommandMetadata(t *testing.T) {
	line := `{"method":"item/completed","params":{"threadId":"th6","turnId":"tu6","item":{"id":"item-6","type":"commandExecution","command":"npm run test","cwd":"/repo","aggregatedOutput":"tests passed","exitCode":0}}}`
	e, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if e.Type != "item.completed" || e.ThreadID != "th6" || e.TurnID != "tu6" {
		t.Fatalf("unexpected event: %#v", e)
	}
	if e.ItemID != "item-6" || e.ItemType != "commandExecution" {
		t.Fatalf("expected command item metadata, got %#v", e)
	}
	if e.Command != "npm run test" || e.CWD != "/repo" || e.Chunk != "tests passed" {
		t.Fatalf("expected nested command metadata, got %#v", e)
	}
	if e.ExitCode == nil || *e.ExitCode != 0 {
		t.Fatalf("expected nested exit code, got %#v", e)
	}
}

func TestParseEventLineThreadTokenUsageUpdated(t *testing.T) {
	line := `{"method":"thread/tokenUsage/updated","params":{"threadId":"th3","turnId":"tu3","tokenUsage":{"last":{"inputTokens":4,"outputTokens":5,"totalTokens":9,"cachedInputTokens":0,"reasoningOutputTokens":0},"total":{"inputTokens":10,"outputTokens":20,"totalTokens":30,"cachedInputTokens":0,"reasoningOutputTokens":0}}}}`
	e, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if e.Type != "thread.tokenUsage.updated" || e.ThreadID != "th3" || e.TurnID != "tu3" {
		t.Fatalf("unexpected event: %#v", e)
	}
	if e.InputTokens != 10 || e.OutputTokens != 20 || e.TotalTokens != 30 {
		t.Fatalf("expected total token usage, got %#v", e)
	}
}

func TestParseEventLineExecCommandOutputDeltaMetadata(t *testing.T) {
	line := `{"type":"exec_command_output_delta","thread_id":"thx","turn_id":"tux","call_id":"call-1","stream":"stderr","chunk":"\u001b[31mboom\u001b[39m"}`
	e, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if e.Type != "exec_command_output_delta" || e.ThreadID != "thx" || e.TurnID != "tux" {
		t.Fatalf("unexpected event metadata: %#v", e)
	}
	if e.CallID != "call-1" || e.Stream != "stderr" || e.Chunk == "" {
		t.Fatalf("expected command output metadata to be parsed, got %#v", e)
	}
	if e.Message == "" {
		t.Fatalf("expected chunk-derived message, got %#v", e)
	}
}

func TestParseEventLineCommandExecutionOutputDeltaNotification(t *testing.T) {
	line := `{"method":"item/commandExecution/outputDelta","params":{"threadId":"thc","turnId":"tuc","callId":"call-2","stream":"stdout","chunk":"ready","command":"npm run dev","cwd":"/repo/apps/frontend"}}`
	e, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if e.Type != "item.commandExecution.outputDelta" || e.ThreadID != "thc" || e.TurnID != "tuc" {
		t.Fatalf("unexpected event metadata: %#v", e)
	}
	if e.CallID != "call-2" || e.Stream != "stdout" || e.Command != "npm run dev" || e.CWD != "/repo/apps/frontend" {
		t.Fatalf("expected command execution metadata to be preserved, got %#v", e)
	}
	if e.Message != "ready" {
		t.Fatalf("expected chunk-based message, got %#v", e.Message)
	}
}

func TestSessionApplyEvent(t *testing.T) {
	s := &Session{}
	s.ApplyEvent(Event{Type: "turn.started", ThreadID: "th", TurnID: "tu", InputTokens: 1})
	s.ApplyEvent(Event{Type: "turn.completed", ThreadID: "th", TurnID: "tu", TotalTokens: 5})
	if s.SessionID != "th-tu" {
		t.Fatalf("unexpected session id: %s", s.SessionID)
	}
	if s.TurnsStarted != 1 || s.TurnsCompleted != 1 {
		t.Fatalf("unexpected turn counters: %+v", s)
	}
	if !s.Terminal || s.TerminalReason != "turn.completed" {
		t.Fatalf("expected terminal turn.completed, got %+v", s)
	}
}

func TestSessionApplyEventUsesCompletedAgentMessagesForLastMessage(t *testing.T) {
	s := &Session{}

	s.ApplyEvent(Event{
		Type:      "item.completed",
		ThreadID:  "th",
		TurnID:    "tu",
		ItemID:    "msg-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Message:   "Planning the verification pass",
	})
	if s.LastMessage != "Planning the verification pass" {
		t.Fatalf("expected commentary summary to persist, got %+v", s)
	}

	s.ApplyEvent(Event{
		Type:      "item.completed",
		ThreadID:  "th",
		TurnID:    "tu",
		ItemID:    "msg-2",
		ItemType:  "agentMessage",
		ItemPhase: "final_answer",
		Message:   "Implemented the change and queued tests.",
	})
	if s.LastMessage != "Implemented the change and queued tests." {
		t.Fatalf("expected latest completed agent message to win, got %+v", s)
	}
}

func TestSessionApplyEventUsesTerminalEventMessagesForLastMessage(t *testing.T) {
	s := &Session{}

	s.ApplyEvent(Event{
		Type:        "turn.completed",
		ThreadID:    "th",
		TurnID:      "tu",
		Message:     "Verification passed.",
		TotalTokens: 9,
	})
	if s.LastMessage != "Verification passed." {
		t.Fatalf("expected terminal event message to persist, got %+v", s)
	}
}

func TestSessionApplyEventUsesFailedAndCancelledTurnMessagesForLastMessage(t *testing.T) {
	tests := []struct {
		name string
		typ  string
	}{
		{name: "failed", typ: "turn.failed"},
		{name: "cancelled", typ: "turn.cancelled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{}

			s.ApplyEvent(Event{
				Type:     tt.typ,
				ThreadID: "th",
				TurnID:   "tu",
				Message:  "Turn ended before completion.",
			})

			if s.LastMessage != "Turn ended before completion." {
				t.Fatalf("expected %s message to persist, got %+v", tt.typ, s)
			}
			if !s.Terminal || s.TerminalReason != tt.typ {
				t.Fatalf("expected %s to mark the session terminal, got %+v", tt.typ, s)
			}
		})
	}
}

func TestSessionSummaryAndCoercionHelpers(t *testing.T) {
	timestamp := time.Date(2026, time.March, 18, 10, 15, 0, 0, time.UTC)
	exitCode := 7

	event := Event{
		Type:         "item.completed",
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		CallID:       "call-1",
		ItemID:       "item-1",
		ItemType:     "commandExecution",
		ItemPhase:    "completed",
		Stream:       "stderr",
		Command:      "go test ./...",
		CWD:          "/repo",
		Chunk:        "boom",
		ExitCode:     &exitCode,
		InputTokens:  3,
		OutputTokens: 4,
		TotalTokens:  7,
		Message:      "failed",
	}
	session := Session{
		IssueID:         "42",
		IssueIdentifier: "MAE-42",
		SessionID:       "session-1",
		ThreadID:        "thread-1",
		TurnID:          "turn-1",
		AppServerPID:    99,
		LastEvent:       "turn.completed",
		LastTimestamp:   timestamp,
		LastMessage:     "all done",
		InputTokens:     11,
		OutputTokens:    12,
		TotalTokens:     23,
		EventsProcessed: 5,
		TurnsStarted:    2,
		TurnsCompleted:  2,
		Terminal:        true,
		TerminalReason:  "turn.completed",
		History:         []Event{event},
		MaxHistory:      4,
	}

	summary := session.Summary()
	if len(summary.History) != 0 {
		t.Fatalf("expected summary to drop history, got %+v", summary)
	}
	if summary.LastMessage != session.LastMessage || summary.SessionID != session.SessionID {
		t.Fatalf("expected summary to preserve metadata, got %+v", summary)
	}

	clone := session.Clone()
	clone.History[0].Message = "mutated"
	if session.History[0].Message != "failed" {
		t.Fatalf("expected clone to copy history slice, got %+v", session.History)
	}

	fromSession, ok := SessionFromAny(session)
	if !ok || fromSession.SessionID != session.SessionID {
		t.Fatalf("expected SessionFromAny(Session) to succeed, got %+v ok=%v", fromSession, ok)
	}

	fromPointer, ok := SessionFromAny(&session)
	if !ok || fromPointer.ThreadID != session.ThreadID {
		t.Fatalf("expected SessionFromAny(*Session) to succeed, got %+v ok=%v", fromPointer, ok)
	}

	if _, ok := SessionFromAny((*Session)(nil)); ok {
		t.Fatal("expected nil *Session to fail coercion")
	}
	if _, ok := SessionFromAny("invalid"); ok {
		t.Fatal("expected invalid session value to fail coercion")
	}

	rawSession := map[string]interface{}{
		"issue_id":              "42 ",
		"issue_identifier":      " MAE-42",
		"session_id":            "session-1",
		"thread_id":             "thread-1",
		"turn_id":               "turn-1",
		"codex_app_server_pid":  int64(99),
		"last_event":            "turn.completed",
		"last_timestamp":        timestamp.Format(time.RFC3339),
		"last_message":          "all done",
		"input_tokens":          jsonNumber("11"),
		"output_tokens":         float64(12),
		"total_tokens":          int32(23),
		"events_processed":      int16(5),
		"turns_started":         int8(2),
		"turns_completed":       int(2),
		"terminal":              true,
		"terminal_reason":       "turn.completed",
		"history":               []interface{}{event, &event, map[string]interface{}{"type": "turn.completed", "thread_id": "thread-1", "turn_id": "turn-1", "message": "done", "exit_code": float64(0)}, "skip"},
		"ignored_extra_payload": "ignored",
	}

	fromMap, ok := SessionFromAny(rawSession)
	if !ok {
		t.Fatal("expected SessionFromAny(map) to succeed")
	}
	if fromMap.IssueID != "42" || fromMap.IssueIdentifier != "MAE-42" {
		t.Fatalf("expected trimmed identifiers, got %+v", fromMap)
	}
	if fromMap.LastTimestamp != timestamp || len(fromMap.History) != 3 {
		t.Fatalf("expected parsed timestamp/history, got %+v", fromMap)
	}
	if fromMap.History[2].ExitCode == nil || *fromMap.History[2].ExitCode != 0 {
		t.Fatalf("expected parsed exit code from history map, got %+v", fromMap.History[2])
	}

	sessions := SessionsFromMap(map[string]interface{}{
		"typed":   session,
		"pointer": &session,
		"raw":     rawSession,
		"bad":     false,
		"nil":     (*Session)(nil),
	})
	if len(sessions) != 3 {
		t.Fatalf("expected only coercible sessions, got %+v", sessions)
	}
	if sessions["raw"].LastTimestamp != timestamp {
		t.Fatalf("expected parsed raw timestamp, got %+v", sessions["raw"])
	}

	if history, ok := eventsValue([]Event{event}); !ok || len(history) != 1 || history[0].Message != "failed" {
		t.Fatalf("expected []Event to clone successfully, got %+v ok=%v", history, ok)
	}
	if history, ok := eventsValue([]interface{}{event, &event, rawSession["history"].([]interface{})[2], nil}); !ok || len(history) != 3 {
		t.Fatalf("expected []interface{} history coercion, got %+v ok=%v", history, ok)
	}
	if _, ok := eventsValue("bad"); ok {
		t.Fatal("expected invalid history value to fail coercion")
	}

	if parsedEvent, ok := eventFromAny(event); !ok || parsedEvent.Type != event.Type {
		t.Fatalf("expected typed event coercion, got %+v ok=%v", parsedEvent, ok)
	}
	if parsedEvent, ok := eventFromAny(&event); !ok || parsedEvent.CallID != event.CallID {
		t.Fatalf("expected pointer event coercion, got %+v ok=%v", parsedEvent, ok)
	}
	if _, ok := eventFromAny((*Event)(nil)); ok {
		t.Fatal("expected nil *Event to fail coercion")
	}
	if _, ok := eventFromAny(123); ok {
		t.Fatal("expected invalid event to fail coercion")
	}

	parsedEvent, ok := eventRecordFromMap(map[string]interface{}{
		"type":          " item.completed ",
		"thread_id":     "thread-1",
		"turn_id":       "turn-1",
		"call_id":       "call-1",
		"item_id":       "item-1",
		"item_type":     "commandExecution",
		"item_phase":    "completed",
		"stream":        "stderr",
		"command":       "go test ./...",
		"cwd":           "/repo",
		"chunk":         "boom",
		"exit_code":     jsonNumber("7"),
		"input_tokens":  float32(3),
		"output_tokens": int64(4),
		"total_tokens":  int16(7),
		"message":       " failed ",
	})
	if !ok {
		t.Fatal("expected eventRecordFromMap to succeed")
	}
	if parsedEvent.Type != "item.completed" || parsedEvent.Message != "failed" {
		t.Fatalf("expected trimmed event fields, got %+v", parsedEvent)
	}
	if parsedEvent.ExitCode == nil || *parsedEvent.ExitCode != 7 {
		t.Fatalf("expected parsed exit code, got %+v", parsedEvent)
	}
	if _, ok := eventRecordFromMap(map[string]interface{}{"ignored": true}); ok {
		t.Fatal("expected empty event map to fail coercion")
	}

	if value, ok := stringValue("  ready "); !ok || value != "ready" {
		t.Fatalf("expected trimmed string value, got %q ok=%v", value, ok)
	}
	if _, ok := stringValue(5); ok {
		t.Fatal("expected non-string value to fail stringValue")
	}

	intTests := []struct {
		name  string
		value interface{}
		want  int
	}{
		{name: "int", value: int(5), want: 5},
		{name: "int8", value: int8(6), want: 6},
		{name: "int16", value: int16(7), want: 7},
		{name: "int32", value: int32(8), want: 8},
		{name: "int64", value: int64(9), want: 9},
		{name: "float32", value: float32(10), want: 10},
		{name: "float64", value: float64(11), want: 11},
		{name: "json-number", value: jsonNumber("12"), want: 12},
	}
	for _, tc := range intTests {
		value, ok := intValue(tc.value)
		if !ok || value != tc.want {
			t.Fatalf("expected %s to parse as %d, got %d ok=%v", tc.name, tc.want, value, ok)
		}
	}
	if _, ok := intValue(jsonNumber("12.5")); ok {
		t.Fatal("expected fractional json.Number to fail intValue")
	}
	if _, ok := intValue("bad"); ok {
		t.Fatal("expected invalid int value to fail coercion")
	}

	if value, ok := boolValue(true); !ok || !value {
		t.Fatalf("expected true bool value, got %v ok=%v", value, ok)
	}
	if _, ok := boolValue("true"); ok {
		t.Fatal("expected non-bool value to fail boolValue")
	}

	if value, ok := timeValue(timestamp); !ok || !value.Equal(timestamp) {
		t.Fatalf("expected time.Time coercion, got %v ok=%v", value, ok)
	}
	if value, ok := timeValue(" " + timestamp.Format(time.RFC3339) + " "); !ok || !value.Equal(timestamp) {
		t.Fatalf("expected RFC3339 string coercion, got %v ok=%v", value, ok)
	}
	if _, ok := timeValue("not-a-time"); ok {
		t.Fatal("expected invalid RFC3339 string to fail timeValue")
	}
}

func jsonNumber(value string) interface{} {
	return json.Number(value)
}

func TestSessionApplyEventIgnoresStreamingAgentMessageDeltasForLastMessage(t *testing.T) {
	s := &Session{LastMessage: "Completed summary"}

	s.ApplyEvent(Event{
		Type:     "item.agentMessage.delta",
		ThreadID: "th",
		TurnID:   "tu",
		ItemID:   "msg-1",
		ItemType: "agentMessage",
		Message:  "Partial stream chunk",
	})
	if s.LastMessage != "Completed summary" {
		t.Fatalf("expected agent delta to leave last message unchanged, got %+v", s)
	}
	if s.LastEvent != "item.agentMessage.delta" || len(s.History) != 1 {
		t.Fatalf("expected streaming delta to remain in session history, got %+v", s)
	}
}

func TestSessionApplyEventIgnoresCommandOutputDeltasForLastMessage(t *testing.T) {
	s := &Session{LastMessage: "Completed summary"}

	s.ApplyEvent(Event{
		Type:     "item.commandExecution.outputDelta",
		ThreadID: "th",
		TurnID:   "tu",
		ItemID:   "cmd-1",
		ItemType: "commandExecution",
		Command:  "go test ./...",
		Chunk:    "ok\tgithub.com/olhapi/maestro/internal/appserver\t0.123s",
		Message:  "ok\tgithub.com/olhapi/maestro/internal/appserver\t0.123s",
	})
	if s.LastMessage != "Completed summary" {
		t.Fatalf("expected command output delta to leave last message unchanged, got %+v", s)
	}
}

func TestSessionApplyEventRetainsCompletedSummaryWhileStreaming(t *testing.T) {
	s := &Session{}

	s.ApplyEvent(Event{
		Type:      "item.completed",
		ThreadID:  "th",
		TurnID:    "tu",
		ItemID:    "msg-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Message:   "Reviewing the remaining edge cases.",
	})
	s.ApplyEvent(Event{
		Type:     "agent_message_content_delta",
		ThreadID: "th",
		TurnID:   "tu",
		ItemID:   "msg-2",
		ItemType: "agentMessage",
		Message:  "Streaming fragment",
	})
	s.ApplyEvent(Event{
		Type:     "exec_command_output_delta",
		ThreadID: "th",
		TurnID:   "tu",
		CallID:   "call-1",
		Stream:   "stdout",
		Chunk:    "line 1",
		Message:  "line 1",
	})
	if s.LastMessage != "Reviewing the remaining edge cases." {
		t.Fatalf("expected completed summary to survive later streaming events, got %+v", s)
	}
}

func TestSessionHistoryRingBuffer(t *testing.T) {
	s := &Session{MaxHistory: 2}
	s.ApplyEvent(Event{Type: "a"})
	s.ApplyEvent(Event{Type: "b"})
	s.ApplyEvent(Event{Type: "c"})
	if len(s.History) != 2 {
		t.Fatalf("expected 2 events kept, got %d", len(s.History))
	}
	if s.History[0].Type != "b" || s.History[1].Type != "c" {
		t.Fatalf("unexpected history order: %#v", s.History)
	}
}

func TestSessionHistoryUsesDefaultHistoryLimit(t *testing.T) {
	s := &Session{}
	for i := 0; i < defaultSessionHistoryLimit+5; i++ {
		s.ApplyEvent(Event{Type: fmt.Sprintf("event-%d", i)})
	}
	if len(s.History) != defaultSessionHistoryLimit {
		t.Fatalf("expected default history limit %d, got %d", defaultSessionHistoryLimit, len(s.History))
	}
	if s.History[0].Type != "event-5" {
		t.Fatalf("expected oldest retained event to be event-5, got %#v", s.History[0])
	}
	if s.History[len(s.History)-1].Type != fmt.Sprintf("event-%d", defaultSessionHistoryLimit+4) {
		t.Fatalf("expected newest event retained, got %#v", s.History[len(s.History)-1])
	}
}

func TestEventFromMessageAndMergeEvents(t *testing.T) {
	msg, ok := protocol.DecodeMessage(`{"method":"turn/completed","params":{"threadId":"th","turn":{"id":"tu"}}}`)
	if !ok {
		t.Fatal("expected message decode")
	}
	typed, ok := EventFromMessage(msg)
	if !ok {
		t.Fatal("expected typed event")
	}
	fallback, ok := ParseEventLine(`{"method":"turn/completed","params":{"threadId":"th","turn":{"id":"tu"}},"input_tokens":3,"output_tokens":5,"total_tokens":8}`)
	if !ok {
		t.Fatal("expected fallback event")
	}
	merged := MergeEvents(typed, fallback)
	if merged.Type != "turn.completed" || merged.ThreadID != "th" || merged.TurnID != "tu" {
		t.Fatalf("unexpected merged event: %+v", merged)
	}
	if merged.TotalTokens != 8 {
		t.Fatalf("expected fallback totals, got %+v", merged)
	}
}

func TestMergeEventsIncludesCommandMetadata(t *testing.T) {
	primary := Event{
		Type:   "exec_command_output_delta",
		CallID: "call-3",
	}
	exitCode := 1
	fallback := Event{
		Stream:   "stderr",
		Command:  "npm test",
		CWD:      "/repo",
		Chunk:    "command failed",
		Message:  "command failed",
		ExitCode: &exitCode,
	}
	merged := MergeEvents(primary, fallback)
	if merged.CallID != "call-3" || merged.Stream != "stderr" || merged.Command != "npm test" || merged.CWD != "/repo" {
		t.Fatalf("expected merged command metadata, got %#v", merged)
	}
	if merged.Chunk != "command failed" || merged.ExitCode == nil || *merged.ExitCode != 1 {
		t.Fatalf("expected merged output payload, got %#v", merged)
	}
}

func TestMergeEventsIncludesItemMetadata(t *testing.T) {
	primary := Event{
		Type:     "item.agentMessage.delta",
		ThreadID: "th7",
		TurnID:   "tu7",
		ItemID:   "item-7",
	}
	fallback := Event{
		ItemType:  "agentMessage",
		ItemPhase: "final_answer",
		Message:   "Done.",
	}
	merged := MergeEvents(primary, fallback)
	if merged.ItemID != "item-7" || merged.ItemType != "agentMessage" || merged.ItemPhase != "final_answer" {
		t.Fatalf("expected merged item metadata, got %#v", merged)
	}
	if merged.Message != "Done." {
		t.Fatalf("expected fallback message, got %#v", merged)
	}
}

func TestEventFromMessageThreadTokenUsageUpdated(t *testing.T) {
	msg, ok := protocol.DecodeMessage(`{"method":"thread/tokenUsage/updated","params":{"threadId":"th4","turnId":"tu4","tokenUsage":{"last":{"inputTokens":1,"outputTokens":2,"totalTokens":3,"cachedInputTokens":0,"reasoningOutputTokens":0},"total":{"inputTokens":11,"outputTokens":7,"totalTokens":18,"cachedInputTokens":0,"reasoningOutputTokens":0}}}}`)
	if !ok {
		t.Fatal("expected message decode")
	}
	evt, ok := EventFromMessage(msg)
	if !ok {
		t.Fatal("expected token usage event")
	}
	if evt.Type != "thread.tokenUsage.updated" || evt.ThreadID != "th4" || evt.TurnID != "tu4" {
		t.Fatalf("unexpected event metadata: %+v", evt)
	}
	if evt.InputTokens != 11 || evt.OutputTokens != 7 || evt.TotalTokens != 18 {
		t.Fatalf("unexpected token usage totals: %+v", evt)
	}
}
