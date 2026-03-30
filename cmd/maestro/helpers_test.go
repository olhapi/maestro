package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/providers"
	"github.com/olhapi/maestro/internal/testutil/inprocessserver"
	"github.com/spf13/cobra"
)

func TestParsePositiveIntBranches(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "trimmed", raw: " 42 ", want: 42},
		{name: "negative", raw: "-7", want: -7},
		{name: "invalid", raw: "abc", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePositiveInt(tc.raw, "--count")
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "--count expects an integer") {
					t.Fatalf("expected parse error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePositiveInt(%q) returned error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("parsePositiveInt(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestHelperCoverageBranches(t *testing.T) {
	if level, name, err := parseLogLevel("debug"); err != nil || level != slog.LevelDebug || name != "debug" {
		t.Fatalf("parseLogLevel(debug) = (%v, %q, %v)", level, name, err)
	}

	if got := parseCSV(" alpha , beta , alpha ,, gamma "); len(got) != 3 || got[0] != "alpha" || got[1] != "beta" || got[2] != "gamma" {
		t.Fatalf("unexpected parsed csv: %#v", got)
	}

	if err := ensureDir(""); err != nil {
		t.Fatalf("ensureDir blank path: %v", err)
	}
	if err := ensureDir("."); err != nil {
		t.Fatalf("ensureDir dot path: %v", err)
	}

	old := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	if logPath, err := setupLoggerWithWriter(nil, "", 1024, 2, slog.LevelInfo); err != nil || logPath != "" {
		t.Fatalf("setupLoggerWithWriter blank logs root = (%q, %v)", logPath, err)
	}

	logsFile := filepath.Join(t.TempDir(), "logs-file")
	if err := os.WriteFile(logsFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write logs file: %v", err)
	}
	if _, err := setupLoggerWithWriter(&bytes.Buffer{}, logsFile, 1024, 2, slog.LevelInfo); err == nil {
		t.Fatal("expected setupLoggerWithWriter to fail when logs root is a file")
	}

	if runtime.GOOS != "windows" {
		logDir := filepath.Join(t.TempDir(), "readonly-logs")
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			t.Fatalf("mkdir readonly logs: %v", err)
		}
		if err := os.Chmod(logDir, 0o555); err != nil {
			t.Fatalf("chmod readonly logs: %v", err)
		}
		if _, err := setupLoggerWithWriter(&bytes.Buffer{}, logDir, 1024, 2, slog.LevelInfo); err == nil {
			t.Fatal("expected setupLoggerWithWriter to fail when log file cannot be created")
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TEAM", "")
	if _, err := openStoreForReadCommands("$HOME/.maestro/$TEAM/maestro.db"); err == nil {
		t.Fatal("expected unresolved db path to fail")
	}

	dbPath := filepath.Join(t.TempDir(), "helpers.db")
	store, svc, err := openProviderService(dbPath)
	if err != nil {
		t.Fatalf("openProviderService: %v", err)
	}
	if store == nil || svc == nil {
		t.Fatal("expected writable store")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close writable store: %v", err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(dbPath, 0o444); err != nil {
			t.Fatalf("chmod read-only db: %v", err)
		}
		store, svc, err := openReadOnlyProviderService(dbPath)
		if err != nil {
			t.Fatalf("openReadOnlyProviderService read-only: %v", err)
		}
		if store == nil || svc == nil {
			t.Fatal("expected read-only provider service")
		}
		if !store.ReadOnly() {
			t.Fatalf("expected read-only db to open in read-only mode")
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close read-only store: %v", err)
		}
	}
}

func TestOpenStoreForReadCommandsFallbackPolicy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fallback.db")
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close writable store: %v", err)
	}

	t.Run("permission errors fall back to read-only", func(t *testing.T) {
		var writableCalls, readOnlyCalls int
		readOnlyStore, err := openStoreForReadCommandsWith(dbPath,
			func(string) (*kanban.Store, error) {
				writableCalls++
				return nil, fs.ErrPermission
			},
			func(path string) (*kanban.Store, error) {
				readOnlyCalls++
				return kanban.NewReadOnlyStore(path)
			},
		)
		if err != nil {
			t.Fatalf("openStoreForReadCommandsWith permission fallback: %v", err)
		}
		t.Cleanup(func() {
			_ = readOnlyStore.Close()
		})
		if writableCalls != 1 || readOnlyCalls != 1 {
			t.Fatalf("expected one writable and one read-only open, got writable=%d readOnly=%d", writableCalls, readOnlyCalls)
		}
		if !readOnlyStore.ReadOnly() {
			t.Fatal("expected fallback store to be read-only")
		}
	})

	t.Run("other errors do not fall back", func(t *testing.T) {
		var writableCalls, readOnlyCalls int
		sentinel := errors.New("migration failed")
		readOnlyStore, err := openStoreForReadCommandsWith(dbPath,
			func(string) (*kanban.Store, error) {
				writableCalls++
				return nil, sentinel
			},
			func(string) (*kanban.Store, error) {
				readOnlyCalls++
				return nil, nil
			},
		)
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected migration error to surface, got %v", err)
		}
		if readOnlyStore != nil {
			t.Fatalf("expected nil store on migration failure, got %#v", readOnlyStore)
		}
		if writableCalls != 1 || readOnlyCalls != 0 {
			t.Fatalf("expected writable open only, got writable=%d readOnly=%d", writableCalls, readOnlyCalls)
		}
	})
}

