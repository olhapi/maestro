package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpapi "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
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

	tests := []struct {
		name    string
		opts    options
		wantErr string
	}{
		{
			name: "full_access_allowed_tools",
			opts: options{
				allowedTools:    "Bash,Edit,Write,MultiEdit",
				permissionMode:  "default",
				strictMCPConfig: "true",
			},
		},
		{
			name: "maestro_approval_prompt",
			opts: options{
				permissionMode:       "default",
				permissionPromptTool: "mcp__maestro__approval_prompt",
				strictMCPConfig:      "true",
			},
		},
		{
			name: "permission_mode",
			opts: options{
				allowedTools:    "Bash,Edit,Write,MultiEdit",
				permissionMode:  "auto",
				strictMCPConfig: "true",
			},
			wantErr: `expected Claude permission mode default or plan`,
		},
		{
			name: "allowed_tools",
			opts: options{
				allowedTools:    "Bash",
				permissionMode:  "default",
				strictMCPConfig: "true",
			},
			wantErr: `expected allowed tools Bash,Edit,Write,MultiEdit`,
		},
		{
			name: "plan mode still uses maestro approval prompt",
			opts: options{
				permissionMode:       "plan",
				permissionPromptTool: "mcp__maestro__approval_prompt",
				strictMCPConfig:      "true",
			},
		},
		{
			name: "approval_prompt_forbids_allowed_tools",
			opts: options{
				allowedTools:         "Bash",
				permissionMode:       "default",
				permissionPromptTool: "mcp__maestro__approval_prompt",
				strictMCPConfig:      "true",
			},
			wantErr: `expected no allowed-tools`,
		},
		{
			name: "unsupported_permission_prompt_tool",
			opts: options{
				permissionMode:       "default",
				permissionPromptTool: "custom_prompt",
				strictMCPConfig:      "true",
			},
			wantErr: `expected supported permission prompt tool`,
		},
		{
			name: "strict_mcp_config",
			opts: options{
				allowedTools:    "Bash,Edit,Write,MultiEdit",
				permissionMode:  "default",
				strictMCPConfig: "false",
			},
			wantErr: `expected strict-mcp-config=true`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validatePermissionFlags(tt.opts)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validatePermissionFlags() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validatePermissionFlags() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestWantsInterruptObservation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts options
		want bool
	}{
		{name: "no_interrupt_fields", opts: options{}, want: false},
		{name: "classification_only", opts: options{interruptClass: "command"}, want: true},
		{name: "tool_name_only", opts: options{interruptToolName: "Bash"}, want: true},
		{name: "decision_only", opts: options{interruptDecision: "allow"}, want: true},
		{name: "note_only", opts: options{interruptNote: "operator approved"}, want: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := wantsInterruptObservation(tt.opts); got != tt.want {
				t.Fatalf("wantsInterruptObservation() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestValidatePendingInterrupt(t *testing.T) {
	t.Parallel()

	t.Run("accepts_maestro_managed_payload", func(t *testing.T) {
		t.Parallel()

		if err := validatePendingInterrupt(validPendingInterrupt(), "CL-1", "", "command", "Bash", "", 0); err != nil {
			t.Fatalf("validatePendingInterrupt() error = %v", err)
		}
	})

	t.Run("rejects_missing_request_meta_correlation", func(t *testing.T) {
		t.Parallel()

		interaction := validPendingInterrupt()
		interaction.Metadata["request_meta"] = map[string]interface{}{}

		err := validatePendingInterrupt(interaction, "CL-1", "", "command", "Bash", "", 0)
		if err == nil || !strings.Contains(err.Error(), "toolUseId correlation") {
			t.Fatalf("validatePendingInterrupt() error = %v, want missing toolUseId correlation", err)
		}
	})

	t.Run("rejects_classification_mismatch", func(t *testing.T) {
		t.Parallel()

		err := validatePendingInterrupt(validPendingInterrupt(), "CL-1", "", "file_write", "Bash", "", 0)
		if err == nil || !strings.Contains(err.Error(), "expected interrupt classification") {
			t.Fatalf("validatePendingInterrupt() error = %v, want classification mismatch", err)
		}
	})
	tests := []struct {
		name string
		edit func(*agentruntime.PendingInteraction)
		want string
	}{
		{
			name: "missing_id",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.ID = ""
			},
			want: "pending interrupt id missing",
		},
		{
			name: "wrong_kind",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Kind = agentruntime.PendingInteractionKindUserInput
			},
			want: "expected approval interrupt",
		},
		{
			name: "wrong_method",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Method = "other"
			},
			want: "expected approval_prompt interrupt method",
		},
		{
			name: "wrong_issue_identifier",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.IssueIdentifier = "OTHER-1"
			},
			want: `expected interrupt issue "CL-1"`,
		},
		{
			name: "missing_request_and_item_ids",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.RequestID = ""
				interaction.ItemID = ""
			},
			want: "expected interrupt request and item ids",
		},
		{
			name: "missing_approval_payload",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Approval = nil
			},
			want: "expected approval payload on pending interrupt",
		},
		{
			name: "missing_command",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Approval.Command = ""
			},
			want: "expected approval command on pending interrupt",
		},
		{
			name: "missing_cwd",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Approval.CWD = ""
			},
			want: "expected approval cwd on pending interrupt",
		},
		{
			name: "missing_reason",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Approval.Reason = ""
			},
			want: "expected approval reason on pending interrupt",
		},
		{
			name: "missing_markdown",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Approval.Markdown = ""
			},
			want: "expected approval markdown on pending interrupt",
		},
		{
			name: "missing_decisions",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Approval.Decisions = nil
			},
			want: "expected approval decisions on pending interrupt",
		},
		{
			name: "wrong_source",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Metadata["source"] = "other"
			},
			want: `expected claude_permission_prompt source`,
		},
		{
			name: "missing_workspace_path",
			edit: func(interaction *agentruntime.PendingInteraction) {
				delete(interaction.Metadata, "workspace_path")
			},
			want: "expected interrupt workspace_path metadata",
		},
		{
			name: "missing_input",
			edit: func(interaction *agentruntime.PendingInteraction) {
				delete(interaction.Metadata, "input")
			},
			want: "expected interrupt input metadata",
		},
		{
			name: "missing_request_meta",
			edit: func(interaction *agentruntime.PendingInteraction) {
				delete(interaction.Metadata, "request_meta")
			},
			want: "expected interrupt request_meta payload",
		},
		{
			name: "missing_tool_use_id",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Metadata["request_meta"] = map[string]interface{}{"other": "value"}
			},
			want: "expected request_meta toolUseId correlation",
		},
		{
			name: "tool_name_mismatch",
			edit: func(interaction *agentruntime.PendingInteraction) {
				interaction.Metadata["tool_name"] = "Write"
			},
			want: `expected interrupt tool "Bash"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			interaction := validPendingInterrupt()
			tt.edit(&interaction)
			err := validatePendingInterrupt(interaction, "CL-1", "", "command", "Bash", "", 0)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validatePendingInterrupt() error = %v, want substring %q", err, tt.want)
			}
		})
	}

	t.Run("accepts plan approval payload", func(t *testing.T) {
		t.Parallel()

		if err := validatePendingInterrupt(validPlanApprovalInterrupt(), "CL-3", "plan_approval", "", "", "awaiting_approval", 2); err != nil {
			t.Fatalf("validatePendingInterrupt() error = %v", err)
		}
	})

	t.Run("rejects plan approval version mismatch", func(t *testing.T) {
		t.Parallel()

		err := validatePendingInterrupt(validPlanApprovalInterrupt(), "CL-3", "plan_approval", "", "", "awaiting_approval", 3)
		if err == nil || !strings.Contains(err.Error(), "expected plan version") {
			t.Fatalf("validatePendingInterrupt() error = %v, want plan version mismatch", err)
		}
	})
}

func TestFilterRuntimeEventsByIssue(t *testing.T) {
	t.Parallel()

	events := []kanban.RuntimeEvent{
		{Identifier: "CL-1", Kind: "run_completed"},
		{Identifier: "CL-2", Kind: "run_failed"},
		{Identifier: "CL-1", Kind: "retry_paused"},
	}

	filtered := filterRuntimeEventsByIssue(events, "CL-1")
	if len(filtered) != 2 {
		t.Fatalf("filterRuntimeEventsByIssue() len = %d, want 2", len(filtered))
	}
	if filtered[0].Kind != "run_completed" || filtered[1].Kind != "retry_paused" {
		t.Fatalf("unexpected filtered events: %+v", filtered)
	}

	all := filterRuntimeEventsByIssue(events, "")
	if !reflect.DeepEqual(all, events) {
		t.Fatalf("filterRuntimeEventsByIssue() with blank issue = %+v, want %+v", all, events)
	}
}

func TestRuntimeEventKinds(t *testing.T) {
	t.Parallel()

	events := []kanban.RuntimeEvent{
		{Kind: " run_completed "},
		{Kind: "retry_paused"},
	}

	got := runtimeEventKinds(events)
	want := []string{"run_completed", "retry_paused"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtimeEventKinds() = %#v, want %#v", got, want)
	}
}

func validPendingInterrupt() agentruntime.PendingInteraction {
	return agentruntime.PendingInteraction{
		ID:              "interrupt-1",
		RequestID:       "toolu_123",
		Kind:            agentruntime.PendingInteractionKindApproval,
		Method:          "approval_prompt",
		IssueIdentifier: "CL-1",
		ItemID:          "toolu_123",
		Approval: &agentruntime.PendingApproval{
			Command:  "pwd",
			CWD:      "/tmp/workspace",
			Reason:   "Claude requested command approval: pwd",
			Markdown: "Approve the command request.",
			Decisions: []agentruntime.PendingApprovalDecision{
				{Value: "allow", Label: "Allow once"},
			},
		},
		Metadata: map[string]interface{}{
			"source":         "claude_permission_prompt",
			"classification": "command",
			"tool_name":      "Bash",
			"workspace_path": "/tmp/workspace",
			"input": map[string]interface{}{
				"command": "pwd",
			},
			"request_meta": map[string]interface{}{
				"claudecode/toolUseId": "toolu_123",
			},
		},
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

func validPlanApprovalInterrupt() agentruntime.PendingInteraction {
	return agentruntime.PendingInteraction{
		ID:                "plan-approval-1",
		Kind:              agentruntime.PendingInteractionKindApproval,
		IssueIdentifier:   "CL-3",
		CollaborationMode: "plan",
		Approval: &agentruntime.PendingApproval{
			Reason:            "Review the proposed plan before execution.",
			Markdown:          "Ship the guarded rollout.",
			PlanStatus:        "awaiting_approval",
			PlanVersionNumber: 2,
			Decisions: []agentruntime.PendingApprovalDecision{
				{Value: "approved", Label: "Approve plan"},
			},
		},
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
	evidence.Bridge.ToolNames = []string{"approval_prompt", "create_issue", "get_issue_execution", "get_runtime_snapshot", "list_issues", "list_runtime_events", "list_sessions", "server_info"}
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
		"tool_call_get_issue_execution=ok",
		"tool_call_list_runtime_events=ok",
		"tool_call_server_info=ok",
		"tool_names=approval_prompt,create_issue,get_issue_execution,get_runtime_snapshot,list_issues,list_runtime_events,list_sessions,server_info",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary evidence missing %q: %s", want, summary)
		}
	}
}

func TestRunHappyPath(t *testing.T) {
	fixture := newProbeRunFixture(t, "happy")

	if err := run(fixture.opts); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	jsonBytes, err := os.ReadFile(fixture.evidencePrefix + ".json")
	if err != nil {
		t.Fatalf("read JSON evidence: %v", err)
	}
	var evidence probeEvidence
	if err := json.Unmarshal(jsonBytes, &evidence); err != nil {
		t.Fatalf("decode JSON evidence: %v", err)
	}
	if !evidence.LiveSessionSeen {
		t.Fatalf("evidence.LiveSessionSeen = false, want true")
	}
	if got, want := evidence.Bridge.ServerName, "maestro"; got != want {
		t.Fatalf("evidence.Bridge.ServerName = %q, want %q", got, want)
	}
	if got, want := filepath.Clean(evidence.Bridge.DBPath), filepath.Clean(fixture.dbPath); got != want {
		t.Fatalf("evidence.Bridge.DBPath = %q, want %q", got, want)
	}
	if got, want := evidence.ServerInfo.Meta.StoreID, fixture.storeID; got != want {
		t.Fatalf("evidence.ServerInfo.Meta.StoreID = %q, want %q", got, want)
	}
	if got, want := evidence.Issue.PermissionProfile, string(kanban.PermissionProfileDefault); got != want {
		t.Fatalf("evidence.Issue.PermissionProfile = %q, want %q", got, want)
	}
	if got, want := evidence.Issue.State, string(kanban.StateReady); got != want {
		t.Fatalf("evidence.Issue.State = %q, want %q", got, want)
	}
	if evidence.Planning.Present {
		t.Fatalf("evidence.Planning.Present = true, want false")
	}
	if got := strings.TrimSpace(asString(evidence.LiveSession.Metadata["provider"])); got != "claude" {
		t.Fatalf("live session provider = %q, want claude", got)
	}

	summaryBytes, err := os.ReadFile(fixture.evidencePrefix + ".summary.txt")
	if err != nil {
		t.Fatalf("read summary evidence: %v", err)
	}
	summary := string(summaryBytes)
	for _, want := range []string{
		"bridge_db_path=" + fixture.dbPath,
		"daemon_entry_stable=true",
		"issue_identifier=MAES-28",
		"issue_permission_profile=default",
		"issue_state=ready",
		"live_claude_session_seen=true",
		"planning_present=false",
		"tool_call_get_issue_execution=ok",
		"tool_call_list_sessions=ok",
		"tool_call_server_info=ok",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary evidence missing %q: %s", want, summary)
		}
	}
}

func TestRunFinalModeUsesPersistedExecution(t *testing.T) {
	fixture := newProbeRunFixture(t, "execution-final")
	fixture.opts.mode = "final"

	if err := run(fixture.opts); err != nil {
		t.Fatalf("run(final) error = %v", err)
	}

	jsonBytes, err := os.ReadFile(fixture.evidencePrefix + ".json")
	if err != nil {
		t.Fatalf("read JSON evidence: %v", err)
	}
	var evidence probeEvidence
	if err := json.Unmarshal(jsonBytes, &evidence); err != nil {
		t.Fatalf("decode JSON evidence: %v", err)
	}
	if evidence.LiveSessionSeen {
		t.Fatal("evidence.LiveSessionSeen = true, want false in final mode")
	}
	if evidence.Execution.SessionSource != "persisted" || evidence.Execution.StopReason != "end_turn" {
		t.Fatalf("unexpected final execution evidence: %+v", evidence.Execution)
	}
	if evidence.DashboardSession.Source != "persisted" || evidence.DashboardSession.StopReason != "end_turn" {
		t.Fatalf("unexpected final dashboard evidence: %+v", evidence.DashboardSession)
	}
}

func TestRunReportsProbeFailures(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		mutate   func(*probeRunFixture)
		want     string
	}{
		{
			name:     "bridge_db_path_mismatch",
			scenario: "happy",
			mutate: func(fixture *probeRunFixture) {
				fixture.opts.dbPath = filepath.Join(filepath.Dir(fixture.dbPath), "other.db")
			},
			want: "bridge db path mismatch",
		},
		{
			name:     "daemon_registry_count_before",
			scenario: "happy",
			mutate: func(fixture *probeRunFixture) {
				writeJSONFile(t, filepath.Join(filepath.Dir(fixture.registryEntryPath), "second.json"), maestromcp.DaemonEntry{
					StoreID: "store-b",
					DBPath:  fixture.dbPath,
				})
			},
			want: "expected exactly one daemon registry entry before bridge probe",
		},
		{
			name:     "daemon_registry_db_mismatch_before",
			scenario: "happy",
			mutate: func(fixture *probeRunFixture) {
				writeJSONFile(t, fixture.registryEntryPath, maestromcp.DaemonEntry{
					StoreID: fixture.storeID,
					DBPath:  filepath.Join(filepath.Dir(fixture.dbPath), "other.db"),
				})
			},
			want: "daemon registry db path mismatch",
		},
		{
			name:     "missing_tool",
			scenario: "missing-tool",
			want:     `expected tool "create_issue" in bridge surface`,
		},
		{
			name:     "runtime_unavailable",
			scenario: "runtime-unavailable",
			want:     "server_info reported runtime_available=false",
		},
		{
			name:     "server_info_bad_data",
			scenario: "server-info-bad-data",
			want:     "decode server_info",
		},
		{
			name:     "server_info_db_mismatch",
			scenario: "server-info-db-mismatch",
			want:     "server_info db path mismatch",
		},
		{
			name:     "server_info_store_mismatch",
			scenario: "server-info-store-mismatch",
			want:     "server_info store id mismatch",
		},
		{
			name:     "list_issues_invalid_json",
			scenario: "list-issues-invalid-json",
			want:     "call list_issues",
		},
		{
			name:     "list_issues_bad_data",
			scenario: "list-issues-bad-data",
			want:     "decode list_issues",
		},
		{
			name:     "runtime_snapshot_invalid_json",
			scenario: "runtime-snapshot-invalid-json",
			want:     "call get_runtime_snapshot",
		},
		{
			name:     "runtime_snapshot_bad_data",
			scenario: "runtime-snapshot-bad-data",
			want:     "decode get_runtime_snapshot",
		},
		{
			name:     "daemon_registry_count_after",
			scenario: "registry-count-after",
			want:     "expected exactly one daemon registry entry after bridge probe",
		},
		{
			name:     "daemon_registry_db_drift_after",
			scenario: "registry-db-drift-after",
			want:     "daemon registry db path drifted",
		},
		{
			name:     "daemon_registry_entry_changed",
			scenario: "registry-drift-after",
			want:     "daemon registry entry changed during Claude bridge probe",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			fixture := newProbeRunFixture(t, tt.scenario)
			if tt.mutate != nil {
				tt.mutate(&fixture)
			}

			err := run(fixture.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("run() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestCallToolEnvelopeFailures(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		want     string
	}{
		{name: "no_content", scenario: "call-no-content", want: "server_info returned no content"},
		{name: "wrong_content_type", scenario: "call-wrong-content", want: "server_info returned unexpected content type"},
		{name: "invalid_json", scenario: "call-invalid-json", want: "server_info response decode"},
		{name: "tool_failure", scenario: "call-fail", want: "server_info failed: scenario failure"},
		{name: "tool_failure_unknown_error", scenario: "call-fail-empty-message", want: "server_info failed: unknown error"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			client := newProbeTestClient(t, tt.scenario)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := callToolEnvelope(ctx, client, "server_info", map[string]interface{}{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("callToolEnvelope() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestWaitForClaudeSessionContextDeadline(t *testing.T) {
	client := newProbeTestClient(t, "list-sessions-empty")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	sessionKey, session, err := waitForClaudeSession(ctx, client)
	if err == nil || err.Error() != "did not observe a live Claude session before context deadline" {
		t.Fatalf("waitForClaudeSession() error = %v, want context deadline message", err)
	}
	if sessionKey != "" {
		t.Fatalf("waitForClaudeSession() sessionKey = %q, want empty", sessionKey)
	}
	if session.SessionID != "" || session.ThreadID != "" {
		t.Fatalf("waitForClaudeSession() session = %+v, want zero value", session)
	}
}

func TestWaitForClaudeSessionSkipsUnsupportedEntries(t *testing.T) {
	client := newProbeTestClient(t, "list-sessions-mixed")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionKey, session, err := waitForClaudeSession(ctx, client)
	if err != nil {
		t.Fatalf("waitForClaudeSession() error = %v", err)
	}
	if got, want := sessionKey, "MAES-28"; got != want {
		t.Fatalf("waitForClaudeSession() sessionKey = %q, want %q", got, want)
	}
	if got := strings.TrimSpace(asString(session.Metadata["provider"])); got != "claude" {
		t.Fatalf("waitForClaudeSession() provider = %q, want claude", got)
	}
	if got := strings.TrimSpace(asString(session.Metadata["transport"])); got != "stdio" {
		t.Fatalf("waitForClaudeSession() transport = %q, want stdio", got)
	}
}

func TestWaitForClaudeSessionIgnoresMalformedPayloads(t *testing.T) {
	client := newProbeTestClient(t, "list-sessions-bad-data")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err := waitForClaudeSession(ctx, client)
	if err == nil || err.Error() != "did not observe a live Claude session before context deadline" {
		t.Fatalf("waitForClaudeSession() error = %v, want context deadline message", err)
	}
}

func TestResolveIssueIdentifier(t *testing.T) {
	t.Parallel()

	issues := listIssuesData{
		Items: []interface{}{
			map[string]interface{}{"identifier": "MAES-1", "state": "backlog"},
			map[string]interface{}{"identifier": "MAES-2", "state": "ready"},
			map[string]interface{}{"identifier": "MAES-3", "state": "in_progress"},
		},
	}

	if got := resolveIssueIdentifier("USER-1", issues); got != "USER-1" {
		t.Fatalf("resolveIssueIdentifier(explicit) = %q, want USER-1", got)
	}
	if got := resolveIssueIdentifier("", issues); got != "MAES-2" {
		t.Fatalf("resolveIssueIdentifier(ready) = %q, want MAES-2", got)
	}
	if got := resolveIssueIdentifier("", listIssuesData{
		Items: []interface{}{
			map[string]interface{}{"identifier": "MAES-9", "state": "backlog"},
		},
	}); got != "MAES-9" {
		t.Fatalf("resolveIssueIdentifier(fallback) = %q, want MAES-9", got)
	}
	if got := resolveIssueIdentifier("", listIssuesData{
		Items: []interface{}{
			"skip",
			map[string]interface{}{"identifier": "", "state": "ready"},
			map[string]interface{}{"identifier": "MAES-10", "state": "in_progress"},
		},
	}); got != "MAES-10" {
		t.Fatalf("resolveIssueIdentifier(malformed) = %q, want MAES-10", got)
	}
	if got := resolveIssueIdentifier("", listIssuesData{}); got != "" {
		t.Fatalf("resolveIssueIdentifier(empty) = %q, want empty", got)
	}
}

func TestWaitForExecutionObservationModes(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		client := newProbeTestClient(t, "happy")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		observation, err := waitForExecutionObservation(ctx, client, "MAES-28", "live", "")
		if err != nil {
			t.Fatalf("waitForExecutionObservation(live) error = %v", err)
		}
		if !observation.Active || observation.SessionSource != "live" || !observation.StreamSeen {
			t.Fatalf("unexpected live observation: %+v", observation)
		}
		if observation.RuntimeProvider != "claude" || observation.RuntimeTransport != "stdio" {
			t.Fatalf("unexpected live runtime observation: %+v", observation)
		}
	})

	t.Run("final", func(t *testing.T) {
		client := newProbeTestClient(t, "execution-final")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		observation, err := waitForExecutionObservation(ctx, client, "MAES-28", "final", "")
		if err != nil {
			t.Fatalf("waitForExecutionObservation(final) error = %v", err)
		}
		if observation.Active || observation.SessionSource != "persisted" || observation.StopReason != "end_turn" {
			t.Fatalf("unexpected final observation: %+v", observation)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		client := newProbeTestClient(t, "execution-bad-data")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := waitForExecutionObservation(ctx, client, "MAES-28", "live", "")
		if err == nil || err.Error() != "did not observe issue execution before context deadline" {
			t.Fatalf("waitForExecutionObservation(deadline) error = %v, want context deadline message", err)
		}
	})
}

func TestWaitForDashboardSessionObservation(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer token-a" {
				t.Fatalf("Authorization header = %q, want Bearer token-a", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entries": []map[string]any{
					{
						"issue_identifier": "MAES-28",
						"status":           "running",
						"stop_reason":      "",
						"source":           "live",
					},
				},
			})
		}))
		t.Cleanup(server.Close)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		observation, err := waitForDashboardSessionObservation(ctx, maestromcp.DaemonEntry{
			BaseURL:     server.URL + "/mcp",
			BearerToken: "token-a",
		}, "MAES-28", "live")
		if err != nil {
			t.Fatalf("waitForDashboardSessionObservation(live) error = %v", err)
		}
		if observation.Source != "live" || observation.Status != "running" {
			t.Fatalf("unexpected live dashboard observation: %+v", observation)
		}
	})

	t.Run("final", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entries": []map[string]any{
					{
						"issue_identifier": "MAES-28",
						"status":           "completed",
						"stop_reason":      "end_turn",
						"source":           "persisted",
					},
				},
			})
		}))
		t.Cleanup(server.Close)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		observation, err := waitForDashboardSessionObservation(ctx, maestromcp.DaemonEntry{
			BaseURL: server.URL + "/mcp",
		}, "MAES-28", "final")
		if err != nil {
			t.Fatalf("waitForDashboardSessionObservation(final) error = %v", err)
		}
		if observation.Source != "persisted" || observation.StopReason != "end_turn" {
			t.Fatalf("unexpected final dashboard observation: %+v", observation)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"entries": []map[string]any{}})
		}))
		t.Cleanup(server.Close)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := waitForDashboardSessionObservation(ctx, maestromcp.DaemonEntry{
			BaseURL: server.URL + "/mcp",
		}, "MAES-28", "live")
		if err == nil || err.Error() != "did not observe dashboard session before context deadline" {
			t.Fatalf("waitForDashboardSessionObservation(deadline) error = %v, want context deadline message", err)
		}
	})

	t.Run("ignores_wrong_source", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entries": []map[string]any{
					{
						"issue_identifier": "MAES-28",
						"status":           "completed",
						"stop_reason":      "end_turn",
						"source":           "persisted",
					},
				},
			})
		}))
		t.Cleanup(server.Close)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := waitForDashboardSessionObservation(ctx, maestromcp.DaemonEntry{
			BaseURL: server.URL + "/mcp",
		}, "MAES-28", "live")
		if err == nil || err.Error() != "did not observe dashboard session before context deadline" {
			t.Fatalf("waitForDashboardSessionObservation(ignores_wrong_source) error = %v, want context deadline message", err)
		}
	})
}

func TestDashboardSessionObservationForIssueErrors(t *testing.T) {
	t.Run("success_and_no_match", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entries": []any{
					"skip",
					map[string]any{
						"issue_identifier": "OTHER-1",
						"status":           "running",
						"stop_reason":      "",
						"source":           "live",
					},
					map[string]any{
						"issue_identifier": "MAES-28",
						"status":           "completed",
						"stop_reason":      "end_turn",
						"source":           "persisted",
					},
				},
			})
		}))
		t.Cleanup(server.Close)

		observation, ok, err := dashboardSessionObservationForIssue(maestromcp.DaemonEntry{
			BaseURL: server.URL + "/mcp",
		}, "MAES-28")
		if err != nil || !ok {
			t.Fatalf("dashboardSessionObservationForIssue(success) = (%+v, %v, %v), want success", observation, ok, err)
		}
		if observation.Source != "persisted" || observation.StopReason != "end_turn" {
			t.Fatalf("unexpected dashboard session observation: %+v", observation)
		}

		observation, ok, err = dashboardSessionObservationForIssue(maestromcp.DaemonEntry{
			BaseURL: server.URL + "/mcp",
		}, "MISSING-1")
		if err != nil || ok {
			t.Fatalf("dashboardSessionObservationForIssue(no_match) = (%+v, %v, %v), want no match", observation, ok, err)
		}
	})

	t.Run("missing_base_url", func(t *testing.T) {
		_, ok, err := dashboardSessionObservationForIssue(maestromcp.DaemonEntry{}, "MAES-28")
		if err == nil || err.Error() != "daemon registry base_url missing" || ok {
			t.Fatalf("dashboardSessionObservationForIssue(missing_base_url) = (%v, %v), want missing base_url error", ok, err)
		}
	})

	t.Run("bad_status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusBadGateway)
		}))
		t.Cleanup(server.Close)

		_, ok, err := dashboardSessionObservationForIssue(maestromcp.DaemonEntry{
			BaseURL: server.URL + "/mcp",
		}, "MAES-28")
		if err == nil || !strings.Contains(err.Error(), "dashboard sessions returned 502: nope") || ok {
			t.Fatalf("dashboardSessionObservationForIssue(bad_status) = (%v, %v), want 502 error", ok, err)
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("{"))
		}))
		t.Cleanup(server.Close)

		_, ok, err := dashboardSessionObservationForIssue(maestromcp.DaemonEntry{
			BaseURL: server.URL + "/mcp",
		}, "MAES-28")
		if err == nil || !strings.Contains(err.Error(), "unexpected end of JSON input") || ok {
			t.Fatalf("dashboardSessionObservationForIssue(invalid_json) = (%v, %v), want JSON decode error", ok, err)
		}
	})
}

func TestRunWithInterruptObservation(t *testing.T) {
	fixture := newProbeRunFixture(t, "happy")
	interaction := validPendingInterrupt()
	interaction.IssueIdentifier = fixture.opts.issueIdentifier
	interaction.Approval.Command = "printf 'ok'\n"

	var mu sync.Mutex
	responded := false
	captured := agentruntime.PendingInteractionResponse{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-a" {
			t.Fatalf("Authorization header = %q, want Bearer token-a", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/app/sessions":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entries": []map[string]any{
					{
						"issue_identifier": fixture.opts.issueIdentifier,
						"status":           "running",
						"stop_reason":      "",
						"source":           "live",
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/app/interrupts":
			mu.Lock()
			items := []agentruntime.PendingInteraction{interaction}
			if responded {
				items = nil
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(agentruntime.PendingInteractionSnapshot{Items: items})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/app/interrupts/"+interaction.ID+"/respond":
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode response payload: %v", err)
			}
			mu.Lock()
			responded = true
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "accepted"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	writeJSONFile(t, fixture.registryEntryPath, maestromcp.DaemonEntry{
		StoreID:     fixture.storeID,
		DBPath:      fixture.dbPath,
		PID:         10,
		BaseURL:     server.URL + "/mcp",
		BearerToken: "token-a",
		Version:     "1.0.0",
		Transport:   "stdio",
	})

	fixture.opts.allowedTools = ""
	fixture.opts.permissionPromptTool = "mcp__maestro__approval_prompt"
	fixture.opts.interruptClass = "command"
	fixture.opts.interruptToolName = "Bash"
	fixture.opts.interruptDecision = "allow"
	fixture.opts.interruptNote = "operator approved"

	if err := run(fixture.opts); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	mu.Lock()
	if !responded {
		mu.Unlock()
		t.Fatal("expected interrupt response to be posted")
	}
	mu.Unlock()
	if captured.Decision != "allow" || captured.Note != "operator approved" {
		t.Fatalf("captured response = %+v, want allow/operator approved", captured)
	}

	jsonBytes, err := os.ReadFile(fixture.evidencePrefix + ".json")
	if err != nil {
		t.Fatalf("read JSON evidence: %v", err)
	}
	var evidence probeEvidence
	if err := json.Unmarshal(jsonBytes, &evidence); err != nil {
		t.Fatalf("decode JSON evidence: %v", err)
	}
	if !evidence.Interrupt.Requested || !evidence.Interrupt.Cleared {
		t.Fatalf("interrupt evidence = %+v, want requested+cleared", evidence.Interrupt)
	}
	if evidence.Interrupt.ResponseStatus != "accepted" || evidence.Interrupt.ResponseDecision != "allow" {
		t.Fatalf("interrupt response evidence = %+v", evidence.Interrupt)
	}
	if got := strings.TrimSpace(asString(evidence.Interrupt.Interaction.Metadata["classification"])); got != "command" {
		t.Fatalf("interrupt classification = %q, want command", got)
	}
	if len(evidence.RuntimeEvents.Items) != 1 || evidence.RuntimeEvents.Items[0].Kind != "run_started" {
		t.Fatalf("runtime events = %+v, want single run_started event", evidence.RuntimeEvents.Items)
	}

	summaryBytes, err := os.ReadFile(fixture.evidencePrefix + ".summary.txt")
	if err != nil {
		t.Fatalf("read summary evidence: %v", err)
	}
	summary := string(summaryBytes)
	for _, want := range []string{
		"interrupt_cleared=true",
		"interrupt_classification=command",
		"interrupt_response_decision=allow",
		"interrupt_response_status=accepted",
		"permission_prompt_tool=mcp__maestro__approval_prompt",
		"runtime_event_count=1",
		"runtime_event_kinds=run_started",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary evidence missing %q: %s", want, summary)
		}
	}
}

func TestPendingInterruptHelpers(t *testing.T) {
	t.Run("waits_for_interrupt_and_clear", func(t *testing.T) {
		var mu sync.Mutex
		interaction := validPendingInterrupt()
		seenMatchingPoll := false

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/app/interrupts":
				mu.Lock()
				items := []agentruntime.PendingInteraction{}
				if !seenMatchingPoll {
					items = []agentruntime.PendingInteraction{
						{ID: "other", IssueIdentifier: "OTHER-1", Metadata: map[string]interface{}{"classification": "command", "tool_name": "Bash"}},
						interaction,
					}
					seenMatchingPoll = true
				}
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(agentruntime.PendingInteractionSnapshot{Items: items})
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/app/interrupts/"+interaction.ID+"/respond":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "accepted"})
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)

		entry := maestromcp.DaemonEntry{BaseURL: server.URL + "/mcp"}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		got, pendingCount, err := waitForPendingInterrupt(ctx, entry, "CL-1", "command", "Bash")
		if err != nil {
			t.Fatalf("waitForPendingInterrupt() error = %v", err)
		}
		if pendingCount != 2 {
			t.Fatalf("pendingCount = %d, want 2", pendingCount)
		}
		if got.ID != interaction.ID {
			t.Fatalf("interaction ID = %q, want %q", got.ID, interaction.ID)
		}

		status, err := respondToPendingInterrupt(entry, interaction.ID, agentruntime.PendingInteractionResponse{Decision: "allow"})
		if err != nil {
			t.Fatalf("respondToPendingInterrupt() error = %v", err)
		}
		if status != "accepted" {
			t.Fatalf("respondToPendingInterrupt() status = %q, want accepted", status)
		}
		if err := waitForPendingInterruptClear(ctx, entry, interaction.ID); err != nil {
			t.Fatalf("waitForPendingInterruptClear() error = %v", err)
		}
	})

	t.Run("fetch_and_respond_errors", func(t *testing.T) {
		t.Run("fetch_bad_status", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", http.StatusBadGateway)
			}))
			t.Cleanup(server.Close)

			_, err := fetchPendingInterrupts(maestromcp.DaemonEntry{BaseURL: server.URL + "/mcp"})
			if err == nil || !strings.Contains(err.Error(), "dashboard interrupts returned 502: nope") {
				t.Fatalf("fetchPendingInterrupts() error = %v, want 502 error", err)
			}
		})

		t.Run("fetch_invalid_json", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("{"))
			}))
			t.Cleanup(server.Close)

			_, err := fetchPendingInterrupts(maestromcp.DaemonEntry{BaseURL: server.URL + "/mcp"})
			if err == nil || !strings.Contains(err.Error(), "unexpected end of JSON input") {
				t.Fatalf("fetchPendingInterrupts() error = %v, want JSON decode error", err)
			}
		})

		t.Run("respond_bad_status", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", http.StatusConflict)
			}))
			t.Cleanup(server.Close)

			_, err := respondToPendingInterrupt(maestromcp.DaemonEntry{BaseURL: server.URL + "/mcp"}, "interrupt-1", agentruntime.PendingInteractionResponse{Decision: "allow"})
			if err == nil || !strings.Contains(err.Error(), "dashboard interrupt respond returned 409: nope") {
				t.Fatalf("respondToPendingInterrupt() error = %v, want 409 error", err)
			}
		})

		t.Run("respond_invalid_json", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte("{"))
			}))
			t.Cleanup(server.Close)

			_, err := respondToPendingInterrupt(maestromcp.DaemonEntry{BaseURL: server.URL + "/mcp"}, "interrupt-1", agentruntime.PendingInteractionResponse{Decision: "allow"})
			if err == nil || !strings.Contains(err.Error(), "unexpected end of JSON input") {
				t.Fatalf("respondToPendingInterrupt() error = %v, want JSON decode error", err)
			}
		})
	})

	t.Run("deadline_errors", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(agentruntime.PendingInteractionSnapshot{
				Items: []agentruntime.PendingInteraction{validPendingInterrupt()},
			})
		}))
		t.Cleanup(server.Close)

		entry := maestromcp.DaemonEntry{BaseURL: server.URL + "/mcp"}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, _, err := waitForPendingInterrupt(ctx, entry, "MISSING-1", "command", "Bash")
		if err == nil || err.Error() != "did not observe a matching pending interrupt before context deadline" {
			t.Fatalf("waitForPendingInterrupt(deadline) error = %v", err)
		}

		if err := waitForPendingInterruptClear(ctx, entry, validPendingInterrupt().ID); err == nil || err.Error() != "pending interrupt did not clear before context deadline" {
			t.Fatalf("waitForPendingInterruptClear(deadline) error = %v", err)
		}
	})

	t.Run("dashboard_request_missing_base_url", func(t *testing.T) {
		_, _, err := dashboardRequest(maestromcp.DaemonEntry{}, http.MethodGet, "/api/v1/app/interrupts", nil)
		if err == nil || err.Error() != "daemon registry base_url missing" {
			t.Fatalf("dashboardRequest() error = %v, want missing base_url", err)
		}
	})
}

func TestBoolFromMap(t *testing.T) {
	t.Parallel()

	if !boolFromMap(map[string]interface{}{"ok": true}, "ok") {
		t.Fatal("boolFromMap(true) = false, want true")
	}
	if boolFromMap(map[string]interface{}{"ok": "true"}, "ok") {
		t.Fatal("boolFromMap(non-bool) = true, want false")
	}
	if boolFromMap(map[string]interface{}{}, "missing") {
		t.Fatal("boolFromMap(missing) = true, want false")
	}
}

func TestMainHappyPath(t *testing.T) {
	fixture := newProbeRunFixture(t, "happy")

	cmd := exec.Command(
		os.Args[0],
		"-test.run=TestHelperProcessProbeMain",
		"--",
		"-mode="+fixture.opts.mode,
		"-mcp-config="+fixture.opts.mcpConfig,
		"-settings="+fixture.opts.settings,
		"-db="+fixture.opts.dbPath,
		"-registry-dir="+fixture.opts.registryDir,
		"-evidence-prefix="+fixture.opts.evidencePrefix,
		"-issue-identifier="+fixture.opts.issueIdentifier,
		"-allowed-tools="+fixture.opts.allowedTools,
		"-permission-mode="+fixture.opts.permissionMode,
		"-strict-mcp-config="+fixture.opts.strictMCPConfig,
	)
	cmd.Env = append(os.Environ(), "GO_WANT_PROBE_MAIN=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("main subprocess error = %v\n%s", err, output)
	}

	if _, err := os.Stat(fixture.evidencePrefix + ".json"); err != nil {
		t.Fatalf("expected JSON evidence after main(): %v", err)
	}
}

func TestLoadSettingsOverlayErrors(t *testing.T) {
	t.Parallel()

	t.Run("missing_file", func(t *testing.T) {
		t.Parallel()
		_, _, err := loadSettingsOverlay(filepath.Join(t.TempDir(), "missing.json"))
		if err == nil || !strings.Contains(err.Error(), "read settings overlay") {
			t.Fatalf("loadSettingsOverlay() error = %v, want read settings overlay", err)
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatalf("write invalid JSON: %v", err)
		}
		_, _, err := loadSettingsOverlay(path)
		if err == nil || !strings.Contains(err.Error(), "decode settings overlay") {
			t.Fatalf("loadSettingsOverlay() error = %v, want decode settings overlay", err)
		}
	})
}

func TestLoadBridgeConfigAdditionalFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "invalid_json",
			body: "{",
			want: "decode MCP config",
		},
		{
			name: "multiple_servers",
			body: `{"mcpServers":{"maestro":{"type":"stdio","command":"maestro","args":["mcp","--db","db.sqlite"]},"other":{"type":"stdio","command":"maestro","args":["mcp","--db","db.sqlite"]}}}`,
			want: "expected exactly one MCP server in config",
		},
		{
			name: "bad_args_prefix",
			body: `{"mcpServers":{"maestro":{"type":"stdio","command":"maestro","args":["serve","--db","db.sqlite"]}}}`,
			want: `expected maestro MCP args to start with "mcp --db"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configPath := filepath.Join(t.TempDir(), "mcp.json")
			if err := os.WriteFile(configPath, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, _, _, _, err := loadBridgeConfig(configPath)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("loadBridgeConfig() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadDaemonEntriesErrors(t *testing.T) {
	t.Parallel()

	t.Run("missing_dir", func(t *testing.T) {
		t.Parallel()
		_, err := loadDaemonEntries(filepath.Join(t.TempDir(), "missing"))
		if err == nil || !strings.Contains(err.Error(), "read daemon registry dir") {
			t.Fatalf("loadDaemonEntries() error = %v, want read daemon registry dir", err)
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "entry.json"), []byte("{"), 0o600); err != nil {
			t.Fatalf("write invalid entry: %v", err)
		}
		_, err := loadDaemonEntries(dir)
		if err == nil || !strings.Contains(err.Error(), "decode daemon registry entry entry.json") {
			t.Fatalf("loadDaemonEntries() error = %v, want decode daemon registry entry", err)
		}
	})
}

func TestWriteEvidenceErrors(t *testing.T) {
	t.Parallel()

	t.Run("json_write", func(t *testing.T) {
		t.Parallel()
		prefix := filepath.Join(t.TempDir(), "missing", "launch")
		err := writeEvidence(prefix, probeEvidence{})
		if err == nil || !strings.Contains(err.Error(), "write JSON evidence") {
			t.Fatalf("writeEvidence() error = %v, want write JSON evidence", err)
		}
	})

	t.Run("summary_write", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		prefix := filepath.Join(dir, "launch")
		if err := os.Mkdir(prefix+".summary.txt", 0o755); err != nil {
			t.Fatalf("mkdir summary path: %v", err)
		}
		err := writeEvidence(prefix, probeEvidence{})
		if err == nil || !strings.Contains(err.Error(), "write summary evidence") {
			t.Fatalf("writeEvidence() error = %v, want write summary evidence", err)
		}
	})
}

