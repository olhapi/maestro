package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/agentruntime"
)

type coverageStringer string

func (s coverageStringer) String() string {
	return string(s)
}

func TestClaudeHelperConversions(t *testing.T) {
	t.Run("primitive conversions", func(t *testing.T) {
		if got := asString("hello"); got != "hello" {
			t.Fatalf("asString string: got %q", got)
		}
		if got := asString(coverageStringer("world")); got != "world" {
			t.Fatalf("asString stringer: got %q", got)
		}
		if got := asString(123); got != "" {
			t.Fatalf("asString fallback: got %q", got)
		}

		if got, ok := boolFromAny(true); !ok || !got {
			t.Fatalf("boolFromAny true: got %v, %v", got, ok)
		}
		if got, ok := boolFromAny(false); !ok || got {
			t.Fatalf("boolFromAny false: got %v, %v", got, ok)
		}
		if _, ok := boolFromAny("not-bool"); ok {
			t.Fatal("boolFromAny should reject non-bools")
		}

		cases := []struct {
			name  string
			value interface{}
			want  int
			ok    bool
		}{
			{name: "int", value: int(1), want: 1, ok: true},
			{name: "int8", value: int8(2), want: 2, ok: true},
			{name: "int16", value: int16(3), want: 3, ok: true},
			{name: "int32", value: int32(4), want: 4, ok: true},
			{name: "int64", value: int64(5), want: 5, ok: true},
			{name: "float32", value: float32(6.9), want: 6, ok: true},
			{name: "float64", value: float64(7.9), want: 7, ok: true},
			{name: "other", value: "nope", want: 0, ok: false},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				got, ok := intFromAny(tc.value)
				if got != tc.want || ok != tc.ok {
					t.Fatalf("intFromAny(%T): got (%d,%v) want (%d,%v)", tc.value, got, ok, tc.want, tc.ok)
				}
			})
		}
	})

	t.Run("map and message helpers", func(t *testing.T) {
		rawMap := map[string]interface{}{"value": "x"}
		if got := mapValue(rawMap); got["value"] != "x" {
			t.Fatalf("mapValue map: got %#v", got)
		}
		if got := mapValue("bad"); got != nil {
			t.Fatalf("mapValue fallback: got %#v", got)
		}

		nested := map[string]interface{}{
			"outer": map[string]interface{}{
				"inner": "value",
			},
		}
		if got := stringFromMap(nested, "outer", "inner"); got != "value" {
			t.Fatalf("stringFromMap nested: got %q", got)
		}
		if got := stringFromMap(nested, "outer", "missing"); got != "" {
			t.Fatalf("stringFromMap missing: got %q", got)
		}

		if got := assistantMessageText(map[string]interface{}{"text": " direct "}); got != "direct" {
			t.Fatalf("assistantMessageText direct: got %q", got)
		}
		if got := assistantMessageText(map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"text": "Hello "},
				map[string]interface{}{"text": "World"},
			},
		}); got != "HelloWorld" {
			t.Fatalf("assistantMessageText content: got %q", got)
		}
		if got := assistantMessagePhase(map[string]interface{}{"phase": "analysis"}); got != "analysis" {
			t.Fatalf("assistantMessagePhase: got %q", got)
		}
		if got := assistantMessagePhase(nil); got != "" {
			t.Fatalf("assistantMessagePhase nil: got %q", got)
		}

		if got := blockPhase(map[string]interface{}{"type": "thinking"}); got != "thinking" {
			t.Fatalf("blockPhase thinking: got %q", got)
		}
		if got := blockPhase(map[string]interface{}{"type": "text"}); got != "commentary" {
			t.Fatalf("blockPhase text: got %q", got)
		}
		if got := blockPhase(map[string]interface{}{"type": "tool_use", "phase": "planning"}); got != "planning" {
			t.Fatalf("blockPhase explicit phase: got %q", got)
		}
		if got := blockPhase(map[string]interface{}{"type": "tool_use"}); got != "commentary" {
			t.Fatalf("blockPhase fallback: got %q", got)
		}

		if got := deltaText(map[string]interface{}{"delta": map[string]interface{}{"text": "alpha"}}); got != "alpha" {
			t.Fatalf("deltaText text: got %q", got)
		}
		if got := deltaText(map[string]interface{}{"delta": map[string]interface{}{"thinking": "beta"}}); got != "beta" {
			t.Fatalf("deltaText thinking: got %q", got)
		}
		if got := deltaText(map[string]interface{}{"delta": map[string]interface{}{"partial_text": "gamma"}}); got != "gamma" {
			t.Fatalf("deltaText partial_text: got %q", got)
		}
		if got := deltaText(map[string]interface{}{"text": "delta"}); got != "delta" {
			t.Fatalf("deltaText fallback: got %q", got)
		}
		if got := deltaText(nil); got != "" {
			t.Fatalf("deltaText nil: got %q", got)
		}
	})

	t.Run("token and fallback helpers", func(t *testing.T) {
		input, output, total := usageTokens(map[string]interface{}{
			"input_tokens":  float64(1),
			"output_tokens": int32(2),
			"total_tokens":  int64(3),
		})
		if input != 1 || output != 2 || total != 3 {
			t.Fatalf("usageTokens: got (%d,%d,%d)", input, output, total)
		}
		if input, output, total := usageTokens(nil); input != 0 || output != 0 || total != 0 {
			t.Fatalf("usageTokens nil: got (%d,%d,%d)", input, output, total)
		}

		client := &stdioClient{}
		if got := fallbackClaudeTurnID(client, nil); got != "turn-1" {
			t.Fatalf("fallbackClaudeTurnID initial: got %q", got)
		}
		client.counter = 4
		if got := fallbackClaudeTurnID(client, nil); got != "turn-4" {
			t.Fatalf("fallbackClaudeTurnID counter: got %q", got)
		}
		if got := fallbackClaudeTurnID(client, &claudeTurnState{resultUUID: "uuid-1"}); got != "uuid-1" {
			t.Fatalf("fallbackClaudeTurnID result uuid: got %q", got)
		}

		if got := claudePermissionMode(agentruntime.PermissionConfig{CollaborationMode: " PLAN "}); got != "plan" {
			t.Fatalf("claudePermissionMode plan: got %q", got)
		}
		if got := claudePermissionMode(agentruntime.PermissionConfig{}); got != "default" {
			t.Fatalf("claudePermissionMode default: got %q", got)
		}
	})
}

