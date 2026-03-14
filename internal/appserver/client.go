package appserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/olhapi/maestro/internal/appserver/protocol"
	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
)

const nonInteractiveToolInputAnswer = "This is a non-interactive session. Operator input is unavailable."

type ToolExecutor func(ctx context.Context, name string, arguments interface{}) map[string]interface{}

type ClientConfig struct {
	Executable        string
	Args              []string
	Env               []string
	Workspace         string
	WorkspaceRoot     string
	IssueID           string
	IssueIdentifier   string
	Prompt            string
	Title             string
	CodexCommand      string
	ExpectedVersion   string
	ApprovalPolicy    interface{}
	ThreadSandbox     string
	TurnSandboxPolicy map[string]interface{}
	ReadTimeout       time.Duration
	TurnTimeout       time.Duration
	StallTimeout      time.Duration
	DynamicTools      []map[string]interface{}
	ToolExecutor      ToolExecutor
	Logger            *slog.Logger
	OnMessage         func(map[string]interface{})
	OnSessionUpdate   func(*Session)
	OnActivityEvent   func(ActivityEvent)
	ResumeThreadID    string
	ResumeSource      string
}

type Result struct {
	Output  string
	Session *Session
}

type RunError struct {
	Kind    string
	Payload map[string]interface{}
	Err     error
}

func (e *RunError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Kind, e.Err)
	}
	return e.Kind
}

func (e *RunError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Client struct {
	cfg     ClientConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	session *Session
	logger  *slog.Logger

	lines   chan string
	lineErr chan error
	waitCh  chan error

	outputMu      sync.Mutex
	output        bytes.Buffer
	threadResumed bool

	requestMu sync.Mutex
	nextID    int
	closeOnce sync.Once
}

func Start(ctx context.Context, cfg ClientConfig) (*Client, error) {
	if err := validateWorkspaceCWD(cfg.Workspace, cfg.WorkspaceRoot); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Executable) == "" {
		return nil, fmt.Errorf("missing app-server executable")
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 5 * time.Second
	}
	if cfg.StallTimeout <= 0 {
		cfg.StallTimeout = 5 * time.Minute
	}
	if cfg.ThreadSandbox == "" {
		cfg.ThreadSandbox = "workspace-write"
	}
	if cfg.ApprovalPolicy == nil {
		cfg.ApprovalPolicy = defaultApprovalPolicy()
	}
	cfg.TurnSandboxPolicy = normalizeTurnSandboxPolicy(cfg.TurnSandboxPolicy, cfg.Workspace, cfg.WorkspaceRoot)
	if strings.TrimSpace(cfg.CodexCommand) == "" && looksLikeCodexCommand(cfg.Executable) {
		cfg.CodexCommand = cfg.Executable
	}
	args := append([]string(nil), cfg.Args...)
	if len(args) == 0 && looksLikeCodexCommand(cfg.Executable) {
		args = append(args, "app-server")
	}

	cmd := exec.CommandContext(ctx, cfg.Executable, args...)
	cmd.Dir = cfg.Workspace
	if len(cfg.Env) > 0 {
		cmd.Env = cfg.Env
	} else {
		cmd.Env = os.Environ()
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	client := &Client{
		cfg:     cfg,
		cmd:     cmd,
		stdin:   stdin,
		session: &Session{MaxHistory: defaultSessionHistoryLimit},
		lines:   make(chan string, 128),
		lineErr: make(chan error, 1),
		waitCh:  make(chan error, 1),
		nextID:  1,
	}
	client.session.IssueID = cfg.IssueID
	client.session.IssueIdentifier = cfg.IssueIdentifier
	client.logger = client.newLogger()
	client.warnOnCodexVersionMismatch()
	client.session.AppServerPID = cmd.Process.Pid
	client.logger.Info("Codex app-server process started", "pid", cmd.Process.Pid)

	var wg sync.WaitGroup
	wg.Add(2)
	go client.readStdout(&wg, stdoutPipe)
	go client.readStderr(&wg, stderrPipe)
	go func() {
		err := cmd.Wait()
		wg.Wait()
		if err != nil {
			client.logger.Warn("Codex app-server process exited with error", "error", err)
		} else {
			client.logger.Info("Codex app-server process exited cleanly")
		}
		select {
		case client.waitCh <- err:
		default:
		}
	}()

	if err := client.initialize(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func Run(ctx context.Context, cfg ClientConfig) (*Result, error) {
	client, err := Start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	if err := client.RunTurn(ctx, cfg.Prompt, cfg.Title); err != nil {
		return &Result{Output: client.Output(), Session: client.Session()}, err
	}
	if err := client.Wait(); err != nil {
		return &Result{Output: client.Output(), Session: client.Session()}, err
	}
	return &Result{Output: client.Output(), Session: client.Session()}, nil
}

func (c *Client) Session() *Session {
	cp := *c.session
	cp.History = append([]Event(nil), c.session.History...)
	return &cp
}

func (c *Client) Output() string {
	c.outputMu.Lock()
	defer c.outputMu.Unlock()
	return strings.TrimSpace(c.output.String())
}

func (c *Client) ThreadResumed() bool {
	return c.threadResumed
}

func (c *Client) Wait() error {
	select {
	case err := <-c.waitCh:
		if err == nil {
			select {
			case readErr := <-c.lineErr:
				if readErr != nil && !isBenignReadCloseError(readErr) {
					return readErr
				}
			default:
			}
		}
		return err
	default:
		return nil
	}
}

func (c *Client) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			select {
			case err := <-c.waitCh:
				closeErr = err
			case <-time.After(100 * time.Millisecond):
				_ = c.cmd.Process.Kill()
				select {
				case err := <-c.waitCh:
					closeErr = err
				case <-time.After(500 * time.Millisecond):
				}
			}
		}
	})
	return closeErr
}

