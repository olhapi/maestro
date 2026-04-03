package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
)

const claudeSessionIdentifierStrategy = "provider_session_uuid"

type stdioClient struct {
	spec          agentruntime.RuntimeSpec
	observers     agentruntime.Observers
	mcpConfigPath string
	settingsPath  string

	mu          sync.Mutex
	session     agentruntime.Session
	output      string
	counter     int
	activeTurn  *runningTurn
	cleanupOnce sync.Once
	cleanup     func()
}

type runningTurn struct {
	pid         int
	interrupted bool
	stopOnce    sync.Once
	done        chan struct{}
}

type claudeTurnState struct {
	sessionStarted bool
	turnStarted    bool
	streamStarted  bool
	turnID         string
	itemPhase      string
	lastAssistant  string
	streamedOutput bytes.Buffer
	resultText     string
	resultStop     string
	resultUUID     string
	resultIsError  bool
	resultSeen     bool
	inputTokens    int
	outputTokens   int
	totalTokens    int
}

func startStdio(spec agentruntime.RuntimeSpec, observers agentruntime.Observers) (agentruntime.Client, error) {
	if strings.TrimSpace(spec.DBPath) == "" {
		return nil, fmt.Errorf("claude runtime requires a db path for the live Maestro MCP bridge")
	}
	configPath, settingsPath, cleanup, err := writeClaudeSupportFiles(spec.DBPath)
	if err != nil {
		return nil, err
	}

	resumeToken := strings.TrimSpace(spec.ResumeToken)
	session := agentruntime.Session{
		IssueID:         spec.IssueID,
		IssueIdentifier: spec.IssueIdentifier,
		SessionID:       resumeToken,
		ThreadID:        resumeToken,
		Metadata:        runtimeMetadataWithExtras(resumeToken, spec.Metadata),
		MaxHistory:      agentruntime.DefaultSessionHistoryLimit,
	}

	return &stdioClient{
		spec:          spec,
		observers:     observers,
		mcpConfigPath: configPath,
		settingsPath:  settingsPath,
		session:       session,
		cleanup:       cleanup,
	}, nil
}

func (c *stdioClient) Capabilities() agentruntime.Capabilities {
	return stdioCapabilities
}

func (c *stdioClient) RunTurn(ctx context.Context, request agentruntime.TurnRequest, onStarted func(*agentruntime.Session)) error {
	if c == nil {
		return nil
	}

	baseCtx, baseCancel := context.WithCancel(ctx)
	defer baseCancel()

	turnCtx := baseCtx
	var timeoutCancel context.CancelFunc
	if c.spec.TurnTimeout > 0 {
		turnCtx, timeoutCancel = context.WithTimeout(baseCtx, c.spec.TurnTimeout)
		defer timeoutCancel()
	}

	input, err := textInput(request.Input)
	if err != nil {
		return err
	}
	command, err := c.buildClaudeCommand()
	if err != nil {
		return err
	}

	cmd := exec.Command("sh", "-lc", command)
	configureClaudeManagedProcess(cmd)
	cmd.Dir = c.spec.Workspace
	cmd.Env = c.spec.Env
	if len(cmd.Env) == 0 {
		cmd.Env = os.Environ()
	}
	cmd.Stdin = strings.NewReader(input)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	done := c.beginTurnLocked()
	c.setActiveTurn(cmd.Process.Pid, done)
	defer c.clearActiveTurn(done)

	go c.watchTurnContext(turnCtx, done)

	var stdoutRaw bytes.Buffer
	var stderrRaw bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(&stderrRaw, stderr)
	}()

	state := &claudeTurnState{}
	c.readClaudeStream(stdout, &stdoutRaw, state, onStarted)

	waitErr := cmd.Wait()
	<-stderrDone

	_, _, finalErr := c.finishTurnLocked(state, stdoutRaw.String(), stderrRaw.String(), waitErr, turnCtx.Err())
	return finalErr
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
	c.requestActiveTurnStop(nil)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()

	c.mu.Lock()
	active := c.activeTurn
	c.mu.Unlock()
	if active != nil {
		select {
		case <-active.done:
		case <-deadline.C:
		}
	}

	c.cleanupOnce.Do(func() {
		if c.cleanup != nil {
			c.cleanup()
		}
	})
	return nil
}

