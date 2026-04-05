package dashboardapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

func TestInterruptEndpointRejectsInvalidRoutesAndMapsProviderErrors(t *testing.T) {
	provider := &interruptProvider{}
	_, srv := setupDashboardServerTest(t, provider)

	for _, tc := range []struct {
		name       string
		method     string
		path       string
		body       interface{}
		respondErr error
		wantStatus int
	}{
		{
			name:       "queue method not allowed",
			method:     http.MethodPost,
			path:       "/api/v1/app/interrupts",
			body:       map[string]interface{}{},
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "missing respond action",
			method:     http.MethodPost,
			path:       "/api/v1/app/interrupts/interrupt-1",
			body:       map[string]interface{}{"decision": "approved"},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "respond method not allowed",
			method:     http.MethodGet,
			path:       "/api/v1/app/interrupts/interrupt-1/respond",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "not found maps to 404",
			method:     http.MethodPost,
			path:       "/api/v1/app/interrupts/interrupt-1/respond",
			body:       map[string]interface{}{"decision": "approved"},
			respondErr: agentruntime.ErrPendingInteractionNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "conflict maps to 409",
			method:     http.MethodPost,
			path:       "/api/v1/app/interrupts/interrupt-1/respond",
			body:       map[string]interface{}{"decision": "approved"},
			respondErr: agentruntime.ErrPendingInteractionConflict,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "invalid input maps to 400",
			method:     http.MethodPost,
			path:       "/api/v1/app/interrupts/interrupt-1/respond",
			body:       map[string]interface{}{"decision": "approved"},
			respondErr: agentruntime.ErrInvalidInteractionResponse,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unexpected errors map to 500",
			method:     http.MethodPost,
			path:       "/api/v1/app/interrupts/interrupt-1/respond",
			body:       map[string]interface{}{"decision": "approved"},
			respondErr: errors.New("boom"),
			wantStatus: http.StatusInternalServerError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.respondErr = tc.respondErr
			provider.responseID = ""
			provider.response = agentruntime.PendingInteractionResponse{}

			resp := requestJSON(t, srv, tc.method, tc.path, tc.body)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected %d, got %d", tc.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestInterruptPlanApprovalRejectsOutOfOrderResponses(t *testing.T) {
	provider := &interruptProvider{}
	store, srv := setupDashboardServerTest(t, provider)

	issue, err := store.CreateIssue("", "", "Plan approval should stay ordered", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	provider.interrupts = agentruntime.PendingInteractionSnapshot{
		Items: []agentruntime.PendingInteraction{
			{
				ID:              "interrupt-older",
				Kind:            agentruntime.PendingInteractionKindUserInput,
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				RequestedAt:     time.Date(2026, 3, 18, 11, 0, 0, 0, time.UTC),
				UserInput: &agentruntime.PendingUserInput{
					Questions: []agentruntime.PendingUserInputQuestion{
						{
							ID:       "earlier-interrupt",
							Question: "Resolve the earlier interrupt first.",
						},
					},
				},
			},
			{
				ID:              "plan-approval-1",
				Kind:            agentruntime.PendingInteractionKindApproval,
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				RequestedAt:     time.Date(2026, 3, 18, 11, 5, 0, 0, time.UTC),
				Approval: &agentruntime.PendingApproval{
					Markdown: "Review the proposed plan before execution.",
					Decisions: []agentruntime.PendingApprovalDecision{
						{Value: "approved", Label: "Approve plan"},
					},
				},
			},
		},
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/interrupts/plan-approval-1/respond", map[string]interface{}{
		"decision": "approved",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
	if provider.responseID != "" {
		t.Fatalf("expected out-of-order approval to bypass RespondToInterrupt, got %q", provider.responseID)
	}
}

func TestInterruptApprovalPromptForwardsAllowDecision(t *testing.T) {
	provider := &interruptProvider{}
	store, srv := setupDashboardServerTest(t, provider)

	issue, err := store.CreateIssue("", "", "Claude permission prompt", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	provider.interrupts = agentruntime.PendingInteractionSnapshot{
		Items: []agentruntime.PendingInteraction{
			{
				ID:              "approval-prompt-call_123",
				Kind:            agentruntime.PendingInteractionKindApproval,
				Method:          "approval_prompt",
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				RequestedAt:     time.Date(2026, 3, 18, 11, 10, 0, 0, time.UTC),
				Approval: &agentruntime.PendingApproval{
					Markdown: "Claude requested command approval: printf '%s\\n' 'maestro claude default profile ok' > default-profile.txt",
					Decisions: []agentruntime.PendingApprovalDecision{
						{Value: "allow", Label: "Allow once"},
						{Value: "deny", Label: "Deny"},
						{Value: "ask", Label: "Ask for clarification"},
					},
				},
			},
		},
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/interrupts/approval-prompt-call_123/respond", map[string]interface{}{
		"decision": "allow",
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if provider.responseID != "approval-prompt-call_123" {
		t.Fatalf("expected approval prompt response to be forwarded, got %q", provider.responseID)
	}
	if provider.response.Decision != "allow" {
		t.Fatalf("expected allow decision to be preserved, got %+v", provider.response)
	}
	if provider.response.Action != "" {
		t.Fatalf("expected empty action for approval prompt response, got %+v", provider.response)
	}
}

func TestInterruptAcknowledgeActionAccepted(t *testing.T) {
	provider := &interruptProvider{}
	_, srv := setupDashboardServerTest(t, provider)

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/interrupts/interrupt-ack/acknowledge", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if provider.ackID != "interrupt-ack" {
		t.Fatalf("expected acknowledge callback for interrupt-ack, got %q", provider.ackID)
	}
}

func TestSessionsEndpointMarksPendingInterruptsAsWaiting(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Waiting on approval", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	lastActivityAt := now.Add(-5 * time.Second)
	provider := testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:     issue.ID,
				Identifier:  issue.Identifier,
				State:       "in_progress",
				Phase:       "implementation",
				Attempt:     2,
				SessionID:   "thread-waiting-turn-waiting",
				TurnCount:   3,
				LastEvent:   "turn.started",
				LastMessage: "Older live message",
				StartedAt:   now.Add(-1 * time.Minute),
				Tokens:      observability.TokenTotals{TotalTokens: 21},
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: agentruntime.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-waiting-turn-waiting",
				ThreadID:        "thread-waiting",
				TurnID:          "turn-waiting",
				LastEvent:       "turn.started",
				LastTimestamp:   now.Add(-20 * time.Second),
				LastMessage:     "Older live message",
				TotalTokens:     21,
				TurnsStarted:    3,
			},
		},
		pendingInterruptsByIssue: map[string]agentruntime.PendingInteraction{
			issue.ID: {
				ID:                "interrupt-1",
				Kind:              agentruntime.PendingInteractionKindApproval,
				IssueID:           issue.ID,
				IssueIdentifier:   issue.Identifier,
				RequestedAt:       now.Add(-15 * time.Second),
				LastActivityAt:    &lastActivityAt,
				LastActivity:      "Approve the repo scope change",
				CollaborationMode: "plan",
				Approval: &agentruntime.PendingApproval{
					Decisions: []agentruntime.PendingApprovalDecision{
						{Value: "approved", Label: "Approve once"},
					},
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
	if !ok || len(entries) != 1 {
		t.Fatalf("expected one session entry, got %#v", payload["entries"])
	}
	entry := entries[0].(map[string]interface{})
	if entry["status"] != "waiting" {
		t.Fatalf("expected waiting status, got %#v", entry)
	}
	if entry["last_message"] != "Approve the repo scope change" {
		t.Fatalf("expected pending interrupt summary to win, got %#v", entry["last_message"])
	}
	pending, ok := entry["pending_interrupt"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected pending interrupt payload, got %#v", entry)
	}
	if pending["id"] != "interrupt-1" || pending["collaboration_mode"] != "plan" {
		t.Fatalf("unexpected pending interrupt payload: %#v", pending)
	}
}

func TestSessionsEndpointMarksQueuedPlanRevisionsAsRevisionQueued(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Waiting on revision", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Plan body", now.Add(-30*time.Second)); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval failed: %v", err)
	}
	revisionRequestedAt := now.Add(-5 * time.Second)
	if err := store.SetIssuePendingPlanRevision(issue.ID, "Tighten the rollout and keep the rollback explicit.", revisionRequestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision failed: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "retry_paused",
		Error:      "plan_approval_pending",
		StopReason: "plan_approval_pending",
		UpdatedAt:  now.Add(-10 * time.Second),
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-waiting-turn-waiting",
			ThreadID:        "thread-waiting",
			TurnID:          "turn-waiting",
			LastEvent:       "turn.paused",
			LastTimestamp:   now.Add(-20 * time.Second),
			LastMessage:     "Waiting for operator approval",
			TotalTokens:     21,
			TurnsStarted:    3,
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}

	provider := testProvider{
		snapshot: observability.Snapshot{
			Retrying: []observability.RetryEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				Phase:      "implementation",
				Attempt:    2,
				DueAt:      now.Add(1 * time.Minute),
				Error:      "plan_approval_pending",
			}},
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
	if !ok || len(entries) != 1 {
		t.Fatalf("expected one session entry, got %#v", payload["entries"])
	}
	entry := entries[0].(map[string]interface{})
	if entry["status"] != "revision_queued" {
		t.Fatalf("expected revision_queued status, got %#v", entry)
	}
	if entry["last_message"] != queuedPlanRevisionText {
		t.Fatalf("expected queued revision summary, got %#v", entry["last_message"])
	}
	if _, ok := entry["pending_interrupt"]; ok {
		t.Fatalf("expected queued revision to avoid pending interrupt payload, got %#v", entry["pending_interrupt"])
	}
}

func TestSessionsEndpointMarksAlertInterruptsAsBlocked(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Blocked on project scope", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	lastActivityAt := now.Add(-5 * time.Second)
	provider := testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:     issue.ID,
				Identifier:  issue.Identifier,
				State:       "in_progress",
				Phase:       "implementation",
				Attempt:     2,
				SessionID:   "thread-blocked-turn-blocked",
				TurnCount:   3,
				LastEvent:   "turn.started",
				LastMessage: "Older live message",
				StartedAt:   now.Add(-1 * time.Minute),
				Tokens:      observability.TokenTotals{TotalTokens: 21},
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: agentruntime.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-blocked-turn-blocked",
				ThreadID:        "thread-blocked",
				TurnID:          "turn-blocked",
				LastEvent:       "turn.started",
				LastTimestamp:   now.Add(-20 * time.Second),
				LastMessage:     "Older live message",
				TotalTokens:     21,
				TurnsStarted:    3,
			},
		},
		pendingInterruptsByIssue: map[string]agentruntime.PendingInteraction{
			issue.ID: {
				ID:              "alert-1",
				Kind:            agentruntime.PendingInteractionKindAlert,
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				RequestedAt:     now.Add(-15 * time.Second),
				LastActivityAt:  &lastActivityAt,
				LastActivity:    "Project repo is outside the current server scope (/repo/current)",
				Alert: &agentruntime.PendingAlert{
					Code:     "project_dispatch_blocked",
					Severity: agentruntime.PendingAlertSeverityError,
					Title:    "Project dispatch blocked",
					Message:  "Project repo is outside the current server scope (/repo/current)",
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
	if !ok || len(entries) != 1 {
		t.Fatalf("expected one session entry, got %#v", payload["entries"])
	}
	entry := entries[0].(map[string]interface{})
	if entry["status"] != "blocked" {
		t.Fatalf("expected blocked status, got %#v", entry)
	}
	if entry["last_message"] != "Project repo is outside the current server scope (/repo/current)" {
		t.Fatalf("expected alert summary to win, got %#v", entry["last_message"])
	}
}

type snapshotBackedSessionsProvider struct {
	testProvider
	pendingInterrupts       agentruntime.PendingInteractionSnapshot
	pendingInterruptLookups int
}

func (p *snapshotBackedSessionsProvider) PendingInterrupts() agentruntime.PendingInteractionSnapshot {
	return p.pendingInterrupts
}

func (p *snapshotBackedSessionsProvider) PendingInterruptForIssue(issueID, identifier string) (*agentruntime.PendingInteraction, bool) {
	p.pendingInterruptLookups++
	return p.testProvider.PendingInterruptForIssue(issueID, identifier)
}

func TestSessionsEndpointUsesSharedInterruptSnapshotInsteadOfPerIssueLookups(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	first, err := store.CreateIssue("", "", "First waiting issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue first failed: %v", err)
	}
	second, err := store.CreateIssue("", "", "Second waiting issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue second failed: %v", err)
	}

	provider := &snapshotBackedSessionsProvider{
		testProvider: testProvider{
			snapshot: observability.Snapshot{
				Running: []observability.RunningEntry{
					{
						IssueID:    first.ID,
						Identifier: first.Identifier,
						Phase:      "implementation",
						SessionID:  "session-1",
						StartedAt:  now.Add(-1 * time.Minute),
						Tokens:     observability.TokenTotals{TotalTokens: 11},
					},
					{
						IssueID:    second.ID,
						Identifier: second.Identifier,
						Phase:      "implementation",
						SessionID:  "session-2",
						StartedAt:  now.Add(-2 * time.Minute),
						Tokens:     observability.TokenTotals{TotalTokens: 17},
					},
				},
			},
			sessions: map[string]interface{}{
				first.Identifier: agentruntime.Session{
					IssueID:         first.ID,
					IssueIdentifier: first.Identifier,
					SessionID:       "session-1",
					ThreadID:        "thread-1",
					TurnID:          "turn-1",
					LastTimestamp:   now.Add(-10 * time.Second),
				},
				second.Identifier: agentruntime.Session{
					IssueID:         second.ID,
					IssueIdentifier: second.Identifier,
					SessionID:       "session-2",
					ThreadID:        "thread-2",
					TurnID:          "turn-2",
					LastTimestamp:   now.Add(-8 * time.Second),
				},
			},
		},
		pendingInterrupts: agentruntime.PendingInteractionSnapshot{
			Items: []agentruntime.PendingInteraction{
				{
					ID:              "interrupt-1",
					Kind:            agentruntime.PendingInteractionKindApproval,
					IssueID:         first.ID,
					IssueIdentifier: first.Identifier,
					RequestedAt:     now.Add(-20 * time.Second),
					Approval: &agentruntime.PendingApproval{
						Decisions: []agentruntime.PendingApprovalDecision{{Value: "approved", Label: "Approve once"}},
					},
				},
				{
					ID:              "interrupt-2",
					Kind:            agentruntime.PendingInteractionKindApproval,
					IssueID:         second.ID,
					IssueIdentifier: second.Identifier,
					RequestedAt:     now.Add(-18 * time.Second),
					Approval: &agentruntime.PendingApproval{
						Decisions: []agentruntime.PendingApprovalDecision{{Value: "approved", Label: "Approve once"}},
					},
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
	if provider.pendingInterruptLookups != 0 {
		t.Fatalf("expected sessions endpoint to use shared interrupt snapshot, got %d per-issue lookups", provider.pendingInterruptLookups)
	}
}
