package kanban

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/olhapi/maestro/internal/appserver"
)

var activityANSIPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func (s *Store) ApplyIssueActivityEvent(issueID, identifier string, attempt int, event appserver.ActivityEvent) error {
	if strings.TrimSpace(issueID) == "" {
		return fmt.Errorf("missing issue_id")
	}
	logicalID, ok := issueActivityLogicalID(attempt, event)
	if !ok {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	existing, err := s.getIssueActivityEntryTx(tx, logicalID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	entry, keep := projectIssueActivityEntry(issueID, identifier, logicalID, attempt, now, event, existing)
	if !keep {
		return nil
	}

	if err := s.appendIssueActivityUpdateTx(tx, issueID, logicalID, now, event); err != nil {
		return err
	}
	if err := s.upsertIssueActivityEntryTx(tx, entry, existing != nil); err != nil {
		return err
	}
	if err := s.appendChangeTx(tx, "issue_activity", issueID, entry.ID, map[string]interface{}{
		"issue_id":   issueID,
		"identifier": identifier,
		"attempt":    attempt,
		"entry_id":   logicalID,
		"event_type": event.Type,
	}); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) getIssueActivityEntryTx(tx *sql.Tx, logicalID string) (*IssueActivityEntry, error) {
	var entry IssueActivityEntry
	var raw string
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	err := tx.QueryRow(`
		SELECT seq, logical_id, issue_id, identifier, attempt, thread_id, turn_id, item_id, kind, item_type, phase, entry_status, tier, title, summary, detail, tone, expandable, started_at, completed_at, created_at, updated_at, raw_payload_json
		FROM issue_activity_entries
		WHERE logical_id = ?`, logicalID).Scan(
		&entry.Seq,
		&entry.ID,
		&entry.IssueID,
		&entry.Identifier,
		&entry.Attempt,
		&entry.ThreadID,
		&entry.TurnID,
		&entry.ItemID,
		&entry.Kind,
		&entry.ItemType,
		&entry.Phase,
		&entry.Status,
		&entry.Tier,
		&entry.Title,
		&entry.Summary,
		&entry.Detail,
		&entry.Tone,
		&entry.Expandable,
		&startedAt,
		&completedAt,
		&entry.CreatedAt,
		&entry.UpdatedAt,
		&raw,
	)
	if err != nil {
		return nil, err
	}
	if startedAt.Valid {
		ts := startedAt.Time.UTC()
		entry.StartedAt = &ts
	}
	if completedAt.Valid {
		ts := completedAt.Time.UTC()
		entry.CompletedAt = &ts
	}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &entry.RawPayload)
	}
	return &entry, nil
}

func (s *Store) appendIssueActivityUpdateTx(tx *sql.Tx, issueID, logicalID string, now time.Time, event appserver.ActivityEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO issue_activity_updates (issue_id, entry_id, event_type, event_ts, payload_json)
		VALUES (?, ?, ?, ?, ?)`,
		issueID,
		logicalID,
		event.Type,
		now,
		string(payload),
	)
	return err
}

func (s *Store) upsertIssueActivityEntryTx(tx *sql.Tx, entry IssueActivityEntry, existing bool) error {
	raw, err := json.Marshal(entry.RawPayload)
	if err != nil {
		return err
	}
	if !existing {
		_, err = tx.Exec(`
			INSERT INTO issue_activity_entries (issue_id, identifier, logical_id, attempt, thread_id, turn_id, item_id, kind, item_type, phase, entry_status, tier, title, summary, detail, tone, expandable, started_at, completed_at, created_at, updated_at, raw_payload_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			entry.IssueID,
			entry.Identifier,
			entry.ID,
			entry.Attempt,
			entry.ThreadID,
			entry.TurnID,
			entry.ItemID,
			entry.Kind,
			entry.ItemType,
			entry.Phase,
			entry.Status,
			entry.Tier,
			entry.Title,
			entry.Summary,
			entry.Detail,
			entry.Tone,
			entry.Expandable,
			entry.StartedAt,
			entry.CompletedAt,
			entry.CreatedAt,
			entry.UpdatedAt,
			string(raw),
		)
		return err
	}
	_, err = tx.Exec(`
		UPDATE issue_activity_entries
		SET identifier = ?, attempt = ?, thread_id = ?, turn_id = ?, item_id = ?, kind = ?, item_type = ?, phase = ?, entry_status = ?, tier = ?, title = ?, summary = ?, detail = ?, tone = ?, expandable = ?, started_at = ?, completed_at = ?, updated_at = ?, raw_payload_json = ?
		WHERE logical_id = ?`,
		entry.Identifier,
		entry.Attempt,
		entry.ThreadID,
		entry.TurnID,
		entry.ItemID,
		entry.Kind,
		entry.ItemType,
		entry.Phase,
		entry.Status,
		entry.Tier,
		entry.Title,
		entry.Summary,
		entry.Detail,
		entry.Tone,
		entry.Expandable,
		entry.StartedAt,
		entry.CompletedAt,
		entry.UpdatedAt,
		string(raw),
		entry.ID,
	)
	return err
}

