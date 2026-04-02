package verification

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/olhapi/maestro/pkg/config"
)

func writeTextFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newClaudeResult() *Result {
	return &Result{OK: true, Checks: map[string]string{}, Remediation: map[string]string{}}
}

func TestClaudeCommandParsingHelpers(t *testing.T) {
	command := `env FOO=bar BAR="baz qux" CLAUDE_CODE_SIMPLE=yes claude --bare --add-dir=docs --permission-mode default --allowedTools Bash,Edit,Write,MultiEdit --permission-prompt-tool mcp__maestro__approval_prompt --settings='{"apiKeyHelper":"inline-helper"}' --settings=override.json --setting-sources=user,project,,local`

	parts := splitClaudeCommand(command)
	if parts.Executable != "claude" {
		t.Fatalf("expected executable claude, got %q", parts.Executable)
	}
	if got, want := parts.Env["FOO"], "bar"; got != want {
		t.Fatalf("expected FOO=%q, got %q", want, got)
	}
	if got, want := parts.Env["BAR"], "baz qux"; got != want {
		t.Fatalf("expected BAR=%q, got %q", want, got)
	}
	if got, want := parts.Env["CLAUDE_CODE_SIMPLE"], "yes"; got != want {
		t.Fatalf("expected CLAUDE_CODE_SIMPLE=%q, got %q", want, got)
	}
	if got := runtimeExecutableFromCommand(command); got != "claude" {
		t.Fatalf("expected runtime executable claude, got %q", got)
	}
	if got, want := shellSplit(`claude "bar baz" qux\ quux 'corge grault'`), []string{"claude", "bar baz", "qux quux", "corge grault"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected shell split: got %v want %v", got, want)
	}
	if key, value := splitShellAssignment("FOO=bar=baz"); key != "FOO" || value != "bar=baz" {
		t.Fatalf("unexpected shell assignment split: %q=%q", key, value)
	}
	if key, value := splitShellAssignment("noequals"); key != "" || value != "" {
		t.Fatalf("expected non-assignment to return empty parts, got %q=%q", key, value)
	}
	if !isShellAssignment("FOO=bar") || isShellAssignment("--flag") || isShellAssignment("=bad") {
		t.Fatalf("unexpected shell assignment detection")
	}
	if got, want := splitClaudeSettingSources(" user,project,, local ,"), []string{"user", "project", "local"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected setting sources: got %v want %v", got, want)
	}

	opts := parseClaudeCommandOptions(command)
	if opts.Executable != "claude" {
		t.Fatalf("expected parsed executable claude, got %q", opts.Executable)
	}
	if !opts.BareMode || opts.BareReason != "--bare" {
		t.Fatalf("expected bare mode from command, got %+v", opts)
	}
	if opts.PermissionMode != "default" {
		t.Fatalf("expected permission mode default, got %q", opts.PermissionMode)
	}
	if got, want := opts.AllowedTools, []string{"Bash", "Edit", "Write", "MultiEdit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected allowed tools: got %v want %v", got, want)
	}
	if opts.PermissionPromptTool != "mcp__maestro__approval_prompt" {
		t.Fatalf("expected permission prompt tool mcp__maestro__approval_prompt, got %q", opts.PermissionPromptTool)
	}
	if !opts.AdditionalDirFlag {
		t.Fatalf("expected additional dir flag to be detected, got %+v", opts)
	}
	if got, want := opts.SettingSources, []string{"user", "project", "local"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected setting sources: got %v want %v", got, want)
	}
	if got, want := opts.SettingsValues, []string{`{"apiKeyHelper":"inline-helper"}`, "override.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected settings values: got %v want %v", got, want)
	}
	if got, want := opts.CommandEnv["FOO"], "bar"; got != want {
		t.Fatalf("expected parsed env FOO=%q, got %q", want, got)
	}
	if got := expectedVersionLabel(""); got != "none" {
		t.Fatalf("expected empty version label to be none, got %q", got)
	}
	if got := expectedVersionLabel(" 1.2.3 "); got != "1.2.3" {
		t.Fatalf("expected trimmed version label, got %q", got)
	}
}

