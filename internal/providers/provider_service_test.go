package providers

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

type serviceProviderStub struct {
	kind         string
	capabilities *kanban.ProviderCapabilities

	validateFunc                  func(context.Context, *kanban.Project) error
	listIssuesFunc                func(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error)
	getIssueFunc                  func(context.Context, *kanban.Project, string) (*kanban.Issue, error)
	createIssueFunc               func(context.Context, *kanban.Project, IssueCreateInput) (*kanban.Issue, error)
	updateIssueFunc               func(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error)
	deleteIssueFunc               func(context.Context, *kanban.Project, *kanban.Issue) error
	setIssueStateFunc             func(context.Context, *kanban.Project, *kanban.Issue, string) (*kanban.Issue, error)
	listIssueCommentsFunc         func(context.Context, *kanban.Project, *kanban.Issue) ([]kanban.IssueComment, error)
	createIssueCommentFunc        func(context.Context, *kanban.Project, *kanban.Issue, IssueCommentInput) (*kanban.IssueComment, error)
	updateIssueCommentFunc        func(context.Context, *kanban.Project, *kanban.Issue, string, IssueCommentInput) (*kanban.IssueComment, error)
	deleteIssueCommentFunc        func(context.Context, *kanban.Project, *kanban.Issue, string) error
	getIssueCommentAttachmentFunc func(context.Context, *kanban.Project, *kanban.Issue, string, string) (*IssueCommentAttachmentContent, error)
}

var _ Provider = (*serviceProviderStub)(nil)

func (p *serviceProviderStub) Kind() string {
	return p.kind
}

func (p *serviceProviderStub) Capabilities() kanban.ProviderCapabilities {
	if p.capabilities != nil {
		return *p.capabilities
	}
	return kanban.DefaultCapabilities(p.kind)
}

func (p *serviceProviderStub) ValidateProject(ctx context.Context, project *kanban.Project) error {
	if p.validateFunc != nil {
		return p.validateFunc(ctx, project)
	}
	return nil
}

func (p *serviceProviderStub) ListIssues(ctx context.Context, project *kanban.Project, query kanban.IssueQuery) ([]kanban.Issue, error) {
	if p.listIssuesFunc != nil {
		return p.listIssuesFunc(ctx, project, query)
	}
	return nil, nil
}

func (p *serviceProviderStub) GetIssue(ctx context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
	if p.getIssueFunc != nil {
		return p.getIssueFunc(ctx, project, identifier)
	}
	return nil, kanban.ErrNotFound
}

func (p *serviceProviderStub) CreateIssue(ctx context.Context, project *kanban.Project, input IssueCreateInput) (*kanban.Issue, error) {
	if p.createIssueFunc != nil {
		return p.createIssueFunc(ctx, project, input)
	}
	return nil, ErrUnsupportedCapability
}

func (p *serviceProviderStub) UpdateIssue(ctx context.Context, project *kanban.Project, issue *kanban.Issue, updates map[string]interface{}) (*kanban.Issue, error) {
	if p.updateIssueFunc != nil {
		return p.updateIssueFunc(ctx, project, issue, updates)
	}
	return nil, ErrUnsupportedCapability
}

func (p *serviceProviderStub) DeleteIssue(ctx context.Context, project *kanban.Project, issue *kanban.Issue) error {
	if p.deleteIssueFunc != nil {
		return p.deleteIssueFunc(ctx, project, issue)
	}
	return ErrUnsupportedCapability
}

func (p *serviceProviderStub) SetIssueState(ctx context.Context, project *kanban.Project, issue *kanban.Issue, state string) (*kanban.Issue, error) {
	if p.setIssueStateFunc != nil {
		return p.setIssueStateFunc(ctx, project, issue, state)
	}
	return nil, ErrUnsupportedCapability
}

func (p *serviceProviderStub) ListIssueComments(ctx context.Context, project *kanban.Project, issue *kanban.Issue) ([]kanban.IssueComment, error) {
	if p.listIssueCommentsFunc != nil {
		return p.listIssueCommentsFunc(ctx, project, issue)
	}
	return nil, nil
}

func (p *serviceProviderStub) CreateIssueComment(ctx context.Context, project *kanban.Project, issue *kanban.Issue, input IssueCommentInput) (*kanban.IssueComment, error) {
	if p.createIssueCommentFunc != nil {
		return p.createIssueCommentFunc(ctx, project, issue, input)
	}
	return nil, ErrUnsupportedCapability
}

func (p *serviceProviderStub) UpdateIssueComment(ctx context.Context, project *kanban.Project, issue *kanban.Issue, commentID string, input IssueCommentInput) (*kanban.IssueComment, error) {
	if p.updateIssueCommentFunc != nil {
		return p.updateIssueCommentFunc(ctx, project, issue, commentID, input)
	}
	return nil, ErrUnsupportedCapability
}

func (p *serviceProviderStub) DeleteIssueComment(ctx context.Context, project *kanban.Project, issue *kanban.Issue, commentID string) error {
	if p.deleteIssueCommentFunc != nil {
		return p.deleteIssueCommentFunc(ctx, project, issue, commentID)
	}
	return ErrUnsupportedCapability
}

func (p *serviceProviderStub) GetIssueCommentAttachmentContent(ctx context.Context, project *kanban.Project, issue *kanban.Issue, commentID, attachmentID string) (*IssueCommentAttachmentContent, error) {
	if p.getIssueCommentAttachmentFunc != nil {
		return p.getIssueCommentAttachmentFunc(ctx, project, issue, commentID, attachmentID)
	}
	return nil, ErrUnsupportedCapability
}

