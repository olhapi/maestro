package codexschema

import "path/filepath"

const (
	SupportedVersion = "0.118.0"
	QuicktypeVersion = "23.2.6"
)

var ConsumedSchemaFiles = []string{
	"v1/InitializeParams.json",
	"v1/InitializeResponse.json",
	"v2/ThreadStartParams.json",
	"v2/ThreadStartResponse.json",
	"v2/TurnStartParams.json",
	"v2/TurnStartResponse.json",
	"v2/ThreadStartedNotification.json",
	"v2/TurnStartedNotification.json",
	"v2/TurnCompletedNotification.json",
	"ExecCommandApprovalParams.json",
	"ExecCommandApprovalResponse.json",
	"ApplyPatchApprovalParams.json",
	"ApplyPatchApprovalResponse.json",
	"CommandExecutionRequestApprovalParams.json",
	"CommandExecutionRequestApprovalResponse.json",
	"FileChangeRequestApprovalParams.json",
	"FileChangeRequestApprovalResponse.json",
	"ToolRequestUserInputParams.json",
	"ToolRequestUserInputResponse.json",
	"McpServerElicitationRequestParams.json",
	"McpServerElicitationRequestResponse.json",
	"DynamicToolCallParams.json",
	"DynamicToolCallResponse.json",
}

func SchemaDir(repoRoot string) string {
	return filepath.Join(repoRoot, "schemas", "codex", SupportedVersion, "json")
}
