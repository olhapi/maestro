package providers

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/olhapi/maestro/internal/kanban"
)

func TestServiceCoverageBranches(t *testing.T) {
	t.Run("registration and resolution guards", func(t *testing.T) {
		var nilSvc *Service
		nilSvc.RegisterProvider(&serviceProviderStub{kind: "stub"})
		nilSvc.RegisterProvider(nil)

		store := newProvidersTestStore(t)
		svc := NewService(store)
		svc.RegisterProvider(&serviceProviderStub{kind: "stub"})
		svc.RegisterProvider(&serviceProviderStub{kind: "other"})

		if got := svc.ProviderForProject(nil); got == nil || got.Kind() != kanban.ProviderKindKanban {
			t.Fatalf("expected kanban provider for nil project, got %#v", got)
		}
		if got := svc.ProviderForProject(&kanban.Project{ProviderKind: "missing"}); got != nil {
			t.Fatalf("expected unknown provider to return nil, got %#v", got)
		}

		emptySvc := &Service{providers: map[string]Provider{}}
		if _, err := emptySvc.providerForKindOrError(""); err == nil {
			t.Fatal("expected empty service to reject default provider kind")
		}
		if _, err := emptySvc.providerForKindOrError("missing"); err == nil {
			t.Fatal("expected missing provider kind to fail")
		}
		if _, _, err := emptySvc.resolveIssueProvider(&kanban.Issue{ProviderKind: "missing"}); err == nil {
			t.Fatal("expected missing provider kind on cold issue lookup to fail")
		}

		project, err := store.CreateProjectWithProvider("Stub Project", "", "", "", "stub", "stub-ref", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		resolvedProject, provider, err := svc.resolveProjectProvider(project.ID)
		if err != nil {
			t.Fatalf("resolveProjectProvider: %v", err)
		}
		if resolvedProject.ID != project.ID || provider.Kind() != "stub" {
			t.Fatalf("unexpected resolved project/provider: %#v %#v", resolvedProject, provider)
		}

		missingProject, err := store.CreateProjectWithProvider("Missing Project", "", "", "", "missing", "missing-ref", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider missing: %v", err)
		}
		if _, _, err := svc.resolveProjectProvider(missingProject.ID); err == nil {
			t.Fatal("expected resolveProjectProvider to fail for unknown provider kind")
		}

		sameKindProject, err := store.CreateProjectWithProvider("Same Kind", "", "", "", "stub", "same-kind", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider same kind: %v", err)
		}
		resolvedIssue, provider, err := svc.resolveIssueProvider(&kanban.Issue{
			ProjectID:        sameKindProject.ID,
			ProviderKind:     "other",
			ProviderIssueRef: "remote-1",
		})
		if err != nil {
			t.Fatalf("resolveIssueProvider mismatch: %v", err)
		}
		if resolvedIssue.ID != sameKindProject.ID || provider.Kind() != "other" {
			t.Fatalf("unexpected resolved issue provider: %#v %#v", resolvedIssue, provider)
		}

		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if !shouldPropagateReadSyncError(cancelled, context.Canceled, true) {
			t.Fatal("expected cancellation to propagate when parent context is done")
		}
		if shouldPropagateReadSyncError(context.Background(), context.DeadlineExceeded, false) {
			t.Fatal("expected best-effort failures not to propagate when context was not propagated")
		}
	})

	t.Run("read only and refresh guards", func(t *testing.T) {
		store := newProvidersTestStore(t)
		project, err := store.CreateProjectWithProvider("Stub Project", "", "", "", "stub", "stub-ref", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		cachedIssue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
			ProviderKind:     "stub",
			ProviderIssueRef: "remote-1",
			Identifier:       "STUB-1",
			Title:            "Cached issue",
			State:            kanban.StateBacklog,
		})
		if err != nil {
			t.Fatalf("UpsertProviderIssue: %v", err)
		}

		var listCalls, getCalls int
		svc := NewReadOnlyService(store)
		svc.RegisterProvider(&serviceProviderStub{
			kind: "stub",
			listIssuesFunc: func(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error) {
				listCalls++
				return nil, nil
			},
			getIssueFunc: func(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
				getCalls++
				return &kanban.Issue{
					ProviderKind:     "stub",
					ProviderIssueRef: "remote-1",
					Identifier:       "STUB-1",
					Title:            "Refreshed title",
					State:            kanban.StateReady,
				}, nil
			},
		})

		if err := svc.SyncIssues(context.Background(), kanban.IssueQuery{ProjectID: project.ID}); err != nil {
			t.Fatalf("SyncIssues on read-only service: %v", err)
		}
		if listCalls != 0 {
			t.Fatalf("expected read-only SyncIssues to skip provider sync, got %d calls", listCalls)
		}

		refreshed, err := svc.RefreshIssue(context.Background(), cachedIssue)
		if err != nil {
			t.Fatalf("RefreshIssue on read-only service: %v", err)
		}
		if getCalls != 0 {
			t.Fatalf("expected read-only RefreshIssue to skip provider refresh, got %d calls", getCalls)
		}
		if refreshed.Identifier != cachedIssue.Identifier || refreshed.Title != cachedIssue.Title {
			t.Fatalf("expected read-only refresh to return cached issue, got %#v", refreshed)
		}

		fetched, err := svc.GetIssueByIdentifier(context.Background(), cachedIssue.Identifier)
		if err != nil {
			t.Fatalf("GetIssueByIdentifier on read-only service: %v", err)
		}
		if getCalls != 0 {
			t.Fatalf("expected read-only GetIssueByIdentifier to skip provider refresh, got %d calls", getCalls)
		}
		if fetched.Identifier != cachedIssue.Identifier {
			t.Fatalf("expected cached issue from read-only lookup, got %#v", fetched)
		}

		if _, err := svc.RefreshIssue(context.Background(), nil); err == nil {
			t.Fatal("expected nil issue refresh to fail")
		}
		if _, err := svc.RefreshIssueByID(context.Background(), "missing"); !kanban.IsNotFound(err) {
			t.Fatalf("expected missing issue ID to fail with not found, got %v", err)
		}

		missingSvc := NewService(store)
		missingSvc.RegisterProvider(&serviceProviderStub{
			kind: "stub",
			getIssueFunc: func(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
				return nil, kanban.ErrNotFound
			},
		})
		_, err = missingSvc.RefreshIssue(context.Background(), &kanban.Issue{
			ProjectID:        project.ID,
			ProviderKind:     "stub",
			ProviderIssueRef: "remote-2",
			Identifier:       "STUB-2",
			Title:            "Missing remote",
			State:            kanban.StateReady,
		})
		if !kanban.IsNotFound(err) {
			t.Fatalf("expected missing provider issue to return not found, got %v", err)
		}
	})

	t.Run("mutation not found branches", func(t *testing.T) {
		store := newProvidersTestStore(t)
		svc := NewService(store)
		project, err := store.CreateProject("Local Project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		epic, err := store.CreateEpic(project.ID, "Epic", "")
		if err != nil {
			t.Fatalf("CreateEpic: %v", err)
		}
		if _, err := store.CreateIssue(project.ID, epic.ID, "Issue", "", 0, nil); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}

		if err := svc.UpdateProject(context.Background(), "missing", "Name", "Desc", "", "", kanban.ProviderKindKanban, "", nil); !kanban.IsNotFound(err) {
			t.Fatalf("expected missing project update to fail with not found, got %v", err)
		}
		if err := svc.UpdateEpic("missing", project.ID, "Epic", ""); !kanban.IsNotFound(err) {
			t.Fatalf("expected missing epic update to fail with not found, got %v", err)
		}
		if err := svc.DeleteEpic("missing"); !kanban.IsNotFound(err) {
			t.Fatalf("expected missing epic delete to fail with not found, got %v", err)
		}
		if _, err := svc.UpdateIssue(context.Background(), "missing", map[string]interface{}{"title": "x"}); !kanban.IsNotFound(err) {
			t.Fatalf("expected missing issue update to fail with not found, got %v", err)
		}
		if err := svc.DeleteIssue(context.Background(), "missing"); !kanban.IsNotFound(err) {
			t.Fatalf("expected missing issue delete to fail with not found, got %v", err)
		}
		if _, err := svc.SetIssueState(context.Background(), "missing", string(kanban.StateDone)); !kanban.IsNotFound(err) {
			t.Fatalf("expected missing issue state update to fail with not found, got %v", err)
		}
		if _, err := svc.ListIssueComments(context.Background(), "missing"); !kanban.IsNotFound(err) {
			t.Fatalf("expected missing issue comments lookup to fail with not found, got %v", err)
		}
		if err := svc.DeleteIssueComment(context.Background(), "missing", "missing"); !kanban.IsNotFound(err) {
			t.Fatalf("expected missing issue comment delete to fail with not found, got %v", err)
		}
		if _, err := svc.GetIssueCommentAttachmentContent(context.Background(), "missing", "missing", "missing"); !kanban.IsNotFound(err) {
			t.Fatalf("expected missing attachment lookup to fail with not found, got %v", err)
		}
	})

	t.Run("create and update guards", func(t *testing.T) {
		store := newProvidersTestStore(t)
		svc := NewService(store)
		var stubProjectID string

		noEpicCaps := kanban.ProviderCapabilities{
			Projects:         true,
			Epics:            false,
			Issues:           true,
			IssueStateUpdate: true,
			IssueDelete:      true,
		}
		svc.RegisterProvider(&serviceProviderStub{
			kind: "stub",
			capabilities: &kanban.ProviderCapabilities{
				Projects:         true,
				Epics:            true,
				Issues:           true,
				IssueStateUpdate: true,
				IssueDelete:      true,
			},
			createIssueFunc: func(_ context.Context, project *kanban.Project, input IssueCreateInput) (*kanban.Issue, error) {
				return &kanban.Issue{
					ProjectID:        project.ID,
					ProviderKind:     "stub",
					ProviderIssueRef: "remote-1",
					Identifier:       "STUB-1",
					Title:            input.Title,
					Description:      input.Description,
					State:            kanban.StateReady,
				}, nil
			},
			getIssueFunc: func(_ context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
				if project.ID != stubProjectID || identifier != "STUB-1" {
					return nil, kanban.ErrNotFound
				}
				return &kanban.Issue{
					ProjectID:        project.ID,
					ProviderKind:     "stub",
					ProviderIssueRef: "remote-1",
					Identifier:       "STUB-1",
					Title:            "Provider issue",
					Description:      "body",
					State:            kanban.StateReady,
				}, nil
			},
			updateIssueFunc: func(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error) {
				t.Fatal("expected no-op update to skip provider update")
				return nil, nil
			},
		})
		svc.RegisterProvider(&serviceProviderStub{
			kind:         "noepic",
			capabilities: &noEpicCaps,
		})

		if _, err := svc.CreateProject(context.Background(), "Broken", "", "", "", "missing", "", nil); err == nil {
			t.Fatal("expected CreateProject to reject unknown provider kinds")
		}

		project, err := store.CreateProjectWithProvider("Stub Project", "", "", "", "stub", "stub-ref", map[string]interface{}{"assignee": "me"})
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		stubProjectID = project.ID
		if err := svc.UpdateProject(context.Background(), project.ID, "Stub Project 2", "", "", "", "missing", "", nil); err == nil {
			t.Fatal("expected UpdateProject to reject unknown provider kinds")
		}

		if _, err := svc.CreateEpic("", "Epic", ""); !errors.Is(err, kanban.ErrValidation) {
			t.Fatalf("expected blank project ID to fail, got %v", err)
		}
		if err := svc.UpdateEpic("epic-1", "", "Epic", ""); !errors.Is(err, kanban.ErrValidation) {
			t.Fatalf("expected blank project ID update to fail, got %v", err)
		}

		noEpicProject, err := store.CreateProjectWithProvider("No Epic", "", "", "", "noepic", "noepic-ref", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider noepic: %v", err)
		}
		if _, err := svc.CreateIssue(context.Background(), IssueCreateInput{
			ProjectID: noEpicProject.ID,
			EpicID:    "epic-1",
			Title:     "Provider issue with epic",
		}); !errors.Is(err, ErrUnsupportedCapability) {
			t.Fatalf("expected unsupported epic usage to be rejected, got %v", err)
		}
		if _, err := svc.CreateIssue(context.Background(), IssueCreateInput{
			ProjectID: noEpicProject.ID,
			EpicID:    "epic-1",
			Title:     "Recurring provider issue",
			IssueType: kanban.IssueTypeRecurring,
		}); !errors.Is(err, ErrUnsupportedCapability) {
			t.Fatalf("expected recurring provider issue to require epic support, got %v", err)
		}

		if _, err := svc.CreateIssue(context.Background(), IssueCreateInput{Title: "Missing project"}); !errors.Is(err, kanban.ErrValidation) {
			t.Fatalf("expected CreateIssue to reject missing project ID, got %v", err)
		}

		createdIssue, err := svc.CreateIssue(context.Background(), IssueCreateInput{
			ProjectID:   project.ID,
			Title:       "Provider issue",
			Description: "body",
			State:       string(kanban.StateReady),
		})
		if err != nil {
			t.Fatalf("CreateIssue provider-backed: %v", err)
		}

		updated, err := svc.UpdateIssue(context.Background(), createdIssue.Identifier, map[string]interface{}{})
		if err != nil {
			t.Fatalf("UpdateIssue no-op: %v", err)
		}
		if updated.Identifier != createdIssue.Identifier {
			t.Fatalf("expected no-op update to return the same issue, got %#v", updated)
		}
	})

	t.Run("kanban provider edge branches", func(t *testing.T) {
		store := newProvidersTestStore(t)
		provider := NewKanbanProvider(store)

		issues, err := provider.ListIssues(context.Background(), nil, kanban.IssueQuery{State: string(kanban.StateReady)})
		if err != nil {
			t.Fatalf("ListIssues nil project: %v", err)
		}
		if len(issues) != 0 {
			t.Fatalf("expected empty issue list, got %#v", issues)
		}

		project, err := store.CreateProject("Kanban Project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		issue, err := provider.CreateIssue(context.Background(), nil, IssueCreateInput{
			ProjectID: project.ID,
			Title:     "Backlog issue",
		})
		if err != nil {
			t.Fatalf("CreateIssue fallback to input project: %v", err)
		}
		if issue.ProjectID != project.ID || issue.State != kanban.StateBacklog {
			t.Fatalf("expected backlog issue to stay local, got %#v", issue)
		}

		if _, err := provider.UpdateIssue(context.Background(), nil, &kanban.Issue{ID: "missing"}, map[string]interface{}{"title": "renamed"}); err == nil {
			t.Fatal("expected update against missing issue to fail")
		}
		if _, err := provider.SetIssueState(context.Background(), nil, &kanban.Issue{ID: "missing"}, string(kanban.StateDone)); err == nil {
			t.Fatal("expected state update against missing issue to fail")
		}

		body := "hello"
		comment, err := provider.CreateIssueComment(context.Background(), nil, issue, IssueCommentInput{
			Body: &body,
			Attachments: []IssueCommentAttachment{{
				Path:        writeProviderCommentAttachment(t, "attachment.txt", []byte("provider attachment")),
				ContentType: "text/plain",
			}},
		})
		if err != nil {
			t.Fatalf("CreateIssueComment: %v", err)
		}
		attachment, path, err := store.GetIssueCommentAttachmentContent(issue.ID, comment.ID, comment.Attachments[0].ID)
		if err != nil {
			t.Fatalf("GetIssueCommentAttachmentContent: %v", err)
		}
		if attachment.ID != comment.Attachments[0].ID {
			t.Fatalf("unexpected attachment metadata: %#v", attachment)
		}
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("Chmod attachment: %v", err)
		}
		if _, err := provider.GetIssueCommentAttachmentContent(context.Background(), nil, issue, comment.ID, comment.Attachments[0].ID); err == nil {
			t.Fatal("expected unreadable attachment file to fail")
		}
	})
}
