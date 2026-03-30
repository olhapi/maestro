package appserver

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
)

func writeFakeCodex(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf 'codex-cli " + version + "\\n'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return path
}

func TestDetectCodexVersionDirectBinary(t *testing.T) {
	path := writeFakeCodex(t, codexschema.SupportedVersion)
	status, err := DetectCodexVersion(path)
	if err != nil {
		t.Fatalf("DetectCodexVersion: %v", err)
	}
	if status.Actual != codexschema.SupportedVersion {
		t.Fatalf("unexpected version: %+v", status)
	}
	if status.ExecutablePath == "" {
		t.Fatalf("expected executable path: %+v", status)
	}
}

func TestDetectCodexVersionSkipsNonCodexCommand(t *testing.T) {
	status, err := DetectCodexVersion("/bin/sh -lc echo")
	if err != nil {
		t.Fatalf("DetectCodexVersion: %v", err)
	}
	if status.ExecutablePath != "" || status.Actual != "" {
		t.Fatalf("expected non-codex command to be ignored, got %+v", status)
	}
}

func TestWarnOnCodexVersionMismatch(t *testing.T) {
	path := writeFakeCodex(t, "0.222.0")
	buf := &bytes.Buffer{}
	client := &Client{
		cfg: ClientConfig{
			CodexCommand:    path + " app-server",
			ExpectedVersion: codexschema.SupportedVersion,
			Workspace:       "/tmp/work",
			Logger:          slog.New(slog.NewJSONHandler(buf, nil)),
		},
	}
	client.logger = client.newLogger()
	client.warnOnCodexVersionMismatch()
	if !strings.Contains(buf.String(), "Codex CLI version mismatch") {
		t.Fatalf("expected mismatch warning, got %s", buf.String())
	}
}

func TestWarnOnCodexVersionMismatchSkipsNonCodexCommands(t *testing.T) {
	buf := &bytes.Buffer{}
	client := &Client{
		cfg: ClientConfig{
			CodexCommand:    "/bin/sh -lc echo",
			ExpectedVersion: codexschema.SupportedVersion,
			Workspace:       "/tmp/work",
			Logger:          slog.New(slog.NewJSONHandler(buf, nil)),
		},
	}
	client.logger = client.newLogger()
	client.warnOnCodexVersionMismatch()
	if buf.Len() != 0 {
		t.Fatalf("expected no logs for non-codex command, got %s", buf.String())
	}
}

func TestDetectCodexVersionCachesAndReportsFailures(t *testing.T) {
	codexVersionCache = sync.Map{}

	missingPath := filepath.Join(t.TempDir(), "codex")
	if _, err := DetectCodexVersion(missingPath); err == nil {
		t.Fatal("expected missing codex binary to fail")
	}

	path := writeFakeCodex(t, "0.123.0")
	status, err := DetectCodexVersion(path)
	if err != nil {
		t.Fatalf("DetectCodexVersion initial: %v", err)
	}
	if status.Actual != "0.123.0" {
		t.Fatalf("unexpected initial version: %+v", status)
	}

	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'codex-cli 9.9.9\\n'\n"), 0o755); err != nil {
		t.Fatalf("overwrite fake codex: %v", err)
	}
	cached, err := DetectCodexVersion(path)
	if err != nil {
		t.Fatalf("DetectCodexVersion cached: %v", err)
	}
	if cached.Actual != "0.123.0" {
		t.Fatalf("expected cached version to remain stable, got %+v", cached)
	}

	invalid := writeFakeCodex(t, "unknown")
	if _, err := DetectCodexVersion(invalid); err == nil || !strings.Contains(err.Error(), "unable to parse codex version") {
		t.Fatalf("expected invalid version output to fail, got %v", err)
	}
}

func TestCodexExecutableFromCommandBranches(t *testing.T) {
	if got := codexExecutableFromCommand(""); got != "" {
		t.Fatalf("expected blank command to be ignored, got %q", got)
	}
	if got := codexExecutableFromCommand("  codex app-server  "); got != "codex" {
		t.Fatalf("expected codex executable to be detected, got %q", got)
	}
	if got := codexExecutableFromCommand("/bin/sh -lc echo"); got != "" {
		t.Fatalf("expected non-codex command to be ignored, got %q", got)
	}
}