func (c *Client) RunTurn(ctx context.Context, prompt, title string) error {
	return c.RunTurnWithStartCallback(ctx, prompt, title, nil)
}

func (c *Client) RunTurnWithStartCallback(ctx context.Context, prompt, title string, onStarted func(*Session)) error {
	return c.RunTurnWithInputsAndStartCallback(ctx, []gen.UserInputElement{protocol.TextInput(prompt)}, title, onStarted)
}

func (c *Client) RunTurnWithInputs(ctx context.Context, input []gen.UserInputElement, title string) error {
	return c.RunTurnWithInputsAndStartCallback(ctx, input, title, nil)
}

func (c *Client) RunTurnWithInputsAndStartCallback(ctx context.Context, input []gen.UserInputElement, title string, onStarted func(*Session)) error {
	requestID := c.nextRequestID()
	req, err := protocol.TurnStartRequest(requestID, c.session.ThreadID, input, filepath.Clean(c.cfg.Workspace), c.cfg.ApprovalPolicy, c.cfg.TurnSandboxPolicy)
	if err != nil {
		return err
	}
	if err := c.sendMessage(req); err != nil {
		return err
	}
	resp, err := c.awaitResponse(ctx, requestID)
	if err != nil {
		return err
	}
	var result gen.TurnStartResponse
	if err := resp.UnmarshalResult(&result); err != nil {
		return fmt.Errorf("decode turn/start response: %w", err)
	}
	turnID := strings.TrimSpace(result.Turn.ID)
	if turnID == "" {
		return fmt.Errorf("invalid turn/start response: missing turn.id")
	}
	if !c.hasTurnStarted(turnID) {
		c.applyEvent(Event{Type: "turn.started", ThreadID: c.session.ThreadID, TurnID: turnID})
		c.emitActivityEvent(ActivityEvent{
			Type:     "turn.started",
			ThreadID: c.session.ThreadID,
			TurnID:   turnID,
		})
	}
	c.logger.Info("Codex turn started",
		"session_id", c.session.SessionID,
		"thread_id", c.session.ThreadID,
		"turn_id", turnID,
		"title", title,
	)
	c.emitMessage("session_started", map[string]interface{}{
		"session_id": c.session.SessionID,
		"thread_id":  c.session.ThreadID,
		"turn_id":    turnID,
	})
	if onStarted != nil {
		onStarted(c.Session())
	}
	return c.awaitTurnCompletion(ctx)
}

func (c *Client) initialize(ctx context.Context) error {
	if err := c.sendMessage(protocol.InitializeRequest(c.nextRequestID(), "Maestro")); err != nil {
		return err
	}
	if _, err := c.awaitResponse(ctx, 1); err != nil {
		return err
	}
	c.logger.Info("Codex session initialized")
	if err := c.sendMessage(protocol.InitializedNotification()); err != nil {
		return err
	}

	return c.initializeThread(ctx)
}

