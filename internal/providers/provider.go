package providers

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/olhapi/maestro/internal/kanban"
)

var ErrUnsupportedCapability = errors.New("unsupported_provider_capability")

type IssueCreateInput struct {
	ProjectID         string
	EpicID            string
	Title             string
	Description       string
	IssueType         kanban.IssueType
	Cron              string
	Enabled           *bool
	PermissionProfile kanban.PermissionProfile
	Priority          int
	Labels            []string
	AgentName         string
	AgentPrompt       string
	State             string
	BlockedBy         []string
	BranchName        string
	PRURL             string
}

type IssueCommentAttachment struct {
	Path        string
	ContentType string
}

type IssueCommentInput struct {
	Body                *string
	ParentCommentID     string
	Attachments         []IssueCommentAttachment
	RemoveAttachmentIDs []string
	Author              kanban.IssueCommentAuthor
}

type IssueCommentAttachmentContent struct {
	Attachment kanban.IssueCommentAttachment
	Content    io.ReadCloser
}

type Provider interface {
	Kind() string
	Capabilities() kanban.ProviderCapabilities
	ValidateProject(context.Context, *kanban.Project) error
	ListIssues(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error)
	GetIssue(context.Context, *kanban.Project, string) (*kanban.Issue, error)
	CreateIssue(context.Context, *kanban.Project, IssueCreateInput) (*kanban.Issue, error)
	UpdateIssue(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error)
	DeleteIssue(context.Context, *kanban.Project, *kanban.Issue) error
	SetIssueState(context.Context, *kanban.Project, *kanban.Issue, string) (*kanban.Issue, error)
	ListIssueComments(context.Context, *kanban.Project, *kanban.Issue) ([]kanban.IssueComment, error)
	CreateIssueComment(context.Context, *kanban.Project, *kanban.Issue, IssueCommentInput) (*kanban.IssueComment, error)
	UpdateIssueComment(context.Context, *kanban.Project, *kanban.Issue, string, IssueCommentInput) (*kanban.IssueComment, error)
	DeleteIssueComment(context.Context, *kanban.Project, *kanban.Issue, string) error
	GetIssueCommentAttachmentContent(context.Context, *kanban.Project, *kanban.Issue, string, string) (*IssueCommentAttachmentContent, error)
}

func normalizeKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return kanban.ProviderKindKanban
	}
	return kind
}
