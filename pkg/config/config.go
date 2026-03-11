package config

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"github.com/olhapi/maestro/internal/codexschema"
	"gopkg.in/yaml.v3"
)

const (
	TrackerKindKanban            = "kanban"
	AgentModeAppServer           = "app_server"
	AgentModeStdio               = "stdio"
	DispatchModeParallel         = "parallel"
	DispatchModePerProjectSerial = "per_project_serial"
)

var (
	ErrMissingWorkflowFile = errors.New("missing_workflow_file")
	ErrWorkflowParse       = errors.New("workflow_parse_error")
	ErrWorkflowFrontMatter = errors.New("workflow_front_matter_not_a_map")
)

type Config struct {
	Tracker   TrackerConfig   `yaml:"tracker"`
	Polling   PollingConfig   `yaml:"polling"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Hooks     HooksConfig     `yaml:"hooks"`
	Agent     AgentConfig     `yaml:"agent"`
	Codex     CodexConfig     `yaml:"codex"`
	Phases    PhasesConfig    `yaml:"phases"`
}

type TrackerConfig struct {
	Kind           string   `yaml:"kind"`
	ActiveStates   []string `yaml:"active_states"`
	TerminalStates []string `yaml:"terminal_states"`
}

type PollingConfig struct {
	IntervalMs int `yaml:"interval_ms"`
}

type WorkspaceConfig struct {
	Root string `yaml:"root"`
}

type HooksConfig struct {
	AfterCreate  string `yaml:"after_create"`
	BeforeRun    string `yaml:"before_run"`
	AfterRun     string `yaml:"after_run"`
	BeforeRemove string `yaml:"before_remove"`
	TimeoutMs    int    `yaml:"timeout_ms"`
}

type AgentConfig struct {
	MaxConcurrentAgents int    `yaml:"max_concurrent_agents"`
	MaxTurns            int    `yaml:"max_turns"`
	MaxRetryBackoffMs   int    `yaml:"max_retry_backoff_ms"`
	MaxAutomaticRetries int    `yaml:"max_automatic_retries"`
	Mode                string `yaml:"mode"`
	DispatchMode        string `yaml:"dispatch_mode"`
}

type CodexConfig struct {
	Command           string                 `yaml:"command"`
	ExpectedVersion   string                 `yaml:"expected_version"`
	ApprovalPolicy    interface{}            `yaml:"approval_policy"`
	ThreadSandbox     string                 `yaml:"thread_sandbox"`
	TurnSandboxPolicy map[string]interface{} `yaml:"turn_sandbox_policy"`
	TurnTimeoutMs     int                    `yaml:"turn_timeout_ms"`
	ReadTimeoutMs     int                    `yaml:"read_timeout_ms"`
	StallTimeoutMs    int                    `yaml:"stall_timeout_ms"`
}

type PhasesConfig struct {
	Review PhasePromptConfig `yaml:"review"`
	Done   PhasePromptConfig `yaml:"done"`
}

type PhasePromptConfig struct {
	Enabled bool   `yaml:"enabled"`
	Prompt  string `yaml:"prompt"`
}

type Workflow struct {
	Path           string
	Config         Config
	PromptTemplate string
}

type workflowPayload struct {
	Config Config
	Prompt string
}

type fileStamp struct {
	ModTime int64
	Size    int64
	Hash    uint64
}

func DefaultConfig() Config {
	return Config{
		Tracker: TrackerConfig{
			Kind:           TrackerKindKanban,
			ActiveStates:   []string{"ready", "in_progress", "in_review"},
			TerminalStates: []string{"done", "cancelled"},
		},
		Polling:   PollingConfig{IntervalMs: 10000},
		Workspace: WorkspaceConfig{Root: "./workspaces"},
		Hooks:     HooksConfig{TimeoutMs: 60000},
		Agent: AgentConfig{
			MaxConcurrentAgents: 3,
			MaxTurns:            4,
			MaxRetryBackoffMs:   60000,
			MaxAutomaticRetries: 8,
			Mode:                AgentModeAppServer,
			DispatchMode:        DispatchModeParallel,
		},
		Codex: CodexConfig{
			Command:         "codex app-server",
			ExpectedVersion: codexschema.SupportedVersion,
			ApprovalPolicy: map[string]interface{}{
				"reject": map[string]interface{}{
					"sandbox_approval": true,
					"rules":            true,
					"mcp_elicitations": true,
				},
			},
			ThreadSandbox: "workspace-write",
			TurnSandboxPolicy: map[string]interface{}{
				"type":          "workspaceWrite",
				"networkAccess": true,
			},
			TurnTimeoutMs:  600000,
			ReadTimeoutMs:  5000,
			StallTimeoutMs: 60000,
		},
	}
}

func DefaultPromptTemplate() string {
	return strings.TrimSpace(`
You are working on issue {{ issue.identifier }}.

Current phase: {{ phase }}

{% if attempt %}
Continuation attempt: {{ attempt }}
{% endif %}

Title: {{ issue.title }}
Description:
{% if issue.description %}
{{ issue.description }}
{% else %}
No description provided.
{% endif %}
`)
}

func DefaultReviewPromptTemplate() string {
	return strings.TrimSpace(`
You are performing the review pass for issue {{ issue.identifier }}.

Title: {{ issue.title }}
State: {{ issue.state }}
Description:
{% if issue.description %}
{{ issue.description }}
{% else %}
No description provided.
{% endif %}

Review the implementation in the current workspace, run focused verification, and fix any issues you find.

- If additional implementation is still required after review, move the issue back to in_progress.
- If the issue is ready to finalize, move it to done.
`)
}

func DefaultDonePromptTemplate() string {
	return strings.TrimSpace(`
You are performing the done pass for issue {{ issue.identifier }}.

Title: {{ issue.title }}
State: {{ issue.state }}
Description:
{% if issue.description %}
{{ issue.description }}
{% else %}
No description provided.
{% endif %}

The implementation is already complete. Perform the project-specific finalization steps for a done issue from the current workspace, such as preparing or updating a PR, merging, or other release bookkeeping.

- Keep the issue in done unless the work truly needs to be reopened.
`)
}

func WorkflowPath(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		repoPath, _ = os.Getwd()
	}
	return filepath.Join(repoPath, "WORKFLOW.md")
}

func ResolveWorkflowPath(repoPath, overridePath string) string {
	if strings.TrimSpace(overridePath) != "" {
		if filepath.IsAbs(overridePath) {
			return filepath.Clean(overridePath)
		}
		if strings.TrimSpace(repoPath) == "" {
			repoPath, _ = os.Getwd()
		}
		return filepath.Clean(filepath.Join(repoPath, overridePath))
	}
	return WorkflowPath(repoPath)
}

func LoadWorkflow(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrMissingWorkflowFile
		}
		return nil, fmt.Errorf("%w: %v", ErrMissingWorkflowFile, err)
	}

	payload, err := parseWorkflowPayload(path, string(data))
	if err != nil {
		return nil, err
	}

	cfg := payload.Config
	applyDefaults(&cfg)
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}

	return &Workflow{
		Path:           path,
		Config:         cfg,
		PromptTemplate: payload.Prompt,
	}, nil
}

func parseWorkflowPayload(path, content string) (*workflowPayload, error) {
	var raw map[string]interface{}
	promptStart := 0

	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---\n")
		if end == -1 {
			frontMatter := content[4:]
			if err := yaml.Unmarshal([]byte(frontMatter), &raw); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
			}
			if raw == nil {
				raw = map[string]interface{}{}
			}
			promptStart = len(content)
		} else {
			frontMatter := content[4 : end+4]
			if err := yaml.Unmarshal([]byte(frontMatter), &raw); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
			}
			if raw == nil {
				raw = map[string]interface{}{}
			}
			promptStart = end + 8
		}
	} else {
		raw = map[string]interface{}{}
	}

	normalized, err := normalizeWorkflowKeys(raw)
	if err != nil {
		return nil, err
	}

	encoded, err := yaml.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
	}

	var cfg Config
	if len(normalized) > 0 {
		if err := yaml.Unmarshal(encoded, &cfg); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
		}
	}

	prompt := strings.TrimSpace(content[promptStart:])
	if prompt == "" && promptStart == 0 {
		prompt = DefaultPromptTemplate()
	}
	if _, err := ParseLiquidTemplate(prompt); err != nil {
		return nil, fmt.Errorf("template_parse_error: %w", err)
	}

	cfg.Workspace.Root = resolvePathValue(filepath.Dir(path), cfg.Workspace.Root, DefaultConfig().Workspace.Root)
	return &workflowPayload{Config: cfg, Prompt: prompt}, nil
}

func normalizeWorkflowKeys(raw map[string]interface{}) (map[string]interface{}, error) {
	if raw == nil {
		return map[string]interface{}{}, nil
	}

	out := cloneMap(raw)
	tracker := ensureMap(out, "tracker")
	polling := ensureMap(out, "polling")
	workspace := ensureMap(out, "workspace")
	hooks := ensureMap(out, "hooks")
	agent := ensureMap(out, "agent")
	codex := ensureMap(out, "codex")
	phases := ensureMap(out, "phases")
	ensureMap(phases, "review")
	ensureMap(phases, "done")

	moveString(out, tracker, "tracker_kind", "kind")
	moveStringSlice(out, tracker, "tracker_active_states", "active_states")
	moveStringSlice(out, tracker, "tracker_terminal_states", "terminal_states")
	moveString(out, polling, "poll_interval", "interval_ms")
	moveString(out, polling, "poll_interval_ms", "interval_ms")
	moveNumeric(out, polling, "poll_interval", "interval_ms")
	moveNumeric(out, polling, "poll_interval_ms", "interval_ms")
	moveString(out, workspace, "workspace_root", "root")
	moveNumeric(out, hooks, "hooks_timeout_ms", "timeout_ms")
	moveNumeric(out, agent, "max_concurrent", "max_concurrent_agents")
	moveNumeric(out, agent, "max_concurrent_agents", "max_concurrent_agents")
	moveNumeric(out, agent, "max_turns", "max_turns")
	moveNumeric(out, agent, "max_retry_backoff_ms", "max_retry_backoff_ms")
	moveNumeric(out, agent, "max_automatic_retries", "max_automatic_retries")
	moveString(out, agent, "agent_mode", "mode")
	moveString(out, agent, "dispatch_mode", "dispatch_mode")
	moveString(out, codex, "codex_command", "command")
	moveString(out, codex, "codex_expected_version", "expected_version")
	moveValue(out, codex, "codex_approval_policy", "approval_policy")
	moveString(out, codex, "codex_thread_sandbox", "thread_sandbox")
	moveMap(out, codex, "codex_turn_sandbox_policy", "turn_sandbox_policy")
	moveNumeric(out, codex, "codex_turn_timeout_ms", "turn_timeout_ms")
	moveNumeric(out, codex, "codex_read_timeout_ms", "read_timeout_ms")
	moveNumeric(out, codex, "codex_stall_timeout_ms", "stall_timeout_ms")

	unsupported := []string{"tracker_api_token", "tracker_project_slug", "tracker_assignee"}
	for _, key := range unsupported {
		if _, ok := out[key]; ok {
			return nil, fmt.Errorf("%w: legacy workflow key %q is not supported in kanban mode", ErrWorkflowParse, key)
		}
	}
	return out, nil
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		if child, ok := v.(map[string]interface{}); ok {
			out[k] = cloneMap(child)
			continue
		}
		out[k] = v
	}
	return out
}

func ensureMap(root map[string]interface{}, key string) map[string]interface{} {
	if current, ok := root[key].(map[string]interface{}); ok {
		return current
	}
	child := map[string]interface{}{}
	root[key] = child
	return child
}

func moveValue(root, dest map[string]interface{}, from, to string) {
	value, ok := root[from]
	if !ok {
		return
	}
	delete(root, from)
	if _, exists := dest[to]; !exists {
		dest[to] = value
	}
}

func moveString(root, dest map[string]interface{}, from, to string) {
	if value, ok := root[from].(string); ok {
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = value
		}
	}
}

func moveMap(root, dest map[string]interface{}, from, to string) {
	if value, ok := root[from].(map[string]interface{}); ok {
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = value
		}
	}
}

func moveNumeric(root, dest map[string]interface{}, from, to string) {
	value, ok := root[from]
	if !ok {
		return
	}
	switch value.(type) {
	case int, int64, float64:
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = value
		}
	}
}

func moveStringSlice(root, dest map[string]interface{}, from, to string) {
	value, ok := root[from]
	if !ok {
		return
	}
	switch typed := value.(type) {
	case []interface{}:
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = typed
		}
	case []string:
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = typed
		}
	case string:
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = splitCSVValues(typed)
		}
	}
}

func splitCSVValues(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func applyDefaults(c *Config) {
	defaults := DefaultConfig()

	if strings.TrimSpace(c.Tracker.Kind) == "" {
		c.Tracker.Kind = defaults.Tracker.Kind
	}
	if len(c.Tracker.ActiveStates) == 0 {
		c.Tracker.ActiveStates = append([]string(nil), defaults.Tracker.ActiveStates...)
	}
	if len(c.Tracker.TerminalStates) == 0 {
		c.Tracker.TerminalStates = append([]string(nil), defaults.Tracker.TerminalStates...)
	}
	if c.Polling.IntervalMs <= 0 {
		c.Polling.IntervalMs = defaults.Polling.IntervalMs
	}
	if strings.TrimSpace(c.Workspace.Root) == "" {
		c.Workspace.Root = defaults.Workspace.Root
	}
	if c.Hooks.TimeoutMs <= 0 {
		c.Hooks.TimeoutMs = defaults.Hooks.TimeoutMs
	}
	if c.Agent.MaxConcurrentAgents <= 0 {
		c.Agent.MaxConcurrentAgents = defaults.Agent.MaxConcurrentAgents
	}
	if c.Agent.MaxTurns <= 0 {
		c.Agent.MaxTurns = defaults.Agent.MaxTurns
	}
	if c.Agent.MaxRetryBackoffMs <= 0 {
		c.Agent.MaxRetryBackoffMs = defaults.Agent.MaxRetryBackoffMs
	}
	if c.Agent.MaxAutomaticRetries <= 0 {
		c.Agent.MaxAutomaticRetries = defaults.Agent.MaxAutomaticRetries
	}
	if strings.TrimSpace(c.Agent.Mode) == "" {
		c.Agent.Mode = defaults.Agent.Mode
	}
	if strings.TrimSpace(c.Agent.DispatchMode) == "" {
		c.Agent.DispatchMode = defaults.Agent.DispatchMode
	}
	if strings.TrimSpace(c.Codex.Command) == "" {
		c.Codex.Command = defaults.Codex.Command
	}
	if strings.TrimSpace(c.Codex.ExpectedVersion) == "" {
		c.Codex.ExpectedVersion = defaults.Codex.ExpectedVersion
	}
	if c.Codex.ApprovalPolicy == nil {
		c.Codex.ApprovalPolicy = defaults.Codex.ApprovalPolicy
	}
	if strings.TrimSpace(c.Codex.ThreadSandbox) == "" {
		c.Codex.ThreadSandbox = defaults.Codex.ThreadSandbox
	}
	if c.Codex.TurnSandboxPolicy == nil {
		c.Codex.TurnSandboxPolicy = defaults.Codex.TurnSandboxPolicy
	}
	if c.Codex.TurnTimeoutMs <= 0 {
		c.Codex.TurnTimeoutMs = defaults.Codex.TurnTimeoutMs
	}
	if c.Codex.ReadTimeoutMs <= 0 {
		c.Codex.ReadTimeoutMs = defaults.Codex.ReadTimeoutMs
	}
	if c.Codex.StallTimeoutMs == 0 {
		c.Codex.StallTimeoutMs = defaults.Codex.StallTimeoutMs
	}
	if c.Phases.Review.Enabled && strings.TrimSpace(c.Phases.Review.Prompt) == "" {
		c.Phases.Review.Prompt = DefaultReviewPromptTemplate()
	}
	if c.Phases.Done.Enabled && strings.TrimSpace(c.Phases.Done.Prompt) == "" {
		c.Phases.Done.Prompt = DefaultDonePromptTemplate()
	}
}

func validateConfig(c *Config) error {
	if strings.TrimSpace(c.Tracker.Kind) != TrackerKindKanban {
		return fmt.Errorf("unsupported tracker.kind %q", strings.TrimSpace(c.Tracker.Kind))
	}
	if strings.TrimSpace(c.Agent.Mode) != AgentModeAppServer && strings.TrimSpace(c.Agent.Mode) != AgentModeStdio {
		return fmt.Errorf("unsupported agent.mode %q", c.Agent.Mode)
	}
	dispatchMode := strings.TrimSpace(c.Agent.DispatchMode)
	if dispatchMode != DispatchModeParallel && dispatchMode != DispatchModePerProjectSerial {
		return fmt.Errorf("unsupported agent.dispatch_mode %q", c.Agent.DispatchMode)
	}
	if strings.TrimSpace(c.Codex.Command) == "" {
		return fmt.Errorf("codex.command is required")
	}
	for _, prompt := range []string{strings.TrimSpace(c.Phases.Review.Prompt), strings.TrimSpace(c.Phases.Done.Prompt)} {
		if prompt == "" {
			continue
		}
		if _, err := ParseLiquidTemplate(prompt); err != nil {
			return fmt.Errorf("template_parse_error: %w", err)
		}
	}
	return nil
}

func resolvePathValue(baseDir, raw, fallback string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = fallback
	}
	if strings.HasPrefix(value, "$") {
		if env := strings.TrimSpace(strings.TrimPrefix(value, "$")); env != "" {
			resolved := strings.TrimSpace(os.Getenv(env))
			if resolved != "" {
				value = resolved
			}
		}
	}
	if strings.HasPrefix(value, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			value = filepath.Join(home, strings.TrimPrefix(value, "~"))
		}
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}

func hashContent(data []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(data)
	return h.Sum64()
}

func currentStamp(path string) (fileStamp, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{
		ModTime: stat.ModTime().UnixNano(),
		Size:    stat.Size(),
		Hash:    hashContent(data),
	}, nil
}
