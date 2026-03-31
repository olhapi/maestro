//go:build !windows

package appserver

import (
	"io"
	"os/exec"
	"testing"
	"time"
)

func TestProcessGroupHelperBranches(t *testing.T) {
	if err := CleanupLingeringAppServerProcess(0); err != nil {
		t.Fatalf("CleanupLingeringAppServerProcess zero pid: %v", err)
	}
	if err := CleanupLingeringAppServerProcess(-1); err != nil {
		t.Fatalf("CleanupLingeringAppServerProcess negative pid: %v", err)
	}
	if err := CleanupLingeringAppServerProcess(999999); err != nil {
		t.Fatalf("CleanupLingeringAppServerProcess missing pid: %v", err)
	}

	if managedProcessExists(0) || managedProcessGroupExists(0) {
		t.Fatal("expected zero pid to report no process or process group")
	}
	if !waitForManagedProcessGroupExit(0, 0) {
		t.Fatal("expected zero pid to count as exited")
	}
	if err := terminateManagedProcessTree(0, 0, 0); err != nil {
		t.Fatalf("terminateManagedProcessTree zero pid: %v", err)
	}
	if err := terminateManagedProcessTree(-1, 0, 0); err != nil {
		t.Fatalf("terminateManagedProcessTree negative pid: %v", err)
	}
	configureManagedProcess(nil)

	if state, ok := processState(0); ok || state != "" {
		t.Fatalf("expected processState(0) to report no state, got %q %v", state, ok)
	}
	if alive, ok := processGroupHasLivingMember(0); ok || alive {
		t.Fatalf("expected processGroupHasLivingMember(0) to report no group, got %v %v", alive, ok)
	}
}

func TestTerminateManagedProcessTreeStopsGracefullyWhenLeaderExitsOnSignal(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-lc", "trap 'exit 0' TERM INT; while :; do sleep 1; done")
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

	if !managedProcessExists(cmd.Process.Pid) {
		t.Fatalf("expected leader %d to be running", cmd.Process.Pid)
	}
	if !managedProcessGroupExists(cmd.Process.Pid) {
		t.Fatalf("expected group %d to be running", cmd.Process.Pid)
	}
	if ok := waitForManagedProcessGroupExit(cmd.Process.Pid, 0); ok {
		t.Fatal("expected zero wait to report a running process group")
	}

	if err := terminateManagedProcessTree(cmd.Process.Pid, 2*time.Second, 2*time.Second); err != nil {
		t.Fatalf("terminateManagedProcessTree: %v", err)
	}

	waitForClientTestCondition(t, 2*time.Second, func() bool {
		return !managedProcessExists(cmd.Process.Pid) && !managedProcessGroupExists(cmd.Process.Pid)
	})

	select {
	case err := <-waitCh:
		if err != nil {
			t.Fatalf("expected clean exit, got %v", err)
		}
	default:
		t.Fatal("expected process wait to complete")
	}
}
