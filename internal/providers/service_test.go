package providers

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

type stubProvider struct {
	kind         string
	validateFunc func(context.Context, *kanban.Project) error
	issues       []kanban.Issue
	lastQuery    kanban.IssueQuery
	listErr      error
	listGate     <-chan struct{}
	listFunc     func(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error)
	getIssue     *kanban.Issue
	getErr       error
	getGate      <-chan struct{}
	getFunc      func(context.Context, *kanban.Project, string) (*kanban.Issue, error)
	createFunc   func(context.Context, *kanban.Project, IssueCreateInput) (*kanban.Issue, error)
	updateFunc   func(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error)
	commentFunc  func(context.Context, *kanban.Project, *kanban.Issue, IssueCommentInput) (*kanban.IssueComment, error)
}

func sampleProviderPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
}

func (p *stubProvider) Kind() string {
	return p.kind
}

func (p *stubProvider) Capabilities() kanban.ProviderCapabilities {
	return kanban.DefaultCapabilities(p.kind)
}

func (p *stubProvider) ValidateProject(ctx context.Context, project *kanban.Project) error {
	if p.validateFunc != nil {
		return p.validateFunc(ctx, project)
	}
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

func withProviderProjectSyncTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	previous := providerProjectSyncTimeout
	providerProjectSyncTimeout = timeout
	t.Cleanup(func() {
		providerProjectSyncTimeout = previous
	})
}

func TestServiceReadOnlyStoreSkipsProviderSyncAndRefresh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only database permissions behave differently on Windows")
	}

	dbPath := filepath.Join(t.TempDir(), "readonly.db")
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	project, err := store.CreateProjectWithProvider("Readonly project", "", "", "", "stub", "", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	issue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		Identifier:       "STUB-1",
		ProviderKind:     "stub",
		ProviderIssueRef: "remote-1",
		Title:            "Read only issue",
		Description:      "cached",
		State:            kanban.StateBacklog,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close writable store: %v", err)
	}
	if err := os.Chmod(dbPath, 0o444); err != nil {
		t.Fatalf("Chmod read-only db: %v", err)
	}

	readOnlyStore, err := kanban.NewReadOnlyStore(dbPath)
	if err != nil {
		t.Fatalf("NewReadOnlyStore: %v", err)
	}
	defer readOnlyStore.Close()
	if !readOnlyStore.ReadOnly() {
		t.Fatal("expected store to report read-only mode")
	}

	var listCalls atomic.Int32
	var getCalls atomic.Int32
	svc := NewService(readOnlyStore)
	svc.RegisterProvider(&stubProvider{
		kind: "stub",
		listFunc: func(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error) {
			listCalls.Add(1)
			return nil, nil
		},
		getFunc: func(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
			getCalls.Add(1)
			return &kanban.Issue{
				Identifier:       "STUB-1",
				ProviderKind:     "stub",
				ProviderIssueRef: "remote-1",
				Title:            "Refreshed title",
				State:            kanban.StateDone,
				UpdatedAt:        time.Now().UTC(),
			}, nil
		},
	})

	issues, total, err := svc.ListIssueSummaries(context.Background(), kanban.IssueQuery{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListIssueSummaries: %v", err)
	}
	if listCalls.Load() != 0 {
		t.Fatalf("expected read-only list to skip provider sync, got %d calls", listCalls.Load())
	}
	if total != 1 || len(issues) != 1 || issues[0].Identifier != issue.Identifier {
		t.Fatalf("unexpected issue list payload: total=%d issues=%#v", total, issues)
	}

	fetched, err := svc.GetIssueByIdentifier(context.Background(), issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier: %v", err)
	}
	if getCalls.Load() != 0 {
		t.Fatalf("expected read-only issue lookup to skip provider refresh, got %d calls", getCalls.Load())
	}
	if fetched.Identifier != issue.Identifier || fetched.Title != issue.Title {
		t.Fatalf("unexpected fetched issue: %+v", fetched)
	}
}

func withProviderListSyncMinInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	previous := providerListSyncMinInterval
	providerListSyncMinInterval = interval
	t.Cleanup(func() {
		providerListSyncMinInterval = previous
	})
}

func TestServiceCreateProjectDoesNotPersistOnValidationFailure(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	validateErr := errors.New("invalid project config")
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		validateFunc: func(_ context.Context, project *kanban.Project) error {
			if project.ProviderKind != "stub" {
				t.Fatalf("expected stub provider kind, got %q", project.ProviderKind)
			}
			return validateErr
		},
	}

	_, err = svc.CreateProject(context.Background(), "Broken", "", "", "", "stub", "stub-ref", map[string]interface{}{"mode": "broken"})
	if !errors.Is(err, validateErr) {
		t.Fatalf("expected validation failure, got %v", err)
	}

	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected no projects to be persisted after validation failure, got %#v", projects)
	}
}

func TestServiceUpdateProjectDoesNotPersistOnValidationFailure(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProject("Existing", "stable", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	validateErr := errors.New("provider rejects update")
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		validateFunc: func(_ context.Context, candidate *kanban.Project) error {
			if candidate.ID != project.ID {
				t.Fatalf("expected project ID %q, got %q", project.ID, candidate.ID)
			}
			if candidate.Name != "Changed" {
				t.Fatalf("expected candidate name to reflect update, got %q", candidate.Name)
			}
			return validateErr
		},
	}

	err = svc.UpdateProject(context.Background(), project.ID, "Changed", "desc", "", "", "stub", "stub-ref", map[string]interface{}{"mode": "broken"})
	if !errors.Is(err, validateErr) {
		t.Fatalf("expected validation failure, got %v", err)
	}

	unchanged, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if unchanged.Name != project.Name || unchanged.ProviderKind != kanban.ProviderKindKanban || unchanged.ProviderProjectRef != "" {
		t.Fatalf("expected persisted project to remain unchanged, got %+v", unchanged)
	}
}

