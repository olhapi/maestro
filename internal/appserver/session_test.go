package appserver

import "testing"

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
