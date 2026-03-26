package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
	"github.com/olhapi/maestro/pkg/config"
)

func runGitForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output))
}

func gitTestEnv() []string {
	env := os.Environ()
	filtered := env[:0]
	for _, value := range env {
		if !strings.HasPrefix(value, "GIT_") {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func initGitRepoForTest(t *testing.T, repoPath string) {
	t.Helper()
	runGitForTest(t, repoPath, "init")
	runGitForTest(t, repoPath, "config", "user.email", "maestro-tests@example.com")
	runGitForTest(t, repoPath, "config", "user.name", "Maestro Tests")
	runGitForTest(t, repoPath, "add", ".")
	runGitForTest(t, repoPath, "commit", "-m", "test init")
	runGitForTest(t, repoPath, "branch", "-M", "main")
}

func initBareGitRepoForTest(t *testing.T, repoPath string) {
	t.Helper()
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("MkdirAll bare repo: %v", err)
	}
	runGitForTest(t, repoPath, "init", "--bare")
}

func cloneGitRepoForTest(t *testing.T, source, target string) {
	t.Helper()
	cmd := exec.Command("git", "clone", "--branch", "main", "--single-branch", source, target)
	cmd.Env = gitTestEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone %s %s failed: %v\n%s", source, target, err, strings.TrimSpace(string(output)))
	}
}

func advanceRemoteMainForTest(t *testing.T, remotePath string) string {
	t.Helper()
	advancePath := filepath.Join(t.TempDir(), "advance")
	cloneGitRepoForTest(t, remotePath, advancePath)
	runGitForTest(t, advancePath, "config", "user.email", "maestro-tests@example.com")
	runGitForTest(t, advancePath, "config", "user.name", "Maestro Tests")
	if err := os.WriteFile(filepath.Join(advancePath, "README.md"), []byte("repo\nremote\n"), 0o644); err != nil {
		t.Fatalf("WriteFile remote README: %v", err)
	}
	runGitForTest(t, advancePath, "add", "README.md")
	runGitForTest(t, advancePath, "commit", "-m", "advance remote main")
	runGitForTest(t, advancePath, "push", "origin", "main")
	return runGitForTest(t, advancePath, "rev-parse", "HEAD")
}

func advanceRemoteDevelopForTest(t *testing.T, remotePath string) string {
	t.Helper()
	advancePath := filepath.Join(t.TempDir(), "advance")
	cloneGitRepoForTest(t, remotePath, advancePath)
	runGitForTest(t, advancePath, "config", "user.email", "maestro-tests@example.com")
	runGitForTest(t, advancePath, "config", "user.name", "Maestro Tests")
	runGitForTest(t, advancePath, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(advancePath, "README.md"), []byte("repo\nremote\ndevelop\n"), 0o644); err != nil {
		t.Fatalf("WriteFile remote README: %v", err)
	}
	runGitForTest(t, advancePath, "add", "README.md")
	runGitForTest(t, advancePath, "commit", "-m", "switch remote default")
	runGitForTest(t, advancePath, "push", "origin", "develop")
	runGitForTest(t, remotePath, "symbolic-ref", "HEAD", "refs/heads/develop")
	return runGitForTest(t, advancePath, "rev-parse", "HEAD")
}

func setupTestRunner(t *testing.T, command string, mode string) (*Runner, *kanban.Store, *config.Manager, string, string) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	workspaceRoot := filepath.Join(tmpDir, "workspaces")

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	workflowContent := `---
tracker:
  kind: kanban
polling:
  interval_ms: 1000
workspace:
  root: ` + workspaceRoot + `
hooks:
  timeout_ms: 1000
phases:
  review:
    enabled: false
  done:
    enabled: false
agent:
  max_concurrent_agents: 2
  max_turns: 3
  max_retry_backoff_ms: 10000
  mode: ` + mode + `
codex:
  command: ` + command + `
  approval_policy: never
  read_timeout_ms: 1000
  turn_timeout_ms: 10000
---
Issue {{ issue.identifier }} {{ issue.title }}{% if attempt %} retry {{ attempt }}{% endif %}
`
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0o644); err != nil {
		t.Fatalf("Failed to write workflow: %v", err)
	}
	initGitRepoForTest(t, tmpDir)

	manager, err := config.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}
	runner := NewRunner(manager, store)

	t.Cleanup(func() {
		_ = store.Close()
	})

	return runner, store, manager, workspaceRoot, tmpDir
}

func workspaceGitDirForTest(t *testing.T, workspacePath string) string {
	t.Helper()
	gitDir := runGitForTest(t, workspacePath, "rev-parse", "--git-dir")
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(workspacePath, gitDir)
	}
	return filepath.Clean(gitDir)
}

func sampleRunnerPNGBytes() []byte {
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

func defaultPromptWorkflowForTest() *config.Workflow {
	cfg := config.DefaultConfig()
	cfg.Phases.Review = config.PhasePromptConfig{
		Enabled: true,
		Prompt:  config.DefaultReviewPromptTemplate(),
	}
	cfg.Phases.Done = config.PhasePromptConfig{
		Enabled: true,
		Prompt:  config.DefaultDonePromptTemplate(),
	}
	return &config.Workflow{
		Config:         cfg,
		PromptTemplate: config.DefaultPromptTemplate(),
	}
}

func baseRunnerAppServerScenario(threadID, turnID string, afterTurnStart ...fakeappserver.Output) fakeappserver.Scenario {
	return fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": threadID}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: append([]fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": turnID}}},
				}}, afterTurnStart...),
			},
		},
	}
}

func TestFakeAppServerHelperProcess(t *testing.T) {
	fakeappserver.MaybeRun()
}

func TestGetOrCreateWorkspace(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Test Issue", "", 0, nil)

	workflow, err := runner.workflowProvider.Current()
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	expectedPath := filepath.Join(workspaceRoot, issue.Identifier)
	if workspace.Path != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, workspace.Path)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, ".git")); err != nil {
		t.Fatalf("expected git worktree metadata in workspace: %v", err)
	}
	if got := runGitForTest(t, workspace.Path, "branch", "--show-current"); got != "codex/"+issue.Identifier {
		t.Fatalf("expected workspace branch codex/%s, got %q", issue.Identifier, got)
	}
	reloaded, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if reloaded.BranchName != "codex/"+issue.Identifier {
		t.Fatalf("expected branch name to persist on issue, got %q", reloaded.BranchName)
	}
}

func TestBuildTurnPrompt(t *testing.T) {
	runner, store, manager, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Fix Login Bug", "Users cannot log in", 1, []string{"bug", "urgent"})
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"agent_name":   "marketing",
		"agent_prompt": "Review the copy before changing the landing page.",
	}); err != nil {
		t.Fatalf("UpdateIssue agent metadata: %v", err)
	}
	issue, _ = store.GetIssue(issue.ID)
	workflow, _ := manager.Current()

	prompt, err := runner.buildTurnPrompt(workflow, issue, 2, 1)
	if err != nil {
		t.Fatalf("Failed to build prompt: %v", err)
	}
	for _, part := range []string{issue.Identifier, "Fix Login Bug", "retry 2"} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("expected prompt to contain %q, got %q", part, prompt)
		}
	}
	for _, part := range []string{"Assigned agent: marketing", "Review the copy before changing the landing page."} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("expected prompt to contain %q, got %q", part, prompt)
		}
	}
	if !strings.Contains(prompt, "Prefer deterministic local verification first") {
		t.Fatalf("expected execution guidance in prompt, got %q", prompt)
	}
}

