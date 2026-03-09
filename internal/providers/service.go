package providers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/olhapi/maestro/internal/kanban"
)

type Service struct {
	store     *kanban.Store
	providers map[string]Provider
}

func NewService(store *kanban.Store) *Service {
	return &Service{
		store: store,
		providers: map[string]Provider{
			kanban.ProviderKindKanban: NewKanbanProvider(store),
			kanban.ProviderKindLinear: NewLinearProvider(),
		},
	}
}

func (s *Service) ProviderForProject(project *kanban.Project) Provider {
	if project == nil {
		return s.providers[kanban.ProviderKindKanban]
	}
	if provider, ok := s.providers[normalizeKind(project.ProviderKind)]; ok {
		return provider
	}
	return s.providers[kanban.ProviderKindKanban]
}

func (s *Service) providerForKind(kind string) Provider {
	if provider, ok := s.providers[normalizeKind(kind)]; ok {
		return provider
	}
	return s.providers[kanban.ProviderKindKanban]
}

func (s *Service) resolveProjectProvider(projectID string) (*kanban.Project, Provider, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, s.providers[kanban.ProviderKindKanban], nil
	}
	project, err := s.store.GetProject(projectID)
	if err != nil {
		return nil, nil, err
	}
	return project, s.ProviderForProject(project), nil
}

