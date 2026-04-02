package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
)

type claudeHarness struct {
	dbPath        string
	argsPath      string
	sessionPath   string
	turnCountPath string
	env           []string
}

func TestStdioRuntimeAttachesLiveMaestroMCPConfig(t *testing.T) {
	harness := newClaudeHarness(t)
	var (
		mu         sync.Mutex
		sessions   []agentruntime.Session
		activities []agentruntime.ActivityEvent
		started    agentruntime.Session
	)

	client := mustStartClaudeRuntime(t, harness, agentruntime.Observers{
		OnSessionUpdate: func(session *agentruntime.Session) {
			if session == nil {
				return
			}
			mu.Lock()
			sessions = append(sessions, session.Clone())
			mu.Unlock()
		},
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			mu.Lock()
			activities = append(activities, event.Clone())
			mu.Unlock()
		},
	})

	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "first turn",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "hello"}},
	}, func(session *agentruntime.Session) {
		if session != nil {
			started = session.Clone()
		}
	}); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(sessions) >= 2 && len(activities) >= 2
	})

	if started.SessionID != "claude-session-1" || started.ThreadID != "claude-session-1" {
		t.Fatalf("expected onStarted snapshot to persist the Claude session id, got %+v", started)
	}

	session := client.Session()
	if session == nil {
		t.Fatal("expected session snapshot")
	}
	if session.SessionID != "claude-session-1" || session.ThreadID != "claude-session-1" {
		t.Fatalf("expected durable Claude session id, got %+v", session)
	}
	if session.Metadata["session_identifier_strategy"] != claudeSessionIdentifierStrategy {
		t.Fatalf("expected session identifier strategy metadata, got %+v", session.Metadata)
	}
	if session.Metadata["provider_session_id"] != "claude-session-1" {
		t.Fatalf("expected provider session id metadata, got %+v", session.Metadata)
	}

	if out := client.Output(); strings.TrimSpace(out) != "hello" {
		t.Fatalf("unexpected output: %q", out)
	}

	invocations := readInvocationArgs(t, harness.argsPath)
	if len(invocations) != 1 {
		t.Fatalf("expected a single Claude invocation, got %#v", invocations)
	}
	first := invocations[0]
	for _, want := range []string{
		"-p",
		"--verbose",
		"--output-format=stream-json",
		"--include-partial-messages",
		"--permission-mode",
		"bypassPermissions",
		"--mcp-config",
		"--strict-mcp-config",
	} {
		if !containsArg(first, want) {
			t.Fatalf("expected claude args to include %q, got %#v", want, first)
		}
	}
	if containsArg(first, "-r") {
		t.Fatalf("did not expect resume flag on the first Claude turn, got %#v", first)
	}

	configPath := argValueAfter(t, first, "--mcp-config")
	if configPath == "" {
		t.Fatalf("expected MCP config path in args, got %#v", first)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected MCP config file to exist before close, got %v", err)
	}

	if len(activities) != 2 {
		t.Fatalf("unexpected Claude activity count: %#v", activities)
	}
	if !hasActivityType(activities, "item.completed") || !hasActivityType(activities, "turn.completed") {
		t.Fatalf("unexpected Claude activity stream: %#v", activities)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected MCP config file to be cleaned up, got %v", err)
	}
}

func TestStdioRuntimeResumesWithPersistedSessionID(t *testing.T) {
	harness := newClaudeHarness(t)
	client := mustStartClaudeRuntime(t, harness, agentruntime.Observers{})
	t.Cleanup(func() { _ = client.Close() })

	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "first",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "first prompt"}},
	}, nil); err != nil {
		t.Fatalf("RunTurn first: %v", err)
	}

	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "second",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "second prompt"}},
	}, nil); err != nil {
		t.Fatalf("RunTurn second: %v", err)
	}

	invocations := readInvocationArgs(t, harness.argsPath)
	if len(invocations) != 2 {
		t.Fatalf("expected two Claude invocations, got %#v", invocations)
	}

	second := invocations[1]
	if !containsArg(second, "-r") {
		t.Fatalf("expected resume flag on the second Claude turn, got %#v", second)
	}
	if got := argValueAfter(t, second, "-r"); got != "claude-session-1" {
		t.Fatalf("expected resume flag to use the durable session id, got %q from %#v", got, second)
	}
	if out := client.Output(); !strings.Contains(out, "first prompt") || !strings.Contains(out, "second prompt") {
		t.Fatalf("expected accumulated output to include both prompts, got %q", out)
	}
}

