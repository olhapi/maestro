package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/kanban"
)

func TestValidatePermissionFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    options
		wantErr string
	}{
		{
			name: "full access allowed tools",
			opts: options{
				permissionMode:  "default",
				allowedTools:    "Bash,Edit,Write,MultiEdit",
				strictMCPConfig: "true",
			},
		},
		{
			name: "maestro approval prompt",
			opts: options{
				permissionMode:       "default",
				permissionPromptTool: "mcp__maestro__approval_prompt",
				strictMCPConfig:      "true",
			},
		},
		{
			name: "plan mode still uses maestro approval prompt",
			opts: options{
				permissionMode:       "plan",
				permissionPromptTool: "mcp__maestro__approval_prompt",
				strictMCPConfig:      "true",
			},
		},
		{
			name: "approval prompt forbids allowed tools",
			opts: options{
				permissionMode:       "default",
				permissionPromptTool: "mcp__maestro__approval_prompt",
				allowedTools:         "Bash",
				strictMCPConfig:      "true",
			},
			wantErr: "expected no allowed-tools",
		},
		{
			name: "unsupported permission prompt tool",
			opts: options{
				permissionMode:       "default",
				permissionPromptTool: "custom_prompt",
				strictMCPConfig:      "true",
			},
			wantErr: "expected supported permission prompt tool",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validatePermissionFlags(tc.opts)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validatePermissionFlags() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validatePermissionFlags() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestWantsInterruptObservation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts options
		want bool
	}{
		{name: "no interrupt fields", opts: options{}, want: false},
		{name: "classification only", opts: options{interruptClass: "command"}, want: true},
		{name: "tool name only", opts: options{interruptToolName: "Bash"}, want: true},
		{name: "decision only", opts: options{interruptDecision: "allow"}, want: true},
		{name: "note only", opts: options{interruptNote: "operator approved"}, want: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := wantsInterruptObservation(tc.opts); got != tc.want {
				t.Fatalf("wantsInterruptObservation() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestValidatePendingInterrupt(t *testing.T) {
	t.Parallel()

	t.Run("accepts maestro managed approval payload", func(t *testing.T) {
		t.Parallel()

		if err := validatePendingInterrupt(validPendingInterrupt(), "CL-1", "", "command", "Bash", "", 0); err != nil {
			t.Fatalf("validatePendingInterrupt() error = %v", err)
		}
	})

	t.Run("rejects missing request meta correlation", func(t *testing.T) {
		t.Parallel()

		interaction := validPendingInterrupt()
		interaction.Metadata["request_meta"] = map[string]interface{}{}

		err := validatePendingInterrupt(interaction, "CL-1", "", "command", "Bash", "", 0)
		if err == nil || !strings.Contains(err.Error(), "toolUseId correlation") {
			t.Fatalf("validatePendingInterrupt() error = %v, want missing toolUseId correlation", err)
		}
	})

	t.Run("rejects classification mismatch", func(t *testing.T) {
		t.Parallel()

		err := validatePendingInterrupt(validPendingInterrupt(), "CL-1", "", "file_write", "Bash", "", 0)
		if err == nil || !strings.Contains(err.Error(), "expected interrupt classification") {
			t.Fatalf("validatePendingInterrupt() error = %v, want classification mismatch", err)
		}
	})

	t.Run("accepts plan approval payload", func(t *testing.T) {
		t.Parallel()

		if err := validatePendingInterrupt(validPlanApprovalInterrupt(), "CL-3", "plan_approval", "", "", "awaiting_approval", 2); err != nil {
			t.Fatalf("validatePendingInterrupt() error = %v", err)
		}
	})

	t.Run("rejects plan approval version mismatch", func(t *testing.T) {
		t.Parallel()

		err := validatePendingInterrupt(validPlanApprovalInterrupt(), "CL-3", "plan_approval", "", "", "awaiting_approval", 3)
		if err == nil || !strings.Contains(err.Error(), "expected plan version") {
			t.Fatalf("validatePendingInterrupt() error = %v, want plan version mismatch", err)
		}
	})
}

func TestFilterRuntimeEventsByIssue(t *testing.T) {
	t.Parallel()

	events := []kanban.RuntimeEvent{
		{Identifier: "CL-1", Kind: "run_completed"},
		{Identifier: "CL-2", Kind: "run_failed"},
		{Identifier: "CL-1", Kind: "retry_paused"},
	}

	filtered := filterRuntimeEventsByIssue(events, "CL-1")
	if len(filtered) != 2 {
		t.Fatalf("filterRuntimeEventsByIssue() len = %d, want 2", len(filtered))
	}
	if filtered[0].Kind != "run_completed" || filtered[1].Kind != "retry_paused" {
		t.Fatalf("unexpected filtered events: %+v", filtered)
	}

	all := filterRuntimeEventsByIssue(events, "")
	if !reflect.DeepEqual(all, events) {
		t.Fatalf("filterRuntimeEventsByIssue() with blank issue = %+v, want %+v", all, events)
	}
}

func TestRuntimeEventKinds(t *testing.T) {
	t.Parallel()

	events := []kanban.RuntimeEvent{
		{Kind: " run_completed "},
		{Kind: "retry_paused"},
	}

	got := runtimeEventKinds(events)
	want := []string{"run_completed", "retry_paused"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtimeEventKinds() = %#v, want %#v", got, want)
	}
}

func validPendingInterrupt() agentruntime.PendingInteraction {
	return agentruntime.PendingInteraction{
		ID:              "interrupt-1",
		RequestID:       "toolu_123",
		Kind:            agentruntime.PendingInteractionKindApproval,
		Method:          "approval_prompt",
		IssueIdentifier: "CL-1",
		ItemID:          "toolu_123",
		Approval: &agentruntime.PendingApproval{
			Command:  "pwd",
			CWD:      "/tmp/workspace",
			Reason:   "Claude requested command approval: pwd",
			Markdown: "Approve the command request.",
			Decisions: []agentruntime.PendingApprovalDecision{
				{Value: "allow", Label: "Allow once"},
			},
		},
		Metadata: map[string]interface{}{
			"source":         "claude_permission_prompt",
			"classification": "command",
			"tool_name":      "Bash",
			"workspace_path": "/tmp/workspace",
			"input": map[string]interface{}{
				"command": "pwd",
			},
			"request_meta": map[string]interface{}{
				"claudecode/toolUseId": "toolu_123",
			},
		},
	}
}

func validPlanApprovalInterrupt() agentruntime.PendingInteraction {
	return agentruntime.PendingInteraction{
		ID:                "plan-approval-1",
		Kind:              agentruntime.PendingInteractionKindApproval,
		IssueIdentifier:   "CL-3",
		CollaborationMode: "plan",
		Approval: &agentruntime.PendingApproval{
			Reason:            "Review the proposed plan before execution.",
			Markdown:          "Ship the guarded rollout.",
			PlanStatus:        "awaiting_approval",
			PlanVersionNumber: 2,
			Decisions: []agentruntime.PendingApprovalDecision{
				{Value: "approved", Label: "Approve plan"},
			},
		},
	}
}