func (p *stubProvider) CreateIssue(ctx context.Context, project *kanban.Project, input IssueCreateInput) (*kanban.Issue, error) {
	if p.createFunc != nil {
		return p.createFunc(ctx, project, input)
	}
	return nil, ErrUnsupportedCapability
}

func (p *stubProvider) UpdateIssue(ctx context.Context, project *kanban.Project, issue *kanban.Issue, updates map[string]interface{}) (*kanban.Issue, error) {
	if p.updateFunc != nil {
		return p.updateFunc(ctx, project, issue, updates)
	}
	return nil, ErrUnsupportedCapability
}

func (p *stubProvider) DeleteIssue(context.Context, *kanban.Project, *kanban.Issue) error {
	return ErrUnsupportedCapability
}

func (p *stubProvider) SetIssueState(context.Context, *kanban.Project, *kanban.Issue, string) (*kanban.Issue, error) {
	return nil, ErrUnsupportedCapability
}

func (p *stubProvider) ListIssueComments(context.Context, *kanban.Project, *kanban.Issue) ([]kanban.IssueComment, error) {
	return nil, ErrUnsupportedCapability
}

func (p *stubProvider) CreateIssueComment(ctx context.Context, project *kanban.Project, issue *kanban.Issue, input IssueCommentInput) (*kanban.IssueComment, error) {
	if p.commentFunc != nil {
		return p.commentFunc(ctx, project, issue, input)
	}
	return nil, ErrUnsupportedCapability
}

func (p *stubProvider) UpdateIssueComment(context.Context, *kanban.Project, *kanban.Issue, string, IssueCommentInput) (*kanban.IssueComment, error) {
	return nil, ErrUnsupportedCapability
}

func (p *stubProvider) DeleteIssueComment(context.Context, *kanban.Project, *kanban.Issue, string) error {
	return ErrUnsupportedCapability
}

func (p *stubProvider) GetIssueCommentAttachmentContent(context.Context, *kanban.Project, *kanban.Issue, string, string) (*IssueCommentAttachmentContent, error) {
	return nil, ErrUnsupportedCapability
}

func TestServiceSyncIssuesPrunesStaleProviderShadowIssues(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	stale, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-stale",
		Identifier:       "STUB-STALE",
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
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		issues: []kanban.Issue{
			{
				ProviderKind:     "stub",
				ProviderIssueRef: "stub-keep",
				Identifier:       "STUB-KEEP",
				Title:            "Kept issue",
				State:            kanban.StateReady,
			},
		},
	}

	if err := svc.SyncIssues(context.Background(), kanban.IssueQuery{ProjectID: project.ID}); err != nil {
		t.Fatalf("SyncIssues: %v", err)
	}

	if _, err := store.GetIssueByProviderRef("stub", "stub-stale"); !kanban.IsNotFound(err) {
		t.Fatalf("expected stale provider issue to be deleted, got %v", err)
	}
	if _, err := store.GetWorkspace(stale.ID); err == nil {
		t.Fatal("expected stale workspace to be removed with the provider issue")
	}

	kept, err := store.GetIssueByProviderRef("stub", "stub-keep")
	if err != nil {
		t.Fatalf("GetIssueByProviderRef keep: %v", err)
	}
	if kept.Identifier != "STUB-KEEP" {
		t.Fatalf("expected kept issue to be synced, got %q", kept.Identifier)
	}
}

func TestServiceSyncIssuesPreservesLocalFieldsOnProviderShadowUpdates(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	existing, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-keep",
		Identifier:       "STUB-KEEP",
		Title:            "Old title",
		State:            kanban.StateBacklog,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}
	if err := store.UpdateIssue(existing.ID, map[string]interface{}{
		"agent_name":   "codex",
		"agent_prompt": "preserve me",
		"branch_name":  "codex/STUB-KEEP",
		"pr_url":       "https://example.com/pr/1",
	}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		issues: []kanban.Issue{{
			ProviderKind:     "stub",
			ProviderIssueRef: "stub-keep",
			Identifier:       "STUB-KEEP",
			Title:            "New title",
			Description:      "Provider refreshed description",
			State:            kanban.StateReady,
			Priority:         2,
		}},
	}

	if err := svc.SyncIssues(context.Background(), kanban.IssueQuery{ProjectID: project.ID}); err != nil {
		t.Fatalf("SyncIssues: %v", err)
	}

	refreshed, err := store.GetIssueByProviderRef("stub", "stub-keep")
	if err != nil {
		t.Fatalf("GetIssueByProviderRef: %v", err)
	}
	if refreshed.Title != "New title" || refreshed.Description != "Provider refreshed description" || refreshed.State != kanban.StateReady {
		t.Fatalf("expected provider fields to refresh, got %#v", refreshed)
	}
	if refreshed.AgentName != "codex" || refreshed.AgentPrompt != "preserve me" || refreshed.BranchName != "codex/STUB-KEEP" || refreshed.PRURL != "https://example.com/pr/1" {
		t.Fatalf("expected local fields to survive provider sync, got %#v", refreshed)
	}
}

