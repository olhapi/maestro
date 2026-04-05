package orchestrator

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agent"
	"github.com/olhapi/maestro/internal/agentruntime"
	codexruntime "github.com/olhapi/maestro/internal/agentruntime/codex"
	"github.com/olhapi/maestro/internal/codexschema"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/internal/providers"
	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
	"github.com/olhapi/maestro/internal/testutil/inprocessserver"
	"github.com/olhapi/maestro/pkg/config"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func neutralWorkflowControlBlocks(defaultRuntime, appServerCommand, stdioCommand, approvalPolicy string, maxConcurrentAgents, maxTurns, maxRetryBackoffMs, maxAutomaticRetries, turnTimeoutMs, readTimeoutMs, stallTimeoutMs int) string {
	return fmt.Sprintf(`orchestrator:
  max_concurrent_agents: %d
  max_turns: %d
  max_retry_backoff_ms: %d
  max_automatic_retries: %d
  dispatch_mode: parallel
runtime:
  default: %s
  codex-appserver:
    provider: codex
    transport: app_server
    command: %s
    expected_version: %s
    approval_policy: %s
    initial_collaboration_mode: default
    turn_timeout_ms: %d
    read_timeout_ms: %d
    stall_timeout_ms: %d
  codex-stdio:
    provider: codex
    transport: stdio
    command: %s
    expected_version: %s
    approval_policy: never
    turn_timeout_ms: %d
    read_timeout_ms: %d
    stall_timeout_ms: %d
  claude:
    provider: claude
    transport: stdio
    command: claude
    approval_policy: never
    turn_timeout_ms: %d
    read_timeout_ms: %d
    stall_timeout_ms: %d
`, maxConcurrentAgents, maxTurns, maxRetryBackoffMs, maxAutomaticRetries, defaultRuntime, appServerCommand, codexschema.SupportedVersion, approvalPolicy, turnTimeoutMs, readTimeoutMs, stallTimeoutMs, stdioCommand, codexschema.SupportedVersion, turnTimeoutMs, readTimeoutMs, stallTimeoutMs, turnTimeoutMs, readTimeoutMs, stallTimeoutMs)
}

type blockingRunner struct {
	started      chan struct{}
	ctxCancelled chan struct{}
	release      chan struct{}
}

func runGitForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv(dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output))
}

