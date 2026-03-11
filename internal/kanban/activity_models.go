package kanban

import "time"

type IssueActivityEntry struct {
	Seq         int64                  `json:"seq"`
	ID          string                 `json:"id"`
	IssueID     string                 `json:"issue_id"`
	Identifier  string                 `json:"identifier"`
	Attempt     int                    `json:"attempt"`
	ThreadID    string                 `json:"thread_id,omitempty"`
	TurnID      string                 `json:"turn_id,omitempty"`
	ItemID      string                 `json:"item_id,omitempty"`
	Kind        string                 `json:"kind"`
	ItemType    string                 `json:"item_type,omitempty"`
	Phase       string                 `json:"phase,omitempty"`
	Status      string                 `json:"status,omitempty"`
	Tier        string                 `json:"tier,omitempty"`
	Title       string                 `json:"title"`
	Summary     string                 `json:"summary"`
	Detail      string                 `json:"detail,omitempty"`
	Tone        string                 `json:"tone,omitempty"`
	Expandable  bool                   `json:"expandable"`
	StartedAt   *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	RawPayload  map[string]interface{} `json:"raw_payload,omitempty"`
}

type IssueActivityUpdate struct {
	Seq       int64                  `json:"seq"`
	IssueID   string                 `json:"issue_id"`
	EntryID   string                 `json:"entry_id"`
	EventType string                 `json:"event_type"`
	EventTS   time.Time              `json:"event_ts"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
}

type ActivityEntry struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	ItemType    string `json:"item_type,omitempty"`
	Phase       string `json:"phase,omitempty"`
	Status      string `json:"status,omitempty"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Detail      string `json:"detail,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	Expandable  bool   `json:"expandable"`
	Tone        string `json:"tone,omitempty"`
}

type ActivityGroup struct {
	Attempt int             `json:"attempt"`
	Phase   string          `json:"phase,omitempty"`
	Status  string          `json:"status,omitempty"`
	Entries []ActivityEntry `json:"entries"`
}
