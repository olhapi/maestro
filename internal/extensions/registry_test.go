package extensions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadFileAndSpecs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ext.json")
	data := `[
  {"name":"echo_tool","description":"echo","command":"echo ok"}
]`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !reg.HasTools() {
		t.Fatal("expected tools to be loaded")
	}
	specs := reg.Specs()
	if len(specs) != 1 || specs[0]["name"] != "echo_tool" {
		t.Fatalf("unexpected specs: %#v", specs)
	}
	inputSchema := specs[0]["inputSchema"].(map[string]interface{})
	properties := inputSchema["properties"].(map[string]interface{})
	if _, ok := properties["args"]; !ok {
		t.Fatalf("expected fallback args schema, got %#v", properties)
	}
	annotations := specs[0]["annotations"].(map[string]interface{})
	if annotations["readOnlyHint"] != false || annotations["destructiveHint"] != true || annotations["idempotentHint"] != false || annotations["openWorldHint"] != true {
		t.Fatalf("expected conservative default annotations, got %#v", annotations)
	}
}

func TestLoadFilePreservesExplicitInputSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ext.json")
	data := `[
  {
    "name":"schema_tool",
    "description":"schema",
    "command":"echo ok",
    "input_schema":{
      "type":"object",
      "properties":{
        "path":{"type":"string","description":"Absolute path"},
        "mode":{"type":"string","description":"Mode","examples":["dry-run"]}
      }
    }
  }
]`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	specs := reg.Specs()
	inputSchema := specs[0]["inputSchema"].(map[string]interface{})
	properties := inputSchema["properties"].(map[string]interface{})
	if _, ok := properties["path"]; !ok {
		t.Fatalf("expected explicit schema properties, got %#v", properties)
	}
	mode := properties["mode"].(map[string]interface{})
	if got := mode["description"]; got != "Mode" {
		t.Fatalf("unexpected explicit schema description: %#v", got)
	}
}

func TestLoadFilePreservesExplicitAnnotations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ext.json")
	data := `[
  {
    "name":"annotated_tool",
    "description":"annotated",
    "command":"echo ok",
    "annotations":{
      "title":"Annotated Tool",
      "read_only_hint":true,
      "destructive_hint":false,
      "idempotent_hint":true,
      "open_world_hint":false
    }
  }
]`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	specs := reg.Specs()
	annotations := specs[0]["annotations"].(map[string]interface{})
	if annotations["title"] != "Annotated Tool" || annotations["readOnlyHint"] != true || annotations["destructiveHint"] != false || annotations["idempotentHint"] != true || annotations["openWorldHint"] != false {
		t.Fatalf("unexpected explicit annotations: %#v", annotations)
	}
}

func TestLoadFileRejectsInvalidInputSchema(t *testing.T) {
	for _, tc := range []string{
		`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"properties":{}}}]`,
		`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"type":"string"}}]`,
		`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"type":"object","properties":[]}}]`,
	} {
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte(tc), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(path); err == nil {
			t.Fatalf("expected invalid input_schema to fail for %s", tc)
		}
	}
}

func TestLoadFileRejectsInvalidAnnotations(t *testing.T) {
	for _, tc := range []string{
		`[{"name":"bad","description":"bad","command":"echo ok","annotations":{"read_only_hint":"yes"}}]`,
		`[{"name":"bad","description":"bad","command":"echo ok","annotations":{"extra":true}}]`,
	} {
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte(tc), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "invalid annotations") {
			t.Fatalf("expected invalid annotations to fail for %s, got %v", tc, err)
		}
	}
}

func TestLoadFileEmptyPathReturnsEmptyRegistry(t *testing.T) {
	reg, err := LoadFile("")
	if err != nil {
		t.Fatalf("LoadFile empty path: %v", err)
	}
	if reg == nil || reg.HasTools() {
		t.Fatalf("expected empty registry, got %#v", reg)
	}
}

