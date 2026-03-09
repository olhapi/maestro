package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/olhapi/symphony-go/internal/agent"
	"github.com/olhapi/symphony-go/internal/appserver"
	"github.com/olhapi/symphony-go/internal/extensions"
	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/internal/observability"
	"github.com/olhapi/symphony-go/pkg/config"
)

const continuationRetryDelay = time.Second

type runningEntry struct {
	cancel    context.CancelFunc
	issue     kanban.Issue
	attempt   int
	startedAt time.Time
}

type retryEntry struct {
	Attempt   int       `json:"attempt"`
	DueAt     time.Time `json:"due_at"`
	Error     string    `json:"error,omitempty"`
	DelayType string    `json:"delay_type,omitempty"`
}

type projectRuntime struct {
	projectID    string
	repoPath     string
	workflowPath string
	workflow     *config.Manager
	runner       runnerExecutor
}

type runnerExecutor interface {
	RunAttempt(ctx context.Context, issue *kanban.Issue, attempt int) (*agent.RunResult, error)
	CleanupWorkspace(ctx context.Context, issue *kanban.Issue) error
}

type Orchestrator struct {
	store      *kanban.Store
	extensions *extensions.Registry

	workflows *config.Manager
	runner    runnerExecutor

	scopedRepoPath     string
	scopedWorkflowPath string

	runnerFactory func(*config.Manager) runnerExecutor

	runtimeMu       sync.Mutex
	projectRuntimes map[string]*projectRuntime

	mu             sync.RWMutex
	running        map[string]runningEntry
	claimed        map[string]struct{}
	retries        map[string]retryEntry
	startedAt      time.Time
	lastTickAt     time.Time
	totalRuns      int
	successfulRuns int
	failedRuns     int
	liveSessions   map[string]*appserver.Session
	eventSeq       int64
	events         []map[string]interface{}
	maxEvents      int
}

func New(store *kanban.Store, workflows *config.Manager) *Orchestrator {
	return NewWithExtensions(store, workflows, nil)
}

func NewWithExtensions(store *kanban.Store, workflows *config.Manager, registry *extensions.Registry) *Orchestrator {
	if registry == nil {
		registry = extensions.EmptyRegistry()
	}
	o := &Orchestrator{
		store:           store,
		extensions:      registry,
		projectRuntimes: make(map[string]*projectRuntime),
		running:         make(map[string]runningEntry),
		claimed:         make(map[string]struct{}),
		retries:         make(map[string]retryEntry),
		startedAt:       time.Now().UTC(),
		liveSessions:    make(map[string]*appserver.Session),
		maxEvents:       500,
	}
	o.workflows = workflows
	o.runnerFactory = func(manager *config.Manager) runnerExecutor {
		runner := agent.NewRunnerWithExtensions(manager, store, registry)
		runner.SetSessionObserver(o.updateLiveSession)
		return runner
	}
	o.runner = o.runnerFactory(workflows)
	return o
}

func NewSharedWithExtensions(store *kanban.Store, registry *extensions.Registry, scopedRepoPath, scopedWorkflowPath string) *Orchestrator {
	if registry == nil {
		registry = extensions.EmptyRegistry()
	}
	if strings.TrimSpace(scopedRepoPath) != "" {
		if abs, err := filepath.Abs(scopedRepoPath); err == nil {
			scopedRepoPath = abs
		}
	}
	if strings.TrimSpace(scopedWorkflowPath) != "" {
		if abs, err := filepath.Abs(scopedWorkflowPath); err == nil {
			scopedWorkflowPath = abs
		}
	}
	o := &Orchestrator{
		store:              store,
		extensions:         registry,
		scopedRepoPath:     scopedRepoPath,
		scopedWorkflowPath: scopedWorkflowPath,
		projectRuntimes:    make(map[string]*projectRuntime),
		running:            make(map[string]runningEntry),
		claimed:            make(map[string]struct{}),
		retries:            make(map[string]retryEntry),
		startedAt:          time.Now().UTC(),
		liveSessions:       make(map[string]*appserver.Session),
		maxEvents:          500,
	}
	o.runnerFactory = func(manager *config.Manager) runnerExecutor {
		runner := agent.NewRunnerWithExtensions(manager, store, registry)
		runner.SetSessionObserver(o.updateLiveSession)
		return runner
	}
	return o
}

