package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	transport "github.com/mark3labs/mcp-go/client/transport"
	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/providers"
)

func TestMCPJSONAndSchemaHelpers(t *testing.T) {
	t.Run("jsonrpc helpers", func(t *testing.T) {
		if got := normalizeJSONRPCVersion(""); got != mcpapi.JSONRPC_VERSION {
			t.Fatalf("expected default jsonrpc version %q, got %q", mcpapi.JSONRPC_VERSION, got)
		}
		if got := normalizeJSONRPCVersion("2.0"); got != "2.0" {
			t.Fatalf("expected explicit jsonrpc version to survive, got %q", got)
		}

		if _, err := parseRequestID(nil); err == nil {
			t.Fatal("expected parseRequestID to reject nil")
		}
		invalidID := json.RawMessage(`bad`)
		if _, err := parseRequestID(&invalidID); err == nil {
			t.Fatal("expected parseRequestID to reject invalid JSON")
		}

		rawID := json.RawMessage(`"req-7"`)
		id, err := parseRequestID(&rawID)
		if err != nil {
			t.Fatalf("parseRequestID failed: %v", err)
		}
		if got := id.Value(); got != "req-7" {
			t.Fatalf("unexpected parsed request id: %q", got)
		}

		params, err := notificationParams(json.RawMessage(`{"_meta":{"origin":"client"},"scope":"all","limit":10}`))
		if err != nil {
			t.Fatalf("notificationParams failed: %v", err)
		}
		if !reflect.DeepEqual(params.Meta, map[string]any{"origin": "client"}) {
			t.Fatalf("unexpected notification meta: %#v", params.Meta)
		}
		if got := params.AdditionalFields["scope"]; got != "all" {
			t.Fatalf("unexpected notification scope: %#v", got)
		}
		if got := params.AdditionalFields["limit"]; got != float64(10) {
			t.Fatalf("unexpected notification limit: %#v", got)
		}

		empty, err := notificationParams(nil)
		if err != nil {
			t.Fatalf("notificationParams(nil) failed: %v", err)
		}
		if len(empty.AdditionalFields) != 0 || len(empty.Meta) != 0 {
			t.Fatalf("expected empty notification params, got %#v", empty)
		}

		if _, err := notificationParams(json.RawMessage(`[`)); err == nil {
			t.Fatal("expected notificationParams to reject invalid JSON")
		}

		if got := rawParams(json.RawMessage(`null`)); got != nil {
			t.Fatalf("expected nil raw params for null, got %#v", got)
		}
		if got := rawParams(json.RawMessage(`{"ok":true}`)); string(got.(json.RawMessage)) != `{"ok":true}` {
			t.Fatalf("unexpected raw params: %#v", got)
		}
		if got := cloneRawParams(nil); got != nil {
			t.Fatalf("expected nil clone for empty raw params, got %#v", got)
		}

		raw := json.RawMessage(`{"nested":{"ok":true}}`)
		cloned := cloneRawParams(raw).(json.RawMessage)
		raw[2] = 'x'
		if bytes.Equal(raw, cloned) {
			t.Fatal("expected cloneRawParams to copy the raw message")
		}

		request, err := bridgeRequestFromMessage(rawJSONRPCMessage{
			JSONRPC: "2.0",
			ID:      &rawID,
			Method:  "tools/list",
			Params:  json.RawMessage(`{"cursor":"next"}`),
		})
		if err != nil {
			t.Fatalf("bridgeRequestFromMessage failed: %v", err)
		}
		if request.JSONRPC != mcpapi.JSONRPC_VERSION {
			t.Fatalf("unexpected request jsonrpc version: %q", request.JSONRPC)
		}
		if got := request.Method; got != "tools/list" {
			t.Fatalf("unexpected request method: %q", got)
		}
		if got := request.Params.(json.RawMessage); string(got) != `{"cursor":"next"}` {
			t.Fatalf("unexpected cloned request params: %s", string(got))
		}

		notification, err := bridgeNotificationFromMessage(rawJSONRPCMessage{
			JSONRPC: "2.0",
			Method:  initializedNotificationName,
			Params:  json.RawMessage(`{"_meta":{"origin":"client"},"scope":"all"}`),
		})
		if err != nil {
			t.Fatalf("bridgeNotificationFromMessage failed: %v", err)
		}
		if notification.JSONRPC != mcpapi.JSONRPC_VERSION {
			t.Fatalf("unexpected notification jsonrpc version: %q", notification.JSONRPC)
		}
		if notification.Method != initializedNotificationName {
			t.Fatalf("unexpected notification method: %q", notification.Method)
		}
		if got := notification.Params.Meta["origin"]; got != "client" {
			t.Fatalf("unexpected notification meta: %#v", notification.Params.Meta)
		}

		if got := replayModeForMethod(string(mcpapi.MethodInitialize)); got != replayHandshakeNone {
			t.Fatalf("expected initialize to skip replay, got %v", got)
		}
		if got := replayModeForMethod(initializedNotificationName); got != replayHandshakeInitializeOnly {
			t.Fatalf("expected initialized notification to replay initialize only, got %v", got)
		}
		if got := replayModeForMethod("tools/list"); got != replayHandshakeFull {
			t.Fatalf("expected tools/list to replay fully, got %v", got)
		}

		if _, err := bridgeRequestFromMessage(rawJSONRPCMessage{ID: &invalidID, Method: "tools/list"}); err == nil {
			t.Fatal("expected bridgeRequestFromMessage to reject invalid ids")
		}
		if _, err := bridgeNotificationFromMessage(rawJSONRPCMessage{Method: "notifications/initialized", Params: json.RawMessage(`[`)}); err == nil {
			t.Fatal("expected bridgeNotificationFromMessage to reject invalid params")
		}

		if !canReplayRequestMethod(string(mcpapi.MethodInitialize)) {
			t.Fatal("expected initialize requests to be replayable")
		}
		if canReplayRequestMethod("tools/call") {
			t.Fatal("expected tools/call requests to stay non-replayable")
		}
		if !canReplayNotificationMethod(initializedNotificationName) {
			t.Fatal("expected initialized notification to be replayable")
		}
		if canReplayNotificationMethod("notifications/other") {
			t.Fatal("expected non-initialized notifications to stay non-replayable")
		}

		if !shouldReconnect(transport.ErrSessionTerminated) {
			t.Fatal("expected session termination to trigger reconnect")
		}
		if !shouldReconnect(&url.Error{Err: errors.New("boom")}) {
			t.Fatal("expected url errors to trigger reconnect")
		}
		if shouldReconnect(nil) {
			t.Fatal("expected nil error to skip reconnect")
		}
		if shouldReconnect(errors.New("boom")) {
			t.Fatal("expected generic errors to skip reconnect")
		}

		if got := generateServerInstanceID(); !strings.HasPrefix(got, "mcp_") || len(got) != 20 {
			t.Fatalf("unexpected server instance id format: %q", got)
		}
	})

	t.Run("argument helpers", func(t *testing.T) {
		args := map[string]any{
			"float":  float64(7),
			"int":    8,
			"int64":  int64(9),
			"list":   []any{"alpha", 1, "beta"},
			"strings": []string{"one", "two"},
			"bool":   true,
			"object": map[string]any{"nested": "value"},
		}

		if got := intArg(args, "float", 1); got != 7 {
			t.Fatalf("unexpected float arg: %d", got)
		}
		if got := intArg(args, "int", 1); got != 8 {
			t.Fatalf("unexpected int arg: %d", got)
		}
		if got := intArg(args, "int64", 1); got != 9 {
			t.Fatalf("unexpected int64 arg: %d", got)
		}
		if got := intArg(args, "missing", 11); got != 11 {
			t.Fatalf("unexpected fallback int arg: %d", got)
		}
		if got := intArg(map[string]any{"bad": "value"}, "bad", 13); got != 13 {
			t.Fatalf("expected unsupported int arg to use fallback, got %d", got)
		}

		if got := stringListArg(args, "list"); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
			t.Fatalf("unexpected list arg: %#v", got)
		}
		if got := stringListArg(args, "strings"); !reflect.DeepEqual(got, []string{"one", "two"}) {
			t.Fatalf("unexpected string slice arg: %#v", got)
		}
		if got := stringListArg(args, "missing"); got != nil {
			t.Fatalf("expected nil for missing list, got %#v", got)
		}

		if got, ok := boolPointerArg(args, "bool"); !ok || got == nil || !*got {
			t.Fatalf("unexpected bool arg: %#v %v", got, ok)
		}
		if got, ok := boolPointerArg(args, "missing"); ok || got != nil {
			t.Fatalf("expected missing bool arg to be absent, got %#v %v", got, ok)
		}
		if got, ok := boolPointerArg(map[string]any{"bad": "value"}, "bad"); ok || got != nil {
			t.Fatalf("expected unsupported bool arg to be absent, got %#v %v", got, ok)
		}

		if got := objectArg(args, "object"); !reflect.DeepEqual(got, map[string]any{"nested": "value"}) {
			t.Fatalf("unexpected object arg: %#v", got)
		}
		if got := objectArg(args, "list"); got != nil {
			t.Fatalf("expected non-object to return nil, got %#v", got)
		}
		if got := objectArg(map[string]any{}, "missing"); got != nil {
			t.Fatalf("expected missing object to return nil, got %#v", got)
		}

		if got := asString(123); got != "" {
			t.Fatalf("expected non-string to coerce to empty string, got %q", got)
		}
		if got := asString("hello"); got != "hello" {
			t.Fatalf("unexpected string conversion: %q", got)
		}

		if got := objectProperty("desc"); !reflect.DeepEqual(got, map[string]any{"type": "object", "description": "desc"}) {
			t.Fatalf("unexpected objectProperty payload: %#v", got)
		}
		if got := stringArrayProperty("labels"); !reflect.DeepEqual(got, map[string]any{
			"type":        "array",
			"description": "labels",
			"items":       map[string]any{"type": "string"},
		}) {
			t.Fatalf("unexpected stringArrayProperty payload: %#v", got)
		}
		if got := objectTool("tool", "desc", map[string]any{"title": stringProperty("title")}); got.Name != "tool" || got.InputSchema.Type != "object" {
			t.Fatalf("unexpected objectTool: %#v", got)
		}

		schema := extensionToolInputSchema(map[string]any{
			"inputSchema": map[string]any{
				"type":       "array",
				"properties": map[string]any{"title": stringProperty("title")},
			},
		})
		if schema.Type != "array" {
			t.Fatalf("unexpected schema type: %q", schema.Type)
		}
		if !reflect.DeepEqual(schema.Properties, map[string]any{"title": stringProperty("title")}) {
			t.Fatalf("unexpected schema properties: %#v", schema.Properties)
		}
		if got := extensionToolInputSchema(map[string]any{}); got.Type != "object" || got.Properties != nil {
			t.Fatalf("expected default object schema, got %#v", got)
		}
	})
}

