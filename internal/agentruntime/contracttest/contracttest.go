package contracttest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
)

type StartResult struct {
	Client agentruntime.Client
	State  any
}

type Contract struct {
	Capabilities           agentruntime.Capabilities
	Provider               string
	Transport              string
	MinActivityEvents      int
	Start                  func(t *testing.T, observers agentruntime.Observers) StartResult
	AssertPermissionUpdate func(t *testing.T, state any)
}

func RunSharedRuntimeContractTests(t *testing.T, contract Contract) {
	t.Helper()

	t.Run("capabilities", func(t *testing.T) {
		result := start(t, contract, agentruntime.Observers{})
		if got := result.Client.Capabilities(); got != contract.Capabilities {
			t.Fatalf("unexpected capabilities: %+v", got)
		}
		runTwoTurns(t, result.Client)
	})

	if !contract.Capabilities.SupportsLocalImageInput() {
		t.Run("rejects_unsupported_local_image", func(t *testing.T) {
			result := start(t, contract, agentruntime.Observers{})
			err := result.Client.RunTurn(context.Background(), agentruntime.TurnRequest{
				Input: []agentruntime.InputItem{{
					Kind: agentruntime.InputItemLocalImage,
					Path: "/tmp/example.png",
					Name: "example",
				}},
			}, nil)
			if !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
				t.Fatalf("expected unsupported capability error, got %v", err)
			}
		})
	}

	t.Run("metadata_and_output_accumulation", func(t *testing.T) {
		var (
			mu         sync.Mutex
			activities []agentruntime.ActivityEvent
		)
		result := start(t, contract, agentruntime.Observers{
			OnActivityEvent: func(event agentruntime.ActivityEvent) {
				mu.Lock()
				defer mu.Unlock()
				activities = append(activities, event.Clone())
			},
		})
		runTwoTurns(t, result.Client)

		if contract.MinActivityEvents > 0 {
			waitForCondition(t, 2*time.Second, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(activities) >= contract.MinActivityEvents
			})
		}

		output := result.Client.Output()
		for _, want := range []string{"first prompt", "second prompt"} {
			if !strings.Contains(output, want) {
				t.Fatalf("expected output to contain %q, got %q", want, output)
			}
		}

		session := result.Client.Session()
		if session == nil {
			t.Fatal("expected session snapshot")
		}
		assertRuntimeMetadata(t, session.Metadata, contract.Provider, contract.Transport)

		mu.Lock()
		defer mu.Unlock()
		for _, event := range activities {
			assertRuntimeMetadata(t, event.Metadata, contract.Provider, contract.Transport)
		}
	})

	if contract.Capabilities.SupportsRuntimePermissionUpdates() {
		t.Run("permission_updates", func(t *testing.T) {
			if contract.AssertPermissionUpdate == nil {
				t.Fatal("missing permission update assertion")
			}
			result := start(t, contract, agentruntime.Observers{})
			if err := result.Client.RunTurn(context.Background(), firstTurnRequest(), nil); err != nil {
				t.Fatalf("RunTurn first: %v", err)
			}
			agentruntime.ApplyPermissionConfig(result.Client, agentruntime.PermissionConfig{
				ThreadSandbox: "danger-full-access",
				TurnSandboxPolicy: map[string]interface{}{
					"type":          "dangerFullAccess",
					"networkAccess": true,
				},
				Metadata: map[string]interface{}{
					"source": "updated",
				},
			})
			if err := result.Client.RunTurn(context.Background(), secondTurnRequest(), nil); err != nil {
				t.Fatalf("RunTurn second: %v", err)
			}
			contract.AssertPermissionUpdate(t, result.State)
		})
	}
}

func start(t *testing.T, contract Contract, observers agentruntime.Observers) StartResult {
	t.Helper()
	if contract.Start == nil {
		t.Fatal("missing runtime contract start hook")
	}
	result := contract.Start(t, observers)
	if result.Client == nil {
		t.Fatal("expected runtime client")
	}
	t.Cleanup(func() { _ = result.Client.Close() })
	return result
}

func firstTurnRequest() agentruntime.TurnRequest {
	return agentruntime.TurnRequest{
		Title: "First",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "first prompt"}},
	}
}

func secondTurnRequest() agentruntime.TurnRequest {
	return agentruntime.TurnRequest{
		Title: "Second",
		Input: []agentruntime.InputItem{{Kind: agentruntime.InputItemText, Text: "second prompt"}},
	}
}

func runTwoTurns(t *testing.T, client agentruntime.Client) {
	t.Helper()
	if err := client.RunTurn(context.Background(), firstTurnRequest(), nil); err != nil {
		t.Fatalf("RunTurn first: %v", err)
	}
	if err := client.RunTurn(context.Background(), secondTurnRequest(), nil); err != nil {
		t.Fatalf("RunTurn second: %v", err)
	}
}

func assertRuntimeMetadata(t *testing.T, metadata map[string]interface{}, provider, transport string) {
	t.Helper()
	if metadata["provider"] != provider {
		t.Fatalf("expected provider metadata %q, got %#v", provider, metadata)
	}
	if metadata["transport"] != transport {
		t.Fatalf("expected transport metadata %q, got %#v", transport, metadata)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
