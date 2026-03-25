package providers

import (
	"context"
	"encoding/json"
	"errors"
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

func TestLinearProviderNormalizeIssueMapsWorkflowStateTypes(t *testing.T) {
	provider := NewLinearProvider()

	cases := []struct {
		name      string
		stateType string
		want      kanban.State
	}{
		{name: "backlog", stateType: "backlog", want: kanban.StateBacklog},
		{name: "unstarted", stateType: "unstarted", want: kanban.StateReady},
		{name: "started", stateType: "started", want: kanban.StateInProgress},
		{name: "completed", stateType: "completed", want: kanban.StateDone},
		{name: "canceled", stateType: "canceled", want: kanban.StateCancelled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issue := provider.normalizeIssue(map[string]interface{}{
				"id":         "linear-1",
				"identifier": "LIN-1",
				"title":      "Linear issue",
				"state": map[string]interface{}{
					"name": "ignored",
					"type": tc.stateType,
				},
			})
			if issue.State != tc.want {
				t.Fatalf("expected %s to normalize to %s, got %s", tc.stateType, tc.want, issue.State)
			}
		})
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
					"nodes": []interface{}{
						map[string]interface{}{
							"id":         "linear-1",
							"identifier": "LIN-1",
							"title":      "Assigned to worker",
							"assignee":   map[string]interface{}{"id": "worker-1"},
							"state":      map[string]interface{}{"name": "Todo", "type": "unstarted"},
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

	issues, err := provider.ListIssues(context.Background(), project, kanban.IssueQuery{
		Assignee: "worker-1",
		State:    string(kanban.StateReady),
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].State != kanban.StateReady {
		t.Fatalf("expected ready issue result, got %#v", issues)
	}

	query, _ := requestBody["query"].(string)
	if !strings.Contains(query, "assignee: {id: {eq: $assigneeID}}") {
		t.Fatalf("expected assignee filter in query, got %q", query)
	}
	if !strings.Contains(query, "state: {type: {eq: $stateType}}") {
		t.Fatalf("expected state filter in query, got %q", query)
	}
	variables := requestBody["variables"].(map[string]interface{})
	if variables["assigneeID"] != "worker-1" {
		t.Fatalf("expected assigneeID variable, got %#v", variables)
	}
	if variables["stateType"] != "unstarted" {
		t.Fatalf("expected stateType variable, got %#v", variables)
	}
}

func TestLinearProviderListIssuesPreservesCustomStateNames(t *testing.T) {
	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"issues": map[string]interface{}{
					"nodes": []interface{}{
						map[string]interface{}{
							"id":         "linear-1",
							"identifier": "LIN-1",
							"title":      "Custom workflow issue",
							"state": map[string]interface{}{
								"name": "QA Ready",
								"type": "unstarted",
							},
							"labels": map[string]interface{}{"nodes": []interface{}{}},
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

	issues, err := provider.ListIssues(context.Background(), project, kanban.IssueQuery{State: "QA Ready"})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Identifier != "LIN-1" {
		t.Fatalf("expected custom state issue to remain visible, got %#v", issues)
	}
	if issues[0].State != kanban.StateReady {
		t.Fatalf("expected custom workflow state to normalize to ready, got %q", issues[0].State)
	}

	query, _ := requestBody["query"].(string)
	if !strings.Contains(query, "state: {name: {eq: $stateName}}") {
		t.Fatalf("expected state-name filter in query, got %q", query)
	}
	variables := requestBody["variables"].(map[string]interface{})
	if variables["stateName"] != "QA Ready" {
		t.Fatalf("expected custom stateName variable, got %#v", variables)
	}
}

func TestLinearProviderSetIssueStateUsesWorkflowStateTypeAndLowestPosition(t *testing.T) {
	var requests []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, body)
		query, _ := body["query"].(string)
		switch {
		case strings.Contains(query, "MaestroLinearState"):
			variables := body["variables"].(map[string]interface{})
			if variables["stateType"] != "started" {
				t.Fatalf("expected started workflow type, got %#v", variables)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"issue": map[string]interface{}{
						"team": map[string]interface{}{
							"states": map[string]interface{}{
								"nodes": []interface{}{
									map[string]interface{}{"id": "state-high", "position": 20},
									map[string]interface{}{"id": "state-low", "position": 10},
								},
							},
						},
					},
				},
			})
		case strings.Contains(query, "MaestroLinearUpdateIssueState"):
			variables := body["variables"].(map[string]interface{})
			if variables["stateId"] != "state-low" {
				t.Fatalf("expected lowest-position state id, got %#v", variables)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"issueUpdate": map[string]interface{}{
						"success": true,
					},
				},
			})
		case strings.Contains(query, "MaestroLinearIssue"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"issues": map[string]interface{}{
						"nodes": []interface{}{
							map[string]interface{}{
								"id":         "linear-1",
								"identifier": "LIN-1",
								"title":      "Updated issue",
								"state":      map[string]interface{}{"name": "In Progress", "type": "started"},
								"labels":     map[string]interface{}{"nodes": []interface{}{}},
								"inverseRelations": map[string]interface{}{
									"nodes": []interface{}{},
								},
							},
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected graphql query: %s", query)
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
	issue := &kanban.Issue{
		ProviderIssueRef: "linear-issue-1",
		Identifier:       "LIN-1",
	}

	updated, err := provider.SetIssueState(context.Background(), project, issue, string(kanban.StateInReview))
	if err != nil {
		t.Fatalf("SetIssueState: %v", err)
	}
	if updated.State != kanban.StateInProgress {
		t.Fatalf("expected started state to normalize to in_progress, got %s", updated.State)
	}
	if len(requests) != 3 {
		t.Fatalf("expected state lookup, mutation, and refresh requests, got %d", len(requests))
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
	body := "Review preview is ready."

	if _, err := provider.CreateIssueComment(context.Background(), project, issue, IssueCommentInput{Body: &body}); err != nil {
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

func TestLinearProviderCreateIssueCommentRejectsEmptyInput(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "test-token")

	provider := NewLinearProvider()
	project := &kanban.Project{}
	issue := &kanban.Issue{ProviderIssueRef: "linear-issue-1"}

	comment, err := provider.CreateIssueComment(context.Background(), project, issue, IssueCommentInput{})
	if !errors.Is(err, kanban.ErrValidation) {
		t.Fatalf("expected validation error, got comment=%#v err=%v", comment, err)
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
	body := "Attached reviewer preview."

	_, err := provider.CreateIssueComment(context.Background(), project, issue, IssueCommentInput{
		Body: &body,
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
	renderedBody, _ := commentVars["body"].(string)
	if !strings.Contains(renderedBody, "Attached reviewer preview.") || !strings.Contains(renderedBody, "https://linear.example/assets/preview.mp4") {
		t.Fatalf("expected comment body to include uploaded asset link, got %q", renderedBody)
	}
	plainBody, attachments := parseLinearCommentBody(renderedBody)
	if plainBody != "Attached reviewer preview." {
		t.Fatalf("expected plain body to round-trip, got %q", plainBody)
	}
	if len(attachments) != 1 || attachments[0].ByteSize != int64(len("preview-bytes")) {
		t.Fatalf("expected attachment byte size to round-trip, got %#v", attachments)
	}
}

func TestExtractLinearMarkdownAttachmentsUsesStableIDs(t *testing.T) {
	body := strings.Join([]string{
		"Legacy preview artifacts:",
		"- [screenshot.png](https://linear.example/assets/one)",
		"- [screenshot.png](https://linear.example/assets/two)",
	}, "\n")

	first := extractLinearMarkdownAttachments(body)
	second := extractLinearMarkdownAttachments(body)

	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected two attachments, got %#v and %#v", first, second)
	}
	if first[0].ID != second[0].ID || first[1].ID != second[1].ID {
		t.Fatalf("expected stable attachment ids, got %#v and %#v", first, second)
	}
	if first[0].ID == first[1].ID {
		t.Fatalf("expected unique attachment ids, got %#v", first)
	}
}

func TestParseLinearCommentBodyIgnoresUnmanagedMarkdownLinks(t *testing.T) {
	body := strings.Join([]string{
		"Deployment notes:",
		"- [runbook](https://example.com/runbook)",
	}, "\n")

	plain, attachments := parseLinearCommentBody(body)

	if plain != body {
		t.Fatalf("expected unmanaged body to remain unchanged, got %q", plain)
	}
	if len(attachments) != 0 {
		t.Fatalf("expected unmanaged markdown links to stay in the body, got %#v", attachments)
	}
}

func TestParseLinearCommentBodyRecognizesLegacyMaestroAttachments(t *testing.T) {
	body := strings.Join([]string{
		"Preview is ready.",
		"",
		"Reviewer preview artifacts:",
		"- [preview.mp4](https://linear.example/assets/preview.mp4)",
	}, "\n")

	plain, attachments := parseLinearCommentBody(body)

	if plain != "Preview is ready." {
		t.Fatalf("expected legacy attachment section to be stripped from the body, got %q", plain)
	}
	if len(attachments) != 1 || attachments[0].Filename != "preview.mp4" {
		t.Fatalf("expected one parsed legacy attachment, got %#v", attachments)
	}
}

func TestLinearProviderListIssueCommentsPaginates(t *testing.T) {
	var afterValues []interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		query, _ := body["query"].(string)
		if !strings.Contains(query, "MaestroLinearIssueComments") {
			t.Fatalf("unexpected graphql query: %s", query)
		}
		variables, _ := body["variables"].(map[string]interface{})
		after := variables["after"]
		afterValues = append(afterValues, after)
		switch after {
		case nil:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"issue": map[string]interface{}{
						"comments": map[string]interface{}{
							"nodes": []map[string]interface{}{
								{
									"id":        "cmt-1",
									"body":      "first page",
									"createdAt": "2026-03-10T10:00:00Z",
									"updatedAt": "2026-03-10T10:00:00Z",
									"user":      map[string]interface{}{"displayName": "Reviewer"},
								},
							},
							"pageInfo": map[string]interface{}{
								"hasNextPage": true,
								"endCursor":   "cursor-1",
							},
						},
					},
				},
			})
		case "cursor-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"issue": map[string]interface{}{
						"comments": map[string]interface{}{
							"nodes": []map[string]interface{}{
								{
									"id":        "cmt-2",
									"body":      "second page",
									"createdAt": "2026-03-10T11:00:00Z",
									"updatedAt": "2026-03-10T11:00:00Z",
									"user":      map[string]interface{}{"displayName": "Reviewer"},
								},
							},
							"pageInfo": map[string]interface{}{
								"hasNextPage": false,
								"endCursor":   "",
							},
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected pagination cursor: %#v", after)
		}
	}))
	defer server.Close()

	t.Setenv("LINEAR_API_KEY", "test-token")

	provider := NewLinearProvider()
	provider.http = server.Client()
	project := &kanban.Project{ProviderConfig: map[string]interface{}{"endpoint": server.URL}}
	issue := &kanban.Issue{ProviderIssueRef: "linear-issue-3"}

	comments, err := provider.ListIssueComments(context.Background(), project, issue)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}

	if len(comments) != 2 {
		t.Fatalf("expected both pages of comments, got %#v", comments)
	}
	if len(afterValues) != 2 || afterValues[0] != nil || afterValues[1] != "cursor-1" {
		t.Fatalf("unexpected pagination cursors: %#v", afterValues)
	}
}

func TestLinearProviderDeleteIssueCommentRejectsCommentOutsideIssue(t *testing.T) {
	var requestCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		query, _ := body["query"].(string)
		if !strings.Contains(query, "MaestroLinearIssueComments") {
			t.Fatalf("unexpected graphql query: %s", query)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"issue": map[string]interface{}{
					"comments": map[string]interface{}{
						"nodes": []interface{}{},
						"pageInfo": map[string]interface{}{
							"hasNextPage": false,
							"endCursor":   "",
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	t.Setenv("LINEAR_API_KEY", "test-token")

	provider := NewLinearProvider()
	provider.http = server.Client()
	project := &kanban.Project{ProviderConfig: map[string]interface{}{"endpoint": server.URL}}
	issue := &kanban.Issue{ProviderIssueRef: "linear-issue-4"}

	err := provider.DeleteIssueComment(context.Background(), project, issue, "cmt-missing")
	if !errors.Is(err, kanban.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("expected only ownership lookup request, got %d", requestCount)
	}
}

func TestLinearProviderGetIssueCommentAttachmentContentDoesNotForwardAuthHeader(t *testing.T) {
	var attachmentAuthHeader string

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset":
			attachmentAuthHeader = r.Header.Get("Authorization")
			_, _ = w.Write([]byte("attachment-bytes"))
		default:
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			query, _ := body["query"].(string)
			if !strings.Contains(query, "MaestroLinearIssueComments") {
				t.Fatalf("unexpected graphql query: %s", query)
			}
			renderedBody := renderLinearCommentBody("Preview", []kanban.IssueCommentAttachment{{
				ID:       "att-1",
				Filename: "preview.txt",
				URL:      server.URL + "/asset",
			}})
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"issue": map[string]interface{}{
						"comments": map[string]interface{}{
							"nodes": []map[string]interface{}{
								{
									"id":        "cmt-1",
									"body":      renderedBody,
									"createdAt": "2026-03-10T10:00:00Z",
									"updatedAt": "2026-03-10T10:00:00Z",
									"user":      map[string]interface{}{"displayName": "Reviewer"},
								},
							},
							"pageInfo": map[string]interface{}{
								"hasNextPage": false,
								"endCursor":   "",
							},
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
	project := &kanban.Project{ProviderConfig: map[string]interface{}{"endpoint": server.URL}}
	issue := &kanban.Issue{ProviderIssueRef: "linear-issue-5"}

	content, err := provider.GetIssueCommentAttachmentContent(context.Background(), project, issue, "cmt-1", "att-1")
	if err != nil {
		t.Fatalf("GetIssueCommentAttachmentContent: %v", err)
	}
	defer content.Content.Close()

	data, err := io.ReadAll(content.Content)
	if err != nil {
		t.Fatalf("read attachment content: %v", err)
	}
	if string(data) != "attachment-bytes" {
		t.Fatalf("unexpected attachment body %q", string(data))
	}
	if attachmentAuthHeader != "" {
		t.Fatalf("expected no Authorization header on attachment request, got %q", attachmentAuthHeader)
	}
}

func TestLinearProviderGetIssueCommentAttachmentContentRejectsUntrustedHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		query, _ := body["query"].(string)
		if !strings.Contains(query, "MaestroLinearIssueComments") {
			t.Fatalf("unexpected graphql query: %s", query)
		}
		renderedBody := renderLinearCommentBody("Preview", []kanban.IssueCommentAttachment{{
			ID:       "att-1",
			Filename: "preview.txt",
			URL:      "https://example.com/asset",
		}})
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"issue": map[string]interface{}{
					"comments": map[string]interface{}{
						"nodes": []map[string]interface{}{
							{
								"id":        "cmt-1",
								"body":      renderedBody,
								"createdAt": "2026-03-10T10:00:00Z",
								"updatedAt": "2026-03-10T10:00:00Z",
								"user":      map[string]interface{}{"displayName": "Reviewer"},
							},
						},
						"pageInfo": map[string]interface{}{
							"hasNextPage": false,
							"endCursor":   "",
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	t.Setenv("LINEAR_API_KEY", "test-token")

	provider := NewLinearProvider()
	provider.http = server.Client()
	project := &kanban.Project{ProviderConfig: map[string]interface{}{"endpoint": server.URL}}
	issue := &kanban.Issue{ProviderIssueRef: "linear-issue-6"}

	content, err := provider.GetIssueCommentAttachmentContent(context.Background(), project, issue, "cmt-1", "att-1")
	if !errors.Is(err, kanban.ErrValidation) {
		t.Fatalf("expected validation error, got content=%#v err=%v", content, err)
	}
}

func TestLinearProviderUpdateIssueCommentRejectsRemovingAllContent(t *testing.T) {
	var requestCount int

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		query, _ := body["query"].(string)
		if !strings.Contains(query, "MaestroLinearIssueComments") {
			t.Fatalf("unexpected graphql query: %s", query)
		}
		renderedBody := renderLinearCommentBody("Preview", []kanban.IssueCommentAttachment{{
			ID:       "att-1",
			Filename: "preview.txt",
			URL:      server.URL + "/asset",
		}})
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"issue": map[string]interface{}{
					"comments": map[string]interface{}{
						"nodes": []map[string]interface{}{
							{
								"id":        "cmt-1",
								"body":      renderedBody,
								"createdAt": "2026-03-10T10:00:00Z",
								"updatedAt": "2026-03-10T10:00:00Z",
								"user":      map[string]interface{}{"displayName": "Reviewer"},
							},
						},
						"pageInfo": map[string]interface{}{
							"hasNextPage": false,
							"endCursor":   "",
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	t.Setenv("LINEAR_API_KEY", "test-token")

	provider := NewLinearProvider()
	provider.http = server.Client()
	project := &kanban.Project{ProviderConfig: map[string]interface{}{"endpoint": server.URL}}
	issue := &kanban.Issue{ProviderIssueRef: "linear-issue-7"}
	emptyBody := ""

	comment, err := provider.UpdateIssueComment(context.Background(), project, issue, "cmt-1", IssueCommentInput{
		Body:                &emptyBody,
		RemoveAttachmentIDs: []string{"att-1"},
	})
	if !errors.Is(err, kanban.ErrValidation) {
		t.Fatalf("expected validation error, got comment=%#v err=%v", comment, err)
	}
	if requestCount != 1 {
		t.Fatalf("expected only the ownership lookup request, got %d", requestCount)
	}
}