func TestBuildTurnPromptIncludesProjectContextInDefaultImplementationPrompt(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	workflow := defaultPromptWorkflowForTest()

	project, err := store.CreateProject("Platform", "Use pnpm in this repo and run focused validation for touched packages.", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Fix Login Bug", "Users cannot log in", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	prompt, err := runner.buildTurnPrompt(workflow, issue, 0, 1)
	if err != nil {
		t.Fatalf("buildTurnPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Project context:\nUse pnpm in this repo and run focused validation for touched packages.") {
		t.Fatalf("expected project context in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Description:") || !strings.Contains(prompt, "Users cannot log in") {
		t.Fatalf("expected issue description in prompt, got %q", prompt)
	}
}

func TestPermissionConfigForIssueUsesFullAccessForIssueOverride(t *testing.T) {
	runner, store, manager, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, err := store.CreateProject("Platform", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Sandbox override", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssuePermissionProfile(issue.ID, kanban.PermissionProfileFullAccess); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	workflow, err := manager.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	permissions := runner.permissionConfigForIssue(issue, workflow.Config.Codex.ApprovalPolicy, workflow.Config.Codex.InitialCollaborationMode)
	if permissions.ThreadSandbox != "danger-full-access" {
		t.Fatalf("expected danger-full-access thread sandbox, got %q", permissions.ThreadSandbox)
	}
	if permissions.TurnSandboxPolicy["type"] != "dangerFullAccess" {
		t.Fatalf("expected dangerFullAccess turn policy, got %#v", permissions.TurnSandboxPolicy)
	}
	if workflow.Config.Codex.Command == "" {
		t.Fatal("expected workflow to remain available")
	}
}

func TestPermissionConfigForIssueUsesPlanThenFullAccessForIssueOverride(t *testing.T) {
	runner, store, manager, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, err := store.CreateProject("Platform", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Plan-first override", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssuePermissionProfile(issue.ID, kanban.PermissionProfilePlanThenFullAccess); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	workflow, err := manager.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	permissions := runner.permissionConfigForIssue(issue, workflow.Config.Codex.ApprovalPolicy, workflow.Config.Codex.InitialCollaborationMode)
	if !reflect.DeepEqual(permissions.ApprovalPolicy, workflow.Config.Codex.ApprovalPolicy) {
		t.Fatalf("expected inherited approval policy, got %#v", permissions.ApprovalPolicy)
	}
	if permissions.ThreadSandbox != "workspace-write" {
		t.Fatalf("expected workspace-write thread sandbox, got %q", permissions.ThreadSandbox)
	}
	if permissions.TurnSandboxPolicy != nil {
		t.Fatalf("expected nil turn sandbox policy, got %#v", permissions.TurnSandboxPolicy)
	}
	if permissions.InitialCollaborationMode != config.InitialCollaborationModePlan {
		t.Fatalf("expected plan collaboration mode, got %q", permissions.InitialCollaborationMode)
	}
}

func TestRunAttemptCancelsAfterCreateHookWithRunContext(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, err := store.CreateProject("Platform", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Bootstrap workspace", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	workflowContent, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	updated := strings.Replace(string(workflowContent), "hooks:\n  timeout_ms: 1000", "hooks:\n  timeout_ms: 5000\n  after_create: sleep 5", 1)
	if err := os.WriteFile(workflowPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err = runner.RunAttempt(ctx, issue, 0)
	if err == nil {
		t.Fatal("expected canceled run to fail")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("expected canceled after_create hook to stop promptly, took %s", elapsed)
	}
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("expected cancellation rather than timeout, got %v", err)
	}
}

func TestPermissionConfigForIssueFallsBackToProjectProfile(t *testing.T) {
	runner, store, manager, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, err := store.CreateProject("Platform", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := store.UpdateProjectPermissionProfile(project.ID, kanban.PermissionProfileFullAccess); err != nil {
		t.Fatalf("UpdateProjectPermissionProfile: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Inherited sandbox override", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssuePermissionProfile(issue.ID, kanban.PermissionProfileDefault); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	workflow, err := manager.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	permissions := runner.permissionConfigForIssue(issue, workflow.Config.Codex.ApprovalPolicy, workflow.Config.Codex.InitialCollaborationMode)
	if permissions.ThreadSandbox != "danger-full-access" {
		t.Fatalf("expected inherited danger-full-access thread sandbox, got %q", permissions.ThreadSandbox)
	}
}

func TestPermissionConfigForIssueDefaultsToSafeBaseline(t *testing.T) {
	runner, store, manager, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, err := store.CreateProject("Platform", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Safe default", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	workflow, err := manager.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	permissions := runner.permissionConfigForIssue(issue, workflow.Config.Codex.ApprovalPolicy, workflow.Config.Codex.InitialCollaborationMode)
	if permissions.ThreadSandbox != "workspace-write" {
		t.Fatalf("expected workspace-write thread sandbox, got %q", permissions.ThreadSandbox)
	}
	if permissions.TurnSandboxPolicy != nil {
		t.Fatalf("expected nil turn sandbox policy for safe baseline, got %#v", permissions.TurnSandboxPolicy)
	}
}

func TestBuildTurnPromptUsesPlanningGuidanceWhenPlanModeEnabled(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	workflow := defaultPromptWorkflowForTest()
	workflow.Config.Codex.InitialCollaborationMode = config.InitialCollaborationModePlan

	issue, err := store.CreateIssue("", "", "Plan the change", "Need clarification", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	firstPrompt, err := runner.buildTurnPrompt(workflow, issue, 0, 1)
	if err != nil {
		t.Fatalf("buildTurnPrompt first turn: %v", err)
	}
	if !strings.Contains(firstPrompt, "Planning guidance:") {
		t.Fatalf("expected planning guidance in first plan-mode prompt, got %q", firstPrompt)
	}
	if !strings.Contains(firstPrompt, "<proposed_plan>") {
		t.Fatalf("expected proposed plan guidance in first plan-mode prompt, got %q", firstPrompt)
	}
	if strings.Contains(firstPrompt, "Execution guidance:") {
		t.Fatalf("did not expect execution guidance in first plan-mode prompt, got %q", firstPrompt)
	}

	continuationPrompt, err := runner.buildTurnPrompt(workflow, issue, 0, 2)
	if err != nil {
		t.Fatalf("buildTurnPrompt continuation: %v", err)
	}
	if !strings.Contains(continuationPrompt, "planning phase") {
		t.Fatalf("expected planning continuation guidance, got %q", continuationPrompt)
	}
	if !strings.Contains(continuationPrompt, "<proposed_plan>") {
		t.Fatalf("expected proposed plan reminder in continuation prompt, got %q", continuationPrompt)
	}
}

func TestCapturePendingPlanApprovalPersistsPlanRequestForPlanMode(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, err := store.CreateProject("Platform", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Capture proposed plan", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	session := &appserver.Session{
		History: []appserver.Event{{
			Type:      "item.completed",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Message:   "<proposed_plan>\nShip the thing safely.\n</proposed_plan>",
		}},
	}

	requested, err := runner.capturePendingPlanApproval(issue, 3, session, true)
	if err != nil {
		t.Fatalf("capturePendingPlanApproval: %v", err)
	}
	if !requested {
		t.Fatal("expected pending plan approval to be requested")
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after capture: %v", err)
	}
	if !updated.PlanApprovalPending {
		t.Fatalf("expected pending plan approval, got %+v", updated)
	}
	if updated.PendingPlanMarkdown != "Ship the thing safely." {
		t.Fatalf("unexpected pending plan markdown %q", updated.PendingPlanMarkdown)
	}
	if updated.PendingPlanRequestedAt == nil {
		t.Fatal("expected pending plan requested timestamp")
	}

	events, err := store.ListRuntimeEvents(0, 20)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected runtime events")
	}
	latest := events[0]
	if latest.Kind != "plan_approval_requested" || latest.Attempt != 3 {
		t.Fatalf("unexpected runtime event: %+v", latest)
	}
}
func TestBuildTurnPromptIncludesProjectContextInDefaultPhasePrompts(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	workflow := defaultPromptWorkflowForTest()

	project, err := store.CreateProject("Platform", "Always preserve the public API unless the issue explicitly allows a breaking change.", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Ship the change", "Issue details", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	cases := []struct {
		name  string
		phase kanban.WorkflowPhase
		want  string
	}{
		{name: "review", phase: kanban.WorkflowPhaseReview, want: "You are performing the review pass"},
		{name: "done", phase: kanban.WorkflowPhaseDone, want: "You are performing the done pass"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issueCopy := *issue
			issueCopy.WorkflowPhase = tc.phase

			prompt, err := runner.buildTurnPrompt(workflow, &issueCopy, 0, 1)
			if err != nil {
				t.Fatalf("buildTurnPrompt: %v", err)
			}
			if !strings.Contains(prompt, tc.want) {
				t.Fatalf("expected phase heading %q, got %q", tc.want, prompt)
			}
			if !strings.Contains(prompt, "Project context:\nAlways preserve the public API unless the issue explicitly allows a breaking change.") {
				t.Fatalf("expected project context in %s prompt, got %q", tc.phase, prompt)
			}
		})
	}
}

func TestBuildTurnPromptRendersProjectVariablesInCustomWorkflow(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	workflow := &config.Workflow{
		Config:         config.DefaultConfig(),
		PromptTemplate: "Project={{ project.name }}|{{ project.description }}\nIssue={{ issue.identifier }}",
	}

	project, err := store.CreateProject("Platform", "Ship focused changes.", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issueWithProject, err := store.CreateIssue(project.ID, "", "Custom prompt", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue with project: %v", err)
	}
	issueWithMissingProject, err := store.CreateIssue("", "", "Missing project", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue missing project: %v", err)
	}
	issueWithMissingProject.ProjectID = "proj-missing"

	cases := []struct {
		name  string
		issue *kanban.Issue
		want  string
	}{
		{name: "present", issue: issueWithProject, want: "Project=Platform|Ship focused changes."},
		{name: "missing", issue: issueWithMissingProject, want: "Project=|"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompt, err := runner.buildTurnPrompt(workflow, tc.issue, 0, 1)
			if err != nil {
				t.Fatalf("buildTurnPrompt: %v", err)
			}
			if !strings.Contains(prompt, tc.want) {
				t.Fatalf("expected prompt to contain %q, got %q", tc.want, prompt)
			}
		})
	}
}

func TestShouldContinueRunPhaseOnlyForSamePhaseWork(t *testing.T) {
	workflow := defaultPromptWorkflowForTest()

	cases := []struct {
		name     string
		runPhase kanban.WorkflowPhase
		state    kanban.State
		workflow func(*config.Workflow)
		want     bool
	}{
		{
			name:     "implementation continues while work stays active",
			runPhase: kanban.WorkflowPhaseImplementation,
			state:    kanban.StateInProgress,
			want:     true,
		},
		{
			name:     "implementation stops after in review transition",
			runPhase: kanban.WorkflowPhaseImplementation,
			state:    kanban.StateInReview,
			want:     false,
		},
		{
			name:     "implementation continues for configured custom active state",
			runPhase: kanban.WorkflowPhaseImplementation,
			state:    kanban.State("qa"),
			workflow: func(workflow *config.Workflow) {
				workflow.Config.Tracker.ActiveStates = append(workflow.Config.Tracker.ActiveStates, "qa")
			},
			want: true,
		},
		{
			name:     "review continues while review phase stays active",
			runPhase: kanban.WorkflowPhaseReview,
			state:    kanban.StateInReview,
			want:     true,
		},
		{
			name:     "review stops when issue reopens for implementation",
			runPhase: kanban.WorkflowPhaseReview,
			state:    kanban.StateInProgress,
			want:     false,
		},
		{
			name:     "done stops after a successful finalization turn",
			runPhase: kanban.WorkflowPhaseDone,
			state:    kanban.StateDone,
			want:     false,
		},
		{
			name:     "done stops when issue reopens",
			runPhase: kanban.WorkflowPhaseDone,
			state:    kanban.StateInReview,
			want:     false,
		},
		{
			name:     "review does not continue when review phase is disabled",
			runPhase: kanban.WorkflowPhaseReview,
			state:    kanban.StateInReview,
			workflow: func(workflow *config.Workflow) { workflow.Config.Phases.Review.Enabled = false },
			want:     false,
		},
		{
			name:     "done does not continue when done phase is disabled",
			runPhase: kanban.WorkflowPhaseDone,
			state:    kanban.StateDone,
			workflow: func(workflow *config.Workflow) { workflow.Config.Phases.Done.Enabled = false },
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := *workflow
			current.Config = workflow.Config
			if tc.workflow != nil {
				tc.workflow(&current)
			}
			issue := &kanban.Issue{State: tc.state}
			if got := shouldContinueRunPhase(&current, tc.runPhase, issue); got != tc.want {
				t.Fatalf("shouldContinueRunPhase(%s, %s) = %v, want %v", tc.runPhase, tc.state, got, tc.want)
			}
		})
	}
}

func TestBuildTurnPromptOmitsProjectContextWhenUnavailable(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	workflow := defaultPromptWorkflowForTest()

	issueWithoutProject, err := store.CreateIssue("", "", "No project", "Issue details", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue without project: %v", err)
	}
	projectWithoutDescription, err := store.CreateProject("Platform", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject empty description: %v", err)
	}
	issueWithoutProjectDescription, err := store.CreateIssue(projectWithoutDescription.ID, "", "Empty project description", "Issue details", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue empty description: %v", err)
	}
	issueWithMissingProject, err := store.CreateIssue("", "", "Missing project", "Issue details", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue missing project: %v", err)
	}
	issueWithMissingProject.ProjectID = "proj-missing"

	cases := []struct {
		name  string
		issue *kanban.Issue
	}{
		{name: "no_project", issue: issueWithoutProject},
		{name: "empty_description", issue: issueWithoutProjectDescription},
		{name: "missing_project", issue: issueWithMissingProject},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompt, err := runner.buildTurnPrompt(workflow, tc.issue, 0, 1)
			if err != nil {
				t.Fatalf("buildTurnPrompt: %v", err)
			}
			if strings.Contains(prompt, "Project context:") {
				t.Fatalf("expected prompt to omit project context, got %q", prompt)
			}
		})
	}
}

func TestBuildTurnPromptUsesPhasePrompt(t *testing.T) {
	runner, store, manager, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	workflowContent := `---
tracker:
  kind: kanban
workspace:
  root: ./workspaces
hooks:
  timeout_ms: 1000
agent:
  max_concurrent_agents: 2
  max_turns: 3
  max_retry_backoff_ms: 10000
  mode: stdio
codex:
  command: cat
  approval_policy: never
  read_timeout_ms: 1000
  turn_timeout_ms: 10000
phases:
  review:
    enabled: true
    prompt: |
      Review {{ issue.identifier }} in {{ phase }}
  done:
    enabled: true
    prompt: |
      Finalize {{ issue.identifier }} in {{ phase }}
---
Implement {{ issue.identifier }} in {{ phase }}
`
	if err := os.WriteFile(manager.Path(), []byte(workflowContent), 0o644); err != nil {
		t.Fatal(err)
	}
	workflow, err := manager.Refresh()
	if err != nil {
		t.Fatal(err)
	}

	issue, _ := store.CreateIssue("", "", "Phased", "", 0, nil)
	issue.WorkflowPhase = kanban.WorkflowPhaseReview
	prompt, err := runner.buildTurnPrompt(workflow, issue, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Review "+issue.Identifier+" in review") {
		t.Fatalf("expected review prompt, got %q", prompt)
	}

	issue.WorkflowPhase = kanban.WorkflowPhaseDone
	prompt, err = runner.buildTurnPrompt(workflow, issue, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Finalize "+issue.Identifier+" in done") {
		t.Fatalf("expected done prompt, got %q", prompt)
	}
}

func TestBuildTurnPromptIncludesPendingAgentCommands(t *testing.T) {
	runner, store, manager, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Follow-up", "", 0, nil)
	workflow, _ := manager.Current()
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatal(err)
	}
	issue, _ = store.GetIssue(issue.ID)

	if _, err := store.CreateIssueAgentCommand(issue.ID, "First follow-up", kanban.IssueAgentCommandPending); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIssueAgentCommand(issue.ID, "Second follow-up", kanban.IssueAgentCommandPending); err != nil {
		t.Fatal(err)
	}

	prompt, err := runner.buildTurnPrompt(workflow, issue, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Operator follow-up commands:") {
		t.Fatalf("expected operator follow-up section, got %q", prompt)
	}
	firstIndex := strings.Index(prompt, "1. First follow-up")
	secondIndex := strings.Index(prompt, "2. Second follow-up")
	if firstIndex == -1 || secondIndex == -1 || firstIndex > secondIndex {
		t.Fatalf("expected ordered commands in prompt, got %q", prompt)
	}
}

func TestContinuationPrompt(t *testing.T) {
	runner, store, manager, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Continue", "", 0, nil)
	workflow, _ := manager.Current()

	prompt, err := runner.buildTurnPrompt(workflow, issue, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Continuation guidance") {
		t.Fatalf("expected continuation prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "switch to another deterministic local check") {
		t.Fatalf("expected blocked-verification guidance, got %q", prompt)
	}
}

func TestRunAgentStdio(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Test", "Description", 0, nil)

	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected successful run, got %+v", result)
	}
	if !strings.Contains(result.Output, issue.Identifier) {
		t.Fatalf("expected output to contain rendered prompt, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "Prefer deterministic local verification first") {
		t.Fatalf("expected execution guidance in output, got %q", result.Output)
	}
}

func TestRunAttemptIncludesAttempt(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Retry", "Body", 0, nil)

	result, err := runner.RunAttempt(context.Background(), issue, 3)
	if err != nil {
		t.Fatalf("RunAttempt failed: %v", err)
	}
	if !strings.Contains(result.Output, "retry 3") {
		t.Fatalf("expected retry attempt in output, got %q", result.Output)
	}
}

func TestRunAttemptStopsWhenReadyIssueIsBlocked(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	blocker, _ := store.CreateIssue("", "", "Blocker", "", 0, nil)
	blocked, _ := store.CreateIssue("", "", "Blocked", "", 0, nil)

	if err := store.UpdateIssueState(blocker.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}
	if err := store.UpdateIssueState(blocked.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocked: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}
	blocked, err := store.GetIssue(blocked.ID)
	if err != nil {
		t.Fatalf("GetIssue blocked: %v", err)
	}

	result, err := runner.RunAttempt(context.Background(), blocked, 0)
	if err == nil {
		t.Fatal("expected blocked transition error")
	}
	if result != nil {
		t.Fatalf("expected nil result on blocked transition, got %#v", result)
	}
	if !kanban.IsBlockedTransition(err) {
		t.Fatalf("expected blocked transition error, got %v", err)
	}
	if !strings.Contains(err.Error(), "cannot move issue to in_progress: blocked by "+blocker.Identifier) {
		t.Fatalf("unexpected error message: %q", err.Error())
	}

	reloaded, err := store.GetIssue(blocked.ID)
	if err != nil {
		t.Fatalf("GetIssue blocked: %v", err)
	}
	if reloaded.State != kanban.StateReady {
		t.Fatalf("expected blocked issue to remain ready, got %s", reloaded.State)
	}
}

func TestRunAgentStdioMarksPendingCommandsDelivered(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Prompt command", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatal(err)
	}
	issue, _ = store.GetIssue(issue.ID)

	if _, err := store.CreateIssueAgentCommand(issue.ID, "Merge the branch to master.", kanban.IssueAgentCommandPending); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIssueAgentCommand(issue.ID, "Close the follow-up checklist.", kanban.IssueAgentCommandPending); err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !strings.Contains(result.Output, "Merge the branch to master.") {
		t.Fatalf("expected output to contain command, got %q", result.Output)
	}
	firstIndex := strings.Index(result.Output, "1. Merge the branch to master.")
	secondIndex := strings.Index(result.Output, "2. Close the follow-up checklist.")
	if firstIndex == -1 || secondIndex == -1 || firstIndex > secondIndex {
		t.Fatalf("expected ordered command output, got %q", result.Output)
	}

	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAgentCommands: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("expected two commands, got %+v", commands)
	}
	for _, command := range commands {
		if command.Status != kanban.IssueAgentCommandDelivered || command.DeliveryMode != "next_run" {
			t.Fatalf("unexpected command state: %+v", commands)
		}
	}
}

func TestRunAgentStdioKeepsCommandsPendingWhenTurnFails(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "command-that-does-not-exist", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Broken stdio command", "", 0, nil)

	command, err := store.CreateIssueAgentCommand(issue.ID, "Retry after the command is fixed.", kanban.IssueAgentCommandPending)
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result == nil || result.Success {
		t.Fatalf("expected unsuccessful run, got %+v", result)
	}

	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAgentCommands: %v", err)
	}
	if len(commands) != 1 || commands[0].ID != command.ID || commands[0].Status != kanban.IssueAgentCommandPending {
		t.Fatalf("expected command to remain pending, got %+v", commands)
	}
}

func TestRunAgentStdioPromotesWaitingCommandsOnceDispatchable(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	blocker, _ := store.CreateIssue("", "", "Blocker", "", 0, nil)
	issue, _ := store.CreateIssue("", "", "Blocked follow-up", "", 0, nil)

	if err := store.UpdateIssueState(blocker.ID, kanban.StateReady); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIssueAgentCommand(issue.ID, "Wait for unblock then merge.", kanban.IssueAgentCommandWaitingForUnblock); err != nil {
		t.Fatal(err)
	}

	if _, err := store.SetIssueBlockers(issue.ID, nil); err != nil {
		t.Fatal(err)
	}
	issue, _ = store.GetIssue(issue.ID)

	result, err := runner.RunAttempt(context.Background(), issue, 0)
	if err != nil {
		t.Fatalf("RunAttempt failed: %v", err)
	}
	if !strings.Contains(result.Output, "Wait for unblock then merge.") {
		t.Fatalf("expected promoted command in output, got %q", result.Output)
	}

	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0].Status != kanban.IssueAgentCommandDelivered {
		t.Fatalf("unexpected command state: %+v", commands)
	}
}

func TestBuildTurnPromptSkipsPendingCommandsWhileBlocked(t *testing.T) {
	runner, store, manager, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	blocker, _ := store.CreateIssue("", "", "Blocker", "", 0, nil)
	issue, _ := store.CreateIssue("", "", "Blocked follow-up", "", 0, nil)
	workflow, _ := manager.Current()

	if err := store.UpdateIssueState(blocker.ID, kanban.StateReady); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIssueAgentCommand(issue.ID, "Do not deliver while blocked.", kanban.IssueAgentCommandPending); err != nil {
		t.Fatal(err)
	}

	prompt, err := runner.buildTurnPrompt(workflow, issue, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "Do not deliver while blocked.") {
		t.Fatalf("expected blocked pending command to be skipped, got %q", prompt)
	}
}

func TestRunAgentAppServerConsumesPendingCommandsInSameThread(t *testing.T) {
	tmpDir := t.TempDir()
	traceFile := filepath.Join(tmpDir, "trace.log")
	releaseFile := filepath.Join(tmpDir, "release")
	scriptPath := filepath.Join(tmpDir, "fake-codex.sh")
	script := `#!/bin/sh
trace_file="$TRACE_FILE"
release_file="$RELEASE_FILE"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-live"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-one"}}}'
      while [ ! -f "$release_file" ]; do sleep 0.01; done
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-live","turnId":"turn-one"}}'
      ;;
    5)
      printf '%s\n' '{"id":4,"result":{"turn":{"id":"turn-two"}}}'
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-live","turnId":"turn-two"}}'
      sleep 0.03
      exit 0
      ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_FILE", traceFile)
	t.Setenv("RELEASE_FILE", releaseFile)

	runner, store, _, _, _ := setupTestRunner(t, "sh "+scriptPath, config.AgentModeAppServer)
	issue, _ := store.CreateIssue("", "", "Live follow-up", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatal(err)
	}
	issue, _ = store.GetIssue(issue.ID)

	done := make(chan struct{})
	resultCh := make(chan *RunResult, 1)
	errCh := make(chan error, 1)
	go func() {
		defer close(done)
		result, err := runner.Run(context.Background(), issue)
		resultCh <- result
		errCh <- err
	}()

	waitForTurnStartCount(t, traceFile, 1)
	if _, err := store.CreateIssueAgentCommand(issue.ID, "Handle the missed merge step.", kanban.IssueAgentCommandPending); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releaseFile, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for runner to finish")
	}
	result := <-resultCh
	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful run, got %+v", result)
	}

	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAgentCommands: %v", err)
	}
	if len(commands) != 1 || commands[0].Status != kanban.IssueAgentCommandDelivered || commands[0].DeliveryMode != "same_thread" || commands[0].DeliveryThreadID != "thread-live" {
		t.Fatalf("unexpected command state: %+v", commands)
	}
	lines := readTraceLines(t, traceFile)
	turnStarts := 0
	for _, payload := range lines {
		if method, _ := payload["method"].(string); method == "turn/start" {
			turnStarts++
		}
	}
	if turnStarts != 2 {
		t.Fatalf("expected two turn/start requests, got %d from %#v", turnStarts, lines)
	}
}

func TestWorkspaceDeterministic(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	workflow, _ := runner.workflowProvider.Current()

	ws1, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace first call: %v", err)
	}
	ws2, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace second call: %v", err)
	}
	if ws1.Path != ws2.Path {
		t.Error("Expected deterministic workspace path")
	}
	expected := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))
	if ws1.Path != expected {
		t.Errorf("Expected path %s, got %s", expected, ws1.Path)
	}
}

func TestWorkspaceBootstrapUsesFreshRemoteDefaultBranch(t *testing.T) {
	runner, store, _, workspaceRoot, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	remotePath := filepath.Join(t.TempDir(), "origin.git")
	initBareGitRepoForTest(t, remotePath)
	runGitForTest(t, repoPath, "remote", "add", "origin", remotePath)
	runGitForTest(t, repoPath, "push", "-u", "origin", "main")

	localMainHead := runGitForTest(t, repoPath, "rev-parse", "main")
	remoteHead := advanceRemoteDevelopForTest(t, remotePath)

	issue, err := store.CreateIssue("", "", "Fresh remote workspace", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace root: %v", err)
	}

	workflow, err := runner.workflowProvider.Current()
	if err != nil {
		t.Fatalf("Current workflow: %v", err)
	}
	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace: %v", err)
	}

	if got := runGitForTest(t, repoPath, "rev-parse", "main"); got != localMainHead {
		t.Fatalf("expected local main to remain at %s, got %s", localMainHead, got)
	}
	if got := runGitForTest(t, repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); got != "origin/develop" {
		t.Fatalf("expected origin/HEAD to refresh to origin/develop, got %q", got)
	}
	if got := runGitForTest(t, repoPath, "rev-parse", "refs/remotes/origin/develop"); got != remoteHead {
		t.Fatalf("expected origin/develop to refresh to %s, got %s", remoteHead, got)
	}
	if got := runGitForTest(t, workspace.Path, "rev-parse", "HEAD"); got != remoteHead {
		t.Fatalf("expected workspace HEAD to start from %s, got %s", remoteHead, got)
	}
	if got := runGitForTest(t, workspace.Path, "branch", "--show-current"); got != "codex/"+issue.Identifier {
		t.Fatalf("expected workspace branch codex/%s, got %q", issue.Identifier, got)
	}
}

func TestWorkspaceBootstrapFallsBackWhenRemoteTrackingBranchIsUnavailable(t *testing.T) {
	runner, store, _, workspaceRoot, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	remotePath := filepath.Join(t.TempDir(), "origin.git")
	initBareGitRepoForTest(t, remotePath)
	runGitForTest(t, repoPath, "remote", "add", "origin", remotePath)
	runGitForTest(t, repoPath, "push", "-u", "origin", "main")
	runGitForTest(t, repoPath, "config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")

	localMainHead := runGitForTest(t, repoPath, "rev-parse", "main")
	advanceRemoteDevelopForTest(t, remotePath)

	issue, err := store.CreateIssue("", "", "Fallback remote workspace", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace root: %v", err)
	}

	workflow, err := runner.workflowProvider.Current()
	if err != nil {
		t.Fatalf("Current workflow: %v", err)
	}
	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace: %v", err)
	}

	if got := runGitForTest(t, workspace.Path, "rev-parse", "HEAD"); got != localMainHead {
		t.Fatalf("expected workspace HEAD to fall back to %s, got %s", localMainHead, got)
	}
	if got := runGitForTest(t, workspace.Path, "branch", "--show-current"); got != "codex/"+issue.Identifier {
		t.Fatalf("expected workspace branch codex/%s, got %q", issue.Identifier, got)
	}
}

func TestWorkspaceRecreatesMissingStoredDirectoryFromLocalBranchWithoutRemoteRefresh(t *testing.T) {
	runner, store, _, workspaceRoot, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	remotePath := filepath.Join(t.TempDir(), "origin.git")
	initBareGitRepoForTest(t, remotePath)
	runGitForTest(t, repoPath, "remote", "add", "origin", remotePath)
	runGitForTest(t, repoPath, "push", "-u", "origin", "main")

	issue, err := store.CreateIssue("", "", "Missing local branch", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	branchName := deterministicIssueBranch(issue)
	runGitForTest(t, repoPath, "branch", branchName, "main")

	path := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace path: %v", err)
	}
	if _, err := store.CreateWorkspace(issue.ID, path); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("RemoveAll workspace path: %v", err)
	}

	badRemotePath := filepath.Join(t.TempDir(), "missing.git")
	runGitForTest(t, repoPath, "remote", "set-url", "origin", badRemotePath)

	workflow, err := runner.workflowProvider.Current()
	if err != nil {
		t.Fatalf("Current workflow: %v", err)
	}
	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace: %v", err)
	}
	if workspace.Path != path {
		t.Fatalf("expected recreated workspace path %s, got %s", path, workspace.Path)
	}
	if got := runGitForTest(t, workspace.Path, "branch", "--show-current"); got != branchName {
		t.Fatalf("expected workspace branch %s, got %q", branchName, got)
	}
}

func TestWorkspaceBootstrapLeavesStalePathUntouchedWhenRefreshFails(t *testing.T) {
	runner, store, _, workspaceRoot, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	remotePath := filepath.Join(t.TempDir(), "origin.git")
	initBareGitRepoForTest(t, remotePath)
	runGitForTest(t, repoPath, "remote", "add", "origin", remotePath)
	runGitForTest(t, repoPath, "push", "-u", "origin", "main")

	issue, err := store.CreateIssue("", "", "Refresh failure", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	stalePath := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace root: %v", err)
	}
	if err := os.WriteFile(stalePath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile stale workspace: %v", err)
	}

	badRemotePath := filepath.Join(t.TempDir(), "missing.git")
	runGitForTest(t, repoPath, "remote", "set-url", "origin", badRemotePath)

	workflow, err := runner.workflowProvider.Current()
	if err != nil {
		t.Fatalf("Current workflow: %v", err)
	}
	_, err = runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err == nil {
		t.Fatal("expected workspace bootstrap to fail")
	}
	if !strings.Contains(err.Error(), "workspace_bootstrap") {
		t.Fatalf("expected workspace bootstrap error, got %v", err)
	}

	data, err := os.ReadFile(stalePath)
	if err != nil {
		t.Fatalf("ReadFile stale workspace: %v", err)
	}
	if string(data) != "stale" {
		t.Fatalf("expected stale workspace content to remain, got %q", string(data))
	}
	if info, err := os.Lstat(stalePath); err != nil {
		t.Fatalf("Lstat stale workspace: %v", err)
	} else if info.IsDir() {
		t.Fatal("expected stale workspace to remain as the original file")
	}
	if _, err := store.GetWorkspace(issue.ID); err == nil {
		t.Fatal("expected no workspace record to be created on refresh failure")
	}
}

func TestRepoBootstrapLockRespectsContextTimeout(t *testing.T) {
	_, _, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)

	unlock, err := repoBootstrapLock(context.Background(), repoPath)
	if err != nil {
		t.Fatalf("repoBootstrapLock: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := repoBootstrapLock(ctx, repoPath)
		result <- err
	}()

	select {
	case err := <-result:
		if err != context.DeadlineExceeded {
			t.Fatalf("expected deadline exceeded, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("expected lock acquisition to stop promptly, took %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected repo bootstrap lock acquisition to respect context timeout")
	}
}

func TestSanitizeWorkspaceKey(t *testing.T) {
	if got := sanitizeWorkspaceKey("MT/Det"); got != "MT_Det" {
		t.Fatalf("expected MT_Det, got %s", got)
	}
	if got := sanitizeWorkspaceKey("../escape"); got == "" || strings.Contains(got, "..") || strings.Contains(got, "/") {
		t.Fatalf("unexpected sanitized key: %s", got)
	}
}

func TestWorkspaceReplacesStaleFilePath(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Stale", "", 0, nil)
	path := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))

	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	workflow, _ := runner.workflowProvider.Current()
	ws, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("expected workspace recovery, got err: %v", err)
	}
	fi, err := os.Stat(ws.Path)
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected workspace dir at %s", ws.Path)
	}
	if got := runGitForTest(t, ws.Path, "branch", "--show-current"); got != "codex/"+issue.Identifier {
		t.Fatalf("expected workspace branch codex/%s, got %q", issue.Identifier, got)
	}
}

func TestWorkspaceRecreatesMissingStoredDirectory(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Missing", "", 0, nil)
	path := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(issue.ID, path); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	workflow, _ := runner.workflowProvider.Current()
	ws, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("expected missing workspace recovery, got err: %v", err)
	}
	if ws.Path != path {
		t.Fatalf("expected recovered workspace path %s, got %s", path, ws.Path)
	}
	fi, err := os.Stat(ws.Path)
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected recreated workspace dir at %s", ws.Path)
	}
	if got := runGitForTest(t, ws.Path, "branch", "--show-current"); got != "codex/"+issue.Identifier {
		t.Fatalf("expected workspace branch codex/%s, got %q", issue.Identifier, got)
	}
}

func TestWorkspaceBootstrapDetachedHeadUsesHEADBase(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Detached head workspace", "", 0, nil)
	workflow, _ := runner.workflowProvider.Current()

	head := runGitForTest(t, repoPath, "rev-parse", "HEAD")
	runGitForTest(t, repoPath, "checkout", "--detach", head)

	ws, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("expected detached-head workspace bootstrap to succeed, got: %v", err)
	}
	if got := runGitForTest(t, ws.Path, "branch", "--show-current"); got != "codex/"+issue.Identifier {
		t.Fatalf("expected workspace branch codex/%s, got %q", issue.Identifier, got)
	}
}

func TestResolveRepoDefaultBranchPrefersMainlineOverCurrentFeatureBranch(t *testing.T) {
	_, _, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	runGitForTest(t, repoPath, "checkout", "-qb", "feature")

	branch, err := resolveRepoDefaultBranch(context.Background(), repoPath)
	if err != nil {
		t.Fatalf("resolveRepoDefaultBranch: %v", err)
	}
	if branch != "main" {
		t.Fatalf("expected default branch main, got %q", branch)
	}
}

func TestResolveRepoDefaultBranchFallsBackToCurrentCustomBranch(t *testing.T) {
	_, _, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	runGitForTest(t, repoPath, "branch", "-M", "release")
	runGitForTest(t, repoPath, "checkout", "release")

	branch, err := resolveRepoDefaultBranch(context.Background(), repoPath)
	if err != nil {
		t.Fatalf("resolveRepoDefaultBranch: %v", err)
	}
	if branch != "release" {
		t.Fatalf("expected current custom branch release, got %q", branch)
	}
}

func TestWorkspaceReinitializesLegacyDirectoryIntoGitWorktree(t *testing.T) {
	runner, store, _, workspaceRoot, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Legacy workspace", "", 0, nil)
	legacyPath := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))

	if err := os.MkdirAll(legacyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyPath, "legacy.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(issue.ID, legacyPath); err != nil {
		t.Fatal(err)
	}

	workflow, _ := runner.workflowProvider.Current()
	ws, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("expected legacy workspace recovery, got err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, ".git")); err != nil {
		t.Fatalf("expected recovered git worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "legacy.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected stale legacy content to be removed, got err=%v", err)
	}
	preserved, err := filepath.Glob(legacyPath + ".legacy-*")
	if err != nil {
		t.Fatalf("Glob preserved workspace: %v", err)
	}
	if len(preserved) != 1 {
		t.Fatalf("expected one preserved workspace, got %v", preserved)
	}
	if data, err := os.ReadFile(filepath.Join(preserved[0], "legacy.txt")); err != nil || string(data) != "stale" {
		t.Fatalf("expected preserved legacy content, got data=%q err=%v", string(data), err)
	}
}

func TestRunAttemptSkipsBeforeRunHookDuringWorkspaceRebaseRecovery(t *testing.T) {
	runner, store, manager, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, err := store.CreateIssue("", "", "Rebase recovery", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	workflow, err := manager.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace: %v", err)
	}

	runGitForTest(t, repoPath, "branch", "feature/rebase-target")
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"branch_name": "feature/rebase-target"}); err != nil {
		t.Fatalf("UpdateIssue branch_name: %v", err)
	}

	gitDir := workspaceGitDirForTest(t, workspace.Path)
	if err := os.MkdirAll(filepath.Join(gitDir, "rebase-merge"), 0o755); err != nil {
		t.Fatalf("MkdirAll rebase state: %v", err)
	}

	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	workflowContent, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("ReadFile workflow: %v", err)
	}
	updated := strings.Replace(string(workflowContent), "hooks:\n  timeout_ms: 1000", "hooks:\n  before_run: exit 1\n  timeout_ms: 1000", 1)
	if err := os.WriteFile(workflowPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	updatedIssue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	result, err := runner.RunAttempt(context.Background(), updatedIssue, 0)
	if err != nil {
		t.Fatalf("RunAttempt: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful recovery run, got %+v", result)
	}
	if !strings.Contains(result.Output, "Workspace recovery note:") {
		t.Fatalf("expected recovery note in prompt output, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "Finish or quit the rebase") {
		t.Fatalf("expected rebase guidance in prompt output, got %q", result.Output)
	}

	events, err := store.ListIssueRuntimeEvents(issue.ID, 20)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "workspace_bootstrap_recovery" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected workspace_bootstrap_recovery event, got %+v", events)
	}
}

func TestPrepareTurnPromptWithWorkspaceAddsRecoveryNoteOnlyWhileRebaseActive(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, err := store.CreateIssue("", "", "Prompt recovery", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	workflow, err := runner.workflowProvider.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace: %v", err)
	}

	gitDir := workspaceGitDirForTest(t, workspace.Path)
	if err := os.MkdirAll(filepath.Join(gitDir, "rebase-merge"), 0o755); err != nil {
		t.Fatalf("MkdirAll rebase state: %v", err)
	}

	firstPrompt, err := runner.prepareTurnPromptWithWorkspace(workflow, issue, 0, 1, workspace.Path)
	if err != nil {
		t.Fatalf("prepareTurnPromptWithWorkspace first turn: %v", err)
	}
	if !strings.Contains(firstPrompt.Prompt, "Workspace recovery note:") {
		t.Fatalf("expected recovery note in first prompt, got %q", firstPrompt.Prompt)
	}

	continuationPrompt, err := runner.prepareTurnPromptWithWorkspace(workflow, issue, 0, 2, workspace.Path)
	if err != nil {
		t.Fatalf("prepareTurnPromptWithWorkspace continuation: %v", err)
	}
	if !strings.Contains(continuationPrompt.Prompt, "Workspace recovery note:") {
		t.Fatalf("expected recovery note in continuation prompt, got %q", continuationPrompt.Prompt)
	}
	if !strings.Contains(continuationPrompt.Prompt, "Continuation guidance") {
		t.Fatalf("expected continuation guidance, got %q", continuationPrompt.Prompt)
	}

	if err := os.RemoveAll(filepath.Join(gitDir, "rebase-merge")); err != nil {
		t.Fatalf("RemoveAll rebase state: %v", err)
	}

	if err := store.AppendRuntimeEvent("workspace_bootstrap_recovery", map[string]interface{}{
		"issue_id":        issue.ID,
		"identifier":      issue.Identifier,
		"phase":           string(issue.WorkflowPhase),
		"attempt":         0,
		"status":          "recovering",
		"message":         workspaceRecoveryNoteText(),
		"recovery_reason": "active_rebase",
		"error":           "workspace recovery required: active Git rebase detected",
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent recovery: %v", err)
	}

	firstPromptAfterEvent, err := runner.prepareTurnPromptWithWorkspace(workflow, issue, 0, 1, workspace.Path)
	if err != nil {
		t.Fatalf("prepareTurnPromptWithWorkspace recovered first prompt: %v", err)
	}
	if !strings.Contains(firstPromptAfterEvent.Prompt, "Workspace recovery note:") {
		t.Fatalf("expected recovery note fallback in first prompt, got %q", firstPromptAfterEvent.Prompt)
	}

	continuationAfterEvent, err := runner.prepareTurnPromptWithWorkspace(workflow, issue, 0, 2, workspace.Path)
	if err != nil {
		t.Fatalf("prepareTurnPromptWithWorkspace recovered continuation: %v", err)
	}
	if strings.Contains(continuationAfterEvent.Prompt, "Workspace recovery note:") {
		t.Fatalf("expected recovery note to stay off continuation prompt after recovery event, got %q", continuationAfterEvent.Prompt)
	}
	if !strings.Contains(continuationAfterEvent.Prompt, "Continuation guidance") {
		t.Fatalf("expected continuation guidance, got %q", continuationAfterEvent.Prompt)
	}
}

func TestGetOrCreateWorkspaceTreatsRepoLevelRebaseAsRecoverable(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, err := store.CreateIssue("", "", "Repo rebase recovery", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	workflow, err := runner.workflowProvider.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace initial: %v", err)
	}
	runGitForTest(t, workspace.Path, "switch", "-c", "feature/recovery-detour")

	repoGitDir := runGitForTest(t, repoPath, "rev-parse", "--git-dir")
	if !filepath.IsAbs(repoGitDir) {
		repoGitDir = filepath.Join(repoPath, repoGitDir)
	}
	if err := os.MkdirAll(filepath.Join(repoGitDir, "rebase-merge"), 0o755); err != nil {
		t.Fatalf("MkdirAll repo rebase state: %v", err)
	}

	recoveredWorkspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace recovery: %v", err)
	}
	if recoveredWorkspace.Path != workspace.Path {
		t.Fatalf("expected recovered workspace path %s, got %s", workspace.Path, recoveredWorkspace.Path)
	}

	events, err := store.ListIssueRuntimeEvents(issue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "workspace_bootstrap_recovery" {
			found = true
			if event.Payload["status"] != "recovering" {
				t.Fatalf("expected recovering payload, got %#v", event.Payload)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected workspace_bootstrap_recovery event, got %+v", events)
	}
}

func TestCleanupWorkspaceRemovesGitWorktree(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, err := store.CreateProject("Platform", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Cleanup workspace", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	workflow, _ := runner.workflowProvider.Current()

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace: %v", err)
	}
	if err := runner.CleanupWorkspace(context.Background(), issue); err != nil {
		t.Fatalf("CleanupWorkspace: %v", err)
	}
	if _, err := os.Stat(workspace.Path); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path to be removed, got err=%v", err)
	}
	if output := runGitForTest(t, repoPath, "worktree", "list", "--porcelain"); strings.Contains(output, workspace.Path) {
		t.Fatalf("expected worktree to be removed from repo listing, got %q", output)
	}
}

func TestWorkspaceReusedBranchRenameCreatesMissingBranch(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, err := store.CreateIssue("", "", "Renamed branch workspace", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	workflow, _ := runner.workflowProvider.Current()

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace initial: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "rename.txt"), []byte("kept"), 0o644); err != nil {
		t.Fatalf("WriteFile rename.txt: %v", err)
	}
	runGitForTest(t, workspace.Path, "add", "rename.txt")
	runGitForTest(t, workspace.Path, "commit", "-m", "preserve workspace history")
	originalHead := runGitForTest(t, workspace.Path, "rev-parse", "HEAD")

	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"branch_name": "feature/renamed"}); err != nil {
		t.Fatalf("UpdateIssue branch_name: %v", err)
	}
	updatedIssue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	reused, err := runner.getOrCreateWorkspace(context.Background(), workflow, updatedIssue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace renamed branch: %v", err)
	}
	if reused.Path != workspace.Path {
		t.Fatalf("expected reused workspace path %s, got %s", workspace.Path, reused.Path)
	}
	if got := runGitForTest(t, reused.Path, "branch", "--show-current"); got != "feature/renamed" {
		t.Fatalf("expected renamed workspace branch feature/renamed, got %q", got)
	}
	if got := runGitForTest(t, reused.Path, "rev-parse", "HEAD"); got != originalHead {
		t.Fatalf("expected renamed branch HEAD %s, got %s", originalHead, got)
	}
	if output := runGitForTest(t, reused.Path, "show", "--stat", "--oneline", "--format=%s", "HEAD"); !strings.Contains(output, "preserve workspace history") {
		t.Fatalf("expected renamed branch to keep prior commit, got %q", output)
	}
}

func TestRunAgentAppServerModeTracksSession(t *testing.T) {
	traceFile := filepath.Join(t.TempDir(), "trace.log")
	t.Setenv("TRACE_FILE", traceFile)

	command, _ := fakeappserver.CommandString(t, baseRunnerAppServerScenario("th1", "tu1",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{
					"threadId": "th1",
					"turn":     map[string]interface{}{"id": "tu1"},
					"usage": map[string]interface{}{
						"prompt_tokens":     5,
						"completion_tokens": 2,
						"total_tokens":      7,
					},
				},
			},
		},
	))
	runner, store, _, _, _ := setupTestRunner(t, command, config.AgentModeAppServer)
	issue, _ := store.CreateIssue("", "", "AppServer", "", 0, nil)

	res, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Success || res.AppSession == nil {
		t.Fatalf("expected app session, got %+v", res)
	}
	if res.AppSession.SessionID != "th1-tu1" {
		t.Fatalf("unexpected session id: %s", res.AppSession.SessionID)
	}
}

func TestRunAgentAppServerStagesImageAssetsOnFirstFreshTurn(t *testing.T) {
	traceFile := filepath.Join(t.TempDir(), "trace.log")
	t.Setenv("TRACE_FILE", traceFile)

	command, _ := fakeappserver.CommandString(t, baseRunnerAppServerScenario("thread-images", "turn-images",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{
					"threadId": "thread-images",
					"turn":     map[string]interface{}{"id": "turn-images"},
				},
			},
		},
	))
	runner, store, _, _, _ := setupTestRunner(t, command, config.AgentModeAppServer)
	issue, _ := store.CreateIssue("", "", "Issue assets", "", 0, nil)

	imageOne, err := store.CreateIssueAsset(issue.ID, "screen-one.png", bytes.NewReader(sampleRunnerPNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueAsset imageOne: %v", err)
	}
	imageTwo, err := store.CreateIssueAsset(issue.ID, "screen-two.png", bytes.NewReader(sampleRunnerPNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueAsset imageTwo: %v", err)
	}
	if _, err := store.CreateIssueAsset(issue.ID, "notes.txt", strings.NewReader("do not stage me")); err != nil {
		t.Fatalf("CreateIssueAsset text: %v", err)
	}

	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful run, got %+v", result)
	}

	workspace, err := store.GetWorkspace(issue.ID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	for _, image := range []kanban.IssueAsset{*imageOne, *imageTwo} {
		stagedPath := filepath.Join(workspace.Path, filepath.FromSlash(appServerIssueAssetStageDir), image.ID+"-"+image.Filename)
		stagedBytes, err := os.ReadFile(stagedPath)
		if err != nil {
			t.Fatalf("expected staged image %s: %v", stagedPath, err)
		}
		if !bytes.Equal(stagedBytes, sampleRunnerPNGBytes()) {
			t.Fatalf("unexpected staged image bytes for %s", stagedPath)
		}
	}

	turnStarts := turnStartPayloads(readTraceLines(t, traceFile))
	if len(turnStarts) != 1 {
		t.Fatalf("expected one turn/start request, got %#v", turnStarts)
	}
	threadStarts := threadStartPayloads(readTraceLines(t, traceFile))
	if len(threadStarts) != 1 {
		t.Fatalf("expected one thread/start request, got %#v", threadStarts)
	}
	threadParams, _ := threadStarts[0]["params"].(map[string]interface{})
	threadConfig, _ := threadParams["config"].(map[string]interface{})
	if threadConfig["initial_collaboration_mode"] != config.InitialCollaborationModeDefault {
		t.Fatalf("unexpected thread/start config: %#v", threadConfig)
	}
	params, _ := turnStarts[0]["params"].(map[string]interface{})
	input, _ := params["input"].([]interface{})
	if len(input) != 3 {
		t.Fatalf("expected text plus two local images, got %#v", input)
	}
	firstInput, _ := input[0].(map[string]interface{})
	if firstInput["type"] != "text" {
		t.Fatalf("unexpected first input: %#v", firstInput)
	}
	for idx, image := range []kanban.IssueAsset{*imageOne, *imageTwo} {
		item, _ := input[idx+1].(map[string]interface{})
		expectedPath := filepath.ToSlash(filepath.Join(".maestro", "issue-assets", image.ID+"-"+image.Filename))
		if item["type"] != "localImage" || item["path"] != expectedPath || item["name"] != image.Filename {
			t.Fatalf("unexpected image input %d: %#v", idx, item)
		}
		if path, _ := item["path"].(string); filepath.IsAbs(path) {
			t.Fatalf("expected workspace-relative image path, got %q", path)
		}
	}
}

func TestRunAgentAppServerWithoutImageAssetsSendsTextOnly(t *testing.T) {
	traceFile := filepath.Join(t.TempDir(), "trace.log")
	t.Setenv("TRACE_FILE", traceFile)

	command, _ := fakeappserver.CommandString(t, baseRunnerAppServerScenario("thread-text", "turn-text",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{
					"threadId": "thread-text",
					"turn":     map[string]interface{}{"id": "turn-text"},
				},
			},
		},
	))
	runner, store, _, _, _ := setupTestRunner(t, command, config.AgentModeAppServer)
	issue, _ := store.CreateIssue("", "", "No assets", "", 0, nil)

	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful run, got %+v", result)
	}

	turnStarts := turnStartPayloads(readTraceLines(t, traceFile))
	if len(turnStarts) != 1 {
		t.Fatalf("expected one turn/start request, got %#v", turnStarts)
	}
	params, _ := turnStarts[0]["params"].(map[string]interface{})
	input, _ := params["input"].([]interface{})
	if len(input) != 1 {
		t.Fatalf("expected text-only turn input, got %#v", input)
	}
	firstInput, _ := input[0].(map[string]interface{})
	if firstInput["type"] != "text" {
		t.Fatalf("unexpected text-only input payload: %#v", firstInput)
	}
}

func TestRunAgentAppServerFailsWhenIssueAssetStagingFails(t *testing.T) {
	traceFile := filepath.Join(t.TempDir(), "trace.log")
	t.Setenv("TRACE_FILE", traceFile)

	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-stage-fail"}}},
				}},
			},
		},
	})
	runner, store, _, _, _ := setupTestRunner(t, command, config.AgentModeAppServer)
	issue, _ := store.CreateIssue("", "", "Missing image asset", "", 0, nil)

	image, err := store.CreateIssueAsset(issue.ID, "broken.png", bytes.NewReader(sampleRunnerPNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueAsset: %v", err)
	}
	_, imagePath, err := store.GetIssueAssetContent(issue.ID, image.ID)
	if err != nil {
		t.Fatalf("GetIssueAssetContent: %v", err)
	}
	if err := os.Remove(imagePath); err != nil {
		t.Fatalf("remove image asset: %v", err)
	}

	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || result.Success || result.Error == nil {
		t.Fatalf("expected staging failure result, got %+v", result)
	}
	if !strings.Contains(result.Error.Error(), image.ID) || !strings.Contains(result.Error.Error(), issue.Identifier) {
		t.Fatalf("expected error to mention issue and image, got %v", result.Error)
	}
	if turns := turnStartPayloads(readTraceLines(t, traceFile)); len(turns) != 0 {
		t.Fatalf("expected staging failure before turn/start, got %#v", turns)
	}
}

func TestRunAgentAppServerDoesNotResendImageAssetsOnContinuationTurn(t *testing.T) {
	traceFile := filepath.Join(t.TempDir(), "trace.log")
	t.Setenv("TRACE_FILE", traceFile)

	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-continue"}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-one"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-continue", "turn": map[string]interface{}{"id": "turn-one"}}}},
				},
			},
			{
				Match:          fakeappserver.Match{Method: "turn/start"},
				WaitForRelease: "finish-second-turn",
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 4, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-two"}}}},
				},
				EmitAfterRelease: []fakeappserver.Output{
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-continue", "turn": map[string]interface{}{"id": "turn-two"}}}},
				},
				ExitCode: fakeappserver.Int(0),
			},
		},
	}
	command, release := fakeappserver.CommandString(t, scenario)
	runner, store, _, _, _ := setupTestRunner(t, command, config.AgentModeAppServer)
	issue, _ := store.CreateIssue("", "", "Continuation assets", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	issue, _ = store.GetIssue(issue.ID)
	if _, err := store.CreateIssueAsset(issue.ID, "continue.png", bytes.NewReader(sampleRunnerPNGBytes())); err != nil {
		t.Fatalf("CreateIssueAsset: %v", err)
	}

	done := make(chan struct{})
	resultCh := make(chan *RunResult, 1)
	errCh := make(chan error, 1)
	go func() {
		defer close(done)
		result, runErr := runner.Run(context.Background(), issue)
		resultCh <- result
		errCh <- runErr
	}()

	waitForTurnStartCount(t, traceFile, 2)
	if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		t.Fatalf("UpdateIssueState done: %v", err)
	}
	release("finish-second-turn")

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for continuation run")
	}
	result := <-resultCh
	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful continuation run, got %+v", result)
	}

	turnStarts := turnStartPayloads(readTraceLines(t, traceFile))
	if len(turnStarts) != 2 {
		t.Fatalf("expected two turn/start requests, got %#v", turnStarts)
	}
	firstInput, _ := turnStarts[0]["params"].(map[string]interface{})["input"].([]interface{})
	if len(firstInput) != 2 {
		t.Fatalf("expected first turn to include one local image, got %#v", firstInput)
	}
	secondInput, _ := turnStarts[1]["params"].(map[string]interface{})["input"].([]interface{})
	if len(secondInput) != 1 {
		t.Fatalf("expected continuation turn to stay text-only, got %#v", secondInput)
	}
}

func TestRunAgentAppServerResumedThreadIncludesIssueAssetInputsOnFirstTurn(t *testing.T) {
	traceFile := filepath.Join(t.TempDir(), "trace.log")
	t.Setenv("TRACE_FILE", traceFile)

	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/resume"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-resumed"}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-resumed"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-resumed", "turn": map[string]interface{}{"id": "turn-resumed"}}}},
				},
				ExitCode: fakeappserver.Int(0),
			},
		},
	}
	command, _ := fakeappserver.CommandString(t, scenario)
	runner, store, _, _, _ := setupTestRunner(t, command, config.AgentModeAppServer)
	issue, _ := store.CreateIssue("", "", "Resumed assets", "", 0, nil)
	image, err := store.CreateIssueAsset(issue.ID, "resume.png", bytes.NewReader(sampleRunnerPNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueAsset: %v", err)
	}
	issue.ResumeThreadID = "thread-stale"

	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful resumed run, got %+v", result)
	}

	workspace, err := store.GetWorkspace(issue.ID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	stagedPath := filepath.Join(workspace.Path, filepath.FromSlash(appServerIssueAssetStageDir), image.ID+"-"+image.Filename)
	stagedBytes, err := os.ReadFile(stagedPath)
	if err != nil {
		t.Fatalf("expected resumed run to stage issue image %s: %v", stagedPath, err)
	}
	if !bytes.Equal(stagedBytes, sampleRunnerPNGBytes()) {
		t.Fatalf("unexpected staged image bytes for resumed run: %s", stagedPath)
	}

	turnStarts := turnStartPayloads(readTraceLines(t, traceFile))
	if len(turnStarts) != 1 {
		t.Fatalf("expected one turn/start request, got %#v", turnStarts)
	}
	input, _ := turnStarts[0]["params"].(map[string]interface{})["input"].([]interface{})
	if len(input) != 2 {
		t.Fatalf("expected resumed thread to send text plus image input, got %#v", input)
	}
	imageInput, _ := input[1].(map[string]interface{})
	expectedPath := filepath.ToSlash(filepath.Join(".maestro", "issue-assets", image.ID+"-"+image.Filename))
	if imageInput["type"] != "localImage" || imageInput["path"] != expectedPath || imageInput["name"] != image.Filename {
		t.Fatalf("unexpected resumed image input: %#v", imageInput)
	}
}

func TestRunAgentAppServerModeAdvertisesAndExecutesDynamicTools(t *testing.T) {
	tmpDir := t.TempDir()
	traceFile := filepath.Join(tmpDir, "trace.log")
	scriptPath := filepath.Join(tmpDir, "fake-codex.sh")
	script := `#!/bin/sh
trace_file="$TRACE_FILE"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-dyn"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-dyn"}}}'
      printf '%s\n' '{"id":120,"method":"item/tool/call","params":{"tool":"ext_echo","arguments":{"args":{"value":"ok"}}}}'
      ;;
    5)
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-dyn","turnId":"turn-dyn"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_FILE", traceFile)

	runner, store, manager, _, _ := setupTestRunner(t, "sh "+scriptPath, config.AgentModeAppServer)
	runner = NewRunnerWithExtensions(manager, store, extensions.NewRegistry([]extensions.Tool{
		{Name: "ext_echo", Description: "echo tool", Command: "echo $MAESTRO_ARGS_JSON"},
	}))
	issue, _ := store.CreateIssue("", "", "Dynamic Tools", "", 0, nil)

	res, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}

	lines := readTraceLines(t, traceFile)
	foundDynamicTools := false
	foundToolResult := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 2 {
			if params, ok := payload["params"].(map[string]interface{}); ok {
				if dynamicTools, ok := params["dynamicTools"].([]interface{}); ok && len(dynamicTools) == 1 {
					spec, _ := dynamicTools[0].(map[string]interface{})
					foundDynamicTools = spec["name"] == "ext_echo"
				}
			}
		}
		if id, ok := asInt(payload["id"]); ok && id == 120 {
			if result, ok := payload["result"].(map[string]interface{}); ok && result["success"] == true {
				items, _ := result["contentItems"].([]interface{})
				if len(items) == 1 {
					item, _ := items[0].(map[string]interface{})
					if text, _ := item["text"].(string); strings.Contains(text, `"value":"ok"`) {
						foundToolResult = true
					}
				}
			}
		}
	}
	if !foundDynamicTools {
		t.Fatal("expected dynamic tool specs in thread/start")
	}
	if !foundToolResult {
		t.Fatal("expected extension-backed tool result in trace")
	}
}