func newProvidersTestStore(t *testing.T) *kanban.Store {
	t.Helper()
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestProviderHelpersAndResolutionBranches(t *testing.T) {
	if got := normalizeKind("  STUB  "); got != "stub" {
		t.Fatalf("normalizeKind: got %q", got)
	}
	if got := normalizeKind(""); got != kanban.ProviderKindKanban {
		t.Fatalf("normalizeKind empty: got %q", got)
	}

	original := map[string]interface{}{"alpha": "one"}
	cloned := cloneProviderConfig(original)
	cloned["alpha"] = "two"
	if original["alpha"] != "one" {
		t.Fatalf("cloneProviderConfig should copy map contents")
	}
	if got := cloneProviderConfig(nil); len(got) != 0 {
		t.Fatalf("cloneProviderConfig nil = %#v", got)
	}
	if got := cloneProviderConfig(map[string]interface{}{}); len(got) != 0 {
		t.Fatalf("cloneProviderConfig empty = %#v", got)
	}
	if got := providerConfigString(nil, "missing"); got != "" {
		t.Fatalf("providerConfigString nil = %q", got)
	}
	if got := providerConfigString(map[string]interface{}{"answer": 42}, "answer"); got != "42" {
		t.Fatalf("providerConfigString int = %q", got)
	}
	if got := providerConfigString(map[string]interface{}{"answer": "  forty-two  "}, "answer"); got != "forty-two" {
		t.Fatalf("providerConfigString string = %q", got)
	}

	input := IssueCreateInput{
		EpicID:      "epic-1",
		AgentName:   "agent",
		AgentPrompt: "prompt",
		BranchName:  "branch",
		PRURL:       "https://example.com/pr/1",
	}
	for key, want := range map[string]string{
		"epic_id":      "epic-1",
		"agent_name":   "agent",
		"agent_prompt": "prompt",
		"branch_name":  "branch",
		"pr_url":       "https://example.com/pr/1",
	} {
		if got := inputValueForKey(input, key); got != want {
			t.Fatalf("inputValueForKey(%s) = %q, want %q", key, got, want)
		}
	}
	if got := inputValueForKey(input, "unknown"); got != "" {
		t.Fatalf("inputValueForKey unknown = %q", got)
	}

	if got, ok := preferredStringValue("  primary  ", "fallback"); !ok || got != "primary" {
		t.Fatalf("preferredStringValue primary = %q %v", got, ok)
	}
	if got, ok := preferredStringValue("", " fallback "); !ok || got != "fallback" {
		t.Fatalf("preferredStringValue fallback = %q %v", got, ok)
	}
	if _, ok := preferredStringValue("   ", "   "); ok {
		t.Fatal("preferredStringValue should reject blanks")
	}

	if got, ok := stringUpdateValue(nil); ok || got != "" {
		t.Fatalf("stringUpdateValue nil = %q %v", got, ok)
	}
	if got, ok := stringUpdateValue("<nil>"); ok || got != "" {
		t.Fatalf("stringUpdateValue <nil> = %q %v", got, ok)
	}
	if got, ok := stringUpdateValue(123); !ok || got != "123" {
		t.Fatalf("stringUpdateValue int = %q %v", got, ok)
	}

	if got, ok := trimmedStringUpdate("   "); ok || got != "" {
		t.Fatalf("trimmedStringUpdate blank = %q %v", got, ok)
	}
	if got, ok := trimmedStringUpdate("  value "); !ok || got != "value" {
		t.Fatalf("trimmedStringUpdate value = %q %v", got, ok)
	}
	if got, ok := trimmedStringUpdate("<nil>"); ok || got != "" {
		t.Fatalf("trimmedStringUpdate <nil> = %q %v", got, ok)
	}

	existing := &kanban.Project{
		ID:                 "proj-1",
		State:              kanban.ProjectStateRunning,
		PermissionProfile:  kanban.PermissionProfileFullAccess,
		CreatedAt:          time.Unix(10, 0).UTC(),
		UpdatedAt:          time.Unix(20, 0).UTC(),
		ProviderConfig:     map[string]interface{}{"from": "existing"},
		ProviderKind:       "existing-kind",
		ProviderProjectRef: "existing-ref",
	}
	candidate, err := buildProjectCandidate(existing, "Name", "Description", "$HOME/repo", "/tmp/workflow.md", "  STUB ", " ref ", map[string]interface{}{"team": "alpha"})
	if err != nil {
		t.Fatalf("buildProjectCandidate: %v", err)
	}
	if candidate.ID != existing.ID || candidate.State != existing.State || candidate.PermissionProfile != existing.PermissionProfile {
		t.Fatalf("expected candidate to inherit existing identity and state, got %+v", candidate)
	}
	if candidate.ProviderKind != "stub" || candidate.ProviderProjectRef != "ref" {
		t.Fatalf("expected normalized provider fields, got %+v", candidate)
	}
	if candidate.ProviderConfig["team"] != "alpha" {
		t.Fatalf("expected provider config clone, got %+v", candidate.ProviderConfig)
	}

	store := newProvidersTestStore(t)
	svc := NewService(store)
	svc.RegisterProvider(&serviceProviderStub{kind: "stub"})
	if got := svc.ProviderForProject(nil); got == nil || got.Kind() != kanban.ProviderKindKanban {
		t.Fatalf("expected default provider for nil project, got %#v", got)
	}
	if got := svc.providerForKind(" stub "); got == nil || got.Kind() != "stub" {
		t.Fatalf("providerForKind = %#v", got)
	}
	if _, err := svc.providerForKindOrError("missing"); err == nil {
		t.Fatal("expected missing provider kind to fail")
	}
	if _, err := svc.providerForProjectOrError(nil); err != nil {
		t.Fatalf("providerForProjectOrError nil: %v", err)
	}
	if _, _, err := svc.resolveProjectProvider(""); err != nil {
		t.Fatalf("resolveProjectProvider blank: %v", err)
	}
	if _, _, err := svc.resolveIssueProvider(nil); err == nil {
		t.Fatal("expected nil issue to fail")
	}
	if got := authoritativeProviderSyncQuery(kanban.IssueQuery{ProjectID: "  proj  ", Assignee: "  me  "}); got.ProjectID != "  proj  " || got.Assignee != "me" {
		t.Fatalf("authoritativeProviderSyncQuery = %+v", got)
	}
	if got := (&Service{}).listSyncKey(kanban.IssueQuery{ProjectID: "  proj  ", Assignee: " me "}); got != "proj|me" {
		t.Fatalf("listSyncKey = %q", got)
	}

	if _, _, propagate := newBoundedContext(nil, time.Second); propagate {
		t.Fatal("expected nil ctx to create a non-propagating timeout")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, propagate := newBoundedContext(cancelled, time.Second); !propagate {
		t.Fatal("expected cancelled ctx to propagate")
	}
	deadlined, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(20*time.Millisecond))
	defer cancelDeadline()
	if _, _, propagate := newBoundedContext(deadlined, time.Second); !propagate {
		t.Fatal("expected short deadline to propagate")
	}
	if !shouldPropagateReadSyncError(cancelled, context.Canceled, true) {
		t.Fatal("expected cancellation to propagate")
	}
	if shouldPropagateReadSyncError(context.Background(), context.DeadlineExceeded, false) {
		t.Fatal("expected best-effort errors not to propagate when parent context was not propagated")
	}
}

func TestServiceProviderBackedLifecycleBranches(t *testing.T) {
	store := newProvidersTestStore(t)
	svc := NewService(store)

	noEpicCaps := kanban.ProviderCapabilities{
		Projects:         true,
		Epics:            false,
		Issues:           true,
		IssueStateUpdate: true,
		IssueDelete:      true,
	}
	stubCaps := kanban.ProviderCapabilities{
		Projects:         true,
		Epics:            true,
		Issues:           true,
		IssueStateUpdate: true,
		IssueDelete:      true,
	}

	noEpicProvider := &serviceProviderStub{
		kind:         "noepic",
		capabilities: &noEpicCaps,
	}
	svc.RegisterProvider(noEpicProvider)

	var (
		createCalls  int
		updateCalls  int
		deleteCalls  int
		stateCalls   int
		commentCalls int
	)
	stubProvider := &serviceProviderStub{
		kind:         "stub",
		capabilities: &stubCaps,
		listIssuesFunc: func(_ context.Context, project *kanban.Project, query kanban.IssueQuery) ([]kanban.Issue, error) {
			return []kanban.Issue{{
				ProjectID:        project.ID,
				ProviderKind:     "stub",
				ProviderIssueRef: "remote-1",
				Identifier:       "PROV-1",
				Title:            "Provider issue",
				State:            kanban.StateReady,
			}}, nil
		},
		getIssueFunc: func(_ context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
			if identifier != "PROV-1" {
				return nil, kanban.ErrNotFound
			}
			return &kanban.Issue{
				ProjectID:        project.ID,
				ProviderKind:     "stub",
				ProviderIssueRef: "remote-1",
				Identifier:       "PROV-1",
				Title:            "Provider issue",
				Description:      "Provider issue body",
				State:            kanban.StateReady,
				AgentName:        "provider-agent",
				AgentPrompt:      "provider prompt",
				BranchName:       "provider/branch",
				PRURL:            "https://provider.example/pr/1",
			}, nil
		},
		createIssueFunc: func(_ context.Context, project *kanban.Project, input IssueCreateInput) (*kanban.Issue, error) {
			createCalls++
			return &kanban.Issue{
				ProjectID:        project.ID,
				EpicID:           input.EpicID,
				ProviderKind:     "stub",
				ProviderIssueRef: "remote-1",
				Identifier:       "PROV-1",
				Title:            input.Title,
				Description:      input.Description,
				State:            kanban.StateReady,
				AgentName:        "provider-agent",
				AgentPrompt:      "provider prompt",
				BranchName:       "provider/branch",
				PRURL:            "https://provider.example/pr/1",
			}, nil
		},
		updateIssueFunc: func(_ context.Context, project *kanban.Project, issue *kanban.Issue, updates map[string]interface{}) (*kanban.Issue, error) {
			updateCalls++
			out := *issue
			if title, ok := updates["title"].(string); ok {
				out.Title = title
			}
			if description, ok := updates["description"].(string); ok {
				out.Description = description
			}
			if state, ok := updates["state"].(string); ok {
				out.State = kanban.State(state)
			}
			return &out, nil
		},
		deleteIssueFunc: func(_ context.Context, project *kanban.Project, issue *kanban.Issue) error {
			deleteCalls++
			return nil
		},
		setIssueStateFunc: func(_ context.Context, project *kanban.Project, issue *kanban.Issue, state string) (*kanban.Issue, error) {
			stateCalls++
			out := *issue
			out.State = kanban.State(state)
			return &out, nil
		},
		listIssueCommentsFunc: func(_ context.Context, project *kanban.Project, issue *kanban.Issue) ([]kanban.IssueComment, error) {
			return nil, nil
		},
		createIssueCommentFunc: func(_ context.Context, project *kanban.Project, issue *kanban.Issue, input IssueCommentInput) (*kanban.IssueComment, error) {
			commentCalls++
			if input.Body == nil || strings.TrimSpace(*input.Body) == "" {
				t.Fatalf("expected comment body to be forwarded, got %#v", input)
			}
			return &kanban.IssueComment{
				ID:      "comment-1",
				IssueID: issue.ID,
				Body:    *input.Body,
			}, nil
		},
		updateIssueCommentFunc: func(_ context.Context, project *kanban.Project, issue *kanban.Issue, commentID string, input IssueCommentInput) (*kanban.IssueComment, error) {
			out := &kanban.IssueComment{ID: commentID, IssueID: issue.ID}
			if input.Body != nil {
				out.Body = *input.Body
			}
			return out, nil
		},
		deleteIssueCommentFunc: func(context.Context, *kanban.Project, *kanban.Issue, string) error { return nil },
		getIssueCommentAttachmentFunc: func(_ context.Context, _ *kanban.Project, issue *kanban.Issue, commentID, attachmentID string) (*IssueCommentAttachmentContent, error) {
			return &IssueCommentAttachmentContent{
				Attachment: kanban.IssueCommentAttachment{
					ID:          attachmentID,
					CommentID:   commentID,
					Filename:    "provider.txt",
					ContentType: "text/plain",
				},
				Content: io.NopCloser(strings.NewReader("provider attachment")),
			}, nil
		},
	}
	svc.RegisterProvider(stubProvider)

	localProject, err := store.CreateProject("Local Project", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject local: %v", err)
	}
	stubProject, err := store.CreateProjectWithProvider("Stub Project", "", "", "", "stub", "stub-ref", map[string]interface{}{"assignee": "me"})
	if err != nil {
		t.Fatalf("CreateProjectWithProvider stub: %v", err)
	}
	noEpicProject, err := store.CreateProjectWithProvider("No Epic Project", "", "", "", "noepic", "noepic-ref", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider noepic: %v", err)
	}

	if got := svc.ProviderForProject(localProject); got == nil || got.Kind() != kanban.ProviderKindKanban {
		t.Fatalf("expected kanban provider for local project, got %#v", got)
	}
	if got := svc.ProviderForProject(stubProject); got == nil || got.Kind() != "stub" {
		t.Fatalf("expected stub provider for stub project, got %#v", got)
	}

	projects, err := svc.ListProjectSummaries()
	if err != nil {
		t.Fatalf("ListProjectSummaries: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("expected three projects, got %#v", projects)
	}

	epic, err := svc.CreateEpic(stubProject.ID, "Epic", "Description")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if err := svc.UpdateEpic(epic.ID, stubProject.ID, "Epic updated", "Updated description"); err != nil {
		t.Fatalf("UpdateEpic: %v", err)
	}
	epics, err := svc.ListEpicSummaries(stubProject.ID)
	if err != nil {
		t.Fatalf("ListEpicSummaries stub: %v", err)
	}
	if len(epics) != 1 || epics[0].Name != "Epic updated" {
		t.Fatalf("unexpected epic summaries: %#v", epics)
	}
	if err := svc.DeleteEpic(epic.ID); err != nil {
		t.Fatalf("DeleteEpic: %v", err)
	}
	if _, err := svc.CreateEpic(noEpicProject.ID, "Blocked", ""); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("expected unsupported epic creation, got %v", err)
	}
	noEpic, err := store.CreateEpic(noEpicProject.ID, "No epic", "")
	if err != nil {
		t.Fatalf("CreateEpic direct: %v", err)
	}
	if err := svc.DeleteEpic(noEpic.ID); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("expected unsupported epic delete, got %v", err)
	}
	if epics, err := svc.ListEpicSummaries(noEpicProject.ID); err != nil || len(epics) != 0 {
		t.Fatalf("expected empty epic summaries for unsupported provider, got %#v err=%v", epics, err)
	}

	recurringEpic, err := svc.CreateEpic(stubProject.ID, "Recurring Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic recurring: %v", err)
	}
	recurring, err := svc.CreateIssue(context.Background(), IssueCreateInput{
		ProjectID: stubProject.ID,
		EpicID:    recurringEpic.ID,
		Title:     "Recurring issue",
		IssueType: kanban.IssueTypeRecurring,
		Cron:      "0 0 * * *",
		Enabled:   boolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateIssue recurring: %v", err)
	}
	if recurring.IssueType != kanban.IssueTypeRecurring || recurring.State != kanban.StateBacklog {
		t.Fatalf("expected recurring kanban issue, got %#v", recurring)
	}

	created, err := svc.CreateIssue(context.Background(), IssueCreateInput{
		ProjectID:   stubProject.ID,
		EpicID:      recurringEpic.ID,
		Title:       "Provider issue",
		Description: "Provider issue body",
		Priority:    1,
		Labels:      []string{"alpha"},
		AgentName:   "input-agent",
		AgentPrompt: "input prompt",
		BranchName:  "input/branch",
		PRURL:       "https://example.com/pr/2",
		State:       string(kanban.StateReady),
	})
	if err != nil {
		t.Fatalf("CreateIssue provider-backed: %v", err)
	}
	if createCalls != 1 {
		t.Fatalf("expected provider create to be called once, got %d", createCalls)
	}
	if created.Title != "Provider issue" || created.AgentName != "input-agent" || created.BranchName != "input/branch" {
		t.Fatalf("expected create to preserve input overrides, got %#v", created)
	}

	detail, err := svc.GetIssueDetailByIdentifier(context.Background(), created.Identifier)
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier: %v", err)
	}
	if detail.Title != "Provider issue" || detail.AgentName != "input-agent" {
		t.Fatalf("unexpected created issue detail: %#v", detail)
	}

	board, err := svc.BoardOverview(context.Background(), stubProject.ID)
	if err != nil {
		t.Fatalf("BoardOverview: %v", err)
	}
	if board[kanban.StateReady] == 0 {
		t.Fatalf("expected board overview to count ready issue, got %#v", board)
	}

	localUpdate, err := svc.UpdateIssue(context.Background(), created.Identifier, map[string]interface{}{
		"agent_name":         "updated-agent",
		"branch_name":        "updated/branch",
		"permission_profile": "full-access",
	})
	if err != nil {
		t.Fatalf("UpdateIssue local-only: %v", err)
	}
	if updateCalls != 0 {
		t.Fatalf("expected provider update to be skipped for local-only changes, got %d", updateCalls)
	}
	if localUpdate.AgentName != "updated-agent" || localUpdate.BranchName != "updated/branch" || localUpdate.PermissionProfile != kanban.PermissionProfileFullAccess {
		t.Fatalf("expected local metadata update, got %#v", localUpdate)
	}

	updatedDetail, err := svc.UpdateIssue(context.Background(), created.Identifier, map[string]interface{}{
		"title":       "Renamed",
		"description": "Updated body",
		"state":       string(kanban.StateInReview),
		"agent_name":  "provider-agent",
	})
	if err != nil {
		t.Fatalf("UpdateIssue provider-backed: %v", err)
	}
	if updateCalls != 1 {
		t.Fatalf("expected provider update to be called once, got %d", updateCalls)
	}
	if updatedDetail.Title != "Renamed" || updatedDetail.State != kanban.StateInReview {
		t.Fatalf("expected provider update to be reflected, got %#v", updatedDetail)
	}

	if _, err := svc.UpdateIssue(context.Background(), created.Identifier, map[string]interface{}{"issue_type": string(kanban.IssueTypeRecurring)}); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("expected recurring issue update to be rejected, got %v", err)
	}
	if _, err := svc.UpdateIssue(context.Background(), created.Identifier, map[string]interface{}{"project_id": localProject.ID}); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("expected provider-backed project move to be rejected, got %v", err)
	}
	if _, err := svc.UpdateIssue(context.Background(), created.Identifier, map[string]interface{}{"cron": "0 * * * *"}); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("expected cron update to be rejected, got %v", err)
	}

	stateDetail, err := svc.SetIssueState(context.Background(), created.Identifier, string(kanban.StateDone))
	if err != nil {
		t.Fatalf("SetIssueState: %v", err)
	}
	if stateCalls != 1 || stateDetail.State != kanban.StateDone {
		t.Fatalf("expected provider set state update, got calls=%d detail=%#v", stateCalls, stateDetail)
	}

	body := "Provider comment"
	comment, err := svc.CreateIssueCommentWithResult(context.Background(), created.Identifier, IssueCommentInput{
		Body: &body,
		Attachments: []IssueCommentAttachment{{
			Path:        filepath.Join(t.TempDir(), "preview.txt"),
			ContentType: "text/plain",
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueCommentWithResult: %v", err)
	}
	if commentCalls != 1 || comment.Body != body {
		t.Fatalf("expected provider comment creation, got calls=%d comment=%#v", commentCalls, comment)
	}
	if err := svc.CreateIssueComment(context.Background(), created.Identifier, IssueCommentInput{Body: &body}); err != nil {
		t.Fatalf("CreateIssueComment wrapper: %v", err)
	}
	comments, err := svc.ListIssueComments(context.Background(), created.Identifier)
	if err != nil {
		t.Fatalf("ListIssueComments provider: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("expected empty provider comment list to normalize to empty slice, got %#v", comments)
	}
	updatedComment, err := svc.UpdateIssueComment(context.Background(), created.Identifier, comment.ID, IssueCommentInput{Body: &body})
	if err != nil {
		t.Fatalf("UpdateIssueComment: %v", err)
	}
	if updatedComment.ID != comment.ID || updatedComment.Body != body {
		t.Fatalf("expected updated comment, got %#v", updatedComment)
	}
	if err := svc.DeleteIssueComment(context.Background(), created.Identifier, comment.ID); err != nil {
		t.Fatalf("DeleteIssueComment: %v", err)
	}
	attachment, err := svc.GetIssueCommentAttachmentContent(context.Background(), created.Identifier, comment.ID, "attachment-1")
	if err != nil {
		t.Fatalf("GetIssueCommentAttachmentContent: %v", err)
	}
	defer attachment.Content.Close()
	data, err := io.ReadAll(attachment.Content)
	if err != nil {
		t.Fatalf("Read attachment content: %v", err)
	}
	if string(data) != "provider attachment" {
		t.Fatalf("expected provider attachment content, got %q", string(data))
	}

	assetDir := t.TempDir()
	assetPath := filepath.Join(assetDir, "preview.txt")
	if err := os.WriteFile(assetPath, []byte("preview"), 0o644); err != nil {
		t.Fatalf("WriteFile asset: %v", err)
	}
	asset, err := svc.AttachIssueAssetPath(context.Background(), created.Identifier, assetPath)
	if err != nil {
		t.Fatalf("AttachIssueAssetPath: %v", err)
	}
	assets, err := svc.ListIssueAssets(context.Background(), created.Identifier)
	if err != nil {
		t.Fatalf("ListIssueAssets: %v", err)
	}
	if len(assets) != 1 || assets[0].ID != asset.ID {
		t.Fatalf("expected attached asset to be listed, got %#v", assets)
	}
	contentAsset, contentPath, err := svc.GetIssueAssetContent(context.Background(), created.Identifier, asset.ID)
	if err != nil {
		t.Fatalf("GetIssueAssetContent: %v", err)
	}
	if contentAsset.ID != asset.ID || !strings.HasSuffix(contentPath, asset.ID+".txt") {
		t.Fatalf("unexpected asset content metadata: asset=%#v path=%q", contentAsset, contentPath)
	}
	if err := svc.DeleteIssueAsset(context.Background(), created.Identifier, asset.ID); err != nil {
		t.Fatalf("DeleteIssueAsset: %v", err)
	}

	if err := svc.DeleteIssue(context.Background(), created.Identifier); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("expected provider delete to be called once, got %d", deleteCalls)
	}
	if _, err := store.GetIssueByIdentifier(created.Identifier); !kanban.IsNotFound(err) {
		t.Fatalf("expected deleted provider issue to be removed from store, got %v", err)
	}

	refreshableIssue := &kanban.Issue{
		ProjectID:        stubProject.ID,
		ProviderKind:     "stub",
		ProviderIssueRef: "remote-3",
		Identifier:       "STUB-REFRESH",
		Title:            "Refresh issue",
		State:            kanban.StateReady,
		ProviderShadow:   true,
	}
	refreshableIssue, err = store.UpsertProviderIssue(stubProject.ID, refreshableIssue)
	if err != nil {
		t.Fatalf("UpsertProviderIssue refreshable: %v", err)
	}
	stubProvider.getIssueFunc = func(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
		return &kanban.Issue{
			ProjectID:        stubProject.ID,
			ProviderKind:     "stub",
			ProviderIssueRef: "remote-3",
			Identifier:       "STUB-REFRESH",
			Title:            "Refreshed provider issue",
			State:            kanban.StateInReview,
		}, nil
	}
	refreshedByID, err := svc.RefreshIssueByID(context.Background(), refreshableIssue.ID)
	if err != nil {
		t.Fatalf("RefreshIssueByID success: %v", err)
	}
	if refreshedByID.Title != "Refreshed provider issue" || refreshedByID.State != kanban.StateInReview {
		t.Fatalf("expected refreshed issue to be updated from provider, got %#v", refreshedByID)
	}
	refreshedByIdentifier, err := svc.GetIssueByIdentifier(context.Background(), refreshableIssue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier refresh: %v", err)
	}
	if refreshedByIdentifier.Title != "Refreshed provider issue" {
		t.Fatalf("expected provider refresh via identifier lookup, got %#v", refreshedByIdentifier)
	}

	refreshIssue := &kanban.Issue{
		ProjectID:        stubProject.ID,
		ProviderKind:     "stub",
		ProviderIssueRef: "remote-2",
		Identifier:       "STUB-SHADOW",
		Title:            "Shadow issue",
		State:            kanban.StateReady,
		ProviderShadow:   true,
	}
	refreshIssue, err = store.UpsertProviderIssue(stubProject.ID, refreshIssue)
	if err != nil {
		t.Fatalf("UpsertProviderIssue shadow: %v", err)
	}
	stubProvider.getIssueFunc = func(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
		return nil, kanban.ErrNotFound
	}
	if _, err := svc.RefreshIssueByID(context.Background(), refreshIssue.ID); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing remote issue to be reported, got %v", err)
	}
	if _, err := store.GetIssue(refreshIssue.ID); !kanban.IsNotFound(err) {
		t.Fatalf("expected shadow issue to be removed after missing refresh, got %v", err)
	}
}

func TestServiceCreateAndUpdateProjectSuccess(t *testing.T) {
	store := newProvidersTestStore(t)
	svc := NewService(store)

	validateCalls := 0
	svc.RegisterProvider(&serviceProviderStub{
		kind: "stub",
		validateFunc: func(_ context.Context, project *kanban.Project) error {
			validateCalls++
			if project.ProviderKind != "stub" {
				t.Fatalf("expected provider kind stub, got %q", project.ProviderKind)
			}
			if project.ProviderProjectRef != "ref-1" && project.ProviderProjectRef != "ref-2" {
				t.Fatalf("expected provider ref ref-1 or ref-2, got %q", project.ProviderProjectRef)
			}
			return nil
		},
	})

	project, err := svc.CreateProject(context.Background(), "Project", "Description", filepath.Join(t.TempDir(), "repo"), filepath.Join(t.TempDir(), "repo", "WORKFLOW.md"), "stub", "ref-1", map[string]interface{}{"team": "alpha"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if validateCalls != 1 {
		t.Fatalf("expected validation during create, got %d", validateCalls)
	}
	if project.ProviderKind != "stub" || project.ProviderProjectRef != "ref-1" {
		t.Fatalf("unexpected created project: %#v", project)
	}
	if project.ProviderConfig["team"] != "alpha" {
		t.Fatalf("expected provider config to persist, got %#v", project.ProviderConfig)
	}

	if err := svc.UpdateProject(context.Background(), project.ID, "Project 2", "Description 2", filepath.Join(t.TempDir(), "repo-2"), filepath.Join(t.TempDir(), "repo-2", "WORKFLOW.md"), "stub", "ref-2", map[string]interface{}{"team": "beta"}); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if validateCalls != 2 {
		t.Fatalf("expected validation during update, got %d", validateCalls)
	}
	updated, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.Name != "Project 2" || updated.ProviderProjectRef != "ref-2" || updated.ProviderConfig["team"] != "beta" {
		t.Fatalf("unexpected updated project: %#v", updated)
	}
}

func TestServiceSyncForRepoPathBranches(t *testing.T) {
	store := newProvidersTestStore(t)
	svc := NewService(store)

	var goodProjectID string
	goodProvider := &serviceProviderStub{
		kind: "stub",
		listIssuesFunc: func(_ context.Context, project *kanban.Project, query kanban.IssueQuery) ([]kanban.Issue, error) {
			if project.ID != goodProjectID {
				return nil, errors.New("sync failed")
			}
			if query.Assignee != "me" {
				t.Fatalf("expected trimmed assignee filter, got %q", query.Assignee)
			}
			return []kanban.Issue{{
				ProjectID:        project.ID,
				ProviderKind:     "stub",
				ProviderIssueRef: "remote-1",
				Identifier:       "SYNC-1",
				Title:            "Synced issue",
				State:            kanban.StateReady,
			}}, nil
		},
	}
	svc.RegisterProvider(goodProvider)

	localRepo := filepath.Join(t.TempDir(), "local")
	goodRepo := filepath.Join(t.TempDir(), "good")
	badRepo := filepath.Join(t.TempDir(), "bad")

	if _, err := store.CreateProject("Local", "", localRepo, ""); err != nil {
		t.Fatalf("CreateProject local: %v", err)
	}
	goodProject, err := store.CreateProjectWithProvider("Good", "", goodRepo, "", "stub", "stub-ref", map[string]interface{}{"assignee": " me "})
	if err != nil {
		t.Fatalf("CreateProjectWithProvider good: %v", err)
	}
	goodProjectID = goodProject.ID
	badProject, err := store.CreateProjectWithProvider("Bad", "", badRepo, "", "stub", "stub-ref-2", map[string]interface{}{"assignee": "me"})
	if err != nil {
		t.Fatalf("CreateProjectWithProvider bad: %v", err)
	}

	if err := svc.SyncForRepoPath(context.Background(), ""); err == nil {
		t.Fatal("expected sync to report first provider error")
	}
	if err := svc.SyncForRepoPath(context.Background(), goodProject.RepoPath); err != nil {
		t.Fatalf("expected sync for single good project to succeed, got %v", err)
	}
	if err := svc.SyncForRepoPath(context.Background(), badProject.RepoPath); err == nil {
		t.Fatal("expected failing project sync to return an error")
	}

	issues, err := store.ListIssues(map[string]interface{}{"project_id": goodProject.ID})
	if err != nil {
		t.Fatalf("ListIssues good project: %v", err)
	}
	if len(issues) != 1 || issues[0].Identifier != "SYNC-1" {
		t.Fatalf("expected synced provider issue, got %#v", issues)
	}
}

func TestServiceMissingLookupBranches(t *testing.T) {
	store := newProvidersTestStore(t)
	svc := NewService(store)

	project, err := store.CreateProject("Lookup Project", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.CreateIssue(project.ID, "", "Lookup issue", "", 0, nil); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	assetPath := filepath.Join(t.TempDir(), "attachment.txt")
	if err := os.WriteFile(assetPath, []byte("attachment"), 0o644); err != nil {
		t.Fatalf("WriteFile asset: %v", err)
	}

	if _, err := svc.GetIssueDetailByIdentifier(context.Background(), "missing"); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue detail to fail with not found, got %v", err)
	}
	if _, err := svc.ListIssueAssets(context.Background(), "missing"); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue assets to fail with not found, got %v", err)
	}
	if _, err := svc.AttachIssueAsset(context.Background(), "missing", "missing.txt", strings.NewReader("data")); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue asset attachment to fail with not found, got %v", err)
	}
	if _, err := svc.AttachIssueAssetPath(context.Background(), "missing", assetPath); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue asset path attachment to fail with not found, got %v", err)
	}
	if _, _, err := svc.GetIssueAssetContent(context.Background(), "missing", "missing"); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue asset content to fail with not found, got %v", err)
	}
	if err := svc.DeleteIssueAsset(context.Background(), "missing", "missing"); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue asset delete to fail with not found, got %v", err)
	}
	if _, err := svc.GetIssueByIdentifier(context.Background(), "missing"); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue lookup to fail with not found, got %v", err)
	}
	if _, err := svc.UpdateIssue(context.Background(), "missing", map[string]interface{}{"title": "x"}); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue update to fail with not found, got %v", err)
	}
	if err := svc.DeleteIssue(context.Background(), "missing"); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue delete to fail with not found, got %v", err)
	}
	if _, err := svc.SetIssueState(context.Background(), "missing", string(kanban.StateDone)); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing state update to fail with not found, got %v", err)
	}
	if _, err := svc.ListIssueComments(context.Background(), "missing"); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue comments lookup to fail with not found, got %v", err)
	}
	if _, err := svc.CreateIssueCommentWithResult(context.Background(), "missing", IssueCommentInput{}); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue comment creation to fail with not found, got %v", err)
	}
	if _, err := svc.UpdateIssueComment(context.Background(), "missing", "missing", IssueCommentInput{}); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue comment update to fail with not found, got %v", err)
	}
	if err := svc.DeleteIssueComment(context.Background(), "missing", "missing"); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing issue comment delete to fail with not found, got %v", err)
	}
	if _, err := svc.GetIssueCommentAttachmentContent(context.Background(), "missing", "missing", "missing"); !kanban.IsNotFound(err) {
		t.Fatalf("expected missing attachment lookup to fail with not found, got %v", err)
	}
}

