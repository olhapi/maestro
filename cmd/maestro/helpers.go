package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/logsink"
)

const guardrailsAcknowledgementFlag = "--i-understand-that-this-will-be-running-without-the-usual-guardrails"

func parseLogLevel(raw string) (slog.Level, string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(raw)); normalized {
	case "", "info":
		return slog.LevelInfo, "info", nil
	case "debug":
		return slog.LevelDebug, "debug", nil
	case "warn", "warning":
		return slog.LevelWarn, "warn", nil
	case "error":
		return slog.LevelError, "error", nil
	default:
		return slog.LevelInfo, "", fmt.Errorf("unsupported log level %q", raw)
	}
}

func guardrailsAcknowledgementBanner() string {
	return strings.Join([]string{
		"This Maestro implementation is a low key engineering preview.",
		"Codex will run without any guardrails.",
		"Maestro is not a supported product and is presented as-is.",
		"To silence this warning, pass " + guardrailsAcknowledgementFlag + ".",
	}, "\n")
}

func setupLoggerWithWriter(stdout io.Writer, logsRoot string, maxBytes int64, maxFiles int, level slog.Level) (string, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	writer := io.Writer(stdout)
	logPath := ""
	if strings.TrimSpace(logsRoot) != "" {
		if err := ensureDir(logsRoot); err != nil {
			return "", err
		}
		logPath = filepath.Join(logsRoot, "maestro.log")
		f, err := logsink.New(logPath, maxBytes, maxFiles)
		if err != nil {
			return "", err
		}
		writer = logsink.Multi(stdout, f)
	}
	h := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
	if logPath != "" {
		slog.Info("Logger initialized",
			"log_file", logPath,
			"log_level", level.String(),
			"rotate_max_bytes", maxBytes,
			"rotate_max_files", maxFiles,
		)
	}
	return logPath, nil
}

func openStore(dbPath string) (*kanban.Store, error) {
	if dbPath == "" {
		dbPath = filepath.Join(".", ".maestro", "maestro.db")
	}
	if err := ensureDir(filepath.Dir(dbPath)); err != nil {
		return nil, err
	}
	return kanban.NewStore(dbPath)
}

func ensureDir(path string) error {
	if strings.TrimSpace(path) == "" || path == "." {
		return nil
	}
	return os.MkdirAll(path, 0o755)
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		label := strings.TrimSpace(part)
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	return out
}

func parsePositiveInt(raw string, flagName string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s expects an integer: %w", flagName, err)
	}
	return value, nil
}

func loadExtensions(extensionsFile string) (*extensions.Registry, error) {
	return extensions.LoadFile(extensionsFile)
}
