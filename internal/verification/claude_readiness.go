package verification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type claudeAuthStatus struct {
	LoggedIn     bool   `json:"loggedIn"`
	AuthMethod   string `json:"authMethod"`
	ApiProvider  string `json:"apiProvider"`
	ApiKeySource string `json:"apiKeySource"`
}

type claudeCommandParts struct {
	Env        map[string]string
	Executable string
	Args       []string
}

type claudeCommandOptions struct {
	CommandEnv           map[string]string
	Executable           string
	BareMode             bool
	BareReason           string
	PermissionMode       string
	AllowedTools         []string
	PermissionPromptTool string
	SettingSources       []string
	SettingsValues       []string
	AdditionalDirFlag    bool
}

type claudeSettingsState struct {
	DefaultMode           string
	AdditionalDirectories []string
	ApiKeyHelper          string
	Env                   map[string]string
}

func splitClaudeCommand(command string) claudeCommandParts {
	parts := claudeCommandParts{Env: map[string]string{}}
	tokens := shellSplit(command)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "env" && parts.Executable == "" && len(parts.Env) == 0 {
			continue
		}
		if isShellAssignment(token) && parts.Executable == "" {
			key, value := splitShellAssignment(token)
			if key != "" {
				parts.Env[key] = value
			}
			continue
		}
		parts.Executable = token
		if i+1 < len(tokens) {
			parts.Args = append(parts.Args, tokens[i+1:]...)
		}
		break
	}
	return parts
}

func splitShellAssignment(token string) (string, string) {
	idx := strings.IndexByte(token, '=')
	if idx <= 0 {
		return "", ""
	}
	return token[:idx], token[idx+1:]
}

func parseClaudeCommandOptions(command string) claudeCommandOptions {
	parts := splitClaudeCommand(command)
	opts := claudeCommandOptions{
		CommandEnv: parts.Env,
		Executable: strings.TrimSpace(parts.Executable),
	}
	if opts.Executable == "" {
		opts.Executable = "claude"
	}

	tokens := parts.Args
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--bare":
			opts.BareMode = true
			if opts.BareReason == "" {
				opts.BareReason = "--bare"
			}
		case token == "--add-dir" || strings.HasPrefix(token, "--add-dir="):
			opts.AdditionalDirFlag = true
		case token == "--permission-mode":
			if i+1 < len(tokens) {
				opts.PermissionMode = tokens[i+1]
				i++
			}
		case strings.HasPrefix(token, "--permission-mode="):
			opts.PermissionMode = strings.TrimPrefix(token, "--permission-mode=")
		case token == "--allowed-tools" || token == "--allowedTools":
			if i+1 < len(tokens) {
				opts.AllowedTools = append(opts.AllowedTools, splitClaudeSettingSources(tokens[i+1])...)
				i++
			}
		case strings.HasPrefix(token, "--allowed-tools="):
			opts.AllowedTools = append(opts.AllowedTools, splitClaudeSettingSources(strings.TrimPrefix(token, "--allowed-tools="))...)
		case strings.HasPrefix(token, "--allowedTools="):
			opts.AllowedTools = append(opts.AllowedTools, splitClaudeSettingSources(strings.TrimPrefix(token, "--allowedTools="))...)
		case token == "--permission-prompt-tool":
			if i+1 < len(tokens) {
				opts.PermissionPromptTool = tokens[i+1]
				i++
			}
		case strings.HasPrefix(token, "--permission-prompt-tool="):
			opts.PermissionPromptTool = strings.TrimPrefix(token, "--permission-prompt-tool=")
		case token == "--settings":
			if i+1 < len(tokens) {
				opts.SettingsValues = append(opts.SettingsValues, tokens[i+1])
				i++
			}
		case strings.HasPrefix(token, "--settings="):
			opts.SettingsValues = append(opts.SettingsValues, strings.TrimPrefix(token, "--settings="))
		case token == "--setting-sources":
			if i+1 < len(tokens) {
				opts.SettingSources = append(opts.SettingSources, splitClaudeSettingSources(tokens[i+1])...)
				i++
			}
		case strings.HasPrefix(token, "--setting-sources="):
			opts.SettingSources = append(opts.SettingSources, splitClaudeSettingSources(strings.TrimPrefix(token, "--setting-sources="))...)
		}
	}

	if isTruthyFlagValue(opts.CommandEnv["CLAUDE_CODE_SIMPLE"]) {
		opts.BareMode = true
		if opts.BareReason == "" {
			opts.BareReason = "CLAUDE_CODE_SIMPLE=1"
		}
	}

	return opts
}

