package appserver

import (
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
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver/protocol"
	"github.com/olhapi/maestro/internal/appserver/protocol/gen"
	"github.com/olhapi/maestro/internal/codexschema"
	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
)

func readTraceLines(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "JSON:") {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "JSON:")), &payload); err != nil {
			t.Fatalf("decode trace line %q: %v", line, err)
		}
		out = append(out, payload)
	}
	return out
}

func helperClientConfig(t *testing.T, workspace, workspaceRoot string, scenario fakeappserver.Scenario) (ClientConfig, func(string)) {
	t.Helper()
	cfg := fakeappserver.NewConfig(t, scenario)
	t.Cleanup(cfg.Close)
	return ClientConfig{
		Executable:    cfg.Executable,
		Args:          cfg.Args,
		Env:           cfg.Env,
		Workspace:     workspace,
		WorkspaceRoot: workspaceRoot,
		Prompt:        "prompt",
		Title:         "test turn",
		ReadTimeout:   2 * time.Second,
		TurnTimeout:   3 * time.Second,
	}, cfg.Release
}

func withTrace(cfg ClientConfig, traceFile string) ClientConfig {
	cfg.Env = append(cfg.Env, "TRACE_FILE="+traceFile)
	return cfg
}

func baseScenario(threadID, turnID string, afterTurnStart ...fakeappserver.Output) fakeappserver.Scenario {
	return fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": threadID}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: append([]fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": turnID}}},
				}}, afterTurnStart...),
			},
		},
	}
}

func waitForClientTestCondition(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func startOrphanedManagedProcessGroup(t *testing.T) (int, int) {
	t.Helper()

	tmpDir := t.TempDir()
	childPIDPath := filepath.Join(tmpDir, "child.pid")
	cmd := exec.Command("/usr/bin/python3", "-c", `
import os
import subprocess
import sys

os.setsid()
child = subprocess.Popen(
    ["/bin/sh", "-lc", 'trap "" TERM INT; while :; do sleep 1; done'],
    stdin=subprocess.DEVNULL,
    stdout=subprocess.DEVNULL,
    stderr=subprocess.DEVNULL,
)
with open(sys.argv[1], "w", encoding="utf-8") as fh:
    fh.write(str(child.pid))
`, childPIDPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orphaned managed process group: %v", err)
	}
	leaderPID := cmd.Process.Pid

	waitForClientTestCondition(t, time.Second, func() bool {
		data, err := os.ReadFile(childPIDPath)
		return err == nil && strings.TrimSpace(string(data)) != ""
	})
	childPIDText, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(childPIDText)))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait orphaned managed process group leader: %v", err)
	}

	waitForClientTestCondition(t, time.Second, func() bool {
		return !managedProcessExists(leaderPID) && managedProcessGroupExists(leaderPID) && managedProcessExists(childPID)
	})
	t.Cleanup(func() {
		_ = terminateManagedProcessTree(leaderPID, managedProcessTerminateWait, managedProcessKillWait)
	})
	return leaderPID, childPID
}

func TestRunRejectsInvalidWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-1")
	outside := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := ClientConfig{
		Executable:    "/bin/sh",
		Args:          []string{"-c", "exit 0"},
		WorkspaceRoot: workspaceRoot,
	}

	if _, err := Run(context.Background(), ClientConfig{
		Executable:    cfg.Executable,
		Args:          cfg.Args,
		Workspace:     workspaceRoot,
		WorkspaceRoot: workspaceRoot,
	}); err == nil {
		t.Fatal("expected workspace root rejection")
	}

	if _, err := Run(context.Background(), ClientConfig{
		Executable:    cfg.Executable,
		Args:          cfg.Args,
		Workspace:     outside,
		WorkspaceRoot: workspaceRoot,
	}); err == nil {
		t.Fatal("expected outside workspace rejection")
	}
}

func TestClientCloseTerminatesManagedProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup test is Unix-specific")
	}

	tmpDir := t.TempDir()
	childPIDPath := filepath.Join(tmpDir, "child.pid")
	cmd := exec.Command("/bin/sh", "-lc",
		fmt.Sprintf("trap 'exit 0' TERM INT; sh -c 'trap \"\" TERM INT; while :; do sleep 1; done' & child=$!; echo $child > %q; while :; do sleep 1; done", childPIDPath),
	)
	configureManagedProcess(cmd)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start managed process: %v", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	waitForClientTestCondition(t, time.Second, func() bool {
		data, err := os.ReadFile(childPIDPath)
		return err == nil && strings.TrimSpace(string(data)) != ""
	})
	childPIDText, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(childPIDText)))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}
	if !managedProcessExists(childPID) {
		t.Fatalf("expected child process %d to be running", childPID)
	}

	client := &Client{
		cmd:    cmd,
		waitCh: waitCh,
	}
	_ = client.Close()

	waitForClientTestCondition(t, 2*time.Second, func() bool {
		return !managedProcessGroupExists(cmd.Process.Pid) && !managedProcessExists(childPID)
	})
}

func TestClientCloseIgnoresAlreadyExitedManagedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup test is Unix-specific")
	}

	cmd := exec.Command("/bin/sh", "-lc", "exit 0")
	configureManagedProcess(cmd)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start managed process: %v", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	waitForClientTestCondition(t, time.Second, func() bool {
		return !managedProcessLeaderExists(cmd.Process.Pid)
	})

	client := &Client{
		cmd:    cmd,
		waitCh: waitCh,
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close returned error for exited process: %v", err)
	}
}

func TestCleanupLingeringAppServerProcessSkipsOrphanedGroupAfterLeaderExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup test is Unix-specific")
	}

	leaderPID, childPID := startOrphanedManagedProcessGroup(t)
	if err := CleanupLingeringAppServerProcess(leaderPID); err != nil {
		t.Fatalf("CleanupLingeringAppServerProcess: %v", err)
	}

	waitForClientTestCondition(t, 200*time.Millisecond, func() bool {
		return managedProcessGroupExists(leaderPID) && managedProcessExists(childPID)
	})
}

func TestRunApprovalRequiredByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-1")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, baseScenario("thread-1", "turn-1",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     99,
				"method": "item/commandExecution/requestApproval",
				"params": map[string]interface{}{"command": "gh pr view"},
			},
		},
	))
	cfg.Title = "ISS-1: Approval required"
	cfg = withTrace(cfg, traceFile)

	_, err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected approval required error")
	}
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Kind != "approval_required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunAutoApprovesCommandExecutionWhenNever(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-2")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := baseScenario("thread-2", "turn-2",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     99,
				"method": "item/commandExecution/requestApproval",
				"params": map[string]interface{}{"command": "gh pr view"},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(99)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-2", "turnId": "turn-2"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-2: Auto approve"
	cfg.ApprovalPolicy = "never"
	cfg = withTrace(cfg, traceFile)

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.Session == nil || res.Session.SessionID != "thread-2-turn-2" {
		t.Fatalf("unexpected session: %+v", res.Session)
	}

	lines := readTraceLines(t, traceFile)
	foundInit := false
	foundThreadStart := false
	foundTurnStart := false
	foundApproval := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 1 {
			foundInit = nestedBool(payload, "params", "capabilities", "experimentalApi")
		}
		if id, ok := asInt(payload["id"]); ok && id == 2 {
			if nestedStringMap(payload, "method") == "thread/start" && nestedStringMap(payload, "params", "cwd") != "" &&
				nestedStringMap(payload, "params", "config", "initial_collaboration_mode") == "default" {
				foundThreadStart = true
			}
		}
		if id, ok := asInt(payload["id"]); ok && id == 3 {
			if nestedStringMap(payload, "method") == "turn/start" && nestedStringMap(payload, "params", "threadId") == "thread-2" {
				foundTurnStart = true
			}
		}
		if id, ok := asInt(payload["id"]); ok && id == 99 {
			if result, ok := payload["result"].(map[string]interface{}); ok && result["decision"] == "acceptForSession" {
				foundApproval = true
			}
		}
	}
	if !foundInit {
		t.Fatal("expected initialize payload with experimentalApi capability")
	}
	if !foundThreadStart {
		t.Fatal("expected thread/start payload in trace")
	}
	if !foundTurnStart {
		t.Fatal("expected turn/start payload in trace")
	}
	if !foundApproval {
		t.Fatal("expected auto approval response in trace")
	}
}

func TestRunResumesThreadWhenConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-RESUME")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/resume"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-resumed"}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-resumed"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-resumed", "turn": map[string]interface{}{"id": "turn-resumed"}}}},
				},
				ExitCode: fakeappserver.Int(0),
			},
		},
	}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.ResumeThreadID = "thread-stale"
	cfg.ResumeSource = "required"
	cfg = withTrace(cfg, traceFile)

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.Session == nil || res.Session.ThreadID != "thread-resumed" {
		t.Fatalf("expected resumed thread, got %+v", res.Session)
	}

	lines := readTraceLines(t, traceFile)
	foundResume := false
	foundStart := false
	for _, payload := range lines {
		switch nestedStringMap(payload, "method") {
		case "thread/resume":
			foundResume = nestedStringMap(payload, "params", "threadId") == "thread-stale"
		case "thread/start":
			foundStart = true
		}
	}
	if !foundResume {
		t.Fatal("expected thread/resume payload in trace")
	}
	if foundStart {
		t.Fatal("expected successful resume not to fall back to thread/start")
	}
}

func TestRunFallsBackToThreadStartWhenResumeFails(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-FALLBACK")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/resume"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "error": map[string]interface{}{"code": -32000, "message": "resume unavailable"}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-fresh"}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 4, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-fresh"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-fresh", "turn": map[string]interface{}{"id": "turn-fresh"}}}},
				},
				ExitCode: fakeappserver.Int(0),
			},
		},
	}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.ResumeThreadID = "thread-stale"
	cfg.ResumeSource = "opportunistic"
	cfg = withTrace(cfg, traceFile)

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.Session == nil || res.Session.ThreadID != "thread-fresh" {
		t.Fatalf("expected fresh thread after fallback, got %+v", res.Session)
	}

	lines := readTraceLines(t, traceFile)
	foundResume := false
	foundStart := false
	for _, payload := range lines {
		switch nestedStringMap(payload, "method") {
		case "thread/resume":
			foundResume = true
		case "thread/start":
			foundStart = nestedStringMap(payload, "params", "config", "initial_collaboration_mode") == "default"
		}
	}
	if !foundResume || !foundStart {
		t.Fatalf("expected resume attempt and fallback thread/start, got %#v", lines)
	}
}

func TestRunFallsBackToFreshThreadWhenTurnStartThreadIsMissing(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-TURN-FALLBACK")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/resume"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-resumed"}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 3, "error": map[string]interface{}{"code": -32600, "message": "thread not found: thread-resumed"}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 4, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-fresh"}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 5, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-fresh"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-fresh", "turn": map[string]interface{}{"id": "turn-fresh"}}}},
				},
				ExitCode: fakeappserver.Int(0),
			},
		},
	}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.ResumeThreadID = "thread-stale"
	cfg.ResumeSource = "required"
	cfg = withTrace(cfg, traceFile)

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.Session == nil || res.Session.ThreadID != "thread-fresh" {
		t.Fatalf("expected fresh thread after turn/start fallback, got %+v", res.Session)
	}

	lines := readTraceLines(t, traceFile)
	turnThreads := make([]string, 0, 2)
	threadStarts := 0
	for _, payload := range lines {
		switch nestedStringMap(payload, "method") {
		case "turn/start":
			turnThreads = append(turnThreads, nestedStringMap(payload, "params", "threadId"))
		case "thread/start":
			threadStarts++
		}
	}
	if threadStarts != 1 {
		t.Fatalf("expected one fallback thread/start, got %d from %#v", threadStarts, lines)
	}
	if len(turnThreads) != 2 || turnThreads[0] != "thread-resumed" || turnThreads[1] != "thread-fresh" {
		t.Fatalf("expected turn/start to retry on a fresh thread, got %#v", turnThreads)
	}
}

func TestRunTurnFallbackToFreshThreadResetsSessionState(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-TURN-RESET")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-original"}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-original"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-original", "turn": map[string]interface{}{"id": "turn-original"}}}},
				},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 4, "error": map[string]interface{}{"code": -32600, "message": "thread not found: thread-original"}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 5, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-fresh"}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 6, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-fresh"}}}},
					{JSON: map[string]interface{}{
						"id":     90,
						"method": "item/commandExecution/requestApproval",
						"params": map[string]interface{}{
							"threadId": "thread-fresh",
							"turnId":   "turn-fresh",
							"itemId":   "approval-item-fresh",
							"command":  "git status",
						},
					}},
				},
			},
			{
				Match: fakeappserver.Match{ID: fakeappserver.Int(90)},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{
						"method": "turn/completed",
						"params": map[string]interface{}{"threadId": "thread-fresh", "turnId": "turn-fresh"},
					},
				}},
				ExitCode: fakeappserver.Int(0),
			},
		},
	}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.InitialCollaborationMode = "default"
	interrupts := make(chan PendingInteraction, 1)
	cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction != nil {
			interrupts <- interaction.Clone()
		}
	}

	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

	if err := client.RunTurn(context.Background(), cfg.Prompt, cfg.Title); err != nil {
		t.Fatalf("first run turn failed: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(context.Background(), cfg.Prompt, cfg.Title)
	}()

	var interaction PendingInteraction
	select {
	case interaction = <-interrupts:
	case <-time.After(2 * time.Second):
		t.Fatal("expected pending interaction on fresh fallback thread")
	}
	if interaction.CollaborationMode != "default" {
		t.Fatalf("expected fresh fallback thread to restore default collaboration mode, got %+v", interaction)
	}
	if err := client.RespondToInteraction(context.Background(), interaction.ID, PendingInteractionResponse{
		Decision: "acceptForSession",
	}); err != nil {
		t.Fatalf("respond to interaction: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("second run turn failed: %v", err)
	}
	if err := client.Wait(); err != nil {
		t.Fatalf("wait failed: %v", err)
	}

	session := client.Session()
	if session.ThreadID != "thread-fresh" || session.TurnsStarted != 1 {
		t.Fatalf("expected reset session state on fresh thread fallback, got %+v", session)
	}
	for _, event := range session.History {
		if event.ThreadID == "thread-original" {
			t.Fatalf("expected stale thread history to be cleared, got %+v", session.History)
		}
	}
}

func TestRunWithoutResumeConfigStartsFreshThread(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-FRESH")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, baseScenario("thread-fresh-default", "turn-fresh-default",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-fresh-default", "turn": map[string]interface{}{"id": "turn-fresh-default"}},
			},
		},
	))
	cfg = withTrace(cfg, traceFile)

	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	foundResume := false
	foundStart := false
	for _, payload := range lines {
		switch nestedStringMap(payload, "method") {
		case "thread/resume":
			foundResume = true
		case "thread/start":
			foundStart = nestedStringMap(payload, "params", "config", "initial_collaboration_mode") == "default"
		}
	}
	if foundResume {
		t.Fatal("expected default initialization not to send thread/resume")
	}
	if !foundStart {
		t.Fatal("expected default initialization to send thread/start")
	}
}

