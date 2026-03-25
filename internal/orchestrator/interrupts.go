package orchestrator

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

const (
	issueProjectDispatchBlockedAlertCode     = "project_dispatch_blocked"
	issueProjectDispatchBlockedAlertIDPrefix = "alert-issue-dispatch-"
	issueProjectDispatchBlockedAlertMethod   = "maestro/issueProjectDispatchBlocked"
)

var derivedAlertCandidateStates = []string{
	string(kanban.StateReady),
	string(kanban.StateInProgress),
	string(kanban.StateInReview),
	string(kanban.StateDone),
}

func (o *Orchestrator) AcknowledgeInterrupt(ctx context.Context, interactionID string) error {
	_ = ctx

	if interaction, found := o.queuedPendingInteractionByID(interactionID); found {
		if !interaction.HasAction(appserver.PendingInteractionActionAcknowledge) {
			return appserver.ErrInvalidInteractionResponse
		}
		if err := o.store.AcknowledgeInterrupt(strings.TrimSpace(interactionID)); err != nil {
			return err
		}
		observability.BroadcastUpdate()
		return nil
	}

	interaction, found, err := o.derivedPendingInteractionByID(interactionID)
	if err != nil {
		return err
	}
	if !found {
		return appserver.ErrPendingInteractionNotFound
	}
	if !interaction.HasAction(appserver.PendingInteractionActionAcknowledge) {
		return appserver.ErrInvalidInteractionResponse
	}
	if err := o.store.AcknowledgeInterrupt(strings.TrimSpace(interactionID)); err != nil {
		return err
	}
	observability.BroadcastUpdate()
	return nil
}

func (o *Orchestrator) queuedPendingInteractionItems() []appserver.PendingInteraction {
	o.mu.RLock()
	defer o.mu.RUnlock()

	items := make([]appserver.PendingInteraction, 0, len(o.pendingInteractionOrder))
	for _, interactionID := range o.pendingInteractionOrder {
		entry, ok := o.pendingInteractions[interactionID]
		if !ok {
			continue
		}
		items = append(items, entry.interaction.Clone())
	}
	return items
}

func (o *Orchestrator) sharedPendingInteractionItems() ([]appserver.PendingInteraction, error) {
	items := o.queuedPendingInteractionItems()
	alerts, err := o.derivedAlertItems()
	if err != nil {
		return items, err
	}
	items = append(items, alerts...)
	return items, nil
}

func (o *Orchestrator) queuedPendingInteractionByID(interactionID string) (*appserver.PendingInteraction, bool) {
	interactionID = strings.TrimSpace(interactionID)
	if interactionID == "" {
		return nil, false
	}
	o.mu.RLock()
	entry, ok := o.pendingInteractions[interactionID]
	o.mu.RUnlock()
	if !ok {
		return nil, false
	}
	cloned := entry.interaction.Clone()
	return &cloned, true
}

func (o *Orchestrator) queuedPendingInteractionForIssue(issueID, identifier string) (*appserver.PendingInteraction, bool) {
	issueID = strings.TrimSpace(issueID)
	identifier = strings.TrimSpace(identifier)
	if issueID == "" && identifier == "" {
		return nil, false
	}

	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, interactionID := range o.pendingInteractionOrder {
		entry, ok := o.pendingInteractions[interactionID]
		if !ok {
			continue
		}
		if entry.interaction.IssueID != issueID && entry.interaction.IssueIdentifier != identifier {
			continue
		}
		cloned := entry.interaction.Clone()
		return &cloned, true
	}
	return nil, false
}

func (o *Orchestrator) derivedPendingInteractionByID(interactionID string) (*appserver.PendingInteraction, bool, error) {
	items, err := o.derivedAlertItems()
	if err != nil {
		return nil, false, err
	}
	return pendingInteractionByID(items, interactionID)
}

