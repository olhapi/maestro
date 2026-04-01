package config

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/olhapi/maestro/internal/codexschema"
	"gopkg.in/yaml.v3"
)

const (
	TrackerKindKanban               = "kanban"
	AgentModeAppServer              = "app_server"
	AgentModeStdio                  = "stdio"
	DispatchModeParallel            = "parallel"
	DispatchModePerProjectSerial    = "per_project_serial"
	InitialCollaborationModePlan    = "plan"
	InitialCollaborationModeDefault = "default"
	DefaultWorkspaceBranchPrefix    = "maestro/"
)

var (
	ErrMissingWorkflowFile = errors.New("missing_workflow_file")
	ErrWorkflowParse       = errors.New("workflow_parse_error")
	ErrWorkflowFrontMatter = errors.New("workflow_front_matter_not_a_map")
)

type Config struct {
	Tracker      TrackerConfig      `yaml:"tracker"`
	Polling      PollingConfig      `yaml:"polling"`
	Workspace    WorkspaceConfig    `yaml:"workspace"`
	Hooks        HooksConfig        `yaml:"hooks"`
	Orchestrator OrchestratorConfig `yaml:"orchestrator"`
	Runtime      RuntimeCatalog     `yaml:"runtime"`
	Phases       PhasesConfig       `yaml:"phases"`

	// Derived fields used by the rest of the codebase during the migration.
	Agent AgentConfig `yaml:"-"`
	Codex CodexConfig `yaml:"-"`
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
	Root         string `yaml:"root"`
	BranchPrefix string `yaml:"branch_prefix"`
}

type HooksConfig struct {
	AfterCreate  string `yaml:"after_create"`
	BeforeRun    string `yaml:"before_run"`
	AfterRun     string `yaml:"after_run"`
	BeforeRemove string `yaml:"before_remove"`
	TimeoutMs    int    `yaml:"timeout_ms"`
}

type OrchestratorConfig struct {
	MaxConcurrentAgents int    `yaml:"max_concurrent_agents"`
	MaxTurns            int    `yaml:"max_turns"`
	MaxRetryBackoffMs   int    `yaml:"max_retry_backoff_ms"`
	MaxAutomaticRetries int    `yaml:"max_automatic_retries"`
	Mode                string `yaml:"mode"`
	DispatchMode        string `yaml:"dispatch_mode"`
}

type RuntimeConfig struct {
	Provider                 string      `yaml:"provider"`
	Transport                string      `yaml:"transport"`
	Command                  string      `yaml:"command"`
	ExpectedVersion          string      `yaml:"expected_version"`
	ApprovalPolicy           interface{} `yaml:"approval_policy"`
	InitialCollaborationMode string      `yaml:"initial_collaboration_mode"`
	TurnTimeoutMs            int         `yaml:"turn_timeout_ms"`
	ReadTimeoutMs            int         `yaml:"read_timeout_ms"`
	StallTimeoutMs           int         `yaml:"stall_timeout_ms"`
}

type RuntimeCatalog struct {
	Default string                   `yaml:"default"`
	Entries map[string]RuntimeConfig `yaml:",inline"`
}

type AgentConfig = OrchestratorConfig

type CodexConfig = RuntimeConfig

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
}

type workflowPayload struct {
	Config Config
	Prompt string
}

type fileStamp struct {
	ModTime int64
	Size    int64
	Hash    uint64
}