func (o *Orchestrator) isSharedMode() bool {
	return o.workflows == nil
}

func (o *Orchestrator) runtimeForProject(project *kanban.Project) (*projectRuntime, error) {
	if !o.isSharedMode() {
		return &projectRuntime{
			projectID:    project.ID,
			workflow:     o.workflows,
			runner:       o.runner,
			repoPath:     project.RepoPath,
			workflowPath: project.WorkflowPath,
		}, nil
	}
	if project == nil {
		return nil, fmt.Errorf("project_not_found")
	}
	if strings.TrimSpace(project.RepoPath) == "" {
		return nil, fmt.Errorf("project_missing_repo_path")
	}
	if o.scopedRepoPath != "" && filepath.Clean(project.RepoPath) != filepath.Clean(o.scopedRepoPath) {
		return nil, fmt.Errorf("project_out_of_scope")
	}

	o.runtimeMu.Lock()
	defer o.runtimeMu.Unlock()

	if cached, ok := o.projectRuntimes[project.ID]; ok {
		if cached.repoPath == project.RepoPath && cached.workflowPath == project.WorkflowPath {
			return cached, nil
		}
	}

	workflowPath := project.WorkflowPath
	if strings.TrimSpace(workflowPath) == "" {
		workflowPath = filepath.Join(project.RepoPath, "WORKFLOW.md")
	}
	if o.scopedWorkflowPath != "" {
		workflowPath = o.scopedWorkflowPath
	}
	if _, created, err := config.EnsureWorkflowAtPath(workflowPath, config.InitOptions{}); err != nil {
		return nil, err
	} else if created {
		slog.Info("Created WORKFLOW.md with bootstrap defaults", "path", workflowPath, "project", project.Name)
	}

	manager, err := config.NewManagerForPath(workflowPath)
	if err != nil {
		return nil, err
	}
	runtime := &projectRuntime{
		projectID:    project.ID,
		repoPath:     project.RepoPath,
		workflowPath: workflowPath,
		workflow:     manager,
		runner:       o.runnerFactory(manager),
	}
	o.projectRuntimes[project.ID] = runtime
	return runtime, nil
}

func (o *Orchestrator) runtimeForIssue(issue *kanban.Issue) (*projectRuntime, *config.Workflow, error) {
	if !o.isSharedMode() {
		workflow, err := o.workflows.Current()
		if err != nil {
			return nil, nil, err
		}
		return &projectRuntime{projectID: issue.ProjectID, workflow: o.workflows, runner: o.runner}, workflow, nil
	}
	if issue == nil || strings.TrimSpace(issue.ProjectID) == "" {
		return nil, nil, fmt.Errorf("issue_missing_project")
	}
	project, err := o.store.GetProject(issue.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	runtime, err := o.runtimeForProject(project)
	if err != nil {
		return nil, nil, err
	}
	workflow, err := runtime.workflow.Current()
	if err != nil {
		return nil, nil, err
	}
	return runtime, workflow, nil
}

func (o *Orchestrator) runningCountForProject(projectID string) int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	count := 0
	for _, entry := range o.running {
		if entry.issue.ProjectID == projectID {
			count++
		}
	}
	return count
}

func (o *Orchestrator) Run(ctx context.Context) error {
	o.cleanupTerminalWorkspaces(ctx)
	for {
		wait := 30 * time.Second
		if !o.isSharedMode() {
			workflow, err := o.workflows.Current()
			if err != nil {
				return err
			}
			wait = time.Duration(workflow.Config.Polling.IntervalMs) * time.Millisecond
			if wait <= 0 {
				wait = 30 * time.Second
			}
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			o.stopAllRuns()
			return ctx.Err()
		case <-timer.C:
			if err := o.tick(ctx); err != nil {
				slog.Error("Tick failed", "error", err)
			}
		}
	}
}

func (o *Orchestrator) tick(ctx context.Context) error {
	o.mu.Lock()
	o.lastTickAt = time.Now().UTC()
	o.appendEventLocked("tick", map[string]interface{}{})
	o.mu.Unlock()

	o.reconcile(ctx)
	o.processRetries(ctx)
	return o.dispatch(ctx)
}

