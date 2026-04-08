package speccheck

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
	"github.com/olhapi/maestro/pkg/config"
)

func tempRepoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	schemaDir := codexschema.SchemaDir(repoRoot)
	for _, rel := range codexschema.ConsumedSchemaFiles {
		src := filepath.Join(schemaDir, rel)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read schema %s: %v", src, err)
		}
		dst := filepath.Join(root, "schemas", "codex", codexschema.SupportedVersion, "json", rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("mkdir schema dir: %v", err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write schema %s: %v", dst, err)
		}
	}
	return root
}

func TestWorkflowPromptRenderAndGranularApprovalHelpers(t *testing.T) {
	if err := validateWorkflowPromptRender("Hello {{ issue.identifier }}"); err != nil {
		t.Fatalf("validateWorkflowPromptRender: %v", err)
	}
	if err := validateWorkflowPromptRender("{{"); err == nil {
		t.Fatal("expected malformed template to fail")
	}
	if !isGranularApprovalPolicy(map[string]interface{}{
		"granular": map[string]interface{}{
			"mcp_elicitations":    true,
			"rules":               true,
			"sandbox_approval":    true,
			"request_permissions": false,
		},
	}) {
		t.Fatal("expected granular approval policy to be recognized")
	}
	for _, candidate := range []interface{}{
		map[string]interface{}{"granular": "bad"},
		map[string]interface{}{},
		[]interface{}{},
	} {
		if isGranularApprovalPolicy(candidate) {
			t.Fatalf("expected candidate to fail granular approval detection: %#v", candidate)
		}
	}
	if isGranularApprovalPolicy(map[string]interface{}{
		"granular": map[string]interface{}{
			"mcp_elicitations":    true,
			"rules":               true,
			"sandbox_approval":    false,
			"request_permissions": false,
		},
	}) {
		t.Fatal("expected malformed granular approval policy to fail")
	}
}

func TestInstalledSkillPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	paths, err := installedSkillPaths(root)
	if err != nil {
		t.Fatalf("installedSkillPaths: %v", err)
	}
	if len(paths) != 2 || paths[0] != "a.txt" || paths[1] != "nested/b.txt" {
		t.Fatalf("unexpected installed skill paths: %#v", paths)
	}

	if _, err := installedSkillPaths(filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected installedSkillPaths to fail for a missing root")
	}
}

