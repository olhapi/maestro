package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
)

func assertContainsAll(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("expected text to contain %q, got %q", want, text)
		}
	}
}

func assertGranularApprovalPolicy(t *testing.T, policy interface{}) {
	t.Helper()
	root, ok := policy.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map approval policy, got %#v", policy)
	}
	granular, ok := root["granular"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected granular approval policy, got %#v", policy)
	}
	if granular["mcp_elicitations"] != true || granular["rules"] != true || granular["sandbox_approval"] != true || granular["request_permissions"] != false {
		t.Fatalf("unexpected granular approval policy: %#v", policy)
	}
}

func assertDefaultPromptSemantics(t *testing.T, prompt string) {
	t.Helper()
	assertContainsAll(t, prompt,
		"Determine the issue status first, then follow the matching flow.",
		"Open the Maestro Workpad comment immediately and update it before new implementation work.",
		"Plan before coding. Design verification before changing code.",
		"Reproduce or inspect current behavior first so the target is explicit.",
		"Treat the persistent workpad comment as the source of truth.",
		"Use the issue branch already prepared by Maestro in the provided workspace.",
		"Do not consider the task complete until the change is merged into the repository default branch.",
		"Use the blocked-access escape hatch only for genuine external blockers after documented fallbacks are exhausted.",
		"In the done phase, after merge, push, and final validation succeed, leave the workspace intact; Maestro handles cleanup hooks and worktree removal after your run exits.",
	)
}

func assertDefaultDonePromptSemantics(t *testing.T, prompt string) {
	t.Helper()
	assertContainsAll(t, prompt,
		"The done phase owns merge-back and finalization for this issue from the current workspace. Maestro handles preview publication, cleanup hooks, and worktree removal after your run exits.",
		"Commit all remaining changes to the prepared issue branch.",
		"Merge the issue branch into the repository default branch.",
		"Rerun the relevant validation on the default branch.",
		"Push the default branch to origin.",
		"Do not remove the issue worktree yourself; leave the workspace intact for Maestro's post-run cleanup.",
		"If merge conflicts, missing credentials, permissions, or required services block completion, report the blocker clearly and stop.",
	)
}

func assertInitDonePromptSemantics(t *testing.T, prompt string) {
	t.Helper()
	assertContainsAll(t, prompt,
		"Finalize issue {{ issue.identifier }} from the current workspace.",
		"The done phase owns merge-back and finalization. Maestro handles preview publication, cleanup hooks, and worktree removal after your run exits.",
		"Commit all remaining changes to the prepared issue branch.",
		"Merge the issue branch into the repository default branch.",
		"Rerun the relevant validation on the default branch.",
		"Push the default branch to origin.",
		"Do not remove the issue worktree yourself; leave the workspace intact for Maestro's post-run cleanup.",
		"If merge conflicts, missing credentials, permissions, or required services block completion, report the blocker clearly and stop.",
	)
}