func TestClaudeMetadataAndCommandHelpers(t *testing.T) {
	t.Run("session metadata", func(t *testing.T) {
		client := newClaudeCoverageClient()
		client.recordClaudeSessionIDLocked("  session-1  ")

		snapshot := client.Session()
		if snapshot.ThreadID != "session-1" || snapshot.SessionID != "session-1" {
			t.Fatalf("recordClaudeSessionIDLocked: got %+v", snapshot)
		}
		if snapshot.Metadata["session_identifier_strategy"] != claudeSessionIdentifierStrategy {
			t.Fatalf("recordClaudeSessionIDLocked metadata: got %+v", snapshot.Metadata)
		}
		if snapshot.Metadata["provider_session_id"] != "session-1" {
			t.Fatalf("recordClaudeSessionIDLocked provider session id: got %+v", snapshot.Metadata)
		}

		client.recordClaudeSessionIDLocked("   ")
		snapshot = client.Session()
		if snapshot.ThreadID != "session-1" || snapshot.SessionID != "session-1" {
			t.Fatalf("recordClaudeSessionIDLocked blank should be ignored, got %+v", snapshot)
		}

		client.session.Metadata = nil
		client.syncClaudeMetadataLocked("session-2")
		if client.session.Metadata["session_identifier_strategy"] != claudeSessionIdentifierStrategy {
			t.Fatalf("syncClaudeMetadataLocked strategy: got %+v", client.session.Metadata)
		}
		if client.session.Metadata["provider_session_id"] != "session-2" {
			t.Fatalf("syncClaudeMetadataLocked provider session id: got %+v", client.session.Metadata)
		}

		client.session.Metadata = nil
		client.session.ThreadID = "session-3"
		client.normalizeClaudeSessionIdentityLocked()
		if client.session.Metadata["provider"] != string(agentruntime.ProviderClaude) {
			t.Fatalf("normalizeClaudeSessionIdentityLocked provider: got %+v", client.session.Metadata)
		}
		if client.session.Metadata["transport"] != string(agentruntime.TransportStdio) {
			t.Fatalf("normalizeClaudeSessionIdentityLocked transport: got %+v", client.session.Metadata)
		}
		if client.session.Metadata["provider_session_id"] != "session-3" {
			t.Fatalf("normalizeClaudeSessionIdentityLocked provider session id: got %+v", client.session.Metadata)
		}
		if client.session.SessionID != "session-3" {
			t.Fatalf("normalizeClaudeSessionIdentityLocked session id: got %+v", client.session)
		}
	})

	t.Run("command assembly", func(t *testing.T) {
		defaultCommand, err := composeClaudeCommand(agentruntime.RuntimeSpec{}, "", filepath.Join("tmp", "mcp.json"))
		if err != nil {
			t.Fatalf("composeClaudeCommand default: %v", err)
		}
		if !strings.HasPrefix(defaultCommand, "claude ") {
			t.Fatalf("composeClaudeCommand default should use claude, got %q", defaultCommand)
		}

		resumeCommand, err := composeClaudeCommand(agentruntime.RuntimeSpec{
			Command: "custom claude",
			Permissions: agentruntime.PermissionConfig{
				CollaborationMode: "plan",
			},
		}, "resume token", filepath.Join("tmp", "with space", "mcp.json"))
		if err != nil {
			t.Fatalf("composeClaudeCommand resume: %v", err)
		}
		for _, want := range []string{"custom claude '-p'", "'-r' 'resume token'", "'--permission-mode' 'plan'"} {
			if !strings.Contains(resumeCommand, want) {
				t.Fatalf("composeClaudeCommand resume missing %q in %q", want, resumeCommand)
			}
		}

		if _, err := composeClaudeCommand(agentruntime.RuntimeSpec{}, "", ""); err == nil {
			t.Fatal("composeClaudeCommand should require an MCP config path")
		}
	})

	t.Run("config writer", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "maestro.db")
		configPath, cleanup, err := writeClaudeMCPConfig(dbPath)
		if err != nil {
			t.Fatalf("writeClaudeMCPConfig: %v", err)
		}

		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal config: %v", err)
		}
		servers, ok := raw["mcpServers"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected mcpServers map, got %#v", raw["mcpServers"])
		}
		maestro, ok := servers["maestro"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected maestro server map, got %#v", servers["maestro"])
		}
		if maestro["type"] != "stdio" || maestro["command"] != "maestro" {
			t.Fatalf("unexpected maestro config: %#v", maestro)
		}
		args, ok := maestro["args"].([]interface{})
		if !ok || len(args) != 3 {
			t.Fatalf("unexpected maestro args: %#v", maestro["args"])
		}
		if args[0] != "mcp" || args[1] != "--db" || args[2] != dbPath {
			t.Fatalf("unexpected maestro args: %#v", args)
		}

		cleanup()
		if _, err := os.Stat(configPath); !os.IsNotExist(err) {
			t.Fatalf("expected cleanup to remove config, got %v", err)
		}

		t.Run("tempdir failure", func(t *testing.T) {
			t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
			if _, _, err := writeClaudeMCPConfig(dbPath); err == nil {
				t.Fatal("expected writeClaudeMCPConfig to fail when the temp directory is unavailable")
			}
		})
	})

	t.Run("output helpers", func(t *testing.T) {
		cases := []struct {
			name   string
			stdout string
			stderr string
			want   string
		}{
			{name: "stdout only", stdout: " stdout ", want: "stdout"},
			{name: "stderr only", stderr: " stderr ", want: "stderr"},
			{name: "both", stdout: " stdout ", stderr: " stderr ", want: "stdout\nstderr"},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				if got := combineOutput(tc.stdout, tc.stderr); got != tc.want {
					t.Fatalf("combineOutput: got %q want %q", got, tc.want)
				}
			})
		}

		if got := runtimeMetadata("  session-9  "); got["provider"] != string(agentruntime.ProviderClaude) || got["transport"] != string(agentruntime.TransportStdio) || got["provider_session_id"] != "session-9" {
			t.Fatalf("runtimeMetadata with session id: got %+v", got)
		}
		if got := runtimeMetadata(""); got["provider"] != string(agentruntime.ProviderClaude) || got["transport"] != string(agentruntime.TransportStdio) {
			t.Fatalf("runtimeMetadata without session id: got %+v", got)
		} else if _, ok := got["provider_session_id"]; ok {
			t.Fatalf("runtimeMetadata without session id should omit provider_session_id: got %+v", got)
		}
	})
}

