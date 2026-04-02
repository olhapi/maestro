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

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/codexschema"
	"github.com/olhapi/maestro/pkg/config"
	"github.com/olhapi/maestro/skills"
)

type Report struct {
	OK     bool              `json:"ok"`
	Checks map[string]string `json:"checks"`
}

var (
	defaultConfigFunc     = config.DefaultConfig
	defaultInitConfigFunc = config.DefaultInitConfig
	installMaestroFunc    = skills.InstallMaestro
	bundledPathsFunc      = skills.BundledPaths
	readBundledFileFunc   = skills.ReadBundledFile
)

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

		selectedName := strings.TrimSpace(workflow.Config.Runtime.Default)
		selectedRuntime, found := workflow.Config.Runtime.RuntimeByName(selectedName)
		if found && selectedRuntime.ExpectedVersion == codexschema.SupportedVersion {
			checks["workflow_version"] = "ok"
		} else {
			ok = false
			checks["workflow_version"] = "fail"
		}

		if err := validateWorkflowPromptRender(workflow.PromptTemplate); err != nil {
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

	if err := validateRuntimeSchemas(repoRoot); err != nil {
		ok = false
		checks["runtime_schema_json"] = "fail"
	} else {
		checks["runtime_schema_json"] = "ok"
	}

	if err := validateSkillInstall(); err != nil {
		ok = false
		checks["skill_install"] = "fail"
	} else {
		checks["skill_install"] = "ok"
	}

	return Report{OK: ok, Checks: checks}
}

func validateWorkflowPromptRender(prompt string) error {
	_, err := config.RenderLiquidTemplate(prompt, sampleWorkflowPromptContext())
	return err
}

func sampleWorkflowPromptContext() map[string]interface{} {
	return map[string]interface{}{
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
		"phase":     "implementation",
		"attempt":   1,
		"plan_mode": false,
	}
}

func validateDefaultConfig() error {
	cfg := defaultConfigFunc()
	initCfg := defaultInitConfigFunc()
	selectedName := strings.TrimSpace(cfg.Runtime.Default)
	selectedRuntime, ok := cfg.Runtime.RuntimeByName(selectedName)
	if selectedName == "" {
		ok = false
	}
	initSelectedName := strings.TrimSpace(initCfg.Runtime.Default)
	initSelectedRuntime, initOK := initCfg.Runtime.RuntimeByName(initSelectedName)
	if initSelectedName == "" {
		initOK = false
	}

	if cfg.Tracker.Kind != config.TrackerKindKanban {
		return fmt.Errorf("default tracker kind = %q", cfg.Tracker.Kind)
	}
	if cfg.Workspace.BranchPrefix != "maestro/" {
		return fmt.Errorf("default branch prefix = %q", cfg.Workspace.BranchPrefix)
	}
	if selectedName != "codex-appserver" || !ok {
		return fmt.Errorf("default runtime.default = %q", cfg.Runtime.Default)
	}
	if selectedRuntime.Provider != "codex" || selectedRuntime.Transport != string(agentruntime.TransportAppServer) || selectedRuntime.Command == "" {
		return fmt.Errorf("default codex-appserver runtime = %#v", selectedRuntime)
	}
	if selectedRuntime.ExpectedVersion != codexschema.SupportedVersion {
		return fmt.Errorf("default expected_version = %q", selectedRuntime.ExpectedVersion)
	}
	codexStdio, ok := cfg.Runtime.RuntimeByName("codex-stdio")
	if !ok {
		return fmt.Errorf("default runtime catalog missing codex-stdio")
	}
	if codexStdio.Provider != "codex" || codexStdio.Transport != string(agentruntime.TransportStdio) {
		return fmt.Errorf("default codex-stdio runtime = %#v", codexStdio)
	}
	claudeRuntime, ok := cfg.Runtime.RuntimeByName("claude")
	if !ok {
		return fmt.Errorf("default runtime catalog missing claude")
	}
	if claudeRuntime.Provider != "claude" || claudeRuntime.Transport != string(agentruntime.TransportStdio) {
		return fmt.Errorf("default claude runtime = %#v", claudeRuntime)
	}
	if selectedRuntime.InitialCollaborationMode != config.InitialCollaborationModeDefault {
		return fmt.Errorf("default initial_collaboration_mode = %q", selectedRuntime.InitialCollaborationMode)
	}
	if selectedRuntime.TurnTimeoutMs != 1800000 || selectedRuntime.ReadTimeoutMs != 10000 || selectedRuntime.StallTimeoutMs != 300000 {
		return fmt.Errorf("unexpected runtime timeout defaults: turn=%d read=%d stall=%d", selectedRuntime.TurnTimeoutMs, selectedRuntime.ReadTimeoutMs, selectedRuntime.StallTimeoutMs)
	}
	if initSelectedName != "codex-appserver" || !initOK {
		return fmt.Errorf("default init runtime.default = %q", initCfg.Runtime.Default)
	}
	if initSelectedRuntime.ApprovalPolicy != "never" {
		return fmt.Errorf("default init approval_policy = %#v", initSelectedRuntime.ApprovalPolicy)
	}
	if initSelectedRuntime.ExpectedVersion != codexschema.SupportedVersion {
		return fmt.Errorf("default init expected_version = %q", initSelectedRuntime.ExpectedVersion)
	}
	if initSelectedRuntime.InitialCollaborationMode != config.InitialCollaborationModeDefault {
		return fmt.Errorf("default init initial_collaboration_mode = %q", initSelectedRuntime.InitialCollaborationMode)
	}
	if !isGranularApprovalPolicy(selectedRuntime.ApprovalPolicy) {
		return fmt.Errorf("default approval_policy has unexpected shape: %#v", selectedRuntime.ApprovalPolicy)
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

func validateRuntimeSchemas(repoRoot string) error {
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
	if err := installMaestroFunc(dest); err != nil {
		return err
	}

	wantPaths, err := bundledPathsFunc()
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
		want, err := readBundledFileFunc(rel)
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
