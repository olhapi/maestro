package claude

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/agentruntime"
)

func TestStartRejectsUnsupportedConfiguration(t *testing.T) {
	tests := []struct {
		name string
		spec agentruntime.RuntimeSpec
	}{
		{
			name: "unsupported provider",
			spec: agentruntime.RuntimeSpec{
				Provider:  agentruntime.Provider("mistral"),
				Transport: agentruntime.TransportStdio,
				Command:   "cat",
			},
		},
		{
			name: "unsupported transport",
			spec: agentruntime.RuntimeSpec{
				Provider:  agentruntime.ProviderClaude,
				Transport: agentruntime.TransportAppServer,
				Command:   "cat",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec
			spec.Workspace = t.TempDir()

			_, err := Start(context.Background(), spec, agentruntime.Observers{})
			if err == nil {
				t.Fatal("expected unsupported capability error")
			}
			if !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
				t.Fatalf("expected unsupported capability error, got %v", err)
			}
		})
	}
}

func TestStdioRuntimeRejectsUnsupportedInputKinds(t *testing.T) {
	tests := []struct {
		name         string
		input        []agentruntime.InputItem
		wantFragment string
	}{
		{
			name: "local image",
			input: []agentruntime.InputItem{{
				Kind: agentruntime.InputItemLocalImage,
				Path: "image.png",
			}},
			wantFragment: "local_image",
		},
		{
			name: "unknown kind",
			input: []agentruntime.InputItem{{
				Kind: agentruntime.InputItemKind("mystery"),
				Text: "ignored",
			}},
			wantFragment: `input kind "mystery"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := mustStartStdioRuntime(t, agentruntime.Observers{})
			t.Cleanup(func() {
				_ = client.Close()
			})

			err := client.RunTurn(context.Background(), agentruntime.TurnRequest{
				Input: tc.input,
			}, nil)
			if err == nil {
				t.Fatal("expected unsupported capability error")
			}
			if !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
				t.Fatalf("expected unsupported capability error, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantFragment) {
				t.Fatalf("expected error to mention %q, got %v", tc.wantFragment, err)
			}
		})
	}
}

func TestStdioRuntimeCombinesOutputBranches(t *testing.T) {
	tests := []struct {
		name          string
		command       string
		wantOutput    string
		wantTurnEvent string
		wantErr       bool
	}{
		{
			name:          "stdout and stderr",
			command:       `printf 'stdout'; printf 'stderr' >&2`,
			wantOutput:    "stdout\nstderr",
			wantTurnEvent: "turn.completed",
		},
		{
			name:          "stderr failure",
			command:       `printf 'stderr' >&2; exit 7`,
			wantOutput:    "stderr",
			wantTurnEvent: "turn.failed",
			wantErr:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := mustStartStdioRuntimeWithCommand(t, tc.command, agentruntime.Observers{})
			t.Cleanup(func() {
				_ = client.Close()
			})

			err := client.RunTurn(context.Background(), agentruntime.TurnRequest{}, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected RunTurn to fail")
				}
			} else if err != nil {
				t.Fatalf("RunTurn: %v", err)
			}

			if got := client.Output(); got != tc.wantOutput {
				t.Fatalf("unexpected output: got %q want %q", got, tc.wantOutput)
			}

			session := client.Session()
			if session == nil {
				t.Fatal("expected session snapshot")
			}
			if session.LastEvent != tc.wantTurnEvent {
				t.Fatalf("unexpected final event: got %q want %q", session.LastEvent, tc.wantTurnEvent)
			}
		})
	}
}

func TestStdioRuntimeUpdatesPermissionsAndNilReceivers(t *testing.T) {
	client := mustStartStdioRuntime(t, agentruntime.Observers{})
	t.Cleanup(func() {
		_ = client.Close()
	})

	stdio := client.(*stdioClient)
	permissions := agentruntime.PermissionConfig{
		ApprovalPolicy: map[string]interface{}{"mode": "never"},
		ThreadSandbox:  "workspace-write",
		Metadata: map[string]interface{}{
			"source": "test",
		},
	}
	stdio.UpdatePermissions(permissions)

	if stdio.spec.Permissions.ThreadSandbox != permissions.ThreadSandbox {
		t.Fatalf("expected permissions to update, got %+v", stdio.spec.Permissions)
	}
	if stdio.spec.Permissions.Metadata["source"] != "test" {
		t.Fatalf("expected permission metadata to update, got %+v", stdio.spec.Permissions)
	}

	if err := client.RespondToInteraction(context.Background(), "interaction-1", agentruntime.PendingInteractionResponse{}); !errors.Is(err, agentruntime.ErrUnsupportedCapability) {
		t.Fatalf("expected unsupported capability error, got %v", err)
	}

	var nilClient *stdioClient
	if nilClient.Session() != nil {
		t.Fatal("expected nil session from nil receiver")
	}
	if nilClient.Output() != "" {
		t.Fatal("expected empty output from nil receiver")
	}
}

func mustStartStdioRuntimeWithCommand(t *testing.T, command string, observers agentruntime.Observers) agentruntime.Client {
	t.Helper()
	client, err := Start(context.Background(), agentruntime.RuntimeSpec{
		Provider:        agentruntime.ProviderClaude,
		Transport:       agentruntime.TransportStdio,
		Command:         command,
		Workspace:       t.TempDir(),
		IssueID:         "iss-claude",
		IssueIdentifier: "ISS-CLAUDE",
	}, observers)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return client
}
