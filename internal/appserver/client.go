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
)

const nonInteractiveToolInputAnswer = "This is a non-interactive session. Operator input is unavailable."

type ToolExecutor func(name string, arguments interface{}) map[string]interface{}

type ClientConfig struct {
	Executable        string
	Args              []string
	Env               []string
	Workspace         string
	WorkspaceRoot     string
	Prompt            string
	Title             string
	ApprovalPolicy    interface{}
	ThreadSandbox     string
	TurnSandboxPolicy map[string]interface{}
	ReadTimeout       time.Duration
	TurnTimeout       time.Duration
	DynamicTools      []map[string]interface{}
	ToolExecutor      ToolExecutor
	OnMessage         func(map[string]interface{})
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

	lines   chan string
	lineErr chan error
	waitCh  chan error

	outputMu sync.Mutex
	output   bytes.Buffer

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
	if cfg.ThreadSandbox == "" {
		cfg.ThreadSandbox = "workspace-write"
	}
	if cfg.ApprovalPolicy == nil {
		cfg.ApprovalPolicy = defaultApprovalPolicy()
	}
	if cfg.TurnSandboxPolicy == nil {
		cfg.TurnSandboxPolicy = defaultTurnSandboxPolicy(cfg.Workspace)
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
		session: &Session{MaxHistory: 50},
		lines:   make(chan string, 128),
		lineErr: make(chan error, 1),
		waitCh:  make(chan error, 1),
		nextID:  1,
	}
	client.session.AppServerPID = cmd.Process.Pid

	var wg sync.WaitGroup
	wg.Add(2)
	go client.readStdout(&wg, stdoutPipe)
	go client.readStderr(&wg, stderrPipe)
	go func() {
		err := cmd.Wait()
		wg.Wait()
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

func (c *Client) Wait() error {
	select {
	case err := <-c.waitCh:
		if err == nil {
			select {
			case readErr := <-c.lineErr:
				if readErr != nil && !errors.Is(readErr, io.EOF) {
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
	requestID := c.nextRequestID()
	if err := c.sendMessage(map[string]interface{}{
		"id":     requestID,
		"method": "turn/start",
		"params": map[string]interface{}{
			"threadId":       c.session.ThreadID,
			"input":          []map[string]interface{}{{"type": "text", "text": prompt}},
			"cwd":            filepath.Clean(c.cfg.Workspace),
			"title":          title,
			"approvalPolicy": c.cfg.ApprovalPolicy,
			"sandboxPolicy":  c.cfg.TurnSandboxPolicy,
		},
	}); err != nil {
		return err
	}
	resp, err := c.awaitResponse(ctx, requestID)
	if err != nil {
		return err
	}
	turnID := nestedString(resp, "turn", "id")
	if turnID == "" {
		return fmt.Errorf("invalid turn/start response: missing turn.id")
	}
	c.session.ApplyEvent(Event{Type: "turn.started", ThreadID: c.session.ThreadID, TurnID: turnID})
	c.emitMessage("session_started", map[string]interface{}{
		"session_id": c.session.SessionID,
		"thread_id":  c.session.ThreadID,
		"turn_id":    turnID,
	})
	return c.awaitTurnCompletion(ctx)
}

func (c *Client) initialize(ctx context.Context) error {
	if err := c.sendMessage(map[string]interface{}{
		"id":     c.nextRequestID(),
		"method": "initialize",
		"params": map[string]interface{}{
			"capabilities": map[string]interface{}{"experimentalApi": true},
			"clientInfo": map[string]interface{}{
				"name":    "symphony-go",
				"title":   "Symphony Go",
				"version": "dev",
			},
		},
	}); err != nil {
		return err
	}
	if _, err := c.awaitResponse(ctx, 1); err != nil {
		return err
	}
	if err := c.sendMessage(map[string]interface{}{
		"method": "initialized",
		"params": map[string]interface{}{},
	}); err != nil {
		return err
	}

	threadRequestID := c.nextRequestID()
	if err := c.sendMessage(map[string]interface{}{
		"id":     threadRequestID,
		"method": "thread/start",
		"params": map[string]interface{}{
			"approvalPolicy": c.cfg.ApprovalPolicy,
			"sandbox":        c.cfg.ThreadSandbox,
			"cwd":            filepath.Clean(c.cfg.Workspace),
			"dynamicTools":   c.cfg.DynamicTools,
		},
	}); err != nil {
		return err
	}
	threadResp, err := c.awaitResponse(ctx, threadRequestID)
	if err != nil {
		return err
	}
	threadID := nestedString(threadResp, "thread", "id")
	if threadID == "" {
		return fmt.Errorf("invalid thread/start response: missing thread.id")
	}
	c.session.ThreadID = threadID
	return nil
}

func (c *Client) nextRequestID() int {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	id := c.nextID
	c.nextID++
	return id
}

func (c *Client) awaitResponse(ctx context.Context, requestID int) (map[string]interface{}, error) {
	for {
		line, err := c.nextLine(ctx, c.cfg.ReadTimeout)
		if err != nil {
			return nil, err
		}
		payload, ok := decodeJSONObject(line)
		if !ok {
			c.logStreamOutput("response", line)
			continue
		}
		c.captureEvent(payload)
		if id, ok := asInt(payload["id"]); ok && id == requestID {
			if errPayload, ok := asMap(payload["error"]); ok {
				return nil, &RunError{Kind: "response_error", Payload: payload, Err: fmt.Errorf("%v", errPayload)}
			}
			if result, ok := asMap(payload["result"]); ok {
				return result, nil
			}
			return nil, &RunError{Kind: "response_error", Payload: payload, Err: fmt.Errorf("missing result")}
		}
	}
}

func (c *Client) awaitTurnCompletion(ctx context.Context) error {
	var deadline time.Time
	if c.cfg.TurnTimeout > 0 {
		deadline = time.Now().Add(c.cfg.TurnTimeout)
	}

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
			return err
		}
		payload, ok := decodeJSONObject(line)
		if !ok {
			c.logStreamOutput("turn", line)
			continue
		}
		c.captureEvent(payload)

		method := nestedString(payload, "method")
		switch method {
		case "turn/completed":
			c.session.ApplyEvent(Event{Type: "turn.completed", ThreadID: c.session.ThreadID, TurnID: c.session.TurnID})
			return nil
		case "turn/failed":
			return &RunError{Kind: "turn_failed", Payload: payload}
		case "turn/cancelled":
			return &RunError{Kind: "turn_cancelled", Payload: payload}
		}

		handled, err := c.handleRequest(payload)
		if err != nil {
			return err
		}
		if handled {
			continue
		}
		if needsInput(method, payload) {
			return &RunError{Kind: "turn_input_required", Payload: payload}
		}
	}
}

func (c *Client) handleRequest(payload map[string]interface{}) (bool, error) {
	method := nestedString(payload, "method")
	id, hasID := payload["id"]
	if method == "" || !hasID {
		return false, nil
	}
	params, _ := asMap(payload["params"])

	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		if c.autoApproveRequests() {
			if err := c.sendMessage(map[string]interface{}{
				"id":     id,
				"result": map[string]interface{}{"decision": "acceptForSession"},
			}); err != nil {
				return true, err
			}
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload})
			return true, nil
		}
		return true, &RunError{Kind: "approval_required", Payload: payload}
	case "execCommandApproval", "applyPatchApproval":
		if c.autoApproveRequests() {
			if err := c.sendMessage(map[string]interface{}{
				"id":     id,
				"result": map[string]interface{}{"decision": "approved_for_session"},
			}); err != nil {
				return true, err
			}
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload})
			return true, nil
		}
		return true, &RunError{Kind: "approval_required", Payload: payload}
	case "item/tool/requestUserInput":
		answers, autoApproved := answersForToolInput(params, c.autoApproveRequests())
		if answers == nil {
			return true, &RunError{Kind: "turn_input_required", Payload: payload}
		}
		if err := c.sendMessage(map[string]interface{}{
			"id":     id,
			"result": map[string]interface{}{"answers": answers},
		}); err != nil {
			return true, err
		}
		if autoApproved {
			c.emitMessage("approval_auto_approved", map[string]interface{}{"payload": payload})
		} else {
			c.emitMessage("tool_input_auto_answered", map[string]interface{}{
				"payload": payload,
				"answer":  nonInteractiveToolInputAnswer,
			})
		}
		return true, nil
	case "item/tool/call":
		result := unsupportedToolResult(toolCallName(params), supportedToolNames(c.cfg.DynamicTools))
		if c.cfg.ToolExecutor != nil {
			result = c.cfg.ToolExecutor(toolCallName(params), toolCallArguments(params))
		}
		if err := c.sendMessage(map[string]interface{}{
			"id":     id,
			"result": result,
		}); err != nil {
			return true, err
		}
		eventName := "tool_call_completed"
		if success, _ := result["success"].(bool); !success {
			eventName = "tool_call_failed"
		}
		c.emitMessage(eventName, map[string]interface{}{"payload": payload})
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
		return "", &RunError{Kind: "read_timeout"}
	case line, ok := <-c.lines:
		if !ok {
			select {
			case err := <-c.lineErr:
				if err == nil {
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
			if !errors.Is(err, io.EOF) {
				select {
				case c.lineErr <- err:
				default:
				}
			} else {
				select {
				case c.lineErr <- io.EOF:
				default:
				}
			}
			close(c.lines)
			return
		}
	}
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

func (c *Client) sendMessage(payload map[string]interface{}) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(encoded, '\n'))
	return err
}

