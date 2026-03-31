package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/testutil/inprocessserver"
)

func TestParentCommandsRequireSubcommands(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{name: "workflow", args: []string{"workflow"}, message: "workflow subcommand is required"},
		{name: "project", args: []string{"project"}, message: "project subcommand is required"},
		{name: "epic", args: []string{"epic"}, message: "epic subcommand is required"},
		{name: "issue", args: []string{"issue"}, message: "issue subcommand is required"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, tc.args...)
			if code == 0 {
				t.Fatalf("expected %s to fail, stdout=%q stderr=%q", tc.name, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.message) {
				t.Fatalf("expected %s error %q, got stdout=%q stderr=%q", tc.name, tc.message, stdout, stderr)
			}
		})
	}
}

func TestCommandModeBranches(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := setupRepo(t)
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	project, err := store.CreateProject("Platform", "", repoPath, filepath.Join(repoPath, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Ship tests", "", 1, []string{"smoke"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Epic Update", "")
	if err != nil {
		t.Fatalf("CreateEpic update target: %v", err)
	}
	blocker, err := store.CreateIssue(project.ID, "", "Blocker", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	issueToDelete, err := store.CreateIssue(project.ID, "", "Delete issue", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue delete target: %v", err)
	}
	issueToDeleteQuiet, err := store.CreateIssue(project.ID, "", "Delete issue quietly", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue quiet delete target: %v", err)
	}
	if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}
	epicJSON, err := store.CreateEpic(project.ID, "Epic JSON", "")
	if err != nil {
		t.Fatalf("CreateEpic json: %v", err)
	}
	epicQuiet, err := store.CreateEpic(project.ID, "Epic Quiet", "")
	if err != nil {
		t.Fatalf("CreateEpic quiet: %v", err)
	}
	deleteRepo := setupRepo(t)
	projectToDelete, err := store.CreateProject("Disposable", "", deleteRepo, filepath.Join(deleteRepo, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("CreateProject delete target: %v", err)
	}
	deleteRepoText := setupRepo(t)
	projectToDeleteText, err := store.CreateProject("Disposable Text", "", deleteRepoText, filepath.Join(deleteRepoText, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("CreateProject delete text target: %v", err)
	}
	assetPath := filepath.Join(t.TempDir(), "coverage.txt")
	if err := os.WriteFile(assetPath, []byte("coverage"), 0o644); err != nil {
		t.Fatalf("WriteFile asset: %v", err)
	}

	server, err := inprocessserver.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/app/issues/"+issue.Identifier+"/execution":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"identifier":     issue.Identifier,
				"active":         false,
				"phase":          "implementation",
				"attempt_number": 4,
				"retry_state":    "scheduled",
				"failure_class":  "workspace_bootstrap",
				"current_error":  "workspace recovery required",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"sessions": map[string]interface{}{
					"thread-1": map[string]interface{}{"status": "running"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/events":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"events": []map[string]interface{}{
					{"kind": "run_started", "seq": 1},
					{"kind": "run_completed", "seq": 2},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/app/runtime/series":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"series": []map[string]interface{}{
					{"bucket": "earlier", "runs_started": 0, "runs_completed": 0, "tokens": 0},
					{"bucket": "current", "runs_started": 1, "runs_completed": 1, "tokens": 42},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/app/issues/"+issue.Identifier+"/retry":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "queued_now"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/app/issues/"+issue.Identifier+"/run-now":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "queued_now"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/app/projects/"+project.ID+"/run":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "refresh_requested", "state": "running"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/app/projects/"+project.ID+"/stop":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "stopped", "state": "stopped"})
		default:
			http.NotFound(w, r)
		}
	}))
	if err != nil {
		t.Fatalf("new in-process server: %v", err)
	}
	defer server.Close()

	code, stdout, stderr := runCLI(t, "--db", dbPath, "status")
	if code != 0 {
		t.Fatalf("status failed: %d stderr=%s", code, stderr)
	}
	for _, want := range []string{"Maestro Status", "Projects:", "Total Issues:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in status output %q", want, stdout)
		}
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "delete", issueToDelete.Identifier, "--json")
	if code != 0 {
		t.Fatalf("issue delete json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"deleted\":true") || !strings.Contains(stdout, issueToDelete.Identifier) {
		t.Fatalf("unexpected issue delete json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "delete", issueToDeleteQuiet.Identifier, "--quiet")
	if code != 0 {
		t.Fatalf("issue delete quiet failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != issueToDeleteQuiet.Identifier {
		t.Fatalf("unexpected quiet issue delete output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "delete", epicJSON.ID, "--json")
	if code != 0 {
		t.Fatalf("epic delete json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"deleted\":true") || !strings.Contains(stdout, epicJSON.ID) {
		t.Fatalf("unexpected epic delete json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "delete", epicQuiet.ID, "--quiet")
	if code != 0 {
		t.Fatalf("epic delete quiet failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != epicQuiet.ID {
		t.Fatalf("unexpected quiet epic delete output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "delete", projectToDelete.ID, "--json")
	if code != 0 {
		t.Fatalf("project delete json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"deleted\":true") || !strings.Contains(stdout, projectToDelete.ID) {
		t.Fatalf("unexpected project delete json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "unblock", issue.Identifier, blocker.Identifier, "--json")
	if code != 0 {
		t.Fatalf("issue unblock json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"blocked_by\":[]") {
		t.Fatalf("unexpected issue unblock json output %q", stdout)
	}
	if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("reset blockers: %v", err)
	}
	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "unblock", issue.Identifier, blocker.Identifier, "--quiet")
	if code != 0 {
		t.Fatalf("issue unblock quiet failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != issue.Identifier {
		t.Fatalf("unexpected quiet issue unblock output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "execution", issue.Identifier, "--api-url", server.URL, "--json")
	if code != 0 {
		t.Fatalf("issue execution json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"identifier\":\""+issue.Identifier+"\"") || !strings.Contains(stdout, "\"failure_class\":\"workspace_bootstrap\"") {
		t.Fatalf("unexpected issue execution json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "retry", issue.Identifier, "--api-url", server.URL, "--quiet")
	if code != 0 {
		t.Fatalf("issue retry quiet failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != issue.Identifier {
		t.Fatalf("unexpected quiet issue retry output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "run-now", issue.Identifier, "--api-url", server.URL, "--quiet")
	if code != 0 {
		t.Fatalf("issue run-now quiet failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != issue.Identifier {
		t.Fatalf("unexpected quiet issue run-now output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "start", project.ID, "--api-url", server.URL, "--quiet")
	if code != 0 {
		t.Fatalf("project start quiet failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != project.ID {
		t.Fatalf("unexpected quiet project start output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "stop", project.ID, "--api-url", server.URL, "--json")
	if code != 0 {
		t.Fatalf("project stop json failed: %d stderr=%s", code, stderr)
	}
	var stopResult map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &stopResult); err != nil {
		t.Fatalf("decode project stop json: %v output=%q", err, stdout)
	}
	if stopResult["state"] != "stopped" || stopResult["status"] != "stopped" {
		t.Fatalf("unexpected project stop json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "move", issue.Identifier, "in_progress", "--quiet")
	if code != 0 {
		t.Fatalf("issue move quiet failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != issue.Identifier {
		t.Fatalf("unexpected quiet issue move output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "repair-tokens", issue.Identifier)
	if code != 0 {
		t.Fatalf("issue repair-tokens text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Recomputed") || !strings.Contains(stdout, issue.Identifier) {
		t.Fatalf("unexpected issue repair-tokens text output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "board")
	if code != 0 {
		t.Fatalf("board text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Board") || !strings.Contains(stdout, issue.Identifier) {
		t.Fatalf("unexpected board text output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "list", "--json")
	if code != 0 {
		t.Fatalf("project list json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"Platform\"") || !strings.Contains(stdout, project.ID) {
		t.Fatalf("unexpected project list json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "show", project.ID, "--json")
	if code != 0 {
		t.Fatalf("project show json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"name\":\"Platform\"") || !strings.Contains(stdout, "\"id\":\""+project.ID+"\"") {
		t.Fatalf("unexpected project show json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "sessions", "--api-url", server.URL, "--json")
	if code != 0 {
		t.Fatalf("sessions json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"thread-1\"") {
		t.Fatalf("unexpected sessions json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "events", "--api-url", server.URL, "--json", "--since", "0", "--limit", "5")
	if code != 0 {
		t.Fatalf("events json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"run_started\"") || !strings.Contains(stdout, "\"run_completed\"") {
		t.Fatalf("unexpected events json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "runtime-series", "--api-url", server.URL, "--json", "--hours", "2")
	if code != 0 {
		t.Fatalf("runtime-series json failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"series\"") {
		t.Fatalf("unexpected runtime-series json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "assets", "add", issue.Identifier, assetPath)
	if code != 0 {
		t.Fatalf("issue assets add text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Attached asset ") || !strings.Contains(stdout, issue.Identifier) {
		t.Fatalf("unexpected issue assets add text output %q", stdout)
	}
	assetID := strings.TrimSpace(strings.Fields(stdout)[2])
	if assetID == "" {
		t.Fatalf("expected asset id in issue assets add output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "assets", "list", issue.Identifier)
	if code != 0 {
		t.Fatalf("issue assets list text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "coverage.txt") {
		t.Fatalf("unexpected issue assets list output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "assets", "remove", issue.Identifier, assetID)
	if code != 0 {
		t.Fatalf("issue assets remove text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Removed asset "+assetID) || !strings.Contains(stdout, issue.Identifier) {
		t.Fatalf("unexpected issue assets remove text output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "comments", "add", issue.Identifier, "--body", "A review comment")
	if code != 0 {
		t.Fatalf("issue comments add text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Added comment ") || !strings.Contains(stdout, issue.Identifier) {
		t.Fatalf("unexpected issue comments add output %q", stdout)
	}
	commentID := strings.TrimSpace(strings.Fields(stdout)[2])
	if commentID == "" {
		t.Fatalf("expected comment id in issue comments add output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "comments", "list", issue.Identifier)
	if code != 0 {
		t.Fatalf("issue comments list text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "A review comment") {
		t.Fatalf("unexpected issue comments list output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "comments", "update", issue.Identifier, commentID, "--body", "Updated review comment")
	if code != 0 {
		t.Fatalf("issue comments update text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Updated comment "+commentID) || !strings.Contains(stdout, issue.Identifier) {
		t.Fatalf("unexpected issue comments update output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "comments", "delete", issue.Identifier, commentID)
	if code != 0 {
		t.Fatalf("issue comments delete text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Deleted comment "+commentID) || !strings.Contains(stdout, issue.Identifier) {
		t.Fatalf("unexpected issue comments delete output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "start", project.ID, "--api-url", server.URL)
	if code != 0 {
		t.Fatalf("project start text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Start status for "+project.ID) || !strings.Contains(stdout, "running") {
		t.Fatalf("unexpected project start text output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "stop", project.ID, "--api-url", server.URL)
	if code != 0 {
		t.Fatalf("project stop text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Stop status for "+project.ID) || !strings.Contains(stdout, "stopped") {
		t.Fatalf("unexpected project stop text output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "update", project.ID, "--repo", repoPath, "--workflow", filepath.Join(repoPath, "WORKFLOW.md"), "--quiet")
	if code != 0 {
		t.Fatalf("project update quiet failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != project.ID {
		t.Fatalf("unexpected quiet project update output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "delete", projectToDeleteText.ID)
	if code != 0 {
		t.Fatalf("project delete text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Deleted project "+projectToDeleteText.ID) {
		t.Fatalf("unexpected project delete text output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "create", "Epic Quiet", "--project", project.ID, "--quiet")
	if code != 0 {
		t.Fatalf("epic create quiet failed: %d stderr=%s", code, stderr)
	}
	quietEpicID := strings.TrimSpace(stdout)
	if quietEpicID == "" {
		t.Fatal("expected quiet epic create id")
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "update", epic.ID, "--name", "Epic Updated", "--project", project.ID)
	if code != 0 {
		t.Fatalf("epic update text failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Updated epic "+epic.ID) {
		t.Fatalf("unexpected epic update text output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "delete", quietEpicID, "--quiet")
	if code != 0 {
		t.Fatalf("epic delete quiet failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != quietEpicID {
		t.Fatalf("unexpected quiet epic delete output %q", stdout)
	}
}

func TestCommandUpdateAndErrorBranches(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := setupRepo(t)
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	project, err := store.CreateProject("Platform", "", repoPath, filepath.Join(repoPath, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Ship coverage", "", 1, []string{"initial"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	otherRepo := setupRepo(t)
	otherProject, err := store.CreateProject("Other", "", otherRepo, filepath.Join(otherRepo, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	otherBlocker, err := store.CreateIssue(otherProject.ID, "", "Other blocker", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue other blocker: %v", err)
	}

	server, err := inprocessserver.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	if err != nil {
		t.Fatalf("new in-process server: %v", err)
	}
	defer server.Close()

	code, stdout, stderr := runCLI(
		t,
		"--db", dbPath,
		"issue", "update", issue.Identifier,
		"--title", "Updated title",
		"--desc", "Updated description",
		"--type", "recurring",
		"--cron", "0 * * * *",
		"--enabled=false",
		"--labels", "alpha,beta",
		"--priority", "5",
		"--permission-profile", "full-access",
		"--project", otherProject.ID,
		"--epic", epic.ID,
		"--branch", "feature/coverage",
		"--agent", "coverage-bot",
		"--agent-prompt", "be concise",
		"--pr-url", "https://example.com/pr/1",
		"--clear-labels",
		"--clear-priority",
		"--clear-epic",
		"--clear-branch",
		"--clear-agent",
		"--clear-agent-prompt",
		"--clear-pr",
		"--json",
	)
	if code != 0 {
		t.Fatalf("issue update json failed: %d stderr=%s", code, stderr)
	}
	var updated map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &updated); err != nil {
		t.Fatalf("decode issue update json: %v output=%q", err, stdout)
	}
	if updated["title"] != "Updated title" || updated["project_id"] != otherProject.ID {
		t.Fatalf("unexpected updated issue payload: %#v", updated)
	}
	if updated["permission_profile"] != "full-access" {
		t.Fatalf("unexpected issue permission profile: %#v", updated)
	}

	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "update", issue.Identifier, "--clear-project")
	if code == 0 || !strings.Contains(stderr, "project_id is required") {
		t.Fatalf("expected clear project update to fail, got code=%d stderr=%s", code, stderr)
	}

	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "update", issue.Identifier)
	if code == 0 || !strings.Contains(stderr, "no updates specified") {
		t.Fatalf("expected empty update to fail, got code=%d stderr=%s", code, stderr)
	}

	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "delete", "ISS-missing")
	if code == 0 || !strings.Contains(stderr, "issue not found") {
		t.Fatalf("expected missing issue delete to fail, got code=%d stderr=%s", code, stderr)
	}

	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "execution", issue.Identifier)
	if code == 0 || !strings.Contains(stderr, "--api-url is required") {
		t.Fatalf("expected execution without api-url to fail, got code=%d stderr=%s", code, stderr)
	}

	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "execution", issue.Identifier, "--api-url", server.URL, "--json")
	if code == 0 || !strings.Contains(stderr, "returned 404") {
		t.Fatalf("expected execution 404 to fail, got code=%d stderr=%s", code, stderr)
	}

	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "retry", issue.Identifier, "--api-url", server.URL, "--quiet")
	if code == 0 || !strings.Contains(stderr, "returned 404") {
		t.Fatalf("expected retry 404 to fail, got code=%d stderr=%s", code, stderr)
	}

	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "run-now", issue.Identifier, "--api-url", server.URL, "--json")
	if code == 0 || !strings.Contains(stderr, "returned 404") {
		t.Fatalf("expected run-now 404 to fail, got code=%d stderr=%s", code, stderr)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "block", issue.Identifier, otherBlocker.Identifier, "--json")
	if code != 0 {
		t.Fatalf("issue block alias failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"blocked_by\"") {
		t.Fatalf("unexpected issue block json output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "blockers", "clear", issue.Identifier, "--json")
	if code != 0 {
		t.Fatalf("issue blockers clear failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"blocked_by\":[]") {
		t.Fatalf("unexpected issue blockers clear json output %q", stdout)
	}

	assetPath := filepath.Join(t.TempDir(), "coverage.txt")
	if err := os.WriteFile(assetPath, []byte("coverage"), 0o644); err != nil {
		t.Fatalf("WriteFile asset: %v", err)
	}
	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "assets", "add", issue.Identifier, assetPath, "--json")
	if code != 0 {
		t.Fatalf("issue assets add failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\"filename\":\"coverage.txt\"") {
		t.Fatalf("unexpected issue assets add json output %q", stdout)
	}
}
