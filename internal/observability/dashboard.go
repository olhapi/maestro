package observability

import (
	"fmt"
	"strings"
	"time"
)

type DashboardOptions struct {
	Now          time.Time
	DashboardURL string
}

func FormatDashboard(snapshot Snapshot, opts DashboardOptions) string {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	lines := []string{
		"MAESTRO STATUS",
		fmt.Sprintf("generated_at=%s", snapshot.GeneratedAt.UTC().Format(time.RFC3339)),
		fmt.Sprintf("running=%d retrying=%d paused=%d total_tokens=%d", len(snapshot.Running), len(snapshot.Retrying), len(snapshot.Paused), snapshot.CodexTotals.TotalTokens),
	}
	if strings.TrimSpace(opts.DashboardURL) != "" {
		lines = append(lines, "dashboard_url="+strings.TrimSpace(opts.DashboardURL))
	}

	if len(snapshot.Running) == 0 {
		lines = append(lines, "running_entries=idle")
	} else {
		lines = append(lines, "running_entries:")
		for _, entry := range snapshot.Running {
			age := now.Sub(entry.StartedAt).Round(time.Second)
			lines = append(lines, fmt.Sprintf(
				"  %s state=%s session=%s turns=%d age=%s tokens=%d event=%s message=%s",
				entry.Identifier,
				entry.State,
				entry.SessionID,
				entry.TurnCount,
				age,
				entry.Tokens.TotalTokens,
				entry.LastEvent,
				sanitizeMessage(entry.LastMessage),
			))
		}
	}

	if len(snapshot.Retrying) == 0 {
		lines = append(lines, "retry_queue=empty")
	} else {
		lines = append(lines, "retry_queue:")
		for _, entry := range snapshot.Retrying {
			dueIn := entry.DueAt.Sub(now).Round(time.Second)
			lines = append(lines, fmt.Sprintf(
				"  %s attempt=%d due_in=%s error=%s",
				entry.Identifier,
				entry.Attempt,
				dueIn,
				sanitizeMessage(entry.Error),
			))
		}
	}

	if len(snapshot.Paused) == 0 {
		lines = append(lines, "paused_queue=empty")
	} else {
		lines = append(lines, "paused_queue:")
		for _, entry := range snapshot.Paused {
			lines = append(lines, fmt.Sprintf(
				"  %s attempt=%d paused_at=%s failures=%d/%d error=%s",
				entry.Identifier,
				entry.Attempt,
				entry.PausedAt.UTC().Format(time.RFC3339),
				entry.ConsecutiveFailures,
				entry.PauseThreshold,
				sanitizeMessage(entry.Error),
			))
		}
	}

	return strings.Join(lines, "\n")
}
