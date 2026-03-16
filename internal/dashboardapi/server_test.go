package dashboardapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type testProvider struct {
	snapshot                 observability.Snapshot
	sessions                 map[string]interface{}
	status                   map[string]interface{}
	pendingInterruptsByIssue map[string]appserver.PendingInteraction
	projectRefreshes         []string
	projectStops             []string
}

type interruptProvider struct {
	testProvider
	interrupts appserver.PendingInteractionSnapshot
	responseID string
	response   appserver.PendingInteractionResponse
	respondErr error
}

func (p testProvider) Status() map[string]interface{} {
	if p.status != nil {
		return p.status
	}
	return map[string]interface{}{"active_runs": len(p.snapshot.Running)}
}

func (p testProvider) Snapshot() observability.Snapshot {
	return p.snapshot
}

func (p testProvider) LiveSessions() map[string]interface{} {
	if p.sessions == nil {
		return map[string]interface{}{"sessions": map[string]interface{}{}}
	}
	return map[string]interface{}{"sessions": p.sessions}
}

func (p testProvider) PendingInterrupts() appserver.PendingInteractionSnapshot {
	return appserver.PendingInteractionSnapshot{}
}

func (p testProvider) PendingInterruptForIssue(issueID, identifier string) (*appserver.PendingInteraction, bool) {
	for _, key := range []string{issueID, identifier} {
		if interaction, ok := p.pendingInterruptsByIssue[key]; ok {
			cloned := interaction.Clone()
			return &cloned, true
		}
	}
	return nil, false
}

func (p testProvider) RespondToInterrupt(ctx context.Context, interactionID string, response appserver.PendingInteractionResponse) error {
	return nil
}

func (p *interruptProvider) PendingInterrupts() appserver.PendingInteractionSnapshot {
	return p.interrupts
}

func (p *interruptProvider) RespondToInterrupt(ctx context.Context, interactionID string, response appserver.PendingInteractionResponse) error {
	p.responseID = interactionID
	p.response = response
	return p.respondErr
}

func (p testProvider) Events(since int64, limit int) map[string]interface{} {
	return map[string]interface{}{"since": since, "last_seq": 0, "events": []interface{}{}}
}

func (p testProvider) RequestRefresh() map[string]interface{} {
	return map[string]interface{}{"status": "accepted"}
}

func (p testProvider) RequestProjectRefresh(projectID string) map[string]interface{} {
	return map[string]interface{}{"status": "accepted", "project_id": projectID, "state": "running"}
}

func (p testProvider) StopProjectRuns(projectID string) map[string]interface{} {
	return map[string]interface{}{"status": "stopped", "project_id": projectID, "state": "stopped", "stopped_runs": 0}
}

func (p testProvider) RetryIssueNow(identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func (p testProvider) RunRecurringIssueNow(identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func setupDashboardServerTest(t *testing.T, provider Provider) (*kanban.Store, *httptest.Server) {
	t.Helper()
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return store, srv
}

func TestIssueExecutionEndpointReturnsLiveSession(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	issue, err := store.CreateIssue("", "", "Live execution", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	provider := testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:     issue.ID,
				Identifier:  issue.Identifier,
				State:       "in_progress",
				Phase:       "implementation",
				Attempt:     2,
				SessionID:   "thread-live-turn-live",
				TurnCount:   3,
				LastEvent:   "turn.started",
				LastMessage: "Working",
				StartedAt:   now.Add(-30 * time.Second),
				Tokens:      observability.TokenTotals{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, SecondsRunning: 30},
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: appserver.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-live-turn-live",
				ThreadID:        "thread-live",
				TurnID:          "turn-live",
				LastEvent:       "turn.started",
				LastTimestamp:   now,
				LastMessage:     "Working",
				TotalTokens:     30,
				TurnsStarted:    3,
				History: []appserver.Event{
					{Type: "turn.started", Message: "Started"},
					{Type: "tool_call_completed", Message: "Ran tests"},
				},
			},
		},
	}
	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
		"issue_id":   issue.ID,
		"identifier": issue.Identifier,
		"phase":      "implementation",
		"attempt":    2,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent failed: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/v1/app/issues/" + issue.Identifier + "/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}
	if payload["session_source"] != "live" || payload["active"] != true {
		t.Fatalf("unexpected live payload: %#v", payload)
	}
	if payload["attempt_number"].(float64) != 2 {
		t.Fatalf("expected attempt 2, got %#v", payload["attempt_number"])
	}
}

