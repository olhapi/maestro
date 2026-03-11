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
	snapshot observability.Snapshot
	sessions map[string]interface{}
}

func (p testProvider) Snapshot() observability.Snapshot {
	return p.snapshot
}

func (p testProvider) LiveSessions() map[string]interface{} {
	return map[string]interface{}{"sessions": p.sessions}
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

func TestIssueExecutionPayloadBuildsDisplayHistoryFromCommandDeltas(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Display history issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 12, 30, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_failed",
		Error:      "run_failed",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-display-turn-display",
			ThreadID:        "thread-display",
			TurnID:          "turn-display",
			LastEvent:       "exec_command_end",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "exec_command_begin", CallID: "cmd-1", Command: "npm run dev", CWD: "/repo/apps/frontend", Message: "npm run dev"},
				{Type: "exec_command_output_delta", CallID: "cmd-1", Stream: "stdout", Chunk: "\x1b[32mready\x1b[39m on http://127.0.0.1:3000"},
				{Type: "exec_command_output_delta", CallID: "cmd-1", Stream: "stderr", Chunk: "error when starting dev server"},
				{Type: "exec_command_end", CallID: "cmd-1", ExitCode: intPtr(1)},
				{Type: "item.completed", Message: "Execution item completed"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 1 {
		t.Fatalf("expected only the grouped command row in the focused feed, got %#v", displayHistory)
	}
	commandRow := displayHistory[0]
	if commandRow.Kind != "command" || commandRow.Title != "Command failed (exit 1)" || commandRow.Tone != "error" {
		t.Fatalf("unexpected command row: %#v", commandRow)
	}
	if commandRow.Command != "npm run dev" || commandRow.CommandState != "failed" {
		t.Fatalf("expected explicit command metadata for failed row, got %#v", commandRow)
	}
	if !commandRow.Expandable || !strings.Contains(commandRow.Detail, "$ npm run dev") {
		t.Fatalf("expected expandable command detail, got %#v", commandRow)
	}
	if strings.Contains(commandRow.Detail, "[32m") {
		t.Fatalf("expected ANSI sequences removed from detail, got %#v", commandRow.Detail)
	}
	if commandRow.TokenCount != 0 {
		t.Fatalf("expected zero token count omitted in display row struct value, got %#v", commandRow.TokenCount)
	}
}

func TestIssueExecutionPayloadRetainsTurnBoundaryAfterCommandGroup(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Command boundary issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 12, 45, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-command-boundary-turn-command-boundary",
			ThreadID:        "thread-command-boundary",
			TurnID:          "turn-command-boundary",
			LastEvent:       "turn.completed",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "exec_command_output_delta", CallID: "cmd-1", Command: "npm test", CWD: "/repo", Chunk: "tests passed"},
				{Type: "turn.completed", Message: "Turn finished cleanly"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 2 {
		t.Fatalf("expected command row plus turn boundary, got %#v", displayHistory)
	}
	if displayHistory[0].Kind != "command" {
		t.Fatalf("expected first row to remain the command summary, got %#v", displayHistory)
	}
	if displayHistory[0].Command != "npm test" || displayHistory[0].CommandState != "output" {
		t.Fatalf("expected command row metadata preserved, got %#v", displayHistory[0])
	}
	if displayHistory[1].Title != "Turn completed" || displayHistory[1].Kind != "event" {
		t.Fatalf("expected turn boundary retained after command row, got %#v", displayHistory[1])
	}
}

func TestIssueExecutionPayloadMarksCompletedCommandState(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Completed command state", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 12, 50, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-command-complete-turn-command-complete",
			ThreadID:        "thread-command-complete",
			TurnID:          "turn-command-complete",
			LastEvent:       "exec_command_end",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "exec_command_begin", CallID: "cmd-2", Command: "go test ./...", CWD: "/repo"},
				{Type: "exec_command_end", CallID: "cmd-2", ExitCode: intPtr(0)},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 1 {
		t.Fatalf("expected a single completed command row, got %#v", displayHistory)
	}
	if displayHistory[0].Command != "go test ./..." || displayHistory[0].CommandState != "completed" {
		t.Fatalf("expected completed command metadata, got %#v", displayHistory[0])
	}
}