func assertWorkflowHasAdvisory(t *testing.T, workflow *Workflow, code string) {
	t.Helper()
	for _, advisory := range workflow.Advisories {
		if advisory.Code == code {
			return
		}
	}
	t.Fatalf("expected advisory %q, got %+v", code, workflow.Advisories)
}

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
	if cfg.Codex.ExpectedVersion != codexschema.SupportedVersion {
		t.Fatalf("expected codex expected version %s, got %q", codexschema.SupportedVersion, cfg.Codex.ExpectedVersion)
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
	if cfg.Codex.InitialCollaborationMode != InitialCollaborationModeDefault {
		t.Fatalf("expected initial collaboration mode %q, got %q", InitialCollaborationModeDefault, cfg.Codex.InitialCollaborationMode)
	}
	assertGranularApprovalPolicy(t, cfg.Codex.ApprovalPolicy)
	assertDefaultPromptSemantics(t, DefaultPromptTemplate())
	if !cfg.Phases.Review.Enabled || !strings.Contains(cfg.Phases.Review.Prompt, "review pass") {
		t.Fatalf("expected review phase defaults, got %+v", cfg.Phases.Review)
	}
	if !cfg.Phases.Done.Enabled {
		t.Fatalf("expected done phase defaults, got %+v", cfg.Phases.Done)
	}
	assertDefaultDonePromptSemantics(t, cfg.Phases.Done.Prompt)
	if advisories := detectWorkflowAdvisories(cfg, DefaultPromptTemplate(), nil); len(advisories) != 0 {
		t.Fatalf("expected default prompt to avoid advisories, got %+v", advisories)
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
  expected_version: ` + codexschema.SupportedVersion + `
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
	if workflow.Config.Codex.ExpectedVersion != codexschema.SupportedVersion {
		t.Fatalf("unexpected codex expected version: %q", workflow.Config.Codex.ExpectedVersion)
	}
	if workflow.Config.Codex.InitialCollaborationMode != InitialCollaborationModeDefault {
		t.Fatalf("expected default initial collaboration mode, got %q", workflow.Config.Codex.InitialCollaborationMode)
	}
	if !workflow.Config.Phases.Review.Enabled || !strings.Contains(workflow.Config.Phases.Review.Prompt, "review pass") {
		t.Fatalf("expected default review prompt, got %+v", workflow.Config.Phases.Review)
	}
	if !workflow.Config.Phases.Done.Enabled {
		t.Fatalf("expected default done prompt, got %+v", workflow.Config.Phases.Done)
	}
	assertDefaultDonePromptSemantics(t, workflow.Config.Phases.Done.Prompt)
	expectedRoot := filepath.Join(tmpDir, "custom-workspaces")
	if workflow.Config.Workspace.Root != expectedRoot {
		t.Fatalf("expected resolved workspace root %s, got %s", expectedRoot, workflow.Config.Workspace.Root)
	}
}

func TestLoadWorkflowDefaultsGranularApprovalPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
codex:
  command: codex app-server
  expected_version: ` + codexschema.SupportedVersion + `
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

	assertGranularApprovalPolicy(t, workflow.Config.Codex.ApprovalPolicy)
}

func TestLoadWorkflowNormalizesMapApprovalPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
codex:
  approval_policy:
    granular:
      sandbox_approval: true
      rules: true
      mcp_elicitations: true
      request_permissions: false
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

	assertGranularApprovalPolicy(t, workflow.Config.Codex.ApprovalPolicy)
}

