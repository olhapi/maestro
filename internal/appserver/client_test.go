package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return path
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
		if line == "" || !strings.HasPrefix(line, "JSON:") {
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

func TestRunRejectsInvalidWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-1")
	outside := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := ClientConfig{
		Executable:    "/bin/sh",
		Args:          []string{"-c", "exit 0"},
		WorkspaceRoot: workspaceRoot,
	}

	if _, err := Run(context.Background(), ClientConfig{
		Executable:    cfg.Executable,
		Args:          cfg.Args,
		Workspace:     workspaceRoot,
		WorkspaceRoot: workspaceRoot,
	}); err == nil {
		t.Fatal("expected workspace root rejection")
	}

	if _, err := Run(context.Background(), ClientConfig{
		Executable:    cfg.Executable,
		Args:          cfg.Args,
		Workspace:     outside,
		WorkspaceRoot: workspaceRoot,
	}); err == nil {
		t.Fatal("expected outside workspace rejection")
	}
}

func TestRunApprovalRequiredByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-1")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	script := writeExecutable(t, tmpDir, "fake-codex.sh", `#!/bin/sh
trace_file="${TRACE_FILE}"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-1"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-1"}}}'
      printf '%s\n' '{"id":99,"method":"item/commandExecution/requestApproval","params":{"command":"gh pr view"}}'
      ;;
    *) sleep 1 ;;
  esac
done
`)

	_, err := Run(context.Background(), ClientConfig{
		Executable:    script,
		Workspace:     workspace,
		WorkspaceRoot: workspaceRoot,
		Prompt:        "prompt",
		Title:         "ISS-1: Approval required",
		Env:           append(os.Environ(), "TRACE_FILE="+traceFile),
		ReadTimeout:   2 * time.Second,
		TurnTimeout:   3 * time.Second,
	})
	if err == nil {
		t.Fatal("expected approval required error")
	}
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Kind != "approval_required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunAutoApprovesCommandExecutionWhenNever(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-2")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	script := writeExecutable(t, tmpDir, "fake-codex.sh", `#!/bin/sh
trace_file="${TRACE_FILE}"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-2"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-2"}}}'
      printf '%s\n' '{"id":99,"method":"item/commandExecution/requestApproval","params":{"command":"gh pr view"}}'
      ;;
    5)
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-2","turnId":"turn-2"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`)

	res, err := Run(context.Background(), ClientConfig{
		Executable:     script,
		Workspace:      workspace,
		WorkspaceRoot:  workspaceRoot,
		Prompt:         "prompt",
		Title:          "ISS-2: Auto approve",
		ApprovalPolicy: "never",
		Env:            append(os.Environ(), "TRACE_FILE="+traceFile),
		ReadTimeout:    2 * time.Second,
		TurnTimeout:    3 * time.Second,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.Session == nil || res.Session.SessionID != "thread-2-turn-2" {
		t.Fatalf("unexpected session: %+v", res.Session)
	}

	lines := readTraceLines(t, traceFile)
	foundInit := false
	foundApproval := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 1 {
			foundInit = nestedBool(payload, "params", "capabilities", "experimentalApi")
		}
		if id, ok := asInt(payload["id"]); ok && id == 99 {
			if result, ok := payload["result"].(map[string]interface{}); ok && result["decision"] == "acceptForSession" {
				foundApproval = true
			}
		}
	}
	if !foundInit {
		t.Fatal("expected initialize payload with experimentalApi capability")
	}
	if !foundApproval {
		t.Fatal("expected auto approval response in trace")
	}
}