func TestStdioRuntimeUsesProvidedResumeTokenOnFirstTurn(t *testing.T) {
	harness := newClaudeHarness(t, "CLAUDE_SESSION_ID=claude-session-resume")
	client, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderClaude,
		Transport:       agentruntime.TransportStdio,
		Command:         writeFakeClaudeCLI(t),
		Workspace:       t.TempDir(),
		IssueID:         "iss-1",
		IssueIdentifier: "ISS-1",
		ResumeToken:     "claude-session-resume",
		DBPath:          harness.dbPath,
		Env:             harness.env,
	}, agentruntime.Observers{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "resume token",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "resume now"}},
	}, nil); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	invocations := readInvocationArgs(t, harness.argsPath)
	if len(invocations) != 1 {
		t.Fatalf("expected one Claude invocation, got %#v", invocations)
	}
	first := invocations[0]
	if !containsArg(first, "-r") {
		t.Fatalf("expected resume flag on the first Claude turn, got %#v", first)
	}
	if got := argValueAfter(t, first, "-r"); got != "claude-session-resume" {
		t.Fatalf("expected resume flag to use the provided token, got %q from %#v", got, first)
	}

	session := client.Session()
	if session == nil {
		t.Fatal("expected session snapshot")
	}
	if session.SessionID != "claude-session-resume" || session.ThreadID != "claude-session-resume" {
		t.Fatalf("expected persisted resume token in session snapshot, got %+v", session)
	}
	if session.Metadata["session_identifier_strategy"] != claudeSessionIdentifierStrategy {
		t.Fatalf("expected session identifier strategy metadata, got %+v", session.Metadata)
	}
	if session.Metadata["provider_session_id"] != "claude-session-resume" {
		t.Fatalf("expected provider session id metadata, got %+v", session.Metadata)
	}
}

func TestStdioRuntimeInterruptsOnContextCancellation(t *testing.T) {
	harness := newClaudeHarness(t, "CLAUDE_FAKE_INTERRUPT_AFTER_STREAM=1", "CLAUDE_FAKE_INTERRUPT_SLEEP_SECONDS=60")
	var (
		mu         sync.Mutex
		activities []agentruntime.ActivityEvent
	)
	client := mustStartClaudeRuntime(t, harness, agentruntime.Observers{
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			mu.Lock()
			activities = append(activities, event.Clone())
			mu.Unlock()
		},
	})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(ctx, agentruntime.TurnRequest{
			Title: "interrupt",
			Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "interrupt me"}},
		}, nil)
	}()

	waitForCondition(t, time.Second, func() bool {
		session := client.Session()
		return session != nil && session.LastEvent == "turn.started"
	})

	cancel()

	var err error
	select {
	case err = <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for interrupted Claude run to exit")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation from interrupted run, got %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		session := client.Session()
		return session != nil && session.TerminalReason == "turn.cancelled"
	})
	waitForCondition(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return hasActivityType(activities, "turn.cancelled")
	})

	if out := client.Output(); !strings.Contains(out, "interrupt me") {
		t.Fatalf("expected interrupted run to preserve streamed output, got %q", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if !hasActivityType(activities, "turn.cancelled") {
		t.Fatalf("expected cancelled turn activity, got %#v", activities)
	}
}

func TestStdioRuntimeCloseInterruptsActiveTurn(t *testing.T) {
	harness := newClaudeHarness(t, "CLAUDE_FAKE_INTERRUPT_AFTER_STREAM=1", "CLAUDE_FAKE_INTERRUPT_SLEEP_SECONDS=60")
	var (
		mu         sync.Mutex
		activities []agentruntime.ActivityEvent
	)
	client := mustStartClaudeRuntime(t, harness, agentruntime.Observers{
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			mu.Lock()
			activities = append(activities, event.Clone())
			mu.Unlock()
		},
	})
	t.Cleanup(func() { _ = client.Close() })

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(context.Background(), agentruntime.TurnRequest{
			Title: "close",
			Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "close me"}},
		}, nil)
	}()

	waitForCondition(t, time.Second, func() bool {
		session := client.Session()
		return session != nil && session.LastEvent == "turn.started"
	})

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var err error
	select {
	case err = <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for close to interrupt Claude run")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected close to cancel the run, got %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		session := client.Session()
		return session != nil && session.TerminalReason == "turn.cancelled"
	})

	mu.Lock()
	cancelled := hasActivityType(activities, "turn.cancelled")
	mu.Unlock()
	if !cancelled {
		t.Fatalf("expected cancelled turn activity, got %#v", activities)
	}

	invocations := readInvocationArgs(t, harness.argsPath)
	if len(invocations) != 1 {
		t.Fatalf("expected a single Claude invocation, got %#v", invocations)
	}
	configPath := argValueAfter(t, invocations[0], "--mcp-config")
	if configPath == "" {
		t.Fatalf("expected MCP config path in args, got %#v", invocations[0])
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected MCP config file to be removed on close, got %v", err)
	}
}

func TestStartRejectsUnsupportedProviderAndTransport(t *testing.T) {
	if _, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:  "other",
		Transport: agentruntime.TransportStdio,
	}, agentruntime.Observers{}); !errorsIsUnsupported(err) {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
	if _, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:  agentruntime.ProviderClaude,
		Transport: "weird",
	}, agentruntime.Observers{}); !errorsIsUnsupported(err) {
		t.Fatalf("expected unsupported transport error, got %v", err)
	}
}

func mustStartClaudeRuntime(t *testing.T, harness claudeHarness, observers agentruntime.Observers) agentruntime.Client {
	t.Helper()
	client, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderClaude,
		Transport:       agentruntime.TransportStdio,
		Command:         writeFakeClaudeCLI(t),
		Workspace:       t.TempDir(),
		IssueID:         "iss-1",
		IssueIdentifier: "ISS-1",
		DBPath:          harness.dbPath,
		Env:             harness.env,
	}, observers)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return client
}

