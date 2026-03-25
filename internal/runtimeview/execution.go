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

type issueExecutionPayloadView struct {
	IssueID              string
	Identifier           string
	Active               bool
	Phase                string
	Attempt              int
	FailureClass         string
	CurrentError         string
	RetryState           string
	SessionSource        string
	RuntimeAvailable     bool
	RuntimeEvents        []kanban.RuntimeEvent
	ActivityGroups       []kanban.ActivityGroup
	DebugActivityGroups  []kanban.ActivityGroup
	AgentCommands        []kanban.IssueAgentCommand
	WorkspaceRecovery    *kanban.WorkspaceRecovery
	HasWorkspaceRecovery bool
	NextRetryAt          string
	HasRetry             bool
	PausedAt             string
	PauseReason          string
	ConsecutiveFailures  int
	PauseThreshold       int
	HasPaused            bool
	Session              interface{}
	HasSession           bool
	PendingInterrupt     *appserver.PendingInteraction
	HasPendingInterrupt  bool
	PlanApproval         kanban.IssuePlanApproval
	HasPlanApproval      bool
}

func (p issueExecutionPayloadView) toMap() map[string]interface{} {
	payload := map[string]interface{}{
		"issue_id":              p.IssueID,
		"identifier":            p.Identifier,
		"active":                p.Active,
		"phase":                 p.Phase,
		"attempt_number":        p.Attempt,
		"failure_class":         p.FailureClass,
		"current_error":         p.CurrentError,
		"retry_state":           p.RetryState,
		"session_source":        p.SessionSource,
		"runtime_events":        p.RuntimeEvents,
		"activity_groups":       p.ActivityGroups,
		"debug_activity_groups": p.DebugActivityGroups,
		"runtime_available":     p.RuntimeAvailable,
		"agent_commands":        p.AgentCommands,
	}
	if p.HasWorkspaceRecovery && p.WorkspaceRecovery != nil {
		payload["workspace_recovery"] = p.WorkspaceRecovery
	}
	if p.HasRetry {
		payload["next_retry_at"] = p.NextRetryAt
	}
	if p.HasPaused {
		payload["paused_at"] = p.PausedAt
		payload["pause_reason"] = p.PauseReason
		payload["consecutive_failures"] = p.ConsecutiveFailures
		payload["pause_threshold"] = p.PauseThreshold
	}
	if p.HasSession {
		payload["session"] = p.Session
	}
	if p.HasPendingInterrupt && p.PendingInterrupt != nil {
		payload["pending_interrupt"] = p.PendingInterrupt
	}
	if p.HasPlanApproval {
		payload["plan_approval"] = p.PlanApproval
	}
	return payload
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
	case paused != nil && paused.Attempt > 0:
		attempt = paused.Attempt
	case retry != nil && retry.Attempt > 0:
		attempt = retry.Attempt
	case persistedSession != nil && persistedSession.Attempt > 0:
		attempt = persistedSession.Attempt
	case len(events) > 0:
		attempt = events[len(events)-1].Attempt
	}

	phase := string(issue.WorkflowPhase)
	switch {
	case running != nil && strings.TrimSpace(running.Phase) != "":
		phase = running.Phase
	case paused != nil && strings.TrimSpace(paused.Phase) != "":
		phase = paused.Phase
	case retry != nil && strings.TrimSpace(retry.Phase) != "":
		phase = retry.Phase
	case persistedSession != nil && strings.TrimSpace(persistedSession.Phase) != "":
		phase = persistedSession.Phase
	}

	currentError := deriveCurrentError(running != nil, retry, paused, persistedSession, events)
	failureClass := deriveFailureClass(running != nil, retry, paused, persistedSession, events)
	workspaceRecovery := deriveWorkspaceRecovery(events)
	retryState := "none"
	if running != nil {
		retryState = "active"
	} else if paused != nil {
		retryState = "paused"
	} else if retry != nil {
		retryState = "scheduled"
	}

	activityGroups, debugActivityGroups := buildActivityGroups(activityEntries, events)

	view := issueExecutionPayloadView{
		IssueID:             issue.ID,
		Identifier:          issue.Identifier,
		Active:              running != nil,
		Phase:               phase,
		Attempt:             attempt,
		FailureClass:        failureClass,
		CurrentError:        currentError,
		RetryState:          retryState,
		SessionSource:       sessionSource,
		RuntimeAvailable:    runtimeAvailable,
		RuntimeEvents:       events,
		ActivityGroups:      activityGroups,
		DebugActivityGroups: debugActivityGroups,
		AgentCommands:       commands,
	}
	if workspaceRecovery != nil {
		view.WorkspaceRecovery = workspaceRecovery
		view.HasWorkspaceRecovery = true
	}
	if retry != nil {
		view.NextRetryAt = retry.DueAt.UTC().Format(time.RFC3339)
		view.HasRetry = true
	}
	if paused != nil {
		view.PausedAt = paused.PausedAt.UTC().Format(time.RFC3339)
		view.PauseReason = paused.Error
		view.ConsecutiveFailures = paused.ConsecutiveFailures
		view.PauseThreshold = paused.PauseThreshold
		view.HasPaused = true
	}
	if session != nil {
		view.Session = session
		view.HasSession = true
	}
	if pendingInterrupt != nil {
		view.PendingInterrupt = pendingInterrupt
		view.HasPendingInterrupt = true
	}
	if issue.PlanApprovalPending && strings.TrimSpace(issue.PendingPlanMarkdown) != "" && issue.PendingPlanRequestedAt != nil {
		view.PlanApproval = kanban.IssuePlanApproval{
			Markdown:    issue.PendingPlanMarkdown,
			RequestedAt: issue.PendingPlanRequestedAt.UTC(),
			Attempt:     attempt,
		}
		view.HasPlanApproval = true
	}
	return view.toMap(), nil
}

