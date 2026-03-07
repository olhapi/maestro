package orchestrator

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/olhapi/symphony-go/internal/agent"
	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/pkg/config"
)

// Orchestrator manages the polling and dispatch of issues to agents
type Orchestrator struct {
	store    *kanban.Store
	workflow *config.Workflow
	runner   *agent.Runner

	mu          sync.RWMutex
	activeRuns  map[string]context.CancelFunc
	retryQueue  map[string]int // issue_id -> retry count
	maxRetries  int
}

// New creates a new orchestrator
func New(store *kanban.Store, workflow *config.Workflow) *Orchestrator {
	return &Orchestrator{
		store:      store,
		workflow:   workflow,
		runner:     agent.NewRunner(workflow, store),
		activeRuns: make(map[string]context.CancelFunc),
		retryQueue: make(map[string]int),
		maxRetries: 3,
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
	// Get current states for all active runs
	o.mu.RLock()
	defer o.mu.RUnlock()

	for issueID := range o.activeRuns {
		issue, err := o.store.GetIssue(issueID)
		if err != nil {
			continue
		}

		// Check if issue is still in an active state
		if !o.isActiveState(string(issue.State)) {
			slog.Info("Stopping run for issue (state changed)", "issue", issue.Identifier, "state", issue.State)
			o.stopRun(issueID)
		}

		// Check if issue is blocked
		if len(issue.BlockedBy) > 0 {
			for _, blocker := range issue.BlockedBy {
				blockerIssue, err := o.store.GetIssueByIdentifier(blocker)
				if err == nil && !o.isTerminalState(string(blockerIssue.State)) {
					slog.Info("Stopping run for issue (blocked)", "issue", issue.Identifier, "blocked_by", blocker)
					o.stopRun(issueID)
					break
				}
			}
		}
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
		if err != nil {
			slog.Error("Run failed", "issue", issue.Identifier, "error", err)
			o.retryQueue[issue.ID]++
			return
		}

		if result.Success {
			slog.Info("Run completed", "issue", issue.Identifier)
			delete(o.retryQueue, issue.ID)
		} else {
			slog.Error("Run unsuccessful", "issue", issue.Identifier, "error", result.Error)
			o.retryQueue[issue.ID]++
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

	return map[string]interface{}{
		"active_runs":   len(o.activeRuns),
		"active_issues": activeIDs,
		"retry_queue":   len(o.retryQueue),
		"max_concurrent": o.workflow.Config.MaxConcurrent,
	}
}
