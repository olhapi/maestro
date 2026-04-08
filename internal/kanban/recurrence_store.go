package kanban

import (
	"database/sql"
	"strings"
	"time"
)

type IssueCreateOptions struct {
	IssueType         IssueType
	Cron              string
	Enabled           *bool
	PermissionProfile PermissionProfile
	AgentName         string
	AgentPrompt       string
}

func boolToSQLite(v bool) int {
	if v {
		return 1
	}
	return 0
}

func boolFromValue(raw interface{}) (bool, bool) {
	switch value := raw.(type) {
	case bool:
		return value, true
	case *bool:
		if value == nil {
			return false, false
		}
		return *value, true
	default:
		return false, false
	}
}

func applyRecurrenceToIssue(issue *Issue, recurrence *IssueRecurrence) {
	if issue == nil {
		return
	}
	issue.IssueType = NormalizeIssueType(string(issue.IssueType))
	if recurrence == nil {
		if issue.IssueType != IssueTypeRecurring {
			issue.Cron = ""
			issue.Enabled = false
			issue.NextRunAt = nil
			issue.LastEnqueuedAt = nil
			issue.PendingRerun = false
		}
		return
	}
	issue.Cron = recurrence.Cron
	issue.Enabled = recurrence.Enabled
	issue.NextRunAt = recurrence.NextRunAt
	issue.LastEnqueuedAt = recurrence.LastEnqueuedAt
	issue.PendingRerun = recurrence.PendingRerun
}

func scanIssueRecurrence(rowScanner interface {
	Scan(dest ...interface{}) error
}) (*IssueRecurrence, error) {
	recurrence := &IssueRecurrence{}
	var nextRunAt sql.NullTime
	var lastEnqueuedAt sql.NullTime
	var enabled int
	var pendingRerun int
	if err := rowScanner.Scan(
		&recurrence.IssueID,
		&recurrence.Cron,
		&enabled,
		&nextRunAt,
		&lastEnqueuedAt,
		&pendingRerun,
		&recurrence.CreatedAt,
		&recurrence.UpdatedAt,
	); err != nil {
		return nil, err
	}
	recurrence.Cron = normalizeCronSpec(recurrence.Cron)
	recurrence.Enabled = enabled != 0
	recurrence.PendingRerun = pendingRerun != 0
	if nextRunAt.Valid {
		next := nextRunAt.Time.UTC()
		recurrence.NextRunAt = &next
	}
	if lastEnqueuedAt.Valid {
		enqueued := lastEnqueuedAt.Time.UTC()
		recurrence.LastEnqueuedAt = &enqueued
	}
	return recurrence, nil
}

