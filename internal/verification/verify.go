package verification

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/pkg/config"
)

type Result struct {
	OK     bool              `json:"ok"`
	Checks map[string]string `json:"checks"`
	Errors []string          `json:"errors,omitempty"`
}

func Run(repoPath, dbPath string) Result {
	res := Result{OK: true, Checks: map[string]string{}}

	if repoPath == "" {
		repoPath, _ = os.Getwd()
	}
	if dbPath == "" {
		dbPath = filepath.Join(repoPath, ".symphony", "symphony.db")
	}

	if _, created, err := config.EnsureWorkflow(repoPath, config.InitOptions{}); err != nil {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("workflow: %v", err))
		res.Checks["workflow"] = "fail"
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
	} else {
		res.Checks["workflow_load"] = "ok"
		_, _ = manager.Current()
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("db_dir: %v", err))
		res.Checks["db_dir"] = "fail"
	} else {
		res.Checks["db_dir"] = "ok"
	}

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("db_open: %v", err))
		res.Checks["db_open"] = "fail"
	} else {
		res.Checks["db_open"] = "ok"
		_ = store.Close()
	}

	return res
}
