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

const defaultInitialCollaborationMode = "default"

type ToolExecutor func(ctx context.Context, name string, arguments interface{}) map[string]interface{}

type ClientConfig struct {
	Executable               string
	Args                     []string
	Env                      []string
	Workspace                string
	WorkspaceRoot            string
	IssueID                  string
	IssueIdentifier          string
	Prompt                   string
	Title                    string
	CodexCommand             string
	ExpectedVersion          string
	ApprovalPolicy           interface{}
	InitialCollaborationMode string
	ThreadSandbox            string
	TurnSandboxPolicy        map[string]interface{}
	ReadTimeout              time.Duration
	TurnTimeout              time.Duration
	StallTimeout             time.Duration
	DynamicTools             []map[string]interface{}
	ToolExecutor             ToolExecutor
	Logger                   *slog.Logger
	OnMessage                func(map[string]interface{})
	OnSessionUpdate          func(*Session)
	OnActivityEvent          func(ActivityEvent)
	OnPendingInteraction     func(*PendingInteraction)
	OnPendingInteractionDone func(string)
	ResumeThreadID           string
	ResumeSource             string
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
	output        tailBuffer
	threadResumed bool

	requestMu sync.Mutex
	nextID    int
	closeOnce sync.Once
	configMu  sync.RWMutex

	activeThreadSandbox string

	pendingMu           sync.Mutex
	pendingInteractions map[string]*interactionWaiter
}

type interactionWaiter struct {
	interaction     PendingInteraction
	payloadID       protocol.RequestID
	responseReadyCh chan struct{}
	response        PendingInteractionResponse
	doneCh          chan struct{}
	responded       bool
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
	if strings.TrimSpace(cfg.InitialCollaborationMode) == "" {
		cfg.InitialCollaborationMode = defaultInitialCollaborationMode
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
	configureManagedProcess(cmd)
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
		cfg:                 cfg,
		cmd:                 cmd,
		stdin:               stdin,
		session:             &Session{MaxHistory: defaultSessionHistoryLimit},
		lines:               make(chan string, 128),
		lineErr:             make(chan error, 1),
		waitCh:              make(chan error, 1),
		nextID:              1,
		pendingInteractions: make(map[string]*interactionWaiter),
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
	defer func() {
		_ = client.Close()
	}()

	runErr := client.RunTurn(ctx, cfg.Prompt, cfg.Title)
	result := &Result{Output: client.Output(), Session: client.Session()}
	if runErr != nil {
		return result, runErr
	}
	closeErr := client.closeWithGrace(2 * time.Second)
	if closeErr != nil {
		return result, closeErr
	}
	return result, nil
}

func (c *Client) Session() *Session {
	cp := c.session.Clone()
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
	return c.closeWithGrace(managedProcessKillWait)
}

func (c *Client) closeWithGrace(grace time.Duration) error {
	var closeErr error
	c.closeOnce.Do(func() {
		captureWaitErr := func(err error) error {
			if err != nil {
				return err
			}
			select {
			case readErr := <-c.lineErr:
				if readErr != nil && !isBenignReadCloseError(readErr) {
					return readErr
				}
			default:
			}
			return nil
		}

		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			// Let the app-server observe stdin EOF and exit cleanly before we escalate.
			select {
			case err := <-c.waitCh:
				closeErr = captureWaitErr(err)
				return
			default:
			}
			if grace > 0 {
				select {
				case err := <-c.waitCh:
					if closeErr == nil {
						closeErr = captureWaitErr(err)
					}
					return
				case <-time.After(grace):
				}
			}
			pid := c.cmd.Process.Pid
			if err := terminateManagedProcessTree(pid, managedProcessTerminateWait, managedProcessKillWait); err != nil && closeErr == nil {
				closeErr = err
			}
			select {
			case err := <-c.waitCh:
				if closeErr == nil {
					closeErr = captureWaitErr(err)
				}
			case <-time.After(managedProcessKillWait):
			}
		}
	})
	return closeErr
}

