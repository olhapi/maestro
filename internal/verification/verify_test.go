package verification

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
	"github.com/olhapi/maestro/pkg/config"
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

func writeFakeCLI(t *testing.T, root, binary, version string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, binary)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '"+binary+"-cli "+version+"\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", root+string(os.PathListSeparator)+oldPath)
	return path
}

func writeFakeCodex(t *testing.T, root, version string) string {
	t.Helper()
	return writeFakeCLI(t, filepath.Join(root, "bin"), "codex", version)
}

func writeFakeClaude(t *testing.T, root, version string) string {
	t.Helper()
	return writeFakeCLI(t, filepath.Join(root, "bin"), "claude", version)
}

func writeRuntimeScript(t *testing.T, root, binary, script string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, binary)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", root+string(os.PathListSeparator)+oldPath)
	return path
}

func resetRuntimeVersionCache(t *testing.T) {
	t.Helper()
	runtimeVersionCache = sync.Map{}
	t.Cleanup(func() {
		runtimeVersionCache = sync.Map{}
	})
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
	writeFakeClaude(t, tmp, "1.2.3")
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
	for _, key := range []string{
		"runtime_catalog",
		"runtime_default",
		"runtime_codex_appserver",
		"runtime_codex_appserver_binary",
		"runtime_codex_appserver_version",
		"runtime_codex_stdio",
		"runtime_codex_stdio_binary",
		"runtime_codex_stdio_version",
		"runtime_claude",
		"runtime_claude_binary",
		"runtime_claude_version",
	} {
		if res.Checks[key] == "" {
			t.Fatalf("expected %s to be populated, got %+v", key, res.Checks)
		}
	}
	if res.Checks["runtime_default"] != "ok" || res.Checks["runtime_claude"] != "ok" {
		t.Fatalf("expected runtime readiness to report ok, got %+v", res.Checks)
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
	writeFakeClaude(t, tmp, "1.2.3")
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflowFixture(codexschema.SupportedVersion)), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Run(tmp, filepath.Join(tmp, "db", "maestro.db"))
	if res.Checks["runtime_default"] != "warn" {
		t.Fatalf("expected default runtime warning, got %+v", res)
	}
	if res.Checks["runtime_codex_appserver_version"] != "warn" || res.Checks["runtime_codex_stdio_version"] != "warn" {
		t.Fatalf("expected codex warning, got %+v", res)
	}
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), "runtime_codex_appserver_version: expected "+codexschema.SupportedVersion+", found 9.9.9") {
		t.Fatalf("unexpected warnings: %+v", res.Warnings)
	}
	if res.Checks["runtime_claude"] != "ok" {
		t.Fatalf("expected claude runtime to stay ready, got %+v", res.Checks)
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

func TestValidateRuntimeReadinessReportsMissingDefaultRuntime(t *testing.T) {
	resetRuntimeVersionCache(t)
	root := t.TempDir()
	writeRuntimeScript(t, root, "codex", `printf 'codex-cli `+codexschema.SupportedVersion+`\n'`)
	res := &Result{Checks: map[string]string{}, Remediation: map[string]string{}}
	workflow := &config.Workflow{
		Config: config.Config{
			Runtime: config.RuntimeCatalog{
				Default: "claude",
				Entries: map[string]config.RuntimeConfig{
					"codex-appserver": {
						Provider:        "codex",
						Transport:       "app_server",
						Command:         "codex app-server",
						ExpectedVersion: codexschema.SupportedVersion,
					},
				},
			},
		},
	}

	validateRuntimeReadiness(res, workflow)

	if res.Checks["runtime_catalog"] != "ok" {
		t.Fatalf("expected runtime catalog to be ok, got %+v", res.Checks)
	}
	if res.Checks["runtime_default"] != "warn" {
		t.Fatalf("expected missing default runtime warning, got %+v", res.Checks)
	}
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), `runtime_default: missing runtime entry "claude"`) {
		t.Fatalf("unexpected warnings: %+v", res.Warnings)
	}
	if got := res.Remediation["runtime_default"]; got != "Regenerate WORKFLOW.md with `maestro workflow init`." {
		t.Fatalf("unexpected default remediation: %q", got)
	}
}

