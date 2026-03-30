package agentruntime

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type permissionConfigClient struct {
	caps    Capabilities
	updates []PermissionConfig
}

func (c *permissionConfigClient) Capabilities() Capabilities { return c.caps }

func (c *permissionConfigClient) RunTurn(context.Context, TurnRequest, func(*Session)) error {
	return nil
}

func (c *permissionConfigClient) UpdatePermissions(config PermissionConfig) {
	c.updates = append(c.updates, config)
}

func (c *permissionConfigClient) RespondToInteraction(context.Context, string, PendingInteractionResponse) error {
	return nil
}

func (c *permissionConfigClient) Session() *Session { return nil }

func (c *permissionConfigClient) Output() string { return "" }

func (c *permissionConfigClient) Close() error { return nil }

func TestApplyPermissionConfigHonorsClientCapabilities(t *testing.T) {
	t.Run("nil client", func(t *testing.T) {
		ApplyPermissionConfig(nil, PermissionConfig{ThreadSandbox: "workspace-write"})
	})

	t.Run("unsupported updates", func(t *testing.T) {
		client := &permissionConfigClient{caps: Capabilities{}}
		ApplyPermissionConfig(client, PermissionConfig{ThreadSandbox: "workspace-write"})
		if len(client.updates) != 0 {
			t.Fatalf("expected no permission updates, got %+v", client.updates)
		}
	})

	t.Run("supported updates", func(t *testing.T) {
		client := &permissionConfigClient{caps: Capabilities{RuntimePermissionUpdates: true}}
		want := PermissionConfig{
			ThreadSandbox: "danger-full-access",
			Metadata: map[string]interface{}{
				"source": "test",
			},
		}
		ApplyPermissionConfig(client, want)
		if len(client.updates) != 1 || client.updates[0].ThreadSandbox != want.ThreadSandbox {
			t.Fatalf("expected permission update to be forwarded, got %+v", client.updates)
		}
	})
}

func TestCapabilityHelpers(t *testing.T) {
	caps := Capabilities{
		Resume:                   true,
		QueuedInteractions:       true,
		PlanGating:               true,
		LocalImageInput:          true,
		DynamicTools:             true,
		RuntimePermissionUpdates: true,
	}
	if !caps.SupportsResume() || !caps.SupportsQueuedInteractions() || !caps.SupportsPlanGating() || !caps.SupportsLocalImageInput() || !caps.SupportsDynamicTools() || !caps.SupportsRuntimePermissionUpdates() {
		t.Fatalf("expected capability helpers to reflect enabled flags, got %+v", caps)
	}
}

func TestSessionCloneSummaryAndResetHelpers(t *testing.T) {
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	session := &Session{
		IssueID:         "iss-1",
		IssueIdentifier: "ISS-1",
		SessionID:       "thread-1-turn-1",
		ThreadID:        "thread-1",
		TurnID:          "turn-1",
		ProcessID:       123,
		LastEvent:       "turn.completed",
		LastTimestamp:   now,
		LastMessage:     "done",
		InputTokens:     5,
		OutputTokens:    7,
		TotalTokens:     12,
		EventsProcessed: 2,
		TurnsStarted:    1,
		TurnsCompleted:  1,
		Terminal:        true,
		TerminalReason:  "turn.completed",
		History: []Event{{
			Type:     "turn.completed",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Message:  "done",
		}},
		Metadata: map[string]interface{}{
			"provider": "codex",
			"nested": map[string]interface{}{
				"transport": "app_server",
			},
		},
		MaxHistory:    3,
		startedTurnID: "turn-1",
	}

	clone := session.Clone()
	clone.History[0].Message = "mutated"
	clone.Metadata["provider"] = "mutated"
	clone.Metadata["nested"].(map[string]interface{})["transport"] = "stdio"

	summary := session.Summary()
	if summary.History != nil {
		t.Fatalf("expected summary to omit history, got %+v", summary.History)
	}
	if summary.Metadata["provider"] != "codex" {
		t.Fatalf("expected summary metadata clone, got %+v", summary.Metadata)
	}

	if session.History[0].Message != "done" {
		t.Fatalf("expected clone to be isolated from history mutation, got %+v", session.History)
	}
	if session.Metadata["nested"].(map[string]interface{})["transport"] != "app_server" {
		t.Fatalf("expected clone to be isolated from metadata mutation, got %+v", session.Metadata)
	}

	session.ResetTurnState()
	if session.SessionID != "" || session.TurnID != "" || session.Terminal || session.TerminalReason != "" {
		t.Fatalf("expected turn state reset, got %+v", session)
	}
	if session.startedTurnID != "" {
		t.Fatalf("expected started turn state to clear, got %+v", session)
	}

	session.ResetThreadState()
	if session.IssueID != "iss-1" || session.IssueIdentifier != "ISS-1" || session.ProcessID != 123 || session.MaxHistory != 3 {
		t.Fatalf("expected thread state reset to preserve identity and process, got %+v", session)
	}
	if session.SessionID != "" || session.ThreadID != "" || session.TurnID != "" || session.Terminal || session.TerminalReason != "" {
		t.Fatalf("expected thread reset to clear turn state, got %+v", session)
	}
	if session.Metadata["provider"] != "codex" {
		t.Fatalf("expected metadata to survive thread reset, got %+v", session.Metadata)
	}
}