func TestClaudeEnvironmentAndStringHelpers(t *testing.T) {
	t.Setenv("BASE_ENV", "base")

	t.Run("environment helpers", func(t *testing.T) {
		merged := mergeClaudeEnvironment(
			map[string]string{" FOO ": "bar", "": "skip"},
			map[string]string{"FOO": "override", "CLAUDE_CODE_USE_BEDROCK": "1"},
			map[string]string{"BAR": "value"},
		)
		if got, want := merged["FOO"], "override"; got != want {
			t.Fatalf("expected override env %q, got %q", want, got)
		}
		if got, want := merged["BAR"], "value"; got != want {
			t.Fatalf("expected BAR=%q, got %q", want, got)
		}
		if got, want := merged["BASE_ENV"], "base"; got != want {
			t.Fatalf("expected BASE_ENV=%q, got %q", want, got)
		}
		if _, ok := merged[""]; ok {
			t.Fatalf("expected empty env key to be ignored")
		}

		if got, want := envMapToList(map[string]string{"B": "2", "A": "1"}), []string{"A=1", "B=2"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected env list: got %v want %v", got, want)
		}
		if got, want := envMapToList(nil), os.Environ(); !reflect.DeepEqual(got, want) {
			t.Fatalf("expected empty env map to return process environment, got %v want %v", got, want)
		}

		if got, want := claudeEnvValue(merged, "FOO"), "override"; got != want {
			t.Fatalf("expected trimmed env value %q, got %q", want, got)
		}
		if got := claudeEnvValue(nil, "FOO"); got != "" {
			t.Fatalf("expected missing env value to be empty, got %q", got)
		}

		if got := claudeCloudProviderDetail(map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_USE_VERTEX": "1"}); got != "bedrock" {
			t.Fatalf("expected bedrock precedence, got %q", got)
		}
		if got := claudeCloudProviderDetail(map[string]string{"CLAUDE_CODE_USE_VERTEX": "true"}); got != "vertex" {
			t.Fatalf("expected vertex provider, got %q", got)
		}
		if got := claudeCloudProviderDetail(map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "yes"}); got != "foundry" {
			t.Fatalf("expected foundry provider, got %q", got)
		}
		if got := claudeCloudProviderDetail(map[string]string{}); got != "" {
			t.Fatalf("expected no cloud provider detail, got %q", got)
		}

		if got := claudeConfigDir(map[string]string{"CLAUDE_CONFIG_DIR": "/custom/claude"}); got != "/custom/claude" {
			t.Fatalf("expected explicit config dir, got %q", got)
		}
		homeDir := t.TempDir()
		if got, want := claudeConfigDir(map[string]string{"HOME": homeDir}), filepath.Join(homeDir, ".claude"); got != want {
			t.Fatalf("expected HOME-based config dir %q, got %q", want, got)
		}
		fallbackHome, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		if got, want := claudeConfigDir(map[string]string{}), filepath.Join(fallbackHome, ".claude"); got != want {
			t.Fatalf("expected fallback config dir %q, got %q", want, got)
		}
	})

	t.Run("string coercion helpers", func(t *testing.T) {
		raw := map[string]interface{}{
			"string":          " hello ",
			"number":          7,
			"empty":           " ",
			"sliceStrings":    []string{" one ", "", "two"},
			"sliceInterfaces": []interface{}{" three ", nil, 4, "<nil>", ""},
			"sliceString":     " four ",
			"sliceNumber":     5,
			"mapInterfaces":   map[string]interface{}{"A": " one ", "B": 2, " ": "skip"},
			"mapStrings":      map[string]string{"C": " three ", " ": "skip"},
		}

		if got, ok := claudeStringValue(raw, "string"); !ok || got != "hello" {
			t.Fatalf("unexpected string value: ok=%v got=%q", ok, got)
		}
		if got, ok := claudeStringValue(raw, "number"); !ok || got != "7" {
			t.Fatalf("unexpected numeric string value: ok=%v got=%q", ok, got)
		}
		if got, ok := claudeStringValue(raw, "empty"); !ok || got != "" {
			t.Fatalf("expected empty string to be preserved, got ok=%v value=%q", ok, got)
		}
		if _, ok := claudeStringValue(raw, "missing"); ok {
			t.Fatalf("expected missing string value to be absent")
		}

		if got, ok := claudeStringSlice(raw, "sliceStrings"); !ok || !reflect.DeepEqual(got, []string{"one", "two"}) {
			t.Fatalf("unexpected string slice coercion: ok=%v got=%v", ok, got)
		}
		if got, ok := claudeStringSlice(raw, "sliceInterfaces"); !ok || !reflect.DeepEqual(got, []string{"three", "4"}) {
			t.Fatalf("unexpected interface slice coercion: ok=%v got=%v", ok, got)
		}
		if got, ok := claudeStringSlice(raw, "sliceString"); !ok || !reflect.DeepEqual(got, []string{"four"}) {
			t.Fatalf("unexpected scalar string slice coercion: ok=%v got=%v", ok, got)
		}
		if got, ok := claudeStringSlice(raw, "sliceNumber"); !ok || !reflect.DeepEqual(got, []string{"5"}) {
			t.Fatalf("unexpected scalar number slice coercion: ok=%v got=%v", ok, got)
		}
		if _, ok := claudeStringSlice(raw, "empty"); ok {
			t.Fatalf("expected empty string slice to be ignored")
		}

		if got, ok := claudeStringMap(raw, "mapInterfaces"); !ok || !reflect.DeepEqual(got, map[string]string{"A": "one", "B": "2"}) {
			t.Fatalf("unexpected interface map coercion: ok=%v got=%v", ok, got)
		}
		if got, ok := claudeStringMap(raw, "mapStrings"); !ok || !reflect.DeepEqual(got, map[string]string{"C": "three"}) {
			t.Fatalf("unexpected string map coercion: ok=%v got=%v", ok, got)
		}
		if _, ok := claudeStringMap(raw, "missing"); ok {
			t.Fatalf("expected missing map to be absent")
		}
		if _, ok := claudeStringMap(map[string]interface{}{"map": "nope"}, "map"); ok {
			t.Fatalf("expected unsupported map type to be ignored")
		}
	})
}

