package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type testClientOptions struct {
	provider       bool
	extensionsFile string
	scopedRepoPath string
}

type testRuntimeProvider struct {
	store          *kanban.Store
	scopedRepoPath string
}

type testMCPClient struct {
	*mcpclient.Client
}

func (c *testMCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcpapi.CallToolResult, error) {
	return c.Client.CallTool(ctx, mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
}

func (c *testMCPClient) ListTools(ctx context.Context, _ any) (*mcpapi.ListToolsResult, error) {
	return c.Client.ListTools(ctx, mcpapi.ListToolsRequest{})
}

func (p testRuntimeProvider) Status() map[string]interface{} {
	status := map[string]interface{}{"active_runs": len(p.snapshot().Running)}
	if strings.TrimSpace(p.scopedRepoPath) != "" {
		status["scoped_repo_path"] = p.scopedRepoPath
	}
	return status
}

func (p testRuntimeProvider) Snapshot() observability.Snapshot {
	return p.snapshot()
}

func (p testRuntimeProvider) LiveSessions() map[string]interface{} {
	issue := p.firstIssue()
	if issue == nil {
		return map[string]interface{}{"sessions": map[string]interface{}{}}
	}
	return map[string]interface{}{
		"sessions": map[string]interface{}{
			issue.Identifier: appserver.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-live-turn-live",
				ThreadID:        "thread-live",
				TurnID:          "turn-live",
				LastEvent:       "turn.started",
				LastTimestamp:   time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
				LastMessage:     "Working",
				TotalTokens:     30,
				TurnsStarted:    3,
			},
		},
	}
}

func (p testRuntimeProvider) RequestProjectRefresh(projectID string) map[string]interface{} {
	project, err := p.store.GetProject(projectID)
	if err != nil {
		return map[string]interface{}{"status": "not_found", "project_id": projectID}
	}
	_ = p.store.UpdateProjectState(projectID, kanban.ProjectStateRunning)
	return map[string]interface{}{
		"status":       "accepted",
		"project_id":   projectID,
		"project_name": project.Name,
		"state":        string(kanban.ProjectStateRunning),
	}
}

func (p testRuntimeProvider) StopProjectRuns(projectID string) map[string]interface{} {
	project, err := p.store.GetProject(projectID)
	if err != nil {
		return map[string]interface{}{"status": "not_found", "project_id": projectID}
	}
	_ = p.store.UpdateProjectState(projectID, kanban.ProjectStateStopped)
	return map[string]interface{}{
		"status":       "stopped",
		"project_id":   projectID,
		"project_name": project.Name,
		"state":        string(kanban.ProjectStateStopped),
		"stopped_runs": 0,
	}
}

