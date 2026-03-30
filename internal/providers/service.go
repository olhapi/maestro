package providers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

var (
	providerReadSyncTimeout     = 2 * time.Second
	providerProjectSyncTimeout  = 5 * time.Second
	providerListSyncMinInterval = time.Second
	ErrAmbiguousProviderIssue   = errors.New("ambiguous provider issue identifier")
)

type syncMode int

const (
	syncModeBlocking syncMode = iota
	syncModeBestEffort
)

type Service struct {
	store     *kanban.Store
	providers map[string]Provider
	syncMu    sync.Mutex
	lastSync  map[string]time.Time
	readOnly  bool
}

func NewService(store *kanban.Store) *Service {
	return newService(store, false)
}

func NewReadOnlyService(store *kanban.Store) *Service {
	return newService(store, true)
}

func newService(store *kanban.Store, readOnly bool) *Service {
	return &Service{
		store: store,
		providers: map[string]Provider{
			kanban.ProviderKindKanban: NewKanbanProvider(store),
		},
		lastSync: make(map[string]time.Time),
		readOnly: readOnly,
	}
}

func (s *Service) isReadOnlyStore() bool {
	return s != nil && (s.readOnly || (s.store != nil && s.store.ReadOnly()))
}

func (s *Service) RegisterProvider(provider Provider) {
	if s == nil || provider == nil {
		return
	}
	s.providers[normalizeKind(provider.Kind())] = provider
}

func (s *Service) ProviderForProject(project *kanban.Project) Provider {
	if project == nil {
		return s.providers[kanban.ProviderKindKanban]
	}
	if provider, ok := s.providers[normalizeKind(project.ProviderKind)]; ok {
		return provider
	}
	return nil
}

func (s *Service) providerForKind(kind string) Provider {
	if provider, ok := s.providers[normalizeKind(kind)]; ok {
		return provider
	}
	return nil
}

func unsupportedProviderKindError(kind string) error {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = kanban.ProviderKindKanban
	}
	return fmt.Errorf("%w: unsupported provider kind %q", kanban.ErrValidation, kind)
}

func (s *Service) providerForProjectOrError(project *kanban.Project) (Provider, error) {
	if project == nil {
		return s.providerForKindOrError(kanban.ProviderKindKanban)
	}
	return s.providerForKindOrError(project.ProviderKind)
}

func (s *Service) providerForKindOrError(kind string) (Provider, error) {
	provider := s.providerForKind(kind)
	if provider == nil {
		return nil, unsupportedProviderKindError(kind)
	}
	return provider, nil
}

func (s *Service) resolveProjectProvider(projectID string) (*kanban.Project, Provider, error) {
	if strings.TrimSpace(projectID) == "" {
		provider, err := s.providerForKindOrError(kanban.ProviderKindKanban)
		if err != nil {
			return nil, nil, err
		}
		return nil, provider, nil
	}
	project, err := s.store.GetProject(projectID)
	if err != nil {
		return nil, nil, err
	}
	provider, err := s.providerForProjectOrError(project)
	if err != nil {
		return nil, nil, err
	}
	return project, provider, nil
}

func (s *Service) resolveIssueProvider(issue *kanban.Issue) (*kanban.Project, Provider, error) {
	if issue == nil {
		return nil, nil, fmt.Errorf("issue is required")
	}
	issueProviderKind := normalizeKind(issue.ProviderKind)
	if strings.TrimSpace(issue.ProjectID) == "" {
		provider, err := s.providerForKindOrError(issueProviderKind)
		if err != nil {
			return nil, nil, err
		}
		return nil, provider, nil
	}
	project, err := s.store.GetProject(issue.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	projectProviderKind := normalizeKind(project.ProviderKind)
	if issueProviderKind != "" && issueProviderKind != projectProviderKind {
		provider, err := s.providerForKindOrError(issueProviderKind)
		if err != nil {
			return nil, nil, err
		}
		return project, provider, nil
	}
	provider, err := s.providerForProjectOrError(project)
	if err != nil {
		return nil, nil, err
	}
	return project, provider, nil
}

func normalizeProjectPaths(repoPath, workflowPath string) (string, string, error) {
	return kanban.ResolveProjectPaths(repoPath, workflowPath)
}

func cloneProjectProviderConfig(providerConfig map[string]interface{}) map[string]interface{} {
	if len(providerConfig) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(providerConfig))
	for key, value := range providerConfig {
		out[key] = value
	}
	return out
}

