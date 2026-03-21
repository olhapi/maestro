package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/olhapi/maestro/internal/agent"
	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/internal/providers"
	"github.com/olhapi/maestro/pkg/config"
)

const (
	continuationRetryDelay       = time.Second
	interruptedRunPauseThreshold = 3
	liveSessionPersistInterval   = 2 * time.Second
	liveTokenSpendFlushInterval  = 5 * time.Second
	automaticRetryHistoryLimit   = 200
	runtimeMaintenanceInterval   = 15 * time.Minute
	providerSyncMinInterval      = time.Second
	gracefulShutdownStopReason   = "graceful_shutdown"
	planApprovalStopReason       = "plan_approval_pending"
	gracefulShutdownWaitTimeout  = 5 * time.Second
	reviewPreviewPublishTimeout  = 15 * time.Second
	reviewPreviewDir             = ".maestro/review-preview"
)

type runningEntry struct {
	cancel    context.CancelFunc
	issue     kanban.Issue
	phase     kanban.WorkflowPhase
	attempt   int
	startedAt time.Time
	done      chan struct{}
}

type pendingInteractionEntry struct {
	interaction appserver.PendingInteraction
	respond     appserver.InteractionResponder
}

type retryEntry struct {
	Attempt        int       `json:"attempt"`
	Phase          string    `json:"phase,omitempty"`
	DueAt          time.Time `json:"due_at"`
	Error          string    `json:"error,omitempty"`
	DelayType      string    `json:"delay_type,omitempty"`
	ResumeThreadID string    `json:"-"`
}

