package speccheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRun(t *testing.T) {
	tmpDir := t.TempDir()
	for _, rel := range []string{
		"pkg/config/config.go",
		"pkg/config/manager.go",
		"pkg/config/init.go",
		"internal/orchestrator/orchestrator.go",
		"internal/agent/runner.go",
		"internal/kanban/store.go",
		"internal/mcp/server.go",
		"internal/observability/server.go",
	} {
		path := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package placeholder\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	workflow := `---
tracker:
  kind: kanban
---
{{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}

	r := Run(tmpDir)
	if !r.OK {
		t.Fatalf("expected spec check OK, got %+v", r)
	}
	if r.Checks["workflow_prompt_render"] != "ok" {
		t.Fatalf("workflow prompt render missing: %+v", r)
	}
}
