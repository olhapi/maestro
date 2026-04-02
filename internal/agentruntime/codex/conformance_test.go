package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/agentruntime/contracttest"
	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
)

func TestStdioRuntimeContract(t *testing.T) {
	contracttest.RunSharedRuntimeContractTests(t, contracttest.Contract{
		Capabilities:      stdioCapabilities,
		Provider:          string(agentruntime.ProviderCodex),
		Transport:         string(agentruntime.TransportStdio),
		MinActivityEvents: 4,
		Start: func(t *testing.T, observers agentruntime.Observers) contracttest.StartResult {
			return contracttest.StartResult{Client: mustStartStdioRuntime(t, observers)}
		},
	})
}

func TestAppServerRuntimeContract(t *testing.T) {
	contracttest.RunSharedRuntimeContractTests(t, contracttest.Contract{
		Capabilities:      appServerCapabilities,
		Provider:          string(agentruntime.ProviderCodex),
		Transport:         string(agentruntime.TransportAppServer),
		MinActivityEvents: 4,
		Start:             startSharedAppServerRuntime,
		AssertPermissionUpdate: func(t *testing.T, state any) {
			tracePath, ok := state.(string)
			if !ok {
				t.Fatalf("expected app-server trace path state, got %T", state)
			}
			lines := readTraceLines(t, tracePath)
			turnStarts := 0
			foundUpdatedPolicy := false
			for _, payload := range lines {
				if nestedStringMap(payload, "method") != "turn/start" {
					continue
				}
				turnStarts++
				if turnStarts != 2 {
					continue
				}
				params, _ := payload["params"].(map[string]interface{})
				sandboxPolicy, _ := params["sandboxPolicy"].(map[string]interface{})
				if sandboxPolicy["type"] == "dangerFullAccess" && nestedStringMap(payload, "params", "threadId") == "thread-contract" {
					foundUpdatedPolicy = true
				}
			}
			if !foundUpdatedPolicy {
				t.Fatalf("expected second turn to use updated sandbox policy, got %#v", lines)
			}
		},
	})
}

func TestAppServerRuntimeAcceptsLocalImageInput(t *testing.T) {
	client := mustStartAppServerRuntime(t, fakeappserver.Scenario{
		Steps: append(initializeOnlyScenario(),
			fakeappserver.Step{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-image"}}},
				}},
			},
			fakeappserver.Step{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-image"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-image", "turn": map[string]interface{}{"id": "turn-image"}}}},
				},
			},
		),
	}, nil, agentruntime.Observers{})
	defer func() { _ = client.Close() }()

	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Input: []agentruntime.InputItem{{
			Kind: agentruntime.InputItemLocalImage,
			Path: "/tmp/example.png",
			Name: "example",
		}},
	}, nil); err != nil {
		t.Fatalf("expected app-server runtime to accept local image input, got %v", err)
	}
}

func TestAppServerRuntimeFreshAndResumedTurns(t *testing.T) {
	t.Run("fresh_turn", func(t *testing.T) {
		tracePath := filepath.Join(t.TempDir(), "trace.log")
		client := mustStartAppServerRuntime(t, fakeappserver.Scenario{
			Steps: append(initializeOnlyScenario(),
				fakeappserver.Step{
					Match: fakeappserver.Match{Method: "thread/start"},
					Emit: []fakeappserver.Output{{
						JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-fresh"}}},
					}},
				},
				fakeappserver.Step{
					Match: fakeappserver.Match{Method: "turn/start"},
					Emit: []fakeappserver.Output{
						{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-fresh"}}}},
						{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-fresh", "turn": map[string]interface{}{"id": "turn-fresh"}}}},
					},
				},
			),
		}, func(spec *agentruntime.RuntimeSpec) {
			spec.Env = append(spec.Env, "TRACE_FILE="+tracePath)
		}, agentruntime.Observers{})
		defer func() { _ = client.Close() }()

		if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
			Title: "Fresh turn",
			Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "prompt"}},
		}, nil); err != nil {
			t.Fatalf("RunTurn fresh: %v", err)
		}

		lines := readTraceLines(t, tracePath)
		if containsMethod(lines, "thread/resume") {
			t.Fatalf("expected fresh run not to resume thread, got %#v", lines)
		}
		if !containsMethod(lines, "thread/start") {
			t.Fatalf("expected fresh run to start a thread, got %#v", lines)
		}
	})

	t.Run("resumed_turn", func(t *testing.T) {
		tracePath := filepath.Join(t.TempDir(), "trace.log")
		client := mustStartAppServerRuntime(t, fakeappserver.Scenario{
			Steps: append(initializeOnlyScenario(),
				fakeappserver.Step{
					Match: fakeappserver.Match{Method: "thread/resume"},
					Emit: []fakeappserver.Output{{
						JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-resumed"}}},
					}},
				},
				fakeappserver.Step{
					Match: fakeappserver.Match{Method: "turn/start"},
					Emit: []fakeappserver.Output{
						{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-resumed"}}}},
						{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-resumed", "turn": map[string]interface{}{"id": "turn-resumed"}}}},
					},
				},
			),
		}, func(spec *agentruntime.RuntimeSpec) {
			spec.ResumeToken = "thread-stale"
			spec.Env = append(spec.Env, "TRACE_FILE="+tracePath)
		}, agentruntime.Observers{})
		defer func() { _ = client.Close() }()

		if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
			Title: "Resumed turn",
			Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "prompt"}},
		}, nil); err != nil {
			t.Fatalf("RunTurn resumed: %v", err)
		}

		lines := readTraceLines(t, tracePath)
		if !containsMethod(lines, "thread/resume") {
			t.Fatalf("expected resumed run to use thread/resume, got %#v", lines)
		}
		if containsMethod(lines, "thread/start") {
			t.Fatalf("expected resumed run not to start a fresh thread, got %#v", lines)
		}
		if got := nestedStringMap(findFirstMethod(lines, "thread/resume"), "params", "threadId"); got != "thread-stale" {
			t.Fatalf("expected resume token to be forwarded, got %#v", lines)
		}
	})
}

