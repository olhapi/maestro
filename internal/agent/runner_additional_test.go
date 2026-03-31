package agent

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	agentruntime "github.com/olhapi/maestro/internal/agentruntime"
	runtimefactory "github.com/olhapi/maestro/internal/agentruntime/factory"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/providers"
	"github.com/olhapi/maestro/pkg/config"
)

type staticWorkflowProvider struct {
	workflow *config.Workflow
	err      error
}

func (p staticWorkflowProvider) Current() (*config.Workflow, error) {
	return p.workflow, p.err
}

type noopRuntimeClient struct {
	capabilities agentruntime.Capabilities
	session      agentruntime.Session
}

func (c *noopRuntimeClient) Capabilities() agentruntime.Capabilities {
	return c.capabilities
}

func (c *noopRuntimeClient) RunTurn(context.Context, agentruntime.TurnRequest, func(*agentruntime.Session)) error {
	return nil
}

func (c *noopRuntimeClient) UpdatePermissions(agentruntime.PermissionConfig) {}

func (c *noopRuntimeClient) RespondToInteraction(context.Context, string, agentruntime.PendingInteractionResponse) error {
	return nil
}

func (c *noopRuntimeClient) Session() *agentruntime.Session {
	cp := c.session.Clone()
	return &cp
}

func (c *noopRuntimeClient) Output() string {
	return ""
}

func (c *noopRuntimeClient) Close() error {
	return nil
}

type deliveringRuntimeClient struct {
	capabilities agentruntime.Capabilities
	session      agentruntime.Session
}

func (c *deliveringRuntimeClient) Capabilities() agentruntime.Capabilities {
	return c.capabilities
}

func (c *deliveringRuntimeClient) RunTurn(_ context.Context, _ agentruntime.TurnRequest, onSession func(*agentruntime.Session)) error {
	if onSession != nil {
		onSession(&c.session)
	}
	return nil
}

func (c *deliveringRuntimeClient) UpdatePermissions(agentruntime.PermissionConfig) {}

func (c *deliveringRuntimeClient) RespondToInteraction(context.Context, string, agentruntime.PendingInteractionResponse) error {
	return nil
}

func (c *deliveringRuntimeClient) Session() *agentruntime.Session {
	cp := c.session.Clone()
	return &cp
}

func (c *deliveringRuntimeClient) Output() string {
	return ""
}

func (c *deliveringRuntimeClient) Close() error {
	return nil
}

type phasedErrContext struct {
	context.Context
	err      error
	errCalls int
}

func (c *phasedErrContext) Done() <-chan struct{} {
	return nil
}

func (c *phasedErrContext) Err() error {
	c.errCalls++
	if c.errCalls >= 2 {
		return c.err
	}
	return nil
}

func blockWorkspaceInsertsForTest(t *testing.T, dbPath, issueID string) {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	triggerSQL := `CREATE TRIGGER block_workspace_inserts
BEFORE INSERT ON workspaces
WHEN NEW.issue_id = '` + issueID + `'
BEGIN
  SELECT RAISE(ABORT, 'workspace insert failed');
END;`
	if _, err := db.Exec(triggerSQL); err != nil {
		t.Fatalf("create workspace insert trigger: %v", err)
	}
}

func failWorkspaceInsertsAfterRowForTest(t *testing.T, dbPath, issueID string) {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	triggerSQL := `CREATE TRIGGER fail_workspace_inserts_after_row
AFTER INSERT ON workspaces
WHEN NEW.issue_id = '` + issueID + `'
BEGIN
  SELECT RAISE(FAIL, 'workspace insert failed after insert');
END;`
	if _, err := db.Exec(triggerSQL); err != nil {
		t.Fatalf("create workspace insert trigger: %v", err)
	}
}

func blockRuntimeEventsForIssueForTest(t *testing.T, dbPath, issueID string) {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	triggerSQL := `CREATE TRIGGER block_runtime_events_for_issue
BEFORE INSERT ON runtime_events
WHEN NEW.issue_id = '` + issueID + `'
BEGIN
  SELECT RAISE(ABORT, 'runtime event failed');
END;`
	if _, err := db.Exec(triggerSQL); err != nil {
		t.Fatalf("create runtime event trigger: %v", err)
	}
}

