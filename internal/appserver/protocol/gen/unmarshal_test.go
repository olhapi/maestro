package gen

import (
	"encoding/json"
	"testing"
)

func TestTentacledSessionSourceUnmarshalJSON(t *testing.T) {
	t.Run("string enum", func(t *testing.T) {
		var got TentacledSessionSource
		if err := json.Unmarshal([]byte(`"appServer"`), &got); err != nil {
			t.Fatalf("unmarshal string session source: %v", err)
		}
		if got.Enum == nil || *got.Enum != AppServer {
			t.Fatalf("unexpected enum value: %+v", got.Enum)
		}
		if got.PurpleSessionSource != nil {
			t.Fatalf("expected object branch to be nil, got %+v", got.PurpleSessionSource)
		}
	})

	t.Run("object sub-agent thread spawn", func(t *testing.T) {
		var got TentacledSessionSource
		payload := []byte(`{"subAgent":{"thread_spawn":{"depth":2,"parent_thread_id":"thread-parent","agent_nickname":"helper","agent_role":"reviewer"}}}`)
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatalf("unmarshal object session source: %v", err)
		}
		if got.Enum != nil {
			t.Fatalf("expected enum branch to be nil, got %+v", got.Enum)
		}
		if got.PurpleSessionSource == nil || got.PurpleSessionSource.SubAgent == nil || got.PurpleSessionSource.SubAgent.PurpleSubAgentSource == nil || got.PurpleSessionSource.SubAgent.PurpleSubAgentSource.ThreadSpawn == nil {
			t.Fatalf("expected thread_spawn branch, got %+v", got.PurpleSessionSource)
		}
		spawn := got.PurpleSessionSource.SubAgent.PurpleSubAgentSource.ThreadSpawn
		if spawn.Depth != 2 || spawn.ParentThreadID != "thread-parent" {
			t.Fatalf("unexpected thread spawn payload: %+v", spawn)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		var got TentacledSessionSource
		if err := json.Unmarshal([]byte(`123`), &got); err == nil {
			t.Fatal("expected invalid payload to fail")
		}
	})
}