type pausedEntry struct {
	IssueState          string    `json:"-"`
	Attempt             int       `json:"attempt"`
	Phase               string    `json:"phase,omitempty"`
	PausedAt            time.Time `json:"paused_at"`
	Error               string    `json:"error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	PauseThreshold      int       `json:"pause_threshold"`
}

type sessionPersistenceState struct {
	LastPersistedAt time.Time
	SessionID       string
	LastEvent       string
	LastTimestamp   time.Time
	TerminalReason  string
	Terminal        bool
}

type issueTokenSpendState struct {
	LastSeenTotals   map[string]int
	LastUnnamedTotal int
	PendingDelta     int
	PendingSince     time.Time
	LastFlushedAt    time.Time
}

type orchestratorTestHooks struct {
	beforeFinishRunRelease           func(issueID string)
	cleanupLingeringAppServerProcess func(pid int) error
}

const scopedRuntimeKey = "__scoped__"

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
	service    *providers.Service
	extensions *extensions.Registry

	workflows *config.Manager
	runner    runnerExecutor

	scopedRepoPath     string
	scopedWorkflowPath string

	runnerFactory func(*config.Manager) runnerExecutor

	runtimeMu       sync.Mutex
	projectRuntimes map[string]*projectRuntime

	mu                      sync.RWMutex
	running                 map[string]runningEntry
	claimed                 map[string]struct{}
	retries                 map[string]retryEntry
	paused                  map[string]pausedEntry
	pendingInteractions     map[string]pendingInteractionEntry
	pendingInteractionOrder []string
	startedAt               time.Time
	lastTickAt              time.Time
	totalRuns               int
	successfulRuns          int
	failedRuns              int
	liveSessions            map[string]*appserver.Session
	retiredAppServerMu      sync.RWMutex
	retiredAppServerIssues  map[string]struct{}
	sessionWriteMu          sync.Mutex
	sessionWrites           map[string]sessionPersistenceState
	tokenSpendMu            sync.Mutex
	tokenSpends             map[string]issueTokenSpendState
	lastProviderSyncAt      time.Time
	lastMaintenanceAt       time.Time
	lastCheckpointAt        time.Time
	lastCheckpointResult    string
	eventSeq                int64
	events                  []map[string]interface{}
	maxEvents               int
	runWG                   sync.WaitGroup
	testHooks               orchestratorTestHooks
}

func New(store *kanban.Store, workflows *config.Manager) *Orchestrator {
	return NewWithExtensions(store, workflows, nil)
}

func NewWithExtensions(store *kanban.Store, workflows *config.Manager, registry *extensions.Registry) *Orchestrator {
	if registry == nil {
		registry = extensions.EmptyRegistry()
	}
	o := &Orchestrator{
		store:                  store,
		service:                providers.NewService(store),
		extensions:             registry,
		projectRuntimes:        make(map[string]*projectRuntime),
		running:                make(map[string]runningEntry),
		claimed:                make(map[string]struct{}),
		retries:                make(map[string]retryEntry),
		paused:                 make(map[string]pausedEntry),
		pendingInteractions:    make(map[string]pendingInteractionEntry),
		startedAt:              time.Now().UTC(),
		liveSessions:           make(map[string]*appserver.Session),
		retiredAppServerIssues: make(map[string]struct{}),
		sessionWrites:          make(map[string]sessionPersistenceState),
		tokenSpends:            make(map[string]issueTokenSpendState),
		maxEvents:              500,
	}
	o.workflows = workflows
	o.runnerFactory = func(manager *config.Manager) runnerExecutor {
		runner := agent.NewRunnerWithExtensions(manager, store, registry)
		runner.SetSessionObserver(o.updateLiveSession)
		runner.SetActivityObserver(o.updateIssueActivity)
		runner.SetInteractionObserver(o.registerPendingInteraction)
		runner.SetInteractionDoneObserver(o.clearPendingInteraction)
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
		store:                  store,
		service:                providers.NewService(store),
		extensions:             registry,
		scopedRepoPath:         scopedRepoPath,
		scopedWorkflowPath:     scopedWorkflowPath,
		projectRuntimes:        make(map[string]*projectRuntime),
		running:                make(map[string]runningEntry),
		claimed:                make(map[string]struct{}),
		retries:                make(map[string]retryEntry),
		paused:                 make(map[string]pausedEntry),
		pendingInteractions:    make(map[string]pendingInteractionEntry),
		startedAt:              time.Now().UTC(),
		liveSessions:           make(map[string]*appserver.Session),
		retiredAppServerIssues: make(map[string]struct{}),
		sessionWrites:          make(map[string]sessionPersistenceState),
		tokenSpends:            make(map[string]issueTokenSpendState),
		maxEvents:              500,
	}
	o.runnerFactory = func(manager *config.Manager) runnerExecutor {
		runner := agent.NewRunnerWithExtensions(manager, store, registry)
		runner.SetSessionObserver(o.updateLiveSession)
		runner.SetActivityObserver(o.updateIssueActivity)
		runner.SetInteractionObserver(o.registerPendingInteraction)
		runner.SetInteractionDoneObserver(o.clearPendingInteraction)
		return runner
	}
	return o
}

func (o *Orchestrator) isSharedMode() bool {
	return o.workflows == nil
}

func (o *Orchestrator) syncProviderIssues(ctx context.Context) {
	repoPath := o.scopedRepoPath
	if !o.isSharedMode() && o.workflows != nil {
		repoPath = filepath.Dir(o.workflows.Path())
	}

	o.mu.RLock()
	lastSyncAt := o.lastProviderSyncAt
	o.mu.RUnlock()
	if !lastSyncAt.IsZero() && time.Since(lastSyncAt) < providerSyncMinInterval {
		return
	}

	if err := o.service.SyncForRepoPath(ctx, repoPath); err != nil {
		slog.Warn("Provider issue sync failed", "repo_path", repoPath, "error", err)
	}

	o.mu.Lock()
	o.lastProviderSyncAt = time.Now().UTC()
	o.mu.Unlock()
}

func (o *Orchestrator) refreshIssue(ctx context.Context, issueID string) (*kanban.Issue, error) {
	issue, err := o.service.RefreshIssueByID(ctx, issueID)
	if err == nil {
		return issue, nil
	}
	return o.store.GetIssue(issueID)
}

func (o *Orchestrator) recurrenceScopeRepoPath() string {
	if o.isSharedMode() {
		return o.scopedRepoPath
	}
	if o.workflows == nil {
		return ""
	}
	return filepath.Dir(o.workflows.Path())
}

func (o *Orchestrator) nextWakeDelay(base time.Duration) time.Duration {
	nextDue, err := o.store.NextRecurringDueAt(o.recurrenceScopeRepoPath())
	if err != nil {
		slog.Warn("Failed to compute next recurring due time", "error", err)
		return base
	}
	if nextDue == nil {
		return base
	}
	dueIn := time.Until(nextDue.UTC())
	if dueIn < 0 {
		dueIn = 0
	}
	if dueIn < base {
		return dueIn
	}
	return base
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

func (o *Orchestrator) runtimeForScopedIssue() (*projectRuntime, error) {
	if !o.isSharedMode() || strings.TrimSpace(o.scopedRepoPath) == "" {
		return nil, fmt.Errorf("issue_missing_project")
	}

	o.runtimeMu.Lock()
	defer o.runtimeMu.Unlock()

	if cached, ok := o.projectRuntimes[scopedRuntimeKey]; ok {
		if cached.repoPath == o.scopedRepoPath && cached.workflowPath == o.scopedWorkflowPath {
			return cached, nil
		}
	}

	workflowPath := o.scopedWorkflowPath
	if strings.TrimSpace(workflowPath) == "" {
		workflowPath = filepath.Join(o.scopedRepoPath, "WORKFLOW.md")
	}
	if _, created, err := config.EnsureWorkflowAtPath(workflowPath, config.InitOptions{}); err != nil {
		return nil, err
	} else if created {
		slog.Info("Created WORKFLOW.md with bootstrap defaults", "path", workflowPath, "repo_path", o.scopedRepoPath)
	}

	manager, err := config.NewManagerForPath(workflowPath)
	if err != nil {
		return nil, err
	}
	runtime := &projectRuntime{
		projectID:    "",
		repoPath:     o.scopedRepoPath,
		workflowPath: workflowPath,
		workflow:     manager,
		runner:       o.runnerFactory(manager),
	}
	o.projectRuntimes[scopedRuntimeKey] = runtime
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
	if issue == nil {
		return nil, nil, fmt.Errorf("issue_missing_project")
	}
	if strings.TrimSpace(issue.ProjectID) == "" {
		runtime, err := o.runtimeForScopedIssue()
		if err != nil {
			return nil, nil, err
		}
		workflow, err := runtime.workflow.Current()
		if err != nil {
			return nil, nil, err
		}
		return runtime, workflow, nil
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
		wait = o.nextWakeDelay(wait)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			o.stopAllRunsGracefully()
			if !o.waitForActiveRuns(gracefulShutdownWaitTimeout) {
				slog.Warn("Timed out waiting for active runs to stop during shutdown", "timeout", gracefulShutdownWaitTimeout)
			}
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

	o.syncProviderIssues(ctx)
	o.reconcileWithProviderSync(ctx, false)
	o.processRetries(ctx)
	o.processPendingRecurringReruns(ctx)
	o.processDueRecurringIssues(ctx)
	o.runMaintenanceIfDue()
	return o.dispatchWithProviderSync(ctx, false)
}

func (o *Orchestrator) runMaintenanceIfDue() {
	o.mu.RLock()
	lastRun := o.lastMaintenanceAt
	protectedIssueIDs := o.maintenanceProtectedIssueIDsLocked()
	o.mu.RUnlock()

	if !lastRun.IsZero() && time.Since(lastRun) < runtimeMaintenanceInterval {
		return
	}

	result, err := o.store.RunMaintenance(protectedIssueIDs)
	if err != nil {
		slog.Warn("Runtime maintenance failed", "error", err)
		return
	}

	o.mu.Lock()
	o.lastMaintenanceAt = result.StartedAt
	o.lastCheckpointAt = result.CheckpointAt
	o.lastCheckpointResult = result.CheckpointResult
	o.mu.Unlock()
}

func (o *Orchestrator) maintenanceProtectedIssueIDsLocked() []string {
	protectedIssueSet := make(map[string]struct{}, len(o.running)+len(o.retries)+len(o.paused))
	for issueID := range o.running {
		protectedIssueSet[issueID] = struct{}{}
	}
	for issueID := range o.retries {
		protectedIssueSet[issueID] = struct{}{}
	}
	for issueID := range o.paused {
		protectedIssueSet[issueID] = struct{}{}
	}
	protectedIssueIDs := make([]string, 0, len(protectedIssueSet))
	for issueID := range protectedIssueSet {
		protectedIssueIDs = append(protectedIssueIDs, issueID)
	}
	sort.Strings(protectedIssueIDs)
	return protectedIssueIDs
}

func (o *Orchestrator) reconcile(ctx context.Context) {
	o.reconcileWithProviderSync(ctx, true)
}

func (o *Orchestrator) reconcileWithProviderSync(ctx context.Context, syncProvider bool) {
	o.reconcilePausedRuns(ctx)

	o.mu.RLock()
	ids := make([]string, 0, len(o.running))
	for issueID := range o.running {
		ids = append(ids, issueID)
	}
	o.mu.RUnlock()

	for _, issueID := range ids {
		o.mu.RLock()
		entry, hasEntry := o.running[issueID]
		o.mu.RUnlock()

		issue, err := o.refreshIssue(ctx, issueID)
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
		if hasEntry && o.shouldAllowRunningTerminalTransition(workflow, issue, entry.phase) {
			continue
		}
		if o.shouldCleanupTerminalIssue(workflow, issue) {
			slog.Info("Stopping run because issue reached terminal state",
				issueLogAttrs(issue, -1, "reason", "terminal_state")...,
			)
			o.stopRun(issueID)
			o.cleanupTerminalAppServerProcess(issue)
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
		dispatchable, reason, _ := o.isDispatchable(workflow, issue)
		if !dispatchable {
			if reason == "paused" {
				continue
			}
			slog.Info("Stopping run during reconciliation",
				issueLogAttrs(issue, -1, "reason", reason)...,
			)
			o.stopRun(issueID)
			o.releaseClaim(issueID)
		}
	}

	o.reconcileOrphanedRuns(ctx, syncProvider)
}

func (o *Orchestrator) reconcilePausedRuns(ctx context.Context) {
	issues, err := o.store.ListIssues(map[string]interface{}{
		"states": []string{"ready", "in_progress", "in_review", "done", "cancelled"},
	})
	if err != nil {
		slog.Warn("Skipping paused run reconciliation because issue listing failed", "error", err)
		return
	}

	for i := range issues {
		issue := &issues[i]
		if issue.State == kanban.StateCancelled {
			o.clearPausedState(issue.ID)
			continue
		}

		o.mu.RLock()
		_, running := o.running[issue.ID]
		_, retrying := o.retries[issue.ID]
		o.mu.RUnlock()
		if running || retrying {
			continue
		}

		paused, ok, err := o.findPausedRun(issue)
		if err != nil {
			slog.Warn("Skipping paused run reconciliation because execution state lookup failed",
				issueLogAttrs(issue, -1, "error", err)...,
			)
			continue
		}
		if !ok {
			o.clearPausedState(issue.ID)
			continue
		}
		if pausedLifecycleReset(issue, paused) {
			o.clearPausedState(issue.ID)
			continue
		}

		o.mu.Lock()
		current, exists := o.paused[issue.ID]
		if !exists || current != paused {
			o.paused[issue.ID] = paused
		}
		o.mu.Unlock()
	}
}

func (o *Orchestrator) reconcileOrphanedRuns(ctx context.Context, syncProvider bool) {
	if syncProvider {
		o.syncProviderIssues(ctx)
	}
	issues, err := o.store.ListIssues(map[string]interface{}{
		"states": []string{"ready", "in_progress", "in_review", "done"},
	})
	if err != nil {
		slog.Warn("Skipping orphaned run reconciliation because issue listing failed", "error", err)
		return
	}

	for i := range issues {
		issue := &issues[i]

		o.mu.RLock()
		_, running := o.running[issue.ID]
		_, retrying := o.retries[issue.ID]
		_, paused := o.paused[issue.ID]
		o.mu.RUnlock()
		if running || retrying || paused {
			continue
		}

		runtime, workflow, err := o.runtimeForIssue(issue)
		if err != nil {
			slog.Warn("Skipping orphaned run reconciliation because runtime resolution failed",
				issueLogAttrs(issue, -1, "error", err)...,
			)
			continue
		}
		phase, attempt, session, persisted, orphaned, err := o.findOrphanedRun(issue)
		if err != nil {
			slog.Warn("Skipping orphaned run reconciliation because execution state lookup failed",
				issueLogAttrs(issue, -1, "error", err)...,
			)
			continue
		}
		if !orphaned {
			continue
		}

		dispatchable, reason, _ := o.isDispatchable(workflow, issue)
		errText := "run_interrupted"
		resumeThreadID, resumeMode := classifyOrphanedResume(workflow, persisted)
		immediateResume := resumeMode != ""
		o.persistExecutionSession(issue, phase, attempt, "run_interrupted", errText, false, "", session)
		if err := o.store.CompactIssueActivityAttemptDiagnostic(issue.ID, attempt); err != nil {
			slog.Warn("Failed to compact interrupted issue activity",
				issueLogAttrs(issue, attempt, "phase", phase, "error", err)...,
			)
		}

		o.mu.Lock()
		if _, ok := o.running[issue.ID]; ok {
			o.mu.Unlock()
			continue
		}
		if _, ok := o.retries[issue.ID]; ok {
			o.mu.Unlock()
			continue
		}
		if _, ok := o.paused[issue.ID]; ok {
			o.mu.Unlock()
			continue
		}
		o.appendEventLocked("run_interrupted", map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      string(phase),
			"attempt":    attempt,
			"error":      errText,
		})
		nextAttemptNumber := 0
		pausedRun := false
		if dispatchable {
			nextAttemptNumber, pausedRun = o.handleInterruptedRunLocked(issue, phase, attempt, session, errText, resumeThreadID, immediateResume)
		}
		o.mu.Unlock()

		if dispatchable && !pausedRun {
			slog.Warn("Recovered orphaned run and scheduled retry",
				issueLogAttrs(issue, attempt, "phase", phase, "next_attempt", nextAttemptNumber)...,
			)
			continue
		}
		if dispatchable && pausedRun {
			slog.Warn("Recovered orphaned run and paused automatic retries",
				issueLogAttrs(issue, attempt, "phase", phase, "next_attempt", nextAttemptNumber, "pause_threshold", interruptedRunPauseThreshold)...,
			)
			continue
		}
		if o.shouldCleanupTerminalIssue(workflow, issue) {
			o.cleanupTerminalAppServerProcess(issue)
			if err := runtime.runner.CleanupWorkspace(ctx, issue); err != nil {
				slog.Warn("Failed to cleanup terminal workspace after orphaned run recovery", "issue", issue.Identifier, "error", err)
			}
		}
		slog.Warn("Recovered orphaned run without retry",
			issueLogAttrs(issue, attempt, "phase", phase, "reason", reason)...,
		)
	}
}

func (o *Orchestrator) findPausedRun(issue *kanban.Issue) (pausedEntry, bool, error) {
	if issue == nil {
		return pausedEntry{}, false, nil
	}

	events, err := o.store.ListIssueRuntimeEvents(issue.ID, 20)
	if err != nil {
		return pausedEntry{}, false, err
	}
	if len(events) == 0 {
		return pausedEntry{}, false, nil
	}
	latest := events[len(events)-1]
	if latest.Kind != "retry_paused" {
		return pausedEntry{}, false, nil
	}
	return pausedEntryFromRuntimeEvent(latest), true, nil
}

func (o *Orchestrator) findOrphanedRun(issue *kanban.Issue) (kanban.WorkflowPhase, int, *appserver.Session, *kanban.ExecutionSessionSnapshot, bool, error) {
	if issue == nil {
		return "", 0, nil, nil, false, nil
	}

	persisted, err := o.store.GetIssueExecutionSession(issue.ID)
	if err != nil && err != sql.ErrNoRows {
		return "", 0, nil, nil, false, err
	}
	events, err := o.store.ListIssueRuntimeEvents(issue.ID, 20)
	if err != nil {
		return "", 0, nil, nil, false, err
	}

	phase := issue.WorkflowPhase
	if !phase.IsValid() {
		phase = kanban.DefaultWorkflowPhaseForState(issue.State)
	}
	attempt := 0
	var session *appserver.Session
	if persisted != nil {
		if parsed := kanban.WorkflowPhase(strings.TrimSpace(persisted.Phase)); parsed.IsValid() {
			phase = parsed
		}
		attempt = persisted.Attempt
		cp := persisted.AppSession
		session = &cp
	}
	if len(events) > 0 {
		latest := events[len(events)-1]
		if parsed := kanban.WorkflowPhase(strings.TrimSpace(latest.Phase)); parsed.IsValid() {
			phase = parsed
		}
		if latest.Attempt > attempt {
			attempt = latest.Attempt
		}
		switch latest.Kind {
		case "run_started":
			return phase, attempt, session, persisted, true, nil
		case "run_failed", "run_unsuccessful", "run_completed", "retry_scheduled", "retry_paused", "manual_retry_requested", "run_interrupted":
			return phase, attempt, session, persisted, false, nil
		}
	}
	if persisted != nil && strings.TrimSpace(persisted.RunKind) == "run_started" {
		return phase, attempt, session, persisted, true, nil
	}
	return phase, attempt, session, persisted, false, nil
}

func classifyOrphanedResume(workflow *config.Workflow, persisted *kanban.ExecutionSessionSnapshot) (string, string) {
	if !isAppServerWorkflow(workflow) || persisted == nil {
		return "", ""
	}
	threadID := strings.TrimSpace(persisted.AppSession.ThreadID)
	if threadID == "" {
		return "", ""
	}
	if persisted.ResumeEligible && strings.TrimSpace(persisted.StopReason) == gracefulShutdownStopReason {
		return threadID, "required"
	}
	if strings.TrimSpace(persisted.StopReason) == "" {
		return threadID, "opportunistic"
	}
	return "", ""
}

func isAppServerWorkflow(workflow *config.Workflow) bool {
	return workflow != nil && strings.TrimSpace(workflow.Config.Agent.Mode) == config.AgentModeAppServer
}

func (o *Orchestrator) shouldAllowRunningTerminalTransition(workflow *config.Workflow, issue *kanban.Issue, runningPhase kanban.WorkflowPhase) bool {
	if issue == nil || !o.isTerminalState(workflow, string(issue.State)) {
		return false
	}
	if issue.State == kanban.StateCancelled {
		return false
	}
	switch runningPhase {
	case kanban.WorkflowPhaseImplementation, kanban.WorkflowPhaseReview, kanban.WorkflowPhaseDone:
		return issue.State == kanban.StateDone
	default:
		return false
	}
}

func dispatchMode(workflow *config.Workflow) string {
	if workflow == nil {
		return config.DispatchModeParallel
	}
	mode := strings.TrimSpace(workflow.Config.Agent.DispatchMode)
	if mode == "" {
		return config.DispatchModeParallel
	}
	return mode
}

func isPerProjectSerialDispatch(workflow *config.Workflow) bool {
	return dispatchMode(workflow) == config.DispatchModePerProjectSerial
}

func priorityBucket(priority int) int {
	if priority > 0 {
		return 0
	}
	return 1
}

func issuePriorityLess(left, right *kanban.Issue) bool {
	leftBucket := priorityBucket(left.Priority)
	rightBucket := priorityBucket(right.Priority)
	if leftBucket != rightBucket {
		return leftBucket < rightBucket
	}
	if leftBucket == 0 && left.Priority != right.Priority {
		return left.Priority < right.Priority
	}
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	return left.Identifier < right.Identifier
}

func (o *Orchestrator) hasProjectCapacity(workflow *config.Workflow, projectID string) bool {
	limit := workflow.Config.Agent.MaxConcurrentAgents
	if isPerProjectSerialDispatch(workflow) {
		limit = 1
	}
	if limit <= 0 {
		return false
	}
	return o.runningCountForProject(projectID) < limit
}

func (o *Orchestrator) dueRetryEntry(issueID string, now time.Time) (retryEntry, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	entry, ok := o.retries[issueID]
	if !ok {
		return retryEntry{}, false
	}
	if _, running := o.running[issueID]; running {
		return retryEntry{}, false
	}
	if entry.DueAt.After(now) {
		return retryEntry{}, false
	}
	return entry, true
}

func (o *Orchestrator) isClaimed(issueID string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	_, ok := o.claimed[issueID]
	return ok
}

func (o *Orchestrator) dispatch(ctx context.Context) error {
	return o.dispatchWithProviderSync(ctx, true)
}

func (o *Orchestrator) dispatchWithProviderSync(ctx context.Context, syncProvider bool) error {
	if syncProvider {
		o.syncProviderIssues(ctx)
	}
	states := []string{"ready", "in_progress", "in_review", "done"}
	if !o.isSharedMode() {
		workflow, err := o.workflows.Current()
		if err != nil {
			return err
		}
		states = o.dispatchCandidateStates(workflow)
	}

	issues, err := o.store.ListDispatchIssues(states)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for i := range issues {
		issue := issues[i].Issue
		dispatchState := issues[i].DispatchState
		runtime, workflow, err := o.runtimeForIssue(&issue)
		if err != nil {
			slog.Warn("Skipping issue dispatch because runtime resolution failed",
				issueLogAttrs(&issue, 0, "error", err)...,
			)
			continue
		}
		if !o.hasProjectCapacity(workflow, issue.ProjectID) {
			continue
		}
		dispatchable, reason, phase := o.isDispatchableWithState(workflow, &issue, &dispatchState)
		if !dispatchable {
			if reason != "terminal_state" {
				slog.Debug("Skipping issue dispatch because it is not dispatchable",
					issueLogAttrs(&issue, 0, "reason", reason)...,
				)
			}
			continue
		}
		retry, useDueRetry := retryEntry{}, false
		if isPerProjectSerialDispatch(workflow) {
			if due, ok := o.dueRetryEntry(issue.ID, now); ok {
				retry = due
				useDueRetry = true
			}
		}

		claimed := o.tryClaim(issue.ID)
		if !claimed && useDueRetry && o.isClaimed(issue.ID) {
			claimed = true
		}
		if !claimed {
			slog.Debug("Issue claim rejected because it is already claimed",
				issueLogAttrs(&issue, 0)...,
			)
			continue
		}
		slog.Info("Issue claim accepted", issueLogAttrs(&issue, 0)...)
		currentDispatchState, stateErr := o.issueDispatchState(&issue)
		if stateErr != nil {
			slog.Info("Releasing issue claim because dispatch state refresh failed",
				issueLogAttrs(&issue, 0, "error", stateErr)...,
			)
			o.releaseClaim(issue.ID)
			continue
		}
		if ok, reason, _ := o.isDispatchableWithState(workflow, &issue, currentDispatchState); !ok {
			slog.Info("Releasing issue claim because issue is no longer dispatchable",
				issueLogAttrs(&issue, 0, "reason", reason)...,
			)
			o.releaseClaim(issue.ID)
			continue
		}
		issue.WorkflowPhase = phase
		attempt := 0
		if useDueRetry {
			attempt = retry.Attempt
		}
		o.startRun(ctx, workflow, runtime.runner, &issue, attempt)
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

		issue, err := o.refreshIssue(ctx, issueID)
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
		if o.shouldCleanupTerminalIssue(workflow, issue) {
			slog.Info("Dropping retry because issue reached terminal state",
				issueLogAttrs(issue, entry.Attempt)...,
			)
			o.cleanupTerminalAppServerProcess(issue)
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
		dispatchable, reason, phase := o.isDispatchable(workflow, issue)
		if !dispatchable {
			slog.Info("Dropping retry because issue is not dispatchable",
				issueLogAttrs(issue, entry.Attempt, "reason", reason)...,
			)
			o.releaseClaim(issueID)
			o.mu.Lock()
			delete(o.retries, issueID)
			o.mu.Unlock()
			continue
		}
		if isPerProjectSerialDispatch(workflow) {
			continue
		}
		if !o.hasProjectCapacity(workflow, issue.ProjectID) {
			continue
		}
		slog.Info("Retry is due; starting issue run",
			issueLogAttrs(issue, entry.Attempt, "delay_type", entry.DelayType, "phase", phase)...,
		)
		issue.WorkflowPhase = phase
		issue.ResumeThreadID = strings.TrimSpace(entry.ResumeThreadID)
		o.startRun(ctx, workflow, runtime.runner, issue, entry.Attempt)
	}
}

func recurringScheduleEventKind(issue *kanban.Issue, now time.Time) string {
	if issue == nil || issue.NextRunAt == nil {
		return "recurring_enqueued"
	}
	if now.Sub(issue.NextRunAt.UTC()) >= time.Minute {
		return "recurring_catch_up_enqueued"
	}
	return "recurring_enqueued"
}

func (o *Orchestrator) recurringIssueOccupied(workflow *config.Workflow, issue *kanban.Issue) bool {
	return o.recurringIssueOccupiedIgnoringRunning(workflow, issue, "")
}

func (o *Orchestrator) recurringIssueOccupiedIgnoringRunning(workflow *config.Workflow, issue *kanban.Issue, ignoreRunningIssueID string) bool {
	if issue == nil {
		return false
	}
	o.mu.RLock()
	_, running := o.running[issue.ID]
	_, retrying := o.retries[issue.ID]
	_, paused := o.paused[issue.ID]
	o.mu.RUnlock()
	if (running && issue.ID != ignoreRunningIssueID) || retrying || paused {
		return true
	}
	switch issue.State {
	case kanban.StateReady, kanban.StateInProgress, kanban.StateInReview:
		return true
	}
	return o.executionPhase(workflow, issue) == kanban.WorkflowPhaseDone
}

func (o *Orchestrator) appendRecurringRuntimeEvent(kind string, issue *kanban.Issue, fields map[string]interface{}) {
	o.mu.Lock()
	defer o.mu.Unlock()
	payload := map[string]interface{}{}
	for key, value := range fields {
		payload[key] = value
	}
	if issue != nil {
		payload["issue_id"] = issue.ID
		payload["identifier"] = issue.Identifier
	}
	o.appendEventLocked(kind, payload)
}

func (o *Orchestrator) recordRecurringPendingRerun(issue *kanban.Issue, reason string) bool {
	if issue == nil || !issue.IsRecurring() {
		return false
	}
	if issue.PendingRerun {
		return false
	}
	if err := o.store.MarkRecurringPendingRerun(issue.ID, true); err != nil {
		slog.Warn("Failed to record recurring pending rerun",
			issueLogAttrs(issue, 0, "reason", reason, "error", err)...,
		)
		return false
	}
	issue.PendingRerun = true
	o.appendRecurringRuntimeEvent("recurring_pending_rerun_recorded", issue, map[string]interface{}{
		"reason": reason,
	})
	return true
}

func (o *Orchestrator) enqueueRecurringIssue(issue *kanban.Issue, eventKind string, keepCurrentNextRun bool) bool {
	if issue == nil || !issue.IsRecurring() {
		return false
	}
	enqueuedAt := time.Now().UTC()
	nextRunAt := issue.NextRunAt
	if !keepCurrentNextRun || nextRunAt == nil || !nextRunAt.After(enqueuedAt) {
		if issue.Enabled {
			computed, err := kanban.NextRecurringRun(issue.Cron, enqueuedAt, time.Local)
			if err != nil {
				slog.Warn("Failed to compute next recurring run",
					issueLogAttrs(issue, 0, "error", err)...,
				)
				return false
			}
			nextRunAt = &computed
		} else {
			nextRunAt = nil
		}
	}
	if err := o.store.RearmRecurringIssue(issue.ID, enqueuedAt, nextRunAt); err != nil {
		slog.Warn("Failed to enqueue recurring issue",
			issueLogAttrs(issue, 0, "error", err)...,
		)
		return false
	}
	issue.State = kanban.StateReady
	issue.WorkflowPhase = kanban.WorkflowPhaseImplementation
	issue.LastEnqueuedAt = &enqueuedAt
	issue.NextRunAt = nextRunAt
	issue.PendingRerun = false
	o.appendRecurringRuntimeEvent(eventKind, issue, map[string]interface{}{
		"cron":             issue.Cron,
		"enabled":          issue.Enabled,
		"last_enqueued_at": enqueuedAt.Format(time.RFC3339),
		"next_run_at": func() interface{} {
			if nextRunAt == nil {
				return nil
			}
			return nextRunAt.UTC().Format(time.RFC3339)
		}(),
	})
	return true
}

func (o *Orchestrator) processPendingRecurringRerun(issue *kanban.Issue) bool {
	return o.processPendingRecurringRerunIgnoringRunning(issue, "")
}

func (o *Orchestrator) processPendingRecurringRerunIgnoringRunning(issue *kanban.Issue, ignoreRunningIssueID string) bool {
	if issue == nil || !issue.IsRecurring() || !issue.PendingRerun {
		return false
	}
	if !issue.Enabled {
		if err := o.store.MarkRecurringPendingRerun(issue.ID, false); err != nil {
			slog.Warn("Failed to clear disabled recurring pending rerun",
				issueLogAttrs(issue, 0, "error", err)...,
			)
			return false
		}
		issue.PendingRerun = false
		o.appendRecurringRuntimeEvent("recurring_pending_rerun_cleared", issue, map[string]interface{}{
			"reason": "disabled",
		})
		return false
	}
	_, workflow, err := o.runtimeForIssue(issue)
	if err != nil {
		slog.Warn("Skipping recurring pending rerun because runtime resolution failed",
			issueLogAttrs(issue, 0, "error", err)...,
		)
		return false
	}
	if o.recurringIssueOccupiedIgnoringRunning(workflow, issue, ignoreRunningIssueID) {
		return false
	}
	return o.enqueueRecurringIssue(issue, "recurring_pending_rerun_enqueued", true)
}

func (o *Orchestrator) processPendingRecurringReruns(ctx context.Context) {
	_ = ctx
	issues, err := o.store.ListPendingRecurringIssues(o.recurrenceScopeRepoPath(), 200)
	if err != nil {
		slog.Warn("Skipping recurring pending reruns because issue listing failed", "error", err)
		return
	}
	for i := range issues {
		o.processPendingRecurringRerun(&issues[i])
	}
}

func (o *Orchestrator) processDueRecurringIssues(ctx context.Context) {
	_ = ctx
	now := time.Now().UTC()
	issues, err := o.store.ListDueRecurringIssues(now, o.recurrenceScopeRepoPath(), 200)
	if err != nil {
		slog.Warn("Skipping due recurring issues because listing failed", "error", err)
		return
	}
	for i := range issues {
		issue := &issues[i]
		if !issue.IsRecurring() || !issue.Enabled {
			continue
		}
		_, workflow, err := o.runtimeForIssue(issue)
		if err != nil {
			slog.Warn("Skipping due recurring issue because runtime resolution failed",
				issueLogAttrs(issue, 0, "error", err)...,
			)
			continue
		}
		if o.recurringIssueOccupied(workflow, issue) {
			o.recordRecurringPendingRerun(issue, "schedule_due")
			continue
		}
		o.enqueueRecurringIssue(issue, recurringScheduleEventKind(issue, now), false)
	}
}

func (o *Orchestrator) startRun(ctx context.Context, workflow *config.Workflow, runner runnerExecutor, issue *kanban.Issue, attempt int) {
	phase := o.executionPhase(workflow, issue)
	runIssue := *issue
	runIssue.WorkflowPhase = phase
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	entry := runningEntry{
		cancel:    cancel,
		issue:     runIssue,
		phase:     phase,
		attempt:   attempt,
		startedAt: time.Now().UTC(),
		done:      done,
	}
	o.unmarkAppServerRetired(runIssue.ID)
	o.mu.Lock()
	delete(o.liveSessions, runIssue.ID)
	delete(o.paused, runIssue.ID)
	o.clearPendingInteractionsForIssueLocked(runIssue.ID)
	o.running[runIssue.ID] = entry
	delete(o.retries, runIssue.ID)
	o.appendEventLocked("run_started", map[string]interface{}{
		"issue_id":    runIssue.ID,
		"identifier":  runIssue.Identifier,
		"title":       runIssue.Title,
		"phase":       string(phase),
		"attempt":     attempt,
		"issue_state": string(runIssue.State),
	})
	o.mu.Unlock()
	o.clearSessionWriteState(runIssue.ID)
	slog.Info("Agent run started", issueLogAttrs(&runIssue, attempt, "phase", phase)...)
	o.persistExecutionSession(&runIssue, phase, attempt, "run_started", "", false, "", &appserver.Session{
		IssueID:         runIssue.ID,
		IssueIdentifier: runIssue.Identifier,
	})

	o.runWG.Add(1)
	go func() {
		defer o.runWG.Done()
		defer close(done)
		result, err := runner.RunAttempt(runCtx, &runIssue, attempt)
		o.finishRun(workflow, &runIssue, phase, attempt, result, err)
	}()
}

func (o *Orchestrator) finishRun(workflow *config.Workflow, issue *kanban.Issue, phase kanban.WorkflowPhase, attempt int, result *agent.RunResult, err error) {
	defer func() {
		if hook := o.testHooks.beforeFinishRunRelease; hook != nil {
			hook(issue.ID)
		}
		o.mu.Lock()
		delete(o.running, issue.ID)
		delete(o.liveSessions, issue.ID)
		o.clearPendingInteractionsForIssueLocked(issue.ID)
		o.mu.Unlock()
		o.clearSessionWriteState(issue.ID)
		observability.BroadcastUpdate()
	}()

	o.mu.Lock()
	o.totalRuns++
	o.mu.Unlock()

	current := issue
	if refreshed, getErr := o.store.GetIssue(issue.ID); getErr == nil && refreshed != nil {
		current = refreshed
	} else {
		cloned := *issue
		current = &cloned
	}
	current.WorkflowPhase = phase
	if isCancelledRunCompletion(err, result) {
		if snapshot, snapshotErr := o.store.GetIssueExecutionSession(issue.ID); snapshotErr == nil && snapshot != nil && snapshot.StopReason == gracefulShutdownStopReason {
			return
		}
		slog.Info("Agent run cancelled",
			issueLogAttrs(current, attempt, "phase", phase)...,
		)
		return
	}
	if result != nil && result.AppSession != nil {
		o.persistFinalIssueTokenSpend(issue.ID, result.AppSession)
	}

	switch {
	case err != nil:
		o.persistExecutionSessionSnapshot(current, phase, attempt, "run_failed", err.Error(), result)
		if compactErr := o.store.CompactIssueActivityAttemptDiagnostic(current.ID, attempt); compactErr != nil {
			slog.Warn("Failed to compact failed issue activity",
				issueLogAttrs(current, attempt, "phase", phase, "error", compactErr)...,
			)
		}
		next := o.handleFailedRun(workflow, current, phase, attempt, result, "run_failed", err.Error())
		slog.Warn("Agent run failed",
			issueLogAttrs(current, attempt, "error", err, "next_attempt", next, "phase", phase)...,
		)
	case result != nil && result.StopReason == planApprovalStopReason:
		next := nextAttempt(attempt)
		o.mu.Lock()
		fields := map[string]interface{}{}
		attachResultMetrics(fields, result)
		o.pauseRetryLocked(current, next, phase, planApprovalStopReason, fields)
		o.mu.Unlock()
		o.persistExecutionSession(current, phase, next, "retry_paused", planApprovalStopReason, false, planApprovalStopReason, result.AppSession)
		slog.Info("Agent run paused pending plan approval",
			issueLogAttrs(current, attempt, "phase", phase, "next_attempt", next)...,
		)
	case result != nil && !result.Success:
		errText := "unsuccessful"
		if result.Error != nil {
			errText = result.Error.Error()
		}
		o.persistExecutionSessionSnapshot(current, phase, attempt, "run_unsuccessful", errText, result)
		if compactErr := o.store.CompactIssueActivityAttemptDiagnostic(current.ID, attempt); compactErr != nil {
			slog.Warn("Failed to compact unsuccessful issue activity",
				issueLogAttrs(current, attempt, "phase", phase, "error", compactErr)...,
			)
		}
		next := o.handleFailedRun(workflow, current, phase, attempt, result, "run_unsuccessful", errText)
		slog.Warn("Agent run completed unsuccessfully",
			issueLogAttrs(current, attempt, "error", errText, "next_attempt", next, "phase", phase)...,
		)
	default:
		o.persistExecutionSessionSnapshot(current, phase, attempt, "run_completed", "", result)
		if compactErr := o.store.CompactIssueActivityAttemptSuccess(current.ID, attempt); compactErr != nil {
			slog.Warn("Failed to compact completed issue activity",
				issueLogAttrs(current, attempt, "phase", phase, "error", compactErr)...,
			)
		}
		o.publishIssuePreviewAsync(current, phase, result)
		next, scheduled := o.handleSuccessfulRun(workflow, current, phase, attempt, result)
		extra := []interface{}{"phase", phase}
		if scheduled {
			extra = append(extra, "next_attempt", next)
		}
		slog.Info("Agent run completed",
			issueLogAttrs(current, attempt, extra...)...,
		)
	}
	o.processPendingRecurringRerunIgnoringRunning(current, issue.ID)
	o.flushIssueTokenSpend(issue.ID, true)
}

func (o *Orchestrator) publishIssuePreviewAsync(issue *kanban.Issue, phase kanban.WorkflowPhase, result *agent.RunResult) {
	if phase != kanban.WorkflowPhaseDone || issue == nil {
		return
	}
	workspace, err := o.store.GetWorkspace(issue.ID)
	if err != nil || workspace == nil {
		return
	}
	previewPath, err := findReviewPreviewVideo(workspace.Path)
	if err != nil || previewPath == "" {
		return
	}
	commentBody := buildIssuePreviewCommentBody(issue, result, previewPath)
	issueCopy := *issue
	go func(issue kanban.Issue, previewPath string, commentBody string) {
		ctx, cancel := context.WithTimeout(context.Background(), reviewPreviewPublishTimeout)
		defer cancel()
		if err := o.service.CreateIssueComment(ctx, issue.Identifier, providers.IssueCommentInput{
			Body: &commentBody,
			Attachments: []providers.IssueCommentAttachment{{
				Path: previewPath,
			}},
		}); err != nil {
			if providers.IsUnsupported(err) {
				return
			}
			slog.Warn("Failed to publish issue preview",
				issueLogAttrs(&issue, -1, "phase", phase, "preview_path", previewPath, "error", err)...,
			)
			return
		}
		slog.Info("Published issue preview",
			issueLogAttrs(&issue, -1, "phase", phase, "preview_path", previewPath)...,
		)
	}(issueCopy, previewPath, commentBody)
}

func findReviewPreviewVideo(workspacePath string) (string, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return "", nil
	}
	previewDir := filepath.Join(workspacePath, filepath.FromSlash(reviewPreviewDir))
	entries, err := os.ReadDir(previewDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var (
		bestPath    string
		bestName    string
		bestModTime time.Time
	)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".mp4", ".webm", ".mov", ".m4v":
			candidatePath := filepath.Join(previewDir, entry.Name())
			modTime := info.ModTime().UTC()
			if bestPath == "" || modTime.After(bestModTime) || (modTime.Equal(bestModTime) && entry.Name() < bestName) {
				bestPath = candidatePath
				bestName = entry.Name()
				bestModTime = modTime
			}
		}
	}
	return bestPath, nil
}

func buildIssuePreviewCommentBody(issue *kanban.Issue, result *agent.RunResult, previewPath string) string {
	lines := []string{"Automated reviewer preview from the done pass."}
	finalMessage := strings.TrimSpace(issuePreviewSummary(result))
	if finalMessage != "" {
		lines = append(lines, "", finalMessage)
	}
	filename := filepath.Base(strings.TrimSpace(previewPath))
	if filename != "" && filename != "." {
		lines = append(lines, "", fmt.Sprintf("Preview file: `%s`", filename))
	}
	return strings.Join(lines, "\n")
}

func issuePreviewSummary(result *agent.RunResult) string {
	if result == nil {
		return ""
	}
	if result.AppSession != nil {
		if message := strings.TrimSpace(result.AppSession.LastMessage); message != "" {
			return message
		}
	}
	return strings.TrimSpace(result.Output)
}

func isCancelledRunCompletion(err error, result *agent.RunResult) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if result == nil || result.Error == nil {
		return false
	}
	return errors.Is(result.Error, context.Canceled)
}

func nextAttempt(attempt int) int {
	if attempt > 0 {
		return attempt + 1
	}
	return 1
}

func (o *Orchestrator) handleFailedRun(workflow *config.Workflow, issue *kanban.Issue, phase kanban.WorkflowPhase, attempt int, result *agent.RunResult, eventKind, errText string) int {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.failedRuns++
	nextPhase := phase
	if !pausesWithoutStateReset(errText) {
		switch phase {
		case kanban.WorkflowPhaseReview:
			o.updateIssueStatePhase(issue, kanban.StateInProgress, kanban.WorkflowPhaseImplementation)
			nextPhase = kanban.WorkflowPhaseImplementation
		case kanban.WorkflowPhaseDone:
			o.updateIssueStatePhase(issue, kanban.StateDone, kanban.WorkflowPhaseDone)
			nextPhase = kanban.WorkflowPhaseDone
		default:
			if issue.State != kanban.StateReady && issue.State != kanban.StateInProgress {
				o.updateIssueStatePhase(issue, kanban.StateInProgress, kanban.WorkflowPhaseImplementation)
			} else {
				o.updateIssuePhase(issue, kanban.WorkflowPhaseImplementation)
			}
			nextPhase = kanban.WorkflowPhaseImplementation
		}
	}

	next := nextAttempt(attempt)
	fields := map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      string(phase),
		"attempt":    attempt,
		"error":      errText,
	}
	attachResultMetrics(fields, result)
	o.appendEventLocked(eventKind, fields)
	if o.shouldPauseRunLocked(issue.ID, errText) {
		o.pauseRetryLocked(issue, next, nextPhase, errText, nil)
		if result != nil && result.AppSession != nil {
			o.persistExecutionSessionSnapshot(issue, nextPhase, next, "retry_paused", errText, result)
		}
		return next
	}
	if !o.scheduleAutomaticRetryLocked(workflow, issue, next, nextPhase, "failure", errText, workflow.Config.Agent.MaxRetryBackoffMs) {
		if result != nil && result.AppSession != nil {
			o.persistExecutionSessionSnapshot(issue, nextPhase, next, "retry_paused", "retry_limit_reached", result)
		}
	}
	return next
}

func (o *Orchestrator) handleSuccessfulRun(workflow *config.Workflow, issue *kanban.Issue, phase kanban.WorkflowPhase, attempt int, result *agent.RunResult) (int, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.successfulRuns++
	previousState := issue.State
	nextPhase, shouldContinue := o.advanceIssueAfterSuccess(workflow, issue, phase)
	fields := map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      string(phase),
		"attempt":    attempt,
	}
	attachResultMetrics(fields, result)
	if shouldContinue && shouldScheduleSuccessfulContinuation(phase, nextPhase, previousState, issue.State) {
		next := nextAttempt(attempt)
		fields["next_retry"] = next
		fields["next_phase"] = string(nextPhase)
		o.appendEventLocked("run_completed", fields)
		if o.scheduleAutomaticRetryLocked(workflow, issue, next, nextPhase, "continuation", "", workflow.Config.Agent.MaxRetryBackoffMs) {
			return next, true
		}
		if result != nil && result.AppSession != nil {
			o.persistExecutionSessionSnapshot(issue, nextPhase, next, "retry_paused", "retry_limit_reached", result)
		}
		return next, false
	}
	o.appendEventLocked("run_completed", fields)
	if shouldContinue {
		next := nextAttempt(attempt)
		o.pauseRetryLocked(issue, next, nextPhase, "no_state_transition", nil)
		if result != nil && result.AppSession != nil {
			o.persistExecutionSessionSnapshot(issue, nextPhase, next, "retry_paused", "no_state_transition", result)
		}
		return next, false
	}
	return 0, false
}

func (o *Orchestrator) handleInterruptedRunLocked(issue *kanban.Issue, phase kanban.WorkflowPhase, attempt int, session *appserver.Session, errText, resumeThreadID string, immediate bool) (int, bool) {
	next := nextAttempt(attempt)
	if o.shouldPauseRunLocked(issue.ID, errText) {
		o.pauseRetryLocked(issue, next, phase, errText, nil)
		if session != nil {
			o.persistExecutionSession(issue, phase, next, "retry_paused", errText, false, "", session)
		}
		return next, true
	}
	var dueAt *time.Time
	if immediate {
		now := time.Now().UTC()
		dueAt = &now
	}
	if !o.scheduleAutomaticRetryLockedWithResume(nil, issue, next, phase, "failure", errText, 0, dueAt, resumeThreadID) {
		if session != nil {
			o.persistExecutionSession(issue, phase, next, "retry_paused", "retry_limit_reached", false, "", session)
		}
		return next, true
	}
	return next, false
}

func (o *Orchestrator) shouldPauseRunLocked(issueID, errText string) bool {
	if pausesWithoutStateReset(errText) {
		return true
	}
	if !isInterruptedRunError(errText) {
		return false
	}
	streak, err := o.interruptedFailureStreak(issueID, 50)
	if err != nil {
		slog.Warn("Failed to compute interrupted run streak", "issue_id", issueID, "error", err)
		return true
	}
	return streak >= interruptedRunPauseThreshold
}

func (o *Orchestrator) pauseRetryLocked(issue *kanban.Issue, attempt int, phase kanban.WorkflowPhase, errText string, extraFields map[string]interface{}) {
	now := time.Now().UTC()
	entry := pausedEntry{
		IssueState: string(issue.State),
		Attempt:    attempt,
		Phase:      string(phase),
		PausedAt:   now,
		Error:      errText,
	}
	if isInterruptedRunError(errText) {
		streak, err := o.interruptedFailureStreak(issue.ID, 50)
		if err != nil {
			slog.Warn("Failed to compute interrupted run streak for pause", "issue_id", issue.ID, "error", err)
			streak = interruptedRunPauseThreshold
		}
		entry.ConsecutiveFailures = streak
		entry.PauseThreshold = interruptedRunPauseThreshold
	}
	o.paused[issue.ID] = entry
	delete(o.retries, issue.ID)
	fields := map[string]interface{}{
		"issue_id":             issue.ID,
		"identifier":           issue.Identifier,
		"issue_state":          string(issue.State),
		"phase":                string(phase),
		"attempt":              attempt,
		"paused_at":            now.Format(time.RFC3339),
		"error":                errText,
		"consecutive_failures": entry.ConsecutiveFailures,
		"pause_threshold":      entry.PauseThreshold,
	}
	for key, value := range extraFields {
		fields[key] = value
	}
	o.appendEventLocked("retry_paused", fields)
	if isInterruptedRunError(errText) {
		slog.Warn("Automatic retries paused after interrupted runs",
			issueLogAttrs(issue, attempt,
				"phase", phase,
				"error", errText,
				"consecutive_failures", entry.ConsecutiveFailures,
				"pause_threshold", entry.PauseThreshold,
			)...,
		)
		return
	}
	slog.Warn("Automatic retries paused",
		issueLogAttrs(issue, attempt, "phase", phase, "error", errText)...,
	)
}

func (o *Orchestrator) advanceIssueAfterSuccess(workflow *config.Workflow, issue *kanban.Issue, phase kanban.WorkflowPhase) (kanban.WorkflowPhase, bool) {
	switch phase {
	case kanban.WorkflowPhaseReview:
		switch issue.State {
		case kanban.StateReady, kanban.StateInProgress:
			o.updateIssueStatePhase(issue, kanban.StateInProgress, kanban.WorkflowPhaseImplementation)
			return kanban.WorkflowPhaseImplementation, true
		case kanban.StateInReview:
			if workflow.Config.Phases.Done.Enabled {
				o.updateIssueStatePhase(issue, kanban.StateDone, kanban.WorkflowPhaseDone)
				return kanban.WorkflowPhaseDone, true
			}
			o.updateIssueStatePhase(issue, kanban.StateDone, kanban.WorkflowPhaseComplete)
			return kanban.WorkflowPhaseComplete, false
		case kanban.StateDone:
			if workflow.Config.Phases.Done.Enabled {
				o.updateIssuePhase(issue, kanban.WorkflowPhaseDone)
				return kanban.WorkflowPhaseDone, true
			}
			o.updateIssuePhase(issue, kanban.WorkflowPhaseComplete)
			return kanban.WorkflowPhaseComplete, false
		case kanban.StateCancelled:
			o.updateIssuePhase(issue, kanban.WorkflowPhaseComplete)
			return kanban.WorkflowPhaseComplete, false
		default:
			return kanban.WorkflowPhaseComplete, false
		}
	case kanban.WorkflowPhaseDone:
		switch issue.State {
		case kanban.StateDone, kanban.StateCancelled:
			o.updateIssuePhase(issue, kanban.WorkflowPhaseComplete)
			return kanban.WorkflowPhaseComplete, false
		case kanban.StateInReview:
			if workflow.Config.Phases.Review.Enabled {
				o.updateIssuePhase(issue, kanban.WorkflowPhaseReview)
				return kanban.WorkflowPhaseReview, true
			}
			o.updateIssueStatePhase(issue, kanban.StateInProgress, kanban.WorkflowPhaseImplementation)
			return kanban.WorkflowPhaseImplementation, true
		case kanban.StateReady, kanban.StateInProgress:
			o.updateIssuePhase(issue, kanban.WorkflowPhaseImplementation)
			return kanban.WorkflowPhaseImplementation, true
		default:
			return kanban.WorkflowPhaseComplete, false
		}
	default:
		switch issue.State {
		case kanban.StateDone:
			if workflow.Config.Phases.Done.Enabled {
				o.updateIssuePhase(issue, kanban.WorkflowPhaseDone)
				return kanban.WorkflowPhaseDone, true
			}
			o.updateIssuePhase(issue, kanban.WorkflowPhaseComplete)
			return kanban.WorkflowPhaseComplete, false
		case kanban.StateCancelled:
			o.updateIssuePhase(issue, kanban.WorkflowPhaseComplete)
			return kanban.WorkflowPhaseComplete, false
		case kanban.StateInReview:
			if workflow.Config.Phases.Review.Enabled {
				o.updateIssuePhase(issue, kanban.WorkflowPhaseReview)
				return kanban.WorkflowPhaseReview, true
			}
			o.updateIssueStatePhase(issue, kanban.StateInProgress, kanban.WorkflowPhaseImplementation)
			return kanban.WorkflowPhaseImplementation, true
		case kanban.StateReady, kanban.StateInProgress:
			if workflow.Config.Phases.Review.Enabled {
				o.updateIssueStatePhase(issue, kanban.StateInReview, kanban.WorkflowPhaseReview)
				return kanban.WorkflowPhaseReview, true
			}
			o.updateIssuePhase(issue, kanban.WorkflowPhaseImplementation)
			return kanban.WorkflowPhaseImplementation, true
		default:
			return kanban.WorkflowPhaseComplete, false
		}
	}
}

func (o *Orchestrator) scheduleAutomaticRetryLocked(workflow *config.Workflow, issue *kanban.Issue, attempt int, phase kanban.WorkflowPhase, delayType, errText string, maxBackoffMs int) bool {
	return o.scheduleAutomaticRetryLockedWithResume(workflow, issue, attempt, phase, delayType, errText, maxBackoffMs, nil, "")
}

func (o *Orchestrator) scheduleAutomaticRetryLockedWithResume(workflow *config.Workflow, issue *kanban.Issue, attempt int, phase kanban.WorkflowPhase, delayType, errText string, maxBackoffMs int, dueAt *time.Time, resumeThreadID string) bool {
	if issue == nil {
		return false
	}
	limit := automaticRetryLimit(workflow)
	if limit > 0 {
		count, err := o.automaticRetryCountLocked(issue.ID)
		if err != nil {
			slog.Warn("Failed to compute automatic retry count; pausing retries",
				issueLogAttrs(issue, attempt, "phase", phase, "error", err)...,
			)
			o.pauseRetryLocked(issue, attempt, phase, "retry_limit_reached", nil)
			return false
		}
		if count >= limit {
			o.pauseRetryLocked(issue, attempt, phase, "retry_limit_reached", nil)
			return false
		}
	}
	if dueAt != nil {
		o.scheduleRetryLockedAt(issue, attempt, phase, delayType, errText, dueAt.UTC(), resumeThreadID)
		return true
	}
	o.scheduleRetryLocked(issue, attempt, phase, delayType, errText, maxBackoffMs)
	return true
}

func (o *Orchestrator) scheduleRetryLocked(issue *kanban.Issue, attempt int, phase kanban.WorkflowPhase, delayType, errText string, maxBackoffMs int) {
	delay := continuationRetryDelay
	if delayType != "continuation" {
		delay = failureRetryDelay(attempt, maxBackoffMs)
	}
	o.scheduleRetryLockedAt(issue, attempt, phase, delayType, errText, time.Now().UTC().Add(delay), "")
}

func (o *Orchestrator) scheduleRetryLockedAt(issue *kanban.Issue, attempt int, phase kanban.WorkflowPhase, delayType, errText string, dueAt time.Time, resumeThreadID string) {
	now := time.Now().UTC()
	if dueAt.Before(now) {
		dueAt = now
	}
	delayMs := dueAt.Sub(now).Milliseconds()
	o.retries[issue.ID] = retryEntry{
		Attempt:        attempt,
		Phase:          string(phase),
		DueAt:          dueAt,
		Error:          errText,
		DelayType:      delayType,
		ResumeThreadID: strings.TrimSpace(resumeThreadID),
	}
	o.claimed[issue.ID] = struct{}{}
	o.appendEventLocked("retry_scheduled", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      string(phase),
		"attempt":    attempt,
		"due_at":     dueAt.UTC().Format(time.RFC3339),
		"delay_ms":   delayMs,
		"delay_type": delayType,
		"error":      errText,
	})
	slog.Info("Retry scheduled",
		issueLogAttrs(issue, attempt,
			"phase", phase,
			"delay_ms", delayMs,
			"delay_type", delayType,
			"error", errText,
		)...,
	)
}

func (o *Orchestrator) updateIssuePhase(issue *kanban.Issue, phase kanban.WorkflowPhase) {
	if err := o.store.UpdateIssueWorkflowPhase(issue.ID, phase); err != nil {
		slog.Warn("Failed to update issue phase", "issue", issue.Identifier, "phase", phase, "error", err)
		return
	}
	issue.WorkflowPhase = phase
}

func (o *Orchestrator) updateIssueStatePhase(issue *kanban.Issue, state kanban.State, phase kanban.WorkflowPhase) {
	if err := o.store.UpdateIssueStateAndPhase(issue.ID, state, phase); err != nil {
		slog.Warn("Failed to update issue state and phase", "issue", issue.Identifier, "state", state, "phase", phase, "error", err)
		return
	}
	issue.State = state
	issue.WorkflowPhase = phase
}

func (o *Orchestrator) dispatchCandidateStates(workflow *config.Workflow) []string {
	states := append([]string(nil), workflow.Config.Tracker.ActiveStates...)
	states = append(states, string(kanban.StateDone))
	return uniqueStrings(states)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (o *Orchestrator) executionPhase(workflow *config.Workflow, issue *kanban.Issue) kanban.WorkflowPhase {
	phase := issue.WorkflowPhase
	if !phase.IsValid() {
		phase = kanban.DefaultWorkflowPhaseForState(issue.State)
	}
	switch issue.State {
	case kanban.StateDone:
		if workflow.Config.Phases.Done.Enabled && phase == kanban.WorkflowPhaseDone {
			return kanban.WorkflowPhaseDone
		}
		return kanban.WorkflowPhaseComplete
	case kanban.StateCancelled:
		return kanban.WorkflowPhaseComplete
	case kanban.StateInReview:
		if workflow.Config.Phases.Review.Enabled && phase == kanban.WorkflowPhaseReview {
			return kanban.WorkflowPhaseReview
		}
		return kanban.WorkflowPhaseImplementation
	default:
		return kanban.WorkflowPhaseImplementation
	}
}

func (o *Orchestrator) shouldCleanupTerminalIssue(workflow *config.Workflow, issue *kanban.Issue) bool {
	if !o.isTerminalState(workflow, string(issue.State)) {
		return false
	}
	return o.executionPhase(workflow, issue) == kanban.WorkflowPhaseComplete
}

func (o *Orchestrator) isDispatchable(workflow *config.Workflow, issue *kanban.Issue) (bool, string, kanban.WorkflowPhase) {
	return o.isDispatchableWithState(workflow, issue, nil)
}

func (o *Orchestrator) issueDispatchState(issue *kanban.Issue) (*kanban.IssueDispatchState, error) {
	if issue == nil {
		return nil, fmt.Errorf("issue is required")
	}
	state, err := o.store.GetIssueDispatchState(issue.ID)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (o *Orchestrator) isDispatchableWithState(workflow *config.Workflow, issue *kanban.Issue, dispatchState *kanban.IssueDispatchState) (bool, string, kanban.WorkflowPhase) {
	phase := o.executionPhase(workflow, issue)
	if allowed, reason := o.projectAllowsDispatch(issue, dispatchState); !allowed {
		return false, reason, phase
	}
	o.mu.RLock()
	_, paused := o.paused[issue.ID]
	o.mu.RUnlock()
	if paused {
		return false, "paused", phase
	}
	if issue.PlanApprovalPending {
		return false, "plan_approval_pending", phase
	}
	switch phase {
	case kanban.WorkflowPhaseComplete:
		if o.isTerminalState(workflow, string(issue.State)) {
			return false, "terminal_state", phase
		}
		return false, "inactive_state", phase
	case kanban.WorkflowPhaseDone:
		if issue.State != kanban.StateDone {
			return false, "phase_state_mismatch", phase
		}
		return true, "", phase
	default:
		if !o.isActiveState(workflow, string(issue.State)) {
			return false, "inactive_state", phase
		}
		if issue.State == kanban.StateInReview && !workflow.Config.Phases.Review.Enabled {
			return false, "review_disabled", phase
		}
		blocked, err := o.isBlocked(issue, dispatchState)
		if err != nil {
			slog.Warn("Failed to determine issue blocker status", issueLogAttrs(issue, 0, "error", err)...)
			return false, "blocked_state_unknown", phase
		}
		if blocked {
			return false, "blocked", phase
		}
		return true, "", phase
	}
}

func (o *Orchestrator) projectAllowsDispatch(issue *kanban.Issue, dispatchState *kanban.IssueDispatchState) (bool, string) {
	if issue == nil || strings.TrimSpace(issue.ProjectID) == "" {
		return true, ""
	}
	if dispatchState != nil {
		if !dispatchState.ProjectExists {
			return false, "project_missing"
		}
		if dispatchState.ProjectState != kanban.ProjectStateRunning {
			return false, "project_stopped"
		}
		return true, ""
	}
	project, err := o.store.GetProject(issue.ProjectID)
	if err != nil {
		return false, "project_missing"
	}
	if project.State != kanban.ProjectStateRunning {
		return false, "project_stopped"
	}
	return true, ""
}

func failureRetryDelay(attempt, maxBackoffMs int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	delay := 10 * time.Second
	maxDelay := time.Duration(maxBackoffMs) * time.Millisecond
	if maxDelay <= 0 {
		maxDelay = 5 * time.Minute
	}
	if delay >= maxDelay {
		return maxDelay
	}
	for i := 1; i < attempt; i++ {
		if delay >= maxDelay/2 {
			return maxDelay
		}
		delay *= 2
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func pausesWithoutStateReset(errText string) bool {
	value := strings.TrimSpace(errText)
	switch {
	case value == "turn_input_required":
		return true
	case strings.Contains(value, "workspace_bootstrap"):
		return true
	default:
		return false
	}
}

func shouldScheduleSuccessfulContinuation(previousPhase, nextPhase kanban.WorkflowPhase, previousState, currentState kanban.State) bool {
	if nextPhase != previousPhase {
		return true
	}
	return previousState != currentState
}

func interruptedFailureStreak(events []kanban.RuntimeEvent) int {
	streak := 0
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		switch event.Kind {
		case "retry_scheduled", "run_started", "claim_released":
			continue
		case "retry_paused":
			if streak == 0 {
				if recovered := payloadInt(event.Payload, "consecutive_failures"); recovered > 0 {
					return recovered
				}
			}
			return streak
		case "manual_retry_requested", "run_completed":
			return streak
		case "run_interrupted":
			streak++
		case "run_failed", "run_unsuccessful":
			if isInterruptedRunError(event.Error) {
				streak++
				continue
			}
			return streak
		default:
			if streak > 0 {
				return streak
			}
		}
	}
	return streak
}

func (o *Orchestrator) interruptedFailureStreak(issueID string, limit int) (int, error) {
	events, err := o.store.ListIssueRuntimeEvents(issueID, limit)
	if err != nil {
		return 0, err
	}
	return interruptedFailureStreak(events), nil
}

func isInterruptedRunError(errText string) bool {
	switch strings.TrimSpace(errText) {
	case "stall_timeout", "turn_timeout", "read_timeout", "run_interrupted":
		return true
	default:
		return false
	}
}

func payloadInt(payload map[string]interface{}, key string) int {
	if payload == nil {
		return 0
	}
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func payloadString(payload map[string]interface{}, key string) string {
	if payload == nil {
		return ""
	}
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func payloadTime(payload map[string]interface{}, key string) time.Time {
	raw := payloadString(payload, key)
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func pausedEntryFromRuntimeEvent(event kanban.RuntimeEvent) pausedEntry {
	pausedAt := payloadTime(event.Payload, "paused_at")
	if pausedAt.IsZero() {
		pausedAt = event.TS
	}
	return pausedEntry{
		IssueState:          payloadString(event.Payload, "issue_state"),
		Attempt:             event.Attempt,
		Phase:               event.Phase,
		PausedAt:            pausedAt,
		Error:               event.Error,
		ConsecutiveFailures: payloadInt(event.Payload, "consecutive_failures"),
		PauseThreshold:      payloadInt(event.Payload, "pause_threshold"),
	}
}

func pausedLifecycleReset(issue *kanban.Issue, paused pausedEntry) bool {
	if issue == nil {
		return false
	}
	if paused.IssueState != "" && normalizeState(string(issue.State)) != normalizeState(paused.IssueState) {
		return true
	}
	if paused.Phase != "" && strings.TrimSpace(paused.Phase) != strings.TrimSpace(string(issue.WorkflowPhase)) {
		return true
	}
	return false
}

func automaticRetryLimit(workflow *config.Workflow) int {
	if workflow == nil {
		return config.DefaultConfig().Agent.MaxAutomaticRetries
	}
	if workflow.Config.Agent.MaxAutomaticRetries <= 0 {
		return config.DefaultConfig().Agent.MaxAutomaticRetries
	}
	return workflow.Config.Agent.MaxAutomaticRetries
}

func automaticRetryCount(events []kanban.RuntimeEvent) int {
	count := 0
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		switch event.Kind {
		case "manual_retry_requested", "retry_paused":
			return count
		case "run_completed":
			if _, ok := event.Payload["next_retry"]; !ok {
				return count
			}
		case "retry_scheduled":
			switch strings.TrimSpace(event.DelayType) {
			case "failure", "continuation":
				count++
			case "manual":
				return count
			}
		}
	}
	return count
}

func (o *Orchestrator) automaticRetryCountLocked(issueID string) (int, error) {
	events, err := o.store.ListIssueRuntimeEvents(issueID, automaticRetryHistoryLimit)
	if err != nil {
		return 0, err
	}
	return automaticRetryCount(events), nil
}

func (o *Orchestrator) cleanupTerminalWorkspaces(ctx context.Context) {
	o.syncProviderIssues(ctx)
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
		runtime, workflow, err := o.runtimeForIssue(&issues[i])
		if err != nil {
			slog.Warn("Skipping startup terminal workspace cleanup because runtime resolution failed",
				issueLogAttrs(&issues[i], -1, "error", err)...,
			)
			continue
		}
		if !o.shouldCleanupTerminalIssue(workflow, &issues[i]) {
			continue
		}
		o.cleanupTerminalAppServerProcess(&issues[i])
		if err := runtime.runner.CleanupWorkspace(ctx, &issues[i]); err != nil {
			slog.Warn("Failed to cleanup terminal workspace", "issue", issues[i].Identifier, "error", err)
		} else {
			slog.Info("Cleaned up terminal workspace",
				issueLogAttrs(&issues[i], -1)...,
			)
		}
	}
}

func (o *Orchestrator) cleanupTerminalAppServerProcess(issue *kanban.Issue) {
	if issue == nil {
		return
	}
	snapshot, err := o.store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		if err != sql.ErrNoRows {
			slog.Warn("Failed to load execution session before terminal cleanup",
				issueLogAttrs(issue, -1, "error", err)...,
			)
		}
		return
	}
	if snapshot == nil {
		return
	}
	pid, hasLivePID := o.liveAppServerPID(issue.ID)
	shouldRetire := hasLivePID || snapshot.AppSession.AppServerPID > 0 || snapshot.ResumeEligible
	if !shouldRetire {
		return
	}
	o.markAppServerRetired(issue.ID)
	if hasLivePID {
		cleanupLingering := appserver.CleanupLingeringAppServerProcess
		if hook := o.testHooks.cleanupLingeringAppServerProcess; hook != nil {
			cleanupLingering = hook
		}
		if err := cleanupLingering(pid); err != nil {
			o.unmarkAppServerRetired(issue.ID)
			slog.Warn("Failed to cleanup lingering app-server process",
				issueLogAttrs(issue, -1, "pid", pid, "error", err)...,
			)
			return
		}
	}
	snapshot.ResumeEligible = false
	snapshot.AppSession.AppServerPID = 0
	snapshot.UpdatedAt = time.Now().UTC()
	if err := o.store.UpsertIssueExecutionSession(*snapshot); err != nil {
		slog.Warn("Failed to retire app-server process metadata after terminal cleanup",
			issueLogAttrs(issue, -1, "pid", pid, "error", err)...,
		)
	}
}

func (o *Orchestrator) liveAppServerPID(issueID string) (int, bool) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return 0, false
	}
	o.mu.RLock()
	session := o.liveSessions[issueID]
	o.mu.RUnlock()
	if session == nil || session.AppServerPID <= 0 {
		return 0, false
	}
	return session.AppServerPID, true
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

func (o *Orchestrator) markAppServerRetired(issueID string) {
	if strings.TrimSpace(issueID) == "" {
		return
	}
	o.retiredAppServerMu.Lock()
	o.retiredAppServerIssues[issueID] = struct{}{}
	o.retiredAppServerMu.Unlock()
}

func (o *Orchestrator) unmarkAppServerRetired(issueID string) {
	if strings.TrimSpace(issueID) == "" {
		return
	}
	o.retiredAppServerMu.Lock()
	delete(o.retiredAppServerIssues, issueID)
	o.retiredAppServerMu.Unlock()
}

func (o *Orchestrator) appServerRetired(issueID string) bool {
	if strings.TrimSpace(issueID) == "" {
		return false
	}
	o.retiredAppServerMu.RLock()
	_, ok := o.retiredAppServerIssues[issueID]
	o.retiredAppServerMu.RUnlock()
	return ok
}

func (o *Orchestrator) stopAllRunsGracefully() {
	o.mu.RLock()
	type runningSnapshot struct {
		entry   runningEntry
		session *appserver.Session
	}
	runs := make([]runningSnapshot, 0, len(o.running))
	for issueID, entry := range o.running {
		var sessionCopy *appserver.Session
		if session := o.liveSessions[issueID]; session != nil {
			cp := cloneSessionWithIssue(session, issueID, entry.issue.Identifier)
			sessionCopy = &cp
		}
		runs = append(runs, runningSnapshot{
			entry:   entry,
			session: sessionCopy,
		})
	}
	o.mu.RUnlock()

	for _, run := range runs {
		issue := run.entry.issue
		_, workflow, err := o.runtimeForIssue(&issue)
		if err != nil {
			slog.Warn("Skipping graceful run marker because runtime resolution failed",
				issueLogAttrs(&issue, run.entry.attempt, "error", err)...,
			)
			continue
		}
		if !isAppServerWorkflow(workflow) {
			continue
		}
		resumeEligible := false
		if run.session != nil && strings.TrimSpace(run.session.ThreadID) != "" {
			resumeEligible = true
		} else if snapshot, err := o.store.GetIssueExecutionSession(issue.ID); err == nil && snapshot != nil && strings.TrimSpace(snapshot.AppSession.ThreadID) != "" {
			resumeEligible = true
		}
		o.persistExecutionSession(&issue, run.entry.phase, run.entry.attempt, "run_started", "", resumeEligible, gracefulShutdownStopReason, run.session)
	}

	o.stopAllRuns()
}

func (o *Orchestrator) stopAllRuns() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for issueID, entry := range o.running {
		entry.cancel()
		delete(o.running, issueID)
	}
}

func (o *Orchestrator) waitForActiveRuns(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		o.runWG.Wait()
		close(done)
	}()
	if timeout <= 0 {
		<-done
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
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

func (o *Orchestrator) clearPausedState(issueID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.paused, issueID)
}

func (o *Orchestrator) isBlocked(issue *kanban.Issue, dispatchState *kanban.IssueDispatchState) (bool, error) {
	if issue == nil {
		return false, nil
	}
	if dispatchState != nil {
		return dispatchState.HasUnresolvedBlockers, nil
	}
	state, err := o.issueDispatchState(issue)
	if err != nil {
		return false, err
	}
	return state.HasUnresolvedBlockers, nil
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
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	dbStats, err := o.store.DBStats()
	if err != nil {
		slog.Warn("Failed to collect database stats", "error", err)
	}

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
			"phase":      entry.Phase,
			"due_at":     entry.DueAt.UTC().Format(time.RFC3339),
			"error":      entry.Error,
			"delay_type": entry.DelayType,
		}
	}

	pausedQueue := make(map[string]interface{}, len(o.paused))
	for id, entry := range o.paused {
		pausedQueue[id] = map[string]interface{}{
			"attempt":              entry.Attempt,
			"phase":                entry.Phase,
			"paused_at":            entry.PausedAt.UTC().Format(time.RFC3339),
			"error":                entry.Error,
			"consecutive_failures": entry.ConsecutiveFailures,
			"pause_threshold":      entry.PauseThreshold,
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
		"paused_count":      len(o.paused),
		"paused":            pausedQueue,
		"events_count":      len(o.events),
		"last_event_seq":    o.eventSeq,
		"run_metrics": map[string]int{
			"total":      o.totalRuns,
			"successful": o.successfulRuns,
			"failed":     o.failedRuns,
		},
		"live_sessions":          o.copyLiveSessionsLocked(),
		"heap_alloc_bytes":       memStats.Alloc,
		"heap_sys_bytes":         memStats.HeapSys,
		"db_page_count":          dbStats.PageCount,
		"db_page_size":           dbStats.PageSize,
		"db_freelist_count":      dbStats.FreelistCount,
		"last_checkpoint_result": o.lastCheckpointResult,
	}
	if !o.lastMaintenanceAt.IsZero() {
		out["last_maintenance_at"] = o.lastMaintenanceAt.Format(time.RFC3339)
	}
	if !o.lastCheckpointAt.IsZero() {
		out["last_checkpoint_at"] = o.lastCheckpointAt.Format(time.RFC3339)
	}
	if workflow != nil {
		out["max_concurrent"] = workflow.Config.Agent.MaxConcurrentAgents
		out["dispatch_mode"] = dispatchMode(workflow)
		out["max_automatic_retries"] = workflow.Config.Agent.MaxAutomaticRetries
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
	runningEntries := make(map[string]runningEntry, len(o.running))
	retryEntries := make(map[string]retryEntry, len(o.retries))
	pausedEntries := make(map[string]pausedEntry, len(o.paused))
	liveSessions := make(map[string]*appserver.Session, len(o.liveSessions))
	for issueID, entry := range o.running {
		runningEntries[issueID] = entry
	}
	for issueID, entry := range o.retries {
		retryEntries[issueID] = entry
	}
	for issueID, entry := range o.paused {
		pausedEntries[issueID] = entry
	}
	for issueID, session := range o.liveSessions {
		if session == nil {
			continue
		}
		cp := session.Clone()
		liveSessions[issueID] = &cp
	}
	o.mu.RUnlock()

	snapshot := observability.Snapshot{
		GeneratedAt: time.Now().UTC(),
		Running:     make([]observability.RunningEntry, 0, len(runningEntries)),
		Retrying:    make([]observability.RetryEntry, 0, len(retryEntries)),
		Paused:      make([]observability.PausedEntry, 0, len(pausedEntries)),
		RateLimits:  nil,
	}
	if workflow != nil {
		snapshot.WorkspaceRoot = workflow.Config.Workspace.Root
	}

	for issueID, entry := range runningEntries {
		session := liveSessions[issueID]
		running := observability.RunningEntry{
			IssueID:    issueID,
			Identifier: entry.issue.Identifier,
			State:      string(entry.issue.State),
			Phase:      string(entry.phase),
			Attempt:    entry.attempt,
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

	for issueID, entry := range retryEntries {
		identifier := issueID
		if running, ok := runningEntries[issueID]; ok {
			identifier = running.issue.Identifier
		} else if issue, err := o.store.GetIssue(issueID); err == nil && issue != nil {
			identifier = issue.Identifier
		}
		retry := observability.RetryEntry{
			IssueID:    issueID,
			Identifier: identifier,
			Phase:      entry.Phase,
			Attempt:    entry.Attempt,
			DueAt:      entry.DueAt,
			DueInMs:    time.Until(entry.DueAt).Milliseconds(),
			Error:      entry.Error,
			DelayType:  entry.DelayType,
		}
		snapshot.Retrying = append(snapshot.Retrying, retry)
	}

	for issueID, entry := range pausedEntries {
		identifier := issueID
		if running, ok := runningEntries[issueID]; ok {
			identifier = running.issue.Identifier
		} else if issue, err := o.store.GetIssue(issueID); err == nil && issue != nil {
			identifier = issue.Identifier
		}
		snapshot.Paused = append(snapshot.Paused, observability.PausedEntry{
			IssueID:             issueID,
			Identifier:          identifier,
			Phase:               entry.Phase,
			Attempt:             entry.Attempt,
			PausedAt:            entry.PausedAt,
			Error:               entry.Error,
			ConsecutiveFailures: entry.ConsecutiveFailures,
			PauseThreshold:      entry.PauseThreshold,
		})
	}

	sort.Slice(snapshot.Running, func(i, j int) bool {
		return snapshot.Running[i].Identifier < snapshot.Running[j].Identifier
	})
	sort.Slice(snapshot.Retrying, func(i, j int) bool {
		return snapshot.Retrying[i].Identifier < snapshot.Retrying[j].Identifier
	})
	sort.Slice(snapshot.Paused, func(i, j int) bool {
		return snapshot.Paused[i].Identifier < snapshot.Paused[j].Identifier
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

func (o *Orchestrator) PendingInterrupts() appserver.PendingInteractionSnapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()

	current := o.currentPendingInteractionLocked()
	snapshot := appserver.PendingInteractionSnapshot{
		Count: len(o.pendingInteractions),
	}
	if current != nil {
		cloned := current.Clone()
		snapshot.Current = &cloned
	}
	return snapshot
}

func (o *Orchestrator) PendingInterruptForIssue(issueID, identifier string) (*appserver.PendingInteraction, bool) {
	issueID = strings.TrimSpace(issueID)
	identifier = strings.TrimSpace(identifier)

	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, interactionID := range o.pendingInteractionOrder {
		entry, ok := o.pendingInteractions[interactionID]
		if !ok {
			continue
		}
		interaction := entry.interaction
		if interaction.IssueID == issueID || interaction.IssueIdentifier == identifier {
			cloned := interaction.Clone()
			return &cloned, true
		}
	}
	return nil, false
}

func (o *Orchestrator) RespondToInterrupt(ctx context.Context, interactionID string, response appserver.PendingInteractionResponse) error {
	o.mu.RLock()
	entry, ok := o.pendingInteractions[strings.TrimSpace(interactionID)]
	o.mu.RUnlock()
	if !ok {
		return appserver.ErrPendingInteractionNotFound
	}
	if entry.respond == nil {
		return appserver.ErrPendingInteractionConflict
	}
	return entry.respond(ctx, interactionID, response)
}

func (o *Orchestrator) registerPendingInteraction(issueID string, interaction *appserver.PendingInteraction, responder appserver.InteractionResponder) {
	if interaction == nil || strings.TrimSpace(issueID) == "" {
		return
	}

	shouldBroadcast := false
	o.mu.Lock()
	entry, ok := o.running[issueID]
	if ok {
		enriched := interaction.Clone()
		enriched.IssueID = issueID
		enriched.IssueIdentifier = entry.issue.Identifier
		enriched.IssueTitle = entry.issue.Title
		enriched.Phase = string(entry.phase)
		enriched.Attempt = entry.attempt
		if strings.TrimSpace(enriched.LastActivity) == "" {
			if session := o.liveSessions[issueID]; session != nil {
				enriched.LastActivity = strings.TrimSpace(session.LastMessage)
				if !session.LastTimestamp.IsZero() {
					ts := session.LastTimestamp.UTC()
					enriched.LastActivityAt = &ts
				}
			}
		}
		if _, exists := o.pendingInteractions[enriched.ID]; !exists {
			o.pendingInteractionOrder = append(o.pendingInteractionOrder, enriched.ID)
		}
		o.pendingInteractions[enriched.ID] = pendingInteractionEntry{
			interaction: enriched,
			respond:     responder,
		}
		shouldBroadcast = true
	}
	o.mu.Unlock()

	if shouldBroadcast {
		observability.BroadcastUpdate()
	}
}

func (o *Orchestrator) clearPendingInteraction(issueID string, interactionID string) {
	if strings.TrimSpace(interactionID) == "" {
		return
	}

	shouldBroadcast := false
	o.mu.Lock()
	if entry, ok := o.pendingInteractions[interactionID]; ok {
		if issueID == "" || entry.interaction.IssueID == issueID {
			delete(o.pendingInteractions, interactionID)
			o.pendingInteractionOrder = filterPendingInteractionOrder(o.pendingInteractionOrder, interactionID)
			shouldBroadcast = true
		}
	}
	o.mu.Unlock()

	if shouldBroadcast {
		observability.BroadcastUpdate()
	}
}

func (o *Orchestrator) clearPendingInteractionsForIssue(issueID string) {
	if strings.TrimSpace(issueID) == "" {
		return
	}

	shouldBroadcast := false
	o.mu.Lock()
	shouldBroadcast = o.clearPendingInteractionsForIssueLocked(issueID)
	o.mu.Unlock()

	if shouldBroadcast {
		observability.BroadcastUpdate()
	}
}

func (o *Orchestrator) clearPendingInteractionsForIssueLocked(issueID string) bool {
	if len(o.pendingInteractions) == 0 {
		return false
	}
	remainingOrder := make([]string, 0, len(o.pendingInteractionOrder))
	removed := false
	for _, interactionID := range o.pendingInteractionOrder {
		entry, ok := o.pendingInteractions[interactionID]
		if !ok {
			continue
		}
		if entry.interaction.IssueID == issueID {
			delete(o.pendingInteractions, interactionID)
			removed = true
			continue
		}
		remainingOrder = append(remainingOrder, interactionID)
	}
	o.pendingInteractionOrder = remainingOrder
	return removed
}

func (o *Orchestrator) currentPendingInteractionLocked() *appserver.PendingInteraction {
	for _, interactionID := range o.pendingInteractionOrder {
		entry, ok := o.pendingInteractions[interactionID]
		if !ok {
			continue
		}
		interaction := entry.interaction.Clone()
		return &interaction
	}
	return nil
}

func filterPendingInteractionOrder(order []string, removeID string) []string {
	if len(order) == 0 {
		return order
	}
	out := make([]string, 0, len(order))
	for _, interactionID := range order {
		if interactionID == removeID {
			continue
		}
		out = append(out, interactionID)
	}
	return out
}

func (o *Orchestrator) updateLiveSession(issueID string, session *appserver.Session) {
	if session == nil {
		return
	}
	cp := session.Clone()

	o.mu.Lock()
	entry, ok := o.running[issueID]
	if !ok {
		o.mu.Unlock()
		return
	}
	cp.IssueID = issueID
	cp.IssueIdentifier = entry.issue.Identifier
	o.liveSessions[issueID] = &cp
	shouldPersist := o.shouldPersistLiveSessionLocked(issueID, &cp)
	o.mu.Unlock()

	if shouldPersist {
		issue := entry.issue
		o.persistExecutionSession(&issue, entry.phase, entry.attempt, "run_started", "", false, "", &cp)
	}
	o.observeIssueTokenSpend(issueID, &cp)
	if o.flushIssueTokenSpend(issueID, false) {
		observability.BroadcastUpdate()
	}
}

func (o *Orchestrator) updateIssueActivity(issueID string, event appserver.ActivityEvent) {
	o.mu.RLock()
	entry, ok := o.running[issueID]
	o.mu.RUnlock()
	if !ok {
		return
	}
	if err := o.store.ApplyIssueActivityEvent(issueID, entry.issue.Identifier, entry.attempt, event); err != nil {
		slog.Warn("Failed to persist issue activity event",
			"issue_id", issueID,
			"issue_identifier", entry.issue.Identifier,
			"attempt", entry.attempt,
			"event_type", event.Type,
			"error", err,
		)
	}
}

func (o *Orchestrator) shouldPersistLiveSessionLocked(issueID string, session *appserver.Session) bool {
	o.sessionWriteMu.Lock()
	defer o.sessionWriteMu.Unlock()
	now := time.Now().UTC()
	state, ok := o.sessionWrites[issueID]
	if !ok {
		return true
	}
	if state.SessionID == "" && strings.TrimSpace(session.SessionID) != "" {
		return true
	}
	if strings.TrimSpace(session.SessionID) != "" && state.SessionID != session.SessionID {
		return true
	}
	if session.Terminal && (!state.Terminal || state.TerminalReason != session.TerminalReason) {
		return true
	}
	if state.LastEvent == session.LastEvent &&
		state.LastTimestamp.Equal(session.LastTimestamp) &&
		state.Terminal == session.Terminal &&
		state.TerminalReason == session.TerminalReason {
		return false
	}
	return now.Sub(state.LastPersistedAt) >= liveSessionPersistInterval
}

func (o *Orchestrator) clearSessionWriteState(issueID string) {
	o.sessionWriteMu.Lock()
	defer o.sessionWriteMu.Unlock()
	delete(o.sessionWrites, issueID)
}

func (o *Orchestrator) observeIssueTokenSpend(issueID string, session *appserver.Session) {
	if session == nil || session.TotalTokens <= 0 {
		return
	}
	runKey := issueTokenSpendRunKey(session)
	now := time.Now().UTC()

	o.tokenSpendMu.Lock()
	defer o.tokenSpendMu.Unlock()

	state := o.tokenSpends[issueID]
	if runKey == "" {
		if session.TotalTokens > state.LastUnnamedTotal {
			if state.PendingDelta == 0 {
				state.PendingSince = now
			}
			state.PendingDelta += session.TotalTokens - state.LastUnnamedTotal
			state.LastUnnamedTotal = session.TotalTokens
		}
		o.tokenSpends[issueID] = state
		return
	}
	if state.LastSeenTotals == nil {
		state.LastSeenTotals = make(map[string]int)
	}
	if session.TotalTokens > state.LastSeenTotals[runKey] {
		if state.PendingDelta == 0 {
			state.PendingSince = now
		}
		state.PendingDelta += session.TotalTokens - state.LastSeenTotals[runKey]
		state.LastSeenTotals[runKey] = session.TotalTokens
	}
	o.tokenSpends[issueID] = state
}

func issueTokenSpendRunKey(session *appserver.Session) string {
	if session == nil {
		return ""
	}
	if threadID := strings.TrimSpace(session.ThreadID); threadID != "" {
		return "thread:" + threadID
	}
	if sessionID := strings.TrimSpace(session.SessionID); sessionID != "" {
		return "session:" + sessionID
	}
	return ""
}

func (o *Orchestrator) persistFinalIssueTokenSpend(issueID string, session *appserver.Session) {
	if session == nil || session.TotalTokens <= 0 {
		return
	}
	o.observeIssueTokenSpend(issueID, session)
	o.flushIssueTokenSpend(issueID, true)
}

func (o *Orchestrator) restoreIssueTokenSpend(issueID string, delta int) {
	if delta <= 0 {
		return
	}
	o.tokenSpendMu.Lock()
	defer o.tokenSpendMu.Unlock()
	state := o.tokenSpends[issueID]
	state.PendingDelta += delta
	state.PendingSince = time.Now().UTC()
	state.LastFlushedAt = time.Time{}
	o.tokenSpends[issueID] = state
}

func (o *Orchestrator) flushIssueTokenSpend(issueID string, force bool) bool {
	o.tokenSpendMu.Lock()
	state, ok := o.tokenSpends[issueID]
	if !ok || state.PendingDelta <= 0 {
		o.tokenSpendMu.Unlock()
		return false
	}
	if !force && !state.PendingSince.IsZero() && time.Since(state.PendingSince) < liveTokenSpendFlushInterval {
		o.tokenSpendMu.Unlock()
		return false
	}
	pending := state.PendingDelta
	state.PendingDelta = 0
	state.PendingSince = time.Time{}
	state.LastFlushedAt = time.Now().UTC()
	o.tokenSpends[issueID] = state
	o.tokenSpendMu.Unlock()

	if err := o.store.AddIssueTokenSpend(issueID, pending); err != nil && err != sql.ErrNoRows {
		slog.Warn("Failed to flush issue token spend", "issue_id", issueID, "delta", pending, "error", err)
		o.restoreIssueTokenSpend(issueID, pending)
		return false
	}
	return true
}

func (o *Orchestrator) clearIssueTokenSpendState(issueID string) {
	o.tokenSpendMu.Lock()
	defer o.tokenSpendMu.Unlock()
	delete(o.tokenSpends, issueID)
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
		cp := summarizeSessionWithIssue(session, issueID, entry.issue.Identifier)
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

func (o *Orchestrator) RequestProjectRefresh(projectID string) map[string]interface{} {
	project, err := o.store.GetProject(projectID)
	if err != nil {
		return map[string]interface{}{
			"status":     "not_found",
			"project_id": projectID,
		}
	}
	if err := o.store.UpdateProjectState(project.ID, kanban.ProjectStateRunning); err != nil {
		return map[string]interface{}{
			"status":       "error",
			"project_id":   project.ID,
			"project_name": project.Name,
			"error":        err.Error(),
		}
	}
	project.State = kanban.ProjectStateRunning
	o.mu.Lock()
	o.appendEventLocked("project_refresh_requested", map[string]interface{}{
		"project_id":   project.ID,
		"project_name": project.Name,
		"state":        string(project.State),
	})
	o.mu.Unlock()
	return map[string]interface{}{
		"status":       "accepted",
		"project_id":   project.ID,
		"project_name": project.Name,
		"state":        string(project.State),
		"requested_at": time.Now().UTC().Format(time.RFC3339),
	}
}

func (o *Orchestrator) StopProjectRuns(projectID string) map[string]interface{} {
	project, err := o.store.GetProject(projectID)
	if err != nil {
		return map[string]interface{}{
			"status":     "not_found",
			"project_id": projectID,
		}
	}
	if err := o.store.UpdateProjectState(project.ID, kanban.ProjectStateStopped); err != nil {
		return map[string]interface{}{
			"status":       "error",
			"project_id":   project.ID,
			"project_name": project.Name,
			"error":        err.Error(),
		}
	}
	project.State = kanban.ProjectStateStopped

	stopped := 0
	identifiers := make([]string, 0)
	o.mu.Lock()
	for issueID, entry := range o.running {
		if entry.issue.ProjectID != projectID {
			continue
		}
		entry.cancel()
		delete(o.running, issueID)
		stopped++
		identifiers = append(identifiers, entry.issue.Identifier)
		o.appendEventLocked("run_stopped", map[string]interface{}{
			"issue_id":   issueID,
			"identifier": entry.issue.Identifier,
			"project_id": projectID,
		})
	}
	o.appendEventLocked("project_stop_requested", map[string]interface{}{
		"project_id":   projectID,
		"project_name": project.Name,
		"state":        string(project.State),
		"stopped_runs": stopped,
		"identifiers":  identifiers,
	})
	o.mu.Unlock()

	return map[string]interface{}{
		"status":       "stopped",
		"project_id":   projectID,
		"project_name": project.Name,
		"state":        string(project.State),
		"stopped_runs": stopped,
		"identifiers":  identifiers,
		"requested_at": time.Now().UTC().Format(time.RFC3339),
	}
}

func (o *Orchestrator) RetryIssueNow(ctx context.Context, identifier string) map[string]interface{} {
	issue, err := o.service.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		status := "error"
		if errors.Is(err, sql.ErrNoRows) || kanban.IsNotFound(err) {
			status = "not_found"
		}
		return map[string]interface{}{
			"status": status,
			"issue":  identifier,
			"error":  err.Error(),
		}
	}

	if issue.State == kanban.StateDone || issue.State == kanban.StateCancelled {
		if issue.WorkflowPhase != kanban.WorkflowPhaseDone || issue.State != kanban.StateDone {
			detail, err := o.service.SetIssueState(ctx, issue.Identifier, string(kanban.StateReady))
			if err != nil {
				return map[string]interface{}{
					"status": "error",
					"error":  err.Error(),
					"issue":  identifier,
				}
			}
			issue = &detail.Issue
		}
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if entry, ok := o.retries[issue.ID]; ok {
		entry.DueAt = time.Now().UTC()
		entry.ResumeThreadID = ""
		o.retries[issue.ID] = entry
		delete(o.paused, issue.ID)
		o.appendEventLocked("manual_retry_requested", map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      entry.Phase,
		})
		return map[string]interface{}{
			"status":       "queued_now",
			"issue":        identifier,
			"retry_due_at": entry.DueAt.Format(time.RFC3339),
		}
	}

	if entry, ok := o.paused[issue.ID]; ok {
		dueAt := time.Now().UTC()
		o.retries[issue.ID] = retryEntry{
			Attempt:   entry.Attempt,
			Phase:     entry.Phase,
			DueAt:     dueAt,
			Error:     entry.Error,
			DelayType: "manual",
		}
		o.claimed[issue.ID] = struct{}{}
		delete(o.paused, issue.ID)
		o.appendEventLocked("manual_retry_requested", map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      entry.Phase,
		})
		return map[string]interface{}{
			"status":       "queued_now",
			"issue":        identifier,
			"retry_due_at": dueAt.Format(time.RFC3339),
		}
	}

	if issue.WorkflowPhase == kanban.WorkflowPhaseDone && issue.State == kanban.StateDone {
		o.retries[issue.ID] = retryEntry{
			Attempt:   0,
			Phase:     string(kanban.WorkflowPhaseDone),
			DueAt:     time.Now().UTC(),
			DelayType: "manual",
		}
		o.claimed[issue.ID] = struct{}{}
		o.appendEventLocked("manual_retry_requested", map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      string(kanban.WorkflowPhaseDone),
		})
		return map[string]interface{}{
			"status":       "queued_now",
			"issue":        identifier,
			"retry_due_at": time.Now().UTC().Format(time.RFC3339),
		}
	}

	o.appendEventLocked("manual_retry_requested", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      string(issue.WorkflowPhase),
	})
	return map[string]interface{}{
		"status": "refresh_requested",
		"issue":  identifier,
	}
}

func (o *Orchestrator) RunRecurringIssueNow(ctx context.Context, identifier string) map[string]interface{} {
	issue, err := o.service.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		status := "error"
		if errors.Is(err, sql.ErrNoRows) || kanban.IsNotFound(err) {
			status = "not_found"
		}
		return map[string]interface{}{
			"status": status,
			"issue":  identifier,
			"error":  err.Error(),
		}
	}
	if !issue.IsRecurring() {
		return map[string]interface{}{
			"status": "not_recurring",
			"issue":  identifier,
		}
	}

	_, workflow, err := o.runtimeForIssue(issue)
	if err != nil {
		return map[string]interface{}{
			"status": "error",
			"issue":  identifier,
			"error":  err.Error(),
		}
	}

	if o.recurringIssueOccupied(workflow, issue) {
		if issue.PendingRerun {
			return map[string]interface{}{
				"status": "pending_rerun_already_set",
				"issue":  identifier,
			}
		}
		if !o.recordRecurringPendingRerun(issue, "manual_run_now") {
			return map[string]interface{}{
				"status": "error",
				"issue":  identifier,
				"error":  "failed to record pending rerun",
			}
		}
		return map[string]interface{}{
			"status": "pending_rerun_recorded",
			"issue":  identifier,
		}
	}

	if !o.enqueueRecurringIssue(issue, "recurring_manual_run_now_enqueued", true) {
		return map[string]interface{}{
			"status": "error",
			"issue":  identifier,
			"error":  "failed to enqueue recurring issue",
		}
	}
	return map[string]interface{}{
		"status":      "queued_now",
		"issue":       identifier,
		"enqueued_at": time.Now().UTC().Format(time.RFC3339),
	}
}

func attachResultMetrics(fields map[string]interface{}, result *agent.RunResult) {
	if result == nil || result.AppSession == nil {
		return
	}
	fields["input_tokens"] = result.AppSession.InputTokens
	fields["output_tokens"] = result.AppSession.OutputTokens
	fields["total_tokens"] = result.AppSession.TotalTokens
	if threadID := strings.TrimSpace(result.AppSession.ThreadID); threadID != "" {
		fields["thread_id"] = threadID
	}
	if sessionID := strings.TrimSpace(result.AppSession.SessionID); sessionID != "" {
		fields["session_id"] = sessionID
	}
}

func (o *Orchestrator) persistExecutionSession(issue *kanban.Issue, phase kanban.WorkflowPhase, attempt int, runKind, errText string, resumeEligible bool, stopReason string, session *appserver.Session) {
	if issue == nil {
		return
	}
	now := time.Now().UTC()
	snapshot := kanban.ExecutionSessionSnapshot{
		IssueID:        issue.ID,
		Identifier:     issue.Identifier,
		Phase:          string(phase),
		Attempt:        attempt,
		RunKind:        runKind,
		Error:          errText,
		ResumeEligible: resumeEligible,
		StopReason:     stopReason,
		UpdatedAt:      now,
	}
	if session != nil {
		snapshot.AppSession = summarizeSessionWithIssue(session, issue.ID, issue.Identifier)
	} else {
		if existing, err := o.store.GetIssueExecutionSession(issue.ID); err == nil && existing != nil {
			snapshot.AppSession = existing.AppSession
		}
		snapshot.AppSession.IssueID = issue.ID
		snapshot.AppSession.IssueIdentifier = issue.Identifier
	}
	if o.appServerRetired(issue.ID) {
		snapshot.AppSession.AppServerPID = 0
		snapshot.ResumeEligible = false
	}
	if err := o.store.UpsertIssueExecutionSession(snapshot); err != nil {
		slog.Warn("Failed to persist issue execution session", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "error", err)
		return
	}
	o.sessionWriteMu.Lock()
	o.sessionWrites[issue.ID] = sessionPersistenceState{
		LastPersistedAt: now,
		SessionID:       snapshot.AppSession.SessionID,
		LastEvent:       snapshot.AppSession.LastEvent,
		LastTimestamp:   snapshot.AppSession.LastTimestamp,
		TerminalReason:  snapshot.AppSession.TerminalReason,
		Terminal:        snapshot.AppSession.Terminal,
	}
	o.sessionWriteMu.Unlock()
}

func (o *Orchestrator) persistExecutionSessionSnapshot(issue *kanban.Issue, phase kanban.WorkflowPhase, attempt int, runKind, errText string, result *agent.RunResult) {
	if result == nil || result.AppSession == nil {
		return
	}
	o.persistExecutionSession(issue, phase, attempt, runKind, errText, false, "", result.AppSession)
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
	cp := session.Clone()
	cp.IssueID = issueID
	cp.IssueIdentifier = identifier
	return cp
}

func summarizeSessionWithIssue(session *appserver.Session, issueID, identifier string) appserver.Session {
	cp := session.Summary()
	cp.IssueID = issueID
	cp.IssueIdentifier = identifier
	return cp
}
