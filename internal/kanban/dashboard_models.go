package kanban

import (
	"sort"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
)

type IssueStateCounts struct {
	Backlog    int `json:"backlog"`
	Ready      int `json:"ready"`
	InProgress int `json:"in_progress"`
	InReview   int `json:"in_review"`
	Done       int `json:"done"`
	Cancelled  int `json:"cancelled"`
}

type StateBucket struct {
	State      string `json:"state"`
	Count      int    `json:"count"`
	IsActive   bool   `json:"is_active,omitempty"`
	IsTerminal bool   `json:"is_terminal,omitempty"`
}

func (c *IssueStateCounts) Add(state State) {
	c.AddCount(state, 1)
}

func (c *IssueStateCounts) AddCount(state State, count int) {
	if count <= 0 {
		return
	}
	switch state {
	case StateBacklog:
		c.Backlog += count
	case StateReady:
		c.Ready += count
	case StateInProgress:
		c.InProgress += count
	case StateInReview:
		c.InReview += count
	case StateDone:
		c.Done += count
	case StateCancelled:
		c.Cancelled += count
	}
}

func (c IssueStateCounts) Total() int {
	return c.Backlog + c.Ready + c.InProgress + c.InReview + c.Done + c.Cancelled
}

func (c IssueStateCounts) Active() int {
	return c.Ready + c.InProgress + c.InReview
}

type ProjectSummary struct {
	Project
	TotalTokensSpent int              `json:"total_tokens_spent"`
	Counts           IssueStateCounts `json:"counts"`
	StateBuckets     []StateBucket    `json:"state_buckets,omitempty"`
	TotalCount       int              `json:"total_count"`
	ActiveCount      int              `json:"active_count"`
	TerminalCount    int              `json:"terminal_count"`
}

type EpicSummary struct {
	Epic
	ProjectName   string           `json:"project_name,omitempty"`
	Counts        IssueStateCounts `json:"counts"`
	StateBuckets  []StateBucket    `json:"state_buckets,omitempty"`
	TotalCount    int              `json:"total_count"`
	ActiveCount   int              `json:"active_count"`
	TerminalCount int              `json:"terminal_count"`
}

type IssueSummary struct {
	Issue
	ProjectName       string     `json:"project_name,omitempty"`
	EpicName          string     `json:"epic_name,omitempty"`
	WorkspacePath     string     `json:"workspace_path,omitempty"`
	WorkspaceRunCount int        `json:"workspace_run_count"`
	WorkspaceLastRun  *time.Time `json:"workspace_last_run,omitempty"`
	IsBlocked         bool       `json:"is_blocked"`
}

type IssueDetail struct {
	IssueSummary
	ProjectDescription       string            `json:"project_description,omitempty"`
	EpicDescription          string            `json:"epic_description,omitempty"`
	ProjectPermissionProfile PermissionProfile `json:"project_permission_profile,omitempty"`
	Assets                   []IssueAsset      `json:"assets"`
}

type IssueQuery struct {
	ProjectID   string
	ProjectName string
	EpicID      string
	State       string
	IssueType   string
	Assignee    string
	Search      string
	Sort        string
	Blocked     *bool
	Limit       int
	Offset      int
}

func BuildStateBuckets(counts map[string]int, activeStates, terminalStates []string) []StateBucket {
	if len(counts) == 0 {
		return nil
	}
	active := make(map[string]struct{}, len(activeStates))
	for _, state := range activeStates {
		active[state] = struct{}{}
	}
	terminal := make(map[string]struct{}, len(terminalStates))
	for _, state := range terminalStates {
		terminal[state] = struct{}{}
	}
	states := make([]string, 0, len(counts))
	for state := range counts {
		states = append(states, state)
	}
	sort.Strings(states)
	buckets := make([]StateBucket, 0, len(states))
	for _, state := range states {
		_, isActive := active[state]
		_, isTerminal := terminal[state]
		buckets = append(buckets, StateBucket{
			State:      state,
			Count:      counts[state],
			IsActive:   isActive,
			IsTerminal: isTerminal,
		})
	}
	return buckets
}

