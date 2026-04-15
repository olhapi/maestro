package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCompleteScenarioWaitsForStdinClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", ".", "--scenario", "complete")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	outLines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			outLines <- scanner.Text()
		}
		close(outLines)
	}()

	sendLine := func(line string) {
		if _, err := fmt.Fprintln(stdin, line); err != nil {
			t.Fatalf("write stdin: %v", err)
		}
	}

	sendLine(`{"id":1,"method":"initialize"}`)
	sendLine(`{"method":"initialized"}`)
	sendLine(`{"id":2,"method":"thread/start"}`)
	sendLine(`{"id":3,"method":"turn/start"}`)

	sawCompletion := false
	deadline := time.After(5 * time.Second)
	for !sawCompletion {
		select {
		case line, ok := <-outLines:
			if !ok {
				t.Fatalf("helper exited before emitting turn/completed\nstderr:\n%s", stderr.String())
			}
			if strings.Contains(line, "turn/completed") {
				sawCompletion = true
			}
		case err := <-done:
			t.Fatalf("helper exited before emitting turn/completed: %v\nstderr:\n%s", err, stderr.String())
		case <-deadline:
			t.Fatalf("timed out waiting for turn/completed\nstderr:\n%s", stderr.String())
		}
	}

	select {
	case err := <-done:
		t.Fatalf("helper exited before stdin was closed: %v\nstderr:\n%s", err, stderr.String())
	case <-time.After(200 * time.Millisecond):
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("Close stdin: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("helper exited with error: %v\nstderr:\n%s", err, stderr.String())
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for helper to exit\nstderr:\n%s", stderr.String())
	}
}
