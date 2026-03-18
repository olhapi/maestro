package kanban

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type repeatingReader struct {
	remaining int64
	value     byte
}

func (r *repeatingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = r.value
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}

func writeCommentTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
	return path
}

func TestIssueCommentValidationAndRetrievalPaths(t *testing.T) {
	store := setupTestStore(t)

	if _, err := store.ListIssueComments(""); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for empty issue id, got %v", err)
	}
	if _, err := store.ListIssueComments("missing"); !IsNotFound(err) {
		t.Fatalf("expected not found for missing issue, got %v", err)
	}

	issue, err := store.CreateIssue("", "", "Validation coverage", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := store.CreateIssueComment(issue.ID, IssueCommentInput{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for empty comment, got %v", err)
	}

	commentDir := t.TempDir()
	attachmentPath := writeCommentTestFile(t, commentDir, "note.txt", "comment attachment")
	body := "Parent comment"
	parent, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
		Body: &body,
		Attachments: []IssueCommentAttachmentInput{{
			Path: attachmentPath,
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment parent: %v", err)
	}
	replyBody := "Reply"
	reply, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
		Body:            &replyBody,
		ParentCommentID: parent.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssueComment reply: %v", err)
	}
	if _, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
		Body:            &replyBody,
		ParentCommentID: reply.ID,
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for reply-to-reply, got %v", err)
	}

	gotComment, err := store.GetIssueComment(issue.ID, parent.ID)
	if err != nil {
		t.Fatalf("GetIssueComment: %v", err)
	}
	if gotComment.ID != parent.ID || len(gotComment.Attachments) != 1 {
		t.Fatalf("unexpected reloaded comment: %#v", gotComment)
	}

	gotAttachment, err := store.GetIssueCommentAttachment(issue.ID, parent.ID, parent.Attachments[0].ID)
	if err != nil {
		t.Fatalf("GetIssueCommentAttachment: %v", err)
	}
	if gotAttachment.Filename != "note.txt" {
		t.Fatalf("unexpected attachment metadata: %#v", gotAttachment)
	}
	if _, err := store.GetIssueCommentAttachment(issue.ID, parent.ID, "missing"); !IsNotFound(err) {
		t.Fatalf("expected missing attachment error, got %v", err)
	}

	storedPath, err := store.issueCommentAttachmentPath(parent.Attachments[0].StoragePath)
	if err != nil {
		t.Fatalf("issueCommentAttachmentPath stored: %v", err)
	}
	if err := os.Remove(storedPath); err != nil {
		t.Fatalf("Remove stored attachment: %v", err)
	}
	if _, _, err := store.GetIssueCommentAttachmentContent(issue.ID, parent.ID, parent.Attachments[0].ID); !IsNotFound(err) {
		t.Fatalf("expected missing attachment content error, got %v", err)
	}
	if _, err := store.issueCommentAttachmentPath("../escape.txt"); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for escaped storage path, got %v", err)
	}
}

