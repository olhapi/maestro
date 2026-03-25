package appserver

import (
	"context"
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
		ReadTimeout:   200 * time.Millisecond,
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
