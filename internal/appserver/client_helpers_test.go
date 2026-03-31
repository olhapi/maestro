package appserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver/protocol"
	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
)

type bufferWriteCloser struct {
	bytes.Buffer
}

func (b *bufferWriteCloser) Close() error {
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAwaitResponseBranches(t *testing.T) {
	t.Run("success ignores non protocol lines", func(t *testing.T) {
		client := &Client{
			cfg:     ClientConfig{ReadTimeout: 50 * time.Millisecond},
			lines:   make(chan string, 2),
			lineErr: make(chan error, 1),
			session: &Session{MaxHistory: 4},
			logger:  discardLogger(),
		}
		client.lines <- "plain stdout"
		client.lines <- `{"id":7,"result":{"ok":true}}`

		msg, err := client.awaitResponse(context.Background(), 7)
		if err != nil {
			t.Fatalf("awaitResponse failed: %v", err)
		}
		if string(msg.Result) != `{"ok":true}` {
			t.Fatalf("unexpected response payload %s", msg.Result)
		}
	})

	t.Run("response error", func(t *testing.T) {
		client := &Client{
			cfg:     ClientConfig{ReadTimeout: 50 * time.Millisecond},
			lines:   make(chan string, 1),
			lineErr: make(chan error, 1),
			session: &Session{MaxHistory: 4},
			logger:  discardLogger(),
		}
		client.lines <- `{"id":8,"error":{"code":-1,"message":"boom"}}`

		_, err := client.awaitResponse(context.Background(), 8)
		var runErr *RunError
		if !errors.As(err, &runErr) || runErr.Kind != "response_error" {
			t.Fatalf("expected response_error, got %v", err)
		}
	})

	t.Run("missing result", func(t *testing.T) {
		client := &Client{
			cfg:     ClientConfig{ReadTimeout: 50 * time.Millisecond},
			lines:   make(chan string, 1),
			lineErr: make(chan error, 1),
			session: &Session{MaxHistory: 4},
			logger:  discardLogger(),
		}
		client.lines <- `{"id":9,"result":null}`

		_, err := client.awaitResponse(context.Background(), 9)
		var runErr *RunError
		if !errors.As(err, &runErr) || runErr.Kind != "response_error" || !strings.Contains(runErr.Error(), "missing result") {
			t.Fatalf("expected missing-result response_error, got %v", err)
		}
	})
}

func TestInitializeUsesGeneratedRequestIDForConfiguredWorkspace(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace := filepath.Join(workspaceRoot, "ISS-1")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	stdin := &bufferWriteCloser{}
	client := &Client{
		cfg: ClientConfig{
			ApprovalPolicy:           "never",
			Workspace:                workspace,
			WorkspaceRoot:            workspaceRoot,
			ReadTimeout:              50 * time.Millisecond,
			TurnTimeout:              200 * time.Millisecond,
			StallTimeout:             200 * time.Millisecond,
			ThreadSandbox:            "workspace-write",
			InitialCollaborationMode: "default",
		},
		stdin:               stdin,
		lines:               make(chan string, 2),
		lineErr:             make(chan error, 1),
		session:             &Session{MaxHistory: 4},
		logger:              discardLogger(),
		nextID:              7,
		pendingInteractions: make(map[string]*interactionWaiter),
	}
	client.lines <- `{"id":7,"result":{}}`
	client.lines <- `{"id":8,"result":{"thread":{"id":"thread-1"}}}`

	if err := client.initialize(context.Background()); err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	output := stdin.String()
	if !strings.Contains(output, `"id":7`) {
		t.Fatalf("expected initialize request to use generated id 7, got %q", output)
	}
	if !strings.Contains(output, `"id":8`) {
		t.Fatalf("expected thread/start request to advance to id 8, got %q", output)
	}
}

func TestAwaitTurnCompletionBranches(t *testing.T) {
	tests := []struct {
		name string
		line string
		kind string
	}{
		{
			name: "failed",
			line: `{"method":"turn/failed","params":{"threadId":"thread-1","turn":{"id":"turn-1"}}}`,
			kind: "turn_failed",
		},
		{
			name: "cancelled",
			line: `{"method":"turn/cancelled","params":{"threadId":"thread-2","turn":{"id":"turn-2"}}}`,
			kind: "turn_cancelled",
		},
		{
			name: "input required",
			line: `{"method":"turn/approval_required","params":{"reason":"needs operator"}}`,
			kind: "turn_input_required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{
				cfg: ClientConfig{
					ReadTimeout: 50 * time.Millisecond,
					TurnTimeout: 200 * time.Millisecond,
				},
				lines:   make(chan string, 1),
				lineErr: make(chan error, 1),
				session: &Session{MaxHistory: 4},
				logger:  discardLogger(),
			}
			client.lines <- tc.line

			err := client.awaitTurnCompletion(context.Background())
			var runErr *RunError
			if !errors.As(err, &runErr) || runErr.Kind != tc.kind {
				t.Fatalf("expected %s, got %v", tc.kind, err)
			}
		})
	}
}

