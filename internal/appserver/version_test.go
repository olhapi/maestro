package appserver

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

func TestDetectCodexVersion(t *testing.T) {
	path := writeFakeCodex(t, codexschema.SupportedVersion)
	status, err := DetectCodexVersion(path + " app-server")
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