func (o *Orchestrator) reconcile(ctx context.Context) {
	o.mu.RLock()
	ids := make([]string, 0, len(o.running))
	for issueID := range o.running {
		ids = append(ids, issueID)
	}
	o.mu.RUnlock()

	for _, issueID := range ids {
		issue, err := o.store.GetIssue(issueID)
		if err != nil {
			slog.Warn("Skipping reconciliation for missing issue", "issue_id", issueID, "error", err)
			continue
		}
		runtime, workflow, err := o.runtimeForIssue(issue)
		if err != nil {
			slog.Warn("Stopping run because runtime resolution failed",
				issueLogAttrs(issue, -1, "reason", "runtime_resolution_failed", "error", err)...,
			)
			o.stopRun(issueID)
			o.releaseClaim(issueID)
			continue
		}
		if o.isTerminalState(workflow, string(issue.State)) {
			slog.Info("Stopping run because issue reached terminal state",
				issueLogAttrs(issue, -1, "reason", "terminal_state")...,
			)
			o.stopRun(issueID)
			if err := runtime.runner.CleanupWorkspace(ctx, issue); err != nil {
				slog.Warn("Failed to cleanup terminal workspace", "issue", issue.Identifier, "error", err)
			} else {
				slog.Info("Cleaned up terminal workspace",
					issueLogAttrs(issue, -1)...,
				)
			}
			o.releaseClaim(issueID)
			continue
		}
		if !o.isActiveState(workflow, string(issue.State)) || o.isBlocked(workflow, *issue) {
			reason := "inactive_state"
			if o.isBlocked(workflow, *issue) {
				reason = "blocked"
			}
			slog.Info("Stopping run during reconciliation",
				issueLogAttrs(issue, -1, "reason", reason)...,
			)
			o.stopRun(issueID)
			o.releaseClaim(issueID)
		}
	}
}

func (o *Orchestrator) dispatch(ctx context.Context) error {
	states := []string{"ready", "in_progress", "in_review"}
	if !o.isSharedMode() {
		workflow, err := o.workflows.Current()
		if err != nil {
			return err
		}
		states = workflow.Config.Tracker.ActiveStates
	}

	issues, err := o.store.ListIssues(map[string]interface{}{
		"states": states,
	})
	if err != nil {
		return err
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Priority != issues[j].Priority {
			return issues[i].Priority < issues[j].Priority
		}
		if !issues[i].CreatedAt.Equal(issues[j].CreatedAt) {
			return issues[i].CreatedAt.Before(issues[j].CreatedAt)
		}
		return issues[i].Identifier < issues[j].Identifier
	})

	for _, issue := range issues {
		runtime, workflow, err := o.runtimeForIssue(&issue)
		if err != nil {
			slog.Warn("Skipping issue dispatch because runtime resolution failed",
				issueLogAttrs(&issue, 0, "error", err)...,
			)
			continue
		}
		capacity := workflow.Config.Agent.MaxConcurrentAgents
		if capacity <= 0 || o.runningCountForProject(issue.ProjectID) >= capacity {
			continue
		}
		if o.isBlocked(workflow, issue) {
			continue
		}
		if !o.tryClaim(issue.ID) {
			slog.Debug("Issue claim rejected because it is already claimed",
				issueLogAttrs(&issue, 0)...,
			)
			continue
		}
		slog.Info("Issue claim accepted", issueLogAttrs(&issue, 0)...)
		if !o.isActiveState(workflow, string(issue.State)) {
			slog.Info("Releasing issue claim because state is no longer active",
				issueLogAttrs(&issue, 0)...,
			)
			o.releaseClaim(issue.ID)
			continue
		}
		o.startRun(ctx, workflow, runtime.runner, &issue, 0)
	}
	return nil
}