func TestAwaitTurnCompletionReturnsRecordedTerminalOutcome(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		wantKind string
	}{
		{
			name:   "completed",
			reason: "turn.completed",
		},
		{
			name:     "failed",
			reason:   "turn.failed",
			wantKind: "turn_failed",
		},
		{
			name:     "cancelled",
			reason:   "turn.cancelled",
			wantKind: "turn_cancelled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{
				cfg: ClientConfig{
					ReadTimeout: 50 * time.Millisecond,
					TurnTimeout: 200 * time.Millisecond,
				},
				lines:   make(chan string),
				lineErr: make(chan error, 1),
				waitCh:  make(chan error, 1),
				session: &Session{
					ThreadID:       "thread-terminal",
					TurnID:         "turn-terminal",
					Terminal:       true,
					TerminalReason: tc.reason,
					MaxHistory:     4,
				},
				logger: discardLogger(),
			}
			close(client.lines)
			client.lineErr <- io.EOF

			err := client.awaitTurnCompletion(context.Background())
			if tc.wantKind == "" {
				if err != nil {
					t.Fatalf("expected nil error for %s, got %v", tc.reason, err)
				}
				return
			}

			var runErr *RunError
			if !errors.As(err, &runErr) || runErr.Kind != tc.wantKind {
				t.Fatalf("expected %s, got %v", tc.wantKind, err)
			}
		})
	}
}

func TestAwaitTurnCompletionTreatsCleanEOFAsCompletion(t *testing.T) {
	client := &Client{
		cfg: ClientConfig{
			ReadTimeout: 50 * time.Millisecond,
			TurnTimeout: 200 * time.Millisecond,
		},
		lines:   make(chan string),
		lineErr: make(chan error, 1),
		waitCh:  make(chan error, 1),
		session: &Session{ThreadID: "thread-eof", TurnID: "turn-eof", MaxHistory: 4},
		logger:  discardLogger(),
	}
	close(client.lines)
	client.lineErr <- io.EOF
	client.waitCh <- nil

	if err := client.awaitTurnCompletion(context.Background()); err != nil {
		t.Fatalf("expected clean EOF to be treated as completion, got %v", err)
	}
}

func TestAwaitTurnCompletionWaitsBrieflyForCleanExitAfterEOF(t *testing.T) {
	client := &Client{
		cfg: ClientConfig{
			ReadTimeout: 50 * time.Millisecond,
			TurnTimeout: 200 * time.Millisecond,
		},
		lines:   make(chan string),
		lineErr: make(chan error, 1),
		waitCh:  make(chan error, 1),
		session: &Session{ThreadID: "thread-eof-race", TurnID: "turn-eof-race", MaxHistory: 4},
		logger:  discardLogger(),
	}
	close(client.lines)
	client.lineErr <- io.EOF
	go func() {
		time.Sleep(10 * time.Millisecond)
		client.waitCh <- nil
	}()

	if err := client.awaitTurnCompletion(context.Background()); err != nil {
		t.Fatalf("expected delayed clean EOF to be treated as completion, got %v", err)
	}
}

