package main

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/kanban"
)

func TestParseGlobalOptions(t *testing.T) {
	opts, remaining, err := parseGlobalOptions([]string{"run", "--log-level", "debug", "--db", "./db.sqlite"})
	if err != nil {
		t.Fatalf("parseGlobalOptions failed: %v", err)
	}
	if opts.logLevel != slog.LevelDebug || opts.logLevelName != "debug" {
		t.Fatalf("unexpected global options: %+v", opts)
	}
	if got := strings.Join(remaining, " "); got != "run --db ./db.sqlite" {
		t.Fatalf("unexpected remaining args: %q", got)
	}
}

func TestParseGlobalOptionsRejectsInvalidLevel(t *testing.T) {
	if _, _, err := parseGlobalOptions([]string{"run", "--log-level", "verbose"}); err == nil {
		t.Fatal("expected invalid log level error")
	}
}

func TestParseLogLevelVariants(t *testing.T) {
	tests := []struct {
		raw   string
		level slog.Level
		name  string
	}{
		{raw: "", level: slog.LevelInfo, name: "info"},
		{raw: "info", level: slog.LevelInfo, name: "info"},
		{raw: "warning", level: slog.LevelWarn, name: "warn"},
		{raw: "error", level: slog.LevelError, name: "error"},
	}
	for _, tc := range tests {
		level, name, err := parseLogLevel(tc.raw)
		if err != nil {
			t.Fatalf("parseLogLevel(%q) returned error: %v", tc.raw, err)
		}
		if level != tc.level || name != tc.name {
			t.Fatalf("parseLogLevel(%q) = (%v, %q), want (%v, %q)", tc.raw, level, name, tc.level, tc.name)
		}
	}
}

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