func TestClaudeSettingsLoadingAndValueHelpers(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	writeTextFile(t, filepath.Join(home, ".claude", "settings.json"), `{"apiKeyHelper":"user-helper","env":{"USER":"1","SHARED":"user"},"defaultMode":"ask"}`)
	writeTextFile(t, filepath.Join(repo, ".claude", "settings.json"), `{"env":{"PROJECT":"2","SHARED":"project"},"permissions":{"defaultMode":"workspace","additionalDirectories":["project-dir",null]}}`)
	writeTextFile(t, filepath.Join(repo, ".claude", "settings.local.json"), `{"env":{"LOCAL":"3"},"permissions":{"additionalDirectories":["local-dir"]}}`)
	writeTextFile(t, filepath.Join(repo, "override.json"), `{"apiKeyHelper":"file-helper","env":{"FILE":"5","SHARED":"file"},"permissions":{"defaultMode":"bypassPermissions","additionalDirectories":["inline-dir",2]},"additionalDirectories":"top-level-dir"}`)

	inlineJSON := `{"apiKeyHelper":"inline-helper","env":{"INLINE":"4","SHARED":"inline"},"permissions":{"additionalDirectories":["inline-inline",3]}}`
	opts := claudeCommandOptions{
		CommandEnv:     map[string]string{"HOME": home},
		SettingSources: []string{"user", "project", "local", "bogus"},
		SettingsValues: []string{inlineJSON, "override.json"},
	}

	state := loadClaudeSettingsState(repo, opts)
	if got, want := state.ApiKeyHelper, "file-helper"; got != want {
		t.Fatalf("expected last settings helper %q, got %q", want, got)
	}
	if got, want := state.DefaultMode, "bypassPermissions"; got != want {
		t.Fatalf("expected last permissions default mode %q, got %q", want, got)
	}
	if got, want := state.AdditionalDirectories, []string{"top-level-dir"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected top-level additional directories %v, got %v", want, got)
	}
	if got, want := state.Env, map[string]string{
		"USER":    "1",
		"PROJECT": "2",
		"LOCAL":   "3",
		"INLINE":  "4",
		"FILE":    "5",
		"SHARED":  "file",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected merged env: got %v want %v", got, want)
	}

	if got, ok := claudeSettingsValue(`{"apiKeyHelper":"inline"}`, repo); !ok || got["apiKeyHelper"] != "inline" {
		t.Fatalf("expected inline JSON settings to parse, got ok=%v raw=%v", ok, got)
	}
	if got, ok := claudeSettingsValue("override.json", repo); !ok || got["apiKeyHelper"] != "file-helper" {
		t.Fatalf("expected relative file settings to parse, got ok=%v raw=%v", ok, got)
	}
	if _, ok := claudeSettingsValue("{not-json", repo); ok {
		t.Fatalf("expected invalid inline JSON to be rejected")
	}
	if _, ok := claudeSettingsValue("missing.json", repo); ok {
		t.Fatalf("expected missing settings file to be rejected")
	}
}