func TestServiceSyncIssuesMovesProviderShadowAcrossProjects(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sourceProject, err := store.CreateProjectWithProvider(
		"Source Project",
		"",
		"",
		"",
		"stub",
		"proj-source",
		map[string]interface{}{"project_slug": "proj-source"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider source: %v", err)
	}
	targetProject, err := store.CreateProjectWithProvider(
		"Target Project",
		"",
		"",
		"",
		"stub",
		"proj-target",
		map[string]interface{}{"project_slug": "proj-target"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider target: %v", err)
	}

	existing, err := store.UpsertProviderIssue(sourceProject.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-moved",
		Identifier:       "STUB-MOVED",
		Title:            "Moved issue",
		State:            kanban.StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		listFunc: func(ctx context.Context, project *kanban.Project, query kanban.IssueQuery) ([]kanban.Issue, error) {
			if project.ID != targetProject.ID {
				return []kanban.Issue{}, nil
			}
			return []kanban.Issue{{
				ProviderKind:     "stub",
				ProviderIssueRef: "stub-moved",
				Identifier:       "STUB-MOVED",
				Title:            "Moved issue",
				State:            kanban.StateReady,
			}}, nil
		},
	}

	if err := svc.SyncIssues(context.Background(), kanban.IssueQuery{ProjectID: targetProject.ID}); err != nil {
		t.Fatalf("SyncIssues target: %v", err)
	}

	moved, err := store.GetIssue(existing.ID)
	if err != nil {
		t.Fatalf("GetIssue moved: %v", err)
	}
	if moved.ProjectID != targetProject.ID {
		t.Fatalf("expected moved issue project %s, got %#v", targetProject.ID, moved)
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
		"Stub Project",
		"",
		repoPath,
		"",
		"stub",
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
		kind: "stub",
		issues: []kanban.Issue{
			{
				ProviderKind:     "stub",
				ProviderIssueRef: "stub-keep",
				Identifier:       "STUB-KEEP",
				Title:            "Kept issue",
				State:            kanban.StateReady,
			},
		},
	}
	svc := NewService(store)
	svc.providers["stub"] = provider

	if err := svc.SyncForRepoPath(context.Background(), repoPath); err != nil {
		t.Fatalf("SyncForRepoPath: %v", err)
	}

	if provider.lastQuery.Assignee != "me" {
		t.Fatalf("expected assignee filter to be forwarded, got %#v", provider.lastQuery)
	}
	if _, err := store.GetIssueByProviderRef("stub", "stub-keep"); err != nil {
		t.Fatalf("expected synced issue for project %s: %v", project.ID, err)
	}
}

func TestServiceSyncForRepoPathAppliesTimeoutPerProject(t *testing.T) {
	withProviderProjectSyncTimeout(t, 20*time.Millisecond)

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	slowProject, err := store.CreateProjectWithProvider(
		"Slow Project",
		"",
		t.TempDir(),
		"",
		"stub",
		"slow-proj",
		map[string]interface{}{"project_slug": "slow-proj"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider slow: %v", err)
	}
	fastProject, err := store.CreateProjectWithProvider(
		"Fast Project",
		"",
		t.TempDir(),
		"",
		"stub",
		"fast-proj",
		map[string]interface{}{"project_slug": "fast-proj"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider fast: %v", err)
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		listFunc: func(ctx context.Context, project *kanban.Project, _ kanban.IssueQuery) ([]kanban.Issue, error) {
			switch project.ProviderProjectRef {
			case slowProject.ProviderProjectRef:
				<-ctx.Done()
				return nil, ctx.Err()
			case fastProject.ProviderProjectRef:
				return []kanban.Issue{{
					ProviderKind:     "stub",
					ProviderIssueRef: "stub-fast-1",
					Identifier:       "STUB-FAST-1",
					Title:            "Fast issue",
					State:            kanban.StateReady,
				}}, nil
			default:
				return nil, nil
			}
		},
	}

	start := time.Now()
	err = svc.SyncForRepoPath(context.Background(), "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected first timed out project error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("expected per-project timeout rather than repo-wide starvation, took %v", elapsed)
	}

	fastIssue, err := store.GetIssueByProviderRef("stub", "stub-fast-1")
	if err != nil {
		t.Fatalf("expected later project to sync despite earlier timeout: %v", err)
	}
	if fastIssue.ProjectID != fastProject.ID {
		t.Fatalf("expected synced issue to belong to fast project, got %#v", fastIssue)
	}
}

func TestServiceCreateIssueCommentDelegatesToProvider(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	if _, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Identifier:       "STUB-1",
		Title:            "Provider issue",
		State:            kanban.StateDone,
	}); err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	var got IssueCommentInput
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		getIssue: &kanban.Issue{
			ProjectID:        project.ID,
			ProviderKind:     "stub",
			ProviderIssueRef: "stub-1",
			Identifier:       "STUB-1",
		},
		commentFunc: func(_ context.Context, gotProject *kanban.Project, issue *kanban.Issue, input IssueCommentInput) (*kanban.IssueComment, error) {
			if gotProject == nil || gotProject.ID != project.ID {
				t.Fatalf("unexpected project context: %#v", gotProject)
			}
			if issue.Identifier != "STUB-1" {
				t.Fatalf("unexpected issue %q", issue.Identifier)
			}
			got = input
			return nil, nil
		},
	}

	body := "Done pass preview"
	input := IssueCommentInput{
		Body: &body,
		Attachments: []IssueCommentAttachment{{
			Path: "/tmp/preview.mp4",
		}},
	}
	if err := svc.CreateIssueComment(context.Background(), "STUB-1", input); err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}
	if got.Body != input.Body || len(got.Attachments) != 1 || got.Attachments[0].Path != input.Attachments[0].Path {
		t.Fatalf("unexpected delegated comment input: %#v", got)
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
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	if _, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-keep",
		Identifier:       "STUB-KEEP",
		Title:            "Cached issue",
		State:            kanban.StateReady,
	}); err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	gate := make(chan struct{})
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind:     "stub",
		listGate: gate,
	}

	items, total, err := svc.ListIssueSummaries(context.Background(), kanban.IssueQuery{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListIssueSummaries: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected cached provider issue after timeout, got total=%d items=%#v", total, items)
	}
	if items[0].Identifier != "STUB-KEEP" {
		t.Fatalf("unexpected cached issue payload: %#v", items[0])
	}
}

