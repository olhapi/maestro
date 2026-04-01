package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver/protocol"
)

func writePythonAppServerScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "appserver.py")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write app-server script: %v", err)
	}
	return path
}

func TestInitializeUsesGeneratedRequestID(t *testing.T) {
	stdin := &bufferWriteCloser{}
	client := &Client{
		cfg: ClientConfig{
			Workspace:    t.TempDir(),
			ReadTimeout:  100 * time.Millisecond,
			TurnTimeout:  100 * time.Millisecond,
			StallTimeout: 100 * time.Millisecond,
		},
		stdin:               stdin,
		lines:               make(chan string, 2),
		lineErr:             make(chan error, 1),
		session:             &Session{MaxHistory: 4},
		logger:              discardLogger(),
		nextID:              7,
		pendingInteractions: make(map[string]*interactionWaiter),
	}
	client.lines <- `{"id":7,"result":{}}`
	client.lines <- `{"id":8,"result":{"thread":{"id":"thread-1"}}}`

	if err := client.initialize(context.Background()); err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdin.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected initialize and thread/start requests, got %q", stdin.String())
	}
	if !strings.Contains(lines[0], `"id":7`) {
		t.Fatalf("expected initialize request to use generated id 7, got %q", lines[0])
	}
	if !strings.Contains(stdin.String(), `"id":8`) {
		t.Fatalf("expected thread/start request to advance request id, got %q", stdin.String())
	}
}