func TestRunStartsFreshThreadWithExplicitDefaultInitialCollaborationMode(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-FRESH-DEFAULT")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, baseScenario("thread-explicit-default", "turn-explicit-default",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-explicit-default", "turn": map[string]interface{}{"id": "turn-explicit-default"}},
			},
		},
	))
	cfg.InitialCollaborationMode = "default"
	cfg = withTrace(cfg, traceFile)

	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	foundStart := false
	for _, payload := range lines {
		if nestedStringMap(payload, "method") == "thread/start" &&
			nestedStringMap(payload, "params", "config", "initial_collaboration_mode") == "default" {
			foundStart = true
		}
	}
	if !foundStart {
		t.Fatalf("expected explicit initial collaboration mode in thread/start payload, got %#v", lines)
	}
}

func TestRunWaitsForApprovalResponseAndResumesTurnInPlanMode(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-PLAN-WAIT")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := baseScenario("thread-plan-wait", "turn-plan-wait",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     99,
				"method": "item/commandExecution/requestApproval",
				"params": map[string]interface{}{
					"threadId": "thread-plan-wait",
					"turnId":   "turn-plan-wait",
					"itemId":   "approval-item-1",
					"command":  "gh pr view",
				},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(99)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-plan-wait", "turnId": "turn-plan-wait"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.InitialCollaborationMode = "plan"
	cfg = withTrace(cfg, traceFile)
	interrupts := make(chan PendingInteraction, 1)
	cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction != nil {
			interrupts <- interaction.Clone()
		}
	}

	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(context.Background(), cfg.Prompt, cfg.Title)
	}()

	var interaction PendingInteraction
	select {
	case interaction = <-interrupts:
	case <-time.After(2 * time.Second):
		t.Fatal("expected pending interaction")
	}
	if interaction.CollaborationMode != "plan" {
		t.Fatalf("expected plan collaboration mode, got %+v", interaction)
	}
	if interaction.Approval == nil || interaction.Approval.Command != "gh pr view" {
		t.Fatalf("unexpected approval payload: %+v", interaction)
	}
	if err := client.RespondToInteraction(context.Background(), interaction.ID, PendingInteractionResponse{
		Decision: "acceptForSession",
	}); err != nil {
		t.Fatalf("respond to interaction: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("run turn failed: %v", err)
	}
	if err := client.Wait(); err != nil {
		t.Fatalf("wait failed: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	foundResponse := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 99 {
			if result, ok := payload["result"].(map[string]interface{}); ok && result["decision"] == "acceptForSession" {
				foundResponse = true
			}
		}
	}
	if !foundResponse {
		t.Fatalf("expected approval response in trace, got %#v", lines)
	}
}

func TestRunPreservesStructuredApprovalDecisions(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-PLAN-STRUCTURED")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := baseScenario("thread-structured", "turn-structured",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     101,
				"method": "item/commandExecution/requestApproval",
				"params": map[string]interface{}{
					"threadId":                    "thread-structured",
					"turnId":                      "turn-structured",
					"itemId":                      "approval-item-structured",
					"command":                     "curl https://api.github.com",
					"proposedExecpolicyAmendment": []string{"allow command=curl https://api.github.com"},
					"proposedNetworkPolicyAmendments": []map[string]interface{}{
						{"action": "allow", "host": "api.github.com"},
					},
				},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(101)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-structured", "turnId": "turn-structured"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg = withTrace(cfg, traceFile)
	interrupts := make(chan PendingInteraction, 1)
	cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction != nil {
			interrupts <- interaction.Clone()
		}
	}

	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(context.Background(), cfg.Prompt, cfg.Title)
	}()

	var interaction PendingInteraction
	select {
	case interaction = <-interrupts:
	case <-time.After(2 * time.Second):
		t.Fatal("expected pending structured interaction")
	}
	if interaction.Approval == nil {
		t.Fatalf("expected approval payload, got %+v", interaction)
	}

	var execpolicyDecision *PendingApprovalDecision
	var networkDecision *PendingApprovalDecision
	for i := range interaction.Approval.Decisions {
		option := interaction.Approval.Decisions[i]
		if _, ok := nestedMap(option.DecisionPayload, "acceptWithExecpolicyAmendment"); ok {
			execpolicyDecision = &interaction.Approval.Decisions[i]
		}
		if _, ok := nestedMap(option.DecisionPayload, "applyNetworkPolicyAmendment"); ok {
			networkDecision = &interaction.Approval.Decisions[i]
		}
	}
	if execpolicyDecision == nil || networkDecision == nil {
		t.Fatalf("expected structured approval options, got %+v", interaction.Approval.Decisions)
	}

	if err := client.RespondToInteraction(context.Background(), interaction.ID, PendingInteractionResponse{
		DecisionPayload: execpolicyDecision.DecisionPayload,
	}); err != nil {
		t.Fatalf("respond to structured interaction: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("run turn failed: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	foundPayload := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 101 {
			if decision, ok := nestedMap(payload, "result", "decision"); ok {
				if _, ok := nestedMap(decision, "acceptWithExecpolicyAmendment"); ok {
					foundPayload = true
				}
			}
		}
	}
	if !foundPayload {
		t.Fatalf("expected structured approval payload in trace, got %#v", lines)
	}
}

func TestRunWaitsForUserInputResponseAndResumesTurnInDefaultMode(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-DEFAULT-WAIT")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := baseScenario("thread-default-wait", "turn-default-wait",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     110,
				"method": "item/tool/requestUserInput",
				"params": map[string]interface{}{
					"threadId": "thread-default-wait",
					"turnId":   "turn-default-wait",
					"itemId":   "input-item-1",
					"questions": []map[string]interface{}{{
						"id":       "path",
						"question": "Where should the agent write the patch?",
						"isOther":  true,
					}},
				},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(110)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-default-wait", "turnId": "turn-default-wait"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.InitialCollaborationMode = "default"
	cfg = withTrace(cfg, traceFile)
	interrupts := make(chan PendingInteraction, 1)
	cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction != nil {
			interrupts <- interaction.Clone()
		}
	}

	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(context.Background(), cfg.Prompt, cfg.Title)
	}()

	var interaction PendingInteraction
	select {
	case interaction = <-interrupts:
	case <-time.After(2 * time.Second):
		t.Fatal("expected pending user input interaction")
	}
	if interaction.CollaborationMode != "default" {
		t.Fatalf("expected default collaboration mode, got %+v", interaction)
	}
	if interaction.UserInput == nil || len(interaction.UserInput.Questions) != 1 {
		t.Fatalf("unexpected input payload: %+v", interaction)
	}
	if err := client.RespondToInteraction(context.Background(), interaction.ID, PendingInteractionResponse{
		Answers: map[string][]string{
			"path": {"./workspaces/output.patch"},
		},
	}); err != nil {
		t.Fatalf("respond to interaction: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("run turn failed: %v", err)
	}
	if err := client.Wait(); err != nil {
		t.Fatalf("wait failed: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	foundResponse := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 110 {
			if answers, ok := nestedMap(payload, "result", "answers"); ok {
				if questionAnswers, ok := answers["path"].(map[string]interface{}); ok {
					if vals, ok := questionAnswers["answers"].([]interface{}); ok && len(vals) == 1 && vals[0] == "./workspaces/output.patch" {
						foundResponse = true
					}
				}
			}
		}
	}
	if !foundResponse {
		t.Fatalf("expected user input response in trace, got %#v", lines)
	}
}