func TestReadOnlyServiceSkipsProviderSyncAndRefresh(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	cachedIssue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-keep",
		Identifier:       "STUB-KEEP",
		Title:            "Cached issue",
		State:            kanban.StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	var listCalls, getCalls atomic.Int32
	svc := NewReadOnlyService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		listFunc: func(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error) {
			listCalls.Add(1)
			return []kanban.Issue{{
				ProviderKind:     "stub",
				ProviderIssueRef: "stub-remote",
				Identifier:       "STUB-REMOTE",
				Title:            "Remote issue",
				State:            kanban.StateDone,
			}}, nil
		},
		getFunc: func(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
			getCalls.Add(1)
			return &kanban.Issue{
				ProviderKind:     "stub",
				ProviderIssueRef: "stub-remote",
				Identifier:       "STUB-REMOTE",
				Title:            "Remote issue",
				State:            kanban.StateDone,
			}, nil
		},
	}

	items, total, err := svc.ListIssueSummaries(context.Background(), kanban.IssueQuery{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListIssueSummaries: %v", err)
	}
	if got := listCalls.Load(); got != 0 {
		t.Fatalf("expected read-only service to skip provider sync, got %d calls", got)
	}
	if total != 1 || len(items) != 1 || items[0].Identifier != cachedIssue.Identifier {
		t.Fatalf("unexpected read-only list payload: total=%d items=%#v", total, items)
	}

	issue, err := svc.GetIssueByIdentifier(context.Background(), cachedIssue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier: %v", err)
	}
	if got := getCalls.Load(); got != 0 {
		t.Fatalf("expected read-only service to skip provider refresh, got %d calls", got)
	}
	if issue.Identifier != cachedIssue.Identifier {
		t.Fatalf("unexpected cached issue returned: %#v", issue)
	}
}

func TestServiceListIssueSummariesPropagatesParentDeadlineExceeded(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	if _, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-keep",
		Identifier:       "STUB-KEEP",
		Title:            "Cached issue",
		State:            kanban.StateReady,
	}); err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind:     "stub",
		listGate: make(chan struct{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, _, err = svc.ListIssueSummaries(ctx, kanban.IssueQuery{ProjectID: project.ID})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected parent deadline to be propagated, got %v", err)
	}
}

func TestServiceListIssueSummariesSkipsRepeatedBestEffortSyncWithinInterval(t *testing.T) {
	withProviderListSyncMinInterval(t, time.Minute)

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	var calls atomic.Int32
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		listFunc: func(ctx context.Context, project *kanban.Project, query kanban.IssueQuery) ([]kanban.Issue, error) {
			calls.Add(1)
			return []kanban.Issue{{
				ProviderKind:     "stub",
				ProviderIssueRef: "stub-1",
				Identifier:       "STUB-1",
				Title:            "Synced issue",
				State:            kanban.StateReady,
			}}, nil
		},
	}

	for i := 0; i < 2; i++ {
		items, total, err := svc.ListIssueSummaries(context.Background(), kanban.IssueQuery{ProjectID: project.ID})
		if err != nil {
			t.Fatalf("ListIssueSummaries call %d: %v", i, err)
		}
		if total != 1 || len(items) != 1 || items[0].Identifier != "STUB-1" {
			t.Fatalf("unexpected list payload on call %d: total=%d items=%#v", i, total, items)
		}
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one provider sync within throttle interval, got %d", got)
	}
}

func TestServiceListIssueSummariesFilteredReadDoesNotPruneNonMatchingProviderIssues(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	if _, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-ready",
		Identifier:       "STUB-READY",
		Title:            "Ready issue",
		State:            kanban.StateReady,
	}); err != nil {
		t.Fatalf("UpsertProviderIssue ready: %v", err)
	}
	if _, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-done",
		Identifier:       "STUB-DONE",
		Title:            "Done issue",
		State:            kanban.StateDone,
	}); err != nil {
		t.Fatalf("UpsertProviderIssue done: %v", err)
	}

	provider := &stubProvider{
		kind: "stub",
		issues: []kanban.Issue{
			{
				ProviderKind:     "stub",
				ProviderIssueRef: "stub-ready",
				Identifier:       "STUB-READY",
				Title:            "Ready issue",
				State:            kanban.StateReady,
			},
			{
				ProviderKind:     "stub",
				ProviderIssueRef: "stub-done",
				Identifier:       "STUB-DONE",
				Title:            "Done issue",
				State:            kanban.StateDone,
			},
		},
	}
	svc := NewService(store)
	svc.providers["stub"] = provider

	items, total, err := svc.ListIssueSummaries(context.Background(), kanban.IssueQuery{
		ProjectID: project.ID,
		State:     string(kanban.StateReady),
	})
	if err != nil {
		t.Fatalf("ListIssueSummaries: %v", err)
	}
	if provider.lastQuery.State != "" {
		t.Fatalf("expected provider sync query to omit transient state filters, got %#v", provider.lastQuery)
	}
	if total != 1 || len(items) != 1 || items[0].Identifier != "STUB-READY" {
		t.Fatalf("expected filtered results from local store, got total=%d items=%#v", total, items)
	}
	if _, err := store.GetIssueByProviderRef("stub", "stub-done"); err != nil {
		t.Fatalf("expected non-matching provider issue to remain cached: %v", err)
	}
}

