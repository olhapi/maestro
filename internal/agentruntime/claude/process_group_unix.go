//go:build !windows

package claude

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configureClaudeManagedProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interruptClaudeProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := signalClaudeProcessTree(pid, syscall.SIGINT); err != nil {
		return err
	}
	if waitForClaudeProcessGroupExit(pid, managedProcessTerminateWait) {
		return nil
	}
	if err := signalClaudeProcessTree(pid, syscall.SIGTERM); err != nil {
		return err
	}
	if waitForClaudeProcessGroupExit(pid, managedProcessKillWait) {
		return nil
	}
	if err := signalClaudeProcessTree(pid, syscall.SIGKILL); err != nil {
		return err
	}
	_ = waitForClaudeProcessGroupExit(pid, managedProcessKillWait)
	return nil
}

func signalClaudeProcessTree(pid int, sig syscall.Signal) error {
	err := signalClaudeProcessGroup(pid, sig)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if errors.Is(err, syscall.EPERM) {
		err = syscall.Kill(pid, sig)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
	}
	return err
}

func signalClaudeProcessGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}

func waitForClaudeProcessGroupExit(pid int, wait time.Duration) bool {
	if pid <= 0 {
		return true
	}
	if !claudeProcessGroupExists(pid) {
		return true
	}
	if wait <= 0 {
		return false
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if !claudeProcessGroupExists(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !claudeProcessGroupExists(pid)
}

func claudeProcessGroupExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
