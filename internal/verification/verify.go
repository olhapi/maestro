package verification

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/pkg/config"
)

var (
	runtimeVersionCache   sync.Map
	runtimeVersionPattern = regexp.MustCompile(`\b(\d+\.\d+\.\d+)\b`)
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
			validateRuntimeReadiness(&res, workflow)
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

func validateRuntimeReadiness(res *Result, workflow *config.Workflow) {
	if res == nil || workflow == nil {
		return
	}

	res.Checks["runtime_catalog"] = "ok"

	runtimeNames := make([]string, 0, len(workflow.Config.Runtime.Entries))
	for name := range workflow.Config.Runtime.Entries {
		runtimeNames = append(runtimeNames, name)
	}
	sort.Strings(runtimeNames)

	defaultName := strings.TrimSpace(workflow.Config.Runtime.Default)
	for _, name := range runtimeNames {
		checkRuntimeReadiness(res, workflow.Config.Runtime.Entries[name], name, name == defaultName)
	}

	if defaultName != "" {
		if status, ok := res.Checks[runtimeCheckKey(defaultName)]; ok {
			res.Checks["runtime_default"] = status
			if remediation, ok := res.Remediation[runtimeCheckKey(defaultName)]; ok {
				res.Remediation["runtime_default"] = remediation
			}
		} else {
			res.Checks["runtime_default"] = "warn"
			res.Warnings = append(res.Warnings, fmt.Sprintf("runtime_default: missing runtime entry %q", defaultName))
			res.Remediation["runtime_default"] = "Regenerate WORKFLOW.md with `maestro workflow init`."
		}
	}
}

func checkRuntimeReadiness(res *Result, runtime config.RuntimeConfig, name string, isDefault bool) {
	key := runtimeCheckKey(name)
	binaryKey := key + "_binary"
	versionKey := key + "_version"

	summary := "ok"
	binaryStatus := "ok"
	versionStatus := "skipped"
	hasExpectedVersion := strings.TrimSpace(runtime.ExpectedVersion) != ""

	command := strings.TrimSpace(runtime.Command)
	if command == "" {
		if isDefault || hasExpectedVersion {
			summary = "warn"
			binaryStatus = "warn"
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: runtime command is empty", binaryKey))
			res.Remediation[binaryKey] = fmt.Sprintf("Set runtime.%s.command in WORKFLOW.md.", name)
		} else {
			summary = "skipped"
			binaryStatus = "skipped"
		}
		res.Checks[key] = summary
		res.Checks[binaryKey] = binaryStatus
		res.Checks[versionKey] = versionStatus
		if isDefault {
			res.Checks["runtime_default"] = summary
		}
		return
	}

	status, err := detectRuntimeVersion(command)
	switch {
	case status.ExecutablePath == "":
		if isDefault || hasExpectedVersion {
			summary = "warn"
			binaryStatus = "warn"
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: unable to locate executable for %q", binaryKey, command))
			res.Remediation[binaryKey] = fmt.Sprintf("Install %s or update runtime.%s.command in WORKFLOW.md.", runtime.Provider, name)
		} else {
			summary = "skipped"
			binaryStatus = "skipped"
		}
	case err != nil:
		binaryStatus = "ok"
		if hasExpectedVersion {
			summary = "warn"
			versionStatus = "warn"
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", versionKey, err))
			res.Remediation[versionKey] = fmt.Sprintf("Run `%s --version` locally or update runtime.%s.expected_version in WORKFLOW.md.", command, name)
		} else {
			versionStatus = "skipped"
		}
	default:
		binaryStatus = "ok"
		if !hasExpectedVersion {
			versionStatus = "skipped"
		} else if status.Actual != runtime.ExpectedVersion {
			summary = "warn"
			versionStatus = "warn"
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: expected %s, found %s (%s)", versionKey, runtime.ExpectedVersion, status.Actual, status.ExecutablePath))
			res.Remediation[versionKey] = fmt.Sprintf("Update runtime.%s.expected_version or install the matching version of %s.", name, runtime.Provider)
		} else {
			versionStatus = "ok"
		}
	}

	if binaryStatus == "warn" || versionStatus == "warn" {
		summary = "warn"
	}

	res.Checks[key] = summary
	res.Checks[binaryKey] = binaryStatus
	res.Checks[versionKey] = versionStatus
	if isDefault {
		res.Checks["runtime_default"] = summary
	}
}

type runtimeVersionStatus struct {
	Command        string
	ExecutablePath string
	Actual         string
}

func detectRuntimeVersion(command string) (runtimeVersionStatus, error) {
	status := runtimeVersionStatus{Command: strings.TrimSpace(command)}
	executable := runtimeExecutableFromCommand(command)
	if executable == "" {
		return status, nil
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return status, err
	}
	resolved = filepath.Clean(resolved)
	status.ExecutablePath = resolved
	if cached, ok := runtimeVersionCache.Load(resolved); ok {
		status.Actual = cached.(string)
		return status, nil
	}
	cmd := exec.Command(resolved, "--version")
	output, err := cmd.Output()
	if err != nil {
		return status, err
	}
	actual := parseRuntimeVersion(output)
	if actual == "" {
		return status, fmt.Errorf("unable to parse runtime version from %q", strings.TrimSpace(string(output)))
	}
	runtimeVersionCache.Store(resolved, actual)
	status.Actual = actual
	return status, nil
}

func runtimeExecutableFromCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func parseRuntimeVersion(output []byte) string {
	match := runtimeVersionPattern.FindSubmatch(bytes.TrimSpace(output))
	if len(match) < 2 {
		return ""
	}
	return string(match[1])
}

func runtimeCheckKey(name string) string {
	sanitized := strings.NewReplacer("-", "_", " ", "_", ".", "_", "/", "_").Replace(strings.TrimSpace(name))
	return "runtime_" + strings.ToLower(strings.Trim(sanitized, "_"))
}
