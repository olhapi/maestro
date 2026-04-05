package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
)

func TestNotificationAndResumeRequestWireShape(t *testing.T) {
	notification := InitializedNotification()
	if notification.Method != MethodInitialized {
		t.Fatalf("unexpected notification method: %q", notification.Method)
	}
	body, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	if !strings.Contains(string(body), MethodInitialized) {
		t.Fatalf("expected initialized notification payload, got %s", body)
	}

	req, err := ThreadResumeRequest(9, "thread-9", "/tmp/work", "never", "workspace-write")
	if err != nil {
		t.Fatalf("ThreadResumeRequest: %v", err)
	}
	msg, ok := DecodeMessage(string(marshalJSON(t, req)))
	if !ok {
		t.Fatal("expected resume request to decode")
	}
	if msg.Method != MethodThreadResume {
		t.Fatalf("unexpected thread resume method: %q", msg.Method)
	}
	var params ThreadResumeParams
	if err := msg.UnmarshalParams(&params); err != nil {
		t.Fatalf("unmarshal resume params: %v", err)
	}
	if params.ThreadID != "thread-9" || params.Sandbox != "workspace-write" || params.Cwd == nil || *params.Cwd != "/tmp/work" {
		t.Fatalf("unexpected resume params: %+v", params)
	}
}

func TestResponseBuildersAndDynamicToolResultFromMap(t *testing.T) {
	tests := []struct {
		name   string
		result interface{}
		want   string
	}{
		{name: "exec command", result: ExecCommandApprovalResult(json.RawMessage("1"), gen.Approved), want: "approved"},
		{name: "apply patch", result: ApplyPatchApprovalResult(json.RawMessage("2"), gen.Denied), want: "denied"},
		{name: "file change", result: FileChangeApprovalResult(json.RawMessage("3"), gen.FileChangeApprovalDecisionAccept), want: "accept"},
		{name: "elicitation", result: MCPServerElicitationRequestResult(json.RawMessage("4"), gen.MCPServerElicitationActionAccept, map[string]interface{}{"text": "done"}), want: "accept"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, ok := DecodeMessage(string(marshalJSON(t, tc.result)))
			if !ok {
				t.Fatal("expected response to decode")
			}
			var decoded map[string]interface{}
			if err := msg.UnmarshalResult(&decoded); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}
			if tc.want != "" && decoded["decision"] != tc.want && decoded["action"] != tc.want {
				t.Fatalf("unexpected result payload: %#v", decoded)
			}
		})
	}

	dynamic, err := DynamicToolCallResultFromMap(json.RawMessage("5"), map[string]interface{}{
		"success": true,
		"contentItems": []interface{}{
			map[string]interface{}{
				"type": gen.InputText,
				"text": "done",
			},
		},
	})
	if err != nil {
		t.Fatalf("DynamicToolCallResultFromMap: %v", err)
	}
	msg, ok := DecodeMessage(string(marshalJSON(t, dynamic)))
	if !ok {
		t.Fatal("expected dynamic tool response to decode")
	}
	var result gen.DynamicToolCallResponse
	if err := msg.UnmarshalResult(&result); err != nil {
		t.Fatalf("unmarshal dynamic tool result: %v", err)
	}
	if !result.Success || len(result.ContentItems) != 1 || result.ContentItems[0].Text == nil || *result.ContentItems[0].Text != "done" {
		t.Fatalf("unexpected dynamic tool result: %+v", result)
	}
}

func TestSandboxConversionHelpers(t *testing.T) {
	sandbox, err := toSandboxMode("workspace-write")
	if err != nil || sandbox == nil || *sandbox != gen.WorkspaceWrite {
		t.Fatalf("unexpected sandbox mode conversion: %+v err=%v", sandbox, err)
	}

	if enum, err := toApprovalPolicyEnum("on-request"); err != nil || enum != gen.OnRequest {
		t.Fatalf("unexpected approval policy enum: %v err=%v", enum, err)
	}
	if policyType, err := toSandboxPolicyType("workspaceWrite"); err != nil || policyType != gen.SandboxPolicyTypeWorkspaceWrite {
		t.Fatalf("unexpected sandbox policy type: %v err=%v", policyType, err)
	}

	if access, err := toNetworkAccess(true); err != nil || access == nil || access.Bool == nil || !*access.Bool {
		t.Fatalf("unexpected boolean network access: %+v err=%v", access, err)
	}
	if access, err := toNetworkAccess("restricted"); err != nil || access == nil || access.Enum == nil || *access.Enum != gen.NetworkAccessRestricted {
		t.Fatalf("unexpected enum network access: %+v err=%v", access, err)
	}
	if access, err := toNetworkAccess(nil); err != nil || access != nil {
		t.Fatalf("expected nil network access, got %+v err=%v", access, err)
	}
	if _, err := toNetworkAccess(123); err == nil || !strings.Contains(err.Error(), "unsupported network access type") {
		t.Fatalf("expected unsupported network access type error, got %v", err)
	}

	if access, err := toReadOnlyAccess(map[string]interface{}{
		"type":                    "restricted",
		"readableRoots":           []interface{}{"/tmp", " ", "/repo"},
		"includePlatformDefaults": true,
	}); err != nil || access == nil || access.Type != gen.ReadOnlyAccessTypeRestricted || len(access.ReadableRoots) != 2 || access.IncludePlatformDefaults == nil || !*access.IncludePlatformDefaults {
		t.Fatalf("unexpected read-only access payload: %+v err=%v", access, err)
	}
	if access, err := toReadOnlyAccess(nil); err != nil || access != nil {
		t.Fatalf("expected nil read-only access, got %+v err=%v", access, err)
	}

	if values := stringSliceValue([]string{"a", "b"}); len(values) != 2 {
		t.Fatalf("expected string slice copy, got %#v", values)
	}
	if values := stringSliceValue([]interface{}{"a", " ", "b"}); len(values) != 2 {
		t.Fatalf("expected interface slice filtering, got %#v", values)
	}
}