func (c *Client) initializeThread(ctx context.Context) error {
	if threadID, resumed := c.tryResumeThread(ctx); resumed {
		c.session.ThreadID = threadID
		c.threadResumed = true
		c.logger.Info("Codex thread resumed", "thread_id", threadID, "source", strings.TrimSpace(c.cfg.ResumeSource))
		return nil
	}

	threadID, err := c.startThread(ctx)
	if err != nil {
		return err
	}
	c.session.ThreadID = threadID
	c.threadResumed = false
	c.logger.Info("Codex thread started", "thread_id", threadID)
	return nil
}

func (c *Client) tryResumeThread(ctx context.Context) (string, bool) {
	threadID := strings.TrimSpace(c.cfg.ResumeThreadID)
	if threadID == "" {
		return "", false
	}

	requestID := c.nextRequestID()
	req, err := protocol.ThreadResumeRequest(requestID, threadID, filepath.Clean(c.cfg.Workspace), c.cfg.ApprovalPolicy, c.cfg.ThreadSandbox)
	if err != nil {
		c.logger.Warn("Codex thread resume unavailable; falling back to thread/start",
			"thread_id", threadID,
			"source", strings.TrimSpace(c.cfg.ResumeSource),
			"error", err,
		)
		return "", false
	}
	if err := c.sendMessage(req); err != nil {
		c.logger.Warn("Codex thread resume send failed; falling back to thread/start",
			"thread_id", threadID,
			"source", strings.TrimSpace(c.cfg.ResumeSource),
			"error", err,
		)
		return "", false
	}
	resp, err := c.awaitResponse(ctx, requestID)
	if err != nil {
		c.logger.Warn("Codex thread resume failed; falling back to thread/start",
			"thread_id", threadID,
			"source", strings.TrimSpace(c.cfg.ResumeSource),
			"error", err,
		)
		return "", false
	}
	resumedThreadID, err := decodeThreadResponse(resp)
	if err != nil {
		c.logger.Warn("Codex thread resume returned invalid payload; falling back to thread/start",
			"thread_id", threadID,
			"source", strings.TrimSpace(c.cfg.ResumeSource),
			"error", err,
		)
		return "", false
	}
	return resumedThreadID, true
}

func (c *Client) startThread(ctx context.Context) (string, error) {
	requestID := c.nextRequestID()
	req, err := protocol.ThreadStartRequest(requestID, filepath.Clean(c.cfg.Workspace), c.cfg.ApprovalPolicy, c.cfg.ThreadSandbox, c.cfg.DynamicTools)
	if err != nil {
		return "", err
	}
	if err := c.sendMessage(req); err != nil {
		return "", err
	}
	resp, err := c.awaitResponse(ctx, requestID)
	if err != nil {
		return "", err
	}
	threadID, err := decodeThreadResponse(resp)
	if err != nil {
		return "", fmt.Errorf("decode thread/start response: %w", err)
	}
	return threadID, nil
}

func decodeThreadResponse(resp protocol.Message) (string, error) {
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := resp.UnmarshalResult(&result); err != nil {
		return "", err
	}
	threadID := strings.TrimSpace(result.Thread.ID)
	if threadID == "" {
		return "", fmt.Errorf("missing thread.id")
	}
	return threadID, nil
}

func (c *Client) nextRequestID() int {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	id := c.nextID
	c.nextID++
	return id
}

func (c *Client) awaitResponse(ctx context.Context, requestID int) (protocol.Message, error) {
	for {
		line, err := c.nextLine(ctx, c.cfg.ReadTimeout)
		if err != nil {
			return protocol.Message{}, err
		}
		payload, ok := protocol.DecodeMessage(line)
		if !ok {
			c.logStreamOutput("response", line)
			continue
		}
		c.captureEvent(line, payload)
		if payload.IsResponseTo(requestID) {
			if payload.Error != nil {
				return protocol.Message{}, &RunError{Kind: "response_error", Payload: payload.Raw, Err: fmt.Errorf("%v", payload.Error)}
			}
			if len(payload.Result) > 0 && string(payload.Result) != "null" {
				return payload, nil
			}
			return protocol.Message{}, &RunError{Kind: "response_error", Payload: payload.Raw, Err: fmt.Errorf("missing result")}
		}
	}
}

