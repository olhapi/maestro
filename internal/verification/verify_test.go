package verification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
)

func workflowFixture(version string) string {
	return `---
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
    command: codex
    expected_version: ` + version + `
    approval_policy: never
    initial_collaboration_mode: default
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
  codex-stdio:
    provider: codex
    transport: stdio
    command: codex exec
    expected_version: ` + version + `
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
Issue {{ issue.identifier }}
`
}

func writeFakeCodex(t *testing.T, root, version string) string {
	t.Helper()
	codexPath := filepath.Join(root, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nprintf 'codex-cli "+version+"\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", filepath.Dir(codexPath)+string(os.PathListSeparator)+oldPath)
	return codexPath
}

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

func TestRunVerificationSucceedsForValidWorkflow(t *testing.T) {
	tmp := t.TempDir()
	writeFakeCodex(t, tmp, codexschema.SupportedVersion)
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflowFixture(codexschema.SupportedVersion)), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Run(tmp, filepath.Join(tmp, "db", "maestro.db"))
	if !res.OK {
		t.Fatalf("expected valid workflow to pass verification, got %+v", res)
	}
	if res.Checks["workflow"] != "ok" || res.Checks["workflow_load"] != "ok" || res.Checks["db_open"] != "ok" {
		t.Fatalf("expected healthy checks, got %+v", res.Checks)
	}
}

func TestRunVerificationReportsWorkflowDirectory(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, "WORKFLOW.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := Run(tmp, filepath.Join(tmp, "db", "maestro.db"))
	if res.Checks["workflow"] != "fail" || res.Checks["workflow_load"] != "fail" {
		t.Fatalf("expected directory workflow to fail, got %+v", res.Checks)
	}
	if len(res.Errors) == 0 || !strings.Contains(strings.Join(res.Errors, "\n"), "is a directory") {
		t.Fatalf("expected directory error, got %+v", res.Errors)
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
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflowFixture(codexschema.SupportedVersion)), 0o644); err != nil {
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
	writeFakeCodex(t, tmp, "9.9.9")
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflowFixture(codexschema.SupportedVersion)), 0o644); err != nil {
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

func TestRunVerificationReportsWorkflowLoadFailure(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte("---\n- not-a-map\n---\nIssue {{ issue.identifier }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Run(tmp, filepath.Join(tmp, "db", "maestro.db"))
	if res.Checks["workflow_load"] != "fail" {
		t.Fatalf("expected workflow_load to fail, got %+v", res.Checks)
	}
	if res.Checks["db_dir"] != "ok" || res.Checks["db_open"] != "ok" {
		t.Fatalf("expected database checks to still succeed, got %+v", res.Checks)
	}
}

func TestRunVerificationReportsDbDirFailure(t *testing.T) {
	tmp := t.TempDir()
	blocked := filepath.Join(tmp, "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflowFixture(codexschema.SupportedVersion)), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Run(tmp, filepath.Join(blocked, "maestro.db"))
	if res.Checks["db_dir"] != "fail" {
		t.Fatalf("expected db_dir to fail, got %+v", res.Checks)
	}
	if res.Checks["db_open"] != "skipped" && res.Checks["db_open"] != "fail" {
		t.Fatalf("expected db_open to be skipped or fail after db_dir error, got %+v", res.Checks)
	}
}
