package config

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	TrackerKindKanban  = "kanban"
	AgentModeAppServer = "app_server"
	AgentModeStdio     = "stdio"
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
	Mode                string `yaml:"mode"`
}

type CodexConfig struct {
	Command           string                 `yaml:"command"`
	ApprovalPolicy    interface{}            `yaml:"approval_policy"`
	ThreadSandbox     string                 `yaml:"thread_sandbox"`
	TurnSandboxPolicy map[string]interface{} `yaml:"turn_sandbox_policy"`
	TurnTimeoutMs     int                    `yaml:"turn_timeout_ms"`
	ReadTimeoutMs     int                    `yaml:"read_timeout_ms"`
	StallTimeoutMs    int                    `yaml:"stall_timeout_ms"`
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
		Polling:   PollingConfig{IntervalMs: 30000},
		Workspace: WorkspaceConfig{Root: "./workspaces"},
		Hooks:     HooksConfig{TimeoutMs: 60000},
		Agent: AgentConfig{
			MaxConcurrentAgents: 3,
			MaxTurns:            20,
			MaxRetryBackoffMs:   300000,
			Mode:                AgentModeAppServer,
		},
		Codex: CodexConfig{
			Command: "codex app-server",
			ApprovalPolicy: map[string]interface{}{
				"reject": map[string]interface{}{
					"sandbox_approval": true,
					"rules":            true,
					"mcp_elicitations": true,
				},
			},
			ThreadSandbox:     "workspace-write",
			TurnSandboxPolicy: map[string]interface{}{"type": "workspaceWrite"},
			TurnTimeoutMs:     3600000,
			ReadTimeoutMs:     5000,
			StallTimeoutMs:    300000,
		},
	}
}

func DefaultPromptTemplate() string {
	return strings.TrimSpace(`
You are working on issue {{ issue.identifier }}.

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

func WorkflowPath(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		repoPath, _ = os.Getwd()
	}
	return filepath.Join(repoPath, "WORKFLOW.md")
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
			return nil, fmt.Errorf("%w: unterminated front matter", ErrWorkflowParse)
		}
		frontMatter := content[4 : end+4]
		if err := yaml.Unmarshal([]byte(frontMatter), &raw); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
		}
		if raw == nil {
			raw = map[string]interface{}{}
		}
		promptStart = end + 8
	} else {
		raw = map[string]interface{}{}
	}

	if err := rejectLegacyKeys(raw); err != nil {
		return nil, err
	}

	encoded, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
	}

	var cfg Config
	if len(raw) > 0 {
		if err := yaml.Unmarshal(encoded, &cfg); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
		}
	}

	prompt := strings.TrimSpace(content[promptStart:])
	if prompt == "" {
		prompt = DefaultPromptTemplate()
	}
	if _, err := ParseLiquidTemplate(prompt); err != nil {
		return nil, fmt.Errorf("template_parse_error: %w", err)
	}

	cfg.Workspace.Root = resolvePathValue(filepath.Dir(path), cfg.Workspace.Root, DefaultConfig().Workspace.Root)
	return &workflowPayload{Config: cfg, Prompt: prompt}, nil
}

func rejectLegacyKeys(raw map[string]interface{}) error {
	legacy := []string{"poll_interval", "max_concurrent", "workspace_root", "active_states", "terminal_states"}
	for _, key := range legacy {
		if _, ok := raw[key]; ok {
			return fmt.Errorf("%w: legacy workflow key %q is not supported", ErrWorkflowParse, key)
		}
	}
	return nil
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
	if strings.TrimSpace(c.Agent.Mode) == "" {
		c.Agent.Mode = defaults.Agent.Mode
	}
	if strings.TrimSpace(c.Codex.Command) == "" {
		c.Codex.Command = defaults.Codex.Command
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
}

func validateConfig(c *Config) error {
	if strings.TrimSpace(c.Tracker.Kind) != TrackerKindKanban {
		return fmt.Errorf("unsupported tracker.kind %q", strings.TrimSpace(c.Tracker.Kind))
	}
	if strings.TrimSpace(c.Agent.Mode) != AgentModeAppServer && strings.TrimSpace(c.Agent.Mode) != AgentModeStdio {
		return fmt.Errorf("unsupported agent.mode %q", c.Agent.Mode)
	}
	if strings.TrimSpace(c.Codex.Command) == "" {
		return fmt.Errorf("codex.command is required")
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