func TestAwaitTurnCompletionWaitsForSlowerCleanExitAfterEOF(t *testing.T) {
	client := &Client{
		cfg: ClientConfig{
			ReadTimeout: 50 * time.Millisecond,
			TurnTimeout: 500 * time.Millisecond,
		},
		lines:   make(chan string),
		lineErr: make(chan error, 1),
		waitCh:  make(chan error, 1),
		session: &Session{ThreadID: "thread-eof-slow", TurnID: "turn-eof-slow", MaxHistory: 4},
		logger:  discardLogger(),
	}
	close(client.lines)
	client.lineErr <- io.EOF
	go func() {
		time.Sleep(150 * time.Millisecond)
		client.waitCh <- nil
	}()

	if err := client.awaitTurnCompletion(context.Background()); err != nil {
		t.Fatalf("expected slower clean EOF to be treated as completion, got %v", err)
	}
}

func TestAwaitTurnCompletionLimitsCleanExitWaitToRemainingTimeout(t *testing.T) {
	client := &Client{
		cfg: ClientConfig{
			ReadTimeout: 50 * time.Millisecond,
			TurnTimeout: 100 * time.Millisecond,
		},
		lines:   make(chan string),
		lineErr: make(chan error, 1),
		waitCh:  make(chan error, 1),
		session: &Session{ThreadID: "thread-eof-timeout", TurnID: "turn-eof-timeout", MaxHistory: 4},
		logger:  discardLogger(),
	}
	close(client.lines)
	client.lineErr <- io.EOF
	go func() {
		time.Sleep(150 * time.Millisecond)
		client.waitCh <- nil
	}()

	start := time.Now()
	if err := client.awaitTurnCompletion(context.Background()); err == nil {
		t.Fatal("expected late clean exit to respect the turn timeout")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("expected EOF fallback to stop near the turn timeout, got %s", elapsed)
	}
}

