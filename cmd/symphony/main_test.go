package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
	binPath := filepath.Join(t.TempDir(), "symphony")
	const releaseVersion = "1.2.3-test"

	buildCmd := exec.Command("go", "build", "-ldflags", "-X main.version="+releaseVersion, "-o", binPath, "./cmd/symphony")
	buildCmd.Dir = repoRoot
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}

	versionCmd := exec.Command(binPath, "version")
	if output, err := versionCmd.CombinedOutput(); err != nil {
		t.Fatalf("version command failed: %v\n%s", err, output)
	} else if got := strings.TrimSpace(string(output)); got != "symphony "+releaseVersion {
		t.Fatalf("unexpected version output: %q", got)
	}
}
