package protocol

import (
	"encoding/json"
	"testing"

	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
)

func TestDecodeMessageAndMessageHelpers(t *testing.T) {
	msg, ok := DecodeMessage(`{"id":123,"method":"turn/start","params":{"title":"hello"},"result":{"ok":true}}`)
	if !ok {
		t.Fatal("expected decode to succeed")
	}
	if !msg.HasID() {
		t.Fatal("expected message to have an id")
	}
	if got, ok := msg.IntID(); !ok || got != 123 {
		t.Fatalf("unexpected IntID result: %d %v", got, ok)
	}
	var params struct {
		Title string `json:"title"`
	}
	if err := msg.UnmarshalParams(&params); err != nil {
		t.Fatalf("UnmarshalParams: %v", err)
	}
	if params.Title != "hello" {
		t.Fatalf("unexpected params: %+v", params)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := msg.UnmarshalResult(&result); err != nil {
		t.Fatalf("UnmarshalResult: %v", err)
	}
	if !result.OK {
		t.Fatalf("unexpected result: %+v", result)
	}

	if _, ok := DecodeMessage("not json"); ok {
		t.Fatal("expected non-JSON line to be ignored")
	}
	if (Message{ID: json.RawMessage("null")}).HasID() {
		t.Fatal("expected null id to be treated as absent")
	}
	if _, ok := (Message{ID: json.RawMessage(`"abc"`)}).IntID(); ok {
		t.Fatal("expected string id to fail IntID")
	}
	if err := (Message{Params: json.RawMessage("null")}).UnmarshalParams(&params); err != nil {
		t.Fatalf("expected null params to be ignored, got %v", err)
	}
	if err := (Message{Result: json.RawMessage("null")}).UnmarshalResult(&result); err != nil {
		t.Fatalf("expected null result to be ignored, got %v", err)
	}
}

func TestApprovalAndSandboxConversions(t *testing.T) {
	thread, err := toThreadApprovalPolicy("on_request")
	if err != nil {
		t.Fatalf("toThreadApprovalPolicy string: %v", err)
	}
	if thread.Enum == nil {
		t.Fatalf("unexpected thread approval enum: %+v", thread)
	}

	threadStructured, err := toThreadApprovalPolicy(map[string]interface{}{
		"granular": map[string]interface{}{
			"mcp_elicitations":    true,
			"rules":               true,
			"sandbox_approval":    false,
			"request_permissions": true,
		},
	})
	if err != nil {
		t.Fatalf("toThreadApprovalPolicy structured: %v", err)
	}
	if threadStructured.PurpleGranularAskForApproval == nil || !threadStructured.PurpleGranularAskForApproval.Granular.MCPElicitations || !*threadStructured.PurpleGranularAskForApproval.Granular.RequestPermissions {
		t.Fatalf("unexpected structured thread approval: %+v", threadStructured)
	}
	if _, err := toThreadApprovalPolicy(123); err == nil {
		t.Fatal("expected unsupported thread approval policy type to fail")
	}

	turn, err := toTurnApprovalPolicy("never")
	if err != nil {
		t.Fatalf("toTurnApprovalPolicy string: %v", err)
	}
	if turn.Enum == nil || *turn.Enum != gen.Never {
		t.Fatalf("unexpected turn approval enum: %+v", turn)
	}

	turnStructured, err := toTurnApprovalPolicy(map[string]interface{}{
		"granular": map[string]interface{}{
			"mcp_elicitations":    true,
			"rules":               false,
			"sandbox_approval":    true,
			"request_permissions": false,
		},
	})
	if err != nil {
		t.Fatalf("toTurnApprovalPolicy structured: %v", err)
	}
	if turnStructured.FluffyGranularAskForApproval == nil || !turnStructured.FluffyGranularAskForApproval.Granular.MCPElicitations || !turnStructured.FluffyGranularAskForApproval.Granular.SandboxApproval {
		t.Fatalf("unexpected structured turn approval: %+v", turnStructured)
	}
	if _, err := toTurnApprovalPolicy(123); err == nil {
		t.Fatal("expected unsupported turn approval policy type to fail")
	}

	if sandbox, err := toSandboxMode(""); err != nil || sandbox != nil {
		t.Fatalf("expected empty sandbox mode to return nil, got %+v err=%v", sandbox, err)
	}
	if sandbox, err := toSandboxMode("workspace-write"); err != nil || sandbox == nil || *sandbox != gen.WorkspaceWrite {
		t.Fatalf("unexpected sandbox mode conversion: %+v err=%v", sandbox, err)
	}

	policy, err := toTurnSandboxPolicy(map[string]interface{}{
		"type":                "dangerFullAccess",
		"networkAccess":       "enabled",
		"writableRoots":       []interface{}{"/tmp", "/work"},
		"excludeTmpdirEnvVar": true,
		"excludeSlashTmp":     false,
		"readOnlyAccess": map[string]interface{}{
			"type":                    "fullAccess",
			"readableRoots":           []interface{}{"/etc", ""},
			"includePlatformDefaults": true,
		},
	})
	if err != nil {
		t.Fatalf("toTurnSandboxPolicy: %v", err)
	}
	if policy.Type != gen.SandboxPolicyTypeDangerFullAccess || policy.NetworkAccess == nil || policy.NetworkAccess.Enum == nil || *policy.NetworkAccess.Enum != gen.Enabled {
		t.Fatalf("unexpected sandbox policy conversion: %+v", policy)
	}
	if len(policy.WritableRoots) != 2 || policy.ReadOnlyAccess == nil || policy.ReadOnlyAccess.Type != gen.FullAccess || policy.ReadOnlyAccess.IncludePlatformDefaults == nil || !*policy.ReadOnlyAccess.IncludePlatformDefaults {
		t.Fatalf("unexpected sandbox policy details: %+v", policy)
	}
	if _, err := toTurnSandboxPolicy(map[string]interface{}{"type": 123}); err == nil {
		t.Fatal("expected invalid sandbox policy type to fail")
	}
	if _, err := toNetworkAccess(123); err == nil {
		t.Fatal("expected invalid network access type to fail")
	}
	if access, err := toReadOnlyAccess("not-a-map"); err != nil || access != nil {
		t.Fatalf("expected non-map read-only access to be ignored, got %+v err=%v", access, err)
	}
	if got := stringSliceValue([]string{"a", "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("unexpected string slice conversion: %#v", got)
	}
	if got := stringSliceValue([]interface{}{"x", 1, "y"}); len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("unexpected interface slice conversion: %#v", got)
	}
	if stringSliceValue(nil) != nil {
		t.Fatal("expected nil slice conversion to stay nil")
	}
}

func TestResponseBuildersAndDynamicToolConversions(t *testing.T) {
	reqID := RequestID(json.RawMessage("1"))
	if got := InitializedNotification(); got.Method != MethodInitialized {
		t.Fatalf("unexpected initialized notification: %+v", got)
	}
	if got, err := ThreadResumeRequest(7, "thread-1", "/work", "never", "workspace-write"); err != nil {
		t.Fatalf("ThreadResumeRequest: %v", err)
	} else if got.Method != MethodThreadResume || got.Params.ThreadID != "thread-1" {
		t.Fatalf("unexpected thread resume request: %+v", got)
	}
	if got := ExecCommandApprovalResult(reqID, gen.ReviewDecision("approve")); got.Result.Decision != "approve" {
		t.Fatalf("unexpected exec command approval result: %+v", got)
	}
	if got := ApplyPatchApprovalResult(reqID, gen.ReviewDecision("deny")); got.Result.Decision != "deny" {
		t.Fatalf("unexpected apply patch approval result: %+v", got)
	}
	if got := FileChangeApprovalResult(reqID, gen.FileChangeApprovalDecision("approve")); got.Result.Decision != "approve" {
		t.Fatalf("unexpected file change approval result: %+v", got)
	}
	if got := MCPServerElicitationRequestResult(reqID, gen.MCPServerElicitationAction("respond"), map[string]interface{}{"message": "hi"}); got.Result.Action != gen.MCPServerElicitationAction("respond") {
		t.Fatalf("unexpected elicitation result: %+v", got)
	}

	payload := map[string]interface{}{
		"success": true,
		"contentItems": []interface{}{
			map[string]interface{}{
				"type": "input_text",
				"text": "hello",
			},
		},
	}
	result, err := DynamicToolCallResultFromMap(reqID, payload)
	if err != nil {
		t.Fatalf("DynamicToolCallResultFromMap: %v", err)
	}
	if !result.Result.Success || len(result.Result.ContentItems) != 1 {
		t.Fatalf("unexpected dynamic tool call result: %+v", result)
	}
	if _, err := DynamicToolCallResultFromMap(reqID, map[string]interface{}{"bad": make(chan int)}); err == nil {
		t.Fatal("expected marshal failure for unsupported payload")
	}
	if _, err := DynamicToolCallResultFromMap(reqID, map[string]interface{}{
		"success": true,
		"contentItems": []interface{}{
			"bad",
		},
	}); err == nil {
		t.Fatal("expected unmarshal failure for malformed payload")
	}
}

func TestApprovalPolicyErrorsAndEmptyBuilders(t *testing.T) {
	if _, err := toApprovalPolicyEnum("never"); err != nil {
		t.Fatalf("toApprovalPolicyEnum: %v", err)
	}
	if _, err := toThreadStructuredApproval(map[string]interface{}{}); err == nil {
		t.Fatal("expected structured thread approval helper to reject missing structure")
	}
	if _, err := toTurnStructuredApproval(map[string]interface{}{}); err == nil {
		t.Fatal("expected structured turn approval helper to reject missing structure")
	}
	if policy, err := toTurnSandboxPolicy(nil); err != nil || policy != nil {
		t.Fatalf("expected nil turn sandbox policy to stay nil, got %+v err=%v", policy, err)
	}
}

func TestProtocolErrorBranches(t *testing.T) {
	if _, ok := DecodeMessage("   "); ok {
		t.Fatal("expected blank line to be ignored")
	}
	if _, ok := DecodeMessage("{broken json"); ok {
		t.Fatal("expected malformed JSON to be ignored")
	}
	if (Message{}).HasID() {
		t.Fatal("expected empty message to report no id")
	}
	if _, ok := (Message{}).IntID(); ok {
		t.Fatal("expected empty message id to fail IntID")
	}

	if got, err := toThreadApprovalPolicy(nil); err != nil || got != nil {
		t.Fatalf("expected nil thread approval policy, got %+v err=%v", got, err)
	}
	if got, err := toTurnApprovalPolicy(nil); err != nil || got != nil {
		t.Fatalf("expected nil turn approval policy, got %+v err=%v", got, err)
	}
	if _, err := toSandboxPolicyType(123); err == nil {
		t.Fatal("expected invalid sandbox policy type to fail")
	}
	if access, err := toNetworkAccess(false); err != nil || access == nil || access.Bool == nil || *access.Bool {
		t.Fatalf("expected boolean network access branch, got %+v err=%v", access, err)
	}
	if _, err := toReadOnlyAccess(map[string]interface{}{"type": 123}); err == nil {
		t.Fatal("expected invalid read-only access type to fail")
	}
	if _, err := toReadOnlyAccess(map[string]interface{}{"type": "fullAccess", "readableRoots": []interface{}{1}}); err != nil {
		t.Fatalf("expected read-only access with filtered roots to succeed, got %v", err)
	}
	if access, err := toReadOnlyAccess(map[string]interface{}{
		"type":                    "fullAccess",
		"readableRoots":           []interface{}{"", "/tmp"},
		"includePlatformDefaults": false,
	}); err != nil || access == nil || access.IncludePlatformDefaults == nil || *access.IncludePlatformDefaults {
		t.Fatalf("expected read-only access false branch to succeed, got %+v err=%v", access, err)
	}
}
