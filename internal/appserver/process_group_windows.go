//go:build windows

package appserver

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func configureManagedProcess(*exec.Cmd) {}

func terminateManagedProcessTree(pid int, _, _ time.Duration) error {
	if pid <= 0 {
		return nil
	}
	output, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		message := strings.ToLower(strings.TrimSpace(string(output)))
		if strings.Contains(message, "not found") || strings.Contains(message, "no running instance") {
			return nil
		}
		return err
	}
	return nil
}

func managedProcessLeaderExists(pid int) bool {
	return pid > 0
}

func managedProcessExists(pid int) bool {
	return pid > 0
}

func managedProcessGroupExists(pid int) bool {
	return pid > 0
}