func splitClaudeSettingSources(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func mergeClaudeEnvironment(scopes ...map[string]string) map[string]string {
	env := make(map[string]string, len(os.Environ()))
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		env[parts[0]] = parts[1]
	}
	for _, scope := range scopes {
		for key, value := range scope {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			env[key] = value
		}
	}
	return env
}

func envMapToList(env map[string]string) []string {
	if len(env) == 0 {
		return os.Environ()
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func claudeEnvValue(env map[string]string, key string) string {
	if env == nil {
		return ""
	}
	return strings.TrimSpace(env[key])
}

func claudeCloudProviderDetail(env map[string]string) string {
	switch {
	case isTruthyFlagValue(claudeEnvValue(env, "CLAUDE_CODE_USE_BEDROCK")):
		return "bedrock"
	case isTruthyFlagValue(claudeEnvValue(env, "CLAUDE_CODE_USE_VERTEX")):
		return "vertex"
	case isTruthyFlagValue(claudeEnvValue(env, "CLAUDE_CODE_USE_FOUNDRY")):
		return "foundry"
	default:
		return ""
	}
}

func claudeAuthSourceFromEnvironment(env map[string]string, state claudeSettingsState) (source, detail, readiness string) {
	if detail := claudeCloudProviderDetail(env); detail != "" {
		return "cloud provider", detail, "ok"
	}
	if claudeEnvValue(env, "ANTHROPIC_AUTH_TOKEN") != "" {
		return "ANTHROPIC_AUTH_TOKEN", "", "warn"
	}
	if claudeEnvValue(env, "ANTHROPIC_API_KEY") != "" {
		return "ANTHROPIC_API_KEY", "", "warn"
	}
	if helper := strings.TrimSpace(state.ApiKeyHelper); helper != "" {
		return "apiKeyHelper", helper, "warn"
	}
	return "OAuth", "claude.ai", "ok"
}

func detectClaudeAuthStatus(command string, env map[string]string) (claudeAuthStatus, error) {
	effective := strings.TrimSpace(command)
	if effective == "" {
		effective = "claude"
	}

	resolved, err := exec.LookPath(effective)
	if err != nil {
		return claudeAuthStatus{}, err
	}

	cmd := exec.Command(resolved, "auth", "status", "--json")
	cmd.Env = envMapToList(env)
	output, err := cmd.CombinedOutput()

	var status claudeAuthStatus
	if jsonErr := json.Unmarshal(bytes.TrimSpace(output), &status); jsonErr != nil {
		if err != nil {
			return status, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
		}
		return status, jsonErr
	}
	return status, err
}

func detectClaudeSessionIssues(repoPath string, opts claudeCommandOptions, state claudeSettingsState) (string, string, []string) {
	bareReason := ""
	switch {
	case opts.BareMode:
		bareReason = "runtime command includes `--bare`"
	case strings.EqualFold(strings.TrimSpace(opts.PermissionMode), "bypassPermissions"):
		bareReason = "runtime command sets `--permission-mode bypassPermissions`"
	case strings.EqualFold(strings.TrimSpace(state.DefaultMode), "bypassPermissions"):
		bareReason = "Claude settings set `permissions.defaultMode: bypassPermissions`"
	}

	directories := append([]string(nil), state.AdditionalDirectories...)
	if opts.AdditionalDirFlag && len(directories) == 0 {
		directories = append(directories, "--add-dir")
	}

	status := "ok"
	if bareReason != "" || len(directories) > 0 {
		status = "fail"
	}

	return status, bareReason, directories
}

func loadClaudeSettingsState(repoPath string, opts claudeCommandOptions) claudeSettingsState {
	state := claudeSettingsState{Env: map[string]string{}}
	sources := opts.SettingSources
	if len(sources) == 0 {
		sources = []string{"user", "project", "local"}
	}
	for _, source := range sources {
		raw, ok := claudeSettingsValueForSource(repoPath, source, opts.CommandEnv)
		if !ok {
			continue
		}
		applyClaudeSettingsState(&state, raw)
	}
	for _, value := range opts.SettingsValues {
		raw, ok := claudeSettingsValue(value, repoPath)
		if !ok {
			continue
		}
		applyClaudeSettingsState(&state, raw)
	}
	return state
}

func claudeSettingsValueForSource(repoPath, source string, commandEnv map[string]string) (map[string]interface{}, bool) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "user":
		dir := claudeConfigDir(commandEnv)
		if dir == "" {
			return nil, false
		}
		return claudeSettingsValue(filepath.Join(dir, "settings.json"), repoPath)
	case "project":
		if strings.TrimSpace(repoPath) == "" {
			return nil, false
		}
		return claudeSettingsValue(filepath.Join(repoPath, ".claude", "settings.json"), repoPath)
	case "local":
		if strings.TrimSpace(repoPath) == "" {
			return nil, false
		}
		return claudeSettingsValue(filepath.Join(repoPath, ".claude", "settings.local.json"), repoPath)
	default:
		return nil, false
	}
}

