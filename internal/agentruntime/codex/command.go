package codex

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/olhapi/maestro/internal/codexschema"
)

// ResolveCommand rewrites simple Codex launch commands to the supported schema version when needed.
func ResolveCommand(command string) (string, error) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", nil
	}

	rewritten, rewriteOK := rewriteCodexLaunchCommand(trimmed)
	status, err := DetectVersion(trimmed)
	if err != nil {
		if rewriteOK {
			return rewritten, nil
		}
		return trimmed, err
	}
	if status.ExecutablePath == "" || status.Actual == "" || status.Actual == codexschema.SupportedVersion {
		return trimmed, nil
	}

	if !rewriteOK {
		return trimmed, nil
	}
	return rewritten, nil
}

func rewriteCodexLaunchCommand(command string) (string, bool) {
	if !isSimpleWhitespaceSeparatedCommand(command) {
		return "", false
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", false
	}

	if looksLikeCodexExecutable(parts[0]) {
		if _, err := exec.LookPath("npx"); err != nil {
			return "", false
		}
		rewritten := make([]string, 0, len(parts)+2)
		rewritten = append(rewritten, "npx", "-y", codexPackageSpec(codexschema.SupportedVersion))
		rewritten = append(rewritten, parts[1:]...)
		return strings.Join(rewritten, " "), true
	}

	if !looksLikeNpxExecutable(parts[0]) {
		return "", false
	}

	for i := 1; i < len(parts); i++ {
		token := parts[i]
		if token == "--" {
			return "", false
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		if !looksLikeCodexPackageSpec(token) {
			return "", false
		}
		rewritten := make([]string, 0, len(parts))
		rewritten = append(rewritten, parts[:i]...)
		rewritten = append(rewritten, codexPackageSpec(codexschema.SupportedVersion))
		rewritten = append(rewritten, parts[i+1:]...)
		return strings.Join(rewritten, " "), true
	}

	return "", false
}

func isSimpleWhitespaceSeparatedCommand(command string) bool {
	return !strings.ContainsAny(command, "\"'`|;&<>")
}

func codexPackageSpec(version string) string {
	return "@openai/codex@" + version
}

func looksLikeCodexExecutable(executable string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(executable)))
	return base == "codex" || base == "codex.cmd" || base == "codex.exe"
}

func looksLikeNpxExecutable(executable string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(executable)))
	return base == "npx" || base == "npx.cmd" || base == "npx.exe"
}

func looksLikeCodexPackageSpec(token string) bool {
	token = strings.TrimSpace(token)
	return token == "@openai/codex" || strings.HasPrefix(token, "@openai/codex@")
}
