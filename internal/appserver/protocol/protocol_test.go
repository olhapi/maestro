package protocol

import (
	"encoding/json"
	"testing"

	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
	"github.com/olhapi/maestro/internal/codexschema"
)

func marshalJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return body
}

func decodeJSONMap(t *testing.T, v interface{}) map[string]interface{} {
	t.Helper()
	body := marshalJSON(t, v)
	var out map[string]interface{}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode json map: %v", err)
	}
	return out
}

func TestDecodeMessageHelpers(t *testing.T) {
	line := `{"id":7,"method":"turn/completed","params":{"threadId":"th-1","turn":{"id":"tu-1","status":"completed","items":[]}}}`
	msg, ok := DecodeMessage(line)
	if !ok {
		t.Fatal("expected decode ok")
	}
	if !msg.HasID() {
		t.Fatal("expected message id")
	}
	if got, ok := msg.IntID(); !ok || got != 7 {
		t.Fatalf("unexpected message id: %d %t", got, ok)
	}
	if !msg.IsResponseTo(7) {
		t.Fatal("expected response id match")
	}

	var params gen.TurnCompletedNotification
	if err := msg.UnmarshalParams(&params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params.ThreadID != "th-1" || params.Turn.ID != "tu-1" {
		t.Fatalf("unexpected params: %+v", params)
	}
}

func TestInitializeRequestWireShape(t *testing.T) {
	req := InitializeRequest(1, "Maestro")
	msg, ok := DecodeMessage(string(marshalJSON(t, req)))
	if !ok {
		t.Fatal("expected decode ok")
	}
	if msg.Method != MethodInitialize {
		t.Fatalf("unexpected method: %s", msg.Method)
	}

	var params gen.InitializeParams
	if err := msg.UnmarshalParams(&params); err != nil {
		t.Fatalf("unmarshal initialize params: %v", err)
	}
	if params.ClientInfo.Name != "maestro" {
		t.Fatalf("unexpected client name: %+v", params.ClientInfo)
	}
	if params.ClientInfo.Title == nil || *params.ClientInfo.Title != "Maestro" {
		t.Fatalf("unexpected client title: %+v", params.ClientInfo)
	}
	if params.Capabilities == nil || params.Capabilities.ExperimentalAPI == nil || !*params.Capabilities.ExperimentalAPI {
		t.Fatalf("unexpected capabilities: %+v", params.Capabilities)
	}
}

func TestThreadAndTurnStartRequestWireShape(t *testing.T) {
	threadReq, err := ThreadStartRequest(2, "/tmp/work", "never", "workspace-write", []map[string]interface{}{
		{"name": "tool-a"},
	}, map[string]interface{}{"initial_collaboration_mode": "plan"})
	if err != nil {
		t.Fatalf("thread start request: %v", err)
	}
	threadPayload := decodeJSONMap(t, threadReq)
	if threadPayload["method"] != MethodThreadStart {
		t.Fatalf("unexpected thread/start method: %+v", threadPayload)
	}
	threadParams := threadPayload["params"].(map[string]interface{})
	if threadParams["approvalPolicy"] != "never" {
		t.Fatalf("expected raw approval policy, got %+v", threadParams["approvalPolicy"])
	}
	if threadParams["sandbox"] != "workspace-write" {
		t.Fatalf("unexpected sandbox: %+v", threadParams["sandbox"])
	}
	if threadParams["cwd"] != "/tmp/work" {
		t.Fatalf("unexpected cwd: %+v", threadParams["cwd"])
	}
	config := threadParams["config"].(map[string]interface{})
	if config["initial_collaboration_mode"] != "plan" {
		t.Fatalf("unexpected thread/start config: %+v", config)
	}
	dynamicTools := threadParams["dynamicTools"].([]interface{})
	if len(dynamicTools) != 1 {
		t.Fatalf("unexpected dynamic tools: %+v", dynamicTools)
	}

	turnReq, err := TurnStartRequest(3, "thread-1", []gen.UserInputElement{
		TextInput("fix it"),
		LocalImageInput(".maestro/issue-assets/img-1-screen.png", "screen.png"),
	}, "/tmp/work", "on-request", map[string]interface{}{
		"type":          "workspaceWrite",
		"networkAccess": true,
		"writableRoots": []string{"/tmp/work"},
	})
	if err != nil {
		t.Fatalf("turn start request: %v", err)
	}
	turnPayload := decodeJSONMap(t, turnReq)
	if turnPayload["method"] != MethodTurnStart {
		t.Fatalf("unexpected turn/start method: %+v", turnPayload)
	}
	turnParams := turnPayload["params"].(map[string]interface{})
	if turnParams["approvalPolicy"] != "on-request" {
		t.Fatalf("expected raw approval policy, got %+v", turnParams["approvalPolicy"])
	}
	if turnParams["threadId"] != "thread-1" {
		t.Fatalf("unexpected thread id: %+v", turnParams["threadId"])
	}
	input := turnParams["input"].([]interface{})
	if len(input) != 2 {
		t.Fatalf("unexpected turn input count: %+v", input)
	}
	firstInput := input[0].(map[string]interface{})
	if firstInput["type"] != string(gen.Text) || firstInput["text"] != "fix it" {
		t.Fatalf("unexpected first turn input: %+v", firstInput)
	}
	secondInput := input[1].(map[string]interface{})
	if secondInput["type"] != string(gen.LocalImage) || secondInput["path"] != ".maestro/issue-assets/img-1-screen.png" || secondInput["name"] != "screen.png" {
		t.Fatalf("unexpected second turn input: %+v", secondInput)
	}
	sandboxPolicy := turnParams["sandboxPolicy"].(map[string]interface{})
	if sandboxPolicy["type"] != "workspaceWrite" {
		t.Fatalf("unexpected sandbox policy: %+v", sandboxPolicy)
	}
}

func TestStructuredApprovalPolicyPreservesRequestPermissions(t *testing.T) {
	cases := []struct {
		name              string
		key               string
		threadRequestPerm bool
		turnRequestPerm   bool
	}{
		{
			name:              "granular",
			key:               "granular",
			threadRequestPerm: false,
			turnRequestPerm:   true,
		},
		{
			name:              "legacy reject",
			key:               "reject",
			threadRequestPerm: false,
			turnRequestPerm:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			threadApproval, err := toThreadApprovalPolicy(map[string]interface{}{
				tc.key: map[string]interface{}{
					"mcp_elicitations":    true,
					"request_permissions": tc.threadRequestPerm,
					"rules":               true,
					"sandbox_approval":    true,
				},
			})
			if err != nil {
				t.Fatalf("thread approval policy: %v", err)
			}
			if threadApproval == nil || threadApproval.PurpleGranularAskForApproval == nil || threadApproval.PurpleGranularAskForApproval.Granular.RequestPermissions == nil || *threadApproval.PurpleGranularAskForApproval.Granular.RequestPermissions != tc.threadRequestPerm {
				t.Fatalf("unexpected thread approval policy: %+v", threadApproval)
			}

			turnApproval, err := toTurnApprovalPolicy(map[string]interface{}{
				tc.key: map[string]interface{}{
					"mcp_elicitations":    true,
					"request_permissions": tc.turnRequestPerm,
					"rules":               false,
					"sandbox_approval":    true,
				},
			})
			if err != nil {
				t.Fatalf("turn approval policy: %v", err)
			}
			if turnApproval == nil || turnApproval.FluffyGranularAskForApproval == nil || turnApproval.FluffyGranularAskForApproval.Granular.RequestPermissions == nil || *turnApproval.FluffyGranularAskForApproval.Granular.RequestPermissions != tc.turnRequestPerm {
				t.Fatalf("unexpected turn approval policy: %+v", turnApproval)
			}
		})
	}
}