func newClaudeHarness(t *testing.T, extraEnv ...string) claudeHarness {
	t.Helper()
	dir := t.TempDir()
	env := append(os.Environ(),
		"CLAUDE_ARGS_PATH="+filepath.Join(dir, "claude-args.txt"),
		"CLAUDE_SESSION_PATH="+filepath.Join(dir, "claude-session.txt"),
		"CLAUDE_TURN_COUNT_PATH="+filepath.Join(dir, "claude-turn-count.txt"),
		"CLAUDE_SESSION_ID=claude-session-1",
	)
	env = append(env, extraEnv...)
	return claudeHarness{
		dbPath:        filepath.Join(dir, "maestro.db"),
		argsPath:      filepath.Join(dir, "claude-args.txt"),
		sessionPath:   filepath.Join(dir, "claude-session.txt"),
		turnCountPath: filepath.Join(dir, "claude-turn-count.txt"),
		env:           env,
	}
}

func writeFakeClaudeCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := strings.TrimSpace(`
		#!/bin/sh
		set -eu

		: "${CLAUDE_ARGS_PATH:?}"
		: "${CLAUDE_SESSION_PATH:?}"
		: "${CLAUDE_TURN_COUNT_PATH:?}"

		prompt=$(cat)
		prompt_json=$(printf '%s' "$prompt" | sed 's/\\/\\\\/g; s/"/\\"/g')

		if [ -f "$CLAUDE_SESSION_PATH" ]; then
			session_id=$(cat "$CLAUDE_SESSION_PATH")
		else
			session_id=${CLAUDE_SESSION_ID:-claude-session-1}
			printf '%s' "$session_id" > "$CLAUDE_SESSION_PATH"
		fi

		turn_number=1
		if [ -f "$CLAUDE_TURN_COUNT_PATH" ]; then
			turn_number=$(($(cat "$CLAUDE_TURN_COUNT_PATH") + 1))
		fi
		printf '%s' "$turn_number" > "$CLAUDE_TURN_COUNT_PATH"

		{
			printf '%s\n' '---'
			for arg in "$@"; do
				printf '%s\n' "$arg"
			done
		} >> "$CLAUDE_ARGS_PATH"

		if [ "$turn_number" -gt 1 ]; then
			expected_resume=$(cat "$CLAUDE_SESSION_PATH")
			actual_resume=""
			prev=""
			for arg in "$@"; do
				if [ "$prev" = "-r" ] || [ "$prev" = "--resume" ]; then
					actual_resume="$arg"
					break
				fi
				prev="$arg"
			done
			if [ "$actual_resume" != "$expected_resume" ]; then
				printf 'expected resume %s, got %s\n' "$expected_resume" "$actual_resume" >&2
				exit 33
			fi
		fi

		emit() {
			printf '%s\n' "$1"
		}

		emit "{\"type\":\"system\",\"subtype\":\"init\",\"cwd\":\"$PWD\",\"session_id\":\"$session_id\",\"mcp_servers\":[],\"tools\":[],\"plugins\":[]}"
		emit "{\"type\":\"stream_event\",\"event\":{\"type\":\"message_start\",\"message\":{\"id\":\"turn-$turn_number\"}}}"
		emit "{\"type\":\"stream_event\",\"event\":{\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\"}}}"
		emit "{\"type\":\"stream_event\",\"event\":{\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"$prompt_json\"}}}"
		emit "{\"type\":\"stream_event\",\"event\":{\"type\":\"content_block_stop\"}}"
		emit "{\"type\":\"stream_event\",\"event\":{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}}"
		emit "{\"type\":\"stream_event\",\"event\":{\"type\":\"message_stop\"}}"

		if [ "${CLAUDE_FAKE_INTERRUPT_AFTER_STREAM:-0}" = "1" ]; then
			sleep "${CLAUDE_FAKE_INTERRUPT_SLEEP_SECONDS:-60}"
		fi

		emit "{\"type\":\"assistant\",\"message\":{\"id\":\"turn-$turn_number\",\"type\":\"message\",\"content\":[{\"type\":\"text\",\"text\":\"$prompt_json\"}],\"stop_reason\":\"end_turn\"}}"
		emit "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"$prompt_json\",\"stop_reason\":\"end_turn\",\"session_id\":\"$session_id\"}"
	`) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}

func readInvocationArgs(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var (
		invocations [][]string
		current     []string
	)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "---" {
			if len(current) > 0 {
				invocations = append(invocations, current)
				current = nil
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		invocations = append(invocations, current)
	}
	return invocations
}

func waitForCondition(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func argValueAfter(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasActivityType(events []agentruntime.ActivityEvent, want string) bool {
	for _, event := range events {
		if event.Type == want {
			return true
		}
	}
	return false
}

func errorsIsUnsupported(err error) bool {
	return errors.Is(err, agentruntime.ErrUnsupportedCapability)
}