func (c *Client) RespondToInteraction(ctx context.Context, interactionID string, response PendingInteractionResponse) error {
	interactionID = strings.TrimSpace(interactionID)
	if interactionID == "" {
		return fmt.Errorf("%w: missing interaction id", ErrInvalidInteractionResponse)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	c.pendingMu.Lock()
	waiter, ok := c.pendingInteractions[interactionID]
	if !ok {
		c.pendingMu.Unlock()
		return ErrPendingInteractionNotFound
	}
	if waiter.responded {
		c.pendingMu.Unlock()
		return ErrPendingInteractionConflict
	}
	select {
	case <-waiter.doneCh:
		c.pendingMu.Unlock()
		return ErrPendingInteractionNotFound
	default:
	}
	normalized, err := normalizePendingInteractionResponse(waiter.interaction, response)
	if err != nil {
		c.pendingMu.Unlock()
		return err
	}
	waiter.responded = true
	waiter.response = normalized
	close(waiter.responseReadyCh)
	c.pendingMu.Unlock()

	return nil
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
	retriedMissingThread := false
	for {
		if err := c.ensureThreadForTurn(ctx); err != nil {
			return err
		}
		if c.session != nil && c.session.Terminal {
			c.session.ResetTurnState()
		}
		requestID := c.nextRequestID()
		approvalPolicy, _, turnSandboxPolicy := c.permissionConfig()
		req, err := protocol.TurnStartRequest(requestID, c.session.ThreadID, input, filepath.Clean(c.cfg.Workspace), approvalPolicy, turnSandboxPolicy)
		if err != nil {
			return err
		}
		if err := c.sendMessage(req); err != nil {
			return err
		}
		resp, err := c.awaitResponse(ctx, requestID)
		if err != nil {
			if !retriedMissingThread && c.shouldRestartTurnWithFreshThread(err) {
				retriedMissingThread = true
				if restartErr := c.restartTurnThread(ctx); restartErr == nil {
					continue
				}
			}
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
}

func (c *Client) shouldRestartTurnWithFreshThread(err error) bool {
	if strings.TrimSpace(c.session.ThreadID) == "" {
		return false
	}
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Kind != "response_error" {
		return false
	}
	if runErr.Payload != nil {
		if raw, ok := runErr.Payload["error"].(map[string]interface{}); ok {
			if message, _ := raw["message"].(string); strings.Contains(strings.ToLower(strings.TrimSpace(message)), "thread not found") {
				return true
			}
		}
	}
	return strings.Contains(strings.ToLower(runErr.Error()), "thread not found")
}

func (c *Client) restartTurnThread(ctx context.Context) error {
	staleThreadID := strings.TrimSpace(c.session.ThreadID)
	if staleThreadID == "" {
		return fmt.Errorf("missing active thread")
	}
	c.logger.Warn("Codex turn thread missing; restarting with a fresh thread",
		"thread_id", staleThreadID,
		"source", strings.TrimSpace(c.cfg.ResumeSource),
	)
	c.clearActiveThread()
	return c.ensureThreadForTurn(ctx)
}

func (c *Client) clearActiveThread() {
	if c.session != nil {
		c.session.ResetThreadState()
	}
	c.threadResumed = false

	c.configMu.Lock()
	defer c.configMu.Unlock()
	c.activeThreadSandbox = ""
}

func (c *Client) initialize(ctx context.Context) error {
	requestID := c.nextRequestID()
	if err := c.sendMessage(protocol.InitializeRequest(requestID, "Maestro")); err != nil {
		return err
	}
	if _, err := c.awaitResponse(ctx, requestID); err != nil {
		return err
	}
	c.logger.Info("Codex session initialized")
	if err := c.sendMessage(protocol.InitializedNotification()); err != nil {
		return err
	}

	return c.initializeThread(ctx)
}

func (c *Client) initializeThread(ctx context.Context) error {
	_, threadSandbox, _ := c.permissionConfig()
	if threadID, resumed := c.tryResumeThread(ctx); resumed {
		c.activateThread(threadID, threadSandbox, true)
		c.logger.Info("Codex thread resumed", "thread_id", threadID, "source", strings.TrimSpace(c.cfg.ResumeSource))
		return nil
	}

	threadID, err := c.startThread(ctx)
	if err != nil {
		return err
	}
	c.activateThread(threadID, threadSandbox, false)
	c.logger.Info("Codex thread started", "thread_id", threadID)
	return nil
}

func (c *Client) tryResumeThread(ctx context.Context) (string, bool) {
	threadID := strings.TrimSpace(c.cfg.ResumeThreadID)
	if threadID == "" {
		return "", false
	}

	requestID := c.nextRequestID()
	approvalPolicy, threadSandbox, _ := c.permissionConfig()
	req, err := protocol.ThreadResumeRequest(requestID, threadID, filepath.Clean(c.cfg.Workspace), approvalPolicy, threadSandbox)
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
	approvalPolicy, threadSandbox, _ := c.permissionConfig()
	req, err := protocol.ThreadStartRequest(
		requestID,
		filepath.Clean(c.cfg.Workspace),
		approvalPolicy,
		threadSandbox,
		c.cfg.DynamicTools,
		map[string]interface{}{"initial_collaboration_mode": strings.TrimSpace(c.cfg.InitialCollaborationMode)},
	)
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

func (c *Client) UpdatePermissionConfig(approvalPolicy interface{}, threadSandbox string, turnSandboxPolicy map[string]interface{}) {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	c.cfg.ApprovalPolicy = approvalPolicy
	if strings.TrimSpace(threadSandbox) != "" {
		c.cfg.ThreadSandbox = threadSandbox
	}
	c.cfg.TurnSandboxPolicy = normalizeTurnSandboxPolicy(turnSandboxPolicy, c.cfg.Workspace, c.cfg.WorkspaceRoot)
}

func (c *Client) permissionConfig() (interface{}, string, map[string]interface{}) {
	c.configMu.RLock()
	defer c.configMu.RUnlock()
	return c.cfg.ApprovalPolicy, c.cfg.ThreadSandbox, normalizeTurnSandboxPolicy(c.cfg.TurnSandboxPolicy, c.cfg.Workspace, c.cfg.WorkspaceRoot)
}

func (c *Client) ensureThreadForTurn(ctx context.Context) error {
	if strings.TrimSpace(c.session.ThreadID) != "" {
		return nil
	}

	_, desiredThreadSandbox, _ := c.permissionConfig()
	desiredThreadSandbox = strings.TrimSpace(desiredThreadSandbox)
	threadID, err := c.startThread(ctx)
	if err != nil {
		return err
	}
	c.activateThread(threadID, desiredThreadSandbox, false)
	c.logger.Info("Codex thread started for turn", "thread_id", threadID)
	return nil
}

func (c *Client) activateThread(threadID, threadSandbox string, resumed bool) {
	c.session.ThreadID = threadID
	c.session.TurnID = ""
	c.session.SessionID = ""
	c.session.Terminal = false
	c.session.TerminalReason = ""
	c.threadResumed = resumed

	c.configMu.Lock()
	defer c.configMu.Unlock()
	c.activeThreadSandbox = strings.TrimSpace(threadSandbox)
}

func (c *Client) activeThreadConfig() string {
	c.configMu.RLock()
	defer c.configMu.RUnlock()
	return strings.TrimSpace(c.activeThreadSandbox)
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
	// awaitResponse may already have applied the terminal event before this loop starts.
	if handled, err := c.terminalTurnCompletionResult(); handled {
		return err
	}

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

func (c *Client) terminalTurnCompletionResult() (bool, error) {
	if c.session == nil || !c.session.Terminal {
		return false, nil
	}

	logFields := []any{
		"session_id", c.session.SessionID,
		"thread_id", c.session.ThreadID,
		"turn_id", c.session.TurnID,
	}

	switch c.session.TerminalReason {
	case "turn.completed", "session.completed", "run.completed":
		c.logger.Info("Codex turn completed", logFields...)
		return true, nil
	case "turn.failed":
		c.logger.Warn("Codex turn failed", logFields...)
		return true, &RunError{Kind: "turn_failed"}
	case "turn.cancelled":
		c.logger.Warn("Codex turn cancelled", logFields...)
		return true, &RunError{Kind: "turn_cancelled"}
	case "run.failed", "error":
		c.logger.Warn("Codex turn failed", logFields...)
		return true, &RunError{Kind: strings.ReplaceAll(c.session.TerminalReason, ".", "_")}
	default:
		return false, nil
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
	if !payload.HasID() {
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
		return c.waitForPendingInteraction(ctx, payload)
	case protocol.MethodItemFileChangeApproval:
		if c.autoApproveRequests() {
			if err := c.sendMessage(protocol.FileChangeApprovalResult(payload.ID, gen.AcceptForSession)); err != nil {
				return true, err
			}
			c.logger.Info("Codex approval auto-approved", "method", method)
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload.Raw})
			return true, nil
		}
		return c.waitForPendingInteraction(ctx, payload)
	case protocol.MethodExecCommandApproval:
		if c.autoApproveRequests() {
			if err := c.sendMessage(protocol.ExecCommandApprovalResult(payload.ID, gen.ApprovedForSession)); err != nil {
				return true, err
			}
			c.logger.Info("Codex approval auto-approved", "method", method)
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload.Raw})
			return true, nil
		}
		return c.waitForPendingInteraction(ctx, payload)
	case protocol.MethodApplyPatchApproval:
		if c.autoApproveRequests() {
			if err := c.sendMessage(protocol.ApplyPatchApprovalResult(payload.ID, gen.ApprovedForSession)); err != nil {
				return true, err
			}
			c.logger.Info("Codex approval auto-approved", "method", method)
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload.Raw})
			return true, nil
		}
		return c.waitForPendingInteraction(ctx, payload)
	case protocol.MethodToolRequestUserInput:
		var params gen.ToolRequestUserInputParams
		if err := payload.UnmarshalParams(&params); err != nil {
			return true, err
		}
		answers, autoApproved := answersForToolInputParams(params, c.autoApproveRequests())
		if answers == nil {
			return c.waitForPendingInteraction(ctx, payload)
		}
		if err := c.sendMessage(protocol.ToolRequestUserInputResult(payload.ID, answers)); err != nil {
			return true, err
		}
		if autoApproved {
			c.logger.Info("Codex tool input auto-approved", "method", method)
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload.Raw})
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
		return true, &RunError{
			Kind:    "unsupported_request",
			Payload: payload.Raw,
			Err:     fmt.Errorf("unsupported request method %q", strings.TrimSpace(method)),
		}
	}
}