func gitTestEnv(dir string) []string {
	env := os.Environ()
	filtered := env[:0]
	for _, value := range env {
		if !strings.HasPrefix(value, "GIT_") {
			filtered = append(filtered, value)
		}
	}
	if strings.TrimSpace(dir) != "" {
		filtered = append(filtered, "PWD="+dir)
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

func samplePNGBytesForTest() []byte {
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

func (r *blockingRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	close(r.started)
	<-ctx.Done()
	close(r.ctxCancelled)
	<-r.release
	return nil, ctx.Err()
}

func (r *blockingRunner) CleanupWorkspace(context.Context, *kanban.Issue) error {
	return nil
}

type countingProvider struct {
	mu        sync.Mutex
	listCalls int
}

type blockingIssueProvider struct {
	issue      kanban.Issue
	getStarted chan struct{}
	getRelease chan struct{}
}

type previewCommentProvider struct {
	store         *kanban.Store
	issue         kanban.Issue
	createStarted chan struct{}
	createRelease chan struct{}
}

func (p *countingProvider) Kind() string {
	return "stub"
}

func (p *countingProvider) Capabilities() kanban.ProviderCapabilities {
	return kanban.DefaultCapabilities("stub")
}

func (p *countingProvider) ValidateProject(context.Context, *kanban.Project) error {
	return nil
}

func (p *countingProvider) ListIssues(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listCalls++
	return nil, nil
}

func (p *countingProvider) GetIssue(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
	return nil, kanban.ErrNotFound
}

func (p *countingProvider) CreateIssue(context.Context, *kanban.Project, providers.IssueCreateInput) (*kanban.Issue, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *countingProvider) UpdateIssue(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *countingProvider) DeleteIssue(context.Context, *kanban.Project, *kanban.Issue) error {
	return providers.ErrUnsupportedCapability
}

func (p *countingProvider) SetIssueState(context.Context, *kanban.Project, *kanban.Issue, string) (*kanban.Issue, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *countingProvider) ListIssueComments(context.Context, *kanban.Project, *kanban.Issue) ([]kanban.IssueComment, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *countingProvider) CreateIssueComment(context.Context, *kanban.Project, *kanban.Issue, providers.IssueCommentInput) (*kanban.IssueComment, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *countingProvider) UpdateIssueComment(context.Context, *kanban.Project, *kanban.Issue, string, providers.IssueCommentInput) (*kanban.IssueComment, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *countingProvider) DeleteIssueComment(context.Context, *kanban.Project, *kanban.Issue, string) error {
	return providers.ErrUnsupportedCapability
}

func (p *countingProvider) GetIssueCommentAttachmentContent(context.Context, *kanban.Project, *kanban.Issue, string, string) (*providers.IssueCommentAttachmentContent, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *countingProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.listCalls
}

func (p *blockingIssueProvider) Kind() string {
	return "stub"
}

func (p *blockingIssueProvider) Capabilities() kanban.ProviderCapabilities {
	return kanban.DefaultCapabilities("stub")
}

func (p *blockingIssueProvider) ValidateProject(context.Context, *kanban.Project) error {
	return nil
}

func (p *blockingIssueProvider) ListIssues(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error) {
	return nil, nil
}

func (p *blockingIssueProvider) GetIssue(ctx context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
	select {
	case p.getStarted <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.getRelease:
	}
	cp := p.issue
	return &cp, nil
}

func (p *blockingIssueProvider) CreateIssue(context.Context, *kanban.Project, providers.IssueCreateInput) (*kanban.Issue, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *blockingIssueProvider) UpdateIssue(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *blockingIssueProvider) DeleteIssue(context.Context, *kanban.Project, *kanban.Issue) error {
	return providers.ErrUnsupportedCapability
}

func (p *blockingIssueProvider) SetIssueState(context.Context, *kanban.Project, *kanban.Issue, string) (*kanban.Issue, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *blockingIssueProvider) ListIssueComments(context.Context, *kanban.Project, *kanban.Issue) ([]kanban.IssueComment, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *blockingIssueProvider) CreateIssueComment(context.Context, *kanban.Project, *kanban.Issue, providers.IssueCommentInput) (*kanban.IssueComment, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *blockingIssueProvider) UpdateIssueComment(context.Context, *kanban.Project, *kanban.Issue, string, providers.IssueCommentInput) (*kanban.IssueComment, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *blockingIssueProvider) DeleteIssueComment(context.Context, *kanban.Project, *kanban.Issue, string) error {
	return providers.ErrUnsupportedCapability
}

func (p *blockingIssueProvider) GetIssueCommentAttachmentContent(context.Context, *kanban.Project, *kanban.Issue, string, string) (*providers.IssueCommentAttachmentContent, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *previewCommentProvider) Kind() string {
	return "stub"
}

func (p *previewCommentProvider) Capabilities() kanban.ProviderCapabilities {
	return kanban.DefaultCapabilities("stub")
}

func (p *previewCommentProvider) ValidateProject(context.Context, *kanban.Project) error {
	return nil
}

func (p *previewCommentProvider) ListIssues(context.Context, *kanban.Project, kanban.IssueQuery) ([]kanban.Issue, error) {
	return nil, nil
}

func (p *previewCommentProvider) GetIssue(context.Context, *kanban.Project, string) (*kanban.Issue, error) {
	cp := p.issue
	return &cp, nil
}

func (p *previewCommentProvider) CreateIssue(context.Context, *kanban.Project, providers.IssueCreateInput) (*kanban.Issue, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *previewCommentProvider) UpdateIssue(context.Context, *kanban.Project, *kanban.Issue, map[string]interface{}) (*kanban.Issue, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *previewCommentProvider) DeleteIssue(context.Context, *kanban.Project, *kanban.Issue) error {
	return providers.ErrUnsupportedCapability
}

func (p *previewCommentProvider) SetIssueState(context.Context, *kanban.Project, *kanban.Issue, string) (*kanban.Issue, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *previewCommentProvider) ListIssueComments(context.Context, *kanban.Project, *kanban.Issue) ([]kanban.IssueComment, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *previewCommentProvider) CreateIssueComment(ctx context.Context, _ *kanban.Project, issue *kanban.Issue, input providers.IssueCommentInput) (*kanban.IssueComment, error) {
	if issue == nil {
		return nil, providers.ErrUnsupportedCapability
	}
	select {
	case p.createStarted <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.createRelease:
	}
	if p.store == nil {
		body := ""
		if input.Body != nil {
			body = strings.TrimSpace(*input.Body)
		}
		return &kanban.IssueComment{
			IssueID: issue.ID,
			Body:    body,
		}, nil
	}
	comment, err := p.store.CreateIssueComment(issue.ID, kanban.IssueCommentInput{
		Body:            input.Body,
		ParentCommentID: input.ParentCommentID,
		Attachments: func() []kanban.IssueCommentAttachmentInput {
			out := make([]kanban.IssueCommentAttachmentInput, 0, len(input.Attachments))
			for _, attachment := range input.Attachments {
				out = append(out, kanban.IssueCommentAttachmentInput{
					Path:        attachment.Path,
					ContentType: attachment.ContentType,
				})
			}
			return out
		}(),
		Author: input.Author,
	})
	if err != nil {
		return nil, err
	}
	return comment, nil
}

func (p *previewCommentProvider) UpdateIssueComment(context.Context, *kanban.Project, *kanban.Issue, string, providers.IssueCommentInput) (*kanban.IssueComment, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (p *previewCommentProvider) DeleteIssueComment(context.Context, *kanban.Project, *kanban.Issue, string) error {
	return providers.ErrUnsupportedCapability
}

func (p *previewCommentProvider) GetIssueCommentAttachmentContent(context.Context, *kanban.Project, *kanban.Issue, string, string) (*providers.IssueCommentAttachmentContent, error) {
	return nil, providers.ErrUnsupportedCapability
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func setupTestOrchestrator(t *testing.T, command string) (*Orchestrator, *kanban.Store, *config.Manager, string) {
	return setupTestOrchestratorWithConcurrency(t, command, 2)
}

func setupTestOrchestratorWithConcurrency(t *testing.T, command string, maxConcurrent int) (*Orchestrator, *kanban.Store, *config.Manager, string) {
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
  active_states:
    - ready
    - in_progress
    - in_review
  terminal_states:
    - done
    - cancelled
polling:
  interval_ms: 50
workspace:
  root: ` + workspaceRoot + `
hooks:
  timeout_ms: 1000
phases:
  review:
    enabled: false
  done:
    enabled: false
` + neutralWorkflowControlBlocks("codex-stdio", "codex app-server", command, "never", maxConcurrent, 2, 100, 8, 1000, 500, 300000) + `
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0o644); err != nil {
		t.Fatalf("Failed to write workflow: %v", err)
	}
	initGitRepoForTest(t, tmpDir)

	manager, err := config.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}

	orch := New(store, manager)
	t.Cleanup(func() {
		orch.stopAllRuns()
		waitForNoRunning(t, orch, time.Second)
	})
	t.Cleanup(func() { _ = store.Close() })
	return orch, store, manager, workspaceRoot
}

func createRunningProjectIssue(t *testing.T, store *kanban.Store, title, body string, priority int, labels []string) (*kanban.Project, *kanban.Issue) {
	t.Helper()
	project, err := store.CreateProject("Platform", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", title, body, priority, labels)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	return project, issue
}

func enablePhaseWorkflow(t *testing.T, manager *config.Manager, workspaceRoot string) {
	t.Helper()
	workflowContent := `---
tracker:
  kind: kanban
  active_states:
    - ready
    - in_progress
    - in_review
  terminal_states:
    - done
    - cancelled
polling:
  interval_ms: 50
workspace:
  root: ` + workspaceRoot + `
hooks:
  timeout_ms: 1000
` + neutralWorkflowControlBlocks("codex-stdio", "codex app-server", "cat", "never", 1, 2, 100, 8, 1000, 500, 300000) + `
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
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}
}

func writeAppServerWorkflow(t *testing.T, manager *config.Manager, workspaceRoot, command, approvalPolicy string, turnTimeoutMs, stallTimeoutMs int) {
	writeAppServerWorkflowWithConcurrency(t, manager, workspaceRoot, command, approvalPolicy, turnTimeoutMs, stallTimeoutMs, 1)
}

func writeAppServerWorkflowWithConcurrency(t *testing.T, manager *config.Manager, workspaceRoot, command, approvalPolicy string, turnTimeoutMs, stallTimeoutMs, maxConcurrentAgents int) {
	t.Helper()
	workflowContent := `---
tracker:
  kind: kanban
  active_states:
    - ready
    - in_progress
    - in_review
  terminal_states:
    - done
    - cancelled
polling:
  interval_ms: 50
workspace:
  root: ` + workspaceRoot + `
hooks:
  timeout_ms: 1000
phases:
  review:
    enabled: false
  done:
    enabled: false
` + neutralWorkflowControlBlocks("codex-appserver", command, "codex exec", approvalPolicy, maxConcurrentAgents, 1, 100, 8, turnTimeoutMs, 500, stallTimeoutMs) + `
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(manager.Path(), []byte(workflowContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}
}

func setWorkflowMaxAutomaticRetries(t *testing.T, manager *config.Manager, maxAutomaticRetries int) {
	t.Helper()
	data, err := os.ReadFile(manager.Path())
	if err != nil {
		t.Fatalf("ReadFile workflow: %v", err)
	}
	updated := strings.Replace(string(data), "  max_automatic_retries: 8\n", fmt.Sprintf("  max_automatic_retries: %d\n", maxAutomaticRetries), 1)
	if err := os.WriteFile(manager.Path(), []byte(updated), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatalf("Refresh workflow: %v", err)
	}
}

func setWorkflowDispatchMode(t *testing.T, manager *config.Manager, dispatchMode string) {
	t.Helper()
	data, err := os.ReadFile(manager.Path())
	if err != nil {
		t.Fatalf("ReadFile workflow: %v", err)
	}
	updated := string(data)
	if strings.Contains(updated, "  dispatch_mode:") {
		updated = regexp.MustCompile(`(?m)^  dispatch_mode: .*$`).ReplaceAllString(updated, "  dispatch_mode: "+dispatchMode)
	} else {
		updated = strings.Replace(updated, "  mode: stdio\n", "  mode: stdio\n  dispatch_mode: "+dispatchMode+"\n", 1)
		updated = strings.Replace(updated, "  mode: app_server\n", "  mode: app_server\n  dispatch_mode: "+dispatchMode+"\n", 1)
	}
	if err := os.WriteFile(manager.Path(), []byte(updated), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatalf("Refresh workflow: %v", err)
	}
}

func waitForLiveSession(t *testing.T, orch *Orchestrator, identifier string, timeout time.Duration) agentruntime.Session {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sessions := agentruntime.SessionsFromMap(orch.LiveSessions()["sessions"].(map[string]interface{}))
		if session, ok := sessions[identifier]; ok {
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for live session %s", identifier)
	return agentruntime.Session{}
}

func waitForWorkspaceRemoval(t *testing.T, store *kanban.Store, issueID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := store.GetWorkspace(issueID); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for workspace %s removal", issueID)
}

func waitForExecutionSnapshot(t *testing.T, store *kanban.Store, issueID string, timeout time.Duration) *kanban.ExecutionSessionSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot, err := store.GetIssueExecutionSession(issueID)
		if err == nil && snapshot.RunKind != "run_started" {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for execution snapshot %s", issueID)
	return nil
}

func waitForRunStartedExecutionSnapshot(t *testing.T, store *kanban.Store, issueID string, timeout time.Duration) *kanban.ExecutionSessionSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot, err := store.GetIssueExecutionSession(issueID)
		if err == nil && snapshot != nil && snapshot.RunKind == "run_started" {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run_started execution snapshot %s", issueID)
	return nil
}

func waitForNoRunning(t *testing.T, orch *Orchestrator, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		orch.mu.RLock()
		running := len(orch.running)
		orch.mu.RUnlock()
		if running == 0 && orch.waitForActiveRuns(20*time.Millisecond) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for running orchestrator jobs to stop")
}

func waitForCondition(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func startLingeringProcessGroup(t *testing.T, childPIDPath string) (*exec.Cmd, int) {
	t.Helper()

	cmd := exec.Command("/usr/bin/python3", "-c", `
import os
import signal
import subprocess
import sys
import time

os.setsid()
child = subprocess.Popen(["/bin/sh", "-lc", 'trap "" TERM INT; while :; do sleep 1; done'])
with open(sys.argv[1], "w", encoding="utf-8") as fh:
    fh.write(str(child.pid))

def shutdown(*_args):
    raise SystemExit(0)

signal.signal(signal.SIGTERM, shutdown)
signal.signal(signal.SIGINT, shutdown)
while True:
    time.sleep(1)
`, childPIDPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lingering process group: %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		data, err := os.ReadFile(childPIDPath)
		return err == nil && strings.TrimSpace(string(data)) != ""
	})
	childPIDText, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatalf("ReadFile child pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(childPIDText)))
	if err != nil {
		t.Fatalf("Atoi child pid: %v", err)
	}
	if !testProcessAlive(childPID) || !testProcessGroupAlive(cmd.Process.Pid) {
		t.Fatalf("expected lingering process group to be alive: leader=%d child=%d", cmd.Process.Pid, childPID)
	}
	return cmd, childPID
}

func waitForRunningCount(t *testing.T, orch *Orchestrator, expected int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		orch.mu.RLock()
		running := len(orch.running)
		orch.mu.RUnlock()
		if running == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for running count %d", expected)
}

func waitForPendingInterruptCount(t *testing.T, orch *Orchestrator, expected int, timeout time.Duration) agentruntime.PendingInteractionSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot := orch.PendingInterrupts()
		if len(snapshot.Items) == expected {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pending interrupt count %d", expected)
	return agentruntime.PendingInteractionSnapshot{}
}

func firstPendingInterrupt(snapshot agentruntime.PendingInteractionSnapshot) *agentruntime.PendingInteraction {
	if len(snapshot.Items) == 0 {
		return nil
	}
	interaction := snapshot.Items[0].Clone()
	return &interaction
}

func forceRetryDue(t *testing.T, orch *Orchestrator, issueID string) {
	t.Helper()
	orch.mu.Lock()
	defer orch.mu.Unlock()
	entry, ok := orch.retries[issueID]
	if !ok {
		t.Fatalf("expected retry entry for %s", issueID)
	}
	entry.DueAt = time.Now().UTC().Add(-time.Millisecond)
	orch.retries[issueID] = entry
}

func waitForRetryEntry(t *testing.T, orch *Orchestrator, issueID string, timeout time.Duration) retryEntry {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		orch.mu.RLock()
		entry, ok := orch.retries[issueID]
		orch.mu.RUnlock()
		if ok {
			return entry
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for retry entry for %s", issueID)
	return retryEntry{}
}

func waitForIssuePauseReason(t *testing.T, store *kanban.Store, issueID, reason string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events, err := store.ListIssueRuntimeEvents(issueID, 20)
		if err == nil && len(events) > 0 {
			latest := events[len(events)-1]
			if latest.Kind == "retry_paused" && latest.Error == reason {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pause reason %s on %s", reason, issueID)
}

func waitForIssueRetryState(t *testing.T, store *kanban.Store, issueID, delayType string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events, err := store.ListIssueRuntimeEvents(issueID, 20)
		if err == nil && len(events) > 0 {
			latest := events[len(events)-1]
			if latest.Kind == "retry_scheduled" && latest.DelayType == delayType {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for retry type %s on %s", delayType, issueID)
}

func waitForIssueStateAndPhase(
	t *testing.T,
	store *kanban.Store,
	issueID string,
	state kanban.State,
	phase kanban.WorkflowPhase,
	timeout time.Duration,
) *kanban.Issue {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		issue, err := store.GetIssue(issueID)
		if err == nil && issue.State == state && issue.WorkflowPhase == phase {
			return issue
		}
		time.Sleep(20 * time.Millisecond)
	}
	current, err := store.GetIssue(issueID)
	if err != nil {
		t.Fatalf("timed out waiting for %s/%s on %s (last error: %v)", state, phase, issueID, err)
	}
	t.Fatalf("timed out waiting for %s/%s on %s (last state: %s/%s)", state, phase, issueID, current.State, current.WorkflowPhase)
	return nil
}

func assertRetryEventInvariants(t *testing.T, events []kanban.RuntimeEvent) {
	t.Helper()
	pendingRetries := 0
	paused := false
	for _, event := range events {
		switch event.Kind {
		case "retry_scheduled":
			if paused {
				t.Fatalf("found retry scheduled after pause: %+v", events)
			}
			pendingRetries++
			if pendingRetries > 1 {
				t.Fatalf("found more than one pending retry in event stream: %+v", events)
			}
			if delay := payloadInt(event.Payload, "delay_ms"); delay <= 0 {
				t.Fatalf("found non-positive retry delay in event stream: %+v", events)
			}
		case "retry_paused":
			paused = true
			pendingRetries = 0
		case "run_started", "run_completed", "run_failed", "run_unsuccessful", "manual_retry_requested":
			pendingRetries = 0
			if event.Kind != "retry_paused" {
				paused = false
			}
		}
	}
}

func writeSharedAppServerWorkflow(t *testing.T, workflowPath, workspaceRoot, command string, reviewEnabled bool, maxAutomaticRetries, turnTimeoutMs, stallTimeoutMs int) {
	t.Helper()
	workflowContent := fmt.Sprintf(`---
tracker:
  kind: kanban
  active_states:
    - ready
    - in_progress
    - in_review
  terminal_states:
    - done
    - cancelled
polling:
  interval_ms: 50
workspace:
  root: %s
hooks:
  timeout_ms: 1000
phases:
  review:
    enabled: %t
  done:
    enabled: false
%s
---
Shared retry stress harness for {{ issue.identifier }}
`, workspaceRoot, reviewEnabled, neutralWorkflowControlBlocks("codex-appserver", command, "codex exec", "never", 1, 1, 100, maxAutomaticRetries, turnTimeoutMs, 200, stallTimeoutMs))
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}
}

func TestDispatchCreatesWorkspace(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	_, issue := createRunningProjectIssue(t, store, "Ready Issue", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}
	waitForNoRunning(t, orch, time.Second)

	workspace, err := store.GetWorkspace(issue.ID)
	if err != nil {
		t.Fatalf("Expected workspace: %v", err)
	}
	if workspace.RunCount < 1 {
		t.Fatalf("expected run count >= 1, got %d", workspace.RunCount)
	}
}

func TestFailureRetryScheduling(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "false")
	_, issue := createRunningProjectIssue(t, store, "Fails", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	retry := waitForRetryEntry(t, orch, issue.ID, time.Second)
	if retry.Attempt != 1 {
		t.Fatalf("expected retry attempt 1, got %d", retry.Attempt)
	}
	if retry.DelayType != "failure" {
		t.Fatalf("expected failure retry, got %s", retry.DelayType)
	}
	waitForNoRunning(t, orch, time.Second)
}

func TestAutomaticRetryLimitPausesRunawayFailures(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestrator(t, "false")
	setWorkflowMaxAutomaticRetries(t, manager, 2)

	_, issue := createRunningProjectIssue(t, store, "Retry limited", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	for i := 0; i < 2; i++ {
		forceRetryDue(t, orch, issue.ID)
		orch.processRetries(context.Background())
		waitForNoRunning(t, orch, time.Second)
	}

	orch.mu.RLock()
	paused, pausedOK := orch.paused[issue.ID]
	_, retryScheduled := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !pausedOK {
		t.Fatal("expected retry limit to pause automatic retries")
	}
	if retryScheduled {
		t.Fatal("expected retry limit to clear scheduled retries")
	}
	if paused.Error != "retry_limit_reached" || paused.Attempt != 3 {
		t.Fatalf("unexpected paused payload: %+v", paused)
	}

	events, err := store.ListIssueRuntimeEvents(issue.ID, 20)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	if latest := events[len(events)-1]; latest.Kind != "retry_paused" || latest.Error != "retry_limit_reached" {
		t.Fatalf("unexpected latest runtime event: %+v", latest)
	}
}

func TestManualRetryResetsAutomaticRetryLimit(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestrator(t, "false")
	setWorkflowMaxAutomaticRetries(t, manager, 1)

	_, issue := createRunningProjectIssue(t, store, "Retry reset", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	forceRetryDue(t, orch, issue.ID)
	orch.processRetries(context.Background())
	waitForNoRunning(t, orch, time.Second)

	orch.mu.RLock()
	if orch.paused[issue.ID].Error != "retry_limit_reached" {
		t.Fatalf("expected retry limit pause, got %+v", orch.paused[issue.ID])
	}
	orch.mu.RUnlock()

	result := orch.RetryIssueNow(context.Background(), issue.Identifier)
	if result["status"] != "queued_now" {
		t.Fatalf("unexpected RetryIssueNow result: %#v", result)
	}

	orch.processRetries(context.Background())
	waitForNoRunning(t, orch, time.Second)

	orch.mu.RLock()
	retry, retryOK := orch.retries[issue.ID]
	paused, pausedOK := orch.paused[issue.ID]
	orch.mu.RUnlock()
	if !retryOK {
		t.Fatal("expected new failure retry after manual reset")
	}
	if pausedOK {
		t.Fatalf("expected manual retry to clear paused state, got %+v", paused)
	}
	if retry.Error == "retry_limit_reached" {
		t.Fatalf("expected reset retry payload, got %+v", retry)
	}
}

func TestContinuationRetryAfterSuccess(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	_, issue := createRunningProjectIssue(t, store, "Succeeds", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	retry := waitForRetryEntry(t, orch, issue.ID, time.Second)
	if retry.DelayType != "continuation" {
		t.Fatalf("expected continuation retry, got %s", retry.DelayType)
	}
	if retry.ResumeThreadID != "" {
		t.Fatalf("expected continuation retry to start fresh, got %+v", retry)
	}
	waitForNoRunning(t, orch, time.Second)
}

func TestImplementationSuccessTransitionsToReviewPhase(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{store: store}

	_, issue := createRunningProjectIssue(t, store, "Needs review", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated := waitForIssueStateAndPhase(t, store, issue.ID, kanban.StateInReview, kanban.WorkflowPhaseReview, time.Second)
	if updated.State != kanban.StateInReview || updated.WorkflowPhase != kanban.WorkflowPhaseReview {
		t.Fatalf("expected in_review/review, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	retry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if retry.Phase != string(kanban.WorkflowPhaseReview) {
		t.Fatalf("expected review retry, got %+v", retry)
	}
}

func TestImplementationSuccessWithoutStateTransitionPausesAutomaticRetry(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	orch.runner = &phaseScriptRunner{store: store}

	_, issue := createRunningProjectIssue(t, store, "No transition", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	orch.mu.RLock()
	paused, pausedOK := orch.paused[issue.ID]
	_, retryScheduled := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !pausedOK {
		t.Fatal("expected retry pause after same-phase successful run")
	}
	if retryScheduled {
		t.Fatal("expected no continuation retry after same-phase successful run")
	}
	if paused.Error != "no_state_transition" || paused.Attempt != 1 {
		t.Fatalf("unexpected paused payload: %+v", paused)
	}

	events, err := store.ListIssueRuntimeEvents(issue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected runtime events for paused retry")
	}
	latest := events[len(events)-1]
	if latest.Kind != "retry_paused" || latest.Error != "no_state_transition" || latest.Attempt != 1 {
		t.Fatalf("unexpected latest runtime event: %+v", latest)
	}
}

func TestImplementationSuccessWithReviewDisabledRequeuesAfterInReviewTransition(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestrator(t, "cat")
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseImplementation: func(issue *kanban.Issue) (*agent.RunResult, error) {
				if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateInReview, kanban.WorkflowPhaseReview); err != nil {
					return nil, err
				}
				return &agent.RunResult{Success: true}, nil
			},
		},
	}

	_, issue := createRunningProjectIssue(t, store, "Review disabled continuation", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated := waitForIssueStateAndPhase(t, store, issue.ID, kanban.StateInProgress, kanban.WorkflowPhaseImplementation, time.Second)
	if updated.State != kanban.StateInProgress || updated.WorkflowPhase != kanban.WorkflowPhaseImplementation {
		t.Fatalf("expected in_progress/implementation, got %s/%s", updated.State, updated.WorkflowPhase)
	}
	workflow, err := manager.Current()
	if err != nil {
		t.Fatalf("Current workflow: %v", err)
	}
	if dispatchable, reason, phase := orch.isDispatchable(workflow, updated); !dispatchable {
		t.Fatalf("expected normalized issue to remain dispatchable, got reason=%s phase=%s", reason, phase)
	}

	orch.mu.RLock()
	paused, pausedOK := orch.paused[issue.ID]
	retry, retryOK := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if pausedOK {
		t.Fatalf("expected continuation retry instead of pause, got %+v", paused)
	}
	if !retryOK {
		t.Fatal("expected continuation retry after in_review normalization")
	}
	if retry.Phase != string(kanban.WorkflowPhaseImplementation) || retry.DelayType != "continuation" {
		t.Fatalf("unexpected retry payload: %+v", retry)
	}
}

func TestIsDispatchableBlocksPendingPlanApproval(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestrator(t, "cat")
	_, issue := createRunningProjectIssue(t, store, "Pending plan approval", "", 0, nil)
	requestedAt := time.Date(2026, 3, 18, 11, 0, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Review the plan.", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}
	issue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	workflow, err := manager.Current()
	if err != nil {
		t.Fatalf("Current workflow: %v", err)
	}
	dispatchable, reason, phase := orch.isDispatchable(workflow, issue)
	if dispatchable {
		t.Fatal("expected pending plan approval to block dispatch")
	}
	if reason != "plan_approval_pending" {
		t.Fatalf("expected plan_approval_pending reason, got %q", reason)
	}
	if phase != issue.WorkflowPhase {
		t.Fatalf("expected workflow phase %q, got %q", issue.WorkflowPhase, phase)
	}
}

func TestPendingInterruptsIncludePlanApprovalRequests(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	_, issue := createRunningProjectIssue(t, store, "Pending plan approval", "", 0, nil)
	requestedAt := time.Date(2026, 3, 18, 11, 0, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Review the plan.", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}

	snapshot := orch.PendingInterrupts()
	if len(snapshot.Items) != 1 {
		t.Fatalf("expected one pending interrupt, got %#v", snapshot.Items)
	}
	current := snapshot.Items[0]
	if current.ID != issuePlanApprovalInteractionID(issue.ID) {
		t.Fatalf("expected plan approval interrupt id %q, got %q", issuePlanApprovalInteractionID(issue.ID), current.ID)
	}
	if current.Kind != agentruntime.PendingInteractionKindApproval {
		t.Fatalf("expected approval interrupt, got %q", current.Kind)
	}
	if current.CollaborationMode != "plan" {
		t.Fatalf("expected plan collaboration mode, got %q", current.CollaborationMode)
	}
	if current.Approval == nil || current.Approval.Markdown != "Review the plan." {
		t.Fatalf("expected plan markdown in interrupt payload, got %+v", current.Approval)
	}
	if current.Approval.Reason != "Review the proposed plan before execution." {
		t.Fatalf("expected plan approval reason, got %+v", current.Approval)
	}
	if current.Approval.PlanStatus != "awaiting_approval" {
		t.Fatalf("expected plan status in interrupt payload, got %+v", current.Approval)
	}
	if current.Approval.PlanVersionNumber != 1 {
		t.Fatalf("expected plan version number in interrupt payload, got %+v", current.Approval)
	}
	if current.Approval.PlanRevisionNote != "" {
		t.Fatalf("expected no pending revision note in interrupt payload, got %+v", current.Approval)
	}
	if current.LastActivity != "Plan v1 ready for approval." {
		t.Fatalf("expected plan approval activity summary, got %q", current.LastActivity)
	}
}

func TestPendingInterruptsOrderPlanApprovalsByRequestedAt(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	_, laterIssue := createRunningProjectIssue(t, store, "Later plan approval", "", 0, nil)
	_, earlierIssue := createRunningProjectIssue(t, store, "Earlier plan approval", "", 0, nil)
	laterRequestedAt := time.Date(2026, 3, 18, 11, 5, 0, 0, time.UTC)
	earlierRequestedAt := time.Date(2026, 3, 18, 11, 0, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApproval(laterIssue.ID, "Later plan body.", laterRequestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval(later): %v", err)
	}
	if err := store.SetIssuePendingPlanApproval(earlierIssue.ID, "Earlier plan body.", earlierRequestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval(earlier): %v", err)
	}

	snapshot := orch.PendingInterrupts()
	if len(snapshot.Items) != 2 {
		t.Fatalf("expected two pending interrupts, got %#v", snapshot.Items)
	}
	if snapshot.Items[0].IssueID != earlierIssue.ID || snapshot.Items[1].IssueID != laterIssue.ID {
		t.Fatalf("expected plan approvals ordered by requested_at, got %#v", snapshot.Items)
	}
	if !snapshot.Items[0].RequestedAt.Before(snapshot.Items[1].RequestedAt) {
		t.Fatalf("expected earlier requested_at first, got %#v", snapshot.Items)
	}
}

func TestDispatchAllowsIssueAfterBlockerDeletion(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	runner := &countingPhaseRunner{
		store:    store,
		runCalls: make(chan string, 4),
	}
	orch.runner = runner

	project, issue := createRunningProjectIssue(t, store, "Blocked issue", "", 0, nil)
	blocker, err := store.CreateIssue(project.ID, "", "Deleted blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}
	if err := store.DeleteIssue(blocker.ID); err != nil {
		t.Fatalf("DeleteIssue blocker: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if got := waitForRunCall(t, runner.runCalls, time.Second); got != issue.Identifier {
		t.Fatalf("expected deleted blocker to unblock issue %s, got run for %s", issue.Identifier, got)
	}
}

func TestDispatchHydratesIssueRelationsForRunner(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	seen := make(chan kanban.Issue, 1)
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseImplementation: func(issue *kanban.Issue) (*agent.RunResult, error) {
				select {
				case seen <- *issue:
				default:
				}
				return &agent.RunResult{Success: true}, nil
			},
		},
	}

	project, issue := createRunningProjectIssue(t, store, "Tagged issue", "", 0, []string{"ops"})
	blocker, err := store.CreateIssue(project.ID, "", "Resolved blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, kanban.StateDone); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}
	if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState issue: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	select {
	case dispatched := <-seen:
		if len(dispatched.Labels) != 1 || dispatched.Labels[0] != "ops" {
			t.Fatalf("expected dispatched labels [ops], got %#v", dispatched.Labels)
		}
		if len(dispatched.BlockedBy) != 1 || dispatched.BlockedBy[0] != blocker.Identifier {
			t.Fatalf("expected dispatched blockers [%s], got %#v", blocker.Identifier, dispatched.BlockedBy)
		}
	case <-time.After(time.Second):
		t.Fatal("expected issue dispatch to reach runner")
	}
}

func TestPlanApprovalStopDoesNotAdvanceImplementationPhase(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseImplementation: func(issue *kanban.Issue) (*agent.RunResult, error) {
				return &agent.RunResult{
					Success:    false,
					StopReason: planApprovalStopReason,
					AppSession: &agentruntime.Session{ThreadID: "thread-plan", SessionID: "thread-plan-turn-1", TotalTokens: 7},
				}, nil
			},
		},
	}

	_, issue := createRunningProjectIssue(t, store, "Plan approval pause", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.State != kanban.StateInProgress || updated.WorkflowPhase != kanban.WorkflowPhaseImplementation {
		t.Fatalf("expected issue to remain in implementation, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	paused, pausedOK := orch.paused[issue.ID]
	_, retryOK := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !pausedOK {
		t.Fatal("expected issue to pause pending plan approval")
	}
	if retryOK {
		t.Fatal("did not expect automatic retry scheduling for pending plan approval")
	}
	if paused.Error != planApprovalStopReason || paused.Phase != string(kanban.WorkflowPhaseImplementation) || paused.Attempt != 1 {
		t.Fatalf("unexpected paused payload: %+v", paused)
	}

	events, err := store.ListIssueRuntimeEvents(issue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected runtime events for paused plan approval")
	}
	latest := events[len(events)-1]
	if latest.Kind != "retry_paused" || latest.Error != planApprovalStopReason || latest.TotalTokens != 7 {
		t.Fatalf("unexpected latest runtime event: %+v", latest)
	}
	if latest.Payload["thread_id"] != "thread-plan" || latest.Payload["session_id"] != "thread-plan-turn-1" {
		t.Fatalf("expected thread identifiers on runtime event payload, got %+v", latest.Payload)
	}
}

func TestFinishRunKeepsIssueRunningUntilPauseBookkeepingCompletes(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	runner := &countingPhaseRunner{
		store:    store,
		runCalls: make(chan string, 4),
	}
	orch.runner = runner

	_, issue := createRunningProjectIssue(t, store, "Finish run bookkeeping", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	releaseFinish := make(chan struct{})
	finishBlocked := make(chan struct{})
	releasedFinish := false
	defer func() {
		if !releasedFinish {
			close(releaseFinish)
		}
	}()
	var finishHookOnce sync.Once
	orch.testHooks.beforeFinishRunRelease = func(issueID string) {
		if issueID != issue.ID {
			return
		}
		finishHookOnce.Do(func() { close(finishBlocked) })
		<-releaseFinish
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := waitForRunCall(t, runner.runCalls, time.Second); got != issue.Identifier {
		t.Fatalf("expected first run for %s, got %s", issue.Identifier, got)
	}

	select {
	case <-finishBlocked:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for finish bookkeeping hook")
	}

	orch.mu.RLock()
	_, running := orch.running[issue.ID]
	orch.mu.RUnlock()
	if !running {
		t.Fatal("expected issue to remain marked running while finish bookkeeping is blocked")
	}

	for i := 0; i < 5; i++ {
		orch.reconcile(context.Background())
		if err := orch.dispatch(context.Background()); err != nil {
			t.Fatalf("dispatch during finish bookkeeping: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	orch.mu.RLock()
	_, running = orch.running[issue.ID]
	_, claimed := orch.claimed[issue.ID]
	orch.mu.RUnlock()
	if !running {
		t.Fatal("expected reconcile to keep the issue marked running while pause bookkeeping is blocked")
	}
	if !claimed {
		t.Fatal("expected reconcile to keep the issue claim while pause bookkeeping is blocked")
	}
	if calls := runner.runCount(issue.Identifier); calls != 1 {
		t.Fatalf("expected a single run while finish bookkeeping is blocked, got %d", calls)
	}

	close(releaseFinish)
	releasedFinish = true
	waitForNoRunning(t, orch, time.Second)
	for i := 0; i < 5; i++ {
		if err := orch.dispatch(context.Background()); err != nil {
			t.Fatalf("dispatch after finish bookkeeping: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls := runner.runCount(issue.Identifier); calls != 1 {
		t.Fatalf("expected pause state to survive finish bookkeeping release, got %d runs", calls)
	}
	orch.reconcile(context.Background())

	orch.mu.RLock()
	paused, pausedOK := orch.paused[issue.ID]
	orch.mu.RUnlock()
	if !pausedOK {
		t.Fatal("expected retry pause after finish bookkeeping completed")
	}
	if paused.Error != "no_state_transition" || paused.Attempt != 1 {
		t.Fatalf("unexpected paused payload after finish bookkeeping: %+v", paused)
	}
	events, err := store.ListIssueRuntimeEvents(issue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected runtime events after finish bookkeeping")
	}
	latest := events[len(events)-1]
	if latest.Kind != "retry_paused" || latest.Error != "no_state_transition" || latest.Attempt != 1 {
		t.Fatalf("unexpected latest runtime event after finish bookkeeping: %+v", latest)
	}
	if calls := runner.runCount(issue.Identifier); calls != 1 {
		t.Fatalf("expected finish bookkeeping regression test to keep run count at 1, got %d", calls)
	}
}

func TestFinishRunImmediatelyRearmsPendingRecurringIssue(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseImplementation: func(issue *kanban.Issue) (*agent.RunResult, error) {
				if err := store.MarkRecurringPendingRerun(issue.ID, true); err != nil {
					return nil, err
				}
				if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
					return nil, err
				}
				return &agent.RunResult{Success: true}, nil
			},
		},
	}

	issue, err := store.CreateIssueWithOptions("", "", "Recurring finish bookkeeping", "", 0, nil, kanban.IssueCreateOptions{
		IssueType: kanban.IssueTypeRecurring,
		Cron:      "*/20 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	releaseFinish := make(chan struct{})
	finishBlocked := make(chan struct{})
	releasedFinish := false
	defer func() {
		if !releasedFinish {
			close(releaseFinish)
		}
	}()
	var finishHookOnce sync.Once
	orch.testHooks.beforeFinishRunRelease = func(issueID string) {
		if issueID != issue.ID {
			return
		}
		finishHookOnce.Do(func() { close(finishBlocked) })
		<-releaseFinish
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	select {
	case <-finishBlocked:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recurring finish bookkeeping hook")
	}

	orch.mu.RLock()
	_, running := orch.running[issue.ID]
	orch.mu.RUnlock()
	if !running {
		t.Fatal("expected recurring issue to remain marked running while finish bookkeeping is blocked")
	}

	var updated *kanban.Issue
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		updated, err = store.GetIssue(issue.ID)
		if err == nil &&
			updated.State == kanban.StateReady &&
			updated.WorkflowPhase == kanban.WorkflowPhaseImplementation &&
			!updated.PendingRerun &&
			updated.LastEnqueuedAt != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if updated == nil || err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.State != kanban.StateReady ||
		updated.WorkflowPhase != kanban.WorkflowPhaseImplementation ||
		updated.PendingRerun ||
		updated.LastEnqueuedAt == nil {
		t.Fatalf("expected recurring issue to be rearmed before running state is released, got %+v", updated)
	}

	events, err := store.ListRuntimeEvents(0, 20)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Identifier == issue.Identifier && event.Kind == "recurring_pending_rerun_enqueued" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected immediate recurring rerun enqueue event, got %#v", events)
	}

	close(releaseFinish)
	releasedFinish = true
	waitForNoRunning(t, orch, time.Second)
}

func TestImplementationSuccessCanSkipReviewAndQueueDonePhase(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseImplementation: func(issue *kanban.Issue) (*agent.RunResult, error) {
				if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
					return nil, err
				}
				return &agent.RunResult{Success: true}, nil
			},
		},
	}

	_, issue := createRunningProjectIssue(t, store, "Skip review", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated := waitForIssueStateAndPhase(t, store, issue.ID, kanban.StateDone, kanban.WorkflowPhaseDone, time.Second)
	if updated.State != kanban.StateDone || updated.WorkflowPhase != kanban.WorkflowPhaseDone {
		t.Fatalf("expected done/done, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	retry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if retry.Phase != string(kanban.WorkflowPhaseDone) {
		t.Fatalf("expected done retry, got %+v", retry)
	}
}

func TestReviewFailureMovesIssueBackToImplementation(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseReview: func(issue *kanban.Issue) (*agent.RunResult, error) {
				return nil, fmt.Errorf("review failed")
			},
		},
	}

	_, issue := createRunningProjectIssue(t, store, "Review failure", "", 0, nil)
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateInReview, kanban.WorkflowPhaseReview); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated := waitForIssueStateAndPhase(t, store, issue.ID, kanban.StateInProgress, kanban.WorkflowPhaseImplementation, time.Second)
	if updated.State != kanban.StateInProgress || updated.WorkflowPhase != kanban.WorkflowPhaseImplementation {
		t.Fatalf("expected in_progress/implementation, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	retry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if retry.Phase != string(kanban.WorkflowPhaseImplementation) || retry.DelayType != "failure" {
		t.Fatalf("expected implementation failure retry, got %+v", retry)
	}
}

func TestReviewSuccessTransitionsToDonePhase(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{store: store}

	_, issue := createRunningProjectIssue(t, store, "Review success", "", 0, nil)
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateInReview, kanban.WorkflowPhaseReview); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated := waitForIssueStateAndPhase(t, store, issue.ID, kanban.StateDone, kanban.WorkflowPhaseDone, time.Second)
	if updated.State != kanban.StateDone || updated.WorkflowPhase != kanban.WorkflowPhaseDone {
		t.Fatalf("expected done/done, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	retry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if retry.Phase != string(kanban.WorkflowPhaseDone) {
		t.Fatalf("expected done retry, got %+v", retry)
	}
}

func TestDoneFailureRetriesInDonePhase(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseDone: func(issue *kanban.Issue) (*agent.RunResult, error) {
				return nil, fmt.Errorf("finalization failed")
			},
		},
	}

	_, issue := createRunningProjectIssue(t, store, "Done failure", "", 0, nil)
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateDone, kanban.WorkflowPhaseDone); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated := waitForIssueStateAndPhase(t, store, issue.ID, kanban.StateDone, kanban.WorkflowPhaseDone, time.Second)
	if updated.State != kanban.StateDone || updated.WorkflowPhase != kanban.WorkflowPhaseDone {
		t.Fatalf("expected done/done, got %s/%s", updated.State, updated.WorkflowPhase)
	}

	orch.mu.RLock()
	retry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if retry.Phase != string(kanban.WorkflowPhaseDone) || retry.DelayType != "failure" {
		t.Fatalf("expected done failure retry, got %+v", retry)
	}
}

func TestInReviewIssuesAreNotDispatchedWhenReviewPhaseDisabled(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	runner := newControlledRunner(store)
	orch.runner = runner

	issue, _ := store.CreateIssue("", "", "Review disabled", "", 0, nil)
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateInReview, kanban.WorkflowPhaseReview); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	orch.mu.RLock()
	_, running := orch.running[issue.ID]
	_, retrying := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if running || retrying {
		t.Fatalf("expected in_review issue to stay idle when review is disabled, running=%v retrying=%v", running, retrying)
	}
	if len(runner.snapshotEvents()) != 0 {
		t.Fatal("expected no run start when review is disabled")
	}
}

func TestReconcileDoesNotKillImplementationRunThatMovedIssueToDone(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	runner := newTerminalTransitionRunner(store)
	orch.runner = runner

	_, issue := createRunningProjectIssue(t, store, "Skip review race", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.waitForMovedToDone(t, time.Second)

	orch.reconcile(context.Background())

	orch.mu.RLock()
	_, stillRunning := orch.running[issue.ID]
	orch.mu.RUnlock()
	if !stillRunning {
		t.Fatal("expected run to remain active after reconcile")
	}

	runner.complete()
	waitForNoRunning(t, orch, time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != kanban.StateDone || updated.WorkflowPhase != kanban.WorkflowPhaseDone {
		t.Fatalf("expected done/done after implementation completion, got %s/%s", updated.State, updated.WorkflowPhase)
	}
}

func TestDoneSuccessMarksIssueCompleteAndAllowsCleanup(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)
	orch.runner = &phaseScriptRunner{store: store}

	_, issue := createRunningProjectIssue(t, store, "Done success", "", 0, nil)
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateDone, kanban.WorkflowPhaseDone); err != nil {
		t.Fatal(err)
	}
	wsPath := filepath.Join(workspaceRoot, issue.Identifier)
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(issue.ID, wsPath); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WorkflowPhase != kanban.WorkflowPhaseComplete {
		t.Fatalf("expected complete phase, got %s", updated.WorkflowPhase)
	}
	waitForWorkspaceRemoval(t, store, issue.ID, time.Second)
}

func TestDoneSuccessPublishesPreviewCommentWhenVideoExists(t *testing.T) {
	{
		orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
		enablePhaseWorkflow(t, manager, workspaceRoot)

		previewProvider := &previewCommentProvider{
			store:         store,
			createStarted: make(chan struct{}, 1),
			createRelease: make(chan struct{}),
		}
		orch.service.RegisterProvider(previewProvider)

		project, err := store.CreateProjectWithProvider("Preview Project", "", "", "", "stub", "", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
			t.Fatalf("UpdateProjectState: %v", err)
		}
		issue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
			ProjectID:        project.ID,
			ProviderKind:     "stub",
			ProviderIssueRef: "stub-1",
			Identifier:       "STUB-1",
			Title:            "Done success",
			State:            kanban.StateDone,
			WorkflowPhase:    kanban.WorkflowPhaseDone,
			ProviderShadow:   true,
		})
		if err != nil {
			t.Fatalf("UpsertProviderIssue: %v", err)
		}
		previewProvider.issue = *issue

		wsPath := filepath.Join(workspaceRoot, issue.Identifier)
		previewDir := filepath.Join(wsPath, ".maestro", "review-preview")
		if err := os.MkdirAll(previewDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(previewDir, "walkthrough.webm"), []byte("video-bytes"), 0o644); err != nil {
			t.Fatalf("WriteFile preview: %v", err)
		}
		if _, err := store.CreateWorkspace(issue.ID, wsPath); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		orch.runner = &phaseScriptRunner{
			store: store,
			handlers: map[kanban.WorkflowPhase]phaseRunHandler{
				kanban.WorkflowPhaseDone: func(issue *kanban.Issue) (*agent.RunResult, error) {
					return &agent.RunResult{
						Success: true,
						AppSession: &agentruntime.Session{
							LastMessage: "Preview generated and validation passed.",
						},
					}, nil
				},
			},
		}

		if err := orch.dispatch(context.Background()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-previewProvider.createStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for preview publication to start")
		}

		waitForNoRunning(t, orch, time.Second)
		waitForWorkspaceRemoval(t, store, issue.ID, time.Second)
		close(previewProvider.createRelease)
		waitForCondition(t, 5*time.Second, func() bool {
			comments, err := store.ListIssueComments(issue.ID)
			return err == nil && len(comments) > 0
		})

		comments, err := store.ListIssueComments(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueComments: %v", err)
		}
		var previewComment *kanban.IssueComment
		for i := range comments {
			if strings.Contains(comments[i].Body, "Automated reviewer preview from the done pass.") {
				previewComment = &comments[i]
				break
			}
		}
		if previewComment == nil {
			t.Fatalf("expected preview comment to be stored, got %d comments", len(comments))
		}
		if !strings.Contains(previewComment.Body, "Preview generated and validation passed.") {
			t.Fatalf("expected final message in comment body, got %q", previewComment.Body)
		}
		if len(previewComment.Attachments) != 1 {
			t.Fatalf("expected one preview attachment, got %d", len(previewComment.Attachments))
		}
		if previewComment.Attachments[0].Filename != "walkthrough.webm" {
			t.Fatalf("expected preview filename walkthrough.webm, got %q", previewComment.Attachments[0].Filename)
		}
		if previewComment.Attachments[0].ContentType != "video/webm" {
			t.Fatalf("expected preview attachment content type video/webm, got %q", previewComment.Attachments[0].ContentType)
		}
		attachment, path, err := store.GetIssueCommentAttachmentContent(issue.ID, previewComment.ID, previewComment.Attachments[0].ID)
		if err != nil {
			t.Fatalf("GetIssueCommentAttachmentContent: %v", err)
		}
		if attachment.ContentType != "video/webm" {
			t.Fatalf("expected stored attachment content type video/webm, got %q", attachment.ContentType)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile attachment: %v", err)
		}
		if string(data) != "video-bytes" {
			t.Fatalf("expected preview attachment body video-bytes, got %q", string(data))
		}
		return
	}

	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)

	type graphqlRequest struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}

	var (
		requests   []graphqlRequest
		requestsMu sync.Mutex
	)
	var uploadedBody string
	var uploadedContentType string
	var server *inprocessserver.Server
	server, err := inprocessserver.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload":
			uploadedContentType = r.Header.Get("Content-Type")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read upload body: %v", err)
			}
			uploadedBody = string(data)
			w.WriteHeader(http.StatusOK)
		default:
			var body graphqlRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			requestsMu.Lock()
			requests = append(requests, body)
			requestsMu.Unlock()
			switch {
			case strings.Contains(body.Query, "query MaestroLinearIssue"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"issues": map[string]interface{}{
							"nodes": []map[string]interface{}{
								{
									"id":         "stub-1",
									"identifier": "STUB-1",
									"title":      "Done success",
									"state":      map[string]interface{}{"name": "done"},
									"labels":     map[string]interface{}{"nodes": []interface{}{}},
									"inverseRelations": map[string]interface{}{
										"nodes": []interface{}{},
									},
								},
							},
						},
					},
				})
			case strings.Contains(body.Query, "fileUpload"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"fileUpload": map[string]interface{}{
							"success": true,
							"uploadFile": map[string]interface{}{
								"uploadUrl": server.URL + "/upload",
								"assetUrl":  "https://stub.example/assets/walkthrough.webm",
							},
						},
					},
				})
			case strings.Contains(body.Query, "commentCreate"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"commentCreate": map[string]interface{}{
							"success": true,
						},
					},
				})
			default:
				t.Fatalf("unexpected graphql query: %s", body.Query)
			}
		}
	}))
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
	defer server.Close()
	t.Setenv("STUB_PROVIDER_API_KEY", "test-token")

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug", "endpoint": server.URL},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState: %v", err)
	}
	issue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProjectID:        project.ID,
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Identifier:       "STUB-1",
		Title:            "Done success",
		State:            kanban.StateDone,
		WorkflowPhase:    kanban.WorkflowPhaseDone,
		ProviderShadow:   true,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateDone, kanban.WorkflowPhaseDone); err != nil {
		t.Fatalf("UpdateIssueStateAndPhase: %v", err)
	}

	wsPath := filepath.Join(workspaceRoot, issue.Identifier)
	previewDir := filepath.Join(wsPath, ".maestro", "review-preview")
	if err := os.MkdirAll(previewDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	previewPath := filepath.Join(previewDir, "walkthrough.webm")
	if err := os.WriteFile(previewPath, []byte("video-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile preview: %v", err)
	}
	if _, err := store.CreateWorkspace(issue.ID, wsPath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseDone: func(issue *kanban.Issue) (*agent.RunResult, error) {
				return &agent.RunResult{
					Success: true,
					AppSession: &agentruntime.Session{
						LastMessage: "Preview generated and validation passed.",
					},
				}, nil
			},
		},
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	var commentRequest *graphqlRequest
	for time.Now().Before(deadline) {
		requestsMu.Lock()
		for i := range requests {
			if strings.Contains(requests[i].Query, "commentCreate") {
				commentRequest = &requests[i]
				break
			}
		}
		requestsMu.Unlock()
		if commentRequest != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if commentRequest == nil {
		requestsMu.Lock()
		queries := make([]string, 0, len(requests))
		for _, req := range requests {
			queries = append(queries, req.Query)
		}
		requestsMu.Unlock()
		t.Fatalf("expected preview commentCreate request, got %d requests: %v", len(queries), queries)
	}

	waitForNoRunning(t, orch, time.Second)
	if uploadedContentType != "video/webm" {
		t.Fatalf("expected uploaded content type video/webm, got %q", uploadedContentType)
	}
	if uploadedBody != "video-bytes" {
		t.Fatalf("expected uploaded video bytes, got %q", uploadedBody)
	}
	body, _ := commentRequest.Variables["body"].(string)
	if !strings.Contains(body, "Automated reviewer preview from the done pass.") {
		t.Fatalf("expected preview intro in comment body, got %q", body)
	}
	if !strings.Contains(body, "Preview generated and validation passed.") {
		t.Fatalf("expected final message in comment body, got %q", body)
	}
	if !strings.Contains(body, "walkthrough.webm") {
		t.Fatalf("expected preview filename in comment body, got %q", body)
	}
	if !strings.Contains(body, "https://stub.example/assets/walkthrough.webm") {
		t.Fatalf("expected uploaded asset link in comment body, got %q", body)
	}
}

func TestLiveSessionsTracksOnlyActiveRuns(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, release := fakeappserver.CommandString(t, func() fakeappserver.Scenario {
		scenario := fakeappserver.Scenario{
			Steps: []fakeappserver.Step{
				{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
				{Match: fakeappserver.Match{Method: "initialized"}},
				{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-live"}}}}}},
				{
					Match:          fakeappserver.Match{Method: "turn/start"},
					Emit:           []fakeappserver.Output{{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-live"}}}}},
					WaitForRelease: "complete",
					EmitAfterRelease: []fakeappserver.Output{{
						JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-live", "turnId": "turn-live"}},
					}},
					ExitCode: fakeappserver.Int(0),
				},
			},
		}
		return scenario
	}())
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 3000, 0)

	_, issue := createRunningProjectIssue(t, store, "Live Session", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}

	session := waitForLiveSession(t, orch, issue.Identifier, 2*time.Second)
	if session.SessionID != "thread-live-turn-live" || session.TurnsStarted != 1 || session.IssueID != issue.ID || session.IssueIdentifier != issue.Identifier {
		t.Fatalf("unexpected live session: %+v", session)
	}
	release("complete")
	waitForNoRunning(t, orch, 3*time.Second)
	sessions := orch.LiveSessions()["sessions"].(map[string]interface{})
	if len(sessions) != 0 {
		t.Fatalf("expected no live sessions after run completion, got %#v", sessions)
	}
}

func TestPendingInterruptRunPersistsLatestExecutionSessionSnapshot(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-approval"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-approval"}}}},
				{JSON: map[string]interface{}{"id": 99, "method": "item/commandExecution/requestApproval", "params": map[string]interface{}{"command": "gh pr view"}}},
			}},
			{Match: fakeappserver.Match{ID: fakeappserver.Int(99)}, Emit: []fakeappserver.Output{{
				JSON: map[string]interface{}{
					"method": "turn/completed",
					"params": map[string]interface{}{"threadId": "thread-approval", "turnId": "turn-approval"},
				},
			}}, ExitCode: fakeappserver.Int(0)},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "on_request", 3000, 3000)

	_, issue := createRunningProjectIssue(t, store, "Approval snapshot", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	pending := waitForPendingInterruptCount(t, orch, 1, 3*time.Second)
	current := firstPendingInterrupt(pending)
	if current == nil || current.IssueID != issue.ID {
		t.Fatalf("expected current pending interrupt for %s, got %+v", issue.ID, pending)
	}
	snapshot := waitForRunStartedExecutionSnapshot(t, store, issue.ID, 3*time.Second)

	if snapshot.RunKind != "run_started" || snapshot.Error != "" || snapshot.Attempt != 0 {
		t.Fatalf("unexpected execution snapshot: %+v", snapshot)
	}
	if snapshot.AppSession.SessionID != "thread-approval-turn-approval" {
		t.Fatalf("unexpected persisted session id: %+v", snapshot.AppSession)
	}
	if len(snapshot.AppSession.History) != 0 {
		t.Fatalf("expected persisted session summary without history, got %+v", snapshot.AppSession)
	}
	sessions := orch.LiveSessions()["sessions"].(map[string]interface{})
	if len(sessions) != 1 {
		t.Fatalf("expected live session while interrupt is pending, got %#v", sessions)
	}

	if err := orch.RespondToInterrupt(context.Background(), current.ID, agentruntime.PendingInteractionResponse{
		Decision: "acceptForSession",
	}); err != nil {
		t.Fatalf("respond to pending interrupt: %v", err)
	}

	waitForPendingInterruptCount(t, orch, 0, 3*time.Second)
	waitForNoRunning(t, orch, 3*time.Second)

	sessions = orch.LiveSessions()["sessions"].(map[string]interface{})
	if len(sessions) != 0 {
		t.Fatalf("expected no live sessions after pending interrupt resolved, got %#v", sessions)
	}
}

