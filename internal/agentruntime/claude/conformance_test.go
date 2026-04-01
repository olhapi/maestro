package claude

import (
	"context"
	"testing"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/agentruntime/contracttest"
)

func TestStdioRuntimeContract(t *testing.T) {
	contracttest.RunSharedRuntimeContractTests(t, contracttest.Contract{
		Capabilities:      stdioCapabilities,
		Provider:          string(agentruntime.ProviderClaude),
		Transport:         string(agentruntime.TransportStdio),
		MinActivityEvents: 4,
		Start: func(t *testing.T, observers agentruntime.Observers) contracttest.StartResult {
			return contracttest.StartResult{Client: mustStartStdioRuntime(t, observers)}
		},
	})
}

func mustStartStdioRuntime(t *testing.T, observers agentruntime.Observers) agentruntime.Client {
	t.Helper()
	client, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderClaude,
		Transport:       agentruntime.TransportStdio,
		Command:         "cat",
		Workspace:       t.TempDir(),
		IssueID:         "iss-claude",
		IssueIdentifier: "ISS-CLAUDE",
	}, observers)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return client
}
