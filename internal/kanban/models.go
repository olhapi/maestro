package kanban

import (
	"strings"
	"time"
)

// State represents the workflow state of an issue
type State string
type ProjectState string

const (
	StateBacklog    State = "backlog"
	StateReady      State = "ready"
	StateInProgress State = "in_progress"
	StateInReview   State = "in_review"
	StateDone       State = "done"
	StateCancelled  State = "cancelled"

	ProjectStateStopped ProjectState = "stopped"
	ProjectStateRunning ProjectState = "running"
)

type WorkflowPhase string
type IssueType string

const (
	ProviderKindKanban = "kanban"
	ProviderKindLinear = "linear"

	WorkflowPhaseImplementation WorkflowPhase = "implementation"
	WorkflowPhaseReview         WorkflowPhase = "review"
	WorkflowPhaseDone           WorkflowPhase = "done"
	WorkflowPhaseComplete       WorkflowPhase = "complete"

	IssueTypeStandard  IssueType = "standard"
	IssueTypeRecurring IssueType = "recurring"
)

// IsValid checks if a state is valid
func (s State) IsValid() bool {
	switch s {
	case StateBacklog, StateReady, StateInProgress, StateInReview, StateDone, StateCancelled:
		return true
	default:
		return false
	}
}

func (s ProjectState) IsValid() bool {
	switch s {
	case ProjectStateStopped, ProjectStateRunning:
		return true
	default:
		return false
	}
}

func (p WorkflowPhase) IsValid() bool {
	switch p {
	case WorkflowPhaseImplementation, WorkflowPhaseReview, WorkflowPhaseDone, WorkflowPhaseComplete:
		return true
	default:
		return false
	}
}

func (t IssueType) IsValid() bool {
	switch t {
	case IssueTypeStandard, IssueTypeRecurring:
		return true
	default:
		return false
	}
}

func DefaultWorkflowPhaseForState(state State) WorkflowPhase {
	switch state {
	case StateDone, StateCancelled:
		return WorkflowPhaseComplete
	default:
		return WorkflowPhaseImplementation
	}
}

func DefaultIssueType() IssueType {
	return IssueTypeStandard
}

func NormalizeProjectState(raw string) ProjectState {
	switch ProjectState(strings.ToLower(strings.TrimSpace(raw))) {
	case ProjectStateRunning:
		return ProjectStateRunning
	default:
		return ProjectStateStopped
	}
}

func ParseIssueType(raw string) (IssueType, error) {
	issueType := IssueType(strings.ToLower(strings.TrimSpace(raw)))
	if issueType == "" {
		return IssueTypeStandard, nil
	}
	if !issueType.IsValid() {
		return "", invalidIssueTypeError(issueType)
	}
	return issueType, nil
}

func NormalizeIssueType(raw string) IssueType {
	switch IssueType(strings.ToLower(strings.TrimSpace(raw))) {
	case IssueTypeRecurring:
		return IssueTypeRecurring
	default:
		return IssueTypeStandard
	}
}

// ActiveStates returns states that should be processed by the orchestrator
func ActiveStates() []State {
	return []State{StateReady, StateInProgress, StateInReview}
}

// TerminalStates return states that indicate work is complete
func TerminalStates() []State {
	return []State{StateDone, StateCancelled}
}