func TestGracefulShutdownMarksActiveAppServerRunResumeEligible(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-graceful"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-graceful"}}}}}, WaitForRelease: "never"},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 6000, 0)

	_, issue := createRunningProjectIssue(t, store, "Graceful shutdown", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForLiveSession(t, orch, issue.Identifier, 2*time.Second)

	orch.stopAllRunsGracefully()
	waitForNoRunning(t, orch, 3*time.Second)

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.RunKind != "run_started" || snapshot.StopReason != gracefulShutdownStopReason || !snapshot.ResumeEligible {
		t.Fatalf("expected graceful shutdown resume marker, got %+v", snapshot)
	}
	if snapshot.AppSession.ThreadID != "thread-graceful" {
		t.Fatalf("expected persisted thread id for resume, got %+v", snapshot.AppSession)
	}
}

func TestRunWaitsForActiveRunsDuringShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := kanban.NewStore(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	orch := NewSharedWithExtensions(store, nil, "", "")
	issue, err := store.CreateIssue("", "", "Shutdown wait", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	runner := &blockingRunner{
		started:      make(chan struct{}),
		ctxCancelled: make(chan struct{}),
		release:      make(chan struct{}),
	}
	workflow := &config.Workflow{Config: config.DefaultConfig()}
	workflow.Config.Phases.Review.Enabled = false
	workflow.Config.Phases.Review.Prompt = ""
	workflow.Config.Phases.Done.Enabled = false
	workflow.Config.Phases.Done.Prompt = ""
	workflow.Config.Agent.Mode = config.AgentModeAppServer

	ctx, cancel := context.WithCancel(context.Background())
	orch.startRun(ctx, workflow, runner, issue, 0)
	<-runner.started

	done := make(chan error, 1)
	go func() {
		done <- orch.Run(ctx)
	}()

	cancel()
	<-runner.ctxCancelled

	select {
	case err := <-done:
		t.Fatalf("Run returned before active run finished: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(runner.release)

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected Run to return after active run finished")
	}
}

func TestSharedOrchestratorInitializesRetiredAppServerTracking(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := kanban.NewStore(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	orch := NewSharedWithExtensions(store, nil, "", "")
	orch.markAppServerRetired("issue-1")
	if !orch.appServerRetired("issue-1") {
		t.Fatal("expected shared orchestrator to track retired app-server issues")
	}
	orch.unmarkAppServerRetired("issue-1")
	if orch.appServerRetired("issue-1") {
		t.Fatal("expected shared orchestrator to clear retired app-server issues")
	}
}

func TestOrphanedGracefulAppServerRunSchedulesImmediateResumeRetry(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	writeAppServerWorkflow(t, manager, workspaceRoot, "cat", "never", 3000, 0)

	issue, err := store.CreateIssue("", "", "Graceful orphan", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:        issue.ID,
		Identifier:     issue.Identifier,
		Phase:          "implementation",
		Attempt:        2,
		RunKind:        "run_started",
		ResumeEligible: true,
		StopReason:     gracefulShutdownStopReason,
		UpdatedAt:      now,
		AppSession:     agentruntime.Session{IssueID: issue.ID, IssueIdentifier: issue.Identifier, SessionID: "thread-graceful-turn-stale", ThreadID: "thread-graceful"},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	orch.reconcile(context.Background())

	orch.mu.RLock()
	retry, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected orphaned graceful run retry to be scheduled")
	}
	if retry.ResumeThreadID != "thread-graceful" {
		t.Fatalf("expected resume thread id, got %+v", retry)
	}
	if retry.DueAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("expected immediate recovery retry, got due_at=%v", retry.DueAt)
	}

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.RunKind != "run_interrupted" || snapshot.ResumeEligible || snapshot.StopReason != "run_interrupted" {
		t.Fatalf("expected interrupted snapshot with cleared resume marker, got %+v", snapshot)
	}
}

func TestOrphanedAppServerRunWithoutGracefulMarkerOpportunisticallyResumes(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	writeAppServerWorkflow(t, manager, workspaceRoot, "cat", "never", 3000, 0)

	issue, err := store.CreateIssue("", "", "Opportunistic orphan", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: agentruntime.Session{IssueID: issue.ID, IssueIdentifier: issue.Identifier, ThreadID: "thread-opportunistic"},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    1,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	orch.reconcile(context.Background())

	orch.mu.RLock()
	retry, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected opportunistic resume retry to be scheduled")
	}
	if retry.ResumeThreadID != "thread-opportunistic" {
		t.Fatalf("expected opportunistic resume hint, got %+v", retry)
	}
	if retry.DueAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("expected immediate opportunistic retry, got due_at=%v", retry.DueAt)
	}
}

func TestOrphanedAppServerRunWithoutThreadIDKeepsFreshStartBackoff(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	writeAppServerWorkflow(t, manager, workspaceRoot, "cat", "never", 3000, 0)

	issue, err := store.CreateIssue("", "", "No thread orphan", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_started",
		UpdatedAt:  now,
		AppSession: agentruntime.Session{IssueID: issue.ID, IssueIdentifier: issue.Identifier, SessionID: "thread-missing-turn-missing"},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	orch.reconcile(context.Background())

	orch.mu.RLock()
	retry, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected orphaned run retry to be scheduled")
	}
	if retry.ResumeThreadID != "" {
		t.Fatalf("expected no resume thread id, got %+v", retry)
	}
	if retry.DueAt.Before(time.Now().UTC().Add(9 * time.Second)) {
		t.Fatalf("expected backoff retry scheduling, got due_at=%v", retry.DueAt)
	}
}

func TestProcessRetriesResumesOrphanedAppServerRunAndFallsBackToFreshStart(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/resume"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "error": map[string]interface{}{"code": -32000, "message": "resume unavailable"}}}}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-fresh"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 4, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-fresh"}}}},
				{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-fresh", "turn": map[string]interface{}{"id": "turn-fresh"}}}},
			}, ExitCode: fakeappserver.Int(0)},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 3000, 0)

	_, issue := createRunningProjectIssue(t, store, "Resume fallback", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	orch.mu.Lock()
	orch.claimed[issue.ID] = struct{}{}
	orch.retries[issue.ID] = retryEntry{
		Attempt:        2,
		Phase:          string(kanban.WorkflowPhaseImplementation),
		DueAt:          time.Now().UTC(),
		DelayType:      "failure",
		Error:          "run_interrupted",
		ResumeThreadID: "thread-stale",
	}
	orch.mu.Unlock()

	orch.processRetries(context.Background())
	waitForNoRunning(t, orch, 3*time.Second)

	snapshot := waitForExecutionSnapshot(t, store, issue.ID, 3*time.Second)
	if snapshot.AppSession.ThreadID != "thread-fresh" {
		t.Fatalf("expected fallback to start a fresh thread, got %+v", snapshot)
	}
	orch.mu.RLock()
	if retry := orch.retries[issue.ID]; retry.ResumeThreadID != "" {
		orch.mu.RUnlock()
		t.Fatalf("expected resume hint to be cleared after fallback run, got %+v", retry)
	}
	orch.mu.RUnlock()
}