func TestAppServerRuntimeNormalizesSessionActivityAndInteractions(t *testing.T) {
	var (
		sessionMu sync.Mutex
		sessions  []agentruntime.Session
		eventMu   sync.Mutex
		events    []agentruntime.ActivityEvent
	)
	interactionCh := make(chan agentruntime.PendingInteraction, 1)

	client := mustStartAppServerRuntime(t, fakeappserver.Scenario{
		Steps: append(initializeOnlyScenario(),
			fakeappserver.Step{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-runtime"}}},
				}},
			},
			fakeappserver.Step{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-runtime"}}}},
					{JSON: map[string]interface{}{"method": "item/started", "params": map[string]interface{}{
						"threadId": "thread-runtime",
						"turnId":   "turn-runtime",
						"item": map[string]interface{}{
							"id":    "msg-1",
							"type":  "agentMessage",
							"phase": "commentary",
							"text":  "thinking",
						},
					}}},
					{JSON: map[string]interface{}{"method": "thread/tokenUsage/updated", "params": map[string]interface{}{
						"threadId": "thread-runtime",
						"turnId":   "turn-runtime",
						"tokenUsage": map[string]interface{}{
							"total": map[string]interface{}{
								"inputTokens":  5,
								"outputTokens": 7,
								"totalTokens":  12,
							},
						},
					}}},
					{JSON: map[string]interface{}{
						"id":     99,
						"method": "item/commandExecution/requestApproval",
						"params": map[string]interface{}{
							"threadId": "thread-runtime",
							"turnId":   "turn-runtime",
							"itemId":   "approval-item",
							"command":  "git status",
							"cwd":      "/repo",
							"reason":   "Need approval",
						},
					}},
				},
			},
			fakeappserver.Step{
				Match: fakeappserver.Match{ID: fakeappserver.Int(99)},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"method": "item/completed", "params": map[string]interface{}{
						"threadId": "thread-runtime",
						"turnId":   "turn-runtime",
						"item": map[string]interface{}{
							"id":    "msg-2",
							"type":  "agentMessage",
							"phase": "final_answer",
							"text":  "done",
						},
					}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{
						"threadId": "thread-runtime",
						"turn": map[string]interface{}{
							"id": "turn-runtime",
						},
					}}},
				},
			},
		),
	}, nil, agentruntime.Observers{
		OnSessionUpdate: func(session *agentruntime.Session) {
			if session == nil {
				return
			}
			sessionMu.Lock()
			defer sessionMu.Unlock()
			sessions = append(sessions, session.Clone())
		},
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			eventMu.Lock()
			defer eventMu.Unlock()
			events = append(events, event.Clone())
		},
		OnPendingInteraction: func(interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder) {
			if interaction == nil {
				return
			}
			interactionCh <- interaction.Clone()
			go func() {
				_ = responder(context.Background(), interaction.ID, agentruntime.PendingInteractionResponse{
					Decision: "acceptForSession",
				})
			}()
		},
	})
	defer func() { _ = client.Close() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(context.Background(), agentruntime.TurnRequest{
			Title: "Normalize",
			Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "prompt"}},
		}, nil)
	}()

	var interaction agentruntime.PendingInteraction
	select {
	case interaction = <-interactionCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pending interaction")
	}
	assertRuntimeMetadata(t, interaction.Metadata, agentruntime.TransportAppServer)
	if interaction.Kind != agentruntime.PendingInteractionKindApproval || interaction.Approval == nil || interaction.Approval.Command != "git status" {
		t.Fatalf("expected normalized approval interaction, got %+v", interaction)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	waitForCondition(t, 2*time.Second, func() bool {
		sessionMu.Lock()
		defer sessionMu.Unlock()
		return len(sessions) > 0
	})
	waitForCondition(t, 2*time.Second, func() bool {
		eventMu.Lock()
		defer eventMu.Unlock()
		return len(events) >= 3
	})

	session := client.Session()
	if session == nil {
		t.Fatal("expected session snapshot")
	}
	assertRuntimeMetadata(t, session.Metadata, agentruntime.TransportAppServer)
	if session.ThreadID != "thread-runtime" || session.TurnID != "turn-runtime" {
		t.Fatalf("expected normalized thread and turn ids, got %+v", session)
	}
	if session.TotalTokens != 12 {
		t.Fatalf("expected token usage update to flow into session, got %+v", session)
	}
	if session.LastMessage != "done" {
		t.Fatalf("expected final answer to become last message, got %+v", session)
	}

	eventMu.Lock()
	defer eventMu.Unlock()
	foundCommentary := false
	foundApproval := false
	for _, event := range events {
		assertRuntimeMetadata(t, event.Metadata, agentruntime.TransportAppServer)
		switch event.Type {
		case "item.started":
			if event.ItemType == "agentMessage" && event.ItemPhase == "commentary" {
				foundCommentary = true
			}
		case "item.commandExecution.requestApproval":
			if event.Command == "git status" {
				foundApproval = true
			}
		}
	}
	if !foundCommentary || !foundApproval {
		t.Fatalf("expected normalized activity events, got %+v", events)
	}
}