func (o *Orchestrator) derivedPendingInteractionForIssue(issueID, identifier string) (*appserver.PendingInteraction, bool, error) {
	items, err := o.derivedAlertItems()
	if err != nil {
		return nil, false, err
	}
	return pendingInteractionForIssue(items, issueID, identifier)
}

func (o *Orchestrator) derivedAlertItems() ([]appserver.PendingInteraction, error) {
	if !o.isSharedMode() || strings.TrimSpace(o.scopedRepoPath) == "" {
		return nil, nil
	}

	dispatchIssues, err := o.store.ListDispatchIssues(derivedAlertCandidateStates)
	if err != nil {
		return nil, err
	}
	if len(dispatchIssues) == 0 {
		if err := o.store.PruneInterruptAcknowledgements(issueProjectDispatchBlockedAlertIDPrefix, nil); err != nil {
			return nil, err
		}
		return nil, nil
	}

	projects := make(map[string]*kanban.Project)
	alerts := make([]appserver.PendingInteraction, 0, len(dispatchIssues))
	alertIDs := make([]string, 0, len(dispatchIssues))
	for i := range dispatchIssues {
		dispatchIssue := dispatchIssues[i]
		if !o.issueEligibleForDerivedAlert(&dispatchIssue.Issue, &dispatchIssue.DispatchState) {
			continue
		}
		project, ok := projects[dispatchIssue.ProjectID]
		if !ok {
			loaded, err := o.store.GetProject(dispatchIssue.ProjectID)
			if err != nil {
				slog.Debug("Skipping derived interrupt alert because project lookup failed", "issue_id", dispatchIssue.ID, "error", err)
				continue
			}
			project = loaded
			projects[dispatchIssue.ProjectID] = project
		}
		scopeError := projectScopeDispatchError(project.RepoPath, o.scopedRepoPath)
		if scopeError == "" {
			continue
		}
		alert := buildIssueProjectDispatchBlockedAlert(dispatchIssue.Issue, project, scopeError)
		alerts = append(alerts, alert)
		alertIDs = append(alertIDs, alert.ID)
	}
	if len(alerts) == 0 {
		if err := o.store.PruneInterruptAcknowledgements(issueProjectDispatchBlockedAlertIDPrefix, nil); err != nil {
			return nil, err
		}
		return nil, nil
	}

	if err := o.store.PruneInterruptAcknowledgements(issueProjectDispatchBlockedAlertIDPrefix, alertIDs); err != nil {
		return nil, err
	}
	acknowledged, err := o.store.ListInterruptAcknowledgements(alertIDs)
	if err != nil {
		return nil, err
	}
	filtered := alerts[:0]
	for _, alert := range alerts {
		if _, ok := acknowledged[alert.ID]; ok {
			continue
		}
		filtered = append(filtered, alert)
	}
	return filtered, nil
}

func pendingInteractionByID(items []appserver.PendingInteraction, interactionID string) (*appserver.PendingInteraction, bool, error) {
	interactionID = strings.TrimSpace(interactionID)
	if interactionID == "" {
		return nil, false, nil
	}
	for i := range items {
		if items[i].ID != interactionID {
			continue
		}
		cloned := items[i].Clone()
		return &cloned, true, nil
	}
	return nil, false, nil
}

func pendingInteractionForIssue(items []appserver.PendingInteraction, issueID, identifier string) (*appserver.PendingInteraction, bool, error) {
	issueID = strings.TrimSpace(issueID)
	identifier = strings.TrimSpace(identifier)
	if issueID == "" && identifier == "" {
		return nil, false, nil
	}
	for i := range items {
		if items[i].IssueID != issueID && items[i].IssueIdentifier != identifier {
			continue
		}
		cloned := items[i].Clone()
		return &cloned, true, nil
	}
	return nil, false, nil
}