func (c *Client) waitForPendingInteraction(ctx context.Context, payload protocol.Message) (bool, error) {
	if !c.supportsPendingInteractionQueue() {
		return true, legacyPendingInteractionError(payload.Method, payload.Raw)
	}

	interaction, err := c.newPendingInteraction(payload)
	if err != nil {
		return true, err
	}
	waiter := c.registerPendingInteraction(*interaction, payload.ID)
	defer c.clearPendingInteraction(interaction.ID)

	c.logger.Warn("Codex operator input required",
		"method", interaction.Method,
		"interaction_id", interaction.ID,
		"issue_identifier", c.cfg.IssueIdentifier,
	)
	response, err := c.awaitPendingInteractionResponse(ctx, waiter)
	if err != nil {
		return true, err
	}
	if err := c.sendPendingInteractionResponse(waiter.interaction, waiter.payloadID, response); err != nil {
		return true, err
	}
	c.emitResolvedInteractionActivity(waiter.interaction, response)
	return true, nil
}

func (c *Client) autoApproveRequests() bool {
	if s, ok := c.cfg.ApprovalPolicy.(string); ok {
		return strings.EqualFold(strings.TrimSpace(s), "never")
	}
	return false
}

func (c *Client) supportsPendingInteractionQueue() bool {
	return c.cfg.OnPendingInteraction != nil && !c.autoApproveRequests()
}