func DefaultConfig() Config {
	runtime := defaultRuntimeCatalog()
	orchestrator := defaultOrchestratorConfig()
	cfg := Config{
		Tracker: TrackerConfig{
			Kind:           TrackerKindKanban,
			ActiveStates:   []string{"ready", "in_progress", "in_review"},
			TerminalStates: []string{"done", "cancelled"},
		},
		Polling: PollingConfig{IntervalMs: 10000},
		Workspace: WorkspaceConfig{
			Root:         "~/.maestro/worktrees",
			BranchPrefix: DefaultWorkspaceBranchPrefix,
		},
		Hooks:        HooksConfig{TimeoutMs: 60000},
		Orchestrator: orchestrator,
		Runtime:      runtime,
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
	cfg.applyDerivedRuntimeFields()
	return cfg
}

func DefaultInitConfig() Config {
	cfg := DefaultConfig()
	if runtime, ok := cfg.Runtime.Entries["codex-appserver"]; ok {
		runtime.ApprovalPolicy = "never"
		cfg.Runtime.Entries["codex-appserver"] = runtime
	}
	cfg.Runtime.Default = "codex-appserver"
	cfg.applyDerivedRuntimeFields()
	return cfg
}

func defaultOrchestratorConfig() OrchestratorConfig {
	return OrchestratorConfig{
		MaxConcurrentAgents: 3,
		MaxTurns:            4,
		MaxRetryBackoffMs:   60000,
		MaxAutomaticRetries: 8,
		Mode:                AgentModeAppServer,
		DispatchMode:        DispatchModeParallel,
	}
}

func defaultRuntimeCatalog() RuntimeCatalog {
	runtime := RuntimeCatalog{
		Default: "codex-appserver",
		Entries: map[string]RuntimeConfig{
			"codex-appserver": {
				Provider:        "codex",
				Transport:       AgentModeAppServer,
				Command:         "codex app-server",
				ExpectedVersion: codexschema.SupportedVersion,
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
			"codex-stdio": {
				Provider:        "codex",
				Transport:       AgentModeStdio,
				Command:         "codex exec",
				ExpectedVersion: codexschema.SupportedVersion,
				ApprovalPolicy:  "never",
				TurnTimeoutMs:   1800000,
				ReadTimeoutMs:   10000,
				StallTimeoutMs:  300000,
			},
			"claude": {
				Provider:       "claude",
				Transport:      AgentModeStdio,
				Command:        "claude",
				ApprovalPolicy: "never",
				TurnTimeoutMs:  1800000,
				ReadTimeoutMs:  10000,
				StallTimeoutMs: 300000,
			},
		},
	}
	return runtime
}

func (c *Config) applyDerivedRuntimeFields() {
	if c == nil {
		return
	}
	c.Orchestrator = normalizeOrchestratorConfig(c.Orchestrator)
	c.Runtime = normalizeRuntimeCatalog(c.Runtime)
	name, runtime := c.Runtime.defaultRuntime()
	if name == "" {
		name, runtime = defaultRuntimeCatalog().defaultRuntime()
	}
	c.Runtime.Default = name
	c.Codex = runtime
	c.Orchestrator.Mode = runtime.Transport
	c.Agent = AgentConfig{
		MaxConcurrentAgents: c.Orchestrator.MaxConcurrentAgents,
		MaxTurns:            c.Orchestrator.MaxTurns,
		MaxRetryBackoffMs:   c.Orchestrator.MaxRetryBackoffMs,
		MaxAutomaticRetries: c.Orchestrator.MaxAutomaticRetries,
		Mode:                runtime.Transport,
		DispatchMode:        c.Orchestrator.DispatchMode,
	}
}

func normalizeOrchestratorConfig(cfg OrchestratorConfig) OrchestratorConfig {
	defaults := defaultOrchestratorConfig()
	if cfg.MaxConcurrentAgents <= 0 {
		cfg.MaxConcurrentAgents = defaults.MaxConcurrentAgents
	}
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = defaults.MaxTurns
	}
	if cfg.MaxRetryBackoffMs <= 0 {
		cfg.MaxRetryBackoffMs = defaults.MaxRetryBackoffMs
	}
	if cfg.MaxAutomaticRetries <= 0 {
		cfg.MaxAutomaticRetries = defaults.MaxAutomaticRetries
	}
	if strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = defaults.Mode
	}
	if strings.TrimSpace(cfg.DispatchMode) == "" {
		cfg.DispatchMode = defaults.DispatchMode
	}
	return cfg
}

func normalizeRuntimeCatalog(catalog RuntimeCatalog) RuntimeCatalog {
	defaults := defaultRuntimeCatalog()
	out := RuntimeCatalog{
		Default: strings.TrimSpace(catalog.Default),
		Entries: make(map[string]RuntimeConfig, len(defaults.Entries)),
	}
	if len(catalog.Entries) == 0 {
		for name, cfg := range defaults.Entries {
			out.Entries[name] = cfg
		}
	} else {
		for name, cfg := range catalog.Entries {
			out.Entries[strings.TrimSpace(name)] = cfg
		}
		for name, cfg := range defaults.Entries {
			current, ok := out.Entries[name]
			if !ok {
				out.Entries[name] = cfg
				continue
			}
			out.Entries[name] = normalizeRuntimeConfig(current, cfg)
		}
	}
	for name, cfg := range out.Entries {
		out.Entries[name] = normalizeRuntimeConfig(cfg, defaults.Entries[name])
	}
	if out.Default == "" || !out.hasEntry(out.Default) {
		out.Default = out.preferredDefaultName()
	}
	return out
}

