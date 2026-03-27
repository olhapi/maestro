package runtimeview

import (
	"database/sql"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type ExecutionProvider interface {
	observability.SnapshotProvider
	observability.SessionProvider
	PendingInterruptForIssue(issueID, identifier string) (*appserver.PendingInteraction, bool)
}

func IssueExecutionPayload(store *kanban.Store, provider ExecutionProvider, issue *kanban.Issue) (map[string]interface{}, error) {
	events, err := store.ListIssueRuntimeEvents(issue.ID, 0)
	if err != nil {
		return nil, err
	}
	activityEntries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		return nil, err
	}
	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		return nil, err
	}

	runtimeAvailable := provider != nil
	snapshot := observability.Snapshot{}
	if runtimeAvailable {
		snapshot = provider.Snapshot()
	}

	running := findRunningEntry(snapshot.Running, issue.ID, issue.Identifier)
	retry := findRetryEntry(snapshot.Retrying, issue.ID, issue.Identifier)
	paused := findPausedEntry(snapshot.Paused, issue.ID, issue.Identifier)

	var liveSession *appserver.Session
	if runtimeAvailable {
		if session, ok := findLiveSession(provider.LiveSessions(), issue.Identifier); ok {
			liveSession = &session
		}
	}
	var pendingInterrupt *appserver.PendingInteraction
	if runtimeAvailable {
		if interaction, ok := provider.PendingInterruptForIssue(issue.ID, issue.Identifier); ok {
			pendingInterrupt = interaction
		}
	}

	persistedSession, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if paused == nil {
		paused = findPersistedPausedEntry(events)
	}

	sessionSource := "none"
	var session interface{}
	if liveSession != nil {
		sessionSource = "live"
		summary := liveSession.Summary()
		session = &summary
	} else if persistedSession != nil {
		sessionSource = "persisted"
		summary := persistedSession.AppSession.Summary()
		session = summary
	}

	attempt := 0
	switch {
	case running != nil && running.Attempt > 0:
		attempt = running.Attempt
	case retry != nil && retry.Attempt > 0:
		attempt = retry.Attempt
	case paused != nil && paused.Attempt > 0:
		attempt = paused.Attempt
	case persistedSession != nil && persistedSession.Attempt > 0:
		attempt = persistedSession.Attempt
	case len(events) > 0:
		attempt = events[len(events)-1].Attempt
	}

	phase := string(issue.WorkflowPhase)
	switch {
	case running != nil && strings.TrimSpace(running.Phase) != "":
		phase = running.Phase
	case retry != nil && strings.TrimSpace(retry.Phase) != "":
		phase = retry.Phase
	case paused != nil && strings.TrimSpace(paused.Phase) != "":
		phase = paused.Phase
	case persistedSession != nil && strings.TrimSpace(persistedSession.Phase) != "":
		phase = persistedSession.Phase
	}

	currentError := deriveCurrentError(running != nil, retry, paused, persistedSession, events)
	failureClass := deriveFailureClass(running != nil, retry, paused, persistedSession, events)
	workspaceRecovery := deriveWorkspaceRecovery(events)
	retryState := "none"
	if running != nil {
		retryState = "active"
	} else if retry != nil {
		retryState = "scheduled"
	} else if paused != nil {
		retryState = "paused"
	}

	activityGroups, debugActivityGroups := buildActivityGroups(activityEntries, events)

	payload := map[string]interface{}{
		"issue_id":              issue.ID,
		"identifier":            issue.Identifier,
		"active":                running != nil,
		"phase":                 phase,
		"attempt_number":        attempt,
		"failure_class":         failureClass,
		"current_error":         currentError,
		"retry_state":           retryState,
		"session_source":        sessionSource,
		"runtime_events":        events,
		"activity_groups":       activityGroups,
		"debug_activity_groups": debugActivityGroups,
		"runtime_available":     runtimeAvailable,
		"agent_commands":        commands,
	}
	if workspaceRecovery != nil {
		payload["workspace_recovery"] = workspaceRecovery
	}
	if retry != nil {
		payload["next_retry_at"] = retry.DueAt.UTC().Format(time.RFC3339)
	}
	if paused != nil {
		payload["paused_at"] = paused.PausedAt.UTC().Format(time.RFC3339)
		payload["pause_reason"] = paused.Error
		payload["consecutive_failures"] = paused.ConsecutiveFailures
		payload["pause_threshold"] = paused.PauseThreshold
	}
	if session != nil {
		payload["session"] = session
	}
	if pendingInterrupt != nil {
		payload["pending_interrupt"] = pendingInterrupt
	}
	if issue.PlanApprovalPending && strings.TrimSpace(issue.PendingPlanMarkdown) != "" && issue.PendingPlanRequestedAt != nil {
		payload["plan_approval"] = kanban.IssuePlanApproval{
			Markdown:    issue.PendingPlanMarkdown,
			RequestedAt: issue.PendingPlanRequestedAt.UTC(),
			Attempt:     attempt,
		}
	}
	if strings.TrimSpace(issue.PendingPlanRevisionMarkdown) != "" && issue.PendingPlanRevisionRequestedAt != nil {
		payload["plan_revision"] = kanban.IssuePlanRevision{
			Markdown:    issue.PendingPlanRevisionMarkdown,
			RequestedAt: issue.PendingPlanRevisionRequestedAt.UTC(),
			Attempt:     attempt,
		}
	}
	return payload, nil
}