func legacyPendingInteractionError(method string, payload map[string]interface{}) error {
	switch method {
	case protocol.MethodToolRequestUserInput:
		return &RunError{Kind: "turn_input_required", Payload: payload}
	default:
		return &RunError{Kind: "approval_required", Payload: payload}
	}
}

func (c *Client) newPendingInteraction(payload protocol.Message) (*PendingInteraction, error) {
	requestID := requestIDString(payload)
	now := time.Now().UTC()
	interaction := PendingInteraction{
		RequestID:       requestID,
		Method:          strings.TrimSpace(payload.Method),
		IssueID:         strings.TrimSpace(c.cfg.IssueID),
		IssueIdentifier: strings.TrimSpace(c.cfg.IssueIdentifier),
		SessionID:       strings.TrimSpace(c.session.SessionID),
		ThreadID:        strings.TrimSpace(c.session.ThreadID),
		TurnID:          strings.TrimSpace(c.session.TurnID),
		RequestedAt:     now,
	}

	if !c.session.LastTimestamp.IsZero() {
		ts := c.session.LastTimestamp.UTC()
		interaction.LastActivityAt = &ts
	}
	if last := strings.TrimSpace(c.session.LastMessage); last != "" {
		interaction.LastActivity = last
	}
	if mode := c.currentInteractionCollaborationMode(); mode != "" {
		interaction.CollaborationMode = mode
	}

	switch payload.Method {
	case protocol.MethodItemCommandExecutionApproval:
		var params gen.CommandExecutionRequestApprovalParams
		if err := payload.UnmarshalParams(&params); err != nil {
			return nil, err
		}
		interaction.Kind = PendingInteractionKindApproval
		interaction.ItemID = strings.TrimSpace(params.ItemID)
		interaction.ThreadID = firstNonEmptyInteractionValue(interaction.ThreadID, strings.TrimSpace(params.ThreadID))
		interaction.TurnID = firstNonEmptyInteractionValue(interaction.TurnID, strings.TrimSpace(params.TurnID))
		interaction.ID = buildPendingInteractionID(interaction.IssueID, interaction.ThreadID, interaction.TurnID, interaction.ItemID, requestID)
		interaction.Approval = &PendingApproval{
			Command:   strings.TrimSpace(stringPtrValue(params.Command)),
			CWD:       strings.TrimSpace(stringPtrValue(params.Cwd)),
			Reason:    strings.TrimSpace(stringPtrValue(params.Reason)),
			Decisions: commandExecutionApprovalDecisions(params),
		}
	case protocol.MethodItemFileChangeApproval:
		var params gen.FileChangeRequestApprovalParams
		if err := payload.UnmarshalParams(&params); err != nil {
			return nil, err
		}
		interaction.Kind = PendingInteractionKindApproval
		interaction.ItemID = strings.TrimSpace(params.ItemID)
		interaction.ThreadID = firstNonEmptyInteractionValue(interaction.ThreadID, strings.TrimSpace(params.ThreadID))
		interaction.TurnID = firstNonEmptyInteractionValue(interaction.TurnID, strings.TrimSpace(params.TurnID))
		interaction.ID = buildPendingInteractionID(interaction.IssueID, interaction.ThreadID, interaction.TurnID, interaction.ItemID, requestID)
		interaction.Approval = &PendingApproval{
			Reason:    strings.TrimSpace(stringPtrValue(params.Reason)),
			Decisions: fileChangeApprovalDecisions(strings.TrimSpace(stringPtrValue(params.GrantRoot))),
		}
	case protocol.MethodExecCommandApproval:
		var params gen.ExecCommandApprovalParams
		if err := payload.UnmarshalParams(&params); err != nil {
			return nil, err
		}
		interaction.Kind = PendingInteractionKindApproval
		interaction.ItemID = strings.TrimSpace(params.CallID)
		interaction.ID = buildPendingInteractionID(interaction.IssueID, interaction.ThreadID, interaction.TurnID, interaction.ItemID, requestID)
		interaction.Approval = &PendingApproval{
			Command:   strings.Join(params.Command, " "),
			CWD:       strings.TrimSpace(params.Cwd),
			Reason:    strings.TrimSpace(stringPtrValue(params.Reason)),
			Decisions: reviewApprovalDecisions(),
		}
	case protocol.MethodApplyPatchApproval:
		var params gen.ApplyPatchApprovalParams
		if err := payload.UnmarshalParams(&params); err != nil {
			return nil, err
		}
		interaction.Kind = PendingInteractionKindApproval
		interaction.ItemID = strings.TrimSpace(params.CallID)
		interaction.ID = buildPendingInteractionID(interaction.IssueID, interaction.ThreadID, interaction.TurnID, interaction.ItemID, requestID)
		interaction.Approval = &PendingApproval{
			Reason:    strings.TrimSpace(stringPtrValue(params.Reason)),
			Decisions: applyPatchApprovalDecisions(strings.TrimSpace(stringPtrValue(params.GrantRoot))),
		}
	case protocol.MethodToolRequestUserInput:
		var params gen.ToolRequestUserInputParams
		if err := payload.UnmarshalParams(&params); err != nil {
			return nil, err
		}
		interaction.Kind = PendingInteractionKindUserInput
		interaction.ItemID = strings.TrimSpace(params.ItemID)
		interaction.ThreadID = firstNonEmptyInteractionValue(interaction.ThreadID, strings.TrimSpace(params.ThreadID))
		interaction.TurnID = firstNonEmptyInteractionValue(interaction.TurnID, strings.TrimSpace(params.TurnID))
		interaction.ID = buildPendingInteractionID(interaction.IssueID, interaction.ThreadID, interaction.TurnID, interaction.ItemID, requestID)
		questions := make([]PendingUserInputQuestion, 0, len(params.Questions))
		for _, question := range params.Questions {
			converted := PendingUserInputQuestion{
				Header:   strings.TrimSpace(question.Header),
				ID:       strings.TrimSpace(question.ID),
				Question: strings.TrimSpace(question.Question),
				IsOther:  boolPtrValue(question.IsOther),
				IsSecret: boolPtrValue(question.IsSecret),
			}
			if len(question.Options) > 0 {
				converted.Options = make([]PendingUserInputOption, 0, len(question.Options))
				for _, option := range question.Options {
					converted.Options = append(converted.Options, PendingUserInputOption{
						Label:       strings.TrimSpace(option.Label),
						Description: strings.TrimSpace(option.Description),
					})
				}
			}
			questions = append(questions, converted)
		}
		interaction.UserInput = &PendingUserInput{Questions: questions}
	default:
		return nil, fmt.Errorf("unsupported interactive method %q", payload.Method)
	}

	if interaction.ID == "" {
		interaction.ID = buildPendingInteractionID(interaction.IssueID, interaction.ThreadID, interaction.TurnID, interaction.ItemID, requestID)
	}
	if interaction.LastActivity == "" {
		interaction.LastActivity = pendingInteractionSummary(interaction)
	}
	if interaction.LastActivityAt == nil {
		ts := now
		interaction.LastActivityAt = &ts
	}
	return &interaction, nil
}

