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
	invocation, ok := codexVersionInvocationFromCommand(command)
	if !ok {
		return status, nil
	}
	resolved, err := exec.LookPath(invocation.Executable)
	if err != nil {
		return status, err
	}
	resolved = filepath.Clean(resolved)
	status.ExecutablePath = resolved
	cacheKey := resolved
	if invocation.CacheKey != "" {
		cacheKey += "\x00" + invocation.CacheKey
	}
	if cached, ok := codexVersionCache.Load(cacheKey); ok {
		status.Actual = cached.(string)
		return status, nil
	}
	cmd := exec.Command(resolved, invocation.Args...)
	output, err := cmd.Output()
	if err != nil {
		return status, err
	}
	actual := parseCodexVersion(output)
	if actual == "" {
		return status, fmt.Errorf("unable to parse codex version from %q", strings.TrimSpace(string(output)))
	}
	codexVersionCache.Store(cacheKey, actual)
	status.Actual = actual
	return status, nil
}

type codexVersionInvocation struct {
	Executable string
	Args       []string
	CacheKey   string
}

func codexVersionInvocationFromCommand(command string) (codexVersionInvocation, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return codexVersionInvocation{}, false
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return codexVersionInvocation{}, false
	}
	if looksLikeCodexCommand(parts[0]) {
		return codexVersionInvocation{
			Executable: parts[0],
			Args:       []string{"--version"},
		}, true
	}
	if looksLikeNPXCommand(parts[0]) {
		return npxCodexVersionInvocation(parts)
	}
	return codexVersionInvocation{}, false
}

func npxCodexVersionInvocation(parts []string) (codexVersionInvocation, bool) {
	args := make([]string, 0, len(parts))
	for i := 1; i < len(parts); i++ {
		token := strings.TrimSpace(parts[i])
		if token == "" {
			continue
		}
		if token == "--" {
			return codexVersionInvocation{}, false
		}
		if strings.HasPrefix(token, "-") {
			args = append(args, token)
			continue
		}
		if !looksLikeCodexPackageSpec(token) {
			return codexVersionInvocation{}, false
		}
		args = append(args, token, "--version")
		return codexVersionInvocation{
			Executable: parts[0],
			Args:       args,
			CacheKey:   strings.Join(args, "\x00"),
		}, true
	}
	return codexVersionInvocation{}, false
}

func looksLikeNPXCommand(executable string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(executable)))
	return base == "npx" || base == "npx.cmd" || base == "npx.exe"
}

func looksLikeCodexPackageSpec(token string) bool {
	token = strings.TrimSpace(token)
	return token == "@openai/codex" || strings.HasPrefix(token, "@openai/codex@")
}

func parseCodexVersion(output []byte) string {
	match := codexVersionPattern.FindSubmatch(bytes.TrimSpace(output))
	if len(match) < 2 {
		return ""
	}
	return string(match[1])
}
