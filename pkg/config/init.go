package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type InitOptions struct {
	WorkspaceRoot string
	CodexCommand  string
	AgentMode     string
	Interactive   bool
	Stdin         io.Reader
	Stdout        io.Writer
}

func EnsureWorkflow(repoPath string, opts InitOptions) (string, bool, error) {
	path := WorkflowPath(repoPath)
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	} else if !os.IsNotExist(err) {
		return path, false, err
	}
	if err := InitWorkflow(repoPath, opts); err != nil {
		return path, false, err
	}
	return path, true, nil
}

func InitWorkflow(repoPath string, opts InitOptions) error {
	if strings.TrimSpace(repoPath) == "" {
		repoPath, _ = os.Getwd()
	}
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		return err
	}

	answers := defaultInitOptions()
	if opts.Interactive {
		answers = promptInitOptions(opts, answers)
	}
	if strings.TrimSpace(opts.WorkspaceRoot) != "" {
		answers.WorkspaceRoot = strings.TrimSpace(opts.WorkspaceRoot)
	}
	if strings.TrimSpace(opts.CodexCommand) != "" {
		answers.CodexCommand = strings.TrimSpace(opts.CodexCommand)
	}
	if strings.TrimSpace(opts.AgentMode) != "" {
		answers.AgentMode = strings.TrimSpace(opts.AgentMode)
	}

	content := buildWorkflowFile(answers)
	return os.WriteFile(filepath.Join(repoPath, "WORKFLOW.md"), []byte(content), 0o644)
}

func defaultInitOptions() InitOptions {
	return InitOptions{
		WorkspaceRoot: "./workspaces",
		CodexCommand:  "codex app-server",
		AgentMode:     AgentModeAppServer,
	}
}

func promptInitOptions(opts InitOptions, defaults InitOptions) InitOptions {
	reader := bufio.NewReader(opts.Stdin)
	writer := opts.Stdout
	if reader == nil {
		reader = bufio.NewReader(os.Stdin)
	}
	if writer == nil {
		writer = os.Stdout
	}

	defaults.WorkspaceRoot = promptLine(reader, writer, "Workspace root", defaults.WorkspaceRoot)
	defaults.CodexCommand = promptLine(reader, writer, "Codex command", defaults.CodexCommand)
	mode := promptLine(reader, writer, "Agent mode (app_server|stdio)", defaults.AgentMode)
	mode = strings.TrimSpace(mode)
	if mode != AgentModeStdio {
		mode = AgentModeAppServer
	}
	defaults.AgentMode = mode
	return defaults
}

func promptLine(reader *bufio.Reader, writer io.Writer, label, fallback string) string {
	fmt.Fprintf(writer, "%s [%s]: ", label, fallback)
	line, err := reader.ReadString('\n')
	if err != nil {
		return fallback
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return fallback
	}
	return line
}

func buildWorkflowFile(opts InitOptions) string {
	return strings.TrimSpace(fmt.Sprintf(`
---
tracker:
  kind: %s
  active_states:
    - ready
    - in_progress
    - in_review
  terminal_states:
    - done
    - cancelled
polling:
  interval_ms: 30000
workspace:
  root: %s
hooks:
  timeout_ms: 60000
agent:
  max_concurrent_agents: 3
  max_turns: 20
  max_retry_backoff_ms: 300000
  mode: %s
codex:
  command: %s
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  turn_timeout_ms: 3600000
  read_timeout_ms: 5000
  stall_timeout_ms: 300000
---

You are working on issue {{ issue.identifier }}.

{%% if attempt %%}
Continuation attempt: {{ attempt }}
{%% endif %%}

Title: {{ issue.title }}
Description:
{%% if issue.description %%}
{{ issue.description }}
{%% else %%}
No description provided.
{%% endif %%}
`, TrackerKindKanban, opts.WorkspaceRoot, opts.AgentMode, opts.CodexCommand))
}
