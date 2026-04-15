package gen

type InitializeParams struct {
	Capabilities *InitializeCapabilities `json:"capabilities"`
	ClientInfo   ClientInfo              `json:"clientInfo"`
}

// Client-declared capabilities negotiated during initialize.
type InitializeCapabilities struct {
	// Opt into receiving experimental API methods and fields.
	ExperimentalAPI *bool `json:"experimentalApi,omitempty"`
	// Exact notification method names that should be suppressed for this connection (for
	// example `thread/started`).
	OptOutNotificationMethods []string `json:"optOutNotificationMethods"`
}

type ClientInfo struct {
	Name    string  `json:"name"`
	Title   *string `json:"title"`
	Version string  `json:"version"`
}

type InitializeResponse struct {
	// Absolute path to the server's $CODEX_HOME directory.
	CodexHome string `json:"codexHome"`
	// Platform family for the running app-server target, for example `"unix"` or `"windows"`.
	PlatformFamily string `json:"platformFamily"`
	// Operating system for the running app-server target, for example `"macos"`, `"linux"`, or
	// `"windows"`.
	PlatformOS string `json:"platformOs"`
	UserAgent  string `json:"userAgent"`
}

type ThreadStartParams struct {
	ApprovalPolicy *ThreadStartParamsApprovalPolicy `json:"approvalPolicy"`
	// Override where approval requests are routed for review on this thread and subsequent
	// turns.
	ApprovalsReviewer     *ApprovalsReviewer     `json:"approvalsReviewer"`
	BaseInstructions      *string                `json:"baseInstructions"`
	Config                map[string]interface{} `json:"config"`
	Cwd                   *string                `json:"cwd"`
	DeveloperInstructions *string                `json:"developerInstructions"`
	Ephemeral             *bool                  `json:"ephemeral"`
	Model                 *string                `json:"model"`
	ModelProvider         *string                `json:"modelProvider"`
	Personality           *Personality           `json:"personality"`
	Sandbox               *SandboxMode           `json:"sandbox"`
	ServiceName           *string                `json:"serviceName"`
	ServiceTier           *ServiceTier           `json:"serviceTier"`
	SessionStartSource    *ThreadStartSource     `json:"sessionStartSource"`
}

type PurpleGranularAskForApproval struct {
	Granular PurpleGranular `json:"granular"`
}

type PurpleGranular struct {
	MCPElicitations    bool  `json:"mcp_elicitations"`
	RequestPermissions *bool `json:"request_permissions,omitempty"`
	Rules              bool  `json:"rules"`
	SandboxApproval    bool  `json:"sandbox_approval"`
	SkillApproval      *bool `json:"skill_approval,omitempty"`
}

type ThreadStartResponse struct {
	ApprovalPolicy *AskForApproval `json:"approvalPolicy"`
	// Reviewer currently used for approval requests on this thread.
	ApprovalsReviewer ApprovalsReviewer         `json:"approvalsReviewer"`
	Cwd               string                    `json:"cwd"`
	Model             string                    `json:"model"`
	ModelProvider     string                    `json:"modelProvider"`
	ReasoningEffort   *ReasoningEffort          `json:"reasoningEffort"`
	Sandbox           SandboxPolicy             `json:"sandbox"`
	ServiceTier       *ServiceTier              `json:"serviceTier"`
	Thread            ThreadStartResponseThread `json:"thread"`
}

type AskForApprovalGranularAskForApproval struct {
	Granular FluffyGranular `json:"granular"`
}

type FluffyGranular struct {
	MCPElicitations    bool  `json:"mcp_elicitations"`
	RequestPermissions *bool `json:"request_permissions,omitempty"`
	Rules              bool  `json:"rules"`
	SandboxApproval    bool  `json:"sandbox_approval"`
	SkillApproval      *bool `json:"skill_approval,omitempty"`
}

type SandboxPolicy struct {
	Type                SandboxPolicyType            `json:"type"`
	Access              *SandboxPolicyReadOnlyAccess `json:"access,omitempty"`
	NetworkAccess       *NetworkAccessUnion          `json:"networkAccess"`
	ExcludeSlashTmp     *bool                        `json:"excludeSlashTmp,omitempty"`
	ExcludeTmpdirEnvVar *bool                        `json:"excludeTmpdirEnvVar,omitempty"`
	ReadOnlyAccess      *SandboxPolicyReadOnlyAccess `json:"readOnlyAccess,omitempty"`
	WritableRoots       []string                     `json:"writableRoots,omitempty"`
}

type SandboxPolicyReadOnlyAccess struct {
	IncludePlatformDefaults *bool              `json:"includePlatformDefaults,omitempty"`
	ReadableRoots           []string           `json:"readableRoots,omitempty"`
	Type                    ReadOnlyAccessType `json:"type"`
}

type ThreadStartResponseThread struct {
	// Optional random unique nickname assigned to an AgentControl-spawned sub-agent.
	AgentNickname *string `json:"agentNickname"`
	// Optional role (agent_role) assigned to an AgentControl-spawned sub-agent.
	AgentRole *string `json:"agentRole"`
	// Version of the CLI that created the thread.
	CLIVersion string `json:"cliVersion"`
	// Unix timestamp (in seconds) when the thread was created.
	CreatedAt int64 `json:"createdAt"`
	// Working directory captured for the thread.
	Cwd string `json:"cwd"`
	// Whether the thread is ephemeral and should not be materialized on disk.
	Ephemeral bool `json:"ephemeral"`
	// Source thread id when this thread was created by forking another thread.
	ForkedFromID *string `json:"forkedFromId"`
	// Optional Git metadata captured when the thread was created.
	GitInfo *PurpleGitInfo `json:"gitInfo"`
	ID      string         `json:"id"`
	// Model provider used for this thread (for example, 'openai').
	ModelProvider string `json:"modelProvider"`
	// Optional user-facing thread title.
	Name *string `json:"name"`
	// [UNSTABLE] Path to the thread on disk.
	Path *string `json:"path"`
	// Usually the first user message in the thread, if available.
	Preview string `json:"preview"`
	// Origin of the thread (CLI, VSCode, codex exec, codex app-server, etc.).
	Source *TentacledSessionSource `json:"source"`
	// Current runtime status for the thread.
	Status PurpleThreadStatus `json:"status"`
	// Only populated on `thread/resume`, `thread/rollback`, `thread/fork`, and `thread/read`
	// (when `includeTurns` is true) responses. For all other responses and notifications
	// returning a Thread, the turns field will be an empty list.
	Turns []PurpleTurn `json:"turns"`
	// Unix timestamp (in seconds) when the thread was last updated.
	UpdatedAt int64 `json:"updatedAt"`
}

type PurpleGitInfo struct {
	Branch    *string `json:"branch"`
	OriginURL *string `json:"originUrl"`
	SHA       *string `json:"sha"`
}

type PurpleSessionSource struct {
	Custom   *string                  `json:"custom,omitempty"`
	SubAgent *TentacledSubAgentSource `json:"subAgent"`
}

type PurpleSubAgentSource struct {
	ThreadSpawn *PurpleThreadSpawn `json:"thread_spawn,omitempty"`
	Other       *string            `json:"other,omitempty"`
}

type PurpleThreadSpawn struct {
	AgentNickname  *string `json:"agent_nickname"`
	AgentPath      *string `json:"agent_path"`
	AgentRole      *string `json:"agent_role"`
	Depth          int64   `json:"depth"`
	ParentThreadID string  `json:"parent_thread_id"`
}

// Current runtime status for the thread.
type PurpleThreadStatus struct {
	Type        ThreadStatusType   `json:"type"`
	ActiveFlags []ThreadActiveFlag `json:"activeFlags,omitempty"`
}

type PurpleTurn struct {
	// Unix timestamp (in seconds) when the turn completed.
	CompletedAt *int64 `json:"completedAt"`
	// Duration between turn start and completion in milliseconds, if known.
	DurationMS *int64 `json:"durationMs"`
	// Only populated when the Turn's status is failed.
	Error *PurpleTurnError `json:"error"`
	ID    string           `json:"id"`
	// Only populated on a `thread/resume` or `thread/fork` response. For all other responses
	// and notifications returning a Turn, the items field will be an empty list.
	Items []PurpleThreadItem `json:"items"`
	// Unix timestamp (in seconds) when the turn started.
	StartedAt *int64     `json:"startedAt"`
	Status    TurnStatus `json:"status"`
}

type PurpleTurnError struct {
	AdditionalDetails *string                 `json:"additionalDetails"`
	CodexErrorInfo    *IndecentCodexErrorInfo `json:"codexErrorInfo"`
	Message           string                  `json:"message"`
}

// Failed to connect to the response SSE stream.
//
// The response SSE stream disconnected in the middle of a turn before completion.
//
// Reached the retry limit for responses.
//
// Returned when `turn/start` or `turn/steer` is submitted while the current active turn
// cannot accept same-turn steering, for example `/review` or manual `/compact`.
type PurpleCodexErrorInfo struct {
	HTTPConnectionFailed           *PurpleHTTPConnectionFailed           `json:"httpConnectionFailed,omitempty"`
	ResponseStreamConnectionFailed *PurpleResponseStreamConnectionFailed `json:"responseStreamConnectionFailed,omitempty"`
	ResponseStreamDisconnected     *PurpleResponseStreamDisconnected     `json:"responseStreamDisconnected,omitempty"`
	ResponseTooManyFailedAttempts  *PurpleResponseTooManyFailedAttempts  `json:"responseTooManyFailedAttempts,omitempty"`
	ActiveTurnNotSteerable         *PurpleActiveTurnNotSteerable         `json:"activeTurnNotSteerable,omitempty"`
}

type PurpleActiveTurnNotSteerable struct {
	TurnKind NonSteerableTurnKind `json:"turnKind"`
}

type PurpleHTTPConnectionFailed struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type PurpleResponseStreamConnectionFailed struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type PurpleResponseStreamDisconnected struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type PurpleResponseTooManyFailedAttempts struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

