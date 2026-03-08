package main

import (
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