func buildProjectValidationCandidate(existing *kanban.Project, name, description, repoPath, workflowPath, providerKind, providerProjectRef string, providerConfig map[string]interface{}) (*kanban.Project, error) {
	repoPath, workflowPath, err := normalizeProjectPaths(repoPath, workflowPath)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	project := &kanban.Project{
		Name:               name,
		Description:        description,
		State:              kanban.ProjectStateStopped,
		PermissionProfile:  kanban.PermissionProfileDefault,
		RepoPath:           repoPath,
		WorkflowPath:       workflowPath,
		ProviderKind:       normalizeKind(providerKind),
		ProviderProjectRef: strings.TrimSpace(providerProjectRef),
		ProviderConfig:     cloneProjectProviderConfig(providerConfig),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if existing != nil {
		project.ID = existing.ID
		project.State = existing.State
		project.PermissionProfile = existing.PermissionProfile
		project.CreatedAt = existing.CreatedAt
		project.UpdatedAt = existing.UpdatedAt
	}
	return project, nil
}

func (s *Service) CreateProject(ctx context.Context, name, description, repoPath, workflowPath, providerKind, providerProjectRef string, providerConfig map[string]interface{}) (*kanban.Project, error) {
	candidate, err := buildProjectValidationCandidate(nil, name, description, repoPath, workflowPath, providerKind, providerProjectRef, providerConfig)
	if err != nil {
		return nil, err
	}
	provider, err := s.providerForProjectOrError(candidate)
	if err != nil {
		return nil, err
	}
	if err := provider.ValidateProject(ctx, candidate); err != nil {
		return nil, err
	}
	project, err := s.store.CreateProjectWithProvider(name, description, repoPath, workflowPath, providerKind, providerProjectRef, providerConfig)
	if err != nil {
		return nil, err
	}
	return s.store.GetProject(project.ID)
}

func (s *Service) UpdateProject(ctx context.Context, id, name, description, repoPath, workflowPath, providerKind, providerProjectRef string, providerConfig map[string]interface{}) error {
	current, err := s.store.GetProject(id)
	if err != nil {
		return err
	}
	candidate, err := buildProjectValidationCandidate(current, name, description, repoPath, workflowPath, providerKind, providerProjectRef, providerConfig)
	if err != nil {
		return err
	}
	provider, err := s.providerForProjectOrError(candidate)
	if err != nil {
		return err
	}
	if err := provider.ValidateProject(ctx, candidate); err != nil {
		return err
	}
	if err := s.store.UpdateProjectWithProvider(id, name, description, repoPath, workflowPath, providerKind, providerProjectRef, providerConfig); err != nil {
		return err
	}
	return nil
}

func (s *Service) ListProjectSummaries() ([]kanban.ProjectSummary, error) {
	return s.store.ListProjectSummaries()
}

func (s *Service) CreateEpic(projectID, name, description string) (*kanban.Epic, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project_id is required", kanban.ErrValidation)
	}
	project, err := s.store.GetProject(projectID)
	if err != nil {
		return nil, err
	}
	provider, err := s.providerForProjectOrError(project)
	if err != nil {
		return nil, err
	}
	if !provider.Capabilities().Epics {
		return nil, ErrUnsupportedCapability
	}
	return s.store.CreateEpic(projectID, name, description)
}

