package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpapi "github.com/mark3labs/mcp-go/mcp"

	maestromcp "github.com/olhapi/maestro/internal/mcp"
)

func TestRunRequiresOptions(t *testing.T) {
	t.Parallel()

	base := options{
		mcpConfig:      "config.json",
		settings:       "settings.json",
		dbPath:         "db.sqlite",
		registryDir:    "registry",
		evidencePrefix: "evidence",
	}

	tests := []struct {
		name string
		opts options
		want string
	}{
		{name: "missing_mcp_config", opts: options{}, want: "mcp-config is required"},
		{name: "missing_settings", opts: options{mcpConfig: base.mcpConfig}, want: "settings is required"},
		{name: "missing_db", opts: options{mcpConfig: base.mcpConfig, settings: base.settings}, want: "db is required"},
		{name: "missing_registry", opts: options{mcpConfig: base.mcpConfig, settings: base.settings, dbPath: base.dbPath}, want: "registry-dir is required"},
		{name: "missing_evidence_prefix", opts: options{mcpConfig: base.mcpConfig, settings: base.settings, dbPath: base.dbPath, registryDir: base.registryDir}, want: "evidence-prefix is required"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := run(tt.opts)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("run() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadBridgeConfig(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		dbPath := filepath.Join(dir, "maestro.db")
		configPath := filepath.Join(dir, "mcp.json")
		writeJSONFile(t, configPath, map[string]any{
			"mcpServers": map[string]any{
				"maestro": map[string]any{
					"type":    "stdio",
					"command": "maestro",
					"args":    []string{"mcp", "--db", dbPath},
				},
			},
		})

		data, serverName, server, bridgeDBPath, err := loadBridgeConfig(configPath)
		if err != nil {
			t.Fatalf("loadBridgeConfig() error = %v", err)
		}
		if serverName != "maestro" {
			t.Fatalf("serverName = %q, want maestro", serverName)
		}
		if server.Command != "maestro" {
			t.Fatalf("server.Command = %q, want maestro", server.Command)
		}
		if got, want := server.Args, []string{"mcp", "--db", dbPath}; len(got) != len(want) || strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("server.Args = %v, want %v", got, want)
		}
		if got, want := bridgeDBPath, dbPath; filepath.Clean(got) != filepath.Clean(want) {
			t.Fatalf("bridgeDBPath = %q, want %q", got, want)
		}
		if !strings.Contains(string(data), `"maestro"`) {
			t.Fatalf("returned config bytes do not contain maestro server: %s", string(data))
		}
	})

	tests := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{
			name: "missing_maestro_server",
			cfg: map[string]any{
				"mcpServers": map[string]any{
					"other": map[string]any{
						"type":    "stdio",
						"command": "maestro",
						"args":    []string{"mcp", "--db", "db.sqlite"},
					},
				},
			},
			want: `expected "maestro" MCP server in config`,
		},
		{
			name: "wrong_type",
			cfg: map[string]any{
				"mcpServers": map[string]any{
					"maestro": map[string]any{
						"type":    "http",
						"command": "maestro",
						"args":    []string{"mcp", "--db", "db.sqlite"},
					},
				},
			},
			want: `expected maestro MCP server type stdio`,
		},
		{
			name: "wrong_command",
			cfg: map[string]any{
				"mcpServers": map[string]any{
					"maestro": map[string]any{
						"type":    "stdio",
						"command": "other",
						"args":    []string{"mcp", "--db", "db.sqlite"},
					},
				},
			},
			want: `expected maestro MCP command`,
		},
		{
			name: "missing_db_arg",
			cfg: map[string]any{
				"mcpServers": map[string]any{
					"maestro": map[string]any{
						"type":    "stdio",
						"command": "maestro",
						"args":    []string{"mcp"},
					},
				},
			},
			want: `expected --db in maestro MCP args`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			configPath := filepath.Join(dir, "mcp.json")
			writeJSONFile(t, configPath, tt.cfg)

			_, _, _, _, err := loadBridgeConfig(configPath)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("loadBridgeConfig() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadSettingsAndValidateSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	writeJSONFile(t, settingsPath, map[string]any{
		"disableAutoMode":        "disable",
		"useAutoModeDuringPlan":  false,
		"disableAllHooks":        true,
		"includeGitInstructions": false,
		"permissions": map[string]any{
			"disableBypassPermissionsMode": "disable",
		},
	})

	data, overlay, err := loadSettingsOverlay(settingsPath)
	if err != nil {
		t.Fatalf("loadSettingsOverlay() error = %v", err)
	}
	if !strings.Contains(string(data), `"disableAutoMode":"disable"`) {
		t.Fatalf("settings evidence missing disableAutoMode: %s", string(data))
	}
	if err := validateSettings(overlay); err != nil {
		t.Fatalf("validateSettings() error = %v", err)
	}

	tests := []struct {
		name     string
		mutate   func(*claudeSettingsOverlay)
		wantText string
	}{
		{
			name: "disable_auto_mode",
			mutate: func(s *claudeSettingsOverlay) {
				s.DisableAutoMode = "warn"
			},
			wantText: `expected disableAutoMode=disable`,
		},
		{
			name: "auto_mode_during_plan",
			mutate: func(s *claudeSettingsOverlay) {
				s.UseAutoModeDuringPlan = true
			},
			wantText: `expected useAutoModeDuringPlan=false`,
		},
		{
			name: "all_hooks",
			mutate: func(s *claudeSettingsOverlay) {
				s.DisableAllHooks = false
			},
			wantText: `expected disableAllHooks=true`,
		},
		{
			name: "git_instructions",
			mutate: func(s *claudeSettingsOverlay) {
				s.IncludeGitInstructions = true
			},
			wantText: `expected includeGitInstructions=false`,
		},
		{
			name: "disable_bypass_permissions_mode",
			mutate: func(s *claudeSettingsOverlay) {
				s.Permissions.DisableBypassPermissionsMode = "warn"
			},
			wantText: `expected permissions.disableBypassPermissionsMode=disable`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			candidate := overlay
			tt.mutate(&candidate)
			err := validateSettings(candidate)
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("validateSettings() error = %v, want substring %q", err, tt.wantText)
			}
		})
	}
}