func TestIssueExecutionPayloadBuildsUniqueIDsForSplitCommandCall(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Split command IDs", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 13, 0, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_failed",
		Error:      "run_failed",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-split-turn-split",
			ThreadID:        "thread-split",
			TurnID:          "turn-split",
			LastEvent:       "exec_command_output_delta",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "exec_command_output_delta", CallID: "cmd-1", Command: "npm run dev", CWD: "/repo/apps/frontend", Chunk: "ready line 1"},
				{Type: "thread.tokenusage.updated", Message: "token usage updated"},
				{Type: "exec_command_output_delta", CallID: "cmd-1", Command: "npm run dev", CWD: "/repo/apps/frontend", Chunk: "ready line 2"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 1 {
		t.Fatalf("expected token updates to be suppressed and command deltas merged, got %#v", displayHistory)
	}
	if displayHistory[0].Kind != "command" {
		t.Fatalf("expected a single command row, got %#v", displayHistory)
	}
	if displayHistory[0].Command != "npm run dev" || displayHistory[0].CommandState != "output" {
		t.Fatalf("expected merged command metadata, got %#v", displayHistory[0])
	}
	if !strings.Contains(displayHistory[0].Detail, "ready line 1") || !strings.Contains(displayHistory[0].Detail, "ready line 2") {
		t.Fatalf("expected merged command detail, got %#v", displayHistory[0])
	}
}

