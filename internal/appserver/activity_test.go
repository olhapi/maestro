package appserver

import (
	"encoding/json"
	"testing"

	"github.com/olhapi/maestro/internal/appserver/protocol"
)

func mustDecodeActivityMessage(t *testing.T, payload map[string]interface{}) protocol.Message {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	msg, ok := protocol.DecodeMessage(string(body))
	if !ok {
		t.Fatalf("DecodeMessage failed for %s", string(body))
	}
	return msg
}

func TestActivityEventFromMessageParsesTurnAndTokenUsageEvents(t *testing.T) {
	for _, tc := range []struct {
		name       string
		method     string
		params     map[string]interface{}
		wantType   string
		wantThread string
		wantTurn   string
	}{
		{
			name:   "started nested",
			method: protocol.MethodTurnStarted,
			params: map[string]interface{}{
				"threadId": "thread-1",
				"turn": map[string]interface{}{
					"id":     "turn-1",
					"status": "inProgress",
					"items":  []interface{}{},
				},
			},
			wantType:   "turn.started",
			wantThread: "thread-1",
			wantTurn:   "turn-1",
		},
		{
			name:   "started flat",
			method: protocol.MethodTurnStarted,
			params: map[string]interface{}{
				"threadId": "thread-1",
				"turnId":   "turn-1",
			},
			wantType:   "turn.started",
			wantThread: "thread-1",
			wantTurn:   "turn-1",
		},
		{
			name:   "completed nested",
			method: protocol.MethodTurnCompleted,
			params: map[string]interface{}{
				"threadId": "thread-2",
				"turn": map[string]interface{}{
					"id":     "turn-2",
					"status": "completed",
					"items":  []interface{}{},
				},
			},
			wantType:   "turn.completed",
			wantThread: "thread-2",
			wantTurn:   "turn-2",
		},
		{
			name:   "completed flat",
			method: protocol.MethodTurnCompleted,
			params: map[string]interface{}{
				"threadId": "thread-2",
				"turnId":   "turn-2",
			},
			wantType:   "turn.completed",
			wantThread: "thread-2",
			wantTurn:   "turn-2",
		},
		{
			name:   "failed nested",
			method: protocol.MethodTurnFailed,
			params: map[string]interface{}{
				"threadId": "thread-3",
				"turn": map[string]interface{}{
					"id":     "turn-3",
					"status": "failed",
				},
			},
			wantType:   "turn.failed",
			wantThread: "thread-3",
			wantTurn:   "turn-3",
		},
		{
			name:   "failed flat",
			method: protocol.MethodTurnFailed,
			params: map[string]interface{}{
				"threadId": "thread-3",
				"turnId":   "turn-3",
			},
			wantType:   "turn.failed",
			wantThread: "thread-3",
			wantTurn:   "turn-3",
		},
		{
			name:   "cancelled nested",
			method: protocol.MethodTurnCancelled,
			params: map[string]interface{}{
				"threadId": "thread-4",
				"turn": map[string]interface{}{
					"id":     "turn-4",
					"status": "cancelled",
				},
			},
			wantType:   "turn.cancelled",
			wantThread: "thread-4",
			wantTurn:   "turn-4",
		},
		{
			name:   "cancelled flat",
			method: protocol.MethodTurnCancelled,
			params: map[string]interface{}{
				"threadId": "thread-4",
				"turnId":   "turn-4",
			},
			wantType:   "turn.cancelled",
			wantThread: "thread-4",
			wantTurn:   "turn-4",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, ok := ActivityEventFromMessage(mustDecodeActivityMessage(t, map[string]interface{}{
				"method": tc.method,
				"params": tc.params,
			}))
			if !ok {
				t.Fatalf("expected %s activity event", tc.method)
			}
			if event.Type != tc.wantType || event.ThreadID != tc.wantThread || event.TurnID != tc.wantTurn {
				t.Fatalf("unexpected turn lifecycle event: %#v", event)
			}
		})
	}

	tokenUsage, ok := ActivityEventFromMessage(mustDecodeActivityMessage(t, map[string]interface{}{
		"method": protocol.MethodThreadTokenUsageUpdated,
		"params": map[string]interface{}{
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"tokenUsage": map[string]interface{}{
				"total": map[string]interface{}{
					"inputTokens":  7,
					"outputTokens": 5,
					"totalTokens":  12,
				},
			},
		},
	}))
	if !ok {
		t.Fatal("expected token usage activity event")
	}
	if tokenUsage.Type != "thread.tokenUsage.updated" || tokenUsage.ThreadID != "thread-1" || tokenUsage.TurnID != "turn-1" {
		t.Fatalf("unexpected token usage event metadata: %#v", tokenUsage)
	}
	if tokenUsage.InputTokens != 7 || tokenUsage.OutputTokens != 5 || tokenUsage.TotalTokens != 12 {
		t.Fatalf("unexpected token usage totals: %#v", tokenUsage)
	}
}