func TestRunResumedThreadWaitsForApprovalWithoutStartupModeLabel(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-RESUME-WAIT")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/resume"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-resumed-wait"}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-resumed-wait"}}}},
					{JSON: map[string]interface{}{
						"id":     120,
						"method": "item/commandExecution/requestApproval",
						"params": map[string]interface{}{
							"threadId": "thread-resumed-wait",
							"turnId":   "turn-resumed-wait",
							"itemId":   "approval-item-2",
							"command":  "git status",
						},
					}},
				},
			},
			{
				Match: fakeappserver.Match{ID: fakeappserver.Int(120)},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{
						"method": "turn/completed",
						"params": map[string]interface{}{"threadId": "thread-resumed-wait", "turnId": "turn-resumed-wait"},
					},
				}},
				ExitCode: fakeappserver.Int(0),
			},
		},
	}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.ResumeThreadID = "thread-stale"
	cfg.ResumeSource = "required"
	cfg = withTrace(cfg, traceFile)
	interrupts := make(chan PendingInteraction, 1)
	cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction != nil {
			interrupts <- interaction.Clone()
		}
	}

	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(context.Background(), cfg.Prompt, cfg.Title)
	}()

	interaction := <-interrupts
	if interaction.CollaborationMode != "" {
		t.Fatalf("expected resumed interaction to omit startup collaboration mode, got %+v", interaction)
	}
	if err := client.RespondToInteraction(context.Background(), interaction.ID, PendingInteractionResponse{
		Decision: "acceptForSession",
	}); err != nil {
		t.Fatalf("respond to interaction: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("run turn failed: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	for _, payload := range lines {
		if nestedStringMap(payload, "method") == "thread/start" {
			t.Fatalf("expected resumed run not to start a new thread, got %#v", lines)
		}
	}
}

func TestRunIncludesGrantRootChoiceForFileChangeApprovals(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-GRANT-ROOT")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := baseScenario("thread-grant-root", "turn-grant-root",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     130,
				"method": "item/fileChange/requestApproval",
				"params": map[string]interface{}{
					"threadId":  "thread-grant-root",
					"turnId":    "turn-grant-root",
					"itemId":    "file-change-item",
					"grantRoot": "/tmp/granted-root",
					"reason":    "Need broader write access",
				},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(130)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-grant-root", "turnId": "turn-grant-root"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg = withTrace(cfg, traceFile)
	interrupts := make(chan PendingInteraction, 1)
	cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction != nil {
			interrupts <- interaction.Clone()
		}
	}

	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(context.Background(), cfg.Prompt, cfg.Title)
	}()

	interaction := <-interrupts
	if interaction.Approval == nil {
		t.Fatalf("expected approval payload, got %+v", interaction)
	}

	var grantRootDecision *PendingApprovalDecision
	for i := range interaction.Approval.Decisions {
		option := interaction.Approval.Decisions[i]
		if option.Value == "acceptForSession" {
			grantRootDecision = &interaction.Approval.Decisions[i]
			break
		}
	}
	if grantRootDecision == nil {
		t.Fatalf("expected accept-for-session option, got %+v", interaction.Approval.Decisions)
	}
	if grantRootDecision.Label != "Accept and grant root" || !strings.Contains(grantRootDecision.Description, "/tmp/granted-root") {
		t.Fatalf("expected grant-root copy, got %+v", grantRootDecision)
	}

	if err := client.RespondToInteraction(context.Background(), interaction.ID, PendingInteractionResponse{
		Decision: "acceptForSession",
	}); err != nil {
		t.Fatalf("respond to grant-root interaction: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("run turn failed: %v", err)
	}
}

func TestRunStopsWaitingIfAppServerExitsDuringInteraction(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-EXIT-WAIT")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{
						"id":     1,
						"result": map[string]interface{}{},
					},
				}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{
						"id": 2,
						"result": map[string]interface{}{
							"thread": map[string]interface{}{"id": "thread-exit-wait"},
						},
					},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				// Emit the request before any turn/start response so awaitResponse has to dispatch it.
				Emit: []fakeappserver.Output{
					{
						JSON: map[string]interface{}{
							"id":     140,
							"method": "item/commandExecution/requestApproval",
							"params": map[string]interface{}{
								"threadId": "thread-exit-wait",
								"turnId":   "turn-exit-wait",
								"itemId":   "approval-item-exit",
								"command":  "git status",
							},
						},
					},
				},
				ExitCode: fakeappserver.Int(1),
			},
		},
	}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	interrupts := make(chan PendingInteraction, 1)
	doneIDs := make(chan string, 1)
	cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction != nil {
			interrupts <- interaction.Clone()
		}
	}
	cfg.OnPendingInteractionDone = func(interactionID string) {
		doneIDs <- interactionID
	}

	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunTurn(context.Background(), cfg.Prompt, cfg.Title)
	}()

	var interaction PendingInteraction
	select {
	case interaction = <-interrupts:
	case <-time.After(2 * time.Second):
		t.Fatal("expected pending interaction")
	}

	var runErr *RunError
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected interrupted run error")
		}
		if !errors.As(err, &runErr) || runErr.Kind != "run_interrupted" {
			t.Fatalf("expected run_interrupted, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected run turn to stop waiting after app-server exit")
	}

	select {
	case doneID := <-doneIDs:
		if doneID != interaction.ID {
			t.Fatalf("expected interaction %q to be cleared, got %q", interaction.ID, doneID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected pending interaction to be cleared")
	}

	if err := client.RespondToInteraction(context.Background(), interaction.ID, PendingInteractionResponse{
		Decision: "acceptForSession",
	}); !errors.Is(err, ErrPendingInteractionNotFound) {
		t.Fatalf("expected cleared interaction to reject late response, got %v", err)
	}
}

func TestRunAutoApprovesApprovalStyleToolInput(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-3")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := baseScenario("thread-3", "turn-3",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     110,
				"method": "item/tool/requestUserInput",
				"params": map[string]interface{}{
					"questions": []map[string]interface{}{{
						"id": "options-3",
						"options": []map[string]interface{}{
							{"label": "Approve this session"},
							{"label": "Reject"},
						},
					}},
				},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(110)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-3", "turnId": "turn-3"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-3: Tool input"
	cfg.ApprovalPolicy = "never"
	cfg = withTrace(cfg, traceFile)

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	foundAnswer := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 110 {
			if answers, ok := nestedMap(payload, "result", "answers"); ok {
				if q, ok := answers["options-3"].(map[string]interface{}); ok {
					if vals, ok := q["answers"].([]interface{}); ok && len(vals) == 1 && vals[0] == "Approve this session" {
						foundAnswer = true
					}
				}
			}
		}
	}
	if !foundAnswer {
		t.Fatal("expected approval-style tool input answer in trace")
	}
}

