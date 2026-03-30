package kanban

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecurrenceHelpersAndPathResolution(t *testing.T) {
	t.Run("bool conversions and recurrence overlay", func(t *testing.T) {
		trueValue := true
		if got, ok := boolFromValue(true); !ok || !got {
			t.Fatalf("boolFromValue(true) = %v %v", got, ok)
		}
		if got, ok := boolFromValue(&trueValue); !ok || !got {
			t.Fatalf("boolFromValue(*bool) = %v %v", got, ok)
		}
		if got, ok := boolFromValue(nil); ok || got {
			t.Fatalf("boolFromValue(nil) = %v %v", got, ok)
		}
		if got, ok := boolFromValue("true"); ok || got {
			t.Fatalf("boolFromValue(string) = %v %v", got, ok)
		}

		issue := &Issue{
			IssueType: IssueTypeStandard,
			Cron:      "0 0 * * *",
			Enabled:   true,
			NextRunAt: func() *time.Time { v := time.Unix(100, 0).UTC(); return &v }(),
			LastEnqueuedAt: func() *time.Time {
				v := time.Unix(50, 0).UTC()
				return &v
			}(),
			PendingRerun: true,
		}
		applyRecurrenceToIssue(issue, nil)
		if issue.Cron != "" || issue.Enabled || issue.NextRunAt != nil || issue.LastEnqueuedAt != nil || issue.PendingRerun {
			t.Fatalf("expected non-recurring issue recurrence fields to be cleared, got %#v", issue)
		}

		recurrence := &IssueRecurrence{
			Cron:      "0 0 * * *",
			Enabled:   true,
			NextRunAt: func() *time.Time { v := time.Unix(100, 0).UTC(); return &v }(),
			LastEnqueuedAt: func() *time.Time {
				v := time.Unix(50, 0).UTC()
				return &v
			}(),
			PendingRerun: true,
		}
		issue = &Issue{IssueType: IssueTypeRecurring}
		applyRecurrenceToIssue(issue, recurrence)
		if issue.Cron != recurrence.Cron || !issue.Enabled || issue.NextRunAt == nil || issue.LastEnqueuedAt == nil || !issue.PendingRerun {
			t.Fatalf("expected recurrence overlay to be copied, got %#v", issue)
		}
	})

	t.Run("default enabled and recurrence builder", func(t *testing.T) {
		if !defaultRecurringEnabled(nil) {
			t.Fatal("expected nil enabled flag to default to true")
		}
		falseValue := false
		if defaultRecurringEnabled(&falseValue) {
			t.Fatal("expected explicit false enabled flag to stay false")
		}

		now := time.Now().UTC()
		current := &IssueRecurrence{
			CreatedAt: now.Add(-time.Hour),
			LastEnqueuedAt: func() *time.Time {
				v := now.Add(-10 * time.Minute).UTC()
				return &v
			}(),
			PendingRerun: true,
		}
		nextRunAt := now.Add(30 * time.Minute)
		recurrence, err := buildIssueRecurrence("issue-1", " 0 0 * * * ", true, &nextRunAt, current, now)
		if err != nil {
			t.Fatalf("buildIssueRecurrence: %v", err)
		}
		if recurrence.IssueID != "issue-1" || recurrence.Cron != "0 0 * * *" || !recurrence.Enabled || !recurrence.PendingRerun {
			t.Fatalf("unexpected recurrence result: %#v", recurrence)
		}
		if recurrence.NextRunAt == nil || !recurrence.NextRunAt.Equal(nextRunAt.UTC()) {
			t.Fatalf("expected next run time to be preserved, got %#v", recurrence.NextRunAt)
		}
		if !recurrence.CreatedAt.Equal(current.CreatedAt) || recurrence.LastEnqueuedAt == nil || !recurrence.LastEnqueuedAt.Equal(current.LastEnqueuedAt.UTC()) {
			t.Fatalf("expected existing recurrence timestamps to be preserved, got %#v", recurrence)
		}
		if _, err := buildIssueRecurrence("issue-1", "not-a-cron", true, nil, nil, now); err == nil {
			t.Fatal("expected invalid cron to fail")
		}
	})

	t.Run("path resolution", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("TEAM", "")

		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chdir(cwd)
		})
		workdir := t.TempDir()
		if err := os.Chdir(workdir); err != nil {
			t.Fatalf("Chdir: %v", err)
		}
		cwdAfter, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd after chdir: %v", err)
		}

		relative, err := resolveConfiguredPath("configs/workflow.md")
		if err != nil {
			t.Fatalf("resolveConfiguredPath relative: %v", err)
		}
		if relative != filepath.Clean(filepath.Join(cwdAfter, "configs/workflow.md")) {
			t.Fatalf("unexpected relative path result: %q", relative)
		}

		absoluteInput := filepath.Join(workdir, "abs", "workflow.md")
		absolute, err := resolveConfiguredPath(absoluteInput)
		if err != nil {
			t.Fatalf("resolveConfiguredPath absolute: %v", err)
		}
		if absolute != filepath.Clean(absoluteInput) {
			t.Fatalf("unexpected absolute path result: %q", absolute)
		}

		homePath, err := resolveConfiguredPath("~/workflow.md")
		if err != nil {
			t.Fatalf("resolveConfiguredPath tilde: %v", err)
		}
		if homePath != filepath.Join(home, "workflow.md") {
			t.Fatalf("unexpected home path result: %q", homePath)
		}

		unresolved, err := resolveConfiguredPath("$TEAM/workflow.md")
		if err != nil {
			t.Fatalf("resolveConfiguredPath unresolved env: %v", err)
		}
		if !strings.HasPrefix(unresolved, "$TEAM") {
			t.Fatalf("expected unresolved env path to remain literal, got %q", unresolved)
		}
	})

	t.Run("foreign key verification", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "fk.db")
		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			t.Fatalf("sql.Open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
			t.Fatalf("disable foreign keys: %v", err)
		}
		for _, stmt := range []string{
			`CREATE TABLE parents (id TEXT PRIMARY KEY)`,
			`CREATE TABLE children (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL REFERENCES parents(id))`,
			`INSERT INTO children (id, parent_id) VALUES ('child-1', 'missing-parent')`,
		} {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("exec %q: %v", stmt, err)
			}
		}

		store := &Store{db: db}
		if err := store.verifyForeignKeys("legacy_migration"); err == nil || !strings.Contains(err.Error(), "foreign key check failed") {
			t.Fatalf("expected foreign key verification error, got %v", err)
		}
	})
}