func (o *Orchestrator) processRetries(ctx context.Context) {
	now := time.Now()

	o.mu.RLock()
	dueIDs := make([]string, 0, len(o.retries))
	for issueID, entry := range o.retries {
		if !entry.DueAt.After(now) {
			dueIDs = append(dueIDs, issueID)
		}
	}
	o.mu.RUnlock()
	sort.Strings(dueIDs)

	for _, issueID := range dueIDs {
		o.mu.RLock()
		entry, ok := o.retries[issueID]
		_, running := o.running[issueID]
		o.mu.RUnlock()
		if !ok || running {
			continue
		}

		issue, err := o.store.GetIssue(issueID)
		if err != nil {
			slog.Warn("Dropping retry because issue lookup failed",
				"issue_id", issueID,
				"attempt", entry.Attempt,
				"error", err,
			)
			o.releaseClaim(issueID)
			o.mu.Lock()
			delete(o.retries, issueID)
			o.mu.Unlock()
			continue
		}
		runtime, workflow, err := o.runtimeForIssue(issue)
		if err != nil {
			slog.Warn("Dropping retry because runtime resolution failed",
				issueLogAttrs(issue, entry.Attempt, "error", err)...,
			)
			o.releaseClaim(issueID)
			o.mu.Lock()
			delete(o.retries, issueID)
			o.mu.Unlock()
			continue
		}
		if o.isTerminalState(workflow, string(issue.State)) {
			slog.Info("Dropping retry because issue reached terminal state",
				issueLogAttrs(issue, entry.Attempt)...,
			)
			if err := runtime.runner.CleanupWorkspace(ctx, issue); err != nil {
				slog.Warn("Failed to cleanup terminal workspace", "issue", issue.Identifier, "error", err)
			} else {
				slog.Info("Cleaned up terminal workspace",
					issueLogAttrs(issue, entry.Attempt)...,
				)
			}
			o.releaseClaim(issueID)
			o.mu.Lock()
			delete(o.retries, issueID)
			o.mu.Unlock()
			continue
		}
		if !o.isActiveState(workflow, string(issue.State)) || o.isBlocked(workflow, *issue) {
			reason := "inactive_state"
			if o.isBlocked(workflow, *issue) {
				reason = "blocked"
			}
			slog.Info("Dropping retry because issue is not dispatchable",
				issueLogAttrs(issue, entry.Attempt, "reason", reason)...,
			)
			o.releaseClaim(issueID)
			o.mu.Lock()
			delete(o.retries, issueID)
			o.mu.Unlock()
			continue
		}

		capacity := workflow.Config.Agent.MaxConcurrentAgents
		if capacity > 0 && o.runningCountForProject(issue.ProjectID) >= capacity {
			continue
		}
		slog.Info("Retry is due; starting issue run",
			issueLogAttrs(issue, entry.Attempt, "delay_type", entry.DelayType)...,
		)
		o.startRun(ctx, workflow, runtime.runner, issue, entry.Attempt)
	}
}

func (o *Orchestrator) startRun(ctx context.Context, workflow *config.Workflow, runner runnerExecutor, issue *kanban.Issue, attempt int) {
	runCtx, cancel := context.WithCancel(ctx)
	entry := runningEntry{
		cancel:    cancel,
		issue:     *issue,
		attempt:   attempt,
		startedAt: time.Now().UTC(),
	}
	o.mu.Lock()
	delete(o.liveSessions, issue.ID)
	o.running[issue.ID] = entry
	delete(o.retries, issue.ID)
	o.appendEventLocked("run_started", map[string]interface{}{
		"issue_id":    issue.ID,
		"identifier":  issue.Identifier,
		"title":       issue.Title,
		"attempt":     attempt,
		"issue_state": string(issue.State),
	})
	o.mu.Unlock()
	slog.Info("Agent run started", issueLogAttrs(issue, attempt)...)

	go func() {
		result, err := runner.RunAttempt(runCtx, issue, attempt)
		o.finishRun(workflow, issue, attempt, result, err)
	}()
}

