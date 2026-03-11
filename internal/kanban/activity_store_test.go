package kanban

import (
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/appserver"
)

func countIssueActivityUpdates(t *testing.T, store *Store, issueID string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM issue_activity_updates WHERE issue_id = ?`, issueID).Scan(&count); err != nil {
		t.Fatalf("count issue activity updates: %v", err)
	}
	return count
}

func TestApplyIssueActivityEventPersistsSingleAgentEntryAcrossStreaming(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Persistent agent timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	for _, event := range []appserver.ActivityEvent{
		{
			Type:      "item.started",
			ThreadID:  "thread-1",
			TurnID:    "turn-1",
			ItemID:    "msg-1",
			ItemType:  "agentMessage",
			ItemPhase: "commentary",
			Item: map[string]interface{}{
				"id":    "msg-1",
				"type":  "agentMessage",
				"phase": "commentary",
				"text":  "Initial streamed summary",
			},
		},
		{
			Type:     "item.agentMessage.delta",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			ItemID:   "msg-1",
			Delta:    " with delta",
		},
		{
			Type:      "item.completed",
			ThreadID:  "thread-1",
			TurnID:    "turn-1",
			ItemID:    "msg-1",
			ItemType:  "agentMessage",
			ItemPhase: "commentary",
			Item: map[string]interface{}{
				"id":    "msg-1",
				"type":  "agentMessage",
				"phase": "commentary",
				"text":  "Authoritative completed update",
			},
		},
	} {
		if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 2, event); err != nil {
			t.Fatalf("ApplyIssueActivityEvent(%s): %v", event.Type, err)
		}
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one logical agent entry, got %#v", entries)
	}
	entry := entries[0]
	if entry.Attempt != 2 || entry.Kind != "agent" || entry.Tier != "primary" || entry.ItemType != "agentMessage" {
		t.Fatalf("unexpected persisted agent entry: %#v", entry)
	}
	if entry.Status != "completed" || entry.Summary != "Authoritative completed update" {
		t.Fatalf("expected authoritative completed agent text, got %#v", entry)
	}
	if entry.StartedAt == nil || entry.CompletedAt == nil {
		t.Fatalf("expected started/completed timestamps, got %#v", entry)
	}
	if count := countIssueActivityUpdates(t, store, issue.ID); count != 3 {
		t.Fatalf("expected 3 append-only activity updates, got %d", count)
	}
}

func TestApplyIssueActivityEventPersistsSingleCommandEntryAcrossStreaming(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Persistent command timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	for _, event := range []appserver.ActivityEvent{
		{
			Type:     "item.started",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			ItemID:   "cmd-1",
			ItemType: "commandExecution",
			Command:  "pnpm test",
			CWD:      "/repo",
			Item: map[string]interface{}{
				"id":      "cmd-1",
				"type":    "commandExecution",
				"command": "pnpm test",
				"cwd":     "/repo",
			},
		},
		{
			Type:     "item.commandExecution.outputDelta",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			ItemID:   "cmd-1",
			Delta:    "streamed output\n",
		},
		{
			Type:      "item.commandExecution.terminalInteraction",
			ThreadID:  "thread-1",
			TurnID:    "turn-1",
			ItemID:    "cmd-1",
			ProcessID: "proc-1",
			Stdin:     "y\n",
		},
		{
			Type:     "item.commandExecution.outputDelta",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			ItemID:   "cmd-1",
			Delta:    "more streamed output\n",
		},
		{
			Type:             "item.completed",
			ThreadID:         "thread-1",
			TurnID:           "turn-1",
			ItemID:           "cmd-1",
			ItemType:         "commandExecution",
			Command:          "pnpm test",
			CWD:              "/repo",
			Status:           "completed",
			AggregatedOutput: "authoritative output\nall tests passed",
			ExitCode:         intPtr(0),
			Item: map[string]interface{}{
				"id":               "cmd-1",
				"type":             "commandExecution",
				"command":          "pnpm test",
				"cwd":              "/repo",
				"status":           "completed",
				"aggregatedOutput": "authoritative output\nall tests passed",
				"exitCode":         0,
			},
		},
	} {
		if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 3, event); err != nil {
			t.Fatalf("ApplyIssueActivityEvent(%s): %v", event.Type, err)
		}
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one logical command entry, got %#v", entries)
	}
	entry := entries[0]
	if entry.Attempt != 3 || entry.Kind != "command" || entry.Tier != "primary" || entry.ItemType != "commandExecution" {
		t.Fatalf("unexpected persisted command entry: %#v", entry)
	}
	if entry.Status != "completed" || entry.Title != "Command completed" || entry.Tone != "success" {
		t.Fatalf("expected completed command status, got %#v", entry)
	}
	if entry.Summary != "pnpm test" {
		t.Fatalf("expected command summary to stay stable, got %#v", entry)
	}
	if !strings.Contains(entry.Detail, "$ pnpm test") || !strings.Contains(entry.Detail, "cwd: /repo") || !strings.Contains(entry.Detail, "authoritative output") {
		t.Fatalf("expected completed command detail, got %#v", entry.Detail)
	}
	if strings.Contains(entry.Detail, "> y") {
		t.Fatalf("expected completed command payload to replace transient terminal input, got %#v", entry.Detail)
	}
	if entry.StartedAt == nil || entry.CompletedAt == nil {
		t.Fatalf("expected started/completed timestamps, got %#v", entry)
	}
	if count := countIssueActivityUpdates(t, store, issue.ID); count != 5 {
		t.Fatalf("expected 5 append-only activity updates, got %d", count)
	}
}

func TestApplyIssueActivityEventKeepsHistoricalAttemptsAndSecondaryRows(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Historical activity timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	for _, event := range []struct {
		attempt int
		event   appserver.ActivityEvent
	}{
		{
			attempt: 1,
			event: appserver.ActivityEvent{
				Type:      "item.completed",
				ThreadID:  "thread-1",
				TurnID:    "turn-1",
				ItemID:    "msg-1",
				ItemType:  "agentMessage",
				ItemPhase: "commentary",
				Item: map[string]interface{}{
					"id":    "msg-1",
					"type":  "agentMessage",
					"phase": "commentary",
					"text":  "Attempt one summary",
				},
			},
		},
		{
			attempt: 2,
			event: appserver.ActivityEvent{
				Type:      "item.completed",
				ThreadID:  "thread-2",
				TurnID:    "turn-2",
				ItemID:    "plan-1",
				ItemType:  "plan",
				ItemPhase: "planning",
				Item: map[string]interface{}{
					"id":   "plan-1",
					"type": "plan",
					"text": "1. Rebuild timeline\n2. Persist rows",
				},
			},
		},
		{
			attempt: 2,
			event: appserver.ActivityEvent{
				Type:     "turn.completed",
				ThreadID: "thread-2",
				TurnID:   "turn-2",
			},
		},
	} {
		if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, event.attempt, event.event); err != nil {
			t.Fatalf("ApplyIssueActivityEvent(%s): %v", event.event.Type, err)
		}
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected three retained activity entries, got %#v", entries)
	}
	if entries[0].Attempt != 1 || entries[0].Summary != "Attempt one summary" {
		t.Fatalf("expected attempt one history to stay visible, got %#v", entries[0])
	}
	if entries[1].Attempt != 2 || entries[1].Tier != "secondary" || entries[1].ItemType != "plan" {
		t.Fatalf("expected plan item to stay secondary in attempt two, got %#v", entries[1])
	}
	if entries[2].Attempt != 2 || entries[2].Kind != "status" || entries[2].Status != "completed" {
		t.Fatalf("expected turn status row in attempt two, got %#v", entries[2])
	}
}

func intPtr(value int) *int {
	return &value
}
