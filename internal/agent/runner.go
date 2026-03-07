package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/pkg/config"
)

// Runner handles executing coding agents for issues
type Runner struct {
	config  *config.Workflow
	store   *kanban.Store
}

// NewRunner creates a new agent runner
func NewRunner(workflow *config.Workflow, store *kanban.Store) *Runner {
	return &Runner{
		config:  workflow,
		store:   store,
	}
}

// RunResult contains the result of an agent run
type RunResult struct {
	Success bool
	Output  string
	Error   error
}

// Run executes the coding agent for an issue
func (r *Runner) Run(ctx context.Context, issue *kanban.Issue) (*RunResult, error) {
	// Get or create workspace
	workspace, err := r.getOrCreateWorkspace(issue)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	// Build prompt from template
	prompt, err := r.buildPrompt(issue)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	// Run before hooks
	if err := r.runHooks(workspace.Path, r.config.Config.Hooks.BeforeRun); err != nil {
		return nil, fmt.Errorf("before_run hook failed: %w", err)
	}

	// Update issue state to in_progress
	if issue.State == kanban.StateReady {
		_ = r.store.UpdateIssueState(issue.ID, kanban.StateInProgress)
	}

	// Run the agent
	result := r.executeAgent(ctx, workspace.Path, prompt)

	// Run after hooks
	_ = r.runHooks(workspace.Path, r.config.Config.Hooks.AfterRun)

	// Update workspace run count
	_ = r.store.UpdateWorkspaceRun(issue.ID)

	return result, nil
}

func (r *Runner) getOrCreateWorkspace(issue *kanban.Issue) (*kanban.Workspace, error) {
	workspace, err := r.store.GetWorkspace(issue.ID)
	if err == nil {
		return workspace, nil
	}

	// Create new workspace
	workspacePath := filepath.Join(r.config.Config.WorkspaceRoot, issue.Identifier)
	if err := os.MkdirAll(workspacePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create workspace directory: %w", err)
	}

	workspace, err = r.store.CreateWorkspace(issue.ID, workspacePath)
	if err != nil {
		return nil, err
	}

	// Run after_create hooks
	_ = r.runHooks(workspacePath, r.config.Config.Hooks.AfterCreate)

	return workspace, nil
}

func (r *Runner) buildPrompt(issue *kanban.Issue) (string, error) {
	tmpl, err := template.New("prompt").Parse(r.config.PromptTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse prompt template: %w", err)
	}

	data := struct {
		*kanban.Issue
		LabelsJoined string
	}{
		Issue:        issue,
		LabelsJoined: strings.Join(issue.Labels, ", "),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute prompt template: %w", err)
	}

	return buf.String(), nil
}

func (r *Runner) runHooks(workspacePath string, hooks []string) error {
	for _, hook := range hooks {
		cmd := exec.Command("sh", "-c", hook)
		cmd.Dir = workspacePath
		cmd.Env = append(os.Environ(), "WORKSPACE_PATH="+workspacePath)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("hook failed: %w", err)
		}
	}
	return nil
}

func (r *Runner) executeAgent(ctx context.Context, workspacePath, prompt string) *RunResult {
	executable := r.config.Config.Agent.Executable
	args := r.config.Config.Agent.Args

	// Build command
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = workspacePath
	cmd.Env = r.config.Config.GetEnv()

	// Pass prompt via stdin
	cmd.Stdin = strings.NewReader(prompt)

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set timeout if configured
	if r.config.Config.Agent.Timeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(r.config.Config.Agent.Timeout)*time.Second)
		defer cancel()
		cmd.Cancel = func() error {
			return cmd.Process.Kill()
		}
		ctx = timeoutCtx
	}

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n" + stderr.String()
	}

	return &RunResult{
		Success: err == nil,
		Output:  output,
		Error:   err,
	}
}