func TestProcessRetriesFallsBackToFreshStartWhenResumedThreadDisappearsBeforeTurnStart(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/resume"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-resumed"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 3, "error": map[string]interface{}{"code": -32600, "message": "thread not found: thread-resumed"}}}}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 4, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-fresh"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 5, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-fresh"}}}},
				{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-fresh", "turn": map[string]interface{}{"id": "turn-fresh"}}}},
			}, ExitCode: fakeappserver.Int(0)},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 3000, 0)

	_, issue := createRunningProjectIssue(t, store, "Turn-start fallback", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	orch.mu.Lock()
	orch.claimed[issue.ID] = struct{}{}
	orch.retries[issue.ID] = retryEntry{
		Attempt:        2,
		Phase:          string(kanban.WorkflowPhaseImplementation),
		DueAt:          time.Now().UTC(),
		DelayType:      "failure",
		Error:          "run_interrupted",
		ResumeThreadID: "thread-stale",
	}
	orch.mu.Unlock()

	orch.processRetries(context.Background())
	waitForNoRunning(t, orch, 3*time.Second)

	snapshot := waitForExecutionSnapshot(t, store, issue.ID, 3*time.Second)
	if snapshot.AppSession.ThreadID != "thread-fresh" {
		t.Fatalf("expected turn/start fallback to persist fresh thread, got %+v", snapshot)
	}
	orch.mu.RLock()
	if retry := orch.retries[issue.ID]; retry.ResumeThreadID != "" {
		orch.mu.RUnlock()
		t.Fatalf("expected resume hint to be cleared after fresh-thread recovery, got %+v", retry)
	}
	orch.mu.RUnlock()
}

func TestStalledRunPersistsLatestExecutionSessionSnapshot(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-stall"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-stall"}}}}}, WaitForRelease: "never"},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 6000, 2500)

	_, issue := createRunningProjectIssue(t, store, "Stall snapshot", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := waitForExecutionSnapshot(t, store, issue.ID, 5*time.Second)
	waitForNoRunning(t, orch, 5*time.Second)

	if snapshot.Error != "stall_timeout" || snapshot.RunKind != "run_unsuccessful" || snapshot.Attempt != 0 {
		t.Fatalf("unexpected stall snapshot: %+v", snapshot)
	}
	if snapshot.AppSession.SessionID != "thread-stall-turn-stall" {
		t.Fatalf("unexpected persisted stall session: %+v", snapshot.AppSession)
	}
}

func TestTurnInputRequiredPausesAutomaticRetries(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-input"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-input"}}}},
				{JSON: map[string]interface{}{
					"id":     4,
					"method": "item/tool/requestUserInput",
					"params": map[string]interface{}{
						"questions": []map[string]interface{}{{
							"id": "input-choice",
							"options": []map[string]interface{}{
								{"label": "Use default"},
								{"label": "Skip"},
							},
						}},
					},
				}},
			}, WaitForRelease: "never"},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 3000, 3000)

	_, issue := createRunningProjectIssue(t, store, "Input required", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, 3*time.Second)

	orch.mu.RLock()
	paused, pausedOK := orch.paused[issue.ID]
	_, retryScheduled := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !pausedOK {
		t.Fatal("expected turn_input_required to pause retries")
	}
	if retryScheduled {
		t.Fatal("expected turn_input_required not to schedule retry")
	}
	if paused.Error != "turn_input_required" {
		t.Fatalf("unexpected paused payload: %+v", paused)
	}

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.RunKind != "retry_paused" || snapshot.Error != "turn_input_required" {
		t.Fatalf("unexpected execution snapshot: %+v", snapshot)
	}
}

