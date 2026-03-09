package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
)

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

func helperClientConfig(t *testing.T, workspace, workspaceRoot string, scenario fakeappserver.Scenario) (ClientConfig, func(string)) {
	t.Helper()
	cfg := fakeappserver.NewConfig(t, scenario)
	t.Cleanup(cfg.Close)
	return ClientConfig{
		Executable:    cfg.Executable,
		Args:          cfg.Args,
		Env:           cfg.Env,
		Workspace:     workspace,
		WorkspaceRoot: workspaceRoot,
		Prompt:        "prompt",
		Title:         "test turn",
		ReadTimeout:   2 * time.Second,
		TurnTimeout:   3 * time.Second,
	}, cfg.Release
}

func withTrace(cfg ClientConfig, traceFile string) ClientConfig {
	cfg.Env = append(cfg.Env, "TRACE_FILE="+traceFile)
	return cfg
}

func baseScenario(threadID, turnID string, afterTurnStart ...fakeappserver.Output) fakeappserver.Scenario {
	return fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": threadID}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: append([]fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": turnID}}},
				}}, afterTurnStart...),
			},
		},
	}
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
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, baseScenario("thread-1", "turn-1",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     99,
				"method": "item/commandExecution/requestApproval",
				"params": map[string]interface{}{"command": "gh pr view"},
			},
		},
	))
	cfg.Title = "ISS-1: Approval required"
	cfg = withTrace(cfg, traceFile)

	_, err := Run(context.Background(), cfg)
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
	scenario := baseScenario("thread-2", "turn-2",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     99,
				"method": "item/commandExecution/requestApproval",
				"params": map[string]interface{}{"command": "gh pr view"},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(99)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-2", "turnId": "turn-2"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-2: Auto approve"
	cfg.ApprovalPolicy = "never"
	cfg = withTrace(cfg, traceFile)

	res, err := Run(context.Background(), cfg)
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
	scenario := baseScenario("thread-3", "turn-3",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     110,
				"method": "item/tool/requestUserInput",
				"params": map[string]interface{}{
					"questions": []map[string]interface{}{{
						"id": "options-3",
						"options": []map[string]interface{}{
							{"label": "Use default"},
							{"label": "Skip"},
						},
					}},
				},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(110)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-3", "turnId": "turn-3"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-3: Tool input"
	cfg = withTrace(cfg, traceFile)

	_, err := Run(context.Background(), cfg)
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
	scenario := baseScenario("thread-4", "turn-4",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     120,
				"method": "item/tool/call",
				"params": map[string]interface{}{"tool": "missing_tool", "arguments": map[string]interface{}{}},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(120)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-4", "turnId": "turn-4"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-4: Tool call"
	cfg = withTrace(cfg, traceFile)

	_, err := Run(context.Background(), cfg)
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
	padding := strings.Repeat("a", 1_100_000)
	scenario := baseScenario("thread-5", "turn-5",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-5", "turnId": "turn-5"},
			},
		},
	)
	scenario.Steps[0].Emit = []fakeappserver.Output{{
		JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}, "padding": padding},
	}}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-5: Large lines"
	res, err := Run(context.Background(), cfg)
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
	scenario := baseScenario("thread-stall-ok", "turn-stall-ok")
	scenario.Steps[3].WaitForRelease = "complete"
	scenario.Steps[3].EmitAfterRelease = []fakeappserver.Output{{
		JSON: map[string]interface{}{
			"method": "turn/completed",
			"params": map[string]interface{}{"threadId": "thread-stall-ok", "turnId": "turn-stall-ok"},
		},
	}}
	scenario.Steps[3].ExitCode = fakeappserver.Int(0)
	cfg, release := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-6: Quiet period"
	cfg.ReadTimeout = 1 * time.Second
	cfg.TurnTimeout = 5 * time.Second
	cfg.StallTimeout = 3 * time.Second
	go func() {
		time.Sleep(2 * time.Second)
		release("complete")
	}()
	res, err := Run(context.Background(), cfg)
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
	scenario := baseScenario("thread-stall-fail", "turn-stall-fail")
	scenario.Steps[3].WaitForRelease = "never"
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-7: Stalled"
	cfg.ReadTimeout = 1 * time.Second
	cfg.TurnTimeout = 5 * time.Second
	cfg.StallTimeout = 2500 * time.Millisecond
	_, err := Run(context.Background(), cfg)
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
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, baseScenario("thread-6", "turn-6",
		fakeappserver.Output{Text: "plain stderr-ish text"},
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-6", "turnId": "turn-6"},
			},
		},
	))
	cfg.IssueID = "issue-6"
	cfg.IssueIdentifier = "ISS-6"
	cfg.Title = "ISS-6: Logging"
	_, err := Run(context.Background(), cfg)
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
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, baseScenario("thread-7", "turn-7",
		fakeappserver.Output{Stream: "stderr", Text: "stderr stream line"},
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-7", "turnId": "turn-7"},
			},
		},
	))
	cfg.IssueID = "issue-7"
	cfg.IssueIdentifier = "ISS-7"
	cfg.Title = "ISS-7: Debug stream logging"
	_, err := Run(context.Background(), cfg)
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