func TestServiceGetIssueByIdentifierServesCachedProviderIssueWhenRefreshFails(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	cached, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Identifier:       "STUB-1",
		Title:            "Cached issue",
		State:            kanban.StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind:   "stub",
		getErr: errors.New("stub unavailable"),
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
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	cached, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Identifier:       "STUB-1",
		Title:            "Cached issue",
		State:            kanban.StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind:    "stub",
		getGate: make(chan struct{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = svc.GetIssueByIdentifier(ctx, cached.Identifier)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected parent deadline to be propagated, got %v", err)
	}
}

func TestServiceProviderIssueAssetsStayLocalAcrossRefresh(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	if _, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Identifier:       "STUB-1",
		Title:            "Cached issue",
		State:            kanban.StateReady,
	}); err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		getIssue: &kanban.Issue{
			ProviderKind:     "stub",
			ProviderIssueRef: "stub-1",
			Identifier:       "STUB-1",
			Title:            "Fresh upstream issue",
			State:            kanban.StateReady,
		},
	}

	asset, err := svc.AttachIssueAsset(context.Background(), "STUB-1", "provider.png", bytes.NewReader(sampleProviderPNGBytes()))
	if err != nil {
		t.Fatalf("AttachIssueAsset: %v", err)
	}
	detail, err := svc.GetIssueDetailByIdentifier(context.Background(), "STUB-1")
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier: %v", err)
	}
	if len(detail.Assets) != 1 || detail.Assets[0].ID != asset.ID {
		t.Fatalf("expected local asset to persist across refresh, got %#v", detail.Assets)
	}
	if detail.Title != "Fresh upstream issue" {
		t.Fatalf("expected provider refresh to still apply, got %q", detail.Title)
	}
}

