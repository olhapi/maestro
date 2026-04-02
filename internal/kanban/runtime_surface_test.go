package kanban

import (
	"testing"

	"github.com/olhapi/maestro/internal/agentruntime"
)

func TestResolveRuntimeSurface(t *testing.T) {
	t.Run("snapshot values win over issue project and session metadata", func(t *testing.T) {
		surface := ResolveRuntimeSurface(
			nil,
			&Issue{
				ProjectID:   "proj-1",
				RuntimeName: "issue-runtime",
			},
			&ExecutionSessionSnapshot{
				RuntimeName:       "snapshot-runtime",
				RuntimeProvider:   "snapshot-provider",
				RuntimeTransport:  "snapshot-transport",
				RuntimeAuthSource: "snapshot-auth",
				StopReason:        "approval_required",
			},
			&agentruntime.Session{
				Metadata: map[string]interface{}{
					"runtime_name": "session-runtime",
					"provider":     "session-provider",
					"transport":    "session-transport",
					"auth_source":  "session-auth",
					"stop_reason":  "session-stop",
				},
				TerminalReason: "session-terminal",
			},
			nil,
			nil,
		)

		if surface.RuntimeName != "snapshot-runtime" {
			t.Fatalf("expected snapshot runtime name, got %#v", surface.RuntimeName)
		}
		if surface.RuntimeProvider != "snapshot-provider" {
			t.Fatalf("expected snapshot runtime provider, got %#v", surface.RuntimeProvider)
		}
		if surface.RuntimeTransport != "snapshot-transport" {
			t.Fatalf("expected snapshot runtime transport, got %#v", surface.RuntimeTransport)
		}
		if surface.RuntimeAuthSource != "snapshot-auth" {
			t.Fatalf("expected snapshot runtime auth source, got %#v", surface.RuntimeAuthSource)
		}
		if surface.StopReason != "approval_required" {
			t.Fatalf("expected snapshot stop reason, got %#v", surface.StopReason)
		}
		if surface.PendingInteractionState != "approval" {
			t.Fatalf("expected approval pending state, got %#v", surface.PendingInteractionState)
		}
	})

	t.Run("issue runtime name wins over project and session metadata", func(t *testing.T) {
		surface := ResolveRuntimeSurface(
			nil,
			&Issue{
				ProjectID:   "proj-1",
				RuntimeName: "issue-runtime",
			},
			nil,
			&agentruntime.Session{
				Metadata: map[string]interface{}{
					"runtime_name": "session-runtime",
					"provider":     "session-provider",
					"transport":    "session-transport",
					"auth_source":  "session-auth",
				},
				TerminalReason: "finished",
			},
			nil,
			nil,
		)

		if surface.RuntimeName != "issue-runtime" {
			t.Fatalf("expected issue runtime name, got %#v", surface.RuntimeName)
		}
		if surface.RuntimeProvider != "session-provider" {
			t.Fatalf("expected session runtime provider, got %#v", surface.RuntimeProvider)
		}
		if surface.RuntimeTransport != "session-transport" {
			t.Fatalf("expected session runtime transport, got %#v", surface.RuntimeTransport)
		}
		if surface.RuntimeAuthSource != "session-auth" {
			t.Fatalf("expected session runtime auth source, got %#v", surface.RuntimeAuthSource)
		}
		if surface.StopReason != "finished" {
			t.Fatalf("expected terminal reason fallback, got %#v", surface.StopReason)
		}
		if surface.PendingInteractionState != "" {
			t.Fatalf("expected no pending state, got %#v", surface.PendingInteractionState)
		}
	})

	t.Run("project runtime fills a blank issue runtime name", func(t *testing.T) {
		store := setupTestStore(t)

		project, err := store.CreateProject("Runtime surface project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if err := store.UpdateProject(project.ID, "Runtime surface project", "", "", "", "project-runtime"); err != nil {
			t.Fatalf("UpdateProject: %v", err)
		}
		issue, err := store.CreateIssueWithOptions(project.ID, "", "Runtime surface issue", "", 0, nil, IssueCreateOptions{})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions: %v", err)
		}

		surface := ResolveRuntimeSurface(
			store,
			issue,
			nil,
			&agentruntime.Session{
				Metadata: map[string]interface{}{
					"runtime_name": "session-runtime",
					"provider":     "session-provider",
					"transport":    "app_server",
					"auth_source":  "cli",
					"stop_reason":  "session-stop",
				},
			},
			nil,
			nil,
		)

		if surface.RuntimeName != "project-runtime" {
			t.Fatalf("expected project runtime name, got %#v", surface.RuntimeName)
		}
		if surface.RuntimeProvider != "session-provider" {
			t.Fatalf("expected session runtime provider, got %#v", surface.RuntimeProvider)
		}
		if surface.RuntimeTransport != "app_server" {
			t.Fatalf("expected session runtime transport, got %#v", surface.RuntimeTransport)
		}
		if surface.RuntimeAuthSource != "cli" {
			t.Fatalf("expected session runtime auth source, got %#v", surface.RuntimeAuthSource)
		}
		if surface.StopReason != "session-stop" {
			t.Fatalf("expected session stop reason, got %#v", surface.StopReason)
		}
		if surface.PendingInteractionState != "" {
			t.Fatalf("expected no pending state, got %#v", surface.PendingInteractionState)
		}
	})

	t.Run("session metadata fills all runtime fields when the issue has no runtime", func(t *testing.T) {
		surface := ResolveRuntimeSurface(
			nil,
			&Issue{},
			nil,
			&agentruntime.Session{
				Metadata: map[string]interface{}{
					"runtime_name":       7,
					"provider":           "session-provider",
					"transport":          "stdio",
					"auth_source":        "OAuth",
					"claude_stop_reason": "end_turn",
				},
			},
			nil,
			nil,
		)

		if surface.RuntimeName != "7" {
			t.Fatalf("expected numeric runtime name to be stringified, got %#v", surface.RuntimeName)
		}
		if surface.RuntimeProvider != "session-provider" {
			t.Fatalf("expected session runtime provider, got %#v", surface.RuntimeProvider)
		}
		if surface.RuntimeTransport != "stdio" {
			t.Fatalf("expected session runtime transport, got %#v", surface.RuntimeTransport)
		}
		if surface.RuntimeAuthSource != "OAuth" {
			t.Fatalf("expected session runtime auth source, got %#v", surface.RuntimeAuthSource)
		}
		if surface.StopReason != "end_turn" {
			t.Fatalf("expected claude stop reason fallback, got %#v", surface.StopReason)
		}
		if surface.PendingInteractionState != "" {
			t.Fatalf("expected no pending state, got %#v", surface.PendingInteractionState)
		}
	})
}