func (s *Store) GetIssueRecurrence(issueID string) (*IssueRecurrence, error) {
	row := s.db.QueryRow(`
		SELECT issue_id, cron, enabled, next_run_at, last_enqueued_at, pending_rerun, created_at, updated_at
		FROM issue_recurrences
		WHERE issue_id = ?`, issueID)
	recurrence, err := scanIssueRecurrence(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return recurrence, nil
}

func (s *Store) issueRecurrenceMap(issueIDs []string) (map[string]IssueRecurrence, error) {
	out := make(map[string]IssueRecurrence, len(issueIDs))
	if len(issueIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, 0, len(issueIDs))
	args := make([]interface{}, 0, len(issueIDs))
	for _, issueID := range issueIDs {
		issueID = strings.TrimSpace(issueID)
		if issueID == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, issueID)
	}
	if len(placeholders) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(`
		SELECT issue_id, cron, enabled, next_run_at, last_enqueued_at, pending_rerun, created_at, updated_at
		FROM issue_recurrences
		WHERE issue_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		recurrence, err := scanIssueRecurrence(rows)
		if err != nil {
			return nil, err
		}
		out[recurrence.IssueID] = *recurrence
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func defaultRecurringEnabled(enabled *bool) bool {
	if enabled == nil {
		return true
	}
	return *enabled
}

func buildIssueRecurrence(issueID string, cron string, enabled bool, nextRunAt *time.Time, current *IssueRecurrence, now time.Time) (*IssueRecurrence, error) {
	cron = normalizeCronSpec(cron)
	if err := ValidateRecurringCron(cron); err != nil {
		return nil, err
	}
	recurrence := &IssueRecurrence{
		IssueID:      issueID,
		Cron:         cron,
		Enabled:      enabled,
		PendingRerun: current != nil && current.PendingRerun,
		CreatedAt:    now.UTC(),
		UpdatedAt:    now.UTC(),
	}
	if current != nil {
		recurrence.CreatedAt = current.CreatedAt
		recurrence.LastEnqueuedAt = current.LastEnqueuedAt
	}
	if nextRunAt != nil {
		next := nextRunAt.UTC()
		recurrence.NextRunAt = &next
	}
	return recurrence, nil
}

func saveIssueRecurrenceTx(tx *sql.Tx, recurrence *IssueRecurrence) error {
	if recurrence == nil {
		return nil
	}
	var nextRunAt interface{}
	if recurrence.NextRunAt != nil {
		nextRunAt = recurrence.NextRunAt.UTC()
	}
	var lastEnqueuedAt interface{}
	if recurrence.LastEnqueuedAt != nil {
		lastEnqueuedAt = recurrence.LastEnqueuedAt.UTC()
	}
	_, err := tx.Exec(`
		INSERT INTO issue_recurrences (issue_id, cron, enabled, next_run_at, last_enqueued_at, pending_rerun, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(issue_id) DO UPDATE SET
			cron = excluded.cron,
			enabled = excluded.enabled,
			next_run_at = excluded.next_run_at,
			last_enqueued_at = excluded.last_enqueued_at,
			pending_rerun = excluded.pending_rerun,
			updated_at = excluded.updated_at`,
		recurrence.IssueID,
		recurrence.Cron,
		boolToSQLite(recurrence.Enabled),
		nextRunAt,
		lastEnqueuedAt,
		boolToSQLite(recurrence.PendingRerun),
		recurrence.CreatedAt.UTC(),
		recurrence.UpdatedAt.UTC(),
	)
	return err
}

func deleteIssueRecurrenceTx(tx *sql.Tx, issueID string) error {
	_, err := tx.Exec(`DELETE FROM issue_recurrences WHERE issue_id = ?`, issueID)
	return err
}

func (s *Store) listRecurringIssueIDs(baseQuery string, args []interface{}) ([]string, error) {
	rows, err := s.db.Query(baseQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var issueID string
		if err := rows.Scan(&issueID); err != nil {
			return nil, err
		}
		ids = append(ids, issueID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) ListDueRecurringIssues(now time.Time, repoPath string, limit int) ([]Issue, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT i.id
		FROM issues i
		INNER JOIN issue_recurrences r ON r.issue_id = i.id
		LEFT JOIN projects p ON p.id = i.project_id
		WHERE i.issue_type = 'recurring'
		  AND i.state <> 'cancelled'
		  AND r.enabled = 1
		  AND r.next_run_at IS NOT NULL
		  AND r.next_run_at <= ?`
	args := []interface{}{now.UTC()}
	if strings.TrimSpace(repoPath) != "" {
		query += ` AND COALESCE(p.repo_path, '') = ?`
		args = append(args, strings.TrimSpace(repoPath))
	}
	query += ` ORDER BY r.next_run_at ASC, i.created_at ASC LIMIT ?`
	args = append(args, limit)

	ids, err := s.listRecurringIssueIDs(query, args)
	if err != nil {
		return nil, err
	}
	return s.loadIssuesByIDs(ids)
}

func (s *Store) ListPendingRecurringIssues(repoPath string, limit int) ([]Issue, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT i.id
		FROM issues i
		INNER JOIN issue_recurrences r ON r.issue_id = i.id
		LEFT JOIN projects p ON p.id = i.project_id
		WHERE i.issue_type = 'recurring'
		  AND i.state <> 'cancelled'
		  AND r.pending_rerun = 1`
	args := []interface{}{}
	if strings.TrimSpace(repoPath) != "" {
		query += ` AND COALESCE(p.repo_path, '') = ?`
		args = append(args, strings.TrimSpace(repoPath))
	}
	query += ` ORDER BY i.updated_at ASC LIMIT ?`
	args = append(args, limit)

	ids, err := s.listRecurringIssueIDs(query, args)
	if err != nil {
		return nil, err
	}
	return s.loadIssuesByIDs(ids)
}

func (s *Store) NextRecurringDueAt(repoPath string) (*time.Time, error) {
	query := `
		SELECT r.next_run_at
		FROM issue_recurrences r
		INNER JOIN issues i ON i.id = r.issue_id
		LEFT JOIN projects p ON p.id = i.project_id
		WHERE i.issue_type = 'recurring'
		  AND i.state <> 'cancelled'
		  AND r.enabled = 1
		  AND r.next_run_at IS NOT NULL`
	args := []interface{}{}
	if strings.TrimSpace(repoPath) != "" {
		query += ` AND COALESCE(p.repo_path, '') = ?`
		args = append(args, strings.TrimSpace(repoPath))
	}
	query += ` ORDER BY r.next_run_at ASC LIMIT 1`
	var due sql.NullTime
	if err := s.db.QueryRow(query, args...).Scan(&due); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !due.Valid {
		return nil, nil
	}
	next := due.Time.UTC()
	return &next, nil
}

func (s *Store) MarkRecurringPendingRerun(issueID string, pending bool) error {
	now := time.Now().UTC()
	res, err := s.db.Exec(`
		UPDATE issue_recurrences
		SET pending_rerun = ?, updated_at = ?
		WHERE issue_id = ?`,
		boolToSQLite(pending), now, issueID,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue_recurrence", issueID)
	}
	return s.appendChange("issue", issueID, "recurrence_updated", map[string]interface{}{
		"pending_rerun": pending,
	})
}

func (s *Store) RearmRecurringIssue(issueID string, enqueuedAt time.Time, nextRunAt *time.Time) error {
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
	res, err := tx.Exec(`
		UPDATE issues
		SET state = ?, workflow_phase = ?, updated_at = ?, started_at = NULL, completed_at = NULL
		WHERE id = ? AND issue_type = ?`,
		StateReady, WorkflowPhaseImplementation, now, issueID, IssueTypeRecurring,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", issueID)
	}
	var nextRunValue interface{}
	if nextRunAt != nil {
		nextRunValue = nextRunAt.UTC()
	}
	if _, err := tx.Exec(`
		UPDATE issue_recurrences
		SET last_enqueued_at = ?, next_run_at = ?, pending_rerun = 0, updated_at = ?
		WHERE issue_id = ?`,
		enqueuedAt.UTC(), nextRunValue, now, issueID,
	); err != nil {
		return err
	}
	if err := s.appendChangeTx(tx, "issue", issueID, "state_changed", map[string]interface{}{
		"state":          StateReady,
		"workflow_phase": WorkflowPhaseImplementation,
		"recurring":      true,
	}); err != nil {
		return err
	}
	if err := s.appendChangeTx(tx, "issue", issueID, "recurrence_updated", map[string]interface{}{
		"last_enqueued_at": enqueuedAt.UTC(),
		"next_run_at":      nextRunValue,
		"pending_rerun":    false,
	}); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return s.ActivateIssueAgentCommandsIfDispatchable(issueID)
}

func disableIssueRecurrenceTx(tx *sql.Tx, issueID string, now time.Time) error {
	_, err := tx.Exec(`UPDATE issue_recurrences SET enabled = 0, updated_at = ? WHERE issue_id = ?`, now.UTC(), issueID)
	return err
}
