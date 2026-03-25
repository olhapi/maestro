package appserver

import (
	"encoding/json"
	"strings"

	"github.com/olhapi/maestro/internal/appserver/protocol"
)

func messageParamsMap(msg protocol.Message) (map[string]interface{}, bool) {
	if len(msg.Params) == 0 || string(msg.Params) == "null" {
		return nil, false
	}
	var params map[string]interface{}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return nil, false
	}
	return params, true
}

func threadTurnIDsFromMap(fields map[string]interface{}) (threadID, turnID string) {
	if fields == nil {
		return "", ""
	}

	threadID = strings.TrimSpace(firstStr(fields, "threadId", "thread_id"))
	if threadID == "" {
		if thread, ok := asMap(fields["thread"]); ok {
			threadID = strings.TrimSpace(firstStr(thread, "id", "threadId", "thread_id"))
		}
	}

	turnID = strings.TrimSpace(firstStr(fields, "turnId", "turn_id"))
	if turnID == "" {
		if turn, ok := asMap(fields["turn"]); ok {
			turnID = strings.TrimSpace(firstStr(turn, "id", "turnId", "turn_id"))
		}
	}

	return threadID, turnID
}
