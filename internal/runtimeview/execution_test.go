package runtimeview

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type testProvider struct {
	snapshot          observability.Snapshot
	sessions          map[string]interface{}
	pendingInterrupts map[string]appserver.PendingInteraction
}

func (p testProvider) Snapshot() observability.Snapshot {
	return p.snapshot
}

func (p testProvider) LiveSessions() map[string]interface{} {
	return map[string]interface{}{"sessions": p.sessions}
}

func (p testProvider) PendingInterruptForIssue(issueID, identifier string) (*appserver.PendingInteraction, bool) {
	if interaction, ok := p.pendingInterrupts[issueID]; ok {
		cloned := interaction.Clone()
		return &cloned, true
	}
	if interaction, ok := p.pendingInterrupts[identifier]; ok {
		cloned := interaction.Clone()
		return &cloned, true
	}
	return nil, false
}

func TestIssueExecutionPayloadUsesLiveRuntimeWhenAvailable(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Live issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_failed",
		Error:      "approval_required",
		UpdatedAt:  time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-persisted-turn-persisted",
			LastEvent:       "turn.approval_required",
			LastTimestamp:   time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_failed", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
		"error":      "approval_required",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "review",
				Attempt:    4,
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: appserver.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-live-turn-live",
				ThreadID:        "thread-live",
				TurnID:          "turn-live",
			},
		},
	}, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["runtime_available"] != true || payload["active"] != true {
		t.Fatalf("unexpected runtime payload: %#v", payload)
	}
	if payload["session_source"] != "live" || payload["phase"] != "review" {
		t.Fatalf("expected live overlay, got %#v", payload)
	}
	if payload["attempt_number"].(int) != 4 {
		t.Fatalf("expected running attempt number, got %#v", payload["attempt_number"])
	}
}

func TestIssueExecutionPayloadIncludesPendingInterruptMetadata(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Waiting issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	payload, err := IssueExecutionPayload(store, testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    1,
			}},
		},
		pendingInterrupts: map[string]appserver.PendingInteraction{
			issue.ID: {
				ID:              "interrupt-1",
				Kind:            appserver.PendingInteractionKindApproval,
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				RequestedAt:     time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC),
			},
		},
	}, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	pending, ok := payload["pending_interrupt"].(*appserver.PendingInteraction)
	if !ok || pending.ID != "interrupt-1" {
		t.Fatalf("expected pending interrupt payload, got %#v", payload["pending_interrupt"])
	}
}

func TestIssueExecutionPayloadFallsBackToPersistedDataWithoutProvider(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Persisted issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    3,
		RunKind:    "run_unsuccessful",
		Error:      "turn_input_required",
		UpdatedAt:  time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-persisted-turn-persisted",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_failed", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    3,
		"error":      "turn_input_required",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["runtime_available"] != false || payload["session_source"] != "persisted" {
		t.Fatalf("unexpected persisted-only payload: %#v", payload)
	}
	if payload["active"] != false {
		t.Fatalf("expected inactive persisted-only payload: %#v", payload)
	}
	if payload["failure_class"] != "turn_input_required" {
		t.Fatalf("unexpected failure class: %#v", payload["failure_class"])
	}
	if payload["attempt_number"].(int) != 3 {
		t.Fatalf("expected persisted attempt number, got %#v", payload["attempt_number"])
	}
}

func TestIssueExecutionPayloadMarksStaleRunStartedSnapshotAsInterrupted(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Interrupted issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 12, 5, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-stale-turn-stale",
			LastEvent:       "turn.started",
			LastTimestamp:   now,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["active"] != false || payload["session_source"] != "persisted" {
		t.Fatalf("unexpected interrupted payload: %#v", payload)
	}
	if payload["failure_class"] != "run_interrupted" {
		t.Fatalf("expected run_interrupted failure class, got %#v", payload["failure_class"])
	}
}

