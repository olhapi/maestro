package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/agentruntime"
	maestromcp "github.com/olhapi/maestro/internal/mcp"
)

type options struct {
	mcpConfig            string
	settings             string
	dbPath               string
	registryDir          string
	evidencePrefix       string
	allowedTools         string
	permissionPromptTool string
	permissionMode       string
	strictMCPConfig      string
}

type mcpConfigFile struct {
	MCPServers map[string]mcpServerConfig `json:"mcpServers"`
}

type mcpServerConfig struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type claudeSettingsOverlay struct {
	DisableAutoMode        string `json:"disableAutoMode"`
	UseAutoModeDuringPlan  bool   `json:"useAutoModeDuringPlan"`
	DisableAllHooks        bool   `json:"disableAllHooks"`
	IncludeGitInstructions bool   `json:"includeGitInstructions"`
	Permissions            struct {
		DisableBypassPermissionsMode string `json:"disableBypassPermissionsMode"`
	} `json:"permissions"`
}

type responseEnvelope struct {
	OK    bool                 `json:"ok"`
	Tool  string               `json:"tool"`
	Meta  responseEnvelopeMeta `json:"meta"`
	Data  json.RawMessage      `json:"data"`
	Error *responseError       `json:"error,omitempty"`
}

type responseEnvelopeMeta struct {
	DBPath           string `json:"db_path"`
	StoreID          string `json:"store_id"`
	ServerInstanceID string `json:"server_instance_id"`
	ChangeSeq        int64  `json:"change_seq"`
}

type responseError struct {
	Message string `json:"message"`
}

type serverInfoData struct {
	ProjectCount     int  `json:"project_count"`
	IssueCount       int  `json:"issue_count"`
	RuntimeAvailable bool `json:"runtime_available"`
}

type listIssuesData struct {
	Items      []interface{} `json:"items"`
	Total      int           `json:"total"`
	Limit      int           `json:"limit"`
	Offset     int           `json:"offset"`
	Pagination interface{}   `json:"pagination"`
}

type probeEvidence struct {
	Bridge struct {
		ServerName           string   `json:"server_name"`
		Command              string   `json:"command"`
		Args                 []string `json:"args"`
		DBPath               string   `json:"db_path"`
		StrictMCPConfig      bool     `json:"strict_mcp_config"`
		AllowedTools         string   `json:"allowed_tools"`
		PermissionPromptTool string   `json:"permission_prompt_tool"`
		PermissionMode       string   `json:"permission_mode"`
		ToolNames            []string `json:"tool_names"`
	} `json:"bridge"`
	Settings claudeSettingsOverlay `json:"settings"`
	Daemon   struct {
		EntriesBefore int                    `json:"entries_before"`
		EntriesAfter  int                    `json:"entries_after"`
		Stable        bool                   `json:"stable"`
		EntryBefore   maestromcp.DaemonEntry `json:"entry_before"`
		EntryAfter    maestromcp.DaemonEntry `json:"entry_after"`
	} `json:"daemon"`
	ServerInfo struct {
		Meta responseEnvelopeMeta `json:"meta"`
		Data serverInfoData       `json:"data"`
	} `json:"server_info"`
	ListIssues struct {
		Total int `json:"total"`
	} `json:"list_issues"`
	RuntimeSnapshot map[string]interface{} `json:"runtime_snapshot"`
	LiveSessionSeen bool                   `json:"live_session_seen"`
	LiveSessionKey  string                 `json:"live_session_key,omitempty"`
	LiveSession     agentruntime.Session   `json:"live_session,omitempty"`
}