func TestResolveCLIRepoPathBranches(t *testing.T) {
	absRepo := filepath.Join(t.TempDir(), "repo")
	if got := resolveCLIRepoPath(absRepo); filepath.Clean(got) != filepath.Clean(absRepo) {
		t.Fatalf("resolveCLIRepoPath(%q) = %q, want %q", absRepo, got, absRepo)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if got := resolveCLIRepoPath(""); filepath.Clean(got) != filepath.Clean(cwd) {
		t.Fatalf("resolveCLIRepoPath(\"\") = %q, want cwd %q", got, cwd)
	}
}

func TestIssueExecutionFormattingHelpers(t *testing.T) {
	if got := issueExecutionString(nil); got != "" {
		t.Fatalf("issueExecutionString(nil) = %q, want empty", got)
	}
	if got := issueExecutionString("  ready  "); got != "ready" {
		t.Fatalf("issueExecutionString(string) = %q, want ready", got)
	}
	if got := issueExecutionString(123); got != "123" {
		t.Fatalf("issueExecutionString(int) = %q, want 123", got)
	}

	if got := workspaceRecoveryStatusLabel(""); got != "" {
		t.Fatalf("workspaceRecoveryStatusLabel empty = %q", got)
	}
	if got := workspaceRecoveryStatusLabel("recovering"); got != "Recovering" {
		t.Fatalf("workspaceRecoveryStatusLabel recovering = %q", got)
	}
	if got := workspaceRecoveryStatusLabel("required"); got != "Required" {
		t.Fatalf("workspaceRecoveryStatusLabel required = %q", got)
	}
	if got := workspaceRecoveryStatusLabel("blocked"); got != "Blocked" {
		t.Fatalf("workspaceRecoveryStatusLabel default = %q", got)
	}
}

func TestReadOnlyStoreAndEnvPathBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only database permissions behave differently on Windows")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TEAM", "")
	if _, err := openReadOnlyStore("$HOME/.maestro/$TEAM/maestro.db"); err == nil {
		t.Fatal("expected unresolved read-only db path to fail")
	}
}

