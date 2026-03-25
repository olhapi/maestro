package speccheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/olhapi/maestro/internal/codexschema"
	"github.com/olhapi/maestro/pkg/config"
	"github.com/olhapi/maestro/skills"
)

type Report struct {
	OK     bool              `json:"ok"`
	Checks map[string]string `json:"checks"`
}

// Run performs semantic conformance checks against the Maestro spec areas.
func Run(repoRoot string) Report {
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}

	checks := map[string]string{}
	ok := true

	workflowPath := config.WorkflowPath(repoRoot)
	workflow, err := config.LoadWorkflow(workflowPath)
	if err != nil {
		ok = false
		checks["workflow_load"] = "fail"
		checks["workflow_version"] = "skipped"
		checks["workflow_prompt_render"] = "skipped"
	} else {
		checks["workflow_load"] = "ok"

		if workflow.Config.Codex.ExpectedVersion == codexschema.SupportedVersion {
			checks["workflow_version"] = "ok"
		} else {
			ok = false
			checks["workflow_version"] = "fail"
		}

		rendered, err := config.RenderLiquidTemplate(workflow.PromptTemplate, map[string]interface{}{
			"issue": map[string]interface{}{
				"identifier":  "ISS-1",
				"title":       "Spec check",
				"description": "Parses correctly",
				"state":       "ready",
			},
			"project": map[string]interface{}{
				"id":          "PRJ-1",
				"name":        "Spec check project",
				"description": "Follow repo-wide guidance",
			},
			"phase":   "implementation",
			"attempt": 1,
		})
		if err != nil {
			ok = false
			checks["workflow_prompt_render"] = "fail"
		} else if !strings.Contains(rendered, "ISS-1") || !strings.Contains(rendered, "Spec check") {
			ok = false
			checks["workflow_prompt_render"] = "fail"
		} else {
			checks["workflow_prompt_render"] = "ok"
		}
	}

	if err := validateDefaultConfig(); err != nil {
		ok = false
		checks["config_defaults"] = "fail"
	} else {
		checks["config_defaults"] = "ok"
	}

	if err := validateCodexSchemas(repoRoot); err != nil {
		ok = false
		checks["codex_schema_json"] = "fail"
	} else {
		checks["codex_schema_json"] = "ok"
	}

	if err := validateSkillInstall(); err != nil {
		ok = false
		checks["skill_install"] = "fail"
	} else {
		checks["skill_install"] = "ok"
	}

	return Report{OK: ok, Checks: checks}
}

func validateDefaultConfig() error {
	cfg := config.DefaultConfig()
	initCfg := config.DefaultInitConfig()

	if cfg.Tracker.Kind != config.TrackerKindKanban {
		return fmt.Errorf("default tracker kind = %q", cfg.Tracker.Kind)
	}
	if cfg.Codex.ExpectedVersion != codexschema.SupportedVersion {
		return fmt.Errorf("default expected_version = %q", cfg.Codex.ExpectedVersion)
	}
	if cfg.Codex.InitialCollaborationMode != config.InitialCollaborationModeDefault {
		return fmt.Errorf("default initial_collaboration_mode = %q", cfg.Codex.InitialCollaborationMode)
	}
	if cfg.Codex.TurnTimeoutMs != 1800000 || cfg.Codex.ReadTimeoutMs != 10000 || cfg.Codex.StallTimeoutMs != 300000 {
		return fmt.Errorf("unexpected codex timeout defaults: turn=%d read=%d stall=%d", cfg.Codex.TurnTimeoutMs, cfg.Codex.ReadTimeoutMs, cfg.Codex.StallTimeoutMs)
	}
	if initCfg.Codex.ExpectedVersion != codexschema.SupportedVersion {
		return fmt.Errorf("default init expected_version = %q", initCfg.Codex.ExpectedVersion)
	}
	if initCfg.Codex.InitialCollaborationMode != config.InitialCollaborationModeDefault {
		return fmt.Errorf("default init initial_collaboration_mode = %q", initCfg.Codex.InitialCollaborationMode)
	}
	if initCfg.Codex.ApprovalPolicy != "never" {
		return fmt.Errorf("default init approval_policy = %#v", initCfg.Codex.ApprovalPolicy)
	}
	if !isGranularApprovalPolicy(cfg.Codex.ApprovalPolicy) {
		return fmt.Errorf("default approval_policy has unexpected shape: %#v", cfg.Codex.ApprovalPolicy)
	}
	return nil
}

func isGranularApprovalPolicy(value interface{}) bool {
	root, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	granular, ok := root["granular"].(map[string]interface{})
	if !ok {
		return false
	}
	return granular["sandbox_approval"] == true &&
		granular["rules"] == true &&
		granular["mcp_elicitations"] == true &&
		granular["request_permissions"] == false
}

func validateCodexSchemas(repoRoot string) error {
	schemaDir := codexschema.SchemaDir(repoRoot)
	for _, rel := range codexschema.ConsumedSchemaFiles {
		path := filepath.Join(schemaDir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read schema %s: %w", rel, err)
		}
		var parsed any
		if err := json.Unmarshal(data, &parsed); err != nil {
			return fmt.Errorf("parse schema %s: %w", rel, err)
		}
	}
	return nil
}

func validateSkillInstall() error {
	tmpDir, err := os.MkdirTemp("", "maestro-speccheck-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	dest := filepath.Join(tmpDir, "skills", "maestro")
	if err := skills.InstallMaestro(dest); err != nil {
		return err
	}

	wantPaths, err := skills.BundledPaths()
	if err != nil {
		return err
	}
	sort.Strings(wantPaths)

	gotPaths, err := installedSkillPaths(dest)
	if err != nil {
		return err
	}
	sort.Strings(gotPaths)

	if len(gotPaths) != len(wantPaths) {
		return fmt.Errorf("installed skill file count = %d, want %d", len(gotPaths), len(wantPaths))
	}
	for i := range wantPaths {
		if wantPaths[i] != gotPaths[i] {
			return fmt.Errorf("installed skill path mismatch at %d: got %q want %q", i, gotPaths[i], wantPaths[i])
		}
	}

	for _, rel := range wantPaths {
		want, err := skills.ReadBundledFile(rel)
		if err != nil {
			return fmt.Errorf("read bundled skill file %s: %w", rel, err)
		}
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("read installed skill file %s: %w", rel, err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("installed skill file %s does not match bundled content", rel)
		}
	}

	return nil
}

func installedSkillPaths(root string) ([]string, error) {
	var paths []string
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return nil, err
	}
	return paths, nil
}
