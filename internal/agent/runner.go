package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	runtimefactory "github.com/olhapi/maestro/internal/agentruntime/factory"
	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/providers"
	"github.com/olhapi/maestro/pkg/config"
)

type WorkflowProvider interface {
	Current() (*config.Workflow, error)
}

type Runner struct {
	workflowProvider        WorkflowProvider
	store                   *kanban.Store
	service                 *providers.Service
	extensions              *extensions.Registry
	runtimeStarter          runtimefactory.WorkflowStarter
	sessionObserver         func(issueID string, session *agentruntime.Session)
	activityObserver        func(issueID string, event agentruntime.ActivityEvent)
	interactionObserver     func(issueID string, interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder)
	interactionDoneObserver func(issueID string, interactionID string)
}

type RunResult struct {
	Success    bool
	Output     string
	Error      error
	StopReason string
	AppSession *agentruntime.Session
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

const firstTurnPlanningGuidance = `
Planning guidance:

- Use this turn to clarify requirements, identify missing constraints, and validate assumptions.
- Ask concise questions when the issue or project context is ambiguous.
- Keep verification lightweight and deterministic.
- End the turn with a single <proposed_plan> block when the plan is ready; do not start implementation yet.
`

const continuationPlanningGuidance = `
Continuation guidance:

- The previous turn completed normally, but the issue is still in the planning phase.
- This is continuation turn #%d of %d for the current agent run.
- Keep refining the plan and ask any remaining blocking questions.
- Finish with a single <proposed_plan> block when the plan is ready; do not start implementation before approval.
- Resume from the current workspace state instead of restarting from scratch.
- The original task instructions are already present in the thread history; do not restate them before acting.
`

const activeThreadCommandPollWindow = 250 * time.Millisecond
const appServerIssueAssetStageDir = ".maestro/issue-assets"
const planApprovalStopReason = "plan_approval_pending"
const workspaceBootstrapRefreshAttempts = 3
const workspaceBootstrapRefreshRetryDelay = 100 * time.Millisecond

var proposedPlanBlockPattern = regexp.MustCompile(`(?s)<proposed_plan>\s*(.*?)\s*</proposed_plan>`)
var repoBootstrapLocks sync.Map

type repoBootstrapLockState struct {
	token chan struct{}
}

func newRepoBootstrapLockState() *repoBootstrapLockState {
	lock := &repoBootstrapLockState{token: make(chan struct{}, 1)}
	lock.token <- struct{}{}
	return lock
}

func (l *repoBootstrapLockState) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.token:
	}
	if err := ctx.Err(); err != nil {
		l.release()
		return nil, err
	}
	return l.release, nil
}

func (l *repoBootstrapLockState) release() {
	select {
	case l.token <- struct{}{}:
	default:
		panic("repo bootstrap lock released without acquisition")
	}
}

