package config

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkflowPathResolveLoadAndManager(t *testing.T) {
	if base := filepath.Base(WorkflowPath("")); base != "WORKFLOW.md" {
		t.Fatalf("expected workflow filename, got %q", base)
	}
	if got := WorkflowPath("/repo"); got != filepath.Join("/repo", "WORKFLOW.md") {
		t.Fatalf("unexpected workflow path: %q", got)
	}

	envRoot := filepath.Join(t.TempDir(), "workflow-root")
	t.Setenv("MAESTRO_WORKFLOW_ROOT", envRoot)
	if got := ResolveWorkflowPath("/repo", "$MAESTRO_WORKFLOW_ROOT/workflow.md"); got != filepath.Clean(filepath.Join(envRoot, "workflow.md")) {
		t.Fatalf("unexpected resolved env path: %q", got)
	}
	if got := ResolveWorkflowPath("/repo", "nested/WORKFLOW.md"); got != filepath.Clean(filepath.Join("/repo", "nested", "WORKFLOW.md")) {
		t.Fatalf("unexpected relative resolved path: %q", got)
	}

	missing := filepath.Join(t.TempDir(), "missing.md")
	if _, err := LoadWorkflow(missing); !errors.Is(err, ErrMissingWorkflowFile) {
		t.Fatalf("expected missing workflow file error, got %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	content := strings.TrimSpace(`
---
tracker:
  kind: kanban
workspace:
  root: ./workspaces
codex:
  command: codex
  approval_policy: never
  initial_collaboration_mode: default
---
Hello {{ issue.identifier }}
`)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	workflow, err := LoadWorkflow(path)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if workflow.Path != path {
		t.Fatalf("unexpected workflow path: %q", workflow.Path)
	}
	if workflow.PromptTemplate != "Hello {{ issue.identifier }}" {
		t.Fatalf("unexpected workflow prompt: %q", workflow.PromptTemplate)
	}
	if got := workflow.Config.Workspace.Root; got != filepath.Clean(filepath.Join(dir, "workspaces")) {
		t.Fatalf("unexpected resolved workspace root: %q", got)
	}
	if len(workflow.Advisories) != 0 {
		t.Fatalf("expected no advisories, got %#v", workflow.Advisories)
	}

	payload, err := parseWorkflowPayload(path, content)
	if err != nil {
		t.Fatalf("parseWorkflowPayload: %v", err)
	}
	if payload.Prompt != "Hello {{ issue.identifier }}" {
		t.Fatalf("unexpected prompt payload: %q", payload.Prompt)
	}
	if got := payload.Config.Workspace.Root; got != filepath.Clean(filepath.Join(dir, "workspaces")) {
		t.Fatalf("unexpected payload workspace root: %q", got)
	}

	if got, err := normalizeWorkflowFrontMatter(nil, ""); err != nil || len(got) != 0 {
		t.Fatalf("expected nil front matter to normalize to empty map, got %#v err=%v", got, err)
	}
	if _, err := normalizeWorkflowFrontMatter(123, ""); err == nil || !errors.Is(err, ErrWorkflowFrontMatter) {
		t.Fatalf("expected front matter type error, got %v", err)
	}
	if got := extractMap(map[interface{}]interface{}{"kind": "kanban"}); got["kind"] != "kanban" {
		t.Fatalf("unexpected extracted map: %#v", got)
	}
	if extractMap("not-a-map") != nil {
		t.Fatal("expected non-map to return nil")
	}
}

func TestConfigHelperBranches(t *testing.T) {
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
	})

	missing := &Manager{path: filepath.Join(t.TempDir(), "missing.md")}
	if current, err := missing.Current(); err == nil || current != nil {
		t.Fatalf("expected missing manager to fail without cached workflow, got current=%#v err=%v", current, err)
	}
	if missing.LastError() == nil {
		t.Fatal("expected missing manager to record last error")
	}

	logWorkflowAdvisories("WORKFLOW.md", []WorkflowAdvisory{{Code: "legacy", Message: "legacy sandbox settings"}})

	root := map[string]interface{}{"legacy": "one, two"}
	dest := map[string]interface{}{}
	moveStringSlice(root, dest, "legacy", "modern")
	if _, ok := root["legacy"]; ok {
		t.Fatal("expected moveStringSlice to remove the source key")
	}
	if got, ok := dest["modern"].([]string); !ok || len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("unexpected moved string slice: %#v", dest["modern"])
	}

	root = map[string]interface{}{"legacy": []interface{}{"a", "b"}}
	dest = map[string]interface{}{"modern": "keep"}
	moveStringSlice(root, dest, "legacy", "modern")
	if _, ok := root["legacy"]; ok {
		t.Fatal("expected moveStringSlice to remove the source slice")
	}
	if got := dest["modern"]; got != "keep" {
		t.Fatalf("expected destination to remain unchanged, got %#v", got)
	}

	if policy, ok := canonicalApprovalPolicyString("ON_REQUEST"); !ok || policy != "on-request" {
		t.Fatalf("unexpected approval policy normalization: %q %v", policy, ok)
	}
	if _, ok := canonicalApprovalPolicyString("invalid"); ok {
		t.Fatal("expected invalid approval policy to fail")
	}

	if !rawWorkflowUsesFullAccess(map[string]interface{}{
		"codex": map[string]interface{}{
			"thread_sandbox": "danger-full-access",
		},
	}) {
		t.Fatal("expected thread sandbox danger-full-access to be detected")
	}
	if !rawWorkflowUsesFullAccess(map[string]interface{}{
		"codex_turn_sandbox_policy": map[string]interface{}{
			"type": "dangerFullAccess",
		},
	}) {
		t.Fatal("expected turn sandbox dangerFullAccess to be detected")
	}
	if !rawWorkflowHasLegacySandboxKeys(map[string]interface{}{
		"codex": map[string]interface{}{
			"turn_sandbox_policy": map[string]interface{}{"type": "workspaceWrite"},
		},
	}) {
		t.Fatal("expected legacy sandbox keys to be detected")
	}

	if !workflowPlanModeBlocksInteractiveRecovery(Config{
		Agent: AgentConfig{Mode: AgentModeAppServer},
		Codex: CodexConfig{ApprovalPolicy: "never", InitialCollaborationMode: InitialCollaborationModePlan},
	}) {
		t.Fatal("expected plan mode recovery block to be detected")
	}
	if workflowPlanModeBlocksInteractiveRecovery(Config{
		Agent: AgentConfig{Mode: AgentModeStdio},
		Codex: CodexConfig{ApprovalPolicy: "never", InitialCollaborationMode: InitialCollaborationModePlan},
	}) {
		t.Fatal("expected stdio mode to skip plan recovery advisory")
	}
	if !workflowUsesLegacyBranchInstructions("Create a dedicated issue branch before editing.\nUse maestro/{{ issue.identifier }}", "") {
		t.Fatal("expected legacy branch instructions to be detected")
	}
	if workflowUsesLegacyBranchInstructions("Use Maestro's prepared issue branch.", "") {
		t.Fatal("expected modern branch instructions to be ignored")
	}

	if err := validateApprovalPolicyValue(nil); err == nil {
		t.Fatal("expected nil approval policy to fail")
	}
	if err := validateApprovalPolicyValue(" "); err == nil {
		t.Fatal("expected blank approval policy to fail")
	}
	if err := validateApprovalPolicyValue(123); err == nil {
		t.Fatal("expected unsupported approval policy type to fail")
	}
	if err := validateApprovalPolicyValue(map[string]interface{}{}); err != nil {
		t.Fatalf("expected approval policy object to pass, got %v", err)
	}

	if err := validateConfig(&Config{Tracker: TrackerConfig{Kind: "invalid"}}); err == nil {
		t.Fatal("expected invalid tracker kind to fail")
	}
	if err := validateConfig(&Config{
		Tracker: TrackerConfig{Kind: TrackerKindKanban},
		Agent:   AgentConfig{Mode: AgentModeAppServer, DispatchMode: DispatchModeParallel},
		Codex:   CodexConfig{ApprovalPolicy: "never", InitialCollaborationMode: InitialCollaborationModeDefault},
	}); err == nil {
		t.Fatal("expected missing codex command to fail")
	}
	if err := validateConfig(&Config{
		Tracker: TrackerConfig{Kind: TrackerKindKanban},
		Agent:   AgentConfig{Mode: AgentModeAppServer, DispatchMode: DispatchModeParallel},
		Codex: CodexConfig{
			Command:                  "codex",
			ApprovalPolicy:           123,
			InitialCollaborationMode: InitialCollaborationModeDefault,
		},
	}); err == nil {
		t.Fatal("expected unsupported approval policy type to fail validation")
	}
	if err := validateConfig(&Config{
		Tracker: TrackerConfig{Kind: TrackerKindKanban},
		Agent:   AgentConfig{Mode: AgentModeAppServer, DispatchMode: DispatchModeParallel},
		Codex: CodexConfig{
			Command:                  "codex",
			ApprovalPolicy:           "never",
			InitialCollaborationMode: "unknown",
		},
	}); err == nil {
		t.Fatal("expected invalid collaboration mode to fail")
	}
	if err := validateConfig(&Config{
		Tracker: TrackerConfig{Kind: TrackerKindKanban},
		Agent:   AgentConfig{Mode: AgentModeAppServer, DispatchMode: DispatchModeParallel},
		Codex: CodexConfig{
			Command:                  "codex",
			ApprovalPolicy:           "never",
			InitialCollaborationMode: InitialCollaborationModeDefault,
		},
		Phases: PhasesConfig{
			Review: PhasePromptConfig{Enabled: true, Prompt: "{{"},
		},
	}); err == nil {
		t.Fatal("expected invalid phase prompt to fail")
	}

	t.Setenv("MAESTRO_WORKFLOW_ROOT", filepath.Join(t.TempDir(), "workflow-root"))
	if got := resolvePathValue("/repo", "$MAESTRO_WORKFLOW_ROOT/workflow.md", "fallback"); got != filepath.Clean(filepath.Join(os.Getenv("MAESTRO_WORKFLOW_ROOT"), "workflow.md")) {
		t.Fatalf("unexpected resolved env path: %q", got)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := expandPathValue("~/workflow.md"); got != filepath.Join(home, "workflow.md") {
		t.Fatalf("unexpected expanded home path: %q", got)
	}
	if got := resolvePathValue("/repo", "", "nested/workflow.md"); got != filepath.Clean(filepath.Join("/repo", "nested", "workflow.md")) {
		t.Fatalf("unexpected fallback path: %q", got)
	}
}

func TestManagerRefreshAndCurrentFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflow := func(prompt string) {
		t.Helper()
		content := strings.TrimSpace(`
---
tracker:
  kind: kanban
codex:
  command: codex
  approval_policy: never
  initial_collaboration_mode: default
---
`) + "\n" + prompt
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	writeWorkflow("Initial {{ issue.identifier }}")
	manager, err := NewManagerForPath(path)
	if err != nil {
		t.Fatalf("NewManagerForPath: %v", err)
	}

	current, err := manager.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.Path != path || current.PromptTemplate != "Initial {{ issue.identifier }}" {
		t.Fatalf("unexpected current workflow: %+v", current)
	}
	if manager.LastError() != nil {
		t.Fatalf("expected no last error, got %v", manager.LastError())
	}

	time.Sleep(10 * time.Millisecond)
	writeWorkflow("Updated {{ issue.identifier }}")
	refreshed, err := manager.Refresh()
	if err != nil {
		t.Fatalf("Refresh after update: %v", err)
	}
	if refreshed.PromptTemplate != "Updated {{ issue.identifier }}" {
		t.Fatalf("unexpected refreshed workflow: %+v", refreshed)
	}
	if manager.LastError() != nil {
		t.Fatalf("expected refresh success to clear last error, got %v", manager.LastError())
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove workflow: %v", err)
	}
	cached, err := manager.Current()
	if err != nil {
		t.Fatalf("Current after removal should keep cached workflow: %v", err)
	}
	if cached.PromptTemplate != "Updated {{ issue.identifier }}" {
		t.Fatalf("expected cached workflow to be preserved, got %+v", cached)
	}
	if manager.LastError() == nil {
		t.Fatal("expected missing workflow to record last error")
	}
}

func TestTemplateParsingAndTruthinessHelpers(t *testing.T) {
	rendered, err := RenderLiquidTemplate("Hello {{ issue.identifier }}{% if plan_mode %} plan{% else %} impl{% endif %}", map[string]interface{}{
		"issue": map[string]interface{}{
			"identifier": "ISS-1",
		},
		"plan_mode": false,
	})
	if err != nil {
		t.Fatalf("RenderLiquidTemplate: %v", err)
	}
	if rendered != "Hello ISS-1 impl" {
		t.Fatalf("unexpected rendered template: %q", rendered)
	}
	if _, err := ParseLiquidTemplate("{% endif %}"); err == nil {
		t.Fatal("expected dangling endif to fail")
	}
	if _, err := ParseLiquidTemplate("{{ }}"); err == nil {
		t.Fatal("expected empty variable expression to fail")
	}
	if _, err := RenderLiquidTemplate("{{ missing.value }}", map[string]interface{}{}); err == nil {
		t.Fatal("expected unknown variable to fail")
	}

	ptr := true
	cases := []struct {
		name string
		value interface{}
		want bool
	}{
		{name: "nil", value: nil, want: false},
		{name: "bool false", value: false, want: false},
		{name: "bool true", value: true, want: true},
		{name: "string empty", value: "  ", want: false},
		{name: "string set", value: "x", want: true},
		{name: "zero int", value: 0, want: false},
		{name: "slice empty", value: []string{}, want: false},
		{name: "slice populated", value: []string{"x"}, want: true},
		{name: "map empty", value: map[string]string{}, want: false},
		{name: "pointer", value: &ptr, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truthy(tc.value); got != tc.want {
				t.Fatalf("truthy(%#v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestInitWorkflowAndPromptHelpers(t *testing.T) {
	if joinReadableList(nil) != "" {
		t.Fatal("expected empty join list")
	}
	if joinReadableList([]string{"a"}) != "a" {
		t.Fatalf("unexpected single-item list")
	}
	if joinReadableList([]string{"a", "b"}) != "a or b" {
		t.Fatalf("unexpected two-item list")
	}
	if joinReadableList([]string{"a", "b", "c"}) != "a, b, or c" {
		t.Fatalf("unexpected three-item list")
	}

	set := initChoiceSet{
		Err: errors.New("invalid choice"),
		Choices: []initChoice{
			{Value: "alpha", Aliases: []string{"a"}},
			{Value: "alpine", Aliases: []string{"al"}},
		},
	}
	if got, err := set.resolve("1"); err != nil || got != "alpha" {
		t.Fatalf("resolve selection failed: got %q err=%v", got, err)
	}
	if got, err := set.resolve("a"); err != nil || got != "alpha" {
		t.Fatalf("resolve alias failed: got %q err=%v", got, err)
	}
	if _, err := set.resolve("alp"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous prefix to fail, got %v", err)
	}
	if _, err := set.resolve(""); err == nil {
		t.Fatal("expected blank selection to fail")
	}

	if _, err := resolveInitOptions("workflow.md", InitOptions{AgentMode: "bogus"}); err == nil {
		t.Fatal("expected invalid agent mode to fail")
	}
	if _, err := resolveInitOptions("workflow.md", InitOptions{DispatchMode: "bogus"}); err == nil {
		t.Fatal("expected invalid dispatch mode to fail")
	}
	if _, err := resolveInitOptions("workflow.md", InitOptions{ApprovalPolicy: "bogus"}); err == nil {
		t.Fatal("expected invalid approval policy to fail")
	}
	if _, err := resolveInitOptions("workflow.md", InitOptions{InitialCollaborationMode: "bogus"}); err == nil {
		t.Fatal("expected invalid collaboration mode to fail")
	}

	var out bytes.Buffer
	if promptConfirm(bufio.NewReader(strings.NewReader("no\n")), &out, "Overwrite?", false) {
		t.Fatal("expected promptConfirm to reject no")
	}
	if !promptConfirm(bufio.NewReader(strings.NewReader("yes\n")), &out, "Overwrite?", false) {
		t.Fatal("expected promptConfirm to accept yes")
	}
	positive := bufio.NewReader(strings.NewReader("0\n12\n"))
	if got := promptPositiveInt(positive, io.Discard, "Max turns", 3); got != 12 {
		t.Fatalf("unexpected promptPositiveInt result: %d", got)
	}
	if got := newInitReader(nil); got == nil {
		t.Fatal("expected newInitReader to create a reader")
	}
	if got := normalizeInitChoiceKey("  Foo_bar  "); got != "foo-bar" {
		t.Fatalf("unexpected normalized choice key: %q", got)
	}
}

func TestEnsureWorkflowAtPathAndInitWorkflowAtPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")

	createdPath, existed, err := EnsureWorkflowAtPath(path, InitOptions{
		WorkspaceRoot:            "./workspaces",
		CodexCommand:             "codex",
		AgentMode:                AgentModeStdio,
		DispatchMode:             DispatchModeParallel,
		MaxConcurrentAgents:      2,
		MaxTurns:                 3,
		MaxAutomaticRetries:      4,
		ApprovalPolicy:           "never",
		InitialCollaborationMode: InitialCollaborationModeDefault,
	})
	if err != nil {
		t.Fatalf("EnsureWorkflowAtPath create: %v", err)
	}
	if createdPath != path || !existed {
		t.Fatalf("expected workflow to be created, got created=%q created_flag=%v", createdPath, existed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected workflow file to exist: %v", err)
	}

	loaded, err := LoadWorkflow(path)
	if err != nil {
		t.Fatalf("LoadWorkflow after init: %v", err)
	}
	if loaded.Config.Agent.Mode != AgentModeStdio {
		t.Fatalf("unexpected init agent mode: %q", loaded.Config.Agent.Mode)
	}

	if err := InitWorkflowAtPath(path, InitOptions{}); err == nil || !errors.Is(err, ErrWorkflowExists) {
		t.Fatalf("expected overwrite protection, got %v", err)
	}

	cancelPath := filepath.Join(dir, "WORKFLOW-cancelled.md")
	if err := os.WriteFile(cancelPath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile stale workflow: %v", err)
	}
	err = InitWorkflowAtPath(cancelPath, InitOptions{
		Interactive: true,
		Stdin:       strings.NewReader("n\n"),
		Stdout:      io.Discard,
	})
	if err == nil || !errors.Is(err, ErrWorkflowInitCancelled) {
		t.Fatalf("expected interactive cancellation, got %v", err)
	}

	if err := InitWorkflowAtPath(cancelPath, InitOptions{
		Force:                    true,
		WorkspaceRoot:            "./workspace",
		CodexCommand:             "codex",
		AgentMode:                AgentModeAppServer,
		DispatchMode:             DispatchModeParallel,
		MaxConcurrentAgents:      1,
		MaxTurns:                 2,
		MaxAutomaticRetries:      3,
		ApprovalPolicy:           "never",
		InitialCollaborationMode: InitialCollaborationModeDefault,
	}); err != nil {
		t.Fatalf("InitWorkflowAtPath force overwrite: %v", err)
	}
	if _, err := os.Stat(cancelPath); err != nil {
		t.Fatalf("expected forced workflow to exist: %v", err)
	}
}