func findEntry[T any](entries []T, match func(*T) bool) *T {
	for i := range entries {
		if match(&entries[i]) {
			return &entries[i]
		}
	}
	return nil
}

func findRunningEntry(entries []observability.RunningEntry, issueID, identifier string) *observability.RunningEntry {
	return findEntry(entries, func(entry *observability.RunningEntry) bool {
		return entry.IssueID == issueID || entry.Identifier == identifier
	})
}

func findRetryEntry(entries []observability.RetryEntry, issueID, identifier string) *observability.RetryEntry {
	return findEntry(entries, func(entry *observability.RetryEntry) bool {
		return entry.IssueID == issueID || entry.Identifier == identifier
	})
}

func findPausedEntry(entries []observability.PausedEntry, issueID, identifier string) *observability.PausedEntry {
	return findEntry(entries, func(entry *observability.PausedEntry) bool {
		return entry.IssueID == issueID || entry.Identifier == identifier
	})
}

func findPersistedPausedEntry(events []kanban.RuntimeEvent) *observability.PausedEntry {
	if len(events) == 0 {
		return nil
	}
	latest := events[len(events)-1]
	if latest.Kind != "retry_paused" {
		return nil
	}
	pausedAt := latest.TS
	if raw, ok := latest.Payload["paused_at"].(string); ok && raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			pausedAt = parsed
		}
	}
	return &observability.PausedEntry{
		IssueID:             latest.IssueID,
		Identifier:          latest.Identifier,
		Phase:               latest.Phase,
		Attempt:             latest.Attempt,
		PausedAt:            pausedAt,
		Error:               latest.Error,
		ConsecutiveFailures: asPayloadInt(latest.Payload["consecutive_failures"]),
		PauseThreshold:      asPayloadInt(latest.Payload["pause_threshold"]),
	}
}

func findLiveSession(all map[string]interface{}, identifier string) (appserver.Session, bool) {
	sessions, ok := all["sessions"].(map[string]interface{})
	if !ok {
		return appserver.Session{}, false
	}
	raw, ok := sessions[identifier]
	if !ok {
		return appserver.Session{}, false
	}
	return appserver.SessionFromAny(raw)
}

func deriveFailureClass(active bool, retry *observability.RetryEntry, paused *observability.PausedEntry, persisted *kanban.ExecutionSessionSnapshot, events []kanban.RuntimeEvent) string {
	if active {
		return ""
	}
	if !active && retry == nil {
		if persisted != nil && strings.TrimSpace(persisted.RunKind) == "run_started" {
			return "run_interrupted"
		}
		if len(events) > 0 && events[len(events)-1].Kind == "run_started" {
			return "run_interrupted"
		}
	}
	if isPlanApprovalPendingError(planApprovalPendingErrorText(retry, paused, persisted)) {
		return ""
	}
	switch {
	case retry != nil:
		if class := normalizeFailureErrorClass(retry.Error); class != "" {
			return class
		}
	case paused != nil:
		if class := normalizeFailureErrorClass(paused.Error); class != "" {
			return class
		}
	case persisted != nil:
		if class := normalizeFailureErrorClass(persisted.Error); class != "" {
			return class
		}
		if class := normalizeFailureKind(persisted.RunKind); class != "" {
			return class
		}
	}
	return deriveHistoricalFailure(events, func(event kanban.RuntimeEvent) string {
		if class := normalizeFailureKind(event.Kind); class == "workspace_bootstrap" {
			return class
		}
		if class := normalizeFailureErrorClass(event.Error); class != "" {
			return class
		}
		return normalizeFailureKind(event.Kind)
	})
}