func writeFakeCodexAndNpx(t *testing.T, root, codexVersion string) {
	t.Helper()

	codexDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll codex dir: %v", err)
	}
	fakeCodex := filepath.Join(codexDir, "codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nprintf 'codex-cli "+codexVersion+"\\n'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile fake codex: %v", err)
	}
	fakeNpx := filepath.Join(codexDir, "npx")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" != \"-y\" ]; then\n" +
		"  echo \"unexpected npx args: $*\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"shift\n" +
		"if [ \"$1\" = \"@openai/codex@" + codexschema.SupportedVersion + "\" ]; then\n" +
		"  printf 'codex-cli " + codexschema.SupportedVersion + "\\n'\n" +
		"else\n" +
		"  printf 'codex-cli " + codexVersion + "\\n'\n" +
		"fi\n"
	if err := os.WriteFile(fakeNpx, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fake npx: %v", err)
	}
	t.Setenv("PATH", codexDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRunReportsCommandResolution(t *testing.T) {
	root := tempRepoRoot(t)
	workflow := `---
tracker:
  kind: kanban
codex:
  command: codex app-server
  approval_policy: never
  initial_collaboration_mode: default
---
Hello {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(root, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}
	writeFakeCodexAndNpx(t, root, "1.2.3")

	report := Run(root)
	if !report.OK {
		t.Fatalf("expected version mismatch to resolve, got %+v", report)
	}
	if report.Checks["workflow_load"] != "ok" {
		t.Fatalf("expected workflow to load, got %+v", report.Checks)
	}
	if report.Checks["workflow_version"] != "ok" {
		t.Fatalf("expected workflow_version to resolve through pinned npx, got %+v", report.Checks)
	}
	if report.Checks["workflow_prompt_render"] != "ok" {
		t.Fatalf("expected prompt render to succeed, got %+v", report.Checks)
	}
	if report.Checks["skill_install"] != "ok" {
		t.Fatalf("expected skill install to remain ok, got %+v", report.Checks)
	}
}

func TestRunReportsWorkflowLoadFailure(t *testing.T) {
	root := tempRepoRoot(t)
	fakeCodexDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeCodexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll codex dir: %v", err)
	}
	fakeCodex := filepath.Join(fakeCodexDir, "codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nprintf 'codex-cli 1.2.3\\n'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile fake codex: %v", err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeCodexDir+string(os.PathListSeparator)+oldPath)

	report := Run(root)
	if report.OK {
		t.Fatalf("expected missing workflow to fail, got %+v", report)
	}
	if report.Checks["workflow_load"] != "fail" {
		t.Fatalf("expected workflow_load to fail, got %+v", report.Checks)
	}
	if report.Checks["workflow_version"] != "skipped" || report.Checks["workflow_prompt_render"] != "skipped" {
		t.Fatalf("expected workflow checks to be skipped, got %+v", report.Checks)
	}
}

func TestRunReportsSuccess(t *testing.T) {
	root := tempRepoRoot(t)
	workflow := `---
tracker:
  kind: kanban
codex:
  command: npx -y @openai/codex@` + codexschema.SupportedVersion + ` app-server
  approval_policy: never
  initial_collaboration_mode: default
---
Hello {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(root, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}
	writeFakeCodexAndNpx(t, root, "1.2.3")

	report := Run(root)
	if !report.OK {
		t.Fatalf("expected success, got %+v", report.Checks)
	}
	for _, key := range []string{"workflow_load", "workflow_version", "workflow_prompt_render", "workflow_advisories", "config_defaults", "codex_schema_json", "skill_install"} {
		if report.Checks[key] != "ok" {
			t.Fatalf("expected %s to be ok, got %+v", key, report.Checks)
		}
	}
}

func TestRunReportsConfigDefaultsFailure(t *testing.T) {
	root := tempRepoRoot(t)
	workflow := `---
tracker:
  kind: kanban
codex:
  command: cat
  approval_policy: never
  initial_collaboration_mode: default
---
Hello {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(root, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}

	origDefaultConfig := defaultConfigFunc
	t.Cleanup(func() {
		defaultConfigFunc = origDefaultConfig
	})
	defaultConfigFunc = func() config.Config {
		cfg := config.DefaultConfig()
		cfg.Tracker.Kind = "other"
		return cfg
	}

	report := Run(root)
	if report.OK {
		t.Fatalf("expected config defaults failure, got %+v", report)
	}
	if report.Checks["config_defaults"] != "fail" {
		t.Fatalf("expected config_defaults to fail, got %+v", report.Checks)
	}
}

func TestRunReportsCodexSchemaFailure(t *testing.T) {
	root := t.TempDir()
	workflow := `---
tracker:
  kind: kanban
codex:
  command: cat
  approval_policy: never
  initial_collaboration_mode: default
---
Hello {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(root, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}

	report := Run(root)
	if report.OK {
		t.Fatalf("expected schema failure, got %+v", report)
	}
	if report.Checks["codex_schema_json"] != "fail" {
		t.Fatalf("expected codex_schema_json to fail, got %+v", report.Checks)
	}
}

func TestRunReportsSkillInstallFailure(t *testing.T) {
	root := tempRepoRoot(t)
	workflow := `---
tracker:
  kind: kanban
codex:
  command: cat
  approval_policy: never
  initial_collaboration_mode: default
---
Hello {{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(root, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}

	origInstall := installMaestroFunc
	t.Cleanup(func() {
		installMaestroFunc = origInstall
	})
	installMaestroFunc = func(string) error {
		return errors.New("install failed")
	}

	report := Run(root)
	if report.OK {
		t.Fatalf("expected skill install failure, got %+v", report)
	}
	if report.Checks["skill_install"] != "fail" {
		t.Fatalf("expected skill_install to fail, got %+v", report.Checks)
	}
}

func TestValidateWorkflowPromptRenderAllowsJSONRoundTrip(t *testing.T) {
	prompt := "Issue {{ issue.identifier }} on project {{ project.name }}"
	if err := validateWorkflowPromptRender(prompt); err != nil {
		t.Fatalf("validateWorkflowPromptRender: %v", err)
	}
	payload, err := json.Marshal(sampleWorkflowPromptContext())
	if err != nil || len(payload) == 0 {
		t.Fatalf("unexpected sample workflow prompt context marshal: %v %q", err, string(payload))
	}
}

func TestValidateDefaultConfigBranches(t *testing.T) {
	origDefaultConfig := defaultConfigFunc
	origDefaultInitConfig := defaultInitConfigFunc
	t.Cleanup(func() {
		defaultConfigFunc = origDefaultConfig
		defaultInitConfigFunc = origDefaultInitConfig
	})

	tests := []struct {
		name string
		cfg  func() config.Config
		init func() config.Config
	}{
		{
			name: "tracker kind",
			cfg: func() config.Config {
				cfg := config.DefaultConfig()
				cfg.Tracker.Kind = "other"
				return cfg
			},
		},
		{
			name: "init approval policy",
			init: func() config.Config {
				cfg := config.DefaultInitConfig()
				cfg.Codex.ApprovalPolicy = "maybe"
				return cfg
			},
		},
		{
			name: "timeout defaults",
			cfg: func() config.Config {
				cfg := config.DefaultConfig()
				cfg.Codex.TurnTimeoutMs = 42
				return cfg
			},
		},
		{
			name: "read timeout",
			cfg: func() config.Config {
				cfg := config.DefaultConfig()
				cfg.Codex.ReadTimeoutMs = 42
				return cfg
			},
		},
		{
			name: "stall timeout",
			cfg: func() config.Config {
				cfg := config.DefaultConfig()
				cfg.Codex.StallTimeoutMs = 42
				return cfg
			},
		},
		{
			name: "init collaboration mode",
			init: func() config.Config {
				cfg := config.DefaultInitConfig()
				cfg.Codex.InitialCollaborationMode = "manual"
				return cfg
			},
		},
		{
			name: "granular approval",
			cfg: func() config.Config {
				cfg := config.DefaultConfig()
				cfg.Codex.ApprovalPolicy = map[string]interface{}{"granular": "bad"}
				return cfg
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cfg != nil {
				defaultConfigFunc = tc.cfg
			} else {
				defaultConfigFunc = config.DefaultConfig
			}
			if tc.init != nil {
				defaultInitConfigFunc = tc.init
			} else {
				defaultInitConfigFunc = config.DefaultInitConfig
			}
			if err := validateDefaultConfig(); err == nil {
				t.Fatal("expected validateDefaultConfig to fail")
			}
		})
	}
}

func TestValidateSkillInstallBranches(t *testing.T) {
	origInstall := installMaestroFunc
	origBundledPaths := bundledPathsFunc
	origReadBundledFile := readBundledFileFunc
	t.Cleanup(func() {
		installMaestroFunc = origInstall
		bundledPathsFunc = origBundledPaths
		readBundledFileFunc = origReadBundledFile
	})

	writeInstalledBundle := func(dest string, files map[string]string) error {
		for rel, content := range files {
			path := filepath.Join(dest, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
		}
		return nil
	}

	t.Run("install error", func(t *testing.T) {
		installMaestroFunc = func(string) error { return errors.New("install failed") }
		bundledPathsFunc = func() ([]string, error) { return []string{"SKILL.md"}, nil }
		readBundledFileFunc = func(string) ([]byte, error) { return []byte("ok"), nil }
		if err := validateSkillInstall(); err == nil {
			t.Fatal("expected install error")
		}
	})

	t.Run("bundled paths error", func(t *testing.T) {
		installMaestroFunc = func(dest string) error {
			return writeInstalledBundle(dest, map[string]string{"SKILL.md": "same"})
		}
		bundledPathsFunc = func() ([]string, error) { return nil, errors.New("paths failed") }
		readBundledFileFunc = func(string) ([]byte, error) { return []byte("ok"), nil }
		if err := validateSkillInstall(); err == nil {
			t.Fatal("expected bundled paths error")
		}
	})

	t.Run("read bundled file error", func(t *testing.T) {
		installMaestroFunc = func(dest string) error {
			return writeInstalledBundle(dest, map[string]string{"SKILL.md": "same"})
		}
		bundledPathsFunc = func() ([]string, error) { return []string{"SKILL.md"}, nil }
		readBundledFileFunc = func(string) ([]byte, error) { return nil, errors.New("read failed") }
		if err := validateSkillInstall(); err == nil {
			t.Fatal("expected bundled file read error")
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		installMaestroFunc = func(dest string) error {
			return writeInstalledBundle(dest, map[string]string{"SKILL.md": "installed"})
		}
		bundledPathsFunc = func() ([]string, error) { return []string{"SKILL.md"}, nil }
		readBundledFileFunc = func(string) ([]byte, error) { return []byte("bundled"), nil }
		if err := validateSkillInstall(); err == nil {
			t.Fatal("expected bundled file mismatch")
		}
	})

	t.Run("count mismatch", func(t *testing.T) {
		installMaestroFunc = func(dest string) error {
			return writeInstalledBundle(dest, map[string]string{
				"SKILL.md": "same",
			})
		}
		bundledPathsFunc = func() ([]string, error) { return []string{"SKILL.md", "references/setup.md"}, nil }
		readBundledFileFunc = func(string) ([]byte, error) { return []byte("same"), nil }
		if err := validateSkillInstall(); err == nil {
			t.Fatal("expected bundled file count mismatch")
		}
	})

	t.Run("path mismatch", func(t *testing.T) {
		installMaestroFunc = func(dest string) error {
			return writeInstalledBundle(dest, map[string]string{"SKILL.md": "same"})
		}
		bundledPathsFunc = func() ([]string, error) { return []string{"README.md"}, nil }
		readBundledFileFunc = func(string) ([]byte, error) { return []byte("same"), nil }
		if err := validateSkillInstall(); err == nil {
			t.Fatal("expected bundled file path mismatch")
		}
	})
}