func (c *Client) awaitTurnCompletion(ctx context.Context) error {
	var deadline time.Time
	if c.cfg.TurnTimeout > 0 {
		deadline = time.Now().Add(c.cfg.TurnTimeout)
	}
	lastProgressAt := time.Now()

	for {
		timeout := c.cfg.ReadTimeout
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return &RunError{Kind: "turn_timeout"}
			}
			if timeout <= 0 || remaining < timeout {
				timeout = remaining
			}
		}

		line, err := c.nextLine(ctx, timeout)
		if err != nil {
			var runErr *RunError
			if errors.As(err, &runErr) && runErr.Kind == "read_timeout" {
				if c.cfg.StallTimeout > 0 && time.Since(lastProgressAt) >= c.cfg.StallTimeout {
					c.logger.Warn("Codex turn stalled",
						"session_id", c.session.SessionID,
						"thread_id", c.session.ThreadID,
						"turn_id", c.session.TurnID,
						"stall_timeout_ms", c.cfg.StallTimeout.Milliseconds(),
					)
					return &RunError{Kind: "stall_timeout"}
				}
				continue
			}
			if errors.Is(err, io.EOF) && c.turnFinishedByCleanProcessExit(100*time.Millisecond) {
				c.logger.Info("Codex turn completed after clean app-server exit",
					"session_id", c.session.SessionID,
					"thread_id", c.session.ThreadID,
					"turn_id", c.session.TurnID,
				)
				return nil
			}
			return err
		}
		lastProgressAt = time.Now()
		payload, ok := protocol.DecodeMessage(line)
		if !ok {
			c.logStreamOutput("turn", line)
			continue
		}
		c.captureEvent(line, payload)

		method := payload.Method
		switch method {
		case protocol.MethodTurnCompleted:
			c.logger.Info("Codex turn completed",
				"session_id", c.session.SessionID,
				"thread_id", c.session.ThreadID,
				"turn_id", c.session.TurnID,
			)
			return nil
		case protocol.MethodTurnFailed:
			c.logger.Warn("Codex turn failed",
				"session_id", c.session.SessionID,
				"thread_id", c.session.ThreadID,
				"turn_id", c.session.TurnID,
			)
			return &RunError{Kind: "turn_failed", Payload: payload.Raw}
		case protocol.MethodTurnCancelled:
			c.logger.Warn("Codex turn cancelled",
				"session_id", c.session.SessionID,
				"thread_id", c.session.ThreadID,
				"turn_id", c.session.TurnID,
			)
			return &RunError{Kind: "turn_cancelled", Payload: payload.Raw}
		}

		handled, err := c.handleRequest(ctx, payload)
		if err != nil {
			return err
		}
		if handled {
			continue
		}
		if needsInput(method, payload.Raw) {
			return &RunError{Kind: "turn_input_required", Payload: payload.Raw}
		}
	}
}

func (c *Client) turnFinishedByCleanProcessExit(wait time.Duration) bool {
	if c.session == nil || strings.TrimSpace(c.session.ThreadID) == "" || strings.TrimSpace(c.session.TurnID) == "" {
		return false
	}
	readWaitResult := func(err error) bool {
		select {
		case c.waitCh <- err:
		default:
		}
		return err == nil
	}
	select {
	case err := <-c.waitCh:
		return readWaitResult(err)
	default:
	}
	if wait <= 0 {
		return false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-c.waitCh:
		return readWaitResult(err)
	case <-timer.C:
		return false
	}
}