func TestClaudeTurnFinalizationBranches(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		client := newClaudeCoverageClient()
		state := &claudeTurnState{
			turnID:       "turn-1",
			resultText:   "final answer",
			resultStop:   "end_turn",
			inputTokens:  1,
			outputTokens: 2,
			totalTokens:  3,
		}

		output, terminalType, err := client.finishTurnLocked(state, "", "", nil, nil)
		if err != nil {
			t.Fatalf("finishTurnLocked completed: %v", err)
		}
		if output != "final answer" || terminalType != "turn.completed" {
			t.Fatalf("finishTurnLocked completed: got (%q,%q)", output, terminalType)
		}

		if got := client.Output(); got != "final answer" {
			t.Fatalf("finishTurnLocked completed output: got %q", got)
		}

		session := client.Session()
		if session.LastEvent != "turn.completed" || session.TurnsCompleted != 1 || !session.Terminal {
			t.Fatalf("finishTurnLocked completed session: %+v", session)
		}
		if session.InputTokens != 1 || session.OutputTokens != 2 || session.TotalTokens != 3 {
			t.Fatalf("finishTurnLocked completed tokens: %+v", session)
		}
		if session.Metadata["provider"] != string(agentruntime.ProviderClaude) || session.Metadata["transport"] != string(agentruntime.TransportStdio) {
			t.Fatalf("finishTurnLocked completed metadata: %+v", session.Metadata)
		}
	})

	t.Run("cancelled by context", func(t *testing.T) {
		client := newClaudeCoverageClient()
		state := &claudeTurnState{turnID: "turn-2"}

		_, terminalType, err := client.finishTurnLocked(state, "", "", nil, context.Canceled)
		if !errors.Is(err, context.Canceled) || terminalType != "turn.cancelled" {
			t.Fatalf("finishTurnLocked cancelled: got (%v,%q)", err, terminalType)
		}
	})

	t.Run("cancelled by interruption", func(t *testing.T) {
		client := newClaudeCoverageClient()
		client.activeTurn = &runningTurn{interrupted: true}
		state := &claudeTurnState{turnID: "turn-3"}

		_, terminalType, err := client.finishTurnLocked(state, "", "", nil, nil)
		if !errors.Is(err, context.Canceled) || terminalType != "turn.cancelled" {
			t.Fatalf("finishTurnLocked interrupted: got (%v,%q)", err, terminalType)
		}
	})

	t.Run("result error", func(t *testing.T) {
		client := newClaudeCoverageClient()
		state := &claudeTurnState{
			turnID:        "turn-4",
			resultText:    "bad answer",
			resultIsError: true,
		}

		_, terminalType, err := client.finishTurnLocked(state, "", "", nil, nil)
		if terminalType != "turn.failed" {
			t.Fatalf("finishTurnLocked result error terminal type: got %q", terminalType)
		}
		if err == nil || !strings.Contains(err.Error(), "claude reported an error: bad answer") {
			t.Fatalf("finishTurnLocked result error: got %v", err)
		}
	})

	t.Run("wait error without output", func(t *testing.T) {
		client := newClaudeCoverageClient()
		state := &claudeTurnState{turnID: "turn-5"}
		waitErr := errors.New("exit 7")

		_, terminalType, err := client.finishTurnLocked(state, "", "", waitErr, nil)
		if terminalType != "turn.failed" || !errors.Is(err, waitErr) {
			t.Fatalf("finishTurnLocked wait error: got (%v,%q)", err, terminalType)
		}
	})
}

