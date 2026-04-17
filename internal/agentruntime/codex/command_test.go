package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
)

func writeFakeCodexCommand(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf 'codex-cli " + version + "\\n'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return path
}

func writeFakeNpxCommand(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "npx")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" != \"-y\" ]; then\n" +
		"  echo \"unexpected npx args: $*\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"shift\n" +
		"if [ \"$1\" != \"@openai/codex@" + version + "\" ]; then\n" +
		"  echo \"unexpected package: $1\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"shift\n" +
		"if [ \"$1\" != \"--version\" ]; then\n" +
		"  echo \"unexpected version probe args: $*\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"printf 'codex-cli " + version + "\\n'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake npx: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func TestResolveCommandKeepsMatchingDirectCodexCommand(t *testing.T) {
	path := writeFakeCodexCommand(t, codexschema.SupportedVersion)
	command := path + " app-server --model gpt-5"

	resolved, err := ResolveCommand(command)
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	if resolved != command {
		t.Fatalf("expected matching command to stay unchanged, got %q", resolved)
	}
}

func TestResolveCommandPinsMissingDirectCodexCommand(t *testing.T) {
	npxPath := writeFakeNpxCommand(t, codexschema.SupportedVersion)
	t.Setenv("PATH", filepath.Dir(npxPath))
	command := "codex app-server --model gpt-5"

	resolved, err := ResolveCommand(command)
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	want := "npx -y @openai/codex@" + codexschema.SupportedVersion + " app-server --model gpt-5"
	if resolved != want {
		t.Fatalf("expected missing codex command to be pinned, got %q", resolved)
	}
}

func TestResolveCommandPinsMismatchedDirectCodexCommand(t *testing.T) {
	path := writeFakeCodexCommand(t, "0.222.0")
	command := path + " app-server --model gpt-5"

	resolved, err := ResolveCommand(command)
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	want := "npx -y @openai/codex@" + codexschema.SupportedVersion + " app-server --model gpt-5"
	if resolved != want {
		t.Fatalf("expected mismatched command to be pinned, got %q", resolved)
	}
}

func TestResolveCommandKeepsDirectCodexCommandWhenNpxMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf 'codex-cli 0.222.0\\n'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", dir)

	command := "codex app-server --model gpt-5"
	resolved, err := ResolveCommand(command)
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	if resolved != command {
		t.Fatalf("expected direct codex command to remain unchanged without npx, got %q", resolved)
	}
}

func TestResolveCommandKeepsPinnedNPXCommand(t *testing.T) {
	writeFakeNpxCommand(t, codexschema.SupportedVersion)
	command := "npx -y @openai/codex@" + codexschema.SupportedVersion + " app-server --model gpt-5"

	resolved, err := ResolveCommand(command)
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	if resolved != command {
		t.Fatalf("expected pinned npx command to stay unchanged, got %q", resolved)
	}
}

func TestResolveCommandPinsUnquotedConfigCommand(t *testing.T) {
	path := writeFakeCodexCommand(t, "0.222.0")
	command := path + " app-server -c model=gpt-5.3-codex"

	resolved, err := ResolveCommand(command)
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	want := "npx -y @openai/codex@" + codexschema.SupportedVersion + " app-server -c model=gpt-5.3-codex"
	if resolved != want {
		t.Fatalf("expected unquoted config command to be pinned, got %q", resolved)
	}
}

func TestResolveCommandLeavesQuotedCodexCommandUntouched(t *testing.T) {
	path := writeFakeCodexCommand(t, "0.222.0")
	command := path + ` app-server --model "foo bar"`

	resolved, err := ResolveCommand(command)
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	if resolved != command {
		t.Fatalf("expected quoted command to stay unchanged, got %q", resolved)
	}
	if !strings.Contains(resolved, `"foo bar"`) {
		t.Fatalf("expected quoted argument to survive, got %q", resolved)
	}
}
