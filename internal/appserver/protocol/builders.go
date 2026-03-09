package protocol

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
)

type ThreadStartParams struct {
	ApprovalPolicy interface{}              `json:"approvalPolicy,omitempty"`
	Cwd            *string                  `json:"cwd,omitempty"`
	DynamicTools   []map[string]interface{} `json:"dynamicTools,omitempty"`
	Sandbox        string                   `json:"sandbox,omitempty"`
}

type TurnStartParams struct {
	ApprovalPolicy interface{}            `json:"approvalPolicy,omitempty"`
	Cwd            *string                `json:"cwd,omitempty"`
	Input          []gen.UserInputElement `json:"input"`
	SandboxPolicy  map[string]interface{} `json:"sandboxPolicy,omitempty"`
	ThreadID       string                 `json:"threadId"`
}

type decisionResponse struct {
	Decision string `json:"decision"`
}

func BoolPtr(v bool) *bool {
	return &v
}

func StringPtr(v string) *string {
	return &v
}

func InitializeRequest(id int, title string) Request[gen.InitializeParams] {
	return Request[gen.InitializeParams]{
		ID:     id,
		Method: MethodInitialize,
		Params: gen.InitializeParams{
			Capabilities: &gen.InitializeCapabilities{
				ExperimentalAPI: BoolPtr(true),
			},
			ClientInfo: gen.ClientInfo{
				Name:    "maestro",
				Title:   StringPtr(title),
				Version: "dev",
			},
		},
	}
}

func InitializedNotification() Notification[struct{}] {
	return Notification[struct{}]{
		Method: MethodInitialized,
		Params: struct{}{},
	}
}

func ThreadStartRequest(id int, workspace string, approvalPolicy interface{}, sandbox string, dynamicTools []map[string]interface{}) (Request[ThreadStartParams], error) {
	return Request[ThreadStartParams]{
		ID:     id,
		Method: MethodThreadStart,
		Params: ThreadStartParams{
			ApprovalPolicy: approvalPolicy,
			Cwd:            StringPtr(workspace),
			DynamicTools:   dynamicTools,
			Sandbox:        sandbox,
		},
	}, nil
}

func TurnStartRequest(id int, threadID, prompt, workspace string, approvalPolicy interface{}, sandboxPolicy map[string]interface{}) (Request[TurnStartParams], error) {
	return Request[TurnStartParams]{
		ID:     id,
		Method: MethodTurnStart,
		Params: TurnStartParams{
			ApprovalPolicy: approvalPolicy,
			Cwd:            StringPtr(workspace),
			Input: []gen.UserInputElement{{
				Type: gen.Text,
				Text: StringPtr(prompt),
			}},
			SandboxPolicy: sandboxPolicy,
			ThreadID:      threadID,
		},
	}, nil
}

func ExecCommandApprovalResult(id RequestID, decision gen.ReviewDecision) SuccessResponse[decisionResponse] {
	return SuccessResponse[decisionResponse]{
		ID:     id,
		Result: decisionResponse{Decision: string(decision)},
	}
}

func ApplyPatchApprovalResult(id RequestID, decision gen.ReviewDecision) SuccessResponse[decisionResponse] {
	return SuccessResponse[decisionResponse]{
		ID:     id,
		Result: decisionResponse{Decision: string(decision)},
	}
}

func CommandExecutionApprovalResult(id RequestID, decision gen.FileChangeApprovalDecision) SuccessResponse[decisionResponse] {
	return SuccessResponse[decisionResponse]{
		ID:     id,
		Result: decisionResponse{Decision: string(decision)},
	}
}

func FileChangeApprovalResult(id RequestID, decision gen.FileChangeApprovalDecision) SuccessResponse[decisionResponse] {
	return SuccessResponse[decisionResponse]{
		ID:     id,
		Result: decisionResponse{Decision: string(decision)},
	}
}

func ToolRequestUserInputResult(id RequestID, answers map[string]gen.ToolRequestUserInputAnswer) SuccessResponse[gen.ToolRequestUserInputResponse] {
	return SuccessResponse[gen.ToolRequestUserInputResponse]{
		ID:     id,
		Result: gen.ToolRequestUserInputResponse{Answers: answers},
	}
}

func DynamicToolCallResult(id RequestID, success bool, text string) SuccessResponse[gen.DynamicToolCallResponse] {
	return SuccessResponse[gen.DynamicToolCallResponse]{
		ID: id,
		Result: gen.DynamicToolCallResponse{
			Success: success,
			ContentItems: []gen.DynamicToolCallResponseDynamicToolCallOutputContentItem{{
				Type: gen.InputText,
				Text: StringPtr(text),
			}},
		},
	}
}

func DynamicToolCallResultFromMap(id RequestID, payload map[string]interface{}) (SuccessResponse[gen.DynamicToolCallResponse], error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return SuccessResponse[gen.DynamicToolCallResponse]{}, err
	}
	var out gen.DynamicToolCallResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return SuccessResponse[gen.DynamicToolCallResponse]{}, err
	}
	return SuccessResponse[gen.DynamicToolCallResponse]{ID: id, Result: out}, nil
}

func toThreadApprovalPolicy(raw interface{}) (*gen.ThreadStartParamsApprovalPolicy, error) {
	if raw == nil {
		return nil, nil
	}
	switch typed := raw.(type) {
	case string:
		enum, err := toApprovalPolicyEnum(typed)
		if err != nil {
			return nil, err
		}
		return &gen.ThreadStartParamsApprovalPolicy{Enum: &enum}, nil
	case map[string]interface{}:
		reject, err := toThreadRejectApproval(typed)
		if err != nil {
			return nil, err
		}
		return &gen.ThreadStartParamsApprovalPolicy{PurpleRejectAskForApproval: reject}, nil
	default:
		return nil, fmt.Errorf("unsupported thread approval policy type %T", raw)
	}
}