func TestServiceProviderErrorBranches(t *testing.T) {
	store := newProvidersTestStore(t)
	svc := NewService(store)

	providerCaps := kanban.ProviderCapabilities{
		Projects:         true,
		Epics:            true,
		Issues:           true,
		IssueStateUpdate: true,
		IssueDelete:      true,
	}
	svc.RegisterProvider(&serviceProviderStub{
		kind:         "stub",
		capabilities: &providerCaps,
		listIssuesFunc: func(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error) {
			return nil, errors.New("sync failed")
		},
		getIssueFunc: func(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
			return nil, errors.New("refresh failed")
		},
		deleteIssueFunc: func(context.Context, *kanban.Project, *kanban.Issue) error {
			return errors.New("delete failed")
		},
		setIssueStateFunc: func(context.Context, *kanban.Project, *kanban.Issue, string) (*kanban.Issue, error) {
			return nil, errors.New("state failed")
		},
		listIssueCommentsFunc: func(context.Context, *kanban.Project, *kanban.Issue) ([]kanban.IssueComment, error) {
			return nil, errors.New("list comments failed")
		},
		createIssueCommentFunc: func(context.Context, *kanban.Project, *kanban.Issue, IssueCommentInput) (*kanban.IssueComment, error) {
			return nil, errors.New("create comment failed")
		},
		updateIssueCommentFunc: func(context.Context, *kanban.Project, *kanban.Issue, string, IssueCommentInput) (*kanban.IssueComment, error) {
			return nil, errors.New("update comment failed")
		},
		deleteIssueCommentFunc: func(context.Context, *kanban.Project, *kanban.Issue, string) error {
			return errors.New("delete comment failed")
		},
		getIssueCommentAttachmentFunc: func(context.Context, *kanban.Project, *kanban.Issue, string, string) (*IssueCommentAttachmentContent, error) {
			return nil, errors.New("attachment failed")
		},
	})

	cachedProject, err := store.CreateProjectWithProvider("Cached Project", "", "", "", "stub", "cached-ref", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider cached: %v", err)
	}
	cachedIssue, err := store.UpsertProviderIssue(cachedProject.ID, &kanban.Issue{
		ProviderKind:     "stub",
		ProviderIssueRef: "remote-1",
		Identifier:       "STUB-1",
		Title:            "Cached issue",
		State:            kanban.StateBacklog,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue cached: %v", err)
	}

	syncProject, err := store.CreateProjectWithProvider("Sync Project", "", "", "", "stub", "sync-ref", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider sync: %v", err)
	}
	if _, err := svc.BoardOverview(context.Background(), syncProject.ID); err == nil {
		t.Fatal("expected board overview sync failure to be returned")
	}

	if got, err := svc.GetIssueByIdentifier(context.Background(), cachedIssue.Identifier); err != nil {
		t.Fatalf("expected cached issue lookup to fall back after refresh failure, got %v", err)
	} else if got.Title != cachedIssue.Title {
		t.Fatalf("expected cached issue to be returned, got %#v", got)
	}

	if _, err := svc.AttachIssueAssetPath(context.Background(), cachedIssue.Identifier, filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("expected missing attachment path to fail")
	}
	if err := svc.DeleteIssue(context.Background(), cachedIssue.Identifier); err == nil {
		t.Fatal("expected provider delete failure to be returned")
	}
	if _, err := svc.SetIssueState(context.Background(), cachedIssue.Identifier, string(kanban.StateDone)); err == nil {
		t.Fatal("expected provider state failure to be returned")
	}
	if _, err := svc.ListIssueComments(context.Background(), cachedIssue.Identifier); err == nil {
		t.Fatal("expected comment list failure to be returned")
	}
	body := "comment"
	if _, err := svc.CreateIssueCommentWithResult(context.Background(), cachedIssue.Identifier, IssueCommentInput{Body: &body}); err == nil {
		t.Fatal("expected comment creation failure to be returned")
	}
	if _, err := svc.UpdateIssueComment(context.Background(), cachedIssue.Identifier, "missing", IssueCommentInput{Body: &body}); err == nil {
		t.Fatal("expected comment update failure to be returned")
	}
	if err := svc.DeleteIssueComment(context.Background(), cachedIssue.Identifier, "missing"); err == nil {
		t.Fatal("expected comment delete failure to be returned")
	}
	if _, err := svc.GetIssueCommentAttachmentContent(context.Background(), cachedIssue.Identifier, "missing", "missing"); err == nil {
		t.Fatal("expected attachment lookup failure to be returned")
	}
}

func TestServiceLookupAndUpdateBranches(t *testing.T) {
	store := newProvidersTestStore(t)
	svc := NewService(store)
	var primaryLookupProjectID string

	providerCaps := kanban.ProviderCapabilities{
		Projects:         true,
		Epics:            true,
		Issues:           true,
		IssueStateUpdate: true,
		IssueDelete:      true,
	}
	svc.RegisterProvider(&serviceProviderStub{
		kind:         "stub",
		capabilities: &providerCaps,
		createIssueFunc: func(_ context.Context, project *kanban.Project, input IssueCreateInput) (*kanban.Issue, error) {
			switch input.Title {
			case "create-error":
				return nil, errors.New("create failed")
			case "create-bad-upsert":
				return &kanban.Issue{
					ProjectID:        project.ID,
					ProviderKind:     "stub",
					ProviderIssueRef: "",
					Identifier:       "UPD-ERR",
					Title:            input.Title,
					State:            kanban.StateReady,
				}, nil
			case "create-bad-local-update":
				return &kanban.Issue{
					ProjectID:        project.ID,
					ProviderKind:     "stub",
					ProviderIssueRef: "remote-create-local",
					Identifier:       "UPD-LOCAL",
					Title:            input.Title,
					State:            kanban.StateReady,
				}, nil
			}
			return &kanban.Issue{
				ProjectID:        project.ID,
				ProviderKind:     "stub",
				ProviderIssueRef: "remote-create",
				Identifier:       "UPD-1",
				Title:            input.Title,
				State:            kanban.StateReady,
			}, nil
		},
		listIssuesFunc: func(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error) {
			return nil, errors.New("sync failed")
		},
		getIssueFunc: func(_ context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
			switch identifier {
			case "UPD-1":
				return &kanban.Issue{
					ProjectID:        project.ID,
					ProviderKind:     "stub",
					ProviderIssueRef: "remote-create",
					Identifier:       "UPD-1",
					Title:            "Created issue",
					State:            kanban.StateReady,
				}, nil
			case "DUP-1":
				return &kanban.Issue{
					ProjectID:        project.ID,
					ProviderKind:     "stub",
					ProviderIssueRef: "remote-dup-" + project.ID,
					Identifier:       "DUP-1",
					Title:            "Duplicate issue",
					State:            kanban.StateReady,
				}, nil
			case "LOOKUP-1":
				if project.ID == primaryLookupProjectID {
					return &kanban.Issue{
						ProjectID:        project.ID,
						ProviderKind:     "stub",
						ProviderIssueRef: "remote-lookup",
						Identifier:       "LOOKUP-1",
						Title:            "Lookup issue",
						State:            kanban.StateReady,
					}, nil
				}
				return nil, kanban.ErrNotFound
			default:
				return nil, errors.New("refresh failed")
			}
		},
		updateIssueFunc: func(_ context.Context, project *kanban.Project, issue *kanban.Issue, updates map[string]interface{}) (*kanban.Issue, error) {
			if title, ok := updates["title"].(string); ok {
				switch title {
				case "update-error":
					return nil, errors.New("update failed")
				case "update-bad-upsert":
					return &kanban.Issue{
						ProjectID:        project.ID,
						ProviderKind:     "stub",
						ProviderIssueRef: "",
						Identifier:       issue.Identifier,
						Title:            title,
						State:            issue.State,
					}, nil
				case "update-bad-local":
					return &kanban.Issue{
						ProjectID:        project.ID,
						ProviderKind:     "stub",
						ProviderIssueRef: "remote-update-local",
						Identifier:       issue.Identifier,
						Title:            title,
						State:            issue.State,
					}, nil
				}
			}
			out := *issue
			out.ProjectID = project.ID
			if title, ok := updates["title"].(string); ok {
				out.Title = title
			}
			return &out, nil
		},
	})

	projectA, err := store.CreateProjectWithProvider("Project A", "", "", "", "stub", "stub-a", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider A: %v", err)
	}
	projectB, err := store.CreateProjectWithProvider("Project B", "", "", "", "stub", "stub-b", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider B: %v", err)
	}
	primaryLookupProjectID = projectA.ID
	localA, err := store.CreateProject("Local A", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject local A: %v", err)
	}
	localB, err := store.CreateProject("Local B", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject local B: %v", err)
	}

	created, err := svc.CreateIssue(context.Background(), IssueCreateInput{
		ProjectID: projectA.ID,
		Title:     "Created issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue provider-backed: %v", err)
	}
	if created.Identifier != "UPD-1" {
		t.Fatalf("expected created issue identifier, got %#v", created)
	}

	if _, err := svc.GetIssueByIdentifier(context.Background(), "DUP-1"); !errors.Is(err, ErrAmbiguousProviderIssue) {
		t.Fatalf("expected ambiguous provider lookup to fail, got %v", err)
	}
	lookedUp, err := svc.GetIssueByIdentifier(context.Background(), "LOOKUP-1")
	if err != nil {
		t.Fatalf("expected provider lookup refresh to succeed, got %v", err)
	}
	if lookedUp.Identifier != "LOOKUP-1" || lookedUp.ProjectID != projectA.ID {
		t.Fatalf("expected provider lookup to resolve project A issue, got %#v", lookedUp)
	}

	if _, err := svc.UpdateIssue(context.Background(), created.Identifier, map[string]interface{}{"issue_type": "bogus"}); err == nil {
		t.Fatal("expected invalid issue type to fail")
	}
	if _, err := svc.UpdateIssue(context.Background(), created.Identifier, map[string]interface{}{"enabled": true}); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("expected enabled update to be rejected, got %v", err)
	}
	brokenProject, err := store.CreateProjectWithProvider("Broken Project", "", "", "", "missing", "broken", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider broken: %v", err)
	}
	if _, err := svc.CreateIssue(context.Background(), IssueCreateInput{
		ProjectID: brokenProject.ID,
		Title:     "broken create",
	}); err == nil {
		t.Fatal("expected create to reject unknown provider kinds")
	}
	if _, err := svc.CreateIssue(context.Background(), IssueCreateInput{
		ProjectID: projectA.ID,
		Title:     "create-error",
	}); err == nil {
		t.Fatal("expected provider create failure to be returned")
	}
	if _, err := svc.CreateIssue(context.Background(), IssueCreateInput{
		ProjectID: projectA.ID,
		Title:     "create-bad-upsert",
	}); err == nil {
		t.Fatal("expected provider create upsert failure to be returned")
	}
	if _, err := svc.CreateIssue(context.Background(), IssueCreateInput{
		ProjectID: projectA.ID,
		EpicID:    "missing-epic",
		Title:     "create-bad-local-update",
	}); err == nil {
		t.Fatal("expected provider create local update failure to be returned")
	}
	if _, err := svc.UpdateIssue(context.Background(), created.Identifier, map[string]interface{}{"title": "update-error"}); err == nil {
		t.Fatal("expected provider update failure to be returned")
	}
	if _, err := svc.UpdateIssue(context.Background(), created.Identifier, map[string]interface{}{"title": "update-bad-upsert"}); err == nil {
		t.Fatal("expected provider update upsert failure to be returned")
	}
	if _, err := svc.UpdateIssue(context.Background(), created.Identifier, map[string]interface{}{"title": "update-bad-local", "epic_id": "missing-epic"}); err == nil {
		t.Fatal("expected provider local update failure to be returned")
	}

	localIssue, err := store.CreateIssue(localA.ID, "", "Local issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue local: %v", err)
	}
	moved, err := svc.UpdateIssue(context.Background(), localIssue.Identifier, map[string]interface{}{
		"project_id": localB.ID,
		"title":      "Moved local issue",
	})
	if err != nil {
		t.Fatalf("UpdateIssue local move: %v", err)
	}
	if moved.ProjectID != localB.ID || moved.Title != "Moved local issue" {
		t.Fatalf("expected local issue move to be persisted, got %#v", moved)
	}
	if _, err := svc.UpdateIssue(context.Background(), localIssue.Identifier, map[string]interface{}{
		"project_id": "missing-project",
		"title":      "Broken local update",
	}); err == nil {
		t.Fatal("expected local provider update failure to be returned")
	}

	if _, err := svc.BoardOverview(context.Background(), projectB.ID); err == nil {
		t.Fatal("expected board overview without cache to report provider sync failure")
	}
}

