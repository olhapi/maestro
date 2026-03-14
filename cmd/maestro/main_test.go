package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/kanban"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := execute(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func setupRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "WORKFLOW.md"), []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func sampleMainPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
}

func setupProjectAndIssue(t *testing.T) (dbPath string, repoPath string, project *kanban.Project, issue *kanban.Issue) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "maestro.db")
	repoPath = setupRepo(t)
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err = store.CreateProject("Platform", "", repoPath, filepath.Join(repoPath, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	issue, err = store.CreateIssue(project.ID, "", "Ship tests", "", 1, []string{"smoke"})
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	return dbPath, repoPath, project, issue
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

func TestOpenStoreUsesHomeDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, err := openStore("")
	if err != nil {
		t.Fatalf("openStore failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	dbPath := filepath.Join(home, ".maestro", "maestro.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected db at %s: %v", dbPath, err)
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

func TestScopedHelpCommands(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"issue", "--help"}, want: "Manage issues"},
		{args: []string{"project", "--help"}, want: "Manage projects"},
		{args: []string{"workflow", "--help"}, want: "Manage WORKFLOW.md files"},
	}
	for _, tc := range tests {
		code, stdout, stderr := runCLI(t, tc.args...)
		if code != 0 {
			t.Fatalf("%v returned code %d stderr=%s", tc.args, code, stderr)
		}
		if !strings.Contains(stdout, tc.want) {
			t.Fatalf("%v missing %q in %q", tc.args, tc.want, stdout)
		}
	}
}

func TestFlagErrorsAndUnknownFlags(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"issue", "create", "hello", "--project"}, want: "flag needs an argument"},
		{args: []string{"issue", "update", "ISS-1", "--labels"}, want: "flag needs an argument"},
		{args: []string{"project", "create", "demo", "--repo"}, want: "flag needs an argument"},
		{args: []string{"issue", "update", "ISS-1", "--unsupported"}, want: "unknown flag"},
	}
	for _, tc := range tests {
		code, _, stderr := runCLI(t, tc.args...)
		if code != exitCodeUsage {
			t.Fatalf("%v returned code %d, want %d", tc.args, code, exitCodeUsage)
		}
		if !strings.Contains(strings.ToLower(stderr), strings.ToLower(tc.want)) {
			t.Fatalf("%v missing %q in %q", tc.args, tc.want, stderr)
		}
	}
}

func TestMCPCommandRejectsExtensionsFlag(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) string
		wantErr string
	}{
		{
			name: "missing file",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing.json")
			},
			wantErr: "no longer accepts --extensions",
		},
		{
			name: "malformed json",
			setup: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "bad.json")
				if err := os.WriteFile(path, []byte(`{`), 0o644); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantErr: "no longer accepts --extensions",
		},
		{
			name: "invalid input schema",
			setup: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "bad-schema.json")
				body := `[{"name":"bad","description":"bad","command":"echo ok","input_schema":{"type":"string"}}]`
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantErr: "no longer accepts --extensions",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, "mcp", "--extensions", tc.setup(t))
			if code == 0 {
				t.Fatalf("expected mcp to fail for %s", tc.name)
			}
			if !strings.Contains(strings.ToLower(stderr), strings.ToLower(tc.wantErr)) {
				t.Fatalf("expected %q in stderr %q", tc.wantErr, stderr)
			}
		})
	}
}

func TestMCPCommandFailsFastWithoutLiveDaemon(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	code, _, stderr := runCLI(t, "--db", dbPath, "mcp")
	if code == 0 {
		t.Fatalf("expected mcp to fail without a live daemon")
	}
	if !strings.Contains(stderr, "no live Maestro daemon found") {
		t.Fatalf("expected missing daemon error, got %q", stderr)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("expected attach-only mcp command not to create %s, err=%v", dbPath, err)
	}
}