func TestRunAnswersToolInputAndContinues(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-3")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	script := writeExecutable(t, tmpDir, "fake-codex.sh", `#!/bin/sh
trace_file="${TRACE_FILE}"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-3"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-3"}}}'
      printf '%s\n' '{"id":110,"method":"item/tool/requestUserInput","params":{"questions":[{"id":"options-3","options":[{"label":"Use default"},{"label":"Skip"}]}]}}'
      ;;
    5)
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-3","turnId":"turn-3"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`)

	_, err := Run(context.Background(), ClientConfig{
		Executable:    script,
		Workspace:     workspace,
		WorkspaceRoot: workspaceRoot,
		Prompt:        "prompt",
		Title:         "ISS-3: Tool input",
		Env:           append(os.Environ(), "TRACE_FILE="+traceFile),
		ReadTimeout:   2 * time.Second,
		TurnTimeout:   3 * time.Second,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	foundAnswer := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 110 {
			if answers, ok := nestedMap(payload, "result", "answers"); ok {
				if q, ok := answers["options-3"].(map[string]interface{}); ok {
					if vals, ok := q["answers"].([]interface{}); ok && len(vals) == 1 && vals[0] == nonInteractiveToolInputAnswer {
						foundAnswer = true
					}
				}
			}
		}
	}
	if !foundAnswer {
		t.Fatal("expected generic tool input answer in trace")
	}
}

func TestRunHandlesUnsupportedToolCall(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-4")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	script := writeExecutable(t, tmpDir, "fake-codex.sh", `#!/bin/sh
trace_file="${TRACE_FILE}"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-4"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-4"}}}'
      printf '%s\n' '{"id":120,"method":"item/tool/call","params":{"tool":"missing_tool","arguments":{}}}'
      ;;
    5)
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-4","turnId":"turn-4"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`)

	_, err := Run(context.Background(), ClientConfig{
		Executable:    script,
		Workspace:     workspace,
		WorkspaceRoot: workspaceRoot,
		Prompt:        "prompt",
		Title:         "ISS-4: Tool call",
		Env:           append(os.Environ(), "TRACE_FILE="+traceFile),
		ReadTimeout:   2 * time.Second,
		TurnTimeout:   3 * time.Second,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	foundUnsupported := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 120 {
			if result, ok := payload["result"].(map[string]interface{}); ok && result["success"] == false {
				items, _ := result["contentItems"].([]interface{})
				if len(items) == 1 {
					item, _ := items[0].(map[string]interface{})
					if text, _ := item["text"].(string); strings.Contains(text, "Unsupported dynamic tool") {
						foundUnsupported = true
					}
				}
			}
		}
	}
	if !foundUnsupported {
		t.Fatal("expected unsupported tool response in trace")
	}
}

func TestRunBuffersLargeProtocolLines(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-5")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	script := writeExecutable(t, tmpDir, "fake-codex.sh", `#!/bin/sh
count=0
padding=$(printf '%*s' 1100000 '' | tr ' ' a)
while IFS= read -r _line; do
  count=$((count + 1))
  case "$count" in
    1) printf '{"id":1,"result":{},"padding":"%s"}\n' "$padding" ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-5"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-5"}}}'
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-5","turnId":"turn-5"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`)

	res, err := Run(context.Background(), ClientConfig{
		Executable:    script,
		Workspace:     workspace,
		WorkspaceRoot: workspaceRoot,
		Prompt:        "prompt",
		Title:         "ISS-5: Large lines",
		ReadTimeout:   2 * time.Second,
		TurnTimeout:   3 * time.Second,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.Session == nil || res.Session.SessionID != "thread-5-turn-5" {
		t.Fatalf("unexpected session after large line: %+v", res.Session)
	}
}

func nestedMap(m map[string]interface{}, path ...string) (map[string]interface{}, bool) {
	var cur interface{} = m
	for _, part := range path {
		next, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur = next[part]
	}
	out, ok := cur.(map[string]interface{})
	return out, ok
}

func nestedBool(m map[string]interface{}, path ...string) bool {
	var cur interface{} = m
	for _, part := range path {
		next, ok := cur.(map[string]interface{})
		if !ok {
			return false
		}
		cur = next[part]
	}
	v, _ := cur.(bool)
	return v
}

func TestRunAllowsQuietPeriodsShorterThanStallTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-6")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	script := writeExecutable(t, tmpDir, "fake-codex.sh", `#!/bin/sh
count=0
while IFS= read -r _line; do
  count=$((count + 1))
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-stall-ok"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-stall-ok"}}}'
      sleep 2
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-stall-ok","turnId":"turn-stall-ok"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`)

	res, err := Run(context.Background(), ClientConfig{
		Executable:    script,
		Workspace:     workspace,
		WorkspaceRoot: workspaceRoot,
		Prompt:        "prompt",
		Title:         "ISS-6: Quiet period",
		ReadTimeout:   1 * time.Second,
		TurnTimeout:   5 * time.Second,
		StallTimeout:  3 * time.Second,
	})
	if err != nil {
		t.Fatalf("run failed after short quiet period: %v", err)
	}
	if res.Session == nil || res.Session.SessionID != "thread-stall-ok-turn-stall-ok" {
		t.Fatalf("unexpected session after quiet period: %+v", res.Session)
	}
}

