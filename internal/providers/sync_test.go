package providers

import (
	"context"
	"testing"

	"github.com/olhapi/maestro/internal/kanban"
)

func TestServiceSyncLoopBranches(t *testing.T) {
	store := newProvidersTestStore(t)
	svc := NewService(store)

	var listCalls int
	svc.RegisterProvider(&serviceProviderStub{
		kind: "stub",
		listIssuesFunc: func(_ context.Context, project *kanban.Project, _ kanban.IssueQuery) ([]kanban.Issue, error) {
			listCalls++
			return []kanban.Issue{{
				ProjectID:        project.ID,
				ProviderKind:     "stub",
				ProviderIssueRef: "remote-1",
				Identifier:       "STUB-1",
				Title:            "Synced issue",
				State:            kanban.StateReady,
			}}, nil
		},
	})

	stubProject, err := store.CreateProjectWithProvider("Stub Project", "", "", "", "stub", "stub-ref", map[string]interface{}{"assignee": "me"})
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	if _, err := store.CreateProject("Kanban Project", "", "", ""); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := svc.syncIssueListIfNeeded(context.Background(), kanban.IssueQuery{ProjectID: stubProject.ID}); err != nil {
		t.Fatalf("syncIssueListIfNeeded first call: %v", err)
	}
	if err := svc.syncIssueListIfNeeded(context.Background(), kanban.IssueQuery{ProjectID: stubProject.ID}); err != nil {
		t.Fatalf("syncIssueListIfNeeded second call: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("expected list sync to run once, got %d", listCalls)
	}

	listCalls = 0
	if err := svc.syncIssues(context.Background(), kanban.IssueQuery{}); err != nil {
		t.Fatalf("syncIssues full sync: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("expected syncIssues to call provider once, got %d", listCalls)
	}

	if err := svc.syncIssues(context.Background(), kanban.IssueQuery{ProjectID: "missing"}); err != nil {
		t.Fatalf("syncIssues missing project filter: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("expected filtered sync to skip provider calls, got %d", listCalls)
	}
}