func TestClaudeIssueImagesPauseUnsupportedRuntimeCapabilityWithoutRetry(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestrator(t, "cat")

	workflowData, err := os.ReadFile(manager.Path())
	if err != nil {
		t.Fatalf("ReadFile workflow: %v", err)
	}
	updated := strings.Replace(string(workflowData), "default: codex-stdio", "default: claude", 1)
	if updated == string(workflowData) {
		t.Fatal("expected workflow to contain the codex-stdio default")
	}
	if err := os.WriteFile(manager.Path(), []byte(updated), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatalf("Refresh workflow: %v", err)
	}

	_, issue := createRunningProjectIssue(t, store, "Unsupported Claude image capability", "", 0, []string{"claude"})
	if _, err := store.CreateIssueAsset(issue.ID, "prompt.png", bytes.NewReader(samplePNGBytesForTest())); err != nil {
		t.Fatalf("CreateIssueAsset: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForIssuePauseReason(t, store, issue.ID, `unsupported_runtime_capability: issue `+issue.Identifier+` has image attachments, but runtime "claude" does not support local_image input; remove the issue image or switch to a runtime with local_image support`, 6*time.Second)
	waitForNoRunning(t, orch, 6*time.Second)

	orch.mu.RLock()
	paused, pausedOK := orch.paused[issue.ID]
	_, retryScheduled := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !pausedOK {
		t.Fatal("expected unsupported runtime capability to pause retries")
	}
	if retryScheduled {
		t.Fatal("expected unsupported runtime capability to avoid scheduling retries")
	}
	if !strings.Contains(paused.Error, "unsupported_runtime_capability") || !strings.Contains(paused.Error, "local_image") {
		t.Fatalf("unexpected paused payload: %+v", paused)
	}

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.RunKind != "retry_paused" {
		t.Fatalf("expected retry_paused snapshot, got %+v", snapshot)
	}
	if snapshot.RuntimeName != "claude" || snapshot.RuntimeProvider != "claude" || snapshot.RuntimeTransport != "stdio" {
		t.Fatalf("expected persisted Claude runtime metadata, got %+v", snapshot)
	}
	if !strings.Contains(snapshot.Error, "unsupported_runtime_capability") || !strings.Contains(snapshot.Error, issue.Identifier) {
		t.Fatalf("unexpected snapshot error: %+v", snapshot)
	}
	if snapshot.AppSession.Metadata["provider"] != "claude" || snapshot.AppSession.Metadata["transport"] != "stdio" {
		t.Fatalf("expected persisted session metadata to remain Claude-specific, got %+v", snapshot.AppSession.Metadata)
	}
}

func TestInteractiveAppServerInterruptQueueUsesFIFOAndPromotesNextRequest(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestratorWithConcurrency(t, "cat", 2)
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-interactive"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-interactive"}}}},
				{JSON: map[string]interface{}{
					"id":     99,
					"method": "item/commandExecution/requestApproval",
					"params": map[string]interface{}{
						"threadId": "thread-interactive",
						"turnId":   "turn-interactive",
						"itemId":   "approval-item",
						"command":  "git status",
					},
				}},
			}},
			{Match: fakeappserver.Match{ID: fakeappserver.Int(99)}, Emit: []fakeappserver.Output{{
				JSON: map[string]interface{}{
					"method": "turn/completed",
					"params": map[string]interface{}{"threadId": "thread-interactive", "turnId": "turn-interactive"},
				},
			}}, ExitCode: fakeappserver.Int(0)},
		},
	})
	writeAppServerWorkflowWithConcurrency(t, manager, workspaceRoot, command, "on-request", 3000, 3000, 2)

	_, first := createRunningProjectIssue(t, store, "First interactive issue", "", 0, nil)
	_ = store.UpdateIssueState(first.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := waitForPendingInterruptCount(t, orch, 1, 3*time.Second)
	current := firstPendingInterrupt(snapshot)
	if current == nil || current.IssueID != first.ID {
		t.Fatalf("expected FIFO current interrupt for first issue, got %+v", snapshot)
	}

	_, second := createRunningProjectIssue(t, store, "Second interactive issue", "", 0, nil)
	_ = store.UpdateIssueState(second.ID, kanban.StateReady)
	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot = waitForPendingInterruptCount(t, orch, 2, 3*time.Second)
	current = firstPendingInterrupt(snapshot)
	if current == nil || current.IssueID != first.ID {
		t.Fatalf("expected first issue to stay at the front of the queue, got %+v", snapshot)
	}

	if err := orch.RespondToInterrupt(context.Background(), current.ID, agentruntime.PendingInteractionResponse{
		Decision: "acceptForSession",
	}); err != nil {
		t.Fatalf("respond to first interrupt: %v", err)
	}

	snapshot = waitForPendingInterruptCount(t, orch, 1, 3*time.Second)
	current = firstPendingInterrupt(snapshot)
	if current == nil || current.IssueID != second.ID {
		t.Fatalf("expected second issue to be promoted, got %+v", snapshot)
	}
	if err := orch.RespondToInterrupt(context.Background(), current.ID, agentruntime.PendingInteractionResponse{
		Decision: "acceptForSession",
	}); err != nil {
		t.Fatalf("respond to second interrupt: %v", err)
	}

	waitForPendingInterruptCount(t, orch, 0, 3*time.Second)
	waitForNoRunning(t, orch, 3*time.Second)
}

func TestInteractiveAppServerRedactsSecretUserInputFromPersistedActivity(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-secret"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-secret"}}}},
				{JSON: map[string]interface{}{
					"id":     100,
					"method": "item/tool/requestUserInput",
					"params": map[string]interface{}{
						"threadId": "thread-secret",
						"turnId":   "turn-secret",
						"itemId":   "secret-item",
						"questions": []map[string]interface{}{{
							"id":       "token",
							"question": "API token",
							"isSecret": true,
						}},
					},
				}},
			}},
			{Match: fakeappserver.Match{ID: fakeappserver.Int(100)}, Emit: []fakeappserver.Output{{
				JSON: map[string]interface{}{
					"method": "turn/completed",
					"params": map[string]interface{}{"threadId": "thread-secret", "turnId": "turn-secret"},
				},
			}}, ExitCode: fakeappserver.Int(0)},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "on-request", 3000, 3000)

	_, issue := createRunningProjectIssue(t, store, "Secret input issue", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := waitForPendingInterruptCount(t, orch, 1, 3*time.Second)
	current := firstPendingInterrupt(snapshot)
	if current == nil {
		t.Fatal("expected pending interrupt")
	}
	secret := "super-secret-token"
	if err := orch.RespondToInterrupt(context.Background(), current.ID, agentruntime.PendingInteractionResponse{
		Answers: map[string][]string{
			"token": []string{secret},
		},
	}); err != nil {
		t.Fatalf("respond to secret interrupt: %v", err)
	}

	waitForPendingInterruptCount(t, orch, 0, 3*time.Second)
	waitForNoRunning(t, orch, 3*time.Second)

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	body := fmt.Sprintf("%#v", entries)
	if strings.Contains(body, secret) {
		t.Fatalf("expected secret input to be redacted from persisted activity, got %s", body)
	}
	if !strings.Contains(body, "[redacted]") {
		t.Fatalf("expected redacted marker in persisted activity, got %s", body)
	}
}

func TestCompletedRunPersistsLatestExecutionSessionSnapshot(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-complete"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-complete"}}}},
				{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-complete", "turnId": "turn-complete"}}},
			}, ExitCode: fakeappserver.Int(0)},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 3000, 3000)

	_, issue := createRunningProjectIssue(t, store, "Completion snapshot", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForNoRunning(t, orch, 3*time.Second)
	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}

	if snapshot.RunKind != "retry_paused" || snapshot.Error != "no_state_transition" {
		t.Fatalf("unexpected completion snapshot: %+v", snapshot)
	}
	if snapshot.AppSession.SessionID != "thread-complete-turn-complete" || snapshot.AppSession.TerminalReason != "turn.completed" {
		t.Fatalf("unexpected persisted completed session: %+v", snapshot.AppSession)
	}
}

func TestCleanupTerminalAppServerProcessKillsLiveSessionProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup test is Unix-specific")
	}

	orch, store, _, workspaceRoot := setupTestOrchestrator(t, "cat")
	issue, err := store.CreateIssue("", "", "Terminal cleanup kills live appserver", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	workspacePath := filepath.Join(workspaceRoot, issue.Identifier)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	childPIDPath := filepath.Join(workspaceRoot, issue.Identifier+"-live-child.pid")
	cmd, _ := startLingeringProcessGroup(t, childPIDPath)

	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      string(kanban.WorkflowPhaseComplete),
		RunKind:    "run_completed",
		UpdatedAt:  time.Now().UTC(),
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			ProcessID:       cmd.Process.Pid,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	orch.liveSessions[issue.ID] = &agentruntime.Session{
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		ProcessID:       cmd.Process.Pid,
	}

	cleanupCalled := 0
	orch.testHooks.cleanupLingeringAppServerProcess = func(pid int) error {
		cleanupCalled = pid
		return nil
	}

	t.Cleanup(func() {
		_ = codexruntime.CleanupLingeringProcess(cmd.Process.Pid)
		_ = cmd.Wait()
	})

	orch.cleanupTerminalAppServerProcess(issue)

	if cleanupCalled != cmd.Process.Pid {
		t.Fatalf("expected terminal cleanup to call lingering process cleanup for pid %d, got %d", cmd.Process.Pid, cleanupCalled)
	}
	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.AppSession.ProcessID != 0 {
		t.Fatalf("expected terminal cleanup to retire app-server pid, got %+v", snapshot.AppSession)
	}
}

func TestCleanupTerminalWorkspacesRetiresPersistedPIDWithoutKillingUnknownProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup test is Unix-specific")
	}

	orch, store, _, workspaceRoot := setupTestOrchestrator(t, "cat")
	issue, err := store.CreateIssue("", "", "Terminal cleanup retires persisted pid safely", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	workspacePath := filepath.Join(workspaceRoot, issue.Identifier)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	childPIDPath := filepath.Join(workspaceRoot, issue.Identifier+"-persisted-child.pid")
	cmd, childPID := startLingeringProcessGroup(t, childPIDPath)

	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:        issue.ID,
		Identifier:     issue.Identifier,
		Phase:          string(kanban.WorkflowPhaseComplete),
		RunKind:        "run_started",
		ResumeEligible: true,
		StopReason:     gracefulShutdownStopReason,
		UpdatedAt:      time.Now().UTC(),
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			ProcessID:       cmd.Process.Pid,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	t.Cleanup(func() {
		_ = codexruntime.CleanupLingeringProcess(cmd.Process.Pid)
		_ = cmd.Wait()
	})

	orch.cleanupTerminalWorkspaces(context.Background())

	if !testProcessGroupAlive(cmd.Process.Pid) || !testProcessAlive(childPID) {
		t.Fatalf("expected persisted-only cleanup to avoid signaling unknown processes: leader=%d child=%d", cmd.Process.Pid, childPID)
	}
	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.ResumeEligible || snapshot.AppSession.ProcessID != 0 {
		t.Fatalf("expected terminal cleanup to retire graceful-shutdown metadata, got %+v", snapshot)
	}
	if _, err := store.GetWorkspace(issue.ID); err == nil {
		t.Fatal("expected terminal workspace cleanup to delete the workspace")
	}
}

func TestCleanupTerminalAppServerProcessKeepsPidRetiredAcrossLaterPersistence(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	issue, err := store.CreateIssue("", "", "Terminal cleanup retires pid across late persistence", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:        issue.ID,
		Identifier:     issue.Identifier,
		Phase:          string(kanban.WorkflowPhaseComplete),
		RunKind:        "run_completed",
		ResumeEligible: true,
		UpdatedAt:      time.Now().UTC(),
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			ThreadID:        "thread-terminal",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	orch.cleanupTerminalAppServerProcess(issue)

	orch.persistExecutionSession(issue, kanban.WorkflowPhaseComplete, 1, "run_completed", "", false, "", &agentruntime.Session{
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		ThreadID:        "thread-terminal",
		ProcessID:       4242,
	})

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.AppSession.ProcessID != 0 {
		t.Fatalf("expected retired app-server pid to remain cleared, got %+v", snapshot.AppSession)
	}
}

func TestReconcileStopsCancelledRunsAndCleansWorkspace(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	runner := newControlledRunner(store)
	orch.runner = runner
	_, issue := createRunningProjectIssue(t, store, "Sleep", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.waitForStarts(t, 1, time.Second)
	if err := store.UpdateIssueState(issue.ID, kanban.StateCancelled); err != nil {
		t.Fatal(err)
	}

	orch.reconcile(context.Background())
	waitForNoRunning(t, orch, time.Second)
	reloaded, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != kanban.StateCancelled {
		t.Fatalf("expected issue to remain cancelled, got %s", reloaded.State)
	}
	waitForWorkspaceRemoval(t, store, issue.ID, time.Second)
}

func TestReconcileStopsCancelledRunsWithoutRetryWhenRunnerReturnsCancelledResult(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	runner := newCancelledResultRunner(store)
	orch.runner = runner
	_, issue := createRunningProjectIssue(t, store, "Sleep", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.waitForStarts(t, 1, time.Second)
	if err := store.UpdateIssueState(issue.ID, kanban.StateCancelled); err != nil {
		t.Fatal(err)
	}

	orch.reconcile(context.Background())
	waitForNoRunning(t, orch, time.Second)
	reloaded, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != kanban.StateCancelled {
		t.Fatalf("expected issue to remain cancelled, got %s", reloaded.State)
	}
	orch.mu.RLock()
	_, hasRetry := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if hasRetry {
		t.Fatal("expected cancelled issue not to schedule a retry")
	}
}

func TestCleanupTerminalWorkspacesOnStartup(t *testing.T) {
	orch, store, _, workspaceRoot := setupTestOrchestrator(t, "cat")
	_, issue := createRunningProjectIssue(t, store, "Done", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateDone)
	wsPath := filepath.Join(workspaceRoot, issue.Identifier)
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(issue.ID, wsPath); err != nil {
		t.Fatal(err)
	}

	orch.cleanupTerminalWorkspaces(context.Background())

	if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path removed, got %v", err)
	}
}

func TestDispatchBlockedByInvalidWorkflowReloadKeepsLastGood(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestrator(t, "cat")
	_, issue := createRunningProjectIssue(t, store, "Ready", "", 0, nil)
	_ = store.UpdateIssueState(issue.ID, kanban.StateReady)

	if err := os.WriteFile(manager.Path(), []byte("---\ntracker:\n  kind: stub\n---\nlegacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(); err != nil {
		t.Fatal(err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.LastError() == nil {
		t.Fatal("expected workflow reload error to be retained")
	}
	waitForNoRunning(t, orch, time.Second)
}

func TestStatusIncludesWorkflowAndRetryFields(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t, "cat")
	status := orch.Status()
	for _, key := range []string{"active_runs", "max_concurrent", "dispatch_mode", "started_at", "uptime_seconds", "poll_interval_ms", "retry_queue", "run_metrics"} {
		if _, ok := status[key]; !ok {
			t.Fatalf("Expected status to have key %s", key)
		}
	}
}

func TestStatusLiveSessionsUseIssueIdentifiers(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	_, issue := createRunningProjectIssue(t, store, "Tracked session", "", 0, nil)
	orch.mu.Lock()
	orch.running[issue.ID] = runningEntry{
		cancel:    func() {},
		issue:     *issue,
		attempt:   1,
		startedAt: time.Now().UTC(),
	}
	orch.liveSessions[issue.ID] = &agentruntime.Session{
		SessionID:       "thread-turn",
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
	}
	orch.mu.Unlock()

	status := orch.Status()
	live, ok := status["live_sessions"].(map[string]*agentruntime.Session)
	if !ok {
		t.Fatalf("unexpected live_sessions payload: %#v", status["live_sessions"])
	}
	session := live[issue.Identifier]
	if session == nil {
		t.Fatalf("expected live session keyed by identifier, got %#v", live)
	}
	if session.IssueID != issue.ID || session.IssueIdentifier != issue.Identifier {
		t.Fatalf("unexpected session metadata: %+v", session)
	}
}

func TestStatusIncludesRuntimeAndMaintenanceMetadata(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t, "cat")
	orch.runMaintenanceIfDue()

	status := orch.Status()
	for _, key := range []string{
		"heap_alloc_bytes",
		"heap_sys_bytes",
		"db_page_count",
		"db_page_size",
		"db_freelist_count",
		"last_maintenance_at",
		"last_checkpoint_at",
		"last_checkpoint_result",
	} {
		if _, ok := status[key]; !ok {
			t.Fatalf("expected status[%q], got %#v", key, status)
		}
	}
}

func TestMaintenanceProtectedIssueIDsIncludeRetryAndPausedIssues(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	runningIssue, err := store.CreateIssue("", "", "Running maintenance issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue running: %v", err)
	}
	retryIssue, err := store.CreateIssue("", "", "Retry maintenance issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue retry: %v", err)
	}
	pausedIssue, err := store.CreateIssue("", "", "Paused maintenance issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue paused: %v", err)
	}

	orch.mu.Lock()
	orch.running[runningIssue.ID] = runningEntry{issue: *runningIssue, cancel: func() {}}
	orch.retries[retryIssue.ID] = retryEntry{Attempt: 2, Phase: "implementation", DueAt: time.Now().UTC().Add(time.Minute)}
	orch.paused[pausedIssue.ID] = pausedEntry{Attempt: 3, Phase: "review", PausedAt: time.Now().UTC()}
	ids := orch.maintenanceProtectedIssueIDsLocked()
	orch.mu.Unlock()

	expected := []string{pausedIssue.ID, retryIssue.ID, runningIssue.ID}
	sort.Strings(expected)
	if got, want := strings.Join(ids, ","), strings.Join(expected, ","); got != want {
		t.Fatalf("maintenance protected issue IDs = %q, want %q", got, want)
	}
}

