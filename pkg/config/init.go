package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ErrWorkflowExists               = errors.New("workflow file already exists")
	ErrWorkflowInitCancelled        = errors.New("workflow initialization cancelled")
	ErrInvalidInitAgentMode         = errors.New("invalid workflow init agent mode")
	ErrInvalidInitDispatchMode      = errors.New("invalid workflow init dispatch mode")
	ErrInvalidInitApprovalPolicy    = errors.New("invalid workflow init approval policy")
	ErrInvalidInitCollaborationMode = errors.New("invalid workflow init collaboration mode")
)

type InitOptions struct {
	WorkspaceRoot            string
	CodexCommand             string
	AgentMode                string
	DispatchMode             string
	MaxConcurrentAgents      int
	MaxTurns                 int
	MaxAutomaticRetries      int
	ApprovalPolicy           string
	InitialCollaborationMode string
	Interactive              bool
	Force                    bool
	Stdin                    io.Reader
	Stdout                   io.Writer
}

type initChoice struct {
	Value       string
	Description string
	Aliases     []string
}

type initChoiceSet struct {
	Err     error
	Choices []initChoice
}

var (
	initAgentModeChoices = initChoiceSet{
		Err: ErrInvalidInitAgentMode,
		Choices: []initChoice{
			{Value: AgentModeAppServer, Description: "Use the Codex app-server protocol.", Aliases: []string{"app", "server", "app-server"}},
			{Value: AgentModeStdio, Description: "Use a codex exec style runner.", Aliases: []string{"std", "exec"}},
		},
	}
	initDispatchModeChoices = initChoiceSet{
		Err: ErrInvalidInitDispatchMode,
		Choices: []initChoice{
			{Value: DispatchModeParallel, Description: "Run multiple issues per project up to max_concurrent_agents.", Aliases: []string{"par"}},
			{Value: DispatchModePerProjectSerial, Description: "Run one issue at a time per project.", Aliases: []string{"serial", "per", "pps", "per-project-serial"}},
		},
	}
	initApprovalPolicyChoices = initChoiceSet{
		Err: ErrInvalidInitApprovalPolicy,
		Choices: []initChoice{
			{Value: "never", Description: "Keep unattended runs non-interactive."},
			{Value: "on-request", Description: "Allow approvals and questions during the run.", Aliases: []string{"req", "request", "on_request"}},
			{Value: "on-failure", Description: "Ask only when a step fails and needs recovery.", Aliases: []string{"fail", "failure", "on_failure"}},
			{Value: "untrusted", Description: "Use Codex's untrusted approval mode."},
		},
	}
	initCollaborationModeChoices = initChoiceSet{
		Err: ErrInvalidInitCollaborationMode,
		Choices: []initChoice{
			{Value: InitialCollaborationModeDefault, Description: "Start fresh app_server threads in execution mode.", Aliases: []string{"def"}},
			{Value: InitialCollaborationModePlan, Description: "Start fresh app_server threads in plan mode."},
		},
	}
)

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
		WorkspaceRoot:            cfg.Workspace.Root,
		CodexCommand:             cfg.Codex.Command,
		AgentMode:                cfg.Agent.Mode,
		DispatchMode:             cfg.Agent.DispatchMode,
		MaxConcurrentAgents:      cfg.Agent.MaxConcurrentAgents,
		MaxTurns:                 cfg.Agent.MaxTurns,
		MaxAutomaticRetries:      cfg.Agent.MaxAutomaticRetries,
		ApprovalPolicy:           strings.TrimSpace(fmt.Sprintf("%v", cfg.Codex.ApprovalPolicy)),
		InitialCollaborationMode: cfg.Codex.InitialCollaborationMode,
	}
}

