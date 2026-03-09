package gen

import (
	"encoding/json"
	"testing"
)

func TestPurpleSessionSourceUnmarshalJSON(t *testing.T) {
	t.Run("string enum", func(t *testing.T) {
		var got PurpleSessionSource
		if err := json.Unmarshal([]byte(`"appServer"`), &got); err != nil {
			t.Fatalf("unmarshal string session source: %v", err)
		}
		if got.Enum == nil || *got.Enum != AppServer {
			t.Fatalf("unexpected enum value: %+v", got.Enum)
		}
		if got.PurpleSubAgentSessionSource != nil {
			t.Fatalf("expected object branch to be nil, got %+v", got.PurpleSubAgentSessionSource)
		}
	})

	t.Run("object sub-agent thread spawn", func(t *testing.T) {
		var got PurpleSessionSource
		payload := []byte(`{"subAgent":{"thread_spawn":{"depth":2,"parent_thread_id":"thread-parent","agent_nickname":"helper","agent_role":"reviewer"}}}`)
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatalf("unmarshal object session source: %v", err)
		}
		if got.Enum != nil {
			t.Fatalf("expected enum branch to be nil, got %+v", got.Enum)
		}
		if got.PurpleSubAgentSessionSource == nil || got.PurpleSubAgentSessionSource.SubAgent == nil || got.PurpleSubAgentSessionSource.SubAgent.PurpleSubAgentSource == nil || got.PurpleSubAgentSessionSource.SubAgent.PurpleSubAgentSource.ThreadSpawn == nil {
			t.Fatalf("expected thread_spawn branch, got %+v", got.PurpleSubAgentSessionSource)
		}
		spawn := got.PurpleSubAgentSessionSource.SubAgent.PurpleSubAgentSource.ThreadSpawn
		if spawn.Depth != 2 || spawn.ParentThreadID != "thread-parent" {
			t.Fatalf("unexpected thread spawn payload: %+v", spawn)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		var got PurpleSessionSource
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
		if got.PurpleRejectAskForApproval != nil {
			t.Fatalf("expected reject branch to be nil, got %+v", got.PurpleRejectAskForApproval)
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
		if got.AskForApprovalRejectAskForApproval != nil {
			t.Fatalf("expected reject branch to be nil, got %+v", got.AskForApprovalRejectAskForApproval)
		}
	})

	t.Run("turn start params reject object", func(t *testing.T) {
		var got TurnStartParamsApprovalPolicy
		if err := json.Unmarshal([]byte(`{"reject":{"mcp_elicitations":true,"rules":false,"sandbox_approval":true}}`), &got); err != nil {
			t.Fatalf("unmarshal reject approval policy: %v", err)
		}
		if got.Enum != nil {
			t.Fatalf("expected enum branch to be nil, got %+v", got.Enum)
		}
		if got.FluffyRejectAskForApproval == nil {
			t.Fatalf("expected reject branch, got %+v", got.FluffyRejectAskForApproval)
		}
		reject := got.FluffyRejectAskForApproval.Reject
		if !reject.MCPElicitations || reject.Rules || !reject.SandboxApproval {
			t.Fatalf("unexpected reject payload: %+v", reject)
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

func TestFluffySessionSourceUnmarshalJSON(t *testing.T) {
	t.Run("string enum", func(t *testing.T) {
		var got FluffySessionSource
		if err := json.Unmarshal([]byte(`"cli"`), &got); err != nil {
			t.Fatalf("unmarshal string session source: %v", err)
		}
		if got.Enum == nil || *got.Enum != CLI {
			t.Fatalf("unexpected enum value: %+v", got.Enum)
		}
		if got.FluffySubAgentSessionSource != nil {
			t.Fatalf("expected object branch to be nil, got %+v", got.FluffySubAgentSessionSource)
		}
	})

	t.Run("object sub-agent other", func(t *testing.T) {
		var got FluffySessionSource
		if err := json.Unmarshal([]byte(`{"subAgent":{"other":"custom"}}`), &got); err != nil {
			t.Fatalf("unmarshal object session source: %v", err)
		}
		if got.Enum != nil {
			t.Fatalf("expected enum branch to be nil, got %+v", got.Enum)
		}
		if got.FluffySubAgentSessionSource == nil || got.FluffySubAgentSessionSource.SubAgent == nil || got.FluffySubAgentSessionSource.SubAgent.FluffySubAgentSource == nil || got.FluffySubAgentSessionSource.SubAgent.FluffySubAgentSource.Other == nil {
			t.Fatalf("expected other sub-agent branch, got %+v", got.FluffySubAgentSessionSource)
		}
		if *got.FluffySubAgentSessionSource.SubAgent.FluffySubAgentSource.Other != "custom" {
			t.Fatalf("unexpected other sub-agent value: %+v", got.FluffySubAgentSessionSource.SubAgent.FluffySubAgentSource.Other)
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
