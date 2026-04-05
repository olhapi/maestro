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

	"github.com/olhapi/maestro/internal/appserver/protocol"
	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
)

func TestNormalizePendingUserInputAnswerBranches(t *testing.T) {
	question := PendingUserInputQuestion{
		ID: "question-1",
		Options: []PendingUserInputOption{
			{Label: "Alpha"},
			{Label: "Beta"},
		},
	}

	if _, err := normalizePendingUserInputAnswer(question, nil); !errors.Is(err, ErrInvalidInteractionResponse) {
		t.Fatalf("expected missing answer error, got %v", err)
	}
	if _, err := normalizePendingUserInputAnswer(question, []string{"   "}); !errors.Is(err, ErrInvalidInteractionResponse) {
		t.Fatalf("expected blank answer error, got %v", err)
	}
	if got, err := normalizePendingUserInputAnswer(question, []string{" Alpha "}); err != nil || got != "Alpha" {
		t.Fatalf("expected option label normalization, got %q err=%v", got, err)
	}
	if got, err := normalizePendingUserInputAnswer(PendingUserInputQuestion{ID: "question-2", IsOther: true}, []string{" free form "}); err != nil || got != " free form " {
		t.Fatalf("expected other answer passthrough, got %q err=%v", got, err)
	}
	if _, err := normalizePendingUserInputAnswer(question, []string{"Gamma"}); !errors.Is(err, ErrInvalidInteractionResponse) {
		t.Fatalf("expected unsupported answer error, got %v", err)
	}
}

func TestInteractionApprovalDecisionHelpers(t *testing.T) {
	custom := PendingInteraction{
		Approval: &PendingApproval{
			Decisions: []PendingApprovalDecision{{Value: "custom", Label: "Custom"}},
		},
	}
	if got := interactionApprovalDecisions(custom); len(got) != 1 || got[0].Value != "custom" {
		t.Fatalf("expected custom approval decisions to win, got %#v", got)
	}

	commandDecisions := interactionApprovalDecisions(PendingInteraction{Method: protocol.MethodItemCommandExecutionApproval})
	if len(commandDecisions) != 4 || commandDecisions[0].Label != "Accept once" || commandDecisions[1].Label != "Accept for session" {
		t.Fatalf("unexpected command execution decisions: %#v", commandDecisions)
	}
	fileChangeDecisions := interactionApprovalDecisions(PendingInteraction{Method: protocol.MethodItemFileChangeApproval})
	if len(fileChangeDecisions) != 4 || fileChangeDecisions[0].Label != "Accept once" || fileChangeDecisions[1].Label != "Accept for session" {
		t.Fatalf("unexpected file change decisions: %#v", fileChangeDecisions)
	}
	execDecisions := interactionApprovalDecisions(PendingInteraction{Method: protocol.MethodExecCommandApproval})
	if len(execDecisions) != 4 || execDecisions[0].Label != "Approve once" || execDecisions[1].Label != "Approve for session" {
		t.Fatalf("unexpected exec command decisions: %#v", execDecisions)
	}

	patchDecisions := applyPatchApprovalDecisions("")
	if len(patchDecisions) != 4 || patchDecisions[1].Label != "Approve for session" {
		t.Fatalf("unexpected patch decisions without grant root: %#v", patchDecisions)
	}

	grantedPatchDecisions := applyPatchApprovalDecisions("/repo")
	if grantedPatchDecisions[1].Label != "Approve and grant root" || !strings.Contains(grantedPatchDecisions[1].Description, "/repo") {
		t.Fatalf("expected grant-root label and description, got %#v", grantedPatchDecisions[1])
	}

	if got := interactionApprovalDecisions(PendingInteraction{Method: protocol.MethodApplyPatchApproval}); len(got) != 4 {
		t.Fatalf("expected apply-patch approvals to reuse review decisions, got %#v", got)
	}
}

