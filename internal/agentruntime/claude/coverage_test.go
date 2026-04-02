package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
)

func TestStartRejectsUnsupportedConfiguration(t *testing.T) {
	tests := []struct {
		name string
		spec agentruntime.RuntimeSpec
	}{
		{
			name: "unsupported provider",
			spec: agentruntime.RuntimeSpec{
				Provider:  agentruntime.Provider("mistral"),
				Transport: agentruntime.TransportStdio,
				Command:   "cat",
			},
		},
		{
			name: "unsupported transport",
			spec: agentruntime.RuntimeSpec{
				Provider:  agentruntime.ProviderClaude,
				Transport: agentruntime.TransportAppServer,
				Command:   "cat",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec
			spec.Workspace = t.TempDir()

			_, err := Start(context.Background(), spec, agentruntime.Observers{})
			if err == nil {
				t.Fatal("expected unsupported capability error")
			}
			if !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
				t.Fatalf("expected unsupported capability error, got %v", err)
			}
		})
	}
}

func TestStdioRuntimeRejectsUnsupportedInputKinds(t *testing.T) {
	tests := []struct {
		name         string
		input        []agentruntime.InputItem
		wantFragment string
	}{
		{
			name: "local image",
			input: []agentruntime.InputItem{{
				Kind: agentruntime.InputItemLocalImage,
				Path: "image.png",
			}},
			wantFragment: "local_image",
		},
		{
			name: "unknown kind",
			input: []agentruntime.InputItem{{
				Kind: agentruntime.InputItemKind("mystery"),
				Text: "ignored",
			}},
			wantFragment: `input kind "mystery"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := mustStartStdioRuntime(t, agentruntime.Observers{})
			t.Cleanup(func() {
				_ = client.Close()
			})

			err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
				Input: tc.input,
			}, nil)
			if err == nil {
				t.Fatal("expected unsupported capability error")
			}
			if !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
				t.Fatalf("expected unsupported capability error, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantFragment) {
				t.Fatalf("expected error to mention %q, got %v", tc.wantFragment, err)
			}
		})
	}
}

func TestStdioRuntimeRequiresDBPath(t *testing.T) {
	_, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderClaude,
		Transport:       agentruntime.TransportStdio,
		Command:         writeShellScript(t, "cat"),
		Workspace:       t.TempDir(),
		IssueID:         "iss-claude",
		IssueIdentifier: "ISS-CLAUDE",
	}, agentruntime.Observers{})
	if err == nil {
		t.Fatal("expected DB path error")
	}
	if !strings.Contains(err.Error(), "db path") {
		t.Fatalf("expected DB path error, got %v", err)
	}
}

func TestStdioRuntimeCombinesOutputBranches(t *testing.T) {
	tests := []struct {
		name          string
		scriptBody    string
		wantOutput    string
		wantTurnEvent string
		wantErr       bool
	}{
		{
			name:          "stdout and stderr",
			scriptBody:    `printf 'stdout'; printf 'stderr' >&2`,
			wantOutput:    "stdout\nstderr",
			wantTurnEvent: "turn.completed",
		},
		{
			name:          "stderr failure",
			scriptBody:    "printf 'stderr' >&2\nexit 7",
			wantOutput:    "stderr",
			wantTurnEvent: "turn.failed",
			wantErr:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := mustStartStdioRuntimeWithScript(t, tc.scriptBody, agentruntime.Observers{})
			t.Cleanup(func() {
				_ = client.Close()
			})

			err := client.RunTurn(context.Background(), agentruntime.TurnRequest{}, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected RunTurn to fail")
				}
			} else if err != nil {
				t.Fatalf("RunTurn: %v", err)
			}

			if got := client.Output(); got != tc.wantOutput {
				t.Fatalf("unexpected output: got %q want %q", got, tc.wantOutput)
			}

			session := client.Session()
			if session == nil {
				t.Fatal("expected session snapshot")
			}
			if session.LastEvent != tc.wantTurnEvent {
				t.Fatalf("unexpected final event: got %q want %q", session.LastEvent, tc.wantTurnEvent)
			}
		})
	}
}

func TestStdioRuntimeUpdatesPermissionsAndNilReceivers(t *testing.T) {
	client := mustStartStdioRuntime(t, agentruntime.Observers{})
	t.Cleanup(func() {
		_ = client.Close()
	})

	stdio := client.(*stdioClient)
	permissions := agentruntime.PermissionConfig{
		ApprovalPolicy: map[string]interface{}{"mode": "never"},
		ThreadSandbox:  "workspace-write",
		Metadata: map[string]interface{}{
			"source": "test",
		},
	}
	stdio.UpdatePermissions(permissions)

	if stdio.spec.Permissions.ThreadSandbox != permissions.ThreadSandbox {
		t.Fatalf("expected permissions to update, got %+v", stdio.spec.Permissions)
	}
	if stdio.spec.Permissions.Metadata["source"] != "test" {
		t.Fatalf("expected permission metadata to update, got %+v", stdio.spec.Permissions)
	}

	if err := client.RespondToInteraction(context.Background(), "interaction-1", agentruntime.PendingInteractionResponse{}); !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
		t.Fatalf("expected unsupported capability error, got %v", err)
	}

	var nilClient *stdioClient
	if nilClient.Session() != nil {
		t.Fatal("expected nil session from nil receiver")
	}
	if nilClient.Output() != "" {
		t.Fatal("expected empty output from nil receiver")
	}
}

func TestStdioRuntimeEmitsLifecycleAndObservers(t *testing.T) {
	sessionCh := make(chan agentruntime.Session, 4)
	activityCh := make(chan agentruntime.ActivityEvent, 4)

	client := mustStartStdioRuntime(t, agentruntime.Observers{
		OnSessionUpdate: func(session *agentruntime.Session) {
			if session != nil {
				sessionCh <- session.Clone()
			}
		},
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			activityCh <- event.Clone()
		},
	})
	t.Cleanup(func() {
		_ = client.Close()
	})

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

	if started.LastEvent != "turn.started" || started.ThreadID == "" || started.TurnID == "" {
		t.Fatalf("expected started session to reflect turn start, got %+v", started)
	}
	if started.Metadata["provider"] != string(agentruntime.ProviderClaude) || started.Metadata["transport"] != string(agentruntime.TransportStdio) {
		t.Fatalf("expected started session metadata to identify claude stdio, got %+v", started.Metadata)
	}

	if out := client.Output(); strings.TrimSpace(out) != "hello" {
		t.Fatalf("unexpected output: %q", out)
	}

	session := client.Session()
	if session == nil {
		t.Fatal("expected session snapshot")
	}
	if session.ThreadID == "" || session.TurnID == "" || session.SessionID == "" {
		t.Fatalf("expected session identifiers to be populated, got %+v", session)
	}
	if session.TurnsStarted != 1 || session.TurnsCompleted != 1 {
		t.Fatalf("expected turn counters to update, got %+v", session)
	}

	updates := collectSessions(t, sessionCh, 2)
	if updates[0].LastEvent != "turn.started" || updates[1].LastEvent != "turn.completed" {
		t.Fatalf("unexpected session updates: %+v", updates)
	}

	events := collectActivityEvents(t, activityCh, 2)
	foundItemCompleted := false
	foundTurnCompleted := false
	for _, event := range events {
		if event.Type == "item.completed" {
			foundItemCompleted = true
		}
		if event.Type == "turn.completed" {
			foundTurnCompleted = true
		}
		if event.Metadata["provider"] != string(agentruntime.ProviderClaude) || event.Metadata["transport"] != string(agentruntime.TransportStdio) {
			t.Fatalf("expected activity metadata to identify claude stdio, got %+v", event.Metadata)
		}
	}
	if !foundItemCompleted || !foundTurnCompleted {
		t.Fatalf("unexpected activity events: %+v", events)
	}
}

func TestStdioClientCloseNilReceiver(t *testing.T) {
	var client *stdioClient
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestBuildClaudeCommandRequiresDBPathAndQuotesCommand(t *testing.T) {
	if _, _, err := buildClaudeCommand(agentruntime.RuntimeSpec{}); err == nil {
		t.Fatal("expected db path validation error")
	}

	command, cleanup, err := buildClaudeCommand(agentruntime.RuntimeSpec{
		Command: "O'Reilly",
		DBPath:  filepath.Join(t.TempDir(), "maestro.db"),
	})
	if err != nil {
		t.Fatalf("buildClaudeCommand: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected cleanup function")
	}
	t.Cleanup(cleanup)

	if !strings.Contains(command, "--mcp-config") || !strings.Contains(command, "--strict-mcp-config") {
		t.Fatalf("expected command to include bridge flags, got %q", command)
	}
	if !strings.Contains(command, "--settings") {
		t.Fatalf("expected command to include the session overlay, got %q", command)
	}
	if got := shellQuoteArg(""); got != "''" {
		t.Fatalf("expected empty shell quote, got %q", got)
	}
	if got := shellQuoteArg("O'Reilly"); got != `'O'"'"'Reilly'` {
		t.Fatalf("expected apostrophe shell quote, got %q", got)
	}
}

func mustStartStdioRuntimeWithScript(t *testing.T, scriptBody string, observers agentruntime.Observers) agentruntime.Client {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" + strings.TrimSpace(scriptBody) + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderClaude,
		Transport:       agentruntime.TransportStdio,
		Command:         scriptPath,
		Workspace:       t.TempDir(),
		IssueID:         "iss-claude",
		IssueIdentifier: "ISS-CLAUDE",
		DBPath:          filepath.Join(t.TempDir(), "maestro.db"),
	}, observers)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return client
}

func collectSessions(t *testing.T, ch <-chan agentruntime.Session, want int) []agentruntime.Session {
	t.Helper()
	out := make([]agentruntime.Session, 0, want)
	deadline := time.After(time.Second)
	for len(out) < want {
		select {
		case session := <-ch:
			out = append(out, session)
		case <-deadline:
			t.Fatalf("timed out waiting for %d session updates, got %d", want, len(out))
		}
	}
	return out
}

func collectActivityEvents(t *testing.T, ch <-chan agentruntime.ActivityEvent, want int) []agentruntime.ActivityEvent {
	t.Helper()
	out := make([]agentruntime.ActivityEvent, 0, want)
	deadline := time.After(time.Second)
	for len(out) < want {
		select {
		case event := <-ch:
			out = append(out, event)
		case <-deadline:
			t.Fatalf("timed out waiting for %d activity events, got %d", want, len(out))
		}
	}
	return out
}

func writeShellScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write shell script: %v", err)
	}
	return path
}