func gitCommandEnv() []string {
	env := os.Environ()
	filtered := env[:0]
	for _, value := range env {
		switch {
		case strings.HasPrefix(value, "GIT_DIR="):
		case strings.HasPrefix(value, "GIT_WORK_TREE="):
		case strings.HasPrefix(value, "GIT_INDEX_FILE="):
		case strings.HasPrefix(value, "GIT_COMMON_DIR="):
		case strings.HasPrefix(value, "GIT_PREFIX="):
		default:
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func workspaceRecoveryNoteText() string {
	return strings.TrimSpace(`
Workspace recovery note:

- Maestro found an active Git rebase in this workspace.
- Finish or quit the rebase in place before making new changes.
- Do not recreate or discard the workspace unless recovery fails.
`)
}

func isWorkspaceBootstrapRebaseError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cannot switch branch while rebasing") ||
		(strings.Contains(message, "while rebasing") && strings.Contains(message, "switch branch"))
}

func workspaceBootstrapRecoveryError(reason string) string {
	switch strings.TrimSpace(reason) {
	case "active_rebase":
		return "workspace recovery required: active Git rebase detected"
	case "branch_switch_blocked":
		return "workspace recovery required: Git blocked the branch switch while rebasing"
	default:
		return "workspace recovery required"
	}
}

func NewRunner(provider WorkflowProvider, store *kanban.Store) *Runner {
	return NewRunnerWithExtensions(provider, store, nil)
}

func NewRunnerWithExtensions(provider WorkflowProvider, store *kanban.Store, registry *extensions.Registry) *Runner {
	if registry == nil {
		registry = extensions.EmptyRegistry()
	}
	return &Runner{
		workflowProvider: provider,
		store:            store,
		service:          providers.NewService(store),
		extensions:       registry,
		runtimeStarter:   runtimefactory.StartWorkflow,
	}
}

func (r *Runner) SetSessionObserver(observer func(issueID string, session *agentruntime.Session)) {
	r.sessionObserver = observer
}

func (r *Runner) SetActivityObserver(observer func(issueID string, event agentruntime.ActivityEvent)) {
	r.activityObserver = observer
}

func (r *Runner) SetInteractionObserver(observer func(issueID string, interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder)) {
	r.interactionObserver = observer
}

func (r *Runner) SetInteractionDoneObserver(observer func(issueID string, interactionID string)) {
	r.interactionDoneObserver = observer
}

func (r *Runner) Run(ctx context.Context, issue *kanban.Issue) (*RunResult, error) {
	return r.RunAttempt(ctx, issue, 0)
}

func (r *Runner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*RunResult, error) {
	workflow, err := r.workflowProvider.Current()
	if err != nil {
		return nil, err
	}
	workflow = r.applyIssuePermissionProfile(workflow, issue)

	workspace, err := r.getOrCreateWorkspace(ctx, workflow, issue)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	recoveryActive, err := workspaceHasActiveRebase(workspace.Path)
	if err != nil {
		return nil, fmt.Errorf("inspect workspace recovery state: %w", err)
	}
	if !recoveryActive {
		if err := r.runHook(ctx, workspace.Path, workflow.Config.Hooks.BeforeRun, "before_run"); err != nil {
			return nil, err
		}
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

func (r *Runner) applyIssuePermissionProfile(workflow *config.Workflow, issue *kanban.Issue) *config.Workflow {
	if workflow == nil {
		return nil
	}
	cloned := *workflow
	cloned.Config = workflow.Config
	cloned.Config.Codex = workflow.Config.Codex
	permissionConfig := r.permissionConfigForIssue(issue, workflow.Config.Codex.ApprovalPolicy, workflow.Config.Codex.InitialCollaborationMode)
	cloned.Config.Codex.ApprovalPolicy = permissionConfig.ApprovalPolicy
	cloned.Config.Codex.InitialCollaborationMode = permissionConfig.InitialCollaborationMode
	return &cloned
}

type permissionConfig struct {
	ApprovalPolicy           interface{}
	ThreadSandbox            string
	TurnSandboxPolicy        map[string]interface{}
	InitialCollaborationMode string
}

func (r *Runner) permissionConfigForIssue(issue *kanban.Issue, approvalPolicy interface{}, initialCollaborationMode string) permissionConfig {
	initialCollaborationMode = strings.TrimSpace(initialCollaborationMode)
	if initialCollaborationMode == "" {
		initialCollaborationMode = config.InitialCollaborationModeDefault
	}
	if override := r.effectiveInitialCollaborationMode(issue); override != "" {
		initialCollaborationMode = override
	}
	switch r.effectivePermissionProfile(issue) {
	case kanban.PermissionProfileFullAccess:
		return permissionConfig{
			ApprovalPolicy: approvalPolicy,
			ThreadSandbox:  "danger-full-access",
			TurnSandboxPolicy: map[string]interface{}{
				"type":          "dangerFullAccess",
				"networkAccess": true,
			},
			InitialCollaborationMode: initialCollaborationMode,
		}
	case kanban.PermissionProfilePlanThenFullAccess:
		return permissionConfig{
			ApprovalPolicy:           approvalPolicy,
			ThreadSandbox:            "workspace-write",
			TurnSandboxPolicy:        nil,
			InitialCollaborationMode: config.InitialCollaborationModePlan,
		}
	default:
		return permissionConfig{
			ApprovalPolicy:           approvalPolicy,
			ThreadSandbox:            "workspace-write",
			TurnSandboxPolicy:        nil,
			InitialCollaborationMode: initialCollaborationMode,
		}
	}
}

func (r *Runner) effectivePermissionProfile(issue *kanban.Issue) kanban.PermissionProfile {
	if issue == nil {
		return kanban.PermissionProfileDefault
	}
	profile := kanban.NormalizePermissionProfile(string(issue.PermissionProfile))
	if profile != kanban.PermissionProfileDefault {
		return profile
	}
	projectID := strings.TrimSpace(issue.ProjectID)
	if projectID == "" || r.store == nil {
		return kanban.PermissionProfileDefault
	}
	project, err := r.store.GetProject(projectID)
	if err != nil {
		return kanban.PermissionProfileDefault
	}
	return kanban.NormalizePermissionProfile(string(project.PermissionProfile))
}

func (r *Runner) effectiveInitialCollaborationMode(issue *kanban.Issue) string {
	if issue == nil {
		return ""
	}
	return string(kanban.NormalizeCollaborationModeOverride(string(issue.CollaborationModeOverride)))
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

func projectWorkspaceSlug(project *kanban.Project) string {
	if project == nil {
		return ""
	}
	candidate := strings.TrimSpace(project.ProviderProjectRef)
	if candidate == "" {
		candidate = strings.TrimSpace(project.Name)
	}
	candidate = strings.ToLower(candidate)
	candidate = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(candidate, "-")
	candidate = strings.Trim(candidate, "-")
	if candidate == "" {
		return "project"
	}
	return candidate
}

func workspaceRootAllowsRehome(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	if strings.HasPrefix(root, "~") || strings.Contains(root, "$") {
		return false
	}
	return filepath.IsAbs(root)
}

func workspacePathForIssue(rootAbs string, project *kanban.Project, issue *kanban.Issue) string {
	issueKey := "issue"
	if issue != nil {
		issueKey = sanitizeWorkspaceKey(issue.Identifier)
	}
	return filepath.Join(rootAbs, projectWorkspaceSlug(project), issueKey)
}

func deterministicIssueBranch(issue *kanban.Issue) string {
	if issue == nil {
		return "codex/issue"
	}
	if branch := strings.TrimSpace(issue.BranchName); branch != "" {
		return branch
	}
	identifier := strings.TrimSpace(issue.Identifier)
	if identifier == "" {
		return "codex/issue"
	}
	return "codex/" + identifier
}

func runGitCommand(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitCommandEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail != "" {
			return "", fmt.Errorf("%w: %s", err, detail)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func repoHasRemote(ctx context.Context, repoPath, remoteName string) (bool, error) {
	if strings.TrimSpace(remoteName) == "" {
		return false, nil
	}
	output, err := runGitCommand(ctx, repoPath, "remote")
	if err != nil {
		return false, err
	}
	for _, remote := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(remote) == remoteName {
			return true, nil
		}
	}
	return false, nil
}

func repoBootstrapLock(ctx context.Context, repoPath string) (func(), error) {
	commonDir, err := gitCommonDir(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	key := canonicalPath(commonDir)
	lock, _ := repoBootstrapLocks.LoadOrStore(key, newRepoBootstrapLockState())
	return lock.(*repoBootstrapLockState).acquire(ctx)
}

func refreshRepoForWorkspaceBootstrap(ctx context.Context, repoPath string) (bool, error) {
	hasOrigin, err := repoHasRemote(ctx, repoPath, "origin")
	if err != nil {
		return false, err
	}
	if !hasOrigin {
		return false, nil
	}

	var lastErr error
	for attempt := 1; attempt <= workspaceBootstrapRefreshAttempts; attempt++ {
		if _, err := runGitCommand(ctx, repoPath, "fetch", "--prune", "origin"); err == nil {
			if _, err := runGitCommand(ctx, repoPath, "remote", "set-head", "origin", "-a"); err != nil {
				return true, nil
			}
			return true, nil
		} else {
			lastErr = err
		}
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		if attempt < workspaceBootstrapRefreshAttempts {
			timer := time.NewTimer(workspaceBootstrapRefreshRetryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return true, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return true, fmt.Errorf("refresh repository refs: %w", lastErr)
}

func workspaceBootstrapFreshBaseRef(ctx context.Context, repoPath string, hasOrigin bool) (string, error) {
	baseRef, err := resolveRepoDefaultBranch(ctx, repoPath)
	if err != nil {
		return "", err
	}
	baseRef = strings.TrimSpace(baseRef)
	if baseRef == "" {
		return "", fmt.Errorf("unable to resolve repository default branch")
	}
	if !hasOrigin {
		return baseRef, nil
	}
	if strings.HasPrefix(baseRef, "origin/") {
		return baseRef, nil
	}
	if gitRefExists(ctx, repoPath, "refs/remotes/origin/"+baseRef) {
		return "origin/" + baseRef, nil
	}
	return baseRef, nil
}

func branchExists(ctx context.Context, repoPath, branch string) bool {
	if strings.TrimSpace(branch) == "" {
		return false
	}
	_, err := runGitCommand(ctx, repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func gitRefExists(ctx context.Context, repoPath, ref string) bool {
	if strings.TrimSpace(ref) == "" {
		return false
	}
	_, err := runGitCommand(ctx, repoPath, "show-ref", "--verify", "--quiet", ref)
	return err == nil
}

func listLocalBranches(ctx context.Context, repoPath string) ([]string, error) {
	output, err := runGitCommand(ctx, repoPath, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}
	return strings.Split(output, "\n"), nil
}

func resolveRepoDefaultBranch(ctx context.Context, repoPath string) (string, error) {
	if repoPath == "" {
		return "", fmt.Errorf("missing repo path")
	}
	if ref, err := runGitCommand(ctx, repoPath, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		ref = strings.TrimSpace(ref)
		if strings.HasPrefix(ref, "origin/") && len(ref) > len("origin/") {
			return strings.TrimSpace(strings.TrimPrefix(ref, "origin/")), nil
		}
	}
	mainlineCandidates := []string{"main", "master", "trunk", "develop"}
	for _, branch := range mainlineCandidates {
		if branchExists(ctx, repoPath, branch) {
			return branch, nil
		}
	}
	localBranches, err := listLocalBranches(ctx, repoPath)
	if err == nil && len(localBranches) == 1 && strings.TrimSpace(localBranches[0]) != "" {
		return strings.TrimSpace(localBranches[0]), nil
	}
	for _, branch := range mainlineCandidates {
		if gitRefExists(ctx, repoPath, "refs/remotes/origin/"+branch) {
			return "origin/" + branch, nil
		}
	}
	if branch, err := runGitCommand(ctx, repoPath, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		branch = strings.TrimSpace(branch)
		if branch != "" {
			return branch, nil
		}
	}
	if branch, err := runGitCommand(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = strings.TrimSpace(branch)
		if branch == "HEAD" {
			return branch, nil
		}
		if branch != "" {
			return branch, nil
		}
	}
	return "", fmt.Errorf("unable to resolve repository default branch")
}

func gitCommonDir(ctx context.Context, dir string) (string, error) {
	value, err := runGitCommand(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(value) {
		return canonicalPath(value), nil
	}
	return canonicalPath(filepath.Join(dir, value)), nil
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}

func workspaceMatchesRepo(ctx context.Context, workspacePath, repoPath string) (bool, error) {
	topLevel, err := runGitCommand(ctx, workspacePath, "rev-parse", "--show-toplevel")
	if err != nil {
		return false, nil
	}
	topLevelAbs := canonicalPath(strings.TrimSpace(topLevel))
	workspaceAbs := canonicalPath(workspacePath)
	if filepath.Clean(topLevelAbs) != filepath.Clean(workspaceAbs) {
		linkedWorktree, err := isLinkedWorktree(ctx, workspacePath)
		if err != nil {
			return false, err
		}
		if !linkedWorktree {
			return false, nil
		}
	}
	workspaceCommonDir, err := gitCommonDir(ctx, workspacePath)
	if err != nil {
		return false, err
	}
	repoCommonDir, err := gitCommonDir(ctx, repoPath)
	if err != nil {
		return false, err
	}
	return filepath.Clean(workspaceCommonDir) == filepath.Clean(repoCommonDir), nil
}

func validateWorkspacePath(path, rootAbs string) (string, error) {
	workspacePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	if !pathWithinRoot(workspacePath, rootAbs) {
		return "", fmt.Errorf("workspace path escape: %s outside %s", workspacePath, rootAbs)
	}
	if fi, err := os.Lstat(workspacePath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(workspacePath)
			if err != nil {
				return "", fmt.Errorf("workspace symlink check failed: %w", err)
			}
			resolvedAbs, err := filepath.Abs(resolved)
			if err != nil {
				return "", fmt.Errorf("resolve workspace symlink: %w", err)
			}
			if !pathWithinRoot(resolvedAbs, rootAbs) {
				return "", fmt.Errorf("workspace symlink escape: %s outside %s", resolvedAbs, rootAbs)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return workspacePath, nil
}

func removeManagedWorkspace(ctx context.Context, workspacePath string) error {
	commonDir, err := gitCommonDir(ctx, workspacePath)
	if err == nil && filepath.Base(commonDir) == ".git" {
		repoPath := filepath.Dir(commonDir)
		if _, removeErr := runGitCommand(ctx, repoPath, "worktree", "remove", "--force", workspacePath); removeErr == nil {
			return nil
		}
	}
	if err := os.RemoveAll(workspacePath); err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}

func workspaceGitDir(workspacePath string) (string, error) {
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	gitPath := filepath.Join(absPath, ".git")
	fi, err := os.Lstat(gitPath)
	if err != nil {
		return "", fmt.Errorf("read workspace git metadata: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(gitPath)
		if err != nil {
			return "", fmt.Errorf("resolve workspace git metadata symlink: %w", err)
		}
		return canonicalPath(resolved), nil
	}
	if fi.IsDir() {
		return canonicalPath(gitPath), nil
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", fmt.Errorf("read workspace git metadata: %w", err)
	}
	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "gitdir:") {
		return "", fmt.Errorf("invalid workspace git metadata: %s", gitPath)
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(content, "gitdir:"))
	if gitDir == "" {
		return "", fmt.Errorf("invalid workspace git metadata: %s", gitPath)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(absPath, gitDir)
	}
	return canonicalPath(gitDir), nil
}

func workspaceHasActiveRebase(workspacePath string) (bool, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return false, nil
	}
	gitDir, err := workspaceGitDir(workspacePath)
	if err != nil {
		return false, err
	}
	gitDirs := []string{gitDir}
	if filepath.Base(filepath.Dir(gitDir)) == "worktrees" {
		commonDir := filepath.Clean(filepath.Dir(filepath.Dir(gitDir)))
		if commonDir != gitDir {
			gitDirs = append(gitDirs, commonDir)
		}
	}
	for _, dir := range gitDirs {
		for _, name := range []string{"rebase-merge", "rebase-apply"} {
			info, statErr := os.Stat(filepath.Join(dir, name))
			if statErr == nil {
				if info.IsDir() {
					return true, nil
				}
				continue
			}
			if !errors.Is(statErr, os.ErrNotExist) {
				return false, statErr
			}
		}
	}
	return false, nil
}

func isLinkedWorktree(ctx context.Context, workspacePath string) (bool, error) {
	gitDir, err := runGitCommand(ctx, workspacePath, "rev-parse", "--git-dir")
	if err != nil {
		return false, nil
	}
	if !filepath.IsAbs(gitDir) {
		gitDir, err = filepath.Abs(filepath.Join(workspacePath, gitDir))
		if err != nil {
			return false, err
		}
	}
	gitDir = filepath.Clean(gitDir)
	needle := string(os.PathSeparator) + ".git" + string(os.PathSeparator) + "worktrees" + string(os.PathSeparator)
	return strings.Contains(gitDir, needle), nil
}

func preserveWorkspaceContents(ctx context.Context, workspacePath string) (string, bool, error) {
	linkedWorktree, err := isLinkedWorktree(ctx, workspacePath)
	if err != nil {
		return "", false, err
	}
	if linkedWorktree {
		return "", false, nil
	}
	entries, err := os.ReadDir(workspacePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if len(entries) == 0 {
		return "", false, nil
	}
	base := workspacePath + ".legacy-" + time.Now().UTC().Format("20060102-150405")
	candidate := base
	for attempt := 1; ; attempt++ {
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(workspacePath, candidate); err != nil {
				return "", false, err
			}
			return candidate, true, nil
		} else if err != nil {
			return "", false, err
		}
		candidate = fmt.Sprintf("%s-%d", base, attempt)
	}
}

func (r *Runner) resolveRepoPathForIssue(workflow *config.Workflow, issue *kanban.Issue) (string, error) {
	if issue != nil && strings.TrimSpace(issue.ProjectID) != "" && r.store != nil {
		project, err := r.store.GetProject(issue.ProjectID)
		if err == nil && strings.TrimSpace(project.RepoPath) != "" {
			return filepath.Abs(project.RepoPath)
		}
		if err != nil && !kanban.IsNotFound(err) {
			return "", err
		}
	}
	if workflow != nil && strings.TrimSpace(workflow.Path) != "" {
		return filepath.Abs(filepath.Dir(workflow.Path))
	}
	return "", fmt.Errorf("missing repository path for issue workspace bootstrap")
}

func (r *Runner) recordWorkspaceRuntimeEvent(issue *kanban.Issue, kind string, fields map[string]interface{}) {
	if r.store == nil || issue == nil {
		return
	}
	event := map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
	}
	for key, value := range fields {
		event[key] = value
	}
	_ = r.store.AppendRuntimeEvent(kind, event)
}

func (r *Runner) recordWorkspaceBootstrapRecovery(issue *kanban.Issue, preparedPath, branchName, currentBranch, reason string, gitErr error) {
	message := workspaceRecoveryNoteText()
	fields := map[string]interface{}{
		"path":            preparedPath,
		"branch":          branchName,
		"current_branch":  currentBranch,
		"status":          "recovering",
		"message":         message,
		"recovery_reason": strings.TrimSpace(reason),
		"error":           workspaceBootstrapRecoveryError(reason),
	}
	if gitErr != nil {
		fields["git_error"] = gitErr.Error()
	}
	r.recordWorkspaceRuntimeEvent(issue, "workspace_bootstrap_recovery", fields)
}

func workspaceProjectForIssue(store *kanban.Store, issue *kanban.Issue) (*kanban.Project, error) {
	if issue == nil || strings.TrimSpace(issue.ProjectID) == "" || store == nil {
		return nil, nil
	}
	project, err := store.GetProject(strings.TrimSpace(issue.ProjectID))
	if err != nil {
		if kanban.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load project for workspace bootstrap: %w", err)
	}
	return project, nil
}

func workspaceCanMoveToRoot(ctx context.Context, workspacePath, repoPath string) (bool, error) {
	info, err := os.Stat(workspacePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	linkedWorktree, err := isLinkedWorktree(ctx, workspacePath)
	if err != nil {
		return false, err
	}
	if !linkedWorktree {
		return false, nil
	}
	return workspaceMatchesRepo(ctx, workspacePath, repoPath)
}

func moveWorkspaceToRoot(ctx context.Context, repoPath, workspacePath, targetPath, rootAbs string) error {
	targetPath, err := validateWorkspacePath(targetPath, rootAbs)
	if err != nil {
		return err
	}
	workspacePath, err = filepath.Abs(workspacePath)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	if filepath.Clean(workspacePath) == filepath.Clean(targetPath) {
		return nil
	}
	unlock, err := repoBootstrapLock(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("lock workspace bootstrap: %w", err)
	}
	defer unlock()

	if _, err := runGitCommand(ctx, repoPath, "rev-parse", "--show-toplevel"); err != nil {
		return fmt.Errorf("repo validation failed: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create target workspace parent: %w", err)
	}
	if _, err := os.Lstat(targetPath); err == nil {
		if err := removeManagedWorkspace(ctx, targetPath); err != nil {
			return fmt.Errorf("remove target workspace %s: %w", targetPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := runGitCommand(ctx, repoPath, "worktree", "move", workspacePath, targetPath); err != nil {
		return fmt.Errorf("move git worktree %s to %s: %w", workspacePath, targetPath, err)
	}
	return nil
}

func (r *Runner) recoverWorkspaceToCurrentRoot(ctx context.Context, workflow *config.Workflow, issue *kanban.Issue, existingPath, targetPath, rootAbs string) error {
	repoPath, err := r.resolveRepoPathForIssue(workflow, issue)
	if err != nil {
		return err
	}
	existingPath, err = filepath.Abs(existingPath)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	targetPath, err = validateWorkspacePath(targetPath, rootAbs)
	if err != nil {
		return err
	}
	if filepath.Clean(existingPath) == filepath.Clean(targetPath) {
		return nil
	}
	canMove, err := workspaceCanMoveToRoot(ctx, existingPath, repoPath)
	if err != nil {
		return err
	}
	if !canMove {
		return nil
	}
	if err := moveWorkspaceToRoot(ctx, repoPath, existingPath, targetPath, rootAbs); err != nil {
		return err
	}
	return nil
}

func (r *Runner) ensureIssueWorkspace(ctx context.Context, workflow *config.Workflow, issue *kanban.Issue, workspacePath, rootAbs string, legacyPath bool) (bool, string, error) {
	repoPath, err := r.resolveRepoPathForIssue(workflow, issue)
	if err != nil {
		return false, "", err
	}
	if _, err := runGitCommand(ctx, repoPath, "rev-parse", "--show-toplevel"); err != nil {
		return false, "", fmt.Errorf("repo validation failed: %w", err)
	}
	branchName := deterministicIssueBranch(issue)
	if issue != nil && strings.TrimSpace(issue.BranchName) != branchName && r.store != nil {
		if err := r.store.UpdateIssue(issue.ID, map[string]interface{}{"branch_name": branchName}); err == nil {
			issue.BranchName = branchName
		}
	}

	unlock, err := repoBootstrapLock(ctx, repoPath)
	if err != nil {
		return false, "", fmt.Errorf("lock workspace bootstrap: %w", err)
	}
	defer unlock()

	normalizedPath := workspacePath
	// Only preserve content when we are explicitly recovering an old legacy path.
	if legacyPath {
		normalizedPath, err = filepath.Abs(workspacePath)
		if err != nil {
			return false, "", fmt.Errorf("resolve workspace path: %w", err)
		}
	} else {
		normalizedPath, err = validateWorkspacePath(workspacePath, rootAbs)
		if err != nil {
			return false, "", err
		}
	}
	if matched, err := workspaceMatchesRepo(ctx, normalizedPath, repoPath); err != nil {
		return false, "", err
	} else if matched {
		recoveryActive, recoveryErr := workspaceHasActiveRebase(normalizedPath)
		if recoveryErr != nil {
			return false, "", recoveryErr
		}
		currentBranch, branchErr := runGitCommand(ctx, normalizedPath, "branch", "--show-current")
		if branchErr != nil && !recoveryActive {
			return false, "", branchErr
		}
		currentBranch = strings.TrimSpace(currentBranch)
		if recoveryActive {
			r.recordWorkspaceBootstrapRecovery(issue, normalizedPath, branchName, currentBranch, "active_rebase", nil)
			return false, normalizedPath, nil
		}
		if currentBranch != branchName {
			if !branchExists(ctx, repoPath, branchName) {
				if currentBranch != "" {
					if _, err := runGitCommand(ctx, normalizedPath, "branch", "-m", branchName); err != nil {
						if isWorkspaceBootstrapRebaseError(err) {
							r.recordWorkspaceBootstrapRecovery(issue, normalizedPath, branchName, currentBranch, "branch_switch_blocked", err)
							return false, normalizedPath, nil
						}
						return false, "", fmt.Errorf("rename workspace branch %s to %s: %w", currentBranch, branchName, err)
					}
				} else {
					if _, err := runGitCommand(ctx, normalizedPath, "switch", "-c", branchName); err != nil {
						if isWorkspaceBootstrapRebaseError(err) {
							r.recordWorkspaceBootstrapRecovery(issue, normalizedPath, branchName, currentBranch, "branch_switch_blocked", err)
							return false, normalizedPath, nil
						}
						return false, "", fmt.Errorf("switch workspace branch %s: %w", branchName, err)
					}
				}
			} else {
				if _, err := runGitCommand(ctx, normalizedPath, "switch", branchName); err != nil {
					if isWorkspaceBootstrapRebaseError(err) {
						r.recordWorkspaceBootstrapRecovery(issue, normalizedPath, branchName, currentBranch, "branch_switch_blocked", err)
						return false, normalizedPath, nil
					}
					return false, "", fmt.Errorf("switch workspace branch %s: %w", branchName, err)
				}
			}
		}
		r.recordWorkspaceRuntimeEvent(issue, "workspace_bootstrap_reused", map[string]interface{}{
			"path":   normalizedPath,
			"branch": branchName,
		})
		return false, normalizedPath, nil
	}

	branchAlreadyExists := branchExists(ctx, repoPath, branchName)
	baseRef := ""
	if !branchAlreadyExists {
		hasOrigin, err := refreshRepoForWorkspaceBootstrap(ctx, repoPath)
		if err != nil {
			return false, "", fmt.Errorf("refresh workspace repo: %w", err)
		}
		baseRef, err = workspaceBootstrapFreshBaseRef(ctx, repoPath, hasOrigin)
		if err != nil {
			return false, "", err
		}
	}

	preparedPath, _, err := prepareWorkspaceDir(normalizedPath, rootAbs, !legacyPath)
	if err != nil {
		return false, "", err
	}

	if legacyPath {
		preservedPath, preserved, err := preserveWorkspaceContents(ctx, preparedPath)
		if err != nil {
			return false, "", fmt.Errorf("preserve workspace contents %s: %w", preparedPath, err)
		}
		if preserved {
			r.recordWorkspaceRuntimeEvent(issue, "workspace_bootstrap_preserved", map[string]interface{}{
				"path":           preparedPath,
				"preserved_path": preservedPath,
			})
		}
	}
	if err := removeManagedWorkspace(ctx, preparedPath); err != nil {
		return false, "", fmt.Errorf("remove stale workspace %s: %w", preparedPath, err)
	}
	args := []string{"worktree", "add"}
	if branchAlreadyExists {
		args = append(args, preparedPath, branchName)
	} else {
		args = append(args, "-b", branchName, preparedPath, baseRef)
	}
	if _, err := runGitCommand(ctx, repoPath, args...); err != nil {
		return false, "", fmt.Errorf("create git worktree %s on %s: %w", preparedPath, branchName, err)
	}
	recordedBaseRef := branchName
	if len(args) > 4 {
		recordedBaseRef = args[len(args)-1]
	}
	r.recordWorkspaceRuntimeEvent(issue, "workspace_bootstrap_created", map[string]interface{}{
		"path":      preparedPath,
		"branch":    branchName,
		"repo_path": repoPath,
		"base_ref":  recordedBaseRef,
	})
	return true, preparedPath, nil
}

func (r *Runner) getOrCreateWorkspace(ctx context.Context, workflow *config.Workflow, issue *kanban.Issue) (*kanban.Workspace, error) {
	rootAbs, err := filepath.Abs(workflow.Config.Workspace.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}

	project, err := workspaceProjectForIssue(r.store, issue)
	if err != nil {
		return nil, err
	}
	targetPath := workspacePathForIssue(rootAbs, project, issue)

	if existing, err := r.store.GetWorkspace(issue.ID); err == nil {
		existingPath, pathErr := filepath.Abs(existing.Path)
		if pathErr != nil {
			return nil, fmt.Errorf("resolve workspace path: %w", pathErr)
		}
		legacyPath := false
		if !pathWithinRoot(existingPath, rootAbs) {
			if workspaceRootAllowsRehome(workflow.Config.Workspace.Root) {
				if err := r.recoverWorkspaceToCurrentRoot(ctx, workflow, issue, existingPath, targetPath, rootAbs); err != nil {
					r.recordWorkspaceRuntimeEvent(issue, "workspace_bootstrap_failed", map[string]interface{}{
						"path":        existing.Path,
						"target_path": targetPath,
						"error":       err.Error(),
						"status":      "required",
						"message":     "Workspace bootstrap failed. Review the workspace blocker and retry once it is resolved.",
					})
					return nil, fmt.Errorf("workspace_bootstrap: %w", err)
				}
				existing, err = r.store.UpdateWorkspacePath(issue.ID, targetPath)
				if err != nil {
					return nil, err
				}
			} else {
				legacyPath = true
			}
		}
		createdNow, preparedPath, err := r.ensureIssueWorkspace(ctx, workflow, issue, existing.Path, rootAbs, legacyPath)
		if err != nil {
			r.recordWorkspaceRuntimeEvent(issue, "workspace_bootstrap_failed", map[string]interface{}{
				"path":    existing.Path,
				"error":   err.Error(),
				"status":  "required",
				"message": "Workspace bootstrap failed. Review the workspace blocker and retry once it is resolved.",
			})
			return nil, fmt.Errorf("workspace_bootstrap: %w", err)
		}
		if preparedPath != existing.Path {
			existing, err = r.store.UpdateWorkspacePath(issue.ID, preparedPath)
			if err != nil {
				return nil, err
			}
		}
		if createdNow {
			if err := r.runHook(ctx, preparedPath, workflow.Config.Hooks.AfterCreate, "after_create"); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}

	createdNow, preparedPath, err := r.ensureIssueWorkspace(ctx, workflow, issue, targetPath, rootAbs, false)
	if err != nil {
		r.recordWorkspaceRuntimeEvent(issue, "workspace_bootstrap_failed", map[string]interface{}{
			"path":    targetPath,
			"error":   err.Error(),
			"status":  "required",
			"message": "Workspace bootstrap failed. Review the workspace blocker and retry once it is resolved.",
		})
		return nil, fmt.Errorf("workspace_bootstrap: %w", err)
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
		if err := r.runHook(ctx, preparedPath, workflow.Config.Hooks.AfterCreate, "after_create"); err != nil {
			return nil, err
		}
	}
	return workspace, nil
}

func prepareWorkspaceDir(path, rootAbs string, enforceRoot bool) (string, bool, error) {
	workspacePath := path
	var err error
	if enforceRoot {
		workspacePath, err = validateWorkspacePath(path, rootAbs)
		if err != nil {
			return "", false, err
		}
	} else {
		workspacePath, err = filepath.Abs(path)
		if err != nil {
			return "", false, fmt.Errorf("resolve workspace path: %w", err)
		}
	}
	if fi, err := os.Lstat(workspacePath); err == nil {
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
	runPhase := issue.WorkflowPhase
	if !runPhase.IsValid() {
		runPhase = kanban.DefaultWorkflowPhaseForState(issue.State)
	}
	activeWorkflow, refreshedIssue, err := r.currentWorkflowIssue(workflow, issue)
	if err != nil {
		return nil, err
	}
	issue = refreshedIssue
	permissions := r.permissionConfigForIssue(issue, activeWorkflow.Config.Codex.ApprovalPolicy, activeWorkflow.Config.Codex.InitialCollaborationMode)
	planMode := strings.EqualFold(strings.TrimSpace(permissions.InitialCollaborationMode), config.InitialCollaborationModePlan)
	client, err := r.startRuntimeClient(ctx, activeWorkflow, workspacePath, issue, permissions)
	if err != nil {
		return &RunResult{Success: false, Error: err}, nil
	}
	defer client.Close()

	for turn := 1; turn <= workflow.Config.Agent.MaxTurns; turn++ {
		activeWorkflow, refreshedIssue, err := r.currentWorkflowIssue(workflow, issue)
		if err != nil {
			return nil, err
		}
		issue = refreshedIssue
		permissions = r.permissionConfigForIssue(issue, activeWorkflow.Config.Codex.ApprovalPolicy, activeWorkflow.Config.Codex.InitialCollaborationMode)
		agentruntime.ApplyPermissionConfig(client, runtimePermissionConfig(permissions))
		prepared, err := r.prepareTurnPromptWithWorkspace(activeWorkflow, issue, attempt, turn, workspacePath)
		if err != nil {
			return nil, err
		}
		consumePlanRevision := turn == 1 && r.planModeForIssue(activeWorkflow, issue) && issueHasPendingPlanRevision(issue)
		input, err := r.prepareRuntimeTurnInput(client.Capabilities(), workspacePath, issue, prepared.Prompt, turn == 1)
		if err != nil {
			return &RunResult{
				Success:    false,
				Output:     client.Output(),
				Error:      err,
				AppSession: client.Session(),
			}, nil
		}
		title := strings.TrimSpace(fmt.Sprintf("%s: %s", issue.Identifier, issue.Title))
		if title == ":" {
			title = "Maestro turn"
		}
		if consumePlanRevision {
			if err := r.clearPendingPlanRevision(issue, attempt); err != nil {
				return &RunResult{
					Success:    false,
					Output:     client.Output(),
					Error:      err,
					AppSession: client.Session(),
				}, nil
			}
		}
		var deliverErr error
		if err := client.RunTurn(ctx, agentruntime.TurnRequest{Title: title, Input: input}, func(session *agentruntime.Session) {
			if client.Capabilities().SupportsResume() {
				deliverErr = r.markDeliveredCommands(issue, prepared.Commands, "next_run", session.ThreadID, attempt)
			}
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
		if !client.Capabilities().SupportsResume() {
			if err := r.markDeliveredCommands(issue, prepared.Commands, "next_run", "", attempt); err != nil {
				return &RunResult{
					Success:    false,
					Output:     client.Output(),
					Error:      err,
					AppSession: client.Session(),
				}, nil
			}
		}
		requestedPlanApproval, err := r.capturePendingPlanApproval(issue, attempt, client.Session(), planMode && client.Capabilities().SupportsPlanGating())
		if err != nil {
			return &RunResult{
				Success:    false,
				Output:     client.Output(),
				Error:      err,
				AppSession: client.Session(),
			}, nil
		}
		if requestedPlanApproval {
			return &RunResult{
				Success:    false,
				Output:     client.Output(),
				StopReason: planApprovalStopReason,
				AppSession: client.Session(),
			}, nil
		}
		deliveredManualCommands, err := r.runPendingCommandsInActiveRuntime(ctx, client, workflow, issue, attempt, title)
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

		refreshed, continueRun := r.refreshForContinuation(workflow, runPhase, issue.ID)
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

func (r *Runner) startRuntimeClient(ctx context.Context, workflow *config.Workflow, workspacePath string, issue *kanban.Issue, permissions permissionConfig) (agentruntime.Client, error) {
	if workflow == nil || issue == nil {
		return nil, fmt.Errorf("workflow and issue are required")
	}
	startRuntime := r.runtimeStarter
	if startRuntime == nil {
		startRuntime = runtimefactory.StartWorkflow
	}
	return startRuntime(ctx, runtimefactory.WorkflowStartRequest{
		Workflow:        workflow,
		WorkspacePath:   workspacePath,
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		Env:             os.Environ(),
		Permissions:     runtimePermissionConfig(permissions),
		DynamicTools:    r.extensions.Specs(),
		ToolExecutor:    r.extensionToolExecutor(),
		ResumeToken:     strings.TrimSpace(issue.ResumeThreadID),
	}, agentruntime.Observers{
		OnSessionUpdate: func(session *agentruntime.Session) {
			if r.sessionObserver == nil || issue == nil || session == nil {
				return
			}
			r.sessionObserver(issue.ID, session)
		},
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			if r.activityObserver == nil || issue == nil {
				return
			}
			r.activityObserver(issue.ID, event)
		},
		OnPendingInteraction: func(interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder) {
			if r.interactionObserver == nil || issue == nil || interaction == nil {
				return
			}
			r.interactionObserver(issue.ID, interaction, responder)
		},
		OnPendingInteractionDone: func(interactionID string) {
			if r.interactionDoneObserver == nil || issue == nil {
				return
			}
			r.interactionDoneObserver(issue.ID, interactionID)
		},
	})
}

func runtimePermissionConfig(config permissionConfig) agentruntime.PermissionConfig {
	return agentruntime.PermissionConfig{
		ApprovalPolicy:    config.ApprovalPolicy,
		ThreadSandbox:     config.ThreadSandbox,
		TurnSandboxPolicy: config.TurnSandboxPolicy,
		CollaborationMode: config.InitialCollaborationMode,
	}
}

func (r *Runner) capturePendingPlanApproval(issue *kanban.Issue, attempt int, session *agentruntime.Session, planMode bool) (bool, error) {
	if issue == nil || !planMode {
		return false, nil
	}
	planMarkdown := extractProposedPlanMarkdown(finalAnswerFromSession(session))
	if planMarkdown == "" {
		return false, nil
	}
	requestedAt := time.Now().UTC()
	threadID := ""
	turnID := ""
	if session != nil {
		threadID = strings.TrimSpace(session.ThreadID)
		turnID = strings.TrimSpace(session.TurnID)
	}
	if err := r.store.SetIssuePendingPlanApprovalWithContext(issue, planMarkdown, requestedAt, attempt, threadID, turnID); err != nil {
		return false, err
	}
	if err := r.store.AppendRuntimeEvent("plan_approval_requested", map[string]interface{}{
		"issue_id":     issue.ID,
		"identifier":   issue.Identifier,
		"phase":        string(issue.WorkflowPhase),
		"attempt":      attempt,
		"requested_at": requestedAt.Format(time.RFC3339),
		"markdown":     planMarkdown,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Runner) clearPendingPlanRevision(issue *kanban.Issue, attempt int) error {
	if r.store == nil || !issueHasPendingPlanRevision(issue) {
		return nil
	}
	revisionMarkdown := strings.TrimSpace(issue.PendingPlanRevisionMarkdown)
	requestedAt := issue.PendingPlanRevisionRequestedAt
	if err := r.store.ClearIssuePendingPlanRevision(issue.ID, "turn_started"); err != nil {
		return err
	}
	issue.PendingPlanRevisionMarkdown = ""
	issue.PendingPlanRevisionRequestedAt = nil
	r.recordPlanRevisionRuntimeEvent(issue, "plan_revision_cleared", attempt, requestedAt, revisionMarkdown, "turn_started")
	return nil
}

func (r *Runner) recordPlanRevisionRuntimeEvent(issue *kanban.Issue, kind string, attempt int, requestedAt *time.Time, markdown, reason string) {
	if r.store == nil || issue == nil {
		return
	}
	payload := map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"title":      issue.Title,
		"phase":      string(issue.WorkflowPhase),
		"attempt":    attempt,
		"markdown":   markdown,
		"reason":     reason,
		"cleared_at": time.Now().UTC().Format(time.RFC3339),
	}
	if requestedAt != nil && !requestedAt.IsZero() {
		payload["requested_at"] = requestedAt.UTC().Format(time.RFC3339)
	}
	_ = r.store.AppendRuntimeEventOnly(kind, payload)
}

func finalAnswerFromSession(session *agentruntime.Session) string {
	if session == nil {
		return ""
	}
	for i := len(session.History) - 1; i >= 0; i-- {
		event := session.History[i]
		if event.Type != "item.completed" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(event.ItemType), "agentMessage") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(event.ItemPhase), "final_answer") {
			continue
		}
		if strings.TrimSpace(event.Message) != "" {
			return event.Message
		}
	}
	if strings.Contains(session.LastMessage, "<proposed_plan>") {
		return session.LastMessage
	}
	return ""
}

// Maestro treats a single explicit <proposed_plan> block as the authoritative plan payload.
// Stream-json is useful for debugging, but this parser only relies on the final message body.
func extractProposedPlanMarkdown(message string) string {
	matches := proposedPlanBlockPattern.FindStringSubmatch(message)
	if len(matches) != 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func (r *Runner) currentWorkflowIssue(workflow *config.Workflow, issue *kanban.Issue) (*config.Workflow, *kanban.Issue, error) {
	if issue == nil {
		return nil, nil, fmt.Errorf("issue is required")
	}
	refreshed, err := r.store.GetIssue(issue.ID)
	if err != nil {
		return nil, nil, err
	}
	if refreshed.ResumeThreadID == "" {
		refreshed.ResumeThreadID = issue.ResumeThreadID
	}
	return workflow, refreshed, nil
}

func (r *Runner) prepareRuntimeTurnInput(capabilities agentruntime.Capabilities, workspacePath string, issue *kanban.Issue, prompt string, includeImages bool) ([]agentruntime.InputItem, error) {
	input := []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: prompt}}
	if !capabilities.SupportsLocalImageInput() {
		return input, nil
	}
	if !includeImages || issue == nil {
		return input, nil
	}

	imageInput, err := r.stageIssueAssetsForRuntime(workspacePath, issue)
	if err != nil {
		return nil, err
	}
	return append(input, imageInput...), nil
}

func (r *Runner) stageIssueAssetsForRuntime(workspacePath string, issue *kanban.Issue) ([]agentruntime.InputItem, error) {
	assets, err := r.store.ListIssueAssets(issue.ID)
	if err != nil {
		return nil, fmt.Errorf("load issue assets for %s: %w", issue.Identifier, err)
	}
	if len(assets) == 0 {
		return nil, nil
	}

	stageDir := filepath.Join(workspacePath, filepath.FromSlash(appServerIssueAssetStageDir))
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return nil, fmt.Errorf("create issue asset staging directory for %s: %w", issue.Identifier, err)
	}

	imageAssets := make([]kanban.IssueAsset, 0, len(assets))
	for _, asset := range assets {
		if !strings.HasPrefix(strings.TrimSpace(asset.ContentType), "image/") {
			continue
		}
		imageAssets = append(imageAssets, asset)
	}
	input := make([]agentruntime.InputItem, 0, len(imageAssets))
	for _, asset := range imageAssets {
		_, srcPath, err := r.store.GetIssueAssetContent(issue.ID, asset.ID)
		if err != nil {
			return nil, fmt.Errorf("stage issue asset %s for %s: %w", asset.ID, issue.Identifier, err)
		}

		stagedName := stagedIssueAssetFilename(asset)
		stagedPath := filepath.Join(stageDir, stagedName)
		if err := copyIssueAssetToWorkspace(srcPath, stagedPath, asset.ID); err != nil {
			return nil, fmt.Errorf("stage issue asset %s for %s: %w", asset.ID, issue.Identifier, err)
		}

		relPath, err := filepath.Rel(workspacePath, stagedPath)
		if err != nil {
			return nil, fmt.Errorf("stage issue asset %s for %s: %w", asset.ID, issue.Identifier, err)
		}
		if relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
			return nil, fmt.Errorf("stage issue asset %s for %s: staged path escaped workspace", asset.ID, issue.Identifier)
		}
		input = append(input, agentruntime.InputItem{
			Kind: agentruntime.InputItemLocalImage,
			Path: filepath.ToSlash(relPath),
			Name: asset.Filename,
		})
	}
	return input, nil
}

func stagedIssueAssetFilename(asset kanban.IssueAsset) string {
	name := strings.TrimSpace(filepath.Base(asset.Filename))
	name = strings.NewReplacer("/", "_", "\\", "_").Replace(name)
	if name == "" || name == "." {
		name = "image"
	}
	return asset.ID + "-" + name
}

func copyIssueAssetToWorkspace(srcPath, dstPath, assetID string) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	tmpFile, err := os.CreateTemp(filepath.Dir(dstPath), assetID+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		_ = tmpFile.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Remove(dstPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (r *Runner) refreshForContinuation(workflow *config.Workflow, runPhase kanban.WorkflowPhase, issueID string) (*kanban.Issue, bool) {
	refreshed, err := r.service.RefreshIssueByID(context.Background(), issueID)
	if err != nil {
		refreshed, err = r.store.GetIssue(issueID)
	}
	if err != nil {
		return nil, false
	}
	return refreshed, shouldContinueRunPhase(workflow, runPhase, refreshed)
}

func shouldContinueRunPhase(workflow *config.Workflow, runPhase kanban.WorkflowPhase, issue *kanban.Issue) bool {
	if workflow == nil || issue == nil {
		return false
	}
	switch runPhase {
	case kanban.WorkflowPhaseReview:
		return workflow.Config.Phases.Review.Enabled && issue.State == kanban.StateInReview
	case kanban.WorkflowPhaseDone:
		return false
	default:
		return issue.State != kanban.StateInReview && isActiveState(workflow, string(issue.State))
	}
}

func (r *Runner) buildTurnPrompt(workflow *config.Workflow, issue *kanban.Issue, attempt int, turn int) (string, error) {
	prepared, err := r.prepareTurnPrompt(workflow, issue, attempt, turn)
	if err != nil {
		return "", err
	}
	return prepared.Prompt, nil
}

func (r *Runner) prepareTurnPrompt(workflow *config.Workflow, issue *kanban.Issue, attempt int, turn int) (preparedTurnPrompt, error) {
	return r.prepareTurnPromptWithWorkspace(workflow, issue, attempt, turn, "")
}

func (r *Runner) prepareTurnPromptWithWorkspace(workflow *config.Workflow, issue *kanban.Issue, attempt int, turn int, workspacePath string) (preparedTurnPrompt, error) {
	recoveryNote, err := r.workspaceRecoveryNoteForPrompt(issue, workspacePath, turn)
	if err != nil {
		return preparedTurnPrompt{}, err
	}
	planMode := r.planModeForIssue(workflow, issue)
	if turn > 1 {
		continuationGuidance := strings.TrimSpace(`
Continuation guidance:

- The previous turn completed normally, but the issue is still in an active state.
- This is continuation turn #%d of %d for the current agent run.
- Resume from the current workspace state instead of restarting from scratch.
- The original task instructions are already present in the thread history; do not restate them before acting.
- If a verification approach was blocked by local tooling or browser issues, switch to another deterministic local check instead of retrying the same path.
`)
		if planMode {
			continuationGuidance = strings.TrimSpace(continuationPlanningGuidance)
		}
		prompt := fmt.Sprintf(continuationGuidance, turn, workflow.Config.Agent.MaxTurns)
		return preparedTurnPrompt{Prompt: appendWorkspaceRecoveryNote(prompt, recoveryNote)}, nil
	}
	phase := issue.WorkflowPhase
	if !phase.IsValid() {
		phase = kanban.DefaultWorkflowPhaseForState(issue.State)
	}
	projectCtx, err := r.projectPromptContext(issue.ProjectID)
	if err != nil {
		return preparedTurnPrompt{}, err
	}
	ctx := map[string]interface{}{
		"issue": map[string]interface{}{
			"id":           issue.ID,
			"identifier":   issue.Identifier,
			"title":        issue.Title,
			"description":  issue.Description,
			"state":        string(issue.State),
			"priority":     issue.Priority,
			"labels":       issue.Labels,
			"agent_name":   issue.AgentName,
			"agent_prompt": issue.AgentPrompt,
			"branch_name":  issue.BranchName,
			"pr_url":       issue.PRURL,
			"blocked_by":   issue.BlockedBy,
			"created_at":   issue.CreatedAt.Format(time.RFC3339),
			"updated_at":   issue.UpdatedAt.Format(time.RFC3339),
		},
		"project":   projectCtx,
		"attempt":   nil,
		"phase":     string(phase),
		"plan_mode": planMode,
	}
	if attempt > 0 {
		ctx["attempt"] = attempt
	}
	rendered, err := config.RenderLiquidTemplate(promptTemplateForPhase(workflow, phase), ctx)
	if err != nil {
		return preparedTurnPrompt{}, fmt.Errorf("template_render_error: %w", err)
	}
	rendered = strings.TrimSpace(rendered)
	rendered = appendAgentInstructions(rendered, issue)
	commands, err := r.pendingCommandsForIssue(issue.ID)
	if err != nil {
		return preparedTurnPrompt{}, err
	}
	rendered = appendOperatorCommands(rendered, commands)
	rendered = prependWorkspaceRecoveryNote(rendered, recoveryNote)
	if revisionNote := pendingPlanRevisionMarkdownForPrompt(issue, planMode, turn); revisionNote != "" {
		rendered = prependPlanRevisionNote(rendered, revisionNote)
	}
	guidance := firstTurnExecutionGuidance
	if planMode {
		guidance = firstTurnPlanningGuidance
	}
	if rendered == "" {
		return preparedTurnPrompt{
			Prompt:   strings.TrimSpace(guidance),
			Commands: commands,
		}, nil
	}
	return preparedTurnPrompt{
		Prompt:   rendered + "\n\n" + strings.TrimSpace(guidance),
		Commands: commands,
	}, nil
}

func issueHasPendingPlanRevision(issue *kanban.Issue) bool {
	return issue != nil && strings.TrimSpace(issue.PendingPlanRevisionMarkdown) != "" && issue.PendingPlanRevisionRequestedAt != nil
}

func workspaceRecoveryNoteForPath(workspacePath string) (string, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return "", nil
	}
	active, err := workspaceHasActiveRebase(workspacePath)
	if err != nil {
		return "", err
	}
	if !active {
		return "", nil
	}
	return workspaceRecoveryNoteText(), nil
}

func (r *Runner) workspaceRecoveryNoteForPrompt(issue *kanban.Issue, workspacePath string, turn int) (string, error) {
	note, err := workspaceRecoveryNoteForPath(workspacePath)
	if err != nil || note != "" || turn != 1 || r.store == nil || issue == nil {
		return note, err
	}
	events, err := r.store.ListIssueRuntimeEvents(issue.ID, 50)
	if err != nil {
		return "", err
	}
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Kind {
		case "workspace_bootstrap_recovery":
			return workspaceRecoveryNoteText(), nil
		case "workspace_bootstrap_created", "workspace_bootstrap_reused", "workspace_bootstrap_preserved", "workspace_bootstrap_failed":
			return "", nil
		}
	}
	return "", nil
}

func appendWorkspaceRecoveryNote(prompt, recoveryNote string) string {
	prompt = strings.TrimSpace(prompt)
	recoveryNote = strings.TrimSpace(recoveryNote)
	if recoveryNote == "" {
		return prompt
	}
	if prompt == "" {
		return recoveryNote
	}
	return prompt + "\n\n" + recoveryNote
}

func pendingPlanRevisionMarkdownForPrompt(issue *kanban.Issue, planMode bool, turn int) string {
	if issue == nil || !planMode || turn != 1 || !issue.PlanApprovalPending || !issueHasPendingPlanRevision(issue) {
		return ""
	}
	return strings.TrimSpace(issue.PendingPlanRevisionMarkdown)
}

func prependPlanRevisionNote(prompt, revisionNote string) string {
	prompt = strings.TrimSpace(prompt)
	revisionNote = strings.TrimSpace(revisionNote)
	if revisionNote == "" {
		return prompt
	}
	section := "Plan revision note:\n\n" + revisionNote
	if prompt == "" {
		return section
	}
	return section + "\n\n" + prompt
}

func prependWorkspaceRecoveryNote(prompt, recoveryNote string) string {
	prompt = strings.TrimSpace(prompt)
	recoveryNote = strings.TrimSpace(recoveryNote)
	if recoveryNote == "" {
		return prompt
	}
	if prompt == "" {
		return recoveryNote
	}
	return recoveryNote + "\n\n" + prompt
}

func (r *Runner) projectPromptContext(projectID string) (map[string]interface{}, error) {
	ctx := map[string]interface{}{
		"id":          "",
		"name":        "",
		"description": "",
	}
	if strings.TrimSpace(projectID) == "" {
		return ctx, nil
	}
	project, err := r.store.GetProject(projectID)
	if err != nil {
		if kanban.IsNotFound(err) {
			return ctx, nil
		}
		return nil, err
	}
	ctx["id"] = project.ID
	ctx["name"] = project.Name
	ctx["description"] = project.Description
	return ctx, nil
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

func (r *Runner) planModeForIssue(workflow *config.Workflow, issue *kanban.Issue) bool {
	if workflow == nil {
		return false
	}
	permissions := r.permissionConfigForIssue(issue, workflow.Config.Codex.ApprovalPolicy, workflow.Config.Codex.InitialCollaborationMode)
	return strings.EqualFold(strings.TrimSpace(permissions.InitialCollaborationMode), config.InitialCollaborationModePlan)
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

func appendAgentInstructions(prompt string, issue *kanban.Issue) string {
	if issue == nil {
		return strings.TrimSpace(prompt)
	}
	agentName := strings.TrimSpace(issue.AgentName)
	agentPrompt := strings.TrimSpace(issue.AgentPrompt)
	if agentName == "" && agentPrompt == "" {
		return strings.TrimSpace(prompt)
	}
	lines := []string{"Issue-specific agent context:"}
	if agentName != "" {
		lines = append(lines, "", "- Assigned agent: "+agentName)
	}
	if agentPrompt != "" {
		lines = append(lines, "- Additional instructions: "+agentPrompt)
	}
	section := strings.Join(lines, "\n")
	prompt = strings.TrimSpace(prompt)
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
	if err := r.store.MarkIssueAgentCommandsDeliveredIfUnchanged(issue.ID, commands, mode, threadID, attempt); err != nil {
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

func (r *Runner) runPendingCommandsInActiveRuntime(ctx context.Context, client agentruntime.Client, workflow *config.Workflow, issue *kanban.Issue, attempt int, title string) (bool, error) {
	if client == nil || !client.Capabilities().SupportsResume() {
		return false, nil
	}
	deadline := time.Now().Add(activeThreadCommandPollWindow)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
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
		case <-ticker.C:
		}
	}
	if len(commands) == 0 {
		return false, nil
	}
	activeWorkflow, refreshedIssue, err := r.currentWorkflowIssue(workflow, issue)
	if err != nil {
		return false, err
	}
	issue = refreshedIssue
	permissions := r.permissionConfigForIssue(issue, activeWorkflow.Config.Codex.ApprovalPolicy, activeWorkflow.Config.Codex.InitialCollaborationMode)
	agentruntime.ApplyPermissionConfig(client, runtimePermissionConfig(permissions))
	var deliverErr error
	if err := client.RunTurn(ctx, agentruntime.TurnRequest{
		Title: title,
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: buildOperatorFollowUpPrompt(commands)}},
	}, func(session *agentruntime.Session) {
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

func (r *Runner) extensionToolExecutor() agentruntime.ToolExecutor {
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