func (s *Store) ListIssueActivityEntries(issueID string) ([]IssueActivityEntry, error) {
	if strings.TrimSpace(issueID) == "" {
		return []IssueActivityEntry{}, nil
	}
	rows, err := s.db.Query(`
		SELECT seq, logical_id, issue_id, identifier, attempt, thread_id, turn_id, item_id, kind, item_type, phase, entry_status, tier, title, summary, detail, tone, expandable, started_at, completed_at, created_at, updated_at, raw_payload_json
		FROM issue_activity_entries
		WHERE issue_id = ?
		ORDER BY attempt ASC, seq ASC`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []IssueActivityEntry{}
	for rows.Next() {
		var entry IssueActivityEntry
		var raw string
		var startedAt sql.NullTime
		var completedAt sql.NullTime
		if err := rows.Scan(
			&entry.Seq,
			&entry.ID,
			&entry.IssueID,
			&entry.Identifier,
			&entry.Attempt,
			&entry.ThreadID,
			&entry.TurnID,
			&entry.ItemID,
			&entry.Kind,
			&entry.ItemType,
			&entry.Phase,
			&entry.Status,
			&entry.Tier,
			&entry.Title,
			&entry.Summary,
			&entry.Detail,
			&entry.Tone,
			&entry.Expandable,
			&startedAt,
			&completedAt,
			&entry.CreatedAt,
			&entry.UpdatedAt,
			&raw,
		); err != nil {
			return nil, err
		}
		if startedAt.Valid {
			ts := startedAt.Time.UTC()
			entry.StartedAt = &ts
		}
		if completedAt.Valid {
			ts := completedAt.Time.UTC()
			entry.CompletedAt = &ts
		}
		if raw != "" {
			_ = json.Unmarshal([]byte(raw), &entry.RawPayload)
		}
		out = append(out, entry)
	}
	return out, nil
}

func projectIssueActivityEntry(issueID, identifier, logicalID string, attempt int, now time.Time, event appserver.ActivityEvent, existing *IssueActivityEntry) (IssueActivityEntry, bool) {
	entry := IssueActivityEntry{
		ID:         logicalID,
		IssueID:    issueID,
		Identifier: identifier,
		Attempt:    attempt,
		ThreadID:   strings.TrimSpace(event.ThreadID),
		TurnID:     strings.TrimSpace(event.TurnID),
		ItemID:     strings.TrimSpace(event.ItemID),
		CreatedAt:  now,
		UpdatedAt:  now,
		RawPayload: activityRawPayload(event),
	}
	if existing != nil {
		entry = *existing
		entry.Identifier = identifier
		entry.Attempt = attempt
		entry.ThreadID = firstNonEmptyString(strings.TrimSpace(event.ThreadID), entry.ThreadID)
		entry.TurnID = firstNonEmptyString(strings.TrimSpace(event.TurnID), entry.TurnID)
		entry.ItemID = firstNonEmptyString(strings.TrimSpace(event.ItemID), entry.ItemID)
		entry.UpdatedAt = now
		entry.RawPayload = activityRawPayload(event)
	}

	switch event.Type {
	case "item.started":
		return projectStartedItem(entry, now, event)
	case "item.completed":
		return projectCompletedItem(entry, now, event)
	case "item.agentMessage.delta":
		return projectAgentDelta(entry, event), true
	case "item.plan.delta":
		return projectPlanDelta(entry, event), true
	case "item.commandExecution.outputDelta":
		return projectCommandDelta(entry, event), true
	case "item.commandExecution.terminalInteraction":
		return projectTerminalInteraction(entry, event), true
	case "turn.started", "turn.completed", "turn.failed", "turn.cancelled":
		return projectTurnStatus(entry, now, event), true
	case "item.commandExecution.requestApproval", "item.fileChange.requestApproval", "execCommandApproval", "applyPatchApproval":
		return projectApprovalStatus(entry, now, event), true
	case "item.commandExecution.approvalResolved", "item.fileChange.approvalResolved", "execCommandApproval.resolved", "applyPatchApproval.resolved":
		return projectApprovalResolved(entry, now, event), true
	case "item.tool.requestUserInput":
		return projectInputStatus(entry, now, event), true
	case "item.tool.userInputSubmitted":
		return projectInputResolved(entry, now, event), true
	default:
		return IssueActivityEntry{}, false
	}
}

func projectStartedItem(entry IssueActivityEntry, now time.Time, event appserver.ActivityEvent) (IssueActivityEntry, bool) {
	itemType := strings.TrimSpace(event.ItemType)
	switch itemType {
	case "agentMessage":
		entry.Kind = "agent"
		entry.Tier = "primary"
		entry.ItemType = itemType
		entry.Phase = strings.TrimSpace(event.ItemPhase)
		entry.Status = "started"
		entry.Title = activityTitleForAgentPhase(entry.Phase)
		entry.Summary = firstNonEmptyString(agentText(event.Item), entry.Summary, "Agent update")
		entry.Detail = ""
		entry.Tone = activityToneForAgentPhase(entry.Phase)
		entry.Expandable = false
		if entry.StartedAt == nil {
			ts := now
			entry.StartedAt = &ts
		}
		entry.CompletedAt = nil
		return entry, true
	case "commandExecution":
		entry.Kind = "command"
		entry.Tier = "primary"
		entry.ItemType = itemType
		entry.Status = "started"
		entry.Title = "Command started"
		entry.Summary = firstNonEmptyString(strings.TrimSpace(event.Command), entry.Summary, "Command started")
		entry.Detail = buildCommandDetail(strings.TrimSpace(event.Command), strings.TrimSpace(event.CWD), existingCommandOutput(entry.Detail), nil)
		entry.Tone = "default"
		entry.Expandable = activityEntryExpandable(entry.Detail, entry.Summary)
		if entry.StartedAt == nil {
			ts := now
			entry.StartedAt = &ts
		}
		entry.CompletedAt = nil
		return entry, true
	default:
		return projectSecondaryItem(entry, now, event, "started"), true
	}
}

func projectCompletedItem(entry IssueActivityEntry, now time.Time, event appserver.ActivityEvent) (IssueActivityEntry, bool) {
	itemType := strings.TrimSpace(event.ItemType)
	switch itemType {
	case "agentMessage":
		entry.Kind = "agent"
		entry.Tier = "primary"
		entry.ItemType = itemType
		entry.Phase = strings.TrimSpace(event.ItemPhase)
		entry.Status = "completed"
		entry.Title = activityTitleForAgentPhase(entry.Phase)
		entry.Summary = firstNonEmptyString(agentText(event.Item), entry.Summary, "Agent update")
		entry.Detail = ""
		entry.Tone = activityToneForAgentPhase(entry.Phase)
		entry.Expandable = false
		if entry.StartedAt == nil {
			ts := now
			entry.StartedAt = &ts
		}
		ts := now
		entry.CompletedAt = &ts
		return entry, true
	case "commandExecution":
		entry.Kind = "command"
		entry.Tier = "primary"
		entry.ItemType = itemType
		entry.Status = firstNonEmptyString(strings.TrimSpace(event.Status), commandCompletionStatus(event.ExitCode))
		entry.Title, entry.Tone = commandCompletionTitleAndTone(event.ExitCode, entry.Status)
		command := firstNonEmptyString(strings.TrimSpace(event.Command), entry.Summary)
		output := firstNonEmptyString(cleanActivityText(event.AggregatedOutput), existingCommandOutput(entry.Detail))
		entry.Summary = firstNonEmptyString(command, firstMeaningfulLine(output), "Command completed")
		entry.Detail = buildCommandDetail(command, strings.TrimSpace(event.CWD), output, event.ExitCode)
		entry.Expandable = activityEntryExpandable(entry.Detail, entry.Summary)
		if entry.StartedAt == nil {
			ts := now
			entry.StartedAt = &ts
		}
		ts := now
		entry.CompletedAt = &ts
		return entry, true
	default:
		return projectSecondaryItem(entry, now, event, "completed"), true
	}
}

func projectAgentDelta(entry IssueActivityEntry, event appserver.ActivityEvent) IssueActivityEntry {
	entry.Kind = "agent"
	entry.Tier = "primary"
	entry.ItemType = "agentMessage"
	if entry.Title == "" {
		entry.Title = activityTitleForAgentPhase(entry.Phase)
	}
	entry.Status = "in_progress"
	entry.Summary = appendActivityText(entry.Summary, event.Delta)
	entry.Tone = activityToneForAgentPhase(entry.Phase)
	entry.Expandable = false
	return entry
}

func projectPlanDelta(entry IssueActivityEntry, event appserver.ActivityEvent) IssueActivityEntry {
	entry.Kind = "secondary"
	entry.Tier = "secondary"
	entry.ItemType = "plan"
	entry.Status = "in_progress"
	if entry.Title == "" {
		entry.Title = "Plan"
	}
	entry.Summary = appendActivityText(entry.Summary, event.Delta)
	entry.Detail = entry.Summary
	entry.Tone = "default"
	entry.Expandable = activityEntryExpandable(entry.Detail, entry.Summary)
	return entry
}

func projectCommandDelta(entry IssueActivityEntry, event appserver.ActivityEvent) IssueActivityEntry {
	entry.Kind = "command"
	entry.Tier = "primary"
	entry.ItemType = "commandExecution"
	entry.Status = "in_progress"
	entry.Title = "Command output"
	output := appendActivityText(existingCommandOutput(entry.Detail), event.Delta)
	command := existingCommandFromDetail(entry.Detail)
	if command == "" {
		command = entry.Summary
	}
	entry.Detail = buildCommandDetail(command, existingCommandCWD(entry.Detail), output, nil)
	if command != "" {
		entry.Summary = command
	} else {
		entry.Summary = firstNonEmptyString(firstMeaningfulLine(output), "Command output")
	}
	entry.Tone = "default"
	entry.Expandable = activityEntryExpandable(entry.Detail, entry.Summary)
	return entry
}

func projectTerminalInteraction(entry IssueActivityEntry, event appserver.ActivityEvent) IssueActivityEntry {
	entry.Kind = "command"
	entry.Tier = "primary"
	entry.ItemType = "commandExecution"
	entry.Status = "in_progress"
	if entry.Title == "" {
		entry.Title = "Command output"
	}
	command := existingCommandFromDetail(entry.Detail)
	if command == "" {
		command = entry.Summary
	}
	cwd := existingCommandCWD(entry.Detail)
	output := existingCommandOutput(entry.Detail)
	input := cleanActivityText(event.Stdin)
	if input != "" {
		if output != "" {
			output += "\n"
		}
		output += "> " + input
	}
	entry.Detail = buildCommandDetail(command, cwd, output, nil)
	entry.Summary = firstNonEmptyString(command, entry.Summary, "Command interaction")
	entry.Tone = "default"
	entry.Expandable = activityEntryExpandable(entry.Detail, entry.Summary)
	return entry
}

func projectTurnStatus(entry IssueActivityEntry, now time.Time, event appserver.ActivityEvent) IssueActivityEntry {
	entry.Kind = "status"
	entry.Tier = "primary"
	entry.ItemType = ""
	entry.Status = strings.TrimPrefix(event.Type, "turn.")
	entry.Title = humanizeActivityLabel(event.Type)
	entry.Summary = defaultActivitySummary(event.Type)
	entry.Detail = ""
	entry.Tone = activityToneForStatus(event.Type)
	entry.Expandable = false
	if strings.HasSuffix(event.Type, ".started") {
		ts := now
		entry.StartedAt = &ts
	}
	if strings.HasSuffix(event.Type, ".completed") || strings.HasSuffix(event.Type, ".failed") || strings.HasSuffix(event.Type, ".cancelled") {
		ts := now
		entry.CompletedAt = &ts
	}
	return entry
}

func projectApprovalStatus(entry IssueActivityEntry, now time.Time, event appserver.ActivityEvent) IssueActivityEntry {
	entry.Kind = "status"
	entry.Tier = "primary"
	entry.Status = "approval_required"
	entry.Title = "Approval required"
	target := firstNonEmptyString(strings.TrimSpace(event.Command), strings.TrimSpace(event.Reason), "The agent requested approval.")
	entry.Summary = target
	entry.Detail = approvalDetail(event.Raw)
	entry.Tone = "error"
	entry.Expandable = activityEntryExpandable(entry.Detail, entry.Summary)
	ts := now
	entry.StartedAt = &ts
	return entry
}

func projectInputStatus(entry IssueActivityEntry, now time.Time, event appserver.ActivityEvent) IssueActivityEntry {
	entry.Kind = "status"
	entry.Tier = "primary"
	entry.Status = "input_required"
	entry.Title = "User input required"
	entry.Summary = inputRequestSummary(event.Raw)
	entry.Detail = inputRequestDetail(event.Raw)
	entry.Tone = "error"
	entry.Expandable = activityEntryExpandable(entry.Detail, entry.Summary)
	ts := now
	entry.StartedAt = &ts
	return entry
}

func projectApprovalResolved(entry IssueActivityEntry, now time.Time, event appserver.ActivityEvent) IssueActivityEntry {
	entry.Kind = "status"
	entry.Tier = "primary"
	decision := strings.TrimSpace(event.Status)
	entry.Status = firstNonEmptyString(decision, "approval_resolved")
	entry.Title = "Approval resolved"
	entry.Summary = approvalDecisionSummary(decision)
	entry.Detail = approvalResponseDetail(event.Raw)
	entry.Tone = approvalDecisionTone(decision)
	entry.Expandable = activityEntryExpandable(entry.Detail, entry.Summary)
	ts := now
	if entry.StartedAt == nil {
		entry.StartedAt = &ts
	}
	entry.CompletedAt = &ts
	return entry
}

func projectInputResolved(entry IssueActivityEntry, now time.Time, event appserver.ActivityEvent) IssueActivityEntry {
	entry.Kind = "status"
	entry.Tier = "primary"
	entry.Status = "input_submitted"
	entry.Title = "User input submitted"
	entry.Summary = inputResponseSummary(event.Raw)
	entry.Detail = inputResponseDetail(event.Raw)
	entry.Tone = "success"
	entry.Expandable = activityEntryExpandable(entry.Detail, entry.Summary)
	ts := now
	if entry.StartedAt == nil {
		entry.StartedAt = &ts
	}
	entry.CompletedAt = &ts
	return entry
}

func projectSecondaryItem(entry IssueActivityEntry, now time.Time, event appserver.ActivityEvent, status string) IssueActivityEntry {
	entry.Kind = "secondary"
	entry.Tier = "secondary"
	entry.ItemType = strings.TrimSpace(event.ItemType)
	entry.Phase = strings.TrimSpace(event.ItemPhase)
	entry.Status = status
	entry.Title = humanizeActivityLabel(entry.ItemType)
	entry.Summary = secondaryItemSummary(entry.ItemType, event.Item)
	entry.Detail = secondaryItemDetail(event.Item)
	entry.Tone = "default"
	entry.Expandable = activityEntryExpandable(entry.Detail, entry.Summary)
	if entry.StartedAt == nil {
		ts := now
		entry.StartedAt = &ts
	}
	if status == "completed" {
		ts := now
		entry.CompletedAt = &ts
	}
	return entry
}

func issueActivityLogicalID(attempt int, event appserver.ActivityEvent) (string, bool) {
	threadID := strings.TrimSpace(event.ThreadID)
	turnID := strings.TrimSpace(event.TurnID)
	itemID := strings.TrimSpace(event.ItemID)
	switch event.Type {
	case "item.started", "item.completed", "item.agentMessage.delta", "item.plan.delta", "item.commandExecution.outputDelta", "item.commandExecution.terminalInteraction":
		if itemID == "" {
			return "", false
		}
		return fmt.Sprintf("attempt:%d:item:%s:%s:%s", attempt, threadID, turnID, itemID), true
	case "turn.started", "turn.completed", "turn.failed", "turn.cancelled":
		if turnID == "" {
			return "", false
		}
		return fmt.Sprintf("attempt:%d:status:%s:%s:%s", attempt, threadID, turnID, event.Type), true
	case "item.commandExecution.requestApproval", "item.fileChange.requestApproval", "execCommandApproval", "applyPatchApproval", "item.tool.requestUserInput":
		suffix := firstNonEmptyString(strings.TrimSpace(event.RequestID), itemID)
		if suffix == "" {
			return "", false
		}
		return fmt.Sprintf("attempt:%d:status:%s:%s:%s", attempt, threadID, turnID, suffix), true
	case "item.commandExecution.approvalResolved", "item.fileChange.approvalResolved", "execCommandApproval.resolved", "applyPatchApproval.resolved", "item.tool.userInputSubmitted":
		suffix := firstNonEmptyString(strings.TrimSpace(event.RequestID), itemID)
		if suffix == "" {
			return "", false
		}
		return fmt.Sprintf("attempt:%d:status:%s:%s:%s", attempt, threadID, turnID, suffix), true
	default:
		return "", false
	}
}

func activityRawPayload(event appserver.ActivityEvent) map[string]interface{} {
	payload := map[string]interface{}{
		"type":      event.Type,
		"thread_id": event.ThreadID,
		"turn_id":   event.TurnID,
	}
	if event.RequestID != "" {
		payload["request_id"] = event.RequestID
	}
	if event.ItemID != "" {
		payload["item_id"] = event.ItemID
	}
	if event.ItemType != "" {
		payload["item_type"] = event.ItemType
	}
	if event.ItemPhase != "" {
		payload["item_phase"] = event.ItemPhase
	}
	if event.Delta != "" {
		payload["delta"] = event.Delta
	}
	if event.Stdin != "" {
		payload["stdin"] = event.Stdin
	}
	if event.ProcessID != "" {
		payload["process_id"] = event.ProcessID
	}
	if event.Command != "" {
		payload["command"] = event.Command
	}
	if event.CWD != "" {
		payload["cwd"] = event.CWD
	}
	if event.AggregatedOutput != "" {
		payload["aggregated_output"] = event.AggregatedOutput
	}
	if event.Status != "" {
		payload["status"] = event.Status
	}
	if event.Reason != "" {
		payload["reason"] = event.Reason
	}
	if event.ExitCode != nil {
		payload["exit_code"] = *event.ExitCode
	}
	if event.Item != nil {
		payload["item"] = event.Item
	}
	if event.Raw != nil {
		payload["raw"] = event.Raw
	}
	return payload
}

func buildCommandDetail(command, cwd, output string, exitCode *int) string {
	parts := []string{}
	command = strings.TrimSpace(command)
	cwd = strings.TrimSpace(cwd)
	output = strings.TrimSpace(output)
	if command != "" {
		parts = append(parts, "$ "+command)
	}
	if cwd != "" {
		parts = append(parts, "cwd: "+cwd)
	}
	if output != "" {
		if len(parts) > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, output)
	}
	if exitCode != nil {
		if len(parts) > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, fmt.Sprintf("exit code: %d", *exitCode))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func existingCommandOutput(detail string) string {
	text := strings.TrimSpace(detail)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	start := 0
	for start < len(lines) {
		line := strings.TrimSpace(lines[start])
		if strings.HasPrefix(line, "$ ") || strings.HasPrefix(line, "cwd: ") {
			start++
			continue
		}
		break
	}
	if start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	if end > start && strings.HasPrefix(strings.TrimSpace(lines[end-1]), "exit code: ") {
		end--
		if end > start && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
	}
	if start >= end {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func existingCommandFromDetail(detail string) string {
	lines := strings.Split(strings.TrimSpace(detail), "\n")
	if len(lines) == 0 {
		return ""
	}
	if strings.HasPrefix(lines[0], "$ ") {
		return strings.TrimSpace(strings.TrimPrefix(lines[0], "$ "))
	}
	return ""
}

func existingCommandCWD(detail string) string {
	lines := strings.Split(strings.TrimSpace(detail), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "cwd: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "cwd: "))
		}
	}
	return ""
}

func commandCompletionStatus(exitCode *int) string {
	if exitCode == nil {
		return "completed"
	}
	if *exitCode == 0 {
		return "completed"
	}
	return "failed"
}

func commandCompletionTitleAndTone(exitCode *int, status string) (string, string) {
	if exitCode != nil && *exitCode != 0 {
		return fmt.Sprintf("Command failed (exit %d)", *exitCode), "error"
	}
	if strings.EqualFold(status, "failed") {
		return "Command failed", "error"
	}
	return "Command completed", "success"
}

func agentText(item map[string]interface{}) string {
	if item == nil {
		return ""
	}
	return cleanActivityText(firstString(item, "text"))
}

func secondaryItemSummary(itemType string, item map[string]interface{}) string {
	switch itemType {
	case "plan":
		return firstNonEmptyString(cleanActivityText(firstString(item, "text")), "Plan updated.")
	case "reasoning":
		values := stringSlice(item["summary"])
		if len(values) > 0 {
			return cleanActivityText(strings.Join(values, "\n"))
		}
		return "Reasoning updated."
	case "fileChange":
		if changes, ok := item["changes"].([]interface{}); ok {
			return fmt.Sprintf("%d file change(s).", len(changes))
		}
		return "File change ready."
	case "mcpToolCall", "dynamicToolCall", "collabAgentToolCall":
		tool := cleanActivityText(firstString(item, "tool"))
		status := cleanActivityText(firstString(item, "status"))
		return firstNonEmptyString(strings.TrimSpace(tool+" "+status), humanizeActivityLabel(itemType))
	case "webSearch":
		return firstNonEmptyString(cleanActivityText(firstString(item, "query")), "Web search executed.")
	case "imageView":
		return firstNonEmptyString(cleanActivityText(firstString(item, "path")), "Image viewed.")
	case "enteredReviewMode", "exitedReviewMode":
		return firstNonEmptyString(cleanActivityText(firstString(item, "review")), humanizeActivityLabel(itemType))
	case "imageGeneration":
		return firstNonEmptyString(cleanActivityText(firstString(item, "result")), "Image generated.")
	case "contextCompaction":
		return "Context compacted."
	default:
		return humanizeActivityLabel(itemType)
	}
}

func secondaryItemDetail(item map[string]interface{}) string {
	if item == nil {
		return ""
	}
	body, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return ""
	}
	return string(body)
}

func approvalDetail(raw map[string]interface{}) string {
	if raw == nil {
		return ""
	}
	params, _ := raw["params"].(map[string]interface{})
	if params == nil {
		return ""
	}
	body, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		return ""
	}
	return string(body)
}

