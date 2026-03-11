package runtimeview

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type ExecutionProvider interface {
	observability.SnapshotProvider
	observability.SessionProvider
}

var (
	ansiSequencePattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	bareAnsiCodePattern = regexp.MustCompile(`\[[0-9;]*m`)
)

type SessionDisplayHistoryEntry struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Detail     string `json:"detail,omitempty"`
	Expandable bool   `json:"expandable"`
	TokenCount int    `json:"token_count,omitempty"`
	Tone       string `json:"tone,omitempty"`
	EventType  string `json:"event_type,omitempty"`
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
	sessionHistory := []appserver.Event{}
	if liveSession != nil {
		sessionSource = "live"
		session = liveSession
		sessionHistory = append(sessionHistory, liveSession.History...)
	} else if persistedSession != nil {
		sessionSource = "persisted"
		session = persistedSession.AppSession
		sessionHistory = append(sessionHistory, persistedSession.AppSession.History...)
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

	sessionDisplayHistory := buildSessionDisplayHistory(sessionHistory)
	if sessionDisplayHistory == nil {
		sessionDisplayHistory = []SessionDisplayHistoryEntry{}
	}

	payload := map[string]interface{}{
		"issue_id":                issue.ID,
		"identifier":              issue.Identifier,
		"active":                  running != nil,
		"phase":                   phase,
		"attempt_number":          attempt,
		"failure_class":           failureClass,
		"current_error":           currentError,
		"retry_state":             retryState,
		"session_source":          sessionSource,
		"runtime_events":          events,
		"session_display_history": sessionDisplayHistory,
		"runtime_available":       runtimeAvailable,
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

func buildSessionDisplayHistory(history []appserver.Event) []SessionDisplayHistoryEntry {
	if len(history) == 0 {
		return nil
	}
	out := make([]SessionDisplayHistoryEntry, 0, len(history))
	for i := 0; i < len(history); {
		if isCommandExecutionEvent(history[i]) {
			entry, next := buildCommandDisplayEntry(history, i, len(out))
			out = append(out, entry)
			i = next
			continue
		}
		out = append(out, buildGenericDisplayEntry(history[i], len(out)))
		i++
	}
	return out
}

func buildCommandDisplayEntry(history []appserver.Event, start, displayIndex int) (SessionDisplayHistoryEntry, int) {
	group := []appserver.Event{history[start]}
	groupCallID := strings.TrimSpace(history[start].CallID)
	for i := start + 1; i < len(history); i++ {
		next := history[i]
		if !isCommandExecutionEvent(next) {
			break
		}
		nextCallID := strings.TrimSpace(next.CallID)
		if groupCallID != "" && nextCallID != "" && nextCallID != groupCallID {
			break
		}
		if groupCallID == "" && nextCallID != "" && len(group) > 1 {
			break
		}
		group = append(group, next)
		if groupCallID == "" && nextCallID != "" {
			groupCallID = nextCallID
		}
	}
	return summarizeCommandGroup(group, displayIndex), start + len(group)
}

func summarizeCommandGroup(group []appserver.Event, displayIndex int) SessionDisplayHistoryEntry {
	command := ""
	cwd := ""
	totalTokens := 0
	hasOutput := false
	hasStart := false
	hasEnd := false
	hasStderr := false
	var exitCode *int
	outputParts := make([]string, 0, len(group))
	lastType := ""
	callID := ""
	for _, event := range group {
		lastType = strings.TrimSpace(event.Type)
		if strings.TrimSpace(callID) == "" && strings.TrimSpace(event.CallID) != "" {
			callID = strings.TrimSpace(event.CallID)
		}
		if command == "" && strings.TrimSpace(event.Command) != "" {
			command = strings.TrimSpace(event.Command)
		}
		if cwd == "" && strings.TrimSpace(event.CWD) != "" {
			cwd = strings.TrimSpace(event.CWD)
		}
		if event.TotalTokens > 0 {
			totalTokens = event.TotalTokens
		}
		if event.ExitCode != nil {
			code := *event.ExitCode
			exitCode = &code
		}
		eventType := strings.ToLower(strings.TrimSpace(event.Type))
		switch {
		case strings.Contains(eventType, "begin"):
			hasStart = true
		case strings.Contains(eventType, "end"):
			hasEnd = true
		}
		if strings.EqualFold(strings.TrimSpace(event.Stream), "stderr") {
			hasStderr = true
		}
		text := cleanTerminalText(event.Chunk)
		if text == "" {
			text = cleanTerminalText(event.Message)
		}
		if text != "" && (isCommandOutputEvent(event) || eventType == "terminal_interaction") {
			hasOutput = true
			outputParts = append(outputParts, text)
		}
	}

	aggregatedOutput := strings.TrimSpace(strings.Join(outputParts, "\n"))
	summarySource := firstNonEmpty(
		firstMeaningfulLine(aggregatedOutput),
		firstMeaningfulLine(cleanTerminalText(group[len(group)-1].Message)),
		command,
		defaultSummaryForEvent(lastType),
	)

	title := "Command event"
	tone := "default"
	switch {
	case exitCode != nil && *exitCode == 0:
		title = "Command completed"
		tone = "success"
	case exitCode != nil && *exitCode != 0:
		title = fmt.Sprintf("Command failed (exit %d)", *exitCode)
		tone = "error"
	case hasOutput:
		title = "Command output"
	case hasStart:
		title = "Command started"
	case hasEnd:
		title = "Command finished"
	}
	if tone == "default" && (hasStderr || isErrorText(summarySource)) {
		tone = "error"
	}

	detailParts := make([]string, 0, 6)
	if command != "" {
		detailParts = append(detailParts, "$ "+command)
	}
	if cwd != "" {
		detailParts = append(detailParts, "cwd: "+cwd)
	}
	if aggregatedOutput != "" {
		if len(detailParts) > 0 {
			detailParts = append(detailParts, "")
		}
		detailParts = append(detailParts, aggregatedOutput)
	}
	if exitCode != nil {
		if len(detailParts) > 0 {
			detailParts = append(detailParts, "")
		}
		detailParts = append(detailParts, fmt.Sprintf("exit code: %d", *exitCode))
	}
	detail := strings.TrimSpace(strings.Join(detailParts, "\n"))
	summary := summarizeText(summarySource, 180)
	expandable := detail != "" && (strings.Contains(detail, "\n") || len(detail) > len(summary)+24)

	id := fmt.Sprintf("session-command-%d", displayIndex)
	if callID != "" {
		id = fmt.Sprintf("session-command-%s-%d", callID, displayIndex)
	}
	entry := SessionDisplayHistoryEntry{
		ID:         id,
		Kind:       "command",
		Title:      title,
		Summary:    summary,
		Detail:     detail,
		Expandable: expandable,
		Tone:       tone,
		EventType:  lastType,
	}
	if totalTokens > 0 {
		entry.TokenCount = totalTokens
	}
	return entry
}

func buildGenericDisplayEntry(event appserver.Event, displayIndex int) SessionDisplayHistoryEntry {
	cleanMessage := cleanTerminalText(event.Message)
	summary := summarizeText(firstNonEmpty(firstMeaningfulLine(cleanMessage), defaultSummaryForEvent(event.Type)), 180)
	detail := strings.TrimSpace(cleanMessage)
	expandable := detail != "" && (strings.Contains(detail, "\n") || len(detail) > len(summary)+24)
	if !expandable {
		detail = ""
	}
	tone := "default"
	if isErrorEventType(event.Type) || isErrorText(cleanMessage) {
		tone = "error"
	}
	id := fmt.Sprintf("session-event-%d", displayIndex)
	if strings.TrimSpace(event.CallID) != "" {
		id = fmt.Sprintf("session-event-%s-%d", strings.TrimSpace(event.CallID), displayIndex)
	}
	entry := SessionDisplayHistoryEntry{
		ID:         id,
		Kind:       "event",
		Title:      titleForEventType(event.Type),
		Summary:    summary,
		Detail:     detail,
		Expandable: expandable,
		Tone:       tone,
		EventType:  strings.TrimSpace(event.Type),
	}
	if event.TotalTokens > 0 {
		entry.TokenCount = event.TotalTokens
	}
	return entry
}

func isCommandExecutionEvent(event appserver.Event) bool {
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	switch {
	case eventType == "":
		return false
	case strings.Contains(eventType, "commandexecution"):
		return true
	case strings.HasPrefix(eventType, "exec_command_"):
		return true
	case eventType == "terminal_interaction":
		return true
	default:
		return false
	}
}

func isCommandOutputEvent(event appserver.Event) bool {
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	switch {
	case strings.Contains(eventType, "outputdelta"):
		return true
	case strings.Contains(eventType, "output_delta"):
		return true
	default:
		return strings.TrimSpace(event.Chunk) != ""
	}
}

func titleForEventType(eventType string) string {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "turn.started":
		return "Turn started"
	case "turn.completed":
		return "Turn completed"
	case "thread.started":
		return "Thread started"
	case "thread.tokenusage.updated":
		return "Token usage updated"
	case "item.started":
		return "Item started"
	case "item.completed":
		return "Item completed"
	default:
		return humanizeEventType(eventType)
	}
}

func defaultSummaryForEvent(eventType string) string {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "turn.started":
		return "Turn execution started."
	case "turn.completed":
		return "Turn execution completed."
	case "thread.tokenusage.updated":
		return "Token usage was updated."
	case "item.started":
		return "Item execution started."
	case "item.completed":
		return "Item execution completed."
	default:
		return "Execution signal received."
	}
}