// EXPERIMENTAL - proposed plan item content. The completed plan item is authoritative and
// may not match the concatenation of `PlanDelta` text.
type PurpleThreadItem struct {
	Content []PurpleContent `json:"content,omitempty"`
	// Unique identifier for this collab tool call.
	ID             string                     `json:"id"`
	Type           ThreadItemType             `json:"type"`
	Fragments      []PurpleHookPromptFragment `json:"fragments,omitempty"`
	MemoryCitation *PurpleMemoryCitation      `json:"memoryCitation"`
	Phase          *MessagePhase              `json:"phase"`
	Text           *string                    `json:"text,omitempty"`
	Summary        []string                   `json:"summary,omitempty"`
	// The command's output, aggregated from stdout and stderr.
	AggregatedOutput *string `json:"aggregatedOutput"`
	// The command to be executed.
	Command *string `json:"command,omitempty"`
	// A best-effort parsing of the command to understand the action(s) it will perform. This
	// returns a list of CommandAction objects because a single shell command may be composed of
	// many commands piped together.
	CommandActions []PurpleCommandAction `json:"commandActions,omitempty"`
	// The command's working directory.
	Cwd *string `json:"cwd,omitempty"`
	// The duration of the command execution in milliseconds.
	//
	// The duration of the MCP tool call in milliseconds.
	//
	// The duration of the dynamic tool call in milliseconds.
	DurationMS *int64 `json:"durationMs"`
	// The command's exit code.
	ExitCode *int64 `json:"exitCode"`
	// Identifier for the underlying PTY process (when available).
	ProcessID *string                 `json:"processId"`
	Source    *CommandExecutionSource `json:"source,omitempty"`
	// Current status of the collab tool call.
	Status    *string                  `json:"status,omitempty"`
	Changes   []PurpleFileUpdateChange `json:"changes,omitempty"`
	Arguments interface{}              `json:"arguments"`
	Error     *PurpleMCPToolCallError  `json:"error"`
	Result    *PurpleResult            `json:"result"`
	Server    *string                  `json:"server,omitempty"`
	// Name of the collab tool that was invoked.
	Tool         *string                                  `json:"tool,omitempty"`
	ContentItems []PurpleDynamicToolCallOutputContentItem `json:"contentItems"`
	Success      *bool                                    `json:"success"`
	// Last known status of the target agents, when available.
	AgentsStates map[string]PurpleCollabAgentState `json:"agentsStates,omitempty"`
	// Model requested for the spawned agent, when applicable.
	Model *string `json:"model"`
	// Prompt text sent as part of the collab tool call, when available.
	Prompt *string `json:"prompt"`
	// Reasoning effort requested for the spawned agent, when applicable.
	ReasoningEffort *ReasoningEffort `json:"reasoningEffort"`
	// Thread ID of the receiving agent, when applicable. In case of spawn operation, this
	// corresponds to the newly spawned agent.
	ReceiverThreadIDS []string `json:"receiverThreadIds,omitempty"`
	// Thread ID of the agent issuing the collab request.
	SenderThreadID *string                `json:"senderThreadId,omitempty"`
	Action         *PurpleWebSearchAction `json:"action"`
	Query          *string                `json:"query,omitempty"`
	Path           *string                `json:"path,omitempty"`
	RevisedPrompt  *string                `json:"revisedPrompt"`
	SavedPath      *string                `json:"savedPath"`
	Review         *string                `json:"review,omitempty"`
}

type PurpleWebSearchAction struct {
	Queries []string            `json:"queries"`
	Query   *string             `json:"query"`
	Type    WebSearchActionType `json:"type"`
	URL     *string             `json:"url"`
	Pattern *string             `json:"pattern"`
}

type PurpleCollabAgentState struct {
	Message *string           `json:"message"`
	Status  CollabAgentStatus `json:"status"`
}

type PurpleFileUpdateChange struct {
	Diff string                `json:"diff"`
	Kind PurplePatchChangeKind `json:"kind"`
	Path string                `json:"path"`
}

type PurplePatchChangeKind struct {
	Type     Type    `json:"type"`
	MovePath *string `json:"move_path"`
}

type PurpleCommandAction struct {
	Command string            `json:"command"`
	Name    *string           `json:"name,omitempty"`
	Path    *string           `json:"path"`
	Type    CommandActionType `json:"type"`
	Query   *string           `json:"query"`
}

type PurpleUserInput struct {
	Text *string `json:"text,omitempty"`
	// UI-defined spans within `text` used to render or persist special elements.
	TextElements []PurpleTextElement `json:"text_elements,omitempty"`
	Type         UserInputType       `json:"type"`
	URL          *string             `json:"url,omitempty"`
	Path         *string             `json:"path,omitempty"`
	Name         *string             `json:"name,omitempty"`
}

type PurpleTextElement struct {
	// Byte range in the parent `text` buffer that this element occupies.
	ByteRange PurpleByteRange `json:"byteRange"`
	// Optional human-readable placeholder for the element, displayed in the UI.
	Placeholder *string `json:"placeholder"`
}

// Byte range in the parent `text` buffer that this element occupies.
type PurpleByteRange struct {
	End   int64 `json:"end"`
	Start int64 `json:"start"`
}

type PurpleDynamicToolCallOutputContentItem struct {
	Text     *string                                   `json:"text,omitempty"`
	Type     InputDynamicToolCallOutputContentItemType `json:"type"`
	ImageURL *string                                   `json:"imageUrl,omitempty"`
}

type PurpleMCPToolCallError struct {
	Message string `json:"message"`
}

type PurpleHookPromptFragment struct {
	HookRunID string `json:"hookRunId"`
	Text      string `json:"text"`
}

type PurpleMemoryCitation struct {
	Entries   []PurpleMemoryCitationEntry `json:"entries"`
	ThreadIDS []string                    `json:"threadIds"`
}

type PurpleMemoryCitationEntry struct {
	LineEnd   int64  `json:"lineEnd"`
	LineStart int64  `json:"lineStart"`
	Note      string `json:"note"`
	Path      string `json:"path"`
}

type PurpleMCPToolCallResult struct {
	Meta              interface{}   `json:"_meta"`
	Content           []interface{} `json:"content"`
	StructuredContent interface{}   `json:"structuredContent"`
}

type TurnStartParams struct {
	// Override the approval policy for this turn and subsequent turns.
	ApprovalPolicy *TurnStartParamsApprovalPolicy `json:"approvalPolicy"`
	// Override where approval requests are routed for review on this turn and subsequent turns.
	ApprovalsReviewer *ApprovalsReviewer `json:"approvalsReviewer"`
	// Override the working directory for this turn and subsequent turns.
	Cwd *string `json:"cwd"`
	// Override the reasoning effort for this turn and subsequent turns.
	Effort *ReasoningEffort   `json:"effort"`
	Input  []UserInputElement `json:"input"`
	// Override the model for this turn and subsequent turns.
	Model *string `json:"model"`
	// Optional JSON Schema used to constrain the final assistant message for this turn.
	OutputSchema interface{} `json:"outputSchema"`
	// Override the personality for this turn and subsequent turns.
	Personality *Personality `json:"personality"`
	// Override the sandbox policy for this turn and subsequent turns.
	SandboxPolicy *DangerFullAccessSandboxPolicyClass `json:"sandboxPolicy"`
	// Override the service tier for this turn and subsequent turns.
	ServiceTier *ServiceTier `json:"serviceTier"`
	// Override the reasoning summary for this turn and subsequent turns.
	Summary  *ReasoningSummary `json:"summary"`
	ThreadID string            `json:"threadId"`
}

type FluffyGranularAskForApproval struct {
	Granular TentacledGranular `json:"granular"`
}

type TentacledGranular struct {
	MCPElicitations    bool  `json:"mcp_elicitations"`
	RequestPermissions *bool `json:"request_permissions,omitempty"`
	Rules              bool  `json:"rules"`
	SandboxApproval    bool  `json:"sandbox_approval"`
	SkillApproval      *bool `json:"skill_approval,omitempty"`
}

type UserInputElement struct {
	Text *string `json:"text,omitempty"`
	// UI-defined spans within `text` used to render or persist special elements.
	TextElements []FluffyTextElement `json:"text_elements,omitempty"`
	Type         UserInputType       `json:"type"`
	URL          *string             `json:"url,omitempty"`
	Path         *string             `json:"path,omitempty"`
	Name         *string             `json:"name,omitempty"`
}

type FluffyTextElement struct {
	// Byte range in the parent `text` buffer that this element occupies.
	ByteRange FluffyByteRange `json:"byteRange"`
	// Optional human-readable placeholder for the element, displayed in the UI.
	Placeholder *string `json:"placeholder"`
}

// Byte range in the parent `text` buffer that this element occupies.
type FluffyByteRange struct {
	End   int64 `json:"end"`
	Start int64 `json:"start"`
}

type DangerFullAccessSandboxPolicyClass struct {
	Type                SandboxPolicyType                            `json:"type"`
	Access              *DangerFullAccessSandboxPolicyReadOnlyAccess `json:"access,omitempty"`
	NetworkAccess       *NetworkAccessUnion                          `json:"networkAccess"`
	ExcludeSlashTmp     *bool                                        `json:"excludeSlashTmp,omitempty"`
	ExcludeTmpdirEnvVar *bool                                        `json:"excludeTmpdirEnvVar,omitempty"`
	ReadOnlyAccess      *DangerFullAccessSandboxPolicyReadOnlyAccess `json:"readOnlyAccess,omitempty"`
	WritableRoots       []string                                     `json:"writableRoots,omitempty"`
}

type DangerFullAccessSandboxPolicyReadOnlyAccess struct {
	IncludePlatformDefaults *bool              `json:"includePlatformDefaults,omitempty"`
	ReadableRoots           []string           `json:"readableRoots,omitempty"`
	Type                    ReadOnlyAccessType `json:"type"`
}

type TurnStartResponse struct {
	Turn TurnStartResponseTurn `json:"turn"`
}

type TurnStartResponseTurn struct {
	// Unix timestamp (in seconds) when the turn completed.
	CompletedAt *int64 `json:"completedAt"`
	// Duration between turn start and completion in milliseconds, if known.
	DurationMS *int64 `json:"durationMs"`
	// Only populated when the Turn's status is failed.
	Error *FluffyTurnError `json:"error"`
	ID    string           `json:"id"`
	// Only populated on a `thread/resume` or `thread/fork` response. For all other responses
	// and notifications returning a Turn, the items field will be an empty list.
	Items []FluffyThreadItem `json:"items"`
	// Unix timestamp (in seconds) when the turn started.
	StartedAt *int64     `json:"startedAt"`
	Status    TurnStatus `json:"status"`
}

type FluffyTurnError struct {
	AdditionalDetails *string                  `json:"additionalDetails"`
	CodexErrorInfo    *HilariousCodexErrorInfo `json:"codexErrorInfo"`
	Message           string                   `json:"message"`
}

// Failed to connect to the response SSE stream.
//
// The response SSE stream disconnected in the middle of a turn before completion.
//
// Reached the retry limit for responses.
//
// Returned when `turn/start` or `turn/steer` is submitted while the current active turn
// cannot accept same-turn steering, for example `/review` or manual `/compact`.
type FluffyCodexErrorInfo struct {
	HTTPConnectionFailed           *FluffyHTTPConnectionFailed           `json:"httpConnectionFailed,omitempty"`
	ResponseStreamConnectionFailed *FluffyResponseStreamConnectionFailed `json:"responseStreamConnectionFailed,omitempty"`
	ResponseStreamDisconnected     *FluffyResponseStreamDisconnected     `json:"responseStreamDisconnected,omitempty"`
	ResponseTooManyFailedAttempts  *FluffyResponseTooManyFailedAttempts  `json:"responseTooManyFailedAttempts,omitempty"`
	ActiveTurnNotSteerable         *FluffyActiveTurnNotSteerable         `json:"activeTurnNotSteerable,omitempty"`
}