func (c *Client) handleRequest(ctx context.Context, payload protocol.Message) (bool, error) {
	method := payload.Method
	if method == "" || !payload.HasID() {
		return false, nil
	}

	switch method {
	case protocol.MethodItemCommandExecutionApproval:
		if c.autoApproveRequests() {
			if err := c.sendMessage(protocol.CommandExecutionApprovalResult(payload.ID, gen.AcceptForSession)); err != nil {
				return true, err
			}
			c.logger.Info("Codex approval auto-approved", "method", method)
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload.Raw})
			return true, nil
		}
		c.logger.Warn("Codex approval required", "method", method)
		return true, &RunError{Kind: "approval_required", Payload: payload.Raw}
	case protocol.MethodItemFileChangeApproval:
		if c.autoApproveRequests() {
			if err := c.sendMessage(protocol.FileChangeApprovalResult(payload.ID, gen.AcceptForSession)); err != nil {
				return true, err
			}
			c.logger.Info("Codex approval auto-approved", "method", method)
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload.Raw})
			return true, nil
		}
		c.logger.Warn("Codex approval required", "method", method)
		return true, &RunError{Kind: "approval_required", Payload: payload.Raw}
	case protocol.MethodExecCommandApproval:
		if c.autoApproveRequests() {
			if err := c.sendMessage(protocol.ExecCommandApprovalResult(payload.ID, gen.ApprovedForSession)); err != nil {
				return true, err
			}
			c.logger.Info("Codex approval auto-approved", "method", method)
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload.Raw})
			return true, nil
		}
		c.logger.Warn("Codex approval required", "method", method)
		return true, &RunError{Kind: "approval_required", Payload: payload.Raw}
	case protocol.MethodApplyPatchApproval:
		if c.autoApproveRequests() {
			if err := c.sendMessage(protocol.ApplyPatchApprovalResult(payload.ID, gen.ApprovedForSession)); err != nil {
				return true, err
			}
			c.logger.Info("Codex approval auto-approved", "method", method)
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload.Raw})
			return true, nil
		}
		c.logger.Warn("Codex approval required", "method", method)
		return true, &RunError{Kind: "approval_required", Payload: payload.Raw}
	case protocol.MethodToolRequestUserInput:
		var params gen.ToolRequestUserInputParams
		if err := payload.UnmarshalParams(&params); err != nil {
			return true, err
		}
		answers, autoApproved := answersForToolInputParams(params, c.autoApproveRequests())
		if answers == nil {
			c.logger.Warn("Codex turn input required", "method", method)
			return true, &RunError{Kind: "turn_input_required", Payload: payload.Raw}
		}
		if err := c.sendMessage(protocol.ToolRequestUserInputResult(payload.ID, answers)); err != nil {
			return true, err
		}
		if autoApproved {
			c.logger.Info("Codex tool input auto-approved", "method", method)
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload.Raw})
		} else {
			c.logger.Info("Codex tool input auto-answered", "method", method)
			c.emitMessage("tool_input_auto_answered", map[string]interface{}{
				"payload": payload.Raw,
				"answer":  nonInteractiveToolInputAnswer,
			})
		}
		return true, nil
	case protocol.MethodToolCall:
		var params gen.DynamicToolCallParams
		if err := payload.UnmarshalParams(&params); err != nil {
			return true, err
		}
		result := unsupportedToolResult(params.Tool, supportedToolNames(c.cfg.DynamicTools))
		if c.cfg.ToolExecutor != nil {
			result = c.cfg.ToolExecutor(ctx, params.Tool, params.Arguments)
		}
		typedResult, err := protocol.DynamicToolCallResultFromMap(payload.ID, result)
		if err != nil {
			return true, err
		}
		if err := c.sendMessage(typedResult); err != nil {
			return true, err
		}
		eventName := "tool_call_completed"
		if success, _ := result["success"].(bool); !success {
			c.logger.Warn("Codex tool call failed", "tool", params.Tool)
			eventName = "tool_call_failed"
		} else {
			c.logger.Info("Codex tool call completed", "tool", params.Tool)
		}
		c.emitMessage(eventName, map[string]interface{}{"payload": payload.Raw})
		return true, nil
	default:
		return false, nil
	}
}

func (c *Client) autoApproveRequests() bool {
	if s, ok := c.cfg.ApprovalPolicy.(string); ok {
		return strings.EqualFold(strings.TrimSpace(s), "never")
	}
	return false
}

func (c *Client) nextLine(ctx context.Context, timeout time.Duration) (string, error) {
	var timer <-chan time.Time
	if timeout > 0 {
		timer = time.After(timeout)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer:
		c.logger.Debug("Codex app-server read timeout", "timeout_ms", timeout.Milliseconds())
		return "", &RunError{Kind: "read_timeout"}
	case line, ok := <-c.lines:
		if !ok {
			select {
			case err := <-c.lineErr:
				if err == nil || isBenignReadCloseError(err) {
					return "", io.EOF
				}
				return "", err
			default:
				return "", io.EOF
			}
		}
		return line, nil
	}
}

