package config

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	TrackerKindKanban                  = "kanban"
	AgentModeAppServer                 = "app_server"
	AgentModeStdio                     = "stdio"
	DispatchModeParallel               = "parallel"
	DispatchModePerProjectSerial       = "per_project_serial"
	InitialCollaborationModePlan       = "plan"
	InitialCollaborationModeDefault    = "default"
	WorkflowAdvisoryPermissions        = "workflow_permissions"
	WorkflowAdvisoryApprovalPolicy     = "workflow_approval_policy"
	WorkflowAdvisoryPlanApprovalPolicy = "workflow_plan_approval_policy"
	WorkflowAdvisoryPromptBranching    = "workflow_prompt_branching"
)

var (
	ErrMissingWorkflowFile = errors.New("missing_workflow_file")
	ErrWorkflowParse       = errors.New("workflow_parse_error")
	ErrWorkflowFrontMatter = errors.New("workflow_front_matter_not_a_map")
)

type Config struct {
	Tracker   TrackerConfig   `yaml:"tracker"`
	Polling   PollingConfig   `yaml:"polling"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Hooks     HooksConfig     `yaml:"hooks"`
	Agent     AgentConfig     `yaml:"agent"`
	Codex     CodexConfig     `yaml:"codex"`
	Phases    PhasesConfig    `yaml:"phases"`
}

type TrackerConfig struct {
	Kind           string   `yaml:"kind"`
	ActiveStates   []string `yaml:"active_states"`
	TerminalStates []string `yaml:"terminal_states"`
}

type PollingConfig struct {
	IntervalMs int `yaml:"interval_ms"`
}

type WorkspaceConfig struct {
	Root string `yaml:"root"`
}

type HooksConfig struct {
	AfterCreate  string `yaml:"after_create"`
	BeforeRun    string `yaml:"before_run"`
	AfterRun     string `yaml:"after_run"`
	BeforeRemove string `yaml:"before_remove"`
	TimeoutMs    int    `yaml:"timeout_ms"`
}

type AgentConfig struct {
	MaxConcurrentAgents int    `yaml:"max_concurrent_agents"`
	MaxTurns            int    `yaml:"max_turns"`
	MaxRetryBackoffMs   int    `yaml:"max_retry_backoff_ms"`
	MaxAutomaticRetries int    `yaml:"max_automatic_retries"`
	Mode                string `yaml:"mode"`
	DispatchMode        string `yaml:"dispatch_mode"`
}

type CodexConfig struct {
	Command                  string      `yaml:"command"`
	ApprovalPolicy           interface{} `yaml:"approval_policy"`
	InitialCollaborationMode string      `yaml:"initial_collaboration_mode"`
	TurnTimeoutMs            int         `yaml:"turn_timeout_ms"`
	ReadTimeoutMs            int         `yaml:"read_timeout_ms"`
	StallTimeoutMs           int         `yaml:"stall_timeout_ms"`
}

type PhasesConfig struct {
	Review PhasePromptConfig `yaml:"review"`
	Done   PhasePromptConfig `yaml:"done"`
}

type PhasePromptConfig struct {
	Enabled bool   `yaml:"enabled"`
	Prompt  string `yaml:"prompt"`
}

type Workflow struct {
	Path           string
	Config         Config
	PromptTemplate string
	Advisories     []WorkflowAdvisory
}

type WorkflowAdvisory struct {
	Code        string
	Message     string
	Remediation string
}

type workflowPayload struct {
	Config Config
	Prompt string
	Raw    map[string]interface{}
}

type fileStamp struct {
	ModTime int64
	Size    int64
	Hash    uint64
}

func DefaultConfig() Config {
	return Config{
		Tracker: TrackerConfig{
			Kind:           TrackerKindKanban,
			ActiveStates:   []string{"ready", "in_progress", "in_review"},
			TerminalStates: []string{"done", "cancelled"},
		},
		Polling:   PollingConfig{IntervalMs: 10000},
		Workspace: WorkspaceConfig{Root: "~/.maestro/worktrees"},
		Hooks:     HooksConfig{TimeoutMs: 60000},
		Agent: AgentConfig{
			MaxConcurrentAgents: 3,
			MaxTurns:            4,
			MaxRetryBackoffMs:   60000,
			MaxAutomaticRetries: 8,
			Mode:                AgentModeAppServer,
			DispatchMode:        DispatchModeParallel,
		},
		Codex: CodexConfig{
			Command: "codex app-server",
			ApprovalPolicy: map[string]interface{}{
				"granular": map[string]interface{}{
					"sandbox_approval":    true,
					"rules":               true,
					"mcp_elicitations":    true,
					"request_permissions": false,
				},
			},
			InitialCollaborationMode: InitialCollaborationModeDefault,
			TurnTimeoutMs:            1800000,
			ReadTimeoutMs:            10000,
			StallTimeoutMs:           300000,
		},
		Phases: PhasesConfig{
			Review: PhasePromptConfig{
				Enabled: true,
				Prompt:  DefaultReviewPromptTemplate(),
			},
			Done: PhasePromptConfig{
				Enabled: true,
				Prompt:  DefaultDonePromptTemplate(),
			},
		},
	}
}

func DefaultInitConfig() Config {
	cfg := DefaultConfig()
	cfg.Codex.ApprovalPolicy = "never"
	return cfg
}

func DefaultPromptTemplate() string {
	return strings.TrimSpace(`
You are working on issue {{ issue.identifier }}.

Current phase: {{ phase }}

{% if attempt %}
Continuation attempt: {{ attempt }}
{% endif %}

Title: {{ issue.title }}
State: {{ issue.state }}
{% if project.description %}
Project context:
{{ project.description }}

{% endif %}
Description:
{% if issue.description %}
{{ issue.description }}
{% else %}
No description provided.
{% endif %}

## Default posture

- Determine the issue status first, then follow the matching flow.
- Open the Maestro Workpad comment immediately and update it before new implementation work.
- Plan before coding. Design verification before changing code.
{% if plan_mode %}
- This is a planning turn. Ask the clarifying questions you need, validate assumptions, and stop with a single <proposed_plan> block once the approach is ready.
- Do not start implementation until the plan is approved.
{% endif %}
- Reproduce or inspect current behavior first so the target is explicit.
- Keep metadata current: state, checklist, acceptance criteria, and links.
- Treat the persistent workpad comment as the source of truth.
- If you find meaningful out-of-scope work, file a separate maestro CLI issue instead of expanding scope. Include a clear title, description, and acceptance criteria; place it in Backlog; use the same project; link the current issue; and add a blocker relation when needed.
- Move status only when the quality bar for that status is met.
- Use the blocked-access escape hatch only for genuine external blockers after documented fallbacks are exhausted.
- In the done phase, after merge, push, and final validation succeed, leave the workspace intact; Maestro handles cleanup hooks and worktree removal after your run exits.

## Instructions

1. Stay inside the provided workspace.
2. Keep the change focused and preserve project conventions.
3. Reproduce or inspect current behavior before editing when possible.
4. Run validation that covers the changed scope.
5. Use the issue branch already prepared by Maestro in the provided workspace. Do not create, rename, or switch issue branches manually unless you are recovering from a broken workspace.
6. Do not consider the task complete until the change is merged into the repository default branch.
7. Before marking done, merge the issue branch into the repository default branch, rerun validation on that branch, and push the default branch to origin.
8. In the done phase, after merge, push, and final validation succeed, leave the workspace intact; Maestro handles preview publication, cleanup hooks, and worktree removal after your run exits.
9. Add an issue comment when you create a branch, commit, PR, or merge commit, when relevant.
10. If blocked by credentials, permissions, merge conflicts, or required services, stop, report it clearly in the final message, and add the same blocker comment.
11. Final message must contain only completed work, validation run, merge status, and blockers.

## Guardrails

- If the workspace branch is unusable or a prior branch was already merged or closed, do not manually create a replacement branch. Report the condition clearly and stop; Maestro owns workspace and branch bootstrap.
- If the issue state is Backlog, do not modify it; wait for a human to move it to Ready.
- Do not edit the issue body for planning or progress updates.
- Use exactly one persistent workpad comment (## Maestro Workpad) per issue.
- Temporary proof edits are allowed only for local verification and must be reverted before commit.
- Keep issue text concise, specific, and reviewer-oriented.
- If blocked and no workpad exists yet, add one blocker comment with the blocker, impact, and next unblock action.
`)
}

func DefaultReviewPromptTemplate() string {
	return strings.TrimSpace(`
You are performing the review pass for issue {{ issue.identifier }}.

Title: {{ issue.title }}
State: {{ issue.state }}
{% if project.description %}
Project context:
{{ project.description }}

{% endif %}
Description:
{% if issue.description %}
{{ issue.description }}
{% else %}
No description provided.
{% endif %}

Review the implementation in the current workspace, run focused verification, and fix any issues you find.

- If additional implementation is still required after review, move the issue back to in_progress.
- If the issue is ready to finalize, move it to done.
`)
}

func DefaultInitReviewPromptTemplate() string {
	return strings.TrimSpace(`
Review the implementation for issue {{ issue.identifier }} in the current workspace.
{% if project.description %}
Project context:
{{ project.description }}

{% endif %}
Run focused verification, fix any issues you find, move the issue back to in_progress if more work is needed, and move it to done when review is complete.
`)
}

func DefaultDonePromptTemplate() string {
	return strings.TrimSpace(`
You are performing the done pass for issue {{ issue.identifier }}.

Title: {{ issue.title }}
State: {{ issue.state }}
{% if project.description %}
Project context:
{{ project.description }}

{% endif %}
Description:
{% if issue.description %}
{{ issue.description }}
{% else %}
No description provided.
{% endif %}

The done phase owns merge-back and finalization for this issue from the current workspace. Maestro handles preview publication, cleanup hooks, and worktree removal after your run exits.

- Commit all remaining changes to the prepared issue branch.
- Merge the issue branch into the repository default branch.
- Rerun the relevant validation on the default branch.
- Push the default branch to origin.
- Do not remove the issue worktree yourself; leave the workspace intact for Maestro's post-run cleanup.
- If merge conflicts, missing credentials, permissions, or required services block completion, report the blocker clearly and stop.
`)
}

func DefaultInitDonePromptTemplate() string {
	return strings.TrimSpace(`
Finalize issue {{ issue.identifier }} from the current workspace.
{% if project.description %}
Project context:
{{ project.description }}

{% endif %}
The done phase owns merge-back and finalization. Maestro handles preview publication, cleanup hooks, and worktree removal after your run exits.

- Commit all remaining changes to the prepared issue branch.
- Merge the issue branch into the repository default branch.
- Rerun the relevant validation on the default branch.
- Push the default branch to origin.
- Do not remove the issue worktree yourself; leave the workspace intact for Maestro's post-run cleanup.
- If merge conflicts, missing credentials, permissions, or required services block completion, report the blocker clearly and stop.
`)
}

func WorkflowPath(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		repoPath, _ = os.Getwd()
	}
	return filepath.Join(repoPath, "WORKFLOW.md")
}

func ResolveWorkflowPath(repoPath, overridePath string) string {
	if strings.TrimSpace(overridePath) != "" {
		overridePath = expandPathValue(overridePath)
		if strings.HasPrefix(overridePath, "$") {
			return filepath.Clean(overridePath)
		}
		if filepath.IsAbs(overridePath) {
			return filepath.Clean(overridePath)
		}
		if strings.TrimSpace(repoPath) == "" {
			repoPath, _ = os.Getwd()
		}
		return filepath.Clean(filepath.Join(repoPath, overridePath))
	}
	return WorkflowPath(repoPath)
}

func LoadWorkflow(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrMissingWorkflowFile
		}
		return nil, fmt.Errorf("%w: %v", ErrMissingWorkflowFile, err)
	}

	payload, err := parseWorkflowPayload(path, string(data))
	if err != nil {
		return nil, err
	}

	cfg := payload.Config
	if err := applyDefaults(&cfg); err != nil {
		return nil, err
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}

	return &Workflow{
		Path:           path,
		Config:         cfg,
		PromptTemplate: payload.Prompt,
		Advisories:     detectWorkflowAdvisories(cfg, payload.Prompt, payload.Raw),
	}, nil
}

func parseWorkflowPayload(path, content string) (*workflowPayload, error) {
	raw, promptStart, err := parseWorkflowFrontMatter(content)
	if err != nil {
		return nil, err
	}

	normalized, err := normalizeWorkflowKeys(raw)
	if err != nil {
		return nil, err
	}

	encoded, err := yaml.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
	}

	var cfg Config
	if len(normalized) > 0 {
		if err := yaml.Unmarshal(encoded, &cfg); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
		}
	}

	prompt := strings.TrimSpace(content[promptStart:])
	if prompt == "" && promptStart == 0 {
		prompt = DefaultPromptTemplate()
	}
	if _, err := ParseLiquidTemplate(prompt); err != nil {
		return nil, fmt.Errorf("template_parse_error: %w", err)
	}

	root, err := resolveWorkspaceRootPath(filepath.Dir(path), cfg.Workspace.Root)
	if err != nil {
		return nil, err
	}
	cfg.Workspace.Root = root
	return &workflowPayload{Config: cfg, Prompt: prompt, Raw: normalized}, nil
}

func parseWorkflowFrontMatter(content string) (map[string]interface{}, int, error) {
	if !strings.HasPrefix(content, "---\n") {
		return map[string]interface{}{}, 0, nil
	}

	end := strings.Index(content[4:], "\n---\n")
	frontMatter := content[4:]
	promptStart := len(content)
	if end != -1 {
		frontMatter = content[4 : end+4]
		promptStart = end + 8
	}

	var raw interface{}
	if err := yaml.Unmarshal([]byte(frontMatter), &raw); err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
	}

	normalized, err := normalizeWorkflowFrontMatter(raw, frontMatter)
	if err != nil {
		return nil, 0, err
	}

	return normalized, promptStart, nil
}

func normalizeWorkflowFrontMatter(raw interface{}, frontMatter string) (map[string]interface{}, error) {
	if raw == nil {
		return map[string]interface{}{}, nil
	}

	switch typed := raw.(type) {
	case map[string]interface{}:
		return normalizeWorkflowMap(typed), nil
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, value := range typed {
			out[fmt.Sprint(key)] = normalizeWorkflowValue(value)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: front matter must be a map, got %T", ErrWorkflowFrontMatter, raw)
	}
}

func normalizeWorkflowMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = normalizeWorkflowValue(value)
	}
	return out
}

func normalizeWorkflowValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return normalizeWorkflowMap(typed)
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			out[fmt.Sprint(key)] = normalizeWorkflowValue(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, child := range typed {
			out[i] = normalizeWorkflowValue(child)
		}
		return out
	default:
		return value
	}
}

func normalizeWorkflowKeys(raw map[string]interface{}) (map[string]interface{}, error) {
	if raw == nil {
		return map[string]interface{}{}, nil
	}

	out := cloneMap(raw)
	tracker := ensureMap(out, "tracker")
	polling := ensureMap(out, "polling")
	workspace := ensureMap(out, "workspace")
	hooks := ensureMap(out, "hooks")
	agent := ensureMap(out, "agent")
	codex := ensureMap(out, "codex")
	phases := ensureMap(out, "phases")
	review := ensureMap(phases, "review")
	done := ensureMap(phases, "done")

	setBoolDefault(review, "enabled", true)
	setBoolDefault(done, "enabled", true)

	moveString(out, tracker, "tracker_kind", "kind")
	moveStringSlice(out, tracker, "tracker_active_states", "active_states")
	moveStringSlice(out, tracker, "tracker_terminal_states", "terminal_states")
	moveString(out, polling, "poll_interval", "interval_ms")
	moveString(out, polling, "poll_interval_ms", "interval_ms")
	moveNumeric(out, polling, "poll_interval", "interval_ms")
	moveNumeric(out, polling, "poll_interval_ms", "interval_ms")
	moveString(out, workspace, "workspace_root", "root")
	moveNumeric(out, hooks, "hooks_timeout_ms", "timeout_ms")
	moveNumeric(out, agent, "max_concurrent", "max_concurrent_agents")
	moveNumeric(out, agent, "max_concurrent_agents", "max_concurrent_agents")
	moveNumeric(out, agent, "max_turns", "max_turns")
	moveNumeric(out, agent, "max_retry_backoff_ms", "max_retry_backoff_ms")
	moveNumeric(out, agent, "max_automatic_retries", "max_automatic_retries")
	moveString(out, agent, "agent_mode", "mode")
	moveString(out, agent, "dispatch_mode", "dispatch_mode")
	moveString(out, codex, "codex_command", "command")
	moveValue(out, codex, "codex_approval_policy", "approval_policy")
	moveString(out, codex, "codex_initial_collaboration_mode", "initial_collaboration_mode")
	moveNumeric(out, codex, "codex_turn_timeout_ms", "turn_timeout_ms")
	moveNumeric(out, codex, "codex_read_timeout_ms", "read_timeout_ms")
	moveNumeric(out, codex, "codex_stall_timeout_ms", "stall_timeout_ms")
	if value, ok := codex["approval_policy"]; ok {
		normalized, err := normalizeApprovalPolicyValue(value, true)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
		}
		codex["approval_policy"] = normalized
	}

	unsupported := []string{"tracker_api_token", "tracker_project_slug", "tracker_assignee"}
	for _, key := range unsupported {
		if _, ok := out[key]; ok {
			return nil, fmt.Errorf("%w: legacy workflow key %q is not supported in kanban mode", ErrWorkflowParse, key)
		}
	}
	return out, nil
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		if child, ok := v.(map[string]interface{}); ok {
			out[k] = cloneMap(child)
			continue
		}
		out[k] = v
	}
	return out
}

func ensureMap(root map[string]interface{}, key string) map[string]interface{} {
	if current, ok := root[key].(map[string]interface{}); ok {
		return current
	}
	child := map[string]interface{}{}
	root[key] = child
	return child
}

func moveValue(root, dest map[string]interface{}, from, to string) {
	value, ok := root[from]
	if !ok {
		return
	}
	delete(root, from)
	if _, exists := dest[to]; !exists {
		dest[to] = value
	}
}

func moveString(root, dest map[string]interface{}, from, to string) {
	if value, ok := root[from].(string); ok {
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = value
		}
	}
}

func moveMap(root, dest map[string]interface{}, from, to string) {
	if value, ok := root[from].(map[string]interface{}); ok {
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = value
		}
	}
}

func moveNumeric(root, dest map[string]interface{}, from, to string) {
	value, ok := root[from]
	if !ok {
		return
	}
	switch value.(type) {
	case int, int64, float64:
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = value
		}
	}
}

func setBoolDefault(dest map[string]interface{}, key string, value bool) {
	if _, exists := dest[key]; exists {
		return
	}
	dest[key] = value
}

func moveStringSlice(root, dest map[string]interface{}, from, to string) {
	value, ok := root[from]
	if !ok {
		return
	}
	switch typed := value.(type) {
	case []interface{}:
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = typed
		}
	case []string:
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = typed
		}
	case string:
		delete(root, from)
		if _, exists := dest[to]; !exists {
			dest[to] = splitCSVValues(typed)
		}
	}
}

func splitCSVValues(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeApprovalPolicyValue(value interface{}, present bool) (interface{}, error) {
	if !present {
		return nil, nil
	}
	if value == nil {
		return nil, fmt.Errorf("approval_policy cannot be blank")
	}

	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		policy, ok := canonicalApprovalPolicyString(trimmed)
		if !ok {
			if trimmed == "" {
				return nil, fmt.Errorf("approval_policy cannot be blank")
			}
			return nil, fmt.Errorf("unsupported approval_policy %q", typed)
		}
		return policy, nil
	case map[string]interface{}:
		return normalizeWorkflowMap(typed), nil
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			out[fmt.Sprint(key)] = normalizeWorkflowValue(child)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported approval_policy type %T", value)
	}
}

func canonicalApprovalPolicyString(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "never":
		return "never", true
	case "on-request", "on_request":
		return "on-request", true
	case "on-failure", "on_failure":
		return "on-failure", true
	case "untrusted":
		return "untrusted", true
	default:
		return "", false
	}
}

func applyDefaults(c *Config) error {
	defaults := DefaultConfig()

	if strings.TrimSpace(c.Tracker.Kind) == "" {
		c.Tracker.Kind = defaults.Tracker.Kind
	}
	if len(c.Tracker.ActiveStates) == 0 {
		c.Tracker.ActiveStates = append([]string(nil), defaults.Tracker.ActiveStates...)
	}
	if len(c.Tracker.TerminalStates) == 0 {
		c.Tracker.TerminalStates = append([]string(nil), defaults.Tracker.TerminalStates...)
	}
	if c.Polling.IntervalMs <= 0 {
		c.Polling.IntervalMs = defaults.Polling.IntervalMs
	}
	if strings.TrimSpace(c.Workspace.Root) == "" {
		c.Workspace.Root = defaults.Workspace.Root
	}
	if c.Hooks.TimeoutMs <= 0 {
		c.Hooks.TimeoutMs = defaults.Hooks.TimeoutMs
	}
	if c.Agent.MaxConcurrentAgents <= 0 {
		c.Agent.MaxConcurrentAgents = defaults.Agent.MaxConcurrentAgents
	}
	if c.Agent.MaxTurns <= 0 {
		c.Agent.MaxTurns = defaults.Agent.MaxTurns
	}
	if c.Agent.MaxRetryBackoffMs <= 0 {
		c.Agent.MaxRetryBackoffMs = defaults.Agent.MaxRetryBackoffMs
	}
	if c.Agent.MaxAutomaticRetries <= 0 {
		c.Agent.MaxAutomaticRetries = defaults.Agent.MaxAutomaticRetries
	}
	if strings.TrimSpace(c.Agent.Mode) == "" {
		c.Agent.Mode = defaults.Agent.Mode
	}
	if strings.TrimSpace(c.Agent.DispatchMode) == "" {
		c.Agent.DispatchMode = defaults.Agent.DispatchMode
	}
	if strings.TrimSpace(c.Codex.Command) == "" {
		c.Codex.Command = defaults.Codex.Command
	}
	normalizedApprovalPolicy, err := normalizeApprovalPolicyValue(c.Codex.ApprovalPolicy, c.Codex.ApprovalPolicy != nil)
	if err != nil {
		return err
	}
	if normalizedApprovalPolicy == nil {
		c.Codex.ApprovalPolicy = defaults.Codex.ApprovalPolicy
	} else {
		c.Codex.ApprovalPolicy = normalizedApprovalPolicy
	}
	c.Codex.InitialCollaborationMode = normalizeInitialCollaborationMode(c.Codex.InitialCollaborationMode)
	if c.Codex.InitialCollaborationMode == "" {
		c.Codex.InitialCollaborationMode = defaults.Codex.InitialCollaborationMode
	}
	if c.Codex.TurnTimeoutMs <= 0 {
		c.Codex.TurnTimeoutMs = defaults.Codex.TurnTimeoutMs
	}
	if c.Codex.ReadTimeoutMs <= 0 {
		c.Codex.ReadTimeoutMs = defaults.Codex.ReadTimeoutMs
	}
	if c.Codex.StallTimeoutMs <= 0 {
		c.Codex.StallTimeoutMs = defaults.Codex.StallTimeoutMs
	}
	if c.Phases.Review.Enabled && strings.TrimSpace(c.Phases.Review.Prompt) == "" {
		c.Phases.Review.Prompt = DefaultReviewPromptTemplate()
	}
	if c.Phases.Done.Enabled && strings.TrimSpace(c.Phases.Done.Prompt) == "" {
		c.Phases.Done.Prompt = DefaultDonePromptTemplate()
	}
	return nil
}

func LegacyWorkflowUsesFullAccess(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return false, nil
	}

	end := strings.Index(content[4:], "\n---\n")
	frontMatter := content[4:]
	if end != -1 {
		frontMatter = content[4 : end+4]
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(frontMatter), &raw); err != nil {
		return false, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
	}
	if raw == nil {
		return false, nil
	}
	return rawWorkflowUsesFullAccess(raw), nil
}

func rawWorkflowUsesFullAccess(raw map[string]interface{}) bool {
	codex := extractMap(raw["codex"])
	if strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", codex["thread_sandbox"])), "danger-full-access") {
		return true
	}
	if policy := extractMap(codex["turn_sandbox_policy"]); strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", policy["type"])), "dangerFullAccess") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", raw["codex_thread_sandbox"])), "danger-full-access") {
		return true
	}
	if policy := extractMap(raw["codex_turn_sandbox_policy"]); strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", policy["type"])), "dangerFullAccess") {
		return true
	}
	return false
}

func extractMap(raw interface{}) map[string]interface{} {
	switch typed := raw.(type) {
	case map[string]interface{}:
		return typed
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, value := range typed {
			out[fmt.Sprint(key)] = value
		}
		return out
	default:
		return nil
	}
}

func detectWorkflowAdvisories(cfg Config, prompt string, raw map[string]interface{}) []WorkflowAdvisory {
	advisories := make([]WorkflowAdvisory, 0, 4)
	hasLegacySandboxKeys := rawWorkflowHasLegacySandboxKeys(raw)
	if hasLegacySandboxKeys {
		advisories = append(advisories, WorkflowAdvisory{
			Code:        WorkflowAdvisoryPermissions,
			Message:     "Legacy sandbox keys in WORKFLOW.md are ignored. Maestro now resolves execution permissions from the project or issue permission profile in the database.",
			Remediation: "Remove legacy sandbox settings from WORKFLOW.md and set the project or issue permission profile to the access level the run requires before starting the agent.",
		})
	}
	if hasLegacySandboxKeys && workflowApprovalPolicyBlocksInteractiveRecovery(cfg) {
		advisories = append(advisories, WorkflowAdvisory{
			Code:        WorkflowAdvisoryApprovalPolicy,
			Message:     "This workflow disables interactive approvals with codex.approval_policy=never while also carrying ignored legacy sandbox settings. If the project or issue permission profile does not already grant the access the task needs, the run can dead-end on sandbox or permission blockers.",
			Remediation: "Either keep approval_policy=never and make sure the project or issue permission profile already grants the required access, or switch to a non-never approval policy if you want the agent to recover through user-approved permission escalations.",
		})
	}
	if workflowPlanModeBlocksInteractiveRecovery(cfg) {
		advisories = append(advisories, WorkflowAdvisory{
			Code:        WorkflowAdvisoryPlanApprovalPolicy,
			Message:     "This workflow starts app_server threads in plan mode but still uses codex.approval_policy=never. Plan turns can pause on <proposed_plan>, but they cannot ask clarifying questions or request approvals interactively until the approval policy allows it.",
			Remediation: "Use approval_policy=on-request for plan-gated runs, or switch initial_collaboration_mode back to default when you want unattended execution-first runs.",
		})
	}
	if cfg.Phases.Done.Enabled && workflowUsesLegacyBranchInstructions(prompt, cfg.Phases.Done.Prompt) {
		advisories = append(advisories, WorkflowAdvisory{
			Code:        WorkflowAdvisoryPromptBranching,
			Message:     "The workflow prompt still tells agents to create or replace issue branches manually or merge through hard-coded mainline branches. Maestro already prepares the issue workspace branch and the repository default branch is not always main.",
			Remediation: "Update WORKFLOW.md to use the branch already prepared by Maestro and describe finalization in terms of the repository default branch instead of hard-coded branch names.",
		})
	}
	return advisories
}

func rawWorkflowHasLegacySandboxKeys(raw map[string]interface{}) bool {
	if raw == nil {
		return false
	}
	codex := extractMap(raw["codex"])
	if _, ok := codex["thread_sandbox"]; ok {
		return true
	}
	if _, ok := codex["turn_sandbox_policy"]; ok {
		return true
	}
	if _, ok := raw["codex_thread_sandbox"]; ok {
		return true
	}
	if _, ok := raw["codex_turn_sandbox_policy"]; ok {
		return true
	}
	return false
}

func workflowApprovalPolicyBlocksInteractiveRecovery(cfg Config) bool {
	if strings.TrimSpace(cfg.Agent.Mode) != AgentModeAppServer {
		return false
	}
	policy, ok := cfg.Codex.ApprovalPolicy.(string)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(policy), "never")
}

func workflowPlanModeBlocksInteractiveRecovery(cfg Config) bool {
	if strings.TrimSpace(cfg.Agent.Mode) != AgentModeAppServer {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Codex.InitialCollaborationMode), InitialCollaborationModePlan) {
		return false
	}
	policy, ok := cfg.Codex.ApprovalPolicy.(string)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(policy), "never")
}

func workflowUsesLegacyBranchInstructions(prompt, donePrompt string) bool {
	combined := strings.ToLower(strings.TrimSpace(prompt + "\n" + donePrompt))
	if combined == "" {
		return false
	}
	legacyFragments := []string{
		"create a dedicated issue branch before editing",
		"use maestro/{{ issue.identifier }}",
		"sync origin/main",
		"merge the issue branch into local main",
		"rerun the relevant validation on main",
		"push main to origin",
		"create a new branch from origin/main",
	}
	for _, fragment := range legacyFragments {
		if strings.Contains(combined, fragment) {
			return true
		}
	}
	return false
}

func validateConfig(c *Config) error {
	if strings.TrimSpace(c.Tracker.Kind) != TrackerKindKanban {
		return fmt.Errorf("unsupported tracker.kind %q", strings.TrimSpace(c.Tracker.Kind))
	}
	if strings.TrimSpace(c.Agent.Mode) != AgentModeAppServer && strings.TrimSpace(c.Agent.Mode) != AgentModeStdio {
		return fmt.Errorf("unsupported agent.mode %q", c.Agent.Mode)
	}
	dispatchMode := strings.TrimSpace(c.Agent.DispatchMode)
	if dispatchMode != DispatchModeParallel && dispatchMode != DispatchModePerProjectSerial {
		return fmt.Errorf("unsupported agent.dispatch_mode %q", c.Agent.DispatchMode)
	}
	if strings.TrimSpace(c.Codex.Command) == "" {
		return fmt.Errorf("codex.command is required")
	}
	if err := validateApprovalPolicyValue(c.Codex.ApprovalPolicy); err != nil {
		return err
	}
	switch c.Codex.InitialCollaborationMode {
	case InitialCollaborationModePlan, InitialCollaborationModeDefault:
	case "":
		return fmt.Errorf("codex.initial_collaboration_mode is required")
	default:
		return fmt.Errorf("unsupported codex.initial_collaboration_mode %q", c.Codex.InitialCollaborationMode)
	}
	for _, prompt := range []string{strings.TrimSpace(c.Phases.Review.Prompt), strings.TrimSpace(c.Phases.Done.Prompt)} {
		if prompt == "" {
			continue
		}
		if _, err := ParseLiquidTemplate(prompt); err != nil {
			return fmt.Errorf("template_parse_error: %w", err)
		}
	}
	return nil
}

func validateApprovalPolicyValue(value interface{}) error {
	if value == nil {
		return fmt.Errorf("codex.approval_policy is required")
	}

	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return fmt.Errorf("codex.approval_policy is required")
		}
		if _, ok := canonicalApprovalPolicyString(trimmed); !ok {
			return fmt.Errorf("unsupported codex.approval_policy %q", typed)
		}
		return nil
	case map[string]interface{}, map[interface{}]interface{}:
		return nil
	default:
		return fmt.Errorf("unsupported codex.approval_policy type %T", value)
	}
}

func normalizeInitialCollaborationMode(raw string) string {
	return strings.TrimSpace(strings.ToLower(raw))
}

func resolvePathValue(baseDir, raw, fallback string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = fallback
	}
	value = expandPathValue(value)
	if strings.HasPrefix(value, "$") {
		return filepath.Clean(value)
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}

func resolveWorkspaceRootPath(baseDir, raw string) (string, error) {
	value := resolvePathValue(baseDir, raw, DefaultConfig().Workspace.Root)
	// Reject unresolved env segments so workspace bootstrap never creates literal "$VAR" directories.
	if hasUnresolvedPathSegment(value) {
		return "", fmt.Errorf("workspace.root contains unresolved environment variable in %q", value)
	}
	return value, nil
}

func hasUnresolvedPathSegment(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" || cleaned == "." {
		return false
	}
	for _, segment := range strings.Split(cleaned, string(filepath.Separator)) {
		if strings.HasPrefix(segment, "$") {
			return true
		}
	}
	return false
}

func expandPathValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}

	if strings.HasPrefix(value, "$") {
		value = os.Expand(value, func(name string) string {
			resolved, ok := os.LookupEnv(name)
			if !ok || strings.TrimSpace(resolved) == "" {
				return "$" + name
			}
			return resolved
		})
	}

	if strings.HasPrefix(value, "~") {
		home, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(home) != "" {
			switch {
			case value == "~":
				return home
			case strings.HasPrefix(value, "~/"):
				return filepath.Join(home, value[2:])
			}
		}
	}

	return value
}

func hashContent(data []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(data)
	return h.Sum64()
}

func currentStamp(path string) (fileStamp, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{
		ModTime: stat.ModTime().UnixNano(),
		Size:    stat.Size(),
		Hash:    hashContent(data),
	}, nil
}