func TestValidatePermissionFlags(t *testing.T) {
	t.Parallel()

	valid := options{
		allowedTools:         "Bash,Edit,Write,MultiEdit",
		permissionMode:       "default",
		permissionPromptTool: "",
		strictMCPConfig:      "true",
	}
	if err := validatePermissionFlags(valid); err != nil {
		t.Fatalf("validatePermissionFlags(valid) error = %v", err)
	}

	tests := []struct {
		name string
		opts options
		want string
	}{
		{
			name: "permission_mode",
			opts: options{
				allowedTools:    valid.allowedTools,
				permissionMode:  "auto",
				strictMCPConfig: valid.strictMCPConfig,
			},
			want: `expected Claude permission mode default`,
		},
		{
			name: "allowed_tools",
			opts: options{
				allowedTools:    "Bash",
				permissionMode:  valid.permissionMode,
				strictMCPConfig: valid.strictMCPConfig,
			},
			want: `expected allowed tools Bash,Edit,Write,MultiEdit`,
		},
		{
			name: "permission_prompt_tool",
			opts: options{
				allowedTools:         valid.allowedTools,
				permissionMode:       valid.permissionMode,
				permissionPromptTool: "ask",
				strictMCPConfig:      valid.strictMCPConfig,
			},
			want: `expected no permission prompt tool`,
		},
		{
			name: "strict_mcp_config",
			opts: options{
				allowedTools:    valid.allowedTools,
				permissionMode:  valid.permissionMode,
				strictMCPConfig: "false",
			},
			want: `expected strict-mcp-config=true`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validatePermissionFlags(tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validatePermissionFlags() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadDaemonEntriesAndHelpers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	entryA := maestromcp.DaemonEntry{
		StoreID:     "store-b",
		DBPath:      filepath.Join(dir, "nested", "..", "maestro.db"),
		PID:         10,
		BaseURL:     "http://127.0.0.1:8080/mcp",
		BearerToken: "token-a",
		Version:     "1.0.0",
		Transport:   "http",
	}
	entryB := maestromcp.DaemonEntry{
		StoreID:     "store-a",
		DBPath:      filepath.Join(dir, "maestro.db"),
		PID:         11,
		BaseURL:     "http://127.0.0.1:8081/mcp",
		BearerToken: "token-b",
		Version:     "1.0.1",
		Transport:   "stdio",
	}
	writeJSONFile(t, filepath.Join(dir, "b.json"), entryA)
	writeJSONFile(t, filepath.Join(dir, "a.json"), entryB)
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("noop"), 0o600); err != nil {
		t.Fatalf("write ignore file: %v", err)
	}

	entries, err := loadDaemonEntries(dir)
	if err != nil {
		t.Fatalf("loadDaemonEntries() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("loadDaemonEntries() returned %d entries, want 2", len(entries))
	}
	if entries[0].StoreID != "store-a" || entries[1].StoreID != "store-b" {
		t.Fatalf("loadDaemonEntries() sorting mismatch: %+v", entries)
	}

	sameLeft := entryA
	sameRight := entryA
	sameRight.DBPath = filepath.Join(dir, "nested", "..", "maestro.db")
	if !sameDaemonEntry(sameLeft, sameRight) {
		t.Fatalf("sameDaemonEntry() = false, want true")
	}
	sameRight.BearerToken = "different"
	if sameDaemonEntry(sameLeft, sameRight) {
		t.Fatalf("sameDaemonEntry() = true after mutation, want false")
	}

	if got := extractDBPathArg([]string{"mcp", "--db", "db.sqlite"}); got != "db.sqlite" {
		t.Fatalf("extractDBPathArg() = %q, want db.sqlite", got)
	}
	if got := extractDBPathArg([]string{"mcp"}); got != "" {
		t.Fatalf("extractDBPathArg() = %q, want empty", got)
	}
}

func TestDecodeDataAndStringHelpers(t *testing.T) {
	t.Parallel()

	if err := decodeData(nil, &map[string]any{}); err == nil || err.Error() != "missing response envelope" {
		t.Fatalf("decodeData(nil) error = %v, want missing response envelope", err)
	}
	if err := decodeData(&responseEnvelope{}, &map[string]any{}); err == nil || err.Error() != "missing response envelope data" {
		t.Fatalf("decodeData(empty) error = %v, want missing response envelope data", err)
	}

	var decoded map[string]any
	if err := decodeData(&responseEnvelope{Data: json.RawMessage(`{"ok":true}`)}, &decoded); err != nil {
		t.Fatalf("decodeData(valid) error = %v", err)
	}
	if decoded["ok"] != true {
		t.Fatalf("decodeData(valid) decoded = %v, want ok=true", decoded)
	}

	tools := []mcpapi.Tool{{Name: "list_sessions"}, {Name: "server_info"}, {Name: "create_issue"}}
	gotTools := sortedToolNames(tools)
	wantTools := []string{"create_issue", "list_sessions", "server_info"}
	if strings.Join(gotTools, ",") != strings.Join(wantTools, ",") {
		t.Fatalf("sortedToolNames() = %v, want %v", gotTools, wantTools)
	}
	if !containsString(gotTools, "server_info") || containsString(gotTools, "missing") {
		t.Fatalf("containsString() mismatch for values %v", gotTools)
	}

	if got := firstNonEmpty("", "  ", "value", "other"); got != "value" {
		t.Fatalf("firstNonEmpty() = %q, want value", got)
	}
	if got := asString("value"); got != "value" {
		t.Fatalf("asString(string) = %q, want value", got)
	}
	if got := asString(testStringer("stringer")); got != "stringer" {
		t.Fatalf("asString(stringer) = %q, want stringer", got)
	}
	if got := asString(42); got != "" {
		t.Fatalf("asString(non-string) = %q, want empty", got)
	}
}

func TestWriteEvidence(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prefix := filepath.Join(dir, "launch-1")
	evidence := probeEvidence{}
	evidence.Bridge.AllowedTools = "Bash,Edit,Write,MultiEdit"
	evidence.Bridge.DBPath = filepath.Join(dir, "maestro.db")
	evidence.Bridge.StrictMCPConfig = true
	evidence.Bridge.PermissionMode = "default"
	evidence.Bridge.PermissionPromptTool = "<none>"
	evidence.Bridge.ToolNames = []string{"create_issue", "server_info"}
	evidence.Settings.DisableAllHooks = true
	evidence.Settings.DisableAutoMode = "disable"
	evidence.Settings.Permissions.DisableBypassPermissionsMode = "disable"
	evidence.Daemon.Stable = true
	evidence.Daemon.EntriesBefore = 1
	evidence.Daemon.EntriesAfter = 1
	evidence.Daemon.EntryBefore = maestromcp.DaemonEntry{StoreID: "store-a", DBPath: filepath.Join(dir, "maestro.db")}
	evidence.ServerInfo.Meta.DBPath = filepath.Join(dir, "maestro.db")
	evidence.ServerInfo.Meta.StoreID = "store-a"
	evidence.LiveSessionSeen = true

	if err := writeEvidence(prefix, evidence); err != nil {
		t.Fatalf("writeEvidence() error = %v", err)
	}

	jsonBytes, err := os.ReadFile(prefix + ".json")
	if err != nil {
		t.Fatalf("read JSON evidence: %v", err)
	}
	if !strings.Contains(string(jsonBytes), `"allowed_tools": "Bash,Edit,Write,MultiEdit"`) {
		t.Fatalf("JSON evidence missing allowed tools: %s", string(jsonBytes))
	}

	summaryBytes, err := os.ReadFile(prefix + ".summary.txt")
	if err != nil {
		t.Fatalf("read summary evidence: %v", err)
	}
	summary := string(summaryBytes)
	for _, want := range []string{
		"allowed_tools=Bash,Edit,Write,MultiEdit",
		"daemon_entry_stable=true",
		"permission_mode=default",
		"permission_prompt_tool=<none>",
		"tool_call_server_info=ok",
		"tool_names=create_issue,server_info",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary evidence missing %q: %s", want, summary)
		}
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type testStringer string

func (s testStringer) String() string {
	return string(s)
}