func (c *Client) currentInteractionCollaborationMode() string {
	if c.threadResumed || c.session == nil || c.session.TurnsStarted != 1 {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(c.cfg.InitialCollaborationMode)) {
	case "plan":
		return "plan"
	case "default":
		return "default"
	default:
		return ""
	}
}

func (c *Client) registerPendingInteraction(interaction PendingInteraction, payloadID protocol.RequestID) *interactionWaiter {
	waiter := &interactionWaiter{
		interaction:     interaction,
		payloadID:       payloadID,
		responseReadyCh: make(chan struct{}),
		doneCh:          make(chan struct{}),
	}
	c.pendingMu.Lock()
	c.pendingInteractions[interaction.ID] = waiter
	c.pendingMu.Unlock()
	if c.cfg.OnPendingInteraction != nil {
		cp := interaction.Clone()
		c.cfg.OnPendingInteraction(&cp)
	}
	return waiter
}

func (c *Client) clearPendingInteraction(interactionID string) {
	c.pendingMu.Lock()
	waiter, ok := c.pendingInteractions[interactionID]
	if ok {
		delete(c.pendingInteractions, interactionID)
	}
	c.pendingMu.Unlock()
	if ok {
		close(waiter.doneCh)
	}
	if c.cfg.OnPendingInteractionDone != nil {
		c.cfg.OnPendingInteractionDone(interactionID)
	}
}

func (c *Client) awaitPendingInteractionResponse(ctx context.Context, waiter *interactionWaiter) (PendingInteractionResponse, error) {
	select {
	case <-ctx.Done():
		return PendingInteractionResponse{}, ctx.Err()
	case err := <-c.waitCh:
		select {
		case c.waitCh <- err:
		default:
		}
		if ctx.Err() != nil {
			return PendingInteractionResponse{}, ctx.Err()
		}
		return PendingInteractionResponse{}, &RunError{Kind: "run_interrupted"}
	case <-waiter.doneCh:
		return PendingInteractionResponse{}, ErrPendingInteractionNotFound
	case <-waiter.responseReadyCh:
		c.pendingMu.Lock()
		response := waiter.response
		c.pendingMu.Unlock()
		return response, nil
	}
}