func TestActivityEventFromMessageParsesItemLifecycleEvents(t *testing.T) {
	started, ok := ActivityEventFromMessage(mustDecodeActivityMessage(t, map[string]interface{}{
		"method": "item/started",
		"params": map[string]interface{}{
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"itemId":   "msg-1",
			"item": map[string]interface{}{
				"id":    "msg-1",
				"type":  "agentMessage",
				"phase": "commentary",
				"text":  "Planning next step",
			},
		},
	}))
	if !ok {
		t.Fatal("expected item/started activity event")
	}
	if started.Type != "item.started" || started.ThreadID != "thread-1" || started.TurnID != "turn-1" {
		t.Fatalf("unexpected started event metadata: %#v", started)
	}
	if started.ItemID != "msg-1" || started.ItemType != "agentMessage" || started.ItemPhase != "commentary" {
		t.Fatalf("unexpected started item fields: %#v", started)
	}
	if started.Item["text"] != "Planning next step" {
		t.Fatalf("expected started item payload, got %#v", started.Item)
	}

	completed, ok := ActivityEventFromMessage(mustDecodeActivityMessage(t, map[string]interface{}{
		"method": "item/completed",
		"params": map[string]interface{}{
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"itemId":   "cmd-1",
			"item": map[string]interface{}{
				"id":               "cmd-1",
				"type":             "commandExecution",
				"command":          "pnpm test",
				"cwd":              "/repo",
				"status":           "completed",
				"aggregatedOutput": "all tests passed",
				"exitCode":         0,
			},
		},
	}))
	if !ok {
		t.Fatal("expected item/completed activity event")
	}
	if completed.ItemID != "cmd-1" || completed.ItemType != "commandExecution" {
		t.Fatalf("unexpected completed item identifiers: %#v", completed)
	}
	if completed.Command != "pnpm test" || completed.CWD != "/repo" || completed.AggregatedOutput != "all tests passed" || completed.Status != "completed" {
		t.Fatalf("unexpected completed command fields: %#v", completed)
	}
	if completed.ExitCode == nil || *completed.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %#v", completed.ExitCode)
	}
}

func TestActivityEventFromMessageParsesStreamingDeltas(t *testing.T) {
	for _, tc := range []struct {
		name     string
		method   string
		itemID   string
		itemType string
		delta    string
	}{
		{
			name:     "agent delta",
			method:   "item/agentMessage/delta",
			itemID:   "msg-1",
			itemType: "agentMessage",
			delta:    "working...",
		},
		{
			name:     "plan delta",
			method:   "item/plan/delta",
			itemID:   "plan-1",
			itemType: "plan",
			delta:    "1. Inspect parser",
		},
		{
			name:     "command output delta",
			method:   "item/commandExecution/outputDelta",
			itemID:   "cmd-1",
			itemType: "commandExecution",
			delta:    "line 1\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, ok := ActivityEventFromMessage(mustDecodeActivityMessage(t, map[string]interface{}{
				"method": tc.method,
				"params": map[string]interface{}{
					"threadId": "thread-1",
					"turnId":   "turn-1",
					"itemId":   tc.itemID,
					"delta":    tc.delta,
				},
			}))
			if !ok {
				t.Fatalf("expected %s activity event", tc.method)
			}
			if event.Type != normalizeEventType(tc.method) || event.ItemID != tc.itemID || event.ItemType != tc.itemType || event.Delta != tc.delta {
				t.Fatalf("unexpected delta event: %#v", event)
			}
		})
	}

	legacyCommandOutput, ok := ActivityEventFromMessage(mustDecodeActivityMessage(t, map[string]interface{}{
		"method": "item/commandExecution/outputDelta",
		"params": map[string]interface{}{
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"callId":   "call-7",
			"chunk":    "line 2\n",
			"command":  "pnpm test",
			"cwd":      "/repo",
		},
	}))
	if !ok {
		t.Fatal("expected legacy command output delta activity event")
	}
	if legacyCommandOutput.ItemID != "call-7" || legacyCommandOutput.Delta != "line 2\n" {
		t.Fatalf("unexpected legacy command output identifiers: %#v", legacyCommandOutput)
	}
	if legacyCommandOutput.CallID != "call-7" {
		t.Fatalf("expected legacy command output call id, got %#v", legacyCommandOutput)
	}
	if legacyCommandOutput.Command != "pnpm test" || legacyCommandOutput.CWD != "/repo" {
		t.Fatalf("expected legacy command metadata to be preserved: %#v", legacyCommandOutput)
	}

	terminalInteraction, ok := ActivityEventFromMessage(mustDecodeActivityMessage(t, map[string]interface{}{
		"method": "item/commandExecution/terminalInteraction",
		"params": map[string]interface{}{
			"threadId":  "thread-1",
			"turnId":    "turn-1",
			"itemId":    "cmd-1",
			"processId": "proc-1",
			"stdin":     "y\n",
		},
	}))
	if !ok {
		t.Fatal("expected terminal interaction activity event")
	}
	if terminalInteraction.ItemType != "commandExecution" || terminalInteraction.ProcessID != "proc-1" || terminalInteraction.Stdin != "y\n" {
		t.Fatalf("unexpected terminal interaction event: %#v", terminalInteraction)
	}
}

