package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/kanban"
)

type badSessionsProvider struct {
	testRuntimeProvider
}

func (p badSessionsProvider) LiveSessions() map[string]interface{} {
	return map[string]interface{}{"sessions": "oops"}
}

func mustEnvelopeData(t *testing.T, result *mcpapi.CallToolResult) map[string]any {
	t.Helper()
	envelope, err := decodeEnvelopeResult(result)
	if err != nil {
		t.Fatalf("decodeEnvelopeResult failed: %v", err)
	}
	data, ok := envelope.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected envelope data map, got %#v", envelope.Data)
	}
	return data
}

func TestMCPServerHandlerBranches(t *testing.T) {
	store := testStore(t, filepath.Join(t.TempDir(), "mcp.db"))
	provider := testRuntimeProvider{store: store, scopedRepoPath: "/repo"}
	server := NewServerWithProvider(store, provider)
	ctx := context.Background()

	t.Run("basic server and extension helpers", func(t *testing.T) {
		data := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleServerInfo(ctx, nil) }))
		if data["project_count"] != float64(0) || data["issue_count"] != float64(0) {
			t.Fatalf("unexpected empty server info counts: %#v", data)
		}

		if result, err := server.handleCallTool(ctx, "missing_tool", map[string]interface{}{}); err != nil {
			t.Fatalf("handleCallTool unknown tool failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || envelope.Error == nil {
			t.Fatalf("unexpected unknown tool response: %#v %v", envelope, err)
		}

		if result, err := server.handleExtensionTool(ctx, "extension", map[string]interface{}{}); err != nil {
			t.Fatalf("handleExtensionTool(nil) failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || envelope.Error == nil {
			t.Fatalf("unexpected nil-extension response: %#v %v", envelope, err)
		}

		if handler := server.StreamableHTTPHandler(); handler == nil {
			t.Fatal("expected StreamableHTTPHandler to return a handler")
		}
	})

	t.Run("project and epic flows", func(t *testing.T) {
		projectData := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleCreateProject(ctx, map[string]interface{}{
			"name":          "Alpha",
			"description":   "Alpha description",
			"repo_path":     "/repo",
			"workflow_path": "",
		}) }))
		projectID := projectData["id"].(string)

		badCreate, err := server.handleCreateProject(ctx, map[string]interface{}{
			"name":          "Bad scope",
			"description":   "",
			"repo_path":     "/elsewhere",
			"workflow_path": "",
		})
		if err != nil {
			t.Fatalf("handleCreateProject bad scope failed: %v", err)
		}
		if envelope, err := decodeEnvelopeResult(badCreate); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "repo_path must match") {
			t.Fatalf("expected scoped repo failure, got %#v %v", envelope, err)
		}

		updateMissingRepo, err := server.handleUpdateProject(ctx, map[string]interface{}{"id": projectID})
		if err != nil {
			t.Fatalf("handleUpdateProject missing repo failed: %v", err)
		}
		if envelope, err := decodeEnvelopeResult(updateMissingRepo); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "repo_path is required") {
			t.Fatalf("expected missing repo_path failure, got %#v %v", envelope, err)
		}

		updatedProject := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleUpdateProject(ctx, map[string]interface{}{
			"id":            projectID,
			"name":          "Alpha v2",
			"description":   "Updated description",
			"repo_path":     "/repo",
			"workflow_path": "workflow.yml",
		}) }))
		if updatedProject["name"] != "Alpha v2" {
			t.Fatalf("unexpected updated project payload: %#v", updatedProject)
		}

		projects := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleListProjects(ctx, map[string]interface{}{"limit": 10, "offset": 0}) }))
		if got := len(projects["items"].([]any)); got == 0 {
			t.Fatalf("expected project list to include created project, got %#v", projects)
		}

		epicData := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleCreateEpic(ctx, map[string]interface{}{
			"project_id":  projectID,
			"name":        "Epic 1",
			"description": "Epic description",
		}) }))
		epicID := epicData["id"].(string)

		updatedEpic := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleUpdateEpic(ctx, map[string]interface{}{
			"id":          epicID,
			"project_id":  projectID,
			"name":        "Epic 1 v2",
			"description": "Epic updated",
		}) }))
		if updatedEpic["name"] != "Epic 1 v2" {
			t.Fatalf("unexpected updated epic payload: %#v", updatedEpic)
		}

		epics := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleListEpics(ctx, map[string]interface{}{
			"project_id": projectID,
			"limit":      10,
			"offset":     0,
		}) }))
		if got := len(epics["items"].([]any)); got == 0 {
			t.Fatalf("expected epic list to include created epic, got %#v", epics)
		}

		if _, err := server.handleDeleteEpic(ctx, map[string]interface{}{"id": epicID}); err != nil {
			t.Fatalf("handleDeleteEpic failed: %v", err)
		}
		if _, err := server.handleDeleteProject(ctx, map[string]interface{}{"id": projectID}); err != nil {
			t.Fatalf("handleDeleteProject failed: %v", err)
		}
	})

	t.Run("issue and runtime flows", func(t *testing.T) {
		projectData := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleCreateProject(ctx, map[string]interface{}{
			"name":          "Issue Project",
			"description":   "Issue description",
			"repo_path":     "/repo",
			"workflow_path": "",
		}) }))
		projectID := projectData["id"].(string)
		blockerData := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleCreateIssue(ctx, map[string]interface{}{
			"project_id":  projectID,
			"epic_id":     "",
			"title":       "Blocker",
			"description": "",
			"issue_type":  string(kanban.IssueTypeStandard),
			"state":       string(kanban.StateBacklog),
			"labels":      []any{"blocker"},
			"blocked_by":  []any{},
		}) }))
		blockerID := blockerData["identifier"].(string)

		issueData := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleCreateIssue(ctx, map[string]interface{}{
			"project_id":  projectID,
			"epic_id":     "",
			"title":       "Primary issue",
			"description": "Issue description",
			"issue_type":  string(kanban.IssueTypeStandard),
			"state":       string(kanban.StateBacklog),
			"labels":      []any{"mcp", "coverage"},
			"blocked_by":  []any{blockerID},
			"branch_name": "feature/mcp",
			"pr_url":      "https://example.com/pr/1",
		}) }))
		issueID := issueData["identifier"].(string)

		if _, err := server.handleGetIssue(ctx, map[string]interface{}{"identifier": issueID}); err != nil {
			t.Fatalf("handleGetIssue failed: %v", err)
		}
		if _, err := server.handleListIssueComments(ctx, map[string]interface{}{"identifier": issueID, "limit": 10, "offset": 0}); err != nil {
			t.Fatalf("handleListIssueComments failed: %v", err)
		}

		lists := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleListIssues(ctx, map[string]interface{}{
			"project_id": projectID,
			"epic_id":    "",
			"state":      string(kanban.StateBacklog),
			"issue_type": string(kanban.IssueTypeStandard),
			"search":     "Primary",
			"sort":       "updated_desc",
			"limit":      10,
			"offset":     0,
		}) }))
		if got := len(lists["items"].([]any)); got == 0 {
			t.Fatalf("expected issue list to include created issue, got %#v", lists)
		}

		noUpdates, err := server.handleUpdateIssue(ctx, map[string]interface{}{"identifier": issueID})
		if err != nil {
			t.Fatalf("handleUpdateIssue(no updates) failed: %v", err)
		}
		if envelope, err := decodeEnvelopeResult(noUpdates); err != nil || envelope.Data.(map[string]any)["updated"] != false {
			t.Fatalf("expected update=false when no fields supplied, got %#v %v", envelope, err)
		}

		updatedIssue := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleUpdateIssue(ctx, map[string]interface{}{
			"identifier": issueID,
			"title":      "Primary issue v2",
			"description": "Updated",
			"labels":     []any{"mcp", "coverage", "updated"},
		}) }))
		if updatedIssue["title"] != "Primary issue v2" {
			t.Fatalf("unexpected updated issue payload: %#v", updatedIssue)
		}

		assetPath := filepath.Join(t.TempDir(), "asset.png")
		if err := os.WriteFile(assetPath, sampleMCPPNGBytes(), 0o600); err != nil {
			t.Fatalf("write asset file: %v", err)
		}
		attached := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleAttachIssueAsset(ctx, map[string]interface{}{
			"identifier": issueID,
			"path":       assetPath,
		}) }))
		asset := attached["asset"].(map[string]any)
		assetID := asset["id"].(string)

		commentPath := filepath.Join(t.TempDir(), "comment.png")
		if err := os.WriteFile(commentPath, sampleMCPPNGBytes(), 0o600); err != nil {
			t.Fatalf("write comment attachment: %v", err)
		}
		commentData := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleCreateIssueComment(ctx, map[string]interface{}{
			"identifier":         issueID,
			"body":               "Initial comment",
			"attachment_paths":    []any{commentPath},
			"remove_attachment_ids": []string{},
		}) }))
		commentID := commentData["id"].(string)

		updatedComment := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleUpdateIssueComment(ctx, map[string]interface{}{
			"identifier":         issueID,
			"comment_id":         commentID,
			"body":               "Updated comment",
			"attachment_paths":    []any{commentPath},
			"remove_attachment_ids": []string{},
		}) }))
		if updatedComment["body"] != "Updated comment" {
			t.Fatalf("unexpected updated comment payload: %#v", updatedComment)
		}

		if _, err := server.handleDeleteIssueComment(ctx, map[string]interface{}{"identifier": issueID, "comment_id": commentID}); err != nil {
			t.Fatalf("handleDeleteIssueComment failed: %v", err)
		}
		if _, err := server.handleDeleteIssueAsset(ctx, map[string]interface{}{"identifier": issueID, "asset_id": assetID}); err != nil {
			t.Fatalf("handleDeleteIssueAsset failed: %v", err)
		}

		if _, err := server.handleSetBlockers(ctx, map[string]interface{}{
			"identifier": issueID,
			"blocked_by": []any{blockerID},
		}); err != nil {
			t.Fatalf("handleSetBlockers failed: %v", err)
		}
		blockedState, err := server.handleSetIssueState(ctx, map[string]interface{}{"identifier": issueID, "state": string(kanban.StateInProgress)})
		if err != nil {
			t.Fatalf("handleSetIssueState(blocked) failed: %v", err)
		}
		if envelope, err := decodeEnvelopeResult(blockedState); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "blocked by") {
			t.Fatalf("expected blocked state transition failure, got %#v %v", envelope, err)
		}

		unblocked, err := server.handleSetIssueState(ctx, map[string]interface{}{"identifier": blockerID, "state": string(kanban.StateDone)})
		if err != nil {
			t.Fatalf("handleSetIssueState(success) failed: %v", err)
		}
		if envelope, err := decodeEnvelopeResult(unblocked); err != nil || !envelope.OK {
			t.Fatalf("expected successful state transition, got %#v %v", envelope, err)
		}

		phaseInvalid, err := server.handleSetIssueWorkflowPhase(ctx, map[string]interface{}{"identifier": issueID, "workflow_phase": "invalid"})
		if err != nil {
			t.Fatalf("handleSetIssueWorkflowPhase(invalid) failed: %v", err)
		}
		if envelope, err := decodeEnvelopeResult(phaseInvalid); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "Invalid workflow phase") {
			t.Fatalf("expected invalid workflow phase failure, got %#v %v", envelope, err)
		}

		if _, err := server.handleSetIssueWorkflowPhase(ctx, map[string]interface{}{"identifier": issueID, "workflow_phase": "review"}); err != nil {
			t.Fatalf("handleSetIssueWorkflowPhase(review) failed: %v", err)
		}

		boardOverview, err := server.handleBoardOverview(ctx, map[string]interface{}{"project_id": projectID})
		if err != nil {
			t.Fatalf("handleBoardOverview failed: %v", err)
		}
		boardData := mustEnvelopeData(t, boardOverview)
		if boardData[string(kanban.StateBacklog)] == nil {
			t.Fatalf("expected board overview counts, got %#v", boardData)
		}

		runtimeEvents, err := server.handleListRuntimeEvents(ctx, map[string]interface{}{"since": int64(0), "limit": 100})
		if err != nil {
			t.Fatalf("handleListRuntimeEvents failed: %v", err)
		}
		if _, ok := mustEnvelopeData(t, runtimeEvents)["events"].([]any); !ok {
			t.Fatalf("expected runtime events payload, got %#v", runtimeEvents)
		}

		runtimeSnapshot := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleGetRuntimeSnapshot(ctx, map[string]interface{}{}) }))
		if runtimeSnapshot == nil {
			t.Fatal("expected runtime snapshot payload")
		}

		sessionsAll := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleListSessions(ctx, map[string]interface{}{}) }))
		if _, ok := sessionsAll["sessions"].(map[string]any); !ok {
			t.Fatalf("expected sessions map, got %#v", sessionsAll)
		}
		sessionIssue := provider.firstIssue()
		if sessionIssue == nil {
			t.Fatal("expected a session issue")
		}
		sessionsOne := mustEnvelopeData(t, mustResult(t, func() (*mcpapi.CallToolResult, error) { return server.handleListSessions(ctx, map[string]interface{}{"identifier": sessionIssue.Identifier}) }))
		if sessionsOne["issue"] != sessionIssue.Identifier {
			t.Fatalf("expected session lookup by identifier, got %#v", sessionsOne)
		}

		if _, err := server.handleDeleteIssue(ctx, map[string]interface{}{"identifier": issueID}); err != nil {
			t.Fatalf("handleDeleteIssue failed: %v", err)
		}
		if _, err := server.handleDeleteIssue(ctx, map[string]interface{}{"identifier": blockerID}); err != nil {
			t.Fatalf("handleDeleteIssue(blocker) failed: %v", err)
		}
		if _, err := server.handleDeleteProject(ctx, map[string]interface{}{"id": projectID}); err != nil {
			t.Fatalf("handleDeleteProject after issues failed: %v", err)
		}
	})

	t.Run("missing issue handler branches", func(t *testing.T) {
		missingArgs := map[string]interface{}{"identifier": "missing-issue"}

		checkError := func(name string, call func() (*mcpapi.CallToolResult, error)) {
			t.Helper()
			result, err := call()
			if err != nil {
				t.Fatalf("%s returned unexpected error: %v", name, err)
			}
			envelope, decErr := decodeEnvelopeResult(result)
			if decErr != nil {
				t.Fatalf("%s failed to decode envelope: %v", name, decErr)
			}
			if envelope.OK || envelope.Error == nil || !strings.Contains(envelope.Error.Message, "Issue not found") {
				t.Fatalf("%s expected missing issue error, got %#v", name, envelope)
			}
		}

		checkError("handleGetIssue", func() (*mcpapi.CallToolResult, error) { return server.handleGetIssue(ctx, missingArgs) })
		checkError("handleListIssueComments", func() (*mcpapi.CallToolResult, error) { return server.handleListIssueComments(ctx, missingArgs) })
		checkError("handleUpdateIssue", func() (*mcpapi.CallToolResult, error) { return server.handleUpdateIssue(ctx, missingArgs) })
		checkError("handleAttachIssueAsset", func() (*mcpapi.CallToolResult, error) { return server.handleAttachIssueAsset(ctx, map[string]interface{}{"identifier": "missing-issue", "path": "/tmp/missing"}) })
		checkError("handleCreateIssueComment", func() (*mcpapi.CallToolResult, error) { return server.handleCreateIssueComment(ctx, map[string]interface{}{"identifier": "missing-issue", "body": "hello"}) })
		checkError("handleUpdateIssueComment", func() (*mcpapi.CallToolResult, error) { return server.handleUpdateIssueComment(ctx, map[string]interface{}{"identifier": "missing-issue", "comment_id": "comment-1"}) })
		checkError("handleDeleteIssueComment", func() (*mcpapi.CallToolResult, error) { return server.handleDeleteIssueComment(ctx, map[string]interface{}{"identifier": "missing-issue", "comment_id": "comment-1"}) })
		checkError("handleDeleteIssueAsset", func() (*mcpapi.CallToolResult, error) { return server.handleDeleteIssueAsset(ctx, map[string]interface{}{"identifier": "missing-issue", "asset_id": "asset-1"}) })
		checkError("handleSetIssueState", func() (*mcpapi.CallToolResult, error) { return server.handleSetIssueState(ctx, map[string]interface{}{"identifier": "missing-issue", "state": string(kanban.StateInProgress)}) })
		checkError("handleSetIssueWorkflowPhase", func() (*mcpapi.CallToolResult, error) { return server.handleSetIssueWorkflowPhase(ctx, map[string]interface{}{"identifier": "missing-issue", "workflow_phase": "review"}) })
		checkError("handleDeleteIssue", func() (*mcpapi.CallToolResult, error) { return server.handleDeleteIssue(ctx, missingArgs) })
		checkError("handleSetBlockers", func() (*mcpapi.CallToolResult, error) { return server.handleSetBlockers(ctx, map[string]interface{}{"identifier": "missing-issue", "blocked_by": []any{"ISSUE-1"}}) })
		checkError("handleGetIssueExecution", func() (*mcpapi.CallToolResult, error) { return server.handleGetIssueExecution(ctx, missingArgs) })
	})

	t.Run("runtime provider nil branches and error responses", func(t *testing.T) {
		nilServer := *server
		nilServer.provider = nil

		if result, err := nilServer.handleRunProject(ctx, map[string]interface{}{"id": "missing"}); err != nil {
			t.Fatalf("handleRunProject(nil provider) failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "runtime_unavailable") {
			t.Fatalf("expected runtime unavailable error, got %#v %v", envelope, err)
		}

		if result, err := nilServer.handleStopProject(ctx, map[string]interface{}{"id": "missing"}); err != nil {
			t.Fatalf("handleStopProject(nil provider) failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "runtime_unavailable") {
			t.Fatalf("expected runtime unavailable error, got %#v %v", envelope, err)
		}

		if result, err := nilServer.handleRetryIssue(ctx, map[string]interface{}{"identifier": "ISSUE-1"}); err != nil {
			t.Fatalf("handleRetryIssue(nil provider) failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "runtime_unavailable") {
			t.Fatalf("expected runtime unavailable error, got %#v %v", envelope, err)
		}

		if result, err := nilServer.handleRunIssueNow(ctx, map[string]interface{}{"identifier": "ISSUE-1"}); err != nil {
			t.Fatalf("handleRunIssueNow(nil provider) failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "runtime_unavailable") {
			t.Fatalf("expected runtime unavailable error, got %#v %v", envelope, err)
		}

		if result, err := nilServer.handleGetRuntimeSnapshot(ctx, map[string]interface{}{}); err != nil {
			t.Fatalf("handleGetRuntimeSnapshot(nil provider) failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "runtime_unavailable") {
			t.Fatalf("expected runtime unavailable error, got %#v %v", envelope, err)
		}

		if result, err := nilServer.handleListSessions(ctx, map[string]interface{}{}); err != nil {
			t.Fatalf("handleListSessions(nil provider) failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "runtime_unavailable") {
			t.Fatalf("expected runtime unavailable error, got %#v %v", envelope, err)
		}
	})

	t.Run("list sessions error and scope handling", func(t *testing.T) {
		badSessionServer := *server
		badSessionServer.provider = badSessionsProvider{testRuntimeProvider: provider}
		if result, err := badSessionServer.handleListSessions(ctx, map[string]interface{}{"identifier": "missing"}); err != nil {
			t.Fatalf("handleListSessions(bad sessions) failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "Live sessions are unavailable") {
			t.Fatalf("expected sessions map error, got %#v %v", envelope, err)
		}

		if result, err := server.handleListSessions(ctx, map[string]interface{}{"identifier": "missing-session"}); err != nil {
			t.Fatalf("handleListSessions(missing session) failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "Session not found") {
			t.Fatalf("expected session not found error, got %#v %v", envelope, err)
		}

		outOfScopeServer := *server
		outOfScopeServer.provider = testRuntimeProvider{store: store, scopedRepoPath: "/scoped"}
		outOfScopeProject, err := store.CreateProject("Out of scope", "", "/elsewhere", "")
		if err != nil {
			t.Fatalf("CreateProject(out of scope) failed: %v", err)
		}
		if result, err := outOfScopeServer.handleRunProject(ctx, map[string]interface{}{"id": outOfScopeProject.ID}); err != nil {
			t.Fatalf("handleRunProject(out of scope) failed: %v", err)
		} else if envelope, err := decodeEnvelopeResult(result); err != nil || envelope.OK || !strings.Contains(envelope.Error.Message, "outside the current server scope") {
			t.Fatalf("expected scope failure, got %#v %v", envelope, err)
		}
	})

	t.Run("closed store error branches", func(t *testing.T) {
		deadStore := testStore(t, filepath.Join(t.TempDir(), "dead.db"))
		deadServer := NewServerWithProvider(deadStore, testRuntimeProvider{store: deadStore})
		if err := deadStore.Close(); err != nil {
			t.Fatalf("deadStore.Close failed: %v", err)
		}

		checkError := func(name string, call func() (*mcpapi.CallToolResult, error)) {
			t.Helper()
			result, err := call()
			if err != nil {
				t.Fatalf("%s returned unexpected error: %v", name, err)
			}
			envelope, decErr := decodeEnvelopeResult(result)
			if decErr != nil {
				t.Fatalf("%s failed to decode envelope: %v", name, decErr)
			}
			if envelope.OK || envelope.Error == nil {
				t.Fatalf("%s expected error envelope, got %#v", name, envelope)
			}
		}

		checkError("handleServerInfo", func() (*mcpapi.CallToolResult, error) { return deadServer.handleServerInfo(ctx, nil) })
		checkError("handleListProjects", func() (*mcpapi.CallToolResult, error) { return deadServer.handleListProjects(ctx, map[string]interface{}{"limit": 10, "offset": 0}) })
		checkError("handleCreateEpic", func() (*mcpapi.CallToolResult, error) { return deadServer.handleCreateEpic(ctx, map[string]interface{}{"project_id": "missing", "name": "Epic", "description": ""}) })
		checkError("handleUpdateEpic", func() (*mcpapi.CallToolResult, error) { return deadServer.handleUpdateEpic(ctx, map[string]interface{}{"id": "missing", "project_id": "missing", "name": "Epic", "description": ""}) })
		checkError("handleDeleteEpic", func() (*mcpapi.CallToolResult, error) { return deadServer.handleDeleteEpic(ctx, map[string]interface{}{"id": "missing"}) })
		checkError("handleDeleteProject", func() (*mcpapi.CallToolResult, error) { return deadServer.handleDeleteProject(ctx, map[string]interface{}{"id": "missing"}) })
		checkError("handleListEpics", func() (*mcpapi.CallToolResult, error) { return deadServer.handleListEpics(ctx, map[string]interface{}{"project_id": "missing", "limit": 10, "offset": 0}) })
		checkError("handleBoardOverview", func() (*mcpapi.CallToolResult, error) { return deadServer.handleBoardOverview(ctx, map[string]interface{}{"project_id": "missing"}) })
		checkError("handleListRuntimeEvents", func() (*mcpapi.CallToolResult, error) { return deadServer.handleListRuntimeEvents(ctx, map[string]interface{}{"since": int64(0), "limit": 10}) })
	})
}

func mustResult(t *testing.T, call func() (*mcpapi.CallToolResult, error)) *mcpapi.CallToolResult {
	t.Helper()
	result, err := call()
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	return result
}
