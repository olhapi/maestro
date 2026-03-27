package runtime_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/codexschema"
	runtimepkg "github.com/olhapi/maestro/internal/runtime"
	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
)

type comparableClient interface {
	RunTurn(ctx context.Context, prompt, title string) error
	UpdatePermissionConfig(approvalPolicy interface{}, threadSandbox string, turnSandboxPolicy map[string]interface{})
	RespondToInteraction(ctx context.Context, interactionID string, response runtimepkg.PendingInteractionResponse) error
	Session() *runtimepkg.Session
	Output() string
	Close() error
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestFakeAppServerHelperProcess(t *testing.T) {
	fakeappserver.MaybeRun()
}

func defaultApprovalPolicyMap() map[string]interface{} {
	return map[string]interface{}{
		"granular": map[string]interface{}{
			"sandbox_approval":    true,
			"rules":               true,
			"mcp_elicitations":    true,
			"request_permissions": false,
		},
	}
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	filtered := env[:0]
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func readTraceLines(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	payloads := make([]map[string]interface{}, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "JSON:") {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "JSON:")), &payload); err != nil {
			t.Fatalf("decode trace line %q: %v", line, err)
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

func traceMethods(payloads []map[string]interface{}) []string {
	methods := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		if method, _ := payload["method"].(string); method != "" {
			methods = append(methods, method)
		}
	}
	return methods
}

func payloadsByMethod(payloads []map[string]interface{}, method string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(payloads))
	for _, payload := range payloads {
		if current, _ := payload["method"].(string); current == method {
			out = append(out, payload)
		}
	}
	return out
}

func nestedStringMap(payload map[string]interface{}, path ...string) string {
	var current interface{} = payload
	for _, key := range path {
		m, ok := current.(map[string]interface{})
		if !ok {
			return ""
		}
		current = m[key]
	}
	text, _ := current.(string)
	return strings.TrimSpace(text)
}

func sessionSummary(session *runtimepkg.Session) map[string]string {
	if session == nil {
		return map[string]string{}
	}
	return map[string]string{
		"session_id":      session.SessionID,
		"thread_id":       session.ThreadID,
		"turn_id":         session.TurnID,
		"last_message":    session.LastMessage,
		"terminal_reason": session.TerminalReason,
	}
}

func startComparableBackends(t *testing.T, scenario fakeappserver.Scenario, configure func(*appserver.ClientConfig, *runtimepkg.CodexBackendConfig, string, string)) (comparableClient, comparableClient, string, string) {
	t.Helper()

	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-1")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	directScenario := fakeappserver.NewConfig(t, scenario)
	runtimeScenario := fakeappserver.NewConfig(t, scenario)

	directTrace := filepath.Join(tmpDir, "direct-trace.log")
	runtimeTrace := filepath.Join(tmpDir, "runtime-trace.log")

	directCfg := appserver.ClientConfig{
		Executable:               "sh",
		Args:                     []string{"-lc", directScenario.Command},
		Env:                      setEnvValue(directScenario.Env, "TRACE_FILE", directTrace),
		Workspace:                workspace,
		WorkspaceRoot:            workspaceRoot,
		IssueID:                  "ISS-1",
		IssueIdentifier:          "ISS-1",
		ExpectedVersion:          codexschema.SupportedVersion,
		ApprovalPolicy:           defaultApprovalPolicyMap(),
		InitialCollaborationMode: "default",
		ThreadSandbox:            "workspace-write",
		ReadTimeout:              2 * time.Second,
		TurnTimeout:              10 * time.Second,
		StallTimeout:             5 * time.Second,
		Logger:                   discardLogger(),
	}
	runtimeCfg := runtimepkg.CodexBackendConfig{
		Command:                  runtimeScenario.Command,
		Env:                      setEnvValue(runtimeScenario.Env, "TRACE_FILE", runtimeTrace),
		Workspace:                workspace,
		WorkspaceRoot:            workspaceRoot,
		IssueID:                  "ISS-1",
		IssueIdentifier:          "ISS-1",
		ExpectedVersion:          codexschema.SupportedVersion,
		ApprovalPolicy:           defaultApprovalPolicyMap(),
		InitialCollaborationMode: "default",
		ThreadSandbox:            "workspace-write",
		ReadTimeout:              2 * time.Second,
		TurnTimeout:              10 * time.Second,
		StallTimeout:             5 * time.Second,
		Logger:                   discardLogger(),
	}
	if configure != nil {
		configure(&directCfg, &runtimeCfg, directTrace, runtimeTrace)
	}

	direct, err := appserver.Start(context.Background(), directCfg)
	if err != nil {
		t.Fatalf("start direct client: %v", err)
	}
	wrapped, err := runtimepkg.NewCodexBackend(context.Background(), runtimeCfg)
	if err != nil {
		_ = direct.Close()
		t.Fatalf("start runtime backend: %v", err)
	}

	t.Cleanup(func() {
		_ = wrapped.Close()
		_ = direct.Close()
	})
	return direct, wrapped, directTrace, runtimeTrace
}

