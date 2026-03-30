package kanban

import (
	"strings"
	"testing"
	"time"

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
	if count := countIssueActivityUpdates(t, store, issue.ID); count != 2 {
		t.Fatalf("expected started/completed activity updates only, got %d", count)
	}
}

func TestApplyIssueActivityEventPreservesFinalAnswerTextWithoutTruncation(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Final answer timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	longFinalAnswer := strings.TrimSpace(strings.Repeat("Final answer stays intact. ", 120))
	if len(longFinalAnswer) <= activitySummaryMaxBytes || len(longFinalAnswer) <= activityPayloadValueMaxBytes {
		t.Fatalf("test fixture must exceed truncation thresholds, got %d bytes", len(longFinalAnswer))
	}

	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, appserver.ActivityEvent{
		Type:      "item.completed",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "msg-1",
		ItemType:  "agentMessage",
		ItemPhase: "final_answer",
		Item: map[string]interface{}{
			"id":    "msg-1",
			"type":  "agentMessage",
			"phase": "final_answer",
			"text":  longFinalAnswer,
		},
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent: %v", err)
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one final-answer entry, got %#v", entries)
	}
	entry := entries[0]
	if entry.Kind != "agent" || entry.Phase != "final_answer" || entry.Title != "Final answer" || entry.Tone != "success" {
		t.Fatalf("unexpected final-answer entry metadata: %#v", entry)
	}
	if entry.Summary != longFinalAnswer {
		t.Fatalf("expected final answer summary to stay intact, got %q", entry.Summary)
	}
	if len(entry.Summary) <= activitySummaryMaxBytes {
		t.Fatalf("expected untruncated final answer summary, got %d bytes", len(entry.Summary))
	}
	item, ok := entry.RawPayload["item"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected final answer item payload, got %#v", entry.RawPayload)
	}
	text, ok := item["text"].(string)
	if !ok {
		t.Fatalf("expected final answer text payload, got %#v", item)
	}
	if text != longFinalAnswer {
		t.Fatalf("expected final answer raw payload to stay intact, got %q", text)
	}
	if len(text) <= activityPayloadValueMaxBytes {
		t.Fatalf("expected untruncated raw final answer payload, got %d bytes", len(text))
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
	if count := countIssueActivityUpdates(t, store, issue.ID); count != 2 {
		t.Fatalf("expected started/completed command updates only, got %d", count)
	}
}

func TestApplyIssueActivityEventTruncatesOversizedCommandDetail(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Oversized command timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	output := "prefix-marker\n" + strings.Repeat("x", activityDetailMaxBytes*2) + "\nlatest failure details"
	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, appserver.ActivityEvent{
		Type:             "item.completed",
		ThreadID:         "thread-1",
		TurnID:           "turn-1",
		ItemID:           "cmd-1",
		ItemType:         "commandExecution",
		Command:          "pnpm test",
		CWD:              "/repo",
		Status:           "completed",
		AggregatedOutput: output,
		ExitCode:         intPtr(0),
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent: %v", err)
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %#v", entries)
	}
	if !strings.Contains(entries[0].Detail, "[truncated]") {
		t.Fatalf("expected truncation marker, got %#v", entries[0].Detail)
	}
	if !strings.Contains(entries[0].Detail, "$ pnpm test") || !strings.Contains(entries[0].Detail, "cwd: /repo") {
		t.Fatalf("expected command metadata to remain visible, got %#v", entries[0].Detail)
	}
	if !strings.Contains(entries[0].Detail, "latest failure details") {
		t.Fatalf("expected newest command output to survive, got %#v", entries[0].Detail)
	}
	if strings.Contains(entries[0].Detail, "prefix-marker") {
		t.Fatalf("expected oldest command output to be trimmed, got %#v", entries[0].Detail)
	}
	if len(entries[0].Detail) > activityDetailMaxBytes {
		t.Fatalf("expected bounded detail size, got %d", len(entries[0].Detail))
	}
}

func TestApplyIssueActivityEventKeepsNewestStreamingCommandOutputWhenTruncated(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Streaming command truncation", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	events := []appserver.ActivityEvent{
		{
			Type:     "item.started",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			ItemID:   "cmd-1",
			ItemType: "commandExecution",
			Command:  "pnpm test",
			CWD:      "/repo",
		},
		{
			Type:     "item.commandExecution.outputDelta",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			ItemID:   "cmd-1",
			Delta:    "prefix-marker\n" + strings.Repeat("x", activityDetailMaxBytes),
		},
		{
			Type:     "item.commandExecution.outputDelta",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			ItemID:   "cmd-1",
			Delta:    "\nlatest streaming line",
		},
	}
	for _, event := range events {
		if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, event); err != nil {
			t.Fatalf("ApplyIssueActivityEvent(%s): %v", event.Type, err)
		}
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %#v", entries)
	}
	if !strings.Contains(entries[0].Detail, "latest streaming line") {
		t.Fatalf("expected latest delta to survive truncation, got %#v", entries[0].Detail)
	}
	if strings.Contains(entries[0].Detail, "prefix-marker") {
		t.Fatalf("expected oldest streaming output to be trimmed, got %#v", entries[0].Detail)
	}
}

