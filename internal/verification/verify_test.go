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

func TestRunVerificationSucceedsForValidWorkflow(t *testing.T) {
	tmp := t.TempDir()
	workflow := `---
tracker:
  kind: kanban
agent:
  mode: stdio
codex:
  command: cat
  approval_policy: never
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
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

func TestRunVerificationResolvesCodexVersionMismatch(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nprintf 'codex-cli 9.9.9\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	npxPath := filepath.Join(tmp, "npx")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" != \"-y\" ]; then\n" +
		"  echo \"unexpected npx args: $*\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"shift\n" +
		"if [ \"$1\" != \"@openai/codex@" + codexschema.SupportedVersion + "\" ]; then\n" +
		"  echo \"unexpected package: $1\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"shift\n" +
		"if [ \"$1\" != \"--version\" ]; then\n" +
		"  echo \"unexpected version probe args: $*\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"printf 'codex-cli " + codexschema.SupportedVersion + "\\n'\n"
	if err := os.WriteFile(npxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	workflow := `---
tracker:
  kind: kanban
codex:
  command: ` + codexPath + ` app-server
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Run(tmp, filepath.Join(tmp, "db", "maestro.db"))
	if res.Checks["codex_version"] != "ok" {
		t.Fatalf("expected codex version to resolve through npx, got %+v", res)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("expected no codex warnings after resolution, got %+v", res.Warnings)
	}
}

func TestRunVerificationResolvesPinnedNPXCodexVersionMismatch(t *testing.T) {
	tmp := t.TempDir()
	npxPath := filepath.Join(tmp, "npx")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" != \"-y\" ]; then\n" +
		"  echo \"unexpected npx args: $*\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"shift\n" +
		"if [ \"$1\" = \"@openai/codex@" + codexschema.SupportedVersion + "\" ]; then\n" +
		"  printf 'codex-cli " + codexschema.SupportedVersion + "\\n'\n" +
		"else\n" +
		"  printf 'codex-cli 9.9.9\\n'\n" +
		"fi\n"
	if err := os.WriteFile(npxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))

	workflow := `---
tracker:
  kind: kanban
codex:
  command: npx -y @openai/codex@0.117.0 app-server
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Run(tmp, filepath.Join(tmp, "db", "maestro.db"))
	if res.Checks["codex_version"] != "ok" {
		t.Fatalf("expected pinned npx command to resolve to supported version, got %+v", res)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("expected no codex warnings after resolution, got %+v", res.Warnings)
	}
}

func TestRunVerificationReportsWorkflowLoadFailure(t *testing.T) {
	tmp := t.TempDir()
	workflow := `---
- not-a-map
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
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
	workflow := `---
tracker:
  kind: kanban
codex:
  command: cat
  approval_policy: never
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
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

func TestRunVerificationWarnsWhenPlanModeKeepsApprovalPolicyNever(t *testing.T) {
	tmp := t.TempDir()
	workflow := `---
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
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Run(tmp, filepath.Join(tmp, "db", "maestro.db"))
	if res.Checks["workflow_plan_approval_policy"] != "warn" {
		t.Fatalf("expected plan-mode approval warning, got %+v", res)
	}
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), "workflow_plan_approval_policy:") {
		t.Fatalf("expected plan-mode warning entry, got %+v", res.Warnings)
	}
	if !strings.Contains(res.Remediation["workflow_plan_approval_policy"], "approval_policy=on-request") {
		t.Fatalf("expected plan-mode remediation, got %+v", res.Remediation)
	}
}