func runTurnAndWait(t *testing.T, client comparableClient, prompt, title string) {
	t.Helper()
	if err := client.RunTurn(context.Background(), prompt, title); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
}

func runPlanTurn(t *testing.T, client comparableClient, interactionCh <-chan runtimepkg.PendingInteraction) runtimepkg.PendingInteraction {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(context.Background(), "Plan prompt", "Plan turn")
	}()

	var interaction runtimepkg.PendingInteraction
	select {
	case interaction = <-interactionCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected pending interaction")
	}

	if interaction.CollaborationMode != "plan" {
		t.Fatalf("expected plan collaboration mode, got %+v", interaction)
	}
	if interaction.Approval == nil || interaction.Approval.Command != "gh pr view" {
		t.Fatalf("unexpected approval payload: %+v", interaction)
	}
	if err := client.RespondToInteraction(context.Background(), interaction.ID, runtimepkg.PendingInteractionResponse{
		Decision: "acceptForSession",
	}); err != nil {
		t.Fatalf("respond to interaction: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	return interaction
}

func TestCodexBackendMatchesDirectClientDefaultMode(t *testing.T) {
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-default"}}}}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-default"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-default", "turn": map[string]interface{}{"id": "turn-default"}}}},
				},
			},
		},
	}
	direct, wrapped, directTrace, runtimeTrace := startComparableBackends(t, scenario, nil)

	runTurnAndWait(t, direct, "Default prompt", "Default mode")
	runTurnAndWait(t, wrapped, "Default prompt", "Default mode")

	if got, want := sessionSummary(direct.Session()), sessionSummary(wrapped.Session()); len(got) != len(want) || got["session_id"] != want["session_id"] || got["thread_id"] != want["thread_id"] || got["turn_id"] != want["turn_id"] {
		t.Fatalf("expected matching session summary, direct=%+v runtime=%+v", got, want)
	}
	if direct.Output() != wrapped.Output() {
		t.Fatalf("expected matching output, direct=%q runtime=%q", direct.Output(), wrapped.Output())
	}

	directPayloads := readTraceLines(t, directTrace)
	runtimePayloads := readTraceLines(t, runtimeTrace)
	if got, want := traceMethods(directPayloads), traceMethods(runtimePayloads); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected matching method sequence, direct=%v runtime=%v", got, want)
	}
	if got := nestedStringMap(payloadsByMethod(directPayloads, "thread/start")[0], "params", "config", "initial_collaboration_mode"); got != "default" {
		t.Fatalf("expected direct thread/start to use default collaboration mode, got %q", got)
	}
	if got := nestedStringMap(payloadsByMethod(runtimePayloads, "thread/start")[0], "params", "config", "initial_collaboration_mode"); got != "default" {
		t.Fatalf("expected runtime thread/start to use default collaboration mode, got %q", got)
	}
}