func TestWaitForPendingInteractionAllowsSynchronousResponse(t *testing.T) {
	stdin := &bufferWriteCloser{}
	doneIDs := make(chan string, 1)
	interactionIDs := make(chan string, 1)
	responseErrs := make(chan error, 1)
	client := &Client{
		cfg: ClientConfig{
			IssueID:         "issue-1",
			IssueIdentifier: "ISS-1",
			Workspace:       t.TempDir(),
			ReadTimeout:     100 * time.Millisecond,
			OnPendingInteractionDone: func(interactionID string) {
				doneIDs <- interactionID
			},
		},
		stdin:               stdin,
		lines:               make(chan string),
		lineErr:             make(chan error, 1),
		waitCh:              make(chan error, 1),
		session:             &Session{SessionID: "session-1", ThreadID: "thread-1", TurnID: "turn-1", MaxHistory: 4},
		logger:              discardLogger(),
		pendingInteractions: make(map[string]*interactionWaiter),
	}
	client.cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction == nil {
			responseErrs <- fmt.Errorf("nil interaction")
			return
		}
		interactionIDs <- interaction.ID
		responseErrs <- client.RespondToInteraction(context.Background(), interaction.ID, PendingInteractionResponse{
			Decision: "acceptForSession",
		})
	}

	msg, ok := protocol.DecodeMessage(`{"id":99,"method":"item/fileChange/requestApproval","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"file-change-1","reason":"Need approval"}}`)
	if !ok {
		t.Fatal("expected test payload to decode")
	}

	resultCh := make(chan error, 1)
	go func() {
		handled, err := client.waitForPendingInteraction(context.Background(), msg)
		if err != nil {
			resultCh <- err
			return
		}
		if !handled {
			resultCh <- fmt.Errorf("expected request to be handled")
			return
		}
		resultCh <- nil
	}()

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("waitForPendingInteraction failed: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for synchronous pending interaction response")
	}

	select {
	case err := <-responseErrs:
		if err != nil {
			t.Fatalf("synchronous response failed: %v", err)
		}
	default:
		t.Fatal("expected callback to respond synchronously")
	}

	var interactionID string
	select {
	case interactionID = <-interactionIDs:
	case <-time.After(1 * time.Second):
		t.Fatal("expected callback to observe pending interaction")
	}

	select {
	case doneID := <-doneIDs:
		if doneID != interactionID {
			t.Fatalf("expected pending interaction %q to be cleared, got %q", interactionID, doneID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected pending interaction cleanup callback")
	}

	if len(client.pendingInteractions) != 0 {
		t.Fatalf("expected pending interactions to be cleared, got %#v", client.pendingInteractions)
	}
	if !strings.Contains(stdin.String(), `"decision":"acceptForSession"`) {
		t.Fatalf("expected approval response in output, got %q", stdin.String())
	}
}

func TestWaitForPendingInteractionSupportsMCPServerElicitationRequests(t *testing.T) {
	stdin := &bufferWriteCloser{}
	doneIDs := make(chan string, 1)
	interactionIDs := make(chan *PendingInteraction, 1)
	responseErrs := make(chan error, 1)
	client := &Client{
		cfg: ClientConfig{
			IssueID:         "issue-1",
			IssueIdentifier: "ISS-1",
			Workspace:       t.TempDir(),
			ReadTimeout:     100 * time.Millisecond,
			OnPendingInteractionDone: func(interactionID string) {
				doneIDs <- interactionID
			},
		},
		stdin:               stdin,
		lines:               make(chan string),
		lineErr:             make(chan error, 1),
		waitCh:              make(chan error, 1),
		session:             &Session{SessionID: "session-1", ThreadID: "thread-1", TurnID: "turn-1", MaxHistory: 4},
		logger:              discardLogger(),
		pendingInteractions: make(map[string]*interactionWaiter),
	}
	client.cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction == nil {
			responseErrs <- fmt.Errorf("nil interaction")
			return
		}
		cloned := interaction.Clone()
		interactionIDs <- &cloned
		responseErrs <- client.RespondToInteraction(context.Background(), interaction.ID, PendingInteractionResponse{
			Action: "accept",
			Content: map[string]interface{}{
				"email": "ops@example.com",
			},
		})
	}

	msg, ok := protocol.DecodeMessage(`{"id":99,"method":"mcpServer/elicitation/request","params":{"serverName":"support-bot","threadId":"thread-1","turnId":"turn-1","message":"Need contact details","mode":"form","requestedSchema":{"type":"object","properties":{"email":{"type":"string","default":"ops@example.com"},"notify":{"type":"boolean","default":true},"priority":{"type":"number","default":3.5}},"required":["email"]}}}`)
	if !ok {
		t.Fatal("expected test payload to decode")
	}

	resultCh := make(chan error, 1)
	go func() {
		handled, err := client.waitForPendingInteraction(context.Background(), msg)
		if err != nil {
			resultCh <- err
			return
		}
		if !handled {
			resultCh <- fmt.Errorf("expected request to be handled")
			return
		}
		resultCh <- nil
	}()

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("waitForPendingInteraction failed: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for synchronous elicitation response")
	}

	select {
	case err := <-responseErrs:
		if err != nil {
			t.Fatalf("synchronous response failed: %v", err)
		}
	default:
		t.Fatal("expected callback to respond synchronously")
	}

	var interaction *PendingInteraction
	select {
	case interaction = <-interactionIDs:
	case <-time.After(1 * time.Second):
		t.Fatal("expected callback to observe pending interaction")
	}
	if interaction.Kind != PendingInteractionKindElicitation {
		t.Fatalf("expected elicitation interaction, got %+v", interaction)
	}
	if interaction.Elicitation == nil || interaction.Elicitation.Mode != "form" || interaction.Elicitation.ServerName != "support-bot" || interaction.Elicitation.Message != "Need contact details" {
		t.Fatalf("unexpected elicitation payload: %+v", interaction)
	}
	if got := interaction.Elicitation.RequestedSchema["type"]; got != "object" {
		t.Fatalf("expected requested schema to survive round trip, got %#v", got)
	}
	properties, ok := interaction.Elicitation.RequestedSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected requested schema properties to survive round trip, got %#v", interaction.Elicitation.RequestedSchema["properties"])
	}
	email, ok := properties["email"].(map[string]interface{})
	if !ok || email["default"] != "ops@example.com" {
		t.Fatalf("expected string default to survive round trip, got %#v", properties["email"])
	}
	notify, ok := properties["notify"].(map[string]interface{})
	if !ok || notify["default"] != true {
		t.Fatalf("expected boolean default to survive round trip, got %#v", properties["notify"])
	}
	priority, ok := properties["priority"].(map[string]interface{})
	if !ok || priority["default"] != 3.5 {
		t.Fatalf("expected number default to survive round trip, got %#v", properties["priority"])
	}

	select {
	case doneID := <-doneIDs:
		if doneID != interaction.ID {
			t.Fatalf("expected pending interaction %q to be cleared, got %q", interaction.ID, doneID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected pending interaction cleanup callback")
	}

	if len(client.pendingInteractions) != 0 {
		t.Fatalf("expected pending interactions to be cleared, got %#v", client.pendingInteractions)
	}
	if !strings.Contains(stdin.String(), `"action":"accept"`) || !strings.Contains(stdin.String(), `"email":"ops@example.com"`) {
		t.Fatalf("expected elicitation response in output, got %q", stdin.String())
	}
}

func TestWaitForPendingInteractionPreservesRichElicitationSchemas(t *testing.T) {
	stdin := &bufferWriteCloser{}
	interactionIDs := make(chan *PendingInteraction, 1)
	responseErrs := make(chan error, 1)
	client := &Client{
		cfg: ClientConfig{
			IssueID:         "issue-1",
			IssueIdentifier: "ISS-1",
			Workspace:       t.TempDir(),
			ReadTimeout:     100 * time.Millisecond,
		},
		stdin:               stdin,
		lines:               make(chan string),
		lineErr:             make(chan error, 1),
		waitCh:              make(chan error, 1),
		session:             &Session{SessionID: "session-1", ThreadID: "thread-1", TurnID: "turn-1", MaxHistory: 4},
		logger:              discardLogger(),
		pendingInteractions: make(map[string]*interactionWaiter),
	}
	client.cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction == nil {
			responseErrs <- fmt.Errorf("nil interaction")
			return
		}
		cloned := interaction.Clone()
		interactionIDs <- &cloned
		responseErrs <- client.RespondToInteraction(context.Background(), interaction.ID, PendingInteractionResponse{
			Action: "accept",
			Content: map[string]interface{}{
				"profile": map[string]interface{}{
					"name": "Ada",
				},
			},
		})
	}

	rawMessage, err := json.Marshal(map[string]interface{}{
		"id":     99,
		"method": "mcpServer/elicitation/request",
		"params": map[string]interface{}{
			"serverName": "support-bot",
			"threadId":   "thread-1",
			"turnId":     "turn-1",
			"message":    "Need operator preferences",
			"mode":       "form",
			"requestedSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"profile": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name": map[string]interface{}{
								"type":    "string",
								"default": "Ada",
							},
							"contact": map[string]interface{}{
								"oneOf": []interface{}{
									map[string]interface{}{
										"title": "Email",
										"type":  "object",
										"properties": map[string]interface{}{
											"address": map[string]interface{}{
												"type":   "string",
												"format": "email",
											},
										},
										"required": []string{"address"},
									},
									map[string]interface{}{
										"title": "Webhook",
										"type":  "object",
										"properties": map[string]interface{}{
											"endpoint": map[string]interface{}{
												"type":   "string",
												"format": "uri",
											},
										},
										"required": []string{"endpoint"},
									},
								},
							},
						},
						"required": []string{"name"},
					},
					"tags": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type":      "string",
							"enum":      []string{"alpha", "beta"},
							"enumNames": []string{"Alpha", "Beta"},
						},
						"default": []string{"alpha"},
					},
				},
				"required": []string{"profile"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal elicitation request: %v", err)
	}
	msg, ok := protocol.DecodeMessage(string(rawMessage))
	if !ok {
		t.Fatal("expected test payload to decode")
	}

	resultCh := make(chan error, 1)
	go func() {
		handled, err := client.waitForPendingInteraction(context.Background(), msg)
		if err != nil {
			resultCh <- err
			return
		}
		if !handled {
			resultCh <- fmt.Errorf("expected request to be handled")
			return
		}
		resultCh <- nil
	}()

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("waitForPendingInteraction failed: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for synchronous elicitation response")
	}

	select {
	case err := <-responseErrs:
		if err != nil {
			t.Fatalf("synchronous response failed: %v", err)
		}
	default:
		t.Fatal("expected callback to respond synchronously")
	}

	var interaction *PendingInteraction
	select {
	case interaction = <-interactionIDs:
	case <-time.After(1 * time.Second):
		t.Fatal("expected callback to observe pending interaction")
	}
	if interaction.Elicitation == nil {
		t.Fatalf("expected elicitation payload, got %+v", interaction)
	}

	properties, ok := interaction.Elicitation.RequestedSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected requested schema properties to survive round trip, got %#v", interaction.Elicitation.RequestedSchema["properties"])
	}
	profile, ok := properties["profile"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested object property to survive, got %#v", properties["profile"])
	}
	profileProperties, ok := profile["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested object properties to survive, got %#v", profile["properties"])
	}
	name, ok := profileProperties["name"].(map[string]interface{})
	if !ok || name["default"] != "Ada" {
		t.Fatalf("expected nested string default to survive, got %#v", profileProperties["name"])
	}
	contact, ok := profileProperties["contact"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested union property to survive, got %#v", profileProperties["contact"])
	}
	branches, ok := contact["oneOf"].([]interface{})
	if !ok || len(branches) != 2 {
		t.Fatalf("expected oneOf branches to survive, got %#v", contact["oneOf"])
	}
	firstBranch, ok := branches[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected first oneOf branch map, got %#v", branches[0])
	}
	firstBranchProperties, ok := firstBranch["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected oneOf branch properties to survive, got %#v", firstBranch["properties"])
	}
	address, ok := firstBranchProperties["address"].(map[string]interface{})
	if !ok || address["format"] != "email" {
		t.Fatalf("expected oneOf branch field metadata to survive, got %#v", firstBranchProperties["address"])
	}
	tags, ok := properties["tags"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected array property to survive, got %#v", properties["tags"])
	}
	if got := tags["default"]; fmt.Sprintf("%v", got) != "[alpha]" {
		t.Fatalf("expected array default to survive, got %#v", tags["default"])
	}
	items, ok := tags["items"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected array items to survive, got %#v", tags["items"])
	}
	enumValues, ok := items["enum"].([]interface{})
	if !ok || len(enumValues) != 2 || enumValues[0] != "alpha" || enumValues[1] != "beta" {
		t.Fatalf("expected array enum values to survive, got %#v", items["enum"])
	}

	if !strings.Contains(stdin.String(), `"action":"accept"`) || !strings.Contains(stdin.String(), `"profile":{"name":"Ada"}`) {
		t.Fatalf("expected elicitation response in output, got %q", stdin.String())
	}
}