func TestBootstrapReturnsCompletedLiveSummaryInsteadOfStreamingDelta(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Streaming summary regression", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	session := &appserver.Session{}
	session.ApplyEvent(appserver.Event{
		Type:      "item.completed",
		ThreadID:  "thread-live",
		TurnID:    "turn-live",
		ItemID:    "msg-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Message:   "Completed summary",
	})
	session.ApplyEvent(appserver.Event{
		Type:     "item.agentMessage.delta",
		ThreadID: "thread-live",
		TurnID:   "turn-live",
		ItemID:   "msg-2",
		ItemType: "agentMessage",
		Message:  "Partial follow-up fragment",
	})
	if session.LastMessage != "Completed summary" {
		t.Fatalf("expected live session to retain the completed summary, got %+v", session)
	}

	lastEventAt := session.LastTimestamp
	provider := testProvider{
		snapshot: observability.Snapshot{
			GeneratedAt: time.Now().UTC().Truncate(time.Second),
			Running: []observability.RunningEntry{{
				IssueID:     issue.ID,
				Identifier:  issue.Identifier,
				State:       "in_progress",
				Phase:       "implementation",
				SessionID:   session.SessionID,
				TurnCount:   session.TurnsStarted,
				LastEvent:   session.LastEvent,
				LastMessage: session.LastMessage,
				StartedAt:   lastEventAt.Add(-15 * time.Second),
				LastEventAt: &lastEventAt,
				Tokens:      observability.TokenTotals{TotalTokens: 12, SecondsRunning: 15},
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: *session,
		},
	}

	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/bootstrap", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	payload := decodeResponse(t, resp)
	overview := payload["overview"].(map[string]interface{})
	snapshot := overview["snapshot"].(map[string]interface{})
	running := snapshot["running"].([]interface{})
	if len(running) != 1 {
		t.Fatalf("expected one running entry, got %#v", running)
	}
	entry := running[0].(map[string]interface{})
	if entry["last_message"] != "Completed summary" {
		t.Fatalf("expected bootstrap running summary to keep completed text, got %#v", entry["last_message"])
	}
}

func TestInterruptEndpointsExposeQueueAndForwardResponses(t *testing.T) {
	provider := &interruptProvider{
		interrupts: appserver.PendingInteractionSnapshot{
			Count: 1,
			Current: &appserver.PendingInteraction{
				ID:              "interrupt-1",
				Kind:            appserver.PendingInteractionKindApproval,
				IssueIdentifier: "ISS-1",
				IssueTitle:      "Review migrations",
				RequestedAt:     time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC),
				Approval: &appserver.PendingApproval{
					Decisions: []appserver.PendingApprovalDecision{
						{Value: "approved", Label: "Approve once"},
					},
				},
			},
		},
	}
	_, srv := setupDashboardServerTest(t, provider)

	resp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/interrupts", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	payload := decodeResponse(t, resp)
	if payload["count"].(float64) != 1 {
		t.Fatalf("unexpected interrupt count: %#v", payload)
	}
	current := payload["current"].(map[string]interface{})
	if current["id"] != "interrupt-1" || current["issue_identifier"] != "ISS-1" {
		t.Fatalf("unexpected current interrupt payload: %#v", current)
	}

	resp = requestJSON(t, srv, http.MethodPost, "/api/v1/app/interrupts/interrupt-1/respond", map[string]interface{}{
		"decision": "approved",
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if provider.responseID != "interrupt-1" || provider.response.Decision != "approved" {
		t.Fatalf("expected provider response capture, got id=%q response=%+v", provider.responseID, provider.response)
	}
}

func TestInterruptEndpointForwardsStructuredDecisionPayloads(t *testing.T) {
	provider := &interruptProvider{}
	_, srv := setupDashboardServerTest(t, provider)

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/interrupts/interrupt-2/respond", map[string]interface{}{
		"decision_payload": map[string]interface{}{
			"acceptWithExecpolicyAmendment": map[string]interface{}{
				"execpolicy_amendment": []string{"allow command=curl https://api.github.com"},
			},
		},
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if provider.responseID != "interrupt-2" {
		t.Fatalf("expected interaction id to be forwarded, got %q", provider.responseID)
	}
	if _, ok := provider.response.DecisionPayload["acceptWithExecpolicyAmendment"]; !ok {
		t.Fatalf("expected structured decision payload, got %+v", provider.response)
	}
}

func TestIssueExecutionEndpointReturnsPersistedSessionAndRetryMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	issue, err := store.CreateIssue("", "", "Persisted execution", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	provider := testProvider{
		snapshot: observability.Snapshot{
			Retrying: []observability.RetryEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    3,
				DueAt:      now.Add(2 * time.Minute),
				DueInMs:    120000,
				Error:      "stall_timeout",
				DelayType:  "failure",
			}},
		},
	}
	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_failed",
		Error:      "approval_required",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-persisted-turn-persisted",
			LastEvent:       "turn.approval_required",
			LastTimestamp:   now,
			LastMessage:     "Waiting for approval",
			History: []appserver.Event{
				{Type: "turn.started", Message: "Started"},
				{Type: "turn.approval_required", Message: "Waiting for approval"},
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}
	for _, kind := range []string{"run_started", "tick", "run_failed", "retry_scheduled"} {
		if err := store.AppendRuntimeEvent(kind, map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    2,
			"error":      "approval_required",
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s) failed: %v", kind, err)
		}
	}

	resp, err := http.Get(srv.URL + "/api/v1/app/issues/" + issue.Identifier + "/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}
	if payload["session_source"] != "persisted" || payload["retry_state"] != "scheduled" {
		t.Fatalf("unexpected persisted payload: %#v", payload)
	}
	if payload["failure_class"] != "stall_timeout" {
		t.Fatalf("expected retry-derived failure class, got %#v", payload["failure_class"])
	}
	events, ok := payload["runtime_events"].([]interface{})
	if !ok || len(events) != 3 {
		t.Fatalf("expected 3 filtered runtime events, got %#v", payload["runtime_events"])
	}
	first := events[0].(map[string]interface{})
	last := events[len(events)-1].(map[string]interface{})
	if first["kind"] != "run_started" || last["kind"] != "retry_scheduled" {
		t.Fatalf("expected oldest-to-newest execution events, got %#v", events)
	}
}

func TestIssueExecutionEndpointReturnsGroupedPersistentActivityHistory(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})
	issue, err := store.CreateIssue("", "", "Persistent activity", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	for _, kind := range []string{"run_started", "run_completed"} {
		if err := store.AppendRuntimeEvent(kind, map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    2,
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s) failed: %v", kind, err)
		}
	}
	for _, event := range []appserver.ActivityEvent{
		{
			Type:      "item.completed",
			ThreadID:  "thread-1",
			TurnID:    "turn-1",
			ItemID:    "msg-1",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Item: map[string]interface{}{
				"id":    "msg-1",
				"type":  "agentMessage",
				"phase": "final_answer",
				"text":  "Authoritative completed answer",
			},
		},
		{
			Type:      "item.completed",
			ThreadID:  "thread-1",
			TurnID:    "turn-1",
			ItemID:    "plan-1",
			ItemType:  "plan",
			ItemPhase: "planning",
			Item: map[string]interface{}{
				"id":   "plan-1",
				"type": "plan",
				"text": "1. Parse documented events",
			},
		},
	} {
		if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 2, event); err != nil {
			t.Fatalf("ApplyIssueActivityEvent(%s) failed: %v", event.Type, err)
		}
	}

	resp, err := http.Get(srv.URL + "/api/v1/app/issues/" + issue.Identifier + "/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}

	groups, ok := payload["activity_groups"].([]interface{})
	if !ok || len(groups) != 1 {
		t.Fatalf("expected one primary activity group, got %#v", payload["activity_groups"])
	}
	group := groups[0].(map[string]interface{})
	if group["attempt"].(float64) != 2 || group["phase"] != "implementation" || group["status"] != "completed" {
		t.Fatalf("unexpected primary activity group metadata: %#v", group)
	}
	entries, ok := group["entries"].([]interface{})
	if !ok || len(entries) != 1 {
		t.Fatalf("expected one primary activity entry, got %#v", group["entries"])
	}
	entry := entries[0].(map[string]interface{})
	if entry["kind"] != "agent" || entry["item_type"] != "agentMessage" || entry["summary"] != "Authoritative completed answer" {
		t.Fatalf("unexpected primary activity entry: %#v", entry)
	}

	debugGroups, ok := payload["debug_activity_groups"].([]interface{})
	if !ok || len(debugGroups) != 1 {
		t.Fatalf("expected one debug activity group, got %#v", payload["debug_activity_groups"])
	}
	debugEntries := debugGroups[0].(map[string]interface{})["entries"].([]interface{})
	if len(debugEntries) != 1 || debugEntries[0].(map[string]interface{})["item_type"] != "plan" {
		t.Fatalf("unexpected debug activity entries: %#v", debugEntries)
	}
}