func TestRunnerPureHelpersBranches(t *testing.T) {
	runner, _, _, workspaceRoot, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)

	if got := workspaceRecoveryNoteText(); !strings.Contains(got, "active Git rebase") {
		t.Fatalf("unexpected workspace recovery note: %q", got)
	}
	if !isWorkspaceBootstrapRebaseError(errors.New("cannot switch branch while rebasing")) {
		t.Fatal("expected direct rebase error match")
	}
	if !isWorkspaceBootstrapRebaseError(errors.New("switch branch while rebasing")) {
		t.Fatal("expected descriptive rebase error match")
	}
	if isWorkspaceBootstrapRebaseError(errors.New("boom")) || isWorkspaceBootstrapRebaseError(nil) {
		t.Fatal("expected non-rebase errors to be ignored")
	}
	if got := workspaceBootstrapRecoveryError("active_rebase"); got != "workspace recovery required: active Git rebase detected" {
		t.Fatalf("unexpected active rebase recovery error: %q", got)
	}
	if got := workspaceBootstrapRecoveryError("branch_switch_blocked"); got != "workspace recovery required: Git blocked the branch switch while rebasing" {
		t.Fatalf("unexpected branch switch recovery error: %q", got)
	}
	if got := workspaceBootstrapRecoveryError("other"); got != "workspace recovery required" {
		t.Fatalf("unexpected fallback recovery error: %q", got)
	}

	if got := sanitizeWorkspaceKey("../My Feature/issue"); strings.Contains(got, "..") || strings.Contains(got, "/") {
		t.Fatalf("unexpected sanitized workspace key: %q", got)
	}
	project := &kanban.Project{Name: "Team Alpha"}
	if got := projectWorkspaceSlug(project); got != "team-alpha" {
		t.Fatalf("unexpected project slug: %q", got)
	}
	if got := deterministicIssueBranch(&kanban.Issue{}); got != "codex/issue" {
		t.Fatalf("unexpected default branch: %q", got)
	}
	if got := deterministicIssueBranch(&kanban.Issue{Identifier: "ISSUE-1", BranchName: "  custom-branch  "}); got != "custom-branch" {
		t.Fatalf("unexpected deterministic branch: %q", got)
	}

	if got := appendWorkspaceRecoveryNote("", "note"); got != "note" {
		t.Fatalf("unexpected appended recovery note: %q", got)
	}
	if got := prependPlanRevisionNote("", "rev"); !strings.Contains(got, "Plan revision note:") {
		t.Fatalf("unexpected prepended plan revision note: %q", got)
	}
	if got := prependWorkspaceRecoveryNote("", "note"); got != "note" {
		t.Fatalf("unexpected prepended workspace recovery note: %q", got)
	}
	if got := appendAgentInstructions("", &kanban.Issue{AgentName: "bot", AgentPrompt: "be concise"}); !strings.Contains(got, "Assigned agent: bot") || !strings.Contains(got, "Additional instructions: be concise") {
		t.Fatalf("unexpected agent instructions: %q", got)
	}
	if got := appendOperatorCommands("", []kanban.IssueAgentCommand{{Command: "restart"}}); !strings.Contains(got, "Operator follow-up commands:") || !strings.Contains(got, "1. restart") {
		t.Fatalf("unexpected operator commands: %q", got)
	}
	if got := buildOperatorFollowUpPrompt([]kanban.IssueAgentCommand{{Command: "restart"}}); !strings.Contains(got, "Operator follow-up commands:") || !strings.Contains(got, "Prefer deterministic local verification first") {
		t.Fatalf("unexpected follow-up prompt: %q", got)
	}
	if got := issueHasPendingPlanRevision(&kanban.Issue{}); got {
		t.Fatal("expected empty issue to have no pending plan revision")
	}
	if got := issueHasPendingPlanRevision(&kanban.Issue{PendingPlanRevisionMarkdown: "rev", PendingPlanRevisionRequestedAt: ptrTime(time.Now())}); !got {
		t.Fatal("expected issue with pending plan revision to report true")
	}

	perm := runtimePermissionConfig(permissionConfig{
		ApprovalPolicy:           "policy",
		ThreadSandbox:            "workspace-write",
		TurnSandboxPolicy:        map[string]interface{}{"type": "sandbox"},
		InitialCollaborationMode: config.InitialCollaborationModePlan,
	})
	if perm.CollaborationMode != config.InitialCollaborationModePlan || perm.ThreadSandbox != "workspace-write" {
		t.Fatalf("unexpected runtime permission config: %#v", perm)
	}

	if got := dynamicToolSuccess("output"); !got["success"].(bool) {
		t.Fatalf("unexpected dynamic tool success payload: %#v", got)
	}
	if got := dynamicToolError("boom"); got["success"].(bool) {
		t.Fatalf("unexpected dynamic tool error payload: %#v", got)
	}
	if got := encodeDynamicToolPayload(func() {}); got == "" {
		t.Fatal("expected encodeDynamicToolPayload fallback to produce text")
	}

	if got := finalAnswerFromSession(nil); got != "" {
		t.Fatalf("expected empty session answer, got %q", got)
	}
	session := &agentruntime.Session{
		History: []agentruntime.Event{{
			Type:      "item.completed",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Message:   "<proposed_plan>\nShip it.\n</proposed_plan>",
		}},
	}
	if got := finalAnswerFromSession(session); !strings.Contains(got, "<proposed_plan>") {
		t.Fatalf("unexpected session final answer: %q", got)
	}

	workflow := defaultPromptWorkflowForTest()
	workflow.Config.Codex.InitialCollaborationMode = config.InitialCollaborationModePlan
	if !runner.planModeForIssue(workflow, &kanban.Issue{}) {
		t.Fatal("expected plan mode to be detected")
	}
	if got := runner.effectiveInitialCollaborationMode(&kanban.Issue{}); got != "" {
		t.Fatalf("unexpected collaboration mode override: %q", got)
	}
	if got := runner.effectivePermissionProfile(nil); got != kanban.PermissionProfileDefault {
		t.Fatalf("unexpected nil permission profile: %q", got)
	}

	projectIssue := &kanban.Issue{ProjectID: "proj", PermissionProfile: kanban.PermissionProfileFullAccess}
	if got := runner.permissionConfigForIssue(projectIssue, "policy", ""); got.ThreadSandbox != "danger-full-access" {
		t.Fatalf("unexpected full-access permission config: %#v", got)
	}
	projectIssue.PermissionProfile = kanban.PermissionProfilePlanThenFullAccess
	if got := runner.permissionConfigForIssue(projectIssue, "policy", ""); got.InitialCollaborationMode != config.InitialCollaborationModePlan {
		t.Fatalf("unexpected plan/full-access permission config: %#v", got)
	}

	if got := runner.applyIssuePermissionProfile(workflow, projectIssue); got == workflow {
		t.Fatal("expected applyIssuePermissionProfile to clone workflow")
	}

	storedProject, err := runner.store.CreateProject("Prompt project", "description", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if got, err := runner.projectPromptContext(""); err != nil || got["id"] != "" || got["name"] != "" {
		t.Fatalf("unexpected empty project prompt context: %#v %v", got, err)
	}
	if got, err := runner.projectPromptContext(storedProject.ID); err != nil || got["name"] != storedProject.Name {
		t.Fatalf("expected project prompt context to succeed, got %#v %v", got, err)
	}

	if _, err := resolveRepoDefaultBranch(context.Background(), ""); err == nil {
		t.Fatal("expected missing repo path to fail")
	}
	if has, err := repoHasRemote(context.Background(), repoPath, ""); err != nil || has {
		t.Fatalf("expected empty remote name to be false, got %v %v", has, err)
	}
	if has, err := repoHasRemote(context.Background(), repoPath, "origin"); err != nil || has {
		t.Fatalf("expected missing origin remote to be false, got %v %v", has, err)
	}
	runGitForTest(t, repoPath, "remote", "add", "origin", repoPath)
	if has, err := repoHasRemote(context.Background(), repoPath, "origin"); err != nil || !has {
		t.Fatalf("expected origin remote to be detected, got %v %v", has, err)
	}
	if !branchExists(context.Background(), repoPath, "main") {
		t.Fatal("expected main branch to exist")
	}
	if branchExists(context.Background(), repoPath, "missing") {
		t.Fatal("expected missing branch to be absent")
	}
	if !gitRefExists(context.Background(), repoPath, "refs/heads/main") {
		t.Fatal("expected main ref to exist")
	}
	if gitRefExists(context.Background(), repoPath, "refs/heads/missing") {
		t.Fatal("expected missing ref to be absent")
	}
	if branches, err := listLocalBranches(context.Background(), repoPath); err != nil || len(branches) == 0 {
		t.Fatalf("expected local branches, got %#v %v", branches, err)
	}
	if cp := canonicalPath(filepath.Join(repoPath, ".")); cp == "" {
		t.Fatal("expected canonical path to be populated")
	}

	workspaceDir := filepath.Join(workspaceRoot, "helper-paths")
	staleFile := filepath.Join(workspaceDir, "stale.txt")
	if err := os.MkdirAll(filepath.Dir(staleFile), 0o755); err != nil {
		t.Fatalf("MkdirAll stale file path: %v", err)
	}
	if err := os.WriteFile(staleFile, []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile stale file: %v", err)
	}
	prepared, created, err := prepareWorkspaceDir(staleFile, workspaceRoot, true)
	if err != nil {
		t.Fatalf("prepareWorkspaceDir stale file: %v", err)
	}
	if !created || prepared != staleFile {
		t.Fatalf("unexpected prepared stale path: %q created=%v", prepared, created)
	}
	if _, err := os.Stat(staleFile); err != nil {
		t.Fatalf("expected stale file path to become a directory: %v", err)
	}
	if _, _, err := prepareWorkspaceDir(filepath.Join(workspaceRoot, "..", "outside"), workspaceRoot, true); err == nil {
		t.Fatal("expected workspace root escape to fail")
	}

	emptyDir := filepath.Join(workspaceRoot, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll empty dir: %v", err)
	}
	if preserved, changed, err := preserveWorkspaceContents(context.Background(), emptyDir); err != nil || changed || preserved != "" {
		t.Fatalf("expected empty dir to stay in place, got %q %v %v", preserved, changed, err)
	}
	filledDir := filepath.Join(workspaceRoot, "filled")
	if err := os.MkdirAll(filledDir, 0o755); err != nil {
		t.Fatalf("MkdirAll filled dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filledDir, "note.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile filled dir: %v", err)
	}
	preservedPath, changed, err := preserveWorkspaceContents(context.Background(), filledDir)
	if err != nil || !changed || preservedPath == "" {
		t.Fatalf("expected filled dir to be preserved, got %q %v %v", preservedPath, changed, err)
	}
	if _, err := os.Stat(preservedPath); err != nil {
		t.Fatalf("expected preserved path to exist: %v", err)
	}

	gitDirRoot := filepath.Join(workspaceRoot, "gitdir")
	if err := os.MkdirAll(filepath.Join(gitDirRoot, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll .git dir: %v", err)
	}
	if got, err := workspaceGitDir(gitDirRoot); err != nil || !strings.HasSuffix(got, ".git") {
		t.Fatalf("expected workspaceGitDir to read directory, got %q %v", got, err)
	}
	gitFileRoot := filepath.Join(workspaceRoot, "gitfile")
	if err := os.MkdirAll(gitFileRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll gitfile root: %v", err)
	}
	realGitDir := filepath.Join(workspaceRoot, "real-git")
	if err := os.MkdirAll(realGitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll real git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitFileRoot, ".git"), []byte("gitdir: "+realGitDir), 0o644); err != nil {
		t.Fatalf("WriteFile .git file: %v", err)
	}
	if got, err := workspaceGitDir(gitFileRoot); err != nil || got != canonicalPath(realGitDir) {
		t.Fatalf("expected workspaceGitDir to resolve gitfile, got %q %v", got, err)
	}

	if matched, err := workspaceMatchesRepo(context.Background(), gitDirRoot, repoPath); err != nil || matched {
		t.Fatalf("expected mismatched workspace to be false, got %v %v", matched, err)
	}
}

func TestRunnerRuntimeClientAndHookBranches(t *testing.T) {
	runner, store, manager, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project := createWorkspaceProject(t, store, "Platform", repoPath)
	issue, err := store.CreateIssue(project.ID, "", "Runtime client", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	workflow, err := manager.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	// startRuntimeClient error branches.
	if _, err := runner.startRuntimeClient(context.Background(), nil, repoPath, issue, permissionConfig{}); err == nil {
		t.Fatal("expected nil workflow to fail")
	}
	if _, err := runner.startRuntimeClient(context.Background(), workflow, repoPath, nil, permissionConfig{}); err == nil {
		t.Fatal("expected nil issue to fail")
	}

	var (
		sessionSeen     bool
		activitySeen    bool
		interactionSeen bool
		doneSeen        bool
	)
	runner.SetSessionObserver(func(issueID string, session *agentruntime.Session) {
		sessionSeen = issueID == issue.ID && session != nil
	})
	runner.SetActivityObserver(func(issueID string, event agentruntime.ActivityEvent) {
		activitySeen = issueID == issue.ID
	})
	runner.SetInteractionObserver(func(issueID string, interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder) {
		interactionSeen = issueID == issue.ID && interaction != nil
		if responder != nil {
			_ = responder(context.Background(), interaction.ID, agentruntime.PendingInteractionResponse{Decision: "approve"})
		}
	})
	runner.SetInteractionDoneObserver(func(issueID string, interactionID string) {
		doneSeen = issueID == issue.ID && strings.TrimSpace(interactionID) != ""
	})

	runner.runtimeStarter = func(ctx context.Context, request runtimefactory.WorkflowStartRequest, observers agentruntime.Observers) (agentruntime.Client, error) {
		if request.IssueID != issue.ID || request.IssueIdentifier != issue.Identifier {
			return nil, fmt.Errorf("unexpected runtime request: %#v", request)
		}
		if observers.OnSessionUpdate != nil {
			observers.OnSessionUpdate(nil)
			observers.OnSessionUpdate(&agentruntime.Session{SessionID: "session-1"})
		}
		if observers.OnActivityEvent != nil {
			observers.OnActivityEvent(agentruntime.ActivityEvent{})
			observers.OnActivityEvent(agentruntime.ActivityEvent{Type: "started"})
		}
		if observers.OnPendingInteraction != nil {
			observers.OnPendingInteraction(nil, nil)
			observers.OnPendingInteraction(&agentruntime.PendingInteraction{ID: "interaction-1"}, func(context.Context, string, agentruntime.PendingInteractionResponse) error { return nil })
		}
		if observers.OnPendingInteractionDone != nil {
			observers.OnPendingInteractionDone("")
			observers.OnPendingInteractionDone("interaction-1")
		}
		return &noopRuntimeClient{capabilities: agentruntime.Capabilities{Resume: true, LocalImageInput: true}}, nil
	}

	client, err := runner.startRuntimeClient(context.Background(), workflow, repoPath, issue, permissionConfig{
		ApprovalPolicy:           "policy",
		ThreadSandbox:            "workspace-write",
		InitialCollaborationMode: config.InitialCollaborationModePlan,
		TurnSandboxPolicy:        map[string]interface{}{"type": "sandbox"},
	})
	if err != nil {
		t.Fatalf("startRuntimeClient: %v", err)
	}
	if client == nil {
		t.Fatal("expected runtime client")
	}
	if !sessionSeen || !activitySeen || !interactionSeen || !doneSeen {
		t.Fatalf("expected observer callbacks to be invoked, got session=%v activity=%v interaction=%v done=%v", sessionSeen, activitySeen, interactionSeen, doneSeen)
	}

	// runHook branches.
	hookWorkflow := *workflow
	hookWorkflow.Config.Hooks.TimeoutMs = 50
	runner.workflowProvider = staticWorkflowProvider{workflow: &hookWorkflow}

	if err := runner.runHook(context.Background(), repoPath, "", "empty"); err != nil {
		t.Fatalf("runHook empty failed: %v", err)
	}
	if err := runner.runHook(context.Background(), repoPath, "printf ok", "success"); err != nil {
		t.Fatalf("runHook success failed: %v", err)
	}
	if err := runner.runHook(context.Background(), repoPath, "exit 2", "error"); err == nil || !strings.Contains(err.Error(), "workspace hook failed") {
		t.Fatalf("expected failing hook error, got %v", err)
	}
	if err := runner.runHook(context.Background(), repoPath, "sleep 1", "timeout"); err == nil || !strings.Contains(err.Error(), "workspace hook timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestRunnerBranchAndPromptCoverage(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Coverage branches", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	issue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	issue.ResumeThreadID = "resume-thread"

	t.Run("current_workflow_issue", func(t *testing.T) {
		if _, _, err := runner.currentWorkflowIssue(workflow, nil); err == nil {
			t.Fatal("expected nil issue to fail")
		}
		if _, _, err := runner.currentWorkflowIssue(workflow, &kanban.Issue{ID: "missing"}); err == nil {
			t.Fatal("expected missing issue lookup to fail")
		}
		refreshedWorkflow, refreshedIssue, err := runner.currentWorkflowIssue(workflow, issue)
		if err != nil {
			t.Fatalf("currentWorkflowIssue: %v", err)
		}
		if refreshedWorkflow != workflow {
			t.Fatalf("expected workflow to be passed through, got %#v", refreshedWorkflow)
		}
		if refreshedIssue.ResumeThreadID != issue.ResumeThreadID {
			t.Fatalf("expected resume thread to be preserved, got %#v", refreshedIssue)
		}
	})

	t.Run("refresh_for_continuation", func(t *testing.T) {
		otherStore, err := kanban.NewStore(filepath.Join(t.TempDir(), "fallback.db"))
		if err != nil {
			t.Fatalf("NewStore fallback: %v", err)
		}
		t.Cleanup(func() { _ = otherStore.Close() })
		runner.service = providers.NewService(otherStore)

		refreshed, continueRun := runner.refreshForContinuation(workflow, kanban.WorkflowPhaseImplementation, issue.ID)
		if refreshed == nil || !continueRun {
			t.Fatalf("expected fallback refresh to continue, got %#v continue=%v", refreshed, continueRun)
		}
		if refreshed.ID != issue.ID {
			t.Fatalf("expected refreshed issue %s, got %s", issue.ID, refreshed.ID)
		}
		if got := shouldContinueRunPhase(nil, kanban.WorkflowPhaseImplementation, issue); got {
			t.Fatal("expected nil workflow to stop continuation")
		}
		if got := shouldContinueRunPhase(workflow, kanban.WorkflowPhaseImplementation, nil); got {
			t.Fatal("expected nil issue to stop continuation")
		}
		if refreshed, continueRun := runner.refreshForContinuation(workflow, kanban.WorkflowPhaseImplementation, "missing"); refreshed != nil || continueRun {
			t.Fatalf("expected missing issue refresh to stop, got %#v continue=%v", refreshed, continueRun)
		}
	})

	t.Run("resolve_repo_path", func(t *testing.T) {
		got, err := runner.resolveRepoPathForIssue(workflow, issue)
		if err != nil {
			t.Fatalf("resolveRepoPathForIssue project: %v", err)
		}
		if filepath.Clean(got) != filepath.Clean(repoPath) {
			t.Fatalf("expected project repo path %s, got %s", repoPath, got)
		}

		workflowOnly := defaultPromptWorkflowForTest()
		workflowOnly.Path = filepath.Join(t.TempDir(), "nested", "WORKFLOW.md")
		if err := os.MkdirAll(filepath.Dir(workflowOnly.Path), 0o755); err != nil {
			t.Fatalf("MkdirAll workflow dir: %v", err)
		}
		if err := os.WriteFile(workflowOnly.Path, []byte(""), 0o644); err != nil {
			t.Fatalf("WriteFile workflow: %v", err)
		}
		got, err = runner.resolveRepoPathForIssue(workflowOnly, &kanban.Issue{})
		if err != nil {
			t.Fatalf("resolveRepoPathForIssue workflow path: %v", err)
		}
		if filepath.Clean(got) != filepath.Clean(filepath.Dir(workflowOnly.Path)) {
			t.Fatalf("expected workflow repo path %s, got %s", filepath.Dir(workflowOnly.Path), got)
		}
		if _, err := (&Runner{}).resolveRepoPathForIssue(nil, &kanban.Issue{}); err == nil {
			t.Fatal("expected missing repo path to fail")
		}
	})

	t.Run("prompt_and_plan_helpers", func(t *testing.T) {
		if got := runner.permissionConfigForIssue(nil, "policy", ""); got.ThreadSandbox != "workspace-write" || got.InitialCollaborationMode != config.InitialCollaborationModeDefault {
			t.Fatalf("unexpected nil-issue permission config: %#v", got)
		}
		if got := runner.applyIssuePermissionProfile(nil, issue); got != nil {
			t.Fatalf("expected nil workflow to stay nil, got %#v", got)
		}
		if got := runner.effectiveInitialCollaborationMode(&kanban.Issue{CollaborationModeOverride: config.InitialCollaborationModePlan}); got != config.InitialCollaborationModePlan {
			t.Fatalf("expected collaboration override, got %q", got)
		}
		if got := runner.effectiveInitialCollaborationMode(&kanban.Issue{CollaborationModeOverride: " "}); got != "" {
			t.Fatalf("expected blank collaboration override to stay empty, got %q", got)
		}
		if got := sanitizeWorkspaceKey("!!!"); got != "issue" {
			t.Fatalf("expected empty sanitizeWorkspaceKey fallback, got %q", got)
		}
		if got := deterministicIssueBranch(&kanban.Issue{Identifier: " ", BranchName: " "}); got != "codex/issue" {
			t.Fatalf("expected blank branch fallback, got %q", got)
		}
		if got := runner.planModeForIssue(nil, issue); got {
			t.Fatal("expected nil workflow to disable plan mode")
		}
		if got := finalAnswerFromSession(&agentruntime.Session{LastMessage: "<proposed_plan>\nFallback plan\n</proposed_plan>"}); !strings.Contains(got, "Fallback plan") {
			t.Fatalf("expected last-message fallback, got %q", got)
		}
		if requested, err := runner.capturePendingPlanApproval(nil, 1, nil, true); err != nil || requested {
			t.Fatalf("expected nil issue to short-circuit, got %v %v", requested, err)
		}
		if requested, err := runner.capturePendingPlanApproval(issue, 1, nil, false); err != nil || requested {
			t.Fatalf("expected plan mode off to short-circuit, got %v %v", requested, err)
		}
		if requested, err := runner.capturePendingPlanApproval(issue, 1, &agentruntime.Session{LastMessage: "plain text"}, true); err != nil || requested {
			t.Fatalf("expected missing plan block to short-circuit, got %v %v", requested, err)
		}
		if err := runner.clearPendingPlanRevision(issue, 1); err != nil {
			t.Fatalf("clearPendingPlanRevision no-op: %v", err)
		}
		runner.recordPlanRevisionRuntimeEvent(nil, "plan_revision_cleared", 1, nil, "", "")

		badCleanupRunner := *runner
		badCleanupRunner.workflowProvider = staticWorkflowProvider{err: errors.New("workflow unavailable")}
		if err := badCleanupRunner.CleanupWorkspace(context.Background(), issue); err == nil {
			t.Fatal("expected cleanup workflow lookup to fail")
		}
	})
}

func TestRunnerRepoAndWorkspaceCoverage(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Workspace branches", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()
	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace: %v", err)
	}

	t.Run("resolve_default_branch", func(t *testing.T) {
		t.Run("single local branch", func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "repo-single")
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatalf("MkdirAll repo: %v", err)
			}
			if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("repo"), 0o644); err != nil {
				t.Fatalf("WriteFile repo seed: %v", err)
			}
			initGitRepoForTest(t, repo)
			runGitForTest(t, repo, "branch", "-M", "release")

			branch, err := resolveRepoDefaultBranch(context.Background(), repo)
			if err != nil {
				t.Fatalf("resolveRepoDefaultBranch: %v", err)
			}
			if branch != "release" {
				t.Fatalf("expected release branch fallback, got %q", branch)
			}

			baseRef, err := workspaceBootstrapFreshBaseRef(context.Background(), repo, false)
			if err != nil {
				t.Fatalf("workspaceBootstrapFreshBaseRef: %v", err)
			}
			if baseRef != "release" {
				t.Fatalf("expected raw release base ref, got %q", baseRef)
			}
			if baseRef, err := workspaceBootstrapFreshBaseRef(context.Background(), repo, true); err != nil || baseRef != "release" {
				t.Fatalf("expected missing remote ref to keep release, got %q %v", baseRef, err)
			}
			if refreshed, err := refreshRepoForWorkspaceBootstrap(context.Background(), repo); err != nil || refreshed {
				t.Fatalf("expected no-origin refresh to skip, got %v %v", refreshed, err)
			}
			refreshFailRepo := filepath.Join(t.TempDir(), "repo-refresh-fail")
			if err := os.MkdirAll(refreshFailRepo, 0o755); err != nil {
				t.Fatalf("MkdirAll refresh fail repo: %v", err)
			}
			if err := os.WriteFile(filepath.Join(refreshFailRepo, "README.md"), []byte("repo"), 0o644); err != nil {
				t.Fatalf("WriteFile refresh fail seed: %v", err)
			}
			initGitRepoForTest(t, refreshFailRepo)
			runGitForTest(t, refreshFailRepo, "branch", "-M", "release")
			runGitForTest(t, refreshFailRepo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
			if refreshed, err := refreshRepoForWorkspaceBootstrap(context.Background(), refreshFailRepo); err == nil || !refreshed {
				t.Fatalf("expected refresh failure after retries, got %v %v", refreshed, err)
			}
		})

		t.Run("origin refs", func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "repo-origin")
			remote := filepath.Join(t.TempDir(), "remote.git")
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatalf("MkdirAll repo: %v", err)
			}
			if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("repo"), 0o644); err != nil {
				t.Fatalf("WriteFile repo seed: %v", err)
			}
			initGitRepoForTest(t, repo)
			runGitForTest(t, repo, "branch", "-M", "release")
			runGitForTest(t, repo, "checkout", "-b", "feature")
			initBareGitRepoForTest(t, remote)
			runGitForTest(t, repo, "remote", "add", "origin", remote)
			runGitForTest(t, repo, "push", "-u", "origin", "release:main")
			runGitForTest(t, repo, "fetch", "origin")
			runGitForTest(t, repo, "remote", "set-head", "origin", "-d")

			branch, err := resolveRepoDefaultBranch(context.Background(), repo)
			if err != nil {
				t.Fatalf("resolveRepoDefaultBranch: %v", err)
			}
			if branch != "origin/main" {
				t.Fatalf("expected origin/main fallback, got %q", branch)
			}
			baseRef, err := workspaceBootstrapFreshBaseRef(context.Background(), repo, true)
			if err != nil {
				t.Fatalf("workspaceBootstrapFreshBaseRef origin main: %v", err)
			}
			if baseRef != "origin/main" {
				t.Fatalf("expected origin/main base ref, got %q", baseRef)
			}

			runGitForTest(t, repo, "push", "-u", "origin", "release:develop")
			runGitForTest(t, repo, "fetch", "origin")
			runGitForTest(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")

			branch, err = resolveRepoDefaultBranch(context.Background(), repo)
			if err != nil {
				t.Fatalf("resolveRepoDefaultBranch origin HEAD: %v", err)
			}
			if branch != "develop" {
				t.Fatalf("expected origin HEAD to resolve develop, got %q", branch)
			}

			baseRef, err = workspaceBootstrapFreshBaseRef(context.Background(), repo, true)
			if err != nil {
				t.Fatalf("workspaceBootstrapFreshBaseRef origin: %v", err)
			}
			if baseRef != "origin/develop" {
				t.Fatalf("expected origin/develop base ref, got %q", baseRef)
			}
		})

		t.Run("detached head", func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "repo-detached")
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatalf("MkdirAll repo: %v", err)
			}
			if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("repo"), 0o644); err != nil {
				t.Fatalf("WriteFile repo seed: %v", err)
			}
			initGitRepoForTest(t, repo)
			runGitForTest(t, repo, "branch", "-M", "release")
			runGitForTest(t, repo, "checkout", "-b", "feature")
			head := runGitForTest(t, repo, "rev-parse", "HEAD")
			runGitForTest(t, repo, "checkout", "--detach", head)

			branch, err := resolveRepoDefaultBranch(context.Background(), repo)
			if err != nil {
				t.Fatalf("resolveRepoDefaultBranch detached: %v", err)
			}
			if branch != "HEAD" {
				t.Fatalf("expected detached HEAD fallback, got %q", branch)
			}
		})

		if _, err := resolveRepoDefaultBranch(context.Background(), ""); err == nil {
			t.Fatal("expected missing repo path to fail")
		}
	})

	t.Run("repo_and_workspace_helpers", func(t *testing.T) {
		if has, err := repoHasRemote(context.Background(), repoPath, ""); err != nil || has {
			t.Fatalf("expected blank remote name to be false, got %v %v", has, err)
		}
		if has, err := repoHasRemote(context.Background(), filepath.Join(t.TempDir(), "missing"), "origin"); err == nil || has {
			t.Fatalf("expected missing repo path to fail, got %v %v", has, err)
		}
		if got := branchExists(context.Background(), repoPath, ""); got {
			t.Fatal("expected empty branch name to be ignored")
		}
		if got := branchExists(context.Background(), filepath.Join(t.TempDir(), "missing"), "main"); got {
			t.Fatal("expected branchExists to ignore missing repos")
		}
		if got := gitRefExists(context.Background(), repoPath, ""); got {
			t.Fatal("expected empty ref name to be ignored")
		}
		if got := gitRefExists(context.Background(), filepath.Join(t.TempDir(), "missing"), "refs/heads/main"); got {
			t.Fatal("expected gitRefExists to ignore missing repos")
		}
		emptyRepo := filepath.Join(t.TempDir(), "empty")
		if err := os.MkdirAll(emptyRepo, 0o755); err != nil {
			t.Fatalf("MkdirAll empty repo: %v", err)
		}
		runGitForTest(t, emptyRepo, "init")
		if branches, err := listLocalBranches(context.Background(), emptyRepo); err != nil || len(branches) != 0 {
			t.Fatalf("expected empty repo to return no local branches, got %#v %v", branches, err)
		}
		if _, err := listLocalBranches(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("expected listLocalBranches on missing repo to fail")
		}
		if _, err := repoBootstrapLock(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("expected repo bootstrap lock on missing repo to fail")
		}
		refreshRepo := filepath.Join(t.TempDir(), "refresh")
		if err := os.MkdirAll(refreshRepo, 0o755); err != nil {
			t.Fatalf("MkdirAll refresh repo: %v", err)
		}
		if err := os.WriteFile(filepath.Join(refreshRepo, "README.md"), []byte("repo"), 0o644); err != nil {
			t.Fatalf("WriteFile refresh seed: %v", err)
		}
		initGitRepoForTest(t, refreshRepo)
		runGitForTest(t, refreshRepo, "branch", "-M", "release")
		runGitForTest(t, refreshRepo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
		if refreshed, err := refreshRepoForWorkspaceBootstrap(context.Background(), refreshRepo); err == nil || !refreshed {
			t.Fatalf("expected refresh failure to report origin remote attempt, got %v %v", refreshed, err)
		}
		runGitForTest(t, repoPath, "remote", "add", "origin", repoPath)
		if has, err := repoHasRemote(context.Background(), repoPath, "origin"); err != nil || !has {
			t.Fatalf("expected origin remote to exist, got %v %v", has, err)
		}
		if branches, err := listLocalBranches(context.Background(), repoPath); err != nil || len(branches) == 0 {
			t.Fatalf("expected local branches, got %#v %v", branches, err)
		}
		if !branchExists(context.Background(), repoPath, "main") {
			t.Fatal("expected main branch to exist")
		}
		if branchExists(context.Background(), repoPath, "missing") {
			t.Fatal("expected missing branch to be absent")
		}
		if !gitRefExists(context.Background(), repoPath, "refs/heads/main") {
			t.Fatal("expected main ref to exist")
		}
		if gitRefExists(context.Background(), repoPath, "refs/heads/missing") {
			t.Fatal("expected missing ref to be absent")
		}

		plainRoot := filepath.Join(t.TempDir(), "plain")
		if err := os.MkdirAll(plainRoot, 0o755); err != nil {
			t.Fatalf("MkdirAll plainRoot: %v", err)
		}
		if got := pathWithinRoot(filepath.Join(plainRoot, "child"), plainRoot); !got {
			t.Fatal("expected child path to be within root")
		}
		if got := pathWithinRoot(filepath.Join(plainRoot, "..", "outside"), plainRoot); got {
			t.Fatal("expected escaped path to be outside root")
		}
		if _, err := validateWorkspacePath(filepath.Join(plainRoot, "..", "outside"), plainRoot); err == nil {
			t.Fatal("expected root escape to fail")
		}
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatalf("MkdirAll outside: %v", err)
		}
		symlinkPath := filepath.Join(plainRoot, "link")
		if err := os.Symlink(outside, symlinkPath); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		if _, err := validateWorkspacePath(symlinkPath, plainRoot); err == nil {
			t.Fatal("expected workspace symlink escape to fail")
		}

		gitDirRoot := filepath.Join(t.TempDir(), "gitdir")
		if err := os.MkdirAll(filepath.Join(gitDirRoot, ".git"), 0o755); err != nil {
			t.Fatalf("MkdirAll git dir: %v", err)
		}
		if got, err := workspaceGitDir(gitDirRoot); err != nil || !strings.HasSuffix(got, ".git") {
			t.Fatalf("expected directory git metadata, got %q %v", got, err)
		}
		gitFileRoot := filepath.Join(t.TempDir(), "gitfile")
		if err := os.MkdirAll(gitFileRoot, 0o755); err != nil {
			t.Fatalf("MkdirAll git file root: %v", err)
		}
		realGitDir := filepath.Join(gitFileRoot, "nested", "gitdir")
		if err := os.MkdirAll(realGitDir, 0o755); err != nil {
			t.Fatalf("MkdirAll real git dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(gitFileRoot, ".git"), []byte("gitdir: nested/gitdir"), 0o644); err != nil {
			t.Fatalf("WriteFile git file: %v", err)
		}
		if got, err := workspaceGitDir(gitFileRoot); err != nil || got != canonicalPath(realGitDir) {
			t.Fatalf("expected relative gitdir resolution, got %q %v", got, err)
		}
		invalidGitRoot := filepath.Join(t.TempDir(), "invalid")
		if err := os.MkdirAll(invalidGitRoot, 0o755); err != nil {
			t.Fatalf("MkdirAll invalid git root: %v", err)
		}
		if err := os.WriteFile(filepath.Join(invalidGitRoot, ".git"), []byte("broken"), 0o644); err != nil {
			t.Fatalf("WriteFile invalid git metadata: %v", err)
		}
		if _, err := workspaceGitDir(invalidGitRoot); err == nil {
			t.Fatal("expected invalid git metadata to fail")
		}
		symlinkGitRoot := filepath.Join(t.TempDir(), "symlink-git")
		if err := os.MkdirAll(symlinkGitRoot, 0o755); err != nil {
			t.Fatalf("MkdirAll symlink git root: %v", err)
		}
		realGitDirTarget := filepath.Join(t.TempDir(), "real-git-target")
		if err := os.MkdirAll(realGitDirTarget, 0o755); err != nil {
			t.Fatalf("MkdirAll real git target: %v", err)
		}
		if err := os.RemoveAll(filepath.Join(symlinkGitRoot, ".git")); err != nil {
			t.Fatalf("RemoveAll symlink .git: %v", err)
		}
		if err := os.Symlink(realGitDirTarget, filepath.Join(symlinkGitRoot, ".git")); err != nil {
			t.Fatalf("Symlink .git: %v", err)
		}
		if got, err := workspaceGitDir(symlinkGitRoot); err != nil || got != canonicalPath(realGitDirTarget) {
			t.Fatalf("expected symlinked git dir resolution, got %q %v", got, err)
		}
		canonicalTarget := filepath.Join(t.TempDir(), "canonical-target")
		if err := os.MkdirAll(canonicalTarget, 0o755); err != nil {
			t.Fatalf("MkdirAll canonical target: %v", err)
		}
		canonicalLink := filepath.Join(t.TempDir(), "canonical-link")
		if err := os.Symlink(canonicalTarget, canonicalLink); err != nil {
			t.Fatalf("Symlink canonical: %v", err)
		}
		if got := canonicalPath(canonicalLink); got != canonicalPath(canonicalTarget) {
			t.Fatalf("expected canonical symlink resolution, got %q", got)
		}

		if active, err := workspaceHasActiveRebase(workspace.Path); err != nil || active {
			t.Fatalf("expected fresh workspace to have no rebase, got %v %v", active, err)
		}
		gitDir := workspaceGitDirForTest(t, workspace.Path)
		if err := os.MkdirAll(filepath.Join(gitDir, "rebase-merge"), 0o755); err != nil {
			t.Fatalf("MkdirAll rebase state: %v", err)
		}
		if active, err := workspaceHasActiveRebase(workspace.Path); err != nil || !active {
			t.Fatalf("expected rebase state to be detected, got %v %v", active, err)
		}

		preserved, changed, err := preserveWorkspaceContents(context.Background(), workspace.Path)
		if err != nil || changed || preserved != "" {
			t.Fatalf("expected linked worktree to skip preservation, got %q %v %v", preserved, changed, err)
		}
		if preserved, changed, err := preserveWorkspaceContents(context.Background(), filepath.Join(t.TempDir(), "missing")); err != nil || changed || preserved != "" {
			t.Fatalf("expected missing dir to be ignored, got %q %v %v", preserved, changed, err)
		}
		plainDir := filepath.Join(t.TempDir(), "plain-dir")
		if err := os.MkdirAll(plainDir, 0o755); err != nil {
			t.Fatalf("MkdirAll plain dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(plainDir, "note.txt"), []byte("keep"), 0o644); err != nil {
			t.Fatalf("WriteFile plain dir: %v", err)
		}
		preserved, changed, err = preserveWorkspaceContents(context.Background(), plainDir)
		if err != nil || !changed || preserved == "" {
			t.Fatalf("expected non-empty dir to be preserved, got %q %v %v", preserved, changed, err)
		}
		cleanupDir := filepath.Join(t.TempDir(), "cleanup-dir")
		if err := os.MkdirAll(cleanupDir, 0o755); err != nil {
			t.Fatalf("MkdirAll cleanup dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cleanupDir, "note.txt"), []byte("keep"), 0o644); err != nil {
			t.Fatalf("WriteFile cleanup dir: %v", err)
		}

		if linked, err := isLinkedWorktree(context.Background(), workspace.Path); err != nil || !linked {
			t.Fatalf("expected linked worktree, got %v %v", linked, err)
		}
		if err := removeManagedWorkspace(context.Background(), cleanupDir); err != nil {
			t.Fatalf("removeManagedWorkspace plain dir: %v", err)
		}
		if _, err := os.Stat(cleanupDir); !os.IsNotExist(err) {
			t.Fatalf("expected plain dir to be removed, got %v", err)
		}
		if matched, err := workspaceMatchesRepo(context.Background(), workspace.Path, repoPath); err != nil || !matched {
			t.Fatalf("expected workspace to match repo, got %v %v", matched, err)
		}
	})
}

func TestRunnerCommandFlowCoverage(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Command flow", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	issue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	t.Run("pending_commands_for_issue", func(t *testing.T) {
		if _, err := runner.pendingCommandsForIssue("missing"); err == nil {
			t.Fatal("expected missing issue to fail")
		}
		backlog, err := store.CreateIssue(project.ID, "", "Backlog", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue backlog: %v", err)
		}
		if commands, err := runner.pendingCommandsForIssue(backlog.ID); err != nil || commands != nil {
			t.Fatalf("expected backlog issue to skip commands, got %#v %v", commands, err)
		}
		blocker, err := store.CreateIssue(project.ID, "", "Blocker", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocker: %v", err)
		}
		if err := store.UpdateIssueState(blocker.ID, kanban.StateReady); err != nil {
			t.Fatalf("UpdateIssueState blocker: %v", err)
		}
		blocked, err := store.CreateIssue(project.ID, "", "Blocked", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocked: %v", err)
		}
		if err := store.UpdateIssueState(blocked.ID, kanban.StateReady); err != nil {
			t.Fatalf("UpdateIssueState blocked: %v", err)
		}
		if _, err := store.SetIssueBlockers(blocked.ID, []string{blocker.Identifier}); err != nil {
			t.Fatalf("SetIssueBlockers: %v", err)
		}
		if _, err := store.CreateIssueAgentCommand(blocked.ID, "Do not deliver while blocked.", kanban.IssueAgentCommandPending); err != nil {
			t.Fatalf("CreateIssueAgentCommand blocked: %v", err)
		}
		if commands, err := runner.pendingCommandsForIssue(blocked.ID); err != nil || commands != nil {
			t.Fatalf("expected blocked issue to skip commands, got %#v %v", commands, err)
		}
		readyCommandIssue, err := store.CreateIssue(project.ID, "", "Ready command", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue ready command: %v", err)
		}
		if err := store.UpdateIssueState(readyCommandIssue.ID, kanban.StateReady); err != nil {
			t.Fatalf("UpdateIssueState ready command: %v", err)
		}
		if _, err := store.CreateIssueAgentCommand(readyCommandIssue.ID, "Deliver me.", kanban.IssueAgentCommandPending); err != nil {
			t.Fatalf("CreateIssueAgentCommand ready: %v", err)
		}
		if commands, err := runner.pendingCommandsForIssue(readyCommandIssue.ID); err != nil || len(commands) != 1 {
			t.Fatalf("expected pending command delivery, got %#v %v", commands, err)
		}
	})

	t.Run("run_pending_commands", func(t *testing.T) {
		if delivered, err := runner.runPendingCommandsInActiveRuntime(context.Background(), nil, workflow, issue, 1, "title"); err != nil || delivered {
			t.Fatalf("expected nil client to short-circuit, got %v %v", delivered, err)
		}
		if delivered, err := runner.runPendingCommandsInActiveRuntime(context.Background(), &noopRuntimeClient{}, workflow, issue, 1, "title"); err != nil || delivered {
			t.Fatalf("expected resume-less client to short-circuit, got %v %v", delivered, err)
		}
		if delivered, err := runner.runPendingCommandsInActiveRuntime(context.Background(), &deliveringRuntimeClient{capabilities: agentruntime.Capabilities{Resume: true}}, workflow, &kanban.Issue{ID: "missing"}, 1, "title"); err == nil || delivered {
			t.Fatalf("expected missing issue to fail, got %v %v", delivered, err)
		}
		emptyIssue, err := store.CreateIssue(project.ID, "", "No pending commands", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue empty: %v", err)
		}
		if err := store.UpdateIssueState(emptyIssue.ID, kanban.StateReady); err != nil {
			t.Fatalf("UpdateIssueState empty: %v", err)
		}
		emptyIssue, err = store.GetIssue(emptyIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue empty: %v", err)
		}
		emptyClient := &deliveringRuntimeClient{
			capabilities: agentruntime.Capabilities{Resume: true},
			session:      agentruntime.Session{ThreadID: "thread-empty"},
		}
		if delivered, err := runner.runPendingCommandsInActiveRuntime(context.Background(), emptyClient, workflow, emptyIssue, 1, "Empty title"); err != nil || delivered {
			t.Fatalf("expected no pending commands to short-circuit after polling, got %v %v", delivered, err)
		}
		commandIssue, err := store.CreateIssue(project.ID, "", "Deliver command", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue command: %v", err)
		}
		if err := store.UpdateIssueState(commandIssue.ID, kanban.StateReady); err != nil {
			t.Fatalf("UpdateIssueState command: %v", err)
		}
		if _, err := store.CreateIssueAgentCommand(commandIssue.ID, "Deliver me in runtime.", kanban.IssueAgentCommandPending); err != nil {
			t.Fatalf("CreateIssueAgentCommand command: %v", err)
		}
		client := &deliveringRuntimeClient{
			capabilities: agentruntime.Capabilities{Resume: true},
			session:      agentruntime.Session{ThreadID: "thread-deliver"},
		}
		delivered, err := runner.runPendingCommandsInActiveRuntime(context.Background(), client, workflow, commandIssue, 1, "Deliver command")
		if err != nil {
			t.Fatalf("runPendingCommandsInActiveRuntime: %v", err)
		}
		if !delivered {
			t.Fatal("expected pending commands to be delivered in active runtime")
		}
		commands, err := store.ListIssueAgentCommands(commandIssue.ID)
		if err != nil {
			t.Fatalf("ListIssueAgentCommands: %v", err)
		}
		if len(commands) != 1 || commands[0].Status != kanban.IssueAgentCommandDelivered {
			t.Fatalf("expected delivered command, got %+v", commands)
		}
	})

	t.Run("prompt_and_plan_notes", func(t *testing.T) {
		if got := appendWorkspaceRecoveryNote("prompt", ""); got != "prompt" {
			t.Fatalf("unexpected appendWorkspaceRecoveryNote result: %q", got)
		}
		if got := appendWorkspaceRecoveryNote("", "recovery"); got != "recovery" {
			t.Fatalf("unexpected appended recovery note: %q", got)
		}
		if got := prependPlanRevisionNote("prompt", ""); got != "prompt" {
			t.Fatalf("unexpected prependPlanRevisionNote result: %q", got)
		}
		if got := prependPlanRevisionNote("", "rev"); !strings.Contains(got, "Plan revision note:") {
			t.Fatalf("unexpected revision note section: %q", got)
		}
		if got := prependWorkspaceRecoveryNote("prompt", ""); got != "prompt" {
			t.Fatalf("unexpected prependWorkspaceRecoveryNote result: %q", got)
		}
		if got := prependWorkspaceRecoveryNote("", "recovery"); got != "recovery" {
			t.Fatalf("unexpected prepended recovery note: %q", got)
		}
		if got := finalAnswerFromSession(&agentruntime.Session{History: []agentruntime.Event{{Type: "item.completed", ItemType: "agentMessage", ItemPhase: "final_answer", Message: "done"}}}); got != "done" {
			t.Fatalf("unexpected final answer history result: %q", got)
		}
		if got := finalAnswerFromSession(&agentruntime.Session{}); got != "" {
			t.Fatalf("expected empty final answer, got %q", got)
		}
		if got := extractProposedPlanMarkdown("prefix <proposed_plan>\nShip the guarded rollout.\n</proposed_plan> suffix"); got != "Ship the guarded rollout." {
			t.Fatalf("unexpected proposed plan extraction: %q", got)
		}
		if got := extractProposedPlanMarkdown("no plan block here"); got != "" {
			t.Fatalf("expected missing proposed plan block to stay empty, got %q", got)
		}
	})
}

