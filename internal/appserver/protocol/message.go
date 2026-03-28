package protocol

import (
	"encoding/json"
	"strings"
)

const (
	MethodInitialize                   = "initialize"
	MethodInitialized                  = "initialized"
	MethodThreadStart                  = "thread/start"
	MethodThreadResume                 = "thread/resume"
	MethodThreadStarted                = "thread/started"
	MethodTurnStart                    = "turn/start"
	MethodTurnStarted                  = "turn/started"
	MethodTurnCompleted                = "turn/completed"
	MethodThreadTokenUsageUpdated      = "thread/tokenUsage/updated"
	MethodTurnFailed                   = "turn/failed"
	MethodTurnCancelled                = "turn/cancelled"
	MethodItemCommandExecutionApproval = "item/commandExecution/requestApproval"
	MethodItemFileChangeApproval       = "item/fileChange/requestApproval"
	MethodExecCommandApproval          = "execCommandApproval"
	MethodApplyPatchApproval           = "applyPatchApproval"
	MethodToolRequestUserInput         = "item/tool/requestUserInput"
	MethodMCPServerElicitationRequest  = "mcpServer/elicitation/request"
	MethodToolCall                     = "item/tool/call"
)

type RequestID = json.RawMessage

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type Message struct {
	ID     RequestID              `json:"id,omitempty"`
	Method string                 `json:"method,omitempty"`
	Params json.RawMessage        `json:"params,omitempty"`
	Result json.RawMessage        `json:"result,omitempty"`
	Error  *Error                 `json:"error,omitempty"`
	Raw    map[string]interface{} `json:"-"`
}

func DecodeMessage(line string) (Message, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return Message{}, false
	}
	var msg Message
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return Message{}, false
	}
	if err := json.Unmarshal([]byte(line), &msg.Raw); err != nil {
		msg.Raw = nil
	}
	return msg, true
}

func (m Message) HasID() bool {
	return len(m.ID) > 0 && string(m.ID) != "null"
}

func (m Message) IntID() (int, bool) {
	if !m.HasID() {
		return 0, false
	}
	var out int
	if err := json.Unmarshal(m.ID, &out); err != nil {
		return 0, false
	}
	return out, true
}

func (m Message) IsResponseTo(id int) bool {
	got, ok := m.IntID()
	return ok && got == id
}

func (m Message) UnmarshalParams(target interface{}) error {
	if len(m.Params) == 0 || string(m.Params) == "null" {
		return nil
	}
	return json.Unmarshal(m.Params, target)
}

func (m Message) UnmarshalResult(target interface{}) error {
	if len(m.Result) == 0 || string(m.Result) == "null" {
		return nil
	}
	return json.Unmarshal(m.Result, target)
}

type Request[P any] struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
	Params P      `json:"params"`
}

type Notification[P any] struct {
	Method string `json:"method"`
	Params P      `json:"params"`
}

type SuccessResponse[R any] struct {
	ID     RequestID `json:"id"`
	Result R         `json:"result"`
}