func (o *Orchestrator) issueEligibleForDerivedAlert(issue *kanban.Issue, dispatchState *kanban.IssueDispatchState) bool {
	if issue == nil || dispatchState == nil {
		return false
	}
	if !dispatchState.ProjectExists ||
		dispatchState.ProjectState != kanban.ProjectStateRunning ||
		dispatchState.HasUnresolvedBlockers ||
		issue.PlanApprovalPending {
		return false
	}
	if issue.WorkflowPhase == kanban.WorkflowPhaseComplete {
		return false
	}

	o.mu.RLock()
	defer o.mu.RUnlock()
	if _, running := o.running[issue.ID]; running {
		return false
	}
	if _, paused := o.paused[issue.ID]; paused {
		return false
	}
	return true
}

func (o *Orchestrator) issueRunning(issueID string) bool {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return false
	}

	o.mu.RLock()
	defer o.mu.RUnlock()
	_, ok := o.running[issueID]
	return ok
}

func projectScopeDispatchError(projectRepoPath, scopedRepoPath string) string {
	projectRepoPath = strings.TrimSpace(projectRepoPath)
	scopedRepoPath = strings.TrimSpace(scopedRepoPath)
	if projectRepoPath == "" || scopedRepoPath == "" {
		return ""
	}
	if filepath.Clean(projectRepoPath) == filepath.Clean(scopedRepoPath) {
		return ""
	}
	return "Project repo is outside the current server scope (" + scopedRepoPath + ")"
}

func buildIssueProjectDispatchBlockedAlert(issue kanban.Issue, project *kanban.Project, scopeError string) appserver.PendingInteraction {
	requestedAt := issue.UpdatedAt.UTC()
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	}
	lastActivityAt := requestedAt
	projectName := "Project"
	projectID := strings.TrimSpace(issue.ProjectID)
	if project != nil {
		projectID = strings.TrimSpace(project.ID)
		if trimmed := strings.TrimSpace(project.Name); trimmed != "" {
			projectName = trimmed
		}
	}
	fingerprint := derivedIssueDispatchBlockedFingerprint(issue.ID, projectID, project.RepoPath, scopeError)
	issueLabel := strings.TrimSpace(issue.Identifier)
	if issueLabel == "" {
		issueLabel = strings.TrimSpace(issue.Title)
	}
	detail := fmt.Sprintf(
		"%s is waiting for execution, but %s cannot dispatch inside the current server scope. Open the issue for execution details, then use the linked project page to fix the repo scope mismatch.",
		issueLabel,
		projectName,
	)
	return appserver.PendingInteraction{
		ID:              issueProjectDispatchBlockedAlertIDPrefix + strings.TrimSpace(issue.ID) + "-" + fingerprint,
		Kind:            appserver.PendingInteractionKindAlert,
		Method:          issueProjectDispatchBlockedAlertMethod,
		IssueID:         strings.TrimSpace(issue.ID),
		IssueIdentifier: strings.TrimSpace(issue.Identifier),
		IssueTitle:      strings.TrimSpace(issue.Title),
		RequestedAt:     requestedAt,
		LastActivityAt:  &lastActivityAt,
		LastActivity:    strings.TrimSpace(scopeError),
		ProjectID:       projectID,
		ProjectName:     projectName,
		Actions: []appserver.PendingInteractionAction{{
			Kind:  appserver.PendingInteractionActionAcknowledge,
			Label: "Acknowledge",
		}},
		Alert: &appserver.PendingAlert{
			Code:     issueProjectDispatchBlockedAlertCode,
			Severity: appserver.PendingAlertSeverityError,
			Title:    "Project dispatch blocked",
			Message:  strings.TrimSpace(scopeError),
			Detail:   detail,
		},
	}
}

func derivedIssueDispatchBlockedFingerprint(issueID, projectID, projectRepoPath, scopeError string) string {
	sum := sha1.Sum([]byte(strings.Join([]string{
		strings.TrimSpace(issueID),
		strings.TrimSpace(projectID),
		strings.TrimSpace(projectRepoPath),
		strings.TrimSpace(scopeError),
	}, "|")))
	return hex.EncodeToString(sum[:])[:12]
}