func TestRunFailsWhenQuietPeriodExceedsStallTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-7")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	script := writeExecutable(t, tmpDir, "fake-codex.sh", `#!/bin/sh
count=0
while IFS= read -r _line; do
  count=$((count + 1))
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-stall-fail"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-stall-fail"}}}'
      sleep 4
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-stall-fail","turnId":"turn-stall-fail"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`)

	_, err := Run(context.Background(), ClientConfig{
		Executable:    script,
		Workspace:     workspace,
		WorkspaceRoot: workspaceRoot,
		Prompt:        "prompt",
		Title:         "ISS-7: Stalled",
		ReadTimeout:   1 * time.Second,
		TurnTimeout:   5 * time.Second,
		StallTimeout:  2500 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected stall timeout")
	}
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Kind != "stall_timeout" {
		t.Fatalf("expected stall_timeout, got %v", err)
	}
}

func captureLogs(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	old := slog.Default()
	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})
	return buf
}

func TestRunLogsLifecycleAtInfoWithoutRawStreams(t *testing.T) {
	logs := captureLogs(t, slog.LevelInfo)

	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-6")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	script := writeExecutable(t, tmpDir, "fake-codex.sh", `#!/bin/sh
count=0
while IFS= read -r _line; do
  count=$((count + 1))
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-6"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-6"}}}'
      printf '%s\n' 'plain stderr-ish text'
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-6","turnId":"turn-6"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`)

	_, err := Run(context.Background(), ClientConfig{
		Executable:      script,
		Workspace:       workspace,
		WorkspaceRoot:   workspaceRoot,
		IssueID:         "issue-6",
		IssueIdentifier: "ISS-6",
		Prompt:          "prompt",
		Title:           "ISS-6: Logging",
		ReadTimeout:     2 * time.Second,
		TurnTimeout:     3 * time.Second,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	text := logs.String()
	for _, want := range []string{
		"Codex app-server process started",
		"Codex session initialized",
		"Codex thread started",
		"Codex turn started",
		"Codex turn completed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in logs: %s", want, text)
		}
	}
	if strings.Contains(text, "Codex app-server stream output") {
		t.Fatalf("expected raw stream logs to be hidden at info level: %s", text)
	}
	if !strings.Contains(text, "\"issue_identifier\":\"ISS-6\"") {
		t.Fatalf("expected issue metadata in logs: %s", text)
	}
}

func TestRunLogsRawStreamsAtDebug(t *testing.T) {
	logs := captureLogs(t, slog.LevelDebug)

	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-7")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	script := writeExecutable(t, tmpDir, "fake-codex.sh", `#!/bin/sh
count=0
while IFS= read -r _line; do
  count=$((count + 1))
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-7"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-7"}}}'
      printf '%s\n' 'stderr stream line' >&2
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-7","turnId":"turn-7"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`)

	_, err := Run(context.Background(), ClientConfig{
		Executable:      script,
		Workspace:       workspace,
		WorkspaceRoot:   workspaceRoot,
		IssueID:         "issue-7",
		IssueIdentifier: "ISS-7",
		Prompt:          "prompt",
		Title:           "ISS-7: Debug stream logging",
		ReadTimeout:     2 * time.Second,
		TurnTimeout:     3 * time.Second,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	text := logs.String()
	if !strings.Contains(text, "Codex app-server stream output") {
		t.Fatalf("expected raw stream logs at debug level: %s", text)
	}
	if !strings.Contains(text, "\"stream\":\"stderr\"") {
		t.Fatalf("expected stderr stream metadata: %s", text)
	}
}