func TestActivityEventFromMessageParsesApprovalAndInputRequests(t *testing.T) {
	approval, ok := ActivityEventFromMessage(mustDecodeActivityMessage(t, map[string]interface{}{
		"id":     99,
		"method": protocol.MethodItemCommandExecutionApproval,
		"params": map[string]interface{}{
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"itemId":   "cmd-1",
			"callId":   "call-9",
			"command":  "pnpm lint",
			"cwd":      "/repo",
			"reason":   "Needs approval",
		},
	}))
	if !ok {
		t.Fatal("expected approval activity event")
	}
	if approval.Type != "item.commandExecution.requestApproval" || approval.RequestID != "99" {
		t.Fatalf("unexpected approval identity: %#v", approval)
	}
	if approval.ItemID != "cmd-1" || approval.Command != "pnpm lint" || approval.CWD != "/repo" || approval.Reason != "Needs approval" {
		t.Fatalf("unexpected approval payload: %#v", approval)
	}
	if approval.CallID != "call-9" {
		t.Fatalf("expected approval call id, got %#v", approval)
	}

	inputRequest, ok := ActivityEventFromMessage(mustDecodeActivityMessage(t, map[string]interface{}{
		"id":     "req-7",
		"method": protocol.MethodToolRequestUserInput,
		"params": map[string]interface{}{
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"questions": []map[string]interface{}{
				{"question": "Which environment should I use?"},
			},
		},
	}))
	if !ok {
		t.Fatal("expected input request activity event")
	}
	if inputRequest.Type != "item.tool.requestUserInput" || inputRequest.RequestID != "req-7" {
		t.Fatalf("unexpected input request identity: %#v", inputRequest)
	}
	if inputRequest.ThreadID != "thread-1" || inputRequest.TurnID != "turn-1" {
		t.Fatalf("unexpected input request routing metadata: %#v", inputRequest)
	}
	if inputRequest.Raw == nil {
		t.Fatalf("expected raw input request payload to be retained: %#v", inputRequest)
	}
}

func TestActivityEventFromMessagePreservesApprovalCallIDs(t *testing.T) {
	tests := []struct {
		name   string
		method string
		params map[string]interface{}
	}{
		{
			name:   "exec command",
			method: protocol.MethodExecCommandApproval,
			params: map[string]interface{}{
				"threadId": "thread-9",
				"turnId":   "turn-9",
				"callId":   "call-9",
				"cwd":      "/repo",
				"reason":   "Needs approval",
			},
		},
		{
			name:   "apply patch",
			method: protocol.MethodApplyPatchApproval,
			params: map[string]interface{}{
				"threadId":  "thread-10",
				"turnId":    "turn-10",
				"callId":    "call-10",
				"grantRoot": "/repo",
				"reason":    "Needs approval",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			callID, _ := tc.params["callId"].(string)
			event, ok := ActivityEventFromMessage(mustDecodeActivityMessage(t, map[string]interface{}{
				"id":     tc.name,
				"method": tc.method,
				"params": tc.params,
			}))
			if !ok {
				t.Fatalf("expected %s activity event", tc.method)
			}
			if event.CallID != callID {
				t.Fatalf("expected call id to survive, got %#v", event)
			}
			if event.ItemID != "" {
				t.Fatalf("expected approval item id to remain empty, got %#v", event)
			}
			if event.ThreadID == "" || event.TurnID == "" {
				t.Fatalf("expected routing metadata to be preserved, got %#v", event)
			}
		})
	}
}
