package appserver

import (
	"bytes"
	"fmt"
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

func writeCountingFakeCodex(t *testing.T, version string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	countFile := filepath.Join(dir, "count")
	script := fmt.Sprintf(`#!/bin/sh
count=0
if [ -f %q ]; then
  count=$(cat %q)
fi
count=$((count + 1))
printf '%%s\n' "$count" > %q
printf 'codex-cli %%s\n' %q
`, countFile, countFile, countFile, version)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write counting fake codex: %v", err)
	}
	return path, countFile
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

func TestDetectCodexVersionCachesResolvedExecutables(t *testing.T) {
	path, countFile := writeCountingFakeCodex(t, codexschema.SupportedVersion)

	first, err := DetectCodexVersion(path + " app-server")
	if err != nil {
		t.Fatalf("first DetectCodexVersion: %v", err)
	}
	second, err := DetectCodexVersion(path + " app-server")
	if err != nil {
		t.Fatalf("second DetectCodexVersion: %v", err)
	}

	if first.Actual != codexschema.SupportedVersion || second.Actual != codexschema.SupportedVersion {
		t.Fatalf("unexpected cached version results: first=%+v second=%+v", first, second)
	}

	data, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read count file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("expected cached lookup to execute the binary once, got %q", strings.TrimSpace(string(data)))
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