func TestRunPendingCommandsInActiveRuntimeContextCancel(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Cancel pending commands", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	issue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	delivered, err := runner.runPendingCommandsInActiveRuntime(ctx, &deliveringRuntimeClient{capabilities: agentruntime.Capabilities{Resume: true}}, defaultPromptWorkflowForTest(), issue, 1, "Cancel flow")
	if err == nil || !errors.Is(err, context.Canceled) || delivered {
		t.Fatalf("expected canceled runtime command polling to stop, got delivered=%v err=%v", delivered, err)
	}
}

func TestExecuteTurnsReportsCommandDeliveryEventFailure(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Execute turns delivery failure", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	if _, err := store.CreateIssueAgentCommand(issue.ID, "Deliver this command.", kanban.IssueAgentCommandPending); err != nil {
		t.Fatalf("CreateIssueAgentCommand: %v", err)
	}
	blockRuntimeEventsForIssueForTest(t, store.DBPath(), issue.ID)
	issue.WorkflowPhase = "invalid"
	workflow := defaultPromptWorkflowForTest()
	workflow.Config.Agent.MaxTurns = 1
	runner.runtimeStarter = func(context.Context, runtimefactory.WorkflowStartRequest, agentruntime.Observers) (agentruntime.Client, error) {
		return &deliveringRuntimeClient{
			capabilities: agentruntime.Capabilities{Resume: true},
			session:      agentruntime.Session{ThreadID: "thread-delivery"},
		}, nil
	}

	result, err := runner.executeTurns(context.Background(), workflow, "", issue, 0)
	if err != nil {
		t.Fatalf("executeTurns: %v", err)
	}
	if result == nil || result.Error == nil || !strings.Contains(result.Error.Error(), "runtime event failed") {
		t.Fatalf("expected runtime event failure to surface, got %#v", result)
	}
}