func TestAwaitTurnCompletionFailsFastOnUnsupportedIdBearingRequest(t *testing.T) {
	client := &Client{
		cfg: ClientConfig{
			ReadTimeout:  20 * time.Millisecond,
			TurnTimeout:  200 * time.Millisecond,
			StallTimeout: 100 * time.Millisecond,
		},
		lines:   make(chan string, 1),
		lineErr: make(chan error, 1),
		session: &Session{ThreadID: "thread-1", TurnID: "turn-1", MaxHistory: 4},
		logger:  discardLogger(),
	}
	client.lines <- `{"id":77,"method":"custom/request","params":{}}`

	err := client.awaitTurnCompletion(context.Background())
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Kind != "unsupported_request" {
		t.Fatalf("expected unsupported_request run error, got %v", err)
	}
}

func TestRunReturnsCloseErrorAfterTurnCompletion(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-CLOSE")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	script := `import json
import signal
import sys
import time

signal.signal(signal.SIGTERM, signal.SIG_IGN)
signal.signal(signal.SIGINT, signal.SIG_IGN)

def emit(payload):
    print(json.dumps(payload), flush=True)

for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue
    payload = json.loads(raw)
    method = payload.get("method")
    request_id = payload.get("id")
    if method == "initialize":
        emit({"id": request_id, "result": {}})
    elif method == "initialized":
        continue
    elif method == "thread/start":
        emit({"id": request_id, "result": {"thread": {"id": "thread-close"}}})
    elif method == "turn/start":
        emit({"id": request_id, "result": {"turn": {"id": "turn-close"}}})
        emit({"method": "turn/completed", "params": {"threadId": "thread-close", "turn": {"id": "turn-close"}}})
        time.sleep(0.03)
        sys.exit(1)
`
	scriptPath := writePythonAppServerScript(t, script)

	res, err := Run(context.Background(), ClientConfig{
		Executable:    "/usr/bin/python3",
		Args:          []string{"-u", scriptPath},
		Workspace:     workspace,
		WorkspaceRoot: workspaceRoot,
		Prompt:        "prompt",
		Title:         "close failure",
		ReadTimeout:   1 * time.Second,
		TurnTimeout:   2 * time.Second,
	})
	if err == nil {
		t.Fatal("expected run to report late app-server exit")
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("expected close error from late exit, got %v", err)
	}
	if res == nil || res.Session == nil {
		t.Fatalf("expected run result to include session, got %#v", res)
	}
	if res.Session.SessionID != "thread-close-turn-close" || !res.Session.Terminal || res.Session.TurnsStarted != 1 {
		t.Fatalf("expected completed turn before close error, got %+v", res.Session)
	}
}
