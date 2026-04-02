package dashboardapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/internal/testutil/inprocessserver"
)

type testProvider struct {
	snapshot                 observability.Snapshot
	sessions                 map[string]interface{}
	status                   map[string]interface{}
	pendingInterruptsByIssue map[string]agentruntime.PendingInteraction
	projectRefreshes         []string
	projectStops             []string
}

type interruptProvider struct {
	testProvider
	interrupts agentruntime.PendingInteractionSnapshot
	responseID string
	response   agentruntime.PendingInteractionResponse
	respondErr error
	ackID      string
	ackErr     error
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

func (p testProvider) PendingInterrupts() agentruntime.PendingInteractionSnapshot {
	items := make([]agentruntime.PendingInteraction, 0, len(p.pendingInterruptsByIssue))
	seen := make(map[string]struct{}, len(p.pendingInterruptsByIssue))
	for _, interaction := range p.pendingInterruptsByIssue {
		if _, ok := seen[interaction.ID]; ok {
			continue
		}
		seen[interaction.ID] = struct{}{}
		items = append(items, interaction.Clone())
	}
	return agentruntime.PendingInteractionSnapshot{Items: items}
}

func (p testProvider) PendingInterruptForIssue(issueID, identifier string) (*agentruntime.PendingInteraction, bool) {
	for _, key := range []string{issueID, identifier} {
		if interaction, ok := p.pendingInterruptsByIssue[key]; ok {
			cloned := interaction.Clone()
			return &cloned, true
		}
	}
	return nil, false
}

func (p testProvider) RespondToInterrupt(ctx context.Context, interactionID string, response agentruntime.PendingInteractionResponse) error {
	return nil
}

func (p testProvider) AcknowledgeInterrupt(ctx context.Context, interactionID string) error {
	return nil
}

func (p *interruptProvider) PendingInterrupts() agentruntime.PendingInteractionSnapshot {
	return p.interrupts
}

func (p *interruptProvider) RespondToInterrupt(ctx context.Context, interactionID string, response agentruntime.PendingInteractionResponse) error {
	p.responseID = interactionID
	p.response = response
	return p.respondErr
}

func (p *interruptProvider) AcknowledgeInterrupt(ctx context.Context, interactionID string) error {
	p.ackID = interactionID
	return p.ackErr
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

func (p testProvider) RetryIssueNow(ctx context.Context, identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func (p testProvider) RunRecurringIssueNow(ctx context.Context, identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func setupDashboardServerTest(t *testing.T, provider Provider) (*kanban.Store, *inprocessserver.Server) {
	t.Helper()
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
	t.Cleanup(srv.Close)
	return store, srv
}

type wsPipeResponseWriter struct {
	conn   net.Conn
	header http.Header
}

func (w *wsPipeResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *wsPipeResponseWriter) WriteHeader(statusCode int) {}

func (w *wsPipeResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *wsPipeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func dialDashboardWebSocket(t *testing.T, server *Server) (*websocket.Conn, chan error) {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	errCh := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		req, err := http.ReadRequest(bufio.NewReader(serverConn))
		if err != nil {
			errCh <- err
			return
		}
		if req.URL == nil {
			req.URL = &url.URL{}
		}
		req.URL.Scheme = "ws"
		req.URL.Host = "example.com"
		rw := &wsPipeResponseWriter{conn: serverConn}
		server.handleWS(rw, req)
		errCh <- nil
	}()

	wsURL, err := url.Parse("ws://example.com/api/v1/ws")
	if err != nil {
		t.Fatalf("parse websocket URL: %v", err)
	}
	conn, resp, err := websocket.NewClient(clientConn, wsURL, nil, 1024, 1024)
	if err != nil {
		_ = clientConn.Close()
		_ = serverConn.Close()
		t.Fatalf("websocket handshake: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return conn, errCh
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
			issue.Identifier: agentruntime.Session{
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
				History: []agentruntime.Event{
					{Type: "turn.started", Message: "Started"},
					{Type: "tool_call_completed", Message: "Ran tests"},
				},
			},
		},
	}
	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
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

	session := &agentruntime.Session{}
	session.ApplyEvent(agentruntime.Event{
		Type:      "item.completed",
		ThreadID:  "thread-live",
		TurnID:    "turn-live",
		ItemID:    "msg-1",
		ItemType:  "agentMessage",
		ItemPhase: "commentary",
		Message:   "Completed summary",
	})
	session.ApplyEvent(agentruntime.Event{
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
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
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

func TestWorkEndpointReturnsBoundedPayload(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.CreateIssue("", "", "Work payload", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	provider := testProvider{
		snapshot: observability.Snapshot{
			Running: []observability.RunningEntry{{
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				State:      "in_progress",
			}},
		},
		sessions: map[string]interface{}{
			issue.Identifier: agentruntime.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-live-turn-live",
				ThreadID:        "thread-live",
				TurnID:          "turn-live",
				LastEvent:       "turn.started",
				LastTimestamp:   time.Now().UTC(),
			},
		},
	}
	mux := http.NewServeMux()
	NewServer(store, provider).Register(mux)
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
	t.Cleanup(srv.Close)

	resp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/work", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	payload := decodeResponse(t, resp)
	overview := payload["overview"].(map[string]interface{})
	if _, ok := overview["status"]; ok {
		t.Fatalf("expected work payload to omit status, got %#v", overview)
	}
	if _, ok := overview["series"]; ok {
		t.Fatalf("expected work payload to omit series, got %#v", overview)
	}
	if _, ok := overview["recent_events"]; ok {
		t.Fatalf("expected work payload to omit recent_events, got %#v", overview)
	}
	snapshot := overview["snapshot"].(map[string]interface{})
	if _, ok := snapshot["running"]; !ok {
		t.Fatalf("expected work payload to include running snapshot, got %#v", snapshot)
	}
	if _, ok := payload["sessions"]; !ok {
		t.Fatalf("expected work payload to include sessions, got %#v", payload)
	}
}

func TestWebSocketEndpointStreamsConnectedAndInvalidateEvents(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	server := NewServer(store, testProvider{})
	conn, serverErrCh := dialDashboardWebSocket(t, server)
	t.Cleanup(func() { _ = conn.Close() })

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}

	var connected map[string]interface{}
	if err := conn.ReadJSON(&connected); err != nil {
		t.Fatalf("Read connected message: %v", err)
	}
	if connected["type"] != "connected" {
		t.Fatalf("expected connected message, got %#v", connected)
	}

	repoPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoPath, "WORKFLOW.md"), []byte("workflow"), 0o644); err != nil {
		t.Fatalf("WriteFile WORKFLOW.md: %v", err)
	}
	if _, err := store.CreateProject("Realtime", "", repoPath, filepath.Join(repoPath, "WORKFLOW.md")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	observability.BroadcastUpdate()

	var invalidation map[string]interface{}
	if err := conn.ReadJSON(&invalidation); err != nil {
		t.Fatalf("Read invalidation message: %v", err)
	}
	if invalidation["type"] != "invalidate" {
		t.Fatalf("expected invalidate message, got %#v", invalidation)
	}
	if runtimeOnly, ok := invalidation["runtime_only"].(bool); !ok || runtimeOnly {
		t.Fatalf("expected non-runtime invalidation, got %#v", invalidation)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("close websocket: %v", err)
	}
	select {
	case err := <-serverErrCh:
		if err != nil {
			t.Fatalf("websocket handler exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("websocket handler did not exit after close")
	}
}

func TestInterruptEndpointsExposeQueueAndForwardResponses(t *testing.T) {
	provider := &interruptProvider{
		interrupts: agentruntime.PendingInteractionSnapshot{
			Items: []agentruntime.PendingInteraction{{
				ID:              "interrupt-1",
				Kind:            agentruntime.PendingInteractionKindApproval,
				IssueIdentifier: "ISS-1",
				IssueTitle:      "Review migrations",
				RequestedAt:     time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC),
				Approval: &agentruntime.PendingApproval{
					Decisions: []agentruntime.PendingApprovalDecision{
						{Value: "approved", Label: "Approve once"},
					},
				},
			}},
		},
	}
	_, srv := setupDashboardServerTest(t, provider)

	resp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/interrupts", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	payload := decodeResponse(t, resp)
	items := payload["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("unexpected interrupt items: %#v", payload)
	}
	current := items[0].(map[string]interface{})
	if current["id"] != "interrupt-1" || current["issue_identifier"] != "ISS-1" {
		t.Fatalf("unexpected interrupt payload: %#v", current)
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

func TestInterruptEndpointPreservesEmptyElicitationObjectSchemas(t *testing.T) {
	provider := testProvider{
		pendingInterruptsByIssue: map[string]agentruntime.PendingInteraction{
			"ISS-9": {
				ID:              "elicitation-empty",
				Kind:            agentruntime.PendingInteractionKindElicitation,
				IssueIdentifier: "ISS-9",
				ThreadID:        "thread-1",
				TurnID:          "turn-1",
				RequestedAt:     time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC),
				Elicitation: &agentruntime.PendingElicitation{
					ServerName: "maestro",
					Message:    "Confirm tool call",
					Mode:       "form",
					RequestedSchema: map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
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
	items := payload["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("unexpected interrupt items: %#v", payload)
	}
	current := items[0].(map[string]interface{})
	elicitation, ok := current["elicitation"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected elicitation payload, got %#v", current["elicitation"])
	}
	requestedSchema, ok := elicitation["requested_schema"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected requested schema object, got %#v", elicitation["requested_schema"])
	}
	properties, ok := requestedSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected empty properties object to survive JSON encoding, got %#v", requestedSchema["properties"])
	}
	if len(properties) != 0 {
		t.Fatalf("expected empty properties object, got %#v", properties)
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

func TestInterruptEndpointForwardsMCPServerElicitationResponses(t *testing.T) {
	provider := &interruptProvider{
		interrupts: agentruntime.PendingInteractionSnapshot{
			Items: []agentruntime.PendingInteraction{{
				ID:          "elicitation-1",
				Kind:        agentruntime.PendingInteractionKindElicitation,
				ThreadID:    "thread-1",
				TurnID:      "turn-1",
				RequestedAt: time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC),
				ItemID:      "support-bot",
				Elicitation: &agentruntime.PendingElicitation{
					ServerName: "support-bot",
					Message:    "Need contact details",
					Mode:       "form",
				},
			}},
		},
	}
	_, srv := setupDashboardServerTest(t, provider)

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/interrupts/elicitation-1/respond", map[string]interface{}{
		"action": "accept",
		"content": map[string]interface{}{
			"email": "ops@example.com",
		},
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if provider.responseID != "elicitation-1" {
		t.Fatalf("expected interaction id to be forwarded, got %q", provider.responseID)
	}
	if provider.response.Action != "accept" {
		t.Fatalf("expected elicitation action to be forwarded, got %+v", provider.response)
	}
	content, ok := provider.response.Content.(map[string]interface{})
	if !ok || content["email"] != "ops@example.com" {
		t.Fatalf("expected structured elicitation content, got %+v", provider.response.Content)
	}
}

func TestInterruptEndpointDoesNotCreateCommandsForElicitationNotes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       map[string]interface{}
		respondErr error
		wantStatus int
	}{
		{
			name: "note only",
			body: map[string]interface{}{
				"note": "Please use ops@example.com",
			},
			respondErr: agentruntime.ErrInvalidInteractionResponse,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "note with content",
			body: map[string]interface{}{
				"action": "accept",
				"note":   "Please use ops@example.com",
				"content": map[string]interface{}{
					"email": "ops@example.com",
				},
			},
			wantStatus: http.StatusAccepted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &interruptProvider{
				interrupts: agentruntime.PendingInteractionSnapshot{
					Items: []agentruntime.PendingInteraction{{
						ID:              "elicitation-1",
						Kind:            agentruntime.PendingInteractionKindElicitation,
						IssueID:         "issue-1",
						IssueIdentifier: "ISS-1",
						ThreadID:        "thread-1",
						TurnID:          "turn-1",
						RequestedAt:     time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC),
						Elicitation: &agentruntime.PendingElicitation{
							ServerName: "support-bot",
							Message:    "Need contact details",
							Mode:       "form",
						},
					}},
				},
				respondErr: tc.respondErr,
			}
			store, srv := setupDashboardServerTest(t, provider)
			issue, err := store.CreateIssue("", "", "Elicitation note issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			provider.interrupts.Items[0].IssueID = issue.ID
			provider.interrupts.Items[0].IssueIdentifier = issue.Identifier

			resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/interrupts/elicitation-1/respond", tc.body)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected %d, got %d", tc.wantStatus, resp.StatusCode)
			}

			commands, err := store.ListIssueAgentCommands(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueAgentCommands: %v", err)
			}
			if len(commands) != 0 {
				t.Fatalf("expected no issue commands for elicitation notes, got %#v", commands)
			}
		})
	}
}

func TestInterruptEndpointExposesProviderSuppliedAlertItems(t *testing.T) {
	provider := &interruptProvider{
		interrupts: agentruntime.PendingInteractionSnapshot{
			Items: []agentruntime.PendingInteraction{{
				ID:              "alert-1",
				Kind:            agentruntime.PendingInteractionKindAlert,
				IssueIdentifier: "ISS-7",
				IssueTitle:      "Blocked issue",
				ProjectName:     "Out of scope project",
				RequestedAt:     time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC),
				Actions: []agentruntime.PendingInteractionAction{{
					Kind:  agentruntime.PendingInteractionActionAcknowledge,
					Label: "Acknowledge",
				}},
				Alert: &agentruntime.PendingAlert{
					Code:     "project_dispatch_blocked",
					Severity: agentruntime.PendingAlertSeverityError,
					Title:    "Project dispatch blocked",
					Message:  "Project repo is outside the current server scope (/repo/current)",
				},
			}},
		},
	}
	_, srv := setupDashboardServerTest(t, provider)

	resp := requestJSON(t, srv, http.MethodGet, "/api/v1/app/interrupts", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	payload := decodeResponse(t, resp)
	items := payload["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected one interrupt item, got %#v", payload)
	}
	current := items[0].(map[string]interface{})
	if current["id"] != "alert-1" || current["kind"] != string(agentruntime.PendingInteractionKindAlert) {
		t.Fatalf("expected provider alert item, got %#v", current)
	}
}

func TestInterruptAcknowledgeEndpointForwardsAcknowledgeRequests(t *testing.T) {
	provider := &interruptProvider{}
	_, srv := setupDashboardServerTest(t, provider)

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/interrupts/alert-1/acknowledge", map[string]interface{}{})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if provider.ackID != "alert-1" {
		t.Fatalf("expected acknowledgement id to be forwarded, got %q", provider.ackID)
	}
}

func TestInterruptRespondEndpointRejectsInvalidAlertResponses(t *testing.T) {
	provider := &interruptProvider{respondErr: agentruntime.ErrInvalidInteractionResponse}
	_, srv := setupDashboardServerTest(t, provider)

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/interrupts/alert-1/respond", map[string]interface{}{
		"decision": "approved",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestInterruptAcknowledgeEndpointRejectsNonAcknowledgeableItems(t *testing.T) {
	provider := &interruptProvider{ackErr: agentruntime.ErrInvalidInteractionResponse}
	_, srv := setupDashboardServerTest(t, provider)

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/interrupts/interrupt-1/acknowledge", map[string]interface{}{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
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
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
	t.Cleanup(srv.Close)
	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    2,
		RunKind:    "run_failed",
		Error:      "approval_required",
		UpdatedAt:  now,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-persisted-turn-persisted",
			LastEvent:       "turn.approval_required",
			LastTimestamp:   now,
			LastMessage:     "Waiting for approval",
			History: []agentruntime.Event{
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
	for _, event := range []agentruntime.ActivityEvent{
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
	if err := store.UpdateIssue(liveIssue.ID, map[string]interface{}{"runtime_name": "claude-stdio"}); err != nil {
		t.Fatalf("UpdateIssue live runtime_name failed: %v", err)
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
			AppSession: agentruntime.Session{
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
			AppSession: agentruntime.Session{
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
			AppSession: agentruntime.Session{
				IssueID:         pausedIssue.ID,
				IssueIdentifier: pausedIssue.Identifier,
				SessionID:       "thread-paused-turn-paused",
				LastEvent:       "run.failed",
				LastTimestamp:   now.Add(-2 * time.Minute),
				LastMessage:     "Paused after repeated failures",
			},
		},
		{
			IssueID:           completedIssue.ID,
			Identifier:        completedIssue.Identifier,
			Phase:             "review",
			Attempt:           1,
			RunKind:           "run_completed",
			RuntimeName:       "codex-appserver",
			RuntimeProvider:   "codex",
			RuntimeTransport:  "app_server",
			RuntimeAuthSource: "cli",
			StopReason:        "end_turn",
			UpdatedAt:         now.Add(-3 * time.Minute),
			AppSession: agentruntime.Session{
				IssueID:         completedIssue.ID,
				IssueIdentifier: completedIssue.Identifier,
				SessionID:       "thread-complete-turn-complete",
				LastEvent:       "turn.completed",
				LastTimestamp:   now.Add(-3 * time.Minute),
				LastMessage:     "Completed cleanly",
				Terminal:        true,
				TerminalReason:  "turn.completed",
				Metadata: map[string]interface{}{
					"provider":    "codex",
					"transport":   "app_server",
					"auth_source": "cli",
					"stop_reason": "end_turn",
				},
			},
		},
		{
			IssueID:    interruptedIssue.ID,
			Identifier: interruptedIssue.Identifier,
			Phase:      "implementation",
			Attempt:    4,
			RunKind:    "run_started",
			UpdatedAt:  now.Add(-4 * time.Minute),
			AppSession: agentruntime.Session{
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
			AppSession: agentruntime.Session{
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
			liveIssue.Identifier: agentruntime.Session{
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
				History: []agentruntime.Event{
					{Type: "turn.started", Message: "Applying changes"},
				},
			},
			liveAlphaIssue.Identifier: agentruntime.Session{
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
				History: []agentruntime.Event{
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
	if got := findSessionFeedEntry(t, entries, liveIssue.Identifier)["runtime_name"]; got != "claude-stdio" {
		t.Fatalf("expected live runtime name, got %#v", got)
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
	if got := findSessionFeedEntry(t, entries, completedIssue.Identifier)["stop_reason"]; got != "end_turn" {
		t.Fatalf("expected completed stop reason, got %#v", got)
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
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
	t.Cleanup(srv.Close)

	if err := store.UpsertIssueExecutionSession(kanban.ExecutionSessionSnapshot{
		IssueID:           issue.ID,
		Identifier:        issue.Identifier,
		Phase:             "implementation",
		Attempt:           3,
		RunKind:           "retry_paused",
		RuntimeName:       "claude",
		RuntimeProvider:   "claude",
		RuntimeTransport:  "stdio",
		RuntimeAuthSource: "OAuth",
		Error:             "stall_timeout",
		StopReason:        "end_turn",
		UpdatedAt:         now,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-paused-turn-paused",
			LastEvent:       "item.started",
			LastTimestamp:   now,
			Metadata: map[string]interface{}{
				"provider":           "claude",
				"transport":          "stdio",
				"auth_source":        "OAuth",
				"claude_stop_reason": "end_turn",
			},
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
	if payload["runtime_name"] != "claude" || payload["runtime_provider"] != "claude" || payload["runtime_transport"] != "stdio" || payload["runtime_auth_source"] != "OAuth" {
		t.Fatalf("unexpected runtime surface payload: %#v", payload)
	}
	if payload["stop_reason"] != "end_turn" {
		t.Fatalf("unexpected stop reason payload: %#v", payload)
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
	srv, err := inprocessserver.New(mux)
	if err != nil {
		t.Fatalf("in-process server failed: %v", err)
	}
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
