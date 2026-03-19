//go:build !windows

package appserver

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configureManagedProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateManagedProcessTree(pid int, termWait, killWait time.Duration) error {
	if pid <= 0 {
		return nil
	}
	if err := signalManagedProcessGroup(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if waitForManagedProcessGroupExit(pid, termWait) {
		return nil
	}
	if err := signalManagedProcessGroup(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	_ = waitForManagedProcessGroupExit(pid, killWait)
	return nil
}

func managedProcessLeaderExists(pid int) bool {
	return managedProcessExists(pid)
}

func signalManagedProcessGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}

func waitForManagedProcessGroupExit(pid int, wait time.Duration) bool {
	if pid <= 0 {
		return true
	}
	if !managedProcessGroupExists(pid) {
		return true
	}
	if wait <= 0 {
		return false
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if !managedProcessGroupExists(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !managedProcessGroupExists(pid)
}

func managedProcessExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func managedProcessGroupExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
