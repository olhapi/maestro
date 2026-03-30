package codex

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/olhapi/maestro/internal/agentruntime"
)

type stdioClient struct {
	spec      agentruntime.RuntimeSpec
	observers agentruntime.Observers

	mu      sync.Mutex
	session agentruntime.Session
	output  string
	counter int
}

func startStdio(spec agentruntime.RuntimeSpec, observers agentruntime.Observers) agentruntime.Client {
	client := &stdioClient{
		spec:      spec,
		observers: observers,
		session: agentruntime.Session{
			IssueID:         spec.IssueID,
			IssueIdentifier: spec.IssueIdentifier,
			Metadata: map[string]interface{}{
				"provider":  string(agentruntime.ProviderCodex),
				"transport": string(agentruntime.TransportStdio),
			},
			MaxHistory: agentruntime.DefaultSessionHistoryLimit,
		},
	}
	return client
}

func (c *stdioClient) Capabilities() agentruntime.Capabilities {
	return stdioCapabilities
}

func (c *stdioClient) RunTurn(ctx context.Context, request agentruntime.TurnRequest, onStarted func(*agentruntime.Session)) error {
	turnCtx := ctx
	var cancel context.CancelFunc
	if c.spec.TurnTimeout > 0 {
		turnCtx, cancel = context.WithTimeout(ctx, c.spec.TurnTimeout)
		defer cancel()
	}

	input, err := textInput(request.Input)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(turnCtx, "sh", "-lc", c.spec.Command)
	cmd.Dir = c.spec.Workspace
	cmd.Env = c.spec.Env
	if len(cmd.Env) == 0 {
		cmd.Env = os.Environ()
	}
	cmd.Stdin = strings.NewReader(input)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	session := c.beginTurn()
	if onStarted != nil {
		cp := session.Clone()
		onStarted(&cp)
	}
	c.emitSessionUpdate(session)

	err = cmd.Wait()
	output := strings.TrimSpace(combineOutput(stdout.String(), stderr.String()))
	if err != nil {
		c.finishTurn(output, "turn.failed")
		return err
	}
	c.finishTurn(output, "turn.completed")
	return nil
}

func (c *stdioClient) UpdatePermissions(config agentruntime.PermissionConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spec.Permissions = config
}

func (c *stdioClient) RespondToInteraction(ctx context.Context, interactionID string, response agentruntime.PendingInteractionResponse) error {
	return agentruntime.ErrUnsupportedCapability
}

func (c *stdioClient) Session() *agentruntime.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := c.session.Clone()
	return &cp
}

func (c *stdioClient) Output() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output
}

func (c *stdioClient) Close() error {
	return nil
}

func (c *stdioClient) beginTurn() agentruntime.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counter++
	c.session.ResetTurnState()
	c.session.IssueID = c.spec.IssueID
	c.session.IssueIdentifier = c.spec.IssueIdentifier
	turnID := fmt.Sprintf("stdio-turn-%d", c.counter)
	c.session.ApplyEvent(agentruntime.Event{
		Type:    "turn.started",
		TurnID:  turnID,
		Message: "",
	})
	return c.session.Clone()
}

func (c *stdioClient) finishTurn(output, terminalType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(output) != "" {
		if strings.TrimSpace(c.output) == "" {
			c.output = strings.TrimSpace(output)
		} else {
			c.output = strings.TrimSpace(c.output) + "\n" + strings.TrimSpace(output)
		}
	}
	if strings.TrimSpace(output) != "" {
		c.session.ApplyEvent(agentruntime.Event{
			Type:      "item.completed",
			TurnID:    c.session.TurnID,
			ItemID:    "final-answer",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Message:   output,
		})
		c.emitActivityLocked(agentruntime.ActivityEvent{
			Type:      "item.completed",
			TurnID:    c.session.TurnID,
			ItemID:    "final-answer",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Reason:    output,
			Metadata: map[string]interface{}{
				"provider":  string(agentruntime.ProviderCodex),
				"transport": string(agentruntime.TransportStdio),
			},
		})
	}
	c.session.ApplyEvent(agentruntime.Event{
		Type:    terminalType,
		TurnID:  c.session.TurnID,
		Message: output,
	})
	c.emitActivityLocked(agentruntime.ActivityEvent{
		Type:   terminalType,
		TurnID: c.session.TurnID,
		Reason: output,
		Metadata: map[string]interface{}{
			"provider":  string(agentruntime.ProviderCodex),
			"transport": string(agentruntime.TransportStdio),
		},
	})
	session := c.session.Clone()
	go c.emitSessionUpdate(session)
}

func (c *stdioClient) emitSessionUpdate(session agentruntime.Session) {
	if c.observers.OnSessionUpdate == nil {
		return
	}
	cp := session.Clone()
	c.observers.OnSessionUpdate(&cp)
}

func (c *stdioClient) emitActivityLocked(event agentruntime.ActivityEvent) {
	if c.observers.OnActivityEvent == nil {
		return
	}
	activity := event.Clone()
	go c.observers.OnActivityEvent(activity)
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

func combineOutput(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	switch {
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}