func TestSessionsEndpointReturnsMergedEntriesAndPrefersLive(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	liveIssue, err := store.CreateIssue("", "", "Zulu live issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue live failed: %v", err)
	}
	liveAlphaIssue, err := store.CreateIssue("", "", "Alpha live issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue second live failed: %v", err)
	}
	pausedIssue, err := store.CreateIssue("", "", "Charlie paused issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue paused failed: %v", err)
	}
	completedIssue, err := store.CreateIssue("", "", "Bravo completed issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue completed failed: %v", err)
	}
	interruptedIssue, err := store.CreateIssue("", "", "Delta interrupted issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue interrupted failed: %v", err)
	}
	failedIssue, err := store.CreateIssue("", "", "Echo failed issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed failed: %v", err)
	}

	for _, snapshot := range []kanban.ExecutionSessionSnapshot{
		{
			IssueID:    liveIssue.ID,
			Identifier: liveIssue.Identifier,
			Phase:      "implementation",
			Attempt:    2,
			RunKind:    "run_failed",
			Error:      "approval_required",
			UpdatedAt:  now.Add(-1 * time.Minute),
			AppSession: appserver.Session{
				IssueID:         liveIssue.ID,
				IssueIdentifier: liveIssue.Identifier,
				SessionID:       "thread-live-old-turn-live-old",
				LastEvent:       "turn.approval_required",
				LastTimestamp:   now.Add(-1 * time.Minute),
				LastMessage:     "Old persisted state",
			},
		},
		{
			IssueID:    liveAlphaIssue.ID,
			Identifier: liveAlphaIssue.Identifier,
			Phase:      "implementation",
			Attempt:    1,
			RunKind:    "run_failed",
			Error:      "approval_required",
			UpdatedAt:  now.Add(-90 * time.Second),
			AppSession: appserver.Session{
				IssueID:         liveAlphaIssue.ID,
				IssueIdentifier: liveAlphaIssue.Identifier,
				SessionID:       "thread-live-alpha-old-turn-live-alpha-old",
				LastEvent:       "turn.approval_required",
				LastTimestamp:   now.Add(-90 * time.Second),
				LastMessage:     "Old alpha persisted state",
			},
		},
		{
			IssueID:    pausedIssue.ID,
			Identifier: pausedIssue.Identifier,
			Phase:      "review",
			Attempt:    3,
			RunKind:    "retry_paused",
			Error:      "stall_timeout",
			UpdatedAt:  now.Add(-2 * time.Minute),
			AppSession: appserver.Session{
				IssueID:         pausedIssue.ID,
				IssueIdentifier: pausedIssue.Identifier,
				SessionID:       "thread-paused-turn-paused",
				LastEvent:       "run.failed",
				LastTimestamp:   now.Add(-2 * time.Minute),
				LastMessage:     "Paused after repeated failures",
			},
		},
		{
			IssueID:    completedIssue.ID,
			Identifier: completedIssue.Identifier,
			Phase:      "review",
			Attempt:    1,
			RunKind:    "run_completed",
			UpdatedAt:  now.Add(-3 * time.Minute),
			AppSession: appserver.Session{
				IssueID:         completedIssue.ID,
				IssueIdentifier: completedIssue.Identifier,
				SessionID:       "thread-complete-turn-complete",
				LastEvent:       "turn.completed",
				LastTimestamp:   now.Add(-3 * time.Minute),
				LastMessage:     "Completed cleanly",
				Terminal:        true,
				TerminalReason:  "turn.completed",
			},
		},
		{
			IssueID:    interruptedIssue.ID,
			Identifier: interruptedIssue.Identifier,
			Phase:      "implementation",
			Attempt:    4,
			RunKind:    "run_started",
			UpdatedAt:  now.Add(-4 * time.Minute),
			AppSession: appserver.Session{
				IssueID:         interruptedIssue.ID,
				IssueIdentifier: interruptedIssue.Identifier,
				SessionID:       "thread-interrupted-turn-interrupted",
				LastEvent:       "turn.started",
				LastTimestamp:   now.Add(-4 * time.Minute),
				LastMessage:     "Lost live heartbeat",
			},
		},
		{
			IssueID:    failedIssue.ID,
			Identifier: failedIssue.Identifier,
			Phase:      "implementation",
			Attempt:    1,
			RunKind:    "run_failed",
			Error:      "approval_required",
			UpdatedAt:  now.Add(-5 * time.Minute),
			AppSession: appserver.Session{
				IssueID:         failedIssue.ID,
				IssueIdentifier: failedIssue.Identifier,
				SessionID:       "thread-failed-turn-failed",
				LastEvent:       "turn.approval_required",
				LastTimestamp:   now.Add(-5 * time.Minute),
				LastMessage:     "Waiting on approval",
			},
		},
	} {
		if err := store.UpsertIssueExecutionSession(snapshot); err != nil {
			t.Fatalf("UpsertIssueExecutionSession(%s) failed: %v", snapshot.Identifier, err)
		}
	}

	provider := testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{
				{
					IssueID:     liveIssue.ID,
					Identifier:  liveIssue.Identifier,
					State:       "in_progress",
					Phase:       "implementation",
					Attempt:     7,
					SessionID:   "thread-live-turn-live",
					TurnCount:   5,
					LastEvent:   "turn.started",
					LastMessage: "Applying changes",
					StartedAt:   now.Add(-30 * time.Second),
					Tokens:      observability.TokenTotals{TotalTokens: 33},
				},
				{
					IssueID:     liveAlphaIssue.ID,
					Identifier:  liveAlphaIssue.Identifier,
					State:       "in_progress",
					Phase:       "implementation",
					Attempt:     2,
					SessionID:   "thread-live-alpha-turn-live-alpha",
					TurnCount:   2,
					LastEvent:   "turn.started",
					LastMessage: "Reviewing alpha changes",
					StartedAt:   now.Add(-45 * time.Second),
					Tokens:      observability.TokenTotals{TotalTokens: 12},
				},
			},
			Paused: []observability.PausedEntry{{
				IssueID:             pausedIssue.ID,
				Identifier:          pausedIssue.Identifier,
				Phase:               "review",
				Attempt:             3,
				PausedAt:            now.Add(-2 * time.Minute),
				Error:               "stall_timeout",
				ConsecutiveFailures: 3,
				PauseThreshold:      3,
			}},
		},
		sessions: map[string]interface{}{
			liveIssue.Identifier: appserver.Session{
				IssueID:         liveIssue.ID,
				IssueIdentifier: liveIssue.Identifier,
				SessionID:       "thread-live-turn-live",
				ThreadID:        "thread-live",
				TurnID:          "turn-live",
				LastEvent:       "turn.started",
				LastTimestamp:   now,
				LastMessage:     "Applying changes",
				TotalTokens:     33,
				EventsProcessed: 6,
				TurnsStarted:    5,
				TurnsCompleted:  4,
				History: []appserver.Event{
					{Type: "turn.started", Message: "Applying changes"},
				},
			},
			liveAlphaIssue.Identifier: appserver.Session{
				IssueID:         liveAlphaIssue.ID,
				IssueIdentifier: liveAlphaIssue.Identifier,
				SessionID:       "thread-live-alpha-turn-live-alpha",
				ThreadID:        "thread-live-alpha",
				TurnID:          "turn-live-alpha",
				LastEvent:       "turn.started",
				LastTimestamp:   now.Add(-10 * time.Second),
				LastMessage:     "Reviewing alpha changes",
				TotalTokens:     12,
				EventsProcessed: 4,
				TurnsStarted:    2,
				TurnsCompleted:  1,
				History: []appserver.Event{
					{Type: "turn.started", Message: "Reviewing alpha changes"},
				},
			},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/app/sessions", nil)
	NewServer(store, provider).handleSessions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode sessions payload: %v", err)
	}

	entries, ok := payload["entries"].([]interface{})
	if !ok || len(entries) != 6 {
		t.Fatalf("expected 6 merged entries, got %#v", payload["entries"])
	}
	if entries[0].(map[string]interface{})["issue_identifier"] != liveAlphaIssue.Identifier {
		t.Fatalf("expected alpha live entry first, got %#v", entries[0])
	}
	if entries[1].(map[string]interface{})["issue_identifier"] != liveIssue.Identifier {
		t.Fatalf("expected zulu live entry second, got %#v", entries[1])
	}
	if entries[2].(map[string]interface{})["issue_identifier"] != completedIssue.Identifier {
		t.Fatalf("expected bravo completed entry first in persisted group, got %#v", entries[2])
	}
	if got := findSessionFeedEntry(t, entries, liveIssue.Identifier)["source"]; got != "live" {
		t.Fatalf("expected live source for duplicate issue, got %#v", got)
	}
	if got := findSessionFeedEntry(t, entries, liveAlphaIssue.Identifier)["issue_title"]; got != liveAlphaIssue.Title {
		t.Fatalf("expected issue title for live alpha entry, got %#v", got)
	}
	if got := findSessionFeedEntry(t, entries, pausedIssue.Identifier)["status"]; got != "paused" {
		t.Fatalf("expected paused status, got %#v", got)
	}
	if got := findSessionFeedEntry(t, entries, completedIssue.Identifier)["status"]; got != "completed" {
		t.Fatalf("expected completed status, got %#v", got)
	}
	if got := findSessionFeedEntry(t, entries, interruptedIssue.Identifier)["status"]; got != "interrupted" {
		t.Fatalf("expected interrupted status, got %#v", got)
	}
	failed := findSessionFeedEntry(t, entries, failedIssue.Identifier)
	if failed["status"] != "failed" || failed["failure_class"] != "approval_required" {
		t.Fatalf("expected failed approval_required entry, got %#v", failed)
	}
}

