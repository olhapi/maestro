package speccheck

import (
	"os"
	"path/filepath"

	"github.com/olhapi/maestro/pkg/config"
)

type Report struct {
	OK     bool              `json:"ok"`
	Checks map[string]string `json:"checks"`
}

// Run performs lightweight local conformance checks against the Maestro spec areas.
func Run(repoRoot string) Report {
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	checks := map[string]string{}
	ok := true

	required := map[string]string{
		"workflow_loader":    "pkg/config/config.go",
		"workflow_manager":   "pkg/config/manager.go",
		"workflow_init":      "pkg/config/init.go",
		"orchestrator":       "internal/orchestrator/orchestrator.go",
		"workspace_runner":   "internal/agent/runner.go",
		"kanban_tracker":     "internal/kanban/store.go",
		"mcp_tools":          "internal/mcp/server.go",
		"observability_http": "internal/observability/server.go",
	}

	for name, rel := range required {
		if _, err := os.Stat(filepath.Join(repoRoot, rel)); err != nil {
			checks[name] = "missing"
			ok = false
		} else {
			checks[name] = "ok"
		}
	}

	workflowPath := config.WorkflowPath(repoRoot)
	if _, err := os.Stat(workflowPath); err != nil {
		checks["workflow_file"] = "missing"
		ok = false
	} else if workflow, err := config.LoadWorkflow(workflowPath); err != nil {
		checks["workflow_file"] = "invalid"
		ok = false
	} else {
		checks["workflow_file"] = "ok"
		if workflow.Config.Tracker.Kind == config.TrackerKindKanban {
			checks["workflow_tracker_kind"] = "ok"
		} else {
			checks["workflow_tracker_kind"] = "invalid"
			ok = false
		}
		if _, err := config.RenderLiquidTemplate(workflow.PromptTemplate, map[string]interface{}{
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
		}); err != nil {
			checks["workflow_prompt_render"] = "invalid"
			ok = false
		} else {
			checks["workflow_prompt_render"] = "ok"
		}
	}

	return Report{OK: ok, Checks: checks}
}