func TestMessageAndTokenHelpers(t *testing.T) {
	if got := extractMessageValue(nil); got != "" {
		t.Fatalf("expected nil message value to be empty, got %q", got)
	}
	if got := extractMessageValue("  ready  "); got != "ready" {
		t.Fatalf("expected trimmed string message, got %q", got)
	}
	if got := extractMessageValue(map[string]interface{}{"message": "  hello "}); got != "  hello " {
		t.Fatalf("expected message map to be extracted, got %q", got)
	}
	if got := extractMessageValue([]interface{}{" first ", map[string]interface{}{"text": "second"}}); got != "first second" {
		t.Fatalf("expected slice messages to join, got %q", got)
	}

	if input, output, total := tokenUsageTotals(2, 3, 0); input != 2 || output != 3 || total != 5 {
		t.Fatalf("expected derived totals, got (%d, %d, %d)", input, output, total)
	}
	if input, output, total := tokenUsageTotals(2, 3, 9); input != 2 || output != 3 || total != 9 {
		t.Fatalf("expected explicit totals to win, got (%d, %d, %d)", input, output, total)
	}

	tests := []struct {
		name string
		in   map[string]interface{}
		want int
	}{
		{name: "float", in: map[string]interface{}{"value": float64(7)}, want: 7},
		{name: "int", in: map[string]interface{}{"value": 8}, want: 8},
		{name: "int64", in: map[string]interface{}{"value": int64(9)}, want: 9},
		{name: "string", in: map[string]interface{}{"value": "10"}, want: 10},
		{name: "missing", in: map[string]interface{}{}, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ptr := firstIntPtr(tc.in, "value")
			if tc.want == 0 {
				if ptr != nil {
					t.Fatalf("expected nil pointer for %s, got %d", tc.name, *ptr)
				}
				return
			}
			if ptr == nil || *ptr != tc.want {
				t.Fatalf("expected %d for %s, got %#v", tc.want, tc.name, ptr)
			}
		})
	}
}