type FluffyActiveTurnNotSteerable struct {
	TurnKind NonSteerableTurnKind `json:"turnKind"`
}

type FluffyHTTPConnectionFailed struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type FluffyResponseStreamConnectionFailed struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type FluffyResponseStreamDisconnected struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type FluffyResponseTooManyFailedAttempts struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

// EXPERIMENTAL - proposed plan item content. The completed plan item is authoritative and
// may not match the concatenation of `PlanDelta` text.
type FluffyThreadItem struct {
	Content []FluffyContent `json:"content,omitempty"`
	// Unique identifier for this collab tool call.
	ID             string                     `json:"id"`
	Type           ThreadItemType             `json:"type"`
	Fragments      []FluffyHookPromptFragment `json:"fragments,omitempty"`
	MemoryCitation *FluffyMemoryCitation      `json:"memoryCitation"`
	Phase          *MessagePhase              `json:"phase"`
	Text           *string                    `json:"text,omitempty"`
	Summary        []string                   `json:"summary,omitempty"`
	// The command's output, aggregated from stdout and stderr.
	AggregatedOutput *string `json:"aggregatedOutput"`
	// The command to be executed.
	Command *string `json:"command,omitempty"`
	// A best-effort parsing of the command to understand the action(s) it will perform. This
	// returns a list of CommandAction objects because a single shell command may be composed of
	// many commands piped together.
	CommandActions []FluffyCommandAction `json:"commandActions,omitempty"`
	// The command's working directory.
	Cwd *string `json:"cwd,omitempty"`
	// The duration of the command execution in milliseconds.
	//
	// The duration of the MCP tool call in milliseconds.
	//
	// The duration of the dynamic tool call in milliseconds.
	DurationMS *int64 `json:"durationMs"`
	// The command's exit code.
	ExitCode *int64 `json:"exitCode"`
	// Identifier for the underlying PTY process (when available).
	ProcessID *string                 `json:"processId"`
	Source    *CommandExecutionSource `json:"source,omitempty"`
	// Current status of the collab tool call.
	Status    *string                  `json:"status,omitempty"`
	Changes   []FluffyFileUpdateChange `json:"changes,omitempty"`
	Arguments interface{}              `json:"arguments"`
	Error     *FluffyMCPToolCallError  `json:"error"`
	Result    *FluffyResult            `json:"result"`
	Server    *string                  `json:"server,omitempty"`
	// Name of the collab tool that was invoked.
	Tool         *string                                  `json:"tool,omitempty"`
	ContentItems []FluffyDynamicToolCallOutputContentItem `json:"contentItems"`
	Success      *bool                                    `json:"success"`
	// Last known status of the target agents, when available.
	AgentsStates map[string]FluffyCollabAgentState `json:"agentsStates,omitempty"`
	// Model requested for the spawned agent, when applicable.
	Model *string `json:"model"`
	// Prompt text sent as part of the collab tool call, when available.
	Prompt *string `json:"prompt"`
	// Reasoning effort requested for the spawned agent, when applicable.
	ReasoningEffort *ReasoningEffort `json:"reasoningEffort"`
	// Thread ID of the receiving agent, when applicable. In case of spawn operation, this
	// corresponds to the newly spawned agent.
	ReceiverThreadIDS []string `json:"receiverThreadIds,omitempty"`
	// Thread ID of the agent issuing the collab request.
	SenderThreadID *string                `json:"senderThreadId,omitempty"`
	Action         *FluffyWebSearchAction `json:"action"`
	Query          *string                `json:"query,omitempty"`
	Path           *string                `json:"path,omitempty"`
	RevisedPrompt  *string                `json:"revisedPrompt"`
	SavedPath      *string                `json:"savedPath"`
	Review         *string                `json:"review,omitempty"`
}

type FluffyWebSearchAction struct {
	Queries []string            `json:"queries"`
	Query   *string             `json:"query"`
	Type    WebSearchActionType `json:"type"`
	URL     *string             `json:"url"`
	Pattern *string             `json:"pattern"`
}

type FluffyCollabAgentState struct {
	Message *string           `json:"message"`
	Status  CollabAgentStatus `json:"status"`
}

type FluffyFileUpdateChange struct {
	Diff string                `json:"diff"`
	Kind FluffyPatchChangeKind `json:"kind"`
	Path string                `json:"path"`
}

type FluffyPatchChangeKind struct {
	Type     Type    `json:"type"`
	MovePath *string `json:"move_path"`
}

type FluffyCommandAction struct {
	Command string            `json:"command"`
	Name    *string           `json:"name,omitempty"`
	Path    *string           `json:"path"`
	Type    CommandActionType `json:"type"`
	Query   *string           `json:"query"`
}

type FluffyUserInput struct {
	Text *string `json:"text,omitempty"`
	// UI-defined spans within `text` used to render or persist special elements.
	TextElements []TentacledTextElement `json:"text_elements,omitempty"`
	Type         UserInputType          `json:"type"`
	URL          *string                `json:"url,omitempty"`
	Path         *string                `json:"path,omitempty"`
	Name         *string                `json:"name,omitempty"`
}

type TentacledTextElement struct {
	// Byte range in the parent `text` buffer that this element occupies.
	ByteRange TentacledByteRange `json:"byteRange"`
	// Optional human-readable placeholder for the element, displayed in the UI.
	Placeholder *string `json:"placeholder"`
}

// Byte range in the parent `text` buffer that this element occupies.
type TentacledByteRange struct {
	End   int64 `json:"end"`
	Start int64 `json:"start"`
}

type FluffyDynamicToolCallOutputContentItem struct {
	Text     *string                                   `json:"text,omitempty"`
	Type     InputDynamicToolCallOutputContentItemType `json:"type"`
	ImageURL *string                                   `json:"imageUrl,omitempty"`
}

type FluffyMCPToolCallError struct {
	Message string `json:"message"`
}

type FluffyHookPromptFragment struct {
	HookRunID string `json:"hookRunId"`
	Text      string `json:"text"`
}

type FluffyMemoryCitation struct {
	Entries   []FluffyMemoryCitationEntry `json:"entries"`
	ThreadIDS []string                    `json:"threadIds"`
}

type FluffyMemoryCitationEntry struct {
	LineEnd   int64  `json:"lineEnd"`
	LineStart int64  `json:"lineStart"`
	Note      string `json:"note"`
	Path      string `json:"path"`
}

type FluffyMCPToolCallResult struct {
	Meta              interface{}   `json:"_meta"`
	Content           []interface{} `json:"content"`
	StructuredContent interface{}   `json:"structuredContent"`
}

type ThreadStartedNotification struct {
	Thread ThreadStartedNotificationThread `json:"thread"`
}

type ThreadStartedNotificationThread struct {
	// Optional random unique nickname assigned to an AgentControl-spawned sub-agent.
	AgentNickname *string `json:"agentNickname"`
	// Optional role (agent_role) assigned to an AgentControl-spawned sub-agent.
	AgentRole *string `json:"agentRole"`
	// Version of the CLI that created the thread.
	CLIVersion string `json:"cliVersion"`
	// Unix timestamp (in seconds) when the thread was created.
	CreatedAt int64 `json:"createdAt"`
	// Working directory captured for the thread.
	Cwd string `json:"cwd"`
	// Whether the thread is ephemeral and should not be materialized on disk.
	Ephemeral bool `json:"ephemeral"`
	// Source thread id when this thread was created by forking another thread.
	ForkedFromID *string `json:"forkedFromId"`
	// Optional Git metadata captured when the thread was created.
	GitInfo *FluffyGitInfo `json:"gitInfo"`
	ID      string         `json:"id"`
	// Model provider used for this thread (for example, 'openai').
	ModelProvider string `json:"modelProvider"`
	// Optional user-facing thread title.
	Name *string `json:"name"`
	// [UNSTABLE] Path to the thread on disk.
	Path *string `json:"path"`
	// Usually the first user message in the thread, if available.
	Preview string `json:"preview"`
	// Origin of the thread (CLI, VSCode, codex exec, codex app-server, etc.).
	Source *StickySessionSource `json:"source"`
	// Current runtime status for the thread.
	Status FluffyThreadStatus `json:"status"`
	// Only populated on `thread/resume`, `thread/rollback`, `thread/fork`, and `thread/read`
	// (when `includeTurns` is true) responses. For all other responses and notifications
	// returning a Thread, the turns field will be an empty list.
	Turns []FluffyTurn `json:"turns"`
	// Unix timestamp (in seconds) when the thread was last updated.
	UpdatedAt int64 `json:"updatedAt"`
}

type FluffyGitInfo struct {
	Branch    *string `json:"branch"`
	OriginURL *string `json:"originUrl"`
	SHA       *string `json:"sha"`
}

type FluffySessionSource struct {
	Custom   *string               `json:"custom,omitempty"`
	SubAgent *StickySubAgentSource `json:"subAgent"`
}

type FluffySubAgentSource struct {
	ThreadSpawn *FluffyThreadSpawn `json:"thread_spawn,omitempty"`
	Other       *string            `json:"other,omitempty"`
}

type FluffyThreadSpawn struct {
	AgentNickname  *string `json:"agent_nickname"`
	AgentPath      *string `json:"agent_path"`
	AgentRole      *string `json:"agent_role"`
	Depth          int64   `json:"depth"`
	ParentThreadID string  `json:"parent_thread_id"`
}

// Current runtime status for the thread.
type FluffyThreadStatus struct {
	Type        ThreadStatusType   `json:"type"`
	ActiveFlags []ThreadActiveFlag `json:"activeFlags,omitempty"`
}

type FluffyTurn struct {
	// Unix timestamp (in seconds) when the turn completed.
	CompletedAt *int64 `json:"completedAt"`
	// Duration between turn start and completion in milliseconds, if known.
	DurationMS *int64 `json:"durationMs"`
	// Only populated when the Turn's status is failed.
	Error *TentacledTurnError `json:"error"`
	ID    string              `json:"id"`
	// Only populated on a `thread/resume` or `thread/fork` response. For all other responses
	// and notifications returning a Turn, the items field will be an empty list.
	Items []TentacledThreadItem `json:"items"`
	// Unix timestamp (in seconds) when the turn started.
	StartedAt *int64     `json:"startedAt"`
	Status    TurnStatus `json:"status"`
}

