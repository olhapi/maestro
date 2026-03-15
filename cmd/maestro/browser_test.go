package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenDashboardWhenReadyLaunchesBrowser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
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
	dashboardBrowserLauncher = func(ctx context.Context, rawURL string) error {
		openedURL = rawURL
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