func (c *Client) sendPendingInteractionResponse(interaction PendingInteraction, payloadID protocol.RequestID, response PendingInteractionResponse) error {
	switch interaction.Method {
	case protocol.MethodItemCommandExecutionApproval:
		if len(response.DecisionPayload) > 0 {
			return c.sendMessage(protocol.CommandExecutionApprovalResultPayload(payloadID, response.DecisionPayload))
		}
		return c.sendMessage(protocol.CommandExecutionApprovalResult(payloadID, gen.FileChangeApprovalDecision(response.Decision)))
	case protocol.MethodItemFileChangeApproval:
		return c.sendMessage(protocol.FileChangeApprovalResult(payloadID, gen.FileChangeApprovalDecision(response.Decision)))
	case protocol.MethodExecCommandApproval:
		return c.sendMessage(protocol.ExecCommandApprovalResult(payloadID, gen.ReviewDecision(response.Decision)))
	case protocol.MethodApplyPatchApproval:
		return c.sendMessage(protocol.ApplyPatchApprovalResult(payloadID, gen.ReviewDecision(response.Decision)))
	case protocol.MethodToolRequestUserInput:
		answers := make(map[string]gen.ToolRequestUserInputAnswer, len(response.Answers))
		for questionID, values := range response.Answers {
			answers[questionID] = gen.ToolRequestUserInputAnswer{Answers: append([]string(nil), values...)}
		}
		return c.sendMessage(protocol.ToolRequestUserInputResult(payloadID, answers))
	default:
		return fmt.Errorf("unsupported interactive method %q", interaction.Method)
	}
}

func (c *Client) emitResolvedInteractionActivity(interaction PendingInteraction, response PendingInteractionResponse) {
	switch interaction.Kind {
	case PendingInteractionKindApproval:
		decision := resolvedApprovalDecisionValue(interaction, response)
		raw := map[string]interface{}{}
		if decision != "" {
			raw["decision"] = decision
		}
		if payload := cloneJSONMap(response.DecisionPayload); len(payload) > 0 {
			raw["decision_payload"] = payload
		}
		if label := resolvedApprovalDecisionLabel(interaction, response); label != "" {
			raw["decision_label"] = label
		}
		c.emitActivityEvent(ActivityEvent{
			Type:      resolvedApprovalEventType(interaction.Method),
			RequestID: interaction.RequestID,
			ThreadID:  interaction.ThreadID,
			TurnID:    interaction.TurnID,
			ItemID:    interaction.ItemID,
			Status:    decision,
			Raw:       raw,
		})
	case PendingInteractionKindUserInput:
		c.emitActivityEvent(ActivityEvent{
			Type:      "item.tool.userInputSubmitted",
			RequestID: interaction.RequestID,
			ThreadID:  interaction.ThreadID,
			TurnID:    interaction.TurnID,
			ItemID:    interaction.ItemID,
			Raw: map[string]interface{}{
				"answers": sanitizePendingInteractionAnswers(interaction, response.Answers),
			},
		})
	}
}

func normalizePendingInteractionResponse(interaction PendingInteraction, response PendingInteractionResponse) (PendingInteractionResponse, error) {
	switch interaction.Kind {
	case PendingInteractionKindApproval:
		options := interactionApprovalDecisions(interaction)
		if len(response.DecisionPayload) > 0 {
			for _, option := range options {
				if !decisionPayloadsEqual(option.DecisionPayload, response.DecisionPayload) {
					continue
				}
				return PendingInteractionResponse{DecisionPayload: cloneJSONMap(option.DecisionPayload)}, nil
			}
			return PendingInteractionResponse{}, fmt.Errorf("%w: unsupported approval decision payload", ErrInvalidInteractionResponse)
		}
		decision := strings.TrimSpace(response.Decision)
		if decision == "" {
			return PendingInteractionResponse{}, fmt.Errorf("%w: missing decision", ErrInvalidInteractionResponse)
		}
		for _, option := range options {
			if len(option.DecisionPayload) > 0 {
				continue
			}
			if option.Value == decision {
				return PendingInteractionResponse{Decision: decision}, nil
			}
		}
		return PendingInteractionResponse{}, fmt.Errorf("%w: unsupported decision %q", ErrInvalidInteractionResponse, decision)
	case PendingInteractionKindUserInput:
		if len(response.Answers) == 0 {
			return PendingInteractionResponse{}, fmt.Errorf("%w: missing answers", ErrInvalidInteractionResponse)
		}
		if interaction.UserInput == nil || len(interaction.UserInput.Questions) == 0 {
			return PendingInteractionResponse{}, fmt.Errorf("%w: missing question schema", ErrInvalidInteractionResponse)
		}
		normalized := make(map[string][]string, len(interaction.UserInput.Questions))
		for _, question := range interaction.UserInput.Questions {
			rawAnswers, ok := response.Answers[question.ID]
			if !ok {
				return PendingInteractionResponse{}, fmt.Errorf("%w: missing answer for %q", ErrInvalidInteractionResponse, question.ID)
			}
			answer, err := normalizePendingUserInputAnswer(question, rawAnswers)
			if err != nil {
				return PendingInteractionResponse{}, err
			}
			normalized[question.ID] = []string{answer}
		}
		return PendingInteractionResponse{Answers: normalized}, nil
	default:
		return PendingInteractionResponse{}, fmt.Errorf("%w: unsupported interaction kind %q", ErrInvalidInteractionResponse, interaction.Kind)
	}
}

func normalizePendingUserInputAnswer(question PendingUserInputQuestion, answers []string) (string, error) {
	if len(answers) == 0 {
		return "", fmt.Errorf("%w: missing answer for %q", ErrInvalidInteractionResponse, question.ID)
	}
	answer := answers[0]
	if strings.TrimSpace(answer) == "" {
		return "", fmt.Errorf("%w: blank answer for %q", ErrInvalidInteractionResponse, question.ID)
	}
	if len(question.Options) == 0 {
		return answer, nil
	}
	for _, option := range question.Options {
		if strings.TrimSpace(option.Label) == strings.TrimSpace(answer) {
			return option.Label, nil
		}
	}
	if question.IsOther {
		return answer, nil
	}
	return "", fmt.Errorf("%w: unsupported answer for %q", ErrInvalidInteractionResponse, question.ID)
}