func TestIssueExecutionPayloadCollapsesAgentMessageDeltasIntoSingleRow(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Agent delta issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 13, 15, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-agent-turn-agent",
			ThreadID:        "thread-agent",
			TurnID:          "turn-agent",
			LastEvent:       "item.completed",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "item.started", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Starting"},
				{Type: "item.agentMessage.delta", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Planning"},
				{Type: "thread.tokenusage.updated", TotalTokens: 24},
				{Type: "agent_message_content_delta", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: " the fix"},
				{Type: "item.completed", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Planning the fix"},
				{Type: "turn.started", Message: "Turn began"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 2 {
		t.Fatalf("expected combined agent row plus turn boundary, got %#v", displayHistory)
	}
	agentRow := displayHistory[0]
	if agentRow.Kind != "agent" || agentRow.Title != "Agent update" || agentRow.Phase != "commentary" {
		t.Fatalf("unexpected agent row: %#v", agentRow)
	}
	if agentRow.Summary != "Planning the fix" {
		t.Fatalf("expected completed-item text to win, got %#v", agentRow)
	}
	if displayHistory[1].Title != "Turn started" {
		t.Fatalf("expected turn boundary retained, got %#v", displayHistory[1])
	}
}

func TestIssueExecutionPayloadCollapsesFragmentedAgentDeltasAcrossChangingItemIDs(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Fragmented agent delta issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 13, 22, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-fragment-turn-fragment",
			ThreadID:        "thread-fragment",
			TurnID:          "turn-fragment",
			LastEvent:       "item.agentMessage.delta",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "item.agentMessage.delta", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "the "},
				{Type: "item.agentMessage.delta", ItemID: "item-2", ItemType: "agentMessage", ItemPhase: "commentary", Message: "routes "},
				{Type: "item.agentMessage.delta", ItemID: "item-3", ItemType: "agentMessage", ItemPhase: "commentary", Message: "are "},
				{Type: "item.agentMessage.delta", ItemID: "item-4", ItemType: "agentMessage", ItemPhase: "commentary", Message: "stable"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 1 {
		t.Fatalf("expected fragmented deltas to collapse into one row, got %#v", displayHistory)
	}
	if displayHistory[0].Kind != "agent" || displayHistory[0].Phase != "commentary" {
		t.Fatalf("unexpected collapsed agent row: %#v", displayHistory[0])
	}
	if displayHistory[0].Summary != "the routes are stable" {
		t.Fatalf("expected merged commentary text, got %#v", displayHistory[0])
	}
}

func TestIssueExecutionPayloadCollapsesAgentDeltasAcrossCodexMarkerEvents(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Codex marker delta issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 11, 19, 38, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-codex-markers-turn-codex-markers",
			ThreadID:        "thread-codex-markers",
			TurnID:          "turn-codex-markers",
			LastEvent:       "item.completed",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "codex.event.agent_message_delta"},
				{Type: "item.agentMessage.delta", ItemID: "msg-1", Message: "tree"},
				{Type: "codex.event.agent_message_content_delta"},
				{Type: "item.agentMessage.delta", ItemID: "msg-1", Message: " onto"},
				{Type: "codex.event.agent_message_delta"},
				{Type: "item.agentMessage.delta", ItemID: "msg-1", Message: " `codex/PHOT-11`"},
				{Type: "codex.event.agent_message_content_delta"},
				{Type: "item.agentMessage.delta", ItemID: "msg-1", Message: " without"},
				{Type: "codex.event.agent_message_delta"},
				{Type: "item.agentMessage.delta", ItemID: "msg-1", Message: " touching"},
				{Type: "codex.event.agent_message_content_delta"},
				{Type: "item.agentMessage.delta", ItemID: "msg-1", Message: " that unrelated change."},
				{Type: "item.completed", ItemID: "msg-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "tree onto `codex/PHOT-11` without touching that unrelated change."},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 1 {
		t.Fatalf("expected codex marker events to stay inside one transcript row, got %#v", displayHistory)
	}
	if displayHistory[0].Summary != "tree onto `codex/PHOT-11` without touching that unrelated change." {
		t.Fatalf("expected merged transcript text, got %#v", displayHistory[0])
	}
}

func TestIssueExecutionPayloadKeepsAdjacentStartedOnlyAgentUpdate(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Adjacent agent updates", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 13, 30, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-adjacent-agent-turn-adjacent-agent",
			ThreadID:        "thread-adjacent-agent",
			TurnID:          "turn-adjacent-agent",
			LastEvent:       "item.started",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "item.completed", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Planning the fix"},
				{Type: "item.started", ItemID: "item-2", ItemType: "agentMessage", ItemPhase: "final_answer", Message: "Done."},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 2 {
		t.Fatalf("expected both agent updates to remain visible, got %#v", displayHistory)
	}
	if displayHistory[0].Summary != "Planning the fix" {
		t.Fatalf("expected first agent row to preserve completed text, got %#v", displayHistory[0])
	}
	secondRow := displayHistory[1]
	if secondRow.Kind != "agent" || secondRow.Title != "Final answer" || secondRow.Phase != "final_answer" {
		t.Fatalf("unexpected second agent row metadata: %#v", secondRow)
	}
	if secondRow.Summary != "Done." {
		t.Fatalf("expected started-only agent text to be used, got %#v", secondRow)
	}
}

func TestIssueExecutionPayloadKeepsAdjacentCompletedCommentaryUpdatesAsSeparateRows(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Adjacent completed commentary updates", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 13, 35, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-commentary-completed-turn-commentary-completed",
			ThreadID:        "thread-commentary-completed",
			TurnID:          "turn-commentary-completed",
			LastEvent:       "item.completed",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "item.completed", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Searching the routes"},
				{Type: "item.completed", ItemID: "item-2", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Applying the patch"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 2 {
		t.Fatalf("expected completed commentary updates to remain separate, got %#v", displayHistory)
	}
	if displayHistory[0].Summary != "Searching the routes" {
		t.Fatalf("expected first commentary row preserved, got %#v", displayHistory[0])
	}
	if displayHistory[1].Summary != "Applying the patch" {
		t.Fatalf("expected second commentary row preserved, got %#v", displayHistory[1])
	}
}

func TestIssueExecutionPayloadKeepsAdjacentCommentaryLifecycleUpdatesAsSeparateRows(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Adjacent commentary updates", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 13, 40, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-commentary-turn-commentary",
			ThreadID:        "thread-commentary",
			TurnID:          "turn-commentary",
			LastEvent:       "item.started",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "item.completed", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Searching the routes"},
				{Type: "item.started", ItemID: "item-2", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Applying the patch"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 2 {
		t.Fatalf("expected commentary lifecycle updates to remain separate, got %#v", displayHistory)
	}
	if displayHistory[0].Summary != "Searching the routes" {
		t.Fatalf("expected first commentary row preserved, got %#v", displayHistory[0])
	}
	if displayHistory[1].Summary != "Applying the patch" {
		t.Fatalf("expected second commentary row preserved, got %#v", displayHistory[1])
	}
}

func TestBuildSessionDisplayHistoryKeepsStableIDForStreamingCommentaryRow(t *testing.T) {
	initial := buildSessionDisplayHistory([]appserver.Event{
		{Type: "item.started", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Plan"},
		{Type: "item.agentMessage.delta", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "ning"},
	})
	if len(initial) != 1 {
		t.Fatalf("expected one streaming commentary row, got %#v", initial)
	}

	updated := buildSessionDisplayHistory([]appserver.Event{
		{Type: "item.started", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Plan"},
		{Type: "item.agentMessage.delta", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "ning"},
		{Type: "item.agentMessage.delta", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: " now"},
	})
	if len(updated) != 1 {
		t.Fatalf("expected updated commentary row to remain grouped, got %#v", updated)
	}
	if initial[0].ID != updated[0].ID {
		t.Fatalf("expected stable row id across streaming updates, got %q then %q", initial[0].ID, updated[0].ID)
	}
	if updated[0].Summary != "ning now" {
		t.Fatalf("expected updated commentary text, got %#v", updated[0])
	}
}

func TestBuildSessionDisplayHistoryMakesRepeatedBaseIDsUnique(t *testing.T) {
	displayHistory := buildSessionDisplayHistory([]appserver.Event{
		{Type: "item.completed", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Searching the routes"},
		{Type: "turn.started", Message: "next turn"},
		{Type: "item.completed", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Applying the patch"},
	})
	if len(displayHistory) != 3 {
		t.Fatalf("expected commentary, turn boundary, commentary rows, got %#v", displayHistory)
	}
	if displayHistory[0].ID != "session-agent-item-1" {
		t.Fatalf("expected first stable id, got %#v", displayHistory[0])
	}
	if displayHistory[2].ID != "session-agent-item-1-2" {
		t.Fatalf("expected duplicate base id to gain deterministic suffix, got %#v", displayHistory[2])
	}
}

func TestIssueExecutionPayloadKeepsCommandBoundaryBetweenCommentaryUpdates(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Commentary command boundary", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	now := time.Date(2026, 3, 9, 13, 45, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-command-boundary-turn-command-boundary",
			ThreadID:        "thread-command-boundary",
			TurnID:          "turn-command-boundary",
			LastEvent:       "item.started",
			LastTimestamp:   now,
			History: []appserver.Event{
				{Type: "item.completed", ItemID: "item-1", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Searching the routes"},
				{Type: "exec_command_output_delta", CallID: "call-1", Stream: "stdout", Chunk: "go test ./...", Message: "go test ./..."},
				{Type: "item.started", ItemID: "item-2", ItemType: "agentMessage", ItemPhase: "commentary", Message: "Applying the patch"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	payload, err := IssueExecutionPayload(store, nil, issue)
	if err != nil {
		t.Fatalf("IssueExecutionPayload: %v", err)
	}

	displayHistory, ok := payload["session_display_history"].([]SessionDisplayHistoryEntry)
	if !ok {
		t.Fatalf("expected typed display history, got %#v", payload["session_display_history"])
	}
	if len(displayHistory) != 3 {
		t.Fatalf("expected command output to break commentary groups, got %#v", displayHistory)
	}
	if displayHistory[0].Summary != "Searching the routes" {
		t.Fatalf("expected first commentary row preserved, got %#v", displayHistory[0])
	}
	if displayHistory[1].Kind != "command" {
		t.Fatalf("expected middle row to remain a command event, got %#v", displayHistory[1])
	}
	if displayHistory[2].Summary != "Applying the patch" {
		t.Fatalf("expected second commentary row preserved, got %#v", displayHistory[2])
	}
}

func intPtr(value int) *int {
	return &value
}
