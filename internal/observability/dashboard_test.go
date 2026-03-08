package observability

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDashboardIdle(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	out := FormatDashboard(Snapshot{
		GeneratedAt: now,
		CodexTotals: TokenTotals{},
	}, DashboardOptions{Now: now})
	if !strings.Contains(out, "running_entries=idle") {
		t.Fatalf("expected idle dashboard, got %q", out)
	}
}

func TestFormatDashboardBusy(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	out := FormatDashboard(Snapshot{
		GeneratedAt: now,
		Running: []RunningEntry{{
			Identifier:  "ISS-101",
			State:       "running",
			SessionID:   "thread-1",
			TurnCount:   4,
			LastEvent:   "turn.completed",
			LastMessage: "command finished",
			StartedAt:   now.Add(-15 * time.Second),
			Tokens:      TokenTotals{TotalTokens: 42},
		}},
		CodexTotals: TokenTotals{TotalTokens: 42},
	}, DashboardOptions{Now: now, DashboardURL: "http://127.0.0.1:8787"})
	for _, want := range []string{"ISS-101", "dashboard_url=http://127.0.0.1:8787", "tokens=42"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in %q", want, out)
		}
	}
}

func TestFormatDashboardRetryQueueSanitizesNewlines(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	out := FormatDashboard(Snapshot{
		GeneratedAt: now,
		Retrying: []RetryEntry{{
			Identifier: "ISS-450",
			Attempt:    3,
			DueAt:      now.Add(3 * time.Second),
			Error:      "boom\\nretry\nagain",
		}},
	}, DashboardOptions{Now: now})
	if strings.Contains(out, "\\n") || strings.Contains(out, "\nagain") {
		t.Fatalf("expected sanitized newlines, got %q", out)
	}
	if !strings.Contains(out, "ISS-450") {
		t.Fatalf("expected retry entry, got %q", out)
	}
}