func interactionApprovalDecisions(interaction PendingInteraction) []PendingApprovalDecision {
	if interaction.Approval != nil && len(interaction.Approval.Decisions) > 0 {
		return interaction.Approval.Decisions
	}
	switch interaction.Method {
	case protocol.MethodItemCommandExecutionApproval:
		return commandExecutionApprovalDecisions(gen.CommandExecutionRequestApprovalParams{})
	case protocol.MethodItemFileChangeApproval:
		return fileChangeApprovalDecisions("")
	case protocol.MethodExecCommandApproval:
		return reviewApprovalDecisions()
	case protocol.MethodApplyPatchApproval:
		return applyPatchApprovalDecisions("")
	default:
		return nil
	}
}

func commandExecutionApprovalDecisions(params gen.CommandExecutionRequestApprovalParams) []PendingApprovalDecision {
	decisions := []PendingApprovalDecision{
		{Value: string(gen.Accept), Label: "Accept once", Description: "Run this request once and keep the turn going."},
	}
	if len(params.ProposedExecpolicyAmendment) > 0 {
		amendment := make([]interface{}, 0, len(params.ProposedExecpolicyAmendment))
		for _, rule := range params.ProposedExecpolicyAmendment {
			amendment = append(amendment, rule)
		}
		decisions = append(decisions, PendingApprovalDecision{
			Value:       "accept_with_execpolicy_amendment",
			Label:       "Approve and store rule",
			Description: "Run this request and allow future matching commands without prompting.",
			DecisionPayload: map[string]interface{}{
				"acceptWithExecpolicyAmendment": map[string]interface{}{
					"execpolicy_amendment": amendment,
				},
			},
		})
	}
	decisions = append(decisions, PendingApprovalDecision{
		Value:       string(gen.AcceptForSession),
		Label:       "Accept for session",
		Description: "Allow similar requests for the rest of the session.",
	})
	for _, amendment := range params.ProposedNetworkPolicyAmendments {
		action := strings.TrimSpace(string(amendment.Action))
		host := strings.TrimSpace(amendment.Host)
		if action == "" || host == "" {
			continue
		}
		label := "Persist allow rule"
		description := "Allow this host for future requests and keep the turn going."
		if strings.EqualFold(action, string(gen.Deny)) {
			label = "Persist deny rule"
			description = "Deny this host for future requests and reject the current request."
		}
		decisions = append(decisions, PendingApprovalDecision{
			Value:       "network_policy_" + action + "_" + sanitizePendingDecisionValue(host),
			Label:       label,
			Description: description,
			DecisionPayload: map[string]interface{}{
				"applyNetworkPolicyAmendment": map[string]interface{}{
					"network_policy_amendment": map[string]interface{}{
						"action": action,
						"host":   host,
					},
				},
			},
		})
	}
	decisions = append(decisions,
		PendingApprovalDecision{Value: string(gen.Decline), Label: "Decline", Description: "Reject the request and let the agent continue the turn."},
		PendingApprovalDecision{Value: string(gen.Cancel), Label: "Cancel", Description: "Reject the request and interrupt the current turn."},
	)
	return decisions
}

func fileChangeApprovalDecisions(grantRoot string) []PendingApprovalDecision {
	acceptForSessionLabel := "Accept for session"
	acceptForSessionDescription := "Allow similar requests for the rest of the session."
	if grantRoot != "" {
		acceptForSessionLabel = "Accept and grant root"
		acceptForSessionDescription = fmt.Sprintf("Allow writes under %s for the rest of the session.", grantRoot)
	}
	return []PendingApprovalDecision{
		{Value: string(gen.Accept), Label: "Accept once", Description: "Run this request once and keep the turn going."},
		{Value: string(gen.AcceptForSession), Label: acceptForSessionLabel, Description: acceptForSessionDescription},
		{Value: string(gen.Decline), Label: "Decline", Description: "Reject the request and let the agent continue the turn."},
		{Value: string(gen.Cancel), Label: "Cancel", Description: "Reject the request and interrupt the current turn."},
	}
}

func reviewApprovalDecisions() []PendingApprovalDecision {
	return []PendingApprovalDecision{
		{Value: string(gen.Approved), Label: "Approve once", Description: "Run this request once and keep the turn going."},
		{Value: string(gen.ApprovedForSession), Label: "Approve for session", Description: "Allow similar requests for the rest of the session."},
		{Value: string(gen.Denied), Label: "Deny", Description: "Reject the request and let the agent continue the turn."},
		{Value: string(gen.Abort), Label: "Abort", Description: "Reject the request and interrupt the current turn."},
	}
}

func applyPatchApprovalDecisions(grantRoot string) []PendingApprovalDecision {
	decisions := reviewApprovalDecisions()
	if grantRoot == "" {
		return decisions
	}
	decisions[1].Label = "Approve and grant root"
	decisions[1].Description = fmt.Sprintf("Allow writes under %s for the rest of the session.", grantRoot)
	return decisions
}