func (o *Orchestrator) finishRun(workflow *config.Workflow, issue *kanban.Issue, attempt int, result *agent.RunResult, err error) {
	o.mu.Lock()
	delete(o.running, issue.ID)
	o.totalRuns++
	delete(o.liveSessions, issue.ID)
	o.mu.Unlock()

	switch {
	case err != nil:
		o.mu.Lock()
		o.failedRuns++
		fields := map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"attempt":    attempt,
			"error":      err.Error(),
		}
		attachResultMetrics(fields, result)
		o.appendEventLocked("run_failed", fields)
		o.scheduleRetryLocked(issue, nextAttempt(attempt), "failure", err.Error(), workflow.Config.Agent.MaxRetryBackoffMs)
		o.mu.Unlock()
		slog.Warn("Agent run failed",
			issueLogAttrs(issue, attempt, "error", err, "next_attempt", nextAttempt(attempt))...,
		)
	case result != nil && !result.Success:
		errText := "unsuccessful"
		if result.Error != nil {
			errText = result.Error.Error()
		}
		o.mu.Lock()
		o.failedRuns++
		fields := map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"attempt":    attempt,
			"error":      errText,
		}
		attachResultMetrics(fields, result)
		o.appendEventLocked("run_unsuccessful", fields)
		o.scheduleRetryLocked(issue, nextAttempt(attempt), "failure", errText, workflow.Config.Agent.MaxRetryBackoffMs)
		o.mu.Unlock()
		slog.Warn("Agent run completed unsuccessfully",
			issueLogAttrs(issue, attempt, "error", errText, "next_attempt", nextAttempt(attempt))...,
		)
	default:
		o.mu.Lock()
		o.successfulRuns++
		next := nextAttempt(attempt)
		fields := map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"attempt":    attempt,
			"next_retry": next,
		}
		attachResultMetrics(fields, result)
		o.appendEventLocked("run_completed", fields)
		o.scheduleRetryLocked(issue, next, "continuation", "", workflow.Config.Agent.MaxRetryBackoffMs)
		o.mu.Unlock()
		slog.Info("Agent run completed",
			issueLogAttrs(issue, attempt, "next_attempt", next)...,
		)
	}
}

func nextAttempt(attempt int) int {
	if attempt > 0 {
		return attempt + 1
	}
	return 1
}

func (o *Orchestrator) scheduleRetryLocked(issue *kanban.Issue, attempt int, delayType, errText string, maxBackoffMs int) {
	delay := continuationRetryDelay
	if delayType != "continuation" {
		delay = failureRetryDelay(attempt, maxBackoffMs)
	}
	o.retries[issue.ID] = retryEntry{
		Attempt:   attempt,
		DueAt:     time.Now().Add(delay),
		Error:     errText,
		DelayType: delayType,
	}
	o.claimed[issue.ID] = struct{}{}
	o.appendEventLocked("retry_scheduled", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"attempt":    attempt,
		"due_at":     time.Now().Add(delay).UTC().Format(time.RFC3339),
		"delay_ms":   delay.Milliseconds(),
		"delay_type": delayType,
		"error":      errText,
	})
	slog.Info("Retry scheduled",
		issueLogAttrs(issue, attempt,
			"delay_ms", delay.Milliseconds(),
			"delay_type", delayType,
			"error", errText,
		)...,
	)
}

func failureRetryDelay(attempt, maxBackoffMs int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	delay := 10 * time.Second
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	maxDelay := time.Duration(maxBackoffMs) * time.Millisecond
	if maxDelay <= 0 {
		maxDelay = 5 * time.Minute
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func (o *Orchestrator) cleanupTerminalWorkspaces(ctx context.Context) {
	states := []string{"done", "cancelled"}
	if !o.isSharedMode() {
		workflow, err := o.workflows.Current()
		if err != nil {
			return
		}
		states = workflow.Config.Tracker.TerminalStates
	}
	issues, err := o.store.ListIssues(map[string]interface{}{"states": states})
	if err != nil {
		slog.Warn("Skipping startup terminal workspace cleanup", "error", err)
		return
	}
	for i := range issues {
		runtime, _, err := o.runtimeForIssue(&issues[i])
		if err != nil {
			slog.Warn("Skipping startup terminal workspace cleanup because runtime resolution failed",
				issueLogAttrs(&issues[i], -1, "error", err)...,
			)
			continue
		}
		if err := runtime.runner.CleanupWorkspace(ctx, &issues[i]); err != nil {
			slog.Warn("Failed to cleanup terminal workspace", "issue", issues[i].Identifier, "error", err)
		} else {
			slog.Info("Cleaned up terminal workspace",
				issueLogAttrs(&issues[i], -1)...,
			)
		}
	}
}

func (o *Orchestrator) stopRun(issueID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if entry, ok := o.running[issueID]; ok {
		entry.cancel()
		delete(o.running, issueID)
		o.appendEventLocked("run_stopped", map[string]interface{}{"issue_id": issueID})
		slog.Info("Agent run stopped", issueLogAttrs(&entry.issue, entry.attempt)...)
	}
}

func (o *Orchestrator) stopAllRuns() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for issueID, entry := range o.running {
		entry.cancel()
		delete(o.running, issueID)
	}
}

func (o *Orchestrator) tryClaim(issueID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.claimed[issueID]; ok {
		return false
	}
	o.claimed[issueID] = struct{}{}
	return true
}

func (o *Orchestrator) releaseClaim(issueID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.claimed, issueID)
	delete(o.retries, issueID)
	o.appendEventLocked("claim_released", map[string]interface{}{"issue_id": issueID})
}