func (s *Service) UpdateEpic(id, projectID, name, description string) error {
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("%w: project_id is required", kanban.ErrValidation)
	}
	project, err := s.store.GetProject(projectID)
	if err != nil {
		return err
	}
	provider, err := s.providerForProjectOrError(project)
	if err != nil {
		return err
	}
	if !provider.Capabilities().Epics {
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
		if err != nil {
			return err
		}
		provider, providerErr := s.providerForProjectOrError(project)
		if providerErr != nil {
			return providerErr
		}
		if !provider.Capabilities().Epics {
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
		provider, err := s.providerForProjectOrError(project)
		if err != nil {
			return nil, err
		}
		if !provider.Capabilities().Epics {
			return []kanban.EpicSummary{}, nil
		}
	}
	return s.store.ListEpicSummaries(projectID)
}

func (s *Service) SyncIssues(ctx context.Context, query kanban.IssueQuery) error {
	if s.isReadOnlyStore() {
		return nil
	}
	return s.syncIssuesWithMode(ctx, query, syncModeBlocking)
}

func (s *Service) syncIssuesWithMode(ctx context.Context, query kanban.IssueQuery, mode syncMode) error {
	switch mode {
	case syncModeBestEffort:
		return s.syncIssuesBestEffort(ctx, query)
	default:
		return s.syncIssues(ctx, query)
	}
}

func (s *Service) syncIssuesBestEffort(ctx context.Context, query kanban.IssueQuery) error {
	syncQuery := authoritativeProviderSyncQuery(query)
	projects, err := s.store.ListProjects()
	if err != nil {
		return err
	}
	var firstUncachedErr error
	for i := range projects {
		project := projects[i]
		if query.ProjectID != "" && project.ID != query.ProjectID {
			continue
		}
		provider, err := s.providerForProjectOrError(&project)
		if err != nil {
			return err
		}
		if provider.Kind() == kanban.ProviderKindKanban {
			continue
		}
		readCtx, cancel, propagateParentContext := s.newReadSyncContext(ctx)
		issues, err := provider.ListIssues(readCtx, &project, syncQuery)
		cancel()
		if err != nil {
			if shouldPropagateReadSyncError(ctx, err, propagateParentContext) {
				return err
			}
			hasCache, cacheErr := s.hasCachedProviderIssues(project.ID, provider.Kind())
			if cacheErr != nil {
				return cacheErr
			}
			if !hasCache && firstUncachedErr == nil {
				firstUncachedErr = err
			}
			slog.Warn("Provider sync on read failed; serving cached issues for project",
				"query", query,
				"project_id", project.ID,
				"provider_kind", provider.Kind(),
				"error", err,
			)
			continue
		}
		if err := s.reconcileProviderIssues(project.ID, provider.Kind(), issues); err != nil {
			return err
		}
	}
	if firstUncachedErr != nil {
		return firstUncachedErr
	}
	return nil
}

func (s *Service) syncIssues(ctx context.Context, query kanban.IssueQuery) error {
	syncQuery := authoritativeProviderSyncQuery(query)
	projects, err := s.store.ListProjects()
	if err != nil {
		return err
	}
	for i := range projects {
		project := projects[i]
		if query.ProjectID != "" && project.ID != query.ProjectID {
			continue
		}
		provider, err := s.providerForProjectOrError(&project)
		if err != nil {
			return err
		}
		if provider.Kind() == kanban.ProviderKindKanban {
			continue
		}
		issues, err := provider.ListIssues(ctx, &project, syncQuery)
		if err != nil {
			return err
		}
		if err := s.reconcileProviderIssues(project.ID, provider.Kind(), issues); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) newReadSyncContext(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	return newBoundedContext(ctx, providerReadSyncTimeout)
}

func (s *Service) newProjectSyncContext(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	return newBoundedContext(ctx, providerProjectSyncTimeout)
}

func newBoundedContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		child, cancel := context.WithCancel(ctx)
		return child, cancel, true
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		child, cancel := context.WithDeadline(ctx, deadline)
		return child, cancel, true
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	return child, cancel, false
}

