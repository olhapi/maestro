//go:build !windows

package claude

import (
	"os/exec"
	"testing"
	"time"
)

func TestClaudeProcessGroupHelpers(t *testing.T) {
	if err := interruptClaudeProcessTree(0); err != nil {
		t.Fatalf("interruptClaudeProcessTree zero pid: %v", err)
	}
	if claudeProcessGroupExists(0) {
		t.Fatal("claudeProcessGroupExists should report false for pid 0")
	}
	if !waitForClaudeProcessGroupExit(0, 0) {
		t.Fatal("waitForClaudeProcessGroupExit should succeed for pid 0")
	}

	nilCmd := (*exec.Cmd)(nil)
	configureClaudeManagedProcess(nilCmd)

	cmd := exec.Command("sh", "-c", "trap '' INT TERM; while :; do :; done")
	configureClaudeManagedProcess(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("expected configureClaudeManagedProcess to enable Setpgid, got %#v", cmd.SysProcAttr)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start busy shell: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	if !claudeProcessGroupExists(pid) {
		t.Fatal("expected claude process group to exist while the child is running")
	}
	if waitForClaudeProcessGroupExit(pid, 0) {
		t.Fatal("expected waitForClaudeProcessGroupExit to return false for a live process with zero wait")
	}

	if err := interruptClaudeProcessTree(pid); err != nil {
		t.Fatalf("interruptClaudeProcessTree live pid: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected interrupted process to exit with a non-nil wait error")
	}
	if claudeProcessGroupExists(pid) {
		t.Fatal("expected process group to be gone after interrupt")
	}
	if !waitForClaudeProcessGroupExit(pid, time.Millisecond) {
		t.Fatal("expected waitForClaudeProcessGroupExit to observe the terminated process group")
	}
}
