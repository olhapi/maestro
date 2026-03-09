package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpapi "github.com/mark3labs/mcp-go/mcp"
	"github.com/olhapi/maestro/internal/kanban"
)

func testStore(t *testing.T) *kanban.Store {
	t.Helper()
	db := filepath.Join(t.TempDir(), "test.db")
	s, err := kanban.NewStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestLoadExtensionsAndExecute(t *testing.T) {
	store := testStore(t)
	extPath := filepath.Join(t.TempDir(), "ext.json")
	json := `[
  {"name":"ext_echo","description":"echo args","command":"echo $MAESTRO_TOOL_NAME:$MAESTRO_ARGS_JSON","timeout_sec":2}
]`
	if err := os.WriteFile(extPath, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewServerWithExtensions(store, extPath)
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
	store := testStore(t)
	extPath := filepath.Join(t.TempDir(), "ext.json")
	json := `[{"name":"ext_off","description":"off","command":"echo hi","allowed":false}]`
	if err := os.WriteFile(extPath, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServerWithExtensions(store, extPath)
	res, _ := s.handleCallTool(context.Background(), "ext_off", map[string]interface{}{})
	if !res.IsError {
		t.Fatalf("expected policy error")
	}
}

func TestExtensionTimeout(t *testing.T) {
	store := testStore(t)
	extPath := filepath.Join(t.TempDir(), "ext.json")
	json := `[{"name":"ext_slow","description":"slow","command":"sleep 2","timeout_sec":1}]`
	if err := os.WriteFile(extPath, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServerWithExtensions(store, extPath)
	res, _ := s.handleCallTool(context.Background(), "ext_slow", map[string]interface{}{})
	if !res.IsError {
		t.Fatalf("expected timeout error")
	}
}

func TestHandleCallToolRecoversPanics(t *testing.T) {
	s := NewServerWithRegistry(nil, nil)
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

	server := NewServer(store)
	if err := server.ServeStdio(); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestStdioServerInfoAndToolEnvelope(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	client := newTestMCPClient(t, dbPath)
	defer client.Close()

	tools, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	foundServerInfo := false
	for _, tool := range tools.Tools {
		if tool.Name == "server_info" {
			foundServerInfo = true
			break
		}
	}
	if !foundServerInfo {
		t.Fatal("expected server_info tool to be registered")
	}

	serverInfo, err := client.CallTool(context.Background(), "server_info", map[string]interface{}{})
	if err != nil {
		t.Fatalf("server_info failed: %v", err)
	}
	env := decodeEnvelope(t, serverInfo)
	meta := env["meta"].(map[string]interface{})
	absDB, _ := filepath.Abs(dbPath)
	if meta["db_path"] != absDB {
		t.Fatalf("expected db_path %q, got %#v", absDB, meta["db_path"])
	}
	if asString(meta["store_id"]) == "" {
		t.Fatal("expected non-empty store_id")
	}
	if asString(meta["server_instance_id"]) == "" {
		t.Fatal("expected non-empty server_instance_id")
	}
	data := env["data"].(map[string]interface{})
	if data["project_count"].(float64) != 0 {
		t.Fatalf("expected zero projects, got %#v", data["project_count"])
	}
}

func TestStdioMutationsExposeIdentityAndSurviveErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := t.TempDir()
	client := newTestMCPClient(t, dbPath)
	defer client.Close()

	projectRes, err := client.CallTool(context.Background(), "create_project", map[string]interface{}{
		"name":      "Demo",
		"repo_path": repoPath,
	})
	if err != nil {
		t.Fatalf("create_project failed: %v", err)
	}
	projectEnv := decodeEnvelope(t, projectRes)
	project := projectEnv["data"].(map[string]interface{})
	projectID := asString(project["id"])
	if projectID == "" {
		t.Fatal("expected project id")
	}
	storeID := asString(projectEnv["meta"].(map[string]interface{})["store_id"])

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
		"title":      "Issue B",
		"project_id": projectID,
		"priority":   2,
	})
	if err != nil {
		t.Fatalf("create_issue B failed: %v", err)
	}
	issueB := decodeEnvelope(t, issueBRes)["data"].(map[string]interface{})

	blockerRes, err := client.CallTool(context.Background(), "set_blockers", map[string]interface{}{
		"identifier": asString(issueB["identifier"]),
		"blocked_by": []interface{}{issueA["identifier"]},
	})
	if err != nil {
		t.Fatalf("set_blockers failed: %v", err)
	}
	blockerEnv := decodeEnvelope(t, blockerRes)
	if asString(blockerEnv["meta"].(map[string]interface{})["store_id"]) != storeID {
		t.Fatal("expected stable store id across tool calls")
	}
	blockerData := blockerEnv["data"].(map[string]interface{})
	blockedBy := blockerData["blocked_by"].([]interface{})
	if len(blockedBy) != 1 || asString(blockedBy[0]) != asString(issueA["identifier"]) {
		t.Fatalf("expected persisted blocker, got %#v", blockerData["blocked_by"])
	}

	getIssueRes, err := client.CallTool(context.Background(), "get_issue", map[string]interface{}{
		"identifier": asString(issueB["identifier"]),
	})
	if err != nil {
		t.Fatalf("get_issue failed: %v", err)
	}
	getIssue := decodeEnvelope(t, getIssueRes)["data"].(map[string]interface{})
	getBlockedBy := getIssue["blocked_by"].([]interface{})
	if len(getBlockedBy) != 1 || asString(getBlockedBy[0]) != asString(issueA["identifier"]) {
		t.Fatalf("expected get_issue blocked_by to be persisted, got %#v", getIssue["blocked_by"])
	}

	listIssuesRes, err := client.CallTool(context.Background(), "list_issues", map[string]interface{}{
		"project_id": projectID,
	})
	if err != nil {
		t.Fatalf("list_issues failed: %v", err)
	}
	items := decodeEnvelope(t, listIssuesRes)["data"].(map[string]interface{})["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(items))
	}

	invalidRes, err := client.CallTool(context.Background(), "set_blockers", map[string]interface{}{
		"identifier": asString(issueB["identifier"]),
		"blocked_by": []interface{}{"MISSING-1"},
	})
	if err != nil {
		t.Fatalf("invalid set_blockers failed at transport level: %v", err)
	}
	if !invalidRes.IsError {
		t.Fatal("expected invalid blocker call to return tool error")
	}
	invalidEnv := decodeEnvelope(t, invalidRes)
	if invalidEnv["ok"] != false {
		t.Fatalf("expected ok=false for invalid blocker, got %#v", invalidEnv["ok"])
	}

	serverInfoRes, err := client.CallTool(context.Background(), "server_info", map[string]interface{}{})
	if err != nil {
		t.Fatalf("server_info after tool error failed: %v", err)
	}
	serverInfo := decodeEnvelope(t, serverInfoRes)
	if asString(serverInfo["meta"].(map[string]interface{})["store_id"]) != storeID {
		t.Fatal("expected transport to remain attached to same store after tool error")
	}
}

func TestStdioServerInfoDistinguishesDifferentDatabases(t *testing.T) {
	dbA := filepath.Join(t.TempDir(), "a.db")
	dbB := filepath.Join(t.TempDir(), "b.db")

	clientA := newTestMCPClient(t, dbA)
	defer clientA.Close()
	clientB := newTestMCPClient(t, dbB)
	defer clientB.Close()

	infoA, err := clientA.CallTool(context.Background(), "server_info", map[string]interface{}{})
	if err != nil {
		t.Fatalf("server_info A failed: %v", err)
	}
	infoB, err := clientB.CallTool(context.Background(), "server_info", map[string]interface{}{})
	if err != nil {
		t.Fatalf("server_info B failed: %v", err)
	}

	metaA := decodeEnvelope(t, infoA)["meta"].(map[string]interface{})
	metaB := decodeEnvelope(t, infoB)["meta"].(map[string]interface{})
	if asString(metaA["store_id"]) == asString(metaB["store_id"]) {
		t.Fatalf("expected different store ids for different databases, got %q", metaA["store_id"])
	}
}

func newTestMCPClient(t *testing.T, dbPath string) *mcpclient.StdioMCPClient {
	t.Helper()
	envPath, err := exec.LookPath("env")
	if err != nil {
		t.Fatalf("env lookup failed: %v", err)
	}
	client, err := mcpclient.NewStdioMCPClient(
		envPath,
		"GO_WANT_MCP_SERVER=1",
		os.Args[0],
		"-test.run=TestHelperProcessMCPServer",
		"--",
		dbPath,
	)
	if err != nil {
		t.Fatalf("NewStdioMCPClient failed: %v", err)
	}
	if _, err := client.Initialize(context.Background(), mcpapi.ClientCapabilities{}, mcpapi.Implementation{Name: "test", Version: "1.0.0"}, "1.0"); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return client
}

func decodeEnvelope(t *testing.T, result *mcpapi.CallToolResult) map[string]interface{} {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected tool content")
	}
	var text string
	switch content := result.Content[0].(type) {
	case map[string]interface{}:
		text = asString(content["text"])
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

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
