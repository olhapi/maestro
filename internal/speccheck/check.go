package speccheck

import (
	"os"
	"path/filepath"
)

type Report struct {
	OK     bool              `json:"ok"`
	Checks map[string]string `json:"checks"`
}

// Run performs lightweight local conformance checks against the Symphony spec areas.
func Run(repoRoot string) Report {
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	checks := map[string]string{}
	ok := true

	required := map[string]string{
		"workflow_loader":   "pkg/config/config.go",
		"orchestrator":      "internal/orchestrator/orchestrator.go",
		"workspace_runner":  "internal/agent/runner.go",
		"kanban_tracker":    "internal/kanban/store.go",
		"mcp_tools":         "internal/mcp/server.go",
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

	return Report{OK: ok, Checks: checks}
}