func TestRegistryNamesAndExecuteBranches(t *testing.T) {
	workdir := t.TempDir()
	t.Setenv("MAESTRO_EXTENSION_SECRET", "topsecret")

	reg := NewRegistry([]Tool{
		{Name: "  first  ", Command: "echo ok"},
		{Name: "disabled", Command: "echo no", Allowed: func() *bool { v := false; return &v }()},
		{Name: "args", Command: "test -n \"$MAESTRO_ARGS_JSON\" && echo args", RequireArgs: true},
		{Name: "wd", Command: "pwd", WorkingDir: workdir},
		{Name: "noenv", Command: "test -z \"$MAESTRO_EXTENSION_SECRET\" && echo ok", DenyEnvPassthrough: true},
		{Name: "slow", Command: "sleep 2", TimeoutSec: 1},
	})

	if got := reg.Names(); len(got) != 6 || got[0] != "first" || got[1] != "disabled" {
		t.Fatalf("unexpected registry names: %#v", got)
	}
	if reg.tools["first"].TimeoutSec != 15 {
		t.Fatalf("expected default timeout, got %d", reg.tools["first"].TimeoutSec)
	}

	out, err := reg.Execute(context.Background(), "first", map[string]interface{}{})
	if err != nil {
		t.Fatalf("Execute first: %v", err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Fatalf("unexpected output from first tool: %q", out)
	}

	if _, err := reg.Execute(context.Background(), "disabled", map[string]interface{}{}); err == nil || !strings.Contains(err.Error(), "disabled by policy") {
		t.Fatalf("expected disabled tool error, got %v", err)
	}
	if _, err := reg.Execute(context.Background(), "args", map[string]interface{}{}); err == nil || !strings.Contains(err.Error(), "requires args object") {
		t.Fatalf("expected args validation error, got %v", err)
	}

	out, err = reg.Execute(context.Background(), "wd", map[string]interface{}{"args": map[string]interface{}{}})
	if err != nil {
		t.Fatalf("Execute wd: %v", err)
	}
	expectedWorkdir := workdir
	if resolved, err := filepath.EvalSymlinks(workdir); err == nil {
		expectedWorkdir = resolved
	}
	if strings.TrimSpace(out) != expectedWorkdir {
		t.Fatalf("expected working directory to be applied, got %q want %q", out, expectedWorkdir)
	}

	out, err = reg.Execute(context.Background(), "noenv", map[string]interface{}{"args": map[string]interface{}{}})
	if err != nil {
		t.Fatalf("Execute noenv: %v", err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Fatalf("expected env passthrough to be disabled, got %q", out)
	}

	if _, err := reg.Execute(context.Background(), "slow", map[string]interface{}{}); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if _, err := (*Registry)(nil).Execute(context.Background(), "missing", nil); err == nil {
		t.Fatal("expected nil registry to reject execution")
	}
}

func TestEmptyArgsHelpers(t *testing.T) {
	if !isEmptyArgs(nil) {
		t.Fatal("expected nil args to be empty")
	}
	if !isEmptyArgs(map[string]interface{}{}) {
		t.Fatal("expected empty map to be empty")
	}
	if !isEmptyArgs(map[string]interface{}{"args": map[string]interface{}{}}) {
		t.Fatal("expected empty nested args to be empty")
	}
	if isEmptyArgs(map[string]interface{}{"args": map[string]interface{}{"x": 1}}) {
		t.Fatal("expected populated nested args to be non-empty")
	}
}

func TestCloneMapAndValidationErrors(t *testing.T) {
	src := map[string]interface{}{
		"nested": map[string]interface{}{
			"key": "value",
		},
	}
	cloned := cloneMap(src)
	cloned["nested"].(map[string]interface{})["key"] = "changed"
	if src["nested"].(map[string]interface{})["key"] != "value" {
		t.Fatal("expected cloneMap to deep clone nested maps")
	}

	if err := validateInputSchema(Tool{Name: "bad", InputSchema: map[string]interface{}{"type": "string"}}); err == nil {
		t.Fatal("expected invalid schema type to fail")
	}
	if err := validateInputSchema(Tool{Name: "bad", InputSchema: map[string]interface{}{"type": "object", "properties": []string{"nope"}}}); err == nil {
		t.Fatal("expected invalid schema properties to fail")
	}

	if err := validateInputSchema(Tool{Name: "ok", InputSchema: map[string]interface{}{"type": "object"}}); err != nil {
		t.Fatalf("expected valid object schema, got %v", err)
	}
	if got := inputSchemaForTool(Tool{Name: "explicit", InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"a": map[string]interface{}{"type": "string"}}}}); got["type"] != "object" {
		t.Fatalf("unexpected cloned input schema: %#v", got)
	}

	if _, err := LoadFile(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected missing registry file to fail")
	}
	_ = errors.New
	_ = time.Second
}

func TestExecuteSuccess(t *testing.T) {
	reg := NewRegistry([]Tool{{Name: "echo_tool", Command: "echo $MAESTRO_TOOL_NAME:$MAESTRO_ARGS_JSON"}})
	out, err := reg.Execute(context.Background(), "echo_tool", map[string]interface{}{"args": map[string]interface{}{"x": 1}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "echo_tool") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestExecuteFailure(t *testing.T) {
	reg := NewRegistry([]Tool{{Name: "bad_tool", Command: "echo nope && exit 1"}})
	if _, err := reg.Execute(context.Background(), "bad_tool", map[string]interface{}{}); err == nil {
		t.Fatal("expected command failure")
	}
}

func TestExecuteRequiresArgs(t *testing.T) {
	reg := NewRegistry([]Tool{{Name: "args_tool", Command: "echo ok", RequireArgs: true}})
	if _, err := reg.Execute(context.Background(), "args_tool", map[string]interface{}{}); err == nil {
		t.Fatal("expected args validation failure")
	}
}