func TestMCPBridgeHelpers(t *testing.T) {
	var stdout bytes.Buffer
	bridge := &stdioBridge{
		stdout:           &stdout,
		pendingResponses: map[string]chan *transport.JSONRPCResponse{},
	}

	t.Run("handleRemoteNotification writes JSON", func(t *testing.T) {
		stdout.Reset()
		bridge.handleRemoteNotification(mcpapi.JSONRPCNotification{
			JSONRPC: mcpapi.JSONRPC_VERSION,
			Notification: mcpapi.Notification{
				Method: initializedNotificationName,
				Params: mcpapi.NotificationParams{},
			},
		})
		responses := decodeBridgeResponses(t, stdout.Bytes())
		if len(responses) != 1 || responses[0]["method"] != initializedNotificationName {
			t.Fatalf("unexpected notification output: %#v", responses)
		}
	})

	t.Run("handleIncomingMessage routes requests notifications and responses", func(t *testing.T) {
		stdout.Reset()
		requestID := mcpapi.NewRequestId("req-1")
		remote := &fakeBridgeRemote{
			sendRequestFunc: func(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
				if request.Method != string(mcpapi.MethodInitialize) {
					return nil, fmt.Errorf("unexpected request method %q", request.Method)
				}
				return fakeBridgeResponse(request.ID, `{"protocolVersion":"`+mcpapi.LATEST_PROTOCOL_VERSION+`"}`), nil
			},
			sendNotificationFunc: func(ctx context.Context, notification mcpapi.JSONRPCNotification) error {
				if notification.Method != initializedNotificationName {
					return fmt.Errorf("unexpected notification %q", notification.Method)
				}
				return nil
			},
		}
		bridge.remote = remote

		if err := bridge.handleIncomingMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":"req-1","method":"initialize","params":{"protocolVersion":"`+mcpapi.LATEST_PROTOCOL_VERSION+`","clientInfo":{"name":"bridge-test","version":"1.0.0"},"capabilities":{}}}`)); err != nil {
			t.Fatalf("handleIncomingMessage(request) failed: %v", err)
		}
		out := decodeBridgeResponses(t, stdout.Bytes())
		if len(out) != 1 {
			t.Fatalf("expected one request response, got %#v", out)
		}
		if out[0]["id"] != "req-1" {
			t.Fatalf("unexpected response id: %#v", out[0])
		}
		result := out[0]["result"].(map[string]any)
		if result["protocolVersion"] != mcpapi.LATEST_PROTOCOL_VERSION {
			t.Fatalf("unexpected initialize result: %#v", result)
		}

		initializeRequest, initializedNotification := bridge.cachedHandshake()
		if initializeRequest == nil || initializedNotification != nil {
			t.Fatalf("expected initialize request to be cached and notification to be empty, got %#v %#v", initializeRequest, initializedNotification)
		}

		stdout.Reset()
		if err := bridge.handleIncomingMessage(context.Background(), []byte(`{"method":"notifications/initialized","params":{}}`)); err != nil {
			t.Fatalf("handleIncomingMessage(notification) failed: %v", err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("expected notification to be forwarded without stdout output, got %q", stdout.String())
		}
		_, initializedNotification = bridge.cachedHandshake()
		if initializedNotification == nil || initializedNotification.Method != initializedNotificationName {
			t.Fatalf("expected initialized notification to be cached, got %#v", initializedNotification)
		}

		responseCh := make(chan *transport.JSONRPCResponse, 1)
		bridge.pendingResponses[requestID.String()] = responseCh
		if err := bridge.handleIncomingMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":"req-1","result":{"ok":true}}`)); err != nil {
			t.Fatalf("handleIncomingMessage(response) failed: %v", err)
		}
		select {
		case response := <-responseCh:
			if response == nil || string(response.Result) != `{"ok":true}` {
				t.Fatalf("unexpected pending response: %#v", response)
			}
		default:
			t.Fatal("expected response to be delivered to pending channel")
		}
	})

	t.Run("openRemote sets handlers and closes failed remotes", func(t *testing.T) {
		var started, closed bool
		remote := &fakeBridgeRemote{
			startFunc: func(ctx context.Context) error {
				started = true
				return nil
			},
			closeFunc: func() error {
				closed = true
				return nil
			},
		}
		bridge := newStdioBridge(filepath.Join(t.TempDir(), "maestro.db"), nil, &bytes.Buffer{}, &bytes.Buffer{})
		bridge.newRemote = func(entry DaemonEntry) (transport.BidirectionalInterface, error) {
			return remote, nil
		}

		got, err := bridge.openRemote(context.Background(), DaemonEntry{BaseURL: "http://example/mcp", BearerToken: "token"})
		if err != nil {
			t.Fatalf("openRemote failed: %v", err)
		}
		if got != remote {
			t.Fatalf("unexpected remote returned: %#v", got)
		}
		if !started {
			t.Fatal("expected remote.Start to be called")
		}
		if remote.requestHandler == nil || remote.notificationHandler == nil {
			t.Fatal("expected remote handlers to be installed")
		}

		failing := &fakeBridgeRemote{
			startFunc: func(context.Context) error {
				return errors.New("boom")
			},
			closeFunc: func() error {
				closed = true
				return nil
			},
		}
		bridge.newRemote = func(entry DaemonEntry) (transport.BidirectionalInterface, error) {
			return failing, nil
		}
		if _, err := bridge.openRemote(context.Background(), DaemonEntry{BaseURL: "http://example/mcp", BearerToken: "token"}); err == nil {
			t.Fatal("expected failing openRemote to return an error")
		}
		if !closed {
			t.Fatal("expected failing remote to be closed")
		}
	})
}