type TentacledTurnError struct {
	AdditionalDetails *string                  `json:"additionalDetails"`
	CodexErrorInfo    *AmbitiousCodexErrorInfo `json:"codexErrorInfo"`
	Message           string                   `json:"message"`
}

// Failed to connect to the response SSE stream.
//
// The response SSE stream disconnected in the middle of a turn before completion.
//
// Reached the retry limit for responses.
//
// Returned when `turn/start` or `turn/steer` is submitted while the current active turn
// cannot accept same-turn steering, for example `/review` or manual `/compact`.
type TentacledCodexErrorInfo struct {
	HTTPConnectionFailed           *TentacledHTTPConnectionFailed           `json:"httpConnectionFailed,omitempty"`
	ResponseStreamConnectionFailed *TentacledResponseStreamConnectionFailed `json:"responseStreamConnectionFailed,omitempty"`
	ResponseStreamDisconnected     *TentacledResponseStreamDisconnected     `json:"responseStreamDisconnected,omitempty"`
	ResponseTooManyFailedAttempts  *TentacledResponseTooManyFailedAttempts  `json:"responseTooManyFailedAttempts,omitempty"`
	ActiveTurnNotSteerable         *TentacledActiveTurnNotSteerable         `json:"activeTurnNotSteerable,omitempty"`
}

type TentacledActiveTurnNotSteerable struct {
	TurnKind NonSteerableTurnKind `json:"turnKind"`
}

type TentacledHTTPConnectionFailed struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type TentacledResponseStreamConnectionFailed struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type TentacledResponseStreamDisconnected struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type TentacledResponseTooManyFailedAttempts struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

// EXPERIMENTAL - proposed plan item content. The completed plan item is authoritative and
// may not match the concatenation of `PlanDelta` text.
type TentacledThreadItem struct {
	Content []TentacledContent `json:"content,omitempty"`
	// Unique identifier for this collab tool call.
	ID             string                        `json:"id"`
	Type           ThreadItemType                `json:"type"`
	Fragments      []TentacledHookPromptFragment `json:"fragments,omitempty"`
	MemoryCitation *TentacledMemoryCitation      `json:"memoryCitation"`
	Phase          *MessagePhase                 `json:"phase"`
	Text           *string                       `json:"text,omitempty"`
	Summary        []string                      `json:"summary,omitempty"`
	// The command's output, aggregated from stdout and stderr.
	AggregatedOutput *string `json:"aggregatedOutput"`
	// The command to be executed.
	Command *string `json:"command,omitempty"`
	// A best-effort parsing of the command to understand the action(s) it will perform. This
	// returns a list of CommandAction objects because a single shell command may be composed of
	// many commands piped together.
	CommandActions []TentacledCommandAction `json:"commandActions,omitempty"`
	// The command's working directory.
	Cwd *string `json:"cwd,omitempty"`
	// The duration of the command execution in milliseconds.
	//
	// The duration of the MCP tool call in milliseconds.
	//
	// The duration of the dynamic tool call in milliseconds.
	DurationMS *int64 `json:"durationMs"`
	// The command's exit code.
	ExitCode *int64 `json:"exitCode"`
	// Identifier for the underlying PTY process (when available).
	ProcessID *string                 `json:"processId"`
	Source    *CommandExecutionSource `json:"source,omitempty"`
	// Current status of the collab tool call.
	Status    *string                     `json:"status,omitempty"`
	Changes   []TentacledFileUpdateChange `json:"changes,omitempty"`
	Arguments interface{}                 `json:"arguments"`
	Error     *TentacledMCPToolCallError  `json:"error"`
	Result    *TentacledResult            `json:"result"`
	Server    *string                     `json:"server,omitempty"`
	// Name of the collab tool that was invoked.
	Tool         *string                                     `json:"tool,omitempty"`
	ContentItems []TentacledDynamicToolCallOutputContentItem `json:"contentItems"`
	Success      *bool                                       `json:"success"`
	// Last known status of the target agents, when available.
	AgentsStates map[string]TentacledCollabAgentState `json:"agentsStates,omitempty"`
	// Model requested for the spawned agent, when applicable.
	Model *string `json:"model"`
	// Prompt text sent as part of the collab tool call, when available.
	Prompt *string `json:"prompt"`
	// Reasoning effort requested for the spawned agent, when applicable.
	ReasoningEffort *ReasoningEffort `json:"reasoningEffort"`
	// Thread ID of the receiving agent, when applicable. In case of spawn operation, this
	// corresponds to the newly spawned agent.
	ReceiverThreadIDS []string `json:"receiverThreadIds,omitempty"`
	// Thread ID of the agent issuing the collab request.
	SenderThreadID *string                   `json:"senderThreadId,omitempty"`
	Action         *TentacledWebSearchAction `json:"action"`
	Query          *string                   `json:"query,omitempty"`
	Path           *string                   `json:"path,omitempty"`
	RevisedPrompt  *string                   `json:"revisedPrompt"`
	SavedPath      *string                   `json:"savedPath"`
	Review         *string                   `json:"review,omitempty"`
}

type TentacledWebSearchAction struct {
	Queries []string            `json:"queries"`
	Query   *string             `json:"query"`
	Type    WebSearchActionType `json:"type"`
	URL     *string             `json:"url"`
	Pattern *string             `json:"pattern"`
}

type TentacledCollabAgentState struct {
	Message *string           `json:"message"`
	Status  CollabAgentStatus `json:"status"`
}

type TentacledFileUpdateChange struct {
	Diff string                   `json:"diff"`
	Kind TentacledPatchChangeKind `json:"kind"`
	Path string                   `json:"path"`
}

type TentacledPatchChangeKind struct {
	Type     Type    `json:"type"`
	MovePath *string `json:"move_path"`
}

type TentacledCommandAction struct {
	Command string            `json:"command"`
	Name    *string           `json:"name,omitempty"`
	Path    *string           `json:"path"`
	Type    CommandActionType `json:"type"`
	Query   *string           `json:"query"`
}

type TentacledUserInput struct {
	Text *string `json:"text,omitempty"`
	// UI-defined spans within `text` used to render or persist special elements.
	TextElements []StickyTextElement `json:"text_elements,omitempty"`
	Type         UserInputType       `json:"type"`
	URL          *string             `json:"url,omitempty"`
	Path         *string             `json:"path,omitempty"`
	Name         *string             `json:"name,omitempty"`
}

type StickyTextElement struct {
	// Byte range in the parent `text` buffer that this element occupies.
	ByteRange StickyByteRange `json:"byteRange"`
	// Optional human-readable placeholder for the element, displayed in the UI.
	Placeholder *string `json:"placeholder"`
}

// Byte range in the parent `text` buffer that this element occupies.
type StickyByteRange struct {
	End   int64 `json:"end"`
	Start int64 `json:"start"`
}

type TentacledDynamicToolCallOutputContentItem struct {
	Text     *string                                   `json:"text,omitempty"`
	Type     InputDynamicToolCallOutputContentItemType `json:"type"`
	ImageURL *string                                   `json:"imageUrl,omitempty"`
}

type TentacledMCPToolCallError struct {
	Message string `json:"message"`
}

type TentacledHookPromptFragment struct {
	HookRunID string `json:"hookRunId"`
	Text      string `json:"text"`
}

type TentacledMemoryCitation struct {
	Entries   []TentacledMemoryCitationEntry `json:"entries"`
	ThreadIDS []string                       `json:"threadIds"`
}

type TentacledMemoryCitationEntry struct {
	LineEnd   int64  `json:"lineEnd"`
	LineStart int64  `json:"lineStart"`
	Note      string `json:"note"`
	Path      string `json:"path"`
}

type TentacledMCPToolCallResult struct {
	Meta              interface{}   `json:"_meta"`
	Content           []interface{} `json:"content"`
	StructuredContent interface{}   `json:"structuredContent"`
}

type TurnStartedNotification struct {
	ThreadID string                      `json:"threadId"`
	Turn     TurnStartedNotificationTurn `json:"turn"`
}

type TurnStartedNotificationTurn struct {
	// Unix timestamp (in seconds) when the turn completed.
	CompletedAt *int64 `json:"completedAt"`
	// Duration between turn start and completion in milliseconds, if known.
	DurationMS *int64 `json:"durationMs"`
	// Only populated when the Turn's status is failed.
	Error *StickyTurnError `json:"error"`
	ID    string           `json:"id"`
	// Only populated on a `thread/resume` or `thread/fork` response. For all other responses
	// and notifications returning a Turn, the items field will be an empty list.
	Items []StickyThreadItem `json:"items"`
	// Unix timestamp (in seconds) when the turn started.
	StartedAt *int64     `json:"startedAt"`
	Status    TurnStatus `json:"status"`
}

type StickyTurnError struct {
	AdditionalDetails *string                `json:"additionalDetails"`
	CodexErrorInfo    *CunningCodexErrorInfo `json:"codexErrorInfo"`
	Message           string                 `json:"message"`
}

// Failed to connect to the response SSE stream.
//
// The response SSE stream disconnected in the middle of a turn before completion.
//
// Reached the retry limit for responses.
//
// Returned when `turn/start` or `turn/steer` is submitted while the current active turn
// cannot accept same-turn steering, for example `/review` or manual `/compact`.
type StickyCodexErrorInfo struct {
	HTTPConnectionFailed           *StickyHTTPConnectionFailed           `json:"httpConnectionFailed,omitempty"`
	ResponseStreamConnectionFailed *StickyResponseStreamConnectionFailed `json:"responseStreamConnectionFailed,omitempty"`
	ResponseStreamDisconnected     *StickyResponseStreamDisconnected     `json:"responseStreamDisconnected,omitempty"`
	ResponseTooManyFailedAttempts  *StickyResponseTooManyFailedAttempts  `json:"responseTooManyFailedAttempts,omitempty"`
	ActiveTurnNotSteerable         *StickyActiveTurnNotSteerable         `json:"activeTurnNotSteerable,omitempty"`
}

type StickyActiveTurnNotSteerable struct {
	TurnKind NonSteerableTurnKind `json:"turnKind"`
}

type StickyHTTPConnectionFailed struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type StickyResponseStreamConnectionFailed struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type StickyResponseStreamDisconnected struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type StickyResponseTooManyFailedAttempts struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

