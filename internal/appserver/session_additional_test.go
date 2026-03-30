package appserver

import (
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver/protocol"
)

func TestSessionHelperCoverageBranches(t *testing.T) {
	t.Run("parse event line walks nested candidates", func(t *testing.T) {
		line := `{
			"event": {"event_type":"turn.completed","threadId":"thread-event"},
			"data": {
				"event": {"event_type":"turn.started","threadId":"thread-data-event"},
				"data": {"event_type":"turn.completed","threadId":"thread-data-data"}
			},
			"payload": {
				"event": {"event_type":"turn.cancelled","threadId":"thread-payload-event"},
				"data": {"event_type":"turn.failed","threadId":"thread-payload-data"}
			},
			"params": {
				"event": {"event_type":"turn.started","threadId":"thread-params-event"},
				"data": {"event_type":"turn.completed","threadId":"thread-params-data"}
			}
		}`
		evt, ok := ParseEventLine(line)
		if !ok {
			t.Fatal("expected nested candidate event to parse")
		}
		if !strings.HasPrefix(evt.Type, "turn.") {
			t.Fatalf("unexpected parsed event type: %+v", evt)
		}
	})

	t.Run("event from message error and empty branches", func(t *testing.T) {
		if evt, ok := EventFromMessage(protocol.Message{Method: protocol.MethodThreadStarted, Params: []byte(`{"thread":1}`)}); ok || evt.ThreadID != "" {
			t.Fatalf("expected malformed thread/start params to fail, got %+v ok=%v", evt, ok)
		}

		if evt, ok := EventFromMessage(protocol.Message{Method: protocol.MethodThreadStarted, Params: []byte(`{"thread":{"id":""}}`)}); ok || evt.ThreadID != "" {
			t.Fatalf("expected empty thread/start payload to be ignored, got %+v ok=%v", evt, ok)
		}

		if evt, ok := EventFromMessage(protocol.Message{Method: protocol.MethodTurnStarted, Params: []byte(`{"threadId":"","turn":{"id":""}}`)}); ok || evt.ThreadID != "" || evt.TurnID != "" {
			t.Fatalf("expected empty turn/start payload to be ignored, got %+v ok=%v", evt, ok)
		}

		if evt, ok := EventFromMessage(protocol.Message{Method: protocol.MethodThreadTokenUsageUpdated, Params: []byte(`{"threadId":"th","turnId":"tu","tokenUsage":{"last":{"inputTokens":4,"outputTokens":5}}}`)}); !ok || evt.InputTokens != 4 || evt.OutputTokens != 5 || evt.TotalTokens != 9 {
			t.Fatalf("expected fallback token usage totals, got %+v ok=%v", evt, ok)
		}
	})

	t.Run("nested item and token usage helpers", func(t *testing.T) {
		evt := &Event{}
		applyNestedItemMetadata(evt, map[string]interface{}{
			"item": map[string]interface{}{
				"id":               "item-1",
				"type":             "commandExecution",
				"phase":            "final_answer",
				"command":          "git status",
				"cwd":              " /repo ",
				"aggregatedOutput": "combined output",
				"exitCode":         7,
				"message":          "",
			},
		})
		if evt.ItemID != "item-1" || evt.ItemType != "commandExecution" || evt.ItemPhase != "final_answer" {
			t.Fatalf("unexpected nested item metadata: %+v", evt)
		}
		if evt.Command != "git status" || evt.CWD != "/repo" || evt.Chunk != "combined output" {
			t.Fatalf("expected nested command metadata to be copied, got %+v", evt)
		}
		if evt.ExitCode == nil || *evt.ExitCode != 7 {
			t.Fatalf("expected exit code to be copied, got %+v", evt.ExitCode)
		}

		if input, output, total := tokenUsageFromMap(nil); input != 0 || output != 0 || total != 0 {
			t.Fatalf("expected nil token usage map to stay empty, got %d %d %d", input, output, total)
		}
		if input, output, total := tokenUsageFromMap(map[string]interface{}{"tokenUsage": "oops"}); input != 0 || output != 0 || total != 0 {
			t.Fatalf("expected non-map token usage to stay empty, got %d %d %d", input, output, total)
		}
		if input, output, total := tokenUsageFromMap(map[string]interface{}{
			"token_usage": map[string]interface{}{
				"last": map[string]interface{}{
					"input_tokens":      2,
					"completion_tokens": 3,
				},
			},
		}); input != 2 || output != 3 || total != 5 {
			t.Fatalf("expected token usage fallback totals, got %d %d %d", input, output, total)
		}
	})
}

func TestSessionParsingHelperBranches(t *testing.T) {
	t.Run("event from message and extract message", func(t *testing.T) {
		if evt, ok := EventFromMessage(protocol.Message{}); ok || evt.Type != "" {
			t.Fatalf("expected empty message to be ignored, got %+v ok=%v", evt, ok)
		}
		if evt, ok := EventFromMessage(protocol.Message{Method: protocol.MethodThreadTokenUsageUpdated, Params: []byte(`{"threadId":"th","turnId":"tu","tokenUsage":{"last":{"inputTokens":4,"outputTokens":5}}}`)}); !ok || evt.TotalTokens != 9 {
			t.Fatalf("expected token usage fallback to use last totals, got %+v ok=%v", evt, ok)
		}
		if evt, ok := EventFromMessage(protocol.Message{Method: protocol.MethodThreadTokenUsageUpdated, Params: []byte(`{"threadId":"th","turnId":"tu","tokenUsage":{"total":{"inputTokens":0,"outputTokens":0,"totalTokens":0},"last":{"inputTokens":1,"outputTokens":2}}}`)}); !ok || evt.TotalTokens != 3 {
			t.Fatalf("expected total-less token usage to sum last totals, got %+v ok=%v", evt, ok)
		}
		if evt, ok := EventFromMessage(protocol.Message{Method: protocol.MethodTurnStarted, Params: []byte(`{"threadId":"","turn":{"id":""}}`)}); ok || evt.ThreadID != "" || evt.TurnID != "" {
			t.Fatalf("expected empty turn payload to be ignored, got %+v ok=%v", evt, ok)
		}

		if got := extractMessage(nil); got != "" {
			t.Fatalf("extractMessage(nil) = %q", got)
		}
		if got := extractMessage(map[string]interface{}{"message": map[string]interface{}{"content": "nested"}}); got != "nested" {
			t.Fatalf("expected nested message content, got %q", got)
		}
		if got := extractMessage(map[string]interface{}{"chunk": []interface{}{"first", map[string]interface{}{"text": "second"}}}); got != "first second" {
			t.Fatalf("expected slice message concatenation, got %q", got)
		}
	})

	t.Run("time parsing", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		if got, ok := timeValue(now); !ok || !got.Equal(now) {
			t.Fatalf("timeValue(time.Time) = %v ok=%v", got, ok)
		}
		if got, ok := timeValue(now.Format(time.RFC3339)); !ok || !got.Equal(now) {
			t.Fatalf("timeValue(string) = %v ok=%v", got, ok)
		}
		if _, ok := timeValue("not-a-time"); ok {
			t.Fatal("expected invalid time string to fail")
		}
		if _, ok := timeValue(123); ok {
			t.Fatal("expected unsupported time value to fail")
		}
	})
}
