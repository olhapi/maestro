package dashboardapi

import (
	"sort"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

const (
	recentSessionFeedLimit  = 12
	recentSessionFeedWindow = 24 * time.Hour
)

func (s *Server) sessionsPayload() map[string]interface{} {
	livePayload := s.provider.LiveSessions()
	sessionsMap := map[string]interface{}{}
	if raw, ok := livePayload["sessions"].(map[string]interface{}); ok {
		for key, value := range raw {
			sessionsMap[key] = value
		}
	}

	payload := map[string]interface{}{
		"sessions": sessionsMap,
		"entries":  []kanban.SessionFeedEntry{},
	}
	entries, err := buildSessionFeedEntries(s.store, s.provider, sessionsMap)
	if err == nil {
		payload["entries"] = entries
	}
	return payload
}

func buildSessionFeedEntries(store *kanban.Store, provider Provider, liveSessions map[string]interface{}) ([]kanban.SessionFeedEntry, error) {
	live := decodeLiveSessions(liveSessions)
	snapshot := provider.Snapshot()
	runningByIdentifier := make(map[string]observability.RunningEntry, len(snapshot.Running))
	for _, entry := range snapshot.Running {
		if id := strings.TrimSpace(entry.Identifier); id != "" {
			runningByIdentifier[id] = entry
		}
	}
	retryByIdentifier := make(map[string]observability.RetryEntry, len(snapshot.Retrying))
	for _, entry := range snapshot.Retrying {
		if id := strings.TrimSpace(entry.Identifier); id != "" {
			retryByIdentifier[id] = entry
		}
	}
	pausedByIdentifier := make(map[string]observability.PausedEntry, len(snapshot.Paused))
	for _, entry := range snapshot.Paused {
		if id := strings.TrimSpace(entry.Identifier); id != "" {
			pausedByIdentifier[id] = entry
		}
	}

	recent, err := store.ListRecentExecutionSessions(time.Now().UTC().Add(-recentSessionFeedWindow), recentSessionFeedLimit)
	if err != nil {
		return nil, err
	}
	titleByIdentifier := loadIssueTitlesByIdentifier(store, live, recent)

	out := make([]kanban.SessionFeedEntry, 0, len(live)+recentSessionFeedLimit)
	seen := make(map[string]struct{}, len(live))
	for identifier, session := range live {
		pendingInterrupt, _ := provider.PendingInterruptForIssue(session.IssueID, firstNonEmpty(session.IssueIdentifier, identifier))
		entry := buildLiveSessionFeedEntry(identifier, session, runningByIdentifier[identifier], titleByIdentifier[identifier], pendingInterrupt)
		out = append(out, entry)
		if entry.IssueIdentifier != "" {
			seen[entry.IssueIdentifier] = struct{}{}
		}
	}
	for _, snapshot := range recent {
		identifier := strings.TrimSpace(snapshot.Identifier)
		if identifier == "" {
			identifier = strings.TrimSpace(snapshot.AppSession.IssueIdentifier)
		}
		if identifier == "" {
			continue
		}
		if _, ok := seen[identifier]; ok {
			continue
		}
		out = append(out, buildPersistedSessionFeedEntry(snapshot, retryByIdentifier[identifier], pausedByIdentifier[identifier], titleByIdentifier[identifier]))
		seen[identifier] = struct{}{}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source == "live"
		}
		leftTitle := sessionFeedSortKey(out[i].IssueTitle, out[i].IssueIdentifier)
		rightTitle := sessionFeedSortKey(out[j].IssueTitle, out[j].IssueIdentifier)
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		return out[i].IssueIdentifier < out[j].IssueIdentifier
	})

	return out, nil
}

func loadIssueTitlesByIdentifier(store *kanban.Store, live map[string]appserver.Session, recent []kanban.ExecutionSessionSnapshot) map[string]string {
	type issueRef struct {
		issueID    string
		identifier string
	}

	refs := make(map[string]issueRef, len(live)+len(recent))
	for identifier, session := range live {
		resolvedIdentifier := strings.TrimSpace(firstNonEmpty(session.IssueIdentifier, identifier))
		if resolvedIdentifier == "" {
			continue
		}
		refs[resolvedIdentifier] = issueRef{
			issueID:    strings.TrimSpace(session.IssueID),
			identifier: resolvedIdentifier,
		}
	}
	for _, snapshot := range recent {
		identifier := strings.TrimSpace(firstNonEmpty(snapshot.Identifier, snapshot.AppSession.IssueIdentifier))
		if identifier == "" {
			continue
		}
		if _, ok := refs[identifier]; ok {
			continue
		}
		refs[identifier] = issueRef{
			issueID:    strings.TrimSpace(snapshot.IssueID),
			identifier: identifier,
		}
	}

	issueIDs := make([]string, 0, len(refs))
	identifiers := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.issueID != "" {
			issueIDs = append(issueIDs, ref.issueID)
		}
		if ref.identifier != "" {
			identifiers = append(identifiers, ref.identifier)
		}
	}

	titlesByID, titlesByIdentifier, err := store.LookupIssueTitles(issueIDs, identifiers)
	if err != nil {
		return map[string]string{}
	}

	out := make(map[string]string, len(refs))
	for identifier, ref := range refs {
		switch {
		case ref.issueID != "" && strings.TrimSpace(titlesByID[ref.issueID]) != "":
			out[identifier] = titlesByID[ref.issueID]
		case ref.identifier != "" && strings.TrimSpace(titlesByIdentifier[ref.identifier]) != "":
			out[identifier] = titlesByIdentifier[ref.identifier]
		}
	}
	return out
}

