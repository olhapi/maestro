package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/agentruntime"
)

func TestStdioRuntimeAttachesLiveMaestroMCPConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	argsPath := filepath.Join(t.TempDir(), "claude-args.txt")
	claudePath := writeFakeClaudeCLI(t)

	client, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderClaude,
		Transport:       agentruntime.TransportStdio,
		Command:         claudePath,
		Workspace:       t.TempDir(),
		IssueID:         "iss-1",
		IssueIdentifier: "ISS-1",
		DBPath:          dbPath,
		Env: append(os.Environ(),
			"CLAUDE_ARGS_PATH="+argsPath,
		),
	}, agentruntime.Observers{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "first turn",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "hello"}},
	}, nil); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	args := readLines(t, argsPath)
	if !containsArg(args, "--mcp-config") {
		t.Fatalf("expected claude args to include --mcp-config, got %#v", args)
	}
	if !containsArg(args, "--strict-mcp-config") {
		t.Fatalf("expected claude args to include --strict-mcp-config, got %#v", args)
	}
	configPath := argValueAfter(t, args, "--mcp-config")
	if configPath == "" {
		t.Fatalf("expected mcp config path in args, got %#v", args)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected MCP config file to exist before close, got %v", err)
	}

	attachment := readClaudeMCPConfig(t, configPath)
	servers, ok := attachment["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected mcpServers map in config, got %#v", attachment)
	}
	if len(servers) != 1 {
		t.Fatalf("expected a single Maestro MCP server, got %#v", servers)
	}
	maestro, ok := servers["maestro"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected maestro server in config, got %#v", servers)
	}
	if maestro["command"] != "maestro" {
		t.Fatalf("expected maestro command to stay on the existing CLI, got %#v", maestro["command"])
	}
	argsValue, ok := maestro["args"].([]interface{})
	if !ok {
		t.Fatalf("expected maestro args array, got %#v", maestro["args"])
	}
	wantArgs := []string{"mcp", "--db", dbPath}
	if len(argsValue) != len(wantArgs) {
		t.Fatalf("expected maestro args %#v, got %#v", wantArgs, argsValue)
	}
	for i, want := range wantArgs {
		if got, _ := argsValue[i].(string); got != want {
			t.Fatalf("expected maestro args %#v, got %#v", wantArgs, argsValue)
		}
	}

	if out := client.Output(); strings.TrimSpace(out) != "hello" {
		t.Fatalf("unexpected output: %q", out)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected MCP config file to be cleaned up, got %v", err)
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

func writeFakeClaudeCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := strings.TrimSpace(`
		#!/bin/sh
		: "${CLAUDE_ARGS_PATH:?}"
		printf '%s\n' "$@" > "$CLAUDE_ARGS_PATH"
		while [ "$#" -gt 0 ]; do
			shift
		done
		cat
	`) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}

func readClaudeMCPConfig(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read MCP config: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse MCP config: %v", err)
	}
	return parsed
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
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

func errorsIsUnsupported(err error) bool {
	return errors.Is(err, agentruntime.ErrUnsupportedCapability)
}