func (o *Orchestrator) isBlocked(workflow *config.Workflow, issue kanban.Issue) bool {
	for _, blocker := range issue.BlockedBy {
		blockerIssue, err := o.store.GetIssueByIdentifier(blocker)
		if err != nil {
			continue
		}
		if !o.isTerminalState(workflow, string(blockerIssue.State)) {
			return true
		}
	}
	return false
}

func (o *Orchestrator) isActiveState(workflow *config.Workflow, state string) bool {
	normalized := normalizeState(state)
	for _, candidate := range workflow.Config.Tracker.ActiveStates {
		if normalizeState(candidate) == normalized {
			return true
		}
	}
	return false
}

func (o *Orchestrator) isTerminalState(workflow *config.Workflow, state string) bool {
	normalized := normalizeState(state)
	for _, candidate := range workflow.Config.Tracker.TerminalStates {
		if normalizeState(candidate) == normalized {
			return true
		}
	}
	return false
}

func normalizeState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}

func (o *Orchestrator) appendEventLocked(kind string, fields map[string]interface{}) {
	o.eventSeq++
	event := map[string]interface{}{
		"seq":  o.eventSeq,
		"kind": kind,
		"ts":   time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range fields {
		event[k] = v
	}
	o.events = append(o.events, event)
	if len(o.events) > o.maxEvents {
		o.events = o.events[len(o.events)-o.maxEvents:]
	}
	if err := o.store.AppendRuntimeEvent(kind, event); err != nil {
		slog.Warn("Failed to persist runtime event", "kind", kind, "error", err)
	}
	observability.BroadcastUpdate()
}

func (o *Orchestrator) Events(since int64, limit int) map[string]interface{} {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	filtered := make([]map[string]interface{}, 0, limit)
	for _, event := range o.events {
		seq, _ := event["seq"].(int64)
		if seq <= since {
			continue
		}
		cp := make(map[string]interface{}, len(event))
		for k, v := range event {
			cp[k] = v
		}
		filtered = append(filtered, cp)
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	lastSeq := since
	if len(filtered) > 0 {
		if seq, ok := filtered[len(filtered)-1]["seq"].(int64); ok {
			lastSeq = seq
		}
	}
	return map[string]interface{}{"since": since, "last_seq": lastSeq, "events": filtered}
}

func (o *Orchestrator) Status() map[string]interface{} {
	var workflow *config.Workflow
	if !o.isSharedMode() {
		workflow, _ = o.workflows.Current()
	}
	o.mu.RLock()
	defer o.mu.RUnlock()

	activeIDs := make([]string, 0, len(o.running))
	for id := range o.running {
		activeIDs = append(activeIDs, id)
	}
	sort.Strings(activeIDs)

	retryQueue := make(map[string]interface{}, len(o.retries))
	for id, entry := range o.retries {
		retryQueue[id] = map[string]interface{}{
			"attempt":    entry.Attempt,
			"due_at":     entry.DueAt.UTC().Format(time.RFC3339),
			"error":      entry.Error,
			"delay_type": entry.DelayType,
		}
	}

	uptimeSec := int(time.Since(o.startedAt).Seconds())
	lastTick := ""
	if !o.lastTickAt.IsZero() {
		lastTick = o.lastTickAt.Format(time.RFC3339)
	}

	out := map[string]interface{}{
		"started_at":        o.startedAt.Format(time.RFC3339),
		"uptime_seconds":    uptimeSec,
		"last_tick_at":      lastTick,
		"active_runs":       len(o.running),
		"active_issues":     activeIDs,
		"retry_queue_count": len(o.retries),
		"retry_queue":       retryQueue,
		"events_count":      len(o.events),
		"last_event_seq":    o.eventSeq,
		"run_metrics": map[string]int{
			"total":      o.totalRuns,
			"successful": o.successfulRuns,
			"failed":     o.failedRuns,
		},
		"live_sessions": o.copyLiveSessionsLocked(),
	}
	if workflow != nil {
		out["max_concurrent"] = workflow.Config.Agent.MaxConcurrentAgents
		out["poll_interval_ms"] = workflow.Config.Polling.IntervalMs
		out["active_states"] = workflow.Config.Tracker.ActiveStates
		out["terminal_states"] = workflow.Config.Tracker.TerminalStates
		out["workflow_path"] = workflow.Path
	} else if o.isSharedMode() {
		out["mode"] = "shared"
		if o.scopedRepoPath != "" {
			out["scoped_repo_path"] = o.scopedRepoPath
		}
	}
	if o.workflows != nil {
		if err := o.workflows.LastError(); err != nil {
			out["workflow_error"] = err.Error()
		}
	}
	return out
}

func (o *Orchestrator) Snapshot() observability.Snapshot {
	var workflow *config.Workflow
	if !o.isSharedMode() {
		workflow, _ = o.workflows.Current()
	}
	o.mu.RLock()
	defer o.mu.RUnlock()

	snapshot := observability.Snapshot{
		GeneratedAt: time.Now().UTC(),
		Running:     make([]observability.RunningEntry, 0, len(o.running)),
		Retrying:    make([]observability.RetryEntry, 0, len(o.retries)),
		RateLimits:  nil,
	}
	if workflow != nil {
		snapshot.WorkspaceRoot = workflow.Config.Workspace.Root
	}

	for issueID, entry := range o.running {
		session := o.liveSessions[issueID]
		running := observability.RunningEntry{
			IssueID:    issueID,
			Identifier: entry.issue.Identifier,
			State:      string(entry.issue.State),
			StartedAt:  entry.startedAt,
		}
		if session != nil {
			running.SessionID = session.SessionID
			running.CodexAppServerPID = session.AppServerPID
			running.TurnCount = session.TurnsStarted
			running.LastEvent = session.LastEvent
			running.LastMessage = session.LastMessage
			if !session.LastTimestamp.IsZero() {
				ts := session.LastTimestamp
				running.LastEventAt = &ts
			}
			running.Tokens = observability.TokenTotals{
				InputTokens:    session.InputTokens,
				OutputTokens:   session.OutputTokens,
				TotalTokens:    session.TotalTokens,
				SecondsRunning: int(time.Since(entry.startedAt).Seconds()),
			}
		} else {
			running.Tokens.SecondsRunning = int(time.Since(entry.startedAt).Seconds())
		}
		snapshot.CodexTotals.InputTokens += running.Tokens.InputTokens
		snapshot.CodexTotals.OutputTokens += running.Tokens.OutputTokens
		snapshot.CodexTotals.TotalTokens += running.Tokens.TotalTokens
		snapshot.CodexTotals.SecondsRunning += running.Tokens.SecondsRunning
		snapshot.Running = append(snapshot.Running, running)
	}

	for issueID, entry := range o.retries {
		identifier := issueID
		if running, ok := o.running[issueID]; ok {
			identifier = running.issue.Identifier
		} else if issue, err := o.store.GetIssue(issueID); err == nil && issue != nil {
			identifier = issue.Identifier
		}
		retry := observability.RetryEntry{
			IssueID:    issueID,
			Identifier: identifier,
			Attempt:    entry.Attempt,
			DueAt:      entry.DueAt,
			DueInMs:    time.Until(entry.DueAt).Milliseconds(),
			Error:      entry.Error,
			DelayType:  entry.DelayType,
		}
		snapshot.Retrying = append(snapshot.Retrying, retry)
	}

	sort.Slice(snapshot.Running, func(i, j int) bool {
		return snapshot.Running[i].Identifier < snapshot.Running[j].Identifier
	})
	sort.Slice(snapshot.Retrying, func(i, j int) bool {
		return snapshot.Retrying[i].Identifier < snapshot.Retrying[j].Identifier
	})
	return snapshot
}

func (o *Orchestrator) LiveSessions() map[string]interface{} {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make(map[string]interface{}, len(o.running))
	for issueID := range o.running {
		entry, ok := o.running[issueID]
		if !ok {
			continue
		}
		session, ok := o.liveSessions[issueID]
		if !ok || session == nil {
			continue
		}
		cp := cloneSessionWithIssue(session, issueID, entry.issue.Identifier)
		out[entry.issue.Identifier] = cp
	}
	return map[string]interface{}{"sessions": out}
}

func (o *Orchestrator) updateLiveSession(issueID string, session *appserver.Session) {
	if session == nil {
		return
	}
	cp := *session
	cp.History = append([]appserver.Event(nil), session.History...)

	o.mu.Lock()
	defer o.mu.Unlock()
	entry, ok := o.running[issueID]
	if !ok {
		return
	}
	cp.IssueID = issueID
	cp.IssueIdentifier = entry.issue.Identifier
	o.liveSessions[issueID] = &cp
}

func (o *Orchestrator) copyLiveSessionsLocked() map[string]*appserver.Session {
	out := make(map[string]*appserver.Session, len(o.running))
	for issueID := range o.running {
		entry, ok := o.running[issueID]
		if !ok {
			continue
		}
		session, ok := o.liveSessions[issueID]
		if !ok || session == nil {
			continue
		}
		cp := cloneSessionWithIssue(session, issueID, entry.issue.Identifier)
		out[entry.issue.Identifier] = &cp
	}
	return out
}

func (o *Orchestrator) RequestRefresh() map[string]interface{} {
	o.mu.Lock()
	o.appendEventLocked("refresh_requested", map[string]interface{}{})
	o.mu.Unlock()
	return map[string]interface{}{
		"requested_at": time.Now().UTC().Format(time.RFC3339),
		"status":       "accepted",
	}
}

func (o *Orchestrator) RetryIssueNow(identifier string) map[string]interface{} {
	o.mu.Lock()
	defer o.mu.Unlock()

	issue, err := o.store.GetIssueByIdentifier(identifier)
	if err != nil {
		return map[string]interface{}{
			"status": "not_found",
			"issue":  identifier,
		}
	}

	if entry, ok := o.retries[issue.ID]; ok {
		entry.DueAt = time.Now().UTC()
		o.retries[issue.ID] = entry
		o.appendEventLocked("manual_retry_requested", map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
		})
		return map[string]interface{}{
			"status":       "queued_now",
			"issue":        identifier,
			"retry_due_at": entry.DueAt.Format(time.RFC3339),
		}
	}

	if issue.State == kanban.StateDone || issue.State == kanban.StateCancelled {
		if err := o.store.UpdateIssueState(issue.ID, kanban.StateReady); err != nil {
			return map[string]interface{}{
				"status": "error",
				"error":  err.Error(),
				"issue":  identifier,
			}
		}
	}

	o.appendEventLocked("manual_retry_requested", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
	})
	return map[string]interface{}{
		"status": "refresh_requested",
		"issue":  identifier,
	}
}

func attachResultMetrics(fields map[string]interface{}, result *agent.RunResult) {
	if result == nil || result.AppSession == nil {
		return
	}
	fields["input_tokens"] = result.AppSession.InputTokens
	fields["output_tokens"] = result.AppSession.OutputTokens
	fields["total_tokens"] = result.AppSession.TotalTokens
}

func issueLogAttrs(issue *kanban.Issue, attempt int, extra ...interface{}) []interface{} {
	attrs := make([]interface{}, 0, 10+len(extra))
	if issue != nil {
		attrs = append(attrs,
			"issue_id", issue.ID,
			"issue_identifier", issue.Identifier,
			"state", string(issue.State),
		)
		if issue.ProjectID != "" {
			attrs = append(attrs, "project_id", issue.ProjectID)
		}
	}
	if attempt >= 0 {
		attrs = append(attrs, "attempt", attempt)
	}
	attrs = append(attrs, extra...)
	return attrs
}

func cloneSessionWithIssue(session *appserver.Session, issueID, identifier string) appserver.Session {
	cp := *session
	cp.History = append([]appserver.Event(nil), session.History...)
	cp.IssueID = issueID
	cp.IssueIdentifier = identifier
	return cp
}
