package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Tracker.Kind != TrackerKindKanban {
		t.Fatalf("expected tracker kind %q, got %q", TrackerKindKanban, cfg.Tracker.Kind)
	}
	if cfg.Polling.IntervalMs != 10000 {
		t.Fatalf("expected poll interval 10000, got %d", cfg.Polling.IntervalMs)
	}
	if cfg.Agent.MaxConcurrentAgents != 3 {
		t.Fatalf("expected max concurrent 3, got %d", cfg.Agent.MaxConcurrentAgents)
	}
	if cfg.Agent.Mode != AgentModeAppServer {
		t.Fatalf("expected app_server mode, got %q", cfg.Agent.Mode)
	}
	if cfg.Codex.ExpectedVersion != "0.111.0" {
		t.Fatalf("expected codex expected version 0.111.0, got %q", cfg.Codex.ExpectedVersion)
	}
	if cfg.Agent.MaxTurns != 4 || cfg.Agent.MaxRetryBackoffMs != 60000 || cfg.Agent.MaxAutomaticRetries != 8 {
		t.Fatalf("unexpected agent defaults: %+v", cfg.Agent)
	}
	if cfg.Agent.DispatchMode != DispatchModeParallel {
		t.Fatalf("expected dispatch mode %q, got %q", DispatchModeParallel, cfg.Agent.DispatchMode)
	}
	if cfg.Codex.TurnTimeoutMs != 1800000 || cfg.Codex.ReadTimeoutMs != 10000 || cfg.Codex.StallTimeoutMs != 300000 {
		t.Fatalf("unexpected codex defaults: %+v", cfg.Codex)
	}
	if cfg.Codex.TurnSandboxPolicy["networkAccess"] != true {
		t.Fatalf("expected default turn sandbox networkAccess=true, got %+v", cfg.Codex.TurnSandboxPolicy)
	}
}

func TestLoadWorkflowNestedSchema(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
  active_states:
    - ready
    - in_progress
  terminal_states:
    - done
polling:
  interval_ms: 1500
workspace:
  root: ./custom-workspaces
hooks:
  before_run: echo setup
  before_remove: echo cleanup
  timeout_ms: 1234
agent:
  max_concurrent_agents: 5
  max_turns: 4
  max_retry_backoff_ms: 9000
  max_automatic_retries: 6
  mode: stdio
codex:
  command: codex --model test app-server
  expected_version: 0.111.0
  approval_policy: never
  thread_sandbox: workspace-write
  read_timeout_ms: 9999
  turn_timeout_ms: 120000
phases:
  review:
    enabled: true
  done:
    enabled: true
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}

	if workflow.Config.Polling.IntervalMs != 1500 {
		t.Fatalf("unexpected poll interval: %d", workflow.Config.Polling.IntervalMs)
	}
	if workflow.Config.Agent.Mode != AgentModeStdio {
		t.Fatalf("unexpected agent mode: %s", workflow.Config.Agent.Mode)
	}
	if workflow.Config.Agent.MaxAutomaticRetries != 6 {
		t.Fatalf("unexpected max automatic retries: %d", workflow.Config.Agent.MaxAutomaticRetries)
	}
	if workflow.Config.Hooks.BeforeRemove != "echo cleanup" {
		t.Fatalf("unexpected before_remove hook: %q", workflow.Config.Hooks.BeforeRemove)
	}
	if workflow.Config.Codex.ExpectedVersion != "0.111.0" {
		t.Fatalf("unexpected codex expected version: %q", workflow.Config.Codex.ExpectedVersion)
	}
	if !workflow.Config.Phases.Review.Enabled || !strings.Contains(workflow.Config.Phases.Review.Prompt, "review pass") {
		t.Fatalf("expected default review prompt, got %+v", workflow.Config.Phases.Review)
	}
	if !workflow.Config.Phases.Done.Enabled || !strings.Contains(workflow.Config.Phases.Done.Prompt, "done pass") {
		t.Fatalf("expected default done prompt, got %+v", workflow.Config.Phases.Done)
	}
	expectedRoot := filepath.Join(tmpDir, "custom-workspaces")
	if workflow.Config.Workspace.Root != expectedRoot {
		t.Fatalf("expected resolved workspace root %s, got %s", expectedRoot, workflow.Config.Workspace.Root)
	}
}