func TestSnapshotAndRetryNowExposeDashboardScenarioShape(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	runningIssue, err := store.CreateIssue("", "", "Running", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue running: %v", err)
	}
	if err := store.UpdateIssueState(runningIssue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState running: %v", err)
	}
	doneIssue, err := store.CreateIssue("", "", "Done", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	if err := store.UpdateIssueState(doneIssue.ID, kanban.StateDone); err != nil {
		t.Fatalf("UpdateIssueState done: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(doneIssue.ID, kanban.WorkflowPhaseDone); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase done: %v", err)
	}

	startedAt := time.Now().UTC().Add(-10 * time.Second)
	orch.mu.Lock()
	orch.running[runningIssue.ID] = runningEntry{
		cancel:    func() {},
		issue:     *runningIssue,
		attempt:   2,
		phase:     kanban.WorkflowPhaseImplementation,
		startedAt: startedAt,
	}
	orch.liveSessions[runningIssue.ID] = &agentruntime.Session{
		SessionID:       "thread-live-turn-live",
		ThreadID:        "thread-live",
		TurnID:          "turn-live",
		LastEvent:       "turn.started",
		LastMessage:     "Working",
		LastTimestamp:   startedAt.Add(8 * time.Second),
		InputTokens:     11,
		OutputTokens:    7,
		TotalTokens:     18,
		TurnsStarted:    2,
		TurnsCompleted:  1,
		IssueID:         runningIssue.ID,
		IssueIdentifier: runningIssue.Identifier,
	}
	orch.retries[doneIssue.ID] = retryEntry{
		Attempt:        3,
		Phase:          string(kanban.WorkflowPhaseDone),
		DueAt:          time.Now().UTC().Add(5 * time.Minute),
		Error:          "approval_required",
		DelayType:      "failure",
		ResumeThreadID: "thread-stale",
	}
	orch.mu.Unlock()

	snapshot := orch.Snapshot()
	if len(snapshot.Running) != 1 || len(snapshot.Retrying) != 1 {
		t.Fatalf("unexpected snapshot shape: %+v", snapshot)
	}
	if snapshot.Running[0].Identifier != runningIssue.Identifier || snapshot.Running[0].Tokens.TotalTokens != 18 {
		t.Fatalf("unexpected running payload: %+v", snapshot.Running[0])
	}
	if snapshot.Retrying[0].Identifier != doneIssue.Identifier || snapshot.Retrying[0].DelayType != "failure" {
		t.Fatalf("unexpected retry payload: %+v", snapshot.Retrying[0])
	}

	live := orch.LiveSessions()["sessions"].(map[string]interface{})
	session, ok := agentruntime.SessionFromAny(live[runningIssue.Identifier])
	if !ok {
		t.Fatalf("expected live session for %s, got %#v", runningIssue.Identifier, live)
	}
	if session.SessionID != "thread-live-turn-live" || session.IssueIdentifier != runningIssue.Identifier {
		t.Fatalf("unexpected live session payload: %+v", session)
	}

	result := orch.RetryIssueNow(context.Background(), doneIssue.Identifier)
	if result["status"] != "queued_now" {
		t.Fatalf("unexpected retry-now result: %#v", result)
	}

	updated := orch.Snapshot()
	if updated.Retrying[0].DueInMs > 1000 {
		t.Fatalf("expected retry to be due immediately, got %+v", updated.Retrying[0])
	}
	orch.mu.RLock()
	if retry := orch.retries[doneIssue.ID]; retry.ResumeThreadID != "" {
		orch.mu.RUnlock()
		t.Fatalf("expected manual retry to clear resume hint, got %+v", retry)
	}
	orch.mu.RUnlock()

	events, err := store.ListRuntimeEvents(0, 10)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(events) == 0 || events[0].Kind != "manual_retry_requested" {
		t.Fatalf("expected manual retry event, got %#v", events)
	}
}

func TestRetryNowAndRefreshHandleAdditionalControlPaths(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	if result := orch.RetryIssueNow(context.Background(), "ISS-404"); result["status"] != "not_found" {
		t.Fatalf("expected not_found retry result, got %#v", result)
	}

	readyIssue, err := store.CreateIssue("", "", "Ready", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue ready: %v", err)
	}
	if err := store.UpdateIssueState(readyIssue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState ready: %v", err)
	}
	if result := orch.RetryIssueNow(context.Background(), readyIssue.Identifier); result["status"] != "refresh_requested" {
		t.Fatalf("expected refresh_requested for ready issue, got %#v", result)
	}

	doneIssue, err := store.CreateIssue("", "", "Done", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	if err := store.UpdateIssueState(doneIssue.ID, kanban.StateDone); err != nil {
		t.Fatalf("UpdateIssueState done: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(doneIssue.ID, kanban.WorkflowPhaseDone); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase done: %v", err)
	}
	if result := orch.RetryIssueNow(context.Background(), doneIssue.Identifier); result["status"] != "queued_now" {
		t.Fatalf("expected queued_now for done issue, got %#v", result)
	}

	refresh := orch.RequestRefresh()
	if refresh["status"] != "accepted" {
		t.Fatalf("unexpected refresh payload: %#v", refresh)
	}
	events, err := store.ListRuntimeEvents(0, 10)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("expected retry/refresh events, got %#v", events)
	}
}

func TestSnapshotDoesNotRefreshProviderIssuesWhileHoldingRuntimeState(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	project, err := store.CreateProjectWithProvider("Slow Provider", "", "", "", "stub", "stub-ref", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	issue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		Identifier:       "STUB-1",
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Title:            "Slow issue",
		State:            kanban.StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	provider := &blockingIssueProvider{
		issue:      *issue,
		getStarted: make(chan struct{}, 1),
		getRelease: make(chan struct{}),
	}
	orch.service.RegisterProvider(provider)

	orch.mu.Lock()
	orch.retries[issue.ID] = retryEntry{
		Attempt: 1,
		Phase:   string(kanban.WorkflowPhaseImplementation),
		DueAt:   time.Now().UTC().Add(time.Minute),
	}
	orch.mu.Unlock()

	done := make(chan observability.Snapshot, 1)
	go func() {
		done <- orch.Snapshot()
	}()

	select {
	case snapshot := <-done:
		if len(snapshot.Retrying) != 1 || snapshot.Retrying[0].Identifier != issue.Identifier {
			t.Fatalf("unexpected snapshot payload: %+v", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("Snapshot blocked on provider refresh")
	}

	select {
	case <-provider.getStarted:
		t.Fatal("Snapshot should not call provider GetIssue for retry metadata")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestRetryIssueNowPreservesPlanApprovalThreadResumeHint(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	issue, err := store.CreateIssue("", "", "Plan approval retry", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(issue.ID, kanban.WorkflowPhaseImplementation); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase: %v", err)
	}
	requestedAt := time.Now().UTC().Truncate(time.Second)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Plan body", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      string(kanban.WorkflowPhaseImplementation),
		Attempt:    2,
		RunKind:    "retry_paused",
		Error:      planApprovalStopReason,
		StopReason: planApprovalStopReason,
		UpdatedAt:  requestedAt,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-plan-turn-1",
			ThreadID:        "thread-plan",
			TurnID:          "turn-plan",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	orch.mu.Lock()
	orch.paused[issue.ID] = pausedEntry{
		Attempt:  2,
		Phase:    string(kanban.WorkflowPhaseImplementation),
		PausedAt: requestedAt,
		Error:    planApprovalStopReason,
	}
	orch.mu.Unlock()

	result := orch.RetryIssueNow(context.Background(), issue.Identifier)
	if result["status"] != "queued_now" {
		t.Fatalf("expected queued_now retry result, got %#v", result)
	}
	if result["resume_thread_id"] != "thread-plan" {
		t.Fatalf("expected resume_thread_id in retry response, got %#v", result)
	}

	orch.mu.RLock()
	retry, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected retry entry after plan approval retry")
	}
	if retry.ResumeThreadID != "thread-plan" {
		t.Fatalf("expected retry to preserve plan approval thread, got %+v", retry)
	}
}

func TestRetryIssueNowPreservesPausedRunThreadResumeHint(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	issue, err := store.CreateIssue("", "", "Paused retry", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(issue.ID, kanban.WorkflowPhaseImplementation); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase: %v", err)
	}
	pausedAt := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      string(kanban.WorkflowPhaseImplementation),
		Attempt:    1,
		RunKind:    "retry_paused",
		Error:      "no_state_transition",
		StopReason: "no_state_transition",
		UpdatedAt:  pausedAt,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-paused-turn-1",
			ThreadID:        "thread-paused",
			TurnID:          "turn-paused",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	orch.mu.Lock()
	orch.paused[issue.ID] = pausedEntry{
		Attempt:  1,
		Phase:    string(kanban.WorkflowPhaseImplementation),
		PausedAt: pausedAt,
		Error:    "no_state_transition",
	}
	orch.mu.Unlock()

	result := orch.RetryIssueNow(context.Background(), issue.Identifier)
	if result["status"] != "queued_now" {
		t.Fatalf("expected queued_now retry result, got %#v", result)
	}
	if result["resume_thread_id"] != "thread-paused" {
		t.Fatalf("expected resume_thread_id in retry response, got %#v", result)
	}

	orch.mu.RLock()
	retry, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected retry entry after paused retry")
	}
	if retry.ResumeThreadID != "thread-paused" {
		t.Fatalf("expected paused retry to preserve thread resume hint, got %+v", retry)
	}
}

func TestResumeThreadIDHelpers(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	issue, err := store.CreateIssue("", "", "Resume helper coverage", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      string(kanban.WorkflowPhaseImplementation),
		Attempt:    1,
		RunKind:    "retry_paused",
		Error:      "no_state_transition",
		StopReason: "no_state_transition",
		UpdatedAt:  time.Now().UTC().Truncate(time.Second),
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-helper-turn-1",
			ThreadID:        "thread-helper",
			TurnID:          "turn-helper",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	if got := orch.retryResumeThreadID(issue.ID, "  thread-preferred  "); got != "thread-preferred" {
		t.Fatalf("expected explicit preferred resume thread to win, got %q", got)
	}
	if got := orch.planApprovalResumeThreadID(issue.ID); got != "" {
		t.Fatalf("expected non-plan-approval stop reason to be ignored, got %q", got)
	}
	if got := orch.retryResumeThreadID("missing-issue", ""); got != "" {
		t.Fatalf("expected missing retry session to return empty resume thread id, got %q", got)
	}
	if got := orch.planApprovalResumeThreadID("missing-issue"); got != "" {
		t.Fatalf("expected missing plan approval session to return empty resume thread id, got %q", got)
	}
}

func TestRetryIssueNowPreservesPendingPlanApprovalWhenRevisionIsQueued(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	issue, err := store.CreateIssue("", "", "Plan revision retry", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(issue.ID, kanban.WorkflowPhaseImplementation); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase: %v", err)
	}
	requestedAt := time.Now().UTC().Truncate(time.Second)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Plan body", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}
	if err := store.SetIssuePendingPlanRevision(issue.ID, "Tighten the rollout and add a rollback check.", requestedAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      string(kanban.WorkflowPhaseImplementation),
		Attempt:    2,
		RunKind:    "retry_paused",
		Error:      planApprovalStopReason,
		StopReason: planApprovalStopReason,
		UpdatedAt:  requestedAt,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-plan-turn-2",
			ThreadID:        "thread-plan",
			TurnID:          "turn-plan",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	orch.mu.Lock()
	orch.paused[issue.ID] = pausedEntry{
		Attempt:  2,
		Phase:    string(kanban.WorkflowPhaseImplementation),
		PausedAt: requestedAt,
		Error:    planApprovalStopReason,
	}
	orch.mu.Unlock()

	result := orch.RetryIssueNow(context.Background(), issue.Identifier)
	if result["status"] != "queued_now" {
		t.Fatalf("expected queued_now retry result, got %#v", result)
	}
	if result["resume_thread_id"] != "thread-plan" {
		t.Fatalf("expected resume_thread_id in retry response, got %#v", result)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !updated.PlanApprovalPending {
		t.Fatalf("expected pending plan approval to remain queued when revision is present, got %+v", updated)
	}
	if updated.PendingPlanRevisionMarkdown != "Tighten the rollout and add a rollback check." {
		t.Fatalf("expected pending plan revision to remain queued, got %+v", updated)
	}

	orch.mu.RLock()
	retry, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected retry entry after plan revision retry")
	}
	if retry.ResumeThreadID != "thread-plan" {
		t.Fatalf("expected retry to preserve plan approval thread, got %+v", retry)
	}
}

func TestProcessRetriesStartsQueuedPlanRevisionRetry(t *testing.T) {
	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	command, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-plan"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-plan"}}}},
				{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-plan", "turnId": "turn-plan"}}},
			}},
		},
	})
	writeAppServerWorkflow(t, manager, workspaceRoot, command, "never", 3000, 3000)

	_, issue := createRunningProjectIssue(t, store, "Queued plan revision", "", 0, nil)
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(issue.ID, kanban.WorkflowPhaseImplementation); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase: %v", err)
	}
	if err := store.UpdateIssuePermissionProfile(issue.ID, kanban.PermissionProfilePlanThenFullAccess); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile: %v", err)
	}
	requestedAt := time.Now().UTC().Truncate(time.Second)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Plan body", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}
	if err := store.SetIssuePendingPlanRevision(issue.ID, "Tighten the rollout and add a rollback check.", requestedAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision: %v", err)
	}

	result := orch.RetryIssueNow(context.Background(), issue.Identifier)
	if result["status"] != "queued_now" {
		t.Fatalf("expected queued_now retry result, got %#v", result)
	}

	orch.processRetries(context.Background())
	waitForRunStartedExecutionSnapshot(t, store, issue.ID, 3*time.Second)
	waitForNoRunning(t, orch, 3*time.Second)

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after retry: %v", err)
	}
	if !updated.PlanApprovalPending {
		t.Fatalf("expected pending plan approval to remain queued, got %+v", updated)
	}
	if updated.PendingPlanRevisionMarkdown != "Tighten the rollout and add a rollback check." || updated.PendingPlanRevisionRequestedAt == nil {
		t.Fatalf("expected pending plan revision to remain attached during drafting, got %+v", updated)
	}
	planning, err := store.GetIssuePlanning(updated)
	if err != nil {
		t.Fatalf("GetIssuePlanning after retry: %v", err)
	}
	if planning == nil || planning.Status != kanban.IssuePlanningStatusDrafting {
		t.Fatalf("expected drafting planning state after turn start, got %#v", planning)
	}
}

func TestFinishRunQueuesImmediateRetryWhenPlanRevisionArrivesBeforePlanPause(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	issue, err := store.CreateIssue("", "", "Plan revision race", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(issue.ID, kanban.WorkflowPhaseImplementation); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase: %v", err)
	}
	requestedAt := time.Now().UTC().Truncate(time.Second)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Plan body", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}
	if err := store.SetIssuePendingPlanRevision(issue.ID, "Tighten the rollout and add a rollback check.", requestedAt.Add(time.Minute)); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision: %v", err)
	}

	workflow, err := orch.workflows.Current()
	if err != nil {
		t.Fatalf("Current workflow: %v", err)
	}
	result := &agent.RunResult{
		Success:    false,
		StopReason: planApprovalStopReason,
		AppSession: &agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-plan-turn-1",
			ThreadID:        "thread-plan",
			TurnID:          "turn-plan",
		},
	}

	orch.finishRun(workflow, orch.runner, issue, kanban.WorkflowPhaseImplementation, 1, result, nil)

	orch.mu.RLock()
	retry, retryQueued := orch.retries[issue.ID]
	_, paused := orch.paused[issue.ID]
	orch.mu.RUnlock()
	if !retryQueued {
		t.Fatal("expected immediate retry to be queued when a plan revision is pending")
	}
	if paused {
		t.Fatal("did not expect plan approval pause when a revision note is already queued")
	}
	if retry.Attempt != 2 {
		t.Fatalf("expected retry attempt 2, got %+v", retry)
	}
	if retry.DelayType != "manual" {
		t.Fatalf("expected manual retry delay type, got %+v", retry)
	}
	if retry.ResumeThreadID != "thread-plan" {
		t.Fatalf("expected retry to preserve thread resume id, got %+v", retry)
	}

	events, err := store.ListRuntimeEvents(0, 10)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected runtime events after plan revision retry scheduling")
	}
	latest := events[0]
	if latest.Kind != "retry_scheduled" || latest.DelayType != "manual" || latest.Error != planApprovalStopReason {
		t.Fatalf("expected manual retry_scheduled event for plan revision, got %+v", latest)
	}
}