func TestCodexBackendMatchesDirectClientFullAccessPermissionUpdates(t *testing.T) {
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-perm"}}}}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-one"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-perm", "turn": map[string]interface{}{"id": "turn-one"}}}},
				},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 4, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-two"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-perm", "turn": map[string]interface{}{"id": "turn-two"}}}},
				},
			},
		},
	}
	fullAccess := map[string]interface{}{
		"type":          "dangerFullAccess",
		"networkAccess": true,
	}
	direct, wrapped, directTrace, runtimeTrace := startComparableBackends(t, scenario, func(directCfg *appserver.ClientConfig, runtimeCfg *runtimepkg.CodexBackendConfig, _, _ string) {
		directCfg.ThreadSandbox = "workspace-write"
		runtimeCfg.ThreadSandbox = "workspace-write"
	})

	runTurnAndWait(t, direct, "First prompt", "Full access")
	runTurnAndWait(t, wrapped, "First prompt", "Full access")

	direct.UpdatePermissionConfig(defaultApprovalPolicyMap(), "danger-full-access", fullAccess)
	wrapped.UpdatePermissionConfig(defaultApprovalPolicyMap(), "danger-full-access", fullAccess)

	runTurnAndWait(t, direct, "Second prompt", "Full access")
	runTurnAndWait(t, wrapped, "Second prompt", "Full access")

	if got, want := sessionSummary(direct.Session()), sessionSummary(wrapped.Session()); got["session_id"] != want["session_id"] || got["thread_id"] != want["thread_id"] || got["turn_id"] != want["turn_id"] {
		t.Fatalf("expected matching session summary, direct=%+v runtime=%+v", got, want)
	}

	directPayloads := readTraceLines(t, directTrace)
	runtimePayloads := readTraceLines(t, runtimeTrace)
	directTurnStarts := payloadsByMethod(directPayloads, "turn/start")
	runtimeTurnStarts := payloadsByMethod(runtimePayloads, "turn/start")
	if len(directTurnStarts) != 2 || len(runtimeTurnStarts) != 2 {
		t.Fatalf("expected two turn/start payloads, direct=%d runtime=%d", len(directTurnStarts), len(runtimeTurnStarts))
	}
	if got := nestedStringMap(directTurnStarts[1], "params", "sandboxPolicy", "type"); got != "dangerFullAccess" {
		t.Fatalf("expected direct second turn to use dangerFullAccess sandbox, got %q", got)
	}
	if got := nestedStringMap(runtimeTurnStarts[1], "params", "sandboxPolicy", "type"); got != "dangerFullAccess" {
		t.Fatalf("expected runtime second turn to use dangerFullAccess sandbox, got %q", got)
	}
	if strings.Join(traceMethods(directPayloads), ",") != strings.Join(traceMethods(runtimePayloads), ",") {
		t.Fatalf("expected matching request methods, direct=%v runtime=%v", traceMethods(directPayloads), traceMethods(runtimePayloads))
	}
}

func TestCodexBackendMatchesDirectClientPlanThenFullAccess(t *testing.T) {
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-plan"}}}}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-plan"}}}},
					{JSON: map[string]interface{}{"id": 99, "method": "item/commandExecution/requestApproval", "params": map[string]interface{}{"threadId": "thread-plan", "turnId": "turn-plan", "command": "gh pr view"}}},
				},
			},
		},
	}
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(99)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-plan", "turnId": "turn-plan"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	directInteractions := make(chan runtimepkg.PendingInteraction, 1)
	runtimeInteractions := make(chan runtimepkg.PendingInteraction, 1)
	direct, wrapped, directTrace, runtimeTrace := startComparableBackends(t, scenario, func(directCfg *appserver.ClientConfig, runtimeCfg *runtimepkg.CodexBackendConfig, _, _ string) {
		directCfg.ApprovalPolicy = "on-request"
		directCfg.InitialCollaborationMode = "plan"
		runtimeCfg.ApprovalPolicy = "on-request"
		runtimeCfg.InitialCollaborationMode = "plan"
		directCfg.OnPendingInteraction = func(interaction *appserver.PendingInteraction) {
			if interaction != nil {
				directInteractions <- interaction.Clone()
			}
		}
		runtimeCfg.OnPendingInteraction = func(interaction *runtimepkg.PendingInteraction) {
			if interaction != nil {
				runtimeInteractions <- interaction.Clone()
			}
		}
	})

	directInteraction := runPlanTurn(t, direct, directInteractions)
	runtimeInteraction := runPlanTurn(t, wrapped, runtimeInteractions)

	if directInteraction.CollaborationMode != runtimeInteraction.CollaborationMode || directInteraction.Approval == nil || runtimeInteraction.Approval == nil || directInteraction.Approval.Command != runtimeInteraction.Approval.Command {
		t.Fatalf("expected matching pending interaction payloads, direct=%+v runtime=%+v", directInteraction, runtimeInteraction)
	}
	if got, want := sessionSummary(direct.Session()), sessionSummary(wrapped.Session()); got["session_id"] != want["session_id"] || got["thread_id"] != want["thread_id"] || got["turn_id"] != want["turn_id"] {
		t.Fatalf("expected matching session summary, direct=%+v runtime=%+v", got, want)
	}
	if direct.Output() != wrapped.Output() {
		t.Fatalf("expected matching output, direct=%q runtime=%q", direct.Output(), wrapped.Output())
	}

	directPayloads := readTraceLines(t, directTrace)
	runtimePayloads := readTraceLines(t, runtimeTrace)
	if strings.Join(traceMethods(directPayloads), ",") != strings.Join(traceMethods(runtimePayloads), ",") {
		t.Fatalf("expected matching request methods, direct=%v runtime=%v", traceMethods(directPayloads), traceMethods(runtimePayloads))
	}
	if got := nestedStringMap(payloadsByMethod(directPayloads, "thread/start")[0], "params", "config", "initial_collaboration_mode"); got != "plan" {
		t.Fatalf("expected direct thread/start to use plan collaboration mode, got %q", got)
	}
	if got := nestedStringMap(payloadsByMethod(runtimePayloads, "thread/start")[0], "params", "config", "initial_collaboration_mode"); got != "plan" {
		t.Fatalf("expected runtime thread/start to use plan collaboration mode, got %q", got)
	}
}