func mustStartStdioRuntime(t *testing.T, observers agentruntime.Observers) agentruntime.Client {
	t.Helper()
	client, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderCodex,
		Transport:       agentruntime.TransportStdio,
		Command:         "cat",
		IssueID:         "iss_stdio",
		IssueIdentifier: "ISS-STDIO",
	}, observers)
	if err != nil {
		t.Fatalf("Start stdio runtime: %v", err)
	}
	return client
}

func mustStartAppServerRuntime(t *testing.T, scenario fakeappserver.Scenario, mutate func(*agentruntime.RuntimeSpec), observers agentruntime.Observers) agentruntime.Client {
	t.Helper()

	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-RUNTIME")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}

	cfg := fakeappserver.NewConfig(t, scenario)
	t.Cleanup(cfg.Close)

	spec := agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderCodex,
		Transport:       agentruntime.TransportAppServer,
		Command:         cfg.Command,
		Workspace:       workspace,
		WorkspaceRoot:   workspaceRoot,
		IssueID:         "iss_app",
		IssueIdentifier: "ISS-APP",
		Env:             append([]string(nil), cfg.Env...),
		ReadTimeout:     2 * time.Second,
		TurnTimeout:     3 * time.Second,
		StallTimeout:    3 * time.Second,
		Permissions: agentruntime.PermissionConfig{
			ThreadSandbox: "workspace-write",
			TurnSandboxPolicy: map[string]interface{}{
				"type": "workspaceWrite",
			},
			CollaborationMode: "default",
		},
	}
	if mutate != nil {
		mutate(&spec)
	}

	client, err := Start(context.Background(), spec, observers)
	if err != nil {
		t.Fatalf("Start app-server runtime: %v", err)
	}
	return client
}

func initializeOnlyScenario() []fakeappserver.Step {
	return []fakeappserver.Step{
		{
			Match: fakeappserver.Match{Method: "initialize"},
			Emit: []fakeappserver.Output{{
				JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}},
			}},
		},
		{Match: fakeappserver.Match{Method: "initialized"}},
	}
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

func startSharedAppServerRuntime(t *testing.T, observers agentruntime.Observers) contracttest.StartResult {
	tracePath := filepath.Join(t.TempDir(), "trace.log")
	client := mustStartAppServerRuntime(t, fakeappserver.Scenario{
		Steps: append(initializeOnlyScenario(),
			fakeappserver.Step{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-contract"}}},
				}},
			},
			fakeappserver.Step{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-1"}}}},
					{Text: "first prompt"},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-contract", "turnId": "turn-1"}}},
				},
			},
			fakeappserver.Step{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 4, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-2"}}}},
					{Text: "second prompt"},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-contract", "turnId": "turn-2"}}},
				},
			},
		),
	}, func(spec *agentruntime.RuntimeSpec) {
		spec.Env = append(spec.Env, "TRACE_FILE="+tracePath)
	}, observers)
	return contracttest.StartResult{
		Client: client,
		State:  tracePath,
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

func containsMethod(lines []map[string]interface{}, method string) bool {
	return findFirstMethod(lines, method) != nil
}

func findFirstMethod(lines []map[string]interface{}, method string) map[string]interface{} {
	for _, payload := range lines {
		if nestedStringMap(payload, "method") == method {
			return payload
		}
	}
	return nil
}

func nestedStringMap(m map[string]interface{}, path ...string) string {
	if m == nil {
		return ""
	}
	var cur interface{} = m
	for _, part := range path {
		next, ok := cur.(map[string]interface{})
		if !ok {
			return ""
		}
		cur = next[part]
	}
	value, _ := cur.(string)
	return value
}

func assertRuntimeMetadata(t *testing.T, metadata map[string]interface{}, transport agentruntime.Transport) {
	t.Helper()
	if metadata["provider"] != string(agentruntime.ProviderCodex) {
		t.Fatalf("expected provider metadata %q, got %#v", agentruntime.ProviderCodex, metadata)
	}
	if metadata["transport"] != string(transport) {
		t.Fatalf("expected transport metadata %q, got %#v", transport, metadata)
	}
}