func main() {
	var opts options
	flag.StringVar(&opts.mcpConfig, "mcp-config", "", "Path to the generated Claude MCP config")
	flag.StringVar(&opts.settings, "settings", "", "Path to the generated Claude settings overlay")
	flag.StringVar(&opts.dbPath, "db", "", "Expected Maestro database path")
	flag.StringVar(&opts.registryDir, "registry-dir", "", "Daemon registry directory")
	flag.StringVar(&opts.evidencePrefix, "evidence-prefix", "", "Prefix for emitted evidence files")
	flag.StringVar(&opts.allowedTools, "allowed-tools", "", "Allowed built-in tools passed to Claude")
	flag.StringVar(&opts.permissionPromptTool, "permission-prompt-tool", "", "Permission prompt tool passed to Claude")
	flag.StringVar(&opts.permissionMode, "permission-mode", "", "Permission mode passed to Claude")
	flag.StringVar(&opts.strictMCPConfig, "strict-mcp-config", "", "Whether --strict-mcp-config was present")
	flag.Parse()

	if err := run(opts); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(opts options) error {
	if strings.TrimSpace(opts.mcpConfig) == "" {
		return fmt.Errorf("mcp-config is required")
	}
	if strings.TrimSpace(opts.settings) == "" {
		return fmt.Errorf("settings is required")
	}
	if strings.TrimSpace(opts.dbPath) == "" {
		return fmt.Errorf("db is required")
	}
	if strings.TrimSpace(opts.registryDir) == "" {
		return fmt.Errorf("registry-dir is required")
	}
	if strings.TrimSpace(opts.evidencePrefix) == "" {
		return fmt.Errorf("evidence-prefix is required")
	}

	expectedDBPath, err := filepath.Abs(opts.dbPath)
	if err != nil {
		return fmt.Errorf("resolve db path: %w", err)
	}

	configBytes, serverName, server, bridgeDBPath, err := loadBridgeConfig(opts.mcpConfig)
	if err != nil {
		return err
	}
	if filepath.Clean(bridgeDBPath) != filepath.Clean(expectedDBPath) {
		return fmt.Errorf("bridge db path mismatch: expected %s, got %s", expectedDBPath, bridgeDBPath)
	}

	settingsBytes, settings, err := loadSettingsOverlay(opts.settings)
	if err != nil {
		return err
	}
	if err := validateSettings(settings); err != nil {
		return err
	}
	if err := validatePermissionFlags(opts); err != nil {
		return err
	}

	if err := os.WriteFile(opts.evidencePrefix+".mcp.json", configBytes, 0o600); err != nil {
		return fmt.Errorf("write MCP evidence: %w", err)
	}
	if err := os.WriteFile(opts.evidencePrefix+".settings.json", settingsBytes, 0o600); err != nil {
		return fmt.Errorf("write settings evidence: %w", err)
	}

	entriesBefore, err := loadDaemonEntries(opts.registryDir)
	if err != nil {
		return err
	}
	if len(entriesBefore) != 1 {
		return fmt.Errorf("expected exactly one daemon registry entry before bridge probe, got %d", len(entriesBefore))
	}
	entryBefore := entriesBefore[0]
	if filepath.Clean(entryBefore.DBPath) != filepath.Clean(expectedDBPath) {
		return fmt.Errorf("daemon registry db path mismatch: expected %s, got %s", expectedDBPath, entryBefore.DBPath)
	}

	evidence := probeEvidence{}
	evidence.Bridge.ServerName = serverName
	evidence.Bridge.Command = server.Command
	evidence.Bridge.Args = append([]string(nil), server.Args...)
	evidence.Bridge.DBPath = bridgeDBPath
	evidence.Bridge.StrictMCPConfig = strings.EqualFold(strings.TrimSpace(opts.strictMCPConfig), "true")
	evidence.Bridge.AllowedTools = strings.TrimSpace(opts.allowedTools)
	evidence.Bridge.PermissionMode = strings.TrimSpace(opts.permissionMode)
	evidence.Bridge.PermissionPromptTool = firstNonEmpty(strings.TrimSpace(opts.permissionPromptTool), "<none>")
	evidence.Settings = settings
	evidence.Daemon.EntriesBefore = len(entriesBefore)
	evidence.Daemon.EntryBefore = entryBefore

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := mcpclient.NewStdioMCPClient(server.Command, os.Environ(), server.Args...)
	if err != nil {
		return fmt.Errorf("start stdio MCP client: %w", err)
	}
	defer client.Close()

	if _, err := client.Initialize(ctx, mcpapi.InitializeRequest{
		Params: mcpapi.InitializeParams{
			ProtocolVersion: mcpapi.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpapi.Implementation{Name: "maestro-claude-e2e-probe", Version: "1.0.0"},
			Capabilities:    mcpapi.ClientCapabilities{},
		},
	}); err != nil {
		return fmt.Errorf("initialize stdio MCP client: %w", err)
	}

	tools, err := client.ListTools(ctx, mcpapi.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	toolNames := sortedToolNames(tools.Tools)
	evidence.Bridge.ToolNames = toolNames
	for _, want := range []string{"server_info", "create_issue", "list_issues", "get_runtime_snapshot", "list_sessions"} {
		if !containsString(toolNames, want) {
			return fmt.Errorf("expected tool %q in bridge surface, got %s", want, strings.Join(toolNames, ","))
		}
	}

	serverInfoEnvelope, err := callToolEnvelope(ctx, client, "server_info", map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("call server_info: %w", err)
	}
	var serverInfo serverInfoData
	if err := decodeData(serverInfoEnvelope, &serverInfo); err != nil {
		return fmt.Errorf("decode server_info: %w", err)
	}
	if !serverInfo.RuntimeAvailable {
		return errors.New("server_info reported runtime_available=false")
	}
	if filepath.Clean(serverInfoEnvelope.Meta.DBPath) != filepath.Clean(expectedDBPath) {
		return fmt.Errorf("server_info db path mismatch: expected %s, got %s", expectedDBPath, serverInfoEnvelope.Meta.DBPath)
	}
	if serverInfoEnvelope.Meta.StoreID != entryBefore.StoreID {
		return fmt.Errorf("server_info store id mismatch: expected %s, got %s", entryBefore.StoreID, serverInfoEnvelope.Meta.StoreID)
	}
	evidence.ServerInfo.Meta = serverInfoEnvelope.Meta
	evidence.ServerInfo.Data = serverInfo

	listIssuesEnvelope, err := callToolEnvelope(ctx, client, "list_issues", map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("call list_issues: %w", err)
	}
	var listIssues listIssuesData
	if err := decodeData(listIssuesEnvelope, &listIssues); err != nil {
		return fmt.Errorf("decode list_issues: %w", err)
	}
	evidence.ListIssues.Total = listIssues.Total

	runtimeSnapshotEnvelope, err := callToolEnvelope(ctx, client, "get_runtime_snapshot", map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("call get_runtime_snapshot: %w", err)
	}
	var runtimeSnapshot map[string]interface{}
	if err := decodeData(runtimeSnapshotEnvelope, &runtimeSnapshot); err != nil {
		return fmt.Errorf("decode get_runtime_snapshot: %w", err)
	}
	evidence.RuntimeSnapshot = runtimeSnapshot

	sessionKey, liveSession, err := waitForClaudeSession(ctx, client)
	if err != nil {
		return err
	}
	evidence.LiveSessionSeen = true
	evidence.LiveSessionKey = sessionKey
	evidence.LiveSession = liveSession

	entriesAfter, err := loadDaemonEntries(opts.registryDir)
	if err != nil {
		return err
	}
	if len(entriesAfter) != 1 {
		return fmt.Errorf("expected exactly one daemon registry entry after bridge probe, got %d", len(entriesAfter))
	}
	entryAfter := entriesAfter[0]
	if filepath.Clean(entryAfter.DBPath) != filepath.Clean(expectedDBPath) {
		return fmt.Errorf("daemon registry db path drifted: expected %s, got %s", expectedDBPath, entryAfter.DBPath)
	}
	evidence.Daemon.EntriesAfter = len(entriesAfter)
	evidence.Daemon.EntryAfter = entryAfter
	evidence.Daemon.Stable = sameDaemonEntry(entryBefore, entryAfter)
	if !evidence.Daemon.Stable {
		return errors.New("daemon registry entry changed during Claude bridge probe")
	}

	if err := writeEvidence(opts.evidencePrefix, evidence); err != nil {
		return err
	}
	return nil
}

func loadBridgeConfig(path string) ([]byte, string, mcpServerConfig, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", mcpServerConfig{}, "", fmt.Errorf("read MCP config: %w", err)
	}
	var cfg mcpConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, "", mcpServerConfig{}, "", fmt.Errorf("decode MCP config: %w", err)
	}
	if len(cfg.MCPServers) != 1 {
		return nil, "", mcpServerConfig{}, "", fmt.Errorf("expected exactly one MCP server in config, got %d", len(cfg.MCPServers))
	}
	server, ok := cfg.MCPServers["maestro"]
	if !ok {
		return nil, "", mcpServerConfig{}, "", errors.New(`expected "maestro" MCP server in config`)
	}
	if strings.TrimSpace(server.Type) != "stdio" {
		return nil, "", mcpServerConfig{}, "", fmt.Errorf("expected maestro MCP server type stdio, got %q", server.Type)
	}
	if strings.TrimSpace(server.Command) != "maestro" {
		return nil, "", mcpServerConfig{}, "", fmt.Errorf("expected maestro MCP command, got %q", server.Command)
	}
	bridgeDBPath := extractDBPathArg(server.Args)
	if bridgeDBPath == "" {
		return nil, "", mcpServerConfig{}, "", errors.New("expected --db in maestro MCP args")
	}
	bridgeDBPath, err = filepath.Abs(bridgeDBPath)
	if err != nil {
		return nil, "", mcpServerConfig{}, "", fmt.Errorf("resolve bridge db path: %w", err)
	}
	if len(server.Args) < 3 || strings.TrimSpace(server.Args[0]) != "mcp" {
		return nil, "", mcpServerConfig{}, "", fmt.Errorf("expected maestro MCP args to start with \"mcp --db\", got %q", strings.Join(server.Args, " "))
	}
	return data, "maestro", server, bridgeDBPath, nil
}

