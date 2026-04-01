package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func assertGranularPolicy(t *testing.T, policy interface{}) {
	t.Helper()
	root, ok := policy.(map[string]interface{})
	if !ok {
		t.Fatalf("expected granular policy map, got %#v", policy)
	}
	granular, ok := root["granular"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected granular policy object, got %#v", policy)
	}
	if granular["sandbox_approval"] != true || granular["rules"] != true || granular["mcp_elicitations"] != true || granular["request_permissions"] != false {
		t.Fatalf("unexpected granular policy shape: %#v", policy)
	}
}

func TestDefaultConfigIncludesRuntimeCatalog(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Workspace.BranchPrefix != "maestro/" {
		t.Fatalf("expected neutral branch prefix, got %q", cfg.Workspace.BranchPrefix)
	}
	if cfg.Runtime.Default != "codex-appserver" {
		t.Fatalf("expected codex-appserver default runtime, got %q", cfg.Runtime.Default)
	}
	appServer, ok := cfg.Runtime.Entries["codex-appserver"]
	if !ok {
		t.Fatal("expected codex-appserver runtime entry")
	}
	if appServer.Provider != "codex" || appServer.Transport != AgentModeAppServer {
		t.Fatalf("unexpected codex-appserver runtime: %#v", appServer)
	}
	assertGranularPolicy(t, appServer.ApprovalPolicy)
	if cfg.Runtime.Entries["codex-stdio"].Transport != AgentModeStdio {
		t.Fatalf("unexpected codex-stdio runtime: %#v", cfg.Runtime.Entries["codex-stdio"])
	}
	if cfg.Runtime.Entries["claude"].Provider != "claude" || cfg.Runtime.Entries["claude"].Transport != AgentModeStdio {
		t.Fatalf("unexpected claude runtime: %#v", cfg.Runtime.Entries["claude"])
	}
	if cfg.Agent.Mode != AgentModeAppServer || cfg.Agent.DispatchMode != DispatchModeParallel {
		t.Fatalf("unexpected derived agent config: %#v", cfg.Agent)
	}
	if cfg.Codex.ExpectedVersion == "" || cfg.Codex.InitialCollaborationMode != InitialCollaborationModeDefault {
		t.Fatalf("unexpected derived runtime config: %#v", cfg.Codex)
	}
}

func TestApplyDefaultsPopulatesRuntimeCatalogAndValidation(t *testing.T) {
	cfg := Config{
		Tracker: TrackerConfig{Kind: TrackerKindKanban},
		Orchestrator: OrchestratorConfig{
			DispatchMode: DispatchModeParallel,
		},
		Runtime: RuntimeCatalog{
			Entries: map[string]RuntimeConfig{
				"codex-appserver": {
					Provider:                 "codex",
					Transport:                AgentModeAppServer,
					Command:                  "codex app-server",
					ExpectedVersion:          "9.9.9",
					ApprovalPolicy:           "never",
					InitialCollaborationMode: InitialCollaborationModeDefault,
					TurnTimeoutMs:            1,
					ReadTimeoutMs:            2,
					StallTimeoutMs:           3,
				},
			},
		},
		Phases: PhasesConfig{
			Review: PhasePromptConfig{Enabled: true},
			Done:   PhasePromptConfig{Enabled: true},
		},
	}

	if err := applyDefaults(&cfg); err != nil {
		t.Fatalf("applyDefaults: %v", err)
	}
	if cfg.Workspace.BranchPrefix != "maestro/" {
		t.Fatalf("expected default branch prefix, got %q", cfg.Workspace.BranchPrefix)
	}
	if cfg.Runtime.Default != "codex-appserver" {
		t.Fatalf("expected runtime.default to resolve, got %q", cfg.Runtime.Default)
	}
	if err := validateConfig(&cfg); err != nil {
		t.Fatalf("validateConfig: %v", err)
	}

	bad := cfg
	bad.Workspace.BranchPrefix = ""
	if err := validateConfig(&bad); err == nil || !strings.Contains(err.Error(), "workspace.branch_prefix") {
		t.Fatalf("expected branch prefix validation error, got %v", err)
	}

	bad = cfg
	bad.Runtime.Default = "missing-runtime"
	if err := validateConfig(&bad); err == nil || !strings.Contains(err.Error(), "runtime.default") {
		t.Fatalf("expected runtime.default validation error, got %v", err)
	}

	bad = cfg
	bad.Runtime.Entries["codex-appserver"] = RuntimeConfig{Provider: "codex", Transport: "invalid", Command: "codex"}
	if err := validateConfig(&bad); err == nil || !strings.Contains(err.Error(), "unsupported runtime.default.transport") {
		t.Fatalf("expected runtime transport validation error, got %v", err)
	}
}

func TestApprovalPolicyHelpers(t *testing.T) {
	if err := validateApprovalPolicyValue(nil); err == nil {
		t.Fatal("expected nil approval policy to fail")
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
		t.Fatalf("expected normalized approval policy map, got %#v", got)
	}
	if policy, ok := canonicalApprovalPolicyString("ON_REQUEST"); !ok || policy != "on-request" {
		t.Fatalf("unexpected canonical policy: %q %v", policy, ok)
	}
}

func TestPathHelpers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("MAESTRO_PATH", "/tmp/maestro")

	if got := expandPathValue("$MAESTRO_PATH/work"); got != "/tmp/maestro/work" {
		t.Fatalf("unexpected expanded env path: %q", got)
	}
	if got := expandPathValue("~"); got != home {
		t.Fatalf("unexpected home expansion: %q", got)
	}
	if got := resolvePathValue("/repo", "./work", "fallback"); got != filepath.Clean("/repo/work") {
		t.Fatalf("unexpected relative path resolution: %q", got)
	}
	if got := resolvePathValue("/repo", "", "$MAESTRO_PATH/db"); got != filepath.Clean("/tmp/maestro/db") {
		t.Fatalf("unexpected env path resolution: %q", got)
	}
	if got := normalizeInitialCollaborationMode(" PLAN "); got != "plan" {
		t.Fatalf("unexpected collaboration mode normalization: %q", got)
	}
}