func TestResponseBuildersWireShape(t *testing.T) {
	approvalMsg, ok := DecodeMessage(string(marshalJSON(t, CommandExecutionApprovalResult(json.RawMessage("99"), gen.AcceptForSession))))
	if !ok {
		t.Fatal("expected approval decode ok")
	}
	var approvalResult map[string]string
	if err := approvalMsg.UnmarshalResult(&approvalResult); err != nil {
		t.Fatalf("unmarshal approval result: %v", err)
	}
	if approvalResult["decision"] != "acceptForSession" {
		t.Fatalf("unexpected approval result: %+v", approvalResult)
	}

	structuredApprovalMsg, ok := DecodeMessage(string(marshalJSON(t, CommandExecutionApprovalResultPayload(json.RawMessage("100"), map[string]interface{}{
		"acceptWithExecpolicyAmendment": map[string]interface{}{
			"execpolicy_amendment": []string{"allow command=curl https://api.github.com"},
		},
	}))))
	if !ok {
		t.Fatal("expected structured approval decode ok")
	}
	structuredDecision, ok := decodeJSONMap(t, structuredApprovalMsg)["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected structured decision result, got %#v", structuredApprovalMsg)
	}
	if _, ok := structuredDecision["decision"].(map[string]interface{}); !ok {
		t.Fatalf("expected structured approval payload, got %+v", structuredDecision)
	}

	toolMsg, ok := DecodeMessage(string(marshalJSON(t, ToolRequestUserInputResult(json.RawMessage("101"), map[string]gen.ToolRequestUserInputAnswer{
		"question-1": {Answers: []string{"Use default"}},
	}))))
	if !ok {
		t.Fatal("expected tool input decode ok")
	}
	var toolResult gen.ToolRequestUserInputResponse
	if err := toolMsg.UnmarshalResult(&toolResult); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if answers := toolResult.Answers["question-1"].Answers; len(answers) != 1 || answers[0] != "Use default" {
		t.Fatalf("unexpected tool answers: %+v", toolResult.Answers)
	}

	dynamicMsg, ok := DecodeMessage(string(marshalJSON(t, DynamicToolCallResult(json.RawMessage("102"), true, "done"))))
	if !ok {
		t.Fatal("expected dynamic tool decode ok")
	}
	var dynamicResult gen.DynamicToolCallResponse
	if err := dynamicMsg.UnmarshalResult(&dynamicResult); err != nil {
		t.Fatalf("unmarshal dynamic tool result: %v", err)
	}
	if !dynamicResult.Success {
		t.Fatalf("expected success result: %+v", dynamicResult)
	}
	if len(dynamicResult.ContentItems) != 1 || dynamicResult.ContentItems[0].Text == nil || *dynamicResult.ContentItems[0].Text != "done" {
		t.Fatalf("unexpected content items: %+v", dynamicResult.ContentItems)
	}
	if dynamicResult.ContentItems[0].Type != gen.InputText {
		t.Fatalf("unexpected content item type: %+v", dynamicResult.ContentItems[0])
	}
}

