package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the runtime configuration
type Config struct {
	// Polling
	PollInterval int `yaml:"poll_interval"` // seconds

	// Concurrency
	MaxConcurrent int `yaml:"max_concurrent"`

	// Workspace
	WorkspaceRoot string `yaml:"workspace_root"`

	// States
	ActiveStates   []string `yaml:"active_states"`
	TerminalStates []string `yaml:"terminal_states"`

	// Agent configuration
	Agent AgentConfig `yaml:"agent"`

	// Hooks
	Hooks HooksConfig `yaml:"hooks"`
}

// AgentConfig configures the coding agent
type AgentConfig struct {
	Executable string            `yaml:"executable"`
	Args       []string          `yaml:"args"`
	Env        map[string]string `yaml:"env"`
	Timeout    int               `yaml:"timeout"` // seconds, 0 = no timeout
}

// HooksConfig configures workspace lifecycle hooks
type HooksConfig struct {
	BeforeRun   []string `yaml:"before_run"`   // Commands to run before agent
	AfterRun    []string `yaml:"after_run"`    // Commands to run after agent
	AfterCreate []string `yaml:"after_create"` // Commands to run after workspace creation
	TimeoutSec  int      `yaml:"timeout_sec"`  // Hook timeout in seconds
}

// Workflow represents a parsed WORKFLOW.md file
type Workflow struct {
	Config         Config
	PromptTemplate string
}

// DefaultConfig returns the default configuration
func DefaultConfig() Config {
	return Config{
		PollInterval:  30,
		MaxConcurrent: 3,
		WorkspaceRoot: "./workspaces",
		ActiveStates:  []string{"ready", "in_progress", "in_review"},
		TerminalStates: []string{"done", "cancelled"},
		Agent: AgentConfig{
			Executable: "codex",
			Args:       []string{},
			Env:        map[string]string{},
			Timeout:    0,
		},
		Hooks: HooksConfig{TimeoutSec: 60},
	}
}

// LoadWorkflow loads a WORKFLOW.md file
func LoadWorkflow(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read workflow file: %w", err)
	}

	content := string(data)

	// Parse YAML front matter
	var config Config
	promptStart := 0

	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---\n")
		if end != -1 {
			frontMatter := content[4 : end+4]
			if err := yaml.Unmarshal([]byte(frontMatter), &config); err != nil {
				return nil, fmt.Errorf("failed to parse front matter: %w", err)
			}
			promptStart = end + 8 // After "---\n---\n"
		}
	} else {
		config = DefaultConfig()
	}

	// Apply defaults for missing fields
	applyDefaults(&config)

	// Extract prompt template
	promptTemplate := strings.TrimSpace(content[promptStart:])

	return &Workflow{
		Config:         config,
		PromptTemplate: promptTemplate,
	}, nil
}

func applyDefaults(c *Config) {
	defaults := DefaultConfig()

	if c.PollInterval == 0 {
		c.PollInterval = defaults.PollInterval
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = defaults.MaxConcurrent
	}
	if c.WorkspaceRoot == "" {
		c.WorkspaceRoot = defaults.WorkspaceRoot
	}
	if len(c.ActiveStates) == 0 {
		c.ActiveStates = defaults.ActiveStates
	}
	if len(c.TerminalStates) == 0 {
		c.TerminalStates = defaults.TerminalStates
	}
	if c.Agent.Executable == "" {
		c.Agent.Executable = defaults.Agent.Executable
	}
	if c.Hooks.TimeoutSec <= 0 {
		c.Hooks.TimeoutSec = defaults.Hooks.TimeoutSec
	}
}

// LoadOrCreateWorkflow loads WORKFLOW.md or creates a default one
func LoadOrCreateWorkflow(path string) (*Workflow, error) {
	workflowPath := filepath.Join(path, "WORKFLOW.md")

	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		// Create default workflow
		defaultWorkflow := `---
poll_interval: 30
max_concurrent: 3
workspace_root: ./workspaces
active_states:
  - ready
  - in_progress
  - in_review
terminal_states:
  - done
  - cancelled
agent:
  executable: codex
  args: []
  timeout: 0
---

# Symphony Workflow

You are an autonomous coding agent working on issue {{.Identifier}}.

## Issue Details

- **Title**: {{.Title}}
- **Description**: {{.Description}}
- **Labels**: {{range .Labels}}{{.}}, {{end}}

## Instructions

1. Create a branch for this issue
2. Implement the changes described
3. Run tests and ensure they pass
4. Create a pull request
5. Update the issue with the PR link

## Guidelines

- Follow the project's coding standards
- Write clear commit messages
- Keep changes focused and minimal
`
		if err := os.WriteFile(workflowPath, []byte(defaultWorkflow), 0644); err != nil {
			return nil, fmt.Errorf("failed to create default workflow: %w", err)
		}
	}

	return LoadWorkflow(workflowPath)
}

// GetEnv returns the environment variables for the agent
func (c *Config) GetEnv() []string {
	env := os.Environ()
	for k, v := range c.Agent.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}