func TestMCPServerHelpers(t *testing.T) {
	store := testStore(t, filepath.Join(t.TempDir(), "server.db"))
	project, err := store.CreateProject("Project", "Description", "/repo", "")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	issue, err := store.CreateIssueWithOptions(project.ID, "", "Issue", "", 0, nil, kanban.IssueCreateOptions{})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions failed: %v", err)
	}

	server := &Server{
		store:      store,
		service:    providers.NewService(store),
		provider:   testRuntimeProvider{store: store, scopedRepoPath: "/repo"},
		extensions: nil,
		instanceID: "instance-1",
		tools:      nil,
	}

	t.Run("response meta and envelope helpers", func(t *testing.T) {
		nilServer := &Server{instanceID: "instance-2"}
		if got := nilServer.responseMeta(); got.ServerInstanceID != "instance-2" || got.DBPath != "" || got.StoreID != "" || got.ChangeSeq != 0 {
			t.Fatalf("unexpected nil-store response meta: %#v", got)
		}

		meta := server.responseMeta()
		if meta.ServerInstanceID != "instance-1" {
			t.Fatalf("unexpected server instance id: %#v", meta)
		}
		if meta.DBPath != store.Identity().DBPath || meta.StoreID != store.Identity().StoreID {
			t.Fatalf("unexpected store metadata: %#v", meta)
		}

		result := server.toolResult("tool", map[string]any{"ok": true})
		envelope, err := decodeEnvelopeResult(result)
		if err != nil {
			t.Fatalf("decodeEnvelopeResult(toolResult) failed: %v", err)
		}
		if !envelope.OK || envelope.Tool != "tool" {
			t.Fatalf("unexpected tool result envelope: %#v", envelope)
		}

		errResult := server.toolError("tool", "boom")
		envelope, err = decodeEnvelopeResult(errResult)
		if err != nil {
			t.Fatalf("decodeEnvelopeResult(toolError) failed: %v", err)
		}
		if envelope.OK || envelope.Error == nil || envelope.Error.Message != "boom" {
			t.Fatalf("unexpected tool error envelope: %#v", envelope)
		}

		badResult := server.toolResult("tool", map[string]any{"fn": func() {}})
		if !badResult.IsError {
			t.Fatal("expected marshal failure to produce an error result")
		}
		content := badResult.Content[0].(mcpapi.TextContent).Text
		if !strings.Contains(content, "failed to encode response") {
			t.Fatalf("unexpected marshal failure body: %s", content)
		}

		if _, err := decodeEnvelopeResult(nil); err == nil {
			t.Fatal("expected nil call tool result to fail decoding")
		}
		if _, err := decodeEnvelopeResult(&mcpapi.CallToolResult{}); err == nil {
			t.Fatal("expected empty call tool result to fail decoding")
		}
		if _, err := decodeEnvelopeResult(&mcpapi.CallToolResult{Content: []mcpapi.Content{mcpapi.TextContent{Type: "text", Text: "not json"}}}); err == nil {
			t.Fatal("expected invalid JSON to fail decoding")
		}
		if _, err := decodeEnvelopeResult(&mcpapi.CallToolResult{Content: []mcpapi.Content{mcpapi.ImageContent{Type: "image"}}}); err == nil {
			t.Fatal("expected unexpected content type to fail decoding")
		}
	})

	t.Run("pagination helpers", func(t *testing.T) {
		if limit, offset := normalizePaginationArgs(map[string]any{"limit": 0, "offset": -5}); limit != mcpPaginationDefaultLimit || offset != 0 {
			t.Fatalf("unexpected normalized pagination args: %d %d", limit, offset)
		}
		if limit, offset := normalizePaginationArgs(map[string]any{"limit": 25, "offset": 3}); limit != 25 || offset != 3 {
			t.Fatalf("unexpected pagination args: %d %d", limit, offset)
		}

		items := []string{"a", "b", "c", "d"}
		page, total, returned := paginateItems(items, 2, 1)
		if total != 4 || returned != 3 || !reflect.DeepEqual(page, []string{"b", "c"}) {
			t.Fatalf("unexpected paginated page: total=%d returned=%d page=%#v", total, returned, page)
		}

		payload := server.paginationPayload("list_issues", map[string]any{"project_id": "proj-1"}, 4, 2, 0, 2)
		if got := payload["has_more"]; got != true {
			t.Fatalf("expected more pages, got %#v", payload)
		}
		nextRequest := payload["next_request"].(map[string]any)
		if nextRequest["tool"] != "list_issues" {
			t.Fatalf("unexpected next request: %#v", payload)
		}
		if payload["next_hint"] != "Use pagination.next_request to fetch the next batch." {
			t.Fatalf("unexpected next hint: %#v", payload["next_hint"])
		}

		noMore := server.paginationPayload("list_issues", nil, 4, 2, 2, 2)
		if got := noMore["has_more"]; got != false {
			t.Fatalf("expected pagination to end, got %#v", noMore)
		}
		if noMore["next_request"] != nil {
			t.Fatalf("expected no next request, got %#v", noMore["next_request"])
		}
	})

	t.Run("repo scope helpers", func(t *testing.T) {
		scoped := &Server{provider: testRuntimeProvider{store: store, scopedRepoPath: "/repo"}}
		if got := scoped.scopedRepoPath(); got != "/repo" {
			t.Fatalf("unexpected scoped repo path: %q", got)
		}
		if err := scoped.validateScopedRepoPath("/repo"); err != nil {
			t.Fatalf("expected matching repo path to pass: %v", err)
		}
		if err := scoped.validateScopedRepoPath("/elsewhere"); err == nil || !strings.Contains(err.Error(), "repo_path must match") {
			t.Fatalf("expected scope mismatch, got %v", err)
		}

		project := &kanban.Project{RepoPath: "/repo", OrchestrationReady: true}
		scoped.decorateProject(project)
		if !project.DispatchReady || project.DispatchError != "" {
			t.Fatalf("expected matching repo path to keep dispatch ready, got %#v", project)
		}

		project = &kanban.Project{RepoPath: "/elsewhere", OrchestrationReady: true}
		scoped.decorateProject(project)
		if project.DispatchReady || !strings.Contains(project.DispatchError, "outside the current server scope") {
			t.Fatalf("expected out-of-scope project to be gated, got %#v", project)
		}

		projects := []kanban.Project{{RepoPath: "/repo", OrchestrationReady: true}, {RepoPath: "/elsewhere", OrchestrationReady: true}}
		scoped.decorateProjects(projects)
		if !projects[0].DispatchReady || projects[1].DispatchReady {
			t.Fatalf("unexpected decorated project slice: %#v", projects)
		}

		summaries := []kanban.ProjectSummary{{Project: kanban.Project{RepoPath: "/repo", OrchestrationReady: true}}}
		scoped.decorateProjectSummaries(summaries)
		if !summaries[0].DispatchReady {
			t.Fatalf("expected decorated project summary to be dispatch ready: %#v", summaries[0])
		}
	})

	t.Run("issue lookup and mutation helpers", func(t *testing.T) {
		if got := server.issueTransitionToolError("set_state", "Failed to update issue state", fmt.Errorf("%w: cannot move issue to in_progress: blocked by ISSUE-1", kanban.ErrBlockedTransition)); !strings.Contains(got.Content[0].(mcpapi.TextContent).Text, "blocked by ISSUE-1") {
			t.Fatalf("expected blocked transition error to be preserved, got %#v", got)
		}

		generic := server.issueTransitionToolError("set_state", "Failed to update issue state", errors.New("boom"))
		if !strings.Contains(generic.Content[0].(mcpapi.TextContent).Text, "Failed to update issue state: boom") {
			t.Fatalf("expected generic transition error to be prefixed, got %#v", generic)
		}

		if got := server.runtimeUnavailable("run_project"); !strings.Contains(got.Content[0].(mcpapi.TextContent).Text, "runtime_unavailable") {
			t.Fatalf("unexpected runtime unavailable payload: %#v", got)
		}

		createdIssue, err := server.lookupIssue(context.Background(), issue.Identifier)
		if err != nil || createdIssue.Identifier != issue.Identifier {
			t.Fatalf("expected direct lookup to succeed: %v %#v", err, createdIssue)
		}

		lookupServer := &Server{
			store: store,
			service: providers.NewService(store),
			provider: testRuntimeProvider{store: store},
		}
		if got, err := lookupServer.lookupIssue(context.Background(), createdIssue.Identifier); err != nil || got.Identifier != createdIssue.Identifier {
			t.Fatalf("expected issue lookup to succeed, got %#v %v", got, err)
		}

		if _, err := lookupServer.lookupIssue(context.Background(), "missing-issue"); err == nil || !strings.Contains(err.Error(), "Issue not found") {
			t.Fatalf("expected missing issue error, got %v", err)
		}

		closedStore := testStore(t, filepath.Join(t.TempDir(), "closed.db"))
		closedServer := &Server{
			store:   closedStore,
			service: providers.NewService(closedStore),
		}
		if err := closedStore.Close(); err != nil {
			t.Fatalf("closedStore.Close failed: %v", err)
		}
		if _, err := closedServer.lookupIssue(context.Background(), issue.Identifier); err == nil {
			t.Fatal("expected closed store lookup to return an error")
		}

		updates := issueMutationArgs(map[string]any{
			"project_id":  "project-1",
			"epic_id":     "epic-1",
			"title":       "Updated title",
			"description": "Updated description",
			"issue_type":   "task",
			"cron":        "0 0 * * *",
			"enabled":     true,
			"priority":    float64(3),
			"labels":      []any{"one", "two"},
			"blocked_by":  []string{"ISSUE-1"},
			"branch_name": "feature/branch",
			"pr_url":      "https://example.com/pr/1",
		}, true)
		if updates["priority"] != 3 || updates["enabled"] != true || !reflect.DeepEqual(updates["labels"], []string{"one", "two"}) {
			t.Fatalf("unexpected issue mutation updates: %#v", updates)
		}
		if updates["project_id"] != "project-1" || updates["epic_id"] != "epic-1" {
			t.Fatalf("expected project fields to be included: %#v", updates)
		}

		commentInput := issueCommentMutationArgs(map[string]any{
			"parent_comment_id":    "parent-1",
			"body":                 "hello",
			"attachment_paths":     []any{"/tmp/one", "/tmp/two"},
			"remove_attachment_ids": []string{"att-1"},
		})
		if commentInput.ParentCommentID != "parent-1" || commentInput.Body == nil || *commentInput.Body != "hello" {
			t.Fatalf("unexpected comment mutation input: %#v", commentInput)
		}
		if !reflect.DeepEqual(commentInput.RemoveAttachmentIDs, []string{"att-1"}) {
			t.Fatalf("unexpected attachment removals: %#v", commentInput.RemoveAttachmentIDs)
		}
		if got := len(commentInput.Attachments); got != 2 || commentInput.Attachments[0].Path != "/tmp/one" {
			t.Fatalf("unexpected comment attachments: %#v", commentInput.Attachments)
		}
	})

	t.Run("envelope encoding fallback", func(t *testing.T) {
		result := server.toolResult("broken", map[string]any{"func": func() {}})
		if !result.IsError {
			t.Fatal("expected unsupported data to force an encoding error")
		}
		if !strings.Contains(result.Content[0].(mcpapi.TextContent).Text, "failed to encode response") {
			t.Fatalf("unexpected fallback body: %s", result.Content[0].(mcpapi.TextContent).Text)
		}
	})
}