func (c *stdioClient) beginTurnLocked() chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.counter++
	c.session.ResetTurnState()
	c.session.IssueID = c.spec.IssueID
	c.session.IssueIdentifier = c.spec.IssueIdentifier
	c.normalizeClaudeSessionIdentityLocked()

	return make(chan struct{})
}

func (c *stdioClient) readClaudeStream(stdout io.Reader, stdoutRaw *bytes.Buffer, state *claudeTurnState, onStarted func(*agentruntime.Session)) {
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			stdoutRaw.Write(line)
			c.handleClaudeLine(bytes.TrimSpace(line), state, onStarted)
		}
		if err != nil {
			if err != io.EOF {
				// Claude emits JSONL; non-JSON diagnostics are ignored and
				// the raw buffers still capture the tail for fallback output.
			}
			break
		}
	}
}

func (c *stdioClient) handleClaudeLine(line []byte, state *claudeTurnState, onStarted func(*agentruntime.Session)) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(line, &raw); err != nil {
		return
	}

	if sessionID := strings.TrimSpace(stringFromMap(raw, "session_id")); sessionID != "" {
		c.recordClaudeSessionID(sessionID)
	}

	switch strings.TrimSpace(asString(raw["type"])) {
	case "system":
		if strings.TrimSpace(asString(raw["subtype"])) == "init" {
			return
		}
	case "assistant":
		message := mapValue(raw["message"])
		if message == nil {
			return
		}
		if id := strings.TrimSpace(stringFromMap(message, "id")); id != "" {
			state.turnID = id
		}
		if text := assistantMessageText(message); text != "" {
			state.lastAssistant = text
		}
		if phase := assistantMessagePhase(message); phase != "" {
			state.itemPhase = phase
		}
		c.ensureClaudeTurnStarted(state, onStarted)
	case "result":
		state.resultSeen = true
		state.resultText = firstNonEmpty(
			strings.TrimSpace(asString(raw["result"])),
			strings.TrimSpace(state.lastAssistant),
			strings.TrimSpace(state.streamedOutput.String()),
		)
		state.resultStop = firstNonEmpty(
			strings.TrimSpace(asString(raw["stop_reason"])),
			strings.TrimSpace(state.resultStop),
		)
		state.resultUUID = firstNonEmpty(
			strings.TrimSpace(asString(raw["uuid"])),
			strings.TrimSpace(state.resultUUID),
		)
		if isError, ok := boolFromAny(raw["is_error"]); ok {
			state.resultIsError = isError
		}
		if subtype := strings.TrimSpace(asString(raw["subtype"])); subtype != "" && !strings.EqualFold(subtype, "success") {
			state.resultIsError = true
		}
		if input, output, total := usageTokens(raw["usage"]); input > 0 || output > 0 || total > 0 {
			state.inputTokens = input
			state.outputTokens = output
			state.totalTokens = total
		}
		c.ensureClaudeTurnStarted(state, onStarted)
	default:
		event := mapValue(raw["event"])
		if event == nil {
			return
		}
		switch strings.TrimSpace(asString(event["type"])) {
		case "message_start":
			message := mapValue(event["message"])
			if message != nil {
				if id := strings.TrimSpace(stringFromMap(message, "id")); id != "" {
					state.turnID = id
				}
				if phase := assistantMessagePhase(message); phase != "" {
					state.itemPhase = phase
				}
			}
			if id := strings.TrimSpace(stringFromMap(event, "message", "id")); id != "" {
				state.turnID = id
			}
			if state.turnID == "" {
				state.turnID = firstNonEmpty(
					strings.TrimSpace(asString(event["message_id"])),
					strings.TrimSpace(asString(event["uuid"])),
					state.resultUUID,
				)
			}
			c.ensureClaudeTurnStarted(state, onStarted)
		case "content_block_start":
			block := mapValue(event["content_block"])
			if block == nil {
				block = mapValue(event["contentBlock"])
			}
			if block != nil {
				if phase := blockPhase(block); phase != "" {
					state.itemPhase = phase
				}
				if id := strings.TrimSpace(stringFromMap(block, "id")); id != "" && state.turnID == "" {
					state.turnID = id
				}
			}
			c.ensureClaudeTurnStarted(state, onStarted)
		case "content_block_delta":
			text := deltaText(event)
			if text == "" {
				return
			}
			if state.turnID == "" {
				state.turnID = firstNonEmpty(state.resultUUID, fallbackClaudeTurnID(c, state))
			}
			c.ensureClaudeTurnStarted(state, onStarted)
			state.streamedOutput.WriteString(text)
			c.recordClaudeStreamDelta(state, text)
		case "message_delta":
			if stopReason := strings.TrimSpace(stringFromMap(event, "delta", "stop_reason")); stopReason != "" {
				state.resultStop = stopReason
			}
			if input, output, total := usageTokens(event["usage"]); input > 0 || output > 0 || total > 0 {
				state.inputTokens = input
				state.outputTokens = output
				state.totalTokens = total
			}
		case "message_stop":
			// result lines can still follow message_stop.
		}
	}
}

