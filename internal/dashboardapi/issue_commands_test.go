package dashboardapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

func TestIssueCommandsEndpointPersistsCommandAndExposesItInExecutionPayload(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})

	issue, err := store.CreateIssue("", "", "Follow-up", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, kanban.StateDone); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/commands", map[string]interface{}{
		"command": "Merge the branch to master.",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	command := body["command"].(map[string]interface{})
	if command["status"].(string) != string(kanban.IssueAgentCommandPending) {
		t.Fatalf("expected pending command, got %#v", command)
	}

	reloaded, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if reloaded.State != kanban.StateInProgress {
		t.Fatalf("expected issue reopened to in_progress, got %s", reloaded.State)
	}

	execution := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+issue.Identifier+"/execution", nil)
	if execution.StatusCode != http.StatusOK {
		t.Fatalf("expected execution 200, got %d", execution.StatusCode)
	}
	executionBody := decodeResponse(t, execution)
	commands := executionBody["agent_commands"].([]interface{})
	if len(commands) != 1 {
		t.Fatalf("expected one command, got %#v", executionBody["agent_commands"])
	}
	if commands[0].(map[string]interface{})["command"].(string) != "Merge the branch to master." {
		t.Fatalf("unexpected command payload: %#v", commands[0])
	}
}

func TestIssueCommandsEndpointStoresWaitingForUnblockWhenTransitionFails(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})

	blocker, err := store.CreateIssue("", "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	blocked, err := store.CreateIssue("", "", "Blocked", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}
	if err := store.UpdateIssueState(blocked.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocked: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+blocked.Identifier+"/commands", map[string]interface{}{
		"command": "Ship the branch after unblocking.",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	command := body["command"].(map[string]interface{})
	if command["status"].(string) != string(kanban.IssueAgentCommandWaitingForUnblock) {
		t.Fatalf("expected waiting_for_unblock command, got %#v", command)
	}

	reloaded, err := store.GetIssue(blocked.ID)
	if err != nil {
		t.Fatalf("GetIssue blocked: %v", err)
	}
	if reloaded.State != kanban.StateReady {
		t.Fatalf("expected blocked issue to remain ready, got %s", reloaded.State)
	}
}

func TestIssueCommandsEndpointStoresWaitingForUnblockWhenIssueIsAlreadyBlockedInProgress(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})

	blocker, err := store.CreateIssue("", "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	blocked, err := store.CreateIssue("", "", "Blocked", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, kanban.StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}
	if err := store.UpdateIssueState(blocked.ID, kanban.StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState blocked: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+blocked.Identifier+"/commands", map[string]interface{}{
		"command": "Wait for unblock before continuing.",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	command := body["command"].(map[string]interface{})
	if command["status"].(string) != string(kanban.IssueAgentCommandWaitingForUnblock) {
		t.Fatalf("expected waiting_for_unblock command, got %#v", command)
	}
}

func TestIssueCommandsEndpointUpdatesAndDeletesQueuedCommands(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})

	issue, err := store.CreateIssue("", "", "Queued follow-up", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	createResp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/commands", map[string]interface{}{
		"command": "Merge the branch to master.",
	})
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("expected create 200, got %d", createResp.StatusCode)
	}
	created := decodeResponse(t, createResp)
	command := created["command"].(map[string]interface{})
	commandID := command["id"].(string)

	updateResp := requestJSON(t, srv, http.MethodPatch, "/api/v1/app/issues/"+issue.Identifier+"/commands/"+commandID, map[string]interface{}{
		"command": "Merge the branch with the latest tests.",
	})
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected update 200, got %d", updateResp.StatusCode)
	}
	updated := decodeResponse(t, updateResp)
	updatedCommand := updated["command"].(map[string]interface{})
	if updatedCommand["command"].(string) != "Merge the branch with the latest tests." {
		t.Fatalf("unexpected updated command payload: %#v", updatedCommand)
	}

	execution := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+issue.Identifier+"/execution", nil)
	if execution.StatusCode != http.StatusOK {
		t.Fatalf("expected execution 200 after update, got %d", execution.StatusCode)
	}
	executionBody := decodeResponse(t, execution)
	commands := executionBody["agent_commands"].([]interface{})
	if len(commands) != 1 {
		t.Fatalf("expected one command after update, got %#v", executionBody["agent_commands"])
	}
	if commands[0].(map[string]interface{})["command"].(string) != "Merge the branch with the latest tests." {
		t.Fatalf("unexpected execution payload after update: %#v", commands[0])
	}

	deleteResp := requestJSON(t, srv, http.MethodDelete, "/api/v1/app/issues/"+issue.Identifier+"/commands/"+commandID, nil)
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected delete 200, got %d", deleteResp.StatusCode)
	}
	_ = decodeResponse(t, deleteResp)

	executionAfterDelete := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+issue.Identifier+"/execution", nil)
	if executionAfterDelete.StatusCode != http.StatusOK {
		t.Fatalf("expected execution 200 after delete, got %d", executionAfterDelete.StatusCode)
	}
	executionAfterDeleteBody := decodeResponse(t, executionAfterDelete)
	if commands := executionAfterDeleteBody["agent_commands"].([]interface{}); len(commands) != 0 {
		t.Fatalf("expected no commands after delete, got %#v", executionAfterDeleteBody["agent_commands"])
	}
}