func inputRequestSummary(raw map[string]interface{}) string {
	params, _ := raw["params"].(map[string]interface{})
	if params == nil {
		return "The agent requested user input."
	}
	questions, _ := params["questions"].([]interface{})
	for _, rawQuestion := range questions {
		question, _ := rawQuestion.(map[string]interface{})
		if prompt := cleanActivityText(firstString(question, "question")); prompt != "" {
			return prompt
		}
	}
	return "The agent requested user input."
}

func inputRequestDetail(raw map[string]interface{}) string {
	params, _ := raw["params"].(map[string]interface{})
	if params == nil {
		return ""
	}
	body, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		return ""
	}
	return string(body)
}

func approvalDecisionSummary(decision string) string {
	decision = strings.TrimSpace(decision)
	switch {
	case decision == "accept":
		return "Operator approved the request once."
	case decision == "acceptForSession":
		return "Operator approved the request for the rest of the session."
	case decision == "approved":
		return "Operator approved the request once."
	case decision == "approved_for_session":
		return "Operator approved the request for the rest of the session."
	case decision == "accept_with_execpolicy_amendment":
		return "Operator approved the request and stored the matching exec rule."
	case strings.HasPrefix(decision, "network_policy_allow_"):
		return "Operator approved the request and stored an allow network rule."
	case strings.HasPrefix(decision, "network_policy_deny_"):
		return "Operator denied the request and stored a deny network rule."
	case decision == "decline":
		return "Operator declined the request and allowed the turn to continue."
	case decision == "denied":
		return "Operator denied the request and allowed the turn to continue."
	case decision == "cancel":
		return "Operator cancelled the request and interrupted the turn."
	case decision == "abort":
		return "Operator aborted the request and interrupted the turn."
	default:
		return "Operator resolved the request."
	}
}