func TestSetupLoggerWithWriterFiltersByLevelAndWritesFile(t *testing.T) {
	old := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	logDir := t.TempDir()
	var stdout bytes.Buffer
	logPath, err := setupLoggerWithWriter(&stdout, logDir, 1024, 2, slog.LevelWarn)
	if err != nil {
		t.Fatalf("setupLoggerWithWriter failed: %v", err)
	}
	slog.Info("hidden info")
	slog.Warn("visible warn", "component", "test")

	if strings.Contains(stdout.String(), "hidden info") {
		t.Fatalf("expected info log to be filtered, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "visible warn") {
		t.Fatalf("expected warn log in stdout, got %q", stdout.String())
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "hidden info") {
		t.Fatalf("expected info log to be filtered from file, got %q", text)
	}
	if !strings.Contains(text, "visible warn") {
		t.Fatalf("expected warn log in file, got %q", text)
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
	binPath := filepath.Join(t.TempDir(), "maestro")
	const releaseVersion = "1.2.3-test"

	buildCmd := exec.Command("go", "build", "-ldflags", "-X main.version="+releaseVersion, "-o", binPath, "./cmd/maestro")
	buildCmd.Dir = repoRoot
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}

	versionCmd := exec.Command(binPath, "version")
	if output, err := versionCmd.CombinedOutput(); err != nil {
		t.Fatalf("version command failed: %v\n%s", err, output)
	} else if got := strings.TrimSpace(string(output)); got != "maestro "+releaseVersion {
		t.Fatalf("unexpected version output: %q", got)
	}
}

func TestCommandHelpersSmoke(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := t.TempDir()

	var workflowOut, workflowErr bytes.Buffer
	if code := workflowCommand([]string{"init", repoPath}, bytes.NewBuffer(nil), &workflowOut, &workflowErr); code != 0 {
		t.Fatalf("workflowCommand failed: code=%d stderr=%s", code, workflowErr.String())
	}
	if _, err := os.Stat(filepath.Join(repoPath, "WORKFLOW.md")); err != nil {
		t.Fatalf("expected workflow file: %v", err)
	}

	var projectCreate bytes.Buffer
	if code := projectCommand([]string{"create", "Platform", "--repo", repoPath, "--workflow", filepath.Join(repoPath, "WORKFLOW.md"), "--db", dbPath}, &projectCreate); code != 0 {
		t.Fatalf("project create failed: %s", projectCreate.String())
	}

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	projects, err := store.ListProjects()
	if err != nil || len(projects) != 1 {
		t.Fatalf("expected project in db: err=%v projects=%v", err, projects)
	}
	projectID := projects[0].ID

	var issueCreate bytes.Buffer
	if code := issueCommand([]string{"create", "Ship coverage", "--project", projectID, "--priority", "2", "--labels", "test,smoke", "--db", dbPath}, &issueCreate); code != 0 {
		t.Fatalf("issue create failed: %s", issueCreate.String())
	}
	identifier := strings.Fields(issueCreate.String())[2]
	identifier = strings.TrimSuffix(identifier, ":")

	var issueList bytes.Buffer
	if code := issueCommand([]string{"list", "--project", projectID, "--db", dbPath}, &issueList); code != 0 {
		t.Fatalf("issue list failed: %s", issueList.String())
	}
	if !strings.Contains(issueList.String(), identifier) {
		t.Fatalf("expected identifier %s in list output %q", identifier, issueList.String())
	}

	var issueShow bytes.Buffer
	if code := issueCommand([]string{"show", identifier, "--db", dbPath}, &issueShow); code != 0 {
		t.Fatalf("issue show failed: %s", issueShow.String())
	}
	if !strings.Contains(issueShow.String(), "Identifier:  "+identifier) {
		t.Fatalf("unexpected issue show output: %q", issueShow.String())
	}

	var issueMove bytes.Buffer
	if code := issueCommand([]string{"move", identifier, "in_progress", "--db", dbPath}, &issueMove); code != 0 {
		t.Fatalf("issue move failed: %s", issueMove.String())
	}

	var issueUpdate bytes.Buffer
	if code := issueCommand([]string{"update", identifier, "--title", "Ship more coverage", "--desc", "Updated", "--pr", "17", "https://example.com/pr/17", "--db", dbPath}, &issueUpdate); code != 0 {
		t.Fatalf("issue update failed: %s", issueUpdate.String())
	}

	blocker, err := store.CreateIssue(projectID, "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	var issueBlock bytes.Buffer
	if code := issueCommand([]string{"block", identifier, blocker.Identifier, "--db", dbPath}, &issueBlock); code != 0 {
		t.Fatalf("issue block failed: %s", issueBlock.String())
	}

	var boardOut bytes.Buffer
	if code := boardCommand([]string{"--db", dbPath}, &boardOut); code != 0 {
		t.Fatalf("board command failed: %s", boardOut.String())
	}
	if !strings.Contains(boardOut.String(), "MAESTRO KANBAN") {
		t.Fatalf("unexpected board output: %q", boardOut.String())
	}

	var statusJSON bytes.Buffer
	if code := statusCommand([]string{"--db", dbPath, "--json"}, &statusJSON); code != 0 {
		t.Fatalf("status json failed: %s", statusJSON.String())
	}
	if !strings.Contains(statusJSON.String(), "\"projects\":1") {
		t.Fatalf("unexpected status json: %q", statusJSON.String())
	}

	var statusDashboard bytes.Buffer
	if code := statusCommand([]string{"--dashboard", "--dashboard-url", "http://127.0.0.1:8787"}, &statusDashboard); code != 0 {
		t.Fatalf("status dashboard failed: %s", statusDashboard.String())
	}
	if !strings.Contains(strings.ToLower(statusDashboard.String()), "dashboard") {
		t.Fatalf("unexpected dashboard output: %q", statusDashboard.String())
	}

	var verifyOut bytes.Buffer
	_ = verifyCommand([]string{"--db", dbPath, "--repo", repoPath, "--json"}, &verifyOut)
	if !strings.Contains(verifyOut.String(), "\"checks\"") {
		t.Fatalf("unexpected verify output: %q", verifyOut.String())
	}

	var specOut bytes.Buffer
	_ = specCheckCommand([]string{"--repo", repoPath, "--json"}, &specOut)
	if !strings.Contains(specOut.String(), "\"checks\"") {
		t.Fatalf("unexpected spec-check output: %q", specOut.String())
	}

	var issueDelete bytes.Buffer
	if code := issueCommand([]string{"delete", identifier, "--db", dbPath}, &issueDelete); code != 0 {
		t.Fatalf("issue delete failed: %s", issueDelete.String())
	}

	var projectDelete bytes.Buffer
	if code := projectCommand([]string{"delete", projectID, "--db", dbPath}, &projectDelete); code != 0 {
		t.Fatalf("project delete failed: %s", projectDelete.String())
	}
}

func TestCommandHelpersErrorPaths(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := t.TempDir()

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	project, err := store.CreateProject("Platform", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Ship tests", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	tests := []struct {
		name string
		run  func(*bytes.Buffer) int
		want string
	}{
		{
			name: "workflow unknown",
			run: func(buf *bytes.Buffer) int {
				return workflowCommand([]string{"unknown"}, bytes.NewBuffer(nil), buf, buf)
			},
			want: "Unknown workflow command",
		},
		{
			name: "issue unknown",
			run: func(buf *bytes.Buffer) int {
				return issueCommand([]string{"unknown", "--db", dbPath}, buf)
			},
			want: "Unknown command",
		},
		{
			name: "issue show missing",
			run: func(buf *bytes.Buffer) int {
				return issueCommand([]string{"show", "ISS-404", "--db", dbPath}, buf)
			},
			want: "Issue not found",
		},
		{
			name: "issue move usage",
			run: func(buf *bytes.Buffer) int {
				return issueCommand([]string{"move", issue.Identifier, "--db", dbPath}, buf)
			},
			want: "Usage: maestro issue move",
		},
		{
			name: "project unknown",
			run: func(buf *bytes.Buffer) int {
				return projectCommand([]string{"unknown", "--db", dbPath}, buf)
			},
			want: "Unknown command",
		},
		{
			name: "project delete usage",
			run: func(buf *bytes.Buffer) int {
				return projectCommand([]string{"delete", "--db", dbPath}, buf)
			},
			want: "Usage: maestro project delete",
		},
		{
			name: "project create missing repo",
			run: func(buf *bytes.Buffer) int {
				return projectCommand([]string{"create", "No Repo", "--db", dbPath}, buf)
			},
			want: "--repo is required",
		},
		{
			name: "verify text",
			run: func(buf *bytes.Buffer) int {
				return verifyCommand([]string{"--db", dbPath, "--repo", repoPath}, buf)
			},
			want: "Verification",
		},
		{
			name: "spec text",
			run: func(buf *bytes.Buffer) int {
				return specCheckCommand([]string{"--repo", repoPath}, buf)
			},
			want: "Spec Check",
		},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		code := tc.run(&buf)
		if code == 0 && strings.Contains(tc.want, "Unknown") {
			t.Fatalf("%s: expected non-zero exit", tc.name)
		}
		if !strings.Contains(buf.String(), tc.want) {
			t.Fatalf("%s: expected %q in %q", tc.name, tc.want, buf.String())
		}
	}
}
