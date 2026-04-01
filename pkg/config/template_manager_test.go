package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
)

func managerWorkflowContent(prompt string) string {
	return strings.TrimSpace(`
---
tracker:
  kind: kanban
workspace:
  root: ./workspaces
  branch_prefix: maestro/
runtime:
  default: codex-appserver
  codex-appserver:
    provider: codex
    transport: app_server
    command: codex app-server
    expected_version: ` + codexschema.SupportedVersion + `
    approval_policy: never
    initial_collaboration_mode: default
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
  codex-stdio:
    provider: codex
    transport: stdio
    command: codex exec
    expected_version: ` + codexschema.SupportedVersion + `
    approval_policy: never
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
  claude:
    provider: claude
    transport: stdio
    command: claude
    approval_policy: never
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
---
` + prompt + `
`)
}

func writeManagerWorkflow(t *testing.T, path, prompt string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(managerWorkflowContent(prompt)), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}
}

func TestLiquidTemplateRenderingAndErrors(t *testing.T) {
	input := "Hello {{ issue.identifier }}{% if project.description %} from {{ project.name }}{% else %} from solo{% endif %} / {{ attempt }}"
	ctx := map[string]interface{}{
		"issue": map[string]interface{}{
			"identifier": "ISS-1",
		},
		"project": map[string]interface{}{
			"name":        "Alpha",
			"description": "review",
		},
		"attempt": 2,
	}

	rendered, err := RenderLiquidTemplate(input, ctx)
	if err != nil {
		t.Fatalf("RenderLiquidTemplate: %v", err)
	}
	if want := "Hello ISS-1 from Alpha / 2"; rendered != want {
		t.Fatalf("unexpected render output: got %q want %q", rendered, want)
	}

	tmpl, err := ParseLiquidTemplate(input)
	if err != nil {
		t.Fatalf("ParseLiquidTemplate: %v", err)
	}
	rendered, err = tmpl.Render(ctx)
	if err != nil {
		t.Fatalf("liquidTemplate.Render: %v", err)
	}
	if want := "Hello ISS-1 from Alpha / 2"; rendered != want {
		t.Fatalf("unexpected template render output: got %q want %q", rendered, want)
	}

	ctx["project"].(map[string]interface{})["description"] = ""
	rendered, err = RenderLiquidTemplate(input, ctx)
	if err != nil {
		t.Fatalf("RenderLiquidTemplate else branch: %v", err)
	}
	if want := "Hello ISS-1 from solo / 2"; rendered != want {
		t.Fatalf("unexpected else render output: got %q want %q", rendered, want)
	}

	if _, err := RenderLiquidTemplate("{{ issue.missing }}", ctx); err == nil {
		t.Fatal("expected missing variable render to fail")
	}
	if _, err := lookupTemplateValue(map[string]interface{}{"issue": "not-a-map"}, "issue.identifier"); err == nil {
		t.Fatal("expected non-map lookup to fail")
	}
	if _, err := lookupTemplateValue(map[string]interface{}{}, ""); err == nil {
		t.Fatal("expected blank lookup expression to fail")
	}

	parseErrors := []struct {
		name  string
		input string
	}{
		{name: "unterminated variable", input: "Hello {{ issue.identifier"},
		{name: "empty variable", input: "Hello {{  }}"},
		{name: "unknown filter", input: "{{ issue.identifier | upper }}"},
		{name: "unterminated tag", input: "{% if issue.identifier"},
		{name: "empty if condition", input: "{% if   %}x{% endif %}"},
		{name: "unknown tag", input: "{% for item in items %}x{% endfor %}"},
		{name: "unexpected else", input: "{% else %}"},
		{name: "unexpected endif", input: "{% endif %}"},
		{name: "missing endif", input: "{% if issue.identifier %}x"},
	}
	for _, tc := range parseErrors {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseLiquidTemplate(tc.input); err == nil {
				t.Fatalf("expected ParseLiquidTemplate to fail for %q", tc.input)
			}
		})
	}

	one := 1
	cases := []struct {
		name  string
		value interface{}
		want  bool
	}{
		{name: "nil", value: nil, want: false},
		{name: "false", value: false, want: false},
		{name: "true", value: true, want: true},
		{name: "empty string", value: "", want: false},
		{name: "blank string", value: "   ", want: false},
		{name: "int zero", value: 0, want: false},
		{name: "int one", value: 1, want: true},
		{name: "int64 zero", value: int64(0), want: false},
		{name: "float zero", value: 0.0, want: false},
		{name: "float one", value: 1.25, want: true},
		{name: "empty slice", value: []string{}, want: false},
		{name: "filled slice", value: []string{"x"}, want: true},
		{name: "empty map", value: map[string]string{}, want: false},
		{name: "filled map", value: map[string]string{"x": "y"}, want: true},
		{name: "nil pointer", value: (*int)(nil), want: false},
		{name: "non-nil pointer", value: &one, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truthy(tc.value); got != tc.want {
				t.Fatalf("truthy(%#v) = %t, want %t", tc.value, got, tc.want)
			}
		})
	}
}

func TestManagerLifecycleAndWorkflowHelpers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	initialPrompt := "Hello {{ issue.identifier }}"
	updatedPrompt := "Updated {{ issue.identifier }}"

	writeManagerWorkflow(t, path, initialPrompt)

	manager, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := manager.Path(); got != path {
		t.Fatalf("unexpected manager path: got %q want %q", got, path)
	}
	if manager.LastError() != nil {
		t.Fatalf("expected no last error for fresh manager, got %v", manager.LastError())
	}

	current, err := manager.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.PromptTemplate != initialPrompt {
		t.Fatalf("unexpected current prompt: got %q want %q", current.PromptTemplate, initialPrompt)
	}

	same, err := manager.Refresh()
	if err != nil {
		t.Fatalf("Refresh without file change: %v", err)
	}
	if same.PromptTemplate != initialPrompt {
		t.Fatalf("unexpected refresh prompt: got %q want %q", same.PromptTemplate, initialPrompt)
	}
	if manager.LastError() != nil {
		t.Fatalf("expected refresh to clear last error, got %v", manager.LastError())
	}

	writeManagerWorkflow(t, path, updatedPrompt)
	refreshed, err := manager.Refresh()
	if err != nil {
		t.Fatalf("Refresh after workflow update: %v", err)
	}
	if refreshed.PromptTemplate != updatedPrompt {
		t.Fatalf("unexpected refreshed prompt: got %q want %q", refreshed.PromptTemplate, updatedPrompt)
	}
	if manager.LastError() != nil {
		t.Fatalf("expected successful refresh to clear last error, got %v", manager.LastError())
	}
}

func TestEnsureWorkflowHelpers(t *testing.T) {
	dir := t.TempDir()

	path, created, err := EnsureWorkflow(dir, InitOptions{})
	if err != nil {
		t.Fatalf("EnsureWorkflow: %v", err)
	}
	if !created {
		t.Fatal("expected EnsureWorkflow to create a missing workflow file")
	}
	if got := filepath.Base(path); got != "WORKFLOW.md" {
		t.Fatalf("unexpected created path: %q", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected workflow file to exist: %v", err)
	}

	samePath, created, err := EnsureWorkflowAtPath(path, InitOptions{})
	if err != nil {
		t.Fatalf("EnsureWorkflowAtPath: %v", err)
	}
	if created {
		t.Fatal("expected EnsureWorkflowAtPath to reuse existing workflow file")
	}
	if samePath != path {
		t.Fatalf("unexpected workflow path: got %q want %q", samePath, path)
	}
}
