package fake

import (
	"context"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	runtimefactory "github.com/olhapi/maestro/internal/agentruntime/factory"
	"github.com/olhapi/maestro/pkg/config"
)

func TestStarterClonesRequestsAndClientCapturesInteractions(t *testing.T) {
	workflow := &config.Workflow{Config: config.DefaultConfig()}
	request := runtimefactory.WorkflowStartRequest{
		Workflow:        workflow,
		RuntimeName:     "codex-stdio",
		RuntimeConfig:   workflow.Config.Runtime.Entries["codex-stdio"],
		WorkspacePath:   "/tmp/workspaces/MAES-321",
		IssueID:         "iss_321",
		IssueIdentifier: "MAES-321",
		DBPath:          "/tmp/maestro.db",
		Env:             []string{"FOO=bar"},
		Permissions: agentruntime.PermissionConfig{
			ThreadSandbox: "workspace-write",
			Metadata: map[string]interface{}{
				"source": "test",
			},
		},
		DynamicTools: []map[string]interface{}{{
			"name": "tool-1",
		}},
		Metadata: map[string]interface{}{
			"request": "meta",
		},
	}

	var seenActivities []agentruntime.ActivityEvent
	var seenSessions []agentruntime.Session
	var doneIDs []string
	observer := agentruntime.Observers{
		OnSessionUpdate: func(session *agentruntime.Session) {
			if session != nil {
				seenSessions = append(seenSessions, session.Clone())
			}
		},
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			seenActivities = append(seenActivities, event.Clone())
		},
		OnPendingInteractionDone: func(interactionID string) {
			doneIDs = append(doneIDs, interactionID)
		},
	}

	starter := NewStarter(Scenario{
		Capabilities: agentruntime.Capabilities{
			QueuedInteractions:       true,
			RuntimePermissionUpdates: true,
		},
		InitialSession: agentruntime.Session{
			IssueID:         "iss_321",
			IssueIdentifier: "MAES-321",
			ThreadID:        "thread-initial",
			Metadata: map[string]interface{}{
				"provider": "fake",
			},
		},
		Turns: []Turn{{
			Activities: []agentruntime.ActivityEvent{{
				Type:   "item.started",
				TurnID: "turn-1",
				Metadata: map[string]interface{}{
					"phase": "commentary",
				},
			}},
			PendingInteractions: []agentruntime.PendingInteraction{{
				ID:          "interaction-1",
				Kind:        agentruntime.PendingInteractionKindApproval,
				RequestedAt: time.Now().UTC(),
				Approval: &agentruntime.PendingApproval{
					Command: "git status",
					Decisions: []agentruntime.PendingApprovalDecision{{
						Value: "approve",
						Label: "Approve",
					}},
				},
				Metadata: map[string]interface{}{
					"source": "scenario",
				},
			}},
			ClearedInteractions: []string{"interaction-1"},
			Output:              "final answer",
		}},
	})

	clientIface, err := starter.Start(context.Background(), request, agentruntime.Observers{
		OnSessionUpdate:          observer.OnSessionUpdate,
		OnActivityEvent:          observer.OnActivityEvent,
		OnPendingInteractionDone: observer.OnPendingInteractionDone,
		OnPendingInteraction: func(interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder) {
			if interaction == nil {
				return
			}
			if err := responder(context.Background(), interaction.ID, agentruntime.PendingInteractionResponse{
				Decision: "approve",
				DecisionPayload: map[string]interface{}{
					"scope": "once",
				},
			}); err != nil {
				t.Fatalf("respond to fake interaction: %v", err)
			}
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	agentruntime.ApplyPermissionConfig(clientIface, agentruntime.PermissionConfig{
		ThreadSandbox: "danger-full-access",
		Metadata: map[string]interface{}{
			"source": "updated",
		},
	})
	if err := clientIface.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "Fake turn",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "prompt"}},
		Metadata: map[string]interface{}{
			"request": "turn",
		},
	}, nil); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	request.Env[0] = "FOO=changed"
	request.RuntimeName = "mutated"
	request.RuntimeConfig.Command = "mutated"
	request.RuntimeConfig.ApprovalPolicy = map[string]interface{}{"mode": "mutated"}
	request.Permissions.Metadata["source"] = "mutated"
	request.DynamicTools[0]["name"] = "tool-mutated"
	request.Metadata["request"] = "mutated"

	capturedRequests := starter.Requests()
	if len(capturedRequests) != 1 {
		t.Fatalf("expected one captured request, got %d", len(capturedRequests))
	}
	captured := capturedRequests[0]
	if captured.Env[0] != "FOO=bar" {
		t.Fatalf("expected starter to clone env, got %#v", captured.Env)
	}
	if captured.RuntimeName != "codex-stdio" {
		t.Fatalf("expected starter to clone runtime name, got %#v", captured.RuntimeName)
	}
	if captured.RuntimeConfig.Command != workflow.Config.Runtime.Entries["codex-stdio"].Command {
		t.Fatalf("expected starter to clone runtime config, got %#v", captured.RuntimeConfig)
	}
	if captured.RuntimeConfig.ApprovalPolicy != "never" {
		t.Fatalf("expected starter to preserve runtime approval policy, got %#v", captured.RuntimeConfig)
	}
	if captured.DBPath != "/tmp/maestro.db" {
		t.Fatalf("expected starter to clone db path, got %#v", captured.DBPath)
	}
	if captured.Permissions.Metadata["source"] != "test" {
		t.Fatalf("expected starter to clone permissions, got %#v", captured.Permissions.Metadata)
	}
	if captured.DynamicTools[0]["name"] != "tool-1" {
		t.Fatalf("expected starter to clone dynamic tools, got %#v", captured.DynamicTools)
	}
	if captured.Metadata["request"] != "meta" {
		t.Fatalf("expected starter to clone request metadata, got %#v", captured.Metadata)
	}

	clients := starter.Clients()
	if len(clients) != 1 {
		t.Fatalf("expected one fake client, got %d", len(clients))
	}
	client := clients[0]
	if len(client.PermissionUpdates()) != 1 || client.PermissionUpdates()[0].ThreadSandbox != "danger-full-access" {
		t.Fatalf("expected permission updates to be recorded, got %+v", client.PermissionUpdates())
	}
	if len(client.Responses()) != 1 || client.Responses()[0].InteractionID != "interaction-1" {
		t.Fatalf("expected one captured response call, got %+v", client.Responses())
	}
	if client.Responses()[0].Response.Decision != "approve" {
		t.Fatalf("expected approval response to be recorded, got %+v", client.Responses())
	}
	if got := client.Output(); got != "final answer" {
		t.Fatalf("expected fake client output to accumulate final answer, got %q", got)
	}

	if len(seenActivities) != 1 || seenActivities[0].Metadata["phase"] != "commentary" {
		t.Fatalf("expected activity observer to receive cloned activity metadata, got %+v", seenActivities)
	}
	if len(seenSessions) == 0 {
		t.Fatal("expected session observer updates")
	}
	if len(doneIDs) != 2 || doneIDs[0] != "interaction-1" || doneIDs[1] != "interaction-1" {
		t.Fatalf("expected fake runtime to emit interaction completion notifications, got %#v", doneIDs)
	}
}

