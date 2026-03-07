package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/olhapi/symphony-go/internal/appserver"
	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/pkg/config"
)

// Runner handles executing coding agents for issues
type Runner struct {
	config *config.Workflow
	store  *kanban.Store
}

// NewRunner creates a new agent runner
func NewRunner(workflow *config.Workflow, store *kanban.Store) *Runner {
	return &Runner{config: workflow, store: store}
}

// RunResult contains the result of an agent run
type RunResult struct {
	Success    bool
	Output     string
	Error      error
	AppSession *appserver.Session
}

// Run executes the coding agent for an issue
func (r *Runner) Run(ctx context.Context, issue *kanban.Issue) (*RunResult, error) {
	workspace, err := r.getOrCreateWorkspace(issue)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	prompt, err := r.buildPrompt(issue)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	if err := r.runHooks(ctx, workspace.Path, r.config.Config.Hooks.BeforeRun, "before_run"); err != nil {
		return nil, err
	}

	if issue.State == kanban.StateReady {
		_ = r.store.UpdateIssueState(issue.ID, kanban.StateInProgress)
	}

	result := r.executeAgent(ctx, workspace.Path, prompt)

	_ = r.runHooks(ctx, workspace.Path, r.config.Config.Hooks.AfterRun, "after_run")
	_ = r.store.UpdateWorkspaceRun(issue.ID)

	return result, nil
}

func sanitizeWorkspaceKey(identifier string) string {
	repl := strings.NewReplacer("/", "_", "\\", "_", "..", "_", " ", "_")
	out := repl.Replace(identifier)
	out = strings.Trim(out, "._")
	if out == "" {
		return "issue"
	}
	return out
}

func (r *Runner) getOrCreateWorkspace(issue *kanban.Issue) (*kanban.Workspace, error) {
	if existing, err := r.store.GetWorkspace(issue.ID); err == nil {
		return existing, nil
	}

	rootAbs, err := filepath.Abs(r.config.Config.WorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}

	workspaceKey := sanitizeWorkspaceKey(issue.Identifier)
	workspacePath := filepath.Join(rootAbs, workspaceKey)

	if fi, err := os.Lstat(workspacePath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(workspacePath)
			if err != nil {
				return nil, fmt.Errorf("workspace symlink check failed: %w", err)
			}
			resolvedAbs, _ := filepath.Abs(resolved)
			if !strings.HasPrefix(resolvedAbs, rootAbs+string(os.PathSeparator)) && resolvedAbs != rootAbs {
				return nil, fmt.Errorf("workspace symlink escape: %s outside %s", resolvedAbs, rootAbs)
			}
		}
		if !fi.IsDir() {
			if err := os.Remove(workspacePath); err != nil {
				return nil, fmt.Errorf("remove stale workspace path: %w", err)
			}
		}
	}

	createdNow := false
	if _, err := os.Stat(workspacePath); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(workspacePath, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create workspace directory: %w", err)
		}
		createdNow = true
	}

	workspace, err := r.store.CreateWorkspace(issue.ID, workspacePath)
	if err != nil {
		// might already exist due to race, read existing
		if existing, gerr := r.store.GetWorkspace(issue.ID); gerr == nil {
			workspace = existing
		} else {
			return nil, err
		}
	}

	if createdNow {
		if err := r.runHooks(context.Background(), workspacePath, r.config.Config.Hooks.AfterCreate, "after_create"); err != nil {
			return nil, err
		}
	}
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
	}{Issue: issue, LabelsJoined: strings.Join(issue.Labels, ", ")}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute prompt template: %w", err)
	}
	return buf.String(), nil
}

func (r *Runner) runHooks(parentCtx context.Context, workspacePath string, hooks []string, hookName string) error {
	for _, hook := range hooks {
		hookCtx := parentCtx
		var cancel context.CancelFunc
		if r.config.Config.Hooks.TimeoutSec > 0 {
			hookCtx, cancel = context.WithTimeout(parentCtx, time.Duration(r.config.Config.Hooks.TimeoutSec)*time.Second)
			defer cancel()
		}

		cmd := exec.CommandContext(hookCtx, "sh", "-c", hook)
		cmd.Dir = workspacePath
		cmd.Env = append(os.Environ(), "WORKSPACE_PATH="+workspacePath)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		if errors.Is(hookCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("workspace hook timeout (%s): %w", hookName, hookCtx.Err())
		}
		if err != nil {
			return fmt.Errorf("workspace hook failed (%s): %v: %s", hookName, err, out.String())
		}
	}
	return nil
}

func (r *Runner) executeAgent(ctx context.Context, workspacePath, prompt string) *RunResult {
	executable := r.config.Config.Agent.Executable
	args := r.config.Config.Agent.Args

	if r.config.Config.Agent.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(r.config.Config.Agent.Timeout)*time.Second)
		defer cancel()
	}

	mode := strings.ToLower(strings.TrimSpace(r.config.Config.Agent.Mode))
	if mode == "app_server" {
		return r.executeAgentAppServer(ctx, workspacePath, executable, args, prompt)
	}
	return r.executeAgentStdio(ctx, workspacePath, executable, args, prompt)
}

func (r *Runner) executeAgentStdio(ctx context.Context, workspacePath, executable string, args []string, prompt string) *RunResult {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = workspacePath
	cmd.Env = r.config.Config.GetEnv()
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
	return &RunResult{Success: err == nil, Output: output, Error: err}
}

func (r *Runner) executeAgentAppServer(ctx context.Context, workspacePath, executable string, args []string, prompt string) *RunResult {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = workspacePath
	cmd.Env = append(r.config.Config.GetEnv(),
		"SYMPHONY_AGENT_MODE=app_server",
		"SYMPHONY_PROMPT_LEN="+fmt.Sprintf("%d", len(prompt)),
	)
	cmd.Stdin = strings.NewReader(prompt)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return &RunResult{Success: false, Error: err}
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return &RunResult{Success: false, Error: err}
	}

	if err := cmd.Start(); err != nil {
		return &RunResult{Success: false, Error: err}
	}

	session := &appserver.Session{}
	var outMu sync.Mutex
	var out bytes.Buffer

	consume := func(scanner *bufio.Scanner, fromErr bool) {
		for scanner.Scan() {
			line := scanner.Text()
			if evt, ok := appserver.ParseEventLine(line); ok {
				session.ApplyEvent(evt)
			}
			outMu.Lock()
			if fromErr {
				out.WriteString("[stderr] ")
			}
			out.WriteString(line)
			out.WriteByte('\n')
			outMu.Unlock()
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); consume(bufio.NewScanner(stdoutPipe), false) }()
	go func() { defer wg.Done(); consume(bufio.NewScanner(stderrPipe), true) }()

	err = cmd.Wait()
	wg.Wait()

	return &RunResult{Success: err == nil, Output: strings.TrimSpace(out.String()), Error: err, AppSession: session}
}
