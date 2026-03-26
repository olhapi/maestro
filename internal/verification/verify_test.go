package verification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
)

func TestRunVerification(t *testing.T) {
	tmp := t.TempDir()
	db := filepath.Join(tmp, "db", "maestro.db")
	res := Run(tmp, db)
	if res.OK {
		t.Fatalf("expected missing workflow to fail verification, got %+v", res)
	}
	if res.Checks["workflow"] != "fail" || res.Checks["workflow_load"] != "fail" {
		t.Fatalf("expected workflow checks to fail without creating a file: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(tmp, "WORKFLOW.md")); !os.IsNotExist(err) {
		t.Fatalf("expected verify to stay read-only, stat err=%v", err)
	}
	if res.Checks["db_open"] != "ok" {
		t.Fatalf("db check failed: %+v", res)
	}
}

func TestRunVerificationUsesHomeDefaultDBPath(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	res := Run(tmp, "")
	if res.OK {
		t.Fatalf("expected missing workflow to fail verification, got %+v", res)
	}
	if res.Checks["workflow"] != "fail" {
		t.Fatalf("expected workflow check to fail, got %+v", res)
	}

	dbPath := filepath.Join(home, ".maestro", "maestro.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected db at %s: %v", dbPath, err)
	}
}

func TestRunVerificationSkipsLiteralDbDirCreationForUnresolvedEnvPath(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TEAM", "")
	workflow := `---
tracker:
  kind: kanban
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})

	res := Run(tmp, "$HOME/.maestro/$TEAM/maestro.db")
	if res.OK {
		t.Fatalf("expected unresolved db path to fail verification, got %+v", res)
	}
	if res.Checks["db_dir"] != "skipped" {
		t.Fatalf("expected db_dir to be skipped, got %+v", res.Checks)
	}
	if res.Checks["db_open"] != "fail" {
		t.Fatalf("expected db_open to fail, got %+v", res.Checks)
	}
	if _, err := os.Stat(filepath.Join(home, ".maestro", "$TEAM")); !os.IsNotExist(err) {
		t.Fatalf("expected verify to avoid creating literal env dir, stat err=%v", err)
	}
}

func TestRunVerificationWarnsOnCodexVersionMismatch(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nprintf 'codex-cli 9.9.9\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := `---
tracker:
  kind: kanban
codex:
  command: ` + codexPath + ` app-server
  expected_version: ` + codexschema.SupportedVersion + `
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Run(tmp, filepath.Join(tmp, "db", "maestro.db"))
	if res.Checks["codex_version"] != "warn" {
		t.Fatalf("expected codex warning, got %+v", res)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "expected "+codexschema.SupportedVersion+", found 9.9.9") {
		t.Fatalf("unexpected warnings: %+v", res.Warnings)
	}
}

func TestRunVerificationWarnsOnWorkflowAdvisories(t *testing.T) {
	tmp := t.TempDir()
	workflow := `---
tracker:
  kind: kanban
codex:
  approval_policy: never
  thread_sandbox: danger-full-access
phases:
  done:
    prompt: |
      Sync origin/main first.
      Merge the issue branch into local main.
      Push main to origin.
---
## Instructions
5. Create a dedicated issue branch before editing. Use maestro/{{ issue.identifier }}.
`
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Run(tmp, filepath.Join(tmp, "db", "maestro.db"))
	if res.Checks["workflow_permissions"] != "warn" {
		t.Fatalf("expected workflow permissions warning, got %+v", res)
	}
	if res.Checks["workflow_approval_policy"] != "warn" {
		t.Fatalf("expected workflow approval policy warning, got %+v", res)
	}
	if res.Checks["workflow_prompt_branching"] != "warn" {
		t.Fatalf("expected workflow prompt warning, got %+v", res)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "workflow_permissions:") || !strings.Contains(joined, "workflow_approval_policy:") || !strings.Contains(joined, "workflow_prompt_branching:") {
		t.Fatalf("expected advisory warnings, got %+v", res.Warnings)
	}
	if !strings.Contains(res.Remediation["workflow_permissions"], "permission profile") {
		t.Fatalf("expected workflow permissions remediation, got %+v", res.Remediation)
	}
	if !strings.Contains(res.Remediation["workflow_approval_policy"], "approval_policy=never") {
		t.Fatalf("expected workflow approval policy remediation, got %+v", res.Remediation)
	}
	if !strings.Contains(res.Remediation["workflow_prompt_branching"], "prepared by Maestro") {
		t.Fatalf("expected workflow prompt remediation, got %+v", res.Remediation)
	}
}
