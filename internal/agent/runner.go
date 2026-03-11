package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/providers"
	"github.com/olhapi/maestro/pkg/config"
)

type WorkflowProvider interface {
	Current() (*config.Workflow, error)
}

type Runner struct {
	workflowProvider WorkflowProvider
	store            *kanban.Store
	service          *providers.Service
	extensions       *extensions.Registry
	sessionObserver  func(issueID string, session *appserver.Session)
	activityObserver func(issueID string, event appserver.ActivityEvent)
}

type RunResult struct {
	Success    bool
	Output     string
	Error      error
	AppSession *appserver.Session
}

type preparedTurnPrompt struct {
	Prompt   string
	Commands []kanban.IssueAgentCommand
}

const firstTurnExecutionGuidance = `
Execution guidance:

- Act on the issue instead of restating the task before doing work.
- Prefer deterministic local verification first: existing tests, targeted shell commands, HTTP checks, and file/content inspection.
- Use browser automation only when the issue explicitly requires browser interaction or local shell checks cannot validate the result.
- For static or local web pages, verify with local commands before considering browser tooling.
- If a verification path is blocked by local environment issues such as browser-session conflicts, stop retrying that path and choose another deterministic local check.
`

const activeThreadCommandPollWindow = 250 * time.Millisecond

func NewRunner(provider WorkflowProvider, store *kanban.Store) *Runner {
	return NewRunnerWithExtensions(provider, store, nil)
}

func NewRunnerWithExtensions(provider WorkflowProvider, store *kanban.Store, registry *extensions.Registry) *Runner {
	if registry == nil {
		registry = extensions.EmptyRegistry()
	}
	return &Runner{workflowProvider: provider, store: store, service: providers.NewService(store), extensions: registry}
}

func (r *Runner) SetSessionObserver(observer func(issueID string, session *appserver.Session)) {
	r.sessionObserver = observer
}

func (r *Runner) SetActivityObserver(observer func(issueID string, event appserver.ActivityEvent)) {
	r.activityObserver = observer
}

func (r *Runner) Run(ctx context.Context, issue *kanban.Issue) (*RunResult, error) {
	return r.RunAttempt(ctx, issue, 0)
}

func (r *Runner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*RunResult, error) {
	workflow, err := r.workflowProvider.Current()
	if err != nil {
		return nil, err
	}

	workspace, err := r.getOrCreateWorkspace(workflow, issue)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	if err := r.runHook(ctx, workspace.Path, workflow.Config.Hooks.BeforeRun, "before_run"); err != nil {
		return nil, err
	}

	if issue.State == kanban.StateReady {
		if _, err := r.service.SetIssueState(ctx, issue.Identifier, string(kanban.StateInProgress)); err != nil {
			return nil, err
		}
	}

	result, runErr := r.executeTurns(ctx, workflow, workspace.Path, issue, attempt)

	_ = r.runHook(ctx, workspace.Path, workflow.Config.Hooks.AfterRun, "after_run")
	_ = r.store.UpdateWorkspaceRun(issue.ID)

	if runErr != nil {
		return result, runErr
	}
	return result, nil
}

func (r *Runner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	workflow, err := r.workflowProvider.Current()
	if err != nil {
		return err
	}
	workspace, err := r.store.GetWorkspace(issue.ID)
	if err != nil {
		return nil
	}
	if err := r.runHook(ctx, workspace.Path, workflow.Config.Hooks.BeforeRemove, "before_remove"); err != nil {
		return err
	}
	return r.store.DeleteWorkspace(issue.ID)
}

func sanitizeWorkspaceKey(identifier string) string {
	re := regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	out := re.ReplaceAllString(identifier, "_")
	out = strings.Trim(out, "._")
	if out == "" {
		return "issue"
	}
	return out
}

