package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
)

func TestWorkflowPathResolveLoadAndManager(t *testing.T) {
	if base := filepath.Base(WorkflowPath("")); base != "WORKFLOW.md" {
		t.Fatalf("expected workflow filename, got %q", base)
	}
	if got := WorkflowPath("/repo"); got != filepath.Join("/repo", "WORKFLOW.md") {
		t.Fatalf("unexpected workflow path: %q", got)
	}

	envRoot := filepath.Join(t.TempDir(), "workflow-root")
	t.Setenv("MAESTRO_WORKFLOW_ROOT", envRoot)
	if got := ResolveWorkflowPath("/repo", "$MAESTRO_WORKFLOW_ROOT/workflow.md"); got != filepath.Clean(filepath.Join(envRoot, "workflow.md")) {
		t.Fatalf("unexpected resolved env path: %q", got)
	}
	if got := ResolveWorkflowPath("/repo", "nested/WORKFLOW.md"); got != filepath.Clean(filepath.Join("/repo", "nested", "WORKFLOW.md")) {
		t.Fatalf("unexpected relative resolved path: %q", got)
	}

	missing := filepath.Join(t.TempDir(), "missing.md")
	if _, err := LoadWorkflow(missing); !errors.Is(err, ErrMissingWorkflowFile) {
		t.Fatalf("expected missing workflow file error, got %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	content := strings.TrimSpace(`
---
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
    expected_version: ` + codexschema.SupportedVersion + `
    approval_policy: never
    initial_collaboration_mode: default
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
  codex-stdio:
    provider: codex
    transport: stdio
    command: codex exec
    expected_version: ` + codexschema.SupportedVersion + `
    approval_policy: never
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
  claude:
    provider: claude
    transport: stdio
    command: claude
    approval_policy: never
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
---
Hello {{ issue.identifier }}
`)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	workflow, err := LoadWorkflow(path)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if workflow.Path != path {
		t.Fatalf("unexpected workflow path: %q", workflow.Path)
	}
	if workflow.PromptTemplate != "Hello {{ issue.identifier }}" {
		t.Fatalf("unexpected workflow prompt: %q", workflow.PromptTemplate)
	}
	if got := workflow.Config.Workspace.Root; got != filepath.Clean(filepath.Join(dir, "workspaces")) {
		t.Fatalf("unexpected resolved workspace root: %q", got)
	}
	if workflow.Config.Workspace.BranchPrefix != "maestro/" {
		t.Fatalf("unexpected branch prefix: %q", workflow.Config.Workspace.BranchPrefix)
	}
	if workflow.Config.Runtime.Default != "codex-appserver" {
		t.Fatalf("unexpected runtime default: %q", workflow.Config.Runtime.Default)
	}
	if selectedRuntime := workflow.Config.SelectedRuntimeConfig(); selectedRuntime.Command != "codex app-server" {
		t.Fatalf("unexpected selected runtime command: %q", selectedRuntime.Command)
	}

	payload, err := parseWorkflowPayload(path, content)
	if err != nil {
		t.Fatalf("parseWorkflowPayload: %v", err)
	}
	if payload.Prompt != "Hello {{ issue.identifier }}" {
		t.Fatalf("unexpected prompt payload: %q", payload.Prompt)
	}
	if got := payload.Config.Workspace.Root; got != filepath.Clean(filepath.Join(dir, "workspaces")) {
		t.Fatalf("unexpected payload workspace root: %q", got)
	}
	if got := payload.Config.Runtime.Default; got != "codex-appserver" {
		t.Fatalf("unexpected payload runtime default: %q", got)
	}
}

func TestConfigManagerKeepsLastKnownGoodWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "WORKFLOW.md")
	initial := strings.TrimSpace(`
---
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
    expected_version: ` + codexschema.SupportedVersion + `
    approval_policy: never
    initial_collaboration_mode: default
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
  codex-stdio:
    provider: codex
    transport: stdio
    command: codex exec
    expected_version: ` + codexschema.SupportedVersion + `
    approval_policy: never
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
  claude:
    provider: claude
    transport: stdio
    command: claude
    approval_policy: never
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
---
Hello {{ issue.identifier }}
`)
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile initial workflow: %v", err)
	}

	manager, err := NewManagerForPath(path)
	if err != nil {
		t.Fatalf("NewManagerForPath: %v", err)
	}

	bad := `---
tracker:
  kind: github
---
{{ issue.identifier }}
`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatalf("WriteFile bad workflow: %v", err)
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