func TestServiceLocalKanbanBranchCoverage(t *testing.T) {
	store := newProvidersTestStore(t)
	svc := NewService(store)

	project, err := store.CreateProject("Local Project", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := svc.CreateEpic(project.ID, "Local Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic local: %v", err)
	}

	issueDetail, err := svc.CreateIssue(context.Background(), IssueCreateInput{
		ProjectID:   project.ID,
		EpicID:      epic.ID,
		Title:       "Recurring local issue",
		Description: "Recurring issue body",
		IssueType:   kanban.IssueTypeRecurring,
		Cron:        "0 0 * * *",
		Enabled:     boolPtr(true),
		State:       string(kanban.StateReady),
		Labels:      []string{"local"},
		AgentName:   "local-agent",
		BranchName:  "local/branch",
	})
	if err != nil {
		t.Fatalf("CreateIssue local recurring: %v", err)
	}
	issue, err := svc.GetIssueByIdentifier(context.Background(), issueDetail.Identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier local: %v", err)
	}
	if !issue.IsRecurring() || issue.EpicID != epic.ID || issue.State != kanban.StateReady {
		t.Fatalf("expected recurring local issue, got %#v", issue)
	}

	board, err := svc.BoardOverview(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("BoardOverview local: %v", err)
	}
	if len(board) != 0 {
		t.Fatalf("expected board overview to exclude recurring issue, got %#v", board)
	}

	refreshed, err := svc.RefreshIssue(context.Background(), issue)
	if err != nil {
		t.Fatalf("RefreshIssue local: %v", err)
	}
	if refreshed.Identifier != issue.Identifier || refreshed.Title != issue.Title {
		t.Fatalf("expected local refresh to return same issue, got %#v", refreshed)
	}

	updated, err := svc.UpdateIssue(context.Background(), issue.Identifier, map[string]interface{}{
		"title":       "Recurring local issue updated",
		"description": "Updated body",
		"branch_name": "local/branch-2",
	})
	if err != nil {
		t.Fatalf("UpdateIssue local: %v", err)
	}
	if updated.Title != "Recurring local issue updated" || updated.BranchName != "local/branch-2" {
		t.Fatalf("unexpected local issue update: %#v", updated)
	}

	stateUpdated, err := svc.SetIssueState(context.Background(), issue.Identifier, string(kanban.StateDone))
	if err != nil {
		t.Fatalf("SetIssueState local: %v", err)
	}
	if stateUpdated.State != kanban.StateDone {
		t.Fatalf("expected local issue state update, got %#v", stateUpdated)
	}

	body := "Local comment"
	commentAttachment := writeProviderCommentAttachment(t, "comment.txt", []byte("local attachment"))
	comment, err := svc.CreateIssueCommentWithResult(context.Background(), issue.Identifier, IssueCommentInput{
		Body: &body,
		Attachments: []IssueCommentAttachment{{
			Path:        commentAttachment,
			ContentType: "text/plain",
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueCommentWithResult local: %v", err)
	}
	if err := svc.CreateIssueComment(context.Background(), issue.Identifier, IssueCommentInput{Body: &body}); err != nil {
		t.Fatalf("CreateIssueComment local wrapper: %v", err)
	}

	comments, err := svc.ListIssueComments(context.Background(), issue.Identifier)
	if err != nil {
		t.Fatalf("ListIssueComments local: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected one local comment, got %#v", comments)
	}
	foundOriginal := false
	for _, candidate := range comments {
		if candidate.ID == comment.ID {
			foundOriginal = true
			break
		}
	}
	if !foundOriginal {
		t.Fatalf("expected comment list to include %q, got %#v", comment.ID, comments)
	}

	attachmentContent, err := svc.GetIssueCommentAttachmentContent(context.Background(), issue.Identifier, comment.ID, comment.Attachments[0].ID)
	if err != nil {
		t.Fatalf("GetIssueCommentAttachmentContent local: %v", err)
	}
	defer attachmentContent.Content.Close()
	data, err := io.ReadAll(attachmentContent.Content)
	if err != nil {
		t.Fatalf("Read attachment content local: %v", err)
	}
	if string(data) != "local attachment" {
		t.Fatalf("unexpected local attachment content: %q", string(data))
	}

	updatedComment, err := svc.UpdateIssueComment(context.Background(), issue.Identifier, comment.ID, IssueCommentInput{
		Body:                &body,
		RemoveAttachmentIDs: []string{comment.Attachments[0].ID},
	})
	if err != nil {
		t.Fatalf("UpdateIssueComment local: %v", err)
	}
	if updatedComment.ID != comment.ID || updatedComment.Body != body {
		t.Fatalf("unexpected updated local comment: %#v", updatedComment)
	}

	if err := svc.DeleteIssueComment(context.Background(), issue.Identifier, comment.ID); err != nil {
		t.Fatalf("DeleteIssueComment local: %v", err)
	}

	assetDir := t.TempDir()
	assetPath := filepath.Join(assetDir, "preview.txt")
	if err := os.WriteFile(assetPath, []byte("asset"), 0o644); err != nil {
		t.Fatalf("WriteFile asset: %v", err)
	}
	asset, err := svc.AttachIssueAssetPath(context.Background(), issue.Identifier, assetPath)
	if err != nil {
		t.Fatalf("AttachIssueAssetPath local: %v", err)
	}
	assets, err := svc.ListIssueAssets(context.Background(), issue.Identifier)
	if err != nil {
		t.Fatalf("ListIssueAssets local: %v", err)
	}
	if len(assets) != 1 || assets[0].ID != asset.ID {
		t.Fatalf("unexpected local assets: %#v", assets)
	}
	contentAsset, contentPath, err := svc.GetIssueAssetContent(context.Background(), issue.Identifier, asset.ID)
	if err != nil {
		t.Fatalf("GetIssueAssetContent local: %v", err)
	}
	if contentAsset.ID != asset.ID || !strings.HasSuffix(contentPath, asset.ID+".txt") {
		t.Fatalf("unexpected asset content lookup: asset=%#v path=%q", contentAsset, contentPath)
	}
	if err := svc.DeleteIssueAsset(context.Background(), issue.Identifier, asset.ID); err != nil {
		t.Fatalf("DeleteIssueAsset local: %v", err)
	}

	if err := svc.DeleteIssue(context.Background(), issue.Identifier); err != nil {
		t.Fatalf("DeleteIssue local: %v", err)
	}
	if _, err := svc.GetIssueByIdentifier(context.Background(), issue.Identifier); !kanban.IsNotFound(err) {
		t.Fatalf("expected deleted local issue to be missing, got %v", err)
	}
}

func TestKanbanProviderDelegatesToStore(t *testing.T) {
	store := newProvidersTestStore(t)
	provider := NewKanbanProvider(store)
	if got := provider.Capabilities(); !got.Epics || !got.Issues || !got.IssueDelete || !got.IssueStateUpdate {
		t.Fatalf("unexpected kanban provider capabilities: %#v", got)
	}
	if err := provider.ValidateProject(context.Background(), nil); err != nil {
		t.Fatalf("ValidateProject: %v", err)
	}

	project, err := store.CreateProject("Kanban Project", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := provider.CreateIssue(context.Background(), project, IssueCreateInput{
		Title:       "Provider issue",
		Description: "Body",
		Labels:      []string{"alpha", "beta"},
		Priority:    2,
		State:       string(kanban.StateReady),
		AgentName:   "agent",
		AgentPrompt: "prompt",
		BranchName:  "branch",
		PRURL:       "https://example.com/pr/1",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issue.State != kanban.StateReady || issue.AgentName != "agent" || issue.BranchName != "branch" {
		t.Fatalf("expected create issue to persist local metadata, got %#v", issue)
	}

	issues, err := provider.ListIssues(context.Background(), project, kanban.IssueQuery{ProjectID: project.ID, State: string(kanban.StateReady)})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Identifier != issue.Identifier {
		t.Fatalf("unexpected issues: %#v", issues)
	}

	fetched, err := provider.GetIssue(context.Background(), project, issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if fetched.Identifier != issue.Identifier {
		t.Fatalf("unexpected fetched issue: %#v", fetched)
	}

	updated, err := provider.UpdateIssue(context.Background(), project, issue, map[string]interface{}{"title": "Renamed"})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updated.Title != "Renamed" {
		t.Fatalf("expected update to persist title, got %#v", updated)
	}

	stateUpdated, err := provider.SetIssueState(context.Background(), project, issue, string(kanban.StateDone))
	if err != nil {
		t.Fatalf("SetIssueState: %v", err)
	}
	if stateUpdated.State != kanban.StateDone {
		t.Fatalf("expected state update, got %#v", stateUpdated)
	}

	body := "hello"
	comment, err := provider.CreateIssueComment(context.Background(), project, issue, IssueCommentInput{
		Body: &body,
		Attachments: []IssueCommentAttachment{{
			Path:        writeProviderCommentAttachment(t, "attachment.txt", []byte("provider attachment")),
			ContentType: "text/plain",
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}
	if comment.Body != body {
		t.Fatalf("unexpected comment body: %#v", comment)
	}

	comments, err := provider.ListIssueComments(context.Background(), project, issue)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(comments) != 1 || comments[0].ID != comment.ID {
		t.Fatalf("unexpected comment list: %#v", comments)
	}

	updatedComment, err := provider.UpdateIssueComment(context.Background(), project, issue, comment.ID, IssueCommentInput{Body: &body})
	if err != nil {
		t.Fatalf("UpdateIssueComment: %v", err)
	}
	if updatedComment.ID != comment.ID {
		t.Fatalf("unexpected updated comment: %#v", updatedComment)
	}

	attachment, err := provider.GetIssueCommentAttachmentContent(context.Background(), project, issue, comment.ID, comment.Attachments[0].ID)
	if err != nil {
		t.Fatalf("GetIssueCommentAttachmentContent: %v", err)
	}
	defer attachment.Content.Close()
	content, err := io.ReadAll(attachment.Content)
	if err != nil {
		t.Fatalf("Read attachment content: %v", err)
	}
	if string(content) != "provider attachment" {
		t.Fatalf("unexpected attachment content: %q", string(content))
	}

	if err := provider.DeleteIssueComment(context.Background(), project, issue, comment.ID); err != nil {
		t.Fatalf("DeleteIssueComment: %v", err)
	}
	if err := provider.DeleteIssue(context.Background(), project, issue); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if _, err := provider.GetIssue(context.Background(), project, issue.Identifier); !kanban.IsNotFound(err) {
		t.Fatalf("expected deleted issue to be missing, got %v", err)
	}
}

func writeProviderCommentAttachment(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile provider attachment: %v", err)
	}
	return path
}

func boolPtr(v bool) *bool {
	return &v
}