func TestRunRejectsNonApprovalToolInputInUnattendedMode(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-3A")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := baseScenario("thread-3a", "turn-3a",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     111,
				"method": "item/tool/requestUserInput",
				"params": map[string]interface{}{
					"questions": []map[string]interface{}{{
						"id": "options-3a",
						"options": []map[string]interface{}{
							{"label": "Use default"},
							{"label": "Skip"},
						},
					}},
				},
			},
		},
	)
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-3A: Tool input"
	cfg.ApprovalPolicy = "never"
	cfg = withTrace(cfg, traceFile)

	_, err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected non-approval tool input to require input")
	}
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Kind != "turn_input_required" {
		t.Fatalf("expected turn_input_required, got %v", err)
	}

	lines := readTraceLines(t, traceFile)
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 111 {
			t.Fatal("expected no auto-answer payload for non-approval tool input")
		}
	}
}

func TestRunRejectsNonApprovalToolInputInUnattendedModeWithObserver(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-3B")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := baseScenario("thread-3b", "turn-3b",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     112,
				"method": "item/tool/requestUserInput",
				"params": map[string]interface{}{
					"questions": []map[string]interface{}{{
						"id": "options-3b",
						"options": []map[string]interface{}{
							{"label": "Use default"},
							{"label": "Skip"},
						},
					}},
				},
			},
		},
	)
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-3B: Tool input"
	cfg.ApprovalPolicy = "never"
	interrupts := make(chan PendingInteraction, 1)
	cfg.OnPendingInteraction = func(interaction *PendingInteraction) {
		if interaction != nil {
			interrupts <- interaction.Clone()
		}
	}

	_, err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected non-approval tool input to require input")
	}
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Kind != "turn_input_required" {
		t.Fatalf("expected turn_input_required, got %v", err)
	}

	select {
	case interaction := <-interrupts:
		t.Fatalf("expected unattended mode not to queue an interaction, got %+v", interaction)
	default:
	}
}

func TestRunHandlesUnsupportedToolCall(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-4")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := baseScenario("thread-4", "turn-4",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id":     120,
				"method": "item/tool/call",
				"params": map[string]interface{}{"tool": "missing_tool", "arguments": map[string]interface{}{}},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{ID: fakeappserver.Int(120)},
		Emit: []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-4", "turnId": "turn-4"},
			},
		}},
		ExitCode: fakeappserver.Int(0),
	})
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-4: Tool call"
	cfg = withTrace(cfg, traceFile)

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	foundUnsupported := false
	for _, payload := range lines {
		if id, ok := asInt(payload["id"]); ok && id == 120 {
			if result, ok := payload["result"].(map[string]interface{}); ok && result["success"] == false {
				items, _ := result["contentItems"].([]interface{})
				if len(items) == 1 {
					item, _ := items[0].(map[string]interface{})
					if text, _ := item["text"].(string); strings.Contains(text, "Unsupported dynamic tool") {
						foundUnsupported = true
					}
				}
			}
		}
	}
	if !foundUnsupported {
		t.Fatal("expected unsupported tool response in trace")
	}
}

func TestRunBuffersLargeProtocolLines(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-5")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("a", 1_100_000)
	scenario := baseScenario("thread-5", "turn-5",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-5", "turnId": "turn-5"},
			},
		},
	)
	scenario.Steps[0].Emit = []fakeappserver.Output{{
		JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}, "padding": padding},
	}}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-5: Large lines"
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.Session == nil || res.Session.SessionID != "thread-5-turn-5" {
		t.Fatalf("unexpected session after large line: %+v", res.Session)
	}
}

func TestRunAcceptsStringThreadSessionSource(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-5A")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{
						"id": 2,
						"result": map[string]interface{}{
							"approvalPolicy": "on-request",
							"cwd":            workspace,
							"model":          "gpt-5",
							"modelProvider":  "openai",
							"sandbox": map[string]interface{}{
								"type":          "dangerFullAccess",
								"networkAccess": true,
							},
							"thread": map[string]interface{}{
								"id":            "thread-5a",
								"cliVersion":    codexschema.SupportedVersion,
								"createdAt":     1,
								"cwd":           workspace,
								"ephemeral":     false,
								"modelProvider": "openai",
								"preview":       "",
								"source":        "appServer",
								"status":        map[string]interface{}{"type": "idle"},
								"turns":         []interface{}{},
								"updatedAt":     2,
							},
						},
					},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{
						JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-5a"}}},
					},
					{
						JSON: map[string]interface{}{
							"method": "turn/completed",
							"params": map[string]interface{}{"threadId": "thread-5a", "turnId": "turn-5a"},
						},
					},
				},
				ExitCode: fakeappserver.Int(0),
			},
		},
	}

	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-5A: String source"
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.Session == nil || res.Session.SessionID != "thread-5a-turn-5a" {
		t.Fatalf("unexpected session: %+v", res.Session)
	}
}

func nestedMap(m map[string]interface{}, path ...string) (map[string]interface{}, bool) {
	var cur interface{} = m
	for _, part := range path {
		next, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur = next[part]
	}
	out, ok := cur.(map[string]interface{})
	return out, ok
}

func nestedBool(m map[string]interface{}, path ...string) bool {
	var cur interface{} = m
	for _, part := range path {
		next, ok := cur.(map[string]interface{})
		if !ok {
			return false
		}
		cur = next[part]
	}
	v, _ := cur.(bool)
	return v
}

func nestedStringMap(m map[string]interface{}, path ...string) string {
	var cur interface{} = m
	for _, part := range path {
		next, ok := cur.(map[string]interface{})
		if !ok {
			return ""
		}
		cur = next[part]
	}
	v, _ := cur.(string)
	return v
}

func TestRunAllowsQuietPeriodsShorterThanStallTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-6")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := baseScenario("thread-stall-ok", "turn-stall-ok")
	scenario.Steps[3].WaitForRelease = "complete"
	scenario.Steps[3].EmitAfterRelease = []fakeappserver.Output{{
		JSON: map[string]interface{}{
			"method": "turn/completed",
			"params": map[string]interface{}{"threadId": "thread-stall-ok", "turnId": "turn-stall-ok"},
		},
	}}
	scenario.Steps[3].ExitCode = fakeappserver.Int(0)
	cfg, release := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-6: Quiet period"
	cfg.ReadTimeout = 1 * time.Second
	cfg.TurnTimeout = 5 * time.Second
	cfg.StallTimeout = 3 * time.Second
	go func() {
		time.Sleep(2 * time.Second)
		release("complete")
	}()
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed after short quiet period: %v", err)
	}
	if res.Session == nil || res.Session.SessionID != "thread-stall-ok-turn-stall-ok" {
		t.Fatalf("unexpected session after quiet period: %+v", res.Session)
	}
}

