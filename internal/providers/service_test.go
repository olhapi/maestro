package providers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

type stubProvider struct {
	kind      string
	issues    []kanban.Issue
	lastQuery kanban.IssueQuery
	listErr   error
	listGate  <-chan struct{}
	listFunc  func(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error)
	getIssue  *kanban.Issue
	getErr    error
	getGate   <-chan struct{}
	getFunc   func(context.Context, *kanban.Project, string) (*kanban.Issue, error)
}

func (p *stubProvider) Kind() string {
	return p.kind
}

func (p *stubProvider) Capabilities() kanban.ProviderCapabilities {
	return kanban.DefaultCapabilities(p.kind)
}

func (p *stubProvider) ValidateProject(context.Context, *kanban.Project) error {
	return nil
}

func (p *stubProvider) ListIssues(ctx context.Context, project *kanban.Project, query kanban.IssueQuery) ([]kanban.Issue, error) {
	p.lastQuery = query
	if p.listFunc != nil {
		return p.listFunc(ctx, project, query)
	}
	if p.listGate != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.listGate:
		}
	}
	if p.listErr != nil {
		return nil, p.listErr
	}
	out := make([]kanban.Issue, len(p.issues))
	copy(out, p.issues)
	return out, nil
}

func (p *stubProvider) GetIssue(ctx context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
	if p.getFunc != nil {
		return p.getFunc(ctx, project, identifier)
	}
	if p.getGate != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.getGate:
		}
	}
	if p.getErr != nil {
		return nil, p.getErr
	}
	if p.getIssue == nil {
		return nil, kanban.ErrNotFound
	}
	cp := *p.getIssue
	return &cp, nil
}

func withProviderReadTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	previous := providerReadSyncTimeout
	providerReadSyncTimeout = timeout
	t.Cleanup(func() {
		providerReadSyncTimeout = previous
	})
}

func (p *stubProvider) CreateIssue(context.Context, *kanban.Project, IssueCreateInput) (*kanban.Issue, error) {
	return nil, ErrUnsupportedCapability
}

func (p *stubProvider) UpdateIssue(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error) {
	return nil, ErrUnsupportedCapability
}

func (p *stubProvider) DeleteIssue(context.Context, *kanban.Project, *kanban.Issue) error {
	return ErrUnsupportedCapability
}

func (p *stubProvider) SetIssueState(context.Context, *kanban.Project, *kanban.Issue, string) (*kanban.Issue, error) {
	return nil, ErrUnsupportedCapability
}

func TestServiceSyncIssuesPrunesStaleProviderShadowIssues(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Linear Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	stale, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     kanban.ProviderKindLinear,
		ProviderIssueRef: "linear-stale",
		Identifier:       "LIN-STALE",
		Title:            "Stale issue",
		State:            kanban.StateBacklog,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue stale: %v", err)
	}
	if _, err := store.CreateWorkspace(stale.ID, filepath.Join(t.TempDir(), "workspace")); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = &stubProvider{
		kind: kanban.ProviderKindLinear,
		issues: []kanban.Issue{
			{
				ProviderKind:     kanban.ProviderKindLinear,
				ProviderIssueRef: "linear-keep",
				Identifier:       "LIN-KEEP",
				Title:            "Kept issue",
				State:            kanban.StateReady,
			},
		},
	}

	if err := svc.SyncIssues(context.Background(), kanban.IssueQuery{ProjectID: project.ID}); err != nil {
		t.Fatalf("SyncIssues: %v", err)
	}

	if _, err := store.GetIssueByProviderRef(kanban.ProviderKindLinear, "linear-stale"); !kanban.IsNotFound(err) {
		t.Fatalf("expected stale provider issue to be deleted, got %v", err)
	}
	if _, err := store.GetWorkspace(stale.ID); err == nil {
		t.Fatal("expected stale workspace to be removed with the provider issue")
	}

	kept, err := store.GetIssueByProviderRef(kanban.ProviderKindLinear, "linear-keep")
	if err != nil {
		t.Fatalf("GetIssueByProviderRef keep: %v", err)
	}
	if kept.Identifier != "LIN-KEEP" {
		t.Fatalf("expected kept issue to be synced, got %q", kept.Identifier)
	}
}