func sessionFeedSortKey(title, identifier string) string {
	key := strings.TrimSpace(title)
	if key == "" {
		key = strings.TrimSpace(identifier)
	}
	return strings.ToLower(key)
}

func decodeLiveSessions(raw map[string]interface{}) map[string]appserver.Session {
	return appserver.SessionsFromMap(raw)
}

func buildLiveSessionFeedEntry(identifier string, session appserver.Session, running observability.RunningEntry, issueTitle string, pendingInterrupt *appserver.PendingInteraction) kanban.SessionFeedEntry {
	updatedAt := session.LastTimestamp
	if updatedAt.IsZero() {
		if running.LastEventAt != nil && !running.LastEventAt.IsZero() {
			updatedAt = *running.LastEventAt
		} else {
			updatedAt = running.StartedAt
		}
	}

	lastMessage := strings.TrimSpace(session.LastMessage)
	if lastMessage == "" {
		lastMessage = strings.TrimSpace(running.LastMessage)
	}
	totalTokens := session.TotalTokens
	if totalTokens == 0 {
		totalTokens = running.Tokens.TotalTokens
	}
	status := "active"
	var pending *appserver.PendingInteraction
	if pendingInterrupt != nil {
		cloned := pendingInterrupt.Clone()
		pending = &cloned
		status = "waiting"
		if pending.LastActivityAt != nil && !pending.LastActivityAt.IsZero() {
			updatedAt = pending.LastActivityAt.UTC()
		}
		if strings.TrimSpace(pending.LastActivity) != "" {
			lastMessage = pending.LastActivity
		}
	}

	return kanban.SessionFeedEntry{
		IssueID:          firstNonEmpty(session.IssueID, running.IssueID),
		IssueIdentifier:  firstNonEmpty(session.IssueIdentifier, identifier, running.Identifier),
		IssueTitle:       issueTitle,
		Source:           "live",
		Active:           true,
		Status:           status,
		PendingInterrupt: pending,
		Phase:            running.Phase,
		Attempt:          running.Attempt,
		RunKind:          "run_started",
		UpdatedAt:        updatedAt,
		LastEvent:        firstNonEmpty(session.LastEvent, running.LastEvent),
		LastMessage:      lastMessage,
		TotalTokens:      totalTokens,
		EventsProcessed:  session.EventsProcessed,
		TurnsStarted:     maxInt(session.TurnsStarted, running.TurnCount),
		TurnsCompleted:   session.TurnsCompleted,
		Terminal:         session.Terminal,
		TerminalReason:   session.TerminalReason,
	}
}

func buildPersistedSessionFeedEntry(snapshot kanban.ExecutionSessionSnapshot, retry observability.RetryEntry, paused observability.PausedEntry, issueTitle string) kanban.SessionFeedEntry {
	session := snapshot.AppSession
	updatedAt := session.LastTimestamp
	if updatedAt.IsZero() {
		updatedAt = snapshot.UpdatedAt
	}
	errorText := firstNonEmpty(paused.Error, retry.Error, snapshot.Error)
	failureClass := normalizeFailureClass(errorText)
	if failureClass == "" {
		failureClass = normalizeFailureClass(snapshot.RunKind)
	}

	status := "failed"
	switch {
	case strings.TrimSpace(paused.Identifier) != "" || strings.EqualFold(snapshot.RunKind, "retry_paused"):
		status = "paused"
	case strings.EqualFold(snapshot.RunKind, "run_completed"):
		status = "completed"
	case strings.EqualFold(snapshot.RunKind, "run_started"):
		status = "interrupted"
	case failureClass != "":
		status = "failed"
	case session.Terminal && strings.Contains(strings.ToLower(session.TerminalReason), "completed"):
		status = "completed"
	case session.Terminal:
		status = "failed"
	default:
		status = "interrupted"
	}

	phase := snapshot.Phase
	if strings.TrimSpace(paused.Phase) != "" {
		phase = paused.Phase
	}
	if strings.TrimSpace(phase) == "" {
		phase = retry.Phase
	}
	attempt := snapshot.Attempt
	if paused.Attempt > 0 {
		attempt = paused.Attempt
	}
	if attempt == 0 && retry.Attempt > 0 {
		attempt = retry.Attempt
	}

	return kanban.SessionFeedEntry{
		IssueID:         snapshot.IssueID,
		IssueIdentifier: firstNonEmpty(snapshot.Identifier, session.IssueIdentifier),
		IssueTitle:      issueTitle,
		Source:          "persisted",
		Active:          false,
		Status:          status,
		Phase:           phase,
		Attempt:         attempt,
		RunKind:         snapshot.RunKind,
		FailureClass:    failureClass,
		UpdatedAt:       updatedAt,
		LastEvent:       session.LastEvent,
		LastMessage:     session.LastMessage,
		TotalTokens:     session.TotalTokens,
		EventsProcessed: session.EventsProcessed,
		TurnsStarted:    session.TurnsStarted,
		TurnsCompleted:  session.TurnsCompleted,
		Terminal:        session.Terminal,
		TerminalReason:  session.TerminalReason,
		Error:           errorText,
	}
}

func normalizeFailureClass(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case value == "":
		return ""
	case strings.Contains(value, "workspace_bootstrap"):
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
	case strings.Contains(value, "run_interrupted"), strings.Contains(value, "interrupted"):
		return "run_interrupted"
	default:
		return value
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
