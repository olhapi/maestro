package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.PollInterval != 30 {
		t.Errorf("Expected PollInterval 30, got %d", cfg.PollInterval)
	}
	if cfg.MaxConcurrent != 3 {
		t.Errorf("Expected MaxConcurrent 3, got %d", cfg.MaxConcurrent)
	}
	if cfg.WorkspaceRoot != "./workspaces" {
		t.Errorf("Expected WorkspaceRoot './workspaces', got %s", cfg.WorkspaceRoot)
	}
	if len(cfg.ActiveStates) != 3 {
		t.Errorf("Expected 3 active states, got %d", len(cfg.ActiveStates))
	}
	if len(cfg.TerminalStates) != 2 {
		t.Errorf("Expected 2 terminal states, got %d", len(cfg.TerminalStates))
	}
	if cfg.Agent.Executable != "codex" {
		t.Errorf("Expected Agent.Executable 'codex', got %s", cfg.Agent.Executable)
	}
}

func TestLoadWorkflowWithFrontMatter(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")

	content := `---
poll_interval: 60
max_concurrent: 5
workspace_root: /tmp/workspaces
agent:
  executable: /usr/local/bin/codex
  timeout: 3600
---

# Custom Workflow

You are working on issue {{.Identifier}}.

## Instructions
1. Read the issue
2. Implement the solution
3. Run tests
`
	if err := os.WriteFile(workflowPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write workflow: %v", err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}

	if workflow.Config.PollInterval != 60 {
		t.Errorf("Expected PollInterval 60, got %d", workflow.Config.PollInterval)
	}
	if workflow.Config.MaxConcurrent != 5 {
		t.Errorf("Expected MaxConcurrent 5, got %d", workflow.Config.MaxConcurrent)
	}
	if workflow.Config.WorkspaceRoot != "/tmp/workspaces" {
		t.Errorf("Expected WorkspaceRoot '/tmp/workspaces', got %s", workflow.Config.WorkspaceRoot)
	}
	if workflow.Config.Agent.Timeout != 3600 {
		t.Errorf("Expected Agent.Timeout 3600, got %d", workflow.Config.Agent.Timeout)
	}
	if workflow.PromptTemplate == "" {
		t.Error("Expected non-empty prompt template")
	}
}

func TestLoadWorkflowWithoutFrontMatter(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")

	content := `# Prompt Only

This is just a prompt without front matter.
`
	if err := os.WriteFile(workflowPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write workflow: %v", err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}

	// Should use defaults
	if workflow.Config.PollInterval != 30 {
		t.Errorf("Expected default PollInterval 30, got %d", workflow.Config.PollInterval)
	}
	// TrimSpace for comparison since LoadWorkflow trims the template
	if strings.TrimSpace(workflow.PromptTemplate) != strings.TrimSpace(content) {
		t.Errorf("Expected prompt template to match content, got: %q", workflow.PromptTemplate)
	}
}

func TestLoadOrCreateWorkflow(t *testing.T) {
	tmpDir := t.TempDir()

	// Should create default workflow
	workflow, err := LoadOrCreateWorkflow(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load/create workflow: %v", err)
	}

	if workflow == nil {
		t.Fatal("Expected non-nil workflow")
	}

	// Check that WORKFLOW.md was created
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		t.Error("Expected WORKFLOW.md to be created")
	}

	// Load again - should load existing
	workflow2, err := LoadOrCreateWorkflow(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load existing workflow: %v", err)
	}

	if workflow2.Config.PollInterval != workflow.Config.PollInterval {
		t.Error("Expected same config on reload")
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := Config{
		PollInterval: 0, // Should get default
	}
	applyDefaults(&cfg)

	if cfg.PollInterval != 30 {
		t.Errorf("Expected default PollInterval 30, got %d", cfg.PollInterval)
	}
	if cfg.MaxConcurrent != 3 {
		t.Errorf("Expected default MaxConcurrent 3, got %d", cfg.MaxConcurrent)
	}
}

func TestGetEnv(t *testing.T) {
	cfg := Config{
		Agent: AgentConfig{
			Env: map[string]string{
				"CUSTOM_VAR": "custom_value",
			},
		},
	}

	env := cfg.GetEnv()

	hasCustom := false
	for _, e := range env {
		if e == "CUSTOM_VAR=custom_value" {
			hasCustom = true
			break
		}
	}

	if !hasCustom {
		t.Error("Expected custom env var to be included")
	}
}
