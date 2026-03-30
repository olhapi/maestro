package observability

import "time"

type TokenTotals struct {
	InputTokens    int `json:"input_tokens"`
	OutputTokens   int `json:"output_tokens"`
	TotalTokens    int `json:"total_tokens"`
	SecondsRunning int `json:"seconds_running"`
}

type RunningEntry struct {
	IssueID           string      `json:"issue_id"`
	Identifier        string      `json:"identifier"`
	WorkspacePath     string      `json:"workspace_path,omitempty"`
	State             string      `json:"state"`
	Phase             string      `json:"phase,omitempty"`
	Attempt           int         `json:"attempt,omitempty"`
	SessionID         string      `json:"session_id,omitempty"`
	CodexAppServerPID int         `json:"codex_app_server_pid,omitempty"`
	TurnCount         int         `json:"turn_count"`
	LastEvent         string      `json:"last_event,omitempty"`
	LastMessage       string      `json:"last_message,omitempty"`
	StartedAt         time.Time   `json:"started_at"`
	LastEventAt       *time.Time  `json:"last_event_at,omitempty"`
	Tokens            TokenTotals `json:"tokens"`
}

type RetryEntry struct {
	IssueID       string    `json:"issue_id"`
	Identifier    string    `json:"identifier"`
	WorkspacePath string    `json:"workspace_path,omitempty"`
	Phase         string    `json:"phase,omitempty"`
	Attempt       int       `json:"attempt"`
	DueAt         time.Time `json:"due_at"`
	DueInMs       int64     `json:"due_in_ms"`
	Error         string    `json:"error,omitempty"`
	DelayType     string    `json:"delay_type,omitempty"`
}

type PausedEntry struct {
	IssueID             string    `json:"issue_id"`
	Identifier          string    `json:"identifier"`
	WorkspacePath       string    `json:"workspace_path,omitempty"`
	Phase               string    `json:"phase,omitempty"`
	Attempt             int       `json:"attempt"`
	PausedAt            time.Time `json:"paused_at"`
	Error               string    `json:"error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	PauseThreshold      int       `json:"pause_threshold"`
}

type Snapshot struct {
	GeneratedAt   time.Time      `json:"generated_at"`
	Running       []RunningEntry `json:"running"`
	Retrying      []RetryEntry   `json:"retrying"`
	Paused        []PausedEntry  `json:"paused"`
	CodexTotals   TokenTotals    `json:"codex_totals"`
	RateLimits    interface{}    `json:"rate_limits"`
	WorkspaceRoot string         `json:"workspace_root,omitempty"`
}