// EXPERIMENTAL - proposed plan item content. The completed plan item is authoritative and
// may not match the concatenation of `PlanDelta` text.
type StickyThreadItem struct {
	Content []StickyContent `json:"content,omitempty"`
	// Unique identifier for this collab tool call.
	ID             string                     `json:"id"`
	Type           ThreadItemType             `json:"type"`
	Fragments      []StickyHookPromptFragment `json:"fragments,omitempty"`
	MemoryCitation *StickyMemoryCitation      `json:"memoryCitation"`
	Phase          *MessagePhase              `json:"phase"`
	Text           *string                    `json:"text,omitempty"`
	Summary        []string                   `json:"summary,omitempty"`
	// The command's output, aggregated from stdout and stderr.
	AggregatedOutput *string `json:"aggregatedOutput"`
	// The command to be executed.
	Command *string `json:"command,omitempty"`
	// A best-effort parsing of the command to understand the action(s) it will perform. This
	// returns a list of CommandAction objects because a single shell command may be composed of
	// many commands piped together.
	CommandActions []StickyCommandAction `json:"commandActions,omitempty"`
	// The command's working directory.
	Cwd *string `json:"cwd,omitempty"`
	// The duration of the command execution in milliseconds.
	//
	// The duration of the MCP tool call in milliseconds.
	//
	// The duration of the dynamic tool call in milliseconds.
	DurationMS *int64 `json:"durationMs"`
	// The command's exit code.
	ExitCode *int64 `json:"exitCode"`
	// Identifier for the underlying PTY process (when available).
	ProcessID *string                 `json:"processId"`
	Source    *CommandExecutionSource `json:"source,omitempty"`
	// Current status of the collab tool call.
	Status    *string                  `json:"status,omitempty"`
	Changes   []StickyFileUpdateChange `json:"changes,omitempty"`
	Arguments interface{}              `json:"arguments"`
	Error     *StickyMCPToolCallError  `json:"error"`
	Result    *StickyResult            `json:"result"`
	Server    *string                  `json:"server,omitempty"`
	// Name of the collab tool that was invoked.
	Tool         *string                                  `json:"tool,omitempty"`
	ContentItems []StickyDynamicToolCallOutputContentItem `json:"contentItems"`
	Success      *bool                                    `json:"success"`
	// Last known status of the target agents, when available.
	AgentsStates map[string]StickyCollabAgentState `json:"agentsStates,omitempty"`
	// Model requested for the spawned agent, when applicable.
	Model *string `json:"model"`
	// Prompt text sent as part of the collab tool call, when available.
	Prompt *string `json:"prompt"`
	// Reasoning effort requested for the spawned agent, when applicable.
	ReasoningEffort *ReasoningEffort `json:"reasoningEffort"`
	// Thread ID of the receiving agent, when applicable. In case of spawn operation, this
	// corresponds to the newly spawned agent.
	ReceiverThreadIDS []string `json:"receiverThreadIds,omitempty"`
	// Thread ID of the agent issuing the collab request.
	SenderThreadID *string                `json:"senderThreadId,omitempty"`
	Action         *StickyWebSearchAction `json:"action"`
	Query          *string                `json:"query,omitempty"`
	Path           *string                `json:"path,omitempty"`
	RevisedPrompt  *string                `json:"revisedPrompt"`
	SavedPath      *string                `json:"savedPath"`
	Review         *string                `json:"review,omitempty"`
}

type StickyWebSearchAction struct {
	Queries []string            `json:"queries"`
	Query   *string             `json:"query"`
	Type    WebSearchActionType `json:"type"`
	URL     *string             `json:"url"`
	Pattern *string             `json:"pattern"`
}

type StickyCollabAgentState struct {
	Message *string           `json:"message"`
	Status  CollabAgentStatus `json:"status"`
}

type StickyFileUpdateChange struct {
	Diff string                `json:"diff"`
	Kind StickyPatchChangeKind `json:"kind"`
	Path string                `json:"path"`
}

type StickyPatchChangeKind struct {
	Type     Type    `json:"type"`
	MovePath *string `json:"move_path"`
}

type StickyCommandAction struct {
	Command string            `json:"command"`
	Name    *string           `json:"name,omitempty"`
	Path    *string           `json:"path"`
	Type    CommandActionType `json:"type"`
	Query   *string           `json:"query"`
}

type StickyUserInput struct {
	Text *string `json:"text,omitempty"`
	// UI-defined spans within `text` used to render or persist special elements.
	TextElements []IndigoTextElement `json:"text_elements,omitempty"`
	Type         UserInputType       `json:"type"`
	URL          *string             `json:"url,omitempty"`
	Path         *string             `json:"path,omitempty"`
	Name         *string             `json:"name,omitempty"`
}

type IndigoTextElement struct {
	// Byte range in the parent `text` buffer that this element occupies.
	ByteRange IndigoByteRange `json:"byteRange"`
	// Optional human-readable placeholder for the element, displayed in the UI.
	Placeholder *string `json:"placeholder"`
}

// Byte range in the parent `text` buffer that this element occupies.
type IndigoByteRange struct {
	End   int64 `json:"end"`
	Start int64 `json:"start"`
}

type StickyDynamicToolCallOutputContentItem struct {
	Text     *string                                   `json:"text,omitempty"`
	Type     InputDynamicToolCallOutputContentItemType `json:"type"`
	ImageURL *string                                   `json:"imageUrl,omitempty"`
}

type StickyMCPToolCallError struct {
	Message string `json:"message"`
}

type StickyHookPromptFragment struct {
	HookRunID string `json:"hookRunId"`
	Text      string `json:"text"`
}

type StickyMemoryCitation struct {
	Entries   []StickyMemoryCitationEntry `json:"entries"`
	ThreadIDS []string                    `json:"threadIds"`
}

type StickyMemoryCitationEntry struct {
	LineEnd   int64  `json:"lineEnd"`
	LineStart int64  `json:"lineStart"`
	Note      string `json:"note"`
	Path      string `json:"path"`
}

type StickyMCPToolCallResult struct {
	Meta              interface{}   `json:"_meta"`
	Content           []interface{} `json:"content"`
	StructuredContent interface{}   `json:"structuredContent"`
}

type TurnCompletedNotification struct {
	ThreadID string                        `json:"threadId"`
	Turn     TurnCompletedNotificationTurn `json:"turn"`
}

type TurnCompletedNotificationTurn struct {
	// Unix timestamp (in seconds) when the turn completed.
	CompletedAt *int64 `json:"completedAt"`
	// Duration between turn start and completion in milliseconds, if known.
	DurationMS *int64 `json:"durationMs"`
	// Only populated when the Turn's status is failed.
	Error *IndigoTurnError `json:"error"`
	ID    string           `json:"id"`
	// Only populated on a `thread/resume` or `thread/fork` response. For all other responses
	// and notifications returning a Turn, the items field will be an empty list.
	Items []IndigoThreadItem `json:"items"`
	// Unix timestamp (in seconds) when the turn started.
	StartedAt *int64     `json:"startedAt"`
	Status    TurnStatus `json:"status"`
}

type IndigoTurnError struct {
	AdditionalDetails *string                `json:"additionalDetails"`
	CodexErrorInfo    *MagentaCodexErrorInfo `json:"codexErrorInfo"`
	Message           string                 `json:"message"`
}

// Failed to connect to the response SSE stream.
//
// The response SSE stream disconnected in the middle of a turn before completion.
//
// Reached the retry limit for responses.
//
// Returned when `turn/start` or `turn/steer` is submitted while the current active turn
// cannot accept same-turn steering, for example `/review` or manual `/compact`.
type IndigoCodexErrorInfo struct {
	HTTPConnectionFailed           *IndigoHTTPConnectionFailed           `json:"httpConnectionFailed,omitempty"`
	ResponseStreamConnectionFailed *IndigoResponseStreamConnectionFailed `json:"responseStreamConnectionFailed,omitempty"`
	ResponseStreamDisconnected     *IndigoResponseStreamDisconnected     `json:"responseStreamDisconnected,omitempty"`
	ResponseTooManyFailedAttempts  *IndigoResponseTooManyFailedAttempts  `json:"responseTooManyFailedAttempts,omitempty"`
	ActiveTurnNotSteerable         *IndigoActiveTurnNotSteerable         `json:"activeTurnNotSteerable,omitempty"`
}

type IndigoActiveTurnNotSteerable struct {
	TurnKind NonSteerableTurnKind `json:"turnKind"`
}

type IndigoHTTPConnectionFailed struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type IndigoResponseStreamConnectionFailed struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type IndigoResponseStreamDisconnected struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

type IndigoResponseTooManyFailedAttempts struct {
	HTTPStatusCode *int64 `json:"httpStatusCode"`
}

// EXPERIMENTAL - proposed plan item content. The completed plan item is authoritative and
// may not match the concatenation of `PlanDelta` text.
type IndigoThreadItem struct {
	Content []IndigoContent `json:"content,omitempty"`
	// Unique identifier for this collab tool call.
	ID             string                     `json:"id"`
	Type           ThreadItemType             `json:"type"`
	Fragments      []IndigoHookPromptFragment `json:"fragments,omitempty"`
	MemoryCitation *IndigoMemoryCitation      `json:"memoryCitation"`
	Phase          *MessagePhase              `json:"phase"`
	Text           *string                    `json:"text,omitempty"`
	Summary        []string                   `json:"summary,omitempty"`
	// The command's output, aggregated from stdout and stderr.
	AggregatedOutput *string `json:"aggregatedOutput"`
	// The command to be executed.
	Command *string `json:"command,omitempty"`
	// A best-effort parsing of the command to understand the action(s) it will perform. This
	// returns a list of CommandAction objects because a single shell command may be composed of
	// many commands piped together.
	CommandActions []IndigoCommandAction `json:"commandActions,omitempty"`
	// The command's working directory.
	Cwd *string `json:"cwd,omitempty"`
	// The duration of the command execution in milliseconds.
	//
	// The duration of the MCP tool call in milliseconds.
	//
	// The duration of the dynamic tool call in milliseconds.
	DurationMS *int64 `json:"durationMs"`
	// The command's exit code.
	ExitCode *int64 `json:"exitCode"`
	// Identifier for the underlying PTY process (when available).
	ProcessID *string                 `json:"processId"`
	Source    *CommandExecutionSource `json:"source,omitempty"`
	// Current status of the collab tool call.
	Status    *string                  `json:"status,omitempty"`
	Changes   []IndigoFileUpdateChange `json:"changes,omitempty"`
	Arguments interface{}              `json:"arguments"`
	Error     *IndigoMCPToolCallError  `json:"error"`
	Result    *IndigoResult            `json:"result"`
	Server    *string                  `json:"server,omitempty"`
	// Name of the collab tool that was invoked.
	Tool         *string                                  `json:"tool,omitempty"`
	ContentItems []IndigoDynamicToolCallOutputContentItem `json:"contentItems"`
	Success      *bool                                    `json:"success"`
	// Last known status of the target agents, when available.
	AgentsStates map[string]IndigoCollabAgentState `json:"agentsStates,omitempty"`
	// Model requested for the spawned agent, when applicable.
	Model *string `json:"model"`
	// Prompt text sent as part of the collab tool call, when available.
	Prompt *string `json:"prompt"`
	// Reasoning effort requested for the spawned agent, when applicable.
	ReasoningEffort *ReasoningEffort `json:"reasoningEffort"`
	// Thread ID of the receiving agent, when applicable. In case of spawn operation, this
	// corresponds to the newly spawned agent.
	ReceiverThreadIDS []string `json:"receiverThreadIds,omitempty"`
	// Thread ID of the agent issuing the collab request.
	SenderThreadID *string                `json:"senderThreadId,omitempty"`
	Action         *IndigoWebSearchAction `json:"action"`
	Query          *string                `json:"query,omitempty"`
	Path           *string                `json:"path,omitempty"`
	RevisedPrompt  *string                `json:"revisedPrompt"`
	SavedPath      *string                `json:"savedPath"`
	Review         *string                `json:"review,omitempty"`
}

