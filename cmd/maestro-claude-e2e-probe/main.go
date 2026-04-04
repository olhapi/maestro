package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
	maestromcp "github.com/olhapi/maestro/internal/mcp"
)

type options struct {
	mode                  string
	issueIdentifier       string
	streamMarker          string
	mcpConfig             string
	settings              string
	dbPath                string
	registryDir           string
	evidencePrefix        string
	allowedTools          string
	permissionPromptTool  string
	permissionMode        string
	strictMCPConfig       string
	interruptApprovalType string
	interruptKind         string
	interruptAction       string
	interruptAlertCode    string
	interruptClass        string
	interruptToolName     string
	interruptPlanStatus   string
	interruptPlanVersion  int
	interruptDecision     string
	interruptNote         string
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

type executionObservation struct {
	Active                  bool                      `json:"active"`
	SessionSource           string                    `json:"session_source,omitempty"`
	FailureClass            string                    `json:"failure_class,omitempty"`
	StopReason              string                    `json:"stop_reason,omitempty"`
	RuntimeName             string                    `json:"runtime_name,omitempty"`
	RuntimeProvider         string                    `json:"runtime_provider,omitempty"`
	RuntimeTransport        string                    `json:"runtime_transport,omitempty"`
	RuntimeAuthSource       string                    `json:"runtime_auth_source,omitempty"`
	PendingInteractionState string                    `json:"pending_interaction_state,omitempty"`
	StreamMarker            string                    `json:"stream_marker,omitempty"`
	StreamSeen              bool                      `json:"stream_seen"`
	Session                 agentruntime.Session      `json:"session,omitempty"`
	WorkspaceRecovery       *kanban.WorkspaceRecovery `json:"workspace_recovery,omitempty"`
}

type runtimeEventsData struct {
	Events []kanban.RuntimeEvent `json:"events"`
}

type dashboardSessionObservation struct {
	Status                  string `json:"status,omitempty"`
	StopReason              string `json:"stop_reason,omitempty"`
	Source                  string `json:"source,omitempty"`
	FailureClass            string `json:"failure_class,omitempty"`
	RuntimeName             string `json:"runtime_name,omitempty"`
	RuntimeProvider         string `json:"runtime_provider,omitempty"`
	RuntimeTransport        string `json:"runtime_transport,omitempty"`
	RuntimeAuthSource       string `json:"runtime_auth_source,omitempty"`
	PendingInteractionState string `json:"pending_interaction_state,omitempty"`
}

type interruptObservation struct {
	Requested        bool                            `json:"requested"`
	PendingCount     int                             `json:"pending_count,omitempty"`
	Matched          bool                            `json:"matched"`
	Action           string                          `json:"action,omitempty"`
	ResponseStatus   string                          `json:"response_status,omitempty"`
	ResponseDecision string                          `json:"response_decision,omitempty"`
	Cleared          bool                            `json:"cleared"`
	Interaction      agentruntime.PendingInteraction `json:"interaction,omitempty"`
}

type issueObservation struct {
	State                     string `json:"state,omitempty"`
	PermissionProfile         string `json:"permission_profile,omitempty"`
	CollaborationModeOverride string `json:"collaboration_mode_override,omitempty"`
	PlanApprovalPending       bool   `json:"plan_approval_pending"`
	PendingPlanRevisionNote   string `json:"pending_plan_revision_note,omitempty"`
}

type planningObservation struct {
	Present                    bool   `json:"present"`
	SessionID                  string `json:"session_id,omitempty"`
	Status                     string `json:"status,omitempty"`
	VersionCount               int    `json:"version_count,omitempty"`
	CurrentVersionNumber       int    `json:"current_version_number,omitempty"`
	CurrentVersionRevisionNote string `json:"current_version_revision_note,omitempty"`
	CurrentVersionThreadID     string `json:"current_version_thread_id,omitempty"`
	CurrentVersionTurnID       string `json:"current_version_turn_id,omitempty"`
	PendingRevisionNote        string `json:"pending_revision_note,omitempty"`
}

type probeEvidence struct {
	Mode            string `json:"mode,omitempty"`
	IssueIdentifier string `json:"issue_identifier,omitempty"`
	Bridge          struct {
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
	RuntimeEvents   struct {
		Items []kanban.RuntimeEvent `json:"items,omitempty"`
	} `json:"runtime_events"`
	Execution        executionObservation        `json:"execution"`
	DashboardSession dashboardSessionObservation `json:"dashboard_session"`
	Interrupt        interruptObservation        `json:"interrupt"`
	Issue            issueObservation            `json:"issue"`
	Planning         planningObservation         `json:"planning"`
	LiveSessionSeen  bool                        `json:"live_session_seen"`
	LiveSessionKey   string                      `json:"live_session_key,omitempty"`
	LiveSession      agentruntime.Session        `json:"live_session,omitempty"`
}

func main() {
	var opts options
	flag.StringVar(&opts.mode, "mode", "live", "Probe mode: live or final")
	flag.StringVar(&opts.issueIdentifier, "issue-identifier", "", "Issue identifier to inspect")
	flag.StringVar(&opts.streamMarker, "stream-marker", "", "Expected live stream marker")
	flag.StringVar(&opts.mcpConfig, "mcp-config", "", "Path to the generated Claude MCP config")
	flag.StringVar(&opts.settings, "settings", "", "Path to the generated Claude settings overlay")
	flag.StringVar(&opts.dbPath, "db", "", "Expected Maestro database path")
	flag.StringVar(&opts.registryDir, "registry-dir", "", "Daemon registry directory")
	flag.StringVar(&opts.evidencePrefix, "evidence-prefix", "", "Prefix for emitted evidence files")
	flag.StringVar(&opts.allowedTools, "allowed-tools", "", "Allowed built-in tools passed to Claude")
	flag.StringVar(&opts.permissionPromptTool, "permission-prompt-tool", "", "Permission prompt tool passed to Claude")
	flag.StringVar(&opts.permissionMode, "permission-mode", "", "Permission mode passed to Claude")
	flag.StringVar(&opts.strictMCPConfig, "strict-mcp-config", "", "Whether --strict-mcp-config was present")
	flag.StringVar(&opts.interruptApprovalType, "interrupt-approval-type", "", "Expected interrupt approval type: claude_permission_prompt or plan_approval")
	flag.StringVar(&opts.interruptKind, "interrupt-kind", "", "Expected interrupt kind: approval or alert")
	flag.StringVar(&opts.interruptAction, "interrupt-action", "", "Interrupt action to invoke: respond or acknowledge")
	flag.StringVar(&opts.interruptAlertCode, "interrupt-alert-code", "", "Expected alert code on a pending alert interrupt")
	flag.StringVar(&opts.interruptClass, "interrupt-classification", "", "Expected pending interrupt classification")
	flag.StringVar(&opts.interruptToolName, "interrupt-tool-name", "", "Expected pending interrupt tool name")
	flag.StringVar(&opts.interruptPlanStatus, "interrupt-plan-status", "", "Expected pending plan approval status")
	flag.IntVar(&opts.interruptPlanVersion, "interrupt-plan-version", 0, "Expected pending plan approval version number")
	flag.StringVar(&opts.interruptDecision, "interrupt-decision", "", "Decision to post back to the pending interrupt")
	flag.StringVar(&opts.interruptNote, "interrupt-note", "", "Optional note to include with the pending interrupt response")
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
	evidence.Mode = strings.TrimSpace(opts.mode)
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

	// Give the live bridge probe enough time to observe the active Claude session.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	for _, want := range []string{"server_info", "create_issue", "list_issues", "get_issue_execution", "get_runtime_snapshot", "list_runtime_events", "list_sessions", "approval_prompt"} {
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
	issueIdentifier := resolveIssueIdentifier(strings.TrimSpace(opts.issueIdentifier), listIssues)
	if issueIdentifier == "" {
		return errors.New("did not find an issue identifier to inspect from list_issues")
	}
	evidence.IssueIdentifier = issueIdentifier

	runtimeSnapshotEnvelope, err := callToolEnvelope(ctx, client, "get_runtime_snapshot", map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("call get_runtime_snapshot: %w", err)
	}
	var runtimeSnapshot map[string]interface{}
	if err := decodeData(runtimeSnapshotEnvelope, &runtimeSnapshot); err != nil {
		return fmt.Errorf("decode get_runtime_snapshot: %w", err)
	}
	evidence.RuntimeSnapshot = runtimeSnapshot

	if _, err := callToolEnvelope(ctx, client, "get_issue_execution", map[string]interface{}{
		"identifier": issueIdentifier,
	}); err != nil {
		return fmt.Errorf("call get_issue_execution: %w", err)
	}
	if _, err := callToolEnvelope(ctx, client, "list_sessions", map[string]interface{}{}); err != nil {
		return fmt.Errorf("call list_sessions: %w", err)
	}

	interruptOnlyMode := strings.EqualFold(strings.TrimSpace(opts.mode), "interrupt")
	if !interruptOnlyMode {
		execution, err := waitForIssueExecutionObservation(entryBefore, issueIdentifier, strings.TrimSpace(opts.mode), strings.TrimSpace(opts.streamMarker))
		if err != nil {
			return err
		}
		evidence.Execution = execution

		dashboardSession, err := waitForDashboardSessionObservation(ctx, entryBefore, issueIdentifier, strings.TrimSpace(opts.mode))
		if err != nil {
			return err
		}
		evidence.DashboardSession = dashboardSession
	}

	if strings.EqualFold(strings.TrimSpace(opts.mode), "live") {
		sessionKey, liveSession, err := waitForClaudeSession(ctx, client)
		if err != nil {
			return err
		}
		evidence.LiveSessionSeen = true
		evidence.LiveSessionKey = sessionKey
		evidence.LiveSession = liveSession
	}

	if wantsInterruptObservation(opts) {
		interrupt, pendingCount, err := waitForPendingInterrupt(
			ctx,
			entryBefore,
			issueIdentifier,
			strings.TrimSpace(opts.interruptKind),
			strings.TrimSpace(opts.interruptClass),
			strings.TrimSpace(opts.interruptToolName),
		)
		if err != nil {
			return err
		}
		if err := validatePendingInterrupt(
			interrupt,
			issueIdentifier,
			strings.TrimSpace(opts.interruptKind),
			strings.TrimSpace(opts.interruptApprovalType),
			strings.TrimSpace(opts.interruptAlertCode),
			strings.TrimSpace(opts.interruptClass),
			strings.TrimSpace(opts.interruptToolName),
			strings.TrimSpace(opts.interruptPlanStatus),
			opts.interruptPlanVersion,
		); err != nil {
			return err
		}
		evidence.Interrupt.Requested = true
		evidence.Interrupt.PendingCount = pendingCount
		evidence.Interrupt.Matched = true
		evidence.Interrupt.Interaction = interrupt
		evidence.Interrupt.Action = strings.TrimSpace(opts.interruptAction)
		if !interruptOnlyMode {
			expectedState := pendingInteractionStateForInterrupt(interrupt)
			if expectedState != "" {
				execution, dashboardSession, err := waitForPendingInteractionSurfaceObservation(
					ctx,
					entryBefore,
					issueIdentifier,
					strings.TrimSpace(opts.mode),
					strings.TrimSpace(evidence.Execution.StreamMarker),
					expectedState,
				)
				if err != nil {
					return err
				}
				evidence.Execution = execution
				evidence.DashboardSession = dashboardSession
			}
		}
		decision := strings.ToLower(strings.TrimSpace(opts.interruptDecision))
		note := strings.TrimSpace(opts.interruptNote)
		action := strings.TrimSpace(opts.interruptAction)
		if action == "" && (decision != "" || note != "") {
			action = "respond"
		}
		switch action {
		case "":
		case "respond":
			status, err := respondToPendingInterrupt(entryBefore, interrupt.ID, agentruntime.PendingInteractionResponse{
				Decision: decision,
				Note:     note,
			})
			if err != nil {
				return err
			}
			evidence.Interrupt.ResponseStatus = status
			evidence.Interrupt.ResponseDecision = decision
			if err := waitForPendingInterruptClear(ctx, entryBefore, strings.TrimSpace(interrupt.ID)); err != nil {
				return err
			}
			evidence.Interrupt.Cleared = true
		case "acknowledge":
			status, err := acknowledgePendingInterrupt(entryBefore, interrupt.ID)
			if err != nil {
				return err
			}
			evidence.Interrupt.ResponseStatus = status
			if err := waitForPendingInterruptClear(ctx, entryBefore, strings.TrimSpace(interrupt.ID)); err != nil {
				return err
			}
			evidence.Interrupt.Cleared = true
		default:
			return fmt.Errorf("unsupported interrupt action %q", action)
		}
	}

	runtimeEventsEnvelope, err := callToolEnvelope(ctx, client, "list_runtime_events", map[string]interface{}{"limit": 200})
	if err != nil {
		return fmt.Errorf("call list_runtime_events: %w", err)
	}
	var runtimeEvents runtimeEventsData
	if err := decodeData(runtimeEventsEnvelope, &runtimeEvents); err != nil {
		return fmt.Errorf("decode list_runtime_events: %w", err)
	}
	evidence.RuntimeEvents.Items = filterRuntimeEventsByIssue(runtimeEvents.Events, issueIdentifier)

	issueEvidence, planningEvidence, err := loadIssuePlanningEvidence(expectedDBPath, issueIdentifier)
	if err != nil {
		return err
	}
	evidence.Issue = issueEvidence
	evidence.Planning = planningEvidence

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
	switch mode := strings.TrimSpace(opts.permissionMode); mode {
	case "default", "plan":
	default:
		return fmt.Errorf("expected Claude permission mode default or plan, got %q", opts.permissionMode)
	}
	if !strings.EqualFold(strings.TrimSpace(opts.strictMCPConfig), "true") {
		return fmt.Errorf("expected strict-mcp-config=true, got %q", opts.strictMCPConfig)
	}
	switch promptTool := strings.TrimSpace(opts.permissionPromptTool); promptTool {
	case "":
		if strings.TrimSpace(opts.allowedTools) != "Bash,Edit,Write,MultiEdit" {
			return fmt.Errorf("expected allowed tools Bash,Edit,Write,MultiEdit, got %q", opts.allowedTools)
		}
	case "mcp__maestro__approval_prompt":
		if strings.TrimSpace(opts.allowedTools) != "" {
			return fmt.Errorf("expected no allowed-tools when using the Maestro approval prompt, got %q", opts.allowedTools)
		}
	default:
		return fmt.Errorf("expected supported permission prompt tool, got %q", opts.permissionPromptTool)
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
	deadline := time.Now().Add(60 * time.Second)
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

func resolveIssueIdentifier(requested string, listIssues listIssuesData) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	for _, raw := range listIssues.Items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		identifier := strings.TrimSpace(asString(item["identifier"]))
		if identifier == "" {
			continue
		}
		state := strings.TrimSpace(asString(item["state"]))
		if state == "ready" || state == "in_progress" {
			return identifier
		}
		if requested == "" {
			requested = identifier
		}
	}
	return requested
}

func waitForIssueExecutionObservation(entry maestromcp.DaemonEntry, issueIdentifier, mode, streamMarker string) (executionObservation, error) {
	deadline := time.Now().Add(60 * time.Second)
	liveMode := !strings.EqualFold(strings.TrimSpace(mode), "final")
	marker := strings.TrimSpace(streamMarker)
	if marker == "" {
		marker = "STREAM:" + issueIdentifier + ":"
	}
	for {
		observation, ok, err := issueExecutionObservationForIssue(entry, issueIdentifier, marker)
		if err == nil && ok && executionObservationMatchesMode(observation, mode) {
			return observation, nil
		}
		if time.Now().After(deadline) {
			if liveMode {
				return executionObservation{}, fmt.Errorf("did not observe live issue execution for %s", issueIdentifier)
			}
			return executionObservation{}, fmt.Errorf("did not observe persisted issue execution for %s", issueIdentifier)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func executionObservationMatchesMode(observation executionObservation, mode string) bool {
	liveMode := !strings.EqualFold(strings.TrimSpace(mode), "final")
	if liveMode {
		return observation.Active &&
			observation.SessionSource == "live" &&
			strings.TrimSpace(observation.Session.ThreadID) != "" &&
			strings.TrimSpace(observation.Session.SessionID) != "" &&
			observation.StreamSeen
	}
	if observation.Active || observation.SessionSource != "persisted" {
		return false
	}
	if strings.TrimSpace(observation.Session.ThreadID) != "" &&
		strings.TrimSpace(observation.Session.SessionID) != "" {
		return true
	}
	return observation.WorkspaceRecovery != nil || strings.TrimSpace(observation.FailureClass) != ""
}

func issueExecutionObservationForIssue(entry maestromcp.DaemonEntry, issueIdentifier, marker string) (executionObservation, bool, error) {
	body, statusCode, err := dashboardRequest(entry, http.MethodGet, "/api/v1/app/issues/"+strings.TrimSpace(issueIdentifier)+"/execution", nil)
	if err != nil {
		return executionObservation{}, false, err
	}
	if statusCode != http.StatusOK {
		return executionObservation{}, false, fmt.Errorf("issue execution returned %d: %s", statusCode, strings.TrimSpace(string(body)))
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return executionObservation{}, false, err
	}
	observation := executionObservation{
		Active:                  boolFromMap(raw, "active"),
		SessionSource:           strings.TrimSpace(asString(raw["session_source"])),
		FailureClass:            strings.TrimSpace(asString(raw["failure_class"])),
		StopReason:              strings.TrimSpace(asString(raw["stop_reason"])),
		RuntimeName:             strings.TrimSpace(asString(raw["runtime_name"])),
		RuntimeProvider:         strings.TrimSpace(asString(raw["runtime_provider"])),
		RuntimeTransport:        strings.TrimSpace(asString(raw["runtime_transport"])),
		RuntimeAuthSource:       strings.TrimSpace(asString(raw["runtime_auth_source"])),
		PendingInteractionState: strings.TrimSpace(asString(raw["pending_interaction_state"])),
		StreamMarker:            marker,
		WorkspaceRecovery:       workspaceRecoveryFromAny(raw["workspace_recovery"]),
	}
	if session, ok := agentruntime.SessionFromAny(raw["session"]); ok {
		observation.Session = session
	}
	if marker != "" && strings.Contains(observation.Session.LastMessage, marker) {
		observation.StreamSeen = true
	}
	return observation, true, nil
}

func waitForDashboardSessionObservation(ctx context.Context, entry maestromcp.DaemonEntry, issueIdentifier, mode string) (dashboardSessionObservation, error) {
	deadline := time.Now().Add(60 * time.Second)
	liveMode := !strings.EqualFold(strings.TrimSpace(mode), "final")
	for {
		observation, ok, err := dashboardSessionObservationForIssue(entry, issueIdentifier)
		if err == nil && ok {
			if liveMode {
				if observation.Source == "live" {
					return observation, nil
				}
			} else if observation.Source == "persisted" {
				return observation, nil
			}
		}
		if time.Now().After(deadline) {
			if liveMode {
				return dashboardSessionObservation{}, fmt.Errorf("did not observe live dashboard session for %s", issueIdentifier)
			}
			return dashboardSessionObservation{}, fmt.Errorf("did not observe persisted dashboard session for %s", issueIdentifier)
		}
		select {
		case <-ctx.Done():
			return dashboardSessionObservation{}, errors.New("did not observe dashboard session before context deadline")
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func dashboardSessionObservationForIssue(entry maestromcp.DaemonEntry, issueIdentifier string) (dashboardSessionObservation, bool, error) {
	body, statusCode, err := dashboardRequest(entry, http.MethodGet, "/api/v1/app/sessions", nil)
	if err != nil {
		return dashboardSessionObservation{}, false, err
	}
	if statusCode != http.StatusOK {
		return dashboardSessionObservation{}, false, fmt.Errorf("dashboard sessions returned %d: %s", statusCode, strings.TrimSpace(string(body)))
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return dashboardSessionObservation{}, false, err
	}
	rawEntries, _ := payload["entries"].([]interface{})
	for _, raw := range rawEntries {
		entryMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(entryMap["issue_identifier"])) != issueIdentifier {
			continue
		}
		return dashboardSessionObservation{
			Status:                  strings.TrimSpace(asString(entryMap["status"])),
			StopReason:              strings.TrimSpace(asString(entryMap["stop_reason"])),
			Source:                  strings.TrimSpace(asString(entryMap["source"])),
			FailureClass:            strings.TrimSpace(asString(entryMap["failure_class"])),
			RuntimeName:             strings.TrimSpace(asString(entryMap["runtime_name"])),
			RuntimeProvider:         strings.TrimSpace(asString(entryMap["runtime_provider"])),
			RuntimeTransport:        strings.TrimSpace(asString(entryMap["runtime_transport"])),
			RuntimeAuthSource:       strings.TrimSpace(asString(entryMap["runtime_auth_source"])),
			PendingInteractionState: strings.TrimSpace(asString(entryMap["pending_interaction_state"])),
		}, true, nil
	}
	return dashboardSessionObservation{}, false, nil
}

func waitForPendingInteractionSurfaceObservation(ctx context.Context, entry maestromcp.DaemonEntry, issueIdentifier, mode, streamMarker, expectedState string) (executionObservation, dashboardSessionObservation, error) {
	deadline := time.Now().Add(60 * time.Second)
	liveMode := !strings.EqualFold(strings.TrimSpace(mode), "final")
	expectedState = strings.TrimSpace(expectedState)
	for {
		execution, executionOK, executionErr := issueExecutionObservationForIssue(entry, issueIdentifier, streamMarker)
		dashboard, dashboardOK, dashboardErr := dashboardSessionObservationForIssue(entry, issueIdentifier)
		if executionErr == nil && executionOK && dashboardErr == nil && dashboardOK {
			executionSourceOK := execution.SessionSource == "persisted"
			dashboardSourceOK := dashboard.Source == "persisted"
			if liveMode {
				executionSourceOK = execution.SessionSource == "live"
				dashboardSourceOK = dashboard.Source == "live"
			}
			if executionSourceOK &&
				dashboardSourceOK &&
				strings.EqualFold(strings.TrimSpace(execution.PendingInteractionState), expectedState) &&
				strings.EqualFold(strings.TrimSpace(dashboard.PendingInteractionState), expectedState) {
				return execution, dashboard, nil
			}
		}
		if time.Now().After(deadline) {
			return executionObservation{}, dashboardSessionObservation{}, fmt.Errorf("did not observe pending interaction state %q for %s", expectedState, issueIdentifier)
		}
		select {
		case <-ctx.Done():
			return executionObservation{}, dashboardSessionObservation{}, errors.New("did not observe pending interaction surface before context deadline")
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func pendingInteractionStateForInterrupt(interaction agentruntime.PendingInteraction) string {
	switch interaction.Kind {
	case agentruntime.PendingInteractionKindApproval:
		return "approval"
	case agentruntime.PendingInteractionKindUserInput:
		return "user_input"
	case agentruntime.PendingInteractionKindElicitation:
		return "elicitation"
	case agentruntime.PendingInteractionKindAlert:
		return "alert"
	default:
		return ""
	}
}

func wantsInterruptObservation(opts options) bool {
	return firstNonEmpty(
		strings.TrimSpace(opts.interruptKind),
		strings.TrimSpace(opts.interruptAction),
		strings.TrimSpace(opts.interruptAlertCode),
		strings.TrimSpace(opts.interruptApprovalType),
		strings.TrimSpace(opts.interruptClass),
		strings.TrimSpace(opts.interruptToolName),
		strings.TrimSpace(opts.interruptPlanStatus),
		func() string {
			if opts.interruptPlanVersion <= 0 {
				return ""
			}
			return fmt.Sprintf("%d", opts.interruptPlanVersion)
		}(),
		strings.TrimSpace(opts.interruptDecision),
		strings.TrimSpace(opts.interruptNote),
	) != ""
}

func waitForPendingInterrupt(ctx context.Context, entry maestromcp.DaemonEntry, issueIdentifier, kind, classification, toolName string) (agentruntime.PendingInteraction, int, error) {
	deadline := time.Now().Add(60 * time.Second)
	for {
		snapshot, err := fetchPendingInterrupts(entry)
		if err == nil {
			for _, item := range snapshot.Items {
				if strings.TrimSpace(item.IssueIdentifier) != strings.TrimSpace(issueIdentifier) {
					continue
				}
				if kind != "" && !strings.EqualFold(string(item.Kind), kind) {
					continue
				}
				if classification != "" && !strings.EqualFold(asString(item.Metadata["classification"]), classification) {
					continue
				}
				if toolName != "" && !strings.EqualFold(asString(item.Metadata["tool_name"]), toolName) {
					continue
				}
				return item.Clone(), len(snapshot.Items), nil
			}
		}
		if time.Now().After(deadline) {
			return agentruntime.PendingInteraction{}, 0, fmt.Errorf("did not observe a matching pending interrupt for %s", issueIdentifier)
		}
		select {
		case <-ctx.Done():
			return agentruntime.PendingInteraction{}, 0, errors.New("did not observe a matching pending interrupt before context deadline")
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func waitForPendingInterruptClear(ctx context.Context, entry maestromcp.DaemonEntry, interactionID string) error {
	deadline := time.Now().Add(12 * time.Second)
	for {
		snapshot, err := fetchPendingInterrupts(entry)
		if err == nil {
			found := false
			for _, item := range snapshot.Items {
				if strings.TrimSpace(item.ID) == strings.TrimSpace(interactionID) {
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pending interrupt %s did not clear", interactionID)
		}
		select {
		case <-ctx.Done():
			return errors.New("pending interrupt did not clear before context deadline")
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func validatePendingInterrupt(interaction agentruntime.PendingInteraction, issueIdentifier, kind, approvalType, alertCode, classification, toolName, planStatus string, planVersion int) error {
	switch strings.ToLower(firstNonEmpty(
		strings.TrimSpace(kind),
		func() string {
			if strings.TrimSpace(alertCode) != "" {
				return "alert"
			}
			return ""
		}(),
		"approval",
	)) {
	case "alert":
		return validateAlertInterrupt(interaction, issueIdentifier, alertCode)
	case "approval":
		switch strings.ToLower(strings.TrimSpace(approvalType)) {
		case "", "claude_permission_prompt":
			return validateClaudePermissionInterrupt(interaction, issueIdentifier, classification, toolName)
		case "plan_approval":
			return validatePlanApprovalInterrupt(interaction, issueIdentifier, planStatus, planVersion)
		default:
			return fmt.Errorf("unsupported interrupt approval type %q", approvalType)
		}
	default:
		return fmt.Errorf("unsupported interrupt kind %q", kind)
	}
}

func validateClaudePermissionInterrupt(interaction agentruntime.PendingInteraction, issueIdentifier, classification, toolName string) error {
	if strings.TrimSpace(interaction.ID) == "" {
		return errors.New("pending interrupt id missing")
	}
	if interaction.Kind != agentruntime.PendingInteractionKindApproval {
		return fmt.Errorf("expected approval interrupt, got %q", interaction.Kind)
	}
	if strings.TrimSpace(interaction.Method) != "approval_prompt" {
		return fmt.Errorf("expected approval_prompt interrupt method, got %q", interaction.Method)
	}
	if strings.TrimSpace(interaction.IssueIdentifier) != strings.TrimSpace(issueIdentifier) {
		return fmt.Errorf("expected interrupt issue %q, got %q", issueIdentifier, interaction.IssueIdentifier)
	}
	if strings.TrimSpace(interaction.RequestID) == "" || strings.TrimSpace(interaction.ItemID) == "" {
		return fmt.Errorf("expected interrupt request and item ids, got %+v", interaction)
	}
	if interaction.Approval == nil {
		return errors.New("expected approval payload on pending interrupt")
	}
	if strings.TrimSpace(interaction.Approval.Command) == "" {
		return errors.New("expected approval command on pending interrupt")
	}
	if strings.TrimSpace(interaction.Approval.CWD) == "" {
		return errors.New("expected approval cwd on pending interrupt")
	}
	if strings.TrimSpace(interaction.Approval.Reason) == "" {
		return errors.New("expected approval reason on pending interrupt")
	}
	if strings.TrimSpace(interaction.Approval.Markdown) == "" {
		return errors.New("expected approval markdown on pending interrupt")
	}
	if len(interaction.Approval.Decisions) == 0 {
		return errors.New("expected approval decisions on pending interrupt")
	}
	if source := strings.TrimSpace(asString(interaction.Metadata["source"])); source != "claude_permission_prompt" {
		return fmt.Errorf("expected claude_permission_prompt source, got %q", source)
	}
	if classification != "" && !strings.EqualFold(asString(interaction.Metadata["classification"]), classification) {
		return fmt.Errorf("expected interrupt classification %q, got %q", classification, asString(interaction.Metadata["classification"]))
	}
	if toolName != "" && !strings.EqualFold(asString(interaction.Metadata["tool_name"]), toolName) {
		return fmt.Errorf("expected interrupt tool %q, got %q", toolName, asString(interaction.Metadata["tool_name"]))
	}
	if strings.TrimSpace(asString(interaction.Metadata["workspace_path"])) == "" {
		return errors.New("expected interrupt workspace_path metadata")
	}
	if _, ok := interaction.Metadata["input"]; !ok {
		return errors.New("expected interrupt input metadata")
	}
	requestMeta, ok := interaction.Metadata["request_meta"].(map[string]interface{})
	if !ok {
		return errors.New("expected interrupt request_meta payload")
	}
	if firstNonEmpty(
		strings.TrimSpace(asString(requestMeta["claudecode/toolUseId"])),
		strings.TrimSpace(asString(requestMeta["claude/toolUseId"])),
	) == "" {
		return errors.New("expected request_meta toolUseId correlation")
	}
	return nil
}

func validatePlanApprovalInterrupt(interaction agentruntime.PendingInteraction, issueIdentifier, planStatus string, planVersion int) error {
	if strings.TrimSpace(interaction.ID) == "" {
		return errors.New("pending interrupt id missing")
	}
	if interaction.Kind != agentruntime.PendingInteractionKindApproval {
		return fmt.Errorf("expected approval interrupt, got %q", interaction.Kind)
	}
	if strings.TrimSpace(interaction.IssueIdentifier) != strings.TrimSpace(issueIdentifier) {
		return fmt.Errorf("expected interrupt issue %q, got %q", issueIdentifier, interaction.IssueIdentifier)
	}
	if strings.TrimSpace(interaction.CollaborationMode) != "plan" {
		return fmt.Errorf("expected plan collaboration mode, got %q", interaction.CollaborationMode)
	}
	if interaction.Approval == nil {
		return errors.New("expected approval payload on pending interrupt")
	}
	if strings.TrimSpace(interaction.Approval.Markdown) == "" {
		return errors.New("expected plan markdown on pending interrupt")
	}
	if strings.TrimSpace(interaction.Approval.Reason) != "Review the proposed plan before execution." {
		return fmt.Errorf("expected plan approval reason, got %q", interaction.Approval.Reason)
	}
	if len(interaction.Approval.Decisions) == 0 {
		return errors.New("expected approval decisions on pending interrupt")
	}
	if planStatus != "" && !strings.EqualFold(strings.TrimSpace(interaction.Approval.PlanStatus), planStatus) {
		return fmt.Errorf("expected plan status %q, got %q", planStatus, interaction.Approval.PlanStatus)
	}
	if planVersion > 0 && interaction.Approval.PlanVersionNumber != planVersion {
		return fmt.Errorf("expected plan version %d, got %d", planVersion, interaction.Approval.PlanVersionNumber)
	}
	return nil
}

func validateAlertInterrupt(interaction agentruntime.PendingInteraction, issueIdentifier, alertCode string) error {
	if strings.TrimSpace(interaction.ID) == "" {
		return errors.New("pending alert id missing")
	}
	if interaction.Kind != agentruntime.PendingInteractionKindAlert {
		return fmt.Errorf("expected alert interrupt, got %q", interaction.Kind)
	}
	if strings.TrimSpace(interaction.IssueIdentifier) != strings.TrimSpace(issueIdentifier) {
		return fmt.Errorf("expected alert issue %q, got %q", issueIdentifier, interaction.IssueIdentifier)
	}
	if interaction.Alert == nil {
		return errors.New("expected alert payload on pending interrupt")
	}
	if alertCode != "" && !strings.EqualFold(strings.TrimSpace(interaction.Alert.Code), alertCode) {
		return fmt.Errorf("expected alert code %q, got %q", alertCode, interaction.Alert.Code)
	}
	if strings.TrimSpace(interaction.Alert.Title) == "" || strings.TrimSpace(interaction.Alert.Message) == "" {
		return errors.New("expected alert title and message")
	}
	if !interaction.HasAction(agentruntime.PendingInteractionActionAcknowledge) {
		return errors.New("expected acknowledge action on alert interrupt")
	}
	return nil
}

func fetchPendingInterrupts(entry maestromcp.DaemonEntry) (agentruntime.PendingInteractionSnapshot, error) {
	body, statusCode, err := dashboardRequest(entry, http.MethodGet, "/api/v1/app/interrupts", nil)
	if err != nil {
		return agentruntime.PendingInteractionSnapshot{}, err
	}
	if statusCode != http.StatusOK {
		return agentruntime.PendingInteractionSnapshot{}, fmt.Errorf("dashboard interrupts returned %d: %s", statusCode, strings.TrimSpace(string(body)))
	}
	var snapshot agentruntime.PendingInteractionSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return agentruntime.PendingInteractionSnapshot{}, err
	}
	return snapshot, nil
}

func respondToPendingInterrupt(entry maestromcp.DaemonEntry, interactionID string, response agentruntime.PendingInteractionResponse) (string, error) {
	body, statusCode, err := dashboardRequest(entry, http.MethodPost, "/api/v1/app/interrupts/"+strings.TrimSpace(interactionID)+"/respond", response)
	if err != nil {
		return "", err
	}
	if statusCode != http.StatusAccepted {
		return "", fmt.Errorf("dashboard interrupt respond returned %d: %s", statusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Status), nil
}

func acknowledgePendingInterrupt(entry maestromcp.DaemonEntry, interactionID string) (string, error) {
	body, statusCode, err := dashboardRequest(entry, http.MethodPost, "/api/v1/app/interrupts/"+strings.TrimSpace(interactionID)+"/acknowledge", map[string]interface{}{})
	if err != nil {
		return "", err
	}
	if statusCode != http.StatusAccepted {
		return "", fmt.Errorf("dashboard interrupt acknowledge returned %d: %s", statusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Status), nil
}

func dashboardRequest(entry maestromcp.DaemonEntry, method, path string, payload interface{}) ([]byte, int, error) {
	baseURL := strings.TrimSuffix(strings.TrimSpace(entry.BaseURL), "/mcp")
	if baseURL == "" {
		return nil, 0, errors.New("daemon registry base_url missing")
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = strings.NewReader(string(data))
	}
	req, err := http.NewRequest(method, baseURL+path, body)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := strings.TrimSpace(entry.BearerToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return data, resp.StatusCode, nil
}

func filterRuntimeEventsByIssue(events []kanban.RuntimeEvent, issueIdentifier string) []kanban.RuntimeEvent {
	if strings.TrimSpace(issueIdentifier) == "" {
		return append([]kanban.RuntimeEvent(nil), events...)
	}
	filtered := make([]kanban.RuntimeEvent, 0, len(events))
	for _, event := range events {
		if strings.TrimSpace(event.Identifier) != strings.TrimSpace(issueIdentifier) {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func boolFromMap(raw map[string]interface{}, key string) bool {
	value, ok := raw[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	default:
		return false
	}
}

func workspaceRecoveryFromAny(value interface{}) *kanban.WorkspaceRecovery {
	raw, ok := value.(map[string]interface{})
	if !ok {
		return nil
	}
	status := strings.TrimSpace(asString(raw["status"]))
	message := strings.TrimSpace(asString(raw["message"]))
	if status == "" && message == "" {
		return nil
	}
	return &kanban.WorkspaceRecovery{
		Status:  status,
		Message: message,
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

func loadIssuePlanningEvidence(dbPath, issueIdentifier string) (issueObservation, planningObservation, error) {
	store, err := kanban.NewReadOnlyStore(dbPath)
	if err != nil {
		return issueObservation{}, planningObservation{}, fmt.Errorf("open read-only store: %w", err)
	}
	defer store.Close()

	issue, err := store.GetIssueByIdentifier(issueIdentifier)
	if err != nil {
		return issueObservation{}, planningObservation{}, fmt.Errorf("load issue %s: %w", issueIdentifier, err)
	}

	issueEvidence := issueObservation{
		State:                     strings.TrimSpace(string(issue.State)),
		PermissionProfile:         strings.TrimSpace(string(kanban.NormalizePermissionProfile(string(issue.PermissionProfile)))),
		CollaborationModeOverride: strings.TrimSpace(string(issue.CollaborationModeOverride)),
		PlanApprovalPending:       issue.PlanApprovalPending,
		PendingPlanRevisionNote:   strings.TrimSpace(issue.PendingPlanRevisionMarkdown),
	}

	planning, err := store.GetIssuePlanning(issue)
	if err != nil {
		return issueEvidence, planningObservation{}, fmt.Errorf("load planning %s: %w", issueIdentifier, err)
	}
	if planning == nil {
		return issueEvidence, planningObservation{}, nil
	}

	planningEvidence := planningObservation{
		Present:              true,
		SessionID:            strings.TrimSpace(planning.SessionID),
		Status:               strings.TrimSpace(string(planning.Status)),
		VersionCount:         len(planning.Versions),
		CurrentVersionNumber: planning.CurrentVersionNumber,
		PendingRevisionNote:  strings.TrimSpace(planning.PendingRevisionNote),
	}
	if planning.CurrentVersion != nil {
		planningEvidence.CurrentVersionRevisionNote = strings.TrimSpace(planning.CurrentVersion.RevisionNote)
		planningEvidence.CurrentVersionThreadID = strings.TrimSpace(planning.CurrentVersion.ThreadID)
		planningEvidence.CurrentVersionTurnID = strings.TrimSpace(planning.CurrentVersion.TurnID)
	}
	return issueEvidence, planningEvidence, nil
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
		"dashboard_session_failure_class=" + evidence.DashboardSession.FailureClass,
		"dashboard_session_pending_interaction_state=" + evidence.DashboardSession.PendingInteractionState,
		"dashboard_session_runtime_auth_source=" + evidence.DashboardSession.RuntimeAuthSource,
		"dashboard_session_runtime_name=" + evidence.DashboardSession.RuntimeName,
		"dashboard_session_runtime_provider=" + evidence.DashboardSession.RuntimeProvider,
		"dashboard_session_runtime_transport=" + evidence.DashboardSession.RuntimeTransport,
		"dashboard_session_source=" + evidence.DashboardSession.Source,
		"dashboard_session_status=" + evidence.DashboardSession.Status,
		"dashboard_session_stop_reason=" + evidence.DashboardSession.StopReason,
		fmt.Sprintf("execution_active=%t", evidence.Execution.Active),
		"execution_failure_class=" + evidence.Execution.FailureClass,
		"execution_pending_interaction_state=" + evidence.Execution.PendingInteractionState,
		"execution_runtime_auth_source=" + evidence.Execution.RuntimeAuthSource,
		"execution_runtime_name=" + evidence.Execution.RuntimeName,
		"execution_runtime_provider=" + evidence.Execution.RuntimeProvider,
		"execution_runtime_transport=" + evidence.Execution.RuntimeTransport,
		"execution_session_identifier_strategy=" + strings.TrimSpace(asString(evidence.Execution.Session.Metadata["session_identifier_strategy"])),
		"execution_session_id=" + strings.TrimSpace(evidence.Execution.Session.SessionID),
		"execution_session_source=" + evidence.Execution.SessionSource,
		"execution_stop_reason=" + evidence.Execution.StopReason,
		"execution_stream_marker=" + evidence.Execution.StreamMarker,
		fmt.Sprintf("execution_stream_seen=%t", evidence.Execution.StreamSeen),
		"execution_thread_id=" + strings.TrimSpace(evidence.Execution.Session.ThreadID),
		"execution_provider_session_id=" + strings.TrimSpace(asString(evidence.Execution.Session.Metadata["provider_session_id"])),
		fmt.Sprintf("execution_workspace_recovery_present=%t", evidence.Execution.WorkspaceRecovery != nil),
		"execution_workspace_recovery_status=" + func() string {
			if evidence.Execution.WorkspaceRecovery == nil {
				return ""
			}
			return strings.TrimSpace(evidence.Execution.WorkspaceRecovery.Status)
		}(),
		"execution_workspace_recovery_message=" + func() string {
			if evidence.Execution.WorkspaceRecovery == nil {
				return ""
			}
			return summaryValue(evidence.Execution.WorkspaceRecovery.Message)
		}(),
		"expected_tools_present=true",
		"interrupt_action=" + evidence.Interrupt.Action,
		"interrupt_alert_code=" + func() string {
			if evidence.Interrupt.Interaction.Alert == nil {
				return ""
			}
			return strings.TrimSpace(evidence.Interrupt.Interaction.Alert.Code)
		}(),
		fmt.Sprintf("interrupt_cleared=%t", evidence.Interrupt.Cleared),
		"interrupt_classification=" + strings.TrimSpace(asString(evidence.Interrupt.Interaction.Metadata["classification"])),
		"interrupt_collaboration_mode=" + strings.TrimSpace(evidence.Interrupt.Interaction.CollaborationMode),
		"interrupt_id=" + strings.TrimSpace(evidence.Interrupt.Interaction.ID),
		"interrupt_kind=" + string(evidence.Interrupt.Interaction.Kind),
		fmt.Sprintf("interrupt_pending_count=%d", evidence.Interrupt.PendingCount),
		"interrupt_plan_status=" + func() string {
			if evidence.Interrupt.Interaction.Approval == nil {
				return ""
			}
			return strings.TrimSpace(evidence.Interrupt.Interaction.Approval.PlanStatus)
		}(),
		"interrupt_plan_version=" + func() string {
			if evidence.Interrupt.Interaction.Approval == nil || evidence.Interrupt.Interaction.Approval.PlanVersionNumber == 0 {
				return ""
			}
			return fmt.Sprintf("%d", evidence.Interrupt.Interaction.Approval.PlanVersionNumber)
		}(),
		fmt.Sprintf("interrupt_requested=%t", evidence.Interrupt.Requested),
		"interrupt_response_decision=" + evidence.Interrupt.ResponseDecision,
		"interrupt_response_status=" + evidence.Interrupt.ResponseStatus,
		"interrupt_source=" + strings.TrimSpace(asString(evidence.Interrupt.Interaction.Metadata["source"])),
		"interrupt_tool_name=" + strings.TrimSpace(asString(evidence.Interrupt.Interaction.Metadata["tool_name"])),
		"issue_collaboration_mode_override=" + evidence.Issue.CollaborationModeOverride,
		"issue_identifier=" + evidence.IssueIdentifier,
		"issue_permission_profile=" + evidence.Issue.PermissionProfile,
		fmt.Sprintf("issue_plan_approval_pending=%t", evidence.Issue.PlanApprovalPending),
		"issue_pending_plan_revision_note=" + summaryValue(evidence.Issue.PendingPlanRevisionNote),
		"issue_state=" + evidence.Issue.State,
		fmt.Sprintf("live_claude_session_seen=%t", evidence.LiveSessionSeen),
		"mode=" + evidence.Mode,
		"permission_mode=" + evidence.Bridge.PermissionMode,
		"permission_prompt_tool=" + evidence.Bridge.PermissionPromptTool,
		fmt.Sprintf("planning_present=%t", evidence.Planning.Present),
		"planning_current_version_number=" + func() string {
			if evidence.Planning.CurrentVersionNumber == 0 {
				return ""
			}
			return fmt.Sprintf("%d", evidence.Planning.CurrentVersionNumber)
		}(),
		"planning_current_version_revision_note=" + summaryValue(evidence.Planning.CurrentVersionRevisionNote),
		"planning_current_version_thread_id=" + evidence.Planning.CurrentVersionThreadID,
		"planning_current_version_turn_id=" + evidence.Planning.CurrentVersionTurnID,
		"planning_pending_revision_note=" + summaryValue(evidence.Planning.PendingRevisionNote),
		"planning_session_id=" + evidence.Planning.SessionID,
		"planning_status=" + evidence.Planning.Status,
		fmt.Sprintf("planning_version_count=%d", evidence.Planning.VersionCount),
		fmt.Sprintf("runtime_event_count=%d", len(evidence.RuntimeEvents.Items)),
		"runtime_event_kinds=" + strings.Join(runtimeEventKinds(evidence.RuntimeEvents.Items), ","),
		fmt.Sprintf("server_db_path=%s", evidence.ServerInfo.Meta.DBPath),
		fmt.Sprintf("server_store_id=%s", evidence.ServerInfo.Meta.StoreID),
		fmt.Sprintf("settings_disable_all_hooks=%t", evidence.Settings.DisableAllHooks),
		"settings_disable_auto_mode=" + evidence.Settings.DisableAutoMode,
		"settings_disable_bypass_permissions_mode=" + evidence.Settings.Permissions.DisableBypassPermissionsMode,
		fmt.Sprintf("settings_include_git_instructions=%t", evidence.Settings.IncludeGitInstructions),
		fmt.Sprintf("settings_use_auto_mode_during_plan=%t", evidence.Settings.UseAutoModeDuringPlan),
		fmt.Sprintf("strict_mcp_config=%t", evidence.Bridge.StrictMCPConfig),
		"tool_call_get_issue_execution=ok",
		"tool_call_get_runtime_snapshot=ok",
		"tool_call_list_issues=ok",
		"tool_call_list_runtime_events=ok",
		"tool_call_list_sessions=ok",
		"tool_call_server_info=ok",
		"tool_names=" + strings.Join(evidence.Bridge.ToolNames, ","),
	}
	if err := os.WriteFile(summaryPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return fmt.Errorf("write summary evidence: %w", err)
	}
	return nil
}

func runtimeEventKinds(events []kanban.RuntimeEvent) []string {
	if len(events) == 0 {
		return nil
	}
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, strings.TrimSpace(event.Kind))
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func summaryValue(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "\n", `\n`)
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
