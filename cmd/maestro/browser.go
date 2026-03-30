package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

var (
	dashboardBrowserLauncher  = launchBrowserURL
	dashboardInteractiveCheck = terminalsInteractive
	browserCommandStarter     = startBrowserCommand
	dashboardHTTPClient       = &http.Client{
		Timeout: 250 * time.Millisecond,
	}
	dashboardOpenPollInterval = 50 * time.Millisecond
	dashboardOpenTimeout      = 3 * time.Second
)

func maybeOpenDashboard(ctx context.Context, baseURL string) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" || browserOpenDisabled() || !dashboardInteractiveCheck() {
		return
	}

	go func() {
		if err := openDashboardWhenReady(ctx, baseURL); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("Failed to open dashboard automatically", "url", baseURL, "error", err)
		}
	}()
}

func browserOpenDisabled() bool {
	return strings.TrimSpace(os.Getenv("MAESTRO_DISABLE_BROWSER_OPEN")) != ""
}

func openDashboardWhenReady(ctx context.Context, baseURL string) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}

	readyCtx, cancel := context.WithTimeout(ctx, dashboardOpenTimeout)
	defer cancel()

	healthURL := baseURL + "/health"
	ticker := time.NewTicker(dashboardOpenPollInterval)
	defer ticker.Stop()

	for {
		req, err := http.NewRequestWithContext(readyCtx, http.MethodGet, healthURL, nil)
		if err != nil {
			return err
		}
		resp, err := dashboardHTTPClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return dashboardBrowserLauncher(context.WithoutCancel(ctx), baseURL)
			}
		}

		select {
		case <-readyCtx.Done():
			return readyCtx.Err()
		case <-ticker.C:
		}
	}
}

func terminalsInteractive() bool {
	return isTerminal(os.Stdout) && isTerminal(os.Stderr)
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

func launchBrowserURL(ctx context.Context, rawURL string) error {
	command, args, err := browserCommandFor(runtime.GOOS, rawURL)
	if err != nil {
		return err
	}

	return browserCommandStarter(ctx, command, args)
}

func startBrowserCommand(ctx context.Context, command string, args []string) error {
	cmd := exec.CommandContext(ctx, command, args...)
	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func browserCommandFor(goos string, rawURL string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{rawURL}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return "xdg-open", []string{rawURL}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", rawURL}, nil
	default:
		return "", nil, fmt.Errorf("unsupported platform %q", goos)
	}
}