func (p testRuntimeProvider) RetryIssueNow(ctx context.Context, identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func (p testRuntimeProvider) RunRecurringIssueNow(ctx context.Context, identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func (p testRuntimeProvider) PendingInterruptForIssue(issueID, identifier string) (*appserver.PendingInteraction, bool) {
	return nil, false
}

func (p testRuntimeProvider) firstIssue() *kanban.Issue {
	issues, err := p.store.ListIssues(nil)
	if err != nil || len(issues) == 0 {
		return nil
	}
	issue := issues[0]
	return &issue
}

func (p testRuntimeProvider) snapshot() observability.Snapshot {
	issue := p.firstIssue()
	out := observability.Snapshot{
		GeneratedAt: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
	}
	if issue == nil {
		return out
	}
	out.Running = []observability.RunningEntry{{
		IssueID:     issue.ID,
		Identifier:  issue.Identifier,
		State:       string(issue.State),
		Phase:       string(issue.WorkflowPhase),
		Attempt:     3,
		SessionID:   "thread-live-turn-live",
		TurnCount:   3,
		LastEvent:   "turn.started",
		LastMessage: "Working",
		StartedAt:   time.Date(2026, 3, 9, 11, 59, 0, 0, time.UTC),
		Tokens:      observability.TokenTotals{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, SecondsRunning: 60},
	}}
	return out
}

func testStore(t *testing.T, dbPath string) *kanban.Store {
	t.Helper()
	if dbPath == "" {
		dbPath = filepath.Join(t.TempDir(), "test.db")
	}
	s, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleMCPPNGBytes() []byte {
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

func TestNewServerWithExtensionsFailsOnBadExtensionFiles(t *testing.T) {
	store := testStore(t, "")

	if _, err := NewServerWithExtensions(store, nil, filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected missing extension file to fail")
	}

	malformedPath := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(malformedPath, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServerWithExtensions(store, nil, malformedPath); err == nil {
		t.Fatal("expected malformed extension json to fail")
	}

	invalidSchemaPath := filepath.Join(t.TempDir(), "bad-schema.json")
	if err := os.WriteFile(invalidSchemaPath, []byte(`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"type":"string"}}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServerWithExtensions(store, nil, invalidSchemaPath); err == nil {
		t.Fatal("expected invalid extension input_schema to fail")
	}
}

func TestLoadExtensionsAndExecute(t *testing.T) {
	store := testStore(t, "")
	extPath := filepath.Join(t.TempDir(), "ext.json")
	body := `[
  {"name":"ext_echo","description":"echo args","command":"echo $MAESTRO_TOOL_NAME:$MAESTRO_ARGS_JSON","timeout_sec":2}
]`
	if err := os.WriteFile(extPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewServerWithExtensions(store, nil, extPath)
	if err != nil {
		t.Fatalf("NewServerWithExtensions: %v", err)
	}
	if s.extensions == nil || !s.extensions.HasTools() {
		t.Fatalf("extension not loaded")
	}

	res, err := s.handleCallTool(context.Background(), "ext_echo", map[string]interface{}{"args": map[string]interface{}{"x": 1}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError || len(res.Content) == 0 {
		t.Fatalf("unexpected error result: %#v", res)
	}
}

func TestExtensionDisabledByPolicy(t *testing.T) {
	store := testStore(t, "")
	extPath := filepath.Join(t.TempDir(), "ext.json")
	body := `[{"name":"ext_off","description":"off","command":"echo hi","allowed":false}]`
	if err := os.WriteFile(extPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewServerWithExtensions(store, nil, extPath)
	if err != nil {
		t.Fatalf("NewServerWithExtensions: %v", err)
	}
	res, _ := s.handleCallTool(context.Background(), "ext_off", map[string]interface{}{})
	if !res.IsError {
		t.Fatalf("expected policy error")
	}
}

func TestExtensionTimeout(t *testing.T) {
	store := testStore(t, "")
	extPath := filepath.Join(t.TempDir(), "ext.json")
	body := `[{"name":"ext_slow","description":"slow","command":"sleep 2","timeout_sec":1}]`
	if err := os.WriteFile(extPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewServerWithExtensions(store, nil, extPath)
	if err != nil {
		t.Fatalf("NewServerWithExtensions: %v", err)
	}
	res, _ := s.handleCallTool(context.Background(), "ext_slow", map[string]interface{}{})
	if !res.IsError {
		t.Fatalf("expected timeout error")
	}
}

func TestHandleCallToolRecoversPanics(t *testing.T) {
	s := NewServerWithRegistry(nil, nil, nil)
	res, err := s.handleCallTool(context.Background(), "server_info", nil)
	if err != nil {
		t.Fatalf("handleCallTool returned error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected recovered panic to be returned as tool error")
	}
	env := decodeEnvelope(t, res)
	if env["tool"] != "server_info" {
		t.Fatalf("expected tool server_info, got %#v", env["tool"])
	}
	if errorPayload, _ := env["error"].(map[string]interface{}); !strings.Contains(asString(errorPayload["message"]), "panic recovered") {
		t.Fatalf("expected panic recovered message, got %#v", env["error"])
	}
}

func TestHelperProcessMCPServer(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_SERVER") != "1" {
		return
	}
	sep := 0
	for i, arg := range os.Args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep == 0 || sep+1 >= len(os.Args) {
		os.Exit(2)
	}
	dbPath := os.Args[sep+1]
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		os.Exit(3)
	}
	defer store.Close()

	var registry *extensions.Registry
	if extPath := os.Getenv("GO_WANT_MCP_EXTENSIONS"); extPath != "" {
		registry, err = extensions.LoadFile(extPath)
		if err != nil {
			os.Exit(4)
		}
	}

	var provider RuntimeProvider
	if os.Getenv("GO_WANT_MCP_PROVIDER") == "1" {
		provider = testRuntimeProvider{store: store, scopedRepoPath: os.Getenv("GO_WANT_MCP_SCOPED_REPO")}
	}

	server := NewServerWithRegistry(store, provider, registry)
	if err := server.ServeStdio(); err != nil {
		os.Exit(5)
	}
	os.Exit(0)
}

func TestStdioListToolsSnapshotAndSchemas(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	extPath := filepath.Join(t.TempDir(), "ext.json")
	body := `[
  {
    "name":"ext_schema",
    "description":"schema-aware extension",
    "command":"echo ok",
    "input_schema":{
      "type":"object",
      "properties":{
        "path":{"type":"string","description":"Absolute path"},
        "mode":{"type":"string","description":"Execution mode","examples":["dry-run"]}
      }
    }
  },
  {
    "name":"ext_fallback",
    "description":"fallback extension",
    "command":"echo ok"
  }
]`
	if err := os.WriteFile(extPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	client := newTestMCPClient(t, dbPath, testClientOptions{extensionsFile: extPath})
	defer client.Close()

	tools, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	wantNames := []string{
		"server_info",
		"create_project",
		"update_project",
		"list_projects",
		"delete_project",
		"create_epic",
		"update_epic",
		"list_epics",
		"delete_epic",
		"create_issue",
		"get_issue",
		"list_issues",
		"update_issue",
		"attach_issue_image",
		"delete_issue_image",
		"set_issue_state",
		"set_issue_workflow_phase",
		"delete_issue",
		"run_project",
		"stop_project",
		"get_issue_execution",
		"retry_issue",
		"run_issue_now",
		"board_overview",
		"set_blockers",
		"list_runtime_events",
		"get_runtime_snapshot",
		"list_sessions",
		"ext_schema",
		"ext_fallback",
	}
	sort.Strings(names)
	sort.Strings(wantNames)
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("unexpected tool list:\n got %v\nwant %v", names, wantNames)
	}

	serverInfo := findTool(t, tools.Tools, "server_info")
	if !strings.Contains(serverInfo.Description, "Maestro") || strings.Contains(strings.ToLower(serverInfo.Description), "symphony") {
		t.Fatalf("unexpected server_info description: %q", serverInfo.Description)
	}
	assertToolProperties(t, findTool(t, tools.Tools, "create_issue"), "blocked_by", "branch_name", "cron", "description", "enabled", "epic_id", "issue_type", "labels", "pr_url", "priority", "project_id", "state", "title")
	assertToolProperties(t, findTool(t, tools.Tools, "list_issues"), "epic_id", "issue_type", "limit", "offset", "project_id", "search", "sort", "state")
	assertToolProperties(t, findTool(t, tools.Tools, "update_issue"), "blocked_by", "branch_name", "cron", "description", "enabled", "epic_id", "identifier", "issue_type", "labels", "pr_url", "priority", "project_id", "title")
	assertToolProperties(t, findTool(t, tools.Tools, "attach_issue_image"), "identifier", "path")
	assertToolProperties(t, findTool(t, tools.Tools, "delete_issue_image"), "identifier", "image_id")
	assertToolProperties(t, findTool(t, tools.Tools, "update_epic"), "description", "id", "name", "project_id")
	assertToolProperties(t, findTool(t, tools.Tools, "set_issue_workflow_phase"), "identifier", "workflow_phase")
	assertToolProperties(t, findTool(t, tools.Tools, "get_issue_execution"), "identifier")
	assertToolProperties(t, findTool(t, tools.Tools, "get_runtime_snapshot"))
	assertToolProperties(t, findTool(t, tools.Tools, "list_sessions"), "identifier")
	assertToolProperties(t, findTool(t, tools.Tools, "retry_issue"), "identifier")
	assertToolProperties(t, findTool(t, tools.Tools, "run_issue_now"), "identifier")
	assertToolProperties(t, findTool(t, tools.Tools, "ext_schema"), "mode", "path")
	assertToolProperties(t, findTool(t, tools.Tools, "ext_fallback"), "args")
}

func TestStdioBuiltInToolCoverage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := t.TempDir()
	tempRepoPath := t.TempDir()
	secondRepoPath := t.TempDir()
	store := testStore(t, dbPath)

	client := newTestMCPClient(t, dbPath, testClientOptions{})
	defer client.Close()

	serverInfoRes, err := client.CallTool(context.Background(), "server_info", map[string]interface{}{})
	if err != nil {
		t.Fatalf("server_info failed: %v", err)
	}
	serverInfo := decodeEnvelope(t, serverInfoRes)
	if serverInfo["data"].(map[string]interface{})["runtime_available"] != false {
		t.Fatalf("expected runtime_available=false, got %#v", serverInfo)
	}

	projectRes, err := client.CallTool(context.Background(), "create_project", map[string]interface{}{
		"name":      "Demo",
		"repo_path": repoPath,
	})
	if err != nil {
		t.Fatalf("create_project failed: %v", err)
	}
	project := decodeEnvelope(t, projectRes)["data"].(map[string]interface{})
	projectID := asString(project["id"])

	updateProjectRes, err := client.CallTool(context.Background(), "update_project", map[string]interface{}{
		"id":            projectID,
		"name":          "Demo Updated",
		"description":   "Updated project",
		"repo_path":     repoPath,
		"workflow_path": filepath.Join(repoPath, "WORKFLOW.md"),
	})
	if err != nil {
		t.Fatalf("update_project failed: %v", err)
	}
	if got := decodeEnvelope(t, updateProjectRes)["data"].(map[string]interface{})["name"]; got != "Demo Updated" {
		t.Fatalf("unexpected update_project payload: %#v", got)
	}

	listProjectsRes, err := client.CallTool(context.Background(), "list_projects", map[string]interface{}{})
	if err != nil {
		t.Fatalf("list_projects failed: %v", err)
	}
	if items := decodeEnvelope(t, listProjectsRes)["data"].(map[string]interface{})["items"].([]interface{}); len(items) == 0 {
		t.Fatal("expected list_projects items")
	}

	tempProjectRes, err := client.CallTool(context.Background(), "create_project", map[string]interface{}{
		"name":      "Temp",
		"repo_path": tempRepoPath,
	})
	if err != nil {
		t.Fatalf("create temp project failed: %v", err)
	}
	tempProjectID := asString(decodeEnvelope(t, tempProjectRes)["data"].(map[string]interface{})["id"])

	epicRes, err := client.CallTool(context.Background(), "create_epic", map[string]interface{}{
		"project_id":  projectID,
		"name":        "Epic A",
		"description": "Main epic",
	})
	if err != nil {
		t.Fatalf("create_epic failed: %v", err)
	}
	epicID := asString(decodeEnvelope(t, epicRes)["data"].(map[string]interface{})["id"])

	updateEpicRes, err := client.CallTool(context.Background(), "update_epic", map[string]interface{}{
		"id":          epicID,
		"project_id":  projectID,
		"name":        "Epic A Updated",
		"description": "Updated epic",
	})
	if err != nil {
		t.Fatalf("update_epic failed: %v", err)
	}
	if got := decodeEnvelope(t, updateEpicRes)["data"].(map[string]interface{})["name"]; got != "Epic A Updated" {
		t.Fatalf("unexpected update_epic payload: %#v", got)
	}

	listEpicsRes, err := client.CallTool(context.Background(), "list_epics", map[string]interface{}{"project_id": projectID})
	if err != nil {
		t.Fatalf("list_epics failed: %v", err)
	}
	if items := decodeEnvelope(t, listEpicsRes)["data"].(map[string]interface{})["items"].([]interface{}); len(items) == 0 {
		t.Fatal("expected list_epics items")
	}

	tempEpicRes, err := client.CallTool(context.Background(), "create_epic", map[string]interface{}{
		"project_id": projectID,
		"name":       "Temp Epic",
	})
	if err != nil {
		t.Fatalf("create temp epic failed: %v", err)
	}
	tempEpicID := asString(decodeEnvelope(t, tempEpicRes)["data"].(map[string]interface{})["id"])

	issueARes, err := client.CallTool(context.Background(), "create_issue", map[string]interface{}{
		"title":      "Issue A",
		"project_id": projectID,
		"priority":   1,
	})
	if err != nil {
		t.Fatalf("create_issue A failed: %v", err)
	}
	issueA := decodeEnvelope(t, issueARes)["data"].(map[string]interface{})

	issueBRes, err := client.CallTool(context.Background(), "create_issue", map[string]interface{}{
		"title":       "Issue B",
		"description": "Second issue",
		"project_id":  projectID,
		"epic_id":     epicID,
		"priority":    2,
		"labels":      []interface{}{"mcp", "coverage"},
		"state":       "ready",
		"blocked_by":  []interface{}{issueA["identifier"]},
		"branch_name": "feat/mcp",
		"pr_url":      "https://example.com/pr/17",
	})
	if err != nil {
		t.Fatalf("create_issue B failed: %v", err)
	}
	issueB := decodeEnvelope(t, issueBRes)["data"].(map[string]interface{})
	issueBIdentifier := asString(issueB["identifier"])
	if issueB["state"] != "ready" {
		t.Fatalf("expected create_issue state ready, got %#v", issueB["state"])
	}

	getIssueRes, err := client.CallTool(context.Background(), "get_issue", map[string]interface{}{
		"identifier": issueBIdentifier,
	})
	if err != nil {
		t.Fatalf("get_issue failed: %v", err)
	}
	getIssue := decodeEnvelope(t, getIssueRes)["data"].(map[string]interface{})
	if getIssue["identifier"] != issueBIdentifier {
		t.Fatalf("unexpected get_issue payload: %#v", getIssue)
	}

	listIssuesRes, err := client.CallTool(context.Background(), "list_issues", map[string]interface{}{
		"project_id": projectID,
		"search":     "Issue",
		"sort":       "priority_asc",
		"limit":      10,
		"offset":     0,
	})
	if err != nil {
		t.Fatalf("list_issues failed: %v", err)
	}
	listIssues := decodeEnvelope(t, listIssuesRes)["data"].(map[string]interface{})
	if got := int(listIssues["total"].(float64)); got < 2 {
		t.Fatalf("expected at least 2 issues, got %d", got)
	}

	secondProjectRes, err := client.CallTool(context.Background(), "create_project", map[string]interface{}{
		"name":      "Second",
		"repo_path": secondRepoPath,
	})
	if err != nil {
		t.Fatalf("create second project failed: %v", err)
	}
	secondProjectID := asString(decodeEnvelope(t, secondProjectRes)["data"].(map[string]interface{})["id"])

	secondEpicRes, err := client.CallTool(context.Background(), "create_epic", map[string]interface{}{
		"project_id": secondProjectID,
		"name":       "Epic B",
	})
	if err != nil {
		t.Fatalf("create second epic failed: %v", err)
	}
	secondEpicID := asString(decodeEnvelope(t, secondEpicRes)["data"].(map[string]interface{})["id"])

	updateIssueRes, err := client.CallTool(context.Background(), "update_issue", map[string]interface{}{
		"identifier":  issueBIdentifier,
		"project_id":  secondProjectID,
		"epic_id":     secondEpicID,
		"title":       "Issue B Updated",
		"description": "Moved issue",
		"priority":    5,
		"labels":      []interface{}{"go", "mcp"},
		"blocked_by":  []interface{}{},
		"branch_name": "feat/mcp-v2",
		"pr_url":      "https://example.com/pr/23",
	})
	if err != nil {
		t.Fatalf("update_issue failed: %v", err)
	}
	updateIssue := decodeEnvelope(t, updateIssueRes)["data"].(map[string]interface{})
	if updateIssue["project_id"] != secondProjectID || updateIssue["epic_id"] != secondEpicID {
		t.Fatalf("unexpected update_issue payload: %#v", updateIssue)
	}

	imagePath := filepath.Join(t.TempDir(), "mcp.png")
	if err := os.WriteFile(imagePath, sampleMCPPNGBytes(), 0o644); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}
	attachIssueImageRes, err := client.CallTool(context.Background(), "attach_issue_image", map[string]interface{}{
		"identifier": issueBIdentifier,
		"path":       imagePath,
	})
	if err != nil {
		t.Fatalf("attach_issue_image failed: %v", err)
	}
	attachIssueImage := decodeEnvelope(t, attachIssueImageRes)["data"].(map[string]interface{})
	image := attachIssueImage["image"].(map[string]interface{})
	if image["content_type"] != "image/png" {
		t.Fatalf("unexpected attach_issue_image payload: %#v", attachIssueImage)
	}
	attachedImageID := asString(image["id"])
	attachedIssue := attachIssueImage["issue"].(map[string]interface{})
	if images, _ := attachedIssue["images"].([]interface{}); len(images) != 1 {
		t.Fatalf("expected attached issue to expose image metadata, got %#v", attachedIssue["images"])
	}

	issueCRes, err := client.CallTool(context.Background(), "create_issue", map[string]interface{}{
		"title":      "Issue C",
		"project_id": secondProjectID,
	})
	if err != nil {
		t.Fatalf("create_issue C failed: %v", err)
	}
	issueCIdentifier := asString(decodeEnvelope(t, issueCRes)["data"].(map[string]interface{})["identifier"])

	setStateRes, err := client.CallTool(context.Background(), "set_issue_state", map[string]interface{}{
		"identifier": issueBIdentifier,
		"state":      "in_progress",
	})
	if err != nil {
		t.Fatalf("set_issue_state failed: %v", err)
	}
	if got := decodeEnvelope(t, setStateRes)["data"].(map[string]interface{})["state"]; got != "in_progress" {
		t.Fatalf("unexpected set_issue_state payload: %#v", got)
	}

	setPhaseRes, err := client.CallTool(context.Background(), "set_issue_workflow_phase", map[string]interface{}{
		"identifier":     issueBIdentifier,
		"workflow_phase": "review",
	})
	if err != nil {
		t.Fatalf("set_issue_workflow_phase failed: %v", err)
	}
	if got := decodeEnvelope(t, setPhaseRes)["data"].(map[string]interface{})["workflow_phase"]; got != "review" {
		t.Fatalf("unexpected set_issue_workflow_phase payload: %#v", got)
	}

	boardRes, err := client.CallTool(context.Background(), "board_overview", map[string]interface{}{"project_id": secondProjectID})
	if err != nil {
		t.Fatalf("board_overview failed: %v", err)
	}
	board := decodeEnvelope(t, boardRes)["data"].(map[string]interface{})
	if board["in_progress"] == nil {
		t.Fatalf("unexpected board_overview payload: %#v", board)
	}

	setBlockersRes, err := client.CallTool(context.Background(), "set_blockers", map[string]interface{}{
		"identifier": issueBIdentifier,
		"blocked_by": []interface{}{issueCIdentifier},
	})
	if err != nil {
		t.Fatalf("set_blockers failed: %v", err)
	}
	blockedBy := decodeEnvelope(t, setBlockersRes)["data"].(map[string]interface{})["blocked_by"].([]interface{})
	if len(blockedBy) != 1 || asString(blockedBy[0]) != issueCIdentifier {
		t.Fatalf("unexpected set_blockers payload: %#v", blockedBy)
	}

	deleteIssueImageRes, err := client.CallTool(context.Background(), "delete_issue_image", map[string]interface{}{
		"identifier": issueBIdentifier,
		"image_id":   attachedImageID,
	})
	if err != nil {
		t.Fatalf("delete_issue_image failed: %v", err)
	}
	deleteIssueImage := decodeEnvelope(t, deleteIssueImageRes)["data"].(map[string]interface{})
	if deleteIssueImage["deleted"] != true {
		t.Fatalf("unexpected delete_issue_image payload: %#v", deleteIssueImage)
	}
	if issueDetail, _ := deleteIssueImage["issue"].(map[string]interface{}); len(issueDetail["images"].([]interface{})) != 0 {
		t.Fatalf("expected issue image list to be empty after delete, got %#v", issueDetail["images"])
	}

	issueBStore, err := store.GetIssueByIdentifier(issueBIdentifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issueBStore.ID,
		Identifier: issueBIdentifier,
		Phase:      "review",
		Attempt:    2,
		RunKind:    "run_failed",
		Error:      "approval_required",
		UpdatedAt:  time.Date(2026, 3, 9, 12, 5, 0, 0, time.UTC),
		AppSession: appserver.Session{
			IssueID:         issueBStore.ID,
			IssueIdentifier: issueBIdentifier,
			SessionID:       "thread-persisted-turn-persisted",
			LastEvent:       "turn.approval_required",
			LastTimestamp:   time.Date(2026, 3, 9, 12, 5, 0, 0, time.UTC),
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_failed", map[string]interface{}{
		"issue_id":   issueBStore.ID,
		"identifier": issueBIdentifier,
		"phase":      "review",
		"attempt":    2,
		"error":      "approval_required",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	getExecutionRes, err := client.CallTool(context.Background(), "get_issue_execution", map[string]interface{}{
		"identifier": issueBIdentifier,
	})
	if err != nil {
		t.Fatalf("get_issue_execution failed: %v", err)
	}
	getExecution := decodeEnvelope(t, getExecutionRes)["data"].(map[string]interface{})
	if getExecution["runtime_available"] != false || getExecution["session_source"] != "persisted" {
		t.Fatalf("unexpected get_issue_execution payload: %#v", getExecution)
	}

	listRuntimeEventsRes, err := client.CallTool(context.Background(), "list_runtime_events", map[string]interface{}{
		"since": 0,
		"limit": 10,
	})
	if err != nil {
		t.Fatalf("list_runtime_events failed: %v", err)
	}
	events := decodeEnvelope(t, listRuntimeEventsRes)["data"].(map[string]interface{})["events"].([]interface{})
	if len(events) == 0 {
		t.Fatal("expected runtime events")
	}

	deleteIssueRes, err := client.CallTool(context.Background(), "delete_issue", map[string]interface{}{
		"identifier": issueCIdentifier,
	})
	if err != nil {
		t.Fatalf("delete_issue failed: %v", err)
	}
	if got := decodeEnvelope(t, deleteIssueRes)["data"].(map[string]interface{})["identifier"]; got != issueCIdentifier {
		t.Fatalf("unexpected delete_issue payload: %#v", got)
	}

	deleteEpicRes, err := client.CallTool(context.Background(), "delete_epic", map[string]interface{}{
		"id": tempEpicID,
	})
	if err != nil {
		t.Fatalf("delete_epic failed: %v", err)
	}
	if got := decodeEnvelope(t, deleteEpicRes)["data"].(map[string]interface{})["id"]; got != tempEpicID {
		t.Fatalf("unexpected delete_epic payload: %#v", got)
	}

	deleteProjectRes, err := client.CallTool(context.Background(), "delete_project", map[string]interface{}{
		"id": tempProjectID,
	})
	if err != nil {
		t.Fatalf("delete_project failed: %v", err)
	}
	if got := decodeEnvelope(t, deleteProjectRes)["data"].(map[string]interface{})["id"]; got != tempProjectID {
		t.Fatalf("unexpected delete_project payload: %#v", got)
	}
}

func TestStdioRuntimeToolsWithProvider(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	store := testStore(t, dbPath)
	issue, err := store.CreateIssue("", "", "Runtime issue", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_failed",
		Error:      "approval_required",
		UpdatedAt:  time.Date(2026, 3, 9, 12, 10, 0, 0, time.UTC),
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-persisted-turn-persisted",
			LastEvent:       "turn.approval_required",
			LastTimestamp:   time.Date(2026, 3, 9, 12, 10, 0, 0, time.UTC),
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	client := newTestMCPClient(t, dbPath, testClientOptions{provider: true})
	defer client.Close()

	serverInfoRes, err := client.CallTool(context.Background(), "server_info", map[string]interface{}{})
	if err != nil {
		t.Fatalf("server_info failed: %v", err)
	}
	if got := decodeEnvelope(t, serverInfoRes)["data"].(map[string]interface{})["runtime_available"]; got != true {
		t.Fatalf("expected runtime_available=true, got %#v", got)
	}

	getExecutionRes, err := client.CallTool(context.Background(), "get_issue_execution", map[string]interface{}{
		"identifier": issue.Identifier,
	})
	if err != nil {
		t.Fatalf("get_issue_execution failed: %v", err)
	}
	getExecution := decodeEnvelope(t, getExecutionRes)["data"].(map[string]interface{})
	if getExecution["runtime_available"] != true || getExecution["session_source"] != "live" || getExecution["active"] != true {
		t.Fatalf("unexpected runtime execution payload: %#v", getExecution)
	}

	getSnapshotRes, err := client.CallTool(context.Background(), "get_runtime_snapshot", map[string]interface{}{})
	if err != nil {
		t.Fatalf("get_runtime_snapshot failed: %v", err)
	}
	snapshot := decodeEnvelope(t, getSnapshotRes)["data"].(map[string]interface{})
	if counts := snapshot["counts"].(map[string]interface{}); counts["running"].(float64) < 1 {
		t.Fatalf("unexpected runtime snapshot payload: %#v", snapshot)
	}

	listSessionsRes, err := client.CallTool(context.Background(), "list_sessions", map[string]interface{}{
		"identifier": issue.Identifier,
	})
	if err != nil {
		t.Fatalf("list_sessions failed: %v", err)
	}
	listSessions := decodeEnvelope(t, listSessionsRes)["data"].(map[string]interface{})
	if listSessions["issue"] != issue.Identifier {
		t.Fatalf("unexpected list_sessions payload: %#v", listSessions)
	}

	retryIssueRes, err := client.CallTool(context.Background(), "retry_issue", map[string]interface{}{
		"identifier": issue.Identifier,
	})
	if err != nil {
		t.Fatalf("retry_issue failed: %v", err)
	}
	if got := decodeEnvelope(t, retryIssueRes)["data"].(map[string]interface{})["status"]; got != "queued_now" {
		t.Fatalf("unexpected retry_issue payload: %#v", got)
	}

	repoPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoPath, "WORKFLOW.md"), []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	projectRes, err := client.CallTool(context.Background(), "create_project", map[string]interface{}{
		"name":      "Runtime project",
		"repo_path": repoPath,
	})
	if err != nil {
		t.Fatalf("create_project failed: %v", err)
	}
	projectID := asString(decodeEnvelope(t, projectRes)["data"].(map[string]interface{})["id"])

	runProjectRes, err := client.CallTool(context.Background(), "run_project", map[string]interface{}{
		"id": projectID,
	})
	if err != nil {
		t.Fatalf("run_project failed: %v", err)
	}
	if got := decodeEnvelope(t, runProjectRes)["data"].(map[string]interface{})["state"]; got != "running" {
		t.Fatalf("unexpected run_project payload: %#v", got)
	}

	stopProjectRes, err := client.CallTool(context.Background(), "stop_project", map[string]interface{}{
		"id": projectID,
	})
	if err != nil {
		t.Fatalf("stop_project failed: %v", err)
	}
	if got := decodeEnvelope(t, stopProjectRes)["data"].(map[string]interface{})["state"]; got != "stopped" {
		t.Fatalf("unexpected stop_project payload: %#v", got)
	}
}

func TestStdioRecurringIssueTools(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	client := newTestMCPClient(t, dbPath, testClientOptions{provider: true})
	defer client.Close()

	projectRes, err := client.CallTool(context.Background(), "create_project", map[string]interface{}{
		"name":      "Automation",
		"repo_path": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("create_project failed: %v", err)
	}
	projectID := asString(decodeEnvelope(t, projectRes)["data"].(map[string]interface{})["id"])

	createIssueRes, err := client.CallTool(context.Background(), "create_issue", map[string]interface{}{
		"project_id": projectID,
		"title":      "Scan GitHub ready-to-work",
		"issue_type": "recurring",
		"cron":       "*/15 * * * *",
		"enabled":    true,
	})
	if err != nil {
		t.Fatalf("create_issue recurring failed: %v", err)
	}
	created := decodeEnvelope(t, createIssueRes)["data"].(map[string]interface{})
	identifier := asString(created["identifier"])
	if created["issue_type"] != "recurring" || created["cron"] != "*/15 * * * *" || created["enabled"] != true {
		t.Fatalf("unexpected recurring create payload: %#v", created)
	}

	listIssuesRes, err := client.CallTool(context.Background(), "list_issues", map[string]interface{}{
		"issue_type": "recurring",
		"limit":      10,
		"offset":     0,
	})
	if err != nil {
		t.Fatalf("list_issues recurring failed: %v", err)
	}
	listIssues := decodeEnvelope(t, listIssuesRes)["data"].(map[string]interface{})
	items := listIssues["items"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["identifier"] != identifier {
		t.Fatalf("unexpected recurring list payload: %#v", listIssues)
	}

	updateIssueRes, err := client.CallTool(context.Background(), "update_issue", map[string]interface{}{
		"identifier": identifier,
		"cron":       "0 * * * *",
		"enabled":    false,
	})
	if err != nil {
		t.Fatalf("update_issue recurring failed: %v", err)
	}
	updated := decodeEnvelope(t, updateIssueRes)["data"].(map[string]interface{})
	if updated["cron"] != "0 * * * *" || updated["enabled"] != false {
		t.Fatalf("unexpected recurring update payload: %#v", updated)
	}

	runNowRes, err := client.CallTool(context.Background(), "run_issue_now", map[string]interface{}{
		"identifier": identifier,
	})
	if err != nil {
		t.Fatalf("run_issue_now failed: %v", err)
	}
	if got := decodeEnvelope(t, runNowRes)["data"].(map[string]interface{})["status"]; got != "queued_now" {
		t.Fatalf("unexpected run_issue_now payload: %#v", got)
	}
}

func TestSetIssueStateRejectsBlockedInProgress(t *testing.T) {
	store := testStore(t, "")
	blocker, err := store.CreateIssue("", "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}
	blocked, err := store.CreateIssue("", "", "Blocked", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}

	client := newTestMCPClient(t, store.DBPath(), testClientOptions{})
	defer client.Close()

	res, err := client.CallTool(context.Background(), "set_issue_state", map[string]interface{}{
		"identifier": blocked.Identifier,
		"state":      "in_progress",
	})
	if err != nil {
		t.Fatalf("set_issue_state failed: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error, got %#v", res)
	}
	env := decodeEnvelope(t, res)
	message := asString(env["error"].(map[string]interface{})["message"])
	if !strings.Contains(message, "cannot move issue to in_progress: blocked by "+blocker.Identifier) {
		t.Fatalf("unexpected error message: %q", message)
	}
}

func TestCreateIssueRejectsBlockedInitialInProgress(t *testing.T) {
	store := testStore(t, "")
	project, err := store.CreateProject("Project", "", t.TempDir(), "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	blocker, err := store.CreateIssue(project.ID, "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, kanban.StateInReview); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}

	client := newTestMCPClient(t, store.DBPath(), testClientOptions{})
	defer client.Close()

	res, err := client.CallTool(context.Background(), "create_issue", map[string]interface{}{
		"project_id": project.ID,
		"title":      "Blocked create",
		"state":      "in_progress",
		"blocked_by": []interface{}{blocker.Identifier},
	})
	if err != nil {
		t.Fatalf("create_issue failed: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error, got %#v", res)
	}
	env := decodeEnvelope(t, res)
	message := asString(env["error"].(map[string]interface{})["message"])
	if !strings.Contains(message, "cannot move issue to in_progress: blocked by "+blocker.Identifier) {
		t.Fatalf("unexpected error message: %q", message)
	}
}

func TestScopedProjectMutationsAndProjectPayloads(t *testing.T) {
	store := testStore(t, "")
	scopedRepoPath := t.TempDir()
	outOfScopeRepoPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(scopedRepoPath, "WORKFLOW.md"), []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
		t.Fatalf("write scoped WORKFLOW.md: %v", err)
	}

	outOfScopeProject, err := store.CreateProject("Outside", "", outOfScopeRepoPath, "")
	if err != nil {
		t.Fatalf("CreateProject out-of-scope: %v", err)
	}
	inScopeProject, err := store.CreateProject("Inside", "", scopedRepoPath, "")
	if err != nil {
		t.Fatalf("CreateProject in-scope: %v", err)
	}

	client := newTestMCPClient(t, store.DBPath(), testClientOptions{provider: true, scopedRepoPath: scopedRepoPath})
	defer client.Close()

	createRes, err := client.CallTool(context.Background(), "create_project", map[string]interface{}{
		"name":      "Blocked",
		"repo_path": outOfScopeRepoPath,
	})
	if err != nil {
		t.Fatalf("create_project failed: %v", err)
	}
	if !createRes.IsError {
		t.Fatalf("expected scoped create_project error, got %#v", createRes)
	}
	createMessage := asString(decodeEnvelope(t, createRes)["error"].(map[string]interface{})["message"])
	if !strings.Contains(createMessage, "repo_path must match the current server scope ("+scopedRepoPath+")") {
		t.Fatalf("unexpected create_project message: %q", createMessage)
	}

	updateRes, err := client.CallTool(context.Background(), "update_project", map[string]interface{}{
		"id":        inScopeProject.ID,
		"name":      "Inside",
		"repo_path": outOfScopeRepoPath,
	})
	if err != nil {
		t.Fatalf("update_project failed: %v", err)
	}
	if !updateRes.IsError {
		t.Fatalf("expected scoped update_project error, got %#v", updateRes)
	}
	updateMessage := asString(decodeEnvelope(t, updateRes)["error"].(map[string]interface{})["message"])
	if !strings.Contains(updateMessage, "repo_path must match the current server scope ("+scopedRepoPath+")") {
		t.Fatalf("unexpected update_project message: %q", updateMessage)
	}

	listRes, err := client.CallTool(context.Background(), "list_projects", map[string]interface{}{})
	if err != nil {
		t.Fatalf("list_projects failed: %v", err)
	}
	items := decodeEnvelope(t, listRes)["data"].(map[string]interface{})["items"].([]interface{})
	var sawOutOfScope bool
	var sawInScope bool
	for _, item := range items {
		project := item.(map[string]interface{})
		switch asString(project["id"]) {
		case outOfScopeProject.ID:
			sawOutOfScope = true
			if project["dispatch_ready"] != false {
				t.Fatalf("expected out-of-scope project dispatch_ready=false, got %#v", project["dispatch_ready"])
			}
			if got := asString(project["dispatch_error"]); got != "Project repo is outside the current server scope ("+scopedRepoPath+")" {
				t.Fatalf("unexpected out-of-scope dispatch_error: %#v", got)
			}
		case inScopeProject.ID:
			sawInScope = true
			if project["dispatch_ready"] != true {
				t.Fatalf("expected in-scope project dispatch_ready=true, got %#v", project["dispatch_ready"])
			}
			if got := asString(project["dispatch_error"]); got != "" {
				t.Fatalf("expected empty dispatch_error for in-scope project, got %#v", got)
			}
		}
	}
	if !sawOutOfScope || !sawInScope {
		t.Fatalf("expected both seeded projects in list_projects payload, got %#v", items)
	}

	runProjectRes, err := client.CallTool(context.Background(), "run_project", map[string]interface{}{
		"id": outOfScopeProject.ID,
	})
	if err != nil {
		t.Fatalf("run_project failed: %v", err)
	}
	if !runProjectRes.IsError {
		t.Fatalf("expected scoped run_project error, got %#v", runProjectRes)
	}
	runProjectMessage := asString(decodeEnvelope(t, runProjectRes)["error"].(map[string]interface{})["message"])
	if !strings.Contains(runProjectMessage, "Project repo is outside the current server scope ("+scopedRepoPath+")") {
		t.Fatalf("unexpected run_project message: %q", runProjectMessage)
	}
}

func TestStdioRuntimeToolsWithoutProviderReturnExplicitErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	client := newTestMCPClient(t, dbPath, testClientOptions{})
	defer client.Close()

	tools, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	for _, name := range []string{"get_runtime_snapshot", "list_sessions", "retry_issue", "run_project", "stop_project"} {
		findTool(t, tools.Tools, name)
	}

	for _, tc := range []struct {
		name string
		args map[string]interface{}
	}{
		{name: "get_runtime_snapshot", args: map[string]interface{}{}},
		{name: "list_sessions", args: map[string]interface{}{}},
		{name: "retry_issue", args: map[string]interface{}{"identifier": "ISS-1"}},
		{name: "run_project", args: map[string]interface{}{"id": "proj-1"}},
		{name: "stop_project", args: map[string]interface{}{"id": "proj-1"}},
	} {
		res, err := client.CallTool(context.Background(), tc.name, tc.args)
		if err != nil {
			t.Fatalf("%s transport failed: %v", tc.name, err)
		}
		if !res.IsError {
			t.Fatalf("%s expected tool error", tc.name)
		}
		env := decodeEnvelope(t, res)
		msg := asString(env["error"].(map[string]interface{})["message"])
		if !strings.Contains(msg, "runtime_unavailable") {
			t.Fatalf("%s expected runtime_unavailable, got %q", tc.name, msg)
		}
	}
}

func TestStdioUnknownToolReturnsToolError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	client := newTestMCPClient(t, dbPath, testClientOptions{})
	defer client.Close()

	_, err := client.CallTool(context.Background(), "unknown_tool", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected unknown tool call to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "tool") || !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("unexpected unknown tool error: %v", err)
	}
}

func newTestMCPClient(t *testing.T, dbPath string, opts testClientOptions) *testMCPClient {
	t.Helper()
	envPath, err := exec.LookPath("env")
	if err != nil {
		t.Fatalf("env lookup failed: %v", err)
	}
	args := []string{"GO_WANT_MCP_SERVER=1"}
	if opts.provider {
		args = append(args, "GO_WANT_MCP_PROVIDER=1")
	}
	if opts.scopedRepoPath != "" {
		args = append(args, "GO_WANT_MCP_SCOPED_REPO="+opts.scopedRepoPath)
	}
	if opts.extensionsFile != "" {
		args = append(args, "GO_WANT_MCP_EXTENSIONS="+opts.extensionsFile)
	}
	args = append(args,
		os.Args[0],
		"-test.run=TestHelperProcessMCPServer",
		"--",
		dbPath,
	)
	client, err := mcpclient.NewStdioMCPClient(envPath, nil, args...)
	if err != nil {
		t.Fatalf("NewStdioMCPClient failed: %v", err)
	}
	if _, err := client.Initialize(context.Background(), mcpapi.InitializeRequest{
		Params: mcpapi.InitializeParams{
			ProtocolVersion: mcpapi.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpapi.Implementation{Name: "test", Version: "1.0.0"},
			Capabilities:    mcpapi.ClientCapabilities{},
		},
	}); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return &testMCPClient{Client: client}
}

func decodeEnvelope(t *testing.T, result *mcpapi.CallToolResult) map[string]interface{} {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected tool content")
	}
	var text string
	switch content := result.Content[0].(type) {
	case mcpapi.TextContent:
		text = content.Text
	default:
		t.Fatalf("unexpected content type %T", result.Content[0])
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("failed to decode envelope %q: %v", text, err)
	}
	return env
}

func findTool(t *testing.T, tools []mcpapi.Tool, name string) mcpapi.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return mcpapi.Tool{}
}

func assertToolProperties(t *testing.T, tool mcpapi.Tool, want ...string) {
	t.Helper()
	var got []string
	for name := range tool.InputSchema.Properties {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s properties mismatch:\n got %v\nwant %v", tool.Name, got, want)
	}
}
