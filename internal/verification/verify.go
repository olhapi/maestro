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
	"unicode"

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

	if claudeRuntime, ok := workflow.Config.Runtime.Entries["claude"]; ok {
		validateClaudeReadiness(res, workflow, claudeRuntime)
	} else {
		res.OK = false
		res.Checks["claude"] = "fail"
		res.Errors = append(res.Errors, "claude: missing runtime entry")
		res.Remediation["claude"] = "Add a `claude` runtime entry to WORKFLOW.md and re-run `maestro verify`."
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

func validateClaudeReadiness(res *Result, workflow *config.Workflow, runtime config.RuntimeConfig) {
	if res == nil || workflow == nil {
		return
	}

	effectiveCommand := strings.TrimSpace(runtime.Command)
	if effectiveCommand == "" {
		effectiveCommand = "claude"
	}

	summary := "ok"
	binaryKey := "runtime_claude_binary"
	versionKey := "runtime_claude_version"

	repoPath := filepath.Dir(workflow.Path)
	opts := parseClaudeCommandOptions(effectiveCommand)
	state := loadClaudeSettingsState(repoPath, opts)
	combinedEnv := mergeClaudeEnvironment(opts.CommandEnv, state.Env)
	authSource, authDetail, authReadiness := claudeAuthSourceFromEnvironment(combinedEnv, state)
	sessionStatus, bareReason, directories := detectClaudeSessionIssues(repoPath, opts, state)

	versionStatus, versionErr := detectRuntimeVersion(effectiveCommand)
	binaryFound := strings.TrimSpace(versionStatus.ExecutablePath) != ""
	versionReadiness := "ok"
	if !binaryFound {
		summary = "fail"
		res.OK = false
		res.Checks[binaryKey] = "fail"
		res.Checks[versionKey] = "skipped"
		res.Checks["claude_version"] = "unavailable"
		res.Checks["claude_version_status"] = "fail"
		res.Checks["claude_version_expected"] = expectedVersionLabel(runtime.ExpectedVersion)
		res.Errors = append(res.Errors, "claude: unable to locate executable")
		res.Remediation["claude"] = "Install Claude Code or update `runtime.claude.command` in WORKFLOW.md, then re-run `maestro verify`."
		res.Remediation[binaryKey] = "Install Claude Code or update `runtime.claude.command` in WORKFLOW.md."
	} else {
		res.Checks[binaryKey] = "ok"
		actualVersion := strings.TrimSpace(versionStatus.Actual)
		if actualVersion == "" {
			actualVersion = "unavailable"
		}
		res.Checks["claude_version"] = actualVersion
		res.Checks["claude_version_expected"] = expectedVersionLabel(runtime.ExpectedVersion)
		if expected := strings.TrimSpace(runtime.ExpectedVersion); expected != "" {
			if versionStatus.Actual == "" {
				versionReadiness = "warn"
				if versionErr != nil {
					res.Warnings = append(res.Warnings, fmt.Sprintf("claude_version: %v", versionErr))
					res.Remediation["claude_version"] = "Run `claude --version` locally or update `runtime.claude.expected_version` in WORKFLOW.md."
				}
			} else if versionStatus.Actual != expected {
				versionReadiness = "warn"
				res.Warnings = append(res.Warnings, fmt.Sprintf("claude_version: expected %s, found %s (%s)", expected, versionStatus.Actual, versionStatus.ExecutablePath))
				res.Remediation["claude_version"] = "Update `runtime.claude.expected_version` or install the matching Claude Code version."
			}
		} else if versionStatus.Actual == "" {
			versionReadiness = "warn"
			if versionErr != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("claude_version: %v", versionErr))
				res.Remediation["claude_version"] = "Run `claude --version` locally or set `runtime.claude.expected_version` in WORKFLOW.md."
			}
		}
		res.Checks["claude_version_status"] = versionReadiness
		if versionReadiness == "ok" {
			res.Checks[versionKey] = "ok"
		} else {
			res.Checks[versionKey] = "warn"
		}

		if authReadiness == "ok" && (authSource == "OAuth" || authSource == "cloud provider") {
			authStatus, authErr := detectClaudeAuthStatus(opts.Executable, combinedEnv)
			if authErr != nil {
				authReadiness = "fail"
				res.Errors = append(res.Errors, fmt.Sprintf("claude_auth_source: %v", authErr))
				res.Remediation["claude_auth_source"] = "Log in with Claude Code or configure a supported auth source, then re-run `maestro verify`."
			} else if !authStatus.LoggedIn {
				authReadiness = "fail"
				res.Errors = append(res.Errors, fmt.Sprintf("claude_auth_source: %s", authSource))
				res.Remediation["claude_auth_source"] = "Log in with Claude Code or configure a supported auth source, then re-run `maestro verify`."
			} else if authSource == "cloud provider" && authDetail == "" {
				authDetail = strings.TrimSpace(authStatus.ApiProvider)
			}
		}
	}

	res.Checks["claude_auth_source"] = authSource
	if authDetail != "" {
		res.Checks["claude_auth_source_detail"] = authDetail
	}
	res.Checks["claude_auth_source_status"] = authReadiness
	if authReadiness == "warn" {
		res.Warnings = append(res.Warnings, fmt.Sprintf("claude_auth_source: %s", authSource))
	}
	if authReadiness == "fail" {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("claude_auth_source: %s", authSource))
		if _, ok := res.Remediation["claude_auth_source"]; !ok {
			res.Remediation["claude_auth_source"] = "Log in with Claude Code or configure a supported auth source, then re-run `maestro verify`."
		}
	}

	res.Checks["claude_session_status"] = sessionStatus
	if bareReason != "" {
		res.Checks["claude_session_bare_mode"] = "fail"
		res.Errors = append(res.Errors, "claude_session_bare_mode: "+bareReason)
		res.Remediation["claude_session_bare_mode"] = "Remove `--bare`, `--permission-mode auto`, `--permission-mode bypassPermissions`, `permissions.defaultMode: auto`, or `permissions.defaultMode: bypassPermissions` from the Claude configuration."
		res.OK = false
	} else {
		res.Checks["claude_session_bare_mode"] = "ok"
	}
	if len(directories) > 0 {
		res.Checks["claude_session_additional_directories"] = "fail"
		res.Checks["claude_additional_directories"] = strings.Join(directories, ", ")
		res.Errors = append(res.Errors, fmt.Sprintf("claude_session_additional_directories: %s", strings.Join(directories, ", ")))
		res.Remediation["claude_session_additional_directories"] = "Remove `additionalDirectories` or `--add-dir` from Claude configuration so the session stays scoped to the Maestro workspace."
		res.OK = false
	} else {
		res.Checks["claude_session_additional_directories"] = "ok"
	}

	if runtime.Provider != "" && runtime.Provider != "claude" {
		summary = "fail"
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("claude: unsupported provider %q", runtime.Provider))
		res.Remediation["claude"] = "Set `runtime.claude.provider` to `claude` in WORKFLOW.md."
	}
	if strings.TrimSpace(runtime.Transport) != "" && strings.TrimSpace(runtime.Transport) != "stdio" {
		summary = "fail"
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("claude: unsupported transport %q", runtime.Transport))
		res.Remediation["claude"] = "Set `runtime.claude.transport` to `stdio` in WORKFLOW.md."
	}

	if versionReadiness == "warn" && summary == "ok" {
		summary = "warn"
	}
	if authReadiness == "warn" && summary == "ok" {
		summary = "warn"
	}
	if authReadiness == "fail" || sessionStatus == "fail" || !binaryFound {
		summary = "fail"
	}

	res.Checks["runtime_claude"] = summary
	res.Checks["claude"] = summary
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
	parts := splitClaudeCommand(command)
	return strings.TrimSpace(parts.Executable)
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

func expectedVersionLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func shellSplit(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	tokens := make([]string, 0, 8)
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}
	for _, r := range command {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && !inSingle:
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func isShellAssignment(token string) bool {
	if token == "" || strings.HasPrefix(token, "--") {
		return false
	}
	idx := strings.IndexByte(token, '=')
	return idx > 0
}
