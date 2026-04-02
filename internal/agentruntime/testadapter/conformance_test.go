package testadapter

import (
	"context"
	"testing"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/agentruntime/contracttest"
)

func TestRuntimeContract(t *testing.T) {
	contracttest.RunSharedRuntimeContractTests(t, contracttest.Contract{
		Capabilities:      Capabilities,
		Provider:          ProviderName,
		Transport:         TransportName,
		MinActivityEvents: 6,
		Start: func(t *testing.T, observers agentruntime.Observers) contracttest.StartResult {
			client := Start(agentruntime.RuntimeSpec{
				IssueID:         "iss_test",
				IssueIdentifier: "ISS-TEST",
				Metadata: map[string]interface{}{
					"source": "contract",
					"nested": map[string]interface{}{
						"transport": "memory",
					},
				},
			}, observers)
			return contracttest.StartResult{
				Client: client,
				State:  client,
			}
		},
		AssertPermissionUpdate: func(t *testing.T, state any) {
			client, ok := state.(*Client)
			if !ok {
				t.Fatalf("expected test adapter client state, got %T", state)
			}
			updates := client.PermissionUpdates()
			if len(updates) != 1 {
				t.Fatalf("expected one permission update, got %+v", updates)
			}
			if updates[0].ThreadSandbox != "danger-full-access" {
				t.Fatalf("expected updated thread sandbox, got %+v", updates[0])
			}
			if updates[0].TurnSandboxPolicy["type"] != "dangerFullAccess" {
				t.Fatalf("expected updated turn sandbox policy, got %+v", updates[0].TurnSandboxPolicy)
			}
			session := client.Session()
			if session.Metadata["source"] != "contract" {
				t.Fatalf("expected runtime metadata to include neutral fields, got %+v", session.Metadata)
			}
			if session.Metadata["nested"].(map[string]interface{})["transport"] != "memory" {
				t.Fatalf("expected nested runtime metadata to survive, got %+v", session.Metadata)
			}
		},
	})
}

func TestStartClonesRuntimeSpecMetadata(t *testing.T) {
	spec := agentruntime.RuntimeSpec{
		IssueID:         "iss_test",
		IssueIdentifier: "ISS-TEST",
		Metadata: map[string]interface{}{
			"source": "original",
			"nested": map[string]interface{}{
				"transport": "memory",
			},
		},
	}

	var activities []agentruntime.ActivityEvent
	client := Start(spec, agentruntime.Observers{
		OnActivityEvent: func(event agentruntime.ActivityEvent) {
			activities = append(activities, event.Clone())
		},
	})

	spec.Metadata["source"] = "mutated"
	spec.Metadata["nested"].(map[string]interface{})["transport"] = "mutated"

	if err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "hello"}},
	}, nil); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	session := client.Session()
	if session.Metadata["source"] != "original" {
		t.Fatalf("expected runtime spec metadata to be cloned, got %+v", session.Metadata)
	}
	if session.Metadata["nested"].(map[string]interface{})["transport"] != "memory" {
		t.Fatalf("expected nested metadata to remain cloned, got %+v", session.Metadata)
	}
	if len(activities) < 2 {
		t.Fatalf("expected activity events to be emitted, got %#v", activities)
	}
	if activities[0].Metadata["source"] != "original" || activities[len(activities)-1].Metadata["source"] != "original" {
		t.Fatalf("expected activity metadata to remain cloned, got %#v", activities)
	}
}