func TestExecuteTurnsFailsWhenIssueCannotBeRefreshed(t *testing.T) {
	runner, _, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	workflow := defaultPromptWorkflowForTest()
	issue := &kanban.Issue{ID: "missing", State: kanban.StateReady, WorkflowPhase: "invalid"}

	if result, err := runner.executeTurns(context.Background(), workflow, "", issue, 0); err == nil || result != nil {
		t.Fatalf("expected executeTurns to fail when the issue cannot be refreshed, got result=%#v err=%v", result, err)
	}
}

func TestRunnerRunAttemptErrorBranches(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, err := store.CreateIssue("", "", "No repository path", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	t.Run("workflow_provider_error", func(t *testing.T) {
		runner.workflowProvider = staticWorkflowProvider{err: errors.New("workflow unavailable")}
		if result, err := runner.RunAttempt(context.Background(), issue, 0); err == nil || result != nil {
			t.Fatalf("expected workflow lookup to fail, got result=%#v err=%v", result, err)
		}
	})

	t.Run("missing_repository_path", func(t *testing.T) {
		workflow := defaultPromptWorkflowForTest()
		runner.workflowProvider = staticWorkflowProvider{workflow: workflow}
		if result, err := runner.RunAttempt(context.Background(), issue, 0); err == nil || result != nil {
			t.Fatalf("expected workspace bootstrap to fail, got result=%#v err=%v", result, err)
		}
	})
}

func TestRunnerRunAttemptZeroTurnsBootstrapsWorkspace(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Zero turns", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	issue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue ready: %v", err)
	}
	workflow := defaultPromptWorkflowForTest()
	workflow.Config.Agent.MaxTurns = 0
	runner.workflowProvider = staticWorkflowProvider{workflow: workflow}
	runner.runtimeStarter = func(context.Context, runtimefactory.WorkflowStartRequest, agentruntime.Observers) (agentruntime.Client, error) {
		return &noopRuntimeClient{capabilities: agentruntime.Capabilities{}}, nil
	}

	result, err := runner.RunAttempt(context.Background(), issue, 2)
	if err != nil {
		t.Fatalf("RunAttempt: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful zero-turn run, got %+v", result)
	}
	workspace, err := store.GetWorkspace(issue.ID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if workspace == nil || !strings.Contains(workspace.Path, projectWorkspaceSlug(project)) {
		t.Fatalf("expected workspace to be created, got %+v", workspace)
	}
}