func (s *Service) resolveIssueProvider(issue *kanban.Issue) (*kanban.Project, Provider, error) {
	if issue == nil {
		return nil, nil, fmt.Errorf("issue is required")
	}
	if strings.TrimSpace(issue.ProjectID) == "" {
		return nil, s.providerForKind(issue.ProviderKind), nil
	}
	project, err := s.store.GetProject(issue.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	return project, s.ProviderForProject(project), nil
}

func (s *Service) CreateProject(ctx context.Context, name, description, repoPath, workflowPath, providerKind, providerProjectRef string, providerConfig map[string]interface{}) (*kanban.Project, error) {
	project, err := s.store.CreateProjectWithProvider(name, description, repoPath, workflowPath, providerKind, providerProjectRef, providerConfig)
	if err != nil {
		return nil, err
	}
	if err := s.ProviderForProject(project).ValidateProject(ctx, project); err != nil {
		return nil, err
	}
	return s.store.GetProject(project.ID)
}

func (s *Service) UpdateProject(ctx context.Context, id, name, description, repoPath, workflowPath, providerKind, providerProjectRef string, providerConfig map[string]interface{}) error {
	if err := s.store.UpdateProjectWithProvider(id, name, description, repoPath, workflowPath, providerKind, providerProjectRef, providerConfig); err != nil {
		return err
	}
	project, err := s.store.GetProject(id)
	if err != nil {
		return err
	}
	return s.ProviderForProject(project).ValidateProject(ctx, project)
}

func (s *Service) ListProjectSummaries() ([]kanban.ProjectSummary, error) {
	return s.store.ListProjectSummaries()
}

func (s *Service) CreateEpic(projectID, name, description string) (*kanban.Epic, error) {
	project, err := s.store.GetProject(projectID)
	if err != nil {
		return nil, err
	}
	if !s.ProviderForProject(project).Capabilities().Epics {
		return nil, ErrUnsupportedCapability
	}
	return s.store.CreateEpic(projectID, name, description)
}

func (s *Service) UpdateEpic(id, projectID, name, description string) error {
	project, err := s.store.GetProject(projectID)
	if err != nil {
		return err
	}
	if !s.ProviderForProject(project).Capabilities().Epics {
		return ErrUnsupportedCapability
	}
	return s.store.UpdateEpic(id, projectID, name, description)
}

func (s *Service) DeleteEpic(id string) error {
	epic, err := s.store.GetEpic(id)
	if err != nil {
		return err
	}
	if epic.ProjectID != "" {
		project, err := s.store.GetProject(epic.ProjectID)
		if err == nil && !s.ProviderForProject(project).Capabilities().Epics {
			return ErrUnsupportedCapability
		}
	}
	return s.store.DeleteEpic(id)
}

func (s *Service) ListEpicSummaries(projectID string) ([]kanban.EpicSummary, error) {
	if strings.TrimSpace(projectID) != "" {
		project, err := s.store.GetProject(projectID)
		if err != nil {
			return nil, err
		}
		if !s.ProviderForProject(project).Capabilities().Epics {
			return []kanban.EpicSummary{}, nil
		}
	}
	return s.store.ListEpicSummaries(projectID)
}

func (s *Service) SyncIssues(ctx context.Context, query kanban.IssueQuery) error {
	projects, err := s.store.ListProjects()
	if err != nil {
		return err
	}
	for i := range projects {
		project := projects[i]
		if query.ProjectID != "" && project.ID != query.ProjectID {
			continue
		}
		provider := s.ProviderForProject(&project)
		if provider.Kind() == kanban.ProviderKindKanban {
			continue
		}
		issues, err := provider.ListIssues(ctx, &project, query)
		if err != nil {
			return err
		}
		if err := s.reconcileProviderIssues(project.ID, provider.Kind(), issues); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileProviderIssues(projectID, providerKind string, issues []kanban.Issue) error {
	refs := make([]string, 0, len(issues))
	for i := range issues {
		refs = append(refs, strings.TrimSpace(issues[i].ProviderIssueRef))
		if _, err := s.store.UpsertProviderIssue(projectID, &issues[i]); err != nil {
			return err
		}
	}
	return s.store.DeleteProviderIssuesExcept(projectID, providerKind, refs)
}

func (s *Service) RefreshIssue(ctx context.Context, issue *kanban.Issue) (*kanban.Issue, error) {
	if issue == nil {
		return nil, fmt.Errorf("issue is required")
	}
	project, provider, err := s.resolveIssueProvider(issue)
	if err != nil {
		return nil, err
	}
	if provider.Kind() == kanban.ProviderKindKanban {
		return s.store.GetIssue(issue.ID)
	}
	refreshed, err := provider.GetIssue(ctx, project, issue.Identifier)
	if err != nil {
		if kanban.IsNotFound(err) && issue.ProviderShadow {
			if deleteErr := s.store.DeleteIssue(issue.ID); deleteErr != nil && !kanban.IsNotFound(deleteErr) {
				return nil, deleteErr
			}
		}
		return nil, err
	}
	return s.store.UpsertProviderIssue(project.ID, refreshed)
}

func (s *Service) GetIssueByIdentifier(ctx context.Context, identifier string) (*kanban.Issue, error) {
	issue, err := s.store.GetIssueByIdentifier(identifier)
	if err == nil {
		_, provider, providerErr := s.resolveIssueProvider(issue)
		if providerErr == nil && provider.Kind() != kanban.ProviderKindKanban {
			return s.RefreshIssue(ctx, issue)
		}
		return issue, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	projects, listErr := s.store.ListProjects()
	if listErr != nil {
		return nil, listErr
	}
	for i := range projects {
		project := projects[i]
		provider := s.ProviderForProject(&project)
		if provider.Kind() == kanban.ProviderKindKanban {
			continue
		}
		refreshed, getErr := provider.GetIssue(ctx, &project, identifier)
		if getErr != nil {
			continue
		}
		return s.store.UpsertProviderIssue(project.ID, refreshed)
	}
	return nil, err
}

func (s *Service) GetIssueDetailByIdentifier(ctx context.Context, identifier string) (*kanban.IssueDetail, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	return s.store.GetIssueDetailByIdentifier(issue.Identifier)
}

func (s *Service) ListIssueSummaries(ctx context.Context, query kanban.IssueQuery) ([]kanban.IssueSummary, int, error) {
	if err := s.SyncIssues(ctx, query); err != nil {
		return nil, 0, err
	}
	return s.store.ListIssueSummaries(query)
}

func (s *Service) CreateIssue(ctx context.Context, input IssueCreateInput) (*kanban.IssueDetail, error) {
	project, provider, err := s.resolveProjectProvider(input.ProjectID)
	if err != nil {
		return nil, err
	}
	if input.EpicID != "" && !provider.Capabilities().Epics {
		return nil, ErrUnsupportedCapability
	}
	issue, err := provider.CreateIssue(ctx, project, input)
	if err != nil {
		return nil, err
	}
	switch provider.Kind() {
	case kanban.ProviderKindKanban:
		return s.store.GetIssueDetailByIdentifier(issue.Identifier)
	default:
		synced, err := s.store.UpsertProviderIssue(project.ID, issue)
		if err != nil {
			return nil, err
		}
		localUpdates := map[string]interface{}{
			"branch_name": input.BranchName,
			"pr_number":   input.PRNumber,
			"pr_url":      input.PRURL,
		}
		if err := s.store.UpdateIssue(synced.ID, localUpdates); err != nil {
			return nil, err
		}
		return s.store.GetIssueDetailByIdentifier(synced.Identifier)
	}
}

func (s *Service) UpdateIssue(ctx context.Context, identifier string, updates map[string]interface{}) (*kanban.IssueDetail, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	project, provider, err := s.resolveIssueProvider(issue)
	if err != nil {
		return nil, err
	}
	updated, err := provider.UpdateIssue(ctx, project, issue, updates)
	if err != nil {
		return nil, err
	}
	if provider.Kind() != kanban.ProviderKindKanban {
		if updated, err = s.store.UpsertProviderIssue(project.ID, updated); err != nil {
			return nil, err
		}
		localUpdates := map[string]interface{}{}
		for _, key := range []string{"branch_name", "pr_number", "pr_url"} {
			if value, ok := updates[key]; ok {
				localUpdates[key] = value
			}
		}
		if len(localUpdates) > 0 {
			if err := s.store.UpdateIssue(updated.ID, localUpdates); err != nil {
				return nil, err
			}
		}
		issue = updated
	}
	return s.store.GetIssueDetailByIdentifier(issue.Identifier)
}

func (s *Service) DeleteIssue(ctx context.Context, identifier string) error {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return err
	}
	project, provider, err := s.resolveIssueProvider(issue)
	if err != nil {
		return err
	}
	if err := provider.DeleteIssue(ctx, project, issue); err != nil {
		return err
	}
	if provider.Kind() == kanban.ProviderKindKanban {
		return nil
	}
	return s.store.DeleteIssue(issue.ID)
}

func (s *Service) SetIssueState(ctx context.Context, identifier, state string) (*kanban.IssueDetail, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	project, provider, err := s.resolveIssueProvider(issue)
	if err != nil {
		return nil, err
	}
	updated, err := provider.SetIssueState(ctx, project, issue, state)
	if err != nil {
		return nil, err
	}
	if provider.Kind() != kanban.ProviderKindKanban {
		if issue, err = s.store.UpsertProviderIssue(project.ID, updated); err != nil {
			return nil, err
		}
	} else {
		issue = updated
	}
	return s.store.GetIssueDetailByIdentifier(issue.Identifier)
}

func (s *Service) SyncForRepoPath(ctx context.Context, repoPath string) error {
	projects, err := s.store.ListProjects()
	if err != nil {
		return err
	}
	for i := range projects {
		project := projects[i]
		if repoPath != "" && project.RepoPath != repoPath {
			continue
		}
		provider := s.ProviderForProject(&project)
		if provider.Kind() == kanban.ProviderKindKanban {
			continue
		}
		query := kanban.IssueQuery{
			Assignee: strings.TrimSpace(providerConfigString(project.ProviderConfig, "assignee")),
		}
		issues, err := provider.ListIssues(ctx, &project, query)
		if err != nil {
			return err
		}
		if err := s.reconcileProviderIssues(project.ID, provider.Kind(), issues); err != nil {
			return err
		}
	}
	return nil
}

func providerConfigString(config map[string]interface{}, key string) string {
	if config == nil {
		return ""
	}
	value, ok := config[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func (s *Service) RefreshIssueByID(ctx context.Context, issueID string) (*kanban.Issue, error) {
	issue, err := s.store.GetIssue(issueID)
	if err != nil {
		return nil, err
	}
	return s.RefreshIssue(ctx, issue)
}

func IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedCapability)
}
