package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
	"github.com/olhapi/maestro/pkg/config"
)

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
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 1000
  turn_timeout_ms: 10000
---
Issue {{ issue.identifier }} {{ issue.title }}{% if attempt %} retry {{ attempt }}{% endif %}
`
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0o644); err != nil {
		t.Fatalf("Failed to write workflow: %v", err)
	}

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
	workspace, err := runner.getOrCreateWorkspace(workflow, issue)
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	expectedPath := filepath.Join(workspaceRoot, issue.Identifier)
	if workspace.Path != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, workspace.Path)
	}
}

func TestBuildTurnPrompt(t *testing.T) {
	runner, store, manager, _, _ := setupTestRunner(t, "cat", config.AgentModeStdio)
	issue, _ := store.CreateIssue("", "", "Fix Login Bug", "Users cannot log in", 1, []string{"bug", "urgent"})
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

func TestApplyProjectPermissionProfileOverridesWorkflowSandboxForFullAccess(t *testing.T) {
	runner, store, manager, _, repoPath := setupTestRunner(t, "cat", config.AgentModeStdio)
	project, err := store.CreateProject("Platform", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := store.UpdateProjectPermissionProfile(project.ID, kanban.ProjectPermissionProfileFullAccess); err != nil {
		t.Fatalf("UpdateProjectPermissionProfile: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Sandbox override", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	workflow, err := manager.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	overridden, err := runner.applyProjectPermissionProfile(workflow, issue)
	if err != nil {
		t.Fatalf("applyProjectPermissionProfile: %v", err)
	}
	if overridden.Config.Codex.ThreadSandbox != "danger-full-access" {
		t.Fatalf("expected danger-full-access thread sandbox, got %q", overridden.Config.Codex.ThreadSandbox)
	}
	if overridden.Config.Codex.TurnSandboxPolicy["type"] != "dangerFullAccess" {
		t.Fatalf("expected dangerFullAccess turn policy, got %#v", overridden.Config.Codex.TurnSandboxPolicy)
	}
	if workflow.Config.Codex.ThreadSandbox != "workspace-write" {
		t.Fatalf("expected source workflow to remain unchanged, got %q", workflow.Config.Codex.ThreadSandbox)
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
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
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

	time.Sleep(100 * time.Millisecond)
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

	ws1, _ := runner.getOrCreateWorkspace(workflow, issue)
	ws2, _ := runner.getOrCreateWorkspace(workflow, issue)
	if ws1.Path != ws2.Path {
		t.Error("Expected deterministic workspace path")
	}
	expected := filepath.Join(workspaceRoot, sanitizeWorkspaceKey(issue.Identifier))
	if ws1.Path != expected {
		t.Errorf("Expected path %s, got %s", expected, ws1.Path)
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
	ws, err := runner.getOrCreateWorkspace(workflow, issue)
	if err != nil {
		t.Fatalf("expected workspace recovery, got err: %v", err)
	}
	fi, err := os.Stat(ws.Path)
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected workspace dir at %s", ws.Path)
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
	ws, err := runner.getOrCreateWorkspace(workflow, issue)
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

func TestRunAgentAppServerStagesIssueImagesOnFirstFreshTurn(t *testing.T) {
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
	issue, _ := store.CreateIssue("", "", "Issue images", "", 0, nil)

	imageOne, err := store.CreateIssueImage(issue.ID, "screen-one.png", bytes.NewReader(sampleRunnerPNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueImage imageOne: %v", err)
	}
	imageTwo, err := store.CreateIssueImage(issue.ID, "screen-two.png", bytes.NewReader(sampleRunnerPNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueImage imageTwo: %v", err)
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
	for _, image := range []kanban.IssueImage{*imageOne, *imageTwo} {
		stagedPath := filepath.Join(workspace.Path, filepath.FromSlash(appServerIssueImageStageDir), image.ID+"-"+image.Filename)
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
	if threadConfig["initial_collaboration_mode"] != config.InitialCollaborationModePlan {
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
	for idx, image := range []kanban.IssueImage{*imageOne, *imageTwo} {
		item, _ := input[idx+1].(map[string]interface{})
		expectedPath := filepath.ToSlash(filepath.Join(".maestro", "issue-images", image.ID+"-"+image.Filename))
		if item["type"] != "localImage" || item["path"] != expectedPath || item["name"] != image.Filename {
			t.Fatalf("unexpected image input %d: %#v", idx, item)
		}
		if path, _ := item["path"].(string); filepath.IsAbs(path) {
			t.Fatalf("expected workspace-relative image path, got %q", path)
		}
	}
}

func TestRunAgentAppServerWithoutIssueImagesSendsTextOnly(t *testing.T) {
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
	issue, _ := store.CreateIssue("", "", "No images", "", 0, nil)

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

func TestRunAgentAppServerFailsWhenIssueImageStagingFails(t *testing.T) {
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

	image, err := store.CreateIssueImage(issue.ID, "broken.png", bytes.NewReader(sampleRunnerPNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueImage: %v", err)
	}
	_, imagePath, err := store.GetIssueImageContent(issue.ID, image.ID)
	if err != nil {
		t.Fatalf("GetIssueImageContent: %v", err)
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

func TestRunAgentAppServerDoesNotResendIssueImagesOnContinuationTurn(t *testing.T) {
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
	issue, _ := store.CreateIssue("", "", "Continuation images", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	issue, _ = store.GetIssue(issue.ID)
	if _, err := store.CreateIssueImage(issue.ID, "continue.png", bytes.NewReader(sampleRunnerPNGBytes())); err != nil {
		t.Fatalf("CreateIssueImage: %v", err)
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

func TestRunAgentAppServerResumedThreadIncludesIssueImageInputsOnFirstTurn(t *testing.T) {
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
	issue, _ := store.CreateIssue("", "", "Resumed images", "", 0, nil)
	image, err := store.CreateIssueImage(issue.ID, "resume.png", bytes.NewReader(sampleRunnerPNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueImage: %v", err)
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
	stagedPath := filepath.Join(workspace.Path, filepath.FromSlash(appServerIssueImageStageDir), image.ID+"-"+image.Filename)
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
	expectedPath := filepath.ToSlash(filepath.Join(".maestro", "issue-images", image.ID+"-"+image.Filename))
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
