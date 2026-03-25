package speccheck

import (
	"os"
	"path/filepath"
	runtimepkg "runtime"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
)

func repoRootFromCaller(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtimepkg.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestRunAgainstRepoRoot(t *testing.T) {
	report := Run(repoRootFromCaller(t))
	if !report.OK {
		t.Fatalf("expected spec check to pass, got %+v", report)
	}
	for _, key := range []string{
		"workflow_load",
		"workflow_version",
		"workflow_prompt_render",
		"config_defaults",
		"codex_schema_json",
		"skill_install",
	} {
		if report.Checks[key] != "ok" {
			t.Fatalf("expected %s to be ok, got %+v", key, report.Checks)
		}
	}
}

func TestValidateWorkflowPromptRenderAllowsCustomPrompts(t *testing.T) {
	prompt := "Run the custom workflow without relying on the sample issue fields."
	if err := validateWorkflowPromptRender(prompt); err != nil {
		t.Fatalf("validateWorkflowPromptRender: %v", err)
	}
}

func TestRunRejectsBadSemanticRepo(t *testing.T) {
	tmp := t.TempDir()
	workflow := `---
- not-a-map
---
Issue {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(tmp, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}

	schemaPath := filepath.Join(tmp, "schemas", "codex", codexschema.SupportedVersion, "json", "v1", "InitializeParams.json")
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath, []byte(`{not-json}`), 0o644); err != nil {
		t.Fatal(err)
	}

	report := Run(tmp)
	if report.OK {
		t.Fatalf("expected spec check to fail, got %+v", report)
	}
	if report.Checks["workflow_load"] != "fail" {
		t.Fatalf("expected workflow_load to fail, got %+v", report.Checks)
	}
	if report.Checks["workflow_version"] != "skipped" {
		t.Fatalf("expected workflow_version to be skipped, got %+v", report.Checks)
	}
	if report.Checks["workflow_prompt_render"] != "skipped" {
		t.Fatalf("expected workflow_prompt_render to be skipped, got %+v", report.Checks)
	}
	if report.Checks["codex_schema_json"] != "fail" {
		t.Fatalf("expected codex_schema_json to fail, got %+v", report.Checks)
	}
	if report.Checks["config_defaults"] != "ok" {
		t.Fatalf("expected config_defaults to remain ok, got %+v", report.Checks)
	}
	if report.Checks["skill_install"] != "ok" {
		t.Fatalf("expected skill_install to remain ok, got %+v", report.Checks)
	}
}
