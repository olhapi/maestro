package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver/protocol"
	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
)

func TestClientThreadAccessors(t *testing.T) {
	client := &Client{session: &Session{}}
	if client.ThreadResumed() {
		t.Fatal("expected new client to report thread not resumed")
	}
	if got := client.activeThreadConfig(); got != "" {
		t.Fatalf("expected empty active thread config, got %q", got)
	}

	client.activateThread("thread-1", "  sandbox-write  ", true)
	if !client.ThreadResumed() {
		t.Fatal("expected activateThread to record resumed state")
	}
	if got := client.activeThreadConfig(); got != "sandbox-write" {
		t.Fatalf("unexpected active thread config %q", got)
	}

	client.activateThread("thread-2", " ", false)
	if client.ThreadResumed() {
		t.Fatal("expected activateThread to clear resumed state")
	}
	if got := client.activeThreadConfig(); got != "" {
		t.Fatalf("expected blank active thread config to be trimmed, got %q", got)
	}
}

func TestRunTurnWithInputsCompletesTurn(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-1")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	scenario := baseScenario("thread-inputs", "turn-inputs",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{
					"threadId": "thread-inputs",
					"turn":     map[string]interface{}{"id": "turn-inputs"},
				},
			},
		},
	)
	scenario.Steps[3].ExitCode = fakeappserver.Int(0)

	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	if err := client.RunTurnWithInputs(context.Background(), []gen.UserInputElement{protocol.TextInput("prompt")}, "input turn"); err != nil {
		t.Fatalf("RunTurnWithInputs: %v", err)
	}
	if session := client.Session(); session == nil || session.SessionID != "thread-inputs-turn-inputs" {
		t.Fatalf("unexpected session after RunTurnWithInputs: %+v", session)
	}
}

func TestStartDefaultingAndValidationBranches(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-DEFAULTS")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Start(context.Background(), ClientConfig{
		Executable:    "/bin/true",
		Workspace:     filepath.Join(tmpDir, "outside"),
		WorkspaceRoot: workspaceRoot,
	}); err == nil {
		t.Fatal("expected invalid workspace to fail")
	}

	if _, err := Start(context.Background(), ClientConfig{
		Workspace:     workspace,
		WorkspaceRoot: workspaceRoot,
	}); err == nil {
		t.Fatal("expected missing executable to fail")
	}

	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}},
				},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-defaults"}}}},
				},
			},
		},
	}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	codexPath := filepath.Join(t.TempDir(), "codex")
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
	}
	args := make([]string, 0, len(cfg.Args))
	for _, arg := range cfg.Args {
		args = append(args, quote(arg))
	}
	script := "#!/bin/sh\nexec " + quote(cfg.Executable) + " " + strings.Join(args, " ") + "\n"
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile codex wrapper: %v", err)
	}
	cfg.Executable = codexPath
	cfg.Args = nil
	cfg.ThreadSandbox = ""
	cfg.InitialCollaborationMode = ""
	cfg.ReadTimeout = 0
	cfg.StallTimeout = 0
	cfg.ApprovalPolicy = nil
	cfg.CodexCommand = ""

	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	if client.cfg.ThreadSandbox != "workspace-write" {
		t.Fatalf("expected default thread sandbox, got %q", client.cfg.ThreadSandbox)
	}
	if client.cfg.InitialCollaborationMode != defaultInitialCollaborationMode {
		t.Fatalf("expected default collaboration mode, got %q", client.cfg.InitialCollaborationMode)
	}
	if client.cfg.ReadTimeout != 5*time.Second {
		t.Fatalf("expected default read timeout, got %s", client.cfg.ReadTimeout)
	}
	if client.cfg.StallTimeout != 5*time.Minute {
		t.Fatalf("expected default stall timeout, got %s", client.cfg.StallTimeout)
	}
	if client.cfg.CodexCommand != codexPath {
		t.Fatalf("expected codex command inference, got %q", client.cfg.CodexCommand)
	}
}