func TestRunnerRunAttemptFailsWhenIssueStateUpdateIsBlocked(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Blocked state update", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	issue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	blockIssueUpdatesForTest(t, store.DBPath(), issue.ID)

	result, err := runner.RunAttempt(context.Background(), issue, 0)
	if err == nil || result != nil {
		t.Fatalf("expected blocked state update to fail, got result=%#v err=%v", result, err)
	}
	if !strings.Contains(err.Error(), "plan revision clear failed") {
		t.Fatalf("expected blocked update error, got %v", err)
	}
}

func TestRunnerRunAttemptAbortsStdioTurnWhenPlanRevisionClearFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stdio shell behavior differs on Windows")
	}

	traceFile := filepath.Join(t.TempDir(), "stdio-turn-started")
	t.Setenv("TRACE_FILE", traceFile)

	runner, store, _, _, repoPath := setupTestRunner(t, `printf started >> "$TRACE_FILE"; cat`, config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Plan revision clear abort", "", 0, nil)
	if err := store.UpdateIssuePermissionProfile(issue.ID, kanban.PermissionProfilePlanThenFullAccess); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"branch_name": deterministicIssueBranch(issue)}); err != nil {
		t.Fatalf("UpdateIssue branch_name: %v", err)
	}
	requestedAt := time.Date(2026, 3, 18, 13, 15, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanRevision(issue.ID, "Tighten the rollout and add a rollback check.", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision: %v", err)
	}
	issue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	blockIssueUpdatesForTest(t, store.DBPath(), issue.ID)

	result, err := runner.Run(context.Background(), issue)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || result.Success {
		t.Fatalf("expected unsuccessful run, got %+v", result)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "plan revision clear failed") {
		t.Fatalf("expected clear failure to surface in run result, got %+v", result)
	}
	if _, statErr := os.Stat(traceFile); statErr == nil {
		t.Fatalf("expected stdio turn not to start, but %s was created", traceFile)
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("expected trace file to be absent, got %v", statErr)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after run: %v", err)
	}
	if updated.PendingPlanRevisionMarkdown != "Tighten the rollout and add a rollback check." || updated.PendingPlanRevisionRequestedAt == nil {
		t.Fatalf("expected pending plan revision to remain queued, got %+v", updated)
	}
}

