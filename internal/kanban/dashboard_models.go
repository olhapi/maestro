package kanban

import (
	"sort"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
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
	switch state {
	case StateBacklog:
		c.Backlog++
	case StateReady:
		c.Ready++
	case StateInProgress:
		c.InProgress++
	case StateInReview:
		c.InReview++
	case StateDone:
		c.Done++
	case StateCancelled:
		c.Cancelled++
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
	ProjectDescription string `json:"project_description,omitempty"`
	EpicDescription    string `json:"epic_description,omitempty"`
}

type IssueQuery struct {
	ProjectID   string
	ProjectName string
	EpicID      string
	State       string
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

type ExecutionSessionSnapshot struct {
	IssueID    string            `json:"issue_id"`
	Identifier string            `json:"identifier"`
	Phase      string            `json:"phase,omitempty"`
	Attempt    int               `json:"attempt"`
	RunKind    string            `json:"run_kind,omitempty"`
	Error      string            `json:"error,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at"`
	AppSession appserver.Session `json:"session"`
}

type SessionFeedEntry struct {
	IssueID         string            `json:"issue_id"`
	IssueIdentifier string            `json:"issue_identifier"`
	IssueTitle      string            `json:"issue_title,omitempty"`
	Source          string            `json:"source"`
	Active          bool              `json:"active"`
	Status          string            `json:"status"`
	Phase           string            `json:"phase,omitempty"`
	Attempt         int               `json:"attempt,omitempty"`
	RunKind         string            `json:"run_kind,omitempty"`
	FailureClass    string            `json:"failure_class,omitempty"`
	UpdatedAt       time.Time         `json:"updated_at"`
	LastEvent       string            `json:"last_event,omitempty"`
	LastMessage     string            `json:"last_message,omitempty"`
	TotalTokens     int               `json:"total_tokens,omitempty"`
	EventsProcessed int               `json:"events_processed,omitempty"`
	TurnsStarted    int               `json:"turns_started,omitempty"`
	TurnsCompleted  int               `json:"turns_completed,omitempty"`
	Terminal        bool              `json:"terminal"`
	TerminalReason  string            `json:"terminal_reason,omitempty"`
	History         []appserver.Event `json:"history,omitempty"`
	Error           string            `json:"error,omitempty"`
}