func shouldPropagateReadSyncError(parent context.Context, err error, propagateParentContext bool) bool {
	if err == nil {
		return false
	}
	if parent != nil && parent.Err() != nil && errors.Is(err, parent.Err()) {
		return true
	}
	if !propagateParentContext {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func (s *Service) reconcileProviderIssues(projectID, providerKind string, issues []kanban.Issue) error {
	return s.store.ReconcileProviderIssues(projectID, providerKind, issues)
}

func (s *Service) hasCachedProviderIssues(projectID, providerKind string) (bool, error) {
	return s.store.HasProviderIssues(projectID, providerKind)
}

func (s *Service) RefreshIssue(ctx context.Context, issue *kanban.Issue) (*kanban.Issue, error) {
	if issue == nil {
		return nil, fmt.Errorf("issue is required")
	}
	project, provider, err := s.resolveIssueProvider(issue)
	if err != nil {
		return nil, err
	}
	if s.isReadOnlyStore() {
		return issue, nil
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

func authoritativeProviderSyncQuery(query kanban.IssueQuery) kanban.IssueQuery {
	return kanban.IssueQuery{
		ProjectID: query.ProjectID,
		Assignee:  strings.TrimSpace(query.Assignee),
	}
}

func (s *Service) GetIssueByIdentifier(ctx context.Context, identifier string) (*kanban.Issue, error) {
	issue, err := s.store.GetIssueByIdentifier(identifier)
	if err == nil {
		_, provider, providerErr := s.resolveIssueProvider(issue)
		if providerErr != nil {
			return nil, providerErr
		}
		if provider != nil && provider.Kind() != kanban.ProviderKindKanban {
			if s.isReadOnlyStore() {
				return issue, nil
			}
			readCtx, cancel, propagateParentContext := s.newReadSyncContext(ctx)
			defer cancel()
			refreshed, refreshErr := s.RefreshIssue(readCtx, issue)
			if refreshErr == nil {
				return refreshed, nil
			}
			if shouldPropagateReadSyncError(ctx, refreshErr, propagateParentContext) {
				return nil, refreshErr
			}
			if kanban.IsNotFound(refreshErr) {
				return nil, refreshErr
			}
			slog.Warn("Provider issue refresh on read failed; serving cached issue",
				"identifier", identifier,
				"issue_id", issue.ID,
				"provider_kind", provider.Kind(),
				"error", refreshErr,
			)
			return issue, nil
		}
		return issue, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	if s.isReadOnlyStore() {
		return nil, err
	}
	projects, listErr := s.store.ListProjects()
	if listErr != nil {
		return nil, listErr
	}
	refreshed, refreshErr := s.lookupProviderIssueByIdentifier(ctx, projects, identifier)
	switch {
	case refreshErr == nil:
		return refreshed, nil
	case refreshErr != nil && refreshErr != sql.ErrNoRows:
		return nil, refreshErr
	}
	return nil, err
}

func (s *Service) lookupProviderIssueByIdentifier(ctx context.Context, projects []kanban.Project, identifier string) (*kanban.Issue, error) {
	type lookupResult struct {
		projectID string
		issue     *kanban.Issue
		err       error
	}

	externalProjects := make([]kanban.Project, 0, len(projects))
	for i := range projects {
		provider, err := s.providerForProjectOrError(&projects[i])
		if err != nil {
			return nil, err
		}
		if provider.Kind() == kanban.ProviderKindKanban {
			continue
		}
		externalProjects = append(externalProjects, projects[i])
	}
	if len(externalProjects) == 0 {
		return nil, sql.ErrNoRows
	}

	readCtx, cancel, propagateParentContext := s.newReadSyncContext(ctx)
	defer cancel()

	results := make(chan lookupResult, len(externalProjects))
	var wg sync.WaitGroup
	for i := range externalProjects {
		project := externalProjects[i]
		provider, err := s.providerForProjectOrError(&project)
		if err != nil {
			return nil, err
		}
		wg.Add(1)
		go func(project kanban.Project, provider Provider) {
			defer wg.Done()
			issue, err := provider.GetIssue(readCtx, &project, identifier)
			results <- lookupResult{projectID: project.ID, issue: issue, err: err}
		}(project, provider)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var (
		firstProviderErr error
		matches          []lookupResult
	)
	for result := range results {
		if shouldPropagateReadSyncError(ctx, result.err, propagateParentContext) {
			return nil, result.err
		}
		if result.err != nil {
			if !kanban.IsNotFound(result.err) && firstProviderErr == nil {
				firstProviderErr = result.err
			}
			continue
		}
		matches = append(matches, result)
	}
	switch len(matches) {
	case 1:
		return s.store.UpsertProviderIssue(matches[0].projectID, matches[0].issue)
	case 0:
	default:
		projectIDs := make([]string, 0, len(matches))
		for _, match := range matches {
			projectIDs = append(projectIDs, match.projectID)
		}
		sort.Strings(projectIDs)
		return nil, fmt.Errorf("%w: identifier %q matched multiple provider projects (%s)", ErrAmbiguousProviderIssue, identifier, strings.Join(projectIDs, ", "))
	}
	if firstProviderErr != nil {
		return nil, firstProviderErr
	}
	return nil, sql.ErrNoRows
}

func (s *Service) GetIssueDetailByIdentifier(ctx context.Context, identifier string) (*kanban.IssueDetail, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	return s.store.GetIssueDetailByIdentifier(issue.Identifier)
}

func (s *Service) ListIssueAssets(ctx context.Context, identifier string) ([]kanban.IssueAsset, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	return s.store.ListIssueAssets(issue.ID)
}

func (s *Service) AttachIssueAsset(ctx context.Context, identifier, filename string, src io.Reader) (*kanban.IssueAsset, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	return s.store.CreateIssueAsset(issue.ID, filename, src)
}

func (s *Service) AttachIssueAssetPath(ctx context.Context, identifier, path string) (*kanban.IssueAsset, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return s.AttachIssueAsset(ctx, identifier, filepath.Base(path), file)
}

func (s *Service) GetIssueAssetContent(ctx context.Context, identifier, assetID string) (*kanban.IssueAsset, string, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, "", err
	}
	return s.store.GetIssueAssetContent(issue.ID, assetID)
}

func (s *Service) DeleteIssueAsset(ctx context.Context, identifier, assetID string) error {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return err
	}
	return s.store.DeleteIssueAsset(issue.ID, assetID)
}

func (s *Service) ListIssueSummaries(ctx context.Context, query kanban.IssueQuery) ([]kanban.IssueSummary, int, error) {
	if err := s.syncIssueListIfNeeded(ctx, query); err != nil {
		return nil, 0, err
	}
	return s.store.ListIssueSummaries(query)
}

func (s *Service) BoardOverview(ctx context.Context, projectID string) (map[kanban.State]int, error) {
	query := kanban.IssueQuery{ProjectID: strings.TrimSpace(projectID)}
	if err := s.syncIssueListIfNeeded(ctx, query); err != nil {
		return nil, err
	}
	return s.store.CountIssuesByState(query.ProjectID)
}

func (s *Service) CreateIssue(ctx context.Context, input IssueCreateInput) (*kanban.IssueDetail, error) {
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	if input.ProjectID == "" {
		return nil, fmt.Errorf("%w: project_id is required", kanban.ErrValidation)
	}
	project, provider, err := s.resolveProjectProvider(input.ProjectID)
	if err != nil {
		return nil, err
	}
	projectProvider := provider
	if input.IssueType == kanban.IssueTypeRecurring {
		provider = s.providers[kanban.ProviderKindKanban]
	}
	if input.EpicID != "" && !provider.Capabilities().Epics {
		return nil, ErrUnsupportedCapability
	}
	if input.EpicID != "" && input.IssueType == kanban.IssueTypeRecurring && !projectProvider.Capabilities().Epics {
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
		localUpdates := providerIssueCreateLocalUpdates(issue, input)
		if len(localUpdates) > 0 {
			if err := s.store.UpdateIssue(synced.ID, localUpdates); err != nil {
				return nil, err
			}
		}
		return s.store.GetIssueDetailByIdentifier(synced.Identifier)
	}
}

func (s *Service) UpdateIssue(ctx context.Context, identifier string, updates map[string]interface{}) (*kanban.IssueDetail, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	targetIssueType := issue.IssueType
	if raw, ok := updates["issue_type"]; ok {
		targetIssueType, err = kanban.ParseIssueType(fmt.Sprint(raw))
		if err != nil {
			return nil, err
		}
		updates["issue_type"] = targetIssueType
	}
	providerBacked := normalizeKind(issue.ProviderKind) != kanban.ProviderKindKanban
	if providerBacked {
		projectID := strings.TrimSpace(issue.ProjectID)
		if projectID == "" {
			return nil, fmt.Errorf("%w: project_id is required", kanban.ErrValidation)
		}
		if raw, ok := updates["project_id"]; ok {
			if requestedProjectID, ok := trimmedStringUpdate(raw); ok && requestedProjectID != projectID {
				return nil, fmt.Errorf("%w: moving provider-backed issues between projects is not supported", ErrUnsupportedCapability)
			}
		}
		if _, ok := updates["cron"]; ok {
			return nil, fmt.Errorf("%w: recurring schedule updates are not supported for provider-backed issues", ErrUnsupportedCapability)
		}
		if _, ok := updates["enabled"]; ok {
			return nil, fmt.Errorf("%w: recurring schedule updates are not supported for provider-backed issues", ErrUnsupportedCapability)
		}
		if targetIssueType == kanban.IssueTypeRecurring {
			return nil, fmt.Errorf("%w: recurring issues must be created as local kanban issues", ErrUnsupportedCapability)
		}
		providerUpdates := providerIssueUpdatePayload(updates)
		localUpdates := providerIssueLocalUpdatePayload(issue, updates)
		if len(providerUpdates) == 0 {
			if len(localUpdates) == 0 {
				return s.store.GetIssueDetailByIdentifier(issue.Identifier)
			}
			if err := s.store.UpdateIssue(issue.ID, localUpdates); err != nil {
				return nil, err
			}
			return s.store.GetIssueDetailByIdentifier(issue.Identifier)
		}
		project, provider, err := s.resolveIssueProvider(issue)
		if err != nil {
			return nil, err
		}
		updated, err := provider.UpdateIssue(ctx, project, issue, providerUpdates)
		if err != nil {
			return nil, err
		}
		if updated, err = s.store.UpsertProviderIssue(project.ID, updated); err != nil {
			return nil, err
		}
		if len(localUpdates) > 0 {
			if err := s.store.UpdateIssue(updated.ID, localUpdates); err != nil {
				return nil, err
			}
		}
		return s.store.GetIssueDetailByIdentifier(updated.Identifier)
	}
	targetProjectID := strings.TrimSpace(issue.ProjectID)
	if raw, ok := updates["project_id"]; ok {
		targetProjectID = strings.TrimSpace(fmt.Sprint(raw))
	}
	if targetProjectID == "" {
		return nil, fmt.Errorf("%w: project_id is required", kanban.ErrValidation)
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

func (s *Service) CreateIssueComment(ctx context.Context, identifier string, input IssueCommentInput) error {
	_, err := s.CreateIssueCommentWithResult(ctx, identifier, input)
	return err
}

func (s *Service) ListIssueComments(ctx context.Context, identifier string) ([]kanban.IssueComment, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	project, provider, err := s.resolveIssueProvider(issue)
	if err != nil {
		return nil, err
	}
	comments, err := provider.ListIssueComments(ctx, project, issue)
	if err != nil {
		return nil, err
	}
	if comments == nil {
		return []kanban.IssueComment{}, nil
	}
	return comments, nil
}

func (s *Service) CreateIssueCommentWithResult(ctx context.Context, identifier string, input IssueCommentInput) (*kanban.IssueComment, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	project, provider, err := s.resolveIssueProvider(issue)
	if err != nil {
		return nil, err
	}
	return provider.CreateIssueComment(ctx, project, issue, input)
}

func (s *Service) UpdateIssueComment(ctx context.Context, identifier, commentID string, input IssueCommentInput) (*kanban.IssueComment, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	project, provider, err := s.resolveIssueProvider(issue)
	if err != nil {
		return nil, err
	}
	return provider.UpdateIssueComment(ctx, project, issue, commentID, input)
}

func (s *Service) DeleteIssueComment(ctx context.Context, identifier, commentID string) error {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return err
	}
	project, provider, err := s.resolveIssueProvider(issue)
	if err != nil {
		return err
	}
	return provider.DeleteIssueComment(ctx, project, issue, commentID)
}

func (s *Service) GetIssueCommentAttachmentContent(ctx context.Context, identifier, commentID, attachmentID string) (*IssueCommentAttachmentContent, error) {
	issue, err := s.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	project, provider, err := s.resolveIssueProvider(issue)
	if err != nil {
		return nil, err
	}
	return provider.GetIssueCommentAttachmentContent(ctx, project, issue, commentID, attachmentID)
}

func (s *Service) SyncForRepoPath(ctx context.Context, repoPath string) error {
	projects, err := s.store.ListProjects()
	if err != nil {
		return err
	}
	var firstErr error
	for i := range projects {
		project := projects[i]
		if repoPath != "" && project.RepoPath != repoPath {
			continue
		}
		provider, err := s.providerForProjectOrError(&project)
		if err != nil {
			return err
		}
		if provider.Kind() == kanban.ProviderKindKanban {
			continue
		}
		query := kanban.IssueQuery{
			Assignee: strings.TrimSpace(providerConfigString(project.ProviderConfig, "assignee")),
		}
		projectCtx, cancel, propagateParentContext := s.newProjectSyncContext(ctx)
		issues, err := provider.ListIssues(projectCtx, &project, query)
		cancel()
		if err != nil {
			if shouldPropagateReadSyncError(ctx, err, propagateParentContext) {
				return err
			}
			if firstErr == nil {
				firstErr = err
			}
			slog.Warn("Provider sync failed; continuing to next project",
				"project_id", project.ID,
				"provider_kind", provider.Kind(),
				"error", err,
			)
			continue
		}
		if err := s.reconcileProviderIssues(project.ID, provider.Kind(), issues); err != nil {
			return err
		}
	}
	return firstErr
}

func (s *Service) syncIssueListIfNeeded(ctx context.Context, query kanban.IssueQuery) error {
	if s.isReadOnlyStore() {
		return nil
	}
	syncQuery := authoritativeProviderSyncQuery(query)
	if !s.shouldSyncListQuery(syncQuery) {
		return nil
	}
	if err := s.syncIssuesWithMode(ctx, query, syncModeBestEffort); err != nil {
		return err
	}
	s.recordListSync(syncQuery)
	return nil
}

func (s *Service) shouldSyncListQuery(query kanban.IssueQuery) bool {
	key := s.listSyncKey(query)

	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	lastAt, ok := s.lastSync[key]
	if !ok {
		return true
	}
	return time.Since(lastAt) >= providerListSyncMinInterval
}

func (s *Service) recordListSync(query kanban.IssueQuery) {
	key := s.listSyncKey(query)

	s.syncMu.Lock()
	s.lastSync[key] = time.Now().UTC()
	s.syncMu.Unlock()
}

func (s *Service) listSyncKey(query kanban.IssueQuery) string {
	return strings.TrimSpace(query.ProjectID) + "|" + strings.TrimSpace(query.Assignee)
}

func buildProjectCandidate(existing *kanban.Project, name, description, repoPath, workflowPath, providerKind, providerProjectRef string, providerConfig map[string]interface{}) (*kanban.Project, error) {
	normalizedRepoPath, normalizedWorkflowPath, err := normalizeProjectPathsForValidation(repoPath, workflowPath)
	if err != nil {
		return nil, err
	}
	project := &kanban.Project{
		Name:               name,
		Description:        description,
		State:              kanban.ProjectStateStopped,
		PermissionProfile:  kanban.PermissionProfileDefault,
		RepoPath:           normalizedRepoPath,
		WorkflowPath:       normalizedWorkflowPath,
		ProviderKind:       normalizeKind(providerKind),
		ProviderProjectRef: strings.TrimSpace(providerProjectRef),
		ProviderConfig:     cloneProviderConfig(providerConfig),
	}
	if existing != nil {
		project.ID = existing.ID
		project.State = existing.State
		project.PermissionProfile = existing.PermissionProfile
		project.CreatedAt = existing.CreatedAt
		project.UpdatedAt = existing.UpdatedAt
	}
	return project, nil
}

func normalizeProjectPathsForValidation(repoPath, workflowPath string) (string, string, error) {
	return kanban.ResolveProjectPaths(repoPath, workflowPath)
}

func cloneProviderConfig(config map[string]interface{}) map[string]interface{} {
	if len(config) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(config))
	for key, value := range config {
		out[key] = value
	}
	return out
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

func providerIssueCreateLocalUpdates(providerIssue *kanban.Issue, input IssueCreateInput) map[string]interface{} {
	updates := make(map[string]interface{})
	for key, value := range map[string]string{
		"epic_id":      providerIssue.EpicID,
		"agent_name":   providerIssue.AgentName,
		"agent_prompt": providerIssue.AgentPrompt,
		"branch_name":  providerIssue.BranchName,
		"pr_url":       providerIssue.PRURL,
	} {
		if trimmed, ok := preferredStringValue(inputValueForKey(input, key), value); ok {
			updates[key] = trimmed
		}
	}
	return updates
}

func providerIssueUpdatePayload(updates map[string]interface{}) map[string]interface{} {
	providerUpdates := make(map[string]interface{})
	for _, key := range []string{"title", "description", "state", "priority", "labels", "blocked_by", "issue_type"} {
		if value, ok := updates[key]; ok {
			providerUpdates[key] = value
		}
	}
	return providerUpdates
}

func providerIssueLocalUpdatePayload(issue *kanban.Issue, updates map[string]interface{}) map[string]interface{} {
	localUpdates := make(map[string]interface{})
	addString := func(key, current string) {
		if raw, ok := updates[key]; ok {
			if value, ok := stringUpdateValue(raw); ok && value != strings.TrimSpace(current) {
				localUpdates[key] = value
			}
		}
	}
	addString("epic_id", issue.EpicID)
	addString("agent_name", issue.AgentName)
	addString("agent_prompt", issue.AgentPrompt)
	addString("branch_name", issue.BranchName)
	addString("pr_url", issue.PRURL)
	return localUpdates
}

func inputValueForKey(input IssueCreateInput, key string) string {
	switch key {
	case "epic_id":
		return input.EpicID
	case "agent_name":
		return input.AgentName
	case "agent_prompt":
		return input.AgentPrompt
	case "branch_name":
		return input.BranchName
	case "pr_url":
		return input.PRURL
	default:
		return ""
	}
}

func preferredStringValue(primary, fallback string) (string, bool) {
	for _, candidate := range []string{primary, fallback} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed, true
		}
	}
	return "", false
}

func stringUpdateValue(raw interface{}) (string, bool) {
	if raw == nil {
		return "", false
	}
	value := strings.TrimSpace(fmt.Sprint(raw))
	if value == "<nil>" {
		return "", false
	}
	return value, true
}

func trimmedStringUpdate(raw interface{}) (string, bool) {
	if raw == nil {
		return "", false
	}
	value := strings.TrimSpace(fmt.Sprint(raw))
	if value == "" || value == "<nil>" {
		return "", false
	}
	return value, true
}