func TestServiceSyncForRepoPathPassesProviderAssigneeFilter(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	repoPath := t.TempDir()
	project, err := store.CreateProjectWithProvider(
		"Linear Project",
		"",
		repoPath,
		"",
		kanban.ProviderKindLinear,
		"proj-slug",
		map[string]interface{}{
			"project_slug": "proj-slug",
			"assignee":     "me",
		},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	provider := &stubProvider{
		kind: kanban.ProviderKindLinear,
		issues: []kanban.Issue{
			{
				ProviderKind:     kanban.ProviderKindLinear,
				ProviderIssueRef: "linear-keep",
				Identifier:       "LIN-KEEP",
				Title:            "Kept issue",
				State:            kanban.StateReady,
			},
		},
	}
	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = provider

	if err := svc.SyncForRepoPath(context.Background(), repoPath); err != nil {
		t.Fatalf("SyncForRepoPath: %v", err)
	}

	if provider.lastQuery.Assignee != "me" {
		t.Fatalf("expected assignee filter to be forwarded, got %#v", provider.lastQuery)
	}
	if _, err := store.GetIssueByProviderRef(kanban.ProviderKindLinear, "linear-keep"); err != nil {
		t.Fatalf("expected synced issue for project %s: %v", project.ID, err)
	}
}

func TestServiceListIssueSummariesServesCachedDataWhenReadSyncTimesOut(t *testing.T) {
	withProviderReadTimeout(t, 20*time.Millisecond)

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Linear Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	if _, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     kanban.ProviderKindLinear,
		ProviderIssueRef: "linear-keep",
		Identifier:       "LIN-KEEP",
		Title:            "Cached issue",
		State:            kanban.StateReady,
	}); err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	gate := make(chan struct{})
	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = &stubProvider{
		kind:     kanban.ProviderKindLinear,
		listGate: gate,
	}

	items, total, err := svc.ListIssueSummaries(context.Background(), kanban.IssueQuery{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListIssueSummaries: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected cached provider issue after timeout, got total=%d items=%#v", total, items)
	}
	if items[0].Identifier != "LIN-KEEP" {
		t.Fatalf("unexpected cached issue payload: %#v", items[0])
	}
}