func TestPrintIssueAssetTableBranches(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	assets := []kanban.IssueAsset{{
		ID:          "asset-1",
		Filename:    "coverage.png",
		ContentType: "image/png",
		ByteSize:    128,
		CreatedAt:   now,
	}}

	var quiet bytes.Buffer
	printIssueAssetTable(&quiet, assets, outputMode{quiet: true})
	if strings.TrimSpace(quiet.String()) != "asset-1" {
		t.Fatalf("unexpected quiet asset table: %q", quiet.String())
	}

	var defaultTable bytes.Buffer
	printIssueAssetTable(&defaultTable, assets, outputMode{})
	defaultText := defaultTable.String()
	for _, want := range []string{"ID", "FILENAME", "SIZE", "asset-1", "coverage.png", "128"} {
		if !strings.Contains(defaultText, want) {
			t.Fatalf("expected %q in asset table %q", want, defaultText)
		}
	}

	var wide bytes.Buffer
	printIssueAssetTable(&wide, assets, outputMode{wide: true})
	wideText := wide.String()
	for _, want := range []string{"CONTENT TYPE", "CREATED", "image/png", now.UTC().Format(time.RFC3339)} {
		if !strings.Contains(wideText, want) {
			t.Fatalf("expected %q in wide asset table %q", want, wideText)
		}
	}
}

func TestPayloadBuilderBranches(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "payloads.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProject("Payload Project", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Payload Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Payload Issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	svc := providers.NewService(store)

	if payload, projectSummary, issues, err := buildProjectPayload(context.Background(), svc, store, project.ID); err != nil || projectSummary == nil || len(issues) != 1 {
		t.Fatalf("buildProjectPayload success = %#v %#v %#v %v", payload, projectSummary, issues, err)
	}
	if _, _, _, err := buildProjectPayload(context.Background(), svc, store, "missing"); err == nil {
		t.Fatal("expected missing project payload lookup to fail")
	}

	if payload, epicSummary, issues, err := buildEpicPayload(context.Background(), svc, store, epic.ID); err != nil || epicSummary == nil || len(issues) != 1 {
		t.Fatalf("buildEpicPayload success = %#v %#v %#v %v", payload, epicSummary, issues, err)
	}
	if _, _, _, err := buildEpicPayload(context.Background(), svc, store, "missing"); err == nil {
		t.Fatal("expected missing epic payload lookup to fail")
	}
	if err := store.UpdateEpic(epic.ID, "", epic.Name, epic.Description); err != nil {
		t.Fatalf("UpdateEpic blank project: %v", err)
	}
	if payload, epicSummary, issues, err := buildEpicPayload(context.Background(), svc, store, epic.ID); err != nil || epicSummary == nil || payload == nil || len(issues) != 1 {
		t.Fatalf("buildEpicPayload blank project = %#v %#v %#v %v", payload, epicSummary, issues, err)
	}

	closedStore, err := kanban.NewStore(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("NewStore closed: %v", err)
	}
	if err := closedStore.Close(); err != nil {
		t.Fatalf("Close closedStore: %v", err)
	}
	closedSvc := providers.NewService(closedStore)
	if _, _, _, err := buildProjectPayload(context.Background(), closedSvc, closedStore, issue.ProjectID); err == nil {
		t.Fatal("expected closed store project payload lookup to fail")
	}
	if _, _, _, err := buildEpicPayload(context.Background(), closedSvc, closedStore, epic.ID); err == nil {
		t.Fatal("expected closed store epic payload lookup to fail")
	}
}

func TestCompletionCommandBranches(t *testing.T) {
	cmd := newCompletionCmd(&cobra.Command{Use: "maestro"})
	if err := cmd.RunE(cmd, []string{"elvish"}); err == nil || !strings.Contains(err.Error(), "unsupported shell") {
		t.Fatalf("expected unsupported shell error, got %v", err)
	}
}

func TestProjectStartStopErrorBranches(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := setupRepo(t)
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.CreateProject("Platform", "", repoPath, filepath.Join(repoPath, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	srv, err := inprocessserver.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	if err != nil {
		t.Fatalf("inprocessserver.New: %v", err)
	}
	t.Cleanup(srv.Close)

	for _, args := range [][]string{
		{"--db", dbPath, "project", "start", project.ID, "--api-url", srv.URL},
		{"--db", dbPath, "project", "stop", project.ID, "--api-url", srv.URL},
	} {
		code, _, stderr := runCLI(t, args...)
		if code == 0 || !strings.Contains(stderr, "returned 500") {
			t.Fatalf("expected project command to fail with 500, got code=%d stderr=%s", code, stderr)
		}
	}
}