func TestClientSummaryAndDecisionHelpers(t *testing.T) {
	var nilRunErr *RunError
	if nilRunErr.Unwrap() != nil {
		t.Fatal("expected nil run error to unwrap to nil")
	}
	runErr := &RunError{Kind: "response_error", Err: errors.New("base")}
	if got := runErr.Unwrap(); got == nil || got.Error() != "base" {
		t.Fatalf("expected unwrap to return base error, got %v", got)
	}

	restartClient := &Client{session: &Session{ThreadID: "thread-1"}}
	if !restartClient.shouldRestartTurnWithFreshThread(&RunError{
		Kind: "response_error",
		Payload: map[string]interface{}{
			"error": map[string]interface{}{"message": "Thread not found"},
		},
	}) {
		t.Fatal("expected payload error to trigger restart")
	}
	if !restartClient.shouldRestartTurnWithFreshThread(&RunError{Kind: "response_error", Err: errors.New("thread not found")}) {
		t.Fatal("expected error text to trigger restart")
	}
	if restartClient.shouldRestartTurnWithFreshThread(&RunError{Kind: "other", Err: errors.New("thread not found")}) {
		t.Fatal("expected non-response errors not to trigger restart")
	}
	if (&Client{session: &Session{}}).shouldRestartTurnWithFreshThread(&RunError{Kind: "response_error", Err: errors.New("thread not found")}) {
		t.Fatal("expected missing thread to prevent restart")
	}

	if got := resolvedApprovalEventType(protocol.MethodItemCommandExecutionApproval); got != "item.commandExecution.approvalResolved" {
		t.Fatalf("unexpected command approval event type %q", got)
	}
	if got := resolvedApprovalEventType(protocol.MethodItemFileChangeApproval); got != "item.fileChange.approvalResolved" {
		t.Fatalf("unexpected file change approval event type %q", got)
	}
	if got := resolvedApprovalEventType(protocol.MethodExecCommandApproval); got != "execCommandApproval.resolved" {
		t.Fatalf("unexpected exec command approval event type %q", got)
	}
	if got := resolvedApprovalEventType(protocol.MethodApplyPatchApproval); got != "applyPatchApproval.resolved" {
		t.Fatalf("unexpected apply patch approval event type %q", got)
	}
	if got := resolvedApprovalEventType("unknown"); got != "approval.resolved" {
		t.Fatalf("unexpected default approval event type %q", got)
	}

	if got := pendingInteractionSummary(PendingInteraction{Kind: PendingInteractionKindApproval}); got != "Operator approval required." {
		t.Fatalf("unexpected approval summary %q", got)
	}
	if got := pendingInteractionSummary(PendingInteraction{Kind: PendingInteractionKindApproval, Approval: &PendingApproval{Command: "  gh pr view  "}}); got != "gh pr view" {
		t.Fatalf("unexpected approval command summary %q", got)
	}
	if got := pendingInteractionSummary(PendingInteraction{Kind: PendingInteractionKindApproval, Approval: &PendingApproval{Reason: "  review needed  "}}); got != "review needed" {
		t.Fatalf("unexpected approval reason summary %q", got)
	}
	if got := pendingInteractionSummary(PendingInteraction{Kind: PendingInteractionKindUserInput}); got != "Operator input required." {
		t.Fatalf("unexpected user input summary %q", got)
	}
	if got := pendingInteractionSummary(PendingInteraction{Kind: PendingInteractionKindUserInput, UserInput: &PendingUserInput{Questions: []PendingUserInputQuestion{{Question: "  Choose option  "}}}}); got != "Choose option" {
		t.Fatalf("unexpected user input question summary %q", got)
	}
	if got := pendingInteractionSummary(PendingInteraction{Kind: PendingInteractionKindUserInput, UserInput: &PendingUserInput{Questions: []PendingUserInputQuestion{{Header: "  Header  "}}}}); got != "Header" {
		t.Fatalf("unexpected user input header summary %q", got)
	}
	if got := pendingInteractionSummary(PendingInteraction{Kind: PendingInteractionKindElicitation}); got != "MCP elicitation required." {
		t.Fatalf("unexpected elicitation summary %q", got)
	}
	if got := pendingInteractionSummary(PendingInteraction{Kind: PendingInteractionKindElicitation, Elicitation: &PendingElicitation{Message: "  Provide details  "}}); got != "Provide details" {
		t.Fatalf("unexpected elicitation message summary %q", got)
	}
	if got := pendingInteractionSummary(PendingInteraction{Kind: PendingInteractionKindElicitation, Elicitation: &PendingElicitation{URL: "  https://example.com  "}}); got != "https://example.com" {
		t.Fatalf("unexpected elicitation URL summary %q", got)
	}

	if got := firstNonEmptyInteractionValue("  ", "", " ready "); got != "ready" {
		t.Fatalf("unexpected first non-empty value %q", got)
	}
	if got := toolCallName(nil); got != "" {
		t.Fatalf("unexpected nil tool name %q", got)
	}
	if got := toolCallName(map[string]interface{}{"tool": "  search  "}); got != "search" {
		t.Fatalf("unexpected tool field name %q", got)
	}
	if got := toolCallName(map[string]interface{}{"name": "  lookup  "}); got != "lookup" {
		t.Fatalf("unexpected name field value %q", got)
	}

	if got, err := jsonMapFromValue(nil); err != nil || got != nil {
		t.Fatalf("expected nil value to stay nil, got %#v err=%v", got, err)
	}
	mapped, err := jsonMapFromValue(struct {
		Name string `json:"name"`
	}{Name: "alpha"})
	if err != nil {
		t.Fatalf("jsonMapFromValue struct: %v", err)
	}
	if mapped["name"] != "alpha" {
		t.Fatalf("unexpected mapped value %#v", mapped)
	}
	rawMapped, err := jsonMapFromRawMessage(json.RawMessage(`{"type":"object","properties":{"profile":{"type":"object","properties":{"name":{"type":"string"}}}}}`))
	if err != nil {
		t.Fatalf("jsonMapFromRawMessage object: %v", err)
	}
	rawProperties, ok := rawMapped["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected raw properties to decode, got %#v", rawMapped["properties"])
	}
	rawProfile, ok := rawProperties["profile"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested raw property to decode, got %#v", rawProperties["profile"])
	}
	if _, ok := rawProfile["properties"].(map[string]interface{}); !ok {
		t.Fatalf("expected nested raw schema to retain properties, got %#v", rawProfile["properties"])
	}
	if got, err := jsonMapFromRawMessage(nil); err != nil || got != nil {
		t.Fatalf("expected nil raw schema to stay nil, got %#v err=%v", got, err)
	}
	if _, err := jsonMapFromRawMessage(json.RawMessage(`"oops"`)); err == nil {
		t.Fatal("expected non-object raw schema to fail")
	}

	if err := CleanupLingeringAppServerProcess(0); err != nil {
		t.Fatalf("expected zero pid cleanup to be a no-op, got %v", err)
	}
}