func loadSettingsOverlay(path string) ([]byte, claudeSettingsOverlay, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, claudeSettingsOverlay{}, fmt.Errorf("read settings overlay: %w", err)
	}
	var overlay claudeSettingsOverlay
	if err := json.Unmarshal(data, &overlay); err != nil {
		return nil, claudeSettingsOverlay{}, fmt.Errorf("decode settings overlay: %w", err)
	}
	return data, overlay, nil
}

func validateSettings(settings claudeSettingsOverlay) error {
	switch {
	case strings.TrimSpace(settings.DisableAutoMode) != "disable":
		return fmt.Errorf("expected disableAutoMode=disable, got %q", settings.DisableAutoMode)
	case settings.UseAutoModeDuringPlan:
		return errors.New("expected useAutoModeDuringPlan=false")
	case !settings.DisableAllHooks:
		return errors.New("expected disableAllHooks=true")
	case settings.IncludeGitInstructions:
		return errors.New("expected includeGitInstructions=false")
	case strings.TrimSpace(settings.Permissions.DisableBypassPermissionsMode) != "disable":
		return fmt.Errorf("expected permissions.disableBypassPermissionsMode=disable, got %q", settings.Permissions.DisableBypassPermissionsMode)
	default:
		return nil
	}
}

func validatePermissionFlags(opts options) error {
	if strings.TrimSpace(opts.permissionMode) != "default" {
		return fmt.Errorf("expected Claude permission mode default, got %q", opts.permissionMode)
	}
	if strings.TrimSpace(opts.allowedTools) != "Bash,Edit,Write,MultiEdit" {
		return fmt.Errorf("expected allowed tools Bash,Edit,Write,MultiEdit, got %q", opts.allowedTools)
	}
	if strings.TrimSpace(opts.permissionPromptTool) != "" {
		return fmt.Errorf("expected no permission prompt tool for full-access session, got %q", opts.permissionPromptTool)
	}
	if !strings.EqualFold(strings.TrimSpace(opts.strictMCPConfig), "true") {
		return fmt.Errorf("expected strict-mcp-config=true, got %q", opts.strictMCPConfig)
	}
	return nil
}

