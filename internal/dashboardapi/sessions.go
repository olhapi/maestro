package dashboardapi

import (
	"sort"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

const (
	recentSessionFeedLimit  = 12
	recentSessionFeedWindow = 24 * time.Hour
	queuedPlanRevisionText  = "Plan revision queued for the next planning turn."
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
	pendingByIssueID, pendingByIdentifier := indexPendingInterrupts(provider.PendingInterrupts().Items)
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
	issuesByIdentifier := loadIssuesByIdentifier(store, live, recent)
	planningByIdentifier := loadPlanningByIdentifier(store, issuesByIdentifier)

	out := make([]kanban.SessionFeedEntry, 0, len(live)+recentSessionFeedLimit)
	seen := make(map[string]struct{}, len(live))
	for identifier, session := range live {
		issue := issuesByIdentifier[firstNonEmpty(session.IssueIdentifier, identifier)]
		pendingInterrupt := pendingInterruptForSession(
			session.IssueID,
			firstNonEmpty(session.IssueIdentifier, identifier),
			pendingByIssueID,
			pendingByIdentifier,
		)
		entry := buildLiveSessionFeedEntry(
			store,
			identifier,
			session,
			runningByIdentifier[identifier],
			issue,
			planningByIdentifier[firstNonEmpty(session.IssueIdentifier, identifier)],
			titleByIdentifier[identifier],
			pendingInterrupt,
		)
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
		out = append(
			out,
			buildPersistedSessionFeedEntry(
				store,
				snapshot,
				retryByIdentifier[identifier],
				pausedByIdentifier[identifier],
				issuesByIdentifier[identifier],
				planningByIdentifier[identifier],
				titleByIdentifier[identifier],
			),
		)
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

func indexPendingInterrupts(items []agentruntime.PendingInteraction) (map[string]agentruntime.PendingInteraction, map[string]agentruntime.PendingInteraction) {
	byIssueID := make(map[string]agentruntime.PendingInteraction, len(items))
	byIdentifier := make(map[string]agentruntime.PendingInteraction, len(items))
	for i := range items {
		interaction := items[i].Clone()
		if issueID := strings.TrimSpace(interaction.IssueID); issueID != "" {
			if _, ok := byIssueID[issueID]; !ok {
				byIssueID[issueID] = interaction
			}
		}
		if identifier := strings.TrimSpace(interaction.IssueIdentifier); identifier != "" {
			if _, ok := byIdentifier[identifier]; !ok {
				byIdentifier[identifier] = interaction
			}
		}
	}
	return byIssueID, byIdentifier
}

func pendingInterruptForSession(
	issueID, identifier string,
	byIssueID, byIdentifier map[string]agentruntime.PendingInteraction,
) *agentruntime.PendingInteraction {
	if interaction, ok := byIssueID[strings.TrimSpace(issueID)]; ok {
		cloned := interaction.Clone()
		return &cloned
	}
	if interaction, ok := byIdentifier[strings.TrimSpace(identifier)]; ok {
		cloned := interaction.Clone()
		return &cloned
	}
	return nil
}

func loadIssueTitlesByIdentifier(store *kanban.Store, live map[string]agentruntime.Session, recent []kanban.ExecutionSessionSnapshot) map[string]string {
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

func loadIssuesByIdentifier(store *kanban.Store, live map[string]agentruntime.Session, recent []kanban.ExecutionSessionSnapshot) map[string]*kanban.Issue {
	identifiers := make(map[string]struct{}, len(live)+len(recent))
	for identifier, session := range live {
		resolvedIdentifier := strings.TrimSpace(firstNonEmpty(session.IssueIdentifier, identifier))
		if resolvedIdentifier != "" {
			identifiers[resolvedIdentifier] = struct{}{}
		}
	}
	for _, snapshot := range recent {
		identifier := strings.TrimSpace(firstNonEmpty(snapshot.Identifier, snapshot.AppSession.IssueIdentifier))
		if identifier != "" {
			identifiers[identifier] = struct{}{}
		}
	}

	out := make(map[string]*kanban.Issue, len(identifiers))
	for identifier := range identifiers {
		issue, err := store.GetIssueByIdentifier(identifier)
		if err != nil {
			continue
		}
		out[identifier] = issue
	}
	return out
}

func loadPlanningByIdentifier(store *kanban.Store, issuesByIdentifier map[string]*kanban.Issue) map[string]*kanban.IssuePlanning {
	out := make(map[string]*kanban.IssuePlanning, len(issuesByIdentifier))
	for identifier, issue := range issuesByIdentifier {
		if issue == nil {
			continue
		}
		planning, err := store.GetIssuePlanning(issue)
		if err != nil || planning == nil {
			continue
		}
		out[identifier] = planning
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

func decodeLiveSessions(raw map[string]interface{}) map[string]agentruntime.Session {
	return agentruntime.SessionsFromMap(raw)
}

func planningHasPendingRevision(planning *kanban.IssuePlanning) bool {
	return planning != nil && strings.TrimSpace(planning.PendingRevisionNote) != ""
}

func buildLiveSessionFeedEntry(
	store *kanban.Store,
	identifier string,
	session agentruntime.Session,
	running observability.RunningEntry,
	issue *kanban.Issue,
	planning *kanban.IssuePlanning,
	issueTitle string,
	pendingInterrupt *agentruntime.PendingInteraction,
) kanban.SessionFeedEntry {
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
	var pending *agentruntime.PendingInteraction
	if pendingInterrupt != nil {
		cloned := pendingInterrupt.Clone()
		pending = &cloned
		if pending.Kind == agentruntime.PendingInteractionKindAlert {
			status = "blocked"
		} else {
			status = "waiting"
		}
		if pending.LastActivityAt != nil && !pending.LastActivityAt.IsZero() {
			updatedAt = pending.LastActivityAt.UTC()
		}
		if strings.TrimSpace(pending.LastActivity) != "" {
			lastMessage = pending.LastActivity
		}
	}
	if planningHasPendingRevision(planning) && (pending == nil || pending.Kind != agentruntime.PendingInteractionKindAlert) {
		status = "revision_queued"
		lastMessage = queuedPlanRevisionText
		if planning.PendingRevisionRequestedAt != nil && !planning.PendingRevisionRequestedAt.IsZero() {
			updatedAt = planning.PendingRevisionRequestedAt.UTC()
		} else if !planning.UpdatedAt.IsZero() {
			updatedAt = planning.UpdatedAt.UTC()
		}
	}
	planSummary := planningSummary(planning)
	if pending == nil || pending.Kind != agentruntime.PendingInteractionKindAlert {
		if planningStatus, planningMessage, planningUpdatedAt, ok := openPlanningFeedState(planning); ok {
			status = planningStatus
			if planningMessage != "" {
				lastMessage = planningMessage
			}
			if planningUpdatedAt != nil && !planningUpdatedAt.IsZero() {
				updatedAt = planningUpdatedAt.UTC()
			}
		}
	}
	runtimeSurface := kanban.ResolveRuntimeSurface(store, issue, nil, &session, pendingInterrupt, planning)

	return kanban.SessionFeedEntry{
		IssueID:          firstNonEmpty(session.IssueID, running.IssueID),
		IssueIdentifier:  firstNonEmpty(session.IssueIdentifier, identifier, running.Identifier),
		IssueTitle:       issueTitle,
		Source:           "live",
		Active:           true,
		Status:           status,
		Planning:         planSummary,
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
		RuntimeSurface:   runtimeSurface,
	}
}

func buildPersistedSessionFeedEntry(
	store *kanban.Store,
	snapshot kanban.ExecutionSessionSnapshot,
	retry observability.RetryEntry,
	paused observability.PausedEntry,
	issue *kanban.Issue,
	planning *kanban.IssuePlanning,
	issueTitle string,
) kanban.SessionFeedEntry {
	session := snapshot.AppSession
	updatedAt := session.LastTimestamp
	if updatedAt.IsZero() {
		updatedAt = snapshot.UpdatedAt
	}
	errorText := firstNonEmpty(paused.Error, retry.Error, snapshot.Error)
	planApprovalWaiting := isPlanApprovalPendingError(errorText) || isPlanApprovalPendingError(snapshot.RunKind) || isPlanApprovalPendingError(snapshot.StopReason)
	planRevisionQueued := planningHasPendingRevision(planning)
	failureClass := normalizeFailureClass(errorText)
	if failureClass == "" {
		failureClass = normalizeFailureClass(snapshot.RunKind)
	}
	if planApprovalWaiting || planRevisionQueued {
		failureClass = ""
		errorText = ""
	}

	status := "failed"
	switch {
	case planRevisionQueued:
		status = "revision_queued"
	case planApprovalWaiting:
		status = "waiting"
	case strings.TrimSpace(paused.Identifier) != "" || strings.EqualFold(snapshot.RunKind, "retry_paused"):
		status = "paused"
	case strings.EqualFold(snapshot.RunKind, "run_completed"):
		status = "completed"
	case strings.EqualFold(snapshot.RunKind, "run_interrupted"):
		status = "interrupted"
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
	lastMessage := session.LastMessage
	if planRevisionQueued {
		lastMessage = queuedPlanRevisionText
		if planning != nil && planning.PendingRevisionRequestedAt != nil && !planning.PendingRevisionRequestedAt.IsZero() {
			updatedAt = planning.PendingRevisionRequestedAt.UTC()
		} else if planning != nil && !planning.UpdatedAt.IsZero() {
			updatedAt = planning.UpdatedAt.UTC()
		}
	}
	planSummary := planningSummary(planning)
	if planningStatus, planningMessage, planningUpdatedAt, ok := openPlanningFeedState(planning); ok {
		status = planningStatus
		if planningMessage != "" {
			lastMessage = planningMessage
		}
		if planningUpdatedAt != nil && !planningUpdatedAt.IsZero() {
			updatedAt = planningUpdatedAt.UTC()
		}
		failureClass = ""
		errorText = ""
	}
	runtimeSurface := kanban.ResolveRuntimeSurface(store, issue, &snapshot, &session, nil, planning)

	return kanban.SessionFeedEntry{
		IssueID:         snapshot.IssueID,
		IssueIdentifier: firstNonEmpty(snapshot.Identifier, session.IssueIdentifier),
		IssueTitle:      issueTitle,
		Source:          "persisted",
		Active:          false,
		Status:          status,
		Planning:        planSummary,
		Phase:           phase,
		Attempt:         attempt,
		RunKind:         snapshot.RunKind,
		FailureClass:    failureClass,
		UpdatedAt:       updatedAt,
		LastEvent:       session.LastEvent,
		LastMessage:     lastMessage,
		TotalTokens:     session.TotalTokens,
		EventsProcessed: session.EventsProcessed,
		TurnsStarted:    session.TurnsStarted,
		TurnsCompleted:  session.TurnsCompleted,
		Terminal:        session.Terminal,
		TerminalReason:  session.TerminalReason,
		Error:           errorText,
		RuntimeSurface:  runtimeSurface,
	}
}

func planningSummary(planning *kanban.IssuePlanning) *kanban.IssuePlanningSummary {
	if planning == nil {
		return nil
	}
	summary := &kanban.IssuePlanningSummary{
		SessionID:                  planning.SessionID,
		Status:                     planning.Status,
		CurrentVersionNumber:       planning.CurrentVersionNumber,
		CurrentVersion:             planning.CurrentVersion,
		PendingRevisionNote:        planning.PendingRevisionNote,
		PendingRevisionRequestedAt: planning.PendingRevisionRequestedAt,
		OpenedAt:                   planning.OpenedAt,
		UpdatedAt:                  planning.UpdatedAt,
		ClosedAt:                   planning.ClosedAt,
	}
	return summary
}

func openPlanningFeedState(planning *kanban.IssuePlanning) (string, string, *time.Time, bool) {
	if planning == nil {
		return "", "", nil, false
	}
	switch planning.Status {
	case kanban.IssuePlanningStatusDrafting:
		return "active", "Revising the plan.", &planning.UpdatedAt, true
	case kanban.IssuePlanningStatusAwaitingApproval:
		return "waiting", "Plan ready for approval.", &planning.UpdatedAt, true
	case kanban.IssuePlanningStatusRevisionRequested:
		return "revision_queued", queuedPlanRevisionText, &planning.UpdatedAt, true
	default:
		return "", "", nil, false
	}
}

func normalizeFailureClass(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case value == "":
		return ""
	case strings.Contains(value, "plan_approval_pending"):
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

func isPlanApprovalPendingError(value string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(value)), "plan_approval_pending")
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