func toTurnApprovalPolicy(raw interface{}) (*gen.TurnStartParamsApprovalPolicy, error) {
	if raw == nil {
		return nil, nil
	}
	switch typed := raw.(type) {
	case string:
		enum, err := toApprovalPolicyEnum(typed)
		if err != nil {
			return nil, err
		}
		return &gen.TurnStartParamsApprovalPolicy{Enum: &enum}, nil
	case map[string]interface{}:
		reject, err := toTurnRejectApproval(typed)
		if err != nil {
			return nil, err
		}
		return &gen.TurnStartParamsApprovalPolicy{FluffyRejectAskForApproval: reject}, nil
	default:
		return nil, fmt.Errorf("unsupported turn approval policy type %T", raw)
	}
}

func toSandboxMode(raw string) (*gen.SandboxMode, error) {
	if raw == "" {
		return nil, nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var out gen.SandboxMode
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode thread sandbox: %w", err)
	}
	return &out, nil
}

func toTurnSandboxPolicy(raw map[string]interface{}) (*gen.DangerFullAccessSandboxPolicyClass, error) {
	if raw == nil {
		return nil, nil
	}
	policyType, err := toSandboxPolicyType(raw["type"])
	if err != nil {
		return nil, err
	}
	networkAccess, err := toNetworkAccess(raw["networkAccess"])
	if err != nil {
		return nil, err
	}
	out := &gen.DangerFullAccessSandboxPolicyClass{
		Type:          policyType,
		NetworkAccess: networkAccess,
		WritableRoots: stringSliceValue(raw["writableRoots"]),
	}
	if readOnly, err := toReadOnlyAccess(raw["readOnlyAccess"]); err != nil {
		return nil, err
	} else {
		out.ReadOnlyAccess = readOnly
	}
	if excludeTmp, ok := boolValue(raw["excludeTmpdirEnvVar"]); ok {
		out.ExcludeTmpdirEnvVar = BoolPtr(excludeTmp)
	}
	if excludeSlash, ok := boolValue(raw["excludeSlashTmp"]); ok {
		out.ExcludeSlashTmp = BoolPtr(excludeSlash)
	}
	return out, nil
}

func toApprovalPolicyEnum(raw string) (gen.ApprovalPolicyEnum, error) {
	body, err := json.Marshal(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	var out gen.ApprovalPolicyEnum
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode approval policy: %w", err)
	}
	return out, nil
}

func toThreadRejectApproval(raw map[string]interface{}) (*gen.PurpleRejectAskForApproval, error) {
	rejectMap, ok := raw["reject"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode thread approval policy: missing reject policy")
	}
	return &gen.PurpleRejectAskForApproval{
		Reject: gen.PurpleReject{
			MCPElicitations: boolValueOrFalse(rejectMap["mcp_elicitations"]),
			Rules:           boolValueOrFalse(rejectMap["rules"]),
			SandboxApproval: boolValueOrFalse(rejectMap["sandbox_approval"]),
		},
	}, nil
}

func toTurnRejectApproval(raw map[string]interface{}) (*gen.FluffyRejectAskForApproval, error) {
	rejectMap, ok := raw["reject"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode turn approval policy: missing reject policy")
	}
	return &gen.FluffyRejectAskForApproval{
		Reject: gen.TentacledReject{
			MCPElicitations: boolValueOrFalse(rejectMap["mcp_elicitations"]),
			Rules:           boolValueOrFalse(rejectMap["rules"]),
			SandboxApproval: boolValueOrFalse(rejectMap["sandbox_approval"]),
		},
	}, nil
}

func toSandboxPolicyType(raw interface{}) (gen.SandboxPolicyType, error) {
	body, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	var out gen.SandboxPolicyType
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode sandbox type: %w", err)
	}
	return out, nil
}

func toNetworkAccess(raw interface{}) (*gen.NetworkAccessUnion, error) {
	switch typed := raw.(type) {
	case bool:
		return &gen.NetworkAccessUnion{Bool: &typed}, nil
	case string:
		body, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		var out gen.NetworkAccess
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("decode network access: %w", err)
		}
		return &gen.NetworkAccessUnion{Enum: &out}, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported network access type %T", raw)
	}
}

func toReadOnlyAccess(raw interface{}) (*gen.DangerFullAccessSandboxPolicyReadOnlyAccess, error) {
	m, ok := raw.(map[string]interface{})
	if !ok || m == nil {
		return nil, nil
	}
	body, err := json.Marshal(m["type"])
	if err != nil {
		return nil, err
	}
	var accessType gen.ReadOnlyAccessType
	if err := json.Unmarshal(body, &accessType); err != nil {
		return nil, fmt.Errorf("decode read-only access type: %w", err)
	}
	out := &gen.DangerFullAccessSandboxPolicyReadOnlyAccess{
		Type:          accessType,
		ReadableRoots: stringSliceValue(m["readableRoots"]),
	}
	if includeDefaults, ok := boolValue(m["includePlatformDefaults"]); ok {
		out.IncludePlatformDefaults = BoolPtr(includeDefaults)
	}
	return out, nil
}

func stringSliceValue(raw interface{}) []string {
	items, ok := raw.([]string)
	if ok {
		return append([]string(nil), items...)
	}
	values, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, item := range values {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, text)
		}
	}
	return out
}

func boolValue(raw interface{}) (bool, bool) {
	value, ok := raw.(bool)
	return value, ok
}

func boolValueOrFalse(raw interface{}) bool {
	value, _ := boolValue(raw)
	return value
}