func claudeSettingsValue(value string, repoPath string) (map[string]interface{}, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, false
	}

	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
			return nil, false
		}
		return raw, true
	}

	path := trimmed
	if !filepath.IsAbs(path) && strings.TrimSpace(repoPath) != "" {
		path = filepath.Join(repoPath, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false
	}
	return raw, true
}

func applyClaudeSettingsState(state *claudeSettingsState, raw map[string]interface{}) {
	if state == nil || len(raw) == 0 {
		return
	}
	if state.Env == nil {
		state.Env = map[string]string{}
	}

	if helper, ok := claudeStringValue(raw, "apiKeyHelper"); ok {
		state.ApiKeyHelper = helper
	}
	if env, ok := claudeStringMap(raw, "env"); ok {
		for key, value := range env {
			state.Env[key] = value
		}
	}
	if mode, ok := claudeStringValue(raw, "defaultMode"); ok {
		state.DefaultMode = mode
	}
	if permissions, ok := raw["permissions"].(map[string]interface{}); ok {
		if mode, ok := claudeStringValue(permissions, "defaultMode"); ok {
			state.DefaultMode = mode
		}
		if dirs, ok := claudeStringSlice(permissions, "additionalDirectories"); ok {
			state.AdditionalDirectories = dirs
		}
	}
	if dirs, ok := claudeStringSlice(raw, "additionalDirectories"); ok {
		state.AdditionalDirectories = dirs
	}
}

func claudeStringValue(raw map[string]interface{}, key string) (string, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), true
	default:
		text := strings.TrimSpace(fmt.Sprint(typed))
		if text == "" || text == "<nil>" {
			return "", false
		}
		return text, true
	}
}

func claudeStringSlice(raw map[string]interface{}, key string) ([]string, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return nil, false
	}
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out, true
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if item == nil {
				continue
			}
			trimmed := strings.TrimSpace(fmt.Sprint(item))
			if trimmed != "" && trimmed != "<nil>" {
				out = append(out, trimmed)
			}
		}
		return out, true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil, false
		}
		return []string{trimmed}, true
	default:
		text := strings.TrimSpace(fmt.Sprint(typed))
		if text == "" || text == "<nil>" {
			return nil, false
		}
		return []string{text}, true
	}
}

func claudeStringMap(raw map[string]interface{}, key string) (map[string]string, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return nil, false
	}
	out := map[string]string{}
	switch typed := value.(type) {
	case map[string]interface{}:
		for k := range typed {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			if text, ok := claudeStringValue(typed, k); ok {
				out[key] = text
			}
		}
	case map[string]string:
		for k, v := range typed {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			if text := strings.TrimSpace(v); text != "" {
				out[key] = text
			}
		}
	default:
		return nil, false
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func claudeConfigDir(env map[string]string) string {
	if dir := strings.TrimSpace(claudeEnvValue(env, "CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	if home := strings.TrimSpace(claudeEnvValue(env, "HOME")); home != "" {
		return filepath.Join(home, ".claude")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

func isTruthyFlagValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
