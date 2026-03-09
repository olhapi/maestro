package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/pkg/config"
)

func setupTestRunner(t *testing.T, command string, mode string) (*Runner, *kanban.Store, *config.Manager, string, string) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	workspaceRoot := filepath.Join(tmpDir, "workspaces")

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	workflowContent := `---
tracker:
  kind: kanban
polling:
  interval_ms: 1000
workspace:
  root: ` + workspaceRoot + `
hooks:
  timeout_ms: 1000
agent:
  max_concurrent_agents: 2
  max_turns: 3
  max_retry_backoff_ms: 10000
  mode: ` + mode + `
codex:
  command: ` + command + `
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 1000
  turn_timeout_ms: 10000
---
Issue {{ issue.identifier }} {{ issue.title }}{% if attempt %} retry {{ attempt }}{% endif %}
`
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0o644); err != nil {
		t.Fatalf("Failed to write workflow: %v", err)
	}

	manager, err := config.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}
	runner := NewRunner(manager, store)

	t.Cleanup(func() {
		_ = store.Close()
	})

	return runner, store, manager, workspaceRoot, tmpDir
}

func TestGetOrCreateWorkspace(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Test Issue", "", 0, nil)

	workflow, err := runner.workflowProvider.Current()
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := runner.getOrCreateWorkspace(workflow, issue)
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	expectedPath := filepath.Join(workspaceRoot, issue.Identifier)
	if workspace.Path != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, workspace.Path)
	}
}

func TestBuildTurnPrompt(t *testing.T) {
	runner, store, manager, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Fix Login Bug", "Users cannot log in", 1, []string{"bug", "urgent"})
	workflow, _ := manager.Current()

	prompt, err := runner.buildTurnPrompt(workflow, issue, 2, 1)
	if err != nil {
		t.Fatalf("Failed to build prompt: %v", err)
	}
	for _, part := range []string{issue.Identifier, "Fix Login Bug", "retry 2"} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("expected prompt to contain %q, got %q", part, prompt)
		}
	}
	if !strings.Contains(prompt, "Prefer deterministic local verification first") {
		t.Fatalf("expected execution guidance in prompt, got %q", prompt)
	}
}

func TestContinuationPrompt(t *testing.T) {
	runner, store, manager, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Continue", "", 0, nil)
	workflow, _ := manager.Current()

	prompt, err := runner.buildTurnPrompt(workflow, issue, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Continuation guidance") {
		t.Fatalf("expected continuation prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "switch to another deterministic local check") {
		t.Fatalf("expected blocked-verification guidance, got %q", prompt)
	}
}

func TestRunAgentStdio(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Test", "Description", 0, nil)

	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected successful run, got %+v", result)
	}
	if !strings.Contains(result.Output, issue.Identifier) {
		t.Fatalf("expected output to contain rendered prompt, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "Prefer deterministic local verification first") {
		t.Fatalf("expected execution guidance in output, got %q", result.Output)
	}
}

func TestRunAttemptIncludesAttempt(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Retry", "Body", 0, nil)

	result, err := runner.RunAttempt(context.Background(), issue, 3)
	if err != nil {
		t.Fatalf("RunAttempt failed: %v", err)
	}
	if !strings.Contains(result.Output, "retry 3") {
		t.Fatalf("expected retry attempt in output, got %q", result.Output)
	}
}

func TestWorkspaceDeterministic(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	workflow, _ := runner.workflowProvider.Current()

	ws1, _ := runner.getOrCreateWorkspace(workflow, issue)
	ws2, _ := runner.getOrCreateWorkspace(workflow, issue)
	if ws1.Path != ws2.Path {
		t.Error("Expected deterministic workspace path")
	}
	expected := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))
	if ws1.Path != expected {
		t.Errorf("Expected path %s, got %s", expected, ws1.Path)
	}
}

func TestSanitizeWorkspaceKey(t *testing.T) {
	if got := sanitizeWorkspaceKey("MT/Det"); got != "MT_Det" {
		t.Fatalf("expected MT_Det, got %s", got)
	}
	if got := sanitizeWorkspaceKey("../escape"); got == "" || strings.Contains(got, "..") || strings.Contains(got, "/") {
		t.Fatalf("unexpected sanitized key: %s", got)
	}
}

func TestWorkspaceReplacesStaleFilePath(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Stale", "", 0, nil)
	path := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))

	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, _ := runner.workflowProvider.Current()
	ws, err := runner.getOrCreateWorkspace(workflow, issue)
	if err != nil {
		t.Fatalf("expected workspace recovery, got err: %v", err)
	}
	fi, err := os.Stat(ws.Path)
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected workspace dir at %s", ws.Path)
	}
}