func (c *stdioClient) ensureClaudeTurnStarted(state *claudeTurnState, onStarted func(*agentruntime.Session)) {
	if state.turnStarted {
		return
	}

	sessionID := c.currentClaudeSessionIDLocked()
	turnID := strings.TrimSpace(state.turnID)
	if turnID == "" {
		turnID = fallbackClaudeTurnID(c, state)
		state.turnID = turnID
	}

	c.mu.Lock()
	c.session.IssueID = c.spec.IssueID
	c.session.IssueIdentifier = c.spec.IssueIdentifier
	if sessionID != "" {
		c.session.ThreadID = sessionID
		c.session.SessionID = sessionID
		c.syncClaudeMetadataLocked(sessionID)
	}
	c.session.ApplyEvent(agentruntime.Event{
		Type:     "turn.started",
		ThreadID: sessionID,
		TurnID:   turnID,
	})
	c.normalizeClaudeSessionIdentityLocked()
	session := c.session.Clone()
	c.mu.Unlock()

	c.emitSessionUpdate(session)
	if onStarted != nil && !state.sessionStarted {
		state.sessionStarted = true
		onStarted(&session)
	}
	state.turnStarted = true
}

func (c *stdioClient) finishTurnLocked(state *claudeTurnState, stdoutRaw, stderrRaw string, waitErr error, turnCtxErr error) (string, string, error) {
	sessionID := c.currentClaudeSessionIDLocked()
	output := firstNonEmpty(
		strings.TrimSpace(state.resultText),
		strings.TrimSpace(state.lastAssistant),
		strings.TrimSpace(state.streamedOutput.String()),
	)
	if output == "" {
		output = strings.TrimSpace(combineOutput(stdoutRaw, stderrRaw))
	}

	terminalType := "turn.completed"
	finalErr := error(nil)
	if turnCtxErr != nil {
		terminalType = "turn.cancelled"
		finalErr = turnCtxErr
	} else if c.activeTurnInterrupted() {
		terminalType = "turn.cancelled"
		finalErr = context.Canceled
	} else if state.resultIsError {
		terminalType = "turn.failed"
		if output == "" {
			finalErr = fmt.Errorf("claude reported an error")
		} else {
			finalErr = fmt.Errorf("claude reported an error: %s", output)
		}
	} else if waitErr != nil {
		terminalType = "turn.failed"
		if output == "" {
			finalErr = waitErr
		} else {
			finalErr = fmt.Errorf("%w: %s", waitErr, output)
		}
	}

	c.mu.Lock()
	if output != "" {
		if strings.TrimSpace(c.output) == "" {
			c.output = output
		} else {
			c.output = strings.TrimSpace(c.output) + "\n" + output
		}
	}
	if c.session.Metadata == nil {
		c.session.Metadata = runtimeMetadataWithExtras(sessionID, c.spec.Metadata)
	}
	if sessionID != "" {
		c.session.Metadata["provider_session_id"] = sessionID
	}
	if state.resultStop != "" {
		c.session.Metadata["claude_stop_reason"] = state.resultStop
	}
	if output != "" {
		c.session.ApplyEvent(agentruntime.Event{
			Type:      "item.completed",
			ThreadID:  sessionID,
			TurnID:    state.turnID,
			ItemID:    state.turnID,
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Message:   output,
		})
		c.normalizeClaudeSessionIdentityLocked()
	}
	c.session.ApplyEvent(agentruntime.Event{
		Type:         terminalType,
		ThreadID:     sessionID,
		TurnID:       state.turnID,
		Message:      output,
		InputTokens:  state.inputTokens,
		OutputTokens: state.outputTokens,
		TotalTokens:  state.totalTokens,
	})
	c.normalizeClaudeSessionIdentityLocked()
	session := c.session.Clone()
	c.mu.Unlock()

	if output != "" {
		c.emitActivity(agentruntime.ActivityEvent{
			Type:      "item.completed",
			ThreadID:  sessionID,
			TurnID:    state.turnID,
			ItemID:    state.turnID,
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Reason:    output,
			Status:    state.resultStop,
			Item: map[string]interface{}{
				"id":    state.turnID,
				"type":  "agentMessage",
				"phase": "final_answer",
				"text":  output,
			},
			Metadata: runtimeMetadataWithExtras(sessionID, c.spec.Metadata),
		})
	}
	c.emitActivity(agentruntime.ActivityEvent{
		Type:         terminalType,
		ThreadID:     sessionID,
		TurnID:       state.turnID,
		Reason:       output,
		Status:       state.resultStop,
		InputTokens:  state.inputTokens,
		OutputTokens: state.outputTokens,
		TotalTokens:  state.totalTokens,
		Metadata:     runtimeMetadataWithExtras(sessionID, c.spec.Metadata),
	})
	c.emitSessionUpdate(session)

	if terminalType == "turn.cancelled" && finalErr == nil {
		finalErr = context.Canceled
	}
	return output, terminalType, finalErr
}