func TestHelperDefaultsAndWorkspaceValidation(t *testing.T) {
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(root, "ISS-1")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	if err := validateWorkspaceCWD(workspace, root); err != nil {
		t.Fatalf("expected valid workspace cwd, got %v", err)
	}

	policy := defaultApprovalPolicy()
	reject, ok := policy["reject"].(map[string]interface{})
	if !ok || reject["sandbox_approval"] != true || reject["rules"] != true {
		t.Fatalf("unexpected default approval policy: %#v", policy)
	}

	sandbox := defaultTurnSandboxPolicy(workspace)
	if sandbox["type"] != "workspaceWrite" {
		t.Fatalf("unexpected sandbox type: %#v", sandbox)
	}
	roots, ok := sandbox["writableRoots"].([]string)
	if !ok || len(roots) != 1 || roots[0] == "" {
		t.Fatalf("unexpected writable roots: %#v", sandbox)
	}

	if !looksLikeCodexCommand("/usr/local/bin/codex") || !looksLikeCodexCommand("C:/tools/codex.exe") {
		t.Fatal("expected codex executable detection")
	}
	if looksLikeCodexCommand("/bin/sh") {
		t.Fatal("expected non-codex executable to be rejected")
	}
}

func TestRunErrorAndInputHelpers(t *testing.T) {
	baseErr := errors.New("boom")
	runErr := &RunError{Kind: "turn_failed", Err: baseErr}
	if runErr.Error() != "turn_failed: boom" {
		t.Fatalf("unexpected run error string: %q", runErr.Error())
	}
	if !errors.Is(runErr.Unwrap(), baseErr) {
		t.Fatalf("expected unwrap to return base error: %v", runErr.Unwrap())
	}
	if (&RunError{Kind: "approval_required"}).Error() != "approval_required" {
		t.Fatal("expected bare kind error string")
	}

	if !needsInput("turn/approval_required", nil) {
		t.Fatal("expected turn approval to require input")
	}
	if !needsInput("", map[string]interface{}{"requiresInput": true}) {
		t.Fatal("expected requiresInput field to require input")
	}
	if !needsInput("", map[string]interface{}{"params": map[string]interface{}{"type": "input_required"}}) {
		t.Fatal("expected nested input type to require input")
	}
	if needsInput("", map[string]interface{}{"params": map[string]interface{}{"type": "notice"}}) {
		t.Fatal("expected non-input payload to be ignored")
	}

	args := toolCallArguments(map[string]interface{}{"arguments": map[string]interface{}{"value": 1}})
	if args.(map[string]interface{})["value"].(int) != 1 {
		t.Fatalf("unexpected tool call args: %#v", args)
	}
	if label := approvalOptionLabel([]interface{}{
		map[string]interface{}{"label": "Approve once"},
		map[string]interface{}{"label": "Approve this session"},
	}); label != "Approve this session" {
		t.Fatalf("unexpected approval label: %q", label)
	}
}

func TestClientHelperMethodsUpdateSessionAndMessages(t *testing.T) {
	var (
		updates []*Session
		msgs    []map[string]interface{}
	)
	client := &Client{
		cfg: ClientConfig{
			Workspace: "/tmp/work",
			OnSessionUpdate: func(session *Session) {
				updates = append(updates, session)
			},
			OnMessage: func(msg map[string]interface{}) {
				msgs = append(msgs, msg)
			},
		},
		session: &Session{MaxHistory: 4},
		waitCh:  make(chan error, 1),
		lineErr: make(chan error, 1),
	}
	client.logger = client.newLogger()

	client.applyEvent(Event{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1", Message: "started"})
	client.applyEvent(Event{Type: "turn.completed", Message: "done", TotalTokens: 12})
	client.emitMessage("session_started", map[string]interface{}{"session_id": "thread-1-turn-1"})

	if len(updates) != 2 {
		t.Fatalf("expected session updates, got %d", len(updates))
	}
	if updates[1].SessionID != "thread-1-turn-1" || !updates[1].Terminal {
		t.Fatalf("unexpected copied session: %+v", updates[1])
	}
	if len(msgs) != 1 || msgs[0]["event"] != "session_started" {
		t.Fatalf("unexpected emitted messages: %#v", msgs)
	}

	client.waitCh <- nil
	client.lineErr <- io.EOF
	if err := client.Wait(); err != nil {
		t.Fatalf("expected nil wait error, got %v", err)
	}

	client.waitCh = make(chan error, 1)
	client.lineErr = make(chan error, 1)
	client.waitCh <- nil
	client.lineErr <- errors.New("read failed")
	if err := client.Wait(); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("expected read failure, got %v", err)
	}
}

func TestParseEventLineVariantsAndSessionHelpers(t *testing.T) {
	line := `{"event":{"event_type":"turn.completed","threadId":"thread-2","turnId":"turn-2","usage":{"prompt_tokens":"4","completion_tokens":5,"total_tokens":9},"message":"done"}}`
	evt, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected event to parse")
	}
	if evt.Type != "turn.completed" || evt.ThreadID != "thread-2" || evt.TurnID != "turn-2" {
		t.Fatalf("unexpected parsed event: %+v", evt)
	}
	if evt.InputTokens != 4 || evt.OutputTokens != 5 || evt.TotalTokens != 9 {
		t.Fatalf("unexpected token totals: %+v", evt)
	}

	if decoded, ok := decodeJSONObject(`{"ok":true}`); !ok || decoded["ok"] != true {
		t.Fatalf("expected decodeJSONObject success, got %#v %v", decoded, ok)
	}
	if _, ok := decodeJSONObject("not-json"); ok {
		t.Fatal("expected non-json line to be ignored")
	}

	if toolCallName(map[string]interface{}{"name": " demo "}) != "demo" {
		t.Fatal("expected toolCallName to trim whitespace")
	}
	if got := toolCallArguments(nil); got == nil {
		t.Fatal("expected default toolCallArguments map")
	}
	names := supportedToolNames([]map[string]interface{}{{"name": " one "}, {"name": ""}, {"skip": "x"}})
	if len(names) != 1 || names[0] != "one" {
		t.Fatalf("unexpected supported tools: %#v", names)
	}
}
