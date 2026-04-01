package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
)

func TestDetectVersionAndStartTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf 'codex-cli 1.2.3\\n'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)

	status, err := DetectVersion("codex app-server")
	if err != nil {
		t.Fatalf("DetectVersion: %v", err)
	}
	if status.ExecutablePath == "" || status.Actual != "1.2.3" {
		t.Fatalf("unexpected version status: %+v", status)
	}

	started := startTime()
	if started.IsZero() || time.Since(started) > time.Second {
		t.Fatalf("unexpected start time: %v", started)
	}
}

func TestStdioClientRunTurnAndLifecycle(t *testing.T) {
	sessionCh := make(chan agentruntime.Session, 1)
	activityCh := make(chan agentruntime.ActivityEvent, 1)
	client := startStdio(agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderCodex,
		Transport:       agentruntime.TransportStdio,
		Command:         "cat",
		Workspace:       t.TempDir(),
		IssueID:         "issue-1",
		IssueIdentifier: "ISS-1",
	}, agentruntime.Observers{
		OnSessionUpdate: func(session *agentruntime.Session) {
			if session != nil {
				sessionCh <- session.Clone()
			}
		},
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			activityCh <- event.Clone()
		},
	})

	stdio, ok := client.(*stdioClient)
	if !ok {
		t.Fatalf("expected stdio client, got %T", client)
	}
	if got := client.Capabilities(); !got.PlanGating || got.DynamicTools {
		t.Fatalf("unexpected stdio capabilities: %+v", got)
	}

	var started agentruntime.Session
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
	if started.TurnID != "stdio-turn-1" || started.LastEvent != "turn.started" {
		t.Fatalf("expected started session to reflect turn start, got %+v", started)
	}

	if out := client.Output(); strings.TrimSpace(out) != "hello" {
		t.Fatalf("unexpected output: %q", out)
	}
	session := client.Session()
	if session == nil || session.TurnID != "stdio-turn-1" || session.LastEvent == "" {
		t.Fatalf("unexpected session state: %+v", session)
	}
	updates := make([]agentruntime.Session, 0, 2)
	for len(updates) < 2 {
		select {
		case updated := <-sessionCh:
			updates = append(updates, updated)
		case <-time.After(time.Second):
			t.Fatal("expected session update callback")
		}
	}
	if updates[0].LastEvent != "turn.started" || updates[1].LastEvent != "turn.completed" {
		t.Fatalf("unexpected session updates: %+v", updates)
	}

	events := make([]agentruntime.ActivityEvent, 0, 2)
	deadline := time.After(time.Second)
	for len(events) < 2 {
		select {
		case event := <-activityCh:
			events = append(events, event)
		case <-deadline:
			t.Fatal("expected activity callback")
		}
	}
	foundItemCompleted := false
	foundTurnCompleted := false
	for _, event := range events {
		if event.Type == "item.completed" {
			foundItemCompleted = true
		}
		if event.Type == "turn.completed" {
			foundTurnCompleted = true
		}
	}
	if !foundItemCompleted || !foundTurnCompleted {
		t.Fatalf("unexpected activity events: %+v", events)
	}

	stdio.UpdatePermissions(agentruntime.PermissionConfig{}.WithProvider(agentruntime.ProviderCodex, agentruntime.ProviderPermissionConfig{
		ApprovalPolicy:    "never",
		ThreadSandbox:     "workspace-write",
		TurnSandboxPolicy: map[string]interface{}{"type": "dangerFullAccess"},
	}))
	if got := stdio.spec.Permissions.ForProvider(agentruntime.ProviderCodex); got.ApprovalPolicy != "never" || got.ThreadSandbox != "workspace-write" {
		t.Fatalf("expected permissions update to be applied, got %+v", got)
	}

	if err := client.RespondToInteraction(context.Background(), "interaction-1", agentruntime.PendingInteractionResponse{}); !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
		t.Fatalf("expected unsupported interaction response, got %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestStdioClientEmitsObserverUpdates(t *testing.T) {
	sessionCh := make(chan struct{}, 1)
	activityCh := make(chan struct{}, 1)
	client := startStdio(agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderCodex,
		Transport:       agentruntime.TransportStdio,
		Command:         "cat",
		Workspace:       t.TempDir(),
		IssueID:         "issue-2",
		IssueIdentifier: "ISS-2",
	}, agentruntime.Observers{
		OnSessionUpdate: func(*agentruntime.Session) {
			select {
			case sessionCh <- struct{}{}:
			default:
			}
		},
		OnActivityEvent: func(agentruntime.ActivityEvent) {
			select {
			case activityCh <- struct{}{}:
			default:
			}
		},
	})

	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "observer turn",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "hello"}},
	}, nil); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	select {
	case <-sessionCh:
	case <-time.After(time.Second):
		t.Fatal("expected session update callback")
	}
	select {
	case <-activityCh:
	case <-time.After(time.Second):
		t.Fatal("expected activity callback")
	}
}