type IndigoWebSearchAction struct {
	Queries []string            `json:"queries"`
	Query   *string             `json:"query"`
	Type    WebSearchActionType `json:"type"`
	URL     *string             `json:"url"`
	Pattern *string             `json:"pattern"`
}

type IndigoCollabAgentState struct {
	Message *string           `json:"message"`
	Status  CollabAgentStatus `json:"status"`
}

type IndigoFileUpdateChange struct {
	Diff string                `json:"diff"`
	Kind IndigoPatchChangeKind `json:"kind"`
	Path string                `json:"path"`
}

type IndigoPatchChangeKind struct {
	Type     Type    `json:"type"`
	MovePath *string `json:"move_path"`
}

type IndigoCommandAction struct {
	Command string            `json:"command"`
	Name    *string           `json:"name,omitempty"`
	Path    *string           `json:"path"`
	Type    CommandActionType `json:"type"`
	Query   *string           `json:"query"`
}

type IndigoUserInput struct {
	Text *string `json:"text,omitempty"`
	// UI-defined spans within `text` used to render or persist special elements.
	TextElements []IndecentTextElement `json:"text_elements,omitempty"`
	Type         UserInputType         `json:"type"`
	URL          *string               `json:"url,omitempty"`
	Path         *string               `json:"path,omitempty"`
	Name         *string               `json:"name,omitempty"`
}

type IndecentTextElement struct {
	// Byte range in the parent `text` buffer that this element occupies.
	ByteRange IndecentByteRange `json:"byteRange"`
	// Optional human-readable placeholder for the element, displayed in the UI.
	Placeholder *string `json:"placeholder"`
}

// Byte range in the parent `text` buffer that this element occupies.
type IndecentByteRange struct {
	End   int64 `json:"end"`
	Start int64 `json:"start"`
}

type IndigoDynamicToolCallOutputContentItem struct {
	Text     *string                                   `json:"text,omitempty"`
	Type     InputDynamicToolCallOutputContentItemType `json:"type"`
	ImageURL *string                                   `json:"imageUrl,omitempty"`
}

type IndigoMCPToolCallError struct {
	Message string `json:"message"`
}

type IndigoHookPromptFragment struct {
	HookRunID string `json:"hookRunId"`
	Text      string `json:"text"`
}

type IndigoMemoryCitation struct {
	Entries   []IndigoMemoryCitationEntry `json:"entries"`
	ThreadIDS []string                    `json:"threadIds"`
}

type IndigoMemoryCitationEntry struct {
	LineEnd   int64  `json:"lineEnd"`
	LineStart int64  `json:"lineStart"`
	Note      string `json:"note"`
	Path      string `json:"path"`
}

type IndigoMCPToolCallResult struct {
	Meta              interface{}   `json:"_meta"`
	Content           []interface{} `json:"content"`
	StructuredContent interface{}   `json:"structuredContent"`
}

type ExecCommandApprovalParams struct {
	// Identifier for this specific approval callback.
	ApprovalID *string `json:"approvalId"`
	// Use to correlate this with [codex_protocol::protocol::ExecCommandBeginEvent] and
	// [codex_protocol::protocol::ExecCommandEndEvent].
	CallID         string          `json:"callId"`
	Command        []string        `json:"command"`
	ConversationID string          `json:"conversationId"`
	Cwd            string          `json:"cwd"`
	ParsedCmd      []ParsedCommand `json:"parsedCmd"`
	Reason         *string         `json:"reason"`
}

type ParsedCommand struct {
	Cmd  string  `json:"cmd"`
	Name *string `json:"name,omitempty"`
	// (Best effort) Path to the file being read by the command. When possible, this is an
	// absolute path, though when relative, it should be resolved against the `cwd`` that will
	// be used to run the command to derive the absolute path.
	Path  *string           `json:"path"`
	Type  ParsedCommandType `json:"type"`
	Query *string           `json:"query"`
}

type ExecCommandApprovalResponse struct {
	Decision *ExecCommandApprovalResponseReviewDecision `json:"decision"`
}

// User has approved this command and wants to apply the proposed execpolicy amendment so
// future matching commands are permitted.
//
// User chose to persist a network policy rule (allow/deny) for future requests to the same
// host.
type PurplePolicyAmendmentReviewDecision struct {
	ApprovedExecpolicyAmendment *PurpleApprovedExecpolicyAmendment `json:"approved_execpolicy_amendment,omitempty"`
	NetworkPolicyAmendment      *PurpleNetworkPolicyAmendment      `json:"network_policy_amendment,omitempty"`
}

type PurpleApprovedExecpolicyAmendment struct {
	ProposedExecpolicyAmendment []string `json:"proposed_execpolicy_amendment"`
}

type PurpleNetworkPolicyAmendment struct {
	NetworkPolicyAmendment FluffyNetworkPolicyAmendment `json:"network_policy_amendment"`
}

type FluffyNetworkPolicyAmendment struct {
	Action NetworkPolicyRuleAction `json:"action"`
	Host   string                  `json:"host"`
}

type ApplyPatchApprovalParams struct {
	// Use to correlate this with [codex_protocol::protocol::PatchApplyBeginEvent] and
	// [codex_protocol::protocol::PatchApplyEndEvent].
	CallID         string                `json:"callId"`
	ConversationID string                `json:"conversationId"`
	FileChanges    map[string]FileChange `json:"fileChanges"`
	// When set, the agent is asking the user to allow writes under this root for the remainder
	// of the session (unclear if this is honored today).
	GrantRoot *string `json:"grantRoot"`
	// Optional explanatory reason (e.g. request for extra write access).
	Reason *string `json:"reason"`
}

type FileChange struct {
	Content     *string `json:"content,omitempty"`
	Type        Type    `json:"type"`
	MovePath    *string `json:"move_path"`
	UnifiedDiff *string `json:"unified_diff,omitempty"`
}

type ApplyPatchApprovalResponse struct {
	Decision *ApplyPatchApprovalResponseReviewDecision `json:"decision"`
}

// User has approved this command and wants to apply the proposed execpolicy amendment so
// future matching commands are permitted.
//
// User chose to persist a network policy rule (allow/deny) for future requests to the same
// host.
type FluffyPolicyAmendmentReviewDecision struct {
	ApprovedExecpolicyAmendment *FluffyApprovedExecpolicyAmendment `json:"approved_execpolicy_amendment,omitempty"`
	NetworkPolicyAmendment      *TentacledNetworkPolicyAmendment   `json:"network_policy_amendment,omitempty"`
}

type FluffyApprovedExecpolicyAmendment struct {
	ProposedExecpolicyAmendment []string `json:"proposed_execpolicy_amendment"`
}

type TentacledNetworkPolicyAmendment struct {
	NetworkPolicyAmendment StickyNetworkPolicyAmendment `json:"network_policy_amendment"`
}

type StickyNetworkPolicyAmendment struct {
	Action NetworkPolicyRuleAction `json:"action"`
	Host   string                  `json:"host"`
}

type CommandExecutionRequestApprovalParams struct {
	// Unique identifier for this specific approval callback.
	//
	// For regular shell/unified_exec approvals, this is null.
	//
	// For zsh-exec-bridge subcommand approvals, multiple callbacks can belong to one parent
	// `itemId`, so `approvalId` is a distinct opaque callback id (a UUID) used to disambiguate
	// routing.
	ApprovalID *string `json:"approvalId"`
	// The command to be executed.
	Command *string `json:"command"`
	// Best-effort parsed command actions for friendly display.
	CommandActions []CommandExecutionRequestApprovalParamsCommandAction `json:"commandActions"`
	// The command's working directory.
	Cwd    *string `json:"cwd"`
	ItemID string  `json:"itemId"`
	// Optional context for a managed-network approval prompt.
	NetworkApprovalContext *NetworkApprovalContext `json:"networkApprovalContext"`
	// Optional proposed execpolicy amendment to allow similar commands without prompting.
	ProposedExecpolicyAmendment []string `json:"proposedExecpolicyAmendment"`
	// Optional proposed network policy amendments (allow/deny host) for future requests.
	ProposedNetworkPolicyAmendments []ProposedNetworkPolicyAmendmentElement `json:"proposedNetworkPolicyAmendments"`
	// Optional explanatory reason (e.g. request for network access).
	Reason   *string `json:"reason"`
	ThreadID string  `json:"threadId"`
	TurnID   string  `json:"turnId"`
}

type CommandExecutionRequestApprovalParamsCommandAction struct {
	Command string            `json:"command"`
	Name    *string           `json:"name,omitempty"`
	Path    *string           `json:"path"`
	Type    CommandActionType `json:"type"`
	Query   *string           `json:"query"`
}

type NetworkApprovalContext struct {
	Host     string                  `json:"host"`
	Protocol NetworkApprovalProtocol `json:"protocol"`
}

type ProposedNetworkPolicyAmendmentElement struct {
	Action NetworkPolicyRuleAction `json:"action"`
	Host   string                  `json:"host"`
}

type CommandExecutionRequestApprovalResponse struct {
	Decision *CommandExecutionApprovalDecision `json:"decision"`
}

// User approved the command, and wants to apply the proposed execpolicy amendment so future
// matching commands can run without prompting.
//
// User chose a persistent network policy rule (allow/deny) for this host.
type PolicyAmendmentCommandExecutionApprovalDecision struct {
	AcceptWithExecpolicyAmendment *AcceptWithExecpolicyAmendment `json:"acceptWithExecpolicyAmendment,omitempty"`
	ApplyNetworkPolicyAmendment   *ApplyNetworkPolicyAmendment   `json:"applyNetworkPolicyAmendment,omitempty"`
}

type AcceptWithExecpolicyAmendment struct {
	ExecpolicyAmendment []string `json:"execpolicy_amendment"`
}

type ApplyNetworkPolicyAmendment struct {
	NetworkPolicyAmendment ApplyNetworkPolicyAmendmentNetworkPolicyAmendment `json:"network_policy_amendment"`
}