func TestRunFailsWhenQuietPeriodExceedsStallTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-7")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := baseScenario("thread-stall-fail", "turn-stall-fail")
	scenario.Steps[3].WaitForRelease = "never"
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-7: Stalled"
	cfg.ReadTimeout = 1 * time.Second
	cfg.TurnTimeout = 5 * time.Second
	cfg.StallTimeout = 2500 * time.Millisecond
	_, err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected stall timeout")
	}
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Kind != "stall_timeout" {
		t.Fatalf("expected stall_timeout, got %v", err)
	}
}

func TestRunDoesNotLogReadTimeoutNoiseAtInfo(t *testing.T) {
	logs := captureLogs(t, slog.LevelInfo)

	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-7A")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := baseScenario("thread-quiet", "turn-quiet")
	scenario.Steps[3].WaitForRelease = "complete"
	scenario.Steps[3].EmitAfterRelease = []fakeappserver.Output{{
		JSON: map[string]interface{}{
			"method": "turn/completed",
			"params": map[string]interface{}{"threadId": "thread-quiet", "turnId": "turn-quiet"},
		},
	}}
	scenario.Steps[3].ExitCode = fakeappserver.Int(0)

	cfg, release := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg.Title = "ISS-7A: Quiet logging"
	cfg.ReadTimeout = 200 * time.Millisecond
	cfg.TurnTimeout = 2 * time.Second
	cfg.StallTimeout = 1500 * time.Millisecond

	go func() {
		time.Sleep(500 * time.Millisecond)
		release("complete")
	}()

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	text := logs.String()
	if strings.Contains(text, "Codex app-server read timeout") {
		t.Fatalf("expected read timeout polling to stay below info level: %s", text)
	}
	if strings.Contains(text, "Codex turn stalled") {
		t.Fatalf("expected quiet successful turn not to be marked stalled: %s", text)
	}
}

func captureLogs(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	old := slog.Default()
	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})
	return buf
}

func TestRunLogsLifecycleAtInfoWithoutRawStreams(t *testing.T) {
	logs := captureLogs(t, slog.LevelInfo)

	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-6")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, baseScenario("thread-6", "turn-6",
		fakeappserver.Output{Text: "plain stderr-ish text"},
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-6", "turnId": "turn-6"},
			},
		},
	))
	cfg.IssueID = "issue-6"
	cfg.IssueIdentifier = "ISS-6"
	cfg.Title = "ISS-6: Logging"
	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	text := logs.String()
	for _, want := range []string{
		"Codex app-server process started",
		"Codex session initialized",
		"Codex thread started",
		"Codex turn started",
		"Codex turn completed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in logs: %s", want, text)
		}
	}
	if strings.Contains(text, "Codex app-server stream output") {
		t.Fatalf("expected raw stream logs to be hidden at info level: %s", text)
	}
	if !strings.Contains(text, "\"issue_identifier\":\"ISS-6\"") {
		t.Fatalf("expected issue metadata in logs: %s", text)
	}
}

func TestRunLogsRawStreamsAtDebug(t *testing.T) {
	logs := captureLogs(t, slog.LevelDebug)

	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-7")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, baseScenario("thread-7", "turn-7",
		fakeappserver.Output{Stream: "stderr", Text: "stderr stream line"},
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-7", "turnId": "turn-7"},
			},
		},
	))
	cfg.IssueID = "issue-7"
	cfg.IssueIdentifier = "ISS-7"
	cfg.Title = "ISS-7: Debug stream logging"
	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	text := logs.String()
	if !strings.Contains(text, "Codex app-server stream output") {
		t.Fatalf("expected raw stream logs at debug level: %s", text)
	}
	if !strings.Contains(text, "\"stream\":\"stderr\"") {
		t.Fatalf("expected stderr stream metadata: %s", text)
	}
}

func TestRunDoesNotDuplicateTurnStartedAfterNoisyPreResponseEvents(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-NOISY")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	const (
		threadID = "thread-noisy"
		turnID   = "turn-noisy"
	)

	turnOutputs := []fakeappserver.Output{
		{
			JSON: map[string]interface{}{
				"method": protocol.MethodTurnStarted,
				"params": map[string]interface{}{
					"threadId": threadID,
					"turn":     map[string]interface{}{"id": turnID},
				},
			},
		},
	}
	for i := 0; i < defaultSessionHistoryLimit+8; i++ {
		turnOutputs = append(turnOutputs, fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": protocol.MethodThreadTokenUsageUpdated,
				"params": map[string]interface{}{
					"threadId": threadID,
					"turnId":   turnID,
					"tokenUsage": map[string]interface{}{
						"last": map[string]interface{}{
							"inputTokens":  i + 1,
							"outputTokens": i + 2,
							"totalTokens":  i + 3,
						},
					},
				},
			},
		})
	}
	turnOutputs = append(turnOutputs,
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"id": 3,
				"result": map[string]interface{}{
					"turn": map[string]interface{}{"id": turnID},
				},
			},
		},
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": protocol.MethodTurnCompleted,
				"params": map[string]interface{}{
					"threadId": threadID,
					"turnId":   turnID,
				},
			},
		},
	)

	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": threadID}}},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit:  turnOutputs,
			},
		},
	}

	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.Session == nil {
		t.Fatal("expected session result")
	}
	if res.Session.TurnsStarted != 1 {
		t.Fatalf("expected a single turn start, got %+v", res.Session)
	}
	if res.Session.SessionID != threadID+"-"+turnID || !res.Session.Terminal {
		t.Fatalf("unexpected final session: %+v", res.Session)
	}
}

func TestHelperDefaultsAndWorkspaceValidation(t *testing.T) {
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(root, "ISS-1")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	if err := validateWorkspaceCWD(workspace, root); err != nil {
		t.Fatalf("expected valid workspace cwd, got %v", err)
	}

	policy := defaultApprovalPolicy()
	granular, ok := policy["granular"].(map[string]interface{})
	if !ok || granular["sandbox_approval"] != true || granular["rules"] != true || granular["mcp_elicitations"] != true || granular["request_permissions"] != false {
		t.Fatalf("unexpected default approval policy: %#v", policy)
	}

	sandbox := defaultTurnSandboxPolicy(workspace, root)
	if sandbox["type"] != "workspaceWrite" {
		t.Fatalf("unexpected sandbox type: %#v", sandbox)
	}
	roots, ok := sandbox["writableRoots"].([]string)
	if !ok || len(roots) < 2 {
		t.Fatalf("unexpected writable roots: %#v", sandbox)
	}
	if roots[0] == "" || roots[1] == "" {
		t.Fatalf("expected non-empty writable roots: %#v", roots)
	}
	if sandbox["networkAccess"] != true {
		t.Fatalf("expected default sandbox networkAccess=true, got %#v", sandbox)
	}

	if !looksLikeCodexCommand("/usr/local/bin/codex") || !looksLikeCodexCommand("C:/tools/codex.exe") || !looksLikeCodexCommand("C:/tools/codex.cmd") {
		t.Fatal("expected codex executable detection")
	}
	if looksLikeCodexCommand("/bin/sh") {
		t.Fatal("expected non-codex executable to be rejected")
	}
}