func TestCleanupLingeringProcessNoop(t *testing.T) {
	if err := CleanupLingeringProcess(0); err != nil {
		t.Fatalf("expected zero pid cleanup to be a no-op, got %v", err)
	}
}

func TestCombineOutput(t *testing.T) {
	if got := combineOutput("stdout", "stderr"); got != "stdout\nstderr" {
		t.Fatalf("unexpected combined output: %q", got)
	}
	if got := combineOutput("stdout", ""); got != "stdout" {
		t.Fatalf("unexpected stdout-only output: %q", got)
	}
	if got := combineOutput("", "stderr"); got != "stderr" {
		t.Fatalf("unexpected stderr-only output: %q", got)
	}
}

func TestStartAppServerUsesInheritedEnvironmentAndNilReceiverHelpers(t *testing.T) {
	cfg := fakeappserver.NewConfig(t, fakeappserver.Scenario{
		Steps: append(initializeOnlyScenario(), fakeappserver.Step{
			Match: fakeappserver.Match{Method: "thread/start"},
			Emit: []fakeappserver.Output{{
				JSON: map[string]interface{}{
					"id": 2,
					"result": map[string]interface{}{
						"thread": map[string]interface{}{"id": "thread-app-server"},
					},
				},
			}},
		}, fakeappserver.Step{
			Match: fakeappserver.Match{Method: "turn/start"},
			Emit: []fakeappserver.Output{{
				JSON: map[string]interface{}{
					"id": 3,
					"result": map[string]interface{}{
						"turn": map[string]interface{}{"id": "turn-app-server"},
					},
				},
			}, {
				JSON: map[string]interface{}{
					"method": "turn/completed",
					"params": map[string]interface{}{
						"threadId": "thread-app-server",
						"turn":     map[string]interface{}{"id": "turn-app-server"},
					},
				},
			}},
			ExitCode: fakeappserver.Int(0),
		}),
	})
	t.Cleanup(cfg.Close)

	for _, kv := range cfg.Env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		t.Setenv(key, value)
	}

	workspaceRoot := filepath.Join(t.TempDir(), "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-1")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}

	client, err := startAppServer(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderCodex,
		Transport:       agentruntime.TransportAppServer,
		Command:         cfg.Command,
		Workspace:       workspace,
		WorkspaceRoot:   workspaceRoot,
		IssueID:         "issue-1",
		IssueIdentifier: "ISS-1",
		Permissions: agentruntime.PermissionConfig{
			Providers: map[agentruntime.Provider]agentruntime.ProviderPermissionConfig{
				agentruntime.ProviderCodex: {
					ThreadSandbox:     "workspace-write",
					TurnSandboxPolicy: map[string]interface{}{"type": "workspaceWrite"},
					CollaborationMode: "default",
				},
			},
		},
	}, agentruntime.Observers{})
	if err != nil {
		t.Fatalf("startAppServer: %v", err)
	}
	defer func() { _ = client.Close() }()

	if session := client.Session(); session == nil || session.IssueIdentifier != "ISS-1" {
		t.Fatalf("unexpected app-server session: %+v", session)
	}
	if output := client.Output(); !strings.Contains(output, "thread-app-server") {
		t.Fatalf("expected startup protocol output, got %q", output)
	}

	var started agentruntime.Session
	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "follow-up",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "hello"}},
	}, func(session *agentruntime.Session) {
		if session != nil {
			started = session.Clone()
		}
	}); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if started.TurnID != "turn-app-server" {
		t.Fatalf("expected started session to be reported, got %+v", started)
	}

	var nilClient *appServerClient
	if session := nilClient.Session(); session != nil {
		t.Fatalf("expected nil app-server client session, got %+v", session)
	}
	if output := nilClient.Output(); output != "" {
		t.Fatalf("expected nil app-server client output to be empty, got %q", output)
	}
	if err := nilClient.Close(); err != nil {
		t.Fatalf("expected nil app-server client close to succeed, got %v", err)
	}
}

func TestAppServerClientRunTurnRejectsUnsupportedInput(t *testing.T) {
	client := &appServerClient{}
	err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Input: []agentruntime.InputItem{{Kind: "unsupported"}},
	}, nil)
	if err == nil {
		t.Fatal("expected unsupported input to fail")
	}
}
