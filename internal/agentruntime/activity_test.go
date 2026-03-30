package agentruntime

import "testing"

func TestActivityEventCloneDeepCopiesNestedFields(t *testing.T) {
	exitCode := 3
	event := ActivityEvent{
		Type: "item.completed",
		Item: map[string]interface{}{
			"command": "go test ./...",
			"nested": map[string]interface{}{
				"status": "ok",
			},
		},
		Raw: map[string]interface{}{
			"turnId": "turn-1",
		},
		Metadata: map[string]interface{}{
			"provider": "codex",
		},
		ExitCode: &exitCode,
	}

	cloned := event.Clone()
	cloned.Item["command"] = "mutated"
	cloned.Item["nested"].(map[string]interface{})["status"] = "changed"
	cloned.Raw["turnId"] = "turn-2"
	cloned.Metadata["provider"] = "other"
	*cloned.ExitCode = 0

	if event.Item["command"] != "go test ./..." {
		t.Fatalf("expected cloned item map to be independent, got %+v", event.Item)
	}
	if event.Item["nested"].(map[string]interface{})["status"] != "ok" {
		t.Fatalf("expected nested item map to be independent, got %+v", event.Item)
	}
	if event.Raw["turnId"] != "turn-1" {
		t.Fatalf("expected raw payload clone to be independent, got %+v", event.Raw)
	}
	if event.Metadata["provider"] != "codex" {
		t.Fatalf("expected metadata clone to be independent, got %+v", event.Metadata)
	}
	if *event.ExitCode != 3 {
		t.Fatalf("expected exit code pointer to be copied by value, got %d", *event.ExitCode)
	}
}
