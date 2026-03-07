package kanban

import "time"

// State represents the workflow state of an issue
type State string

const (
	StateBacklog    State = "backlog"
	StateReady      State = "ready"
	StateInProgress State = "in_progress"
	StateInReview   State = "in_review"
	StateDone       State = "done"
	StateCancelled  State = "cancelled"
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
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id,omitempty"`
	EpicID      string    `json:"epic_id,omitempty"`
	Identifier  string    `json:"identifier"` // Human-readable: PROJ-123
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	State       State     `json:"state"`
	Priority    int       `json:"priority,omitempty"` // Lower = higher priority
	Labels      []string  `json:"labels,omitempty"`
	BranchName  string    `json:"branch_name,omitempty"`
	PRNumber    int       `json:"pr_number,omitempty"`
	PRURL       string    `json:"pr_url,omitempty"`
	BlockedBy   []string  `json:"blocked_by,omitempty"` // Issue identifiers
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Workspace represents an isolated working directory for an issue
type Workspace struct {
	IssueID    string    `json:"issue_id"`
	Path       string    `json:"path"`
	CreatedAt  time.Time `json:"created_at"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	RunCount   int       `json:"run_count"`
}