func (c *Client) readStdout(wg *sync.WaitGroup, r io.Reader) {
	defer wg.Done()
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			c.writeOutput(trimmed, false)
			c.lines <- trimmed
		}
		if err != nil {
			lineErr := err
			if isBenignReadCloseError(err) {
				lineErr = io.EOF
			}
			select {
			case c.lineErr <- lineErr:
			default:
			}
			close(c.lines)
			return
		}
	}
}

func isBenignReadCloseError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "file already closed") || strings.Contains(msg, "closed pipe")
}

func (c *Client) readStderr(wg *sync.WaitGroup, r io.Reader) {
	defer wg.Done()
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			c.writeOutput(trimmed, true)
			c.logStreamOutput("stderr", trimmed)
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) sendMessage(payload interface{}) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(encoded, '\n'))
	return err
}

func (c *Client) captureEvent(line string, payload protocol.Message) {
	if activity, ok := ActivityEventFromMessage(payload); ok {
		c.emitActivityEvent(activity)
	}
	var primary Event
	var primaryOK bool
	if payload.Method != "" {
		primary, primaryOK = EventFromMessage(payload)
	}
	fallback, fallbackOK := ParseEventLine(line)
	switch {
	case primaryOK && fallbackOK:
		c.applyEvent(MergeEvents(primary, fallback))
	case primaryOK:
		c.applyEvent(primary)
	case fallbackOK:
		c.applyEvent(fallback)
	}
}

func (c *Client) hasTurnStarted(turnID string) bool {
	if c.session == nil || strings.TrimSpace(turnID) == "" {
		return false
	}
	for i := len(c.session.History) - 1; i >= 0; i-- {
		event := c.session.History[i]
		if event.TurnID != turnID {
			continue
		}
		return event.Type == "turn.started"
	}
	return false
}

func (c *Client) applyEvent(evt Event) {
	c.session.ApplyEvent(evt)
	if c.cfg.OnSessionUpdate == nil {
		return
	}
	cp := *c.session
	cp.History = append([]Event(nil), c.session.History...)
	c.cfg.OnSessionUpdate(&cp)
}

func (c *Client) emitActivityEvent(event ActivityEvent) {
	if c.cfg.OnActivityEvent == nil {
		return
	}
	c.cfg.OnActivityEvent(event)
}

func (c *Client) emitMessage(event string, details map[string]interface{}) {
	if c.cfg.OnMessage == nil {
		return
	}
	msg := map[string]interface{}{
		"event":     event,
		"timestamp": time.Now().UTC(),
	}
	for k, v := range details {
		msg[k] = v
	}
	c.cfg.OnMessage(msg)
}

func (c *Client) writeOutput(line string, stderr bool) {
	c.outputMu.Lock()
	defer c.outputMu.Unlock()
	if stderr {
		c.output.WriteString("[stderr] ")
	}
	c.output.WriteString(line)
	c.output.WriteByte('\n')
}

func (c *Client) warnOnCodexVersionMismatch() {
	if strings.TrimSpace(c.cfg.ExpectedVersion) == "" {
		return
	}
	status, err := DetectCodexVersion(c.cfg.CodexCommand)
	if err != nil {
		if strings.TrimSpace(c.cfg.CodexCommand) != "" {
			c.logger.Warn("Unable to detect Codex CLI version", "command", c.cfg.CodexCommand, "error", err)
		}
		return
	}
	if status.ExecutablePath == "" || status.Actual == "" {
		return
	}
	if status.Actual != c.cfg.ExpectedVersion {
		c.logger.Warn("Codex CLI version mismatch",
			"command", c.cfg.CodexCommand,
			"executable", status.ExecutablePath,
			"expected_version", c.cfg.ExpectedVersion,
			"actual_version", status.Actual,
		)
	}
}

func validateWorkspaceCWD(workspace, workspaceRoot string) error {
	workspacePath := filepath.Clean(workspace)
	rootPath := filepath.Clean(workspaceRoot)
	if workspacePath == "" || rootPath == "" {
		return nil
	}
	workspaceAbs, err := filepath.Abs(workspacePath)
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(rootPath)
	if err != nil {
		return err
	}
	if workspaceAbs == rootAbs {
		return &RunError{Kind: "invalid_workspace_cwd", Err: fmt.Errorf("workspace root rejected: %s", workspaceAbs)}
	}
	if workspaceAbs != rootAbs && !strings.HasPrefix(workspaceAbs, rootAbs+string(os.PathSeparator)) {
		return &RunError{Kind: "invalid_workspace_cwd", Err: fmt.Errorf("workspace outside root: %s not under %s", workspaceAbs, rootAbs)}
	}
	return nil
}