func approvalDecisionTone(decision string) string {
	decision = strings.TrimSpace(decision)
	switch {
	case decision == "accept",
		decision == "acceptForSession",
		decision == "approved",
		decision == "approved_for_session",
		decision == "accept_with_execpolicy_amendment",
		strings.HasPrefix(decision, "network_policy_allow_"):
		return "success"
	default:
		return "error"
	}
}

func approvalResponseDetail(raw map[string]interface{}) string {
	if raw == nil {
		return ""
	}
	body, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return ""
	}
	return string(body)
}

func inputResponseSummary(raw map[string]interface{}) string {
	answers, _ := raw["answers"].(map[string]interface{})
	if len(answers) == 0 {
		return "Operator submitted input."
	}
	for _, value := range answers {
		vals, _ := value.([]string)
		if len(vals) > 0 {
			return cleanActivityText(vals[0])
		}
		generic, _ := value.([]interface{})
		if len(generic) > 0 {
			if first, ok := generic[0].(string); ok {
				return cleanActivityText(first)
			}
		}
	}
	return "Operator submitted input."
}

func inputResponseDetail(raw map[string]interface{}) string {
	if raw == nil {
		return ""
	}
	body, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return ""
	}
	return string(body)
}

func appendActivityText(current, delta string) string {
	delta = cleanActivityTextPreserveWhitespace(delta)
	if delta == "" {
		return current
	}
	if current == "" {
		return delta
	}
	return current + delta
}

