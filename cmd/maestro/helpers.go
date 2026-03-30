package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/logsink"
	"github.com/olhapi/maestro/internal/providers"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const guardrailsAcknowledgementFlag = "--i-understand-that-this-will-be-running-without-the-usual-guardrails"

func parseLogLevel(raw string) (slog.Level, string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(raw)); normalized {
	case "":
		return slog.LevelWarn, "warn", nil
	case "info":
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
	resolvedPath, err := resolveDatabasePath(dbPath)
	if err != nil {
		return nil, err
	}
	if err := ensureDir(filepath.Dir(resolvedPath)); err != nil {
		return nil, err
	}
	return kanban.NewStore(resolvedPath)
}

func openReadOnlyStore(dbPath string) (*kanban.Store, error) {
	resolvedPath, err := resolveDatabasePath(dbPath)
	if err != nil {
		return nil, err
	}
	return kanban.NewReadOnlyStore(resolvedPath)
}

func openProviderService(dbPath string) (*kanban.Store, *providers.Service, error) {
	store, err := openStore(dbPath)
	if err != nil {
		return nil, nil, err
	}
	return store, providers.NewService(store), nil
}

func openReadOnlyProviderService(dbPath string) (*kanban.Store, *providers.Service, error) {
	store, err := openStoreForReadCommands(dbPath)
	if err != nil {
		return nil, nil, err
	}
	if store.ReadOnly() {
		return store, providers.NewReadOnlyService(store), nil
	}
	return store, providers.NewService(store), nil
}

func openStoreForReadCommands(dbPath string) (*kanban.Store, error) {
	return openStoreForReadCommandsWith(dbPath, openStore, openReadOnlyStore)
}

func openStoreForReadCommandsWith(dbPath string, writableOpener, readOnlyOpener func(string) (*kanban.Store, error)) (*kanban.Store, error) {
	store, err := writableOpener(dbPath)
	if err == nil {
		return store, nil
	}
	if !shouldFallbackToReadOnly(err) {
		return nil, err
	}
	readOnlyStore, roErr := readOnlyOpener(dbPath)
	if roErr == nil {
		return readOnlyStore, nil
	}
	return nil, err
}

func shouldFallbackToReadOnly(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrPermission) || os.IsPermission(err) {
		return true
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case sqlite3.SQLITE_PERM, sqlite3.SQLITE_READONLY:
			return true
		}
		if sqliteErr.Code() == sqlite3.SQLITE_IOERR|(13<<8) {
			return true
		}
	}
	return false
}

func resolveDatabasePath(dbPath string) (string, error) {
	rawPath := dbPath
	resolvedPath := kanban.ResolveDBPath(dbPath)
	if kanban.HasUnresolvedExpandedEnvPath(rawPath, resolvedPath) {
		return "", fmt.Errorf("failed to resolve database path: unresolved environment variable in %q", resolvedPath)
	}
	return filepath.Abs(resolvedPath)
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