func TestNormalizeTurnSandboxPolicyFillsMissingWorkspaceWriteDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	repoRoot := filepath.Join(tmpDir, "repo")
	workspaceRoot := filepath.Join(repoRoot, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-9")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	got := normalizeTurnSandboxPolicy(map[string]interface{}{"type": "workspaceWrite"}, workspace, workspaceRoot)
	if got["networkAccess"] != true {
		t.Fatalf("expected networkAccess=true, got %#v", got)
	}
	roots, ok := got["writableRoots"].([]string)
	if !ok {
		t.Fatalf("expected writableRoots to be []string, got %#v", got["writableRoots"])
	}
	if len(roots) != 3 {
		t.Fatalf("expected workspace, workspace root, and repo root; got %#v", roots)
	}
	if roots[2] != repoRoot {
		t.Fatalf("expected repo root writable for local-path pushes, got %#v", roots)
	}
}

func TestRunErrorAndInputHelpers(t *testing.T) {
	baseErr := errors.New("boom")
	runErr := &RunError{Kind: "turn_failed", Err: baseErr}
	if runErr.Error() != "turn_failed: boom" {
		t.Fatalf("unexpected run error string: %q", runErr.Error())
	}
	if !errors.Is(runErr.Unwrap(), baseErr) {
		t.Fatalf("expected unwrap to return base error: %v", runErr.Unwrap())
	}
	if (&RunError{Kind: "approval_required"}).Error() != "approval_required" {
		t.Fatal("expected bare kind error string")
	}

	if !needsInput("turn/approval_required", nil) {
		t.Fatal("expected turn approval to require input")
	}
	if err := legacyPendingInteractionError(protocol.MethodMCPServerElicitationRequest, nil); err == nil {
		t.Fatal("expected elicitation fallback error")
	} else {
		var runErr *RunError
		if !errors.As(err, &runErr) || runErr.Kind != "turn_input_required" {
			t.Fatalf("expected elicitation fallback to require input, got %v", err)
		}
	}
	if !needsInput("", map[string]interface{}{"requiresInput": true}) {
		t.Fatal("expected requiresInput field to require input")
	}
	if !needsInput("", map[string]interface{}{"params": map[string]interface{}{"type": "input_required"}}) {
		t.Fatal("expected nested input type to require input")
	}
	if needsInput("", map[string]interface{}{"params": map[string]interface{}{"type": "notice"}}) {
		t.Fatal("expected non-input payload to be ignored")
	}

	args := toolCallArguments(map[string]interface{}{"arguments": map[string]interface{}{"value": 1}})
	if args.(map[string]interface{})["value"].(int) != 1 {
		t.Fatalf("unexpected tool call args: %#v", args)
	}
	if label := approvalOptionLabel([]interface{}{
		map[string]interface{}{"label": "Approve once"},
		map[string]interface{}{"label": "Approve this session"},
	}); label != "Approve this session" {
		t.Fatalf("unexpected approval label: %q", label)
	}
}

func TestClientHelperMethodsUpdateSessionAndMessages(t *testing.T) {
	var (
		updates []*Session
		msgs    []map[string]interface{}
	)
	client := &Client{
		cfg: ClientConfig{
			Workspace: "/tmp/work",
			OnSessionUpdate: func(session *Session) {
				updates = append(updates, session)
			},
			OnMessage: func(msg map[string]interface{}) {
				msgs = append(msgs, msg)
			},
		},
		session: &Session{MaxHistory: 4},
		waitCh:  make(chan error, 1),
		lineErr: make(chan error, 1),
	}
	client.logger = client.newLogger()

	client.applyEvent(Event{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1", Message: "started"})
	client.applyEvent(Event{Type: "turn.completed", Message: "done", TotalTokens: 12})
	client.emitMessage("session_started", map[string]interface{}{"session_id": "thread-1-turn-1"})

	if len(updates) != 2 {
		t.Fatalf("expected session updates, got %d", len(updates))
	}
	if updates[1].SessionID != "thread-1-turn-1" || !updates[1].Terminal {
		t.Fatalf("unexpected copied session: %+v", updates[1])
	}
	if len(msgs) != 1 || msgs[0]["event"] != "session_started" {
		t.Fatalf("unexpected emitted messages: %#v", msgs)
	}

	client.waitCh <- nil
	client.lineErr <- io.EOF
	if err := client.Wait(); err != nil {
		t.Fatalf("expected nil wait error, got %v", err)
	}

	client.waitCh = make(chan error, 1)
	client.lineErr = make(chan error, 1)
	client.waitCh <- nil
	client.lineErr <- os.ErrClosed
	if err := client.Wait(); err != nil {
		t.Fatalf("expected nil wait error for closed stream, got %v", err)
	}

	client.waitCh = make(chan error, 1)
	client.lineErr = make(chan error, 1)
	client.waitCh <- nil
	client.lineErr <- errors.New("read |0: file already closed")
	if err := client.Wait(); err != nil {
		t.Fatalf("expected nil wait error for closed descriptor text, got %v", err)
	}

	client.waitCh = make(chan error, 1)
	client.lineErr = make(chan error, 1)
	client.waitCh <- nil
	client.lineErr <- errors.New("read failed")
	if err := client.Wait(); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("expected read failure, got %v", err)
	}
}

func TestRespondToInteractionRejectsStoppedWaiter(t *testing.T) {
	waiter := &interactionWaiter{
		interaction: PendingInteraction{
			ID:     "interrupt-1",
			Kind:   PendingInteractionKindApproval,
			Method: protocol.MethodExecCommandApproval,
			Approval: &PendingApproval{
				Decisions: reviewApprovalDecisions(),
			},
		},
		responseReadyCh: make(chan struct{}),
		doneCh:          make(chan struct{}),
	}
	close(waiter.doneCh)

	client := &Client{
		pendingInteractions: map[string]*interactionWaiter{
			"interrupt-1": waiter,
		},
	}

	err := client.RespondToInteraction(context.Background(), "interrupt-1", PendingInteractionResponse{
		Decision: string(gen.Approved),
	})
	if !errors.Is(err, ErrPendingInteractionNotFound) {
		t.Fatalf("expected stopped waiter to reject response, got %v", err)
	}
}

func TestUpdatePermissionConfigAppliesToSubsequentTurns(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-PERM")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-perm"}}}}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-1"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-perm", "turnId": "turn-1"}}},
				},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 4, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-2"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-perm", "turnId": "turn-2"}}},
				},
			},
		},
	}

	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg = withTrace(cfg, traceFile)
	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	if err := client.RunTurn(context.Background(), "first", "First"); err != nil {
		t.Fatalf("first RunTurn: %v", err)
	}

	client.UpdatePermissionConfig(
		defaultApprovalPolicy(),
		"danger-full-access",
		map[string]interface{}{
			"type":          "dangerFullAccess",
			"networkAccess": true,
		},
	)

	if err := client.RunTurn(context.Background(), "second", "Second"); err != nil {
		t.Fatalf("second RunTurn: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	threadStarts := 0
	turnStarts := 0
	foundUpdatedTurnSandbox := false
	for _, payload := range lines {
		switch payload["method"] {
		case "thread/start":
			threadStarts++
		case "turn/start":
			turnStarts++
			if turnStarts != 2 {
				continue
			}
			params, _ := payload["params"].(map[string]interface{})
			sandboxPolicy, _ := params["sandboxPolicy"].(map[string]interface{})
			if sandboxPolicy["type"] == "dangerFullAccess" && nestedStringMap(params, "threadId") == "thread-perm" {
				foundUpdatedTurnSandbox = true
			}
		}
	}
	if threadStarts != 1 {
		t.Fatalf("expected permission update to reuse the active thread, got %#v", lines)
	}
	if !foundUpdatedTurnSandbox {
		t.Fatalf("expected second turn/start to use updated sandbox policy, got %#v", lines)
	}
}

func TestUpdatePermissionConfigCanDowngradeSubsequentTurnsToWorkspaceWrite(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-PERM")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	traceFile := filepath.Join(tmpDir, "trace.log")
	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 2, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-perm"}}}}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 3, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-1"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-perm", "turnId": "turn-1"}}},
				},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{
					{JSON: map[string]interface{}{"id": 4, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-2"}}}},
					{JSON: map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"threadId": "thread-perm", "turnId": "turn-2"}}},
				},
			},
		},
	}

	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	cfg = withTrace(cfg, traceFile)
	cfg.ThreadSandbox = "danger-full-access"
	cfg.TurnSandboxPolicy = map[string]interface{}{
		"type":          "dangerFullAccess",
		"networkAccess": true,
	}

	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	if err := client.RunTurn(context.Background(), "first", "First"); err != nil {
		t.Fatalf("first RunTurn: %v", err)
	}

	client.UpdatePermissionConfig(defaultApprovalPolicy(), "workspace-write", nil)

	if err := client.RunTurn(context.Background(), "second", "Second"); err != nil {
		t.Fatalf("second RunTurn: %v", err)
	}

	lines := readTraceLines(t, traceFile)
	threadStarts := 0
	turnStarts := 0
	foundUpdatedTurnSandbox := false
	for _, payload := range lines {
		switch payload["method"] {
		case "thread/start":
			threadStarts++
		case "turn/start":
			turnStarts++
			if turnStarts != 2 {
				continue
			}
			params, _ := payload["params"].(map[string]interface{})
			sandboxPolicy, _ := params["sandboxPolicy"].(map[string]interface{})
			if sandboxPolicy["type"] == "workspaceWrite" && nestedStringMap(params, "threadId") == "thread-perm" {
				foundUpdatedTurnSandbox = true
			}
		}
	}
	if threadStarts != 1 {
		t.Fatalf("expected permission downgrade to reuse the active thread, got %#v", lines)
	}
	if !foundUpdatedTurnSandbox {
		t.Fatalf("expected second turn/start to use workspaceWrite sandbox policy, got %#v", lines)
	}
}

