package extensions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