func findRunningEntry(entries []observability.RunningEntry, issueID, identifier string) *observability.RunningEntry {
	for i := range entries {
		if entries[i].IssueID == issueID || entries[i].Identifier == identifier {
			return &entries[i]
		}
	}
	return nil
}

func findRetryEntry(entries []observability.RetryEntry, issueID, identifier string) *observability.RetryEntry {
	for i := range entries {
		if entries[i].IssueID == issueID || entries[i].Identifier == identifier {
			return &entries[i]
		}
	}
	return nil
}

func findPausedEntry(entries []observability.PausedEntry, issueID, identifier string) *observability.PausedEntry {
	for i := range entries {
		if entries[i].IssueID == issueID || entries[i].Identifier == identifier {
			return &entries[i]
		}
	}
	return nil
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
	switch {
	case paused != nil:
		if class := normalizeFailureErrorClass(paused.Error); class != "" {
			return class
		}
	case retry != nil:
		if class := normalizeFailureErrorClass(retry.Error); class != "" {
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
	if retry == nil && paused == nil {
		if persisted != nil && strings.TrimSpace(persisted.RunKind) == "run_started" {
			return "run_interrupted"
		}
		if len(events) > 0 && events[len(events)-1].Kind == "run_started" {
			return "run_interrupted"
		}
	}
	for i := len(events) - 1; i >= 0; i-- {
		if class := normalizeFailureKind(events[i].Kind); class == "workspace_bootstrap" {
			return class
		}
		if class := normalizeFailureErrorClass(events[i].Error); class != "" {
			return class
		}
		if class := normalizeFailureKind(events[i].Kind); class != "" {
			return class
		}
	}
	return ""
}

func deriveCurrentError(active bool, retry *observability.RetryEntry, paused *observability.PausedEntry, persisted *kanban.ExecutionSessionSnapshot, events []kanban.RuntimeEvent) string {
	if active {
		return ""
	}
	switch {
	case paused != nil && strings.TrimSpace(paused.Error) != "":
		return paused.Error
	case retry != nil && strings.TrimSpace(retry.Error) != "":
		return retry.Error
	case persisted != nil && strings.TrimSpace(persisted.Error) != "":
		return persisted.Error
	default:
		for i := len(events) - 1; i >= 0; i-- {
			if strings.TrimSpace(events[i].Error) != "" {
				return events[i].Error
			}
		}
	}
	return ""
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
