package runtimeview

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type ExecutionProvider interface {
	observability.SnapshotProvider
	observability.SessionProvider
}

func IssueExecutionPayload(store *kanban.Store, provider ExecutionProvider, issue *kanban.Issue) (map[string]interface{}, error) {
	events, err := store.ListIssueRuntimeEvents(issue.ID, 50)
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
		session = liveSession
	} else if persistedSession != nil {
		sessionSource = "persisted"
		session = persistedSession.AppSession
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
	retryState := "none"
	if running != nil {
		retryState = "active"
	} else if retry != nil {
		retryState = "scheduled"
	} else if paused != nil {
		retryState = "paused"
	}

	payload := map[string]interface{}{
		"issue_id":          issue.ID,
		"identifier":        issue.Identifier,
		"active":            running != nil,
		"phase":             phase,
		"attempt_number":    attempt,
		"failure_class":     failureClass,
		"current_error":     currentError,
		"retry_state":       retryState,
		"session_source":    sessionSource,
		"runtime_events":    events,
		"runtime_available": runtimeAvailable,
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
	return payload, nil
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
	switch session := raw.(type) {
	case appserver.Session:
		return session, true
	case *appserver.Session:
		if session == nil {
			return appserver.Session{}, false
		}
		return *session, true
	case map[string]interface{}:
		body, err := json.Marshal(session)
		if err != nil {
			return appserver.Session{}, false
		}
		var decoded appserver.Session
		if err := json.Unmarshal(body, &decoded); err != nil {
			return appserver.Session{}, false
		}
		return decoded, true
	default:
		return appserver.Session{}, false
	}
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
	switch {
	case retry != nil:
		if class := normalizeFailureClass(retry.Error); class != "" {
			return class
		}
	case paused != nil:
		if class := normalizeFailureClass(paused.Error); class != "" {
			return class
		}
	case persisted != nil:
		if class := normalizeFailureClass(persisted.Error); class != "" {
			return class
		}
		if class := normalizeFailureClass(persisted.RunKind); class != "" {
			return class
		}
	}
	for i := len(events) - 1; i >= 0; i-- {
		if class := normalizeFailureClass(events[i].Error); class != "" {
			return class
		}
		if class := normalizeFailureClass(events[i].Kind); class != "" {
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
	case retry != nil && strings.TrimSpace(retry.Error) != "":
		return retry.Error
	case paused != nil && strings.TrimSpace(paused.Error) != "":
		return paused.Error
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

func normalizeFailureClass(value string) string {
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