func sanitizePendingDecisionValue(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ".", "_", ":", "_", "@", "_", "-", "_")
	return replacer.Replace(strings.TrimSpace(value))
}

func decisionPayloadsEqual(left, right map[string]interface{}) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == 0 && len(right) == 0
	}
	leftBody, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightBody, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return bytes.Equal(leftBody, rightBody)
}

func resolvedApprovalDecisionLabel(interaction PendingInteraction, response PendingInteractionResponse) string {
	for _, option := range interactionApprovalDecisions(interaction) {
		switch {
		case len(response.DecisionPayload) > 0 && decisionPayloadsEqual(option.DecisionPayload, response.DecisionPayload):
			return option.Label
		case response.Decision != "" && option.Value == response.Decision:
			return option.Label
		}
	}
	return ""
}

func resolvedApprovalDecisionValue(interaction PendingInteraction, response PendingInteractionResponse) string {
	for _, option := range interactionApprovalDecisions(interaction) {
		switch {
		case len(response.DecisionPayload) > 0 && decisionPayloadsEqual(option.DecisionPayload, response.DecisionPayload):
			return strings.TrimSpace(option.Value)
		case response.Decision != "" && option.Value == response.Decision:
			return strings.TrimSpace(response.Decision)
		}
	}
	return strings.TrimSpace(response.Decision)
}

func pendingInteractionSummary(interaction PendingInteraction) string {
	switch interaction.Kind {
	case PendingInteractionKindApproval:
		if interaction.Approval == nil {
			return "Operator approval required."
		}
		if command := strings.TrimSpace(interaction.Approval.Command); command != "" {
			return command
		}
		if reason := strings.TrimSpace(interaction.Approval.Reason); reason != "" {
			return reason
		}
		return "Operator approval required."
	case PendingInteractionKindUserInput:
		if interaction.UserInput == nil || len(interaction.UserInput.Questions) == 0 {
			return "Operator input required."
		}
		first := interaction.UserInput.Questions[0]
		if question := strings.TrimSpace(first.Question); question != "" {
			return question
		}
		if header := strings.TrimSpace(first.Header); header != "" {
			return header
		}
		return "Operator input required."
	default:
		return "Operator input required."
	}
}

func sanitizePendingInteractionAnswers(interaction PendingInteraction, answers map[string][]string) map[string]interface{} {
	sanitized := make(map[string]interface{}, len(answers))
	secretByQuestion := make(map[string]bool)
	if interaction.UserInput != nil {
		for _, question := range interaction.UserInput.Questions {
			secretByQuestion[question.ID] = question.IsSecret
		}
	}
	for questionID, values := range answers {
		if secretByQuestion[questionID] {
			sanitized[questionID] = []string{"[redacted]"}
			continue
		}
		cloned := append([]string(nil), values...)
		sanitized[questionID] = cloned
	}
	return sanitized
}

func resolvedApprovalEventType(method string) string {
	switch method {
	case protocol.MethodItemCommandExecutionApproval:
		return "item.commandExecution.approvalResolved"
	case protocol.MethodItemFileChangeApproval:
		return "item.fileChange.approvalResolved"
	case protocol.MethodExecCommandApproval:
		return "execCommandApproval.resolved"
	case protocol.MethodApplyPatchApproval:
		return "applyPatchApproval.resolved"
	default:
		return "approval.resolved"
	}
}

func buildPendingInteractionID(issueID, threadID, turnID, itemID, requestID string) string {
	parts := []string{strings.TrimSpace(issueID), strings.TrimSpace(threadID), strings.TrimSpace(turnID), strings.TrimSpace(itemID), strings.TrimSpace(requestID)}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, ":")
}

func firstNonEmptyInteractionValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolPtrValue(value *bool) bool {
	return value != nil && *value
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
	if c.session.startedTurnID == turnID {
		return true
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
	cp := c.session.Clone()
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
		_, _ = c.output.WriteString("[stderr] ")
	}
	_, _ = c.output.WriteString(line)
	_ = c.output.WriteByte('\n')
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
		"granular": map[string]interface{}{
			"sandbox_approval":    true,
			"rules":               true,
			"mcp_elicitations":    true,
			"request_permissions": false,
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
	if !autoApprove {
		return nil, false
	}
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
		label := approvalOptionLabel(q["options"])
		if label == "" {
			return nil, false
		}
		answers[id] = map[string]interface{}{"answers": []string{label}}
	}
	return answers, autoApprove
}

func answersForToolInputParams(params gen.ToolRequestUserInputParams, autoApprove bool) (map[string]gen.ToolRequestUserInputAnswer, bool) {
	if !autoApprove {
		return nil, false
	}
	if len(params.Questions) == 0 {
		return nil, false
	}
	answers := make(map[string]gen.ToolRequestUserInputAnswer, len(params.Questions))
	for _, question := range params.Questions {
		if strings.TrimSpace(question.ID) == "" {
			return nil, false
		}
		label := approvalOptionLabelFromQuestions(question.Options)
		if label == "" {
			return nil, false
		}
		answers[question.ID] = gen.ToolRequestUserInputAnswer{Answers: []string{label}}
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
