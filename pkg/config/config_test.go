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

func assertDefaultPromptSemantics(t *testing.T, prompt string) {
	t.Helper()
	assertContainsAll(t, prompt,
		"Determine the issue status first, then follow the matching flow.",
		"Open the Maestro Workpad comment immediately and update it before new implementation work.",
		"Plan before coding. Design verification before changing code.",
		"Reproduce or inspect current behavior first so the target is explicit.",
		"Treat the persistent workpad comment as the source of truth.",
		"file a separate Maestro issue instead of expanding scope.",
		"Use the issue branch already prepared by Maestro in the provided workspace.",
		"Do not consider the task complete until the change is merged into the repository default branch.",
		"Use the blocked-access escape hatch only for genuine external blockers after documented fallbacks are exhausted.",
	)
}

func assertDefaultReviewPromptSemantics(t *testing.T, prompt string) {
	t.Helper()
	assertContainsAll(t, prompt,
		"Review the implementation",
		"current issue workspace",
		"focused verification",
		"in_progress",
		"done",
	)
}

func assertDefaultDonePromptSemantics(t *testing.T, prompt string) {
	t.Helper()
	assertContainsAll(t, prompt,
		"current issue workspace",
		"Commit all remaining changes to the prepared issue branch.",
		"Merge the issue branch into the repository default branch.",
		"Rerun the relevant validation on the default branch.",
		"Push the default branch to origin.",
		"Do not remove the issue worktree yourself; leave the workspace intact for Maestro's post-run cleanup.",
		"If merge conflicts, missing credentials, permissions, or required services block completion, report the blocker clearly and stop.",
	)
}

func readWorkflowFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Tracker.Kind != TrackerKindKanban {
		t.Fatalf("expected tracker kind %q, got %q", TrackerKindKanban, cfg.Tracker.Kind)
	}
	if cfg.Polling.IntervalMs != 10000 {
		t.Fatalf("expected poll interval 10000, got %d", cfg.Polling.IntervalMs)
	}
	if cfg.Workspace.Root != "~/.maestro/worktrees" {
		t.Fatalf("expected default workspace root ~/.maestro/worktrees, got %q", cfg.Workspace.Root)
	}
	if cfg.Workspace.BranchPrefix != "maestro/" {
		t.Fatalf("expected neutral branch prefix, got %q", cfg.Workspace.BranchPrefix)
	}
	if cfg.Runtime.Default != "codex-appserver" {
		t.Fatalf("expected default runtime codex-appserver, got %q", cfg.Runtime.Default)
	}
	if cfg.Agent.Mode != AgentModeAppServer || cfg.Agent.DispatchMode != DispatchModeParallel {
		t.Fatalf("unexpected derived agent config: %+v", cfg.Agent)
	}
	if cfg.Codex.ExpectedVersion != codexschema.SupportedVersion {
		t.Fatalf("expected codex expected version %s, got %q", codexschema.SupportedVersion, cfg.Codex.ExpectedVersion)
	}
	assertGranularPolicy(t, cfg.Runtime.Entries["codex-appserver"].ApprovalPolicy)
	assertDefaultPromptSemantics(t, DefaultPromptTemplate())
	assertDefaultReviewPromptSemantics(t, DefaultReviewPromptTemplate())
	assertDefaultDonePromptSemantics(t, DefaultDonePromptTemplate())
}

func TestLoadWorkflowRejectsLegacyTopLevelSections(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
agent:
  mode: app_server
codex:
  command: codex app-server
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadWorkflow(workflowPath)
	if err == nil || !(strings.Contains(err.Error(), "field agent") || strings.Contains(err.Error(), "field codex")) {
		t.Fatalf("expected legacy top-level fields to fail, got %v", err)
	}
}

