package appserver

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var codexVersionCache sync.Map

var codexVersionPattern = regexp.MustCompile(`\b(\d+\.\d+\.\d+)\b`)

type CodexVersionStatus struct {
	Command        string
	ExecutablePath string
	Expected       string
	Actual         string
}

func DetectCodexVersion(command string) (CodexVersionStatus, error) {
	status := CodexVersionStatus{Command: strings.TrimSpace(command)}
	executable := codexExecutableFromCommand(command)
	if executable == "" {
		return status, nil
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return status, err
	}
	resolved = filepath.Clean(resolved)
	status.ExecutablePath = resolved
	if cached, ok := codexVersionCache.Load(resolved); ok {
		status.Actual = cached.(string)
		return status, nil
	}
	cmd := exec.Command(resolved, "--version")
	output, err := cmd.Output()
	if err != nil {
		return status, err
	}
	actual := parseCodexVersion(output)
	if actual == "" {
		return status, fmt.Errorf("unable to parse codex version from %q", strings.TrimSpace(string(output)))
	}
	codexVersionCache.Store(resolved, actual)
	status.Actual = actual
	return status, nil
}

func codexExecutableFromCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ""
	}
	if looksLikeCodexCommand(parts[0]) {
		return parts[0]
	}
	return ""
}

func parseCodexVersion(output []byte) string {
	match := codexVersionPattern.FindSubmatch(bytes.TrimSpace(output))
	if len(match) < 2 {
		return ""
	}
	return string(match[1])
}