func loadDaemonEntries(dir string) ([]maestromcp.DaemonEntry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read daemon registry dir: %w", err)
	}
	entries := make([]maestromcp.DaemonEntry, 0, len(items))
	for _, item := range items {
		if item.IsDir() || filepath.Ext(item.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, fmt.Errorf("read daemon registry entry %s: %w", item.Name(), err)
		}
		var entry maestromcp.DaemonEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, fmt.Errorf("decode daemon registry entry %s: %w", item.Name(), err)
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].StoreID < entries[j].StoreID
	})
	return entries, nil
}

func extractDBPathArg(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
	}
	return ""
}

func callToolEnvelope(ctx context.Context, client *mcpclient.Client, name string, arguments map[string]interface{}) (*responseEnvelope, error) {
	result, err := client.CallTool(ctx, mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{
			Name:      name,
			Arguments: arguments,
		},
	})
	if err != nil {
		return nil, err
	}
	if result == nil || len(result.Content) == 0 {
		return nil, fmt.Errorf("%s returned no content", name)
	}
	text, ok := result.Content[0].(mcpapi.TextContent)
	if !ok {
		return nil, fmt.Errorf("%s returned unexpected content type %T", name, result.Content[0])
	}
	var envelope responseEnvelope
	if err := json.Unmarshal([]byte(text.Text), &envelope); err != nil {
		return nil, fmt.Errorf("%s response decode: %w", name, err)
	}
	if !envelope.OK {
		message := "unknown error"
		if envelope.Error != nil && strings.TrimSpace(envelope.Error.Message) != "" {
			message = envelope.Error.Message
		}
		return nil, fmt.Errorf("%s failed: %s", name, message)
	}
	return &envelope, nil
}