func TestMetadataString(t *testing.T) {
	t.Run("empty map", func(t *testing.T) {
		if got := metadataString(nil, "runtime_name"); got != "" {
			t.Fatalf("expected empty string for nil metadata, got %#v", got)
		}
		if got := metadataString(map[string]interface{}{}, "runtime_name"); got != "" {
			t.Fatalf("expected empty string for empty metadata, got %#v", got)
		}
	})

	t.Run("missing and nil values", func(t *testing.T) {
		metadata := map[string]interface{}{
			"runtime_name": nil,
		}
		if got := metadataString(metadata, "runtime_name"); got != "" {
			t.Fatalf("expected empty string for nil metadata value, got %#v", got)
		}
		if got := metadataString(metadata, "provider"); got != "" {
			t.Fatalf("expected empty string for missing metadata key, got %#v", got)
		}
	})

	t.Run("trims and stringifies values", func(t *testing.T) {
		metadata := map[string]interface{}{
			"runtime_name": " runtime-alpha ",
			"attempt":      3,
		}
		if got := metadataString(metadata, "runtime_name"); got != "runtime-alpha" {
			t.Fatalf("expected trimmed string metadata, got %#v", got)
		}
		if got := metadataString(metadata, "attempt"); got != "3" {
			t.Fatalf("expected numeric metadata to stringify, got %#v", got)
		}
	})
}

func TestPendingInteractionState(t *testing.T) {
	t.Run("pending interaction kinds take priority", func(t *testing.T) {
		cases := []struct {
			name       string
			pending    *agentruntime.PendingInteraction
			planning   *IssuePlanning
			stopReason string
			want       string
		}{
			{
				name:    "approval",
				pending: &agentruntime.PendingInteraction{Kind: agentruntime.PendingInteractionKindApproval},
				want:    "approval",
			},
			{
				name:    "user input",
				pending: &agentruntime.PendingInteraction{Kind: agentruntime.PendingInteractionKindUserInput},
				want:    "user_input",
			},
			{
				name:    "elicitation",
				pending: &agentruntime.PendingInteraction{Kind: agentruntime.PendingInteractionKindElicitation},
				want:    "elicitation",
			},
			{
				name:    "alert",
				pending: &agentruntime.PendingInteraction{Kind: agentruntime.PendingInteractionKindAlert},
				want:    "alert",
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				if got := pendingInteractionState(tc.pending, tc.planning, tc.stopReason); got != tc.want {
					t.Fatalf("expected %q, got %#v", tc.want, got)
				}
			})
		}
	})

	t.Run("planning status maps to the operator facing state", func(t *testing.T) {
		cases := []struct {
			name     string
			planning *IssuePlanning
			want     string
		}{
			{name: "drafting", planning: &IssuePlanning{Status: IssuePlanningStatusDrafting}, want: "planning"},
			{name: "awaiting approval", planning: &IssuePlanning{Status: IssuePlanningStatusAwaitingApproval}, want: "approval"},
			{name: "revision requested", planning: &IssuePlanning{Status: IssuePlanningStatusRevisionRequested}, want: "revision_requested"},
			{name: "abandoned", planning: &IssuePlanning{Status: IssuePlanningStatusAbandoned}, want: "abandoned"},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				if got := pendingInteractionState(nil, tc.planning, ""); got != tc.want {
					t.Fatalf("expected %q, got %#v", tc.want, got)
				}
			})
		}
	})

	t.Run("stop reason fallback covers approval user input elicitation and alert", func(t *testing.T) {
		cases := []struct {
			name       string
			stopReason string
			want       string
		}{
			{name: "plan approval pending", stopReason: "plan_approval_pending", want: "approval"},
			{name: "approval pending", stopReason: "approval_pending", want: "approval"},
			{name: "approval required", stopReason: "approval_required", want: "approval"},
			{name: "turn input required", stopReason: "turn_input_required", want: "user_input"},
			{name: "user input required", stopReason: "user_input_required", want: "user_input"},
			{name: "elicitation required", stopReason: "elicitation_required", want: "elicitation"},
			{name: "alert", stopReason: "alert", want: "alert"},
			{name: "blank", stopReason: "", want: ""},
			{name: "unknown", stopReason: "something_else", want: ""},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				if got := pendingInteractionState(nil, nil, tc.stopReason); got != tc.want {
					t.Fatalf("expected %q, got %#v", tc.want, got)
				}
			})
		}
	})
}
