package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/testutil/inprocessserver"
)

func TestOpenDashboardWhenReadyLaunchesBrowser(t *testing.T) {
	server, err := inprocessserver.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	if err != nil {
		t.Fatalf("new in-process server: %v", err)
	}
	defer server.Close()

	oldLauncher := dashboardBrowserLauncher
	oldClient := dashboardHTTPClient
	oldPollInterval := dashboardOpenPollInterval
	oldTimeout := dashboardOpenTimeout
	t.Cleanup(func() {
		dashboardBrowserLauncher = oldLauncher
		dashboardHTTPClient = oldClient
		dashboardOpenPollInterval = oldPollInterval
		dashboardOpenTimeout = oldTimeout
	})

	var openedURL string
	var launcherCanceled bool
	var launcherHasDeadline bool
	dashboardBrowserLauncher = func(ctx context.Context, rawURL string) error {
		openedURL = rawURL
		_, launcherHasDeadline = ctx.Deadline()
		launcherCanceled = ctx.Err() != nil
		return nil
	}
	dashboardHTTPClient = server.Client()
	dashboardOpenPollInterval = time.Millisecond
	dashboardOpenTimeout = time.Second

	if err := openDashboardWhenReady(context.Background(), server.URL+"/"); err != nil {
		t.Fatalf("openDashboardWhenReady returned error: %v", err)
	}
	if openedURL != server.URL {
		t.Fatalf("expected browser launch %q, got %q", server.URL, openedURL)
	}
	if launcherHasDeadline {
		t.Fatal("expected browser launch context without readiness deadline")
	}
	if launcherCanceled {
		t.Fatal("expected browser launch context to remain active")
	}
}

func TestBrowserCommandForKnownPlatforms(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{goos: "darwin", want: "open"},
		{goos: "linux", want: "xdg-open"},
		{goos: "windows", want: "rundll32"},
	}

	for _, tc := range tests {
		command, args, err := browserCommandFor(tc.goos, "http://127.0.0.1:8787")
		if err != nil {
			t.Fatalf("browserCommandFor(%q) returned error: %v", tc.goos, err)
		}
		if command != tc.want {
			t.Fatalf("browserCommandFor(%q) command = %q, want %q", tc.goos, command, tc.want)
		}
		if len(args) == 0 {
			t.Fatalf("browserCommandFor(%q) returned no arguments", tc.goos)
		}
	}
}

func TestLaunchBrowserURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("browser launcher test uses a shell stub")
	}

	command, _, err := browserCommandFor(runtime.GOOS, "https://example.com/docs")
	if err != nil {
		t.Fatalf("browserCommandFor returned error: %v", err)
	}

	dir := t.TempDir()
	calledPath := filepath.Join(dir, "called-url.txt")
	launcherPath := filepath.Join(dir, command)
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$1\" > %q\n", calledPath)
	if err := os.WriteFile(launcherPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write launcher stub: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)

	if err := launchBrowserURL(context.Background(), "https://example.com/docs"); err != nil {
		t.Fatalf("launchBrowserURL returned error: %v", err)
	}

	// Give the launched shell stub enough room to start under heavier pre-push load.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(calledPath)
		if err == nil {
			if got := string(data); got != "https://example.com/docs" {
				t.Fatalf("launcher argument = %q, want %q", got, "https://example.com/docs")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected launcher stub to be invoked at %s", calledPath)
}

func TestTerminalsInteractiveUsesStdoutAndStderr(t *testing.T) {
	if isTerminal(nil) {
		t.Fatal("expected nil file to be non-terminal")
	}

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	t.Cleanup(func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
	})

	if terminalsInteractive() {
		t.Fatal("expected piped stdio to be non-interactive")
	}
}

func TestMaybeOpenDashboardAndTerminalHelpers(t *testing.T) {
	oldInteractive := dashboardInteractiveCheck
	oldLauncher := dashboardBrowserLauncher
	oldClient := dashboardHTTPClient
	oldPollInterval := dashboardOpenPollInterval
	oldTimeout := dashboardOpenTimeout
	t.Cleanup(func() {
		dashboardInteractiveCheck = oldInteractive
		dashboardBrowserLauncher = oldLauncher
		dashboardHTTPClient = oldClient
		dashboardOpenPollInterval = oldPollInterval
		dashboardOpenTimeout = oldTimeout
	})

	launched := make(chan string, 1)
	dashboardInteractiveCheck = func() bool { return true }
	dashboardBrowserLauncher = func(ctx context.Context, rawURL string) error {
		launched <- rawURL
		return nil
	}
	dashboardHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/health" {
				t.Fatalf("unexpected request path %q", req.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("{}")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
	dashboardOpenPollInterval = time.Millisecond
	dashboardOpenTimeout = time.Second

	maybeOpenDashboard(context.Background(), "http://127.0.0.1:8787/")
	select {
	case got := <-launched:
		if got != "http://127.0.0.1:8787" {
			t.Fatalf("unexpected launched url %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected dashboard launch")
	}

	dashboardInteractiveCheck = func() bool { return false }
	maybeOpenDashboard(context.Background(), "http://127.0.0.1:8787")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