func defaultApprovalPolicy() map[string]interface{} {
	return map[string]interface{}{
		"reject": map[string]interface{}{
			"sandbox_approval": true,
			"rules":            true,
			"mcp_elicitations": true,
		},
	}
}

func normalizeTurnSandboxPolicy(raw map[string]interface{}, workspace, workspaceRoot string) map[string]interface{} {
	if raw == nil {
		return defaultTurnSandboxPolicy(workspace, workspaceRoot)
	}

	sandbox := make(map[string]interface{}, len(raw)+4)
	for k, v := range raw {
		sandbox[k] = v
	}
	if strings.TrimSpace(fmt.Sprintf("%v", sandbox["type"])) == "" {
		sandbox["type"] = "workspaceWrite"
	}
	if _, ok := sandbox["networkAccess"]; !ok {
		sandbox["networkAccess"] = true
	}

	if strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", sandbox["type"])), "workspaceWrite") {
		if _, ok := sandbox["writableRoots"]; !ok {
			sandbox["writableRoots"] = defaultSandboxWritableRoots(workspace, workspaceRoot)
		}
		if _, ok := sandbox["readOnlyAccess"]; !ok {
			sandbox["readOnlyAccess"] = map[string]interface{}{"type": "fullAccess"}
		}
		if _, ok := sandbox["excludeTmpdirEnvVar"]; !ok {
			sandbox["excludeTmpdirEnvVar"] = false
		}
		if _, ok := sandbox["excludeSlashTmp"]; !ok {
			sandbox["excludeSlashTmp"] = false
		}
	}
	return sandbox
}

func defaultSandboxWritableRoots(workspace, workspaceRoot string) []string {
	roots := make([]string, 0, 3)
	seen := map[string]struct{}{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = filepath.Clean(path)
		}
		if abs == "" {
			return
		}
		if _, ok := seen[abs]; ok {
			return
		}
		seen[abs] = struct{}{}
		roots = append(roots, abs)
	}

	add(workspace)
	add(workspaceRoot)
	if workspaceRoot != "" {
		rootAbs, err := filepath.Abs(workspaceRoot)
		if err != nil {
			rootAbs = filepath.Clean(workspaceRoot)
		}
		parent := filepath.Dir(rootAbs)
		if parent != "" && parent != "." && parent != rootAbs {
			if _, err := os.Stat(filepath.Join(parent, ".git")); err == nil {
				add(parent)
			}
		}
	}

	if len(roots) == 0 {
		add(".")
	}
	return roots
}

func defaultTurnSandboxPolicy(workspace, workspaceRoot string) map[string]interface{} {
	return map[string]interface{}{
		"type":                "workspaceWrite",
		"writableRoots":       defaultSandboxWritableRoots(workspace, workspaceRoot),
		"readOnlyAccess":      map[string]interface{}{"type": "fullAccess"},
		"networkAccess":       true,
		"excludeTmpdirEnvVar": false,
		"excludeSlashTmp":     false,
	}
}

func looksLikeCodexCommand(executable string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(executable)))
	return base == "codex" || base == "codex.exe"
}

func asInt(v interface{}) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	default:
		return 0, false
	}
}

func toolCallName(params map[string]interface{}) string {
	if params == nil {
		return ""
	}
	for _, key := range []string{"tool", "name"} {
		if s, ok := params[key].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func toolCallArguments(params map[string]interface{}) interface{} {
	if params == nil {
		return map[string]interface{}{}
	}
	if args, ok := params["arguments"]; ok {
		return args
	}
	return map[string]interface{}{}
}

func supportedToolNames(specs []map[string]interface{}) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		if name, ok := spec["name"].(string); ok && strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	return names
}

func unsupportedToolResult(tool string, supported []string) map[string]interface{} {
	return map[string]interface{}{
		"success": false,
		"contentItems": []map[string]interface{}{
			{
				"type": "inputText",
				"text": encodeToolPayload(map[string]interface{}{
					"error": map[string]interface{}{
						"message":        fmt.Sprintf("Unsupported dynamic tool: %q.", tool),
						"supportedTools": supported,
					},
				}),
			},
		},
	}
}

func encodeToolPayload(payload interface{}) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("%v", payload)
	}
	return string(data)
}