func TestTruncateActivityTailPreservesUtf8AndTinyBudgets(t *testing.T) {
	got := truncateActivityTail("\n\n"+strings.Repeat("é", 10), len("[truncated]\n")+4)
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if !strings.HasPrefix(got, "...[truncated]") {
		t.Fatalf("expected marker prefix, got %q", got)
	}

	got = truncateActivityTail(strings.Repeat("é", 6), 5)
	if got == "" {
		t.Fatal("expected non-empty tiny-budget truncation")
	}
}

func TestTruncateCommandDetailFallsBackWhenMetadataConsumesBudget(t *testing.T) {
	detail := buildCommandDetail("pnpm test", "/very/long/path/that/leaves/no-room", "older output\nlatest output", intPtr(1))
	got := truncateCommandDetail(detail, len("$ pnpm test\n\nexit code: 1"))
	if len(got) == 0 {
		t.Fatal("expected truncated detail")
	}
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestActivityProjectionHelperEdgeCases(t *testing.T) {
	if got := secondaryItemSummary("plan", nil); got != "Plan updated." {
		t.Fatalf("secondaryItemSummary(plan nil) = %q", got)
	}
	if got := secondaryItemSummary("reasoning", map[string]interface{}{}); got != "Reasoning updated." {
		t.Fatalf("secondaryItemSummary(reasoning empty) = %q", got)
	}
	if got := secondaryItemSummary("fileChange", map[string]interface{}{"changes": "not-a-slice"}); got != "File change ready." {
		t.Fatalf("secondaryItemSummary(fileChange fallback) = %q", got)
	}
	if got := secondaryItemSummary("mcpToolCall", map[string]interface{}{}); got != "McpToolCall" {
		t.Fatalf("secondaryItemSummary(tool fallback) = %q", got)
	}
	if got := secondaryItemSummary("enteredReviewMode", map[string]interface{}{}); got != "EnteredReviewMode" {
		t.Fatalf("secondaryItemSummary(review fallback) = %q", got)
	}
	if got := secondaryItemSummary("imageGeneration", map[string]interface{}{}); got != "Image generated." {
		t.Fatalf("secondaryItemSummary(imageGeneration fallback) = %q", got)
	}
	if got := secondaryItemSummary("custom_event", nil); got != "Custom Event" {
		t.Fatalf("secondaryItemSummary(default) = %q", got)
	}

	if got := secondaryItemDetail(map[string]interface{}{"bad": make(chan int)}); got != "" {
		t.Fatalf("secondaryItemDetail marshal failure = %q", got)
	}
	if got := approvalDetail(map[string]interface{}{}); got != "" {
		t.Fatalf("approvalDetail missing params = %q", got)
	}
	if got := approvalDetail(map[string]interface{}{"params": map[string]interface{}{"bad": make(chan int)}}); got != "" {
		t.Fatalf("approvalDetail marshal failure = %q", got)
	}
	if got := inputRequestSummary(map[string]interface{}{}); got != "The agent requested user input." {
		t.Fatalf("inputRequestSummary missing params = %q", got)
	}
	if got := inputRequestSummary(map[string]interface{}{"params": map[string]interface{}{"questions": []interface{}{map[string]interface{}{"question": "  "}}}}); got != "The agent requested user input." {
		t.Fatalf("inputRequestSummary blank question = %q", got)
	}
	if got := inputRequestSummary(map[string]interface{}{"params": map[string]interface{}{"questions": []interface{}{map[string]interface{}{"question": "Which environment?"}}}}); got != "Which environment?" {
		t.Fatalf("inputRequestSummary question = %q", got)
	}
	if got := inputRequestDetail(map[string]interface{}{}); got != "" {
		t.Fatalf("inputRequestDetail missing params = %q", got)
	}
	if got := inputRequestDetail(map[string]interface{}{"params": map[string]interface{}{"bad": make(chan int)}}); got != "" {
		t.Fatalf("inputRequestDetail marshal failure = %q", got)
	}
	if got := planApprovalDetail(map[string]interface{}{"bad": make(chan int)}); got != "" {
		t.Fatalf("planApprovalDetail marshal failure = %q", got)
	}
	if got := approvalResponseDetail(map[string]interface{}{"bad": make(chan int)}); got != "" {
		t.Fatalf("approvalResponseDetail marshal failure = %q", got)
	}
	if got := inputResponseSummary(map[string]interface{}{}); got != "Operator submitted input." {
		t.Fatalf("inputResponseSummary empty = %q", got)
	}
	if got := inputResponseSummary(map[string]interface{}{"answers": map[string]interface{}{"path": []string{"  chosen  "}}}); got != "chosen" {
		t.Fatalf("inputResponseSummary []string = %q", got)
	}
	if got := inputResponseSummary(map[string]interface{}{"answers": map[string]interface{}{"path": []interface{}{"  chosen  "}}}); got != "chosen" {
		t.Fatalf("inputResponseSummary []interface{} = %q", got)
	}
	if got := inputResponseDetail(map[string]interface{}{"bad": make(chan int)}); got != "" {
		t.Fatalf("inputResponseDetail marshal failure = %q", got)
	}
	if got := firstMeaningfulLine("\n\n   \nfirst\nsecond"); got != "first" {
		t.Fatalf("firstMeaningfulLine = %q", got)
	}
	if got := firstMeaningfulLine("   \n\t"); got != "" {
		t.Fatalf("firstMeaningfulLine empty = %q", got)
	}
	if got := humanizeActivityLabel(""); got != "Activity" {
		t.Fatalf("humanizeActivityLabel empty = %q", got)
	}
	if got := humanizeActivityLabel("turn.completed/status_update"); got != "Turn Completed Status Update" {
		t.Fatalf("humanizeActivityLabel separators = %q", got)
	}
}

func TestCompactIssueActivityAttemptHelpers(t *testing.T) {
	store := setupTestStore(t)
	successIssue, err := store.CreateIssue("", "", "Compaction success", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue success: %v", err)
	}
	for _, event := range []appserver.ActivityEvent{
		{Type: "item.completed", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "cmd-1", ItemType: "commandExecution", Command: "pnpm test", CWD: "/repo", Status: "completed", AggregatedOutput: "ok", ExitCode: intPtr(0)},
		{Type: "turn.completed", ThreadID: "thread-1", TurnID: "turn-1"},
	} {
		if err := store.ApplyIssueActivityEvent(successIssue.ID, successIssue.Identifier, 1, event); err != nil {
			t.Fatalf("ApplyIssueActivityEvent success(%s): %v", event.Type, err)
		}
	}
	if err := store.CompactIssueActivityAttemptSuccess(successIssue.ID, 1); err != nil {
		t.Fatalf("CompactIssueActivityAttemptSuccess: %v", err)
	}

	diagnosticIssue, err := store.CreateIssue("", "", "Compaction diagnostic", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue diagnostic: %v", err)
	}
	for i := 0; i < activityDiagnosticTailLimit+5; i++ {
		if err := store.ApplyIssueActivityEvent(diagnosticIssue.ID, diagnosticIssue.Identifier, 1, appserver.ActivityEvent{
			Type:             "item.completed",
			ThreadID:         "thread-2",
			TurnID:           "turn-2",
			ItemID:           "cmd-" + strings.Repeat("x", i%2) + string(rune('a'+(i%26))),
			ItemType:         "commandExecution",
			Command:          "echo line",
			CWD:              "/repo",
			Status:           "completed",
			AggregatedOutput: strings.Repeat("line\n", i+1),
			ExitCode:         intPtr(1),
		}); err != nil {
			t.Fatalf("ApplyIssueActivityEvent diagnostic(%d): %v", i, err)
		}
	}
	if err := store.CompactIssueActivityAttemptDiagnostic(diagnosticIssue.ID, 1); err != nil {
		t.Fatalf("CompactIssueActivityAttemptDiagnostic: %v", err)
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
	if len(entries) != 2 {
		t.Fatalf("expected compacted successful attempt plus historical attempt, got %#v", entries)
	}
	if entries[0].Attempt != 1 || entries[0].Summary != "Attempt one summary" {
		t.Fatalf("expected attempt one history to stay visible, got %#v", entries[0])
	}
	if entries[1].Attempt != 2 || entries[1].Kind != "status" || entries[1].Status != "completed" {
		t.Fatalf("expected compacted turn status row in attempt two, got %#v", entries[1])
	}
}

func TestApplyIssueActivityEventPersistsApprovalAndInputStatusRows(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Approval and input timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	approvalRaw := map[string]interface{}{
		"params": map[string]interface{}{
			"command": "pnpm lint",
			"cwd":     "/repo",
			"reason":  "Needs approval",
		},
	}
	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 4, appserver.ActivityEvent{
		Type:      "item.commandExecution.requestApproval",
		RequestID: "req-1",
		ThreadID:  "thread-4",
		TurnID:    "turn-4",
		ItemID:    "cmd-approval",
		Command:   "pnpm lint",
		CWD:       "/repo",
		Reason:    "Needs approval",
		Raw:       approvalRaw,
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent approval: %v", err)
	}

	inputRaw := map[string]interface{}{
		"params": map[string]interface{}{
			"questions": []interface{}{
				map[string]interface{}{"question": "Which environment should I use?"},
			},
		},
	}
	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 4, appserver.ActivityEvent{
		Type:      "item.tool.requestUserInput",
		RequestID: "req-2",
		ThreadID:  "thread-4",
		TurnID:    "turn-4",
		Raw:       inputRaw,
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent input request: %v", err)
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected two status rows, got %#v", entries)
	}
	if entries[0].Status != "approval_required" || entries[0].Tone != "error" || !strings.Contains(entries[0].Detail, "\"command\": \"pnpm lint\"") {
		t.Fatalf("unexpected approval entry: %#v", entries[0])
	}
	if entries[1].Status != "input_required" || entries[1].Summary != "Which environment should I use?" || !strings.Contains(entries[1].Detail, "\"questions\"") {
		t.Fatalf("unexpected input request entry: %#v", entries[1])
	}
}

func TestApplyIssueActivityEventProjectsStructuredApprovalResolutionAsSuccess(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Structured approval timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 4, appserver.ActivityEvent{
		Type:      "item.commandExecution.requestApproval",
		RequestID: "req-structured",
		ThreadID:  "thread-4",
		TurnID:    "turn-4",
		Command:   "curl https://api.github.com",
		Raw: map[string]interface{}{
			"params": map[string]interface{}{
				"command": "curl https://api.github.com",
			},
		},
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent approval request: %v", err)
	}

	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 4, appserver.ActivityEvent{
		Type:      "item.commandExecution.approvalResolved",
		RequestID: "req-structured",
		ThreadID:  "thread-4",
		TurnID:    "turn-4",
		Status:    "accept_with_execpolicy_amendment",
		Raw: map[string]interface{}{
			"decision":       "accept_with_execpolicy_amendment",
			"decision_label": "Approve and store rule",
		},
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent approval resolved: %v", err)
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one merged approval row, got %#v", entries)
	}
	if entries[0].Status != "accept_with_execpolicy_amendment" || entries[0].Tone != "success" {
		t.Fatalf("expected structured approval to resolve as success, got %#v", entries[0])
	}
	if entries[0].Summary != "Operator approved the request and stored the matching exec rule." {
		t.Fatalf("unexpected structured approval summary: %#v", entries[0])
	}
}

func TestApplyIssueActivityEventProjectsPlanApprovalRows(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Plan approval timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 7, appserver.ActivityEvent{
		Type:     "plan.approvalRequested",
		ThreadID: "thread-plan",
		TurnID:   "turn-plan",
		Raw: map[string]interface{}{
			"markdown": "Review the plan before execution.",
		},
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent plan approval request: %v", err)
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries requested: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one plan approval request row, got %#v", entries)
	}
	if entries[0].Status != "plan_approval_pending" || entries[0].Tone != "default" {
		t.Fatalf("unexpected plan approval request entry: %#v", entries[0])
	}
	if entries[0].Summary != "Review the plan before execution." || !strings.Contains(entries[0].Detail, "\"markdown\": \"Review the plan before execution.\"") {
		t.Fatalf("unexpected plan approval request content: %#v", entries[0])
	}

	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 7, appserver.ActivityEvent{
		Type:     "plan.approved",
		ThreadID: "thread-plan",
		TurnID:   "turn-plan",
		Raw: map[string]interface{}{
			"markdown": "Review the plan before execution.",
			"decision": "approved",
		},
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent plan approval resolved: %v", err)
	}

	entries, err = store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries resolved: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected separate plan publication and approval rows, got %#v", entries)
	}
	if entries[0].Kind != "plan_version_published" || entries[0].Status != "plan_approval_pending" {
		t.Fatalf("unexpected plan version entry: %#v", entries[0])
	}
	if entries[1].Kind != "plan_approved" || entries[1].Status != "plan_approved" || entries[1].Tone != "success" {
		t.Fatalf("unexpected plan approval resolved entry: %#v", entries[1])
	}
	if entries[1].Summary != "Operator approved the plan and resumed execution." {
		t.Fatalf("unexpected plan approval resolved summary: %#v", entries[1])
	}
}

func TestApplyIssueActivityEventProjectsPlanRevisionAppliedRow(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Plan revision applied timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	requestedAt := time.Date(2026, 3, 18, 9, 35, 0, 0, time.UTC)
	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 7, appserver.ActivityEvent{
		Type: "plan.revisionApplied",
		Raw: map[string]interface{}{
			"session_id":    "plan-session-1",
			"requested_at":  requestedAt.Format(time.RFC3339),
			"revision_note": "Tighten the rollout and keep the rollback explicit.",
		},
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent plan revision applied: %v", err)
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one plan revision applied row, got %#v", entries)
	}
	if entries[0].Kind != "plan_revision_applied" || entries[0].Status != "drafting" {
		t.Fatalf("unexpected plan revision applied entry: %#v", entries[0])
	}
	if entries[0].Summary != "Tighten the rollout and keep the rollback explicit." {
		t.Fatalf("unexpected plan revision applied summary: %#v", entries[0])
	}
	if entries[0].CompletedAt == nil {
		t.Fatalf("expected plan revision applied row to resolve immediately, got %#v", entries[0])
	}
	if !strings.Contains(entries[0].Detail, "\"requested_at\": \""+requestedAt.Format(time.RFC3339)+"\"") {
		t.Fatalf("expected detail to retain revision request context, got %#v", entries[0])
	}
}

func TestApplyIssueActivityEventProjectsSecondaryAndFailureEntries(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Secondary activity timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	for _, event := range []appserver.ActivityEvent{
		{
			Type:     "item.plan.delta",
			ThreadID: "thread-5",
			TurnID:   "turn-5",
			ItemID:   "plan-1",
			Delta:    "1. Parse docs\n",
		},
		{
			Type:      "item.completed",
			ThreadID:  "thread-5",
			TurnID:    "turn-5",
			ItemID:    "plan-1",
			ItemType:  "plan",
			ItemPhase: "planning",
			Item: map[string]interface{}{
				"id":   "plan-1",
				"type": "plan",
				"text": "1. Parse docs\n2. Persist rows",
			},
		},
		{
			Type:      "item.completed",
			ThreadID:  "thread-5",
			TurnID:    "turn-5",
			ItemID:    "reason-1",
			ItemType:  "reasoning",
			ItemPhase: "analysis",
			Item: map[string]interface{}{
				"id":      "reason-1",
				"type":    "reasoning",
				"summary": []interface{}{"Need stable ids", "Need authoritative completed items"},
			},
		},
		{
			Type:     "turn.failed",
			ThreadID: "thread-5",
			TurnID:   "turn-5",
		},
	} {
		if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 5, event); err != nil {
			t.Fatalf("ApplyIssueActivityEvent(%s): %v", event.Type, err)
		}
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected three persisted entries, got %#v", entries)
	}
	if entries[0].Tier != "secondary" || entries[0].ItemType != "plan" || !strings.Contains(entries[0].Summary, "Parse docs") {
		t.Fatalf("unexpected plan entry: %#v", entries[0])
	}
	if entries[1].Tier != "secondary" || entries[1].ItemType != "reasoning" || !strings.Contains(entries[1].Summary, "Need stable ids") {
		t.Fatalf("unexpected reasoning entry: %#v", entries[1])
	}
	if entries[2].Kind != "status" || entries[2].Status != "failed" || entries[2].Tone != "error" || entries[2].Summary != "Turn execution failed." {
		t.Fatalf("unexpected failed turn entry: %#v", entries[2])
	}
}

func intPtr(value int) *int {
	return &value
}