func TestClaudeHandleClaudeLineBranches(t *testing.T) {
	t.Run("ignored shapes", func(t *testing.T) {
		client := newClaudeCoverageClient()
		client.handleClaudeLine([]byte("not-json"), &claudeTurnState{}, nil)
		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{"type": "assistant"}), &claudeTurnState{}, nil)
		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{
			"event": map[string]interface{}{"type": "content_block_start"},
		}), &claudeTurnState{}, nil)
		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{
			"type":    "system",
			"subtype": "init",
		}), &claudeTurnState{}, nil)
	})

	t.Run("assistant text updates session identity", func(t *testing.T) {
		client := newClaudeCoverageClient()
		state := &claudeTurnState{}
		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{
			"session_id": "session-1",
			"type":       "assistant",
			"message": map[string]interface{}{
				"id":    "turn-1",
				"phase": "analysis",
				"content": []interface{}{
					map[string]interface{}{"text": "Hello "},
					map[string]interface{}{"text": "world"},
				},
			},
		}), state, nil)

		if !state.turnStarted || state.turnID != "turn-1" || state.lastAssistant != "Helloworld" || state.itemPhase != "analysis" {
			t.Fatalf("assistant line state: %+v", state)
		}

		session := client.Session()
		if session.ThreadID != "session-1" || session.SessionID != "session-1" {
			t.Fatalf("assistant line session: %+v", session)
		}
		if session.Metadata["provider_session_id"] != "session-1" {
			t.Fatalf("assistant line metadata: %+v", session.Metadata)
		}
	})

	t.Run("message start and content blocks", func(t *testing.T) {
		client := newClaudeCoverageClient()

		state := &claudeTurnState{}
		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{
			"event": map[string]interface{}{
				"type": "message_start",
				"message": map[string]interface{}{
					"id":    "message-1",
					"phase": "planning",
				},
			},
		}), state, nil)
		if !state.turnStarted || state.turnID != "message-1" || state.itemPhase != "planning" {
			t.Fatalf("message_start state: %+v", state)
		}

		state = &claudeTurnState{}
		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{
			"event": map[string]interface{}{
				"type": "content_block_start",
				"contentBlock": map[string]interface{}{
					"id":   "block-1",
					"type": "thinking",
				},
			},
		}), state, nil)
		if !state.turnStarted || state.turnID != "block-1" || state.itemPhase != "thinking" {
			t.Fatalf("contentBlock alias state: %+v", state)
		}

		state = &claudeTurnState{}
		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{
			"event": map[string]interface{}{
				"type": "content_block_start",
				"content_block": map[string]interface{}{
					"id":    "block-2",
					"type":  "tool_use",
					"phase": "analysis",
				},
			},
		}), state, nil)
		if !state.turnStarted || state.turnID != "block-2" || state.itemPhase != "analysis" {
			t.Fatalf("content_block state: %+v", state)
		}
	})

	t.Run("deltas and result", func(t *testing.T) {
		client := newClaudeCoverageClient()

		state := &claudeTurnState{}
		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{
			"event": map[string]interface{}{
				"type":  "content_block_delta",
				"delta": map[string]interface{}{"thinking": "thinking text"},
			},
		}), state, nil)
		if !state.turnStarted || state.turnID != "turn-1" || state.streamedOutput.String() != "thinking text" {
			t.Fatalf("content_block_delta state: %+v", state)
		}

		state = &claudeTurnState{}
		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{
			"event": map[string]interface{}{
				"type":  "message_delta",
				"delta": map[string]interface{}{"stop_reason": "end_turn"},
				"usage": map[string]interface{}{
					"input_tokens":  float64(7),
					"output_tokens": float64(8),
					"total_tokens":  float64(15),
				},
			},
		}), state, nil)
		if state.resultStop != "end_turn" || state.inputTokens != 7 || state.outputTokens != 8 || state.totalTokens != 15 {
			t.Fatalf("message_delta state: %+v", state)
		}

		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{
			"event": map[string]interface{}{"type": "message_stop"},
		}), &claudeTurnState{}, nil)

		state = &claudeTurnState{}
		client.handleClaudeLine(mustMarshalJSON(t, map[string]interface{}{
			"type":        "result",
			"result":      "final answer",
			"stop_reason": "stop",
			"uuid":        "result-1",
			"is_error":    true,
			"subtype":     "error",
			"usage": map[string]interface{}{
				"input_tokens":  1,
				"output_tokens": 2,
				"total_tokens":  3,
			},
		}), state, nil)
		if !state.turnStarted || !state.resultSeen || !state.resultIsError {
			t.Fatalf("result state flags: %+v", state)
		}
		if state.turnID != "result-1" || state.resultUUID != "result-1" || state.resultText != "final answer" || state.resultStop != "stop" {
			t.Fatalf("result state values: %+v", state)
		}
		if state.inputTokens != 1 || state.outputTokens != 2 || state.totalTokens != 3 {
			t.Fatalf("result tokens: %+v", state)
		}
	})
}

