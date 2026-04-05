package gen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

func isNullJSON(data []byte) bool {
	return bytes.Equal(bytes.TrimSpace(data), []byte("null"))
}

func unmarshalStringOrObject[E ~string, O any](data []byte, enumTarget **E, objectTarget **O) error {
	if isNullJSON(data) {
		*enumTarget = nil
		*objectTarget = nil
		return nil
	}

	var enumValue E
	enumErr := json.Unmarshal(data, &enumValue)
	if enumErr == nil {
		*enumTarget = &enumValue
		*objectTarget = nil
		return nil
	}

	var objectValue O
	objectErr := json.Unmarshal(data, &objectValue)
	if objectErr == nil {
		*enumTarget = nil
		*objectTarget = &objectValue
		return nil
	}

	return fmt.Errorf("decode union string/object: %w", errors.Join(enumErr, objectErr))
}

func unmarshalBoolOrString[E ~string](data []byte, boolTarget **bool, enumTarget **E) error {
	if isNullJSON(data) {
		*boolTarget = nil
		*enumTarget = nil
		return nil
	}

	var boolValue bool
	boolErr := json.Unmarshal(data, &boolValue)
	if boolErr == nil {
		*boolTarget = &boolValue
		*enumTarget = nil
		return nil
	}

	var enumValue E
	enumErr := json.Unmarshal(data, &enumValue)
	if enumErr == nil {
		*boolTarget = nil
		*enumTarget = &enumValue
		return nil
	}

	return fmt.Errorf("decode union bool/string: %w", errors.Join(boolErr, enumErr))
}

func (s *TentacledSessionSource) UnmarshalJSON(data []byte) error {
	return unmarshalStringOrObject[SessionSource, PurpleSessionSource](data, &s.Enum, &s.PurpleSessionSource)
}

func (s *StickySessionSource) UnmarshalJSON(data []byte) error {
	return unmarshalStringOrObject[SessionSource, FluffySessionSource](data, &s.Enum, &s.FluffySessionSource)
}

func (p *ThreadStartParamsApprovalPolicy) UnmarshalJSON(data []byte) error {
	return unmarshalStringOrObject[ApprovalPolicyEnum, PurpleGranularAskForApproval](data, &p.Enum, &p.PurpleGranularAskForApproval)
}

func (p *AskForApproval) UnmarshalJSON(data []byte) error {
	return unmarshalStringOrObject[ApprovalPolicyEnum, AskForApprovalGranularAskForApproval](data, &p.Enum, &p.AskForApprovalGranularAskForApproval)
}

func (p *TurnStartParamsApprovalPolicy) UnmarshalJSON(data []byte) error {
	return unmarshalStringOrObject[ApprovalPolicyEnum, FluffyGranularAskForApproval](data, &p.Enum, &p.FluffyGranularAskForApproval)
}

func (n *NetworkAccessUnion) UnmarshalJSON(data []byte) error {
	return unmarshalBoolOrString[NetworkAccess](data, &n.Bool, &n.Enum)
}

func (s *TentacledSubAgentSource) UnmarshalJSON(data []byte) error {
	return unmarshalStringOrObject[SubAgentSource, PurpleSubAgentSource](data, &s.Enum, &s.PurpleSubAgentSource)
}

func (s *StickySubAgentSource) UnmarshalJSON(data []byte) error {
	return unmarshalStringOrObject[SubAgentSource, FluffySubAgentSource](data, &s.Enum, &s.FluffySubAgentSource)
}

func (t *Title) UnmarshalJSON(data []byte) error {
	if t == nil {
		return nil
	}
	if isNullJSON(data) {
		*t = Title{}
		return nil
	}

	var boolValue bool
	boolErr := json.Unmarshal(data, &boolValue)
	if boolErr == nil {
		*t = Title{Bool: &boolValue}
		return nil
	}

	var doubleValue float64
	doubleErr := json.Unmarshal(data, &doubleValue)
	if doubleErr == nil {
		*t = Title{Double: &doubleValue}
		return nil
	}

	var stringValue string
	stringErr := json.Unmarshal(data, &stringValue)
	if stringErr == nil {
		*t = Title{String: &stringValue}
		return nil
	}

	var stringArrayValue []string
	stringArrayErr := json.Unmarshal(data, &stringArrayValue)
	if stringArrayErr == nil {
		*t = Title{StringArray: stringArrayValue}
		return nil
	}

	return fmt.Errorf("decode title union: %w", errors.Join(boolErr, doubleErr, stringErr, stringArrayErr))
}

func (t *Title) MarshalJSON() ([]byte, error) {
	if t == nil {
		return []byte("null"), nil
	}
	switch {
	case t.Bool != nil:
		return json.Marshal(*t.Bool)
	case t.Double != nil:
		return json.Marshal(*t.Double)
	case t.String != nil:
		return json.Marshal(*t.String)
	case t.StringArray != nil:
		return json.Marshal(t.StringArray)
	default:
		return []byte("null"), nil
	}
}
