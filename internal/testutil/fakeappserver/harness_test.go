package fakeappserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func waitForBufferContains(t *testing.T, buf *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in buffer: %q", want, buf.String())
}

func TestRunScenarioPreservesStepOrder(t *testing.T) {
	scenario := Scenario{
		Steps: []Step{
			{
				Match:          Match{Method: "first"},
				Emit:           []Output{{Text: "one"}},
				DelayMS:        1,
				EmitAfterDelay: []Output{{Text: "one-after-delay"}},
			},
			{
				Match: Match{Method: "second"},
				Emit:  []Output{{Text: "two"}},
			},
		},
	}

	stdout := &lockedBuffer{}
	if err := RunScenario(strings.NewReader("{\"method\":\"first\"}\n{\"method\":\"second\"}\n"), stdout, io.Discard, "", nil, scenario); err != nil {
		t.Fatalf("RunScenario: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 || lines[0] != "one" || lines[1] != "one-after-delay" || lines[2] != "two" {
		t.Fatalf("unexpected output order: %#v", lines)
	}
}

func TestRunScenarioWaitForRelease(t *testing.T) {
	controlReader, controlWriter := io.Pipe()
	defer controlReader.Close()
	defer controlWriter.Close()

	scenario := Scenario{
		Steps: []Step{
			{
				Match:            Match{Method: "wait"},
				Emit:             []Output{{Text: "before"}},
				WaitForRelease:   "release-me",
				EmitAfterRelease: []Output{{Text: "after"}},
			},
		},
	}

	stdout := &lockedBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- RunScenario(strings.NewReader("{\"method\":\"wait\"}\n"), stdout, io.Discard, "", json.NewDecoder(controlReader), scenario)
	}()

	waitForBufferContains(t, stdout, "before")
	if strings.Contains(stdout.String(), "after") {
		t.Fatalf("expected after-release output to wait for the release signal: %q", stdout.String())
	}
	select {
	case err := <-done:
		t.Fatalf("RunScenario returned before release: %v", err)
	default:
	}

	if _, err := fmt.Fprintln(controlWriter, `{"release":"release-me"}`); err != nil {
		t.Fatalf("write release signal: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("RunScenario: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 || lines[0] != "before" || lines[1] != "after" {
		t.Fatalf("unexpected output order after release: %#v", lines)
	}
}

func TestRunScenarioReturnsExitCode(t *testing.T) {
	scenario := Scenario{
		Steps: []Step{
			{
				Match:    Match{Method: "exit"},
				Emit:     []Output{{Text: "done"}},
				ExitCode: Int(7),
			},
		},
	}

	stdout := &lockedBuffer{}
	err := RunScenario(strings.NewReader("{\"method\":\"exit\"}\n"), stdout, io.Discard, "", nil, scenario)
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) || exitErr.Code != 7 {
		t.Fatalf("expected exit code error, got %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "done" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}
