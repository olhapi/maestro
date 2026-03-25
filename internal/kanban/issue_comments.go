package kanban

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MaxIssueCommentAttachmentBytes int64 = 25 * 1024 * 1024

func IssueCommentAssetRoot(dbPath string) string {
	resolved := ResolveDBPath(dbPath)
	return filepath.Join(filepath.Dir(resolved), "assets", "comments")
}

func (s *Store) IssueCommentAssetRoot() string {
	return IssueCommentAssetRoot(s.dbPath)
}

func (s *Store) ensureIssueCommentTables() error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS issue_comments (
			id TEXT PRIMARY KEY,
			issue_id TEXT NOT NULL,
			parent_comment_id TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			author_json TEXT NOT NULL DEFAULT '{}',
			provider_kind TEXT NOT NULL DEFAULT 'kanban',
			provider_comment_ref TEXT NOT NULL DEFAULT '',
			deleted_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`ALTER TABLE issue_comments ADD COLUMN parent_comment_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issue_comments ADD COLUMN author_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE issue_comments ADD COLUMN provider_kind TEXT NOT NULL DEFAULT 'kanban'`,
		`ALTER TABLE issue_comments ADD COLUMN provider_comment_ref TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issue_comments ADD COLUMN deleted_at DATETIME`,
		`CREATE INDEX IF NOT EXISTS idx_issue_comments_issue_created ON issue_comments(issue_id, created_at ASC, id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_comments_issue_parent_created ON issue_comments(issue_id, parent_comment_id, created_at ASC, id ASC)`,
		`CREATE TABLE IF NOT EXISTS issue_comment_attachments (
			id TEXT PRIMARY KEY,
			comment_id TEXT NOT NULL,
			filename TEXT NOT NULL,
			content_type TEXT NOT NULL,
			byte_size INTEGER NOT NULL,
			url TEXT NOT NULL DEFAULT '',
			storage_path TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (comment_id) REFERENCES issue_comments(id)
		)`,
		`ALTER TABLE issue_comment_attachments ADD COLUMN url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issue_comment_attachments ADD COLUMN storage_path TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_issue_comment_attachments_comment_created ON issue_comment_attachments(comment_id, created_at ASC, id ASC)`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	return nil
}

func (s *Store) ListIssueComments(issueID string) ([]IssueComment, error) {
	if strings.TrimSpace(issueID) == "" {
		return nil, validationErrorf("issue_id is required")
	}
	if _, err := s.GetIssue(issueID); err != nil {
		if err == sql.ErrNoRows {
			return nil, notFoundError("issue", issueID)
		}
		return nil, err
	}
	flat, err := s.listIssueCommentsFlat(issueID)
	if err != nil {
		return nil, err
	}
	return nestIssueComments(flat), nil
}

