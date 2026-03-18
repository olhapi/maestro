package kanban

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssueCommentsThreadingAndAttachments(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Commented issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	attachmentPath := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(attachmentPath, []byte("hello comments"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	topBody := "Top-level comment"
	top, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
		Body: &topBody,
		Attachments: []IssueCommentAttachmentInput{{
			Path: attachmentPath,
		}},
		Author: IssueCommentAuthor{Type: "source", Name: "UI"},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment top: %v", err)
	}
	if len(top.Attachments) != 1 {
		t.Fatalf("expected attachment on top comment, got %#v", top.Attachments)
	}

	replyBody := "Reply comment"
	reply, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
		Body:            &replyBody,
		ParentCommentID: top.ID,
		Author:          IssueCommentAuthor{Type: "source", Name: "CLI"},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment reply: %v", err)
	}
	if reply.ParentCommentID != top.ID {
		t.Fatalf("expected reply parent %q, got %#v", top.ID, reply)
	}

	comments, err := store.ListIssueComments(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(comments) != 1 || len(comments[0].Replies) != 1 {
		t.Fatalf("expected one threaded comment tree, got %#v", comments)
	}

	attachment, path, err := store.GetIssueCommentAttachmentContent(issue.ID, top.ID, top.Attachments[0].ID)
	if err != nil {
		t.Fatalf("GetIssueCommentAttachmentContent: %v", err)
	}
	if attachment.Filename != "note.txt" {
		t.Fatalf("unexpected attachment metadata: %#v", attachment)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile attachment: %v", err)
	}
	if string(data) != "hello comments" {
		t.Fatalf("unexpected attachment content %q", string(data))
	}
}

func TestIssueCommentDeleteSoftAndHardDelete(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Delete comments", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	body := "Parent"
	parent, err := store.CreateIssueComment(issue.ID, IssueCommentInput{Body: &body})
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

	if err := store.DeleteIssueComment(issue.ID, parent.ID); err != nil {
		t.Fatalf("DeleteIssueComment parent: %v", err)
	}
	comments, err := store.ListIssueComments(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(comments) != 1 || comments[0].DeletedAt == nil || strings.TrimSpace(comments[0].Body) != "" {
		t.Fatalf("expected soft-deleted parent, got %#v", comments)
	}

	if err := store.DeleteIssueComment(issue.ID, reply.ID); err != nil {
		t.Fatalf("DeleteIssueComment reply: %v", err)
	}
	comments, err = store.ListIssueComments(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueComments second: %v", err)
	}
	if len(comments) != 1 || len(comments[0].Replies) != 0 {
		t.Fatalf("expected hard-deleted reply only, got %#v", comments)
	}
}

func TestIssueCommentUpdateRejectsRemovingAllContent(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Reject empty updates", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	attachmentPath := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(attachmentPath, []byte("hello comments"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	body := "Original comment"
	comment, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
		Body: &body,
		Attachments: []IssueCommentAttachmentInput{{
			Path: attachmentPath,
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}

	emptyBody := ""
	updated, err := store.UpdateIssueComment(issue.ID, comment.ID, IssueCommentInput{
		Body:                &emptyBody,
		RemoveAttachmentIDs: []string{comment.Attachments[0].ID},
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error, got updated=%#v err=%v", updated, err)
	}

	reloaded, err := store.GetIssueComment(issue.ID, comment.ID)
	if err != nil {
		t.Fatalf("GetIssueComment: %v", err)
	}
	if reloaded.Body != body || len(reloaded.Attachments) != 1 {
		t.Fatalf("expected comment to remain unchanged, got %#v", reloaded)
	}
}