func humanizeEventType(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Event"
	}
	replacer := strings.NewReplacer(".", " ", "/", " ", "_", " ")
	base := replacer.Replace(strings.TrimSpace(value))
	var b strings.Builder
	var previous rune
	for i, r := range base {
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(previous) {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
		previous = r
	}
	words := strings.Fields(strings.ToLower(b.String()))
	for i, word := range words {
		if len(word) == 0 {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

func cleanTerminalText(value string) string {
	text := strings.ReplaceAll(value, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = ansiSequencePattern.ReplaceAllString(text, "")
	text = bareAnsiCodePattern.ReplaceAllString(text, "")
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, text)
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func firstMeaningfulLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func summarizeText(value string, max int) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= max || max <= 0 {
		return trimmed
	}
	if max <= 1 {
		return trimmed[:max]
	}
	return strings.TrimSpace(trimmed[:max-1]) + "..."
}

func isErrorEventType(eventType string) bool {
	value := strings.ToLower(strings.TrimSpace(eventType))
	switch {
	case value == "":
		return false
	case strings.Contains(value, "failed"):
		return true
	case strings.Contains(value, "error"):
		return true
	case strings.Contains(value, "cancelled"):
		return true
	default:
		return false
	}
}

func isErrorText(value string) bool {
	clean := strings.ToLower(strings.TrimSpace(value))
	switch {
	case clean == "":
		return false
	case strings.Contains(clean, "error"):
		return true
	case strings.Contains(clean, "failed"):
		return true
	case strings.Contains(clean, "exception"):
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
