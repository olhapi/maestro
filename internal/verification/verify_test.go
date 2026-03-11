package verification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
  expected_version: 0.111.0
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
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "expected 0.111.0, found 9.9.9") {
		t.Fatalf("unexpected warnings: %+v", res.Warnings)
	}
}
