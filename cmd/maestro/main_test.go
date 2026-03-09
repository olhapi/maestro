package main

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseGlobalOptions(t *testing.T) {
	opts, remaining, err := parseGlobalOptions([]string{"run", "--log-level", "debug", "--db", "./db.sqlite"})
	if err != nil {
		t.Fatalf("parseGlobalOptions failed: %v", err)
	}
	if opts.logLevel != slog.LevelDebug || opts.logLevelName != "debug" {
		t.Fatalf("unexpected global options: %+v", opts)
	}
	if got := strings.Join(remaining, " "); got != "run --db ./db.sqlite" {
		t.Fatalf("unexpected remaining args: %q", got)
	}
}

func TestParseGlobalOptionsRejectsInvalidLevel(t *testing.T) {
	if _, _, err := parseGlobalOptions([]string{"run", "--log-level", "verbose"}); err == nil {
		t.Fatal("expected invalid log level error")
	}
}

func TestParseRunOptions(t *testing.T) {
	opts := parseRunOptions([]string{
		"--workflow", "./custom.md",
		"--extensions", "./ext.json",
		"--db", "./db.sqlite",
		"--logs-root", "./logs",
		"--port", "8787",
		"--log-max-bytes", "1234",
		"--log-max-files", "9",
		guardrailsAcknowledgementFlag,
		"/repo/path",
	})
	if opts.repoPath != "/repo/path" {
		t.Fatalf("unexpected repo path: %+v", opts)
	}
	if opts.workflowPath != "./custom.md" || opts.extensionsFile != "./ext.json" {
		t.Fatalf("unexpected workflow/extensions options: %+v", opts)
	}
	if !opts.acknowledgedUnsafe {
		t.Fatal("expected ack flag to be parsed")
	}
	if opts.logMaxBytes != 1234 || opts.logMaxFiles != 9 {
		t.Fatalf("unexpected log rotation settings: %+v", opts)
	}
}

func TestSetupLoggerWithWriterFiltersByLevelAndWritesFile(t *testing.T) {
	old := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	logDir := t.TempDir()
	var stdout bytes.Buffer
	logPath, err := setupLoggerWithWriter(&stdout, logDir, 1024, 2, slog.LevelWarn)
	if err != nil {
		t.Fatalf("setupLoggerWithWriter failed: %v", err)
	}
	slog.Info("hidden info")
	slog.Warn("visible warn", "component", "test")

	if strings.Contains(stdout.String(), "hidden info") {
		t.Fatalf("expected info log to be filtered, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "visible warn") {
		t.Fatalf("expected warn log in stdout, got %q", stdout.String())
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "hidden info") {
		t.Fatalf("expected info log to be filtered from file, got %q", text)
	}
	if !strings.Contains(text, "visible warn") {
		t.Fatalf("expected warn log in file, got %q", text)
	}
}

func TestGuardrailsAcknowledgementBannerMentionsFlag(t *testing.T) {
	banner := guardrailsAcknowledgementBanner()
	for _, want := range []string{
		"engineering preview",
		"without any guardrails",
		guardrailsAcknowledgementFlag,
	} {
		if !strings.Contains(strings.ToLower(banner), strings.ToLower(want)) {
			t.Fatalf("expected %q in banner %q", want, banner)
		}
	}
}

func TestReleaseBuildReportsInjectedVersion(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve test file path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	binPath := filepath.Join(t.TempDir(), "maestro")
	const releaseVersion = "1.2.3-test"

	buildCmd := exec.Command("go", "build", "-ldflags", "-X main.version="+releaseVersion, "-o", binPath, "./cmd/maestro")
	buildCmd.Dir = repoRoot
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}

	versionCmd := exec.Command(binPath, "version")
	if output, err := versionCmd.CombinedOutput(); err != nil {
		t.Fatalf("version command failed: %v\n%s", err, output)
	} else if got := strings.TrimSpace(string(output)); got != "maestro "+releaseVersion {
		t.Fatalf("unexpected version output: %q", got)
	}
}
