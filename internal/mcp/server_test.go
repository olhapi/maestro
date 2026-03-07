package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/olhapi/symphony-go/internal/kanban"
)

func testStore(t *testing.T) *kanban.Store {
	t.Helper()
	db := filepath.Join(t.TempDir(), "test.db")
	s, err := kanban.NewStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestLoadExtensionsAndExecute(t *testing.T) {
	store := testStore(t)
	extPath := filepath.Join(t.TempDir(), "ext.json")
	json := `[
  {"name":"ext_echo","description":"echo args","command":"echo $SYMPHONY_TOOL_NAME:$SYMPHONY_ARGS_JSON"}
]`
	if err := os.WriteFile(extPath, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewServerWithExtensions(store, extPath)
	if _, ok := s.extensionTools["ext_echo"]; !ok {
		t.Fatalf("extension not loaded")
	}

	res, err := s.handleCallTool(context.Background(), "ext_echo", map[string]interface{}{"args": map[string]interface{}{"x": 1}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError || len(res.Content) == 0 {
		t.Fatalf("unexpected error result: %#v", res)
	}
	if len(res.Content) == 0 {
		t.Fatalf("expected extension content")
	}
}