func deriveCurrentError(active bool, retry *observability.RetryEntry, paused *observability.PausedEntry, persisted *kanban.ExecutionSessionSnapshot, events []kanban.RuntimeEvent) string {
	if active {
		return ""
	}
	if isPlanApprovalPendingError(planApprovalPendingErrorText(retry, paused, persisted)) {
		return ""
	}
	switch {
	case retry != nil && strings.TrimSpace(retry.Error) != "":
		return retry.Error
	case paused != nil && strings.TrimSpace(paused.Error) != "":
		return paused.Error
	case persisted != nil && strings.TrimSpace(persisted.Error) != "":
		return persisted.Error
	default:
		return deriveHistoricalFailure(events, func(event kanban.RuntimeEvent) string {
			return strings.TrimSpace(event.Error)
		})
	}
}

func deriveHistoricalFailure(events []kanban.RuntimeEvent, valueFn func(kanban.RuntimeEvent) string) string {
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if isHistoricalFailureResetEvent(event.Kind) {
			return ""
		}
		if value := strings.TrimSpace(valueFn(event)); value != "" {
			return value
		}
	}
	return ""
}

func isHistoricalFailureResetEvent(kind string) bool {
	switch kind {
	case "run_completed", "workspace_bootstrap_created", "workspace_bootstrap_reused", "workspace_bootstrap_preserved":
		return true
	default:
		return false
	}
}

func deriveWorkspaceRecovery(events []kanban.RuntimeEvent) *kanban.WorkspaceRecovery {
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Kind {
		case "workspace_bootstrap_recovery":
			return workspaceRecoveryFromEvent(events[i], "recovering")
		case "workspace_bootstrap_failed":
			return workspaceRecoveryFromEvent(events[i], "required")
		case "workspace_bootstrap_created", "workspace_bootstrap_reused", "workspace_bootstrap_preserved":
			return nil
		}
	}
	return nil
}

func workspaceRecoveryFromEvent(event kanban.RuntimeEvent, defaultStatus string) *kanban.WorkspaceRecovery {
	status := strings.TrimSpace(eventPayloadString(event.Payload, "status"))
	if status == "" {
		status = defaultStatus
	}
	message := strings.TrimSpace(eventPayloadString(event.Payload, "message"))
	if message == "" {
		message = strings.TrimSpace(event.Error)
	}
	if message == "" {
		if status == "recovering" {
			message = "Workspace recovery is in progress."
		} else {
			message = "Workspace bootstrap failed. Review the blocker and retry once it is resolved."
		}
	}
	return &kanban.WorkspaceRecovery{
		Status:  status,
		Message: message,
	}
}

func eventPayloadString(payload map[string]interface{}, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func asPayloadInt(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func normalizeFailureErrorClass(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case value == "":
		return ""
	case strings.Contains(value, "plan_approval_pending"):
		return ""
	case strings.Contains(value, "approval_required"):
		return "approval_required"
	case strings.Contains(value, "turn_input_required"):
		return "turn_input_required"
	case strings.Contains(value, "stall_timeout"):
		return "stall_timeout"
	case strings.Contains(value, "run_unsuccessful"), strings.Contains(value, "unsuccessful"):
		return "run_unsuccessful"
	case strings.Contains(value, "run_failed"):
		return "run_failed"
	default:
		return value
	}
}

func isPlanApprovalPendingError(value string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(value)), "plan_approval_pending")
}

func planApprovalPendingErrorText(retry *observability.RetryEntry, paused *observability.PausedEntry, persisted *kanban.ExecutionSessionSnapshot) string {
	if retry != nil && isPlanApprovalPendingError(retry.Error) {
		return retry.Error
	}
	if paused != nil && isPlanApprovalPendingError(paused.Error) {
		return paused.Error
	}
	if persisted != nil && isPlanApprovalPendingError(persisted.Error) {
		return persisted.Error
	}
	return ""
}

func normalizeFailureKind(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case value == "":
		return ""
	case value == "workspace_bootstrap", value == "workspace_bootstrap_recovery", value == "workspace_bootstrap_failed":
		return "workspace_bootstrap"
	case strings.Contains(value, "approval_required"):
		return "approval_required"
	case strings.Contains(value, "turn_input_required"):
		return "turn_input_required"
	case strings.Contains(value, "stall_timeout"):
		return "stall_timeout"
	case strings.Contains(value, "run_unsuccessful"), strings.Contains(value, "unsuccessful"):
		return "run_unsuccessful"
	case strings.Contains(value, "run_failed"):
		return "run_failed"
	default:
		return ""
	}
}