func TestThreadStartResponseDecodesStringSessionSource(t *testing.T) {
	msg, ok := DecodeMessage(`{"id":2,"result":{"approvalPolicy":"on-request","cwd":"/tmp/work","model":"gpt-5","modelProvider":"openai","sandbox":{"type":"dangerFullAccess","networkAccess":true},"thread":{"id":"thread-1","cliVersion":"` + codexschema.SupportedVersion + `","createdAt":1,"cwd":"/tmp/work","ephemeral":false,"modelProvider":"openai","preview":"","source":"appServer","status":{"type":"idle"},"turns":[],"updatedAt":2}}}`)
	if !ok {
		t.Fatal("expected decode ok")
	}

	var result gen.ThreadStartResponse
	if err := msg.UnmarshalResult(&result); err != nil {
		t.Fatalf("unmarshal thread/start result: %v", err)
	}
	if result.Thread.Source == nil || result.Thread.Source.Enum == nil {
		t.Fatalf("expected string session source, got %+v", result.Thread.Source)
	}
	if *result.Thread.Source.Enum != gen.AppServer {
		t.Fatalf("unexpected session source: %+v", *result.Thread.Source.Enum)
	}
	if result.ApprovalPolicy == nil || result.ApprovalPolicy.Enum == nil || *result.ApprovalPolicy.Enum != gen.OnRequest {
		t.Fatalf("unexpected approval policy: %+v", result.ApprovalPolicy)
	}
}

func TestThreadStartedNotificationDecodesNestedSubAgentSource(t *testing.T) {
	msg, ok := DecodeMessage(`{"method":"thread/started","params":{"thread":{"id":"thread-2","cliVersion":"` + codexschema.SupportedVersion + `","createdAt":1,"cwd":"/tmp/work","ephemeral":false,"modelProvider":"openai","preview":"","source":{"subAgent":"review"},"status":{"type":"active"},"turns":[],"updatedAt":2}}}`)
	if !ok {
		t.Fatal("expected decode ok")
	}

	var params gen.ThreadStartedNotification
	if err := msg.UnmarshalParams(&params); err != nil {
		t.Fatalf("unmarshal thread/started params: %v", err)
	}
	if params.Thread.Source == nil || params.Thread.Source.FluffySessionSource == nil || params.Thread.Source.FluffySessionSource.SubAgent == nil || params.Thread.Source.FluffySessionSource.SubAgent.Enum == nil {
		t.Fatalf("expected nested sub-agent session source, got %+v", params.Thread.Source)
	}
	if *params.Thread.Source.FluffySessionSource.SubAgent.Enum != gen.Review {
		t.Fatalf("unexpected sub-agent source: %+v", *params.Thread.Source.FluffySessionSource.SubAgent.Enum)
	}
}