func TestCheckRuntimeReadinessCoversCommandBranches(t *testing.T) {
	resetRuntimeVersionCache(t)
	root := t.TempDir()
	writeRuntimeScript(t, root, "badver", `case "$1" in
--version) exit 1 ;;
*) printf 'badver-cli 1.2.3\n' ;;
esac`)
	writeRuntimeScript(t, root, "noversion", `case "$1" in
--version) printf 'noversion\n' ;;
*) printf 'noversion\n' ;;
esac`)
	writeRuntimeScript(t, root, "goodver", `printf 'goodver-cli 1.2.3\n'`)

	t.Run("empty default command", func(t *testing.T) {
		res := &Result{Checks: map[string]string{}, Remediation: map[string]string{}}
		checkRuntimeReadiness(res, config.RuntimeConfig{Provider: "codex", Transport: "app_server", Command: "", ExpectedVersion: codexschema.SupportedVersion}, "empty-default", true)

		if res.Checks["runtime_empty_default"] != "warn" || res.Checks["runtime_empty_default_binary"] != "warn" || res.Checks["runtime_empty_default_version"] != "skipped" {
			t.Fatalf("unexpected empty default readiness: %+v", res.Checks)
		}
		if res.Checks["runtime_default"] != "warn" {
			t.Fatalf("expected default warning, got %+v", res.Checks)
		}
	})

	t.Run("empty optional command", func(t *testing.T) {
		res := &Result{Checks: map[string]string{}, Remediation: map[string]string{}}
		checkRuntimeReadiness(res, config.RuntimeConfig{Provider: "claude", Transport: "stdio", Command: ""}, "empty-optional", false)

		if res.Checks["runtime_empty_optional"] != "skipped" || res.Checks["runtime_empty_optional_binary"] != "skipped" || res.Checks["runtime_empty_optional_version"] != "skipped" {
			t.Fatalf("unexpected empty optional readiness: %+v", res.Checks)
		}
	})

	t.Run("missing binary with expected version", func(t *testing.T) {
		res := &Result{Checks: map[string]string{}, Remediation: map[string]string{}}
		checkRuntimeReadiness(res, config.RuntimeConfig{Provider: "codex", Transport: "stdio", Command: "missing-binary", ExpectedVersion: codexschema.SupportedVersion}, "missing-binary", false)

		if res.Checks["runtime_missing_binary"] != "warn" || res.Checks["runtime_missing_binary_binary"] != "warn" || res.Checks["runtime_missing_binary_version"] != "skipped" {
			t.Fatalf("unexpected missing binary readiness: %+v", res.Checks)
		}
		if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), `runtime_missing_binary_binary: unable to locate executable for "missing-binary"`) {
			t.Fatalf("unexpected missing binary warnings: %+v", res.Warnings)
		}
	})

	t.Run("missing binary without expected version", func(t *testing.T) {
		res := &Result{Checks: map[string]string{}, Remediation: map[string]string{}}
		checkRuntimeReadiness(res, config.RuntimeConfig{Provider: "claude", Transport: "stdio", Command: "missing-optional"}, "missing-optional", false)

		if res.Checks["runtime_missing_optional"] != "skipped" || res.Checks["runtime_missing_optional_binary"] != "skipped" || res.Checks["runtime_missing_optional_version"] != "skipped" {
			t.Fatalf("unexpected missing optional readiness: %+v", res.Checks)
		}
	})

	t.Run("version command failure without expected version", func(t *testing.T) {
		res := &Result{Checks: map[string]string{}, Remediation: map[string]string{}}
		checkRuntimeReadiness(res, config.RuntimeConfig{Provider: "codex", Transport: "stdio", Command: "badver", ExpectedVersion: ""}, "badver", false)

		if res.Checks["runtime_badver"] != "ok" || res.Checks["runtime_badver_binary"] != "ok" || res.Checks["runtime_badver_version"] != "skipped" {
			t.Fatalf("unexpected badver readiness: %+v", res.Checks)
		}
	})

	t.Run("version parse failure with expected version", func(t *testing.T) {
		res := &Result{Checks: map[string]string{}, Remediation: map[string]string{}}
		checkRuntimeReadiness(res, config.RuntimeConfig{Provider: "codex", Transport: "stdio", Command: "noversion", ExpectedVersion: codexschema.SupportedVersion}, "noversion", false)

		if res.Checks["runtime_noversion"] != "warn" || res.Checks["runtime_noversion_binary"] != "ok" || res.Checks["runtime_noversion_version"] != "warn" {
			t.Fatalf("unexpected noversion readiness: %+v", res.Checks)
		}
		if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), "runtime_noversion_version: unable to parse runtime version") {
			t.Fatalf("unexpected noversion warnings: %+v", res.Warnings)
		}
	})

	t.Run("version match and cache hit", func(t *testing.T) {
		res := &Result{Checks: map[string]string{}, Remediation: map[string]string{}}
		checkRuntimeReadiness(res, config.RuntimeConfig{Provider: "codex", Transport: "stdio", Command: "goodver", ExpectedVersion: "1.2.3"}, "goodver", false)
		checkRuntimeReadiness(res, config.RuntimeConfig{Provider: "codex", Transport: "stdio", Command: "goodver", ExpectedVersion: "9.9.9"}, "goodver-mismatch", false)

		if res.Checks["runtime_goodver"] != "ok" || res.Checks["runtime_goodver_version"] != "ok" {
			t.Fatalf("unexpected goodver readiness: %+v", res.Checks)
		}
		if res.Checks["runtime_goodver_mismatch"] != "warn" || res.Checks["runtime_goodver_mismatch_version"] != "warn" {
			t.Fatalf("unexpected cached mismatch readiness: %+v", res.Checks)
		}
	})
}

func TestRuntimeHelperParsing(t *testing.T) {
	resetRuntimeVersionCache(t)
	root := t.TempDir()
	writeRuntimeScript(t, root, "cached", `printf 'cached-cli 1.2.3\n'`)

	if got := runtimeExecutableFromCommand("  cached app-server --flag  "); got != "cached" {
		t.Fatalf("unexpected runtime executable: %q", got)
	}
	if got := runtimeExecutableFromCommand("   "); got != "" {
		t.Fatalf("expected empty executable for whitespace, got %q", got)
	}
	if got := parseRuntimeVersion([]byte("cached-cli 1.2.3")); got != "1.2.3" {
		t.Fatalf("unexpected parsed version: %q", got)
	}
	if got := parseRuntimeVersion([]byte("cached-cli latest")); got != "" {
		t.Fatalf("expected unparsable output to return empty version, got %q", got)
	}

	first, err := detectRuntimeVersion("cached app-server")
	if err != nil {
		t.Fatalf("first detectRuntimeVersion: %v", err)
	}
	second, err := detectRuntimeVersion("cached app-server")
	if err != nil {
		t.Fatalf("second detectRuntimeVersion: %v", err)
	}
	if first.ExecutablePath == "" || first.Actual != "1.2.3" {
		t.Fatalf("unexpected first detection: %+v", first)
	}
	if second.Actual != first.Actual || second.ExecutablePath != first.ExecutablePath {
		t.Fatalf("unexpected cached detection: first=%+v second=%+v", first, second)
	}
}
