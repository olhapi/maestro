package appserver

import (
	"fmt"
	"testing"

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