func (c *stdioClient) recordClaudeStreamDelta(state *claudeTurnState, delta string) {
	if state == nil || delta == "" {
		return
	}

	sessionID := c.currentClaudeSessionIDLocked()
	turnID := strings.TrimSpace(state.turnID)
	if turnID == "" {
		turnID = fallbackClaudeTurnID(c, state)
		state.turnID = turnID
	}
	itemID := "stream-" + turnID
	phase := firstNonEmpty(state.itemPhase, "commentary")
	text := strings.TrimSpace(state.streamedOutput.String())

	eventType := "item.agentMessage.delta"
	c.mu.Lock()
	c.session.IssueID = c.spec.IssueID
	c.session.IssueIdentifier = c.spec.IssueIdentifier
	if sessionID != "" {
		c.session.ThreadID = sessionID
		c.session.SessionID = sessionID
		c.syncClaudeMetadataLocked(sessionID)
	}
	if !state.streamStarted {
		eventType = "item.started"
		state.streamStarted = true
	}
	event := agentruntime.Event{
		Type:      eventType,
		ThreadID:  sessionID,
		TurnID:    turnID,
		ItemID:    itemID,
		ItemType:  "agentMessage",
		ItemPhase: phase,
		Message:   text,
	}
	if eventType == "item.agentMessage.delta" {
		event.Chunk = delta
	}
	c.session.ApplyEvent(event)
	c.normalizeClaudeSessionIdentityLocked()
	session := c.session.Clone()
	c.mu.Unlock()

	if eventType == "item.started" {
		c.emitActivity(agentruntime.ActivityEvent{
			Type:      "item.started",
			ThreadID:  sessionID,
			TurnID:    turnID,
			ItemID:    itemID,
			ItemType:  "agentMessage",
			ItemPhase: phase,
			Item: map[string]interface{}{
				"id":    itemID,
				"type":  "agentMessage",
				"phase": phase,
				"text":  text,
			},
			Metadata: runtimeMetadataWithExtras(sessionID, c.spec.Metadata),
		})
	} else {
		c.emitActivity(agentruntime.ActivityEvent{
			Type:      "item.agentMessage.delta",
			ThreadID:  sessionID,
			TurnID:    turnID,
			ItemID:    itemID,
			ItemType:  "agentMessage",
			ItemPhase: phase,
			Delta:     delta,
			Metadata:  runtimeMetadataWithExtras(sessionID, c.spec.Metadata),
		})
	}
	c.emitSessionUpdate(session)
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

func (c *stdioClient) buildClaudeCommand() (string, error) {
	c.mu.Lock()
	spec := c.spec
	resumeToken := strings.TrimSpace(c.session.ThreadID)
	if resumeToken == "" {
		resumeToken = strings.TrimSpace(c.spec.ResumeToken)
	}
	mcpConfigPath := c.mcpConfigPath
	settingsPath := c.settingsPath
	c.mu.Unlock()

	return composeClaudeCommand(spec, resumeToken, mcpConfigPath, settingsPath)
}

func (c *stdioClient) currentClaudeSessionIDLocked() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sessionID := strings.TrimSpace(c.session.ThreadID); sessionID != "" {
		return sessionID
	}
	return strings.TrimSpace(c.spec.ResumeToken)
}