func TestFirstNonEmptyAllEmpty(t *testing.T) {
	t.Parallel()
	if got := firstNonEmpty("", " ", "\t"); got != "" {
		t.Fatalf("firstNonEmpty() = %q, want empty", got)
	}
}

type probeRunFixture struct {
	opts              options
	dbPath            string
	storeID           string
	registryDir       string
	registryEntryPath string
	evidencePrefix    string
}

func seedProbeFixtureStore(t *testing.T, dbPath, issueIdentifier string) {
	t.Helper()

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("create probe store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close probe store: %v", err)
		}
	}()

	projectPrefix := "MAES"
	targetOrdinal := 28
	if rawPrefix, rawOrdinal, ok := strings.Cut(issueIdentifier, "-"); ok {
		if trimmedPrefix := strings.TrimSpace(rawPrefix); trimmedPrefix != "" {
			projectPrefix = trimmedPrefix
		}
		if ordinal, err := strconv.Atoi(strings.TrimSpace(rawOrdinal)); err == nil && ordinal > 0 {
			targetOrdinal = ordinal
		}
	}

	project, err := store.CreateProject(projectPrefix, "", "", "")
	if err != nil {
		t.Fatalf("CreateProject(%q): %v", projectPrefix, err)
	}
	for i := 1; i < targetOrdinal; i++ {
		if _, err := store.CreateIssue(project.ID, "", fmt.Sprintf("Seed issue %d", i), "", 0, nil); err != nil {
			t.Fatalf("seed issue %d: %v", i, err)
		}
	}

	issue, err := store.CreateIssue(project.ID, "", "Probe issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue probe: %v", err)
	}
	if issue.Identifier != issueIdentifier {
		t.Fatalf("probe issue identifier = %q, want %q", issue.Identifier, issueIdentifier)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState(%s): %v", issueIdentifier, err)
	}
}

