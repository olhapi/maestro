package runtimeview

import (
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

func TestFindEntryHelpers(t *testing.T) {
	running := []observability.RunningEntry{{
		IssueID:    "issue-1",
		Identifier: "ISS-1",
	}, {
		IssueID:    "issue-2",
		Identifier: "ISS-2",
	}}
	retry := []observability.RetryEntry{{
		IssueID:    "issue-1",
		Identifier: "ISS-1",
		Attempt:    2,
	}}
	paused := []observability.PausedEntry{{
		IssueID:    "issue-1",
		Identifier: "ISS-1",
		Attempt:    3,
	}}

	if got := findRunningEntry(running, "issue-2", "missing"); got == nil || got.Identifier != "ISS-2" {
		t.Fatalf("expected running entry lookup by issue id, got %+v", got)
	}
	if got := findRetryEntry(retry, "missing", "ISS-1"); got == nil || got.Attempt != 2 {
		t.Fatalf("expected retry entry lookup by identifier, got %+v", got)
	}
	if got := findPausedEntry(paused, "missing", "ISS-1"); got == nil || got.Attempt != 3 {
		t.Fatalf("expected paused entry lookup by identifier, got %+v", got)
	}
	if got := findRunningEntry(running, "missing", "missing"); got != nil {
		t.Fatalf("expected missing running entry to return nil, got %+v", got)
	}
}

func TestPersistedPauseAndFailureDerivationHelpers(t *testing.T) {
	pausedAt := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	events := []kanban.RuntimeEvent{{
		Kind:  "run_started",
		Phase: "implementation",
		Attempt: 1,
	}, {
		Kind:   "retry_paused",
		Phase:  "review",
		Attempt: 2,
		Error:  "plan_approval_pending",
		TS:     pausedAt,
		Payload: map[string]interface{}{
			"paused_at":            pausedAt.Format(time.RFC3339),
			"consecutive_failures":  2,
			"pause_threshold":       3,
			"status":                "paused",
			"message":               "Plan approval pending",
		},
	}, {
		Kind: "run_completed",
	}}

	if got := findPersistedPausedEntry(events); got != nil {
		t.Fatalf("expected reset event to clear persisted pause, got %+v", got)
	}

	pausedOnly := events[1:2]
	persisted := findPersistedPausedEntry(pausedOnly)
	if persisted == nil || persisted.Attempt != 2 || persisted.ConsecutiveFailures != 2 || persisted.PauseThreshold != 3 {
		t.Fatalf("expected persisted pause entry, got %+v", persisted)
	}
	if !persisted.PausedAt.Equal(pausedAt) {
		t.Fatalf("expected parsed paused_at, got %s", persisted.PausedAt)
	}

	if got := deriveCurrentError(false, nil, nil, nil, events); got != "" {
		t.Fatalf("expected reset history to clear current error, got %q", got)
	}
	if got := deriveCurrentError(false, &observability.RetryEntry{Error: "turn_input_required"}, nil, nil, nil); got != "turn_input_required" {
		t.Fatalf("expected retry error to surface, got %q", got)
	}
	if got := deriveFailureClass(false, &observability.RetryEntry{Error: "approval_required"}, nil, nil, nil); got != "approval_required" {
		t.Fatalf("expected normalized retry failure class, got %q", got)
	}
	if got := deriveFailureClass(false, nil, &observability.PausedEntry{Error: "turn_input_required"}, nil, nil); got != "turn_input_required" {
		t.Fatalf("expected paused failure class, got %q", got)
	}
	if got := deriveFailureClass(false, nil, nil, &kanban.ExecutionSessionSnapshot{RunKind: "run_failed", Error: "run_failed"}, nil); got != "run_failed" {
		t.Fatalf("expected persisted failure class, got %q", got)
	}
	if got := deriveFailureClass(false, nil, nil, nil, []kanban.RuntimeEvent{{Kind: "workspace_bootstrap_recovery", Error: "workspace_bootstrap"}}); got != "workspace_bootstrap" {
		t.Fatalf("expected historical failure class, got %q", got)
	}
	if got := normalizeFailureErrorClass(" plan_approval_pending "); got != "" {
		t.Fatalf("expected plan approval pending errors to be ignored, got %q", got)
	}
	if got := normalizeFailureKind("workspace_bootstrap_failed"); got != "workspace_bootstrap" {
		t.Fatalf("unexpected normalized failure kind %q", got)
	}
}

func TestWorkspaceRecoveryAndActivityGroupHelpers(t *testing.T) {
	recoveredAt := time.Date(2026, 3, 29, 12, 5, 0, 0, time.UTC)
	recovery := deriveWorkspaceRecovery([]kanban.RuntimeEvent{{
		Kind: "workspace_bootstrap_recovery",
		TS:   recoveredAt,
		Payload: map[string]interface{}{
			"message": "Recovering workspace",
			"status":  "recovering",
		},
	}})
	if recovery == nil || recovery.Status != "recovering" || recovery.Message != "Recovering workspace" {
		t.Fatalf("unexpected workspace recovery payload: %+v", recovery)
	}
	if deriveWorkspaceRecovery([]kanban.RuntimeEvent{{Kind: "workspace_bootstrap_created"}}) != nil {
		t.Fatal("expected bootstrap created event to clear recovery state")
	}

	entries := []kanban.IssueActivityEntry{{
		ID:         "entry-1",
		IssueID:    "issue-1",
		Identifier: "ISS-1",
		Attempt:    1,
		Kind:       "item.completed",
		ItemType:   "agentMessage",
		Phase:      "implementation",
		Status:     "done",
		Tier:       "primary",
		Title:      "Primary activity",
		Summary:    "summary",
		Detail:     "detail",
		Expandable: true,
		Tone:       "success",
		StartedAt:  &recoveredAt,
		CompletedAt: func() *time.Time {
			ts := recoveredAt.Add(5 * time.Minute)
			return &ts
		}(),
	}, {
		ID:         "entry-2",
		IssueID:    "issue-1",
		Identifier: "ISS-1",
		Attempt:    1,
		Kind:       "debug",
		Tier:       "secondary",
		Title:      "Debug activity",
		Summary:    "debug summary",
	}}
	mainGroups, debugGroups := buildActivityGroups(entries, []kanban.RuntimeEvent{{
		Kind:    "run_started",
		Attempt: 1,
		Phase:   "implementation",
	}, {
		Kind:    "run_completed",
		Attempt: 1,
		Phase:   "implementation",
	}})
	if len(mainGroups) != 1 || len(debugGroups) != 1 {
		t.Fatalf("unexpected activity grouping: main=%#v debug=%#v", mainGroups, debugGroups)
	}
	if mainGroups[0].Status != "completed" || mainGroups[0].Phase != "implementation" {
		t.Fatalf("expected attempt metadata on main group, got %+v", mainGroups[0])
	}
	meta := buildAttemptMetadata([]kanban.RuntimeEvent{{
		Kind:    "retry_scheduled",
		Attempt: 2,
		Phase:   "review",
	}, {
		Kind:    "retry_paused",
		Attempt: 2,
		Phase:   "review",
	}, {
		Kind:    "run_started",
		Attempt: 3,
		Phase:   "done",
	}})
	if meta[2].status != "paused" || meta[2].phase != "review" {
		t.Fatalf("unexpected retry metadata: %+v", meta[2])
	}
	if meta[3].status != "active" || meta[3].phase != "done" {
		t.Fatalf("unexpected active metadata: %+v", meta[3])
	}
}