func TestServiceCreateEpicRequiresProject(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewService(store)
	_, err = svc.CreateEpic("", "Epic", "")
	if err == nil {
		t.Fatal("expected project_id validation error")
	}
	if !kanban.IsValidation(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestServiceCreateIssueRequiresProject(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewService(store)
	_, err = svc.CreateIssue(context.Background(), IssueCreateInput{
		Title: "Missing project",
	})
	if err == nil {
		t.Fatal("expected project_id validation error")
	}
	if !kanban.IsValidation(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func newProviderBackedIssueFixture(t *testing.T) (*kanban.Store, *kanban.Project, *kanban.Epic, *kanban.Issue) {
	t.Helper()

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Identifier:       "STUB-1",
		Title:            "Provider issue",
		State:            kanban.StateBacklog,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}
	return store, project, epic, issue
}

func TestServiceUpdateIssueRequiresProject(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Projectless", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	svc := NewService(store)
	_, err = svc.UpdateIssue(context.Background(), issue.Identifier, map[string]interface{}{"title": "Renamed"})
	if err == nil {
		t.Fatal("expected project_id validation error")
	}
	if !kanban.IsValidation(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestServiceCreateIssuePreservesProviderLocalMetadata(t *testing.T) {
	t.Run("blank input keeps provider metadata", func(t *testing.T) {
		store, project, epic, _ := newProviderBackedIssueFixture(t)

		svc := NewService(store)
		svc.providers["stub"] = &stubProvider{
			kind: "stub",
			createFunc: func(_ context.Context, projectArg *kanban.Project, input IssueCreateInput) (*kanban.Issue, error) {
				if projectArg == nil || projectArg.ID != project.ID {
					t.Fatalf("expected project %s, got %#v", project.ID, projectArg)
				}
				if input.EpicID != epic.ID {
					t.Fatalf("expected epic %s, got %s", epic.ID, input.EpicID)
				}
				if input.AgentName != "" || input.AgentPrompt != "" || input.BranchName != "" || input.PRURL != "" {
					t.Fatalf("expected blank local input metadata, got %#v", input)
				}
				return &kanban.Issue{
					ProviderKind:     "stub",
					ProviderIssueRef: "stub-create-1",
					Identifier:       "STUB-CREATE-1",
					Title:            input.Title,
					State:            kanban.StateReady,
					AgentName:        "provider-agent",
					AgentPrompt:      "provider prompt",
					BranchName:       "provider/branch",
					PRURL:            "https://provider.example/pr/1",
				}, nil
			},
		}

		detail, err := svc.CreateIssue(context.Background(), IssueCreateInput{
			ProjectID:   project.ID,
			EpicID:      epic.ID,
			Title:       "Provider issue",
			Description: "desc",
			Priority:    2,
		})
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if detail.EpicID != epic.ID {
			t.Fatalf("expected epic %s to persist, got %q", epic.ID, detail.EpicID)
		}
		if detail.AgentName != "provider-agent" || detail.AgentPrompt != "provider prompt" {
			t.Fatalf("expected provider agent metadata to persist, got %#v", detail)
		}
		if detail.BranchName != "provider/branch" || detail.PRURL != "https://provider.example/pr/1" {
			t.Fatalf("expected provider branch/pr metadata to persist, got %#v", detail)
		}
	})

	t.Run("explicit input overrides provider metadata", func(t *testing.T) {
		store, project, epic, _ := newProviderBackedIssueFixture(t)

		svc := NewService(store)
		svc.providers["stub"] = &stubProvider{
			kind: "stub",
			createFunc: func(_ context.Context, projectArg *kanban.Project, input IssueCreateInput) (*kanban.Issue, error) {
				if projectArg == nil || projectArg.ID != project.ID {
					t.Fatalf("expected project %s, got %#v", project.ID, projectArg)
				}
				if input.EpicID != epic.ID {
					t.Fatalf("expected epic %s, got %s", epic.ID, input.EpicID)
				}
				if input.AgentName != "requested-agent" || input.AgentPrompt != "requested prompt" || input.BranchName != "requested/branch" || input.PRURL != "https://example.com/pr/99" {
					t.Fatalf("expected explicit local input metadata, got %#v", input)
				}
				return &kanban.Issue{
					ProviderKind:     "stub",
					ProviderIssueRef: "stub-create-2",
					Identifier:       "STUB-2",
					Title:            input.Title,
					State:            kanban.StateReady,
					AgentName:        "provider-agent",
					AgentPrompt:      "provider prompt",
					BranchName:       "provider/branch",
					PRURL:            "https://provider.example/pr/1",
				}, nil
			},
		}

		detail, err := svc.CreateIssue(context.Background(), IssueCreateInput{
			ProjectID:   project.ID,
			EpicID:      epic.ID,
			Title:       "Provider issue",
			Description: "desc",
			Priority:    2,
			AgentName:   "requested-agent",
			AgentPrompt: "requested prompt",
			BranchName:  "requested/branch",
			PRURL:       "https://example.com/pr/99",
		})
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if detail.EpicID != epic.ID {
			t.Fatalf("expected epic %s to persist, got %q", epic.ID, detail.EpicID)
		}
		if detail.AgentName != "requested-agent" || detail.AgentPrompt != "requested prompt" {
			t.Fatalf("expected explicit agent metadata to persist, got %#v", detail)
		}
		if detail.BranchName != "requested/branch" || detail.PRURL != "https://example.com/pr/99" {
			t.Fatalf("expected explicit branch/pr metadata to persist, got %#v", detail)
		}
	})
}

func TestServiceUpdateIssueRejectsRecurringConversionForProviderBackedIssue(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	issue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Identifier:       "STUB-1",
		Title:            "Provider issue",
		State:            kanban.StateBacklog,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	var updateCalled bool
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind:     "stub",
		getIssue: issue,
		updateFunc: func(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error) {
			updateCalled = true
			return nil, nil
		},
	}

	_, err = svc.UpdateIssue(context.Background(), issue.Identifier, map[string]interface{}{
		"issue_type": "recurring",
		"cron":       "*/15 * * * *",
	})
	if err == nil {
		t.Fatal("expected unsupported recurring conversion error")
	}
	if !IsUnsupported(err) {
		t.Fatalf("expected unsupported capability error, got %v", err)
	}
	if updateCalled {
		t.Fatal("expected provider update to be skipped for recurring conversion")
	}
}

func TestServiceUpdateIssueStoresLocalMetadataForProviderBackedIssue(t *testing.T) {
	store, project, epic, issue := newProviderBackedIssueFixture(t)
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"epic_id":      epic.ID,
		"agent_name":   "codex",
		"agent_prompt": "preserve me",
		"branch_name":  "codex/STUB-1",
		"pr_url":       "https://example.com/pr/1",
	}); err != nil {
		t.Fatalf("UpdateIssue setup: %v", err)
	}

	svc := NewService(store)
	updateCalled := false
	svc.providers["stub"] = &stubProvider{
		kind:     "stub",
		getIssue: issue,
		updateFunc: func(_ context.Context, projectArg *kanban.Project, currentIssue *kanban.Issue, updates map[string]interface{}) (*kanban.Issue, error) {
			updateCalled = true
			if projectArg == nil || projectArg.ID != project.ID {
				t.Fatalf("expected project %s, got %#v", project.ID, projectArg)
			}
			if currentIssue == nil || currentIssue.ID != issue.ID {
				t.Fatalf("expected issue %s, got %#v", issue.ID, currentIssue)
			}
			if _, ok := updates["project_id"]; ok {
				t.Fatalf("expected project_id to be excluded from provider update: %#v", updates)
			}
			if _, ok := updates["epic_id"]; ok {
				t.Fatalf("expected epic_id to be excluded from provider update: %#v", updates)
			}
			if _, ok := updates["agent_name"]; ok {
				t.Fatalf("expected agent_name to be excluded from provider update: %#v", updates)
			}
			if _, ok := updates["agent_prompt"]; ok {
				t.Fatalf("expected agent_prompt to be excluded from provider update: %#v", updates)
			}
			if _, ok := updates["branch_name"]; ok {
				t.Fatalf("expected branch_name to be excluded from provider update: %#v", updates)
			}
			if _, ok := updates["pr_url"]; ok {
				t.Fatalf("expected pr_url to be excluded from provider update: %#v", updates)
			}
			if updates["title"] != "Fresh title" {
				t.Fatalf("expected provider title update, got %#v", updates)
			}
			cp := *currentIssue
			cp.Title = "Fresh title"
			return &cp, nil
		},
	}

	detail, err := svc.UpdateIssue(context.Background(), issue.Identifier, map[string]interface{}{
		"project_id":   project.ID,
		"title":        "Fresh title",
		"agent_name":   "marketing",
		"agent_prompt": "Review homepage positioning.",
	})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if !updateCalled {
		t.Fatal("expected provider update to be called for provider-owned fields")
	}
	if detail.AgentName != "marketing" || detail.AgentPrompt != "Review homepage positioning." {
		t.Fatalf("expected updated agent metadata, got %#v", detail)
	}
	if detail.EpicID != epic.ID {
		t.Fatalf("expected epic metadata to persist, got %#v", detail)
	}
	if detail.BranchName != "codex/STUB-1" || detail.PRURL != "https://example.com/pr/1" {
		t.Fatalf("expected branch/pr metadata to persist, got %#v", detail)
	}
	if detail.Title != "Fresh title" {
		t.Fatalf("expected updated title, got %#v", detail)
	}
}

func TestServiceUpdateIssueClearsProviderBackedLocalMetadata(t *testing.T) {
	store, _, epic, issue := newProviderBackedIssueFixture(t)
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"epic_id":      epic.ID,
		"agent_name":   "codex",
		"agent_prompt": "preserve me",
		"branch_name":  "codex/STUB-1",
		"pr_url":       "https://example.com/pr/1",
	}); err != nil {
		t.Fatalf("UpdateIssue setup: %v", err)
	}

	updateCalled := false
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind:     "stub",
		getIssue: issue,
		updateFunc: func(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error) {
			updateCalled = true
			return nil, nil
		},
	}

	detail, err := svc.UpdateIssue(context.Background(), issue.Identifier, map[string]interface{}{
		"epic_id":      "",
		"agent_name":   "",
		"agent_prompt": "",
		"branch_name":  "",
		"pr_url":       "",
	})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updateCalled {
		t.Fatal("expected provider update to be skipped for local-only clears")
	}
	if detail.EpicID != "" || detail.AgentName != "" || detail.AgentPrompt != "" || detail.BranchName != "" || detail.PRURL != "" {
		t.Fatalf("expected local metadata to clear, got %#v", detail)
	}
}

