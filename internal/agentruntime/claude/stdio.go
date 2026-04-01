package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/olhapi/maestro/internal/agentruntime"
)

type stdioClient struct {
	spec      agentruntime.RuntimeSpec
	observers agentruntime.Observers

	command string

	mu      sync.Mutex
	session agentruntime.Session
	output  string
	counter int

	cleanupOnce sync.Once
	cleanup     func()
}

func startStdio(spec agentruntime.RuntimeSpec, observers agentruntime.Observers) (agentruntime.Client, error) {
	command, cleanup, err := buildClaudeCommand(spec)
	if err != nil {
		return nil, err
	}
	return &stdioClient{
		spec:      spec,
		observers: observers,
		command:   command,
		session: agentruntime.Session{
			IssueID:         spec.IssueID,
			IssueIdentifier: spec.IssueIdentifier,
			Metadata: map[string]interface{}{
				"provider":  string(agentruntime.ProviderClaude),
				"transport": string(agentruntime.TransportStdio),
			},
			MaxHistory: agentruntime.DefaultSessionHistoryLimit,
		},
		cleanup: cleanup,
	}, nil
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

	cmd := exec.CommandContext(turnCtx, "sh", "-lc", c.command)
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

func (c *stdioClient) RespondToInteraction(context.Context, string, agentruntime.PendingInteractionResponse) error {
	return agentruntime.ErrUnsupportedCapability
}

func (c *stdioClient) Session() *agentruntime.Session {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := c.session.Clone()
	return &cp
}

func (c *stdioClient) Output() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output
}

func (c *stdioClient) Close() error {
	if c == nil {
		return nil
	}
	c.cleanupOnce.Do(func() {
		if c.cleanup != nil {
			c.cleanup()
		}
	})
	return nil
}

func (c *stdioClient) beginTurn() agentruntime.Session {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.counter++
	threadID := strings.TrimSpace(c.session.ThreadID)
	c.session.ResetTurnState()
	if threadID == "" {
		threadID = "claude-thread"
	}
	c.session.ThreadID = threadID
	c.session.IssueID = c.spec.IssueID
	c.session.IssueIdentifier = c.spec.IssueIdentifier
	c.session.ApplyEvent(agentruntime.Event{
		Type:     "turn.started",
		ThreadID: threadID,
		TurnID:   fmt.Sprintf("turn-%d", c.counter),
	})
	return c.session.Clone()
}

func (c *stdioClient) finishTurn(output, terminalType string) {
	c.mu.Lock()
	if strings.TrimSpace(output) != "" {
		if strings.TrimSpace(c.output) == "" {
			c.output = strings.TrimSpace(output)
		} else {
			c.output = strings.TrimSpace(c.output) + "\n" + strings.TrimSpace(output)
		}
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
		Type:     terminalType,
		ThreadID: c.session.ThreadID,
		TurnID:   c.session.TurnID,
		Message:  output,
	})
	session := c.session.Clone()
	c.mu.Unlock()

	if strings.TrimSpace(output) != "" {
		c.emitActivity(agentruntime.ActivityEvent{
			Type:      "item.completed",
			ThreadID:  session.ThreadID,
			TurnID:    session.TurnID,
			ItemID:    "final-answer",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Reason:    output,
			Metadata:  runtimeMetadata(),
		})
	}
	c.emitActivity(agentruntime.ActivityEvent{
		Type:     terminalType,
		ThreadID: session.ThreadID,
		TurnID:   session.TurnID,
		Reason:   output,
		Metadata: runtimeMetadata(),
	})
	go c.emitSessionUpdate(session)
}

func (c *stdioClient) emitSessionUpdate(session agentruntime.Session) {
	if c.observers.OnSessionUpdate == nil {
		return
	}
	cp := session.Clone()
	c.observers.OnSessionUpdate(&cp)
}

func (c *stdioClient) emitActivity(event agentruntime.ActivityEvent) {
	if c.observers.OnActivityEvent == nil {
		return
	}
	go c.observers.OnActivityEvent(event.Clone())
}

func buildClaudeCommand(spec agentruntime.RuntimeSpec) (string, func(), error) {
	command := strings.TrimSpace(spec.Command)
	if command == "" {
		command = "claude"
	}
	if strings.TrimSpace(spec.DBPath) == "" {
		return "", nil, fmt.Errorf("claude runtime requires a db path for the live Maestro MCP bridge")
	}
	configPath, cleanup, err := writeClaudeMCPConfig(spec.DBPath)
	if err != nil {
		return "", nil, err
	}
	command = command + " --mcp-config " + shellQuoteArg(configPath) + " --strict-mcp-config"
	return command, cleanup, nil
}

func writeClaudeMCPConfig(dbPath string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "maestro-claude-mcp-*")
	if err != nil {
		return "", nil, err
	}
	configPath := filepath.Join(dir, "mcp.json")
	config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"maestro": map[string]interface{}{
				"type":    "stdio",
				"command": "maestro",
				"args": []string{
					"mcp",
					"--db",
					dbPath,
				},
			},
		},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	if err := os.WriteFile(configPath, append(data, '\n'), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return configPath, func() {
		_ = os.RemoveAll(dir)
	}, nil
}

func shellQuoteArg(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
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

func runtimeMetadata() map[string]interface{} {
	return map[string]interface{}{
		"provider":  string(agentruntime.ProviderClaude),
		"transport": string(agentruntime.TransportStdio),
	}
}