func TestStarterClonesNestedScenarioData(t *testing.T) {
	scenario := Scenario{
		Capabilities: agentruntime.Capabilities{
			QueuedInteractions:       true,
			RuntimePermissionUpdates: true,
		},
		InitialSession: agentruntime.Session{
			IssueID:         "iss_321",
			IssueIdentifier: "MAES-321",
			ThreadID:        "thread-initial",
			Metadata: map[string]interface{}{
				"provider": "fake",
				"nested": map[string]interface{}{
					"source": "original",
				},
			},
		},
		Turns: []Turn{{
			StartedSession: &agentruntime.Session{
				ThreadID: "thread-started",
				TurnID:   "turn-started",
				Metadata: map[string]interface{}{
					"stage": "started",
					"nested": map[string]interface{}{
						"state": "original",
					},
				},
			},
			SessionUpdates: []agentruntime.Session{{
				ThreadID: "thread-update",
				TurnID:   "turn-update",
				Metadata: map[string]interface{}{
					"stage": "update",
					"nested": map[string]interface{}{
						"state": "original",
					},
				},
			}},
			Activities: []agentruntime.ActivityEvent{{
				Type:     "item.started",
				ThreadID: "thread-activity",
				TurnID:   "turn-activity",
				Metadata: map[string]interface{}{
					"phase": "commentary",
					"nested": map[string]interface{}{
						"source": "original",
					},
				},
			}},
			PendingInteractions: []agentruntime.PendingInteraction{{
				ID:          "interaction-1",
				Kind:        agentruntime.PendingInteractionKindApproval,
				RequestedAt: time.Now().UTC(),
				Approval: &agentruntime.PendingApproval{
					Command: "git status",
					Decisions: []agentruntime.PendingApprovalDecision{{
						Value: "approve",
						Label: "Approve",
					}},
				},
				Metadata: map[string]interface{}{
					"source": "scenario",
					"nested": map[string]interface{}{
						"kind": "original",
					},
				},
			}},
			ClearedInteractions: []string{"interaction-1"},
			FinalSession: &agentruntime.Session{
				ThreadID: "thread-final",
				TurnID:   "turn-final",
				Metadata: map[string]interface{}{
					"stage": "final",
					"nested": map[string]interface{}{
						"state": "original",
					},
				},
			},
			Output: "final answer",
		}},
	}

	starter := NewStarter(scenario)
	scenario.InitialSession.Metadata["nested"].(map[string]interface{})["source"] = "mutated"
	scenario.Turns[0].StartedSession.Metadata["nested"].(map[string]interface{})["state"] = "mutated"
	scenario.Turns[0].SessionUpdates[0].Metadata["nested"].(map[string]interface{})["state"] = "mutated"
	scenario.Turns[0].Activities[0].Metadata["nested"].(map[string]interface{})["source"] = "mutated"
	scenario.Turns[0].PendingInteractions[0].Metadata["nested"].(map[string]interface{})["kind"] = "mutated"
	scenario.Turns[0].FinalSession.Metadata["nested"].(map[string]interface{})["state"] = "mutated"

	var (
		seenSessions     []agentruntime.Session
		seenActivities   []agentruntime.ActivityEvent
		seenInteractions []agentruntime.PendingInteraction
	)
	clientIface, err := starter.Start(context.Background(), runtimefactory.WorkflowStartRequest{}, agentruntime.Observers{
		OnSessionUpdate: func(session *agentruntime.Session) {
			if session != nil {
				seenSessions = append(seenSessions, session.Clone())
			}
		},
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			seenActivities = append(seenActivities, event.Clone())
		},
		OnPendingInteraction: func(interaction *agentruntime.PendingInteraction, responder agentruntime.InteractionResponder) {
			if interaction != nil {
				seenInteractions = append(seenInteractions, interaction.Clone())
			}
			if interaction == nil {
				return
			}
			if err := responder(context.Background(), interaction.ID, agentruntime.PendingInteractionResponse{
				Decision: "approve",
			}); err != nil {
				t.Fatalf("respond to fake interaction: %v", err)
			}
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	initialSession := clientIface.Session()
	if initialSession.Metadata["nested"].(map[string]interface{})["source"] != "original" {
		t.Fatalf("expected initial session metadata to remain cloned, got %+v", initialSession.Metadata)
	}

	if err := clientIface.RunTurn(context.Background(), agentruntime.TurnRequest{
		Title: "Deep clone",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "prompt"}},
	}, nil); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	finalSession := clientIface.Session()
	if finalSession.Metadata["nested"].(map[string]interface{})["state"] != "original" {
		t.Fatalf("expected final session metadata to remain cloned, got %+v", finalSession.Metadata)
	}
	if len(seenSessions) < 3 {
		t.Fatalf("expected session updates for start, update, and final snapshots, got %#v", seenSessions)
	}
	if seenSessions[0].Metadata["nested"].(map[string]interface{})["state"] != "original" {
		t.Fatalf("expected started session metadata to remain cloned, got %+v", seenSessions[0].Metadata)
	}
	if seenSessions[1].Metadata["nested"].(map[string]interface{})["state"] != "original" {
		t.Fatalf("expected update session metadata to remain cloned, got %+v", seenSessions[1].Metadata)
	}
	if seenSessions[len(seenSessions)-1].Metadata["nested"].(map[string]interface{})["state"] != "original" {
		t.Fatalf("expected final session metadata to remain cloned, got %+v", seenSessions[len(seenSessions)-1].Metadata)
	}
	if len(seenActivities) != 1 || seenActivities[0].Metadata["nested"].(map[string]interface{})["source"] != "original" {
		t.Fatalf("expected activity metadata to remain cloned, got %+v", seenActivities)
	}
	if len(seenInteractions) != 1 || seenInteractions[0].Metadata["nested"].(map[string]interface{})["kind"] != "original" {
		t.Fatalf("expected interaction metadata to remain cloned, got %+v", seenInteractions)
	}
	if got := clientIface.Output(); got != "final answer" {
		t.Fatalf("expected fake client output to remain intact, got %q", got)
	}
}