func TestIssueExecutionPayloadClearsHistoricalFailureForActiveRecoveredRun(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Recovered issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 12, 10, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-recovered-turn-recovered",
			LastEvent:       "item.started",
			LastTimestamp:   now,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	for _, event := range []struct {
		kind    string
		attempt int
		error   string
	}{
		{kind: "run_interrupted", attempt: 1, error: "run_interrupted"},
		{kind: "retry_scheduled", attempt: 2, error: "run_interrupted"},
		{kind: "run_started", attempt: 2},
	} {
		payload := map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    event.attempt,
		}
		if event.error != "" {
			payload["error"] = event.error
		}
		if err := store.AppendRuntimeEvent(event.kind, payload); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s): %v", event.kind, err)
		}
	}

	payload, err := IssueExecutionPayload(store, testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    2,
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: appserver.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-live-turn-live",
				LastEvent:       "item.started",
				LastTimestamp:   now,
			},
		},
	}, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["active"] != true || payload["retry_state"] != "active" {
		t.Fatalf("expected active recovered payload, got %#v", payload)
	}
	if payload["failure_class"] != "" {
		t.Fatalf("expected cleared failure class for active recovered run, got %#v", payload["failure_class"])
	}
	if payload["current_error"] != "" {
		t.Fatalf("expected cleared current error for active recovered run, got %#v", payload["current_error"])
	}
}

func TestIssueExecutionPayloadIncludesAgentCommands(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Commanded issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	command, err := store.CreateIssueAgentCommand(issue.ID, "Merge the branch to master.", kanban.IssueAgentCommandPending)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand: %v", err)
	}
	if err := store.MarkIssueAgentCommandsDelivered(issue.ID, []string{command.ID}, "same_thread", "thread-live", 2); err != nil {
		t.Fatalf("MarkIssueAgentCommandsDelivered: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	commands, ok := payload["agent_commands"].([]kanban.IssueAgentCommand)
	if !ok {
		t.Fatalf("expected typed agent command list, got %#v", payload["agent_commands"])
	}
	if len(commands) != 1 {
		t.Fatalf("expected one command, got %#v", commands)
	}
	if commands[0].Status != kanban.IssueAgentCommandDelivered || commands[0].DeliveryThreadID != "thread-live" {
		t.Fatalf("unexpected command payload: %+v", commands[0])
	}
}

func TestIssueExecutionPayloadReturnsPausedRetryMetadata(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Paused issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	pausedAt := time.Date(2026, 3, 9, 12, 15, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    3,
		RunKind:    "retry_paused",
		Error:      "stall_timeout",
		UpdatedAt:  pausedAt,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-paused-turn-paused",
			LastEvent:       "item.started",
			LastTimestamp:   pausedAt,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
		"issue_id":             issue.ID,
		"identifier":           issue.Identifier,
		"phase":                "implementation",
		"attempt":              3,
		"paused_at":            pausedAt.Format(time.RFC3339),
		"error":                "stall_timeout",
		"consecutive_failures": 3,
		"pause_threshold":      3,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["retry_state"] != "paused" {
		t.Fatalf("expected paused retry state, got %#v", payload["retry_state"])
	}
	if payload["pause_reason"] != "stall_timeout" || payload["failure_class"] != "stall_timeout" {
		t.Fatalf("unexpected paused failure metadata: %#v", payload)
	}
	if payload["consecutive_failures"].(int) != 3 || payload["pause_threshold"].(int) != 3 {
		t.Fatalf("unexpected paused streak metadata: %#v", payload)
	}
	if payload["paused_at"] != pausedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected paused_at: %#v", payload["paused_at"])
	}
}

func TestIssueExecutionPayloadReturnsRetryLimitPauseReason(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Retry limited issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	pausedAt := time.Date(2026, 3, 9, 12, 20, 0, 0, time.UTC)
	if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
		"issue_id":        issue.ID,
		"identifier":      issue.Identifier,
		"issue_state":     "in_progress",
		"phase":           "implementation",
		"attempt":         4,
		"paused_at":       pausedAt.Format(time.RFC3339),
		"error":           "retry_limit_reached",
		"pause_threshold": 8,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if payload["retry_state"] != "paused" || payload["pause_reason"] != "retry_limit_reached" {
		t.Fatalf("unexpected retry limit payload: %#v", payload)
	}
	if payload["failure_class"] != "retry_limit_reached" {
		t.Fatalf("unexpected failure class: %#v", payload["failure_class"])
	}
}

func TestIssueExecutionPayloadGroupsPersistentActivityByAttempt(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Attempt timeline issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    1,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent run_started: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    1,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent run_completed: %v", err)
	}

	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:      "item.started",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "agent-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-1", "type": "agentMessage", "phase": "commentary", "text": "Plan"},
	})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:     "item.agentMessage.delta",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "agent-1",
		Delta:    "ning the fix",
	})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:      "item.completed",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "agent-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-1", "type": "agentMessage", "phase": "commentary", "text": "Planning the fix"},
	})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:     "item.started",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "cmd-1",
		ItemType: "commandExecution",
		Command:  "npm test",
		CWD:      "/repo",
		Item: map[string]interface{}{
			"id":      "cmd-1",
			"type":    "commandExecution",
			"command": "npm test",
			"cwd":     "/repo",
		},
	})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:     "item.commandExecution.outputDelta",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "cmd-1",
		Delta:    "all checks green",
	})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:             "item.completed",
		ThreadID:         "thread-1",
		TurnID:           "turn-1",
		ItemID:           "cmd-1",
		ItemType:         "commandExecution",
		Command:          "npm test",
		CWD:              "/repo",
		AggregatedOutput: "all checks green",
		ExitCode:         intPtr(0),
		Status:           "completed",
		Item: map[string]interface{}{
			"id":               "cmd-1",
			"type":             "commandExecution",
			"command":          "npm test",
			"cwd":              "/repo",
			"aggregatedOutput": "all checks green",
			"exitCode":         0,
			"status":           "completed",
		},
	})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{Type: "turn.completed", ThreadID: "thread-1", TurnID: "turn-1"})

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	groups, ok := payload["activity_groups"].([]kanban.ActivityGroup)
	if !ok {
		t.Fatalf("expected activity groups, got %#v", payload["activity_groups"])
	}
	if len(groups) != 1 {
		t.Fatalf("expected one attempt group, got %#v", groups)
	}
	if groups[0].Attempt != 1 || groups[0].Status != "completed" || groups[0].Phase != "implementation" {
		t.Fatalf("unexpected group metadata: %#v", groups[0])
	}
	if len(groups[0].Entries) != 2 {
		t.Fatalf("expected compacted successful activity to keep the latest substantive row and completion status, got %#v", groups[0].Entries)
	}
	if groups[0].Entries[0].Kind != "command" || groups[0].Entries[0].Title != "Command completed" {
		t.Fatalf("expected compacted command row to remain visible, got %#v", groups[0].Entries[0])
	}
	if groups[0].Entries[1].Kind != "status" || groups[0].Entries[1].Title != "Turn Completed" {
		t.Fatalf("expected compacted completion status row, got %#v", groups[0].Entries[1])
	}
}

