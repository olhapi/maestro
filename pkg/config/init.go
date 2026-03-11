package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/olhapi/maestro/internal/codexschema"
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
	return EnsureWorkflowAtPath(path, opts)
}

func EnsureWorkflowAtPath(path string, opts InitOptions) (string, bool, error) {
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	} else if !os.IsNotExist(err) {
		return path, false, err
	}
	if err := InitWorkflowAtPath(path, opts); err != nil {
		return path, false, err
	}
	return path, true, nil
}

func InitWorkflow(repoPath string, opts InitOptions) error {
	return InitWorkflowAtPath(WorkflowPath(repoPath), opts)
}

func InitWorkflowAtPath(path string, opts InitOptions) error {
	if strings.TrimSpace(path) == "" {
		path = WorkflowPath("")
	}
	if !filepath.IsAbs(path) {
		cwd, _ := os.Getwd()
		path = filepath.Join(cwd, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
	return os.WriteFile(path, []byte(content), 0o644)
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
  interval_ms: 10000
workspace:
  root: %s
hooks:
  timeout_ms: 60000
phases:
  review:
    enabled: false
    prompt: |
      Review the implementation for issue {{ issue.identifier }} in the current workspace.
      Run focused verification, fix any issues you find, move the issue back to in_progress if more work is needed, and move it to done when review is complete.
  done:
    enabled: false
    prompt: |
      Finalize issue {{ issue.identifier }} from the current workspace.
      Perform the project-specific done steps, such as opening or updating a PR, merging, or other release bookkeeping, while keeping the issue in done unless it truly needs to be reopened.
agent:
  max_concurrent_agents: 3
  max_turns: 4
  max_retry_backoff_ms: 60000
  max_automatic_retries: 8
  mode: %s
  dispatch_mode: parallel
codex:
  command: %s
  expected_version: %s
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
    networkAccess: true
  turn_timeout_ms: 600000
  read_timeout_ms: 5000
  stall_timeout_ms: 300000
---

You are working on issue {{ issue.identifier }}.

Current phase: {{ phase }}

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
`, TrackerKindKanban, opts.WorkspaceRoot, opts.AgentMode, opts.CodexCommand, codexschema.SupportedVersion))
}
