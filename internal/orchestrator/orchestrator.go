package orchestrator

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/olhapi/symphony-go/internal/appserver"
	"github.com/olhapi/symphony-go/internal/agent"
	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/pkg/config"
)

// Orchestrator manages the polling and dispatch of issues to agents
type Orchestrator struct {
	store    *kanban.Store
	workflow *config.Workflow
	runner   *agent.Runner

	mu             sync.RWMutex
	activeRuns     map[string]context.CancelFunc
	retryQueue     map[string]int // issue_id -> retry count
	maxRetries     int
	startedAt      time.Time
	lastTickAt     time.Time
	totalRuns      int
	successfulRuns int
	failedRuns     int
	liveSessions   map[string]*appserver.Session
}

// New creates a new orchestrator
func New(store *kanban.Store, workflow *config.Workflow) *Orchestrator {
	return &Orchestrator{
		store:      store,
		workflow:   workflow,
		runner:     agent.NewRunner(workflow, store),
		activeRuns: make(map[string]context.CancelFunc),
		retryQueue: make(map[string]int),
		maxRetries:   3,
		startedAt:    time.Now().UTC(),
		liveSessions: make(map[string]*appserver.Session),
	}
}

// Run starts the orchestrator loop
func (o *Orchestrator) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Duration(o.workflow.Config.PollInterval) * time.Second)
	defer ticker.Stop()

	slog.Info("Orchestrator started", "poll_interval", o.workflow.Config.PollInterval)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Orchestrator stopped (context cancelled)")
			return ctx.Err()
		case <-sigChan:
			slog.Info("Orchestrator stopping (signal received)")
			o.stopAllRuns()
			return nil
		case <-ticker.C:
			if err := o.tick(ctx); err != nil {
				slog.Error("Tick failed", "error", err)
			}
		}
	}
}

func (o *Orchestrator) tick(ctx context.Context) error {
	o.mu.Lock()
	o.lastTickAt = time.Now().UTC()
	o.mu.Unlock()

	// Reconcile - check if any active runs should be stopped
	if err := o.reconcile(); err != nil {
		slog.Error("Reconciliation failed", "error", err)
	}

	// Dispatch new runs
	if err := o.dispatch(ctx); err != nil {
		slog.Error("Dispatch failed", "error", err)
	}

	// Process retry queue
	o.processRetries(ctx)

	return nil
}

func (o *Orchestrator) reconcile() error {
	// Snapshot active issue IDs first
	o.mu.RLock()
	ids := make([]string, 0, len(o.activeRuns))
	for issueID := range o.activeRuns {
		ids = append(ids, issueID)
	}
	o.mu.RUnlock()

	toStop := make([]string, 0)
	for _, issueID := range ids {
		issue, err := o.store.GetIssue(issueID)
		if err != nil {
			continue
		}

		if !o.isActiveState(string(issue.State)) {
			slog.Info("Stopping run for issue (state changed)", "issue", issue.Identifier, "state", issue.State)
			toStop = append(toStop, issueID)
			continue
		}

		if len(issue.BlockedBy) > 0 {
			for _, blocker := range issue.BlockedBy {
				blockerIssue, err := o.store.GetIssueByIdentifier(blocker)
				if err == nil && !o.isTerminalState(string(blockerIssue.State)) {
					slog.Info("Stopping run for issue (blocked)", "issue", issue.Identifier, "blocked_by", blocker)
					toStop = append(toStop, issueID)
					break
				}
			}
		}
	}

	if len(toStop) > 0 {
		o.mu.Lock()
		for _, issueID := range toStop {
			o.stopRun(issueID)
		}
		o.mu.Unlock()
	}
	return nil
}

func (o *Orchestrator) dispatch(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Check capacity
	if len(o.activeRuns) >= o.workflow.Config.MaxConcurrent {
		return nil
	}

	// Get issues in active states (prioritizing "ready")
	issues, err := o.store.ListIssues(map[string]interface{}{
		"state": string(kanban.StateReady),
	})
	if err != nil {
		return err
	}

	// Also get in_progress issues that aren't running
	inProgress, err := o.store.ListIssues(map[string]interface{}{
		"state": string(kanban.StateInProgress),
	})
	if err == nil {
		for _, issue := range inProgress {
			if _, running := o.activeRuns[issue.ID]; !running {
				issues = append(issues, issue)
			}
		}
	}

	// Filter out blocked issues
	var eligible []kanban.Issue
	for _, issue := range issues {
		if o.isBlocked(issue) {
			continue
		}
		eligible = append(eligible, issue)
	}

	// Dispatch up to capacity
	available := o.workflow.Config.MaxConcurrent - len(o.activeRuns)
	for i := 0; i < len(eligible) && i < available; i++ {
		issue := eligible[i]
		o.startRun(ctx, &issue)
	}

	return nil
}

func (o *Orchestrator) isBlocked(issue kanban.Issue) bool {
	for _, blocker := range issue.BlockedBy {
		blockerIssue, err := o.store.GetIssueByIdentifier(blocker)
		if err != nil {
			continue
		}
		if !o.isTerminalState(string(blockerIssue.State)) {
			return true
		}
	}
	return false
}

