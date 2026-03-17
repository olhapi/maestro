package providers

import (
	"context"

	"github.com/olhapi/maestro/internal/kanban"
)

type KanbanProvider struct {
	store *kanban.Store
}

func NewKanbanProvider(store *kanban.Store) *KanbanProvider {
	return &KanbanProvider{store: store}
}

func (p *KanbanProvider) Kind() string {
	return kanban.ProviderKindKanban
}

func (p *KanbanProvider) Capabilities() kanban.ProviderCapabilities {
	return kanban.DefaultCapabilities(kanban.ProviderKindKanban)
}

func (p *KanbanProvider) ValidateProject(context.Context, *kanban.Project) error {
	return nil
}

func (p *KanbanProvider) ListIssues(_ context.Context, project *kanban.Project, query kanban.IssueQuery) ([]kanban.Issue, error) {
	filter := map[string]interface{}{}
	if project != nil && project.ID != "" {
		filter["project_id"] = project.ID
	}
	if query.EpicID != "" {
		filter["epic_id"] = query.EpicID
	}
	if query.State != "" {
		filter["state"] = query.State
	}
	if query.IssueType != "" {
		filter["issue_type"] = query.IssueType
	}
	return p.store.ListIssues(filter)
}

func (p *KanbanProvider) GetIssue(_ context.Context, _ *kanban.Project, identifier string) (*kanban.Issue, error) {
	return p.store.GetIssueByIdentifier(identifier)
}

func (p *KanbanProvider) CreateIssue(_ context.Context, project *kanban.Project, input IssueCreateInput) (*kanban.Issue, error) {
	projectID := input.ProjectID
	if project != nil && project.ID != "" {
		projectID = project.ID
	}
	issue, err := p.store.CreateIssueWithOptions(projectID, input.EpicID, input.Title, input.Description, input.Priority, input.Labels, kanban.IssueCreateOptions{
		IssueType: input.IssueType,
		Cron:      input.Cron,
		Enabled:   input.Enabled,
	})
	if err != nil {
		return nil, err
	}
	updates := map[string]interface{}{
		"agent_name":   input.AgentName,
		"agent_prompt": input.AgentPrompt,
		"blocked_by":   input.BlockedBy,
		"branch_name":  input.BranchName,
		"pr_url":       input.PRURL,
	}
	if err := p.store.UpdateIssue(issue.ID, updates); err != nil {
		return nil, err
	}
	if input.State != "" && input.State != string(kanban.StateBacklog) {
		if err := p.store.UpdateIssueState(issue.ID, kanban.State(input.State)); err != nil {
			return nil, err
		}
	}
	return p.store.GetIssue(issue.ID)
}

func (p *KanbanProvider) UpdateIssue(_ context.Context, _ *kanban.Project, issue *kanban.Issue, updates map[string]interface{}) (*kanban.Issue, error) {
	if err := p.store.UpdateIssue(issue.ID, updates); err != nil {
		return nil, err
	}
	return p.store.GetIssue(issue.ID)
}

func (p *KanbanProvider) DeleteIssue(_ context.Context, _ *kanban.Project, issue *kanban.Issue) error {
	return p.store.DeleteIssue(issue.ID)
}

func (p *KanbanProvider) SetIssueState(_ context.Context, _ *kanban.Project, issue *kanban.Issue, state string) (*kanban.Issue, error) {
	if err := p.store.UpdateIssueState(issue.ID, kanban.State(state)); err != nil {
		return nil, err
	}
	return p.store.GetIssue(issue.ID)
}

func (p *KanbanProvider) CreateIssueComment(context.Context, *kanban.Project, *kanban.Issue, IssueCommentInput) error {
	return ErrUnsupportedCapability
}