func newProbeRunFixture(t *testing.T, scenario string) probeRunFixture {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "maestro.db")
	absDBPath, err := filepath.Abs(dbPath)
	if err != nil {
		t.Fatalf("resolve db path: %v", err)
	}
	issueIdentifier := "MAES-28"
	registryDir := filepath.Join(root, "registry")
	if err := os.MkdirAll(registryDir, 0o755); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	configPath := filepath.Join(root, "mcp.json")
	settingsPath := filepath.Join(root, "settings.json")
	evidencePrefix := filepath.Join(root, "evidence")
	storeID := "store-a"
	dashboardStatus := "running"
	dashboardStopReason := ""
	dashboardSource := "live"
	if scenario == "execution-final" {
		dashboardStatus = "completed"
		dashboardStopReason = "end_turn"
		dashboardSource = "persisted"
	}
	dashboardServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/app/sessions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"entries": []map[string]any{
				{
					"issue_identifier": issueIdentifier,
					"status":           dashboardStatus,
					"stop_reason":      dashboardStopReason,
					"source":           dashboardSource,
				},
			},
		})
	}))
	t.Cleanup(dashboardServer.Close)
	dashboardBaseURL := dashboardServer.URL + "/mcp"

	seedProbeFixtureStore(t, absDBPath, issueIdentifier)

	writeJSONFile(t, configPath, map[string]any{
		"mcpServers": map[string]any{
			"maestro": map[string]any{
				"type":    "stdio",
				"command": "maestro",
				"args":    []string{"mcp", "--db", absDBPath},
			},
		},
	})
	writeJSONFile(t, settingsPath, map[string]any{
		"disableAutoMode":        "disable",
		"useAutoModeDuringPlan":  false,
		"disableAllHooks":        true,
		"includeGitInstructions": false,
		"permissions": map[string]any{
			"disableBypassPermissionsMode": "disable",
		},
	})

	registryEntryPath := filepath.Join(registryDir, "entry.json")
	writeJSONFile(t, registryEntryPath, maestromcp.DaemonEntry{
		StoreID:     storeID,
		DBPath:      absDBPath,
		PID:         10,
		BaseURL:     dashboardBaseURL,
		BearerToken: "token-a",
		Version:     "1.0.0",
		Transport:   "stdio",
	})

	binDir := t.TempDir()
	wrapperPath := filepath.Join(binDir, "maestro")
	wrapper := "#!/bin/sh\nexec \"$GO_PROBE_TEST_BINARY\" -test.run=TestHelperProcessProbeMCPServer -- \"$@\"\n"
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	t.Setenv("GO_PROBE_TEST_BINARY", os.Args[0])
	t.Setenv("GO_WANT_PROBE_MCP_SERVER", "1")
	t.Setenv("GO_PROBE_SCENARIO", scenario)
	t.Setenv("GO_PROBE_DB_PATH", absDBPath)
	t.Setenv("GO_PROBE_STORE_ID", storeID)
	t.Setenv("GO_PROBE_REGISTRY_DIR", registryDir)
	t.Setenv("GO_PROBE_REGISTRY_ENTRY", registryEntryPath)
	t.Setenv("GO_PROBE_BASE_URL", dashboardBaseURL)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return probeRunFixture{
		opts: options{
			mode:                 "live",
			issueIdentifier:      issueIdentifier,
			mcpConfig:            configPath,
			settings:             settingsPath,
			dbPath:               absDBPath,
			registryDir:          registryDir,
			evidencePrefix:       evidencePrefix,
			allowedTools:         "Bash,Edit,Write,MultiEdit",
			permissionMode:       "default",
			strictMCPConfig:      "true",
			permissionPromptTool: "",
		},
		dbPath:            absDBPath,
		storeID:           storeID,
		registryDir:       registryDir,
		registryEntryPath: registryEntryPath,
		evidencePrefix:    evidencePrefix,
	}
}