func AggregateStateBuckets(buckets []StateBucket) (total, active, terminal int) {
	for _, bucket := range buckets {
		total += bucket.Count
		if bucket.IsActive {
			active += bucket.Count
		}
		if bucket.IsTerminal {
			terminal += bucket.Count
		}
	}
	return total, active, terminal
}

type RuntimeEvent struct {
	Seq          int64                  `json:"seq"`
	Kind         string                 `json:"kind"`
	IssueID      string                 `json:"issue_id,omitempty"`
	Identifier   string                 `json:"identifier,omitempty"`
	Title        string                 `json:"title,omitempty"`
	Phase        string                 `json:"phase,omitempty"`
	Attempt      int                    `json:"attempt,omitempty"`
	DelayType    string                 `json:"delay_type,omitempty"`
	InputTokens  int                    `json:"input_tokens,omitempty"`
	OutputTokens int                    `json:"output_tokens,omitempty"`
	TotalTokens  int                    `json:"total_tokens,omitempty"`
	Error        string                 `json:"error,omitempty"`
	TS           time.Time              `json:"ts"`
	Payload      map[string]interface{} `json:"payload,omitempty"`
}

type RuntimeSeriesPoint struct {
	Bucket        string `json:"bucket"`
	RunsStarted   int    `json:"runs_started"`
	RunsCompleted int    `json:"runs_completed"`
	RunsFailed    int    `json:"runs_failed"`
	Retries       int    `json:"retries"`
	Tokens        int    `json:"tokens"`
}

type IssuePlanApproval struct {
	Markdown    string    `json:"markdown"`
	RequestedAt time.Time `json:"requested_at"`
	Attempt     int       `json:"attempt"`
}

type IssuePlanRevision struct {
	Markdown    string    `json:"markdown"`
	RequestedAt time.Time `json:"requested_at"`
	Attempt     int       `json:"attempt"`
}

type IssuePlanningStatus string

const (
	IssuePlanningStatusDrafting          IssuePlanningStatus = "drafting"
	IssuePlanningStatusAwaitingApproval  IssuePlanningStatus = "awaiting_approval"
	IssuePlanningStatusRevisionRequested IssuePlanningStatus = "revision_requested"
	IssuePlanningStatusApproved          IssuePlanningStatus = "approved"
	IssuePlanningStatusAbandoned         IssuePlanningStatus = "abandoned"
)

// IssuePlanVersion records one persisted plan checkpoint in the planning lineage.
// SessionID is the durable provider session key, VersionNumber increments per revision,
// Markdown stores the canonical <proposed_plan> body, RevisionNote captures the latest
// requested revision text, and ThreadID/TurnID preserve lineage when the provider exposes it.
type IssuePlanVersion struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"session_id"`
	VersionNumber int       `json:"version_number"`
	Markdown      string    `json:"markdown"`
	RevisionNote  string    `json:"revision_note,omitempty"`
	Attempt       int       `json:"attempt,omitempty"`
	ThreadID      string    `json:"thread_id,omitempty"`
	TurnID        string    `json:"turn_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// IssuePlanning groups all plan versions for one planning session.
// SessionID is the durable provider session key that ties the lineage together.
type IssuePlanning struct {
	SessionID                  string              `json:"session_id"`
	Status                     IssuePlanningStatus `json:"status"`
	CurrentVersionNumber       int                 `json:"current_version_number"`
	CurrentVersion             *IssuePlanVersion   `json:"current_version,omitempty"`
	Versions                   []IssuePlanVersion  `json:"versions,omitempty"`
	PendingRevisionNote        string              `json:"pending_revision_note,omitempty"`
	PendingRevisionRequestedAt *time.Time          `json:"pending_revision_requested_at,omitempty"`
	OpenedAt                   time.Time           `json:"opened_at"`
	UpdatedAt                  time.Time           `json:"updated_at"`
	ClosedAt                   *time.Time          `json:"closed_at,omitempty"`
	ClosedReason               string              `json:"closed_reason,omitempty"`
}