func TestServiceUpdateIssueRejectsProviderBackedProjectMoves(t *testing.T) {
	store, project, epic, issue := newProviderBackedIssueFixture(t)
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"epic_id":     epic.ID,
		"branch_name": "codex/STUB-1",
	}); err != nil {
		t.Fatalf("UpdateIssue setup: %v", err)
	}
	otherProject, err := store.CreateProjectWithProvider(
		"Other Project",
		"",
		"",
		"",
		"stub",
		"other-slug",
		map[string]interface{}{"project_slug": "other-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider other: %v", err)
	}
	if _, err := store.CreateEpic(otherProject.ID, "Other Epic", ""); err != nil {
		t.Fatalf("CreateEpic other: %v", err)
	}

	svc := NewService(store)
	updateCalled := false
	svc.providers["stub"] = &stubProvider{
		kind:     "stub",
		getIssue: issue,
		updateFunc: func(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error) {
			updateCalled = true
			return nil, nil
		},
	}

	_, err = svc.UpdateIssue(context.Background(), issue.Identifier, map[string]interface{}{
		"project_id": otherProject.ID,
		"title":      "Moved issue",
	})
	if err == nil {
		t.Fatal("expected unsupported project move error")
	}
	if !IsUnsupported(err) {
		t.Fatalf("expected unsupported capability error, got %v", err)
	}
	if updateCalled {
		t.Fatal("expected provider update to be skipped when project moves are rejected")
	}

	persisted, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if persisted.ProjectID != project.ID || persisted.EpicID != epic.ID || persisted.BranchName != "codex/STUB-1" {
		t.Fatalf("expected local state to remain unchanged, got %#v", persisted)
	}
}

func TestServiceListIssueSummariesReturnsErrorWhenAnyProjectLacksCache(t *testing.T) {
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
		"stub",
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
		"stub",
		"fast-proj",
		map[string]interface{}{"project_slug": "fast-proj"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider fast: %v", err)
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		listFunc: func(_ context.Context, project *kanban.Project, _ kanban.IssueQuery) ([]kanban.Issue, error) {
			switch project.ProviderProjectRef {
			case slowProject.ProviderProjectRef:
				return nil, context.DeadlineExceeded
			case fastProject.ProviderProjectRef:
				return []kanban.Issue{{
					ProviderKind:     "stub",
					ProviderIssueRef: "stub-fast-1",
					Identifier:       "STUB-FAST-1",
					Title:            "Fast issue",
					State:            kanban.StateReady,
				}}, nil
			default:
				return nil, nil
			}
		},
	}

	_, _, err = svc.ListIssueSummaries(context.Background(), kanban.IssueQuery{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected provider error for uncached project failure, got %v", err)
	}

	items, total, err := store.ListIssueSummaries(kanban.IssueQuery{})
	if err != nil {
		t.Fatalf("store.ListIssueSummaries: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected the healthy project to still sync before returning the error, got total=%d items=%#v", total, items)
	}
	if items[0].Identifier != "STUB-FAST-1" || items[0].ProjectID != fastProject.ID {
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
		"stub",
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
		"stub",
		"fast-proj",
		map[string]interface{}{"project_slug": "fast-proj"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider fast: %v", err)
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		getFunc: func(ctx context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
			switch project.ProviderProjectRef {
			case slowProject.ProviderProjectRef:
				<-ctx.Done()
				return nil, ctx.Err()
			case fastProject.ProviderProjectRef:
				return &kanban.Issue{
					ProviderKind:     "stub",
					ProviderIssueRef: "stub-fast-1",
					Identifier:       identifier,
					Title:            "Fast issue",
					State:            kanban.StateReady,
				}, nil
			default:
				return nil, kanban.ErrNotFound
			}
		},
	}

	issue, err := svc.GetIssueByIdentifier(context.Background(), "STUB-FAST-1")
	if err != nil {
		t.Fatalf("GetIssueByIdentifier: %v", err)
	}
	if issue.ProjectID != fastProject.ID || issue.Identifier != "STUB-FAST-1" {
		t.Fatalf("expected cold miss lookup to continue to the healthy project, got %#v", issue)
	}
}

func TestServiceGetIssueByIdentifierColdMissQueriesProjectsInParallel(t *testing.T) {
	withProviderReadTimeout(t, 60*time.Millisecond)

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	projectRefs := []string{"proj-a", "proj-b", "proj-c", "proj-d"}
	for _, ref := range projectRefs {
		if _, err := store.CreateProjectWithProvider(
			ref,
			"",
			"",
			"",
			"stub",
			ref,
			map[string]interface{}{"project_slug": ref},
		); err != nil {
			t.Fatalf("CreateProjectWithProvider %s: %v", ref, err)
		}
	}

	var started atomic.Int32
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		getFunc: func(ctx context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
			started.Add(1)
			for started.Load() < int32(len(projectRefs)) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
					time.Sleep(1 * time.Millisecond)
				}
			}
			time.Sleep(15 * time.Millisecond)
			if project.ProviderProjectRef == "proj-d" {
				return &kanban.Issue{
					ProviderKind:     "stub",
					ProviderIssueRef: "stub-fast-1",
					Identifier:       identifier,
					Title:            "Fast issue",
					State:            kanban.StateReady,
				}, nil
			}
			return nil, kanban.ErrNotFound
		},
	}

	start := time.Now()
	issue, err := svc.GetIssueByIdentifier(context.Background(), "STUB-FAST-1")
	if err != nil {
		t.Fatalf("GetIssueByIdentifier: %v", err)
	}
	if issue.Identifier != "STUB-FAST-1" {
		t.Fatalf("unexpected issue returned: %#v", issue)
	}
	if elapsed := time.Since(start); elapsed >= 40*time.Millisecond {
		t.Fatalf("expected parallel provider probes, lookup took %v", elapsed)
	}
}