func TestLoadWorkflowRejectsBlankApprovalPolicy(t *testing.T) {
	cases := []struct {
		name    string
		enabled string
	}{
		{
			name: "empty_scalar",
			enabled: `  approval_policy:
`,
		},
		{
			name: "quoted_blank",
			enabled: `  approval_policy: ""
`,
		},
		{
			name: "unknown_scalar",
			enabled: `  approval_policy: maybe
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
			content := `---
tracker:
  kind: kanban
codex:
  command: codex app-server
` + tc.enabled + `---
Issue {{ issue.identifier }}
`
			if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := LoadWorkflow(workflowPath); err == nil || !strings.Contains(err.Error(), "approval_policy") {
				t.Fatalf("expected invalid approval policy error, got %v", err)
			}
		})
	}
}

func TestLoadWorkflowRejectsNonMapFrontMatter(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
- kanban
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadWorkflow(workflowPath)
	if err == nil {
		t.Fatal("expected non-map front matter to fail")
	}
	if !errors.Is(err, ErrWorkflowFrontMatter) {
		t.Fatalf("expected ErrWorkflowFrontMatter, got %v", err)
	}
}

func TestLoadWorkflowAcceptsCommentOnlyFrontMatter(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
# default workflow configuration is inherited
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
	if workflow == nil || workflow.Config.Codex.Command == "" {
		t.Fatalf("expected workflow to load with defaults, got %+v", workflow)
	}
}

func TestLoadWorkflowDefaultsNegativeStallTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
codex:
  command: codex app-server
  stall_timeout_ms: -1
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
	if workflow.Config.Codex.StallTimeoutMs != 300000 {
		t.Fatalf("expected stall timeout to default to 300000, got %d", workflow.Config.Codex.StallTimeoutMs)
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

func TestLoadWorkflowPreservesExplicitInitialCollaborationMode(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
codex:
  initial_collaboration_mode: default
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
	if workflow.Config.Codex.InitialCollaborationMode != InitialCollaborationModeDefault {
		t.Fatalf("expected explicit initial collaboration mode %q, got %q", InitialCollaborationModeDefault, workflow.Config.Codex.InitialCollaborationMode)
	}
}

func TestLoadWorkflowDefaultsPhaseEnablementWhenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
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
	if !workflow.Config.Phases.Review.Enabled || !strings.Contains(workflow.Config.Phases.Review.Prompt, "review pass") {
		t.Fatalf("expected default review phase when missing from workflow, got %+v", workflow.Config.Phases.Review)
	}
	if !workflow.Config.Phases.Done.Enabled {
		t.Fatalf("expected default done phase when missing from workflow, got %+v", workflow.Config.Phases.Done)
	}
	assertDefaultDonePromptSemantics(t, workflow.Config.Phases.Done.Prompt)
}

func TestLoadWorkflowPreservesExplicitDisabledPhases(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
phases:
  review:
    enabled: false
  done:
    enabled: false
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
	if workflow.Config.Phases.Review.Enabled || workflow.Config.Phases.Done.Enabled {
		t.Fatalf("expected explicit phase disablement to be preserved, got review=%+v done=%+v", workflow.Config.Phases.Review, workflow.Config.Phases.Done)
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

func TestLoadWorkflowAcceptsLegacyFlatSchemaAliases(t *testing.T) {
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

func TestLoadWorkflowResolvesWorkspaceRootPaths(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("WORKSPACE_BASE", workDir)

	cases := []struct {
		name string
		root string
		want string
	}{
		{
			name: "env",
			root: "$WORKSPACE_BASE/workspaces",
			want: filepath.Join(workDir, "workspaces"),
		},
		{
			name: "home",
			root: "~/workspaces",
			want: filepath.Join(homeDir, "workspaces"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
			content := fmt.Sprintf(`---
tracker:
  kind: kanban
workspace:
  root: %q
---
Issue {{ issue.identifier }}
`, tc.root)
			if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}

			workflow, err := LoadWorkflow(workflowPath)
			if err != nil {
				t.Fatalf("LoadWorkflow: %v", err)
			}
			if workflow.Config.Workspace.Root != tc.want {
				t.Fatalf("expected workspace root %q, got %q", tc.want, workflow.Config.Workspace.Root)
			}
		})
	}
}

func TestLoadWorkflowRejectsUnsupportedTrackerKind(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: github
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

func TestLoadWorkflowRejectsUnsupportedLegacyTrackerFields(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
tracker_api_token: secret
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadWorkflow(workflowPath); err == nil || !strings.Contains(err.Error(), "legacy workflow key \"tracker_api_token\" is not supported in kanban mode") {
		t.Fatalf("expected unsupported legacy workflow key error, got %v", err)
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

func TestResolveWorkflowPathResolvesEnvAndHomePaths(t *testing.T) {
	homeDir := t.TempDir()
	workflowDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("WORKFLOW_ROOT", workflowDir)

	if got := ResolveWorkflowPath("", "$WORKFLOW_ROOT/WORKFLOW.md"); got != filepath.Join(workflowDir, "WORKFLOW.md") {
		t.Fatalf("expected env-expanded workflow path, got %s", got)
	}
	if got := ResolveWorkflowPath("", "~/WORKFLOW.md"); got != filepath.Join(homeDir, "WORKFLOW.md") {
		t.Fatalf("expected home-expanded workflow path, got %s", got)
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
  kind: github
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

func TestManagerRefreshClearsLastErrorAfterRecovery(t *testing.T) {
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

	if err := os.Chmod(workflowPath, 0o000); err != nil {
		t.Fatal(err)
	}
	_, _ = manager.Refresh()
	if manager.LastError() == nil {
		t.Fatal("expected refresh failure to be retained")
	}

	if err := os.Chmod(workflowPath, 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Refresh()
	if err != nil {
		t.Fatalf("Refresh after recovery: %v", err)
	}
	if current == nil || !strings.Contains(current.PromptTemplate, "Hello") {
		t.Fatalf("expected recovered workflow, got %+v", current)
	}
	if manager.LastError() != nil {
		t.Fatalf("expected last error to clear after recovery, got %v", manager.LastError())
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
		"# Tracker configuration. Supported tracker kind today: kanban.",
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
		"enabled: true",
		"issue.*, project.*, phase, and attempt",
		"max_concurrent_agents: 3",
		"max_turns: 4",
		"max_retry_backoff_ms: 60000",
		"max_automatic_retries: 8",
		"mode: stdio",
		"Other options: parallel, per_project_serial.",
		"codex app-server --model test",
		"expected_version: " + codexschema.SupportedVersion,
		"approval_policy: never",
		"initial_collaboration_mode: default",
		"on-request, on-failure, untrusted",
		"Ignored for stdio runs and resumed threads.",
		"turn_timeout_ms: 1800000",
		"read_timeout_ms: 10000",
		"stall_timeout_ms: 300000",
		"{{ issue.identifier }}",
		"Maestro handles cleanup hooks and worktree removal after your run exits.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected generated workflow to contain %q", want)
		}
	}
	assertDefaultPromptSemantics(t, text)
	assertInitDonePromptSemantics(t, text)
}

func TestInitWorkflowInteractiveWizardUsesDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	var stdout bytes.Buffer
	if err := InitWorkflow(tmpDir, InitOptions{
		Interactive: true,
		Stdin:       strings.NewReader(strings.Repeat("\n", 9)),
		Stdout:      &stdout,
	}); err != nil {
		t.Fatalf("InitWorkflow: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "WORKFLOW.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"root: ./workspaces",
		"command: codex app-server",
		"mode: app_server",
		"dispatch_mode: parallel",
		"max_concurrent_agents: 3",
		"max_turns: 4",
		"max_automatic_retries: 8",
		"approval_policy: never",
		"initial_collaboration_mode: default",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected generated workflow to contain %q", want)
		}
	}
	for _, want := range []string{
		"Target workflow file:",
		"Workspace root [",
		"Codex command [",
		"Agent mode (app_server|stdio) [",
		"Dispatch mode (parallel|per_project_serial) [",
		"Max concurrent agents [",
		"Max turns [",
		"Max automatic retries [",
		"Approval policy (never|on-request|on-failure|untrusted) [",
		"Initial collaboration mode (default|plan) [",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected wizard output to contain %q, got %q", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "Runtime setup:") {
		t.Fatalf("expected wizard output, got %q", stdout.String())
	}
}

func TestInitWorkflowInteractiveWizardSupportsCustomStdioTuning(t *testing.T) {
	tmpDir := t.TempDir()
	var stdout bytes.Buffer
	if err := InitWorkflow(tmpDir, InitOptions{
		Interactive: true,
		Stdin:       strings.NewReader("./ws\ncodex exec --model test\nstdio\nper_project_serial\n5\n6\n7\n"),
		Stdout:      &stdout,
	}); err != nil {
		t.Fatalf("InitWorkflow: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "WORKFLOW.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"root: ./ws",
		"command: codex exec --model test",
		"mode: stdio",
		"dispatch_mode: per_project_serial",
		"max_concurrent_agents: 5",
		"max_turns: 6",
		"max_automatic_retries: 7",
		"approval_policy: never",
		"initial_collaboration_mode: default",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected generated workflow to contain %q", want)
		}
	}
	if strings.Contains(stdout.String(), "Approval policy (never|on-request|on-failure|untrusted)") || strings.Contains(stdout.String(), "Initial collaboration mode (default|plan)") {
		t.Fatalf("expected stdio wizard to skip app_server-only prompts, got %q", stdout.String())
	}
}

func TestInitWorkflowInteractiveWizardSupportsAppServerCollaborationTuning(t *testing.T) {
	tmpDir := t.TempDir()
	var stdout bytes.Buffer
	if err := InitWorkflow(tmpDir, InitOptions{
		Interactive: true,
		Stdin:       strings.NewReader("./ws\ncodex app-server --model test\napp_server\nparallel\n4\n5\n6\non-request\nplan\n"),
		Stdout:      &stdout,
	}); err != nil {
		t.Fatalf("InitWorkflow: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "WORKFLOW.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"root: ./ws",
		"command: codex app-server --model test",
		"mode: app_server",
		"dispatch_mode: parallel",
		"max_concurrent_agents: 4",
		"max_turns: 5",
		"max_automatic_retries: 6",
		"approval_policy: on-request",
		"initial_collaboration_mode: plan",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected generated workflow to contain %q", want)
		}
	}
	assertContainsAll(t, stdout.String(),
		"Approval policy (never|on-request|on-failure|untrusted) [",
		"Initial collaboration mode (default|plan) [",
	)
}

func TestInitWorkflowInteractiveWizardRepromptsOnInvalidInput(t *testing.T) {
	tmpDir := t.TempDir()
	var stdout bytes.Buffer
	if err := InitWorkflow(tmpDir, InitOptions{
		Interactive: true,
		Stdin:       strings.NewReader("\n\nbad-mode\nstdio\nserial\nper_project_serial\n0\n2\nabc\n5\n-1\n6\n"),
		Stdout:      &stdout,
	}); err != nil {
		t.Fatalf("InitWorkflow: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "WORKFLOW.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"mode: stdio",
		"dispatch_mode: per_project_serial",
		"max_concurrent_agents: 2",
		"max_turns: 5",
		"max_automatic_retries: 6",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected generated workflow to contain %q", want)
		}
	}
	if strings.Count(stdout.String(), "Invalid value:") < 4 {
		t.Fatalf("expected wizard to report invalid input and reprompt, got %q", stdout.String())
	}
}

func TestInitWorkflowExplicitOverridesTakePrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	var stdout bytes.Buffer
	if err := InitWorkflow(tmpDir, InitOptions{
		Interactive:   true,
		WorkspaceRoot: "./flag-ws",
		CodexCommand:  "codex exec --model custom",
		AgentMode:     AgentModeStdio,
		Stdin:         strings.NewReader(""),
		Stdout:        &stdout,
	}); err != nil {
		t.Fatalf("InitWorkflow: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "WORKFLOW.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"root: ./flag-ws",
		"command: codex exec --model custom",
		"mode: stdio",
		"dispatch_mode: parallel",
		"max_concurrent_agents: 3",
		"max_turns: 4",
		"max_automatic_retries: 8",
		"initial_collaboration_mode: default",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected generated workflow to contain %q", want)
		}
	}
	if strings.Contains(stdout.String(), "Workspace root [") || strings.Contains(stdout.String(), "Codex command [") || strings.Contains(stdout.String(), "Agent mode (app_server|stdio) [") {
		t.Fatalf("expected explicit values to skip prompts, got %q", stdout.String())
	}
	assertContainsAll(t, stdout.String(),
		"Dispatch mode (parallel|per_project_serial) [",
		"Max concurrent agents [",
		"Max turns [",
		"Max automatic retries [",
	)
}

func TestGeneratedWorkflowRoundTrips(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := buildWorkflowFile(InitOptions{
		WorkspaceRoot:            "./ws",
		CodexCommand:             "codex app-server --model test",
		AgentMode:                AgentModeAppServer,
		DispatchMode:             DispatchModePerProjectSerial,
		MaxConcurrentAgents:      5,
		MaxTurns:                 6,
		MaxAutomaticRetries:      7,
		ApprovalPolicy:           "on-request",
		InitialCollaborationMode: InitialCollaborationModePlan,
	})
	assertDefaultPromptSemantics(t, content)
	assertInitDonePromptSemantics(t, content)
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
	if workflow.Config.Agent.Mode != AgentModeAppServer {
		t.Fatalf("unexpected agent mode: %q", workflow.Config.Agent.Mode)
	}
	if workflow.Config.Agent.DispatchMode != DispatchModePerProjectSerial {
		t.Fatalf("unexpected dispatch mode: %q", workflow.Config.Agent.DispatchMode)
	}
	if workflow.Config.Agent.MaxConcurrentAgents != 5 {
		t.Fatalf("unexpected max concurrent agents: %d", workflow.Config.Agent.MaxConcurrentAgents)
	}
	if workflow.Config.Agent.MaxTurns != 6 {
		t.Fatalf("unexpected max turns: %d", workflow.Config.Agent.MaxTurns)
	}
	if workflow.Config.Agent.MaxAutomaticRetries != 7 {
		t.Fatalf("unexpected max automatic retries: %d", workflow.Config.Agent.MaxAutomaticRetries)
	}
	if workflow.Config.Codex.Command != "codex app-server --model test" {
		t.Fatalf("unexpected codex command: %q", workflow.Config.Codex.Command)
	}
	if workflow.Config.Codex.InitialCollaborationMode != InitialCollaborationModePlan {
		t.Fatalf("unexpected initial collaboration mode: %q", workflow.Config.Codex.InitialCollaborationMode)
	}
	if got := strings.TrimSpace(workflow.Config.Codex.ApprovalPolicy.(string)); got != "on-request" {
		t.Fatalf("unexpected approval policy: %q", got)
	}
	if !workflow.Config.Phases.Review.Enabled || !strings.Contains(workflow.Config.Phases.Review.Prompt, "Review the implementation for issue") {
		t.Fatalf("expected generated workflow review phase to round-trip, got %+v", workflow.Config.Phases.Review)
	}
	if !workflow.Config.Phases.Done.Enabled {
		t.Fatalf("expected generated workflow done phase to round-trip, got %+v", workflow.Config.Phases.Done)
	}
	assertInitDonePromptSemantics(t, workflow.Config.Phases.Done.Prompt)
	if !strings.Contains(workflow.PromptTemplate, "{{ issue.identifier }}") {
		t.Fatalf("unexpected prompt template: %q", workflow.PromptTemplate)
	}
	if strings.Contains(content, "thread_sandbox:") || strings.Contains(content, "turn_sandbox_policy:") {
		t.Fatalf("expected generated workflow to omit sandbox fields, got %q", content)
	}
	if len(workflow.Advisories) != 0 {
		t.Fatalf("expected generated workflow to avoid advisories, got %+v", workflow.Advisories)
	}
}

func TestLegacyWorkflowUsesFullAccess(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
codex:
  thread_sandbox: danger-full-access
  turn_sandbox_policy:
    type: dangerFullAccess
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	usesFullAccess, err := LegacyWorkflowUsesFullAccess(workflowPath)
	if err != nil {
		t.Fatalf("LegacyWorkflowUsesFullAccess: %v", err)
	}
	if !usesFullAccess {
		t.Fatal("expected legacy workflow helper to detect full access")
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if workflow.Config.Codex.Command == "" {
		t.Fatal("expected workflow to remain loadable after ignoring legacy sandbox fields")
	}
	assertWorkflowHasAdvisory(t, workflow, WorkflowAdvisoryPermissions)
}

func TestLoadWorkflowWarnsWhenApprovalPolicyNeverBlocksLegacySandboxRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
codex:
  approval_policy: never
  thread_sandbox: danger-full-access
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
	assertWorkflowHasAdvisory(t, workflow, WorkflowAdvisoryPermissions)
	assertWorkflowHasAdvisory(t, workflow, WorkflowAdvisoryApprovalPolicy)
}

func TestLoadWorkflowWarnsWhenPlanModeUsesApprovalPolicyNever(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
agent:
  mode: app_server
codex:
  approval_policy: never
  initial_collaboration_mode: plan
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
	assertWorkflowHasAdvisory(t, workflow, WorkflowAdvisoryPlanApprovalPolicy)
}

func TestLoadWorkflowWarnsOnLegacyBranchInstructions(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
phases:
  done:
    prompt: |
      Sync origin/main first.
      Merge the issue branch into local main.
      Push main to origin.
---
Title: {{ issue.title }}
State: {{ issue.state }}

## Instructions
5. Create a dedicated issue branch before editing. Use maestro/{{ issue.identifier }}.
6. Do not consider the task complete until the change is merged into local main.
7. Before marking done, sync origin/main, merge the issue branch into local main, rerun validation on main, and push main to origin.
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	assertWorkflowHasAdvisory(t, workflow, WorkflowAdvisoryPromptBranching)
}

func TestLoadWorkflowSkipsDoneBranchAdvisoryWhenDoneDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
phases:
  done:
    enabled: false
    prompt: |
      Sync origin/main first.
      Merge the issue branch into local main.
      Push main to origin.
---
Title: {{ issue.title }}
State: {{ issue.state }}

## Instructions
5. Create a dedicated issue branch before editing. Use maestro/{{ issue.identifier }}.
6. Do not consider the task complete until the change is merged into local main.
7. Before marking done, sync origin/main, merge the issue branch into local main, rerun validation on main, and push main to origin.
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if len(workflow.Advisories) != 0 {
		t.Fatalf("expected disabled done phase to skip branch advisory, got %+v", workflow.Advisories)
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

func TestInitWorkflowRejectsInvalidExplicitAgentMode(t *testing.T) {
	tmpDir := t.TempDir()
	err := InitWorkflow(tmpDir, InitOptions{AgentMode: "invalid"})
	if !errors.Is(err, ErrInvalidInitAgentMode) {
		t.Fatalf("expected invalid agent mode error, got %v", err)
	}
}

func TestInitWorkflowNonInteractiveRequiresForceToOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := InitWorkflowAtPath(workflowPath, InitOptions{})
	if !errors.Is(err, ErrWorkflowExists) {
		t.Fatalf("expected existing workflow error, got %v", err)
	}
	data, readErr := os.ReadFile(workflowPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "original" {
		t.Fatalf("expected existing file to remain unchanged, got %q", string(data))
	}
}

func TestInitWorkflowForceOverwritesExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := InitWorkflowAtPath(workflowPath, InitOptions{
		Force:         true,
		WorkspaceRoot: "./ws",
	}); err != nil {
		t.Fatalf("InitWorkflowAtPath: %v", err)
	}
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if text == "original" || !strings.Contains(text, "root: ./ws") {
		t.Fatalf("expected workflow to be overwritten, got %q", text)
	}
}

func TestInitWorkflowInteractiveOverwriteDeclined(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := InitWorkflowAtPath(workflowPath, InitOptions{
		Interactive: true,
		Stdin:       strings.NewReader("n\n"),
		Stdout:      &stdout,
	})
	if !errors.Is(err, ErrWorkflowInitCancelled) {
		t.Fatalf("expected cancelled error, got %v", err)
	}
	data, readErr := os.ReadFile(workflowPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "original" {
		t.Fatalf("expected existing file to remain unchanged, got %q", string(data))
	}
	if !strings.Contains(stdout.String(), "Overwrite?") {
		t.Fatalf("expected overwrite prompt, got %q", stdout.String())
	}
}

func TestInitWorkflowInteractiveOverwriteAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := InitWorkflowAtPath(workflowPath, InitOptions{
		Interactive: true,
		Stdin:       strings.NewReader("y\n\n\n\n"),
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("InitWorkflowAtPath: %v", err)
	}
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "original" {
		t.Fatalf("expected existing workflow to be replaced")
	}
}

func TestLoadWorkflowRejectsInvalidInitialCollaborationMode(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
codex:
  initial_collaboration_mode: invalid
---
Implement {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadWorkflow(workflowPath)
	if err == nil || !strings.Contains(err.Error(), "unsupported codex.initial_collaboration_mode") {
		t.Fatalf("expected invalid initial collaboration mode error, got %v", err)
	}
}
