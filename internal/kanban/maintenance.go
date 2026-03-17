package kanban

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	runtimeEventRetentionDays     = 30
	changeEventRetentionDays      = 14
	issueActivityRetentionDays    = 30
	completedSessionRetentionDays = 30
)

type DBStats struct {
	PageCount     int `json:"page_count"`
	PageSize      int `json:"page_size"`
	FreelistCount int `json:"freelist_count"`
}

type MaintenanceResult struct {
	StartedAt        time.Time `json:"started_at"`
	CheckpointAt     time.Time `json:"checkpoint_at"`
	CheckpointResult string    `json:"checkpoint_result"`
}

func (s *Store) DBStats() (DBStats, error) {
	stats := DBStats{}
	if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&stats.PageCount); err != nil {
		return DBStats{}, err
	}
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&stats.PageSize); err != nil {
		return DBStats{}, err
	}
	if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&stats.FreelistCount); err != nil {
		return DBStats{}, err
	}
	return stats, nil
}

func (s *Store) RunMaintenance(protectedIssueIDs []string) (MaintenanceResult, error) {
	result := MaintenanceResult{StartedAt: time.Now().UTC()}
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	if err := deleteExpiredRowsTx(tx, "runtime_events", "event_ts", "issue_id", now.AddDate(0, 0, -runtimeEventRetentionDays), protectedIssueIDs, ""); err != nil {
		return result, err
	}
	if err := deleteExpiredRowsTx(tx, "change_events", "event_ts", "", now.AddDate(0, 0, -changeEventRetentionDays), nil, ""); err != nil {
		return result, err
	}
	if err := deleteExpiredRowsTx(tx, "issue_activity_updates", "event_ts", "issue_id", now.AddDate(0, 0, -issueActivityRetentionDays), protectedIssueIDs, ""); err != nil {
		return result, err
	}
	if err := deleteExpiredRowsTx(tx, "issue_activity_entries", "updated_at", "issue_id", now.AddDate(0, 0, -issueActivityRetentionDays), protectedIssueIDs, ""); err != nil {
		return result, err
	}
	if err := deleteExpiredRowsTx(tx, "issue_execution_sessions", "updated_at", "issue_id", now.AddDate(0, 0, -completedSessionRetentionDays), protectedIssueIDs, ""); err != nil {
		return result, err
	}

	if err := tx.Commit(); err != nil {
		return result, err
	}
	tx = nil

	row := s.db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`)
	var busy, logFrames, checkpointed int
	if err := row.Scan(&busy, &logFrames, &checkpointed); err != nil {
		return result, err
	}
	if _, err := s.db.Exec(`PRAGMA optimize`); err != nil {
		return result, err
	}
	result.CheckpointAt = time.Now().UTC()
	result.CheckpointResult = fmt.Sprintf("busy=%d log=%d checkpointed=%d", busy, logFrames, checkpointed)
	return result, nil
}

func deleteExpiredRowsTx(tx *sql.Tx, table, tsColumn, issueColumn string, cutoff time.Time, protectedIssueIDs []string, extraPredicate string) error {
	clauses := []string{tsColumn + ` < ?`}
	args := []interface{}{cutoff}
	if strings.TrimSpace(extraPredicate) != "" {
		clauses = append(clauses, extraPredicate)
	}
	if strings.TrimSpace(issueColumn) != "" && len(protectedIssueIDs) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(protectedIssueIDs)), ",")
		clauses = append(clauses, issueColumn+` NOT IN (`+placeholders+`)`)
		for _, id := range protectedIssueIDs {
			args = append(args, id)
		}
	}
	_, err := tx.Exec(`DELETE FROM `+table+` WHERE `+strings.Join(clauses, ` AND `), args...)
	return err
}