func TestRetryIssueNowQueuesPlanApprovalRetryWhenOnlyPendingFlagIsSet(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	issue, err := store.CreateIssue("", "", "Plan approval retry without snapshot", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	if err := store.UpdateIssueWorkflowPhase(issue.ID, kanban.WorkflowPhaseImplementation); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase: %v", err)
	}
	requestedAt := time.Now().UTC().Truncate(time.Second)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Plan body", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}

	result := orch.RetryIssueNow(context.Background(), issue.Identifier)
	if result["status"] != "queued_now" {
		t.Fatalf("expected queued_now retry result, got %#v", result)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.PlanApprovalPending {
		t.Fatalf("expected pending plan approval to clear, got %+v", updated)
	}

	orch.mu.RLock()
	retry, ok := orch.retries[issue.ID]
	orch.mu.RUnlock()
	if !ok {
		t.Fatal("expected retry entry after plan approval retry")
	}
	if retry.ResumeThreadID != "" {
		t.Fatalf("expected retry without snapshot to omit resume thread id, got %+v", retry)
	}
	if retry.Error != planApprovalStopReason {
		t.Fatalf("expected plan approval stop reason, got %+v", retry)
	}
}

func TestRunRecurringIssueNowQueuesIdleRecurringIssue(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	issue, err := store.CreateIssueWithOptions("", "", "Scan GitHub ready-to-work", "", 0, nil, kanban.IssueCreateOptions{
		IssueType: kanban.IssueTypeRecurring,
		Cron:      "*/15 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions: %v", err)
	}

	result := orch.RunRecurringIssueNow(context.Background(), issue.Identifier)
	if result["status"] != "queued_now" {
		t.Fatalf("expected queued_now, got %#v", result)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.State != kanban.StateReady || updated.WorkflowPhase != kanban.WorkflowPhaseImplementation {
		t.Fatalf("expected ready implementation after run-now, got state=%s phase=%s", updated.State, updated.WorkflowPhase)
	}
	if updated.LastEnqueuedAt == nil || updated.NextRunAt == nil || !updated.NextRunAt.After(*updated.LastEnqueuedAt) {
		t.Fatalf("expected recurring schedule metadata after run-now, got %+v", updated)
	}

	events, err := store.ListRuntimeEvents(0, 20)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "recurring_manual_run_now_enqueued" && event.Identifier == issue.Identifier {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected recurring_manual_run_now_enqueued event, got %#v", events)
	}
}

func TestRunRecurringIssueNowCoalescesOccupiedRecurringIssue(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	issue, err := store.CreateIssueWithOptions("", "", "Occupied recurring", "", 0, nil, kanban.IssueCreateOptions{
		IssueType: kanban.IssueTypeRecurring,
		Cron:      "0 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	result := orch.RunRecurringIssueNow(context.Background(), issue.Identifier)
	if result["status"] != "pending_rerun_recorded" {
		t.Fatalf("expected pending_rerun_recorded, got %#v", result)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !updated.PendingRerun {
		t.Fatalf("expected pending rerun to be recorded, got %+v", updated)
	}

	result = orch.RunRecurringIssueNow(context.Background(), issue.Identifier)
	if result["status"] != "pending_rerun_already_set" {
		t.Fatalf("expected pending_rerun_already_set, got %#v", result)
	}
}

func TestProcessDueRecurringIssuesEnqueuesCatchUpAndCoalescesBusyRuns(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestrator(t, "cat")
	project, err := store.CreateProject("Recurring", "", filepath.Dir(manager.Path()), manager.Path())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState recurring project: %v", err)
	}

	idleIssue, err := store.CreateIssueWithOptions(project.ID, "", "Idle recurring", "", 0, nil, kanban.IssueCreateOptions{
		IssueType: kanban.IssueTypeRecurring,
		Cron:      "*/10 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions idle: %v", err)
	}
	busyIssue, err := store.CreateIssueWithOptions(project.ID, "", "Busy recurring", "", 0, nil, kanban.IssueCreateOptions{
		IssueType: kanban.IssueTypeRecurring,
		Cron:      "*/10 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions busy: %v", err)
	}
	if err := store.UpdateIssueState(busyIssue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState busy: %v", err)
	}

	past := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	db, err := sql.Open("sqlite3", store.DBPath())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE issue_recurrences SET next_run_at = ?, pending_rerun = 0 WHERE issue_id = ?`, past, idleIssue.ID); err != nil {
		t.Fatalf("update idle recurrence: %v", err)
	}
	if _, err := db.Exec(`UPDATE issue_recurrences SET next_run_at = ?, pending_rerun = 0 WHERE issue_id = ?`, past, busyIssue.ID); err != nil {
		t.Fatalf("update busy recurrence: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close recurrence db: %v", err)
	}

	orch.processDueRecurringIssues(context.Background())

	idleUpdated, err := store.GetIssue(idleIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue idle: %v", err)
	}
	if idleUpdated.State != kanban.StateReady || idleUpdated.LastEnqueuedAt == nil {
		t.Fatalf("expected idle recurring issue to be enqueued, got %+v", idleUpdated)
	}

	busyUpdated, err := store.GetIssue(busyIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue busy: %v", err)
	}
	if !busyUpdated.PendingRerun {
		t.Fatalf("expected busy recurring issue to record pending rerun, got %+v", busyUpdated)
	}

	events, err := store.ListRuntimeEvents(0, 20)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	var foundCatchUp bool
	var foundPending bool
	for _, event := range events {
		if event.Kind == "recurring_catch_up_enqueued" && event.Identifier == idleIssue.Identifier {
			foundCatchUp = true
		}
		if event.Kind == "recurring_pending_rerun_recorded" && event.Identifier == busyIssue.Identifier {
			foundPending = true
		}
	}
	if !foundCatchUp || !foundPending {
		t.Fatalf("expected catch-up and pending-rerun events, got %#v", events)
	}
}

func TestProcessPendingRecurringRerunEnqueuesWhenIssueBecomesIdle(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")

	issue, err := store.CreateIssueWithOptions("", "", "Pending recurring", "", 0, nil, kanban.IssueCreateOptions{
		IssueType: kanban.IssueTypeRecurring,
		Cron:      "*/20 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions: %v", err)
	}
	if err := store.MarkRecurringPendingRerun(issue.ID, true); err != nil {
		t.Fatalf("MarkRecurringPendingRerun: %v", err)
	}

	pending, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue pending: %v", err)
	}
	if !pending.PendingRerun {
		t.Fatalf("expected pending rerun before processing, got %+v", pending)
	}

	if !orch.processPendingRecurringRerun(pending) {
		t.Fatal("expected pending recurring rerun to be enqueued")
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue updated: %v", err)
	}
	if updated.State != kanban.StateReady || updated.PendingRerun {
		t.Fatalf("expected ready recurring issue with cleared pending rerun, got %+v", updated)
	}
}

func TestSharedDispatchUsesScopedRuntimeForProjectlessIssue(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	repoPath := filepath.Join(tmpDir, "repo")
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("MkdirAll repo: %v", err)
	}

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	workflowContent := `---
tracker:
  kind: kanban
  active_states:
    - ready
    - in_progress
    - in_review
  terminal_states:
    - done
    - cancelled
polling:
  interval_ms: 50
workspace:
  root: ` + workspaceRoot + `
hooks:
  timeout_ms: 1000
` + neutralWorkflowControlBlocks("codex-appserver", "cat", "codex exec", "never", 1, 1, 100, 8, 1000, 500, 300000) + `
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(workflowContent), 0o644); err != nil {
		t.Fatalf("WriteFile workflow: %v", err)
	}

	orch := NewSharedWithExtensions(store, nil, repoPath, workflowPath)
	runner := newControlledRunner(store)
	orch.runnerFactory = func(*config.Manager) runnerExecutor { return runner }
	t.Cleanup(func() {
		orch.stopAllRuns()
		waitForNoRunning(t, orch, time.Second)
	})

	issue, err := store.CreateIssue("", "", "Scoped issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState failed: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}

	starts := runner.waitForStarts(t, 1, time.Second)
	if starts[0] != issue.Identifier {
		t.Fatalf("expected start for %s, got %v", issue.Identifier, starts)
	}

	runner.complete(issue.Identifier)
	waitForNoRunning(t, orch, time.Second)
}

func TestSharedDBStressPreventsRunawayRetriesAndLockContention(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "stress.db")
	workspaceRoot := filepath.Join(tmpDir, "workspaces")

	logBuffer := &syncBuffer{}
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	adminStore, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore admin: %v", err)
	}
	t.Cleanup(func() { _ = adminStore.Close() })

	completeCommand, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-complete"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-complete"}}}},
				{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-complete", "turn": map[string]interface{}{"id": "turn-complete", "status": "completed", "items": []interface{}{}}}}},
			}, ExitCode: fakeappserver.Int(0)},
		},
	})
	inputCommand, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-input"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{
				{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-input"}}}},
				{JSON: map[string]interface{}{"id": 4, "method": "item/tool/requestUserInput", "params": map[string]interface{}{"questions": []map[string]interface{}{{"id": "path", "options": []map[string]interface{}{{"label": "Option A"}, {"label": "Option B"}}}}}}},
			}, WaitForRelease: "never"},
		},
	})
	stallCommand, _ := fakeappserver.CommandString(t, fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{Match: fakeappserver.Match{Method: "initialize"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}}},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{Match: fakeappserver.Match{Method: "thread/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-stall"}}}}}},
			{Match: fakeappserver.Match{Method: "turn/start"}, Emit: []fakeappserver.Output{{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-stall"}}}}}, WaitForRelease: "never"},
		},
	})

	type sharedFixture struct {
		repoPath     string
		workflowPath string
		projectID    string
		issueID      string
		identifier   string
	}

	createFixture := func(name, command string, reviewEnabled bool, turnTimeoutMs, stallTimeoutMs int) sharedFixture {
		repoPath := filepath.Join(tmpDir, name)
		workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
		if err := os.MkdirAll(repoPath, 0o755); err != nil {
			t.Fatalf("MkdirAll repo: %v", err)
		}
		writeSharedAppServerWorkflow(t, workflowPath, workspaceRoot, command, reviewEnabled, 8, turnTimeoutMs, stallTimeoutMs)
		initGitRepoForTest(t, repoPath)
		project, err := adminStore.CreateProject(name, "", repoPath, workflowPath)
		if err != nil {
			t.Fatalf("CreateProject %s: %v", name, err)
		}
		if err := adminStore.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
			t.Fatalf("UpdateProjectState %s: %v", name, err)
		}
		issue, err := adminStore.CreateIssue(project.ID, "", name+" issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue %s: %v", name, err)
		}
		if err := adminStore.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
			t.Fatalf("UpdateIssueState %s: %v", name, err)
		}
		return sharedFixture{
			repoPath:     repoPath,
			workflowPath: workflowPath,
			projectID:    project.ID,
			issueID:      issue.ID,
			identifier:   issue.Identifier,
		}
	}

	fixtures := []sharedFixture{
		createFixture("advance-project", completeCommand, true, 1500, 1500),
		createFixture("no-transition-project", completeCommand, false, 1500, 1500),
		createFixture("input-project", inputCommand, false, 1500, 1500),
		createFixture("stall-project", stallCommand, false, 1500, 250),
	}

	var (
		orchestrators []*Orchestrator
		stores        []*kanban.Store
	)
	for _, fixture := range fixtures {
		store, err := kanban.NewStore(dbPath)
		if err != nil {
			t.Fatalf("NewStore shared: %v", err)
		}
		stores = append(stores, store)
		orch := NewSharedWithExtensions(store, nil, fixture.repoPath, fixture.workflowPath)
		orchestrators = append(orchestrators, orch)
	}
	t.Cleanup(func() {
		for _, orch := range orchestrators {
			orch.stopAllRuns()
		}
		for _, orch := range orchestrators {
			waitForNoRunning(t, orch, 5*time.Second)
		}
		for _, store := range stores {
			_ = store.Close()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, len(orchestrators))
	for _, orch := range orchestrators {
		wg.Add(1)
		go func(orch *Orchestrator) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if ctx.Err() != nil {
					return
				}
				if err := orch.tick(ctx); err != nil {
					errCh <- err
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
		}(orch)
	}

	waitForIssueRetryState(t, adminStore, fixtures[0].issueID, "continuation", 6*time.Second)
	waitForIssuePauseReason(t, adminStore, fixtures[1].issueID, "no_state_transition", 6*time.Second)
	waitForIssuePauseReason(t, adminStore, fixtures[2].issueID, "turn_input_required", 6*time.Second)
	waitForIssuePauseReason(t, adminStore, fixtures[3].issueID, "stall_timeout", 6*time.Second)

	cancel()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("tick failed: %v", err)
		}
	}

	if strings.Contains(logBuffer.String(), "database is locked") {
		t.Fatalf("unexpected sqlite lock contention in logs: %s", logBuffer.String())
	}

	for _, fixture := range fixtures {
		events, err := adminStore.ListIssueRuntimeEvents(fixture.issueID, 50)
		if err != nil {
			t.Fatalf("ListIssueRuntimeEvents %s: %v", fixture.identifier, err)
		}
		assertRetryEventInvariants(t, events)
	}

	advanceEvents, err := adminStore.ListIssueRuntimeEvents(fixtures[0].issueID, 20)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents advance: %v", err)
	}
	foundContinuation := false
	for _, event := range advanceEvents {
		if event.Kind == "retry_scheduled" && event.DelayType == "continuation" {
			foundContinuation = true
			break
		}
	}
	if !foundContinuation {
		t.Fatalf("expected continuation retry for normal advancement, got %+v", advanceEvents)
	}
	if latest := advanceEvents[len(advanceEvents)-1]; latest.Kind == "retry_paused" {
		t.Fatalf("expected advance path to stay unpaused, got %+v", latest)
	}

	noTransitionSnapshot, err := adminStore.GetIssueExecutionSession(fixtures[1].issueID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession no-transition: %v", err)
	}
	if noTransitionSnapshot.Error != "no_state_transition" {
		t.Fatalf("unexpected no-transition snapshot: %+v", noTransitionSnapshot)
	}

	inputSnapshot, err := adminStore.GetIssueExecutionSession(fixtures[2].issueID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession input: %v", err)
	}
	if inputSnapshot.Error != "turn_input_required" {
		t.Fatalf("unexpected input snapshot: %+v", inputSnapshot)
	}

	stallSnapshot, err := adminStore.GetIssueExecutionSession(fixtures[3].issueID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession stall: %v", err)
	}
	if stallSnapshot.Error != "stall_timeout" && stallSnapshot.Error != "run_interrupted" {
		t.Fatalf("unexpected stall snapshot: %+v", stallSnapshot)
	}
}

type runnerEvent struct {
	kind       string
	identifier string
}

type phaseRunHandler func(issue *kanban.Issue) (*agent.RunResult, error)

type phaseScriptRunner struct {
	store    *kanban.Store
	handlers map[kanban.WorkflowPhase]phaseRunHandler
}

type countingPhaseRunner struct {
	store    *kanban.Store
	runCalls chan string
	mu       sync.Mutex
	counts   map[string]int
}

func (r *countingPhaseRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	if issue.State == kanban.StateReady {
		if err := r.store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
			return nil, err
		}
	}
	if r.runCalls != nil {
		r.runCalls <- issue.Identifier
	}
	r.mu.Lock()
	if r.counts == nil {
		r.counts = make(map[string]int)
	}
	r.counts[issue.Identifier]++
	r.mu.Unlock()
	return &agent.RunResult{Success: true}, nil
}

func (r *countingPhaseRunner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	return nil
}

func (r *countingPhaseRunner) runCount(identifier string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[identifier]
}

func (r *phaseScriptRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	if issue.State == kanban.StateReady {
		if err := r.store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
			return nil, err
		}
	}
	if handler := r.handlers[issue.WorkflowPhase]; handler != nil {
		return handler(issue)
	}
	return &agent.RunResult{Success: true}, nil
}

func (r *phaseScriptRunner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	workspace, err := r.store.GetWorkspace(issue.ID)
	if err == nil && workspace != nil {
		_ = os.RemoveAll(workspace.Path)
	}
	return r.store.DeleteWorkspace(issue.ID)
}

type terminalTransitionRunner struct {
	store       *kanban.Store
	movedToDone chan struct{}
	release     chan struct{}
	once        sync.Once
}

func newTerminalTransitionRunner(store *kanban.Store) *terminalTransitionRunner {
	return &terminalTransitionRunner{
		store:       store,
		movedToDone: make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (r *terminalTransitionRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	if issue.State == kanban.StateReady {
		if err := r.store.UpdateIssueState(issue.ID, kanban.StateInProgress); err != nil {
			return nil, err
		}
	}
	if err := r.store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		return nil, err
	}
	r.once.Do(func() { close(r.movedToDone) })

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.release:
	}
	return &agent.RunResult{Success: true}, nil
}

func (r *terminalTransitionRunner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	return nil
}

func (r *terminalTransitionRunner) waitForMovedToDone(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-r.movedToDone:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for issue to move to done")
	}
}

func (r *terminalTransitionRunner) complete() {
	close(r.release)
}

type controlledRunner struct {
	store   *kanban.Store
	mu      sync.Mutex
	starts  []string
	events  []runnerEvent
	waiters map[string]chan struct{}
}

func newControlledRunner(store *kanban.Store) *controlledRunner {
	return &controlledRunner{
		store:   store,
		waiters: make(map[string]chan struct{}),
	}
}

func (r *controlledRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	if issue.State == kanban.StateReady {
		_ = r.store.UpdateIssueState(issue.ID, kanban.StateInProgress)
	}
	r.mu.Lock()
	r.starts = append(r.starts, issue.Identifier)
	r.events = append(r.events, runnerEvent{kind: "start", identifier: issue.Identifier})
	ch, ok := r.waiters[issue.Identifier]
	if !ok {
		ch = make(chan struct{})
		r.waiters[issue.Identifier] = ch
	}
	r.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-ch:
	}

	if err := r.store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.events = append(r.events, runnerEvent{kind: "done", identifier: issue.Identifier})
	r.mu.Unlock()
	return &agent.RunResult{Success: true}, nil
}

func (r *controlledRunner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	return nil
}

func (r *controlledRunner) complete(identifier string) {
	r.mu.Lock()
	ch, ok := r.waiters[identifier]
	if !ok {
		ch = make(chan struct{})
		r.waiters[identifier] = ch
	}
	delete(r.waiters, identifier)
	r.mu.Unlock()
	close(ch)
}

func (r *controlledRunner) waitForStarts(t *testing.T, expected int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		starts := append([]string(nil), r.starts...)
		r.mu.Unlock()
		if len(starts) >= expected {
			return starts
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d starts", expected)
	return nil
}

func (r *controlledRunner) snapshotEvents() []runnerEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runnerEvent(nil), r.events...)
}

type cancelledResultRunner struct {
	store   *kanban.Store
	mu      sync.Mutex
	starts  []string
	waiters map[string]chan struct{}
}

func newCancelledResultRunner(store *kanban.Store) *cancelledResultRunner {
	return &cancelledResultRunner{
		store:   store,
		waiters: make(map[string]chan struct{}),
	}
}

func (r *cancelledResultRunner) RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error) {
	if issue.State == kanban.StateReady {
		_ = r.store.UpdateIssueState(issue.ID, kanban.StateInProgress)
	}
	r.mu.Lock()
	r.starts = append(r.starts, issue.Identifier)
	ch, ok := r.waiters[issue.Identifier]
	if !ok {
		ch = make(chan struct{})
		r.waiters[issue.Identifier] = ch
	}
	r.mu.Unlock()

	select {
	case <-ctx.Done():
		return &agent.RunResult{Success: false, Error: ctx.Err()}, nil
	case <-ch:
	}

	if err := r.store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		return nil, err
	}
	return &agent.RunResult{Success: true}, nil
}

func (r *cancelledResultRunner) CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error {
	return nil
}