func decodeJSONObject(line string) (map[string]interface{}, bool) {
	msg, ok := protocol.DecodeMessage(line)
	return msg.Raw, ok
}

func answersForToolInput(params map[string]interface{}, autoApprove bool) (map[string]interface{}, bool) {
	questions, ok := params["questions"].([]interface{})
	if !ok || len(questions) == 0 {
		return nil, false
	}
	answers := make(map[string]interface{}, len(questions))
	for _, raw := range questions {
		q, ok := raw.(map[string]interface{})
		if !ok {
			return nil, false
		}
		id, _ := q["id"].(string)
		if strings.TrimSpace(id) == "" {
			return nil, false
		}
		if autoApprove {
			label := approvalOptionLabel(q["options"])
			if label == "" {
				return nil, false
			}
			answers[id] = map[string]interface{}{"answers": []string{label}}
			continue
		}
		answers[id] = map[string]interface{}{"answers": []string{nonInteractiveToolInputAnswer}}
	}
	return answers, autoApprove
}

func answersForToolInputParams(params gen.ToolRequestUserInputParams, autoApprove bool) (map[string]gen.ToolRequestUserInputAnswer, bool) {
	if len(params.Questions) == 0 {
		return nil, false
	}
	answers := make(map[string]gen.ToolRequestUserInputAnswer, len(params.Questions))
	for _, question := range params.Questions {
		if strings.TrimSpace(question.ID) == "" {
			return nil, false
		}
		if autoApprove {
			label := approvalOptionLabelFromQuestions(question.Options)
			if label == "" {
				return nil, false
			}
			answers[question.ID] = gen.ToolRequestUserInputAnswer{Answers: []string{label}}
			continue
		}
		answers[question.ID] = gen.ToolRequestUserInputAnswer{Answers: []string{nonInteractiveToolInputAnswer}}
	}
	return answers, autoApprove
}

func approvalOptionLabel(v interface{}) string {
	options, ok := v.([]interface{})
	if !ok {
		return ""
	}
	typed := make([]gen.ToolRequestUserInputOption, 0, len(options))
	for _, raw := range options {
		opt, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		label, _ := opt["label"].(string)
		description, _ := opt["description"].(string)
		typed = append(typed, gen.ToolRequestUserInputOption{
			Label:       label,
			Description: description,
		})
	}
	return approvalOptionLabelFromQuestions(typed)
}

func approvalOptionLabelFromQuestions(options []gen.ToolRequestUserInputOption) string {
	var fallback string
	for _, opt := range options {
		label := opt.Label
		normalized := strings.ToLower(strings.TrimSpace(label))
		switch normalized {
		case "approve this session":
			return label
		case "approve once":
			fallback = label
		default:
			if fallback == "" && (strings.HasPrefix(normalized, "approve") || strings.HasPrefix(normalized, "allow")) {
				fallback = label
			}
		}
	}
	return fallback
}

func needsInput(method string, payload map[string]interface{}) bool {
	if strings.HasPrefix(method, "turn/") {
		switch method {
		case "turn/input_required", "turn/needs_input", "turn/need_input", "turn/request_input", "turn/request_response", "turn/provide_input", "turn/approval_required":
			return true
		}
	}
	params, _ := asMap(payload["params"])
	return needsInputField(payload) || needsInputField(params)
}

func needsInputField(payload map[string]interface{}) bool {
	if payload == nil {
		return false
	}
	for _, key := range []string{"requiresInput", "needsInput", "input_required", "inputRequired"} {
		if payload[key] == true {
			return true
		}
	}
	if t, _ := payload["type"].(string); t == "input_required" || t == "needs_input" {
		return true
	}
	return false
}

func (c *Client) logStreamOutput(stream, line string) {
	text := strings.TrimSpace(line)
	if text == "" {
		return
	}
	c.logger.Debug("Codex app-server stream output", "stream", stream, "text", text)
}

func (c *Client) newLogger() *slog.Logger {
	logger := c.cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []any{
		"component", "appserver",
		"workspace", filepath.Clean(c.cfg.Workspace),
	}
	if c.cfg.IssueID != "" {
		attrs = append(attrs, "issue_id", c.cfg.IssueID)
	}
	if c.cfg.IssueIdentifier != "" {
		attrs = append(attrs, "issue_identifier", c.cfg.IssueIdentifier)
	}
	return logger.With(attrs...)
}