func TestIssueCommandsEndpointSteersQueuedCommandsAndRequestsRetry(t *testing.T) {
	provider := &retryTrackingProvider{}
	store, srv := setupDashboardServerTest(t, provider)

	issue, err := store.CreateIssue("", "", "Steerable follow-up", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	command, err := store.CreateIssueAgentCommand(issue.ID, "Merge the branch after the tests finish.", kanban.IssueAgentCommandWaitingForUnblock)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand: %v", err)
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/commands/"+command.ID+"/steer", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected steer 200, got %d", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	steeredCommand := body["command"].(map[string]interface{})
	if steeredCommand["status"].(string) != string(kanban.IssueAgentCommandWaitingForUnblock) {
		t.Fatalf("expected waiting_for_unblock command after steering, got %#v", steeredCommand)
	}
	if steeredCommand["steered_at"].(string) == "" {
		t.Fatalf("expected steered_at timestamp, got %#v", steeredCommand)
	}
	if len(provider.retried) != 1 || provider.retried[0] != issue.Identifier {
		t.Fatalf("expected retry callback for %s, got %v", issue.Identifier, provider.retried)
	}

	execution := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+issue.Identifier+"/execution", nil)
	if execution.StatusCode != http.StatusOK {
		t.Fatalf("expected execution 200, got %d", execution.StatusCode)
	}
	executionBody := decodeResponse(t, execution)
	commands := executionBody["agent_commands"].([]interface{})
	if len(commands) != 1 {
		t.Fatalf("expected one command, got %#v", executionBody["agent_commands"])
	}
	gotCommand := commands[0].(map[string]interface{})
	if gotCommand["steered_at"].(string) == "" {
		t.Fatalf("expected steered_at in execution payload, got %#v", gotCommand)
	}
}

func TestIssueCommandsEndpointSteeringSkipsRetryWhilePlanApprovalIsPending(t *testing.T) {
	provider := &retryTrackingProvider{}
	store, srv := setupDashboardServerTest(t, provider)
	provider.store = store

	issue, err := store.CreateIssue("", "", "Plan-gated follow-up", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	requestedAt := time.Date(2026, 3, 18, 14, 0, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Wait for explicit approval.", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}
	command, err := store.CreateIssueAgentCommand(issue.ID, "Merge once the plan is approved.", kanban.IssueAgentCommandWaitingForUnblock)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand: %v", err)
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/commands/"+command.ID+"/steer", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected steer 200, got %d", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	steeredCommand := body["command"].(map[string]interface{})
	if steeredCommand["status"].(string) != string(kanban.IssueAgentCommandWaitingForUnblock) {
		t.Fatalf("expected waiting_for_unblock command after steering, got %#v", steeredCommand)
	}
	if steeredCommand["steered_at"].(string) == "" {
		t.Fatalf("expected steered_at timestamp, got %#v", steeredCommand)
	}
	if len(provider.retried) != 0 {
		t.Fatalf("expected steering not to retry while plan approval is pending, got %v", provider.retried)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !updated.PlanApprovalPending || updated.PendingPlanMarkdown != "Wait for explicit approval." {
		t.Fatalf("expected pending plan approval to remain queued, got %+v", updated)
	}
	if updated.PendingPlanRequestedAt == nil || !updated.PendingPlanRequestedAt.Equal(requestedAt) {
		t.Fatalf("unexpected pending plan requested_at, got %+v", updated.PendingPlanRequestedAt)
	}
	if updated.PendingPlanRevisionMarkdown != "" || updated.PendingPlanRevisionRequestedAt != nil {
		t.Fatalf("unexpected pending plan revision state, got %+v", updated)
	}
}

func TestIssueCommandsEndpointRejectsDeliveredCommands(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})

	issue, err := store.CreateIssue("", "", "Delivered follow-up", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	createResp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/commands", map[string]interface{}{
		"command": "Ship the branch.",
	})
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("expected create 200, got %d", createResp.StatusCode)
	}
	commandID := decodeResponse(t, createResp)["command"].(map[string]interface{})["id"].(string)

	if err := store.MarkIssueAgentCommandsDelivered(issue.ID, []string{commandID}, "same_thread", "thread-live", 1); err != nil {
		t.Fatalf("MarkIssueAgentCommandsDelivered: %v", err)
	}

	updateResp := requestJSON(t, srv, http.MethodPatch, "/api/v1/app/issues/"+issue.Identifier+"/commands/"+commandID, map[string]interface{}{
		"command": "Ship the branch, then verify the release notes.",
	})
	if updateResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected update 404 for delivered command, got %d", updateResp.StatusCode)
	}

	deleteResp := requestJSON(t, srv, http.MethodDelete, "/api/v1/app/issues/"+issue.Identifier+"/commands/"+commandID, nil)
	if deleteResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected delete 404 for delivered command, got %d", deleteResp.StatusCode)
	}

	steerResp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/commands/"+commandID+"/steer", nil)
	if steerResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected steer 404 for delivered command, got %d", steerResp.StatusCode)
	}
}
