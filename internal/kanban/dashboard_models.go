package kanban

import (
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
	Counts IssueStateCounts `json:"counts"`
}

type EpicSummary struct {
	Epic
	ProjectName string           `json:"project_name,omitempty"`
	Counts      IssueStateCounts `json:"counts"`
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
	Search      string
	Sort        string
	Blocked     *bool
	Limit       int
	Offset      int
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
