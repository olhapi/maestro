package agentruntime

import (
	"testing"
	"time"
)

func TestSessionApplyEventTracksTurnLifecycle(t *testing.T) {
	session := &Session{
		IssueID:         "iss_123",
		IssueIdentifier: "ISS-123",
		Metadata: map[string]interface{}{
			"provider": "codex",
		},
		MaxHistory: 2,
	}

	session.ApplyEvent(Event{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"})
	session.ApplyEvent(Event{
		Type:      "item.completed",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemType:  "agentMessage",
		ItemPhase: "final_answer",
		Message:   "working",
	})
	session.ApplyEvent(Event{
		Type:         "turn.completed",
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		Message:      "done",
		TotalTokens:  42,
		InputTokens:  20,
		OutputTokens: 22,
	})

	if session.SessionID != "thread-1-turn-1" {
		t.Fatalf("expected session id to track thread/turn, got %q", session.SessionID)
	}
	if session.TurnsStarted != 1 || session.TurnsCompleted != 1 {
		t.Fatalf("expected turn counters to increment, got started=%d completed=%d", session.TurnsStarted, session.TurnsCompleted)
	}
	if !session.Terminal || session.TerminalReason != "turn.completed" {
		t.Fatalf("expected terminal completion state, got terminal=%v reason=%q", session.Terminal, session.TerminalReason)
	}
	if session.TotalTokens != 42 || session.InputTokens != 20 || session.OutputTokens != 22 {
		t.Fatalf("expected token totals to update, got %+v", session)
	}
	if session.LastMessage != "done" {
		t.Fatalf("expected final message to be preserved, got %q", session.LastMessage)
	}
	if len(session.History) != 2 {
		t.Fatalf("expected history trimming to keep 2 events, got %d", len(session.History))
	}
	if session.History[0].Type != "item.completed" || session.History[1].Type != "turn.completed" {
		t.Fatalf("expected trimmed history to keep newest events, got %+v", session.History)
	}
	if !session.HasStartedTurn("turn-1") {
		t.Fatal("expected started turn helper to reflect current turn")
	}
}

func TestSessionFromAnyParsesStoredSessionAndClonesMetadata(t *testing.T) {
	timestamp := time.Now().UTC().Truncate(time.Second)
	rawMetadata := map[string]interface{}{
		"provider": "codex",
		"nested": map[string]interface{}{
			"transport": "app_server",
		},
	}
	raw := map[string]interface{}{
		"issue_id":             "iss_456",
		"issue_identifier":     "ISS-456",
		"session_id":           "thread-2-turn-2",
		"thread_id":            "thread-2",
		"turn_id":              "turn-2",
		"codex_app_server_pid": 777,
		"last_event":           "turn.completed",
		"last_timestamp":       timestamp.Format(time.RFC3339),
		"last_message":         "complete",
		"input_tokens":         7,
		"output_tokens":        8,
		"total_tokens":         15,
		"events_processed":     3,
		"turns_started":        1,
		"turns_completed":      1,
		"terminal":             true,
		"terminal_reason":      "turn.completed",
		"history":              []interface{}{map[string]interface{}{"type": "turn.started", "thread_id": "thread-2", "turn_id": "turn-2"}},
		"metadata":             rawMetadata,
	}

	session, ok := SessionFromAny(raw)
	if !ok {
		t.Fatal("expected SessionFromAny to parse persisted map")
	}
	if session.ProcessID != 777 {
		t.Fatalf("expected process id from persisted payload, got %d", session.ProcessID)
	}
	if !session.LastTimestamp.Equal(timestamp) {
		t.Fatalf("expected timestamp round-trip, got %s want %s", session.LastTimestamp, timestamp)
	}
	if len(session.History) != 1 || session.History[0].Type != "turn.started" {
		t.Fatalf("expected history to parse, got %+v", session.History)
	}
	if session.Metadata["provider"] != "codex" {
		t.Fatalf("expected metadata to parse, got %+v", session.Metadata)
	}

	rawNested := rawMetadata["nested"].(map[string]interface{})
	rawNested["transport"] = "mutated"
	if session.Metadata["nested"].(map[string]interface{})["transport"] != "app_server" {
		t.Fatalf("expected metadata clone to be isolated from source mutation, got %+v", session.Metadata)
	}
}
