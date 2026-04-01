package testadapter

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/olhapi/maestro/internal/agentruntime"
)

const (
	ProviderName  = "test"
	TransportName = "memory"
)

var Capabilities = agentruntime.Capabilities{
	RuntimePermissionUpdates: true,
}

type Client struct {
	spec      agentruntime.RuntimeSpec
	observers agentruntime.Observers

	mu                sync.Mutex
	session           agentruntime.Session
	output            string
	counter           int
	permissionUpdates []agentruntime.PermissionConfig
}

func Start(spec agentruntime.RuntimeSpec, observers agentruntime.Observers) agentruntime.Client {
	return &Client{
		spec:      spec,
		observers: observers,
		session: agentruntime.Session{
			IssueID:         spec.IssueID,
			IssueIdentifier: spec.IssueIdentifier,
			Metadata: map[string]interface{}{
				"provider":  ProviderName,
				"transport": TransportName,
			},
			MaxHistory: agentruntime.DefaultSessionHistoryLimit,
		},
	}
}

func (c *Client) Capabilities() agentruntime.Capabilities {
	return Capabilities
}

func (c *Client) RunTurn(ctx context.Context, request agentruntime.TurnRequest, onStarted func(*agentruntime.Session)) error {
	output, err := textInput(request.Input)
	if err != nil {
		return err
	}

	session := c.beginTurn()
	if onStarted != nil {
		cp := session.Clone()
		onStarted(&cp)
	}
	c.emitSessionUpdate(session)
	c.emitActivity(agentruntime.ActivityEvent{
		Type:     "turn.started",
		ThreadID: session.ThreadID,
		TurnID:   session.TurnID,
		Metadata: runtimeMetadata(),
	})

	c.mu.Lock()
	if strings.TrimSpace(output) != "" {
		c.appendOutputLocked(output)
		c.session.ApplyEvent(agentruntime.Event{
			Type:      "item.completed",
			ThreadID:  c.session.ThreadID,
			TurnID:    c.session.TurnID,
			ItemID:    "final-answer",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Message:   output,
		})
	}
	c.session.ApplyEvent(agentruntime.Event{
		Type:     "turn.completed",
		ThreadID: c.session.ThreadID,
		TurnID:   c.session.TurnID,
		Message:  output,
	})
	finalSession := c.session.Clone()
	c.mu.Unlock()

	if strings.TrimSpace(output) != "" {
		c.emitActivity(agentruntime.ActivityEvent{
			Type:      "item.completed",
			ThreadID:  finalSession.ThreadID,
			TurnID:    finalSession.TurnID,
			ItemID:    "final-answer",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Reason:    output,
			Metadata:  runtimeMetadata(),
		})
	}
	c.emitSessionUpdate(finalSession)
	c.emitActivity(agentruntime.ActivityEvent{
		Type:     "turn.completed",
		ThreadID: finalSession.ThreadID,
		TurnID:   finalSession.TurnID,
		Reason:   output,
		Metadata: runtimeMetadata(),
	})
	return nil
}

func (c *Client) UpdatePermissions(config agentruntime.PermissionConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spec.Permissions = config.Clone()
	c.permissionUpdates = append(c.permissionUpdates, config.Clone())
}

func (c *Client) RespondToInteraction(ctx context.Context, interactionID string, response agentruntime.PendingInteractionResponse) error {
	return agentruntime.ErrUnsupportedCapability
}

func (c *Client) Session() *agentruntime.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := c.session.Clone()
	return &cp
}

func (c *Client) Output() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output
}

func (c *Client) Close() error {
	return nil
}

func (c *Client) PermissionUpdates() []agentruntime.PermissionConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]agentruntime.PermissionConfig, 0, len(c.permissionUpdates))
	for _, config := range c.permissionUpdates {
		out = append(out, config.Clone())
	}
	return out
}

func (c *Client) beginTurn() agentruntime.Session {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.counter++
	c.session.ResetTurnState()
	c.session.IssueID = c.spec.IssueID
	c.session.IssueIdentifier = c.spec.IssueIdentifier
	threadID := strings.TrimSpace(c.session.ThreadID)
	if threadID == "" {
		threadID = "memory-thread"
	}
	turnID := fmt.Sprintf("memory-turn-%d", c.counter)
	c.session.ApplyEvent(agentruntime.Event{
		Type:     "turn.started",
		ThreadID: threadID,
		TurnID:   turnID,
	})
	return c.session.Clone()
}

func (c *Client) appendOutputLocked(output string) {
	output = strings.TrimSpace(output)
	if output == "" {
		return
	}
	if strings.TrimSpace(c.output) == "" {
		c.output = output
		return
	}
	c.output = strings.TrimSpace(c.output) + "\n" + output
}

func (c *Client) emitSessionUpdate(session agentruntime.Session) {
	if c.observers.OnSessionUpdate == nil {
		return
	}
	cp := session.Clone()
	c.observers.OnSessionUpdate(&cp)
}

func (c *Client) emitActivity(event agentruntime.ActivityEvent) {
	if c.observers.OnActivityEvent == nil {
		return
	}
	c.observers.OnActivityEvent(event.Clone())
}

func textInput(items []agentruntime.InputItem) (string, error) {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		switch item.Kind {
		case agentruntime.InputItemText:
			lines = append(lines, item.Text)
		case agentruntime.InputItemLocalImage:
			return "", fmt.Errorf("%w: local_image", agentruntime.ErrUnsupportedCapability)
		default:
			return "", fmt.Errorf("%w: input kind %q", agentruntime.ErrUnsupportedCapability, item.Kind)
		}
	}
	return strings.Join(lines, "\n\n"), nil
}

func runtimeMetadata() map[string]interface{} {
	return map[string]interface{}{
		"provider":  ProviderName,
		"transport": TransportName,
	}
}

func cloneJSONValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneJSONMap(typed)
	case []interface{}:
		cloned := make([]interface{}, len(typed))
		for i := range typed {
			cloned[i] = cloneJSONValue(typed[i])
		}
		return cloned
	default:
		return typed
	}
}

func cloneJSONMap(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}