func normalizeRuntimeConfig(cfg RuntimeConfig, defaults RuntimeConfig) RuntimeConfig {
	if strings.TrimSpace(cfg.Provider) == "" {
		cfg.Provider = defaults.Provider
	}
	if strings.TrimSpace(cfg.Transport) == "" {
		cfg.Transport = defaults.Transport
	}
	if strings.TrimSpace(cfg.Command) == "" {
		cfg.Command = defaults.Command
	}
	if strings.TrimSpace(cfg.ExpectedVersion) == "" {
		cfg.ExpectedVersion = defaults.ExpectedVersion
	}
	normalizedApprovalPolicy, err := normalizeApprovalPolicyValue(cfg.ApprovalPolicy, cfg.ApprovalPolicy != nil)
	if err != nil {
		normalizedApprovalPolicy = cfg.ApprovalPolicy
	}
	if normalizedApprovalPolicy == nil {
		cfg.ApprovalPolicy = defaults.ApprovalPolicy
	} else {
		cfg.ApprovalPolicy = normalizedApprovalPolicy
	}
	if strings.TrimSpace(cfg.InitialCollaborationMode) == "" {
		cfg.InitialCollaborationMode = defaults.InitialCollaborationMode
	}
	if cfg.TurnTimeoutMs <= 0 {
		cfg.TurnTimeoutMs = defaults.TurnTimeoutMs
	}
	if cfg.ReadTimeoutMs <= 0 {
		cfg.ReadTimeoutMs = defaults.ReadTimeoutMs
	}
	if cfg.StallTimeoutMs <= 0 {
		cfg.StallTimeoutMs = defaults.StallTimeoutMs
	}
	return cfg
}

func (catalog RuntimeCatalog) hasEntry(name string) bool {
	if catalog.Entries == nil {
		return false
	}
	_, ok := catalog.Entries[strings.TrimSpace(name)]
	return ok
}

func (catalog RuntimeCatalog) defaultRuntime() (string, RuntimeConfig) {
	if catalog.Entries == nil {
		return "", RuntimeConfig{}
	}
	if name := strings.TrimSpace(catalog.Default); name != "" {
		if runtime, ok := catalog.Entries[name]; ok {
			return name, runtime
		}
	}
	for _, preferred := range []string{"codex-appserver", "codex-stdio", "claude"} {
		if runtime, ok := catalog.Entries[preferred]; ok {
			return preferred, runtime
		}
	}
	names := make([]string, 0, len(catalog.Entries))
	for name := range catalog.Entries {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", RuntimeConfig{}
	}
	name := names[0]
	return name, catalog.Entries[name]
}

func (catalog RuntimeCatalog) preferredDefaultName() string {
	name, _ := catalog.defaultRuntime()
	return name
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
- If you find meaningful out-of-scope work, file a separate Maestro issue instead of expanding scope. Include a clear title, description, and acceptance criteria; place it in Backlog; use the same project; link the current issue; and add a blocker relation when needed.
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

Review the implementation in the current issue workspace, run focused verification, and fix any issues you find.

- If additional implementation is still required after review, move the issue back to in_progress.
- If the issue is ready to finalize, move it to done.
`)
}

func DefaultInitReviewPromptTemplate() string {
	return strings.TrimSpace(`
Review the implementation for issue {{ issue.identifier }} in the current issue workspace.
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

The done phase owns merge-back and finalization for this issue from the current issue workspace. Maestro handles preview publication, cleanup hooks, and worktree removal after your run exits.

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
Finalize issue {{ issue.identifier }} from the current issue workspace.
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
	}, nil
}

func parseWorkflowPayload(path, content string) (*workflowPayload, error) {
	raw, promptStart, err := parseWorkflowFrontMatter(content)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if len(raw) > 0 {
		encoded, err := yaml.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
		}
		dec := yaml.NewDecoder(strings.NewReader(string(encoded)))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
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
	cfg.applyDerivedRuntimeFields()
	return &workflowPayload{Config: cfg, Prompt: prompt}, nil
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
	normalized, err := coerceWorkflowFrontMatter(raw)
	if err != nil {
		return nil, 0, err
	}
	return normalized, promptStart, nil
}

func coerceWorkflowFrontMatter(raw interface{}) (map[string]interface{}, error) {
	if raw == nil {
		return map[string]interface{}{}, nil
	}
	switch typed := raw.(type) {
	case map[string]interface{}:
		return coerceWorkflowMap(typed), nil
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, value := range typed {
			out[fmt.Sprint(key)] = coerceWorkflowValue(value)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: front matter must be a map, got %T", ErrWorkflowFrontMatter, raw)
	}
}

func coerceWorkflowMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = coerceWorkflowValue(value)
	}
	return out
}

func coerceWorkflowValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return coerceWorkflowMap(typed)
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			out[fmt.Sprint(key)] = coerceWorkflowValue(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, child := range typed {
			out[i] = coerceWorkflowValue(child)
		}
		return out
	default:
		return value
	}
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
		return coerceWorkflowMap(typed), nil
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			out[fmt.Sprint(key)] = coerceWorkflowValue(child)
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
	if strings.TrimSpace(c.Workspace.BranchPrefix) == "" {
		c.Workspace.BranchPrefix = defaults.Workspace.BranchPrefix
	}
	if c.Orchestrator.MaxConcurrentAgents <= 0 {
		c.Orchestrator.MaxConcurrentAgents = defaults.Orchestrator.MaxConcurrentAgents
	}
	if c.Orchestrator.MaxTurns <= 0 {
		c.Orchestrator.MaxTurns = defaults.Orchestrator.MaxTurns
	}
	if c.Orchestrator.MaxRetryBackoffMs <= 0 {
		c.Orchestrator.MaxRetryBackoffMs = defaults.Orchestrator.MaxRetryBackoffMs
	}
	if c.Orchestrator.MaxAutomaticRetries <= 0 {
		c.Orchestrator.MaxAutomaticRetries = defaults.Orchestrator.MaxAutomaticRetries
	}
	if strings.TrimSpace(c.Orchestrator.Mode) == "" {
		c.Orchestrator.Mode = defaults.Orchestrator.Mode
	}
	if strings.TrimSpace(c.Orchestrator.DispatchMode) == "" {
		c.Orchestrator.DispatchMode = defaults.Orchestrator.DispatchMode
	}
	if c.Phases.Review.Enabled && strings.TrimSpace(c.Phases.Review.Prompt) == "" {
		c.Phases.Review.Prompt = DefaultReviewPromptTemplate()
	}
	if c.Phases.Done.Enabled && strings.TrimSpace(c.Phases.Done.Prompt) == "" {
		c.Phases.Done.Prompt = DefaultDonePromptTemplate()
	}
	c.applyDerivedRuntimeFields()
	return nil
}

func validateConfig(c *Config) error {
	if strings.TrimSpace(c.Tracker.Kind) != TrackerKindKanban {
		return fmt.Errorf("unsupported tracker.kind %q", strings.TrimSpace(c.Tracker.Kind))
	}
	if strings.TrimSpace(c.Workspace.Root) == "" {
		return fmt.Errorf("workspace.root is required")
	}
	if strings.TrimSpace(c.Workspace.BranchPrefix) == "" {
		return fmt.Errorf("workspace.branch_prefix is required")
	}
	dispatchMode := strings.TrimSpace(c.Orchestrator.DispatchMode)
	if dispatchMode != DispatchModeParallel && dispatchMode != DispatchModePerProjectSerial {
		return fmt.Errorf("unsupported orchestrator.dispatch_mode %q", c.Orchestrator.DispatchMode)
	}
	if c.Runtime.Entries == nil || len(c.Runtime.Entries) == 0 {
		return fmt.Errorf("runtime is required")
	}
	if strings.TrimSpace(c.Runtime.Default) == "" {
		return fmt.Errorf("runtime.default is required")
	}
	runtime, ok := c.Runtime.Entries[strings.TrimSpace(c.Runtime.Default)]
	if !ok {
		return fmt.Errorf("runtime.default %q does not match a runtime entry", c.Runtime.Default)
	}
	if err := validateRuntimeConfig(runtime); err != nil {
		return err
	}
	for name, runtime := range c.Runtime.Entries {
		if err := validateRuntimeEntry(name, runtime); err != nil {
			return err
		}
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

func validateRuntimeEntry(name string, runtime RuntimeConfig) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("runtime entry name is required")
	}
	if strings.TrimSpace(runtime.Provider) == "" {
		return fmt.Errorf("runtime.%s.provider is required", name)
	}
	if strings.TrimSpace(runtime.Transport) != AgentModeAppServer && strings.TrimSpace(runtime.Transport) != AgentModeStdio {
		return fmt.Errorf("unsupported runtime.%s.transport %q", name, runtime.Transport)
	}
	if strings.TrimSpace(runtime.Command) == "" {
		return fmt.Errorf("runtime.%s.command is required", name)
	}
	if err := validateApprovalPolicyValue(runtime.ApprovalPolicy); err != nil {
		return fmt.Errorf("runtime.%s.%w", name, err)
	}
	if strings.TrimSpace(runtime.InitialCollaborationMode) != "" {
		switch normalizeInitialCollaborationMode(runtime.InitialCollaborationMode) {
		case InitialCollaborationModePlan, InitialCollaborationModeDefault:
		default:
			return fmt.Errorf("unsupported runtime.%s.initial_collaboration_mode %q", name, runtime.InitialCollaborationMode)
		}
	}
	return nil
}

func validateRuntimeConfig(runtime RuntimeConfig) error {
	return validateRuntimeEntry("default", runtime)
}

func validateApprovalPolicyValue(value interface{}) error {
	if value == nil {
		return fmt.Errorf("runtime.approval_policy is required")
	}

	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return fmt.Errorf("runtime.approval_policy is required")
		}
		if _, ok := canonicalApprovalPolicyString(trimmed); !ok {
			return fmt.Errorf("unsupported runtime.approval_policy %q", typed)
		}
		return nil
	case map[string]interface{}, map[interface{}]interface{}:
		return nil
	default:
		return fmt.Errorf("unsupported runtime.approval_policy type %T", value)
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