func TestRunAgentAppServerModeTracksSession(t *testing.T) {
	tmpDir := t.TempDir()
	traceFile := filepath.Join(tmpDir, "trace.log")
	scriptPath := filepath.Join(tmpDir, "fake-codex.sh")
	script := `#!/bin/sh
trace_file="$TRACE_FILE"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"th1"}}}' ;;
    4) printf '%s\n' '{"id":3,"result":{"turn":{"id":"tu1"}}}'; printf '%s\n' '{"method":"turn/completed","params":{"threadId":"th1","turnId":"tu1","usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}}'; exit 0 ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_FILE", traceFile)

	runner, store, _, _, _ := setupTestRunner(t, "sh "+scriptPath, config.AgentModeAppServer)
	issue, _ := store.CreateIssue("", "", "AppServer", "", 0, nil)

	res, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Success || res.AppSession == nil {
		t.Fatalf("expected app session, got %+v", res)
	}
	if res.AppSession.SessionID != "th1-tu1" {
		t.Fatalf("unexpected session id: %s", res.AppSession.SessionID)
	}
}

func TestRunAgentAppServerModeAdvertisesAndExecutesDynamicTools(t *testing.T) {
	tmpDir := t.TempDir()
	traceFile := filepath.Join(tmpDir, "trace.log")
	scriptPath := filepath.Join(tmpDir, "fake-codex.sh")
	script := `#!/bin/sh
trace_file="$TRACE_FILE"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-dyn"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-dyn"}}}'
      printf '%s\n' '{"id":120,"method":"item/tool/call","params":{"tool":"ext_echo","arguments":{"args":{"value":"ok"}}}}'
      ;;
    5)
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-dyn","turnId":"turn-dyn"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_FILE", traceFile)

	runner, store, manager, _, _ := setupTestRunner(t, "sh "+scriptPath, config.AgentModeAppServer)
	runner = NewRunnerWithExtensions(manager, store, extensions.NewRegistry([]extensions.Tool{
		{Name: "ext_echo", Description: "echo tool", Command: "echo $MAESTRO_ARGS_JSON"},
	}))
	issue, _ := store.CreateIssue("", "", "Dynamic Tools", "", 0, nil)

	res, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}

	lines := readTraceLines(t, traceFile)
	foundDynamicTools := false
	foundToolResult := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 2 {
			if params, ok := payload["params"].(map[string]interface{}); ok {
				if dynamicTools, ok := params["dynamicTools"].([]interface{}); ok && len(dynamicTools) == 1 {
					spec, _ := dynamicTools[0].(map[string]interface{})
					foundDynamicTools = spec["name"] == "ext_echo"
				}
			}
		}
		if id, ok := asInt(payload["id"]); ok && id == 120 {
			if result, ok := payload["result"].(map[string]interface{}); ok && result["success"] == true {
				items, _ := result["contentItems"].([]interface{})
				if len(items) == 1 {
					item, _ := items[0].(map[string]interface{})
					if text, _ := item["text"].(string); strings.Contains(text, `"value":"ok"`) {
						foundToolResult = true
					}
				}
			}
		}
	}
	if !foundDynamicTools {
		t.Fatal("expected dynamic tool specs in thread/start")
	}
	if !foundToolResult {
		t.Fatal("expected extension-backed tool result in trace")
	}
}

func TestRunAgentAppServerModeReportsDynamicToolFailures(t *testing.T) {
	tmpDir := t.TempDir()
	traceFile := filepath.Join(tmpDir, "trace.log")
	scriptPath := filepath.Join(tmpDir, "fake-codex.sh")
	script := `#!/bin/sh
trace_file="$TRACE_FILE"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-fail"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-fail"}}}'
      printf '%s\n' '{"id":121,"method":"item/tool/call","params":{"tool":"ext_fail","arguments":{"args":{"value":"bad"}}}}'
      ;;
    5)
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-fail","turnId":"turn-fail"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_FILE", traceFile)

	runner, store, manager, _, _ := setupTestRunner(t, "sh "+scriptPath, config.AgentModeAppServer)
	runner = NewRunnerWithExtensions(manager, store, extensions.NewRegistry([]extensions.Tool{
		{Name: "ext_fail", Description: "fail tool", Command: "echo nope && exit 1"},
	}))
	issue, _ := store.CreateIssue("", "", "Dynamic Tool Failures", "", 0, nil)

	if _, err := runner.Run(context.Background(), issue); err != nil {
		t.Fatalf("run: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 121 {
			if result, ok := payload["result"].(map[string]interface{}); ok && result["success"] == false {
				items, _ := result["contentItems"].([]interface{})
				if len(items) != 1 {
					t.Fatalf("unexpected content items: %#v", result)
				}
				item, _ := items[0].(map[string]interface{})
				text, _ := item["text"].(string)
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(text), &parsed); err != nil {
					t.Fatalf("expected JSON error payload, got %q: %v", text, err)
				}
				return
			}
		}
	}
	t.Fatal("expected failed extension-backed tool response")
}

func TestCleanupWorkspaceRunsBeforeRemoveHook(t *testing.T) {
	traceFileDir := t.TempDir()
	traceFile := filepath.Join(traceFileDir, "cleanup.log")
	command := "cat"
	runner, store, manager, workspaceRoot, repoDir := setupTestRunner(t, command, config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Cleanup", "", 0, nil)

	workflowText := `---
tracker:
  kind: kanban
workspace:
  root: ` + workspaceRoot + `
hooks:
  before_remove: echo cleaned >> ` + traceFile + `
  timeout_ms: 1000
agent:
  mode: stdio
codex:
  command: cat
---
{{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(repoDir, "WORKFLOW.md"), []byte(workflowText), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}

	if _, err := runner.Run(context.Background(), issue); err != nil {
		t.Fatal(err)
	}
	if err := runner.CleanupWorkspace(context.Background(), issue); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(traceFile); err != nil {
		t.Fatalf("expected before_remove hook output, got %v", err)
	}
}

func readTraceLines(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "JSON:") {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "JSON:")), &payload); err != nil {
			t.Fatalf("decode trace line %q: %v", line, err)
		}
		out = append(out, payload)
	}
	return out
}

func asInt(v interface{}) (int, bool) {
	switch typed := v.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}