func TestLoadWorkflowAcceptsCustomPhasePrompts(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
phases:
  review:
    enabled: true
    prompt: |
      Review {{ issue.identifier }} during {{ phase }}
  done:
    enabled: true
    prompt: |
      Finalize {{ issue.identifier }} during {{ phase }}
---
Implement {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if strings.TrimSpace(workflow.Config.Phases.Review.Prompt) != "Review {{ issue.identifier }} during {{ phase }}" {
		t.Fatalf("unexpected review prompt: %q", workflow.Config.Phases.Review.Prompt)
	}
	if strings.TrimSpace(workflow.Config.Phases.Done.Prompt) != "Finalize {{ issue.identifier }} during {{ phase }}" {
		t.Fatalf("unexpected done prompt: %q", workflow.Config.Phases.Done.Prompt)
	}
}

func TestLoadWorkflowAcceptsPerProjectSerialDispatchMode(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
agent:
  dispatch_mode: per_project_serial
---
Implement {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if workflow.Config.Agent.DispatchMode != DispatchModePerProjectSerial {
		t.Fatalf("expected dispatch mode %q, got %q", DispatchModePerProjectSerial, workflow.Config.Agent.DispatchMode)
	}
}

func TestLoadWorkflowRejectsUnsupportedDispatchMode(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
agent:
  dispatch_mode: global_serial
---
Implement {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadWorkflow(workflowPath); err == nil || !strings.Contains(err.Error(), "unsupported agent.dispatch_mode") {
		t.Fatalf("expected unsupported dispatch mode error, got %v", err)
	}
}

func TestLoadWorkflowRejectsLegacyFlatSchema(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
poll_interval: 30
workspace_root: ./workspaces
---
Hello {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("expected legacy flat schema aliasing, got %v", err)
	}
	if workflow.Config.Polling.IntervalMs != 30 {
		t.Fatalf("expected poll interval 30, got %d", workflow.Config.Polling.IntervalMs)
	}
	if workflow.Config.Workspace.Root != filepath.Join(tmpDir, "workspaces") {
		t.Fatalf("unexpected workspace root: %s", workflow.Config.Workspace.Root)
	}
}

func TestLoadWorkflowRejectsUnsupportedTrackerKind(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: linear
---
Hello {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadWorkflow(workflowPath); err == nil || !strings.Contains(err.Error(), "unsupported tracker.kind") {
		t.Fatalf("expected unsupported tracker kind error, got %v", err)
	}
}

func TestLoadWorkflowWithoutFrontMatterUsesDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `Issue {{ issue.identifier }}`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if workflow.Config.Tracker.Kind != TrackerKindKanban {
		t.Fatalf("unexpected tracker kind: %s", workflow.Config.Tracker.Kind)
	}
	if workflow.PromptTemplate != content {
		t.Fatalf("unexpected prompt template: %q", workflow.PromptTemplate)
	}
}

func TestLoadWorkflowWithConfigOnlyFrontMatterKeepsEmptyPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
---`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if workflow.PromptTemplate != "" {
		t.Fatalf("expected empty prompt, got %q", workflow.PromptTemplate)
	}
}

func TestLoadWorkflowAcceptsUnterminatedFrontMatter(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
polling:
  interval_ms: 42
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if workflow.Config.Polling.IntervalMs != 42 {
		t.Fatalf("unexpected poll interval: %d", workflow.Config.Polling.IntervalMs)
	}
	if workflow.PromptTemplate != "" {
		t.Fatalf("expected empty prompt, got %q", workflow.PromptTemplate)
	}
}

func TestResolveWorkflowPathUsesOverrideRelativeToRepo(t *testing.T) {
	tmpDir := t.TempDir()
	got := ResolveWorkflowPath(tmpDir, "nested/CUSTOM.md")
	want := filepath.Join(tmpDir, "nested", "CUSTOM.md")
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestNewManagerForPath(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "custom.workflow.md")
	content := `---
tracker:
  kind: kanban
---
{{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	manager, err := NewManagerForPath(workflowPath)
	if err != nil {
		t.Fatalf("NewManagerForPath: %v", err)
	}
	if manager.Path() != workflowPath {
		t.Fatalf("expected path %s, got %s", workflowPath, manager.Path())
	}
}

func TestRenderLiquidTemplate(t *testing.T) {
	rendered, err := RenderLiquidTemplate(`Hello {{ issue.identifier }}{% if attempt %} #{{ attempt }}{% endif %}`, map[string]interface{}{
		"issue":   map[string]interface{}{"identifier": "ISS-1"},
		"attempt": 2,
	})
	if err != nil {
		t.Fatalf("RenderLiquidTemplate: %v", err)
	}
	if rendered != "Hello ISS-1 #2" {
		t.Fatalf("unexpected render output: %q", rendered)
	}
}

func TestRenderLiquidTemplateRejectsUnknownVariable(t *testing.T) {
	if _, err := RenderLiquidTemplate(`{{ issue.missing }}`, map[string]interface{}{
		"issue": map[string]interface{}{"identifier": "ISS-1"},
	}); err == nil {
		t.Fatal("expected unknown variable error")
	}
}

func TestManagerReloadKeepsLastKnownGood(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	initial := `---
tracker:
  kind: kanban
---
Hello {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	manager, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	bad := `---
tracker:
  kind: linear
---
{{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	current, err := manager.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if !strings.Contains(current.PromptTemplate, "Hello") {
		t.Fatalf("expected last known good workflow, got %q", current.PromptTemplate)
	}
	if manager.LastError() == nil {
		t.Fatal("expected reload error to be retained")
	}
}

func TestInitWorkflowWritesExpectedFile(t *testing.T) {
	tmpDir := t.TempDir()
	if err := InitWorkflow(tmpDir, InitOptions{
		WorkspaceRoot: "./ws",
		CodexCommand:  "codex app-server --model test",
		AgentMode:     AgentModeStdio,
	}); err != nil {
		t.Fatalf("InitWorkflow: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "WORKFLOW.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"# Tracker provider configuration. Supported tracker kind today: kanban.",
		"tracker:",
		"kind: kanban",
		"active_states:",
		"terminal_states:",
		"interval_ms: 10000",
		"root: ./ws",
		"# after_create: ./scripts/after-create.sh",
		"# before_run: ./scripts/before-run.sh",
		"# after_run: ./scripts/after-run.sh",
		"# before_remove: ./scripts/before-remove.sh",
		"timeout_ms: 60000",
		"phases:",
		"enabled: false",
		"issue.*, phase, and attempt",
		"max_concurrent_agents: 3",
		"max_turns: 4",
		"max_retry_backoff_ms: 60000",
		"max_automatic_retries: 8",
		"mode: stdio",
		"Other options: parallel, per_project_serial.",
		"codex app-server --model test",
		"expected_version: 0.111.0",
		"approval_policy: never",
		"on-request, on-failure, untrusted",
		"read-only, workspace-write, danger-full-access",
		"type: workspaceWrite",
		"networkAccess: true",
		"# writableRoots:",
		"# readOnlyAccess:",
		"# excludeTmpdirEnvVar: false",
		"# excludeSlashTmp: false",
		"turn_timeout_ms: 1800000",
		"read_timeout_ms: 10000",
		"stall_timeout_ms: 300000",
		"{{ issue.identifier }}",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected generated workflow to contain %q", want)
		}
	}
}

func TestGeneratedWorkflowRoundTrips(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := buildWorkflowFile(InitOptions{
		WorkspaceRoot: "./ws",
		CodexCommand:  "codex app-server --model test",
		AgentMode:     AgentModeStdio,
	})
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if workflow.Config.Tracker.Kind != TrackerKindKanban {
		t.Fatalf("unexpected tracker kind: %q", workflow.Config.Tracker.Kind)
	}
	if workflow.Config.Workspace.Root != filepath.Join(tmpDir, "ws") {
		t.Fatalf("unexpected workspace root: %s", workflow.Config.Workspace.Root)
	}
	if workflow.Config.Agent.Mode != AgentModeStdio {
		t.Fatalf("unexpected agent mode: %q", workflow.Config.Agent.Mode)
	}
	if workflow.Config.Agent.DispatchMode != DispatchModeParallel {
		t.Fatalf("unexpected dispatch mode: %q", workflow.Config.Agent.DispatchMode)
	}
	if workflow.Config.Agent.MaxAutomaticRetries != 8 {
		t.Fatalf("unexpected max automatic retries: %d", workflow.Config.Agent.MaxAutomaticRetries)
	}
	if workflow.Config.Codex.Command != "codex app-server --model test" {
		t.Fatalf("unexpected codex command: %q", workflow.Config.Codex.Command)
	}
	if workflow.Config.Codex.TurnSandboxPolicy["networkAccess"] != true {
		t.Fatalf("expected networkAccess=true, got %+v", workflow.Config.Codex.TurnSandboxPolicy)
	}
	if !strings.Contains(workflow.PromptTemplate, "{{ issue.identifier }}") {
		t.Fatalf("unexpected prompt template: %q", workflow.PromptTemplate)
	}
}

func TestEnsureWorkflowCreatesMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	path, created, err := EnsureWorkflow(tmpDir, InitOptions{})
	if err != nil {
		t.Fatalf("EnsureWorkflow: %v", err)
	}
	if !created {
		t.Fatal("expected workflow to be created")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected workflow file at %s: %v", path, err)
	}
}