func TestIssueStateValidationAndAliases(t *testing.T) {
	dbPath, _, _, issue := setupProjectAndIssue(t)
	code, _, stderr := runCLI(t, "--db", dbPath, "issue", "mv", issue.Identifier, "invalid")
	if code != exitCodeUsage {
		t.Fatalf("expected usage exit code, got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "invalid state") {
		t.Fatalf("expected invalid state error, got %q", stderr)
	}

	code, stdout, stderr := runCLI(t, "--db", dbPath, "issue", "ls", "--quiet")
	if code != 0 {
		t.Fatalf("issue ls failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, issue.Identifier) {
		t.Fatalf("issue ls output missing identifier: %q", stdout)
	}
}

func TestIssueProjectEpicBoardJSONFlows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := setupRepo(t)

	code, stdout, stderr := runCLI(t, "--db", dbPath, "project", "create", "Platform", "--repo", repoPath, "--json")
	if code != 0 {
		t.Fatalf("project create failed: %d stderr=%s", code, stderr)
	}
	var project kanban.Project
	if err := json.Unmarshal([]byte(stdout), &project); err != nil {
		t.Fatalf("decode project: %v\n%s", err, stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "create", "CLI Overhaul", "--project", project.ID, "--json")
	if code != 0 {
		t.Fatalf("epic create failed: %d stderr=%s", code, stderr)
	}
	var epic kanban.Epic
	if err := json.Unmarshal([]byte(stdout), &epic); err != nil {
		t.Fatalf("decode epic: %v\n%s", err, stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "create", "Ship coverage", "--project", project.ID, "--epic", epic.ID, "--labels", "test,smoke", "--priority", "2", "--json")
	if code != 0 {
		t.Fatalf("issue create failed: %d stderr=%s", code, stderr)
	}
	var created kanban.IssueDetail
	if err := json.Unmarshal([]byte(stdout), &created); err != nil {
		t.Fatalf("decode issue create: %v\n%s", err, stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "update", created.Identifier, "--labels", "go,cli", "--priority", "5", "--branch", "feat/cli", "--pr-url", "https://example.com/pr/17", "--json")
	if code != 0 {
		t.Fatalf("issue update failed: %d stderr=%s", code, stderr)
	}
	var updated kanban.IssueDetail
	if err := json.Unmarshal([]byte(stdout), &updated); err != nil {
		t.Fatalf("decode issue update: %v\n%s", err, stdout)
	}
	if updated.Priority != 5 || updated.BranchName != "feat/cli" || updated.PRURL != "https://example.com/pr/17" {
		t.Fatalf("unexpected issue update payload: %+v", updated)
	}

	imagePath := filepath.Join(t.TempDir(), "coverage.png")
	if err := os.WriteFile(imagePath, sampleMainPNGBytes(), 0o644); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "images", "add", created.Identifier, imagePath, "--json")
	if code != 0 {
		t.Fatalf("issue image add failed: %d stderr=%s", code, stderr)
	}
	var image kanban.IssueImage
	if err := json.Unmarshal([]byte(stdout), &image); err != nil {
		t.Fatalf("decode issue image add: %v\n%s", err, stdout)
	}
	if image.ContentType != "image/png" {
		t.Fatalf("unexpected issue image payload: %+v", image)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "images", "list", created.Identifier, "--json")
	if code != 0 {
		t.Fatalf("issue image list failed: %d stderr=%s", code, stderr)
	}
	var imageList struct {
		Items []kanban.IssueImage `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &imageList); err != nil {
		t.Fatalf("decode issue image list: %v\n%s", err, stdout)
	}
	if len(imageList.Items) != 1 || imageList.Items[0].ID != image.ID {
		t.Fatalf("unexpected issue image list payload: %+v", imageList)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "show", created.Identifier)
	if code != 0 {
		t.Fatalf("issue show with images failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Images:") || !strings.Contains(stdout, image.ID) {
		t.Fatalf("expected image metadata in issue show output, got %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "images", "remove", created.Identifier, image.ID, "--json")
	if code != 0 {
		t.Fatalf("issue image remove failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"deleted\":true") {
		t.Fatalf("unexpected issue image remove payload: %s", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "list", "--project", project.ID, "--json")
	if code != 0 {
		t.Fatalf("issue list failed: %d stderr=%s", code, stderr)
	}
	var issueList struct {
		Items []kanban.IssueSummary `json:"items"`
		Total int                   `json:"total"`
	}
	if err := json.Unmarshal([]byte(stdout), &issueList); err != nil {
		t.Fatalf("decode issue list: %v\n%s", err, stdout)
	}
	if issueList.Total != 1 || len(issueList.Items) != 1 || issueList.Items[0].Identifier != created.Identifier {
		t.Fatalf("unexpected issue list payload: %+v", issueList)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "show", project.ID, "--json")
	if code != 0 {
		t.Fatalf("project show failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"project\"") || !strings.Contains(stdout, "\"issues\"") {
		t.Fatalf("unexpected project show payload: %s", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "show", epic.ID, "--json")
	if code != 0 {
		t.Fatalf("epic show failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"epic\"") || !strings.Contains(stdout, "\"issues\"") {
		t.Fatalf("unexpected epic show payload: %s", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "board", "--json")
	if code != 0 {
		t.Fatalf("board json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"columns\"") || !strings.Contains(stdout, "\"counts\"") {
		t.Fatalf("unexpected board payload: %s", stdout)
	}
}

func TestBlockerLifecycleCommands(t *testing.T) {
	dbPath, _, project, issue := setupProjectAndIssue(t)
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()
	blockerA, _ := store.CreateIssue(project.ID, "", "Blocker A", "", 0, nil)
	blockerB, _ := store.CreateIssue(project.ID, "", "Blocker B", "", 0, nil)

	code, _, stderr := runCLI(t, "--db", dbPath, "issue", "blockers", "set", issue.Identifier, blockerA.Identifier, blockerB.Identifier)
	if code != 0 {
		t.Fatalf("blockers set failed: %d stderr=%s", code, stderr)
	}
	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "unblock", issue.Identifier, blockerA.Identifier)
	if code != 0 {
		t.Fatalf("unblock failed: %d stderr=%s", code, stderr)
	}
	reloaded, err := store.GetIssueByIdentifier(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier failed: %v", err)
	}
	if len(reloaded.BlockedBy) != 1 || reloaded.BlockedBy[0] != blockerB.Identifier {
		t.Fatalf("unexpected blockers after unblock: %+v", reloaded.BlockedBy)
	}
	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "blockers", "clear", issue.Identifier)
	if code != 0 {
		t.Fatalf("blockers clear failed: %d stderr=%s", code, stderr)
	}
	reloaded, err = store.GetIssueByIdentifier(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier failed: %v", err)
	}
	if len(reloaded.BlockedBy) != 0 {
		t.Fatalf("expected blockers to be cleared, got %+v", reloaded.BlockedBy)
	}
}

func TestVerifyAndDoctorOutputs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := setupRepo(t)

	code, stdout, stderr := runCLI(t, "--db", dbPath, "verify", "--repo", repoPath, "--json")
	if code != 0 {
		t.Fatalf("verify json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"checks\"") || !strings.Contains(stdout, "\"remediation\"") {
		t.Fatalf("unexpected verify json: %s", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "doctor", "--repo", repoPath)
	if code != 0 {
		t.Fatalf("doctor failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Doctor") {
		t.Fatalf("unexpected doctor output: %s", stdout)
	}
}

func TestCompletionCommand(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		code, stdout, stderr := runCLI(t, "completion", shell)
		if code != 0 {
			t.Fatalf("completion %s failed: %d stderr=%s", shell, code, stderr)
		}
		if !strings.Contains(stdout, "maestro") {
			t.Fatalf("completion output for %s missing command name", shell)
		}
	}
}

func TestTextModeRecurringIssueCommands(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := setupRepo(t)

	code, stdout, stderr := runCLI(t, "--db", dbPath, "project", "create", "Automation", "--repo", repoPath, "--quiet")
	if code != 0 {
		t.Fatalf("project create failed: %d stderr=%s", code, stderr)
	}
	projectID := strings.TrimSpace(stdout)
	if projectID == "" {
		t.Fatal("expected quiet project id")
	}

	code, stdout, stderr = runCLI(
		t,
		"--db", dbPath,
		"issue", "create", "Scan GitHub ready-to-work",
		"--project", projectID,
		"--type", "recurring",
		"--cron", "*/15 * * * *",
		"--enabled=false",
		"--quiet",
	)
	if code != 0 {
		t.Fatalf("recurring issue create failed: %d stderr=%s", code, stderr)
	}
	identifier := strings.TrimSpace(stdout)
	if identifier == "" {
		t.Fatal("expected quiet issue identifier")
	}

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	issue, err := store.GetIssueByIdentifier(identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier: %v", err)
	}
	if issue.IssueType != kanban.IssueTypeRecurring || issue.Cron != "*/15 * * * *" || issue.Enabled {
		t.Fatalf("unexpected recurring issue after create: %+v", issue)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "list", "--type", "recurring", "--wide")
	if code != 0 {
		t.Fatalf("issue list recurring failed: %d stderr=%s", code, stderr)
	}
	for _, want := range []string{"TYPE", identifier, "recurring"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in recurring list output %q", want, stdout)
		}
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "show", identifier)
	if code != 0 {
		t.Fatalf("issue show recurring failed: %d stderr=%s", code, stderr)
	}
	for _, want := range []string{"Type:\trecurring", "Cron:\t*/15 * * * *", "Schedule:\tdisabled"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in recurring show output %q", want, stdout)
		}
	}

	code, stdout, stderr = runCLI(
		t,
		"--db", dbPath,
		"issue", "update", identifier,
		"--cron", "0 * * * *",
		"--enabled=true",
		"--quiet",
	)
	if code != 0 {
		t.Fatalf("issue update recurring failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != identifier {
		t.Fatalf("unexpected quiet issue update output %q", stdout)
	}

	updated, err := store.GetIssueByIdentifier(identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier updated: %v", err)
	}
	if updated.Cron != "0 * * * *" || !updated.Enabled {
		t.Fatalf("unexpected recurring issue after update: %+v", updated)
	}
}

func TestLiveCommandsUseAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/state":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"generated_at": "2026-03-09T00:00:00Z",
				"counts":       map[string]int{"running": 1, "retrying": 1},
				"running": []map[string]interface{}{
					{"issue_identifier": "ISS-1", "state": "in_progress", "session_id": "sess-1", "turn_count": 3, "started_at": "2026-03-09T00:00:00Z", "last_event": "turn.started"},
				},
				"retrying": []map[string]interface{}{
					{"issue_identifier": "ISS-2", "attempt": 2, "due_at": "2026-03-09T00:10:00Z", "error": "boom"},
				},
				"codex_totals": map[string]int{"total_tokens": 42},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"sessions": map[string]interface{}{
					"ISS-1": map[string]interface{}{"session_id": "sess-1", "last_event": "turn.started", "last_timestamp": "2026-03-09T00:00:00Z"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/events":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"events": []kanban.RuntimeEvent{{Seq: 1, Kind: "run_started", Identifier: "ISS-1"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/app/runtime/series":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"series": []kanban.RuntimeSeriesPoint{{Bucket: "12:00", RunsStarted: 1}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/app/issues/ISS-1/execution":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"identifier":     "ISS-1",
				"active":         true,
				"phase":          "implementation",
				"attempt_number": 3,
				"retry_state":    "scheduled",
				"failure_class":  "approval_required",
				"current_error":  "approval_required",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/app/projects/proj-1/run":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "refresh_requested", "state": "running"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/app/projects/proj-1/stop":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "stopped", "state": "stopped"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/app/issues/ISS-1/retry":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "queued_now"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/app/issues/ISS-1/run-now":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "queued_now"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"status", "--dashboard", "--api-url", server.URL}, want: "MAESTRO STATUS"},
		{args: []string{"sessions", "--api-url", server.URL}, want: "ISS-1"},
		{args: []string{"events", "--api-url", server.URL}, want: "run_started"},
		{args: []string{"runtime-series", "--api-url", server.URL}, want: "12:00"},
		{args: []string{"issue", "execution", "ISS-1", "--api-url", server.URL}, want: "approval_required"},
		{args: []string{"issue", "retry", "ISS-1", "--api-url", server.URL}, want: "queued_now"},
		{args: []string{"issue", "run-now", "ISS-1", "--api-url", server.URL}, want: "queued_now"},
		{args: []string{"project", "start", "proj-1", "--api-url", server.URL}, want: "refresh_requested"},
		{args: []string{"project", "stop", "proj-1", "--api-url", server.URL}, want: "stopped"},
	}
	for _, tc := range tests {
		code, stdout, stderr := runCLI(t, tc.args...)
		if code != 0 {
			t.Fatalf("%v failed: %d stderr=%s", tc.args, code, stderr)
		}
		if !strings.Contains(stdout, tc.want) {
			t.Fatalf("%v missing %q in %q", tc.args, tc.want, stdout)
		}
	}
}