func newProbeTestClient(t *testing.T, scenario string) *mcpclient.Client {
	t.Helper()

	envPath, err := exec.LookPath("env")
	if err != nil {
		t.Fatalf("env lookup failed: %v", err)
	}
	args := []string{
		"GO_WANT_PROBE_MCP_SERVER=1",
		"GO_PROBE_SCENARIO=" + scenario,
		"GO_PROBE_DB_PATH=/tmp/maestro-probe.db",
		"GO_PROBE_STORE_ID=store-test",
		os.Args[0],
		"-test.run=TestHelperProcessProbeMCPServer",
		"--",
	}
	client, err := mcpclient.NewStdioMCPClient(envPath, nil, args...)
	if err != nil {
		t.Fatalf("NewStdioMCPClient() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Initialize(ctx, mcpapi.InitializeRequest{
		Params: mcpapi.InitializeParams{
			ProtocolVersion: mcpapi.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpapi.Implementation{Name: "probe-test", Version: "1.0.0"},
			Capabilities:    mcpapi.ClientCapabilities{},
		},
	}); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	return client
}

func TestHelperProcessProbeMCPServer(t *testing.T) {
	if os.Getenv("GO_WANT_PROBE_MCP_SERVER") != "1" {
		return
	}

	scenario := strings.TrimSpace(os.Getenv("GO_PROBE_SCENARIO"))
	dbPath := firstNonEmpty(os.Getenv("GO_PROBE_DB_PATH"), "/tmp/maestro-probe.db")
	storeID := firstNonEmpty(os.Getenv("GO_PROBE_STORE_ID"), "store-test")
	registryDir := strings.TrimSpace(os.Getenv("GO_PROBE_REGISTRY_DIR"))
	registryEntryPath := strings.TrimSpace(os.Getenv("GO_PROBE_REGISTRY_ENTRY"))
	baseURL := firstNonEmpty(strings.TrimSpace(os.Getenv("GO_PROBE_BASE_URL")), "http://127.0.0.1:8080/mcp")
	listSessionCalls := 0

	mcp := mcpserver.NewMCPServer("probe-helper", "1.0.0")
	toolNames := []string{"approval_prompt", "create_issue", "get_issue_execution", "get_runtime_snapshot", "list_issues", "list_runtime_events", "list_sessions", "server_info"}
	if scenario == "missing-tool" {
		toolNames = []string{"approval_prompt", "get_issue_execution", "get_runtime_snapshot", "list_issues", "list_runtime_events", "list_sessions", "server_info"}
	}
	for _, name := range toolNames {
		toolName := name
		mcp.AddTool(mcpapi.NewTool(toolName), func(context.Context, mcpapi.CallToolRequest) (*mcpapi.CallToolResult, error) {
			return probeToolResult(toolName, scenario, dbPath, storeID, registryDir, registryEntryPath, baseURL, &listSessionCalls)
		})
	}

	if err := mcpserver.ServeStdio(mcp); err != nil {
		t.Fatalf("ServeStdio() error = %v", err)
	}
}