func (c *stdioClient) recordClaudeSessionID(sessionID string) agentruntime.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recordClaudeSessionIDLocked(sessionID)
	return c.session.Clone()
}

func (c *stdioClient) recordClaudeSessionIDLocked(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	c.session.ThreadID = sessionID
	c.session.SessionID = sessionID
	c.syncClaudeMetadataLocked(sessionID)
}

func (c *stdioClient) syncClaudeMetadataLocked(sessionID string) {
	if c.session.Metadata == nil {
		c.session.Metadata = runtimeMetadataWithExtras(sessionID, c.spec.Metadata)
	}
	c.session.Metadata["session_identifier_strategy"] = claudeSessionIdentifierStrategy
	if sessionID != "" {
		c.session.Metadata["provider_session_id"] = sessionID
	}
}

func (c *stdioClient) normalizeClaudeSessionIdentityLocked() {
	if c.session.Metadata == nil {
		c.session.Metadata = runtimeMetadataWithExtras("", c.spec.Metadata)
	}
	c.session.Metadata["provider"] = string(agentruntime.ProviderClaude)
	c.session.Metadata["transport"] = string(agentruntime.TransportStdio)
	c.session.Metadata["session_identifier_strategy"] = claudeSessionIdentifierStrategy
	if sessionID := strings.TrimSpace(c.session.ThreadID); sessionID != "" {
		c.session.SessionID = sessionID
		c.session.Metadata["provider_session_id"] = sessionID
	}
}

func (c *stdioClient) setActiveTurn(pid int, done chan struct{}) {
	c.mu.Lock()
	c.activeTurn = &runningTurn{
		pid:  pid,
		done: done,
	}
	c.mu.Unlock()
}

func (c *stdioClient) clearActiveTurn(done chan struct{}) {
	c.mu.Lock()
	active := c.activeTurn
	if active != nil && active.done == done {
		c.activeTurn = nil
	}
	c.mu.Unlock()
	if done != nil {
		close(done)
	}
}

func (c *stdioClient) requestActiveTurnStop(done chan struct{}) {
	c.mu.Lock()
	active := c.activeTurn
	if active != nil && done != nil && active.done != done {
		c.mu.Unlock()
		return
	}
	if active != nil {
		active.interrupted = true
	}
	c.mu.Unlock()
	if active == nil {
		return
	}
	active.stopOnce.Do(func() {
		_ = interruptClaudeProcessTree(active.pid)
	})
}

