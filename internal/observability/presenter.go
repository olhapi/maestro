package observability

import (
	"path/filepath"
	"strings"
	"time"
)

type SnapshotProvider interface {
	Snapshot() Snapshot
}

type RefreshProvider interface {
	RequestRefresh() map[string]interface{}
}

func StatePayload(provider SnapshotProvider) map[string]interface{} {
	snapshot := provider.Snapshot()
	return map[string]interface{}{
		"generated_at": snapshot.GeneratedAt.UTC().Format(time.RFC3339),
		"counts": map[string]int{
			"running":  len(snapshot.Running),
			"retrying": len(snapshot.Retrying),
		},
		"running":      runningPayload(snapshot.Running),
		"retrying":     retryPayload(snapshot.Retrying),
		"codex_totals": snapshot.CodexTotals,
		"rate_limits":  snapshot.RateLimits,
	}
}

func IssuePayload(provider SnapshotProvider, issueIdentifier string) (map[string]interface{}, bool) {
	snapshot := provider.Snapshot()
	var running *RunningEntry
	for i := range snapshot.Running {
		if snapshot.Running[i].Identifier == issueIdentifier {
			running = &snapshot.Running[i]
			break
		}
	}
	var retry *RetryEntry
	for i := range snapshot.Retrying {
		if snapshot.Retrying[i].Identifier == issueIdentifier {
			retry = &snapshot.Retrying[i]
			break
		}
	}
	if running == nil && retry == nil {
		return nil, false
	}

	workspacePath := ""
	if snapshot.WorkspaceRoot != "" {
		workspacePath = filepath.Join(snapshot.WorkspaceRoot, issueIdentifier)
	}
	payload := map[string]interface{}{
		"issue_identifier": issueIdentifier,
		"workspace": map[string]interface{}{
			"path": workspacePath,
		},
		"attempts": map[string]interface{}{
			"restart_count":         restartCount(retry),
			"current_retry_attempt": retryAttempt(retry),
		},
		"logs": map[string]interface{}{
			"codex_session_logs": []string{},
		},
	}
	if running != nil {
		payload["issue_id"] = running.IssueID
		payload["status"] = "running"
		payload["phase"] = running.Phase
		payload["running"] = runningEntryPayload(*running)
		payload["recent_events"] = recentEventsPayload(*running)
	}
	if retry != nil {
		payload["issue_id"] = retry.IssueID
		if running == nil {
			payload["status"] = "retrying"
			payload["recent_events"] = []map[string]interface{}{}
		}
		payload["phase"] = retry.Phase
		payload["retry"] = retryEntryPayload(*retry)
		payload["last_error"] = retry.Error
	}
	if _, ok := payload["status"]; !ok {
		payload["status"] = "running"
	}
	payload["tracked"] = map[string]interface{}{}
	return payload, true
}

func RefreshPayload(provider RefreshProvider) map[string]interface{} {
	if provider == nil {
		return map[string]interface{}{"requested_at": time.Now().UTC().Format(time.RFC3339)}
	}
	return provider.RequestRefresh()
}

func runningPayload(entries []RunningEntry) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		out = append(out, runningEntryPayload(entry))
	}
	return out
}

func retryPayload(entries []RetryEntry) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		out = append(out, retryEntryPayload(entry))
	}
	return out
}

func runningEntryPayload(entry RunningEntry) map[string]interface{} {
	payload := map[string]interface{}{
		"issue_id":         entry.IssueID,
		"issue_identifier": entry.Identifier,
		"state":            entry.State,
		"phase":            entry.Phase,
		"attempt":          entry.Attempt,
		"session_id":       entry.SessionID,
		"turn_count":       entry.TurnCount,
		"last_event":       entry.LastEvent,
		"last_message":     sanitizeMessage(entry.LastMessage),
		"started_at":       entry.StartedAt.UTC().Format(time.RFC3339),
		"tokens":           entry.Tokens,
	}
	if entry.LastEventAt != nil {
		payload["last_event_at"] = entry.LastEventAt.UTC().Format(time.RFC3339)
	}
	return payload
}

func retryEntryPayload(entry RetryEntry) map[string]interface{} {
	return map[string]interface{}{
		"issue_id":         entry.IssueID,
		"issue_identifier": entry.Identifier,
		"phase":            entry.Phase,
		"attempt":          entry.Attempt,
		"due_at":           entry.DueAt.UTC().Format(time.RFC3339),
		"error":            sanitizeMessage(entry.Error),
	}
}

func recentEventsPayload(entry RunningEntry) []map[string]interface{} {
	if entry.LastEventAt == nil {
		return []map[string]interface{}{}
	}
	return []map[string]interface{}{
		{
			"at":      entry.LastEventAt.UTC().Format(time.RFC3339),
			"event":   entry.LastEvent,
			"message": sanitizeMessage(entry.LastMessage),
		},
	}
}

func sanitizeMessage(message string) string {
	message = strings.ReplaceAll(message, "\\n", " ")
	message = strings.ReplaceAll(message, "\n", " ")
	return strings.Join(strings.Fields(strings.TrimSpace(message)), " ")
}

func retryAttempt(entry *RetryEntry) int {
	if entry == nil {
		return 0
	}
	return entry.Attempt
}

func restartCount(entry *RetryEntry) int {
	attempt := retryAttempt(entry)
	if attempt <= 0 {
		return 0
	}
	return attempt - 1
}
