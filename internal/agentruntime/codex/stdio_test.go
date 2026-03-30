package codex

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
)

func TestStdioClientRunTurnAccumulatesOutputAndSession(t *testing.T) {
	client, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderCodex,
		Transport:       agentruntime.TransportStdio,
		Command:         "cat",
		IssueID:         "iss_123",
		IssueIdentifier: "ISS-123",
	}, agentruntime.Observers{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	if got := client.Capabilities(); got != stdioCapabilities {
		t.Fatalf("unexpected stdio capabilities: %+v", got)
	}

	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "First turn",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "first prompt"}},
	}, nil); err != nil {
		t.Fatalf("RunTurn first: %v", err)
	}
	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "Second turn",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "second prompt"}},
	}, nil); err != nil {
		t.Fatalf("RunTurn second: %v", err)
	}

	output := client.Output()
	if !strings.Contains(output, "first prompt") || !strings.Contains(output, "second prompt") {
		t.Fatalf("expected aggregated output from both turns, got %q", output)
	}

	session := client.Session()
	if session == nil {
		t.Fatal("expected session snapshot")
	}
	if session.ProcessID != 0 {
		t.Fatalf("expected stdio session to avoid app-server pid tracking, got %d", session.ProcessID)
	}
	if session.IssueID != "iss_123" || session.IssueIdentifier != "ISS-123" {
		t.Fatalf("expected session to retain issue identity, got %+v", session)
	}
	if session.TurnsStarted != 2 || session.TurnsCompleted != 2 {
		t.Fatalf("expected turn counters to track both turns, got %+v", session)
	}
	if !session.Terminal || session.TerminalReason != "turn.completed" {
		t.Fatalf("expected completed session terminal state, got %+v", session)
	}
}

func TestStdioClientRejectsLocalImageInput(t *testing.T) {
	client, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:  agentruntime.ProviderCodex,
		Transport: agentruntime.TransportStdio,
		Command:   "cat",
	}, agentruntime.Observers{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	err = client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Input: []agentruntime.InputItem{{
			Kind: agentruntime.InputItemLocalImage,
			Path: "/tmp/example.png",
			Name: "example",
		}},
	}, nil)
	if !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
		t.Fatalf("expected unsupported capability error, got %v", err)
	}
}

func TestStdioClientRunTurnFailureNotifiesObservers(t *testing.T) {
	sessionCh := make(chan agentruntime.Session, 1)
	activityCh := make(chan agentruntime.ActivityEvent, 1)

	client, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderCodex,
		Transport:       agentruntime.TransportStdio,
		Command:         "printf stdout; printf stderr >&2; exit 1",
		IssueID:         "iss_456",
		IssueIdentifier: "ISS-456",
	}, agentruntime.Observers{
		OnSessionUpdate: func(session *agentruntime.Session) {
			if session != nil {
				sessionCh <- session.Clone()
			}
		},
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			activityCh <- event.Clone()
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	err = client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "failing turn",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "prompt"}},
	}, nil)
	if err == nil {
		t.Fatal("expected failing command to return an error")
	}

	deadline := time.After(2 * time.Second)
	var terminalSession agentruntime.Session
	for terminalSession.TerminalReason != "turn.failed" {
		select {
		case session := <-sessionCh:
			terminalSession = session
		case <-deadline:
			t.Fatal("timed out waiting for terminal session update")
		}
	}
	if terminalSession.TurnID == "" {
		t.Fatalf("unexpected session update: %+v", terminalSession)
	}

	deadline = time.After(2 * time.Second)
	for {
		select {
		case activity := <-activityCh:
			if activity.Type == "turn.failed" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for turn.failed activity update")
		}
	}
}
