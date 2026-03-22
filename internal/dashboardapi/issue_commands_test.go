package dashboardapi

import (
	"net/http"
	"testing"

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
}