func TestGetOrCreateWorkspaceInsertFailure(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Workspace insert failure", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()
	blockWorkspaceInsertsForTest(t, store.DBPath(), issue.ID)

	if _, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue); err == nil || !strings.Contains(err.Error(), "workspace insert failed") {
		t.Fatalf("expected workspace insert failure to surface, got %v", err)
	}
}

func TestRunnerAssetCoverage(t *testing.T) {
	if got := stagedIssueAssetFilename(kanban.IssueAsset{ID: "asset-1", Filename: ""}); got != "asset-1-image" {
		t.Fatalf("expected empty filename fallback, got %q", got)
	}
	if got := stagedIssueAssetFilename(kanban.IssueAsset{ID: "asset-2", Filename: "folder\\image.png"}); got != "asset-2-folder_image.png" {
		t.Fatalf("expected filename sanitization, got %q", got)
	}

	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "source.txt")
	dstFile := filepath.Join(dstDir, "target.txt")
	if err := os.WriteFile(srcFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	if err := copyIssueAssetToWorkspace(srcFile, dstFile, "asset"); err != nil {
		t.Fatalf("copyIssueAssetToWorkspace success: %v", err)
	}
	data, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("ReadFile destination: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected copied content, got %q", string(data))
	}
	overwriteDst := filepath.Join(dstDir, "overwrite.txt")
	if err := os.WriteFile(overwriteDst, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile overwrite dst: %v", err)
	}
	if err := copyIssueAssetToWorkspace(srcFile, overwriteDst, "asset"); err != nil {
		t.Fatalf("copyIssueAssetToWorkspace overwrite: %v", err)
	}
	data, err = os.ReadFile(overwriteDst)
	if err != nil {
		t.Fatalf("ReadFile overwrite destination: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected overwritten content, got %q", string(data))
	}
	if err := copyIssueAssetToWorkspace(filepath.Join(srcDir, "missing.txt"), filepath.Join(dstDir, "missing.txt"), "asset"); err == nil {
		t.Fatal("expected missing source copy to fail")
	}
	if err := copyIssueAssetToWorkspace(srcFile, filepath.Join(t.TempDir(), "missing-parent", "target.txt"), "asset"); err == nil {
		t.Fatal("expected missing destination parent to fail")
	}

	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", "", "Asset staging", "", 0, nil)
	if inputs, err := runner.stageIssueAssetsForRuntime(t.TempDir(), issue); err != nil || inputs != nil {
		t.Fatalf("expected empty asset staging to be ignored, got %#v %v", inputs, err)
	}
	image, err := store.CreateIssueAsset(issue.ID, "broken.png", strings.NewReader("not-a-real-image"))
	if err != nil {
		t.Fatalf("CreateIssueAsset: %v", err)
	}
	_, imagePath, err := store.GetIssueAssetContent(issue.ID, image.ID)
	if err != nil {
		t.Fatalf("GetIssueAssetContent: %v", err)
	}
	if err := os.Remove(imagePath); err != nil {
		t.Fatalf("Remove staged asset source: %v", err)
	}
	if _, err := runner.stageIssueAssetsForRuntime(t.TempDir(), issue); err == nil {
		t.Fatal("expected missing asset content to fail")
	}
	stageFailureRoot := t.TempDir()
	stageFailurePath := filepath.Join(stageFailureRoot, filepath.FromSlash(appServerIssueAssetStageDir))
	if err := os.MkdirAll(filepath.Dir(stageFailurePath), 0o755); err != nil {
		t.Fatalf("MkdirAll stage failure parent: %v", err)
	}
	if err := os.WriteFile(stageFailurePath, []byte("file"), 0o644); err != nil {
		t.Fatalf("WriteFile stage failure path: %v", err)
	}
	stageFailureIssue := issue
	if _, err := store.CreateIssueAsset(stageFailureIssue.ID, "cover.png", strings.NewReader("content")); err != nil {
		t.Fatalf("CreateIssueAsset stage failure: %v", err)
	}
	if _, err := runner.stageIssueAssetsForRuntime(stageFailureRoot, stageFailureIssue); err == nil {
		t.Fatal("expected stage directory file conflict to fail")
	}

	successIssue, err := store.CreateIssue(issue.ProjectID, "", "Asset staging success", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue success: %v", err)
	}
	if _, err := store.CreateIssueAsset(successIssue.ID, "screen.png", bytes.NewReader(sampleRunnerPNGBytes())); err != nil {
		t.Fatalf("CreateIssueAsset image: %v", err)
	}
	if _, err := store.CreateIssueAsset(successIssue.ID, "notes.txt", strings.NewReader("plain text asset")); err != nil {
		t.Fatalf("CreateIssueAsset text: %v", err)
	}
	inputs, err := runner.stageIssueAssetsForRuntime(t.TempDir(), successIssue)
	if err != nil {
		t.Fatalf("stageIssueAssetsForRuntime success: %v", err)
	}
	if len(inputs) != 1 {
		t.Fatalf("expected only one image input, got %#v", inputs)
	}
	if inputs[0].Kind != agentruntime.InputItemLocalImage || inputs[0].Name != "screen.png" {
		t.Fatalf("unexpected staged input: %#v", inputs[0])
	}
}

func TestRunnerPromptRenderingBranches(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Prompt render", "Body", 0, nil)
	issue.AgentName = ""
	issue.AgentPrompt = ""
	workflow := defaultPromptWorkflowForTest()

	workflow.PromptTemplate = "{{"
	if _, err := runner.buildTurnPrompt(workflow, issue, 0, 1); err == nil || !strings.Contains(err.Error(), "template_render_error") {
		t.Fatalf("expected template render error, got %v", err)
	}

	workflow = defaultPromptWorkflowForTest()
	workflow.PromptTemplate = ""
	issueCopy := *issue
	issueCopy.ProjectID = project.ID
	prompt, err := runner.buildTurnPrompt(workflow, &issueCopy, 0, 1)
	if err != nil {
		t.Fatalf("buildTurnPrompt empty template: %v", err)
	}
	if !strings.Contains(prompt, "Execution guidance:") {
		t.Fatalf("expected execution guidance fallback, got %q", prompt)
	}
}

func TestRepoBootstrapLockBranches(t *testing.T) {
	lock := newRepoBootstrapLockState()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lock.acquire(ctx); err == nil {
		t.Fatal("expected canceled context to fail acquisition")
	}

	unlock, err := lock.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	waitingCtx, waitingCancel := context.WithCancel(context.Background())
	acquired := make(chan error, 1)
	go func() {
		_, err := lock.acquire(waitingCtx)
		acquired <- err
	}()
	time.Sleep(10 * time.Millisecond)
	waitingCancel()
	if err := <-acquired; err == nil {
		t.Fatal("expected blocked acquisition to fail after cancellation")
	}
	unlock()

	if _, err := lock.acquire(&phasedErrContext{Context: context.Background(), err: context.Canceled}); err == nil {
		t.Fatal("expected post-acquire context cancellation to fail acquisition")
	}

	didPanic := false
	func() {
		defer func() {
			if recover() != nil {
				didPanic = true
			}
		}()
		lock.release()
	}()
	if !didPanic {
		t.Fatal("expected double release to panic")
	}
}