func TestIssueExecutionPayloadKeepsHistoricalAttemptsVisible(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "History issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	for _, event := range []struct {
		kind    string
		attempt int
	}{
		{kind: "run_completed", attempt: 1},
		{kind: "run_started", attempt: 2},
	} {
		if err := store.AppendRuntimeEvent(event.kind, map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    event.attempt,
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s): %v", event.kind, err)
		}
	}
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:      "item.completed",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "agent-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-1", "type": "agentMessage", "phase": "commentary", "text": "First attempt"},
	})
	mustApplyActivityEvent(t, store, issue, 2, appserver.ActivityEvent{Type: "turn.started", ThreadID: "thread-2", TurnID: "turn-2"})
	mustApplyActivityEvent(t, store, issue, 2, appserver.ActivityEvent{
		Type:      "item.completed",
		ThreadID:  "thread-2",
		TurnID:    "turn-2",
		ItemID:    "agent-2",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-2", "type": "agentMessage", "phase": "commentary", "text": "Second attempt"},
	})

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	groups := payload["activity_groups"].([]kanban.ActivityGroup)
	if len(groups) != 2 {
		t.Fatalf("expected both attempts to remain visible, got %#v", groups)
	}
	if groups[0].Entries[1].Summary != "First attempt" || groups[1].Entries[1].Summary != "Second attempt" {
		t.Fatalf("unexpected grouped history: %#v", groups)
	}
}

