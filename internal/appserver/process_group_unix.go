//go:build !windows

package appserver

import (
	"bytes"
	"bufio"
	"errors"
	"os/exec"
	"strconv"
	"strings"
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
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	state, ok := processState(pid)
	if !ok {
		return true
	}
	return !strings.HasPrefix(strings.TrimSpace(state), "Z")
}

func managedProcessGroupExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	alive, ok := processGroupHasLivingMember(pid)
	if !ok {
		return true
	}
	return alive
}

func processState(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	if err != nil {
		return "", false
	}
	state := strings.TrimSpace(string(out))
	return state, state != ""
}

func processGroupHasLivingMember(pgid int) (bool, bool) {
	if pgid <= 0 {
		return false, false
	}
	out, err := exec.Command("ps", "-ax", "-o", "pid=,pgid=,stat=").Output()
	if err != nil {
		return false, false
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		linePGID, err := strconv.Atoi(fields[1])
		if err != nil || linePGID != pgid {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(fields[2]), "Z") {
			return true, true
		}
	}
	if err := scanner.Err(); err != nil {
		return false, false
	}
	return false, true
}