func TestLoadWorkflowRejectsInvalidRuntimeEntry(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	content := `---
tracker:
  kind: kanban
workspace:
  root: ./workspaces
  branch_prefix: maestro/
runtime:
  default: codex-appserver
  codex-appserver:
    provider: codex
    transport: app_server
    command: codex app-server
    approval_policy: never
    initial_collaboration_mode: invalid
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
---
Hello {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadWorkflow(workflowPath)
	if err == nil || !strings.Contains(err.Error(), "unsupported runtime.default.initial_collaboration_mode") {
		t.Fatalf("expected invalid runtime entry to fail, got %v", err)
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
	if workflow.Config.Workspace.BranchPrefix != "maestro/" {
		t.Fatalf("unexpected branch prefix: %s", workflow.Config.Workspace.BranchPrefix)
	}
	if workflow.PromptTemplate != content {
		t.Fatalf("unexpected prompt template: %q", workflow.PromptTemplate)
	}
}

func TestInitWorkflowWritesNeutralSchema(t *testing.T) {
	tmpDir := t.TempDir()
	if err := InitWorkflow(tmpDir, InitOptions{
		WorkspaceRoot:            "./ws",
		RuntimeCommand:           "codex app-server --model test",
		AgentMode:                AgentModeAppServer,
		DispatchMode:             DispatchModePerProjectSerial,
		MaxConcurrentAgents:      4,
		MaxTurns:                 5,
		MaxAutomaticRetries:      6,
		ApprovalPolicy:           "on-request",
		InitialCollaborationMode: InitialCollaborationModePlan,
	}); err != nil {
		t.Fatalf("InitWorkflow: %v", err)
	}

	text := readWorkflowFile(t, filepath.Join(tmpDir, "WORKFLOW.md"))
	assertContainsAll(t, text,
		"tracker:",
		"polling:",
		"workspace:",
		"branch_prefix: maestro/",
		"hooks:",
		"orchestrator:",
		"max_concurrent_agents: 4",
		"max_turns: 5",
		"max_automatic_retries: 6",
		"dispatch_mode: per_project_serial",
		"runtime:",
		"default: codex-appserver",
		"codex-appserver:",
		"transport: app_server",
		"command: codex app-server --model test",
		"approval_policy: on-request",
		"initial_collaboration_mode: plan",
		"codex-stdio:",
		"claude:",
		"phases:",
	)
	if strings.Contains(text, "\nagent:\n") || strings.Contains(text, "\ncodex:\n") {
		t.Fatalf("expected neutral front matter, got %q", text)
	}
	assertDefaultPromptSemantics(t, text)
	assertDefaultReviewPromptSemantics(t, text)
	assertDefaultDonePromptSemantics(t, text)
}

func TestInitWorkflowInteractiveWizardUsesDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	var stdout bytes.Buffer
	if err := InitWorkflow(tmpDir, InitOptions{
		Interactive: true,
		Stdin:       strings.NewReader(strings.Repeat("\n", 6)),
		Stdout:      &stdout,
	}); err != nil {
		t.Fatalf("InitWorkflow: %v", err)
	}

	text := readWorkflowFile(t, filepath.Join(tmpDir, "WORKFLOW.md"))
	assertContainsAll(t, text,
		"branch_prefix: maestro/",
		"default: codex-appserver",
		"approval_policy: never",
		"initial_collaboration_mode: default",
	)
	assertContainsAll(t, stdout.String(),
		"Workspace root",
		"Runtime command",
		"Agent mode",
		"Dispatch mode",
		"Initial collaboration mode",
		"Start fresh runtime sessions in default mode.",
		"Start fresh runtime sessions in plan mode.",
	)
}

func TestGeneratedWorkflowRoundTrips(t *testing.T) {
	tmpDir := t.TempDir()
	content := buildWorkflowFile(InitOptions{
		WorkspaceRoot:            "./ws",
		RuntimeCommand:           "codex app-server --model test",
		AgentMode:                AgentModeAppServer,
		DispatchMode:             DispatchModePerProjectSerial,
		MaxConcurrentAgents:      5,
		MaxTurns:                 6,
		MaxAutomaticRetries:      7,
		ApprovalPolicy:           "on-request",
		InitialCollaborationMode: InitialCollaborationModePlan,
	})
	if err := os.WriteFile(filepath.Join(tmpDir, "WORKFLOW.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, err := LoadWorkflow(filepath.Join(tmpDir, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if workflow.Config.Workspace.Root != filepath.Join(tmpDir, "ws") {
		t.Fatalf("unexpected workspace root: %s", workflow.Config.Workspace.Root)
	}
	if workflow.Config.Runtime.Default != "codex-appserver" || workflow.Config.Codex.Command != "codex app-server --model test" {
		t.Fatalf("unexpected selected runtime: %+v", workflow.Config.Codex)
	}
	if workflow.Config.Codex.InitialCollaborationMode != InitialCollaborationModePlan {
		t.Fatalf("unexpected initial collaboration mode: %q", workflow.Config.Codex.InitialCollaborationMode)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", workflow.Config.Codex.ApprovalPolicy)); got != "on-request" {
		t.Fatalf("unexpected approval policy: %q", got)
	}
}

func TestValidateInitOptionAliasesAndPrefixes(t *testing.T) {
	tests := []struct {
		name     string
		validate func(string) (string, error)
		input    string
		want     string
	}{
		{name: "agent alias", validate: validateInitAgentMode, input: "server", want: AgentModeAppServer},
		{name: "dispatch alias", validate: validateInitDispatchMode, input: "pps", want: DispatchModePerProjectSerial},
		{name: "approval underscore alias", validate: validateInitApprovalPolicy, input: "on_request", want: "on-request"},
		{name: "collaboration alias", validate: validateInitCollaborationMode, input: "def", want: InitialCollaborationModeDefault},
		{name: "approval prefix", validate: validateInitApprovalPolicy, input: "requ", want: "on-request"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.validate(tc.input)
			if err != nil {
				t.Fatalf("validate %q: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestValidateInitApprovalPolicyRejectsAmbiguousPrefix(t *testing.T) {
	_, err := validateInitApprovalPolicy("on")
	if !errors.Is(err, ErrInvalidInitApprovalPolicy) {
		t.Fatalf("expected invalid approval policy error, got %v", err)
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
}

func TestInitWorkflowOverwriteBehaviour(t *testing.T) {
	t.Run("noninteractive requires force", func(t *testing.T) {
		tmpDir := t.TempDir()
		workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
		if err := os.WriteFile(workflowPath, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}

		err := InitWorkflowAtPath(workflowPath, InitOptions{})
		if !errors.Is(err, ErrWorkflowExists) {
			t.Fatalf("expected existing workflow error, got %v", err)
		}
		if got := readWorkflowFile(t, workflowPath); got != "original" {
			t.Fatalf("expected existing file to remain unchanged, got %q", got)
		}
	})

	t.Run("force overwrites", func(t *testing.T) {
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
		text := readWorkflowFile(t, workflowPath)
		if strings.Contains(text, "original") || !strings.Contains(text, "branch_prefix: maestro/") {
			t.Fatalf("expected workflow to be overwritten, got %q", text)
		}
	})

	t.Run("interactive decline", func(t *testing.T) {
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
		if got := readWorkflowFile(t, workflowPath); got != "original" {
			t.Fatalf("expected existing file to remain unchanged, got %q", got)
		}
		if !strings.Contains(stdout.String(), "Overwrite?") {
			t.Fatalf("expected overwrite prompt, got %q", stdout.String())
		}
	})

	t.Run("interactive accept", func(t *testing.T) {
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
		if got := readWorkflowFile(t, workflowPath); got == "original" {
			t.Fatalf("expected existing workflow to be replaced")
		}
	})
}

func TestInitWorkflowRejectsInvalidExplicitAgentMode(t *testing.T) {
	tmpDir := t.TempDir()
	err := InitWorkflow(tmpDir, InitOptions{AgentMode: "invalid"})
	if !errors.Is(err, ErrInvalidInitAgentMode) {
		t.Fatalf("expected invalid agent mode error, got %v", err)
	}
}