func decodeData(envelope *responseEnvelope, target interface{}) error {
	if envelope == nil {
		return errors.New("missing response envelope")
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("missing response envelope data")
	}
	return json.Unmarshal(envelope.Data, target)
}

func sortedToolNames(tools []mcpapi.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func waitForClaudeSession(ctx context.Context, client *mcpclient.Client) (string, agentruntime.Session, error) {
	deadline := time.Now().Add(12 * time.Second)
	for {
		envelope, err := callToolEnvelope(ctx, client, "list_sessions", map[string]interface{}{})
		if err == nil {
			raw := map[string]interface{}{}
			if decodeErr := decodeData(envelope, &raw); decodeErr == nil {
				if sessionsRaw, ok := raw["sessions"].(map[string]interface{}); ok {
					for key, session := range agentruntime.SessionsFromMap(sessionsRaw) {
						if strings.TrimSpace(asString(session.Metadata["provider"])) != "claude" {
							continue
						}
						if strings.TrimSpace(asString(session.Metadata["transport"])) != "stdio" {
							continue
						}
						if strings.TrimSpace(session.ThreadID) == "" || strings.TrimSpace(session.SessionID) == "" {
							continue
						}
						return key, session, nil
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return "", agentruntime.Session{}, errors.New("did not observe a live Claude session through list_sessions")
		}
		select {
		case <-ctx.Done():
			return "", agentruntime.Session{}, errors.New("did not observe a live Claude session before context deadline")
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func sameDaemonEntry(a, b maestromcp.DaemonEntry) bool {
	return a.StoreID == b.StoreID &&
		filepath.Clean(a.DBPath) == filepath.Clean(b.DBPath) &&
		a.PID == b.PID &&
		a.BaseURL == b.BaseURL &&
		a.BearerToken == b.BearerToken &&
		a.Version == b.Version &&
		a.Transport == b.Transport
}

func writeEvidence(prefix string, evidence probeEvidence) error {
	jsonPath := prefix + ".json"
	summaryPath := prefix + ".summary.txt"

	body, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON evidence: %w", err)
	}
	if err := os.WriteFile(jsonPath, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("write JSON evidence: %w", err)
	}

	lines := []string{
		"allowed_tools=" + evidence.Bridge.AllowedTools,
		fmt.Sprintf("bridge_db_path=%s", evidence.Bridge.DBPath),
		fmt.Sprintf("daemon_db_path=%s", evidence.Daemon.EntryBefore.DBPath),
		fmt.Sprintf("daemon_entry_stable=%t", evidence.Daemon.Stable),
		fmt.Sprintf("daemon_registry_entries_after=%d", evidence.Daemon.EntriesAfter),
		fmt.Sprintf("daemon_registry_entries_before=%d", evidence.Daemon.EntriesBefore),
		fmt.Sprintf("daemon_store_id=%s", evidence.Daemon.EntryBefore.StoreID),
		"expected_tools_present=true",
		fmt.Sprintf("live_claude_session_seen=%t", evidence.LiveSessionSeen),
		"permission_mode=" + evidence.Bridge.PermissionMode,
		"permission_prompt_tool=" + evidence.Bridge.PermissionPromptTool,
		fmt.Sprintf("server_db_path=%s", evidence.ServerInfo.Meta.DBPath),
		fmt.Sprintf("server_store_id=%s", evidence.ServerInfo.Meta.StoreID),
		fmt.Sprintf("settings_disable_all_hooks=%t", evidence.Settings.DisableAllHooks),
		"settings_disable_auto_mode=" + evidence.Settings.DisableAutoMode,
		"settings_disable_bypass_permissions_mode=" + evidence.Settings.Permissions.DisableBypassPermissionsMode,
		fmt.Sprintf("settings_include_git_instructions=%t", evidence.Settings.IncludeGitInstructions),
		fmt.Sprintf("settings_use_auto_mode_during_plan=%t", evidence.Settings.UseAutoModeDuringPlan),
		fmt.Sprintf("strict_mcp_config=%t", evidence.Bridge.StrictMCPConfig),
		"tool_call_get_runtime_snapshot=ok",
		"tool_call_list_issues=ok",
		"tool_call_list_sessions=ok",
		"tool_call_server_info=ok",
		"tool_names=" + strings.Join(evidence.Bridge.ToolNames, ","),
	}
	if err := os.WriteFile(summaryPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return fmt.Errorf("write summary evidence: %w", err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func asString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}