func TestServiceGetIssueByIdentifierColdMissRejectsAmbiguousProviderMatches(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	projectRefs := []string{"proj-a", "proj-b"}
	for _, ref := range projectRefs {
		if _, err := store.CreateProjectWithProvider(
			ref,
			"",
			"",
			"",
			"stub",
			ref,
			map[string]interface{}{"project_slug": ref},
		); err != nil {
			t.Fatalf("CreateProjectWithProvider %s: %v", ref, err)
		}
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		getFunc: func(ctx context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
			if project.ProviderProjectRef == "proj-b" {
				select {
				case <-time.After(10 * time.Millisecond):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return &kanban.Issue{
				ProviderKind:     "stub",
				ProviderIssueRef: project.ProviderProjectRef + "-1",
				Identifier:       identifier,
				Title:            project.ProviderProjectRef,
				State:            kanban.StateReady,
			}, nil
		},
	}

	_, err = svc.GetIssueByIdentifier(context.Background(), "STUB-AMBIG")
	if !errors.Is(err, ErrAmbiguousProviderIssue) {
		t.Fatalf("expected ambiguous provider issue error, got %v", err)
	}
	if _, err := store.GetIssueByIdentifier("STUB-AMBIG"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ambiguous lookup not to persist an issue, got %v", err)
	}
}

func TestServiceGetIssueByIdentifierColdMissReturnsProviderErrorWhenAllProjectsFail(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	); err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	providerErr := errors.New("stub unavailable")
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind:   "stub",
		getErr: providerErr,
	}

	_, err = svc.GetIssueByIdentifier(context.Background(), "STUB-MISS")
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
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	); err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind:    "stub",
		getGate: make(chan struct{}),
	}

	start := time.Now()
	_, err = svc.GetIssueByIdentifier(context.Background(), "STUB-MISS")
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
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	providerErr := errors.New("stub unavailable")
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind:    "stub",
		listErr: providerErr,
	}

	_, _, err = svc.ListIssueSummaries(context.Background(), kanban.IssueQuery{ProjectID: project.ID})
	if !errors.Is(err, providerErr) {
		t.Fatalf("expected provider error when no cached results exist, got %v", err)
	}
}

func TestServiceCreateProjectValidationFailureDoesNotPersistProject(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	validationErr := errors.New("missing STUB_PROVIDER_API_KEY")
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		validateFunc: func(_ context.Context, project *kanban.Project) error {
			if project.ProviderProjectRef != "proj-slug" {
				t.Fatalf("unexpected project for validation: %#v", project)
			}
			return validationErr
		},
	}

	_, err = svc.CreateProject(context.Background(), "Stub Project", "", "", "", "stub", "proj-slug", map[string]interface{}{"project_slug": "proj-slug"})
	if !errors.Is(err, validationErr) {
		t.Fatalf("expected validation failure, got %v", err)
	}

	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected project creation rollback, got %#v", projects)
	}
}

func TestServiceUpdateProjectValidationFailureLeavesStoredProjectUntouched(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"good-slug",
		map[string]interface{}{"project_slug": "good-slug"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	validationErr := errors.New("invalid provider config")
	svc := NewService(store)
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		validateFunc: func(_ context.Context, candidate *kanban.Project) error {
			if candidate.ProviderProjectRef == "bad-slug" {
				return validationErr
			}
			return nil
		},
	}

	err = svc.UpdateProject(context.Background(), project.ID, project.Name, project.Description, project.RepoPath, project.WorkflowPath, project.ProviderKind, "bad-slug", map[string]interface{}{"project_slug": "bad-slug"})
	if !errors.Is(err, validationErr) {
		t.Fatalf("expected validation failure, got %v", err)
	}

	stored, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if stored.ProviderProjectRef != "good-slug" {
		t.Fatalf("expected original provider config to remain, got %#v", stored)
	}
}

func TestServiceProjectPathOverridesExpandEnvAndHomePaths(t *testing.T) {
	homeDir := t.TempDir()
	repoBase := t.TempDir()
	workflowBase := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("MAESTRO_REPO_BASE", repoBase)
	t.Setenv("MAESTRO_WORKFLOW_BASE", workflowBase)

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewService(store)
	validated := 0
	wantRepo := filepath.Join(repoBase, "repo")
	wantWorkflow := filepath.Join(homeDir, "workflow", "WORKFLOW.md")
	svc.providers["stub"] = &stubProvider{
		kind: "stub",
		validateFunc: func(_ context.Context, candidate *kanban.Project) error {
			validated++
			if candidate.RepoPath != wantRepo {
				t.Fatalf("expected repo path %q, got %q", wantRepo, candidate.RepoPath)
			}
			if candidate.WorkflowPath != wantWorkflow {
				t.Fatalf("expected workflow path %q, got %q", wantWorkflow, candidate.WorkflowPath)
			}
			return nil
		},
	}

	project, err := svc.CreateProject(context.Background(), "Path Project", "", "$MAESTRO_REPO_BASE/repo", "~/workflow/WORKFLOW.md", "stub", "stub-ref", nil)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if validated != 1 {
		t.Fatalf("expected one validation during create, got %d", validated)
	}
	if project.RepoPath != wantRepo || project.WorkflowPath != wantWorkflow {
		t.Fatalf("expected create to persist expanded paths, got %#v", project)
	}

	wantRepo = filepath.Join(homeDir, "updated-repo")
	wantWorkflow = filepath.Join(workflowBase, "updated", "WORKFLOW.md")
	if err := svc.UpdateProject(context.Background(), project.ID, project.Name, project.Description, "~/updated-repo", "$MAESTRO_WORKFLOW_BASE/updated/WORKFLOW.md", project.ProviderKind, project.ProviderProjectRef, nil); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if validated != 2 {
		t.Fatalf("expected two validations after update, got %d", validated)
	}

	updated, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.RepoPath != wantRepo || updated.WorkflowPath != wantWorkflow {
		t.Fatalf("expected update to persist expanded paths, got %#v", updated)
	}
}