func TestRunAgentAppServerModeReportsDynamicToolFailures(t *testing.T) {
	tmpDir := t.TempDir()
	traceFile := filepath.Join(tmpDir, "trace.log")
	scriptPath := filepath.Join(tmpDir, "fake-codex.sh")
	script := `#!/bin/sh
trace_file="$TRACE_FILE"
count=0
while IFS= read -r line; do
  count=$((count + 1))
  printf 'JSON:%s\n' "$line" >> "$trace_file"
  case "$count" in
    1) printf '%s\n' '{"id":1,"result":{}}' ;;
    2) ;;
    3) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-fail"}}}' ;;
    4)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-fail"}}}'
      printf '%s\n' '{"id":121,"method":"item/tool/call","params":{"tool":"ext_fail","arguments":{"args":{"value":"bad"}}}}'
      ;;
    5)
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-fail","turnId":"turn-fail"}}'
      exit 0
      ;;
    *) exit 0 ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_FILE", traceFile)

	runner, store, manager, _, _ := setupTestRunner(t, "sh "+scriptPath, config.AgentModeAppServer)
	runner = NewRunnerWithExtensions(manager, store, extensions.NewRegistry([]extensions.Tool{
		{Name: "ext_fail", Description: "fail tool", Command: "echo nope && exit 1"},
	}))
	issue, _ := store.CreateIssue("", "", "Dynamic Tool Failures", "", 0, nil)

	if _, err := runner.Run(context.Background(), issue); err != nil {
		t.Fatalf("run: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 121 {
			if result, ok := payload["result"].(map[string]interface{}); ok && result["success"] == false {
				items, _ := result["contentItems"].([]interface{})
				if len(items) != 1 {
					t.Fatalf("unexpected content items: %#v", result)
				}
				item, _ := items[0].(map[string]interface{})
				text, _ := item["text"].(string)
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(text), &parsed); err != nil {
					t.Fatalf("expected JSON error payload, got %q: %v", text, err)
				}
				return
			}
		}
	}
	t.Fatal("expected failed extension-backed tool response")
}

func TestCleanupWorkspaceRunsBeforeRemoveHook(t *testing.T) {
	traceFileDir := t.TempDir()
	traceFile := filepath.Join(traceFileDir, "cleanup.log")
	command := "cat"
	runner, store, manager, workspaceRoot, repoDir := setupTestRunner(t, command, config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Cleanup", "", 0, nil)

	workflowText := `---
tracker:
  kind: kanban
workspace:
  root: ` + workspaceRoot + `
hooks:
  before_remove: echo cleaned >> ` + traceFile + `
  timeout_ms: 1000
agent:
  mode: stdio
codex:
  command: cat
---
{{ issue.identifier }}
`
	if err := os.WriteFile(filepath.Join(repoDir, "WORKFLOW.md"), []byte(workflowText), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}

	if _, err := runner.Run(context.Background(), issue); err != nil {
		t.Fatal(err)
	}
	if err := runner.CleanupWorkspace(context.Background(), issue); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(traceFile); err != nil {
		t.Fatalf("expected before_remove hook output, got %v", err)
	}
}

func readTraceLines(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "JSON:") {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "JSON:")), &payload); err != nil {
			t.Fatalf("decode trace line %q: %v", line, err)
		}
		out = append(out, payload)
	}
	return out
}

func asInt(v interface{}) (int, bool) {
	switch typed := v.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func turnStartPayloads(payloads []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(payloads))
	for _, payload := range payloads {
		if method, _ := payload["method"].(string); method == "turn/start" {
			out = append(out, payload)
		}
	}
	return out
}

func threadStartPayloads(payloads []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(payloads))
	for _, payload := range payloads {
		if method, _ := payload["method"].(string); method == "thread/start" {
			out = append(out, payload)
		}
	}
	return out
}

func waitForTurnStartCount(t *testing.T, tracePath string, want int) []map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		payloads := turnStartPayloads(readTraceLinesIfPresent(t, tracePath))
		if len(payloads) >= want {
			return payloads
		}
		time.Sleep(25 * time.Millisecond)
	}
	payloads := turnStartPayloads(readTraceLinesIfPresent(t, tracePath))
	t.Fatalf("timed out waiting for %d turn/start payloads, got %#v", want, payloads)
	return nil
}

func readTraceLinesIfPresent(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "JSON:") {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "JSON:")), &payload); err != nil {
			t.Fatalf("decode trace line %q: %v", line, err)
		}
		out = append(out, payload)
	}
	return out
}
