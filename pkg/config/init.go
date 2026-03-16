package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/olhapi/maestro/internal/appserver"
)

var (
	ErrWorkflowExists        = errors.New("workflow file already exists")
	ErrWorkflowInitCancelled = errors.New("workflow initialization cancelled")
	ErrInvalidInitAgentMode  = errors.New("invalid workflow init agent mode")
	ErrInvalidSandboxProfile = errors.New("invalid workflow init sandbox profile")
)

const (
	SandboxProfileCareful = "careful"
	SandboxProfileYolo    = "yolo"
	SandboxProfileSecure  = "secure"
)

type InitOptions struct {
	WorkspaceRoot  string
	CodexCommand   string
	AgentMode      string
	SandboxProfile string
	Interactive    bool
	Force          bool
	Stdin          io.Reader
	Stdout         io.Writer
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
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", path)
		}
		if !opts.Force {
			if !opts.Interactive {
				return fmt.Errorf("%w: %s already exists; rerun with --force to overwrite", ErrWorkflowExists, path)
			}
			if !confirmOverwrite(opts, path) {
				return fmt.Errorf("%w: existing WORKFLOW.md left unchanged at %s", ErrWorkflowInitCancelled, path)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	answers, err := resolveInitOptions(path, opts)
	if err != nil {
		return err
	}

	content := buildWorkflowFile(answers)
	return os.WriteFile(path, []byte(content), 0o644)
}

func defaultInitOptions() InitOptions {
	cfg := DefaultInitConfig()
	return InitOptions{
		WorkspaceRoot:  cfg.Workspace.Root,
		CodexCommand:   cfg.Codex.Command,
		AgentMode:      cfg.Agent.Mode,
		SandboxProfile: SandboxProfileCareful,
	}
}

func resolveInitOptions(path string, opts InitOptions) (InitOptions, error) {
	answers := defaultInitOptions()
	if opts.Interactive {
		answers = promptInitOptions(path, opts, answers)
	}
	if strings.TrimSpace(opts.WorkspaceRoot) != "" {
		answers.WorkspaceRoot = strings.TrimSpace(opts.WorkspaceRoot)
	}
	if strings.TrimSpace(opts.CodexCommand) != "" {
		answers.CodexCommand = strings.TrimSpace(opts.CodexCommand)
	}
	if strings.TrimSpace(opts.AgentMode) != "" {
		mode, err := validateInitAgentMode(opts.AgentMode)
		if err != nil {
			return InitOptions{}, err
		}
		answers.AgentMode = mode
	}
	if strings.TrimSpace(opts.SandboxProfile) != "" {
		profile, err := validateSandboxProfile(opts.SandboxProfile)
		if err != nil {
			return InitOptions{}, err
		}
		answers.SandboxProfile = profile
	}
	return answers, nil
}

func promptInitOptions(path string, opts InitOptions, defaults InitOptions) InitOptions {
	reader := newInitReader(opts.Stdin)
	writer := opts.Stdout
	if writer == nil {
		writer = os.Stdout
	}

	fmt.Fprintf(writer, "Target workflow file: %s\n", path)
	if strings.TrimSpace(opts.WorkspaceRoot) == "" {
		defaults.WorkspaceRoot = promptLine(reader, writer, "Workspace root", defaults.WorkspaceRoot)
	}

	customRuntime := strings.TrimSpace(opts.CodexCommand) != "" || strings.TrimSpace(opts.AgentMode) != ""
	if !customRuntime {
		customRuntime = promptRuntimeChoice(reader, writer, defaults.CodexCommand)
	}
	if customRuntime {
		if strings.TrimSpace(opts.CodexCommand) == "" {
			defaults.CodexCommand = promptLine(reader, writer, "Codex command", defaults.CodexCommand)
		}
		if strings.TrimSpace(opts.AgentMode) == "" {
			defaults.AgentMode = promptAgentMode(reader, writer, defaults.AgentMode)
		}
	}
	if strings.TrimSpace(opts.SandboxProfile) == "" {
		defaults.SandboxProfile = promptSandboxProfile(reader, writer, defaults.SandboxProfile)
	}
	return defaults
}

func confirmOverwrite(opts InitOptions, path string) bool {
	reader := newInitReader(opts.Stdin)
	writer := opts.Stdout
	if writer == nil {
		writer = os.Stdout
	}
	label := fmt.Sprintf("WORKFLOW.md already exists at %s. Overwrite?", path)
	return promptConfirm(reader, writer, label, false)
}

func newInitReader(r io.Reader) *bufio.Reader {
	if r == nil {
		return bufio.NewReader(os.Stdin)
	}
	return bufio.NewReader(r)
}

func promptRuntimeChoice(reader *bufio.Reader, writer io.Writer, recommendedCommand string) bool {
	status, err := appserver.DetectCodexVersion(recommendedCommand)
	recommendedLabel := recommendedCommand + " (recommended)"
	switch {
	case err == nil && status.Actual != "":
		recommendedLabel = fmt.Sprintf("%s (recommended, detected Codex %s)", recommendedCommand, status.Actual)
	case err != nil:
		recommendedLabel = fmt.Sprintf("%s (recommended, verify will check installation)", recommendedCommand)
	}
	fmt.Fprintln(writer, "Runtime setup:")
	fmt.Fprintf(writer, "  1) %s\n", recommendedLabel)
	fmt.Fprintln(writer, "  2) Custom / advanced")
	choice := promptLine(reader, writer, "Runtime selection", "1")
	choice = strings.TrimSpace(strings.ToLower(choice))
	return choice == "2" || choice == "custom" || choice == "advanced"
}

func promptAgentMode(reader *bufio.Reader, writer io.Writer, fallback string) string {
	mode := promptLine(reader, writer, "Agent mode (app_server|stdio)", fallback)
	return normalizePromptAgentMode(mode, fallback)
}

func promptSandboxProfile(reader *bufio.Reader, writer io.Writer, fallback string) string {
	fmt.Fprintln(writer, "Sandbox access:")
	fmt.Fprintln(writer, "  1) Careful (recommended, workspace-write with network access)")
	fmt.Fprintln(writer, "  2) Secure (workspace-write with network disabled)")
	fmt.Fprintln(writer, "  3) YOLO (danger-full-access with network access)")
	choice := promptLine(reader, writer, "Sandbox profile", sandboxProfilePromptDefault(fallback))
	return normalizePromptSandboxProfile(choice, fallback)
}

func promptConfirm(reader *bufio.Reader, writer io.Writer, label string, defaultYes bool) bool {
	fallback := "y/N"
	if defaultYes {
		fallback = "Y/n"
	}
	fmt.Fprintf(writer, "%s [%s]: ", label, fallback)
	line, err := reader.ReadString('\n')
	if err != nil {
		return defaultYes
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
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

func validateInitAgentMode(raw string) (string, error) {
	mode := strings.TrimSpace(strings.ToLower(raw))
	switch mode {
	case AgentModeAppServer, AgentModeStdio:
		return mode, nil
	case "":
		return "", fmt.Errorf("%w: expected %s or %s", ErrInvalidInitAgentMode, AgentModeAppServer, AgentModeStdio)
	default:
		return "", fmt.Errorf("%w: %q (expected %s or %s)", ErrInvalidInitAgentMode, raw, AgentModeAppServer, AgentModeStdio)
	}
}

func normalizePromptAgentMode(raw, fallback string) string {
	mode := strings.TrimSpace(raw)
	if mode == "" {
		return fallback
	}
	validated, err := validateInitAgentMode(mode)
	if err != nil {
		return fallback
	}
	return validated
}

func validateSandboxProfile(raw string) (string, error) {
	profile := strings.TrimSpace(strings.ToLower(raw))
	switch profile {
	case SandboxProfileCareful, SandboxProfileYolo, SandboxProfileSecure:
		return profile, nil
	case "":
		return "", fmt.Errorf("%w: expected %s, %s, or %s", ErrInvalidSandboxProfile, SandboxProfileCareful, SandboxProfileSecure, SandboxProfileYolo)
	default:
		return "", fmt.Errorf("%w: %q (expected %s, %s, or %s)", ErrInvalidSandboxProfile, raw, SandboxProfileCareful, SandboxProfileSecure, SandboxProfileYolo)
	}
}

func normalizePromptSandboxProfile(raw, fallback string) string {
	choice := strings.TrimSpace(strings.ToLower(raw))
	switch choice {
	case "1":
		return SandboxProfileCareful
	case "2":
		return SandboxProfileSecure
	case "3":
		return SandboxProfileYolo
	case "careful", "secure", "yolo":
		return choice
	case "":
		return fallback
	default:
		validated, err := validateSandboxProfile(choice)
		if err != nil {
			return fallback
		}
		return validated
	}
}

func sandboxProfilePromptDefault(fallback string) string {
	switch strings.TrimSpace(strings.ToLower(fallback)) {
	case SandboxProfileSecure:
		return "2"
	case SandboxProfileYolo:
		return "3"
	default:
		return "1"
	}
}

func buildWorkflowFile(opts InitOptions) string {
	cfg := DefaultInitConfig()
	if strings.TrimSpace(opts.WorkspaceRoot) != "" {
		cfg.Workspace.Root = strings.TrimSpace(opts.WorkspaceRoot)
	}
	if strings.TrimSpace(opts.CodexCommand) != "" {
		cfg.Codex.Command = strings.TrimSpace(opts.CodexCommand)
	}
	if strings.TrimSpace(opts.AgentMode) != "" {
		cfg.Agent.Mode = normalizePromptAgentMode(opts.AgentMode, cfg.Agent.Mode)
	}
	applySandboxProfile(&cfg, opts.SandboxProfile)
	turnPolicyType, _ := cfg.Codex.TurnSandboxPolicy["type"].(string)
	if strings.TrimSpace(turnPolicyType) == "" {
		turnPolicyType = "workspaceWrite"
	}
	networkAccess, ok := cfg.Codex.TurnSandboxPolicy["networkAccess"].(bool)
	if !ok {
		networkAccess = true
	}
	reviewPrompt := indentBlock(DefaultInitReviewPromptTemplate(), "      ")
	donePrompt := indentBlock(DefaultInitDonePromptTemplate(), "      ")
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
  interval_ms: %d

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
  timeout_ms: %d

# Optional extra prompts for later workflow phases.
phases:
  review:
    # Enable a dedicated review pass after implementation. Other option: false.
    enabled: %t
    # Prompt rendered when the issue enters review. Uses the same template variables
    # as the main prompt, such as issue.*, project.*, phase, and attempt.
    prompt: |
%s
  done:
    # Enable a dedicated finalization pass after implementation is otherwise complete.
    enabled: %t
    # Prompt rendered when the issue enters done for project-specific wrap-up steps.
    prompt: |
%s

# Agent runtime settings.
agent:
  # Maximum concurrent issues per project when dispatch_mode is parallel.
  max_concurrent_agents: %d
  # Maximum turns Maestro gives Codex before ending an attempt.
  max_turns: %d
  # Maximum delay between automatic retries after failed attempts.
  max_retry_backoff_ms: %d
  # Maximum automatic retry attempts for the same issue before Maestro stops retrying.
  max_automatic_retries: %d
  # Agent transport. Other options: app_server, stdio.
  mode: %s
  # Scheduling behavior. Other options: parallel, per_project_serial.
  dispatch_mode: %s

# Codex CLI launch and sandbox settings.
codex:
  # Exact command Maestro launches for the agent.
  command: %s
  # Expected codex --version. Mismatches warn but do not hard-fail.
  expected_version: %s
  # Approval mode for Codex. Other string options: on-request, on-failure, untrusted.
  # A structured reject object is also supported for per-category rejection policies.
  approval_policy: %v
  # Initial collaboration mode for fresh app_server threads. Other option: default.
  # Ignored for stdio runs and resumed threads.
  initial_collaboration_mode: %s
  # Setup presets: YOLO => danger-full-access, Careful => workspace-write, Secure => workspace-write without network.
  # Thread-level sandbox. Other options: read-only, workspace-write, danger-full-access.
  thread_sandbox: %s
  turn_sandbox_policy:
    # Per-turn sandbox policy. Other policy types: readOnly, externalSandbox, dangerFullAccess.
    type: %s
    # Network access during a turn. For externalSandbox, the schema also allows enabled/restricted.
    networkAccess: %t
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
  turn_timeout_ms: %d
  # Maximum time to wait for streamed output before considering the stream stalled.
  read_timeout_ms: %d
  # Maximum idle time without Codex activity before Maestro aborts the turn.
  stall_timeout_ms: %d
---

%s
`, cfg.Tracker.Kind, cfg.Tracker.Kind, cfg.Polling.IntervalMs, cfg.Workspace.Root, cfg.Hooks.TimeoutMs, cfg.Phases.Review.Enabled, reviewPrompt, cfg.Phases.Done.Enabled, donePrompt, cfg.Agent.MaxConcurrentAgents, cfg.Agent.MaxTurns, cfg.Agent.MaxRetryBackoffMs, cfg.Agent.MaxAutomaticRetries, cfg.Agent.Mode, cfg.Agent.DispatchMode, cfg.Codex.Command, cfg.Codex.ExpectedVersion, cfg.Codex.ApprovalPolicy, cfg.Codex.InitialCollaborationMode, cfg.Codex.ThreadSandbox, turnPolicyType, networkAccess, cfg.Codex.TurnTimeoutMs, cfg.Codex.ReadTimeoutMs, cfg.Codex.StallTimeoutMs, DefaultPromptTemplate()))
}

func indentBlock(text, prefix string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func applySandboxProfile(cfg *Config, rawProfile string) {
	if cfg == nil {
		return
	}
	profile := normalizePromptSandboxProfile(rawProfile, SandboxProfileCareful)
	switch profile {
	case SandboxProfileYolo:
		cfg.Codex.ThreadSandbox = "danger-full-access"
		cfg.Codex.TurnSandboxPolicy = map[string]interface{}{
			"type":          "dangerFullAccess",
			"networkAccess": true,
		}
	case SandboxProfileSecure:
		cfg.Codex.ThreadSandbox = "workspace-write"
		cfg.Codex.TurnSandboxPolicy = map[string]interface{}{
			"type":          "workspaceWrite",
			"networkAccess": false,
		}
	default:
		cfg.Codex.ThreadSandbox = "workspace-write"
		cfg.Codex.TurnSandboxPolicy = map[string]interface{}{
			"type":          "workspaceWrite",
			"networkAccess": true,
		}
	}
}