func TestSessionFromAnyAndSessionsFromMap(t *testing.T) {
	timestamp := time.Date(2026, 3, 29, 12, 5, 0, 0, time.UTC)
	session, ok := SessionFromAny(&Session{
		IssueID:       "iss-2",
		IssueIdentifier: "ISS-2",
		Metadata:      map[string]interface{}{"provider": "codex"},
	})
	if !ok || session.IssueID != "iss-2" {
		t.Fatalf("expected SessionFromAny pointer path, got %+v ok=%v", session, ok)
	}

	sessionMap := map[string]interface{}{
		"issue_id":         "iss-3",
		"issue_identifier": "ISS-3",
		"session_id":      "thread-3-turn-3",
		"thread_id":       "thread-3",
		"turn_id":         "turn-3",
		"codex_app_server_pid": 321,
		"last_event":       "turn.completed",
		"last_timestamp":   timestamp.Format(time.RFC3339),
		"last_message":     "done",
		"input_tokens":     float64(4),
		"output_tokens":    json.Number("6"),
		"total_tokens":     int64(10),
		"events_processed": 2,
		"turns_started":    1,
		"turns_completed":  1,
		"terminal":         true,
		"terminal_reason":  "turn.completed",
		"history": []interface{}{
			map[string]interface{}{
				"type":      "turn.started",
				"thread_id": "thread-3",
				"turn_id":   "turn-3",
			},
		},
		"metadata": map[string]interface{}{
			"transport": "app_server",
		},
	}
	parsed, ok := SessionFromAny(sessionMap)
	if !ok {
		t.Fatal("expected map session to parse")
	}
	if parsed.ProcessID != 321 || parsed.InputTokens != 4 || parsed.OutputTokens != 6 || parsed.TotalTokens != 10 {
		t.Fatalf("unexpected parsed session values: %+v", parsed)
	}
	if !parsed.LastTimestamp.Equal(timestamp) {
		t.Fatalf("expected timestamp to parse, got %s", parsed.LastTimestamp)
	}

	sessions := SessionsFromMap(map[string]interface{}{
		"a": parsed,
		"b": &parsed,
		"c": 123,
	})
	if len(sessions) != 2 {
		t.Fatalf("expected only valid sessions to survive, got %+v", sessions)
	}
}

func TestSessionParsingHelpers(t *testing.T) {
	if got, ok := stringValue("  hello "); !ok || got != "hello" {
		t.Fatalf("unexpected stringValue: %q %v", got, ok)
	}
	if _, ok := stringValue(123); ok {
		t.Fatal("expected non-string value to fail stringValue")
	}
	if got, ok := intValue(json.Number("9")); !ok || got != 9 {
		t.Fatalf("unexpected intValue: %d %v", got, ok)
	}
	if got, ok := boolValue(true); !ok || !got {
		t.Fatalf("unexpected boolValue: %v %v", got, ok)
	}
	if _, ok := boolValue("true"); ok {
		t.Fatal("expected non-bool value to fail boolValue")
	}
	if got, ok := timeValue("2026-03-29T12:00:00Z"); !ok || got.UTC().Format(time.RFC3339) != "2026-03-29T12:00:00Z" {
		t.Fatalf("unexpected timeValue parse: %s %v", got, ok)
	}

	event, ok := eventFromAny(map[string]interface{}{
		"type":         "item.completed",
		"thread_id":    "thread-1",
		"turn_id":      "turn-1",
		"item_id":      "item-1",
		"item_type":    "agentMessage",
		"item_phase":   "final_answer",
		"stream":       "stdout",
		"command":      "echo done",
		"cwd":          "/tmp",
		"chunk":        "done",
		"exit_code":    0,
		"input_tokens": 3,
		"output_tokens": 4,
		"total_tokens":  7,
		"message":      " done ",
	})
	if !ok || event.Message != "done" || event.ExitCode == nil || *event.ExitCode != 0 {
		t.Fatalf("unexpected event parsing result: %+v ok=%v", event, ok)
	}
	if msg, ok := sessionSummaryMessage(Event{Type: "turn.completed", Message: "done", Chunk: "done"}); ok || msg != "" {
		t.Fatal("expected chunk-only message to be ignored")
	}
	if msg, ok := sessionSummaryMessage(Event{Type: "item.completed", ItemType: "agentMessage", Message: "summary"}); !ok || msg != "summary" {
		t.Fatalf("expected summary message, got %q %v", msg, ok)
	}
	if !messageDerivedFromChunkOnly(Event{Chunk: "done"}, "done") {
		t.Fatal("expected chunk-only detection to match")
	}
}

func TestSessionHistoryHelpers(t *testing.T) {
	events, ok := eventsValue([]interface{}{
		Event{Type: "turn.started"},
		map[string]interface{}{"type": "turn.completed", "message": "done"},
		123,
	})
	if !ok || len(events) != 2 {
		t.Fatalf("expected mixed event slice to parse, got %+v ok=%v", events, ok)
	}
	if parsed, ok := eventFromAny(&Event{Type: "turn.completed"}); !ok || parsed.Type != "turn.completed" {
		t.Fatalf("expected pointer event parsing, got %+v ok=%v", parsed, ok)
	}
	if _, ok := eventFromAny(nil); ok {
		t.Fatal("expected nil event to fail")
	}

	cloned := cloneJSONMap(map[string]interface{}{
		"nested": map[string]interface{}{
			"transport": "app_server",
		},
		"values": []interface{}{map[string]interface{}{"mode": "safe"}},
	})
	cloned["nested"].(map[string]interface{})["transport"] = "stdio"
	if got := cloned["nested"].(map[string]interface{})["transport"]; got != "stdio" {
		t.Fatalf("expected cloned map to be mutable, got %v", got)
	}
	if got := cloned["values"].([]interface{})[0].(map[string]interface{})["mode"]; got != "safe" {
		t.Fatalf("expected nested slice entry to survive cloning, got %v", got)
	}
	if got := cloneJSONValue([]interface{}{map[string]interface{}{"mode": "safe"}}); got == nil {
		t.Fatal("expected cloneJSONValue slice to preserve data")
	}
}
