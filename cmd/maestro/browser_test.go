package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	oldStarter := browserCommandStarter
	t.Cleanup(func() {
		browserCommandStarter = oldStarter
	})

	var gotCommand string
	var gotArgs []string
	browserCommandStarter = func(_ context.Context, command string, args []string) error {
		gotCommand = command
		gotArgs = append([]string(nil), args...)
		return nil
	}

	const rawURL = "https://example.com/docs"
	wantCommand, wantArgs, err := browserCommandFor(runtime.GOOS, rawURL)
	if err != nil {
		t.Fatalf("browserCommandFor returned error: %v", err)
	}

	if err := launchBrowserURL(context.Background(), rawURL); err != nil {
		t.Fatalf("launchBrowserURL returned error: %v", err)
	}

	if gotCommand != wantCommand {
		t.Fatalf("launchBrowserURL command = %q, want %q", gotCommand, wantCommand)
	}
	if !slices.Equal(gotArgs, wantArgs) {
		t.Fatalf("launchBrowserURL args = %v, want %v", gotArgs, wantArgs)
	}
}

func TestStartBrowserCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper test is POSIX-specific")
	}

	dir := t.TempDir()
	markerPath := filepath.Join(dir, "started.txt")
	scriptPath := filepath.Join(dir, "launcher.sh")
	script := "#!/bin/sh\nprintf 'ok' > \"$1\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write launcher helper: %v", err)
	}

	if err := startBrowserCommand(context.Background(), "sh", []string{scriptPath, markerPath}); err != nil {
		t.Fatalf("startBrowserCommand returned error: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(markerPath)
		if err == nil {
			if string(data) != "ok" {
				t.Fatalf("marker payload = %q, want %q", string(data), "ok")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected helper marker at %s", markerPath)
}

func TestStartBrowserCommandReturnsStartError(t *testing.T) {
	if err := startBrowserCommand(context.Background(), "definitely-not-a-real-browser-command", nil); err == nil {
		t.Fatal("expected startBrowserCommand to return an error for a missing executable")
	}
}

func TestBrowserCommandForUnsupportedPlatform(t *testing.T) {
	if _, _, err := browserCommandFor("plan9", "https://example.com/docs"); err == nil {
		t.Fatal("expected unsupported platform error")
	}
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

func TestMaybeOpenDashboardRespectsDisableEnv(t *testing.T) {
	oldInteractive := dashboardInteractiveCheck
	oldLauncher := dashboardBrowserLauncher
	t.Cleanup(func() {
		dashboardInteractiveCheck = oldInteractive
		dashboardBrowserLauncher = oldLauncher
	})

	t.Setenv("MAESTRO_DISABLE_BROWSER_OPEN", "1")
	dashboardInteractiveCheck = func() bool { return true }
	dashboardBrowserLauncher = func(context.Context, string) error {
		t.Fatal("expected browser launch to be disabled by env")
		return nil
	}

	maybeOpenDashboard(context.Background(), "http://127.0.0.1:8787")
	time.Sleep(25 * time.Millisecond)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