func (s *Store) CreateIssueComment(issueID string, input IssueCommentInput) (*IssueComment, error) {
	if strings.TrimSpace(issueID) == "" {
		return nil, validationErrorf("issue_id is required")
	}
	if _, err := s.GetIssue(issueID); err != nil {
		if err == sql.ErrNoRows {
			return nil, notFoundError("issue", issueID)
		}
		return nil, err
	}
	body := strings.TrimSpace(commentBodyValue(input.Body))
	if body == "" && len(input.Attachments) == 0 {
		return nil, validationErrorf("comment body or attachments are required")
	}
	parentID, err := s.validateIssueCommentParent(issueID, input.ParentCommentID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	comment := IssueComment{
		ID:              generateID("cmt"),
		IssueID:         issueID,
		ParentCommentID: parentID,
		Body:            body,
		Author:          normalizeLocalIssueCommentAuthor(input.Author),
		ProviderKind:    ProviderKindKanban,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	attachments, err := s.materializeCommentAttachments(issueID, comment.ID, input.Attachments, now)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		s.cleanupIssueCommentAttachmentPaths(attachments)
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	if err := insertIssueCommentTx(tx, comment); err != nil {
		s.cleanupIssueCommentAttachmentPaths(attachments)
		return nil, err
	}
	if err := insertIssueCommentAttachmentsTx(tx, attachments); err != nil {
		s.cleanupIssueCommentAttachmentPaths(attachments)
		return nil, err
	}
	if err := s.appendChangeTx(tx, "issue_comment", comment.ID, "created", map[string]interface{}{
		"issue_id":             issueID,
		"parent_comment_id":    parentID,
		"attachment_count":     len(attachments),
		"provider_kind":        comment.ProviderKind,
		"provider_comment_ref": comment.ProviderCommentRef,
	}); err != nil {
		s.cleanupIssueCommentAttachmentPaths(attachments)
		return nil, err
	}
	if err := s.commitTx(tx, true); err != nil {
		s.cleanupIssueCommentAttachmentPaths(attachments)
		return nil, err
	}
	tx = nil
	return s.GetIssueComment(issueID, comment.ID)
}

func (s *Store) UpdateIssueComment(issueID, commentID string, input IssueCommentInput) (*IssueComment, error) {
	comment, err := s.GetIssueComment(issueID, commentID)
	if err != nil {
		return nil, err
	}
	if comment.DeletedAt != nil {
		return nil, validationErrorf("deleted comments cannot be edited")
	}

	updates := map[string]interface{}{}
	bodyChanged := input.Body != nil
	if bodyChanged {
		updates["body"] = strings.TrimSpace(commentBodyValue(input.Body))
	} else {
		updates["body"] = comment.Body
	}

	newAttachments, err := s.materializeCommentAttachments(issueID, commentID, input.Attachments, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	var addedPaths []IssueCommentAttachment
	addedPaths = newAttachments
	defer func() {
		if len(addedPaths) > 0 {
			s.cleanupIssueCommentAttachmentPaths(addedPaths)
		}
	}()
	remainingAttachments := comment.Attachments[:0]
	for _, attachment := range comment.Attachments {
		if containsIssueCommentAttachmentID(input.RemoveAttachmentIDs, attachment.ID) {
			continue
		}
		remainingAttachments = append(remainingAttachments, attachment)
	}
	if strings.TrimSpace(updates["body"].(string)) == "" && len(remainingAttachments)+len(newAttachments) == 0 {
		return nil, validationErrorf("comment body or attachments are required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	removedPaths, err := s.deleteIssueCommentAttachmentsTx(tx, comment.ID, input.RemoveAttachmentIDs)
	if err != nil {
		return nil, err
	}
	if bodyChanged || len(newAttachments) > 0 || len(input.RemoveAttachmentIDs) > 0 {
		updateTime := time.Now().UTC()
		if bodyChanged || len(newAttachments) > 0 || len(input.RemoveAttachmentIDs) > 0 {
			if _, err := tx.Exec(`UPDATE issue_comments SET body = ?, updated_at = ? WHERE id = ? AND issue_id = ?`,
				updates["body"], updateTime, comment.ID, issueID); err != nil {
				return nil, err
			}
		}
		if len(newAttachments) > 0 {
			if err := insertIssueCommentAttachmentsTx(tx, newAttachments); err != nil {
				return nil, err
			}
		}
		if err := s.appendChangeTx(tx, "issue_comment", comment.ID, "updated", map[string]interface{}{
			"issue_id":               issueID,
			"removed_attachment_ids": input.RemoveAttachmentIDs,
			"added_attachment_count": len(newAttachments),
		}); err != nil {
			return nil, err
		}
		if err := s.commitTx(tx, true); err != nil {
			return nil, err
		}
		tx = nil
		s.cleanupIssueCommentAttachmentPaths(removedPaths)
		addedPaths = nil
		return s.GetIssueComment(issueID, commentID)
	}
	if err := tx.Rollback(); err != nil {
		return nil, err
	}
	tx = nil
	addedPaths = nil
	return comment, nil
}

func containsIssueCommentAttachmentID(ids []string, candidate string) bool {
	for _, id := range ids {
		if strings.TrimSpace(id) == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}

func (s *Store) DeleteIssueComment(issueID, commentID string) error {
	comment, err := s.GetIssueComment(issueID, commentID)
	if err != nil {
		return err
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

	hasReplies, err := issueCommentHasRepliesTx(tx, comment.ID)
	if err != nil {
		return err
	}
	attachmentPaths, err := s.deleteAllIssueCommentAttachmentsTx(tx, comment.ID)
	if err != nil {
		return err
	}

	action := "deleted"
	if hasReplies {
		now := time.Now().UTC()
		if _, err := tx.Exec(`
			UPDATE issue_comments
			SET body = '', deleted_at = ?, updated_at = ?
			WHERE id = ? AND issue_id = ?`,
			now, now, comment.ID, issueID,
		); err != nil {
			return err
		}
		action = "soft_deleted"
	} else {
		res, err := tx.Exec(`DELETE FROM issue_comments WHERE id = ? AND issue_id = ?`, comment.ID, issueID)
		if err != nil {
			return err
		}
		if rows, err := res.RowsAffected(); err == nil && rows == 0 {
			return notFoundError("issue_comment", commentID)
		}
	}
	if err := s.appendChangeTx(tx, "issue_comment", comment.ID, action, map[string]interface{}{"issue_id": issueID}); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	s.cleanupIssueCommentAttachmentPaths(attachmentPaths)
	return nil
}

func (s *Store) GetIssueComment(issueID, commentID string) (*IssueComment, error) {
	comment, err := s.getIssueCommentFlat(issueID, commentID)
	if err != nil {
		return nil, err
	}
	return comment, nil
}

func (s *Store) GetIssueCommentAttachment(issueID, commentID, attachmentID string) (*IssueCommentAttachment, error) {
	row := s.db.QueryRow(`
		SELECT a.id, a.comment_id, a.filename, a.content_type, a.byte_size, a.url, a.storage_path, a.created_at, a.updated_at
		FROM issue_comment_attachments a
		INNER JOIN issue_comments c ON c.id = a.comment_id
		WHERE c.issue_id = ? AND c.id = ? AND a.id = ?`,
		issueID, commentID, attachmentID,
	)
	attachment, err := scanIssueCommentAttachment(row)
	if err == sql.ErrNoRows {
		return nil, notFoundError("issue_comment_attachment", attachmentID)
	}
	if err != nil {
		return nil, err
	}
	return attachment, nil
}

func (s *Store) GetIssueCommentAttachmentContent(issueID, commentID, attachmentID string) (*IssueCommentAttachment, string, error) {
	attachment, err := s.GetIssueCommentAttachment(issueID, commentID, attachmentID)
	if err != nil {
		return nil, "", err
	}
	path, err := s.issueCommentAttachmentPath(attachment.StoragePath)
	if err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, "", notFoundError("issue_comment_attachment", attachmentID)
		}
		return nil, "", err
	}
	return attachment, path, nil
}

func (s *Store) listIssueCommentsFlat(issueID string) ([]IssueComment, error) {
	rows, err := s.db.Query(`
		SELECT id, issue_id, parent_comment_id, body, author_json, provider_kind, provider_comment_ref, deleted_at, created_at, updated_at
		FROM issue_comments
		WHERE issue_id = ?
		ORDER BY created_at ASC, id ASC`,
		issueID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	comments := []IssueComment{}
	commentIDs := []string{}
	for rows.Next() {
		comment, err := scanIssueComment(rows)
		if err != nil {
			return nil, err
		}
		comments = append(comments, *comment)
		commentIDs = append(commentIDs, comment.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	attachments, err := s.issueCommentAttachmentsMap(commentIDs)
	if err != nil {
		return nil, err
	}
	for i := range comments {
		commentAttachments := attachments[comments[i].ID]
		if commentAttachments == nil {
			commentAttachments = []IssueCommentAttachment{}
		}
		comments[i].Attachments = commentAttachments
	}
	return comments, nil
}

func (s *Store) getIssueCommentFlat(issueID, commentID string) (*IssueComment, error) {
	row := s.db.QueryRow(`
		SELECT id, issue_id, parent_comment_id, body, author_json, provider_kind, provider_comment_ref, deleted_at, created_at, updated_at
		FROM issue_comments
		WHERE issue_id = ? AND id = ?`,
		issueID, commentID,
	)
	comment, err := scanIssueComment(row)
	if err == sql.ErrNoRows {
		return nil, notFoundError("issue_comment", commentID)
	}
	if err != nil {
		return nil, err
	}
	attachments, err := s.issueCommentAttachmentsMap([]string{comment.ID})
	if err != nil {
		return nil, err
	}
	commentAttachments := attachments[comment.ID]
	if commentAttachments == nil {
		commentAttachments = []IssueCommentAttachment{}
	}
	comment.Attachments = commentAttachments
	comment.Replies = []IssueComment{}
	return comment, nil
}

func (s *Store) issueCommentAttachmentsMap(commentIDs []string) (map[string][]IssueCommentAttachment, error) {
	out := map[string][]IssueCommentAttachment{}
	if len(commentIDs) == 0 {
		return out, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(commentIDs)), ",")
	args := make([]interface{}, 0, len(commentIDs))
	for _, id := range commentIDs {
		args = append(args, id)
	}
	rows, err := s.db.Query(`
		SELECT id, comment_id, filename, content_type, byte_size, url, storage_path, created_at, updated_at
		FROM issue_comment_attachments
		WHERE comment_id IN (`+placeholders+`)
		ORDER BY created_at ASC, id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		attachment, err := scanIssueCommentAttachment(rows)
		if err != nil {
			return nil, err
		}
		out[attachment.CommentID] = append(out[attachment.CommentID], *attachment)
	}
	return out, rows.Err()
}

func (s *Store) validateIssueCommentParent(issueID, parentCommentID string) (string, error) {
	parentCommentID = strings.TrimSpace(parentCommentID)
	if parentCommentID == "" {
		return "", nil
	}
	parent, err := s.GetIssueComment(issueID, parentCommentID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(parent.ParentCommentID) != "" {
		return "", validationErrorf("replies to replies are not supported")
	}
	return parent.ID, nil
}

func (s *Store) materializeCommentAttachments(issueID, commentID string, inputs []IssueCommentAttachmentInput, now time.Time) ([]IssueCommentAttachment, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	root := s.IssueCommentAssetRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	issueDir := filepath.Join(root, issueID, commentID)
	if err := os.MkdirAll(issueDir, 0o755); err != nil {
		return nil, err
	}

	out := make([]IssueCommentAttachment, 0, len(inputs))
	for _, input := range inputs {
		attachment, err := writeIssueCommentAttachment(root, issueDir, commentID, input, now)
		if err != nil {
			s.cleanupIssueCommentAttachmentPaths(out)
			return nil, err
		}
		out = append(out, *attachment)
	}
	return out, nil
}

func insertIssueCommentTx(tx *sql.Tx, comment IssueComment) error {
	authorJSON, err := json.Marshal(comment.Author)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO issue_comments (id, issue_id, parent_comment_id, body, author_json, provider_kind, provider_comment_ref, deleted_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		comment.ID,
		comment.IssueID,
		comment.ParentCommentID,
		comment.Body,
		string(authorJSON),
		comment.ProviderKind,
		comment.ProviderCommentRef,
		comment.DeletedAt,
		comment.CreatedAt,
		comment.UpdatedAt,
	)
	return err
}

func insertIssueCommentAttachmentsTx(tx *sql.Tx, attachments []IssueCommentAttachment) error {
	for _, attachment := range attachments {
		if _, err := tx.Exec(`
			INSERT INTO issue_comment_attachments (id, comment_id, filename, content_type, byte_size, url, storage_path, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			attachment.ID,
			attachment.CommentID,
			attachment.Filename,
			attachment.ContentType,
			attachment.ByteSize,
			attachment.URL,
			attachment.StoragePath,
			attachment.CreatedAt,
			attachment.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func issueCommentHasRepliesTx(tx *sql.Tx, commentID string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM issue_comments WHERE parent_comment_id = ? LIMIT 1)`, commentID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *Store) deleteIssueCommentAttachmentsTx(tx *sql.Tx, commentID string, attachmentIDs []string) ([]IssueCommentAttachment, error) {
	if len(attachmentIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(attachmentIDs)), ",")
	args := make([]interface{}, 0, len(attachmentIDs)+1)
	args = append(args, commentID)
	for _, id := range attachmentIDs {
		args = append(args, strings.TrimSpace(id))
	}
	rows, err := tx.Query(`
		SELECT id, comment_id, filename, content_type, byte_size, url, storage_path, created_at, updated_at
		FROM issue_comment_attachments
		WHERE comment_id = ? AND id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	attachments := []IssueCommentAttachment{}
	for rows.Next() {
		attachment, err := scanIssueCommentAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, *attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(attachments) == 0 {
		return nil, nil
	}
	deleteArgs := make([]interface{}, 0, len(attachmentIDs)+1)
	deleteArgs = append(deleteArgs, commentID)
	for _, id := range attachmentIDs {
		deleteArgs = append(deleteArgs, strings.TrimSpace(id))
	}
	if _, err := tx.Exec(`DELETE FROM issue_comment_attachments WHERE comment_id = ? AND id IN (`+placeholders+`)`, deleteArgs...); err != nil {
		return nil, err
	}
	return attachments, nil
}

func (s *Store) deleteAllIssueCommentAttachmentsTx(tx *sql.Tx, commentID string) ([]IssueCommentAttachment, error) {
	rows, err := tx.Query(`
		SELECT id, comment_id, filename, content_type, byte_size, url, storage_path, created_at, updated_at
		FROM issue_comment_attachments
		WHERE comment_id = ?`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	attachments := []IssueCommentAttachment{}
	for rows.Next() {
		attachment, err := scanIssueCommentAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, *attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_comment_attachments WHERE comment_id = ?`, commentID); err != nil {
		return nil, err
	}
	return attachments, nil
}

func (s *Store) deleteIssueCommentsTx(tx *sql.Tx, issueID string) ([]IssueCommentAttachment, error) {
	rows, err := tx.Query(`
		SELECT a.id, a.comment_id, a.filename, a.content_type, a.byte_size, a.url, a.storage_path, a.created_at, a.updated_at
		FROM issue_comment_attachments a
		INNER JOIN issue_comments c ON c.id = a.comment_id
		WHERE c.issue_id = ?`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	attachments := []IssueCommentAttachment{}
	for rows.Next() {
		attachment, err := scanIssueCommentAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, *attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_comment_attachments WHERE comment_id IN (SELECT id FROM issue_comments WHERE issue_id = ?)`, issueID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_comments WHERE issue_id = ?`, issueID); err != nil {
		return nil, err
	}
	return attachments, nil
}

func (s *Store) cleanupIssueCommentAttachmentPaths(attachments []IssueCommentAttachment) {
	seenDirs := map[string]struct{}{}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.StoragePath) == "" {
			continue
		}
		path, err := s.issueCommentAttachmentPath(attachment.StoragePath)
		if err != nil {
			continue
		}
		_ = os.Remove(path)
		dir := filepath.Dir(path)
		if _, ok := seenDirs[dir]; ok {
			continue
		}
		seenDirs[dir] = struct{}{}
		removeIfEmpty(dir)
		removeIfEmpty(filepath.Dir(dir))
	}
}

func (s *Store) issueCommentAttachmentPath(storagePath string) (string, error) {
	root := s.IssueCommentAssetRoot()
	fullPath := filepath.Join(root, filepath.FromSlash(strings.TrimSpace(storagePath)))
	rel, err := filepath.Rel(root, fullPath)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", validationErrorf("invalid stored comment attachment path")
	}
	return fullPath, nil
}

func scanIssueComment(scanner interface {
	Scan(dest ...interface{}) error
}) (*IssueComment, error) {
	comment := &IssueComment{}
	var authorJSON string
	var deletedAt sql.NullTime
	if err := scanner.Scan(
		&comment.ID,
		&comment.IssueID,
		&comment.ParentCommentID,
		&comment.Body,
		&authorJSON,
		&comment.ProviderKind,
		&comment.ProviderCommentRef,
		&deletedAt,
		&comment.CreatedAt,
		&comment.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if strings.TrimSpace(authorJSON) != "" {
		_ = json.Unmarshal([]byte(authorJSON), &comment.Author)
	}
	if deletedAt.Valid {
		comment.DeletedAt = &deletedAt.Time
	}
	return comment, nil
}

func scanIssueCommentAttachment(scanner interface {
	Scan(dest ...interface{}) error
}) (*IssueCommentAttachment, error) {
	attachment := &IssueCommentAttachment{}
	if err := scanner.Scan(
		&attachment.ID,
		&attachment.CommentID,
		&attachment.Filename,
		&attachment.ContentType,
		&attachment.ByteSize,
		&attachment.URL,
		&attachment.StoragePath,
		&attachment.CreatedAt,
		&attachment.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return attachment, nil
}

func nestIssueComments(flat []IssueComment) []IssueComment {
	if len(flat) == 0 {
		return []IssueComment{}
	}
	exists := make(map[string]struct{}, len(flat))
	for _, comment := range flat {
		exists[comment.ID] = struct{}{}
	}
	byParent := map[string][]IssueComment{}
	for _, comment := range flat {
		parent := strings.TrimSpace(comment.ParentCommentID)
		if parent != "" {
			if _, ok := exists[parent]; !ok {
				parent = ""
				comment.ParentCommentID = ""
			}
		}
		byParent[parent] = append(byParent[parent], comment)
	}
	var build func(parentID string) []IssueComment
	build = func(parentID string) []IssueComment {
		items := byParent[parentID]
		out := make([]IssueComment, 0, len(items))
		for _, item := range items {
			item.Replies = build(item.ID)
			out = append(out, item)
		}
		return out
	}
	return build("")
}

func normalizeLocalIssueCommentAuthor(author IssueCommentAuthor) IssueCommentAuthor {
	name := strings.TrimSpace(author.Name)
	if name == "" {
		name = "System"
	}
	kind := strings.TrimSpace(author.Type)
	if kind == "" {
		kind = "source"
	}
	return IssueCommentAuthor{
		Type:  kind,
		Name:  name,
		Email: strings.TrimSpace(author.Email),
	}
}

func commentBodyValue(body *string) string {
	if body == nil {
		return ""
	}
	return *body
}

func writeIssueCommentAttachment(root, issueDir, commentID string, input IssueCommentAttachmentInput, now time.Time) (*IssueCommentAttachment, error) {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return nil, validationErrorf("attachment path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	filename := normalizeIssueCommentAttachmentFilename(filepath.Base(path))
	contentType, byteSize, tempPath, err := copyIssueCommentAttachmentTemp(root, file, filename, strings.TrimSpace(input.ContentType))
	if err != nil {
		return nil, err
	}
	defer func() {
		if strings.TrimSpace(tempPath) != "" {
			_ = os.Remove(tempPath)
		}
	}()

	attachmentID := generateID("cat")
	ext := filepath.Ext(filename)
	if ext == "" {
		if guessed, err := mime.ExtensionsByType(contentType); err == nil && len(guessed) > 0 {
			ext = guessed[0]
		}
	}
	finalPath := filepath.Join(issueDir, attachmentID+ext)
	if err := os.Rename(tempPath, finalPath); err != nil {
		return nil, err
	}
	tempPath = ""

	relPath, err := filepath.Rel(root, finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	return &IssueCommentAttachment{
		ID:          attachmentID,
		CommentID:   commentID,
		Filename:    filename,
		ContentType: contentType,
		ByteSize:    byteSize,
		StoragePath: filepath.ToSlash(relPath),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func copyIssueCommentAttachmentTemp(root string, src io.Reader, filename, contentType string) (string, int64, string, error) {
	tempFile, err := os.CreateTemp(root, ".issue-comment-attachment-*")
	if err != nil {
		return "", 0, "", err
	}
	defer tempFile.Close()

	limited := io.LimitReader(src, MaxIssueCommentAttachmentBytes+1)
	sniff := &limitedBuffer{limit: 512}
	written, err := io.Copy(io.MultiWriter(tempFile, sniff), limited)
	if err != nil {
		return "", 0, "", err
	}
	if written > MaxIssueCommentAttachmentBytes {
		_ = os.Remove(tempFile.Name())
		return "", 0, "", validationErrorf("comment attachment %s exceeds %d bytes", filename, MaxIssueCommentAttachmentBytes)
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = http.DetectContentType(sniff.Bytes())
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	return contentType, written, tempFile.Name(), nil
}

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	originalLen := len(p)
	if l.limit <= 0 {
		return originalLen, nil
	}
	if remaining := l.limit - l.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = l.buf.Write(p)
	}
	return originalLen, nil
}

func (l *limitedBuffer) Bytes() []byte {
	return l.buf.Bytes()
}

func normalizeIssueCommentAttachmentFilename(name string) string {
	base := strings.TrimSpace(filepath.Base(name))
	base = strings.ReplaceAll(base, string(filepath.Separator), "_")
	if base == "" || base == "." {
		return "attachment"
	}
	return base
}
