package extensions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
      },
      "required":["path"],
      "additionalProperties":false,
      "$defs":{
        "mode_config":{
          "type":"object",
          "properties":{
            "enabled":{"type":"boolean"}
          },
          "required":["enabled"],
          "additionalProperties":false
        }
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
	if got := inputSchema["type"]; got != "object" {
		t.Fatalf("unexpected schema type: %#v", got)
	}
	if got := schemaStringSlice(t, inputSchema["required"]); !reflect.DeepEqual(got, []string{"path"}) {
		t.Fatalf("unexpected required list: %#v", got)
	}
	if got := inputSchema["additionalProperties"]; got != false {
		t.Fatalf("unexpected additionalProperties: %#v", got)
	}
	defs := inputSchema["$defs"].(map[string]interface{})
	if _, ok := defs["mode_config"]; !ok {
		t.Fatalf("expected $defs to be preserved, got %#v", defs)
	}
	properties := inputSchema["properties"].(map[string]interface{})
	if _, ok := properties["path"]; !ok {
		t.Fatalf("expected explicit schema properties, got %#v", properties)
	}
	mode := properties["mode"].(map[string]interface{})
	if got := mode["description"]; got != "Mode" {
		t.Fatalf("unexpected explicit schema description: %#v", got)
	}
	examples := mode["examples"].([]interface{})
	if len(examples) != 1 || examples[0] != "dry-run" {
		t.Fatalf("unexpected examples: %#v", examples)
	}

	mode["description"] = "mutated"
	examples[0] = "mutated"
	specsAgain := reg.Specs()
	modeAgain := specsAgain[0]["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})["mode"].(map[string]interface{})
	if got := modeAgain["description"]; got != "Mode" {
		t.Fatalf("expected deep clone for description, got %#v", got)
	}
	examplesAgain := modeAgain["examples"].([]interface{})
	if len(examplesAgain) != 1 || examplesAgain[0] != "dry-run" {
		t.Fatalf("expected deep clone for examples, got %#v", examplesAgain)
	}
}

func TestLoadFileRejectsInvalidInputSchema(t *testing.T) {
	for _, tc := range []string{
		`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"properties":{}}}]`,
		`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"type":"string"}}]`,
		`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"type":"object","properties":[]}}]`,
		`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"type":"object","required":"path"}}]`,
		`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"type":"object","required":[1]}}]`,
		`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"type":"object","additionalProperties":"nope"}}]`,
		`[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"type":"object","$defs":{"thing":"nope"}}}]`,
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

func TestExecuteUsesWorkingDirExactArgsAndEnvGating(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "manifest")
	workspaceDir := filepath.Join(manifestDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(manifestDir, "ext.json")
	data := `[
  {
    "name":"echo_tool",
    "description":"echo",
    "command":"printf '%s|%s|%s' \"$PWD\" \"${SENTINEL:-missing}\" \"$MAESTRO_ARGS_JSON\"",
    "working_dir":"workspace",
    "deny_env_passthrough":true,
    "input_schema":{
      "type":"object",
      "properties":{
        "alpha":{"type":"number"},
        "nested":{
          "type":"object",
          "properties":{
            "x":{"type":"string"}
          },
          "required":["x"],
          "additionalProperties":false
        }
      },
      "required":["alpha","nested"],
      "additionalProperties":false,
      "$defs":{
        "nested":{
          "type":"object",
          "properties":{
            "x":{"type":"string"}
          },
          "required":["x"],
          "additionalProperties":false
        }
      }
    }
  }
]`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENTINEL", "visible")

	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	args := map[string]interface{}{
		"alpha": 1,
		"nested": map[string]interface{}{
			"x": "y",
		},
	}
	out, err := reg.Execute(context.Background(), "echo_tool", args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	expectedWD, err := filepath.EvalSymlinks(workspaceDir)
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks: %v", err)
	}
	expected := expectedWD + "|missing|{\"alpha\":1,\"nested\":{\"x\":\"y\"}}"
	if out != expected {
		t.Fatalf("unexpected output:\n got %q\nwant %q", out, expected)
	}
}

func TestExecuteRejectsInvalidArgsBeforeLaunchingCommand(t *testing.T) {
	sideEffect := filepath.Join(t.TempDir(), "ran")
	reg := NewRegistry([]Tool{{
		Name:   "schema_tool",
		Command: "touch " + fmt.Sprintf("%q", sideEffect),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string"},
			},
			"required":             []interface{}{"path"},
			"additionalProperties": false,
		},
	}})

	t.Run("missing required field", func(t *testing.T) {
		if _, err := reg.Execute(context.Background(), "schema_tool", map[string]interface{}{}); err == nil {
			t.Fatal("expected validation error")
		}
		if _, err := os.Stat(sideEffect); !os.IsNotExist(err) {
			t.Fatalf("expected command to be skipped, stat err=%v", err)
		}
	})

	t.Run("rejects extra fields", func(t *testing.T) {
		if _, err := reg.Execute(context.Background(), "schema_tool", map[string]interface{}{
			"path":  "ok",
			"extra": true,
		}); err == nil {
			t.Fatal("expected additionalProperties validation error")
		}
		if _, err := os.Stat(sideEffect); !os.IsNotExist(err) {
			t.Fatalf("expected command to be skipped, stat err=%v", err)
		}
	})
}

func TestExecuteRequiresArgs(t *testing.T) {
	sideEffect := filepath.Join(t.TempDir(), "disabled")
	reg := NewRegistry([]Tool{{Name: "args_tool", Command: "touch " + fmt.Sprintf("%q", sideEffect), RequireArgs: true}})
	if _, err := reg.Execute(context.Background(), "args_tool", map[string]interface{}{}); err == nil {
		t.Fatal("expected args validation failure")
	}
	if _, err := os.Stat(sideEffect); !os.IsNotExist(err) {
		t.Fatalf("expected command to be skipped, stat err=%v", err)
	}
}

func TestExecuteRejectedByPolicyBeforeLaunch(t *testing.T) {
	sideEffect := filepath.Join(t.TempDir(), "policy")
	reg := NewRegistry([]Tool{{Name: "blocked_tool", Command: "touch " + fmt.Sprintf("%q", sideEffect), Allowed: boolPtr(false)}})
	if _, err := reg.Execute(context.Background(), "blocked_tool", map[string]interface{}{}); err == nil {
		t.Fatal("expected policy validation failure")
	}
	if _, err := os.Stat(sideEffect); !os.IsNotExist(err) {
		t.Fatalf("expected command to be skipped, stat err=%v", err)
	}
}

func TestExecuteFailure(t *testing.T) {
	reg := NewRegistry([]Tool{{Name: "bad_tool", Command: "echo nope && exit 1"}})
	if _, err := reg.Execute(context.Background(), "bad_tool", map[string]interface{}{}); err == nil {
		t.Fatal("expected command failure")
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func schemaStringSlice(t *testing.T, value interface{}) []string {
	t.Helper()
	switch typed := value.(type) {
	case []string:
		return typed
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				t.Fatalf("expected string in slice, got %T", item)
			}
			out = append(out, text)
		}
		return out
	default:
		t.Fatalf("expected string slice, got %T", value)
		return nil
	}
}