func TestApprovalPolicyUnmarshalJSON(t *testing.T) {
	t.Run("thread start params string enum", func(t *testing.T) {
		var got ThreadStartParamsApprovalPolicy
		if err := json.Unmarshal([]byte(`"never"`), &got); err != nil {
			t.Fatalf("unmarshal thread start approval policy: %v", err)
		}
		if got.Enum == nil || *got.Enum != Never {
			t.Fatalf("unexpected enum value: %+v", got.Enum)
		}
		if got.PurpleGranularAskForApproval != nil {
			t.Fatalf("expected reject branch to be nil, got %+v", got.PurpleGranularAskForApproval)
		}
	})

	t.Run("thread start response string enum", func(t *testing.T) {
		var got AskForApproval
		if err := json.Unmarshal([]byte(`"on-request"`), &got); err != nil {
			t.Fatalf("unmarshal response approval policy: %v", err)
		}
		if got.Enum == nil || *got.Enum != OnRequest {
			t.Fatalf("unexpected enum value: %+v", got.Enum)
		}
		if got.AskForApprovalGranularAskForApproval != nil {
			t.Fatalf("expected reject branch to be nil, got %+v", got.AskForApprovalGranularAskForApproval)
		}
	})

	t.Run("turn start params granular object", func(t *testing.T) {
		var got TurnStartParamsApprovalPolicy
		if err := json.Unmarshal([]byte(`{"granular":{"mcp_elicitations":true,"request_permissions":false,"rules":false,"sandbox_approval":true}}`), &got); err != nil {
			t.Fatalf("unmarshal granular approval policy: %v", err)
		}
		if got.Enum != nil {
			t.Fatalf("expected enum branch to be nil, got %+v", got.Enum)
		}
		if got.FluffyGranularAskForApproval == nil {
			t.Fatalf("expected reject branch, got %+v", got.FluffyGranularAskForApproval)
		}
		reject := got.FluffyGranularAskForApproval.Granular
		if !reject.MCPElicitations || reject.Rules || !reject.SandboxApproval {
			t.Fatalf("unexpected reject payload: %+v", reject)
		}
		if reject.RequestPermissions == nil || *reject.RequestPermissions {
			t.Fatalf("expected request_permissions=false, got %+v", reject.RequestPermissions)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		var got AskForApproval
		if err := json.Unmarshal([]byte(`true`), &got); err == nil {
			t.Fatal("expected invalid payload to fail")
		}
	})
}

func TestNetworkAccessUnionUnmarshalJSON(t *testing.T) {
	t.Run("bool", func(t *testing.T) {
		var got NetworkAccessUnion
		if err := json.Unmarshal([]byte(`true`), &got); err != nil {
			t.Fatalf("unmarshal bool network access: %v", err)
		}
		if got.Bool == nil || !*got.Bool {
			t.Fatalf("unexpected bool branch: %+v", got.Bool)
		}
		if got.Enum != nil {
			t.Fatalf("expected enum branch to be nil, got %+v", got.Enum)
		}
	})

	t.Run("enum", func(t *testing.T) {
		var got NetworkAccessUnion
		if err := json.Unmarshal([]byte(`"restricted"`), &got); err != nil {
			t.Fatalf("unmarshal enum network access: %v", err)
		}
		if got.Enum == nil || *got.Enum != NetworkAccessRestricted {
			t.Fatalf("unexpected enum branch: %+v", got.Enum)
		}
		if got.Bool != nil {
			t.Fatalf("expected bool branch to be nil, got %+v", got.Bool)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		var got NetworkAccessUnion
		if err := json.Unmarshal([]byte(`{}`), &got); err == nil {
			t.Fatal("expected invalid payload to fail")
		}
	})
}

func TestStickySessionSourceUnmarshalJSON(t *testing.T) {
	t.Run("string enum", func(t *testing.T) {
		var got StickySessionSource
		if err := json.Unmarshal([]byte(`"cli"`), &got); err != nil {
			t.Fatalf("unmarshal string session source: %v", err)
		}
		if got.Enum == nil || *got.Enum != CLI {
			t.Fatalf("unexpected enum value: %+v", got.Enum)
		}
		if got.FluffySessionSource != nil {
			t.Fatalf("expected object branch to be nil, got %+v", got.FluffySessionSource)
		}
	})

	t.Run("object sub-agent other", func(t *testing.T) {
		var got StickySessionSource
		if err := json.Unmarshal([]byte(`{"subAgent":{"other":"custom"}}`), &got); err != nil {
			t.Fatalf("unmarshal object session source: %v", err)
		}
		if got.Enum != nil {
			t.Fatalf("expected enum branch to be nil, got %+v", got.Enum)
		}
		if got.FluffySessionSource == nil || got.FluffySessionSource.SubAgent == nil || got.FluffySessionSource.SubAgent.FluffySubAgentSource == nil || got.FluffySessionSource.SubAgent.FluffySubAgentSource.Other == nil {
			t.Fatalf("expected other sub-agent branch, got %+v", got.FluffySessionSource)
		}
		if *got.FluffySessionSource.SubAgent.FluffySubAgentSource.Other != "custom" {
			t.Fatalf("unexpected other sub-agent value: %+v", got.FluffySessionSource.SubAgent.FluffySubAgentSource.Other)
		}
	})
}

func TestSubAgentSourceUnmarshalJSON(t *testing.T) {
	t.Run("purple enum", func(t *testing.T) {
		var got TentacledSubAgentSource
		if err := json.Unmarshal([]byte(`"review"`), &got); err != nil {
			t.Fatalf("unmarshal purple sub-agent enum: %v", err)
		}
		if got.Enum == nil || *got.Enum != Review {
			t.Fatalf("unexpected enum value: %+v", got.Enum)
		}
		if got.PurpleSubAgentSource != nil {
			t.Fatalf("expected object branch to be nil, got %+v", got.PurpleSubAgentSource)
		}
	})

	t.Run("sticky object other", func(t *testing.T) {
		var got StickySubAgentSource
		if err := json.Unmarshal([]byte(`{"other":"custom"}`), &got); err != nil {
			t.Fatalf("unmarshal sticky sub-agent object: %v", err)
		}
		if got.Enum != nil {
			t.Fatalf("expected enum branch to be nil, got %+v", got.Enum)
		}
		if got.FluffySubAgentSource == nil || got.FluffySubAgentSource.Other == nil || *got.FluffySubAgentSource.Other != "custom" {
			t.Fatalf("unexpected object payload: %+v", got.FluffySubAgentSource)
		}
	})
}