func TestIssueCommentCreateAttachmentOnlyAndHardDeletePaths(t *testing.T) {
	store := setupTestStore(t)

	missingBody := "Missing"
	if _, err := store.CreateIssueComment("missing", IssueCommentInput{Body: &missingBody}); !IsNotFound(err) {
		t.Fatalf("expected missing issue error, got %v", err)
	}

	issue, err := store.CreateIssue("", "", "Attachment-only comments", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	commentDir := t.TempDir()
	attachmentPath := writeCommentTestFile(t, commentDir, "evidence", "attachment only")
	comment, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
		Attachments: []IssueCommentAttachmentInput{{
			Path: attachmentPath,
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment attachment-only: %v", err)
	}
	if comment.Body != "" || comment.Author.Name != "System" || comment.Author.Type != "source" {
		t.Fatalf("unexpected attachment-only comment metadata: %#v", comment)
	}
	if len(comment.Attachments) != 1 || !strings.Contains(comment.Attachments[0].ContentType, "text/plain") {
		t.Fatalf("unexpected attachment-only comment attachments: %#v", comment.Attachments)
	}

	if err := store.DeleteIssueComment(issue.ID, comment.ID); err != nil {
		t.Fatalf("DeleteIssueComment hard delete: %v", err)
	}
	comments, err := store.ListIssueComments(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueComments after hard delete: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("expected attachment-only comment to be removed, got %#v", comments)
	}
	if _, err := store.GetIssueComment(issue.ID, comment.ID); !IsNotFound(err) {
		t.Fatalf("expected deleted comment to be missing, got %v", err)
	}
	if err := store.DeleteIssueComment(issue.ID, comment.ID); !IsNotFound(err) {
		t.Fatalf("expected deleting missing comment to fail with not found, got %v", err)
	}
}

func TestIssueCommentUpdateAndDeleteHelpers(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Update coverage", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	commentDir := t.TempDir()
	firstAttachmentPath := writeCommentTestFile(t, commentDir, "first.txt", "first attachment")
	body := "Original body"
	comment, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
		Body: &body,
		Attachments: []IssueCommentAttachmentInput{{
			Path: firstAttachmentPath,
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}
	firstStoredPath, err := store.issueCommentAttachmentPath(comment.Attachments[0].StoragePath)
	if err != nil {
		t.Fatalf("issueCommentAttachmentPath first: %v", err)
	}

	secondAttachmentPath := writeCommentTestFile(t, commentDir, "second.txt", "second attachment")
	updatedBody := "Updated body"
	updated, err := store.UpdateIssueComment(issue.ID, comment.ID, IssueCommentInput{
		Body: &updatedBody,
		Attachments: []IssueCommentAttachmentInput{{
			Path: secondAttachmentPath,
		}},
		RemoveAttachmentIDs: []string{comment.Attachments[0].ID},
	})
	if err != nil {
		t.Fatalf("UpdateIssueComment: %v", err)
	}
	if updated.Body != updatedBody || len(updated.Attachments) != 1 || updated.Attachments[0].Filename != "second.txt" {
		t.Fatalf("unexpected updated comment: %#v", updated)
	}
	if _, err := os.Stat(firstStoredPath); !os.IsNotExist(err) {
		t.Fatalf("expected original attachment to be removed, stat err=%v", err)
	}

	noop, err := store.UpdateIssueComment(issue.ID, comment.ID, IssueCommentInput{})
	if err != nil {
		t.Fatalf("UpdateIssueComment noop: %v", err)
	}
	if noop.Body != updatedBody || len(noop.Attachments) != 1 {
		t.Fatalf("expected noop update to return current comment, got %#v", noop)
	}

	replyBody := "Reply keeps parent soft deleted"
	reply, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
		Body:            &replyBody,
		ParentCommentID: comment.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssueComment reply: %v", err)
	}
	if err := store.DeleteIssueComment(issue.ID, comment.ID); err != nil {
		t.Fatalf("DeleteIssueComment parent: %v", err)
	}
	if _, err := store.UpdateIssueComment(issue.ID, comment.ID, IssueCommentInput{Body: &updatedBody}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error editing soft-deleted comment, got %v", err)
	}
	if err := store.DeleteIssueComment(issue.ID, reply.ID); err != nil {
		t.Fatalf("DeleteIssueComment reply: %v", err)
	}
}

func TestIssueCommentDeletionTransactionsAndAttachmentHelpers(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Delete helpers", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	commentDir := t.TempDir()
	attachmentPath := writeCommentTestFile(t, commentDir, "cleanup.txt", "cleanup me")
	body := "With cleanup"
	comment, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
		Body: &body,
		Attachments: []IssueCommentAttachmentInput{{
			Path: attachmentPath,
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}
	storedPath, err := store.issueCommentAttachmentPath(comment.Attachments[0].StoragePath)
	if err != nil {
		t.Fatalf("issueCommentAttachmentPath: %v", err)
	}
	storedDir := filepath.Dir(storedPath)

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	attachments, err := store.deleteIssueCommentsTx(tx, issue.ID)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("deleteIssueCommentsTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected deleted attachment metadata, got %#v", attachments)
	}
	store.cleanupIssueCommentAttachmentPaths(attachments)

	comments, err := store.ListIssueComments(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("expected all comments deleted, got %#v", comments)
	}
	if _, err := os.Stat(storedPath); !os.IsNotExist(err) {
		t.Fatalf("expected stored attachment path removed, stat err=%v", err)
	}
	if _, err := os.Stat(storedDir); !os.IsNotExist(err) {
		t.Fatalf("expected empty attachment dir removed, stat err=%v", err)
	}

	root := t.TempDir()
	filename, byteSize, tempPath, err := copyIssueCommentAttachmentTemp(root, bytes.NewBufferString("plain text attachment"), "note", "")
	if err != nil {
		t.Fatalf("copyIssueCommentAttachmentTemp: %v", err)
	}
	if !strings.Contains(filename, "text/plain") || byteSize == 0 {
		t.Fatalf("unexpected copied attachment metadata: contentType=%q byteSize=%d", filename, byteSize)
	}
	if err := os.Remove(tempPath); err != nil {
		t.Fatalf("Remove tempPath: %v", err)
	}

	if _, _, _, err := copyIssueCommentAttachmentTemp(root, &repeatingReader{remaining: MaxIssueCommentAttachmentBytes + 1, value: 'a'}, "too-large.bin", "application/octet-stream"); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected size validation error, got %v", err)
	}

	issueDir := filepath.Join(root, "issue", "comment")
	if err := os.MkdirAll(issueDir, 0o755); err != nil {
		t.Fatalf("MkdirAll issueDir: %v", err)
	}
	if _, err := writeIssueCommentAttachment(root, issueDir, "comment", IssueCommentAttachmentInput{}, time.Now().UTC()); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for empty attachment path, got %v", err)
	}

	buffer := &limitedBuffer{limit: 4}
	if _, err := buffer.Write([]byte("abcdef")); err != nil {
		t.Fatalf("limitedBuffer.Write: %v", err)
	}
	if got := string(buffer.Bytes()); got != "abcd" {
		t.Fatalf("limitedBuffer.Bytes() = %q, want %q", got, "abcd")
	}
}
