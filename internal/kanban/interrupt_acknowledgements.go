package kanban

import (
	"database/sql"
	"strings"
	"time"
)

func (s *Store) AcknowledgeInterrupt(interruptID string) error {
	interruptID = strings.TrimSpace(interruptID)
	if interruptID == "" {
		return validationErrorf("interrupt_id is required")
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO interrupt_acknowledgements (interrupt_id, acknowledged_at) VALUES (?, ?)`,
		interruptID,
		time.Now().UTC(),
	)
	return err
}

func (s *Store) ListInterruptAcknowledgements(interruptIDs []string) (map[string]time.Time, error) {
	cleanIDs := make([]string, 0, len(interruptIDs))
	seen := make(map[string]struct{}, len(interruptIDs))
	for _, interruptID := range interruptIDs {
		interruptID = strings.TrimSpace(interruptID)
		if interruptID == "" {
			continue
		}
		if _, ok := seen[interruptID]; ok {
			continue
		}
		seen[interruptID] = struct{}{}
		cleanIDs = append(cleanIDs, interruptID)
	}
	if len(cleanIDs) == 0 {
		return map[string]time.Time{}, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cleanIDs)), ",")
	args := make([]interface{}, 0, len(cleanIDs))
	for _, interruptID := range cleanIDs {
		args = append(args, interruptID)
	}

	rows, err := s.db.Query(
		`SELECT interrupt_id, acknowledged_at FROM interrupt_acknowledgements WHERE interrupt_id IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	acknowledged := make(map[string]time.Time, len(cleanIDs))
	for rows.Next() {
		var (
			interruptID    string
			acknowledgedAt time.Time
		)
		if err := rows.Scan(&interruptID, &acknowledgedAt); err != nil {
			return nil, err
		}
		acknowledged[interruptID] = acknowledgedAt.UTC()
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return acknowledged, nil
}

func (s *Store) PruneInterruptAcknowledgements(prefix string, keepIDs []string) error {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return validationErrorf("prefix is required")
	}

	cleanIDs := make([]string, 0, len(keepIDs))
	seen := make(map[string]struct{}, len(keepIDs))
	for _, interruptID := range keepIDs {
		interruptID = strings.TrimSpace(interruptID)
		if interruptID == "" || !strings.HasPrefix(interruptID, prefix) {
			continue
		}
		if _, ok := seen[interruptID]; ok {
			continue
		}
		seen[interruptID] = struct{}{}
		cleanIDs = append(cleanIDs, interruptID)
	}

	args := []interface{}{prefix + "%"}
	query := `DELETE FROM interrupt_acknowledgements WHERE interrupt_id LIKE ?`
	if len(cleanIDs) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cleanIDs)), ",")
		query += ` AND interrupt_id NOT IN (` + placeholders + `)`
		for _, interruptID := range cleanIDs {
			args = append(args, interruptID)
		}
	}

	_, err := s.db.Exec(query, args...)
	return err
}

func (s *Store) InterruptAcknowledged(interruptID string) (bool, error) {
	interruptID = strings.TrimSpace(interruptID)
	if interruptID == "" {
		return false, validationErrorf("interrupt_id is required")
	}
	var acknowledgedAt sql.NullTime
	err := s.db.QueryRow(
		`SELECT acknowledged_at FROM interrupt_acknowledgements WHERE interrupt_id = ?`,
		interruptID,
	).Scan(&acknowledgedAt)
	switch err {
	case nil:
		return acknowledgedAt.Valid, nil
	case sql.ErrNoRows:
		return false, nil
	default:
		return false, err
	}
}