type ApplyNetworkPolicyAmendmentNetworkPolicyAmendment struct {
	Action NetworkPolicyRuleAction `json:"action"`
	Host   string                  `json:"host"`
}

type FileChangeRequestApprovalParams struct {
	// [UNSTABLE] When set, the agent is asking the user to allow writes under this root for the
	// remainder of the session (unclear if this is honored today).
	GrantRoot *string `json:"grantRoot"`
	ItemID    string  `json:"itemId"`
	// Optional explanatory reason (e.g. request for extra write access).
	Reason   *string `json:"reason"`
	ThreadID string  `json:"threadId"`
	TurnID   string  `json:"turnId"`
}

type FileChangeRequestApprovalResponse struct {
	Decision FileChangeApprovalDecision `json:"decision"`
}

// EXPERIMENTAL. Params sent with a request_user_input event.
type ToolRequestUserInputParams struct {
	ItemID    string                         `json:"itemId"`
	Questions []ToolRequestUserInputQuestion `json:"questions"`
	ThreadID  string                         `json:"threadId"`
	TurnID    string                         `json:"turnId"`
}

// EXPERIMENTAL. Represents one request_user_input question and its required options.
type ToolRequestUserInputQuestion struct {
	Header   string                       `json:"header"`
	ID       string                       `json:"id"`
	IsOther  *bool                        `json:"isOther,omitempty"`
	IsSecret *bool                        `json:"isSecret,omitempty"`
	Options  []ToolRequestUserInputOption `json:"options"`
	Question string                       `json:"question"`
}

// EXPERIMENTAL. Defines a single selectable option for request_user_input.
type ToolRequestUserInputOption struct {
	Description string `json:"description"`
	Label       string `json:"label"`
}

// EXPERIMENTAL. Response payload mapping question ids to answers.
type ToolRequestUserInputResponse struct {
	Answers map[string]ToolRequestUserInputAnswer `json:"answers"`
}

// EXPERIMENTAL. Captures a user's answer to a request_user_input question.
type ToolRequestUserInputAnswer struct {
	Answers []string `json:"answers"`
}

type MCPServerElicitationRequestParams struct {
	ServerName string `json:"serverName"`
	ThreadID   string `json:"threadId"`
	// Active Codex turn when this elicitation was observed, if app-server could correlate one.
	//
	// This is nullable because MCP models elicitation as a standalone server-to-client request
	// identified by the MCP server request id. It may be triggered during a turn, but turn
	// context is app-server correlation rather than part of the protocol identity of the
	// elicitation itself.
	TurnID          *string               `json:"turnId"`
	Meta            interface{}           `json:"_meta"`
	Message         string                `json:"message"`
	Mode            Mode                  `json:"mode"`
	RequestedSchema *MCPElicitationSchema `json:"requestedSchema,omitempty"`
	ElicitationID   *string               `json:"elicitationId,omitempty"`
	URL             *string               `json:"url,omitempty"`
}

// Typed form schema for MCP `elicitation/create` requests.
//
// This matches the `requestedSchema` shape from the MCP 2025-11-25
// `ElicitRequestFormParams` schema.
type MCPElicitationSchema struct {
	Schema     *string                                  `json:"$schema"`
	Properties map[string]MCPElicitationPrimitiveSchema `json:"properties"`
	Required   []string                                 `json:"required"`
	Type       MCPElicitationObjectType                 `json:"type"`
}

type MCPElicitationPrimitiveSchema struct {
	Default     *Title                         `json:"default"`
	Description *string                        `json:"description"`
	Enum        []string                       `json:"enum,omitempty"`
	Title       *string                        `json:"title"`
	Type        MCPElicitationType             `json:"type"`
	OneOf       []MCPElicitationConstOption    `json:"oneOf,omitempty"`
	Items       *MCPElicitationTitledEnumItems `json:"items,omitempty"`
	MaxItems    *int64                         `json:"maxItems"`
	MinItems    *int64                         `json:"minItems"`
	EnumNames   []string                       `json:"enumNames"`
	Format      *MCPElicitationStringFormat    `json:"format"`
	MaxLength   *int64                         `json:"maxLength"`
	MinLength   *int64                         `json:"minLength"`
	Maximum     *float64                       `json:"maximum"`
	Minimum     *float64                       `json:"minimum"`
}

type MCPElicitationTitledEnumItems struct {
	Enum  []string                    `json:"enum,omitempty"`
	Type  *MCPElicitationStringType   `json:"type,omitempty"`
	AnyOf []MCPElicitationConstOption `json:"anyOf,omitempty"`
}

type MCPElicitationConstOption struct {
	Const string `json:"const"`
	Title string `json:"title"`
}

type MCPServerElicitationRequestResponse struct {
	// Optional client metadata for form-mode action handling.
	Meta   interface{}                `json:"_meta"`
	Action MCPServerElicitationAction `json:"action"`
	// Structured user input for accepted elicitations, mirroring RMCP
	// `CreateElicitationResult`.
	//
	// This is nullable because decline/cancel responses have no content.
	Content interface{} `json:"content"`
}

type DynamicToolCallParams struct {
	Arguments interface{} `json:"arguments"`
	CallID    string      `json:"callId"`
	ThreadID  string      `json:"threadId"`
	Tool      string      `json:"tool"`
	TurnID    string      `json:"turnId"`
}

type DynamicToolCallResponse struct {
	ContentItems []DynamicToolCallResponseDynamicToolCallOutputContentItem `json:"contentItems"`
	Success      bool                                                      `json:"success"`
}

type DynamicToolCallResponseDynamicToolCallOutputContentItem struct {
	Text     *string                                   `json:"text,omitempty"`
	Type     InputDynamicToolCallOutputContentItemType `json:"type"`
	ImageURL *string                                   `json:"imageUrl,omitempty"`
}

type ApprovalPolicyEnum string

const (
	Never     ApprovalPolicyEnum = "never"
	OnFailure ApprovalPolicyEnum = "on-failure"
	OnRequest ApprovalPolicyEnum = "on-request"
	Untrusted ApprovalPolicyEnum = "untrusted"
)

// Configures who approval requests are routed to for review. Examples include sandbox
// escapes, blocked network access, MCP approval prompts, and ARC escalations. Defaults to
// `user`. `guardian_subagent` uses a carefully prompted subagent to gather relevant context
// and apply a risk-based decision framework before approving or denying the request.
//
// Reviewer currently used for approval requests on this thread.
type ApprovalsReviewer string

const (
	GuardianSubagent ApprovalsReviewer = "guardian_subagent"
	User             ApprovalsReviewer = "user"
)

type Personality string

const (
	Friendly        Personality = "friendly"
	PersonalityNone Personality = "none"
	Pragmatic       Personality = "pragmatic"
)

type SandboxMode string

const (
	DangerFullAccess SandboxMode = "danger-full-access"
	ReadOnly         SandboxMode = "read-only"
	WorkspaceWrite   SandboxMode = "workspace-write"
)

type ServiceTier string

const (
	Fast ServiceTier = "fast"
	Flex ServiceTier = "flex"
)

type ThreadStartSource string

const (
	Clear   ThreadStartSource = "clear"
	Startup ThreadStartSource = "startup"
)

// See
// https://platform.openai.com/docs/guides/reasoning?api-mode=responses#get-started-with-reasoning
type ReasoningEffort string

const (
	High                ReasoningEffort = "high"
	Low                 ReasoningEffort = "low"
	Medium              ReasoningEffort = "medium"
	Minimal             ReasoningEffort = "minimal"
	ReasoningEffortNone ReasoningEffort = "none"
	Xhigh               ReasoningEffort = "xhigh"
)

type ReadOnlyAccessType string

const (
	FullAccess                   ReadOnlyAccessType = "fullAccess"
	ReadOnlyAccessTypeRestricted ReadOnlyAccessType = "restricted"
)

type NetworkAccess string

const (
	Enabled                 NetworkAccess = "enabled"
	NetworkAccessRestricted NetworkAccess = "restricted"
)

type SandboxPolicyType string

const (
	ExternalSandbox                   SandboxPolicyType = "externalSandbox"
	SandboxPolicyTypeDangerFullAccess SandboxPolicyType = "dangerFullAccess"
	SandboxPolicyTypeReadOnly         SandboxPolicyType = "readOnly"
	SandboxPolicyTypeWorkspaceWrite   SandboxPolicyType = "workspaceWrite"
)

type SubAgentSource string

const (
	MemoryConsolidation   SubAgentSource = "memory_consolidation"
	SubAgentSourceCompact SubAgentSource = "compact"
	SubAgentSourceReview  SubAgentSource = "review"
)

type SessionSource string

const (
	AppServer            SessionSource = "appServer"
	CLI                  SessionSource = "cli"
	Exec                 SessionSource = "exec"
	SessionSourceUnknown SessionSource = "unknown"
	Vscode               SessionSource = "vscode"
)

type ThreadActiveFlag string

const (
	WaitingOnApproval  ThreadActiveFlag = "waitingOnApproval"
	WaitingOnUserInput ThreadActiveFlag = "waitingOnUserInput"
)

type ThreadStatusType string

const (
	Active      ThreadStatusType = "active"
	Idle        ThreadStatusType = "idle"
	NotLoaded   ThreadStatusType = "notLoaded"
	SystemError ThreadStatusType = "systemError"
)

type NonSteerableTurnKind string

const (
	NonSteerableTurnKindCompact NonSteerableTurnKind = "compact"
	NonSteerableTurnKindReview  NonSteerableTurnKind = "review"
)

type CodexErrorInfoEnum string

const (
	BadRequest            CodexErrorInfoEnum = "badRequest"
	CodexErrorInfoOther   CodexErrorInfoEnum = "other"
	ContextWindowExceeded CodexErrorInfoEnum = "contextWindowExceeded"
	InternalServerError   CodexErrorInfoEnum = "internalServerError"
	SandboxError          CodexErrorInfoEnum = "sandboxError"
	ServerOverloaded      CodexErrorInfoEnum = "serverOverloaded"
	ThreadRollbackFailed  CodexErrorInfoEnum = "threadRollbackFailed"
	Unauthorized          CodexErrorInfoEnum = "unauthorized"
	UsageLimitExceeded    CodexErrorInfoEnum = "usageLimitExceeded"
)

type WebSearchActionType string

const (
	FindInPage                WebSearchActionType = "findInPage"
	OpenPage                  WebSearchActionType = "openPage"
	WebSearchActionTypeOther  WebSearchActionType = "other"
	WebSearchActionTypeSearch WebSearchActionType = "search"
)

type CollabAgentStatus string