func (r *Runner) getOrCreateWorkspace(workflow *config.Workflow, issue *kanban.Issue) (*kanban.Workspace, error) {
	rootAbs, err := filepath.Abs(workflow.Config.Workspace.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}

	workspacePath := filepath.Join(rootAbs, sanitizeWorkspaceKey(issue.Identifier))
	if existing, err := r.store.GetWorkspace(issue.ID); err == nil {
		preparedPath, createdNow, err := prepareWorkspaceDir(existing.Path, rootAbs)
		if err != nil {
			return nil, err
		}
		if preparedPath != existing.Path {
			existing, err = r.store.UpdateWorkspacePath(issue.ID, preparedPath)
			if err != nil {
				return nil, err
			}
		}
		if createdNow {
			if err := r.runHook(context.Background(), preparedPath, workflow.Config.Hooks.AfterCreate, "after_create"); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}

	preparedPath, createdNow, err := prepareWorkspaceDir(workspacePath, rootAbs)
	if err != nil {
		return nil, err
	}

	workspace, err := r.store.CreateWorkspace(issue.ID, preparedPath)
	if err != nil {
		if existing, gerr := r.store.GetWorkspace(issue.ID); gerr == nil {
			workspace = existing
		} else {
			return nil, err
		}
	}
	if createdNow {
		if err := r.runHook(context.Background(), preparedPath, workflow.Config.Hooks.AfterCreate, "after_create"); err != nil {
			return nil, err
		}
	}
	return workspace, nil
}

func prepareWorkspaceDir(path, rootAbs string) (string, bool, error) {
	workspacePath, err := filepath.Abs(path)
	if err != nil {
		return "", false, fmt.Errorf("resolve workspace path: %w", err)
	}
	if !pathWithinRoot(workspacePath, rootAbs) {
		return "", false, fmt.Errorf("workspace path escape: %s outside %s", workspacePath, rootAbs)
	}
	if fi, err := os.Lstat(workspacePath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(workspacePath)
			if err != nil {
				return "", false, fmt.Errorf("workspace symlink check failed: %w", err)
			}
			resolvedAbs, err := filepath.Abs(resolved)
			if err != nil {
				return "", false, fmt.Errorf("resolve workspace symlink: %w", err)
			}
			if !pathWithinRoot(resolvedAbs, rootAbs) {
				return "", false, fmt.Errorf("workspace symlink escape: %s outside %s", resolvedAbs, rootAbs)
			}
		}
		if !fi.IsDir() {
			if err := os.Remove(workspacePath); err != nil {
				return "", false, fmt.Errorf("remove stale workspace path: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}

	createdNow := false
	if _, err := os.Stat(workspacePath); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(workspacePath, 0o755); err != nil {
			return "", false, fmt.Errorf("failed to create workspace directory: %w", err)
		}
		createdNow = true
	} else if err != nil {
		return "", false, err
	}

	return workspacePath, createdNow, nil
}

func pathWithinRoot(path, rootAbs string) bool {
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func (r *Runner) executeTurns(ctx context.Context, workflow *config.Workflow, workspacePath string, issue *kanban.Issue, attempt int) (*RunResult, error) {
	var allOutput strings.Builder
	mode := strings.ToLower(strings.TrimSpace(workflow.Config.Agent.Mode))
	if mode == config.AgentModeAppServer {
		return r.executeAppServerTurns(ctx, workflow, workspacePath, issue, attempt, &allOutput)
	}
	return r.executeStdioTurns(ctx, workflow, workspacePath, issue, attempt, &allOutput)
}

func (r *Runner) executeStdioTurns(ctx context.Context, workflow *config.Workflow, workspacePath string, issue *kanban.Issue, attempt int, allOutput *strings.Builder) (*RunResult, error) {
	for turn := 1; turn <= workflow.Config.Agent.MaxTurns; turn++ {
		prepared, err := r.prepareTurnPrompt(workflow, issue, attempt, turn)
		if err != nil {
			return nil, err
		}
		out, err := r.executeStdioTurn(ctx, workspacePath, workflow.Config.Codex.Command, prepared.Prompt, workflow.Config.Codex.TurnTimeoutMs)
		if out != "" {
			if allOutput.Len() > 0 {
				allOutput.WriteString("\n")
			}
			allOutput.WriteString(out)
		}
		if err != nil {
			return &RunResult{Success: false, Output: allOutput.String(), Error: err}, nil
		}
		if err := r.markDeliveredCommands(issue, prepared.Commands, "next_run", "", attempt); err != nil {
			return &RunResult{Success: false, Output: allOutput.String(), Error: err}, nil
		}

		refreshed, continueRun := r.refreshForContinuation(workflow, issue.ID)
		if !continueRun {
			return &RunResult{Success: true, Output: allOutput.String()}, nil
		}
		issue = refreshed
	}
	return &RunResult{Success: true, Output: allOutput.String()}, nil
}

func (r *Runner) executeAppServerTurns(ctx context.Context, workflow *config.Workflow, workspacePath string, issue *kanban.Issue, attempt int, allOutput *strings.Builder) (*RunResult, error) {
	client, err := appserver.Start(ctx, appserver.ClientConfig{
		Executable:        "sh",
		Args:              []string{"-lc", workflow.Config.Codex.Command},
		Env:               os.Environ(),
		Workspace:         workspacePath,
		WorkspaceRoot:     workflow.Config.Workspace.Root,
		IssueID:           issue.ID,
		IssueIdentifier:   issue.Identifier,
		CodexCommand:      workflow.Config.Codex.Command,
		ExpectedVersion:   workflow.Config.Codex.ExpectedVersion,
		ApprovalPolicy:    workflow.Config.Codex.ApprovalPolicy,
		ThreadSandbox:     workflow.Config.Codex.ThreadSandbox,
		TurnSandboxPolicy: workflow.Config.Codex.TurnSandboxPolicy,
		ReadTimeout:       time.Duration(workflow.Config.Codex.ReadTimeoutMs) * time.Millisecond,
		TurnTimeout:       time.Duration(workflow.Config.Codex.TurnTimeoutMs) * time.Millisecond,
		StallTimeout:      time.Duration(workflow.Config.Codex.StallTimeoutMs) * time.Millisecond,
		DynamicTools:      r.extensions.Specs(),
		ToolExecutor:      r.extensionToolExecutor(),
		ResumeThreadID:    strings.TrimSpace(issue.ResumeThreadID),
		ResumeSource:      "orphaned_run_recovery",
		OnSessionUpdate: func(session *appserver.Session) {
			if r.sessionObserver == nil || issue == nil || session == nil {
				return
			}
			r.sessionObserver(issue.ID, session)
		},
		OnActivityEvent: func(event appserver.ActivityEvent) {
			if r.activityObserver == nil || issue == nil {
				return
			}
			r.activityObserver(issue.ID, event)
		},
	})
	if err != nil {
		return &RunResult{Success: false, Error: err}, nil
	}
	defer client.Close()

	for turn := 1; turn <= workflow.Config.Agent.MaxTurns; turn++ {
		prepared, err := r.prepareTurnPrompt(workflow, issue, attempt, turn)
		if err != nil {
			return nil, err
		}
		title := strings.TrimSpace(fmt.Sprintf("%s: %s", issue.Identifier, issue.Title))
		if title == ":" {
			title = "Maestro turn"
		}
		var deliverErr error
		if err := client.RunTurnWithStartCallback(ctx, prepared.Prompt, title, func(session *appserver.Session) {
			deliverErr = r.markDeliveredCommands(issue, prepared.Commands, "next_run", session.ThreadID, attempt)
		}); err != nil {
			return &RunResult{
				Success:    false,
				Output:     client.Output(),
				Error:      err,
				AppSession: client.Session(),
			}, nil
		}
		if deliverErr != nil {
			return &RunResult{
				Success:    false,
				Output:     client.Output(),
				Error:      deliverErr,
				AppSession: client.Session(),
			}, nil
		}
		deliveredManualCommands, err := r.runPendingCommandsInActiveThread(ctx, client, issue, attempt, title)
		if err != nil {
			return &RunResult{
				Success:    false,
				Output:     client.Output(),
				Error:      err,
				AppSession: client.Session(),
			}, nil
		}
		if deliveredManualCommands {
			return &RunResult{Success: true, Output: client.Output(), AppSession: client.Session()}, nil
		}

		refreshed, continueRun := r.refreshForContinuation(workflow, issue.ID)
		if !continueRun {
			return &RunResult{
				Success:    true,
				Output:     client.Output(),
				AppSession: client.Session(),
			}, nil
		}
		issue = refreshed
	}
	return &RunResult{Success: true, Output: client.Output(), AppSession: client.Session()}, nil
}

func (r *Runner) refreshForContinuation(workflow *config.Workflow, issueID string) (*kanban.Issue, bool) {
	refreshed, err := r.service.RefreshIssueByID(context.Background(), issueID)
	if err != nil {
		refreshed, err = r.store.GetIssue(issueID)
	}
	if err != nil {
		return nil, false
	}
	return refreshed, isActiveState(workflow, string(refreshed.State))
}

func (r *Runner) buildTurnPrompt(workflow *config.Workflow, issue *kanban.Issue, attempt int, turn int) (string, error) {
	prepared, err := r.prepareTurnPrompt(workflow, issue, attempt, turn)
	if err != nil {
		return "", err
	}
	return prepared.Prompt, nil
}

func (r *Runner) prepareTurnPrompt(workflow *config.Workflow, issue *kanban.Issue, attempt int, turn int) (preparedTurnPrompt, error) {
	if turn > 1 {
		return preparedTurnPrompt{Prompt: fmt.Sprintf(strings.TrimSpace(`
Continuation guidance:

- The previous turn completed normally, but the issue is still in an active state.
- This is continuation turn #%d of %d for the current agent run.
- Resume from the current workspace state instead of restarting from scratch.
- The original task instructions are already present in the thread history; do not restate them before acting.
- If a verification approach was blocked by local tooling or browser issues, switch to another deterministic local check instead of retrying the same path.
`), turn, workflow.Config.Agent.MaxTurns)}, nil
	}
	phase := issue.WorkflowPhase
	if !phase.IsValid() {
		phase = kanban.DefaultWorkflowPhaseForState(issue.State)
	}
	ctx := map[string]interface{}{
		"issue": map[string]interface{}{
			"id":          issue.ID,
			"identifier":  issue.Identifier,
			"title":       issue.Title,
			"description": issue.Description,
			"state":       string(issue.State),
			"priority":    issue.Priority,
			"labels":      issue.Labels,
			"branch_name": issue.BranchName,
			"pr_number":   issue.PRNumber,
			"pr_url":      issue.PRURL,
			"blocked_by":  issue.BlockedBy,
			"created_at":  issue.CreatedAt.Format(time.RFC3339),
			"updated_at":  issue.UpdatedAt.Format(time.RFC3339),
		},
		"attempt": nil,
		"phase":   string(phase),
	}
	if attempt > 0 {
		ctx["attempt"] = attempt
	}
	rendered, err := config.RenderLiquidTemplate(promptTemplateForPhase(workflow, phase), ctx)
	if err != nil {
		return preparedTurnPrompt{}, fmt.Errorf("template_render_error: %w", err)
	}
	rendered = strings.TrimSpace(rendered)
	commands, err := r.pendingCommandsForIssue(issue.ID)
	if err != nil {
		return preparedTurnPrompt{}, err
	}
	rendered = appendOperatorCommands(rendered, commands)
	if rendered == "" {
		return preparedTurnPrompt{
			Prompt:   strings.TrimSpace(firstTurnExecutionGuidance),
			Commands: commands,
		}, nil
	}
	return preparedTurnPrompt{
		Prompt:   rendered + "\n\n" + strings.TrimSpace(firstTurnExecutionGuidance),
		Commands: commands,
	}, nil
}

func promptTemplateForPhase(workflow *config.Workflow, phase kanban.WorkflowPhase) string {
	switch phase {
	case kanban.WorkflowPhaseReview:
		return workflow.Config.Phases.Review.Prompt
	case kanban.WorkflowPhaseDone:
		return workflow.Config.Phases.Done.Prompt
	default:
		return workflow.PromptTemplate
	}
}

func (r *Runner) pendingCommandsForIssue(issueID string) ([]kanban.IssueAgentCommand, error) {
	if err := r.store.ActivateIssueAgentCommandsIfDispatchable(issueID); err != nil {
		return nil, err
	}
	issue, err := r.store.GetIssue(issueID)
	if err != nil {
		return nil, err
	}
	if issue.State != kanban.StateReady && issue.State != kanban.StateInProgress && issue.State != kanban.StateInReview {
		return nil, nil
	}
	unresolved, err := r.store.UnresolvedBlockersForIssue(issueID)
	if err != nil {
		return nil, err
	}
	if len(unresolved) > 0 {
		return nil, nil
	}
	return r.store.ListPendingIssueAgentCommands(issueID)
}

func appendOperatorCommands(prompt string, commands []kanban.IssueAgentCommand) string {
	prompt = strings.TrimSpace(prompt)
	if len(commands) == 0 {
		return prompt
	}
	lines := []string{
		"Operator follow-up commands:",
		"",
		"- These commands supplement the original issue instructions.",
		"- Act on them directly without restating the original task.",
	}
	for i, command := range commands {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, command.Command))
	}
	section := strings.Join(lines, "\n")
	if prompt == "" {
		return section
	}
	return prompt + "\n\n" + section
}

func buildOperatorFollowUpPrompt(commands []kanban.IssueAgentCommand) string {
	return appendOperatorCommands("", commands) + "\n\n" + strings.TrimSpace(firstTurnExecutionGuidance)
}

func (r *Runner) markDeliveredCommands(issue *kanban.Issue, commands []kanban.IssueAgentCommand, mode, threadID string, attempt int) error {
	if issue == nil || len(commands) == 0 {
		return nil
	}
	ids := make([]string, 0, len(commands))
	for _, command := range commands {
		ids = append(ids, command.ID)
	}
	if err := r.store.MarkIssueAgentCommandsDelivered(issue.ID, ids, mode, threadID, attempt); err != nil {
		return err
	}
	return r.store.AppendRuntimeEvent("manual_command_delivered", map[string]interface{}{
		"issue_id":           issue.ID,
		"identifier":         issue.Identifier,
		"attempt":            attempt,
		"delivery_mode":      mode,
		"delivery_thread_id": threadID,
		"command_ids":        ids,
		"command_count":      len(ids),
	})
}

func (r *Runner) runPendingCommandsInActiveThread(ctx context.Context, client *appserver.Client, issue *kanban.Issue, attempt int, title string) (bool, error) {
	deadline := time.Now().Add(activeThreadCommandPollWindow)
	var commands []kanban.IssueAgentCommand
	for {
		var err error
		commands, err = r.pendingCommandsForIssue(issue.ID)
		if err != nil {
			return false, err
		}
		if len(commands) > 0 || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	if len(commands) == 0 {
		return false, nil
	}
	var deliverErr error
	if err := client.RunTurnWithStartCallback(ctx, buildOperatorFollowUpPrompt(commands), title, func(session *appserver.Session) {
		deliverErr = r.markDeliveredCommands(issue, commands, "same_thread", session.ThreadID, attempt)
	}); err != nil {
		return false, err
	}
	if deliverErr != nil {
		return false, deliverErr
	}
	return true, nil
}

func (r *Runner) runHook(parentCtx context.Context, workspacePath, hook, hookName string) error {
	if strings.TrimSpace(hook) == "" {
		return nil
	}
	workflow, err := r.workflowProvider.Current()
	if err != nil {
		return err
	}
	hookCtx := parentCtx
	var cancel context.CancelFunc
	if workflow.Config.Hooks.TimeoutMs > 0 {
		hookCtx, cancel = context.WithTimeout(parentCtx, time.Duration(workflow.Config.Hooks.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	cmd := exec.CommandContext(hookCtx, "sh", "-lc", hook)
	cmd.Dir = workspacePath
	cmd.Env = append(os.Environ(), "WORKSPACE_PATH="+workspacePath)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	if errors.Is(hookCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("workspace hook timeout (%s): %w", hookName, hookCtx.Err())
	}
	if err != nil {
		return fmt.Errorf("workspace hook failed (%s): %v: %s", hookName, err, out.String())
	}
	return nil
}

func (r *Runner) executeStdioTurn(ctx context.Context, workspacePath, command, prompt string, timeoutMs int) (string, error) {
	turnCtx := ctx
	var cancel context.CancelFunc
	if timeoutMs > 0 {
		turnCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()
	}
	cmd := exec.CommandContext(turnCtx, "sh", "-lc", command)
	cmd.Dir = workspacePath
	cmd.Env = os.Environ()
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}
	return strings.TrimSpace(output), err
}

func isActiveState(workflow *config.Workflow, state string) bool {
	normalized := normalizeState(state)
	for _, s := range workflow.Config.Tracker.ActiveStates {
		if normalizeState(s) == normalized {
			return true
		}
	}
	return false
}

func normalizeState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}

func (r *Runner) extensionToolExecutor() appserver.ToolExecutor {
	if r.extensions == nil || !r.extensions.HasTools() {
		return nil
	}
	return func(ctx context.Context, name string, arguments interface{}) map[string]interface{} {
		output, err := r.extensions.Execute(ctx, name, arguments)
		if err != nil {
			return dynamicToolError(err.Error())
		}
		return dynamicToolSuccess(output)
	}
}

func dynamicToolSuccess(text string) map[string]interface{} {
	return map[string]interface{}{
		"success": true,
		"contentItems": []map[string]interface{}{
			{
				"type": "inputText",
				"text": text,
			},
		},
	}
}

func dynamicToolError(message string) map[string]interface{} {
	return map[string]interface{}{
		"success": false,
		"contentItems": []map[string]interface{}{
			{
				"type": "inputText",
				"text": encodeDynamicToolPayload(map[string]interface{}{
					"error": map[string]interface{}{
						"message": message,
					},
				}),
			},
		},
	}
}

func encodeDynamicToolPayload(payload interface{}) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("%v", payload)
	}
	return string(data)
}
