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
	if s.SessionID != "th-tu" {
		t.Fatalf("unexpected session id: %s", s.SessionID)
	}
}