func findSessionFeedEntry(t *testing.T, entries []interface{}, identifier string) map[string]interface{} {
	t.Helper()
	for _, raw := range entries {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(entry["issue_identifier"])) == identifier {
			return entry
		}
	}
	t.Fatalf("missing session feed entry for %s", identifier)
	return nil
}

func asString(value interface{}) string {
	text, _ := value.(string)
	return text
}

func TestIssueExecutionEndpointReturnsPausedRetryMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	issue, err := store.CreateIssue("", "", "Paused execution", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	mux := http.NewServeMux()
	NewServer(store, testProvider{}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    3,
		RunKind:    "retry_paused",
		Error:      "stall_timeout",
		UpdatedAt:  now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-paused-turn-paused",
			LastEvent:       "item.started",
			LastTimestamp:   now,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}
	if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
		"issue_id":             issue.ID,
		"identifier":           issue.Identifier,
		"phase":                "implementation",
		"attempt":              3,
		"paused_at":            now.Format(time.RFC3339),
		"error":                "stall_timeout",
		"consecutive_failures": 3,
		"pause_threshold":      3,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent retry_paused failed: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/v1/app/issues/" + issue.Identifier + "/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}
	if payload["retry_state"] != "paused" || payload["pause_reason"] != "stall_timeout" {
		t.Fatalf("unexpected paused execution payload: %#v", payload)
	}
	if payload["consecutive_failures"].(float64) != 3 || payload["pause_threshold"].(float64) != 3 {
		t.Fatalf("unexpected paused streak payload: %#v", payload)
	}
}

func TestIssueExecutionEndpointReturnsRetryLimitPauseReason(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	issue, err := store.CreateIssue("", "", "Retry limit execution", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	mux := http.NewServeMux()
	NewServer(store, testProvider{}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
		"issue_id":        issue.ID,
		"identifier":      issue.Identifier,
		"issue_state":     "in_progress",
		"phase":           "implementation",
		"attempt":         4,
		"paused_at":       now.Format(time.RFC3339),
		"error":           "retry_limit_reached",
		"pause_threshold": 8,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent retry_paused failed: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/v1/app/issues/" + issue.Identifier + "/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}
	if payload["retry_state"] != "paused" || payload["pause_reason"] != "retry_limit_reached" {
		t.Fatalf("unexpected retry limit execution payload: %#v", payload)
	}
}

func TestIssueExecutionEndpointReturnsNotFoundForMissingIssue(t *testing.T) {
	_, srv := setupDashboardServerTest(t, testProvider{})
	resp, err := http.Get(srv.URL + "/api/v1/app/issues/ISS-404/execution")
	if err != nil {
		t.Fatalf("GET execution failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
