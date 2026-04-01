package claude

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
)

func TestStdioRuntimeEmitsLifecycleAndObservers(t *testing.T) {
	sessionCh := make(chan agentruntime.Session, 4)
	activityCh := make(chan agentruntime.ActivityEvent, 4)

	client := mustStartStdioRuntime(t, agentruntime.Observers{
		OnSessionUpdate: func(session *agentruntime.Session) {
			if session != nil {
				sessionCh <- session.Clone()
			}
		},
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			activityCh <- event.Clone()
		},
	})
	t.Cleanup(func() {
		_ = client.Close()
	})

	var started agentruntime.Session
	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "first turn",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "hello"}},
	}, func(session *agentruntime.Session) {
		if session != nil {
			started = session.Clone()
		}
	}); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	if started.LastEvent != "turn.started" || started.ThreadID == "" || started.TurnID == "" {
		t.Fatalf("expected started session to reflect turn start, got %+v", started)
	}
	if started.Metadata["provider"] != string(agentruntime.ProviderClaude) || started.Metadata["transport"] != string(agentruntime.TransportStdio) {
		t.Fatalf("expected started session metadata to identify claude stdio, got %+v", started.Metadata)
	}

	if out := client.Output(); strings.TrimSpace(out) != "hello" {
		t.Fatalf("unexpected output: %q", out)
	}

	session := client.Session()
	if session == nil {
		t.Fatal("expected session snapshot")
	}
	if session.ThreadID == "" || session.TurnID == "" || session.SessionID == "" {
		t.Fatalf("expected session identifiers to be populated, got %+v", session)
	}
	if session.TurnsStarted != 1 || session.TurnsCompleted != 1 {
		t.Fatalf("expected turn counters to update, got %+v", session)
	}

	updates := collectSessions(t, sessionCh, 2)
	if updates[0].LastEvent != "turn.started" || updates[1].LastEvent != "turn.completed" {
		t.Fatalf("unexpected session updates: %+v", updates)
	}

	events := collectActivityEvents(t, activityCh, 2)
	foundItemCompleted := false
	foundTurnCompleted := false
	for _, event := range events {
		if event.Type == "item.completed" {
			foundItemCompleted = true
		}
		if event.Type == "turn.completed" {
			foundTurnCompleted = true
		}
		if event.Metadata["provider"] != string(agentruntime.ProviderClaude) || event.Metadata["transport"] != string(agentruntime.TransportStdio) {
			t.Fatalf("expected activity metadata to identify claude stdio, got %+v", event.Metadata)
		}
	}
	if !foundItemCompleted || !foundTurnCompleted {
		t.Fatalf("unexpected activity events: %+v", events)
	}
}

func collectSessions(t *testing.T, ch <-chan agentruntime.Session, want int) []agentruntime.Session {
	t.Helper()
	out := make([]agentruntime.Session, 0, want)
	deadline := time.After(time.Second)
	for len(out) < want {
		select {
		case session := <-ch:
			out = append(out, session)
		case <-deadline:
			t.Fatalf("timed out waiting for %d session updates, got %d", want, len(out))
		}
	}
	return out
}

func collectActivityEvents(t *testing.T, ch <-chan agentruntime.ActivityEvent, want int) []agentruntime.ActivityEvent {
	t.Helper()
	out := make([]agentruntime.ActivityEvent, 0, want)
	deadline := time.After(time.Second)
	for len(out) < want {
		select {
		case event := <-ch:
			out = append(out, event)
		case <-deadline:
			t.Fatalf("timed out waiting for %d activity events, got %d", want, len(out))
		}
	}
	return out
}
