package agentruntime

type ActivityEvent struct {
	Type             string                 `json:"type"`
	RequestID        string                 `json:"request_id,omitempty"`
	ThreadID         string                 `json:"thread_id"`
	TurnID           string                 `json:"turn_id"`
	ItemID           string                 `json:"item_id,omitempty"`
	ItemType         string                 `json:"item_type,omitempty"`
	ItemPhase        string                 `json:"item_phase,omitempty"`
	Delta            string                 `json:"delta,omitempty"`
	Stdin            string                 `json:"stdin,omitempty"`
	ProcessID        string                 `json:"process_id,omitempty"`
	Command          string                 `json:"command,omitempty"`
	CWD              string                 `json:"cwd,omitempty"`
	AggregatedOutput string                 `json:"aggregated_output,omitempty"`
	Status           string                 `json:"status,omitempty"`
	Reason           string                 `json:"reason,omitempty"`
	InputTokens      int                    `json:"input_tokens,omitempty"`
	OutputTokens     int                    `json:"output_tokens,omitempty"`
	TotalTokens      int                    `json:"total_tokens,omitempty"`
	ExitCode         *int                   `json:"exit_code,omitempty"`
	Item             map[string]interface{} `json:"item,omitempty"`
	Raw              map[string]interface{} `json:"raw,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

func (event ActivityEvent) Clone() ActivityEvent {
	cloned := event
	cloned.Item = cloneJSONMap(event.Item)
	cloned.Raw = cloneJSONMap(event.Raw)
	cloned.Metadata = cloneJSONMap(event.Metadata)
	if event.ExitCode != nil {
		code := *event.ExitCode
		cloned.ExitCode = &code
	}
	return cloned
}
