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
# Tracker provider configuration. Supported tracker kind today: %s.
tracker:
  # Tracker backend to read and write issues from.
  kind: %s
  # States that should be treated as active work and picked up by orchestration.
  active_states:
    - ready
    - in_progress
    - in_review
  # States that should be treated as terminal and left alone by orchestration.
  terminal_states:
    - done
    - cancelled

# How often Maestro scans the tracker for runnable work.
polling:
  interval_ms: 10000

# Where Maestro creates per-issue workspaces. Relative paths resolve from the repo root;
# absolute paths, $ENV_VAR paths, and ~/ paths are also supported.
workspace:
  root: %s

# Optional shell hooks that run inside the issue workspace.
hooks:
  # Runs immediately after Maestro creates or reuses a workspace.
  # after_create: ./scripts/after-create.sh
  # Runs before each agent attempt starts.
  # before_run: ./scripts/before-run.sh
  # Runs after each agent attempt finishes, even when the attempt fails.
  # after_run: ./scripts/after-run.sh
  # Runs before Maestro removes a workspace during cleanup.
  # before_remove: ./scripts/before-remove.sh
  # Maximum runtime for each hook command before Maestro terminates it.
  timeout_ms: 60000

# Optional extra prompts for later workflow phases.
phases:
  review:
    # Enable a dedicated review pass after implementation. Other option: false.
    enabled: false
    # Prompt rendered when the issue enters review. Uses the same template variables
    # as the main prompt, such as issue.*, phase, and attempt.
    prompt: |
      Review the implementation for issue {{ issue.identifier }} in the current workspace.
      Run focused verification, fix any issues you find, move the issue back to in_progress if more work is needed, and move it to done when review is complete.
  done:
    # Enable a dedicated finalization pass after implementation is otherwise complete.
    enabled: false
    # Prompt rendered when the issue enters done for project-specific wrap-up steps.
    prompt: |
      Finalize issue {{ issue.identifier }} from the current workspace.
      Perform the project-specific done steps, such as opening or updating a PR, merging, or other release bookkeeping, while keeping the issue in done unless it truly needs to be reopened.

# Agent runtime settings.
agent:
  # Maximum concurrent issues per project when dispatch_mode is parallel.
  max_concurrent_agents: 3
  # Maximum turns Maestro gives Codex before ending an attempt.
  max_turns: 4
  # Maximum delay between automatic retries after failed attempts.
  max_retry_backoff_ms: 60000
  # Maximum automatic retry attempts for the same issue before Maestro stops retrying.
  max_automatic_retries: 8
  # Agent transport. Other options: app_server, stdio.
  mode: %s
  # Scheduling behavior. Other options: parallel, per_project_serial.
  dispatch_mode: parallel

# Codex CLI launch and sandbox settings.
codex:
  # Exact command Maestro launches for the agent.
  command: %s
  # Expected codex --version. Mismatches warn but do not hard-fail.
  expected_version: %s
  # Approval mode for Codex. Other string options: on-request, on-failure, untrusted.
  # A structured reject object is also supported for per-category rejection policies.
  approval_policy: never
  # Thread-level sandbox. Other options: read-only, workspace-write, danger-full-access.
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    # Per-turn sandbox policy. Other policy types: readOnly, externalSandbox, dangerFullAccess.
    type: workspaceWrite
    # Network access during a turn. For externalSandbox, the schema also allows enabled/restricted.
    networkAccess: true
    # Optional for workspaceWrite. If omitted, Maestro fills writable roots automatically.
    # writableRoots:
    #   - /absolute/path/to/repo
    # Optional for workspaceWrite. Other options: fullAccess or restricted.
    # readOnlyAccess:
    #   type: fullAccess
    #   # For restricted, you can also set includePlatformDefaults and readableRoots.
    # Optional for workspaceWrite only.
    # excludeTmpdirEnvVar: false
    # Optional for workspaceWrite only.
    # excludeSlashTmp: false
  # Maximum total runtime for one turn before Maestro cancels it.
  turn_timeout_ms: 1800000
  # Maximum time to wait for streamed output before considering the stream stalled.
  read_timeout_ms: 10000
  # Maximum idle time without Codex activity before Maestro aborts the turn.
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
`, TrackerKindKanban, TrackerKindKanban, opts.WorkspaceRoot, opts.AgentMode, opts.CodexCommand, codexschema.SupportedVersion))
}