func TestClaudeLowCoverageHelpers(t *testing.T) {
	t.Run("nil receivers and runtime controls", func(t *testing.T) {
		var nilClient *stdioClient
		if nilClient.Output() != "" {
			t.Fatal("expected nil Output receiver to return an empty string")
		}
		if nilClient.Session() != nil {
			t.Fatal("expected nil Session receiver to return nil")
		}
		if err := nilClient.Close(); err != nil {
			t.Fatalf("nil Close receiver: %v", err)
		}

		client := newClaudeCoverageClient()
		permissions := agentruntime.PermissionConfig{
			ApprovalPolicy: map[string]interface{}{"mode": "never"},
			ThreadSandbox:  "workspace-write",
			Metadata: map[string]interface{}{
				"source": "test",
			},
		}
		client.UpdatePermissions(permissions)
		if client.spec.Permissions.ThreadSandbox != permissions.ThreadSandbox {
			t.Fatalf("UpdatePermissions thread sandbox: %+v", client.spec.Permissions)
		}
		if client.spec.Permissions.Metadata["source"] != "test" {
			t.Fatalf("UpdatePermissions metadata: %+v", client.spec.Permissions.Metadata)
		}
		if err := client.RespondToInteraction(context.Background(), "interaction-1", agentruntime.PendingInteractionResponse{}); !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
			t.Fatalf("RespondToInteraction: %v", err)
		}
	})

	t.Run("text and map helpers", func(t *testing.T) {
		if got, err := textInput([]agentruntime.InputItem{
			{Kind: agentruntime.InputItemText, Text: "first"},
			{Kind: agentruntime.InputItemText, Text: "second"},
		}); err != nil || got != "first\n\nsecond" {
			t.Fatalf("textInput join: got (%q,%v)", got, err)
		}
		if _, err := textInput([]agentruntime.InputItem{{Kind: agentruntime.InputItemLocalImage, Path: "image.png"}}); !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
			t.Fatalf("textInput local image: %v", err)
		}
		if _, err := textInput([]agentruntime.InputItem{{Kind: agentruntime.InputItemKind("mystery"), Text: "ignored"}}); !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
			t.Fatalf("textInput unknown kind: %v", err)
		}

		if got := stringFromMap(nil, "outer"); got != "" {
			t.Fatalf("stringFromMap nil root: got %q", got)
		}
		if got := stringFromMap(map[string]interface{}{"outer": "value"}, "outer", "inner"); got != "" {
			t.Fatalf("stringFromMap non-map intermediate: got %q", got)
		}
		if got := assistantMessageText(nil); got != "" {
			t.Fatalf("assistantMessageText nil: got %q", got)
		}
		if got := assistantMessageText(map[string]interface{}{
			"content": []interface{}{
				"skip",
				map[string]interface{}{"text": "A"},
			},
		}); got != "A" {
			t.Fatalf("assistantMessageText mixed content: got %q", got)
		}
		if got := blockPhase(nil); got != "" {
			t.Fatalf("blockPhase nil: got %q", got)
		}
	})

	t.Run("build command and start stdio", func(t *testing.T) {
		command, cleanup, err := buildClaudeCommand(agentruntime.RuntimeSpec{
			Command: "custom claude",
			DBPath:  filepath.Join(t.TempDir(), "maestro.db"),
			Permissions: agentruntime.PermissionConfig{
				CollaborationMode: "plan",
			},
		})
		if err != nil {
			t.Fatalf("buildClaudeCommand: %v", err)
		}
		if cleanup == nil {
			t.Fatal("expected buildClaudeCommand cleanup")
		}
		t.Cleanup(cleanup)
		if !strings.Contains(command, "custom claude '-p'") || !strings.Contains(command, "'--permission-mode' 'plan'") {
			t.Fatalf("buildClaudeCommand command: %q", command)
		}

		if _, _, err := buildClaudeCommand(agentruntime.RuntimeSpec{}); err == nil {
			t.Fatal("expected buildClaudeCommand to require a DB path")
		}

		client, err := Start(context.Background(), agentruntime.RuntimeSpec{
			Provider:        agentruntime.ProviderClaude,
			Transport:       agentruntime.TransportStdio,
			Command:         writeFakeClaudeCLI(t),
			Workspace:       t.TempDir(),
			IssueID:         "iss-start",
			IssueIdentifier: "ISS-START",
			ResumeToken:     "resume-1",
			DBPath:          filepath.Join(t.TempDir(), "maestro.db"),
		}, agentruntime.Observers{})
		if err != nil {
			t.Fatalf("Start stdio: %v", err)
		}
		t.Cleanup(func() { _ = client.Close() })

		session := client.Session()
		if session == nil || session.ThreadID != "resume-1" || session.SessionID != "resume-1" {
			t.Fatalf("Start stdio session: %+v", session)
		}
		if session.Metadata["provider_session_id"] != "resume-1" {
			t.Fatalf("Start stdio metadata: %+v", session.Metadata)
		}
	})
}

func newClaudeCoverageClient() *stdioClient {
	return &stdioClient{
		spec: agentruntime.RuntimeSpec{
			IssueID:         "iss-1",
			IssueIdentifier: "ISS-1",
			Transport:       agentruntime.TransportStdio,
			Permissions: agentruntime.PermissionConfig{
				CollaborationMode: "plan",
			},
		},
	}
}

func mustMarshalJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}