func TestRunClearsTerminalStateBeforeStartingAnotherTurnOnSameThread(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspaces")
	workspace := filepath.Join(workspaceRoot, "ISS-MULTI-TURN")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	scenario := baseScenario("thread-multi-turn", "turn-1",
		fakeappserver.Output{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-multi-turn", "turnId": "turn-1"},
			},
		},
	)
	scenario.Steps = append(scenario.Steps, fakeappserver.Step{
		Match: fakeappserver.Match{Method: "turn/start"},
		Emit: []fakeappserver.Output{
			{JSON: map[string]interface{}{"id": 4, "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-2"}}}},
			{JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{"threadId": "thread-multi-turn", "turnId": "turn-2"},
			}},
		},
	})

	cfg, _ := helperClientConfig(t, workspace, workspaceRoot, scenario)
	client, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	if err := client.RunTurn(context.Background(), "first", "First"); err != nil {
		t.Fatalf("first RunTurn: %v", err)
	}
	if err := client.RunTurn(context.Background(), "second", "Second"); err != nil {
		t.Fatalf("second RunTurn: %v", err)
	}

	session := client.Session()
	if session.TurnsStarted != 2 || session.TurnsCompleted != 2 {
		t.Fatalf("expected two completed turns on the same thread, got %+v", session)
	}
	if session.SessionID != "thread-multi-turn-turn-2" || session.TurnID != "turn-2" || !session.Terminal || session.TerminalReason != "turn.completed" {
		t.Fatalf("unexpected final session after second turn: %+v", session)
	}
}

func TestEmitResolvedInteractionActivityPreservesStructuredApprovalStatus(t *testing.T) {
	var events []ActivityEvent
	client := &Client{
		cfg: ClientConfig{
			OnActivityEvent: func(event ActivityEvent) {
				events = append(events, event)
			},
		},
	}

	decisionPayload := map[string]interface{}{
		"acceptWithExecpolicyAmendment": map[string]interface{}{
			"execpolicy_amendment": []interface{}{"allow command=curl https://api.github.com"},
		},
	}
	interaction := PendingInteraction{
		RequestID: "req-structured",
		Kind:      PendingInteractionKindApproval,
		Method:    protocol.MethodItemCommandExecutionApproval,
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "item-1",
		Approval: &PendingApproval{
			Decisions: []PendingApprovalDecision{
				{
					Value:           "accept_with_execpolicy_amendment",
					Label:           "Approve and store rule",
					DecisionPayload: decisionPayload,
				},
			},
		},
	}

	client.emitResolvedInteractionActivity(interaction, PendingInteractionResponse{
		DecisionPayload: decisionPayload,
	})

	if len(events) != 1 {
		t.Fatalf("expected one activity event, got %#v", events)
	}
	if events[0].Status != "accept_with_execpolicy_amendment" {
		t.Fatalf("expected structured approval status, got %+v", events[0])
	}
	if events[0].Raw["decision"] != "accept_with_execpolicy_amendment" {
		t.Fatalf("expected structured decision value in raw payload, got %+v", events[0].Raw)
	}
	if events[0].Raw["decision_label"] != "Approve and store rule" {
		t.Fatalf("expected structured decision label in raw payload, got %+v", events[0].Raw)
	}
}

func TestParseEventLineVariantsAndSessionHelpers(t *testing.T) {
	line := `{"event":{"event_type":"turn.completed","threadId":"thread-2","turnId":"turn-2","usage":{"prompt_tokens":"4","completion_tokens":5,"total_tokens":9},"message":"done"}}`
	evt, ok := ParseEventLine(line)
	if !ok {
		t.Fatal("expected event to parse")
	}
	if evt.Type != "turn.completed" || evt.ThreadID != "thread-2" || evt.TurnID != "turn-2" {
		t.Fatalf("unexpected parsed event: %+v", evt)
	}
	if evt.InputTokens != 4 || evt.OutputTokens != 5 || evt.TotalTokens != 9 {
		t.Fatalf("unexpected token totals: %+v", evt)
	}

	if decoded, ok := decodeJSONObject(`{"ok":true}`); !ok || decoded["ok"] != true {
		t.Fatalf("expected decodeJSONObject success, got %#v %v", decoded, ok)
	}
	if _, ok := decodeJSONObject("not-json"); ok {
		t.Fatal("expected non-json line to be ignored")
	}

	if toolCallName(map[string]interface{}{"name": " demo "}) != "demo" {
		t.Fatal("expected toolCallName to trim whitespace")
	}
	if got := toolCallArguments(nil); got == nil {
		t.Fatal("expected default toolCallArguments map")
	}
	names := supportedToolNames([]map[string]interface{}{{"name": " one "}, {"name": ""}, {"skip": "x"}})
	if len(names) != 1 || names[0] != "one" {
		t.Fatalf("unexpected supported tools: %#v", names)
	}
}