func TestRunAttemptHookAndPlanApprovalBranches(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Hook and plan coverage", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	issue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	t.Run("before_run_hook_error", func(t *testing.T) {
		workflow := defaultPromptWorkflowForTest()
		workflow.Config.Hooks.BeforeRun = "exit 1"
		runner.workflowProvider = staticWorkflowProvider{workflow: workflow}
		runner.runtimeStarter = func(context.Context, runtimefactory.WorkflowStartRequest, agentruntime.Observers) (agentruntime.Client, error) {
			return &noopRuntimeClient{capabilities: agentruntime.Capabilities{}}, nil
		}

		if result, err := runner.RunAttempt(context.Background(), issue, 0); err == nil || result != nil {
			t.Fatalf("expected before_run hook failure, got result=%#v err=%v", result, err)
		}
	})

	t.Run("template_render_error", func(t *testing.T) {
		workflow := defaultPromptWorkflowForTest()
		workflow.PromptTemplate = "{{"
		workflow.Config.Agent.MaxTurns = 1
		workflow.Config.Hooks.BeforeRun = ""
		runner.workflowProvider = staticWorkflowProvider{workflow: workflow}
		runner.runtimeStarter = func(context.Context, runtimefactory.WorkflowStartRequest, agentruntime.Observers) (agentruntime.Client, error) {
			return &noopRuntimeClient{capabilities: agentruntime.Capabilities{}}, nil
		}

		if result, err := runner.RunAttempt(context.Background(), issue, 0); err == nil || result != nil || !strings.Contains(err.Error(), "template_render_error") {
			t.Fatalf("expected template render failure, got result=%#v err=%v", result, err)
		}
	})

	t.Run("plan_approval_requested", func(t *testing.T) {
		workflow := defaultPromptWorkflowForTest()
		workflow.Config.Agent.MaxTurns = 1
		runner.workflowProvider = staticWorkflowProvider{workflow: workflow}
		if err := store.UpdateIssuePermissionProfile(issue.ID, kanban.PermissionProfilePlanThenFullAccess); err != nil {
			t.Fatalf("UpdateIssuePermissionProfile: %v", err)
		}
		issue, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue updated: %v", err)
		}
		runner.runtimeStarter = func(context.Context, runtimefactory.WorkflowStartRequest, agentruntime.Observers) (agentruntime.Client, error) {
			return &deliveringRuntimeClient{
				capabilities: agentruntime.Capabilities{Resume: true, PlanGating: true},
				session: agentruntime.Session{
					ThreadID: "thread-plan",
					TurnID:   "turn-plan",
					History: []agentruntime.Event{{
						Type:      "item.completed",
						ItemType:  "agentMessage",
						ItemPhase: "final_answer",
						Message:   "<proposed_plan>\nShip the plan.\n</proposed_plan>",
					}},
				},
			}, nil
		}

		result, err := runner.RunAttempt(context.Background(), issue, 0)
		if err != nil {
			t.Fatalf("RunAttempt plan approval: %v", err)
		}
		if result == nil || result.StopReason != planApprovalStopReason {
			t.Fatalf("expected plan approval stop, got %#v", result)
		}
		updated, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue after plan approval: %v", err)
		}
		if !updated.PlanApprovalPending || !strings.Contains(updated.PendingPlanMarkdown, "Ship the plan.") {
			t.Fatalf("expected pending plan to be recorded, got %#v", updated)
		}
		events, err := store.ListIssueRuntimeEvents(issue.ID, 20)
		if err != nil {
			t.Fatalf("ListIssueRuntimeEvents: %v", err)
		}
		found := false
		for _, event := range events {
			if event.Kind == "plan_approval_requested" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected plan approval runtime event, got %+v", events)
		}
	})
}

func TestCapturePendingPlanApprovalErrorBranches(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Plan approval error", "", 0, nil)
	blockRuntimeEventsForIssueForTest(t, store.DBPath(), issue.ID)

	requested, err := runner.capturePendingPlanApproval(issue, 1, &agentruntime.Session{
		ThreadID:    "thread-plan",
		TurnID:      "turn-plan",
		LastMessage: "<proposed_plan>\nShip the plan.\n</proposed_plan>",
	}, true)
	if err == nil || requested {
		t.Fatalf("expected runtime event failure, got requested=%v err=%v", requested, err)
	}
}

func TestWorkspacePathUpdateAndCleanupBranches(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Workspace path update", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace initial: %v", err)
	}
	legacyStoredPath := workspace.Path + string(os.PathSeparator) + "."
	if _, err := store.UpdateWorkspacePath(issue.ID, legacyStoredPath); err != nil {
		t.Fatalf("UpdateWorkspacePath legacy path: %v", err)
	}

	reused, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace legacy path: %v", err)
	}
	if reused.Path != workspace.Path {
		t.Fatalf("expected workspace path to normalize back to %s, got %s", workspace.Path, reused.Path)
	}

	otherIssue, err := store.CreateIssue(project.ID, "", "No workspace yet", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	if err := runner.CleanupWorkspace(context.Background(), otherIssue); err != nil {
		t.Fatalf("CleanupWorkspace without workspace: %v", err)
	}
}

func TestGetOrCreateWorkspaceRejectsExistingPathOutsideCurrentRoot(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Workspace root change", "", 0, nil)

	workflow, err := runner.workflowProvider.Current()
	if err != nil {
		t.Fatalf("workflowProvider.Current: %v", err)
	}

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace initial: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, ".git")); err != nil {
		t.Fatalf("expected initial git worktree metadata: %v", err)
	}

	workflow.Config.Workspace.Root = filepath.Join(t.TempDir(), "new-workspace-root")

	if _, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue); err == nil || !strings.Contains(err.Error(), "workspace path escape") {
		t.Fatalf("expected stored workspace path outside the new root to be rejected, got %v", err)
	}
}

func TestGetOrCreateWorkspaceFallsBackAfterInsertError(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Workspace insert fallback", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()
	failWorkspaceInsertsAfterRowForTest(t, store.DBPath(), issue.ID)

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace fallback: %v", err)
	}
	if workspace == nil || workspace.Path == "" {
		t.Fatalf("expected workspace to be recovered from fallback, got %#v", workspace)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, ".git")); err != nil {
		t.Fatalf("expected git worktree metadata in recovered workspace: %v", err)
	}
}

func TestGetOrCreateWorkspaceAfterCreateHookError(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Workspace hook error", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()
	workflow.Config.Hooks.AfterCreate = "exit 1"

	if _, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue); err == nil || !strings.Contains(err.Error(), "workspace hook failed") {
		t.Fatalf("expected after_create hook failure, got %v", err)
	}
}

func TestGetOrCreateWorkspaceFailsWhenRepoBootstrapLockUnavailable(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Workspace lock error", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()

	if _, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue); err != nil {
		t.Fatalf("getOrCreateWorkspace initial: %v", err)
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("LookPath git: %v", err)
	}
	wrapperDir := t.TempDir()
	wrapperPath := filepath.Join(wrapperDir, "git")
	wrapperScript := "#!/bin/sh\nif [ \"$1\" = \"rev-parse\" ] && [ \"$2\" = \"--git-common-dir\" ]; then\n  echo 'fatal: bootstrap lock unavailable' >&2\n  exit 1\nfi\nexec " + shellQuote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), 0o755); err != nil {
		t.Fatalf("WriteFile wrapper: %v", err)
	}
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue); err == nil || !strings.Contains(err.Error(), "workspace_bootstrap") {
		t.Fatalf("expected repo bootstrap lock failure, got %v", err)
	}
}

func TestRunnerHelperBranchCoverage(t *testing.T) {
	t.Run("prepare_workspace_dir_legacy_file", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "legacy-workspace")
		if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
			t.Fatalf("WriteFile legacy path: %v", err)
		}
		got, created, err := prepareWorkspaceDir(path, root, false)
		if err != nil {
			t.Fatalf("prepareWorkspaceDir legacy file: %v", err)
		}
		if !created || got != filepath.Clean(path) {
			t.Fatalf("unexpected prepared legacy path: %q created=%v", got, created)
		}
	})

	t.Run("workspace_git_dir_invalid", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir:"), 0o644); err != nil {
			t.Fatalf("WriteFile invalid git metadata: %v", err)
		}
		if _, err := workspaceGitDir(root); err == nil {
			t.Fatal("expected invalid git metadata to fail")
		}
	})

	t.Run("workspace_git_dir_dangling_symlink", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, ".git")); err != nil {
			t.Fatalf("Symlink git metadata: %v", err)
		}
		if _, err := workspaceGitDir(root); err == nil {
			t.Fatal("expected dangling git metadata symlink to fail")
		}
	})

	t.Run("workspace_has_active_rebase_error", func(t *testing.T) {
		if _, err := workspaceHasActiveRebase(filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("expected missing workspace git metadata to fail")
		}
	})

	t.Run("copy_issue_asset_destination_directory", func(t *testing.T) {
		srcDir := t.TempDir()
		srcFile := filepath.Join(srcDir, "source.txt")
		if err := os.WriteFile(srcFile, []byte("hello"), 0o644); err != nil {
			t.Fatalf("WriteFile source: %v", err)
		}
		dstDir := t.TempDir()
		dstPath := filepath.Join(dstDir, "target.txt")
		if err := os.MkdirAll(dstPath, 0o755); err != nil {
			t.Fatalf("MkdirAll dst path: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dstPath, "child.txt"), []byte("keep"), 0o644); err != nil {
			t.Fatalf("WriteFile dst child: %v", err)
		}
		if err := copyIssueAssetToWorkspace(srcFile, dstPath, "asset"); err == nil {
			t.Fatal("expected destination directory to fail asset copy")
		}
	})

	t.Run("pending_commands_review_state", func(t *testing.T) {
		runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
		_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Review pending commands", "", 0, nil)
		if err := store.UpdateIssueState(issue.ID, kanban.StateInReview); err != nil {
			t.Fatalf("UpdateIssueState: %v", err)
		}
		if _, err := store.CreateIssueAgentCommand(issue.ID, "Review command", kanban.IssueAgentCommandPending); err != nil {
			t.Fatalf("CreateIssueAgentCommand: %v", err)
		}
		commands, err := runner.pendingCommandsForIssue(issue.ID)
		if err != nil {
			t.Fatalf("pendingCommandsForIssue: %v", err)
		}
		if len(commands) != 1 {
			t.Fatalf("expected one pending command, got %#v", commands)
		}
	})
}

func TestWorkspaceReuseSwitchesToExistingBranch(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Reuse existing branch", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace initial: %v", err)
	}
	existingBranch := "feature/existing-target"
	runGitForTest(t, repoPath, "branch", existingBranch, "main")
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"branch_name": existingBranch}); err != nil {
		t.Fatalf("UpdateIssue branch_name: %v", err)
	}
	updatedIssue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue updated: %v", err)
	}

	reused, err := runner.getOrCreateWorkspace(context.Background(), workflow, updatedIssue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace existing branch: %v", err)
	}
	if reused.Path != workspace.Path {
		t.Fatalf("expected reused workspace path %s, got %s", workspace.Path, reused.Path)
	}
	if got := runGitForTest(t, reused.Path, "branch", "--show-current"); got != existingBranch {
		t.Fatalf("expected workspace branch %s, got %q", existingBranch, got)
	}
}

