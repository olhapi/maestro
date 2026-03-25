package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/codexschema"
	"github.com/olhapi/maestro/internal/kanban"
)

func TestRunCommandDefaultsPortTo8787(t *testing.T) {
	cmd := newRootCmd(io.Discard, io.Discard)
	runCmd, _, err := cmd.Find([]string{"run"})
	if err != nil {
		t.Fatalf("find run command: %v", err)
	}
	got := runCmd.Flags().Lookup("port").DefValue
	if got != defaultHTTPPort {
		t.Fatalf("run --port default = %q, want %q", got, defaultHTTPPort)
	}
}

func TestShellQuoteArg(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "maestro", want: "maestro"},
		{name: "empty", in: "", want: shellQuoteArg("")},
		{name: "spaces", in: "My Project", want: shellQuoteArg("My Project")},
		{name: "apostrophe", in: "repo's path", want: shellQuoteArg("repo's path")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellQuoteArg(tc.in); got != tc.want {
				t.Fatalf("shellQuoteArg(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func writeFakeCodexCLI(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf 'codex-cli " + version + "\\n'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return path
}

func repoRootFromCaller(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestTextModeCRUDCommandsAndWorkflowInit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro db", "maestro.db")
	repoPath := setupRepo(t)
	opsRepoPath := setupRepo(t)
	codexPath := writeFakeCodexCLI(t, codexschema.SupportedVersion)

	initRepo := filepath.Join(t.TempDir(), "workflow init repo")
	code, stdout, stderr := runCLI(t, "--db", dbPath, "workflow", "init", initRepo, "--defaults", "--codex-command", codexPath+" app-server")
	if code != 0 {
		t.Fatalf("workflow init failed: %d stderr=%s", code, stderr)
	}
	workflowPath := filepath.Join(initRepo, "WORKFLOW.md")
	if _, err := os.Stat(workflowPath); err != nil {
		t.Fatalf("expected workflow file: %v", err)
	}
	workflowData, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read generated workflow: %v", err)
	}
	workflowText := string(workflowData)
	if !strings.Contains(workflowText, "phases:") || !strings.Contains(workflowText, "enabled: true") {
		t.Fatalf("expected generated workflow to enable default review/done phases, got %q", workflowText)
	}
	for _, want := range []string{
		"Initialized " + workflowPath,
		"Verification",
		"codex_version: ok",
		"Next steps",
		"Register the repo:",
		"Start the orchestrator:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected workflow init output to contain %q, got %q", want, stdout)
		}
	}
	if !strings.Contains(stdout, buildMaestroCommand(dbPath, "project", "create", "My Project", "--repo", initRepo)) {
		t.Fatalf("expected quoted next-step command in output %q", stdout)
	}
	if !strings.Contains(stdout, buildMaestroCommand(dbPath, "run", initRepo)) {
		t.Fatalf("expected quoted run command in output %q", stdout)
	}
	if !strings.Contains(stdout, buildMaestroCommand(dbPath, "verify", "--repo", initRepo)) {
		t.Fatalf("expected quoted verify command in output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "create", "Platform", "--repo", repoPath)
	if code != 0 {
		t.Fatalf("project create failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Created project ") {
		t.Fatalf("unexpected project create output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "create", "Ops", "--repo", opsRepoPath, "--quiet")
	if code != 0 {
		t.Fatalf("project quiet create failed: %d stderr=%s", code, stderr)
	}
	opsID := strings.TrimSpace(stdout)
	if opsID == "" {
		t.Fatal("expected quiet project id")
	}

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	var platform kanban.Project
	for _, project := range projects {
		if project.Name == "Platform" {
			platform = project
			break
		}
	}
	if platform.ID == "" {
		t.Fatal("expected Platform project to exist")
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "create", "CLI", "--project", platform.ID)
	if code != 0 {
		t.Fatalf("epic create failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Created epic ") {
		t.Fatalf("unexpected epic create output %q", stdout)
	}

	epics, err := store.ListEpics("")
	if err != nil {
		t.Fatalf("ListEpics failed: %v", err)
	}
	if len(epics) != 1 {
		t.Fatalf("expected 1 epic, got %d", len(epics))
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "create", "Ship text coverage", "--project", platform.ID, "--epic", epics[0].ID, "--labels", "cli,coverage")
	if code != 0 {
		t.Fatalf("issue create failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Created issue ") {
		t.Fatalf("unexpected issue create output %q", stdout)
	}

	issues, err := store.ListIssues(nil)
	if err != nil {
		t.Fatalf("ListIssues failed: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	issue := issues[0]

	assetPath := filepath.Join(t.TempDir(), "issue-image.png")
	if err := os.WriteFile(assetPath, sampleMainPNGBytes(), 0o644); err != nil {
		t.Fatalf("write issue image fixture: %v", err)
	}
	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "images", "add", issue.Identifier, assetPath, "--quiet")
	if code != 0 {
		t.Fatalf("issue images add failed: %d stderr=%s", code, stderr)
	}
	assetID := strings.TrimSpace(stdout)
	if assetID == "" {
		t.Fatal("expected quiet issue images add output")
	}
	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "images", "list", issue.Identifier, "--quiet")
	if code != 0 {
		t.Fatalf("issue images list failed: %d stderr=%s", code, stderr)
	}
	if got := strings.TrimSpace(stdout); got != assetID {
		t.Fatalf("unexpected issue images list output %q, want %q", got, assetID)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "list")
	if code != 0 {
		t.Fatalf("project list failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Platform") || !strings.Contains(stdout, "Ops") {
		t.Fatalf("unexpected project list output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "show", platform.ID)
	if code != 0 {
		t.Fatalf("project show failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Project:\tPlatform") || !strings.Contains(stdout, "State:\tstopped") || !strings.Contains(stdout, issue.Identifier) {
		t.Fatalf("unexpected project show output %q", stdout)
	}

	updatedWorkflow := filepath.Join(repoPath, "ALT_WORKFLOW.md")
	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "update", platform.ID, "--name", "Platform Core", "--desc", "Updated project", "--repo", repoPath, "--workflow", updatedWorkflow)
	if code != 0 {
		t.Fatalf("project update failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Updated project "+platform.ID) {
		t.Fatalf("unexpected project update output %q", stdout)
	}
	updatedProject, err := store.GetProject(platform.ID)
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if updatedProject.Name != "Platform Core" || updatedProject.WorkflowPath != updatedWorkflow {
		t.Fatalf("unexpected updated project: %+v", updatedProject)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "list", "--project", platform.ID)
	if code != 0 {
		t.Fatalf("epic list failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "CLI") {
		t.Fatalf("unexpected epic list output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "show", epics[0].ID)
	if code != 0 {
		t.Fatalf("epic show failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Epic:\tCLI") || !strings.Contains(stdout, issue.Identifier) {
		t.Fatalf("unexpected epic show output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "update", epics[0].ID, "--name", "CLI Delivery", "--desc", "Updated epic", "--project", platform.ID, "--quiet")
	if code != 0 {
		t.Fatalf("epic update failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != epics[0].ID {
		t.Fatalf("unexpected epic quiet output %q", stdout)
	}
	updatedEpic, err := store.GetEpic(epics[0].ID)
	if err != nil {
		t.Fatalf("GetEpic failed: %v", err)
	}
	if updatedEpic.Name != "CLI Delivery" || updatedEpic.Description != "Updated epic" {
		t.Fatalf("unexpected updated epic: %+v", updatedEpic)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "show", issue.Identifier)
	if code != 0 {
		t.Fatalf("issue show failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Identifier:\t"+issue.Identifier) || !strings.Contains(stdout, "Title:\t"+issue.Title) {
		t.Fatalf("unexpected issue show output %q", stdout)
	}

	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "comments", "add", issue.Identifier, "--body", "CLI note")
	if code != 0 {
		t.Fatalf("issue comment add failed: %d stderr=%s", code, stderr)
	}
	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "show", issue.Identifier)
	if code != 0 {
		t.Fatalf("issue show with comments failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Comments:\t1") || !strings.Contains(stdout, "CLI note") {
		t.Fatalf("expected comment metadata in issue show output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "move", issue.Identifier, "done", "--quiet")
	if code != 0 {
		t.Fatalf("issue move failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != issue.Identifier {
		t.Fatalf("unexpected issue move quiet output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "update", issue.Identifier, "--clear-labels", "--clear-priority", "--clear-epic", "--clear-branch", "--clear-pr")
	if code != 0 {
		t.Fatalf("issue clear update failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Updated issue "+issue.Identifier) {
		t.Fatalf("unexpected issue update output %q", stdout)
	}
	detail, err := store.GetIssueDetailByIdentifier(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier failed: %v", err)
	}
	if detail.ProjectID != issue.ProjectID || detail.EpicID != "" || detail.Priority != 0 || len(detail.Labels) != 0 || detail.BranchName != "" || detail.PRURL != "" {
		t.Fatalf("expected cleared issue fields, got %+v", detail)
	}

	code, _, stderr = runCLI(t, "--db", dbPath, "issue", "update", issue.Identifier, "--clear-project")
	if code == 0 {
		t.Fatal("expected clear-project to be rejected")
	}
	if !strings.Contains(stderr, "project_id is required") {
		t.Fatalf("unexpected clear-project error %q", stderr)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "issue", "delete", issue.Identifier)
	if code != 0 {
		t.Fatalf("issue delete failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Deleted issue "+issue.Identifier) {
		t.Fatalf("unexpected issue delete output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "epic", "delete", epics[0].ID)
	if code != 0 {
		t.Fatalf("epic delete failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Deleted epic "+epics[0].ID) {
		t.Fatalf("unexpected epic delete output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "project", "delete", opsID, "--quiet")
	if code != 0 {
		t.Fatalf("project delete failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != opsID {
		t.Fatalf("unexpected quiet project delete output %q", stdout)
	}
}

func TestWorkflowInitHelpIncludesSetupFlags(t *testing.T) {
	code, stdout, stderr := runCLI(t, "workflow", "init", "--help")
	if code != 0 {
		t.Fatalf("workflow init help failed: %d stderr=%s", code, stderr)
	}
	for _, want := range []string{
		"--workspace-root",
		"--codex-command",
		"--agent-mode",
		"--force",
		"--defaults",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected workflow init help to contain %q, got %q", want, stdout)
		}
	}
}

func TestWorkflowInitRequiresForceToOverwriteExistingFile(t *testing.T) {
	repoPath := setupRepo(t)
	code, _, stderr := runCLI(t, "workflow", "init", repoPath, "--defaults")
	if code != exitCodeUsage {
		t.Fatalf("expected usage error, got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "--force") {
		t.Fatalf("expected overwrite guidance, got %q", stderr)
	}
}

func TestWorkflowInitReturnsSuccessWhenVerificationWarns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := t.TempDir()
	missingCodex := filepath.Join(t.TempDir(), "codex")

	code, stdout, stderr := runCLI(t, "--db", dbPath, "workflow", "init", repoPath, "--defaults", "--codex-command", missingCodex+" app-server")
	if code != 0 {
		t.Fatalf("workflow init should succeed with warnings: %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"Verification",
		"Warnings:",
		"codex_version:",
		"Next steps",
		"Review the warnings and remediation above",
		"Re-run readiness checks:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected warning workflow init output to contain %q, got %q", want, stdout)
		}
	}
	if strings.Contains(stdout, "Register the repo:") {
		t.Fatalf("expected advisory next steps without project registration, got %q", stdout)
	}
}

func TestWorkflowInitOmitsSandboxFields(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	repoPath := t.TempDir()
	codexPath := writeFakeCodexCLI(t, codexschema.SupportedVersion)

	code, stdout, stderr := runCLI(t, "--db", dbPath, "workflow", "init", repoPath, "--defaults", "--codex-command", codexPath+" app-server")
	if code != 0 {
		t.Fatalf("workflow init failed: %d stderr=%s stdout=%s", code, stderr, stdout)
	}

	data, err := os.ReadFile(filepath.Join(repoPath, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	text := string(data)
	for _, unwanted := range []string{
		"thread_sandbox:",
		"turn_sandbox_policy:",
	} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("expected workflow to omit %q, got %q", unwanted, text)
		}
	}
}

func TestStatusSpecCheckAndBlockAliasTextModes(t *testing.T) {
	dbPath, _, project, issue := setupProjectAndIssue(t)
	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()
	blocker, err := store.CreateIssue(project.ID, "", "Blocked by", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker failed: %v", err)
	}

	code, stdout, stderr := runCLI(t, "--db", dbPath, "issue", "block", issue.Identifier, blocker.Identifier, "--quiet")
	if code != 0 {
		t.Fatalf("issue block alias failed: %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != issue.Identifier {
		t.Fatalf("unexpected issue block quiet output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "--db", dbPath, "status")
	if code != 0 {
		t.Fatalf("status failed: %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Maestro Status") || !strings.Contains(stdout, "Projects: 1") {
		t.Fatalf("unexpected status output %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "spec-check", "--repo", repoRootFromCaller(t))
	if code != 0 {
		t.Fatalf("expected passing spec-check exit code, got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Spec Check") || !strings.Contains(stdout, "workflow_prompt_render: ok") {
		t.Fatalf("unexpected spec-check output %q", stdout)
	}
}