func (o *Orchestrator) startRun(ctx context.Context, issue *kanban.Issue) {
	runCtx, cancel := context.WithCancel(ctx)
	o.activeRuns[issue.ID] = cancel

	slog.Info("Starting run", "issue", issue.Identifier, "title", issue.Title)

	go func() {
		defer func() {
			o.mu.Lock()
			delete(o.activeRuns, issue.ID)
			o.mu.Unlock()
		}()

		result, err := o.runner.Run(runCtx, issue)
		o.mu.Lock()
		o.totalRuns++
		o.mu.Unlock()
		if err != nil {
			slog.Error("Run failed", "issue", issue.Identifier, "error", err)
			o.mu.Lock()
			o.retryQueue[issue.ID]++
			o.failedRuns++
			o.mu.Unlock()
			return
		}

		if result.AppSession != nil {
			o.mu.Lock()
			o.liveSessions[issue.ID] = result.AppSession
			o.mu.Unlock()
		}

		if result.Success {
			slog.Info("Run completed", "issue", issue.Identifier)
			o.mu.Lock()
			delete(o.retryQueue, issue.ID)
			o.successfulRuns++
			o.mu.Unlock()
		} else {
			slog.Error("Run unsuccessful", "issue", issue.Identifier, "error", result.Error)
			o.mu.Lock()
			o.retryQueue[issue.ID]++
			o.failedRuns++
			o.mu.Unlock()
		}
	}()
}

func (o *Orchestrator) stopRun(issueID string) {
	if cancel, ok := o.activeRuns[issueID]; ok {
		cancel()
		delete(o.activeRuns, issueID)
	}
}

func (o *Orchestrator) stopAllRuns() {
	o.mu.Lock()
	defer o.mu.Unlock()

	for _, cancel := range o.activeRuns {
		cancel()
	}
	o.activeRuns = make(map[string]context.CancelFunc)
}

func (o *Orchestrator) processRetries(ctx context.Context) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Simple retry logic - move failed issues back to ready state
	for issueID, retries := range o.retryQueue {
		if retries >= o.maxRetries {
			slog.Warn("Max retries exceeded, moving to backlog", "issue_id", issueID)
			_ = o.store.UpdateIssueState(issueID, kanban.StateBacklog)
			delete(o.retryQueue, issueID)
			continue
		}

		// Reset to ready for retry
		_ = o.store.UpdateIssueState(issueID, kanban.StateReady)
	}
}

func (o *Orchestrator) isActiveState(state string) bool {
	for _, s := range o.workflow.Config.ActiveStates {
		if s == state {
			return true
		}
	}
	return false
}

func (o *Orchestrator) isTerminalState(state string) bool {
	for _, s := range o.workflow.Config.TerminalStates {
		if s == state {
			return true
		}
	}
	return false
}

// Status returns the current orchestrator status
func (o *Orchestrator) Status() map[string]interface{} {
	o.mu.RLock()
	defer o.mu.RUnlock()

	activeIDs := make([]string, 0, len(o.activeRuns))
	for id := range o.activeRuns {
		activeIDs = append(activeIDs, id)
	}

	retryByIssue := make(map[string]int, len(o.retryQueue))
	for k, v := range o.retryQueue {
		retryByIssue[k] = v
	}
	live := make(map[string]*appserver.Session, len(o.liveSessions))
	for k, v := range o.liveSessions {
		cp := *v
		cp.History = append([]appserver.Event(nil), v.History...)
		live[k] = &cp
	}

	uptimeSec := int(time.Since(o.startedAt).Seconds())
	lastTick := ""
	if !o.lastTickAt.IsZero() {
		lastTick = o.lastTickAt.Format(time.RFC3339)
	}

	return map[string]interface{}{
		"started_at":         o.startedAt.Format(time.RFC3339),
		"uptime_seconds":     uptimeSec,
		"last_tick_at":       lastTick,
		"active_runs":        len(o.activeRuns),
		"active_issues":      activeIDs,
		"retry_queue_count":  len(o.retryQueue),
		"retry_queue":        retryByIssue,
		"max_concurrent":     o.workflow.Config.MaxConcurrent,
		"poll_interval_sec":  o.workflow.Config.PollInterval,
		"active_states":      o.workflow.Config.ActiveStates,
		"terminal_states":    o.workflow.Config.TerminalStates,
		"run_metrics": map[string]int{
			"total":      o.totalRuns,
			"successful": o.successfulRuns,
			"failed":     o.failedRuns,
		},
		"live_sessions": live,
	}
}

func (o *Orchestrator) LiveSessions() map[string]interface{} {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make(map[string]interface{}, len(o.liveSessions))
	for issueID, s := range o.liveSessions {
		cp := *s
		cp.History = append([]appserver.Event(nil), s.History...)
		out[issueID] = cp
	}
	return map[string]interface{}{"sessions": out}
}
