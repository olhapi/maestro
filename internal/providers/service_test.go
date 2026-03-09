package providers

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/olhapi/maestro/internal/kanban"
)

type stubProvider struct {
	kind      string
	issues    []kanban.Issue
	lastQuery kanban.IssueQuery
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

func (p *stubProvider) ListIssues(_ context.Context, _ *kanban.Project, query kanban.IssueQuery) ([]kanban.Issue, error) {
	p.lastQuery = query
	out := make([]kanban.Issue, len(p.issues))
	copy(out, p.issues)
	return out, nil
}

func (p *stubProvider) GetIssue(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
	return nil, kanban.ErrNotFound
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
