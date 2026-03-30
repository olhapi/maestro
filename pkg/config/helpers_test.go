package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeWorkflowKeysMigratesLegacyFields(t *testing.T) {
	raw := map[string]interface{}{
		"tracker_kind":           "kanban",
		"tracker_active_states":  "ready, in_progress, , in_review",
		"tracker_terminal_states": []interface{}{"done", "cancelled"},
		"poll_interval":          5000,
		"workspace_root":        "./workspaces",
		"hooks_timeout_ms":      1234,
		"max_concurrent":        5,
		"max_turns":            6,
		"max_retry_backoff_ms": 7,
		"max_automatic_retries": 8,
		"agent_mode":           "stdio",
		"dispatch_mode":        "per_project_serial",
		"codex_command":        "codex app-server",
		"codex_expected_version": "dev",
		"codex_approval_policy": map[interface{}]interface{}{
			"granular": map[interface{}]interface{}{
				"sandbox_approval":    true,
				"rules":               true,
				"mcp_elicitations":    true,
				"request_permissions": false,
			},
		},
		"codex_initial_collaboration_mode": "plan",
		"codex_turn_timeout_ms":            9,
		"codex_read_timeout_ms":            10,
		"codex_stall_timeout_ms":           11,
	}

	normalized, err := normalizeWorkflowKeys(raw)
	if err != nil {
		t.Fatalf("normalizeWorkflowKeys: %v", err)
	}
	if _, ok := normalized["tracker_kind"]; ok {
		t.Fatal("expected tracker_kind to be moved")
	}
	tracker := normalized["tracker"].(map[string]interface{})
	if tracker["kind"] != "kanban" {
		t.Fatalf("unexpected tracker kind: %#v", tracker)
	}
	if got := tracker["active_states"].([]string); !reflect.DeepEqual(got, []string{"ready", "in_progress", "in_review"}) {
		t.Fatalf("unexpected active states: %#v", got)
	}
	if got := tracker["terminal_states"].([]interface{}); !reflect.DeepEqual(got, []interface{}{"done", "cancelled"}) {
		t.Fatalf("unexpected terminal states: %#v", got)
	}
	polling := normalized["polling"].(map[string]interface{})
	if polling["interval_ms"] != 5000 {
		t.Fatalf("unexpected polling interval: %#v", polling)
	}
	workspace := normalized["workspace"].(map[string]interface{})
	if workspace["root"] != "./workspaces" {
		t.Fatalf("unexpected workspace root: %#v", workspace)
	}
	codex := normalized["codex"].(map[string]interface{})
	if codex["command"] != "codex app-server" || codex["expected_version"] != "dev" {
		t.Fatalf("unexpected codex values: %#v", codex)
	}
	if _, ok := codex["approval_policy"].(map[string]interface{}); !ok {
		t.Fatalf("expected approval policy to normalize to map, got %#v", codex["approval_policy"])
	}
	phases := normalized["phases"].(map[string]interface{})
	if phases["review"].(map[string]interface{})["enabled"] != true || phases["done"].(map[string]interface{})["enabled"] != true {
		t.Fatalf("expected phase defaults, got %#v", phases)
	}
}

func TestMoveAndSplitHelpers(t *testing.T) {
	root := map[string]interface{}{
		"map": map[string]interface{}{"mode": "safe"},
		"string": "value",
		"numeric": float64(9),
		"slice": "a, b, ,c",
	}
	dest := map[string]interface{}{}

	moveMap(root, dest, "map", "nested")
	moveValue(root, dest, "string", "text")
	moveNumeric(root, dest, "numeric", "count")
	moveStringSlice(root, dest, "slice", "items")

	if _, ok := root["map"]; ok {
		t.Fatal("expected moveMap to remove source key")
	}
	if dest["text"] != "value" || dest["count"] != float64(9) {
		t.Fatalf("unexpected moveValue/moveNumeric results: %#v", dest)
	}
	if got := dest["items"].([]string); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("unexpected moveStringSlice result: %#v", got)
	}
	if got := splitCSVValues("x, y, , z"); !reflect.DeepEqual(got, []string{"x", "y", "z"}) {
		t.Fatalf("unexpected splitCSVValues result: %#v", got)
	}
	if move := ensureMap(dest, "nested"); move == nil || move["mode"] != "safe" {
		t.Fatalf("expected ensureMap to preserve nested map, got %#v", move)
	}
	setBoolDefault(dest, "enabled", true)
	if dest["enabled"] != true {
		t.Fatalf("expected setBoolDefault to populate missing key, got %#v", dest)
	}
	setBoolDefault(dest, "enabled", false)
	if dest["enabled"] != true {
		t.Fatalf("expected setBoolDefault not to overwrite existing key, got %#v", dest)
	}
}

