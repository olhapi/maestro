package providers

import (
	"context"
	"os"

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
		IssueType:   input.IssueType,
		Cron:        input.Cron,
		Enabled:     input.Enabled,
		RuntimeName: input.RuntimeName,
		AgentName:   input.AgentName,
		AgentPrompt: input.AgentPrompt,
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

func (p *KanbanProvider) ListIssueComments(_ context.Context, _ *kanban.Project, issue *kanban.Issue) ([]kanban.IssueComment, error) {
	return p.store.ListIssueComments(issue.ID)
}

func (p *KanbanProvider) CreateIssueComment(_ context.Context, _ *kanban.Project, issue *kanban.Issue, input IssueCommentInput) (*kanban.IssueComment, error) {
	return p.store.CreateIssueComment(issue.ID, toKanbanIssueCommentInput(input))
}

func (p *KanbanProvider) UpdateIssueComment(_ context.Context, _ *kanban.Project, issue *kanban.Issue, commentID string, input IssueCommentInput) (*kanban.IssueComment, error) {
	return p.store.UpdateIssueComment(issue.ID, commentID, toKanbanIssueCommentInput(input))
}

func (p *KanbanProvider) DeleteIssueComment(_ context.Context, _ *kanban.Project, issue *kanban.Issue, commentID string) error {
	return p.store.DeleteIssueComment(issue.ID, commentID)
}

func (p *KanbanProvider) GetIssueCommentAttachmentContent(_ context.Context, _ *kanban.Project, issue *kanban.Issue, commentID, attachmentID string) (*IssueCommentAttachmentContent, error) {
	attachment, path, err := p.store.GetIssueCommentAttachmentContent(issue.ID, commentID, attachmentID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &IssueCommentAttachmentContent{
		Attachment: *attachment,
		Content:    file,
	}, nil
}

func toKanbanIssueCommentInput(input IssueCommentInput) kanban.IssueCommentInput {
	attachments := make([]kanban.IssueCommentAttachmentInput, 0, len(input.Attachments))
	for _, attachment := range input.Attachments {
		attachments = append(attachments, kanban.IssueCommentAttachmentInput{
			Path:        attachment.Path,
			ContentType: attachment.ContentType,
		})
	}
	return kanban.IssueCommentInput{
		Body:                input.Body,
		ParentCommentID:     input.ParentCommentID,
		Attachments:         attachments,
		RemoveAttachmentIDs: append([]string(nil), input.RemoveAttachmentIDs...),
		Author:              input.Author,
	}
}
