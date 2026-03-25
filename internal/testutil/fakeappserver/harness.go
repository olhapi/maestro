package fakeappserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	HelperTestName   = "TestFakeAppServerHelperProcess"
	envHelperMode    = "MAESTRO_FAKE_APP_SERVER_HELPER"
	envScenarioPath  = "MAESTRO_FAKE_APP_SERVER_SCENARIO"
	envControlAddr   = "MAESTRO_FAKE_APP_SERVER_CONTROL_ADDR"
	envTraceFilePath = "TRACE_FILE"
)

type Scenario struct {
	Steps []Step `json:"steps"`
}

type Step struct {
	Match            Match    `json:"match"`
	Emit             []Output `json:"emit,omitempty"`
	WaitForRelease   string   `json:"wait_for_release,omitempty"`
	EmitAfterRelease []Output `json:"emit_after_release,omitempty"`
	ExitCode         *int     `json:"exit_code,omitempty"`
}

type Match struct {
	Method string `json:"method,omitempty"`
	ID     *int   `json:"id,omitempty"`
}

type Output struct {
	Stream string                 `json:"stream,omitempty"`
	Text   string                 `json:"text,omitempty"`
	JSON   map[string]interface{} `json:"json,omitempty"`
}

type Config struct {
	Executable string
	Args       []string
	Env        []string
	Command    string

	closeOnce sync.Once
	closeFn   func()
	releaseFn func(name string)
}

func (c *Config) Release(name string) {
	if c != nil && c.releaseFn != nil {
		c.releaseFn(name)
	}
}

func (c *Config) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		if c.closeFn != nil {
			c.closeFn()
		}
	})
}

func NewConfig(t *testing.T, scenario Scenario) *Config {
	t.Helper()

	dir := t.TempDir()
	scenarioPath := filepath.Join(dir, "scenario.json")
	body, err := json.MarshalIndent(scenario, "", "  ")
	if err != nil {
		t.Fatalf("marshal fake app-server scenario: %v", err)
	}
	if err := os.WriteFile(scenarioPath, body, 0o644); err != nil {
		t.Fatalf("write fake app-server scenario: %v", err)
	}

	control, err := newControlServer()
	if err != nil {
		t.Fatalf("start fake app-server control server: %v", err)
	}
	t.Cleanup(control.Close)

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	args := []string{"-test.run=^" + HelperTestName + "$", "--"}
	env := append(os.Environ(),
		envHelperMode+"=1",
		envScenarioPath+"="+scenarioPath,
		envControlAddr+"="+control.addr,
	)

	commandParts := []string{
		shellEnv(envHelperMode, "1"),
		shellEnv(envScenarioPath, scenarioPath),
		shellEnv(envControlAddr, control.addr),
		shellQuote(exe),
	}
	for _, arg := range args {
		commandParts = append(commandParts, shellQuote(arg))
	}

	return &Config{
		Executable: exe,
		Args:       args,
		Env:        env,
		Command:    strings.Join(commandParts, " "),
		closeFn:    control.Close,
		releaseFn:  control.Release,
	}
}

func MaybeRun() {
	if os.Getenv(envHelperMode) != "1" {
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func run() error {
	path := os.Getenv(envScenarioPath)
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("missing %s", envScenarioPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read scenario: %w", err)
	}
	var scenario Scenario
	if err := json.Unmarshal(data, &scenario); err != nil {
		return fmt.Errorf("decode scenario: %w", err)
	}

	var control *json.Decoder
	if addr := os.Getenv(envControlAddr); strings.TrimSpace(addr) != "" {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return fmt.Errorf("connect control server: %w", err)
		}
		defer conn.Close()
		control = json.NewDecoder(conn)
	}

	tracePath := os.Getenv(envTraceFilePath)
	scanner := bufio.NewScanner(os.Stdin)
	const maxLine = 2 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)

	stepIndex := 0
	for scanner.Scan() {
		line := scanner.Text()
		if err := traceLine(tracePath, line); err != nil {
			return err
		}
		if stepIndex >= len(scenario.Steps) {
			continue
		}
		payload, ok := decodeObject(line)
		if !ok {
			return fmt.Errorf("expected JSON request at step %d, got %q", stepIndex, line)
		}
		step := scenario.Steps[stepIndex]
		if !step.Match.matches(payload) {
			return fmt.Errorf("unexpected payload at step %d: want %+v got %s", stepIndex, step.Match, line)
		}
		if err := emitOutputs(step.Emit); err != nil {
			return err
		}
		if step.WaitForRelease != "" {
			if control == nil {
				return fmt.Errorf("step %d requires release %q but no control channel configured", stepIndex, step.WaitForRelease)
			}
			if err := waitForRelease(control, step.WaitForRelease); err != nil {
				return err
			}
		}
		if err := emitOutputs(step.EmitAfterRelease); err != nil {
			return err
		}
		if step.ExitCode != nil {
			os.Exit(*step.ExitCode)
		}
		stepIndex++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan stdin: %w", err)
	}
	if stepIndex != len(scenario.Steps) {
		return fmt.Errorf("scenario incomplete: ran %d/%d steps", stepIndex, len(scenario.Steps))
	}
	return nil
}