func TestCodexBackendMatchesDirectClientResumeMode(t *testing.T) {
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/resume"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-resumed"}}}}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-resumed"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-resumed", "turn": map[string]interface{}{"id": "turn-resumed"}}}},
				},
			},
		},
	}
	direct, wrapped, directTrace, runtimeTrace := startComparableBackends(t, scenario, func(directCfg *appserver.ClientConfig, runtimeCfg *runtimepkg.CodexBackendConfig, _, _ string) {
		directCfg.ResumeThreadID = "thread-stale"
		directCfg.ResumeSource = "required"
		runtimeCfg.ResumeThreadID = "thread-stale"
		runtimeCfg.ResumeSource = "required"
	})

	runTurnAndWait(t, direct, "Resume prompt", "Resume mode")
	runTurnAndWait(t, wrapped, "Resume prompt", "Resume mode")

	if got, want := sessionSummary(direct.Session()), sessionSummary(wrapped.Session()); got["session_id"] != want["session_id"] || got["thread_id"] != want["thread_id"] || got["turn_id"] != want["turn_id"] {
		t.Fatalf("expected matching session summary, direct=%+v runtime=%+v", got, want)
	}
	if direct.Output() != wrapped.Output() {
		t.Fatalf("expected matching output, direct=%q runtime=%q", direct.Output(), wrapped.Output())
	}

	directPayloads := readTraceLines(t, directTrace)
	runtimePayloads := readTraceLines(t, runtimeTrace)
	if strings.Join(traceMethods(directPayloads), ",") != strings.Join(traceMethods(runtimePayloads), ",") {
		t.Fatalf("expected matching request methods, direct=%v runtime=%v", traceMethods(directPayloads), traceMethods(runtimePayloads))
	}
	if got := nestedStringMap(payloadsByMethod(directPayloads, "thread/resume")[0], "params", "threadId"); got != "thread-stale" {
		t.Fatalf("expected direct thread/resume to use the persisted thread id, got %q", got)
	}
	if got := nestedStringMap(payloadsByMethod(runtimePayloads, "thread/resume")[0], "params", "threadId"); got != "thread-stale" {
		t.Fatalf("expected runtime thread/resume to use the persisted thread id, got %q", got)
	}
	if len(payloadsByMethod(directPayloads, "thread/start")) != 0 || len(payloadsByMethod(runtimePayloads, "thread/start")) != 0 {
		t.Fatalf("expected resume mode not to fall back to thread/start, direct=%v runtime=%v", traceMethods(directPayloads), traceMethods(runtimePayloads))
	}
}
