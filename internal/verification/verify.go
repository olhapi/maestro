package verification

import (
	"fmt"
	"os"
	"path/filepath"

	codexruntime "github.com/olhapi/maestro/internal/agentruntime/codex"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/pkg/config"
)

type Result struct {
	OK          bool              `json:"ok"`
	Checks      map[string]string `json:"checks"`
	Errors      []string          `json:"errors,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
	Remediation map[string]string `json:"remediation"`
}

func Run(repoPath, dbPath string) Result {
	res := Result{OK: true, Checks: map[string]string{}, Remediation: map[string]string{}}

	if repoPath == "" {
		repoPath, _ = os.Getwd()
	}
	rawDBPath := dbPath
	dbPath = kanban.ResolveDBPath(dbPath)

	workflowPath := config.WorkflowPath(repoPath)
	if info, err := os.Stat(workflowPath); err != nil {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("workflow: %v", err))
		res.Checks["workflow"] = "fail"
		res.Remediation["workflow"] = "Run `maestro workflow init` in the repo root, then re-run `maestro verify`."
		res.Errors = append(res.Errors, fmt.Sprintf("workflow_load: %v", err))
		res.Checks["workflow_load"] = "fail"
		res.Remediation["workflow_load"] = "Create or fix WORKFLOW.md, then re-run `maestro verify`."
	} else if info.IsDir() {
		res.OK = false
		dirErr := fmt.Errorf("%s is a directory", workflowPath)
		res.Errors = append(res.Errors, fmt.Sprintf("workflow: %v", dirErr))
		res.Checks["workflow"] = "fail"
		res.Remediation["workflow"] = "Replace the WORKFLOW.md directory with a valid workflow file."
		res.Errors = append(res.Errors, fmt.Sprintf("workflow_load: %v", dirErr))
		res.Checks["workflow_load"] = "fail"
		res.Remediation["workflow_load"] = "Create or fix WORKFLOW.md, then re-run `maestro verify`."
	} else {
		res.Checks["workflow"] = "ok"
		workflow, err := config.LoadWorkflow(workflowPath)
		if err != nil {
			res.OK = false
			res.Errors = append(res.Errors, fmt.Sprintf("workflow_load: %v", err))
			res.Checks["workflow_load"] = "fail"
			res.Remediation["workflow_load"] = "Fix the WORKFLOW.md format or regenerate it with `maestro workflow init`."
		} else {
			res.Checks["workflow_load"] = "ok"
			for _, advisory := range workflow.Advisories {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s", advisory.Code, advisory.Message))
				res.Checks[advisory.Code] = "warn"
				if advisory.Remediation != "" {
					res.Remediation[advisory.Code] = advisory.Remediation
				}
			}
			status, err := codexruntime.DetectVersion(workflow.Config.Codex.Command)
			switch {
			case err != nil && status.Command != "":
				res.Warnings = append(res.Warnings, fmt.Sprintf("codex_version: %v", err))
				res.Checks["codex_version"] = "warn"
			case status.ExecutablePath == "":
				res.Checks["codex_version"] = "skipped"
			case workflow.Config.Codex.ExpectedVersion != "" && status.Actual != workflow.Config.Codex.ExpectedVersion:
				res.Warnings = append(res.Warnings, fmt.Sprintf("codex_version: expected %s, found %s (%s)", workflow.Config.Codex.ExpectedVersion, status.Actual, status.ExecutablePath))
				res.Checks["codex_version"] = "warn"
			default:
				res.Checks["codex_version"] = "ok"
			}
		}
	}

	if kanban.HasUnresolvedExpandedEnvPath(rawDBPath, dbPath) {
		res.OK = false
		res.Checks["db_dir"] = "skipped"
		res.Errors = append(res.Errors, fmt.Sprintf("db_open: unresolved environment variable in %q", dbPath))
		res.Checks["db_open"] = "fail"
		res.Remediation["db_open"] = "Provide a fully resolved `--db` path."
		return res
	} else if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("db_dir: %v", err))
		res.Checks["db_dir"] = "fail"
		res.Remediation["db_dir"] = "Create or fix permissions on the `~/.maestro` directory, or pass `--db` to a writable path."
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