func TestServiceListIssueSummariesPropagatesParentDeadlineExceeded(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Linear Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	if _, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     kanban.ProviderKindLinear,
		ProviderIssueRef: "linear-keep",
		Identifier:       "LIN-KEEP",
		Title:            "Cached issue",
		State:            kanban.StateReady,
	}); err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = &stubProvider{
		kind:     kanban.ProviderKindLinear,
		listGate: make(chan struct{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, _, err = svc.ListIssueSummaries(ctx, kanban.IssueQuery{ProjectID: project.ID})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected parent deadline to be propagated, got %v", err)
	}
}

func TestServiceGetIssueByIdentifierServesCachedProviderIssueWhenRefreshFails(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Linear Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	cached, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     kanban.ProviderKindLinear,
		ProviderIssueRef: "linear-1",
		Identifier:       "LIN-1",
		Title:            "Cached issue",
		State:            kanban.StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = &stubProvider{
		kind:   kanban.ProviderKindLinear,
		getErr: errors.New("linear unavailable"),
	}

	issue, err := svc.GetIssueByIdentifier(context.Background(), cached.Identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier: %v", err)
	}
	if issue.ID != cached.ID || issue.Title != cached.Title {
		t.Fatalf("expected cached issue after refresh failure, got %#v", issue)
	}
}

func TestServiceGetIssueByIdentifierPropagatesParentDeadlineExceeded(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Linear Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	cached, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     kanban.ProviderKindLinear,
		ProviderIssueRef: "linear-1",
		Identifier:       "LIN-1",
		Title:            "Cached issue",
		State:            kanban.StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = &stubProvider{
		kind:    kanban.ProviderKindLinear,
		getGate: make(chan struct{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = svc.GetIssueByIdentifier(ctx, cached.Identifier)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected parent deadline to be propagated, got %v", err)
	}
}

func TestServiceListIssueSummariesBestEffortContinuesAcrossProjects(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	slowProject, err := store.CreateProjectWithProvider(
		"Slow Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"slow-proj",
		map[string]interface{}{"project_slug": "slow-proj"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider slow: %v", err)
	}
	fastProject, err := store.CreateProjectWithProvider(
		"Fast Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"fast-proj",
		map[string]interface{}{"project_slug": "fast-proj"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider fast: %v", err)
	}

	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = &stubProvider{
		kind: kanban.ProviderKindLinear,
		listFunc: func(_ context.Context, project *kanban.Project, _ kanban.IssueQuery) ([]kanban.Issue, error) {
			switch project.ProviderProjectRef {
			case slowProject.ProviderProjectRef:
				return nil, context.DeadlineExceeded
			case fastProject.ProviderProjectRef:
				return []kanban.Issue{{
					ProviderKind:     kanban.ProviderKindLinear,
					ProviderIssueRef: "linear-fast-1",
					Identifier:       "LIN-FAST-1",
					Title:            "Fast issue",
					State:            kanban.StateReady,
				}}, nil
			default:
				return nil, nil
			}
		},
	}

	items, total, err := svc.ListIssueSummaries(context.Background(), kanban.IssueQuery{})
	if err != nil {
		t.Fatalf("ListIssueSummaries: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected the healthy project to sync, got total=%d items=%#v", total, items)
	}
	if items[0].Identifier != "LIN-FAST-1" || items[0].ProjectID != fastProject.ID {
		t.Fatalf("unexpected synced issue payload: %#v", items[0])
	}
}

func TestServiceGetIssueByIdentifierColdMissContinuesAcrossProjects(t *testing.T) {
	withProviderReadTimeout(t, 20*time.Millisecond)

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	slowProject, err := store.CreateProjectWithProvider(
		"Slow Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"slow-proj",
		map[string]interface{}{"project_slug": "slow-proj"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider slow: %v", err)
	}
	fastProject, err := store.CreateProjectWithProvider(
		"Fast Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"fast-proj",
		map[string]interface{}{"project_slug": "fast-proj"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider fast: %v", err)
	}

	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = &stubProvider{
		kind: kanban.ProviderKindLinear,
		getFunc: func(ctx context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
			switch project.ProviderProjectRef {
			case slowProject.ProviderProjectRef:
				<-ctx.Done()
				return nil, ctx.Err()
			case fastProject.ProviderProjectRef:
				return &kanban.Issue{
					ProviderKind:     kanban.ProviderKindLinear,
					ProviderIssueRef: "linear-fast-1",
					Identifier:       identifier,
					Title:            "Fast issue",
					State:            kanban.StateReady,
				}, nil
			default:
				return nil, kanban.ErrNotFound
			}
		},
	}

	issue, err := svc.GetIssueByIdentifier(context.Background(), "LIN-FAST-1")
	if err != nil {
		t.Fatalf("GetIssueByIdentifier: %v", err)
	}
	if issue.ProjectID != fastProject.ID || issue.Identifier != "LIN-FAST-1" {
		t.Fatalf("expected cold miss lookup to continue to the healthy project, got %#v", issue)
	}
}

func TestServiceGetIssueByIdentifierColdMissReturnsProviderErrorWhenAllProjectsFail(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.CreateProjectWithProvider(
		"Linear Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	); err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	providerErr := errors.New("linear unavailable")
	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = &stubProvider{
		kind:   kanban.ProviderKindLinear,
		getErr: providerErr,
	}

	_, err = svc.GetIssueByIdentifier(context.Background(), "LIN-MISS")
	if !errors.Is(err, providerErr) {
		t.Fatalf("expected provider error on cold miss, got %v", err)
	}
}

func TestServiceGetIssueByIdentifierColdMissReturnsBoundedProviderTimeout(t *testing.T) {
	withProviderReadTimeout(t, 20*time.Millisecond)

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.CreateProjectWithProvider(
		"Linear Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	); err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = &stubProvider{
		kind:    kanban.ProviderKindLinear,
		getGate: make(chan struct{}),
	}

	start := time.Now()
	_, err = svc.GetIssueByIdentifier(context.Background(), "LIN-MISS")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded provider timeout to surface, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("expected bounded remote probe, took %v", elapsed)
	}
}

func TestServiceListIssueSummariesReturnsProviderErrorWhenNoCachedResults(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Linear Project",
		"",
		"",
		"",
		kanban.ProviderKindLinear,
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	providerErr := errors.New("linear unavailable")
	svc := NewService(store)
	svc.providers[kanban.ProviderKindLinear] = &stubProvider{
		kind:    kanban.ProviderKindLinear,
		listErr: providerErr,
	}

	_, _, err = svc.ListIssueSummaries(context.Background(), kanban.IssueQuery{ProjectID: project.ID})
	if !errors.Is(err, providerErr) {
		t.Fatalf("expected provider error when no cached results exist, got %v", err)
	}
}