func TestHelperProcessProbeMain(t *testing.T) {
	if os.Getenv("GO_WANT_PROBE_MAIN") != "1" {
		return
	}

	args := []string{"maestro-claude-e2e-probe"}
	for i, arg := range os.Args {
		if arg == "--" {
			args = append(args, os.Args[i+1:]...)
			break
		}
	}
	os.Args = args
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	main()
	os.Exit(0)
}

func probeToolResult(name, scenario, dbPath, storeID, registryDir, registryEntryPath, baseURL string, listSessionCalls *int) (*mcpapi.CallToolResult, error) {
	meta := responseEnvelopeMeta{
		DBPath:           dbPath,
		StoreID:          storeID,
		ServerInstanceID: "probe-helper",
		ChangeSeq:        1,
	}

	switch name {
	case "server_info":
		switch scenario {
		case "call-no-content":
			return &mcpapi.CallToolResult{}, nil
		case "call-wrong-content":
			return &mcpapi.CallToolResult{Content: []mcpapi.Content{mcpapi.NewImageContent("Zm9v", "image/png")}}, nil
		case "call-invalid-json":
			return mcpapi.NewToolResultText("{"), nil
		case "call-fail":
			return probeEnvelopeResult("server_info", meta, nil, false, "scenario failure")
		case "call-fail-empty-message":
			return probeEnvelopeResult("server_info", meta, nil, false, "")
		case "runtime-unavailable":
			return probeEnvelopeResult("server_info", meta, map[string]any{
				"project_count":     0,
				"issue_count":       0,
				"runtime_available": false,
			}, true, "")
		case "server-info-bad-data":
			return probeEnvelopeResultWithRawData("server_info", meta, json.RawMessage(`"bad"`), true, "")
		case "server-info-db-mismatch":
			meta.DBPath = filepath.Join(filepath.Dir(dbPath), "other.db")
		case "server-info-store-mismatch":
			meta.StoreID = "store-other"
		default:
		}
		return probeEnvelopeResult("server_info", meta, map[string]any{
			"project_count":     0,
			"issue_count":       0,
			"runtime_available": true,
		}, true, "")
	case "create_issue":
		return probeEnvelopeResult("create_issue", meta, map[string]any{"id": "iss_1"}, true, "")
	case "list_issues":
		if scenario == "list-issues-invalid-json" {
			return mcpapi.NewToolResultText("{"), nil
		}
		if scenario == "list-issues-bad-data" {
			return probeEnvelopeResultWithRawData("list_issues", meta, json.RawMessage(`"bad"`), true, "")
		}
		return probeEnvelopeResult("list_issues", meta, map[string]any{
			"items": []any{
				map[string]any{
					"identifier": "MAES-28",
					"state":      "ready",
				},
			},
			"total":      1,
			"limit":      50,
			"offset":     0,
			"pagination": map[string]any{},
		}, true, "")
	case "get_issue_execution":
		if scenario == "execution-bad-data" {
			return probeEnvelopeResultWithRawData("get_issue_execution", meta, json.RawMessage(`"bad"`), true, "")
		}
		if scenario == "execution-final" {
			return probeEnvelopeResult("get_issue_execution", meta, map[string]any{
				"active":            false,
				"session_source":    "persisted",
				"failure_class":     "",
				"stop_reason":       "end_turn",
				"runtime_name":      "claude",
				"runtime_provider":  "claude",
				"runtime_transport": "stdio",
				"session": agentruntime.Session{
					IssueID:         "iss_1",
					IssueIdentifier: "MAES-28",
					SessionID:       "thread-1",
					ThreadID:        "thread-1",
					TurnID:          "turn-1",
					LastEvent:       "turn.completed",
					LastMessage:     "STREAM:MAES-28:success-live",
					Metadata: map[string]interface{}{
						"provider":                    "claude",
						"transport":                   "stdio",
						"provider_session_id":         "thread-1",
						"session_identifier_strategy": "provider_session_uuid",
					},
				},
			}, true, "")
		}
		return probeEnvelopeResult("get_issue_execution", meta, map[string]any{
			"active":            true,
			"session_source":    "live",
			"failure_class":     "",
			"stop_reason":       "",
			"runtime_name":      "claude",
			"runtime_provider":  "claude",
			"runtime_transport": "stdio",
			"session": agentruntime.Session{
				IssueID:         "iss_1",
				IssueIdentifier: "MAES-28",
				SessionID:       "thread-1",
				ThreadID:        "thread-1",
				TurnID:          "turn-1",
				LastEvent:       "turn.started",
				LastMessage:     "STREAM:MAES-28:success-live",
				Metadata: map[string]interface{}{
					"provider":                    "claude",
					"transport":                   "stdio",
					"provider_session_id":         "thread-1",
					"session_identifier_strategy": "provider_session_uuid",
				},
			},
		}, true, "")
	case "get_runtime_snapshot":
		if scenario == "runtime-snapshot-invalid-json" {
			return mcpapi.NewToolResultText("{"), nil
		}
		if scenario == "runtime-snapshot-bad-data" {
			return probeEnvelopeResultWithRawData("get_runtime_snapshot", meta, json.RawMessage(`"bad"`), true, "")
		}
		return probeEnvelopeResult("get_runtime_snapshot", meta, map[string]any{
			"running": []any{},
		}, true, "")
	case "list_sessions":
		if listSessionCalls != nil {
			*listSessionCalls++
		}
		if scenario == "list-sessions-empty" {
			return probeEnvelopeResult("list_sessions", meta, map[string]any{
				"sessions": map[string]any{},
			}, true, "")
		}
		if scenario == "list-sessions-bad-data" {
			return probeEnvelopeResultWithRawData("list_sessions", meta, json.RawMessage(`"bad"`), true, "")
		}
		if scenario == "list-sessions-mixed" && listSessionCalls != nil && *listSessionCalls == 1 {
			return probeEnvelopeResult("list_sessions", meta, map[string]any{
				"sessions": map[string]any{
					"wrong-provider": agentruntime.Session{
						SessionID: "session-provider",
						ThreadID:  "thread-provider",
						Metadata: map[string]interface{}{
							"provider":  "codex",
							"transport": "stdio",
						},
					},
					"wrong-transport": agentruntime.Session{
						SessionID: "session-transport",
						ThreadID:  "thread-transport",
						Metadata: map[string]interface{}{
							"provider":  "claude",
							"transport": "http",
						},
					},
					"missing-ids": agentruntime.Session{
						Metadata: map[string]interface{}{
							"provider":  "claude",
							"transport": "stdio",
						},
					},
				},
			}, true, "")
		}
		if scenario == "registry-count-after" && strings.TrimSpace(registryDir) != "" {
			writeJSONFileForHelper(filepath.Join(registryDir, "second.json"), maestromcp.DaemonEntry{
				StoreID: "store-b",
				DBPath:  dbPath,
			})
		}
		if scenario == "registry-drift-after" && strings.TrimSpace(registryEntryPath) != "" {
			writeJSONFileForHelper(registryEntryPath, maestromcp.DaemonEntry{
				StoreID:     storeID,
				DBPath:      dbPath,
				PID:         10,
				BaseURL:     baseURL,
				BearerToken: "token-b",
				Version:     "1.0.0",
				Transport:   "stdio",
			})
		}
		if scenario == "registry-db-drift-after" && strings.TrimSpace(registryEntryPath) != "" {
			writeJSONFileForHelper(registryEntryPath, maestromcp.DaemonEntry{
				StoreID:     storeID,
				DBPath:      filepath.Join(filepath.Dir(dbPath), "other.db"),
				PID:         10,
				BaseURL:     baseURL,
				BearerToken: "token-a",
				Version:     "1.0.0",
				Transport:   "stdio",
			})
		}
		return probeEnvelopeResult("list_sessions", meta, map[string]any{
			"sessions": map[string]any{
				"MAES-28": agentruntime.Session{
					IssueID:         "iss_1",
					IssueIdentifier: "MAES-28",
					SessionID:       "session-1",
					ThreadID:        "thread-1",
					TurnID:          "turn-1",
					LastEvent:       "turn.started",
					LastMessage:     "Working",
					Metadata: map[string]interface{}{
						"provider":  "claude",
						"transport": "stdio",
					},
				},
			},
		}, true, "")
	case "list_runtime_events":
		events := []kanban.RuntimeEvent{
			{Identifier: "OTHER-1", Kind: "run_completed"},
		}
		switch scenario {
		case "execution-final":
			events = append(events, kanban.RuntimeEvent{Identifier: "MAES-28", Kind: "run_completed"})
		default:
			events = append(events, kanban.RuntimeEvent{Identifier: "MAES-28", Kind: "run_started"})
		}
		return probeEnvelopeResult("list_runtime_events", meta, map[string]any{
			"events": events,
		}, true, "")
	default:
		return probeEnvelopeResult(name, meta, map[string]any{}, true, "")
	}
}

func probeEnvelopeResult(tool string, meta responseEnvelopeMeta, data any, ok bool, message string) (*mcpapi.CallToolResult, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return probeEnvelopeResultWithRawData(tool, meta, payload, ok, message)
}

func probeEnvelopeResultWithRawData(tool string, meta responseEnvelopeMeta, payload json.RawMessage, ok bool, message string) (*mcpapi.CallToolResult, error) {
	envelope := responseEnvelope{
		OK:   ok,
		Tool: tool,
		Meta: meta,
		Data: payload,
	}
	if !ok {
		envelope.Error = &responseError{Message: message}
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return mcpapi.NewToolResultText(string(body)), nil
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

func writeJSONFileForHelper(path string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		panic(err)
	}
}

type testStringer string

func (s testStringer) String() string {
	return string(s)
}