func cleanActivityText(value string) string {
	text := cleanActivityTextPreserveWhitespace(value)
	text = strings.TrimSpace(text)
	return text
}

func cleanActivityTextPreserveWhitespace(value string) string {
	text := strings.ReplaceAll(value, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = activityANSIPattern.ReplaceAllString(text, "")
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
	return text
}

func activityEntryExpandable(detail, summary string) bool {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return false
	}
	return strings.Contains(detail, "\n") || len(detail) > len(strings.TrimSpace(summary))+24
}

func firstMeaningfulLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func activityTitleForAgentPhase(phase string) string {
	if strings.EqualFold(strings.TrimSpace(phase), "final_answer") {
		return "Final answer"
	}
	return "Agent update"
}

func activityToneForAgentPhase(phase string) string {
	if strings.EqualFold(strings.TrimSpace(phase), "final_answer") {
		return "success"
	}
	return "default"
}

func activityToneForStatus(eventType string) string {
	switch eventType {
	case "turn.completed":
		return "success"
	case "turn.failed", "turn.cancelled":
		return "error"
	default:
		return "default"
	}
}

func defaultActivitySummary(eventType string) string {
	switch eventType {
	case "turn.started":
		return "Turn execution started."
	case "turn.completed":
		return "Turn execution completed."
	case "turn.failed":
		return "Turn execution failed."
	case "turn.cancelled":
		return "Turn execution was cancelled."
	default:
		return humanizeActivityLabel(eventType)
	}
}

func humanizeActivityLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Activity"
	}
	replacer := strings.NewReplacer(".", " ", "/", " ", "_", " ")
	base := replacer.Replace(value)
	words := strings.Fields(base)
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstString(m map[string]interface{}, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, key := range keys {
		raw, ok := m[key]
		if !ok {
			continue
		}
		if text := asString(raw); text != "" {
			return text
		}
	}
	return ""
}

func stringSlice(raw interface{}) []string {
	values, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := asString(value); text != "" {
			out = append(out, text)
		}
	}
	return out
}
