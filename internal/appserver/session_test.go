package appserver

import (
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