type IssuePlanningSummary struct {
	SessionID                  string              `json:"session_id"`
	Status                     IssuePlanningStatus `json:"status"`
	CurrentVersionNumber       int                 `json:"current_version_number"`
	CurrentVersion             *IssuePlanVersion   `json:"current_version,omitempty"`
	PendingRevisionNote        string              `json:"pending_revision_note,omitempty"`
	PendingRevisionRequestedAt *time.Time          `json:"pending_revision_requested_at,omitempty"`
	OpenedAt                   time.Time           `json:"opened_at"`
	UpdatedAt                  time.Time           `json:"updated_at"`
	ClosedAt                   *time.Time          `json:"closed_at,omitempty"`
}

type WorkspaceRecovery struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type ExecutionSessionSnapshot struct {
	IssueID           string               `json:"issue_id"`
	Identifier        string               `json:"identifier"`
	Phase             string               `json:"phase,omitempty"`
	Attempt           int                  `json:"attempt"`
	RunKind           string               `json:"run_kind,omitempty"`
	RuntimeName       string               `json:"runtime_name,omitempty"`
	RuntimeProvider   string               `json:"runtime_provider,omitempty"`
	RuntimeTransport  string               `json:"runtime_transport,omitempty"`
	RuntimeAuthSource string               `json:"runtime_auth_source,omitempty"`
	Error             string               `json:"error,omitempty"`
	ResumeEligible    bool                 `json:"resume_eligible,omitempty"`
	StopReason        string               `json:"-"`
	UpdatedAt         time.Time            `json:"updated_at"`
	AppSession        agentruntime.Session `json:"session"`
}

type IssueAgentCommandStatus string

const (
	IssueAgentCommandPending           IssueAgentCommandStatus = "pending"
	IssueAgentCommandWaitingForUnblock IssueAgentCommandStatus = "waiting_for_unblock"
	IssueAgentCommandDelivered         IssueAgentCommandStatus = "delivered"
)

type IssueAgentCommand struct {
	ID               string                  `json:"id"`
	IssueID          string                  `json:"issue_id"`
	Command          string                  `json:"command"`
	Status           IssueAgentCommandStatus `json:"status"`
	CreatedAt        time.Time               `json:"created_at"`
	DeliveredAt      *time.Time              `json:"delivered_at,omitempty"`
	SteeredAt        *time.Time              `json:"steered_at,omitempty"`
	DeliveryMode     string                  `json:"delivery_mode,omitempty"`
	DeliveryThreadID string                  `json:"delivery_thread_id,omitempty"`
	DeliveryAttempt  int                     `json:"delivery_attempt,omitempty"`
}

type SessionFeedEntry struct {
	IssueID          string                           `json:"issue_id"`
	IssueIdentifier  string                           `json:"issue_identifier"`
	IssueTitle       string                           `json:"issue_title,omitempty"`
	Source           string                           `json:"source"`
	Active           bool                             `json:"active"`
	Status           string                           `json:"status"`
	Planning         *IssuePlanningSummary            `json:"planning,omitempty"`
	PendingInterrupt *agentruntime.PendingInteraction `json:"pending_interrupt,omitempty"`
	Phase            string                           `json:"phase,omitempty"`
	Attempt          int                              `json:"attempt,omitempty"`
	RunKind          string                           `json:"run_kind,omitempty"`
	FailureClass     string                           `json:"failure_class,omitempty"`
	UpdatedAt        time.Time                        `json:"updated_at"`
	LastEvent        string                           `json:"last_event,omitempty"`
	LastMessage      string                           `json:"last_message,omitempty"`
	TotalTokens      int                              `json:"total_tokens,omitempty"`
	EventsProcessed  int                              `json:"events_processed,omitempty"`
	TurnsStarted     int                              `json:"turns_started,omitempty"`
	TurnsCompleted   int                              `json:"turns_completed,omitempty"`
	Terminal         bool                             `json:"terminal"`
	TerminalReason   string                           `json:"terminal_reason,omitempty"`
	Error            string                           `json:"error,omitempty"`
	RuntimeSurface
}
