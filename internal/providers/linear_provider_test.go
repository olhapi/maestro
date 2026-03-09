package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olhapi/maestro/internal/kanban"
)

func TestLinearProviderNormalizeIssueFiltersNonBlockingRelations(t *testing.T) {
	provider := NewLinearProvider()

	issue := provider.normalizeIssue(map[string]interface{}{
		"id":         "linear-1",
		"identifier": "LIN-1",
		"title":      "Linear issue",
		"state": map[string]interface{}{
			"name": "Todo",
		},
		"inverseRelations": map[string]interface{}{
			"nodes": []interface{}{
				map[string]interface{}{
					"type": "blocks",
					"issue": map[string]interface{}{
						"identifier": "LIN-2",
					},
				},
				map[string]interface{}{
					"type": "related",
					"issue": map[string]interface{}{
						"identifier": "LIN-3",
					},
				},
				map[string]interface{}{
					"issue": map[string]interface{}{
						"identifier": "LIN-4",
					},
				},
			},
		},
	})

	if issue.ProviderKind != kanban.ProviderKindLinear {
		t.Fatalf("expected linear provider kind, got %q", issue.ProviderKind)
	}
	if len(issue.BlockedBy) != 1 || issue.BlockedBy[0] != "LIN-2" {
		t.Fatalf("expected only blocking inverse relation to be preserved, got %#v", issue.BlockedBy)
	}
}

func TestLinearProviderListIssuesFiltersByAssignee(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		query, _ := body["query"].(string)
		switch {
		case query == "":
			t.Fatal("expected graphql query")
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"issues": map[string]interface{}{
						"nodes": []map[string]interface{}{
							{
								"id":         "linear-1",
								"identifier": "LIN-1",
								"title":      "Assigned to worker",
								"assignee":   map[string]interface{}{"id": "worker-1"},
								"state":      map[string]interface{}{"name": "ready"},
								"labels":     map[string]interface{}{"nodes": []interface{}{}},
								"inverseRelations": map[string]interface{}{
									"nodes": []interface{}{},
								},
							},
							{
								"id":         "linear-2",
								"identifier": "LIN-2",
								"title":      "Assigned elsewhere",
								"assignee":   map[string]interface{}{"id": "worker-2"},
								"state":      map[string]interface{}{"name": "ready"},
								"labels":     map[string]interface{}{"nodes": []interface{}{}},
								"inverseRelations": map[string]interface{}{
									"nodes": []interface{}{},
								},
							},
							{
								"id":         "linear-3",
								"identifier": "LIN-3",
								"title":      "Unassigned",
								"state":      map[string]interface{}{"name": "ready"},
								"labels":     map[string]interface{}{"nodes": []interface{}{}},
								"inverseRelations": map[string]interface{}{
									"nodes": []interface{}{},
								},
							},
						},
						"pageInfo": map[string]interface{}{
							"hasNextPage": false,
							"endCursor":   "",
						},
					},
				},
			})
		}
	}))
	defer server.Close()

	t.Setenv("LINEAR_API_KEY", "test-token")

	provider := NewLinearProvider()
	provider.http = server.Client()

	project := &kanban.Project{
		ProviderProjectRef: "proj-slug",
		ProviderConfig: map[string]interface{}{
			"endpoint": server.URL,
		},
	}

	issues, err := provider.ListIssues(context.Background(), project, kanban.IssueQuery{Assignee: "worker-1"})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Identifier != "LIN-1" {
		t.Fatalf("expected only matching assignee issue, got %#v", issues)
	}
}

func TestLinearProviderResolveAssigneeMatcherForViewer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"viewer": map[string]interface{}{
					"id": "viewer-123",
				},
			},
		})
	}))
	defer server.Close()

	t.Setenv("LINEAR_API_KEY", "test-token")

	provider := NewLinearProvider()
	provider.http = server.Client()
	project := &kanban.Project{
		ProviderConfig: map[string]interface{}{
			"endpoint": server.URL,
		},
	}

	matcher, err := provider.resolveAssigneeMatcher(context.Background(), project, "me")
	if err != nil {
		t.Fatalf("resolveAssigneeMatcher: %v", err)
	}
	if matcher == nil {
		t.Fatal("expected matcher")
	}
	if _, ok := matcher.matchValues["viewer-123"]; !ok {
		t.Fatalf("expected viewer id match set, got %#v", matcher.matchValues)
	}
}