func TestDefaultsValidationAndPathHelpers(t *testing.T) {
	cfg := Config{
		Phases: PhasesConfig{
			Review: PhasePromptConfig{Enabled: true},
			Done:   PhasePromptConfig{Enabled: true},
		},
	}
	if err := applyDefaults(&cfg); err != nil {
		t.Fatalf("applyDefaults: %v", err)
	}
	if cfg.Tracker.Kind != TrackerKindKanban || cfg.Agent.Mode != AgentModeAppServer || cfg.Codex.InitialCollaborationMode != InitialCollaborationModeDefault {
		t.Fatalf("expected defaults to populate missing fields, got %+v", cfg)
	}
	if !strings.Contains(cfg.Phases.Review.Prompt, "review pass") || !strings.Contains(cfg.Phases.Done.Prompt, "done phase") {
		t.Fatalf("expected default phase prompts, got %+v", cfg.Phases)
	}
	if err := validateConfig(&cfg); err != nil {
		t.Fatalf("validateConfig: %v", err)
	}

	bad := cfg
	bad.Tracker.Kind = "jira"
	if err := validateConfig(&bad); err == nil || !strings.Contains(err.Error(), "unsupported tracker.kind") {
		t.Fatalf("expected tracker validation error, got %v", err)
	}
	bad = cfg
	bad.Agent.Mode = "invalid"
	if err := validateConfig(&bad); err == nil || !strings.Contains(err.Error(), "unsupported agent.mode") {
		t.Fatalf("expected agent validation error, got %v", err)
	}
	bad = cfg
	bad.Codex.ApprovalPolicy = 123
	if err := validateConfig(&bad); err == nil || !strings.Contains(err.Error(), "codex.approval_policy") {
		t.Fatalf("expected approval policy validation error, got %v", err)
	}

	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("MAESTRO_PATH", "/tmp/maestro")
	t.Setenv("HOME", home)
	if got := expandPathValue("$MAESTRO_PATH/work"); got != "/tmp/maestro/work" {
		t.Fatalf("unexpected expanded env path: %q", got)
	}
	if got := expandPathValue("~"); got != home {
		t.Fatalf("expected home expansion, got %q", got)
	}
	if got := resolvePathValue("/repo", "./work", "fallback"); got != filepath.Clean("/repo/work") {
		t.Fatalf("unexpected relative path resolution: %q", got)
	}
	if got := resolvePathValue("/repo", "", "$MAESTRO_PATH/db"); got != filepath.Clean("/tmp/maestro/db") {
		t.Fatalf("unexpected env path resolution: %q", got)
	}
	if got, ok := canonicalApprovalPolicyString("on_request"); !ok || got != "on-request" {
		t.Fatalf("unexpected canonical approval policy: %q %v", got, ok)
	}
	if _, ok := canonicalApprovalPolicyString("maybe"); ok {
		t.Fatal("expected invalid policy to fail canonicalization")
	}
	if got := normalizeInitialCollaborationMode(" PLAN "); got != "plan" {
		t.Fatalf("unexpected collaboration mode normalization: %q", got)
	}
}

func TestApprovalPolicyAndAdvisoryHelpers(t *testing.T) {
	if err := validateApprovalPolicyValue(nil); err == nil {
		t.Fatal("expected nil approval policy to fail validation")
	}
	if err := validateApprovalPolicyValue("never"); err != nil {
		t.Fatalf("validateApprovalPolicyValue string: %v", err)
	}
	if err := validateApprovalPolicyValue(map[string]interface{}{"granular": map[string]interface{}{"rules": true}}); err != nil {
		t.Fatalf("validateApprovalPolicyValue map: %v", err)
	}

	if got, err := normalizeApprovalPolicyValue("on_request", true); err != nil || got != "on-request" {
		t.Fatalf("unexpected normalized approval policy: %#v err=%v", got, err)
	}
	if got, err := normalizeApprovalPolicyValue(map[interface{}]interface{}{"granular": map[interface{}]interface{}{"rules": true}}, true); err != nil {
		t.Fatalf("normalizeApprovalPolicyValue map: %v", err)
	} else if _, ok := got.(map[string]interface{}); !ok {
		t.Fatalf("expected map approval policy, got %#v", got)
	}

	cfg := DefaultConfig()
	cfg.Agent.Mode = AgentModeAppServer
	cfg.Codex.ApprovalPolicy = "never"
	cfg.Codex.InitialCollaborationMode = InitialCollaborationModePlan
	if !workflowApprovalPolicyBlocksInteractiveRecovery(cfg) {
		t.Fatal("expected approval policy blocker")
	}
	if !workflowPlanModeBlocksInteractiveRecovery(cfg) {
		t.Fatal("expected plan mode blocker")
	}
	if !workflowUsesLegacyBranchInstructions(
		"Create a dedicated issue branch before editing.",
		"Merge the issue branch into local main and push main to origin.",
	) {
		t.Fatal("expected legacy branch instruction detection")
	}
	if !rawWorkflowUsesFullAccess(map[string]interface{}{
		"codex": map[string]interface{}{
			"turn_sandbox_policy": map[string]interface{}{"type": "dangerFullAccess"},
		},
	}) {
		t.Fatal("expected raw workflow full access detection")
	}
}
