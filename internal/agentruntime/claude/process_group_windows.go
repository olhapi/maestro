//go:build windows

package claude

import (
	"os/exec"
	"strconv"
	"strings"
)

func configureClaudeManagedProcess(*exec.Cmd) {}

func interruptClaudeProcessTree(pid int) error {
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