func TestDecodeThreadResponseBranches(t *testing.T) {
	threadID, err := decodeThreadResponse(protocol.Message{
		Result: json.RawMessage(`{"thread":{"id":"thread-1"}}`),
	})
	if err != nil {
		t.Fatalf("decodeThreadResponse success: %v", err)
	}
	if threadID != "thread-1" {
		t.Fatalf("unexpected decoded thread ID %q", threadID)
	}
	if _, err := decodeThreadResponse(protocol.Message{Result: json.RawMessage(`{"thread":{}}`)}); err == nil {
		t.Fatal("expected missing thread id to fail")
	}
}

func TestClientParsingAndInteractionHelperBranches(t *testing.T) {
	var nilRunErr *RunError
	if nilRunErr.Error() != "" || nilRunErr.Unwrap() != nil {
		t.Fatal("expected nil run error helpers to be empty")
	}
	if got := (&RunError{Kind: "response_error"}).Error(); got != "response_error" {
		t.Fatalf("unexpected kind-only error string %q", got)
	}

	if params, ok := messageParamsMap(protocol.Message{}); ok || params != nil {
		t.Fatalf("expected empty message params to be ignored, got %#v", params)
	}
	if params, ok := messageParamsMap(protocol.Message{Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1"}`)}); !ok || params["threadId"] != "thread-1" {
		t.Fatalf("unexpected decoded params %#v ok=%v", params, ok)
	}
	if _, ok := messageParamsMap(protocol.Message{Params: json.RawMessage(`{`)}); ok {
		t.Fatal("expected invalid JSON params to fail")
	}

	if got := requestIDString(protocol.Message{}); got != "" {
		t.Fatalf("expected missing request id to be empty, got %q", got)
	}
	if got := requestIDString(protocol.Message{ID: json.RawMessage(`"req-1"`)}); got != "req-1" {
		t.Fatalf("unexpected string request id %q", got)
	}
	if got := requestIDString(protocol.Message{ID: json.RawMessage(`42`)}); got != "42" {
		t.Fatalf("unexpected numeric request id %q", got)
	}
	if got := requestIDString(protocol.Message{ID: json.RawMessage(`bad`)}); got != "bad" {
		t.Fatalf("expected invalid request id JSON to fall back to raw text, got %q", got)
	}

	if got := firstNonEmptyInteractionValue(" ", "", " ready "); got != "ready" {
		t.Fatalf("unexpected first non-empty value %q", got)
	}

	approval := PendingInteraction{
		Kind:   PendingInteractionKindApproval,
		Method: protocol.MethodExecCommandApproval,
		Approval: &PendingApproval{
			Decisions: []PendingApprovalDecision{{
				Value:           "allow",
				Label:           "Allow once",
				DecisionPayload: map[string]interface{}{"allow": map[string]interface{}{"rule": "x"}},
			}},
		},
	}
	if label := resolvedApprovalDecisionLabel(approval, PendingInteractionResponse{DecisionPayload: map[string]interface{}{"allow": map[string]interface{}{"rule": "x"}}}); label != "Allow once" {
		t.Fatalf("unexpected approval label %q", label)
	}
	if value := resolvedApprovalDecisionValue(approval, PendingInteractionResponse{DecisionPayload: map[string]interface{}{"allow": map[string]interface{}{"rule": "x"}}}); value != "allow" {
		t.Fatalf("unexpected approval value %q", value)
	}
	if _, err := normalizePendingInteractionResponse(approval, PendingInteractionResponse{}); err == nil || !strings.Contains(err.Error(), "missing decision") {
		t.Fatalf("expected missing approval decision error, got %v", err)
	}
	if _, err := normalizePendingInteractionResponse(approval, PendingInteractionResponse{Decision: "deny"}); err == nil || !strings.Contains(err.Error(), "unsupported decision") {
		t.Fatalf("expected unsupported approval decision error, got %v", err)
	}
	normalized, err := normalizePendingInteractionResponse(approval, PendingInteractionResponse{DecisionPayload: map[string]interface{}{"allow": map[string]interface{}{"rule": "x"}}})
	if err != nil || len(normalized.DecisionPayload) == 0 {
		t.Fatalf("expected matching approval payload to normalize, got %#v err=%v", normalized, err)
	}

	if _, err := normalizePendingInteractionResponse(PendingInteraction{Kind: PendingInteractionKindUserInput}, PendingInteractionResponse{Answers: map[string][]string{"q1": {"yes"}}}); err == nil || !strings.Contains(err.Error(), "missing question schema") {
		t.Fatalf("expected missing question schema error, got %v", err)
	}

	if _, err := normalizePendingInteractionResponse(PendingInteraction{Kind: PendingInteractionKindElicitation}, PendingInteractionResponse{Action: "accept"}); err == nil || !strings.Contains(err.Error(), "missing content") {
		t.Fatalf("expected missing elicitation content error, got %v", err)
	}
	if normalized, err := normalizePendingInteractionResponse(PendingInteraction{Kind: PendingInteractionKindElicitation}, PendingInteractionResponse{Action: "decline"}); err != nil || normalized.Action != "decline" {
		t.Fatalf("expected decline action to normalize, got %#v err=%v", normalized, err)
	}

	var logOutput bytes.Buffer
	client := &Client{
		cfg:    ClientConfig{Workspace: "/tmp/work"},
		logger: slog.New(slog.NewJSONHandler(&logOutput, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	client.logStreamOutput("stdout", "   ")
	client.logStreamOutput("stderr", "hello world")
	if !strings.Contains(logOutput.String(), "Codex app-server stream output") || !strings.Contains(logOutput.String(), "stderr") {
		t.Fatalf("expected stream log output, got %s", logOutput.String())
	}

	if _, err := jsonMapFromValue(make(chan int)); err == nil {
		t.Fatal("expected jsonMapFromValue to fail for unsupported values")
	}
}

func TestClientCompletionAndThreadStateBranches(t *testing.T) {
	client := &Client{
		logger: discardLogger(),
		session: &Session{
			Terminal:       true,
			SessionID:      "session-1",
			ThreadID:       "thread-1",
			TurnID:         "turn-1",
			TerminalReason: "turn.completed",
		},
	}

	if handled, err := client.terminalTurnCompletionResult(); !handled || err != nil {
		t.Fatalf("expected completed terminal session to short-circuit, got handled=%v err=%v", handled, err)
	}
	for _, tc := range []struct {
		reason string
		want   string
	}{
		{reason: "turn.failed", want: "turn_failed"},
		{reason: "turn.cancelled", want: "turn_cancelled"},
		{reason: "run.failed", want: "run_failed"},
		{reason: "error", want: "error"},
	} {
		client.session.TerminalReason = tc.reason
		handled, err := client.terminalTurnCompletionResult()
		var runErr *RunError
		if !handled || err == nil || !errors.As(err, &runErr) || runErr.Kind != tc.want {
			t.Fatalf("expected %s terminal reason to map to %s, got handled=%v err=%v", tc.reason, tc.want, handled, err)
		}
	}
	client.session.TerminalReason = "unknown"
	if handled, err := client.terminalTurnCompletionResult(); handled || err != nil {
		t.Fatalf("expected unknown terminal reason to pass through, got handled=%v err=%v", handled, err)
	}

	client.session.Terminal = false
	if handled, err := client.terminalTurnCompletionResult(); handled || err != nil {
		t.Fatalf("expected non-terminal session to pass through, got handled=%v err=%v", handled, err)
	}

	client.session = &Session{ThreadID: "thread-1", TurnID: "turn-1"}
	client.waitCh = make(chan error, 1)
	if client.turnFinishedByCleanProcessExit(0) {
		t.Fatal("expected clean exit detection to fail without a signal")
	}
	client.waitCh <- nil
	if !client.turnFinishedByCleanProcessExit(0) {
		t.Fatal("expected queued nil wait result to count as clean exit")
	}
	client.waitCh = make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		client.waitCh <- errors.New("exit 1")
	}()
	if client.turnFinishedByCleanProcessExit(50 * time.Millisecond) {
		t.Fatal("expected non-nil wait result to be treated as an unclean exit")
	}

	client.session = &Session{TurnsStarted: 1}
	client.cfg.InitialCollaborationMode = "plan"
	if got := client.currentInteractionCollaborationMode(); got != "plan" {
		t.Fatalf("expected plan collaboration mode, got %q", got)
	}
	client.threadResumed = true
	if got := client.currentInteractionCollaborationMode(); got != "" {
		t.Fatalf("expected resumed thread to suppress collaboration mode, got %q", got)
	}
	client.threadResumed = false
	client.session.TurnsStarted = 2
	if got := client.currentInteractionCollaborationMode(); got != "" {
		t.Fatalf("expected later turns to suppress collaboration mode, got %q", got)
	}

	if got := client.currentInteractionCollaborationMode(); got != "" {
		t.Fatalf("expected no collaboration mode when turns started != 1, got %q", got)
	}
}

func TestClientInfrastructureHelperBranches(t *testing.T) {
	t.Run("answers and input field helpers", func(t *testing.T) {
		if answers, ok := answersForToolInput(nil, false); answers != nil || ok {
			t.Fatalf("expected disabled auto-approval to short-circuit, got %#v %v", answers, ok)
		}
		if answers, ok := answersForToolInput(map[string]interface{}{}, true); answers != nil || ok {
			t.Fatalf("expected missing questions to fail, got %#v %v", answers, ok)
		}
		if answers, ok := answersForToolInput(map[string]interface{}{
			"questions": []interface{}{map[string]interface{}{"id": "q1", "options": []interface{}{"skip"}}},
		}, true); answers != nil || ok {
			t.Fatalf("expected invalid question options to fail, got %#v %v", answers, ok)
		}
		if answers, ok := answersForToolInput(map[string]interface{}{
			"questions": []interface{}{"oops"},
		}, true); answers != nil || ok {
			t.Fatalf("expected invalid question payload to fail, got %#v %v", answers, ok)
		}
		if answers, ok := answersForToolInput(map[string]interface{}{
			"questions": []interface{}{map[string]interface{}{"id": "q1", "options": []interface{}{map[string]interface{}{"label": "  "}}}},
		}, true); answers != nil || ok {
			t.Fatalf("expected blank approval labels to fail, got %#v %v", answers, ok)
		}
		if answers, ok := answersForToolInput(map[string]interface{}{
			"questions": []interface{}{map[string]interface{}{"id": "q1", "options": []interface{}{map[string]interface{}{"label": "Allow once"}}}},
		}, true); !ok || answers["q1"].(map[string]interface{})["answers"].([]string)[0] != "Allow once" {
			t.Fatalf("expected approval label to propagate, got %#v %v", answers, ok)
		}

		if answers, ok := answersForToolInputParams(gen.ToolRequestUserInputParams{}, false); answers != nil || ok {
			t.Fatalf("expected disabled typed auto-approval to short-circuit, got %#v %v", answers, ok)
		}
		if answers, ok := answersForToolInputParams(gen.ToolRequestUserInputParams{Questions: []gen.ToolRequestUserInputQuestion{{ID: " "}}}, true); answers != nil || ok {
			t.Fatalf("expected blank typed question id to fail, got %#v %v", answers, ok)
		}
		if answers, ok := answersForToolInputParams(gen.ToolRequestUserInputParams{
			Questions: []gen.ToolRequestUserInputQuestion{{
				ID:      "typed",
				Options: []gen.ToolRequestUserInputOption{{Label: "Approve this session"}},
			}},
		}, true); !ok || answers["typed"].Answers[0] != "Approve this session" {
			t.Fatalf("expected typed approval label to propagate, got %#v %v", answers, ok)
		}

		for _, tc := range []struct {
			name string
			p    map[string]interface{}
			want bool
		}{
			{name: "requires input", p: map[string]interface{}{"requiresInput": true}, want: true},
			{name: "needs input", p: map[string]interface{}{"needsInput": true}, want: true},
			{name: "type", p: map[string]interface{}{"type": "needs_input"}, want: true},
			{name: "false", p: map[string]interface{}{"type": "other"}, want: false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if got := needsInputField(tc.p); got != tc.want {
					t.Fatalf("needsInputField(%v) = %v, want %v", tc.p, got, tc.want)
				}
			})
		}
	})

	t.Run("workspace and sandbox defaults", func(t *testing.T) {
		if err := validateWorkspaceCWD("", ""); err == nil {
			t.Fatal("expected blank workspace/root to fail after cleaning")
		}
		repoRoot := t.TempDir()
		workspaceRoot := filepath.Join(repoRoot, "workspaces")
		workspace := filepath.Join(workspaceRoot, "ISS-1")
		if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
			t.Fatalf("mkdir .git: %v", err)
		}
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			t.Fatalf("mkdir workspace: %v", err)
		}
		if err := validateWorkspaceCWD(workspace, workspaceRoot); err != nil {
			t.Fatalf("validateWorkspaceCWD inside root: %v", err)
		}
		if err := validateWorkspaceCWD(workspaceRoot, workspaceRoot); err == nil {
			t.Fatal("expected identical workspace and root to fail")
		}
		if err := validateWorkspaceCWD(filepath.Join(t.TempDir(), "outside"), workspaceRoot); err == nil {
			t.Fatal("expected workspace outside root to fail")
		}

		roots := defaultSandboxWritableRoots(workspace, workspaceRoot)
		if len(roots) != 3 {
			t.Fatalf("expected workspace, root, and repo parent roots, got %#v", roots)
		}
		for _, want := range []string{workspace, workspaceRoot, repoRoot} {
			found := false
			for _, got := range roots {
				if got == want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected writable roots to include %q, got %#v", want, roots)
			}
		}
		if got := needsInputField(nil); got {
			t.Fatal("expected nil payload to report no input requirement")
		}
	})

	t.Run("io and line helpers", func(t *testing.T) {
		if isBenignReadCloseError(nil) || !isBenignReadCloseError(io.EOF) || !isBenignReadCloseError(os.ErrClosed) {
			t.Fatal("expected benign close detection to recognize EOF and closed errors")
		}
		if !isBenignReadCloseError(errors.New("file already closed")) || !isBenignReadCloseError(errors.New("closed pipe")) {
			t.Fatal("expected benign close detection to match error text")
		}
		if isBenignReadCloseError(errors.New("boom")) {
			t.Fatal("expected unrelated error to be treated as fatal")
		}

		client := &Client{
			stdin:   &bufferWriteCloser{},
			lines:   make(chan string, 1),
			lineErr: make(chan error, 1),
			logger:  discardLogger(),
		}
		if err := client.sendMessage(map[string]interface{}{"event": "ping"}); err != nil {
			t.Fatalf("sendMessage: %v", err)
		}
		if got := client.stdin.(*bufferWriteCloser).String(); !strings.Contains(got, `"event":"ping"`) || !strings.HasSuffix(got, "\n") {
			t.Fatalf("unexpected encoded payload %q", got)
		}

		client.lines <- "line-1"
		if got, err := client.nextLine(context.Background(), 10*time.Millisecond); err != nil || got != "line-1" {
			t.Fatalf("nextLine immediate = %q err=%v", got, err)
		}

		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := client.nextLine(canceled, 10*time.Millisecond); err == nil {
			t.Fatal("expected canceled context to fail")
		}

		if _, err := client.nextLine(context.Background(), 5*time.Millisecond); err == nil {
			t.Fatal("expected timeout to fail")
		} else {
			var runErr *RunError
			if !errors.As(err, &runErr) || runErr.Kind != "read_timeout" {
				t.Fatalf("expected read timeout run error, got %v", err)
			}
		}

		closedClient := &Client{
			lines:   make(chan string),
			lineErr: make(chan error, 1),
			logger:  discardLogger(),
		}
		close(closedClient.lines)
		closedClient.lineErr <- io.EOF
		if _, err := closedClient.nextLine(context.Background(), 10*time.Millisecond); !errors.Is(err, io.EOF) {
			t.Fatalf("expected EOF after closed lines, got %v", err)
		}
		closedClient = &Client{
			lines:   make(chan string),
			lineErr: make(chan error, 1),
			logger:  discardLogger(),
		}
		close(closedClient.lines)
		closedClient.lineErr <- errors.New("boom")
		if _, err := closedClient.nextLine(context.Background(), 10*time.Millisecond); err == nil || err.Error() != "boom" {
			t.Fatalf("expected non-benign read error to bubble up, got %v", err)
		}
		closedClient = &Client{
			lines:   make(chan string),
			lineErr: make(chan error, 1),
			logger:  discardLogger(),
		}
		close(closedClient.lines)
		if _, err := closedClient.nextLine(context.Background(), 10*time.Millisecond); !errors.Is(err, io.EOF) {
			t.Fatalf("expected closed lines without error to report EOF, got %v", err)
		}
	})
}
