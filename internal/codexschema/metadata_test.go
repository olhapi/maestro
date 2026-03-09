package codexschema

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVendoredSchemasExistForConsumedSubset(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	schemaDir := SchemaDir(repoRoot)
	for _, rel := range ConsumedSchemaFiles {
		path := filepath.Join(schemaDir, rel)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected vendored schema %s: %v", path, err)
		}
	}
	models := filepath.Join(repoRoot, "internal", "appserver", "protocol", "gen", "models.go")
	if _, err := os.Stat(models); err != nil {
		t.Fatalf("expected generated models: %v", err)
	}
}
