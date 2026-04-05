package config

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
)

func cloneRuntimeEntries(in map[string]RuntimeConfig) map[string]RuntimeConfig {
	out := make(map[string]RuntimeConfig, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func TestRuntimeCatalogHelpers(t *testing.T) {
	var nilConfig *Config
	nilConfig.applyDerivedRuntimeFields()

	defaults := RuntimeConfig{
		Provider:                 "codex",
		Transport:                AgentModeAppServer,
		Command:                  "codex app-server",
		ExpectedVersion:          codexschema.SupportedVersion,
		ApprovalPolicy:           "never",
		InitialCollaborationMode: InitialCollaborationModeDefault,
		TurnTimeoutMs:            1,
		ReadTimeoutMs:            2,
		StallTimeoutMs:           3,
	}

	cases := []struct {
		name    string
		catalog RuntimeCatalog
		want    string
	}{
		{name: "empty", catalog: RuntimeCatalog{}, want: ""},
		{name: "explicit default", catalog: RuntimeCatalog{Default: "custom", Entries: map[string]RuntimeConfig{"custom": defaults}}, want: "custom"},
		{name: "preferred codex stdio", catalog: RuntimeCatalog{Entries: map[string]RuntimeConfig{"codex-stdio": defaults, "beta": defaults}}, want: "codex-stdio"},
		{name: "sorted fallback", catalog: RuntimeCatalog{Entries: map[string]RuntimeConfig{"beta": defaults, "alpha": defaults}}, want: "alpha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, runtime := tc.catalog.defaultRuntime()
			if name != tc.want {
				t.Fatalf("defaultRuntime name = %q, want %q", name, tc.want)
			}
			if tc.want == "" && runtime != (RuntimeConfig{}) {
				t.Fatalf("expected zero runtime, got %#v", runtime)
			}
			if tc.want != "" && runtime == (RuntimeConfig{}) {
				t.Fatal("expected non-zero runtime")
			}
		})
	}

	catalog := RuntimeCatalog{Entries: map[string]RuntimeConfig{"x": defaults}}
	if !catalog.hasEntry(" x ") {
		t.Fatal("expected hasEntry to trim whitespace")
	}
	if catalog.hasEntry("missing") {
		t.Fatal("expected missing entry to report false")
	}
	if (RuntimeCatalog{}).hasEntry("anything") {
		t.Fatal("expected nil catalog to report false")
	}

	normalized := normalizeRuntimeConfig(RuntimeConfig{}, defaults)
	if normalized.Provider != defaults.Provider || normalized.Transport != defaults.Transport || normalized.Command != defaults.Command {
		t.Fatalf("expected defaults to be applied, got %#v", normalized)
	}
	if normalized.ApprovalPolicy != defaults.ApprovalPolicy {
		t.Fatalf("expected default approval policy, got %#v", normalized.ApprovalPolicy)
	}

	normalized = normalizeRuntimeConfig(RuntimeConfig{ApprovalPolicy: map[interface{}]interface{}{"granular": map[interface{}]interface{}{"rules": true}}}, defaults)
	policy, ok := normalized.ApprovalPolicy.(map[string]interface{})
	if !ok || policy["granular"] == nil {
		t.Fatalf("expected normalized approval policy map, got %#v", normalized.ApprovalPolicy)
	}

	normalized = normalizeRuntimeConfig(RuntimeConfig{ApprovalPolicy: "ON_REQUEST"}, defaults)
	if normalized.ApprovalPolicy != "on-request" {
		t.Fatalf("expected canonical approval policy, got %#v", normalized.ApprovalPolicy)
	}

	unsupported := []string{"bad"}
	normalized = normalizeRuntimeConfig(RuntimeConfig{ApprovalPolicy: unsupported}, defaults)
	if got, ok := normalized.ApprovalPolicy.([]string); !ok || len(got) != 1 || got[0] != "bad" {
		t.Fatalf("expected unsupported policy to remain unchanged, got %#v", normalized.ApprovalPolicy)
	}

	cfg := &Config{
		Orchestrator: OrchestratorConfig{DispatchMode: DispatchModePerProjectSerial},
		Runtime: RuntimeCatalog{
			Entries: map[string]RuntimeConfig{
				"custom": defaults,
			},
		},
	}
	cfg.applyDerivedRuntimeFields()
	if cfg.Runtime.Default != "codex-appserver" {
		t.Fatalf("expected default runtime fallback, got %q", cfg.Runtime.Default)
	}
	if cfg.Agent.Mode != cfg.SelectedRuntimeConfig().Transport {
		t.Fatalf("expected derived agent mode to match codex transport, got %#v", cfg.Agent)
	}
}

func TestWorkflowPathAndFrontMatterHelpers(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	home := t.TempDir()
	envRoot := filepath.Join(t.TempDir(), "env-root")
	t.Setenv("HOME", home)
	t.Setenv("MAESTRO_RESOLVE_ROOT", envRoot)

	if got := WorkflowPath(""); got != filepath.Join(cwd, "WORKFLOW.md") {
		t.Fatalf("unexpected workflow path: %q", got)
	}
	if got := ResolveWorkflowPath("/repo", ""); got != filepath.Join("/repo", "WORKFLOW.md") {
		t.Fatalf("unexpected resolved workflow path: %q", got)
	}
	if got := ResolveWorkflowPath("/repo", "/abs/path/WORKFLOW.md"); got != filepath.Clean("/abs/path/WORKFLOW.md") {
		t.Fatalf("unexpected absolute override: %q", got)
	}
	if got := ResolveWorkflowPath("/repo", "$MAESTRO_RESOLVE_ROOT/workflow.md"); got != filepath.Clean(filepath.Join(envRoot, "workflow.md")) {
		t.Fatalf("unexpected env override: %q", got)
	}
	if got := ResolveWorkflowPath("/repo", "$MISSING/workflow.md"); got != filepath.Clean("$MISSING/workflow.md") {
		t.Fatalf("unexpected unresolved env override: %q", got)
	}

	if got := resolvePathValue("", "", "fallback"); got != filepath.Join(cwd, "fallback") {
		t.Fatalf("unexpected default path value: %q", got)
	}
	if got := resolvePathValue("/repo", "~/workflow.md", "fallback"); got != filepath.Join(home, "workflow.md") {
		t.Fatalf("unexpected home-expanded path: %q", got)
	}
	if got, err := resolveWorkspaceRootPath("/repo", "./workspaces"); err != nil || got != filepath.Clean("/repo/workspaces") {
		t.Fatalf("unexpected resolved workspace root: %q err=%v", got, err)
	}
	if _, err := resolveWorkspaceRootPath("/repo", "$MISSING/workspaces"); err == nil {
		t.Fatal("expected unresolved workspace root to fail")
	}
	if !hasUnresolvedPathSegment("$MISSING/workspaces") || hasUnresolvedPathSegment("/repo/workspaces") {
		t.Fatal("unexpected unresolved path detection result")
	}
	if got := expandPathValue("$MAESTRO_RESOLVE_ROOT/workflow.md"); got != filepath.Join(envRoot, "workflow.md") {
		t.Fatalf("unexpected expanded env path: %q", got)
	}
	if got := expandPathValue("~"); got != home {
		t.Fatalf("unexpected home path expansion: %q", got)
	}
	if got := expandPathValue("$MISSING/workflow.md"); got != "$MISSING/workflow.md" {
		t.Fatalf("unexpected unresolved env expansion: %q", got)
	}

	missingPath := filepath.Join(t.TempDir(), "missing")
	if _, err := currentStamp(missingPath); err == nil {
		t.Fatal("expected currentStamp to fail for a missing file")
	}
	stampPath := filepath.Join(t.TempDir(), "stamp.txt")
	if err := os.WriteFile(stampPath, []byte("abc"), 0o644); err != nil {
		t.Fatalf("WriteFile stamp: %v", err)
	}
	stamp, err := currentStamp(stampPath)
	if err != nil {
		t.Fatalf("currentStamp: %v", err)
	}
	if stamp.Size != 3 || stamp.Hash == 0 {
		t.Fatalf("unexpected file stamp: %#v", stamp)
	}

	noFrontMatter, promptStart, err := parseWorkflowFrontMatter("Prompt only")
	if err != nil {
		t.Fatalf("parseWorkflowFrontMatter without front matter: %v", err)
	}
	if promptStart != 0 || len(noFrontMatter) != 0 {
		t.Fatalf("unexpected empty front matter parse result: %#v start=%d", noFrontMatter, promptStart)
	}

	withFrontMatter := strings.TrimSpace(`
---
tracker:
  kind: kanban
nested:
  key: value
---
Prompt
`)
	raw, start, err := parseWorkflowFrontMatter(withFrontMatter)
	if err != nil {
		t.Fatalf("parseWorkflowFrontMatter with front matter: %v", err)
	}
	if start == 0 {
		t.Fatal("expected prompt start to advance")
	}
	tracker, ok := raw["tracker"].(map[string]interface{})
	if !ok || tracker["kind"] != "kanban" {
		t.Fatalf("unexpected parsed front matter: %#v", raw)
	}

	coerced, err := coerceWorkflowFrontMatter(map[string]interface{}{
		"nested": map[interface{}]interface{}{"key": "value"},
		"items":  []interface{}{map[interface{}]interface{}{"child": 1}},
	})
	if err != nil {
		t.Fatalf("coerceWorkflowFrontMatter map[string]interface{}: %v", err)
	}
	if nested, ok := coerced["nested"].(map[string]interface{}); !ok || nested["key"] != "value" {
		t.Fatalf("unexpected coerced nested map: %#v", coerced["nested"])
	}
	if items, ok := coerced["items"].([]interface{}); !ok || len(items) != 1 {
		t.Fatalf("unexpected coerced slice: %#v", coerced["items"])
	}

	coerced, err = coerceWorkflowFrontMatter(map[interface{}]interface{}{"foo": "bar"})
	if err != nil {
		t.Fatalf("coerceWorkflowFrontMatter map[interface{}]interface{}: %v", err)
	}
	if coerced["foo"] != "bar" {
		t.Fatalf("unexpected coerced legacy front matter: %#v", coerced)
	}
	if _, err := coerceWorkflowFrontMatter("bad"); err == nil {
		t.Fatal("expected non-map front matter to fail")
	}

	if got, err := normalizeApprovalPolicyValue(nil, false); err != nil || got != nil {
		t.Fatalf("unexpected absent approval policy normalization: %#v err=%v", got, err)
	}
	if _, err := normalizeApprovalPolicyValue(nil, true); err == nil {
		t.Fatal("expected nil approval policy to fail")
	}
	if got, err := normalizeApprovalPolicyValue("on_request", true); err != nil || got != "on-request" {
		t.Fatalf("unexpected normalized approval policy string: %#v err=%v", got, err)
	}
	if got, err := normalizeApprovalPolicyValue(map[interface{}]interface{}{"granular": map[interface{}]interface{}{"rules": true}}, true); err != nil {
		t.Fatalf("normalizeApprovalPolicyValue legacy map: %v", err)
	} else if _, ok := got.(map[string]interface{}); !ok {
		t.Fatalf("expected normalized approval policy map, got %#v", got)
	}
	if _, err := normalizeApprovalPolicyValue(123, true); err == nil {
		t.Fatal("expected unsupported approval policy type to fail")
	}

	for _, tc := range []struct {
		raw  string
		want string
		ok   bool
	}{
		{raw: "never", want: "never", ok: true},
		{raw: "ON_REQUEST", want: "on-request", ok: true},
		{raw: "on_failure", want: "on-failure", ok: true},
		{raw: "untrusted", want: "untrusted", ok: true},
		{raw: "bad", want: "", ok: false},
	} {
		got, ok := canonicalApprovalPolicyString(tc.raw)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("canonicalApprovalPolicyString(%q) = %q,%t want %q,%t", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestValidationHelpers(t *testing.T) {
	base := DefaultConfig()
	validRuntime := base.Runtime.Entries["codex-appserver"]

	if err := validateConfig(&base); err != nil {
		t.Fatalf("validateConfig(base): %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "tracker kind", mutate: func(c *Config) { c.Tracker.Kind = "other" }},
		{name: "workspace root", mutate: func(c *Config) { c.Workspace.Root = "" }},
		{name: "branch prefix", mutate: func(c *Config) { c.Workspace.BranchPrefix = "" }},
		{name: "dispatch mode", mutate: func(c *Config) { c.Orchestrator.DispatchMode = "bad" }},
		{name: "runtime missing", mutate: func(c *Config) { c.Runtime.Entries = nil }},
		{name: "runtime default blank", mutate: func(c *Config) { c.Runtime.Default = "" }},
		{name: "runtime default missing", mutate: func(c *Config) { c.Runtime.Default = "missing" }},
		{name: "runtime default provider", mutate: func(c *Config) {
			runtimeCfg := validRuntime
			runtimeCfg.Provider = ""
			c.Runtime.Entries["codex-appserver"] = runtimeCfg
		}},
		{name: "runtime default transport", mutate: func(c *Config) {
			runtimeCfg := validRuntime
			runtimeCfg.Transport = "bad"
			c.Runtime.Entries["codex-appserver"] = runtimeCfg
		}},
		{name: "runtime default command", mutate: func(c *Config) {
			runtimeCfg := validRuntime
			runtimeCfg.Command = ""
			c.Runtime.Entries["codex-appserver"] = runtimeCfg
		}},
		{name: "runtime default approval policy", mutate: func(c *Config) {
			runtimeCfg := validRuntime
			runtimeCfg.ApprovalPolicy = 123
			c.Runtime.Entries["codex-appserver"] = runtimeCfg
		}},
		{name: "runtime default collab mode", mutate: func(c *Config) {
			runtimeCfg := validRuntime
			runtimeCfg.InitialCollaborationMode = "manual"
			c.Runtime.Entries["codex-appserver"] = runtimeCfg
		}},
		{name: "review prompt parse", mutate: func(c *Config) { c.Phases.Review.Prompt = "{{" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.Runtime.Entries = cloneRuntimeEntries(base.Runtime.Entries)
			tc.mutate(&cfg)
			if err := validateConfig(&cfg); err == nil {
				t.Fatal("expected validateConfig to fail")
			}
		})
	}

	if err := validateRuntimeEntry("", validRuntime); err == nil {
		t.Fatal("expected empty runtime name to fail")
	}
	if err := validateRuntimeEntry("runtime", RuntimeConfig{}); err == nil {
		t.Fatal("expected empty runtime config to fail")
	}
	if err := validateApprovalPolicyValue([]string{}); err == nil {
		t.Fatal("expected unsupported approval policy type to fail")
	}
}

func TestInitChoiceAndPromptHelpers(t *testing.T) {
	if reader := newInitReader(nil); reader == nil {
		t.Fatal("expected newInitReader(nil) to return a reader")
	}
	if reader := newInitReader(strings.NewReader("value\n")); reader == nil {
		t.Fatal("expected newInitReader to wrap custom readers")
	}

	if got := joinReadableList(nil); got != "" {
		t.Fatalf("unexpected joinReadableList(nil): %q", got)
	}
	if got := joinReadableList([]string{"one"}); got != "one" {
		t.Fatalf("unexpected joinReadableList(1): %q", got)
	}
	if got := joinReadableList([]string{"one", "two"}); got != "one or two" {
		t.Fatalf("unexpected joinReadableList(2): %q", got)
	}
	if got := joinReadableList([]string{"one", "two", "three"}); got != "one, two, or three" {
		t.Fatalf("unexpected joinReadableList(3): %q", got)
	}
	if got := normalizeInitChoiceKey("  Foo__Bar  "); got != "foo-bar" {
		t.Fatalf("unexpected normalized choice key: %q", got)
	}

	aliasChoices := initChoiceSet{
		Err: errors.New("invalid choice"),
		Choices: []initChoice{
			{Value: AgentModeAppServer, Description: "app server", Aliases: []string{"app", "server"}},
			{Value: AgentModeStdio, Description: "stdio", Aliases: []string{"std"}},
		},
	}
	if got, err := aliasChoices.resolve("1"); err != nil || got != AgentModeAppServer {
		t.Fatalf("resolve numeric selection: got %q err=%v", got, err)
	}
	if got, err := aliasChoices.resolve("server"); err != nil || got != AgentModeAppServer {
		t.Fatalf("resolve alias selection: got %q err=%v", got, err)
	}
	if got, err := aliasChoices.resolve("st"); err != nil || got != AgentModeStdio {
		t.Fatalf("resolve prefix selection: got %q err=%v", got, err)
	}
	if _, err := aliasChoices.resolve(""); err == nil {
		t.Fatal("expected blank selection to fail")
	}

	ambiguousChoices := initChoiceSet{
		Err: errors.New("invalid choice"),
		Choices: []initChoice{
			{Value: "apple", Description: "apple"},
			{Value: "apricot", Description: "apricot"},
		},
	}
	if _, err := ambiguousChoices.resolve("a"); err == nil {
		t.Fatal("expected ambiguous selection to fail")
	}

	reader := bufio.NewReader(strings.NewReader("invalid\n2\n"))
	writer := &bytes.Buffer{}
	if got := promptChoice(reader, writer, "Agent mode", AgentModeStdio, aliasChoices); got != AgentModeStdio {
		t.Fatalf("unexpected promptChoice result: %q", got)
	}
	if !strings.Contains(writer.String(), "Invalid value") {
		t.Fatalf("expected promptChoice to report invalid values, got %q", writer.String())
	}

	reader = bufio.NewReader(strings.NewReader("0\n7\n"))
	writer.Reset()
	if got := promptPositiveInt(reader, writer, "Max turns", 3); got != 7 {
		t.Fatalf("unexpected promptPositiveInt result: %d", got)
	}
	if !strings.Contains(writer.String(), "Invalid value") {
		t.Fatalf("expected promptPositiveInt to report invalid values, got %q", writer.String())
	}

	reader = bufio.NewReader(strings.NewReader("\n"))
	writer.Reset()
	if got := promptLine(reader, writer, "Workspace root", "./workspaces"); got != "./workspaces" {
		t.Fatalf("unexpected promptLine fallback: %q", got)
	}

	reader = bufio.NewReader(strings.NewReader("yes\n"))
	writer.Reset()
	if !promptConfirm(reader, writer, "Overwrite?", false) {
		t.Fatal("expected promptConfirm to accept yes input")
	}

	reader = bufio.NewReader(strings.NewReader("\n"))
	writer.Reset()
	if promptConfirm(reader, writer, "Overwrite?", false) {
		t.Fatal("expected promptConfirm default false for blank input")
	}

	reader = bufio.NewReader(strings.NewReader("y\n"))
	writer.Reset()
	if !confirmOverwrite(InitOptions{Stdin: reader, Stdout: writer}, "/tmp/WORKFLOW.md") {
		t.Fatal("expected confirmOverwrite to accept yes input")
	}
}
