package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestLinearProviderListIssuesPushesAssigneeAndStateFiltersToGraphQL(t *testing.T) {
	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"issues": map[string]interface{}{
					"nodes": []interface{}{},
					"pageInfo": map[string]interface{}{
						"hasNextPage": false,
						"endCursor":   "",
					},
				},
			},
		})
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

	if _, err := provider.ListIssues(context.Background(), project, kanban.IssueQuery{
		Assignee: "worker-1",
		State:    "ready",
	}); err != nil {
		t.Fatalf("ListIssues: %v", err)
	}

	query, _ := requestBody["query"].(string)
	if !strings.Contains(query, "assignee: {id: {eq: $assigneeID}}") {
		t.Fatalf("expected assignee filter in query, got %q", query)
	}
	if !strings.Contains(query, "state: {name: {eq: $stateName}}") {
		t.Fatalf("expected state filter in query, got %q", query)
	}
	variables := requestBody["variables"].(map[string]interface{})
	if variables["assigneeID"] != "worker-1" {
		t.Fatalf("expected assigneeID variable, got %#v", variables)
	}
	if variables["stateName"] != "ready" {
		t.Fatalf("expected stateName variable, got %#v", variables)
	}
}

func TestLinearProviderCreateIssueCommentCreatesPlainComment(t *testing.T) {
	var mutationBody map[string]interface{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&mutationBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"commentCreate": map[string]interface{}{
					"success": true,
				},
			},
		})
	}))
	defer server.Close()

	t.Setenv("LINEAR_API_KEY", "test-token")

	provider := NewLinearProvider()
	provider.http = server.Client()
	project := &kanban.Project{ProviderConfig: map[string]interface{}{"endpoint": server.URL}}
	issue := &kanban.Issue{ProviderIssueRef: "linear-issue-1"}

	if err := provider.CreateIssueComment(context.Background(), project, issue, IssueCommentInput{Body: "Review preview is ready."}); err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}

	query, _ := mutationBody["query"].(string)
	if query == "" || mutationBody["variables"] == nil {
		t.Fatalf("expected graphql mutation payload, got %#v", mutationBody)
	}
	variables := mutationBody["variables"].(map[string]interface{})
	if variables["issueId"] != "linear-issue-1" || variables["body"] != "Review preview is ready." {
		t.Fatalf("unexpected comment mutation variables: %#v", variables)
	}
}

func TestLinearProviderCreateIssueCommentUploadsAttachmentsAndAppendsLinks(t *testing.T) {
	tempDir := t.TempDir()
	attachmentPath := filepath.Join(tempDir, "preview.mp4")
	if err := os.WriteFile(attachmentPath, []byte("preview-bytes"), 0o644); err != nil {
		t.Fatalf("write attachment: %v", err)
	}

	type graphqlRequest struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}

	var requests []graphqlRequest
	var uploadedContentType string
	var uploadedBody string

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload":
			uploadedContentType = r.Header.Get("Content-Type")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read upload body: %v", err)
			}
			uploadedBody = string(data)
			w.WriteHeader(http.StatusOK)
		default:
			var body graphqlRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			requests = append(requests, body)
			switch {
			case strings.Contains(body.Query, "fileUpload"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"fileUpload": map[string]interface{}{
							"success": true,
							"uploadFile": map[string]interface{}{
								"uploadUrl": server.URL + "/upload",
								"assetUrl":  "https://linear.example/assets/preview.mp4",
								"headers": []map[string]interface{}{
									{"key": "x-amz-acl", "value": "private"},
								},
							},
						},
					},
				})
			case strings.Contains(body.Query, "commentCreate"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"commentCreate": map[string]interface{}{
							"success": true,
						},
					},
				})
			default:
				t.Fatalf("unexpected graphql query: %s", body.Query)
			}
		}
	}))
	defer server.Close()

	t.Setenv("LINEAR_API_KEY", "test-token")

	provider := NewLinearProvider()
	provider.http = server.Client()
	project := &kanban.Project{ProviderConfig: map[string]interface{}{"endpoint": server.URL}}
	issue := &kanban.Issue{ProviderIssueRef: "linear-issue-2"}

	err := provider.CreateIssueComment(context.Background(), project, issue, IssueCommentInput{
		Body: "Attached reviewer preview.",
		Attachments: []IssueCommentAttachment{
			{Path: attachmentPath, ContentType: "video/mp4"},
		},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("expected fileUpload and commentCreate requests, got %d", len(requests))
	}
	if uploadedContentType != "video/mp4" {
		t.Fatalf("expected uploaded content type video/mp4, got %q", uploadedContentType)
	}
	if uploadedBody != "preview-bytes" {
		t.Fatalf("unexpected uploaded body %q", uploadedBody)
	}
	commentVars := requests[1].Variables
	body, _ := commentVars["body"].(string)
	if !strings.Contains(body, "Attached reviewer preview.") || !strings.Contains(body, "https://linear.example/assets/preview.mp4") {
		t.Fatalf("expected comment body to include uploaded asset link, got %q", body)
	}
}