func (c *Client) captureEvent(payload map[string]interface{}) {
	encoded, err := json.Marshal(payload)
	if err == nil {
		if evt, ok := ParseEventLine(string(encoded)); ok {
			c.session.ApplyEvent(evt)
		}
	}
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

func defaultTurnSandboxPolicy(workspace string) map[string]interface{} {
	root := workspace
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = filepath.Clean(root)
	}
	return map[string]interface{}{
		"type":                "workspaceWrite",
		"writableRoots":       []string{absRoot},
		"readOnlyAccess":      map[string]interface{}{"type": "fullAccess"},
		"networkAccess":       false,
		"excludeTmpdirEnvVar": false,
		"excludeSlashTmp":     false,
	}
}

func looksLikeCodexCommand(executable string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(executable)))
	return base == "codex" || base == "codex.exe"
}

func decodeJSONObject(line string) (map[string]interface{}, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return nil, false
	}
	return payload, true
}

func nestedString(m map[string]interface{}, path ...string) string {
	var cur interface{} = m
	for _, part := range path {
		next, ok := asMap(cur)
		if !ok {
			return ""
		}
		cur = next[part]
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
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
		answer := nonInteractiveToolInputAnswer
		if autoApprove {
			if label := approvalOptionLabel(q["options"]); label != "" {
				answer = label
			}
		}
		answers[id] = map[string]interface{}{"answers": []string{answer}}
	}
	return answers, autoApprove
}

func approvalOptionLabel(v interface{}) string {
	options, ok := v.([]interface{})
	if !ok {
		return ""
	}
	var fallback string
	for _, raw := range options {
		opt, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		label, _ := opt["label"].(string)
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
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "error"), strings.Contains(lower, "warn"), strings.Contains(lower, "failed"), strings.Contains(lower, "fatal"), strings.Contains(lower, "panic"), strings.Contains(lower, "exception"):
		slog.Warn("Codex app-server stream output", "stream", stream, "text", text)
	default:
		slog.Info("Codex app-server stream output", "stream", stream, "text", text)
	}
}