// Project represents a top-level project/container
type Project struct {
	ID                 string                 `json:"id"`
	Name               string                 `json:"name"`
	Description        string                 `json:"description,omitempty"`
	State              ProjectState           `json:"state"`
	RepoPath           string                 `json:"repo_path,omitempty"`
	WorkflowPath       string                 `json:"workflow_path,omitempty"`
	ProviderKind       string                 `json:"provider_kind,omitempty"`
	ProviderProjectRef string                 `json:"provider_project_ref,omitempty"`
	ProviderConfig     map[string]interface{} `json:"provider_config,omitempty"`
	Capabilities       ProviderCapabilities   `json:"capabilities"`
	OrchestrationReady bool                   `json:"orchestration_ready"`
	DispatchReady      bool                   `json:"dispatch_ready"`
	DispatchError      string                 `json:"dispatch_error,omitempty"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
}

// Epic represents a collection of related issues
type Epic struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Issue represents a single work item
type Issue struct {
	ID               string        `json:"id"`
	ProjectID        string        `json:"project_id,omitempty"`
	EpicID           string        `json:"epic_id,omitempty"`
	Identifier       string        `json:"identifier"` // Human-readable: PROJ-123
	IssueType        IssueType     `json:"issue_type"`
	ProviderKind     string        `json:"provider_kind,omitempty"`
	ProviderIssueRef string        `json:"provider_issue_ref,omitempty"`
	ProviderShadow   bool          `json:"provider_shadow,omitempty"`
	Title            string        `json:"title"`
	Description      string        `json:"description,omitempty"`
	State            State         `json:"state"`
	WorkflowPhase    WorkflowPhase `json:"workflow_phase"`
	Priority         int           `json:"priority,omitempty"` // Lower = higher priority
	Labels           []string      `json:"labels,omitempty"`
	AgentName        string        `json:"agent_name,omitempty"`
	AgentPrompt      string        `json:"agent_prompt,omitempty"`
	BranchName       string        `json:"branch_name,omitempty"`
	PRURL            string        `json:"pr_url,omitempty"`
	BlockedBy        []string      `json:"blocked_by,omitempty"` // Issue identifiers
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	TotalTokensSpent int           `json:"total_tokens_spent"`
	StartedAt        *time.Time    `json:"started_at,omitempty"`
	CompletedAt      *time.Time    `json:"completed_at,omitempty"`
	LastSyncedAt     *time.Time    `json:"last_synced_at,omitempty"`
	Cron             string        `json:"cron,omitempty"`
	Enabled          bool          `json:"enabled"`
	NextRunAt        *time.Time    `json:"next_run_at,omitempty"`
	LastEnqueuedAt   *time.Time    `json:"last_enqueued_at,omitempty"`
	PendingRerun     bool          `json:"pending_rerun"`
	ResumeThreadID   string        `json:"-"`
}

func (i Issue) IsRecurring() bool {
	return NormalizeIssueType(string(i.IssueType)) == IssueTypeRecurring
}

type IssueRecurrence struct {
	IssueID        string     `json:"issue_id,omitempty"`
	Cron           string     `json:"cron"`
	Enabled        bool       `json:"enabled"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	LastEnqueuedAt *time.Time `json:"last_enqueued_at,omitempty"`
	PendingRerun   bool       `json:"pending_rerun"`
	CreatedAt      time.Time  `json:"created_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at,omitempty"`
}

type IssueImage struct {
	ID          string    `json:"id"`
	IssueID     string    `json:"issue_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	ByteSize    int64     `json:"byte_size"`
	StoragePath string    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Workspace represents an isolated working directory for an issue
type Workspace struct {
	IssueID   string     `json:"issue_id"`
	Path      string     `json:"path"`
	CreatedAt time.Time  `json:"created_at"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	RunCount  int        `json:"run_count"`
}

type StoreIdentity struct {
	DBPath  string `json:"db_path"`
	StoreID string `json:"store_id"`
}

type ProviderCapabilities struct {
	Projects         bool `json:"projects"`
	Epics            bool `json:"epics"`
	Issues           bool `json:"issues"`
	IssueStateUpdate bool `json:"issue_state_update"`
	IssueDelete      bool `json:"issue_delete"`
}

func DefaultCapabilities(kind string) ProviderCapabilities {
	switch strings.TrimSpace(kind) {
	case ProviderKindLinear:
		return ProviderCapabilities{
			Projects:         true,
			Epics:            false,
			Issues:           true,
			IssueStateUpdate: true,
			IssueDelete:      true,
		}
	default:
		return ProviderCapabilities{
			Projects:         true,
			Epics:            true,
			Issues:           true,
			IssueStateUpdate: true,
			IssueDelete:      true,
		}
	}
}