func TestClaudeAuthStatusAndSessionIssues(t *testing.T) {
	t.Run("auth status", func(t *testing.T) {
		tmp := t.TempDir()
		writeFakeClaude(t, tmp, "1.2.3")

		status, err := detectClaudeAuthStatus("claude", map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"})
		if err != nil {
			t.Fatalf("expected successful auth status lookup, got %v", err)
		}
		if !status.LoggedIn || status.ApiProvider != "bedrock" {
			t.Fatalf("unexpected auth status: %+v", status)
		}

		status, err = detectClaudeAuthStatus("claude", map[string]string{"FAKE_CLAUDE_AUTH_STATUS_JSON": "not-json"})
		if err == nil || !strings.Contains(err.Error(), "invalid character") {
			t.Fatalf("expected JSON parse error, got status=%+v err=%v", status, err)
		}

		failingRoot := t.TempDir()
		writeRuntimeScript(t, filepath.Join(failingRoot, "bin"), "claude", `case "$1" in
auth)
  printf 'not-json\n'
  exit 1
  ;;
esac
`)
		status, err = detectClaudeAuthStatus("claude", map[string]string{})
		if err == nil || !strings.Contains(err.Error(), "not-json") {
			t.Fatalf("expected command failure to include output, got status=%+v err=%v", status, err)
		}

		if _, err := detectClaudeAuthStatus("missing-claude", map[string]string{}); err == nil {
			t.Fatalf("expected missing binary lookup to fail")
		}
	})

	t.Run("session issues", func(t *testing.T) {
		if status, reason, dirs := detectClaudeSessionIssues("", claudeCommandOptions{}, claudeSettingsState{}); status != "ok" || reason != "" || len(dirs) != 0 {
			t.Fatalf("expected clean session to be ok, got status=%q reason=%q dirs=%v", status, reason, dirs)
		}
		if status, reason, dirs := detectClaudeSessionIssues("", claudeCommandOptions{BareMode: true}, claudeSettingsState{}); status != "fail" || reason != "runtime command includes `--bare`" || len(dirs) != 0 {
			t.Fatalf("expected bare session failure, got status=%q reason=%q dirs=%v", status, reason, dirs)
		}
		if status, reason, _ := detectClaudeSessionIssues("", claudeCommandOptions{PermissionMode: "bypassPermissions"}, claudeSettingsState{}); status != "fail" || reason != "runtime command sets `--permission-mode bypassPermissions`" {
			t.Fatalf("expected permission bypass failure, got status=%q reason=%q", status, reason)
		}
		if status, reason, _ := detectClaudeSessionIssues("", claudeCommandOptions{PermissionMode: "auto"}, claudeSettingsState{}); status != "fail" || reason != "runtime command sets `--permission-mode auto`" {
			t.Fatalf("expected permission auto failure, got status=%q reason=%q", status, reason)
		}
		if status, reason, _ := detectClaudeSessionIssues("", claudeCommandOptions{}, claudeSettingsState{DefaultMode: "bypassPermissions"}); status != "fail" || reason != "Claude settings set `permissions.defaultMode: bypassPermissions`" {
			t.Fatalf("expected settings default mode failure, got status=%q reason=%q", status, reason)
		}
		if status, reason, _ := detectClaudeSessionIssues("", claudeCommandOptions{}, claudeSettingsState{DefaultMode: "auto"}); status != "fail" || reason != "Claude settings set `permissions.defaultMode: auto`" {
			t.Fatalf("expected settings auto mode failure, got status=%q reason=%q", status, reason)
		}
		if status, reason, dirs := detectClaudeSessionIssues("", claudeCommandOptions{AdditionalDirFlag: true}, claudeSettingsState{}); status != "fail" || reason != "" || !reflect.DeepEqual(dirs, []string{"--add-dir"}) {
			t.Fatalf("expected add-dir flag to be converted into additional directories, got status=%q reason=%q dirs=%v", status, reason, dirs)
		}
		if status, reason, dirs := detectClaudeSessionIssues("", claudeCommandOptions{AdditionalDirFlag: true}, claudeSettingsState{AdditionalDirectories: []string{"docs"}}); status != "fail" || reason != "" || !reflect.DeepEqual(dirs, []string{"docs"}) {
			t.Fatalf("expected configured additional directories to be preserved, got status=%q reason=%q dirs=%v", status, reason, dirs)
		}
	})
}

func TestValidateClaudeReadinessFailureAndWarningStates(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "")
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "")
	t.Setenv("CLAUDE_CODE_USE_FOUNDRY", "")
	emptyConfigDir := t.TempDir()

	t.Run("warns on token auth and version mismatch", func(t *testing.T) {
		repo := t.TempDir()
		writeFakeClaude(t, repo, "1.2.3")
		workflow := &config.Workflow{Path: filepath.Join(repo, "WORKFLOW.md")}
		result := newClaudeResult()
		runtime := config.RuntimeConfig{
			Provider:        "claude",
			Transport:       "stdio",
			Command:         fmt.Sprintf("CLAUDE_CONFIG_DIR=%s ANTHROPIC_AUTH_TOKEN=test-token claude", emptyConfigDir),
			ExpectedVersion: "9.9.9",
		}

		validateClaudeReadiness(result, workflow, runtime)

		if !result.OK {
			t.Fatalf("expected token auth and version mismatch to warn rather than fail, got %+v", result)
		}
		if got, want := result.Checks["claude_auth_source"], "ANTHROPIC_AUTH_TOKEN"; got != want {
			t.Fatalf("expected token auth source %q, got %q", want, got)
		}
		if got, want := result.Checks["claude_auth_source_status"], "warn"; got != want {
			t.Fatalf("expected token auth to warn, got %q", got)
		}
		if got, want := result.Checks["claude_version_status"], "warn"; got != want {
			t.Fatalf("expected version mismatch to warn, got %q", got)
		}
		if got, want := result.Checks["runtime_claude"], "warn"; got != want {
			t.Fatalf("expected overall runtime readiness to warn, got %q", got)
		}
	})

	t.Run("fails when auth status rejects oauth login", func(t *testing.T) {
		repo := t.TempDir()
		writeFakeClaude(t, repo, "1.2.3")
		workflow := &config.Workflow{Path: filepath.Join(repo, "WORKFLOW.md")}
		result := newClaudeResult()
		runtime := config.RuntimeConfig{
			Provider:        "claude",
			Transport:       "stdio",
			Command:         fmt.Sprintf("CLAUDE_CONFIG_DIR=%s claude", emptyConfigDir),
			ExpectedVersion: "1.2.3",
		}
		t.Setenv("FAKE_CLAUDE_AUTH_STATUS_JSON", `{"loggedIn":false,"authMethod":"claude.ai","apiProvider":"firstParty"}`)

		validateClaudeReadiness(result, workflow, runtime)

		if result.OK {
			t.Fatalf("expected rejected oauth login to fail readiness, got %+v", result)
		}
		if got, want := result.Checks["claude_auth_source"], "OAuth"; got != want {
			t.Fatalf("expected oauth auth source %q, got %q", want, got)
		}
		if got, want := result.Checks["claude_auth_source_status"], "fail"; got != want {
			t.Fatalf("expected oauth auth status fail, got %q", got)
		}
		if !strings.Contains(strings.Join(result.Errors, "\n"), "claude_auth_source: OAuth") {
			t.Fatalf("expected oauth failure to be reported, got %+v", result.Errors)
		}
	})

	t.Run("fails on unsupported provider and transport", func(t *testing.T) {
		repo := t.TempDir()
		writeFakeClaude(t, repo, "1.2.3")
		workflow := &config.Workflow{Path: filepath.Join(repo, "WORKFLOW.md")}
		result := newClaudeResult()
		runtime := config.RuntimeConfig{
			Provider:        "other",
			Transport:       "grpc",
			Command:         fmt.Sprintf("CLAUDE_CONFIG_DIR=%s claude", emptyConfigDir),
			ExpectedVersion: "1.2.3",
		}

		validateClaudeReadiness(result, workflow, runtime)

		if result.OK {
			t.Fatalf("expected unsupported runtime config to fail readiness, got %+v", result)
		}
		if !strings.Contains(strings.Join(result.Errors, "\n"), "unsupported provider \"other\"") {
			t.Fatalf("expected unsupported provider error, got %+v", result.Errors)
		}
		if !strings.Contains(strings.Join(result.Errors, "\n"), "unsupported transport \"grpc\"") {
			t.Fatalf("expected unsupported transport error, got %+v", result.Errors)
		}
		if got, want := result.Checks["runtime_claude"], "fail"; got != want {
			t.Fatalf("expected runtime readiness to fail, got %q", got)
		}
	})

	t.Run("fails when claude binary is missing", func(t *testing.T) {
		repo := t.TempDir()
		workflow := &config.Workflow{Path: filepath.Join(repo, "WORKFLOW.md")}
		result := newClaudeResult()
		runtime := config.RuntimeConfig{
			Provider:        "claude",
			Transport:       "stdio",
			Command:         fmt.Sprintf("CLAUDE_CONFIG_DIR=%s missing-claude", emptyConfigDir),
			ExpectedVersion: "1.2.3",
		}

		validateClaudeReadiness(result, workflow, runtime)

		if result.OK {
			t.Fatalf("expected missing binary to fail readiness, got %+v", result)
		}
		if got, want := result.Checks["claude_version"], "unavailable"; got != want {
			t.Fatalf("expected unavailable version, got %q", got)
		}
		if got, want := result.Checks["claude_version_status"], "fail"; got != want {
			t.Fatalf("expected version status fail for missing binary, got %q", got)
		}
		if !strings.Contains(strings.Join(result.Errors, "\n"), "unable to locate executable") {
			t.Fatalf("expected missing binary error, got %+v", result.Errors)
		}
	})
}
