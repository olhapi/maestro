package dashboardapi

import (
	"net/http"
	"testing"
)

func TestIssueEndpointsExposeTotalTokensSpent(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})

	issue, err := store.CreateIssue("", "", "Token spend", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AddIssueTokenSpend(issue.ID, 42); err != nil {
		t.Fatalf("AddIssueTokenSpend: %v", err)
	}

	listIssues := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues?limit=20", nil)
	if listIssues.StatusCode != http.StatusOK {
		t.Fatalf("list issues expected 200, got %d", listIssues.StatusCode)
	}
	listPayload := decodeResponse(t, listIssues)
	items := listPayload["items"].([]interface{})
	if items[0].(map[string]interface{})["total_tokens_spent"].(float64) != 42 {
		t.Fatalf("expected list token spend to be 42, got %#v", items[0])
	}

	getIssue := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+issue.Identifier, nil)
	if getIssue.StatusCode != http.StatusOK {
		t.Fatalf("get issue expected 200, got %d", getIssue.StatusCode)
	}
	issuePayload := decodeResponse(t, getIssue)
	if issuePayload["total_tokens_spent"].(float64) != 42 {
		t.Fatalf("expected detail token spend to be 42, got %#v", issuePayload)
	}
}
