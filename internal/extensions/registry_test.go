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
