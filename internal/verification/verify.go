package verification

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/pkg/config"
)

type Result struct {
	OK          bool              `json:"ok"`
	Checks      map[string]string `json:"checks"`
	Errors      []string          `json:"errors,omitempty"`
	Remediation map[string]string `json:"remediation"`
}

func Run(repoPath, dbPath string) Result {
	res := Result{OK: true, Checks: map[string]string{}, Remediation: map[string]string{}}

	if repoPath == "" {
		repoPath, _ = os.Getwd()
	}
	if dbPath == "" {
		dbPath = filepath.Join(repoPath, ".maestro", "maestro.db")
	}

	if _, created, err := config.EnsureWorkflow(repoPath, config.InitOptions{}); err != nil {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("workflow: %v", err))
		res.Checks["workflow"] = "fail"
		res.Remediation["workflow"] = "Run `maestro workflow init` in the repo root, then re-run `maestro verify`."
	} else {
		res.Checks["workflow"] = "ok"
		if created {
			slog.Info("Created WORKFLOW.md with bootstrap defaults", "path", config.WorkflowPath(repoPath))
			res.Checks["workflow_bootstrap"] = "created"
		}
	}

	if manager, err := config.NewManager(repoPath); err != nil {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("workflow_load: %v", err))
		res.Checks["workflow_load"] = "fail"
		res.Remediation["workflow_load"] = "Fix the WORKFLOW.md format or regenerate it with `maestro workflow init`."
	} else {
		res.Checks["workflow_load"] = "ok"
		_, _ = manager.Current()
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("db_dir: %v", err))
		res.Checks["db_dir"] = "fail"
		res.Remediation["db_dir"] = "Create or fix permissions on the `.maestro` directory, or pass `--db` to a writable path."
	} else {
		res.Checks["db_dir"] = "ok"
	}

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("db_open: %v", err))
		res.Checks["db_open"] = "fail"
		res.Remediation["db_open"] = "Make sure the database path is writable and not locked by another process, or choose a different `--db` path."
	} else {
		res.Checks["db_open"] = "ok"
		_ = store.Close()
	}

	return res
}