func TestIssueExecutionPayloadUsesCompletedItemAsAuthoritativeAgentText(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Agent authoritative text", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:      "item.started",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "agent-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-1", "type": "agentMessage", "phase": "commentary", "text": "Pl"},
	})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{Type: "item.agentMessage.delta", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "agent-1", Delta: "ann"})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{Type: "item.agentMessage.delta", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "agent-1", Delta: "ing the fi"})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:      "item.completed",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "agent-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Item:      map[string]interface{}{"id": "agent-1", "type": "agentMessage", "phase": "commentary", "text": "Planning the fix"},
	})

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	groups := payload["activity_groups"].([]kanban.ActivityGroup)
	if len(groups) != 1 || len(groups[0].Entries) != 1 {
		t.Fatalf("expected a single authoritative agent row, got %#v", groups)
	}
	if groups[0].Entries[0].Summary != "Planning the fix" {
		t.Fatalf("expected completed text to replace partial stream, got %#v", groups[0].Entries[0])
	}
}

func TestIssueExecutionPayloadCollapsesCommandStreamingIntoOnePersistentRow(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Command streaming row", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:     "item.started",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "cmd-1",
		ItemType: "commandExecution",
		Command:  "npm run dev",
		CWD:      "/repo/apps/frontend",
		Item: map[string]interface{}{
			"id":      "cmd-1",
			"type":    "commandExecution",
			"command": "npm run dev",
			"cwd":     "/repo/apps/frontend",
		},
	})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{Type: "item.commandExecution.outputDelta", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "cmd-1", Delta: "ready line 1\n"})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{Type: "item.commandExecution.outputDelta", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "cmd-1", Delta: "ready line 2"})
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:             "item.completed",
		ThreadID:         "thread-1",
		TurnID:           "turn-1",
		ItemID:           "cmd-1",
		ItemType:         "commandExecution",
		Command:          "npm run dev",
		CWD:              "/repo/apps/frontend",
		AggregatedOutput: "ready line 1\nready line 2",
		ExitCode:         intPtr(1),
		Status:           "failed",
		Item: map[string]interface{}{
			"id":               "cmd-1",
			"type":             "commandExecution",
			"command":          "npm run dev",
			"cwd":              "/repo/apps/frontend",
			"aggregatedOutput": "ready line 1\nready line 2",
			"exitCode":         1,
			"status":           "failed",
		},
	})

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	groups := payload["activity_groups"].([]kanban.ActivityGroup)
	if len(groups) != 1 || len(groups[0].Entries) != 1 {
		t.Fatalf("expected a single command row, got %#v", groups)
	}
	command := groups[0].Entries[0]
	if command.Kind != "command" || command.Title != "Command failed (exit 1)" {
		t.Fatalf("expected failed command row, got %#v", command)
	}
	if !strings.Contains(command.Detail, "ready line 1") || !strings.Contains(command.Detail, "ready line 2") {
		t.Fatalf("expected accumulated output in command detail, got %#v", command)
	}
}

func TestIssueExecutionPayloadRoutesNonPrimaryItemsToDebugGroups(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Debug activity issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	mustApplyActivityEvent(t, store, issue, 1, appserver.ActivityEvent{
		Type:     "item.completed",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "plan-1",
		ItemType: "plan",
		Item:     map[string]interface{}{"id": "plan-1", "type": "plan", "text": "- inspect routes\n- patch contract"},
	})

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	if groups := payload["activity_groups"].([]kanban.ActivityGroup); len(groups) != 0 {
		t.Fatalf("expected no primary activity groups, got %#v", groups)
	}
	debugGroups := payload["debug_activity_groups"].([]kanban.ActivityGroup)
	if len(debugGroups) != 1 || len(debugGroups[0].Entries) != 1 {
		t.Fatalf("expected plan item in debug groups, got %#v", debugGroups)
	}
	if debugGroups[0].Entries[0].ItemType != "plan" {
		t.Fatalf("expected plan item type preserved, got %#v", debugGroups[0].Entries[0])
	}
}

func mustApplyActivityEvent(t *testing.T, store *kanban.Store, issue *kanban.Issue, attempt int, event appserver.ActivityEvent) {
	t.Helper()
	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, attempt, event); err != nil {
		t.Fatalf("ApplyIssueActivityEvent(%s): %v", event.Type, err)
	}
}

func intPtr(value int) *int {
	return &value
}