func TestWorkspaceReuseCreatesBranchFromDetachedHead(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Reuse detached head branch", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace initial: %v", err)
	}
	runGitForTest(t, workspace.Path, "checkout", "--detach", "HEAD")
	targetBranch := "feature/detached-switch"
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"branch_name": targetBranch}); err != nil {
		t.Fatalf("UpdateIssue branch_name: %v", err)
	}
	updatedIssue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue updated: %v", err)
	}

	reused, err := runner.getOrCreateWorkspace(context.Background(), workflow, updatedIssue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace detached head: %v", err)
	}
	if reused.Path != workspace.Path {
		t.Fatalf("expected reused workspace path %s, got %s", workspace.Path, reused.Path)
	}
	if got := runGitForTest(t, reused.Path, "branch", "--show-current"); got != targetBranch {
		t.Fatalf("expected workspace branch %s, got %q", targetBranch, got)
	}
}

func TestWorkspaceBootstrapUsesExistingBranchWithoutRefresh(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Existing branch bootstrap", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()
	branchName := deterministicIssueBranch(issue)
	runGitForTest(t, repoPath, "branch", branchName, "main")

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace existing branch bootstrap: %v", err)
	}
	if got := runGitForTest(t, workspace.Path, "branch", "--show-current"); got != branchName {
		t.Fatalf("expected workspace branch %s, got %q", branchName, got)
	}
}

func TestWorkspaceReuseRenamesMissingBranch(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Rename missing branch", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace initial: %v", err)
	}
	targetBranch := "feature/renamed-target"
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"branch_name": targetBranch}); err != nil {
		t.Fatalf("UpdateIssue branch_name: %v", err)
	}
	updatedIssue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue updated: %v", err)
	}

	reused, err := runner.getOrCreateWorkspace(context.Background(), workflow, updatedIssue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace renamed branch: %v", err)
	}
	if reused.Path != workspace.Path {
		t.Fatalf("expected reused workspace path %s, got %s", workspace.Path, reused.Path)
	}
	if got := runGitForTest(t, reused.Path, "branch", "--show-current"); got != targetBranch {
		t.Fatalf("expected workspace branch %s, got %q", targetBranch, got)
	}
}

func TestWorkspaceReuseRecordsBranchSwitchBlockedRecovery(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Blocked branch recovery", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()

	workspace, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace initial: %v", err)
	}
	targetBranch := "feature/recovery-blocked"
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"branch_name": targetBranch}); err != nil {
		t.Fatalf("UpdateIssue branch_name: %v", err)
	}
	updatedIssue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue updated: %v", err)
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("LookPath git: %v", err)
	}
	wrapperDir := t.TempDir()
	wrapperPath := filepath.Join(wrapperDir, "git")
	wrapperScript := "#!/bin/sh\nif [ \"$1\" = \"branch\" ] && [ \"$2\" = \"-m\" ]; then\n  echo 'fatal: cannot switch branch while rebasing' >&2\n  exit 1\nfi\nexec " + shellQuote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), 0o755); err != nil {
		t.Fatalf("WriteFile wrapper: %v", err)
	}
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	reused, err := runner.getOrCreateWorkspace(context.Background(), workflow, updatedIssue)
	if err != nil {
		t.Fatalf("getOrCreateWorkspace blocked branch recovery: %v", err)
	}
	if reused.Path != workspace.Path {
		t.Fatalf("expected reused workspace path %s, got %s", workspace.Path, reused.Path)
	}
	if got := runGitForTest(t, reused.Path, "branch", "--show-current"); got == targetBranch {
		t.Fatalf("expected branch switch to remain blocked, got %q", got)
	}
	events, err := store.ListIssueRuntimeEvents(issue.ID, 20)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "workspace_bootstrap_recovery" {
			if event.Payload["recovery_reason"] != "branch_switch_blocked" {
				t.Fatalf("expected blocked branch recovery reason, got %#v", event.Payload)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected workspace_bootstrap_recovery event, got %+v", events)
	}
}

func TestWorkspaceReuseFailsWhenCurrentBranchCannotBeRead(t *testing.T) {
	runner, store, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", repoPath, "Unreadable current branch", "", 0, nil)
	workflow := defaultPromptWorkflowForTest()

	if _, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue); err != nil {
		t.Fatalf("getOrCreateWorkspace initial: %v", err)
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("LookPath git: %v", err)
	}
	wrapperDir := t.TempDir()
	wrapperPath := filepath.Join(wrapperDir, "git")
	wrapperScript := "#!/bin/sh\nif [ \"$1\" = \"branch\" ] && [ \"$2\" = \"--show-current\" ]; then\n  echo 'fatal: branch lookup failed' >&2\n  exit 1\nfi\nexec " + shellQuote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), 0o755); err != nil {
		t.Fatalf("WriteFile wrapper: %v", err)
	}
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := runner.getOrCreateWorkspace(context.Background(), workflow, issue); err == nil || !strings.Contains(err.Error(), "workspace_bootstrap") {
		t.Fatalf("expected unreadable branch to fail workspace bootstrap, got %v", err)
	}
	events, err := store.ListIssueRuntimeEvents(issue.ID, 20)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "workspace_bootstrap_failed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected workspace_bootstrap_failed event, got %+v", events)
	}
}

func TestWorkspaceRecoveryNoteForPathBranches(t *testing.T) {
	if note, err := workspaceRecoveryNoteForPath(""); err != nil || note != "" {
		t.Fatalf("expected empty recovery note for blank path, got %q %v", note, err)
	}
	if _, err := workspaceRecoveryNoteForPath(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected missing workspace path to fail")
	}
}

func TestResolveRepoDefaultBranchOnEmptyRepo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "empty-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("MkdirAll repo: %v", err)
	}
	runGitForTest(t, repo, "init")

	branch, err := resolveRepoDefaultBranch(context.Background(), repo)
	if err != nil {
		t.Fatalf("resolveRepoDefaultBranch: %v", err)
	}
	if branch == "" {
		t.Fatal("expected a default branch name for an empty repo")
	}
}

func TestRefreshRepoForWorkspaceBootstrapIgnoresSetHeadFailure(t *testing.T) {
	_, _, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	remotePath := filepath.Join(t.TempDir(), "origin.git")
	initBareGitRepoForTest(t, remotePath)
	runGitForTest(t, repoPath, "remote", "add", "origin", remotePath)
	runGitForTest(t, repoPath, "push", "-u", "origin", "main")

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("LookPath git: %v", err)
	}
	wrapperDir := t.TempDir()
	wrapperPath := filepath.Join(wrapperDir, "git")
	wrapperScript := "#!/bin/sh\nif [ \"$1\" = \"remote\" ] && [ \"$2\" = \"set-head\" ]; then\n  exit 1\nfi\nexec " + shellQuote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), 0o755); err != nil {
		t.Fatalf("WriteFile wrapper: %v", err)
	}
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	refreshed, err := refreshRepoForWorkspaceBootstrap(context.Background(), repoPath)
	if err != nil {
		t.Fatalf("refreshRepoForWorkspaceBootstrap: %v", err)
	}
	if !refreshed {
		t.Fatal("expected origin refresh to report refreshed refs")
	}
}

func TestRefreshRepoForWorkspaceBootstrapContextCancel(t *testing.T) {
	_, _, _, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	remotePath := filepath.Join(t.TempDir(), "origin.git")
	initBareGitRepoForTest(t, remotePath)
	runGitForTest(t, repoPath, "remote", "add", "origin", remotePath)
	runGitForTest(t, repoPath, "push", "-u", "origin", "main")

	badRemote := filepath.Join(t.TempDir(), "missing.git")
	runGitForTest(t, repoPath, "remote", "set-url", "origin", badRemote)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	refreshed, err := refreshRepoForWorkspaceBootstrap(ctx, repoPath)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled refresh to fail fast, got refreshed=%v err=%v", refreshed, err)
	}
}

func TestPrepareTurnPromptWithWorkspacePlanModeBranches(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	workflow := defaultPromptWorkflowForTest()
	workflow.Config.Codex.InitialCollaborationMode = config.InitialCollaborationModePlan
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", "", "Plan mode prompt", "Cover planning guidance", 0, nil)
	issue.State = kanban.StateInProgress
	issue.WorkflowPhase = kanban.WorkflowPhaseImplementation
	issue.PlanApprovalPending = true
	issue.PendingPlanRevisionMarkdown = "Refine the steps"
	issue.PendingPlanRevisionRequestedAt = ptrTime(time.Now())

	prepared, err := runner.prepareTurnPromptWithWorkspace(workflow, issue, 0, 1, "")
	if err != nil {
		t.Fatalf("prepareTurnPromptWithWorkspace plan mode: %v", err)
	}
	if !strings.Contains(prepared.Prompt, "Planning guidance:") {
		t.Fatalf("expected planning guidance in prompt, got %q", prepared.Prompt)
	}
	if !strings.Contains(prepared.Prompt, "Plan revision note:") {
		t.Fatalf("expected plan revision note in prompt, got %q", prepared.Prompt)
	}

	continuation, err := runner.prepareTurnPromptWithWorkspace(workflow, issue, 0, 2, "")
	if err != nil {
		t.Fatalf("prepareTurnPromptWithWorkspace continuation: %v", err)
	}
	if !strings.Contains(continuation.Prompt, "Continuation guidance:") {
		t.Fatalf("expected continuation guidance in prompt, got %q", continuation.Prompt)
	}

	workflow.PromptTemplate = ""
	plainIssue := *issue
	plainIssue.PlanApprovalPending = false
	plainIssue.PendingPlanRevisionMarkdown = ""
	plainIssue.PendingPlanRevisionRequestedAt = nil
	plainPrompt, err := runner.prepareTurnPromptWithWorkspace(workflow, &plainIssue, 0, 1, "")
	if err != nil {
		t.Fatalf("prepareTurnPromptWithWorkspace empty plan prompt: %v", err)
	}
	if !strings.Contains(plainPrompt.Prompt, "Planning guidance:") {
		t.Fatalf("expected planning guidance fallback in prompt, got %q", plainPrompt.Prompt)
	}
}

func TestPrepareTurnPromptWithWorkspaceRecoveryError(t *testing.T) {
	runner, store, _, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	workflow := defaultPromptWorkflowForTest()
	_, issue := createWorkspaceProjectIssue(t, store, "Platform", "", "Prompt recovery error", "Cover recovery errors", 0, nil)

	if _, err := runner.prepareTurnPromptWithWorkspace(workflow, issue, 0, 1, filepath.Join(t.TempDir(), "missing-workspace")); err == nil {
		t.Fatal("expected missing workspace recovery lookup to fail")
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