func resolveInitOptions(path string, opts InitOptions) (InitOptions, error) {
	answers := defaultInitOptions()
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
	if strings.TrimSpace(opts.DispatchMode) != "" {
		dispatchMode, err := validateInitDispatchMode(opts.DispatchMode)
		if err != nil {
			return InitOptions{}, err
		}
		answers.DispatchMode = dispatchMode
	}
	if opts.MaxConcurrentAgents > 0 {
		answers.MaxConcurrentAgents = opts.MaxConcurrentAgents
	}
	if opts.MaxTurns > 0 {
		answers.MaxTurns = opts.MaxTurns
	}
	if opts.MaxAutomaticRetries > 0 {
		answers.MaxAutomaticRetries = opts.MaxAutomaticRetries
	}
	if strings.TrimSpace(opts.ApprovalPolicy) != "" {
		approvalPolicy, err := validateInitApprovalPolicy(opts.ApprovalPolicy)
		if err != nil {
			return InitOptions{}, err
		}
		answers.ApprovalPolicy = approvalPolicy
	}
	if strings.TrimSpace(opts.InitialCollaborationMode) != "" {
		collaborationMode, err := validateInitCollaborationMode(opts.InitialCollaborationMode)
		if err != nil {
			return InitOptions{}, err
		}
		answers.InitialCollaborationMode = collaborationMode
	}
	if opts.Interactive {
		answers = promptInitOptions(path, opts, answers)
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
	if strings.TrimSpace(opts.CodexCommand) == "" {
		defaults.CodexCommand = promptLine(reader, writer, "Codex command", defaults.CodexCommand)
	}
	if strings.TrimSpace(opts.AgentMode) == "" {
		defaults.AgentMode = promptAgentMode(reader, writer, defaults.AgentMode)
	}
	if strings.TrimSpace(opts.DispatchMode) == "" {
		defaults.DispatchMode = promptDispatchMode(reader, writer, defaults.DispatchMode)
	}
	if opts.MaxConcurrentAgents <= 0 {
		defaults.MaxConcurrentAgents = promptPositiveInt(reader, writer, "Max concurrent agents", defaults.MaxConcurrentAgents)
	}
	if opts.MaxTurns <= 0 {
		defaults.MaxTurns = promptPositiveInt(reader, writer, "Max turns", defaults.MaxTurns)
	}
	if opts.MaxAutomaticRetries <= 0 {
		defaults.MaxAutomaticRetries = promptPositiveInt(reader, writer, "Max automatic retries", defaults.MaxAutomaticRetries)
	}
	if defaults.AgentMode == AgentModeAppServer {
		if strings.TrimSpace(opts.ApprovalPolicy) == "" {
			defaults.ApprovalPolicy = promptApprovalPolicy(reader, writer, defaults.ApprovalPolicy)
		}
		if strings.TrimSpace(opts.InitialCollaborationMode) == "" {
			defaults.InitialCollaborationMode = promptInitialCollaborationMode(reader, writer, defaults.InitialCollaborationMode)
		}
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

func promptAgentMode(reader *bufio.Reader, writer io.Writer, fallback string) string {
	return promptChoice(reader, writer, "Agent mode", fallback, initAgentModeChoices)
}

func promptDispatchMode(reader *bufio.Reader, writer io.Writer, fallback string) string {
	return promptChoice(reader, writer, "Dispatch mode", fallback, initDispatchModeChoices)
}

func promptApprovalPolicy(reader *bufio.Reader, writer io.Writer, fallback string) string {
	return promptChoice(reader, writer, "Approval policy", fallback, initApprovalPolicyChoices)
}

func promptInitialCollaborationMode(reader *bufio.Reader, writer io.Writer, fallback string) string {
	return promptChoice(reader, writer, "Initial collaboration mode", fallback, initCollaborationModeChoices)
}

func promptChoice(reader *bufio.Reader, writer io.Writer, label, fallback string, choices initChoiceSet) string {
	for {
		fmt.Fprintf(writer, "%s:\n", label)
		fmt.Fprintf(writer, "  Press Enter to keep the default: %s\n", fallback)
		fmt.Fprintln(writer, "  Enter a number, alias, unique prefix, or full value.")
		for i, choice := range choices.Choices {
			defaultSuffix := ""
			if choice.Value == fallback {
				defaultSuffix = " (default)"
			}
			fmt.Fprintf(writer, "  %d. %s%s: %s\n", i+1, choice.Value, defaultSuffix, choice.Description)
		}
		fmt.Fprint(writer, "Selection: ")
		value, err := reader.ReadString('\n')
		if err != nil {
			return fallback
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return fallback
		}
		validated, err := choices.resolve(value)
		if err == nil {
			return validated
		}
		fmt.Fprintf(writer, "Invalid value: %v\n", err)
	}
}

func promptPositiveInt(reader *bufio.Reader, writer io.Writer, label string, fallback int) int {
	for {
		value := promptLine(reader, writer, label, strconv.Itoa(fallback))
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && parsed > 0 {
			return parsed
		}
		fmt.Fprintf(writer, "Invalid value: expected a positive integer for %s\n", label)
	}
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
	return initAgentModeChoices.resolve(raw)
}

func validateInitDispatchMode(raw string) (string, error) {
	return initDispatchModeChoices.resolve(raw)
}

func validateInitApprovalPolicy(raw string) (string, error) {
	if canonical, ok := canonicalApprovalPolicyString(raw); ok {
		return canonical, nil
	}
	return initApprovalPolicyChoices.resolve(raw)
}

func validateInitCollaborationMode(raw string) (string, error) {
	return initCollaborationModeChoices.resolve(raw)
}

func buildWorkflowFile(opts InitOptions) string {
	cfg := DefaultInitConfig()
	initDefaults := DefaultInitConfig()
	if strings.TrimSpace(opts.WorkspaceRoot) != "" {
		cfg.Workspace.Root = strings.TrimSpace(opts.WorkspaceRoot)
	}
	if strings.TrimSpace(opts.CodexCommand) != "" {
		cfg.Codex.Command = strings.TrimSpace(opts.CodexCommand)
	}
	if strings.TrimSpace(opts.AgentMode) != "" {
		cfg.Agent.Mode = strings.TrimSpace(opts.AgentMode)
	}
	if strings.TrimSpace(opts.DispatchMode) != "" {
		cfg.Agent.DispatchMode = strings.TrimSpace(opts.DispatchMode)
	}
	if opts.MaxConcurrentAgents > 0 {
		cfg.Agent.MaxConcurrentAgents = opts.MaxConcurrentAgents
	}
	if opts.MaxTurns > 0 {
		cfg.Agent.MaxTurns = opts.MaxTurns
	}
	if opts.MaxAutomaticRetries > 0 {
		cfg.Agent.MaxAutomaticRetries = opts.MaxAutomaticRetries
	}
	if strings.TrimSpace(opts.ApprovalPolicy) != "" {
		cfg.Codex.ApprovalPolicy = strings.TrimSpace(opts.ApprovalPolicy)
	}
	if strings.TrimSpace(opts.InitialCollaborationMode) != "" {
		cfg.Codex.InitialCollaborationMode = strings.TrimSpace(opts.InitialCollaborationMode)
	}
	reviewPrompt := indentBlock(DefaultInitReviewPromptTemplate(), "      ")
	donePrompt := indentBlock(DefaultInitDonePromptTemplate(), "      ")
	reviewEnabledComment := formatInitBoolComment(initDefaults.Phases.Review.Enabled)
	doneEnabledComment := formatInitBoolComment(initDefaults.Phases.Done.Enabled)
	agentModeComment := formatInitChoiceComment(initAgentModeChoices, initDefaults.Agent.Mode)
	dispatchModeComment := formatInitChoiceComment(initDispatchModeChoices, initDefaults.Agent.DispatchMode)
	approvalPolicyComment := formatInitChoiceComment(initApprovalPolicyChoices, strings.TrimSpace(fmt.Sprintf("%v", initDefaults.Codex.ApprovalPolicy)))
	initialCollaborationModeComment := formatInitChoiceComment(initCollaborationModeChoices, initDefaults.Codex.InitialCollaborationMode)
	return strings.TrimSpace(fmt.Sprintf(`
---
# Tracker configuration. Supported tracker kind today: %s.
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
    # Enable a dedicated review pass after implementation. %s
    enabled: %t
    # Prompt rendered when the issue enters review. Uses the same template variables
    # as the main prompt, such as issue.*, project.*, phase, and attempt.
    prompt: |
%s
  done:
    # Enable a dedicated finalization pass after implementation is otherwise complete. %s
    enabled: %t
    # Prompt rendered when the issue enters done for project-specific wrap-up steps.
    prompt: |
%s

# Agent runtime settings.
agent:
  # Maximum concurrent issues per project when dispatch_mode is parallel.
  # dispatch_mode=per_project_serial forces effective per-project concurrency to 1.
  max_concurrent_agents: %d
  # Maximum turns Maestro gives Codex before ending an attempt.
  max_turns: %d
  # Maximum delay between automatic retries after failed attempts.
  max_retry_backoff_ms: %d
  # Maximum automatic retry attempts for the same issue before Maestro stops retrying.
  max_automatic_retries: %d
  # Agent transport. %s
  mode: %s
  # Scheduling behavior. %s
  dispatch_mode: %s

# Codex CLI launch and collaboration settings.
codex:
  # Exact command Maestro launches for the agent. Direct Codex commands are
  # automatically pinned to the supported schema version when needed.
  command: %s
  # Approval mode for Codex. %s
  # "never" keeps unattended runs non-interactive, so permission recovery must come
  # from the project or issue permission profile rather than live approval prompts.
  # Use on-request when initial_collaboration_mode is plan so the agent can ask
  # questions and recover through approvals before Maestro promotes the run.
  # A structured granular object is also supported for per-category approval policies.
  approval_policy: %v
  # Initial collaboration mode for fresh app_server threads. %s
  # Use plan for a planning pass before implementation. Pair it with on-request
  # when you want the agent to ask questions and pause for approval.
  # Ignored for stdio runs and resumed threads.
  initial_collaboration_mode: %s
  # Maximum total runtime for one turn before Maestro cancels it.
  turn_timeout_ms: %d
  # Maximum time to wait for streamed output before considering the stream stalled.
  read_timeout_ms: %d
  # Maximum idle time without Codex activity before Maestro aborts the turn.
  stall_timeout_ms: %d
---

If Codex is not installed globally, Maestro can fall back to the pinned npx form when the configured direct Codex command does not match the supported schema version.

%s
`, cfg.Tracker.Kind, cfg.Tracker.Kind, cfg.Polling.IntervalMs, cfg.Workspace.Root, cfg.Hooks.TimeoutMs, reviewEnabledComment, cfg.Phases.Review.Enabled, reviewPrompt, doneEnabledComment, cfg.Phases.Done.Enabled, donePrompt, cfg.Agent.MaxConcurrentAgents, cfg.Agent.MaxTurns, cfg.Agent.MaxRetryBackoffMs, cfg.Agent.MaxAutomaticRetries, agentModeComment, cfg.Agent.Mode, dispatchModeComment, cfg.Agent.DispatchMode, cfg.Codex.Command, approvalPolicyComment, cfg.Codex.ApprovalPolicy, initialCollaborationModeComment, cfg.Codex.InitialCollaborationMode, cfg.Codex.TurnTimeoutMs, cfg.Codex.ReadTimeoutMs, cfg.Codex.StallTimeoutMs, DefaultPromptTemplate()))
}

func indentBlock(text, prefix string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func (s initChoiceSet) resolve(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: expected %s", s.Err, joinReadableList(s.values()))
	}

	if selection, err := strconv.Atoi(trimmed); err == nil {
		if selection >= 1 && selection <= len(s.Choices) {
			return s.Choices[selection-1].Value, nil
		}
		return "", fmt.Errorf("%w: %q (expected %s)", s.Err, raw, joinReadableList(s.values()))
	}

	key := normalizeInitChoiceKey(trimmed)
	if key == "" {
		return "", fmt.Errorf("%w: expected %s", s.Err, joinReadableList(s.values()))
	}

	if matches := s.findMatches(key, true); len(matches) == 1 {
		return matches[0], nil
	}

	matches := s.findMatches(key, false)
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("%w: %q (expected %s)", s.Err, raw, joinReadableList(s.values()))
	default:
		return "", fmt.Errorf("%w: %q is ambiguous (matches %s)", s.Err, raw, joinReadableList(matches))
	}
}

func (s initChoiceSet) findMatches(input string, exact bool) []string {
	seen := make(map[string]struct{}, len(s.Choices))
	matches := make([]string, 0, len(s.Choices))
	for _, choice := range s.Choices {
		for _, candidate := range s.candidateKeys(choice) {
			if exact && candidate != input {
				continue
			}
			if !exact && !strings.HasPrefix(candidate, input) {
				continue
			}
			if _, ok := seen[choice.Value]; ok {
				break
			}
			seen[choice.Value] = struct{}{}
			matches = append(matches, choice.Value)
			break
		}
	}
	return matches
}

func (s initChoiceSet) candidateKeys(choice initChoice) []string {
	keys := []string{normalizeInitChoiceKey(choice.Value)}
	for _, alias := range choice.Aliases {
		keys = append(keys, normalizeInitChoiceKey(alias))
	}
	return keys
}

func (s initChoiceSet) values() []string {
	values := make([]string, 0, len(s.Choices))
	for _, choice := range s.Choices {
		values = append(values, choice.Value)
	}
	return values
}

func normalizeInitChoiceKey(raw string) string {
	replacer := strings.NewReplacer("_", "-", " ", "-")
	value := replacer.Replace(strings.ToLower(strings.TrimSpace(raw)))
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return strings.Trim(value, "-")
}

func joinReadableList(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " or " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", or " + values[len(values)-1]
	}
}

func formatInitChoiceComment(choices initChoiceSet, defaultValue string) string {
	return fmt.Sprintf("Available values: %s. Fresh maestro init default: %s.", strings.Join(choices.values(), ", "), defaultValue)
}

func formatInitBoolComment(defaultValue bool) string {
	return fmt.Sprintf("Available values: true, false. Fresh maestro init default: %t.", defaultValue)
}