func TestHandleRequestAutoApprovalAndToolExecution(t *testing.T) {
	makeClient := func() (*Client, *bufferWriteCloser) {
		stdin := &bufferWriteCloser{}
		client := &Client{
			cfg: ClientConfig{
				ApprovalPolicy: "never",
				Logger:         discardLogger(),
				ToolExecutor: func(_ context.Context, name string, arguments interface{}) map[string]interface{} {
					return map[string]interface{}{
						"success": true,
						"contentItems": []map[string]interface{}{
							{"type": "inputText", "text": encodeToolPayload(map[string]interface{}{"tool": name, "arguments": arguments})},
						},
					}
				},
			},
			stdin:   stdin,
			session: &Session{MaxHistory: 4},
		}
		client.logger = client.newLogger()
		return client, stdin
	}

	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "file change approval",
			line: `{"id":21,"method":"item/fileChange/requestApproval","params":{}}`,
			want: "acceptForSession",
		},
		{
			name: "exec command approval",
			line: `{"id":22,"method":"execCommandApproval","params":{}}`,
			want: "approved_for_session",
		},
		{
			name: "apply patch approval",
			line: `{"id":23,"method":"applyPatchApproval","params":{}}`,
			want: "approved_for_session",
		},
		{
			name: "tool call",
			line: `{"id":24,"method":"item/tool/call","params":{"tool":"echo_tool","arguments":{"value":7}}}`,
			want: `\"tool\":\"echo_tool\"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, stdin := makeClient()
			msg, ok := protocol.DecodeMessage(tc.line)
			if !ok {
				t.Fatalf("failed to decode test message %q", tc.line)
			}
			handled, err := client.handleRequest(context.Background(), msg)
			if err != nil {
				t.Fatalf("handleRequest failed: %v", err)
			}
			if !handled {
				t.Fatal("expected request to be handled")
			}
			if !strings.Contains(stdin.String(), tc.want) {
				t.Fatalf("expected %q in encoded response %q", tc.want, stdin.String())
			}
		})
	}
}

func TestInputAnswerAndVersionHelpers(t *testing.T) {
	answers, autoApproved := answersForToolInput(map[string]interface{}{
		"questions": []interface{}{
			map[string]interface{}{
				"id": "approval",
				"options": []interface{}{
					map[string]interface{}{"label": "Approve once"},
					map[string]interface{}{"label": "Approve this session"},
				},
			},
		},
	}, true)
	if !autoApproved {
		t.Fatal("expected autoApproved result")
	}
	if got := answers["approval"].(map[string]interface{})["answers"].([]string)[0]; got != "Approve this session" {
		t.Fatalf("unexpected auto approval label %q", got)
	}
	if answers, ok := answersForToolInput(map[string]interface{}{"questions": []interface{}{map[string]interface{}{}}}, false); answers != nil || ok {
		t.Fatalf("expected invalid tool input questions to fail, got %#v %v", answers, ok)
	}

	typedAnswers, autoApproved := answersForToolInputParams(gen.ToolRequestUserInputParams{
		Questions: []gen.ToolRequestUserInputQuestion{
			{
				ID: "q1",
				Options: []gen.ToolRequestUserInputOption{
					{Label: "Allow once"},
				},
			},
		},
	}, true)
	if !autoApproved || typedAnswers["q1"].Answers[0] != "Allow once" {
		t.Fatalf("unexpected typed answers %#v auto=%v", typedAnswers, autoApproved)
	}
	if answers, ok := answersForToolInputParams(gen.ToolRequestUserInputParams{
		Questions: []gen.ToolRequestUserInputQuestion{{ID: " "}},
	}, false); answers != nil || ok {
		t.Fatalf("expected invalid typed tool input to fail, got %#v %v", answers, ok)
	}

	msg, ok := protocol.DecodeMessage(`{"method":"thread/started","params":{"thread":{"id":"thread-3"}}}`)
	if !ok {
		t.Fatal("expected thread started message decode")
	}
	evt, ok := EventFromMessage(msg)
	if !ok || evt.ThreadID != "thread-3" || evt.Type != "thread.started" {
		t.Fatalf("unexpected thread event %+v ok=%v", evt, ok)
	}
	if evt, ok := EventFromMessage(protocol.Message{Method: "unknown"}); ok || evt.Type != "" {
		t.Fatalf("expected unknown protocol message to be ignored, got %+v ok=%v", evt, ok)
	}

	if got := approvalOptionLabel([]interface{}{map[string]interface{}{"label": "Allow command"}}); got != "Allow command" {
		t.Fatalf("unexpected approval fallback %q", got)
	}
	if approvalOptionLabel([]interface{}{"skip"}) != "" {
		t.Fatal("expected invalid approval options to be ignored")
	}
	if got := approvalOptionLabelFromQuestions([]gen.ToolRequestUserInputOption{{Label: "Approve once"}}); got != "Approve once" {
		t.Fatalf("unexpected question approval label %q", got)
	}
	if value, ok := asInt(int64(5)); !ok || value != 5 {
		t.Fatalf("expected int64 conversion, got %d %v", value, ok)
	}
	if value, ok := asInt(float64(6)); !ok || value != 6 {
		t.Fatalf("expected float64 conversion, got %d %v", value, ok)
	}
	if toolCallName(map[string]interface{}{"tool": " demo_tool "}) != "demo_tool" {
		t.Fatal("expected toolCallName to prefer tool key")
	}
	if args := toolCallArguments(map[string]interface{}{}); len(args.(map[string]interface{})) != 0 {
		t.Fatalf("expected empty args map, got %#v", args)
	}
	if names := supportedToolNames([]map[string]interface{}{{"name": " one "}, {"name": "two"}}); len(names) != 2 || names[0] != "one" || names[1] != "two" {
		t.Fatalf("unexpected supported tool names %#v", names)
	}
	contentItems := unsupportedToolResult("missing", []string{"one"})["contentItems"].([]map[string]interface{})
	text, _ := contentItems[0]["text"].(string)
	if !strings.Contains(text, `"supportedTools":["one"]`) {
		t.Fatal("expected unsupported tool payload to include supported names")
	}
	if got := encodeToolPayload(make(chan int)); !strings.Contains(got, "0x") {
		t.Fatalf("expected fallback stringification for unsupported payload, got %q", got)
	}
	if _, ok := decodeJSONObject(`{"id":1,"result":{}}`); !ok {
		t.Fatal("expected decodeJSONObject to accept protocol JSON")
	}
	if codexExecutableFromCommand("") != "" || codexExecutableFromCommand("/bin/sh -lc echo") != "" {
		t.Fatal("expected non-codex commands to be ignored")
	}
	if codexExecutableFromCommand("codex app-server") != "codex" {
		t.Fatal("expected codex executable extraction")
	}
	if parseCodexVersion([]byte("codex-cli version unknown")) != "" {
		t.Fatal("expected parseCodexVersion to reject invalid output")
	}
}