func decodeObject(line string) (map[string]interface{}, bool) {
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

func traceLine(path, line string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open trace file: %w", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "JSON:%s\n", line); err != nil {
		return fmt.Errorf("write trace line: %w", err)
	}
	return nil
}

func emitOutputs(outputs []Output) error {
	for _, output := range outputs {
		target := os.Stdout
		if strings.EqualFold(output.Stream, "stderr") {
			target = os.Stderr
		}
		switch {
		case output.JSON != nil:
			body, err := json.Marshal(output.JSON)
			if err != nil {
				return fmt.Errorf("marshal output JSON: %w", err)
			}
			if _, err := fmt.Fprintln(target, string(body)); err != nil {
				return fmt.Errorf("write JSON output: %w", err)
			}
		default:
			if _, err := fmt.Fprintln(target, output.Text); err != nil {
				return fmt.Errorf("write text output: %w", err)
			}
		}
	}
	return nil
}

func waitForRelease(decoder *json.Decoder, want string) error {
	for {
		var msg struct {
			Release string `json:"release"`
		}
		if err := decoder.Decode(&msg); err != nil {
			return fmt.Errorf("read control release: %w", err)
		}
		if msg.Release == want {
			return nil
		}
	}
}

func (m Match) matches(payload map[string]interface{}) bool {
	if m.Method != "" {
		method, _ := payload["method"].(string)
		if method != m.Method {
			return false
		}
	}
	if m.ID != nil {
		switch value := payload["id"].(type) {
		case float64:
			if int(value) != *m.ID {
				return false
			}
		case int:
			if value != *m.ID {
				return false
			}
		default:
			return false
		}
	}
	return true
}

type controlServer struct {
	addr     string
	listener net.Listener
	releases chan string
	done     chan struct{}
	once     sync.Once
}

func newControlServer() (*controlServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	server := &controlServer{
		addr:     ln.Addr().String(),
		listener: ln,
		releases: make(chan string, 32),
		done:     make(chan struct{}),
	}
	go server.serve()
	return server, nil
}

func (s *controlServer) Release(name string) {
	select {
	case s.releases <- name:
	case <-s.done:
	}
}

func (s *controlServer) Close() {
	s.once.Do(func() {
		close(s.done)
		_ = s.listener.Close()
		close(s.releases)
	})
}

func (s *controlServer) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	for {
		select {
		case <-s.done:
			return
		case name, ok := <-s.releases:
			if !ok {
				return
			}
			if err := encoder.Encode(map[string]string{"release": name}); err != nil {
				return
			}
		}
	}
}

func shellEnv(key, value string) string {
	return key + "=" + shellQuote(value)
}

func shellQuote(value string) string {
	return strconv.Quote(value)
}

func CommandString(t *testing.T, scenario Scenario) (string, func(string)) {
	t.Helper()
	cfg := NewConfig(t, scenario)
	return cfg.Command, cfg.Release
}

func ExecCommand(t *testing.T, scenario Scenario) (*exec.Cmd, func(string)) {
	t.Helper()
	cfg := NewConfig(t, scenario)
	cmd := exec.Command(cfg.Executable, cfg.Args...)
	cmd.Env = cfg.Env
	t.Cleanup(cfg.Close)
	return cmd, cfg.Release
}

func Int(v int) *int {
	return &v
}