func (r *cancelledResultRunner) waitForStarts(t *testing.T, expected int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		starts := append([]string(nil), r.starts...)
		r.mu.Unlock()
		if len(starts) >= expected {
			return starts
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d starts", expected)
	return nil
}

func assertBlockedExecution(t *testing.T, events []runnerEvent, blockers map[string][]string) {
	t.Helper()
	doneAt := map[string]int{}
	for idx, event := range events {
		switch event.kind {
		case "done":
			doneAt[event.identifier] = idx
		case "start":
			for _, blocker := range blockers[event.identifier] {
				doneIdx, ok := doneAt[blocker]
				if !ok || doneIdx >= idx {
					t.Fatalf("issue %s started before blocker %s completed; events=%v", event.identifier, blocker, events)
				}
			}
		}
	}
}

func createDependencyGraph(t *testing.T, store *kanban.Store) map[string]*kanban.Issue {
	t.Helper()
	project, err := store.CreateProject("Graph", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState failed: %v", err)
	}
	makeIssue := func(title string, priority int) *kanban.Issue {
		issue, err := store.CreateIssue(project.ID, "", title, "", priority, nil)
		if err != nil {
			t.Fatalf("CreateIssue failed: %v", err)
		}
		if err := store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
			t.Fatalf("UpdateIssueState failed: %v", err)
		}
		return issue
	}
	issues := map[string]*kanban.Issue{
		"A": makeIssue("A", 1),
		"B": makeIssue("B", 2),
		"C": makeIssue("C", 3),
		"D": makeIssue("D", 4),
		"E": makeIssue("E", 5),
	}
	if _, err := store.SetIssueBlockers(issues["B"].ID, []string{issues["A"].Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}
	if _, err := store.SetIssueBlockers(issues["C"].ID, []string{issues["A"].Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}
	if _, err := store.SetIssueBlockers(issues["D"].ID, []string{issues["B"].Identifier, issues["C"].Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}
	return issues
}

func TestDispatchRespectsDependencyOrderSerial(t *testing.T) {
	orch, store, _, _ := setupTestOrchestratorWithConcurrency(t, "cat", 1)
	runner := newControlledRunner(store)
	orch.runner = runner
	issues := createDependencyGraph(t, store)

	expected := []string{
		issues["A"].Identifier,
		issues["B"].Identifier,
		issues["C"].Identifier,
		issues["D"].Identifier,
		issues["E"].Identifier,
	}

	for idx, identifier := range expected {
		if err := orch.dispatch(context.Background()); err != nil {
			t.Fatalf("dispatch failed: %v", err)
		}
		starts := runner.waitForStarts(t, idx+1, time.Second)
		if starts[idx] != identifier {
			t.Fatalf("expected start %d to be %s, got %s (all starts=%v)", idx, identifier, starts[idx], starts)
		}
		runner.complete(identifier)
		waitForNoRunning(t, orch, time.Second)
	}

	assertBlockedExecution(t, runner.snapshotEvents(), map[string][]string{
		issues["B"].Identifier: {issues["A"].Identifier},
		issues["C"].Identifier: {issues["A"].Identifier},
		issues["D"].Identifier: {issues["B"].Identifier, issues["C"].Identifier},
	})
}

func TestDispatchRespectsDependencyOrderParallel(t *testing.T) {
	orch, store, _, _ := setupTestOrchestratorWithConcurrency(t, "cat", 2)
	runner := newControlledRunner(store)
	orch.runner = runner
	issues := createDependencyGraph(t, store)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	starts := runner.waitForStarts(t, 2, time.Second)
	got := map[string]bool{
		starts[0]: true,
		starts[1]: true,
	}
	if !got[issues["A"].Identifier] || !got[issues["E"].Identifier] {
		t.Fatalf("expected initial parallel starts A and E, got %v", starts)
	}

	runner.complete(issues["E"].Identifier)
	runner.complete(issues["A"].Identifier)
	waitForNoRunning(t, orch, time.Second)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	starts = runner.waitForStarts(t, 4, time.Second)
	got = map[string]bool{
		starts[2]: true,
		starts[3]: true,
	}
	if !got[issues["B"].Identifier] || !got[issues["C"].Identifier] {
		t.Fatalf("expected B and C after A, got %v", starts)
	}

	runner.complete(issues["B"].Identifier)
	waitForRunningCount(t, orch, 1, time.Second)
	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	starts = runner.waitForStarts(t, 4, time.Second)
	if len(starts) != 4 {
		t.Fatalf("expected D to stay blocked while C is running, got %v", starts)
	}

	runner.complete(issues["C"].Identifier)
	waitForNoRunning(t, orch, time.Second)
	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	starts = runner.waitForStarts(t, 5, time.Second)
	if starts[4] != issues["D"].Identifier {
		t.Fatalf("expected D after B and C, got %v", starts)
	}
	runner.complete(issues["D"].Identifier)
	waitForNoRunning(t, orch, time.Second)

	assertBlockedExecution(t, runner.snapshotEvents(), map[string][]string{
		issues["B"].Identifier: {issues["A"].Identifier},
		issues["C"].Identifier: {issues["A"].Identifier},
		issues["D"].Identifier: {issues["B"].Identifier, issues["C"].Identifier},
	})
}

func TestDispatchPerProjectSerialStartsOneIssueAtATimePerProject(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestratorWithConcurrency(t, "cat", 2)
	setWorkflowDispatchMode(t, manager, config.DispatchModePerProjectSerial)
	runner := newControlledRunner(store)
	orch.runner = runner

	project, err := store.CreateProject("Serial", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState project failed: %v", err)
	}
	first, err := store.CreateIssue(project.ID, "", "First", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue first failed: %v", err)
	}
	second, err := store.CreateIssue(project.ID, "", "Second", "", 2, nil)
	if err != nil {
		t.Fatalf("CreateIssue second failed: %v", err)
	}
	if err := store.UpdateIssueState(first.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState first failed: %v", err)
	}
	if err := store.UpdateIssueState(second.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState second failed: %v", err)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	starts := runner.waitForStarts(t, 1, time.Second)
	if starts[0] != first.Identifier {
		t.Fatalf("expected first start %s, got %v", first.Identifier, starts)
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("second dispatch failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	runner.mu.Lock()
	startCount := len(runner.starts)
	runner.mu.Unlock()
	if startCount != 1 {
		t.Fatalf("expected only one active start for project in serial mode, got %d starts: %v", startCount, starts)
	}

	runner.complete(first.Identifier)
	waitForNoRunning(t, orch, time.Second)

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatalf("third dispatch failed: %v", err)
	}
	starts = runner.waitForStarts(t, 2, time.Second)
	if starts[1] != second.Identifier {
		t.Fatalf("expected second start %s after first completed, got %v", second.Identifier, starts)
	}
	runner.complete(second.Identifier)
	waitForNoRunning(t, orch, time.Second)
}

func TestPerProjectSerialDispatchPrefersHigherPriorityOverDueRetry(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestratorWithConcurrency(t, "cat", 2)
	setWorkflowDispatchMode(t, manager, config.DispatchModePerProjectSerial)
	runner := newControlledRunner(store)
	orch.runner = runner

	project, err := store.CreateProject("Priority", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState project failed: %v", err)
	}
	high, err := store.CreateIssue(project.ID, "", "High", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue high failed: %v", err)
	}
	lowWithRetry, err := store.CreateIssue(project.ID, "", "Low retry", "", 5, nil)
	if err != nil {
		t.Fatalf("CreateIssue low failed: %v", err)
	}
	if err := store.UpdateIssueState(high.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState high failed: %v", err)
	}
	if err := store.UpdateIssueState(lowWithRetry.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState low failed: %v", err)
	}

	orch.mu.Lock()
	orch.retries[lowWithRetry.ID] = retryEntry{
		Attempt:   2,
		Phase:     string(kanban.WorkflowPhaseImplementation),
		DueAt:     time.Now().UTC().Add(-time.Second),
		DelayType: "manual",
	}
	orch.claimed[lowWithRetry.ID] = struct{}{}
	orch.mu.Unlock()

	if err := orch.tick(context.Background()); err != nil {
		t.Fatalf("tick failed: %v", err)
	}
	starts := runner.waitForStarts(t, 1, time.Second)
	if starts[0] != high.Identifier {
		t.Fatalf("expected higher priority fresh issue %s to start first, got %v", high.Identifier, starts)
	}

	runner.complete(high.Identifier)
	waitForNoRunning(t, orch, time.Second)

	if err := orch.tick(context.Background()); err != nil {
		t.Fatalf("second tick failed: %v", err)
	}
	starts = runner.waitForStarts(t, 2, time.Second)
	if starts[1] != lowWithRetry.Identifier {
		t.Fatalf("expected retry issue %s to start second, got %v", lowWithRetry.Identifier, starts)
	}
	runner.complete(lowWithRetry.Identifier)
	waitForNoRunning(t, orch, time.Second)
}

func TestFindReviewPreviewVideoReturnsKnownMediaExtensions(t *testing.T) {
	workspace := t.TempDir()
	previewDir := filepath.Join(workspace, ".maestro", "review-preview")
	if err := os.MkdirAll(previewDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	expected := filepath.Join(previewDir, "walkthrough.webm")
	if err := os.WriteFile(expected, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(previewDir, "notes.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatalf("WriteFile txt: %v", err)
	}

	got, err := findReviewPreviewVideo(workspace)
	if err != nil {
		t.Fatalf("findReviewPreviewVideo: %v", err)
	}
	if got != expected {
		t.Fatalf("expected %s, got %s", expected, got)
	}
}

func TestTickSyncsProviderIssuesOnlyOnce(t *testing.T) {
	orch, store, manager, _ := setupTestOrchestrator(t, "codex")
	repoPath := filepath.Dir(manager.Path())

	if _, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		repoPath,
		manager.Path(),
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug"},
	); err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}

	provider := &countingProvider{}
	orch.service.RegisterProvider(provider)

	if err := orch.tick(context.Background()); err != nil {
		t.Fatalf("tick failed: %v", err)
	}
	if calls := provider.Calls(); calls != 1 {
		t.Fatalf("expected one provider sync per tick, got %d", calls)
	}
}

func TestRetryIssueNowDoesNotHoldMutexAcrossProviderLookupAndHonorsCancellation(t *testing.T) {
	orch, store, _, _ := setupTestOrchestrator(t, "cat")
	project, err := store.CreateProjectWithProvider("Slow Provider", "", "", "", "stub", "stub-ref", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	issue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		Identifier:       "STUB-1",
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Title:            "Slow issue",
		State:            kanban.StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}

	provider := &blockingIssueProvider{
		issue:      *issue,
		getStarted: make(chan struct{}, 1),
		getRelease: make(chan struct{}),
	}
	orch.service.RegisterProvider(provider)

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan map[string]interface{}, 1)
	go func() {
		resultCh <- orch.RetryIssueNow(ctx, issue.Identifier)
	}()

	select {
	case <-provider.getStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider lookup to start")
	}

	refreshDone := make(chan map[string]interface{}, 1)
	go func() {
		refreshDone <- orch.RequestRefresh()
	}()

	select {
	case result := <-refreshDone:
		if result["status"] != "accepted" {
			t.Fatalf("unexpected refresh result while retry lookup is blocked: %#v", result)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("expected RequestRefresh to acquire orchestrator mutex while retry lookup is blocked")
	}

	cancel()
	select {
	case result := <-resultCh:
		if result["status"] != "error" {
			t.Fatalf("expected canceled retry to report error status, got %#v", result)
		}
		if !strings.Contains(fmt.Sprint(result["error"]), context.Canceled.Error()) {
			t.Fatalf("expected canceled retry error, got %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled retry result")
	}
}

func TestFindReviewPreviewVideoSkipsSymlinkArtifacts(t *testing.T) {
	workspace := t.TempDir()
	previewDir := filepath.Join(workspace, ".maestro", "review-preview")
	if err := os.MkdirAll(previewDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "secret.mp4")
	if err := os.WriteFile(outside, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(previewDir, "leak.mp4")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	got, err := findReviewPreviewVideo(workspace)
	if err != nil {
		t.Fatalf("findReviewPreviewVideo: %v", err)
	}
	if got != "" {
		t.Fatalf("expected symlink preview to be ignored, got %s", got)
	}
}

func TestFindReviewPreviewVideoReturnsNewestArtifact(t *testing.T) {
	workspace := t.TempDir()
	previewDir := filepath.Join(workspace, ".maestro", "review-preview")
	if err := os.MkdirAll(previewDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	oldest := filepath.Join(previewDir, "older.mp4")
	newest := filepath.Join(previewDir, "newer.webm")
	if err := os.WriteFile(oldest, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile older: %v", err)
	}
	if err := os.WriteFile(newest, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile newer: %v", err)
	}
	base := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldest, base, base); err != nil {
		t.Fatalf("Chtimes older: %v", err)
	}
	newerTime := base.Add(2 * time.Minute)
	if err := os.Chtimes(newest, newerTime, newerTime); err != nil {
		t.Fatalf("Chtimes newer: %v", err)
	}

	got, err := findReviewPreviewVideo(workspace)
	if err != nil {
		t.Fatalf("findReviewPreviewVideo: %v", err)
	}
	if got != newest {
		t.Fatalf("expected newest artifact %s, got %s", newest, got)
	}
}

func TestBuildIssuePreviewCommentBodyIncludesSummaryAndFilename(t *testing.T) {
	body := buildIssuePreviewCommentBody(&agent.RunResult{
		Output: "fallback output",
		AppSession: &agentruntime.Session{
			LastMessage: "Validation passed and the feature is ready.",
		},
	}, "/tmp/preview.mp4")

	if !strings.Contains(body, "Automated reviewer preview from the done pass.") {
		t.Fatalf("expected intro in comment body, got %q", body)
	}
	if !strings.Contains(body, "Validation passed and the feature is ready.") {
		t.Fatalf("expected final message in comment body, got %q", body)
	}
	if !strings.Contains(body, "Preview file: `preview.mp4`") {
		t.Fatalf("expected filename in comment body, got %q", body)
	}
}

func TestDonePreviewPublicationDoesNotBlockRunCompletion(t *testing.T) {
	{
		orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
		enablePhaseWorkflow(t, manager, workspaceRoot)

		previewProvider := &previewCommentProvider{
			store:         store,
			createStarted: make(chan struct{}, 1),
			createRelease: make(chan struct{}),
		}
		orch.service.RegisterProvider(previewProvider)

		project, err := store.CreateProjectWithProvider("Preview Project", "", "", "", "stub", "", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
			t.Fatalf("UpdateProjectState: %v", err)
		}
		issue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
			ProjectID:        project.ID,
			ProviderKind:     "stub",
			ProviderIssueRef: "stub-1",
			Identifier:       "STUB-1",
			Title:            "Done success",
			State:            kanban.StateDone,
			WorkflowPhase:    kanban.WorkflowPhaseDone,
			ProviderShadow:   true,
		})
		if err != nil {
			t.Fatalf("UpsertProviderIssue: %v", err)
		}
		previewProvider.issue = *issue

		wsPath := filepath.Join(workspaceRoot, issue.Identifier)
		previewDir := filepath.Join(wsPath, ".maestro", "review-preview")
		if err := os.MkdirAll(previewDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(previewDir, "walkthrough.webm"), []byte("video-bytes"), 0o644); err != nil {
			t.Fatalf("WriteFile preview: %v", err)
		}
		if _, err := store.CreateWorkspace(issue.ID, wsPath); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		orch.runner = &phaseScriptRunner{
			store: store,
			handlers: map[kanban.WorkflowPhase]phaseRunHandler{
				kanban.WorkflowPhaseDone: func(issue *kanban.Issue) (*agent.RunResult, error) {
					return &agent.RunResult{Success: true}, nil
				},
			},
		}

		if err := orch.dispatch(context.Background()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-previewProvider.createStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for preview publication to start")
		}

		waitForNoRunning(t, orch, time.Second)
		waitForWorkspaceRemoval(t, store, issue.ID, time.Second)
		close(previewProvider.createRelease)
		waitForCondition(t, 5*time.Second, func() bool {
			comments, err := store.ListIssueComments(issue.ID)
			return err == nil && len(comments) > 0
		})
		return
	}

	orch, store, manager, workspaceRoot := setupTestOrchestrator(t, "cat")
	enablePhaseWorkflow(t, manager, workspaceRoot)

	type previewGraphqlRequest struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}

	releaseRequests := make(chan struct{})
	var requestCount int
	var requestMu sync.Mutex
	var server *inprocessserver.Server
	server, err := inprocessserver.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload":
			<-releaseRequests
			w.WriteHeader(http.StatusOK)
		default:
			var body previewGraphqlRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			requestMu.Lock()
			requestCount++
			requestMu.Unlock()
			<-releaseRequests
			switch {
			case strings.Contains(body.Query, "query MaestroLinearIssue"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"issues": map[string]interface{}{
							"nodes": []map[string]interface{}{
								{
									"id":               "stub-1",
									"identifier":       "STUB-1",
									"title":            "Done success",
									"state":            map[string]interface{}{"name": "done"},
									"labels":           map[string]interface{}{"nodes": []interface{}{}},
									"inverseRelations": map[string]interface{}{"nodes": []interface{}{}},
								},
							},
						},
					},
				})
			case strings.Contains(body.Query, "fileUpload"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"fileUpload": map[string]interface{}{
							"success": true,
							"uploadFile": map[string]interface{}{
								"uploadUrl": server.URL + "/upload",
								"assetUrl":  "https://stub.example/assets/walkthrough.webm",
							},
						},
					},
				})
			case strings.Contains(body.Query, "commentCreate"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"commentCreate": map[string]interface{}{"success": true},
					},
				})
			default:
				t.Fatalf("unexpected graphql query: %s", body.Query)
			}
		}
	}))
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
	defer server.Close()
	t.Setenv("STUB_PROVIDER_API_KEY", "test-token")

	project, err := store.CreateProjectWithProvider(
		"Stub Project",
		"",
		"",
		"",
		"stub",
		"proj-slug",
		map[string]interface{}{"project_slug": "proj-slug", "endpoint": server.URL},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState: %v", err)
	}
	issue, err := store.UpsertProviderIssue(project.ID, &kanban.Issue{
		ProjectID:        project.ID,
		ProviderKind:     "stub",
		ProviderIssueRef: "stub-1",
		Identifier:       "STUB-1",
		Title:            "Done success",
		State:            kanban.StateDone,
		WorkflowPhase:    kanban.WorkflowPhaseDone,
		ProviderShadow:   true,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue: %v", err)
	}
	if err := store.UpdateIssueStateAndPhase(issue.ID, kanban.StateDone, kanban.WorkflowPhaseDone); err != nil {
		t.Fatalf("UpdateIssueStateAndPhase: %v", err)
	}

	wsPath := filepath.Join(workspaceRoot, issue.Identifier)
	previewDir := filepath.Join(wsPath, ".maestro", "review-preview")
	if err := os.MkdirAll(previewDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(previewDir, "walkthrough.webm"), []byte("video-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile preview: %v", err)
	}
	if _, err := store.CreateWorkspace(issue.ID, wsPath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	orch.runner = &phaseScriptRunner{
		store: store,
		handlers: map[kanban.WorkflowPhase]phaseRunHandler{
			kanban.WorkflowPhaseDone: func(issue *kanban.Issue) (*agent.RunResult, error) {
				return &agent.RunResult{Success: true}, nil
			},
		},
	}

	if err := orch.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 20*time.Second, func() bool {
		requestMu.Lock()
		defer requestMu.Unlock()
		return requestCount >= 1
	})
	waitForNoRunning(t, orch, time.Second)
	waitForWorkspaceRemoval(t, store, issue.ID, time.Second)

	requestMu.Lock()
	gotRequests := requestCount
	requestMu.Unlock()
	if gotRequests < 1 {
		t.Fatalf("expected preview publication to start in the background before workspace cleanup completed, got %d requests", gotRequests)
	}

	close(releaseRequests)
	waitForCondition(t, 20*time.Second, func() bool {
		requestMu.Lock()
		defer requestMu.Unlock()
		return requestCount >= 3
	})
}
