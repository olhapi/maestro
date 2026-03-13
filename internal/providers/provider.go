package providers

import (
	"context"
	"errors"
	"strings"

	"github.com/olhapi/maestro/internal/kanban"
)

var ErrUnsupportedCapability = errors.New("unsupported_provider_capability")

type IssueCreateInput struct {
	ProjectID   string
	EpicID      string
	Title       string
	Description string
	IssueType   kanban.IssueType
	Cron        string
	Enabled     *bool
	Priority    int
	Labels      []string
	State       string
	BlockedBy   []string
	BranchName  string
	PRURL       string
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
}

func normalizeKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return kanban.ProviderKindKanban
	}
	return kind
}
