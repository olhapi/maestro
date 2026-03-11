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