func (c *stdioClient) activeTurnInterrupted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeTurn == nil {
		return false
	}
	return c.activeTurn.interrupted
}

func (c *stdioClient) watchTurnContext(ctx context.Context, done chan struct{}) {
	<-ctx.Done()
	c.requestActiveTurnStop(done)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func asString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func boolFromAny(value interface{}) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	default:
		return false, false
	}
}

func intFromAny(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func mapValue(value interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	if typed, ok := value.(map[string]interface{}); ok {
		return typed
	}
	return nil
}

func stringFromMap(raw map[string]interface{}, keys ...string) string {
	current := raw
	for i, key := range keys {
		if current == nil {
			return ""
		}
		value, ok := current[key]
		if !ok {
			return ""
		}
		if i == len(keys)-1 {
			return asString(value)
		}
		next := mapValue(value)
		if next == nil {
			return ""
		}
		current = next
	}
	return ""
}

func assistantMessageText(message map[string]interface{}) string {
	if message == nil {
		return ""
	}
	if text := strings.TrimSpace(asString(message["text"])); text != "" {
		return text
	}
	content, ok := message["content"].([]interface{})
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(content))
	for _, part := range content {
		block := mapValue(part)
		if block == nil {
			continue
		}
		if text := strings.TrimSpace(asString(block["text"])); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func assistantMessagePhase(message map[string]interface{}) string {
	if message == nil {
		return ""
	}
	if phase := strings.TrimSpace(asString(message["phase"])); phase != "" {
		return phase
	}
	return ""
}

func blockPhase(block map[string]interface{}) string {
	if block == nil {
		return ""
	}
	switch strings.TrimSpace(asString(block["type"])) {
	case "thinking":
		return "thinking"
	case "text":
		return "commentary"
	default:
		return firstNonEmpty(strings.TrimSpace(asString(block["phase"])), "commentary")
	}
}

func deltaText(event map[string]interface{}) string {
	if event == nil {
		return ""
	}
	delta := mapValue(event["delta"])
	if delta != nil {
		if text := strings.TrimSpace(asString(delta["text"])); text != "" {
			return text
		}
		if text := strings.TrimSpace(asString(delta["thinking"])); text != "" {
			return text
		}
		if text := strings.TrimSpace(asString(delta["partial_text"])); text != "" {
			return text
		}
	}
	if text := strings.TrimSpace(asString(event["text"])); text != "" {
		return text
	}
	return ""
}

func usageTokens(raw interface{}) (input, output, total int) {
	usage := mapValue(raw)
	if usage == nil {
		return 0, 0, 0
	}
	if v, ok := intFromAny(usage["input_tokens"]); ok {
		input = v
	}
	if v, ok := intFromAny(usage["output_tokens"]); ok {
		output = v
	}
	if v, ok := intFromAny(usage["total_tokens"]); ok {
		total = v
	}
	return input, output, total
}

func fallbackClaudeTurnID(c *stdioClient, state *claudeTurnState) string {
	if state != nil {
		if id := strings.TrimSpace(state.resultUUID); id != "" {
			return id
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counter <= 0 {
		return "turn-1"
	}
	return fmt.Sprintf("turn-%d", c.counter)
}

func claudePermissionMode(config agentruntime.PermissionConfig) string {
	switch strings.TrimSpace(strings.ToLower(config.CollaborationMode)) {
	case "plan":
		return "plan"
	default:
		return "default"
	}
}

func claudeUsesApprovalPrompt(config agentruntime.PermissionConfig) bool {
	return strings.TrimSpace(config.ThreadSandbox) != "danger-full-access"
}

func claudeAllowedTools(config agentruntime.PermissionConfig) string {
	if !claudeUsesApprovalPrompt(config) {
		return "Bash,Edit,Write,MultiEdit"
	}
	return ""
}

func buildClaudeCommand(spec agentruntime.RuntimeSpec) (string, func(), error) {
	if strings.TrimSpace(spec.DBPath) == "" {
		return "", nil, fmt.Errorf("claude runtime requires a db path for the live Maestro MCP bridge")
	}
	configPath, settingsPath, cleanup, err := writeClaudeSupportFiles(spec.DBPath)
	if err != nil {
		return "", nil, err
	}
	command, err := composeClaudeCommand(spec, strings.TrimSpace(spec.ResumeToken), configPath, settingsPath)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return command, cleanup, nil
}

func composeClaudeCommand(spec agentruntime.RuntimeSpec, resumeToken, mcpConfigPath, settingsPath string) (string, error) {
	command := strings.TrimSpace(spec.Command)
	if command == "" {
		command = "claude"
	}
	if strings.TrimSpace(mcpConfigPath) == "" {
		return "", fmt.Errorf("claude runtime requires a mcp config path")
	}
	if strings.TrimSpace(settingsPath) == "" {
		return "", fmt.Errorf("claude runtime requires a settings overlay path")
	}

	args := []string{
		"-p",
		"--verbose",
		"--output-format=stream-json",
		"--include-partial-messages",
		"--permission-mode",
		claudePermissionMode(spec.Permissions),
		"--settings",
		settingsPath,
	}

	if allowedTools := claudeAllowedTools(spec.Permissions); allowedTools != "" {
		args = append(args,
			"--allowed-tools",
			allowedTools,
		)
	} else {
		args = append(args,
			"--permission-prompt-tool",
			"mcp__maestro__approval_prompt",
		)
	}

	if resumeToken != "" {
		args = append(args, "-r", resumeToken)
	}

	args = append(args,
		"--mcp-config",
		mcpConfigPath,
		"--strict-mcp-config",
	)

	for _, arg := range args {
		command += " " + shellQuoteArg(arg)
	}
	return command, nil
}

func writeClaudeSupportFiles(dbPath string) (string, string, func(), error) {
	dir, err := os.MkdirTemp("", "maestro-claude-mcp-*")
	if err != nil {
		return "", "", nil, err
	}

	cleanup := func() {
		_ = os.RemoveAll(dir)
	}

	mcpConfigPath := filepath.Join(dir, "mcp.json")
	settingsPath := filepath.Join(dir, "settings.json")

	mcpConfig := map[string]interface{}{
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
	if err := writeClaudeJSONFile(mcpConfigPath, mcpConfig); err != nil {
		cleanup()
		return "", "", nil, err
	}
	if err := writeClaudeJSONFile(settingsPath, claudeSessionSettingsOverlay()); err != nil {
		cleanup()
		return "", "", nil, err
	}

	return mcpConfigPath, settingsPath, cleanup, nil
}

func writeClaudeMCPConfig(dbPath string) (string, func(), error) {
	configPath, _, cleanup, err := writeClaudeSupportFiles(dbPath)
	if err != nil {
		return "", nil, err
	}
	return configPath, cleanup, nil
}

// This overlay keeps Claude inside Maestro-managed approval flows for the session.
func claudeSessionSettingsOverlay() map[string]interface{} {
	return map[string]interface{}{
		"disableAutoMode":        "disable",
		"useAutoModeDuringPlan":  false,
		"disableAllHooks":        true,
		"includeGitInstructions": false,
		"permissions": map[string]interface{}{
			"disableBypassPermissionsMode": "disable",
		},
	}
}

func writeClaudeJSONFile(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return nil
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

func runtimeMetadata(sessionID string) map[string]interface{} {
	return runtimeMetadataWithExtras(sessionID, nil)
}

func runtimeMetadataWithExtras(sessionID string, extra map[string]interface{}) map[string]interface{} {
	metadata := map[string]interface{}{
		"provider":                    string(agentruntime.ProviderClaude),
		"transport":                   string(agentruntime.TransportStdio),
		"session_identifier_strategy": claudeSessionIdentifierStrategy,
	}
	for key, value := range extra {
		metadata[key] = value
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		metadata["provider_session_id"] = sessionID
	}
	return metadata
}