const (
	CollabAgentStatusCompleted   CollabAgentStatus = "completed"
	CollabAgentStatusInterrupted CollabAgentStatus = "interrupted"
	Errored                      CollabAgentStatus = "errored"
	NotFound                     CollabAgentStatus = "notFound"
	PendingInit                  CollabAgentStatus = "pendingInit"
	Running                      CollabAgentStatus = "running"
	Shutdown                     CollabAgentStatus = "shutdown"
)

type Type string

const (
	Add    Type = "add"
	Delete Type = "delete"
	Update Type = "update"
)

type CommandActionType string

const (
	CommandActionTypeRead    CommandActionType = "read"
	CommandActionTypeSearch  CommandActionType = "search"
	CommandActionTypeUnknown CommandActionType = "unknown"
	ListFiles                CommandActionType = "listFiles"
)

type UserInputType string

const (
	Image      UserInputType = "image"
	LocalImage UserInputType = "localImage"
	Mention    UserInputType = "mention"
	Skill      UserInputType = "skill"
	Text       UserInputType = "text"
)

type InputDynamicToolCallOutputContentItemType string

const (
	InputImage InputDynamicToolCallOutputContentItemType = "inputImage"
	InputText  InputDynamicToolCallOutputContentItemType = "inputText"
)

// Mid-turn assistant text (for example preamble/progress narration).
//
// Additional tool calls or assistant output may follow before turn completion.
//
// The assistant's terminal answer text for the current turn.
type MessagePhase string

const (
	Commentary  MessagePhase = "commentary"
	FinalAnswer MessagePhase = "final_answer"
)

type CommandExecutionSource string

const (
	Agent                  CommandExecutionSource = "agent"
	UnifiedExecInteraction CommandExecutionSource = "unifiedExecInteraction"
	UnifiedExecStartup     CommandExecutionSource = "unifiedExecStartup"
	UserShell              CommandExecutionSource = "userShell"
)

type ThreadItemType string

const (
	AgentMessage             ThreadItemType = "agentMessage"
	CollabAgentToolCall      ThreadItemType = "collabAgentToolCall"
	CommandExecution         ThreadItemType = "commandExecution"
	ContextCompaction        ThreadItemType = "contextCompaction"
	DynamicToolCall          ThreadItemType = "dynamicToolCall"
	EnteredReviewMode        ThreadItemType = "enteredReviewMode"
	ExitedReviewMode         ThreadItemType = "exitedReviewMode"
	HookPrompt               ThreadItemType = "hookPrompt"
	ImageGeneration          ThreadItemType = "imageGeneration"
	ImageView                ThreadItemType = "imageView"
	MCPToolCall              ThreadItemType = "mcpToolCall"
	Plan                     ThreadItemType = "plan"
	Reasoning                ThreadItemType = "reasoning"
	ThreadItemTypeFileChange ThreadItemType = "fileChange"
	UserMessage              ThreadItemType = "userMessage"
	WebSearch                ThreadItemType = "webSearch"
)

type TurnStatus string

const (
	Failed                TurnStatus = "failed"
	InProgress            TurnStatus = "inProgress"
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusInterrupted TurnStatus = "interrupted"
)

// Option to disable reasoning summaries.
type ReasoningSummary string

const (
	Auto                 ReasoningSummary = "auto"
	Concise              ReasoningSummary = "concise"
	Detailed             ReasoningSummary = "detailed"
	ReasoningSummaryNone ReasoningSummary = "none"
)

type ParsedCommandType string

const (
	ParsedCommandTypeListFiles ParsedCommandType = "list_files"
	ParsedCommandTypeRead      ParsedCommandType = "read"
	ParsedCommandTypeSearch    ParsedCommandType = "search"
	ParsedCommandTypeUnknown   ParsedCommandType = "unknown"
)

type NetworkPolicyRuleAction string

const (
	Allow NetworkPolicyRuleAction = "allow"
	Deny  NetworkPolicyRuleAction = "deny"
)

// User has approved this command and the agent should execute it.
//
// User has approved this request and wants future prompts in the same session-scoped
// approval cache to be automatically approved for the remainder of the session.
//
// User has denied this command and the agent should not execute it, but it should continue
// the session and try something else.
//
// User has denied this command and the agent should not do anything until the user's next
// command.
type ReviewDecision string

const (
	Abort              ReviewDecision = "abort"
	Approved           ReviewDecision = "approved"
	ApprovedForSession ReviewDecision = "approved_for_session"
	Denied             ReviewDecision = "denied"
)

type NetworkApprovalProtocol string

const (
	HTTP      NetworkApprovalProtocol = "http"
	HTTPS     NetworkApprovalProtocol = "https"
	Socks5TCP NetworkApprovalProtocol = "socks5Tcp"
	Socks5UDP NetworkApprovalProtocol = "socks5Udp"
)

// User approved the command.
//
// User approved the command and future prompts in the same session-scoped approval cache
// should run without prompting.
//
// User denied the command. The agent will continue the turn.
//
// User denied the command. The turn will also be immediately interrupted.
//
// User approved the file changes.
//
// User approved the file changes and future changes to the same files should run without
// prompting.
//
// User denied the file changes. The agent will continue the turn.
//
// User denied the file changes. The turn will also be immediately interrupted.
type FileChangeApprovalDecision string

const (
	AcceptForSession                  FileChangeApprovalDecision = "acceptForSession"
	FileChangeApprovalDecisionAccept  FileChangeApprovalDecision = "accept"
	FileChangeApprovalDecisionCancel  FileChangeApprovalDecision = "cancel"
	FileChangeApprovalDecisionDecline FileChangeApprovalDecision = "decline"
)

type Mode string

const (
	Form Mode = "form"
	URL  Mode = "url"
)

type MCPElicitationStringFormat string

const (
	Date     MCPElicitationStringFormat = "date"
	DateTime MCPElicitationStringFormat = "date-time"
	Email    MCPElicitationStringFormat = "email"
	URI      MCPElicitationStringFormat = "uri"
)

type MCPElicitationStringType string

const (
	MCPElicitationStringTypeString MCPElicitationStringType = "string"
)

type MCPElicitationType string

const (
	Array                    MCPElicitationType = "array"
	Boolean                  MCPElicitationType = "boolean"
	Integer                  MCPElicitationType = "integer"
	MCPElicitationTypeString MCPElicitationType = "string"
	Number                   MCPElicitationType = "number"
)

type MCPElicitationObjectType string

const (
	Object MCPElicitationObjectType = "object"
)

type MCPServerElicitationAction string

const (
	MCPServerElicitationActionAccept  MCPServerElicitationAction = "accept"
	MCPServerElicitationActionCancel  MCPServerElicitationAction = "cancel"
	MCPServerElicitationActionDecline MCPServerElicitationAction = "decline"
)

type ThreadStartParamsApprovalPolicy struct {
	Enum                         *ApprovalPolicyEnum
	PurpleGranularAskForApproval *PurpleGranularAskForApproval
}

type AskForApproval struct {
	AskForApprovalGranularAskForApproval *AskForApprovalGranularAskForApproval
	Enum                                 *ApprovalPolicyEnum
}

type NetworkAccessUnion struct {
	Bool *bool
	Enum *NetworkAccess
}

// Origin of the thread (CLI, VSCode, codex exec, codex app-server, etc.).
type TentacledSessionSource struct {
	Enum                *SessionSource
	PurpleSessionSource *PurpleSessionSource
}

type TentacledSubAgentSource struct {
	Enum                 *SubAgentSource
	PurpleSubAgentSource *PurpleSubAgentSource
}

type IndecentCodexErrorInfo struct {
	Enum                 *CodexErrorInfoEnum
	PurpleCodexErrorInfo *PurpleCodexErrorInfo
}

type PurpleContent struct {
	PurpleUserInput *PurpleUserInput
	String          *string
}

type PurpleResult struct {
	PurpleMCPToolCallResult *PurpleMCPToolCallResult
	String                  *string
}

// Override the approval policy for this turn and subsequent turns.
type TurnStartParamsApprovalPolicy struct {
	Enum                         *ApprovalPolicyEnum
	FluffyGranularAskForApproval *FluffyGranularAskForApproval
}

type HilariousCodexErrorInfo struct {
	Enum                 *CodexErrorInfoEnum
	FluffyCodexErrorInfo *FluffyCodexErrorInfo
}

type FluffyContent struct {
	FluffyUserInput *FluffyUserInput
	String          *string
}

type FluffyResult struct {
	FluffyMCPToolCallResult *FluffyMCPToolCallResult
	String                  *string
}

// Origin of the thread (CLI, VSCode, codex exec, codex app-server, etc.).
type StickySessionSource struct {
	Enum                *SessionSource
	FluffySessionSource *FluffySessionSource
}

type StickySubAgentSource struct {
	Enum                 *SubAgentSource
	FluffySubAgentSource *FluffySubAgentSource
}

type AmbitiousCodexErrorInfo struct {
	Enum                    *CodexErrorInfoEnum
	TentacledCodexErrorInfo *TentacledCodexErrorInfo
}

type TentacledContent struct {
	String             *string
	TentacledUserInput *TentacledUserInput
}

type TentacledResult struct {
	String                     *string
	TentacledMCPToolCallResult *TentacledMCPToolCallResult
}

type CunningCodexErrorInfo struct {
	Enum                 *CodexErrorInfoEnum
	StickyCodexErrorInfo *StickyCodexErrorInfo
}

type StickyContent struct {
	StickyUserInput *StickyUserInput
	String          *string
}

type StickyResult struct {
	StickyMCPToolCallResult *StickyMCPToolCallResult
	String                  *string
}

type MagentaCodexErrorInfo struct {
	Enum                 *CodexErrorInfoEnum
	IndigoCodexErrorInfo *IndigoCodexErrorInfo
}

type IndigoContent struct {
	IndigoUserInput *IndigoUserInput
	String          *string
}

type IndigoResult struct {
	IndigoMCPToolCallResult *IndigoMCPToolCallResult
	String                  *string
}

// User's decision in response to an ExecApprovalRequest.
type ExecCommandApprovalResponseReviewDecision struct {
	Enum                                *ReviewDecision
	PurplePolicyAmendmentReviewDecision *PurplePolicyAmendmentReviewDecision
}

// User's decision in response to an ExecApprovalRequest.
type ApplyPatchApprovalResponseReviewDecision struct {
	Enum                                *ReviewDecision
	FluffyPolicyAmendmentReviewDecision *FluffyPolicyAmendmentReviewDecision
}

type CommandExecutionApprovalDecision struct {
	Enum                                            *FileChangeApprovalDecision
	PolicyAmendmentCommandExecutionApprovalDecision *PolicyAmendmentCommandExecutionApprovalDecision
}

type Title struct {
	Bool        *bool
	Double      *float64
	String      *string
	StringArray []string
}
