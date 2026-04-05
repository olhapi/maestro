package codexschema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestVendoredSchemasExistForConsumedSubset(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	schemaDir := SchemaDir(repoRoot)
	for _, rel := range ConsumedSchemaFiles {
		path := filepath.Join(schemaDir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected vendored schema %s: %v", path, err)
		}
		var parsed any
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("expected vendored schema %s to be valid JSON: %v", path, err)
		}
	}
	models := filepath.Join(repoRoot, "internal", "appserver", "protocol", "gen", "models.go")
	if _, err := os.Stat(models); err != nil {
		t.Fatalf("expected generated models: %v", err)
	}
}

func TestUpdateScriptUsesSharedSupportedVersion(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	scriptPath := filepath.Join(repoRoot, "scripts", "update_codex_schemas.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read update script: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "internal/codexschema/metadata.go") {
		t.Fatalf("expected %s to reference the shared metadata file", scriptPath)
	}
	if !strings.Contains(text, `@openai/codex@$VERSION`) {
		t.Fatalf("expected %s to generate schemas through a pinned npx package", scriptPath)
	}
	if strings.Contains(text, "command -v codex") {
		t.Fatalf("expected %s to avoid requiring a preinstalled codex binary", scriptPath)
	}
	if regexp.MustCompile(`VERSION="\$\{CODEX_SCHEMA_VERSION:-0\.[0-9]+\.[0-9]+\}"`).MatchString(text) {
		t.Fatalf("expected %s to avoid hardcoding a fallback schema version", scriptPath)
	}
}
