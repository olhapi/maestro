package testadapter

import (
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
		},
	})
}
