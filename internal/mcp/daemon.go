package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	transport "github.com/mark3labs/mcp-go/client/transport"
	mcpapi "github.com/mark3labs/mcp-go/mcp"

	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/kanban"
)

const daemonRegistryEnv = "MAESTRO_DAEMON_REGISTRY_DIR"

type DaemonEntry struct {
	StoreID     string    `json:"store_id"`
	DBPath      string    `json:"db_path"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	BaseURL     string    `json:"base_url"`
	BearerToken string    `json:"bearer_token"`
	Version     string    `json:"version"`
}

type DaemonHandle struct {
	Entry    DaemonEntry
	listener net.Listener
	server   *http.Server
	lockFile *os.File
	once     sync.Once
}

func StartManagedDaemon(ctx context.Context, store *kanban.Store, provider RuntimeProvider, registry *extensions.Registry, version string) (*DaemonHandle, error) {
	identity := store.Identity()
	lockFile, err := acquireDaemonLock(ctx, identity)
	if err != nil {
		return nil, err
	}
	releaseLock := true
	defer func() {
		if releaseLock {
			_ = closeDaemonLock(lockFile)
		}
	}()

	if err := ensureSingleDaemonOwner(ctx, identity); err != nil {
		return nil, err
	}

	token, err := generateSecretToken(24)
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for private MCP endpoint: %w", err)
	}

	server := NewServerWithRegistry(store, provider, registry)
	mux := http.NewServeMux()
	mux.Handle("/mcp", requireBearerToken(token, server.StreamableHTTPHandler()))
	httpServer := &http.Server{
		Handler: mux,
	}

	entry := DaemonEntry{
		StoreID:     identity.StoreID,
		DBPath:      identity.DBPath,
		PID:         os.Getpid(),
		StartedAt:   time.Now().UTC(),
		BaseURL:     "http://" + ln.Addr().String() + "/mcp",
		BearerToken: token,
		Version:     version,
	}
	if err := writeDaemonEntry(entry); err != nil {
		_ = ln.Close()
		return nil, err
	}

	handle := &DaemonHandle{
		Entry:    entry,
		listener: ln,
		server:   httpServer,
		lockFile: lockFile,
	}
	releaseLock = false

	go func() {
		<-ctx.Done()
		_ = handle.Close()
	}()

	go func() {
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Private MCP server failed", "error", err)
		}
		_ = removeDaemonEntryIfOwned(entry)
	}()

	return handle, nil
}

func (h *DaemonHandle) Close() error {
	var closeErr error
	h.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		closeErr = h.server.Shutdown(ctx)
		if err := closeDaemonLock(h.lockFile); err != nil && closeErr == nil {
			closeErr = err
		}
		_ = removeDaemonEntryIfOwned(h.Entry)
	})
	return closeErr
}

func acquireDaemonLock(ctx context.Context, identity kanban.StoreIdentity) (*os.File, error) {
	lockPath, err := daemonLockPath(identity.StoreID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := tryLockFile(lockFile); err != nil {
		_ = lockFile.Close()
		if !errors.Is(err, errDaemonLockAlreadyHeld) {
			return nil, err
		}
		entry, _, readErr := readDaemonEntry(identity.StoreID)
		if readErr == nil {
			probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()
			if probeErr := probeDaemon(probeCtx, *entry, identity); probeErr == nil {
				return nil, fmt.Errorf("a Maestro daemon is already running for %s at %s (pid %d)", identity.DBPath, entry.BaseURL, entry.PID)
			}
		}
		return nil, fmt.Errorf("a Maestro daemon is already starting or running for %s", identity.DBPath)
	}
	return lockFile, nil
}

func closeDaemonLock(lockFile *os.File) error {
	if lockFile == nil {
		return nil
	}
	if err := unlockFile(lockFile); err != nil {
		_ = lockFile.Close()
		return err
	}
	return lockFile.Close()
}

func ensureSingleDaemonOwner(ctx context.Context, identity kanban.StoreIdentity) error {
	entry, path, err := readDaemonEntry(identity.StoreID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if err := probeDaemon(probeCtx, *entry, identity); err == nil {
		return fmt.Errorf("a Maestro daemon is already running for %s at %s (pid %d)", identity.DBPath, entry.BaseURL, entry.PID)
	}

	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

func DiscoverDaemonForStore(ctx context.Context, identity kanban.StoreIdentity) (*DaemonEntry, error) {
	entry, _, err := readDaemonEntry(identity.StoreID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no live Maestro daemon found for %s; start `maestro run --db %s` first", identity.DBPath, identity.DBPath)
		}
		return nil, err
	}

	probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if err := probeDaemon(probeCtx, *entry, identity); err != nil {
		return nil, fmt.Errorf("Maestro daemon for %s is unavailable: %w", identity.DBPath, err)
	}
	return entry, nil
}

func DiscoverDaemonForDBPath(ctx context.Context, dbPath string) (*DaemonEntry, error) {
	absDBPath, err := canonicalDBPath(dbPath)
	if err != nil {
		return nil, err
	}

	entries, err := listDaemonEntries()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no live Maestro daemon found for %s; start `maestro run --db %s` first", absDBPath, absDBPath)
		}
		return nil, err
	}

	var probeErr error
	for _, entry := range entries {
		if filepath.Clean(entry.DBPath) != filepath.Clean(absDBPath) {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		err := probeDaemon(probeCtx, entry, kanban.StoreIdentity{
			DBPath:  absDBPath,
			StoreID: entry.StoreID,
		})
		cancel()
		if err == nil {
			matched := entry
			return &matched, nil
		}
		if probeErr == nil {
			probeErr = err
		}
	}
	if probeErr != nil {
		return nil, fmt.Errorf("Maestro daemon for %s is unavailable: %w", absDBPath, probeErr)
	}
	return nil, fmt.Errorf("no live Maestro daemon found for %s; start `maestro run --db %s` first", absDBPath, absDBPath)
}

func probeDaemon(ctx context.Context, entry DaemonEntry, expected kanban.StoreIdentity) error {
	client, err := mcpclient.NewStreamableHttpClient(entry.BaseURL,
		transport.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + entry.BearerToken,
		}),
	)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.Start(ctx); err != nil {
		return err
	}
	if _, err := client.Initialize(ctx, mcpapi.InitializeRequest{
		Params: mcpapi.InitializeParams{
			ProtocolVersion: mcpapi.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpapi.Implementation{Name: "maestro-daemon-probe", Version: entry.Version},
			Capabilities:    mcpapi.ClientCapabilities{},
		},
	}); err != nil {
		return err
	}

	result, err := client.CallTool(ctx, mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{
			Name:      "server_info",
			Arguments: map[string]any{},
		},
	})
	if err != nil {
		return err
	}

	envelope, err := decodeEnvelopeResult(result)
	if err != nil {
		return err
	}
	if envelope.Meta.StoreID != expected.StoreID {
		return fmt.Errorf("store mismatch: expected %s, got %s", expected.StoreID, envelope.Meta.StoreID)
	}
	if filepath.Clean(envelope.Meta.DBPath) != filepath.Clean(expected.DBPath) {
		return fmt.Errorf("db path mismatch: expected %s, got %s", expected.DBPath, envelope.Meta.DBPath)
	}
	return nil
}

func requireBearerToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeDaemonEntry(entry DaemonEntry) error {
	path, err := daemonEntryPath(entry.StoreID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), entry.StoreID+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.WriteString(tmp, "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func readDaemonEntry(storeID string) (*DaemonEntry, string, error) {
	path, err := daemonEntryPath(storeID)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, err
	}
	var entry DaemonEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, path, fmt.Errorf("read daemon registry %s: %w", path, err)
	}
	return &entry, path, nil
}

func removeDaemonEntryIfOwned(entry DaemonEntry) error {
	current, path, err := readDaemonEntry(entry.StoreID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if current.PID != entry.PID || current.BearerToken != entry.BearerToken || current.BaseURL != entry.BaseURL {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func daemonEntryPath(storeID string) (string, error) {
	root, err := daemonRegistryRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, storeID+".json"), nil
}

func daemonLockPath(storeID string) (string, error) {
	root, err := daemonRegistryRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, storeID+".lock"), nil
}

func daemonRegistryRoot() (string, error) {
	if override := strings.TrimSpace(os.Getenv(daemonRegistryEnv)); override != "" {
		return filepath.Abs(override)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "maestro", "daemons"), nil
}

func generateSecretToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func canonicalDBPath(dbPath string) (string, error) {
	return filepath.Abs(kanban.ResolveDBPath(dbPath))
}

func listDaemonEntries() ([]DaemonEntry, error) {
	root, err := daemonRegistryRoot()
	if err != nil {
		return nil, err
	}
	dirEntries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	entries := make([]DaemonEntry, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		if dirEntry.IsDir() || filepath.Ext(dirEntry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(root, dirEntry.Name())
		entry, err := readDaemonEntryFile(path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, *entry)
	}
	return entries, nil
}

func readDaemonEntryFile(path string) (*DaemonEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entry DaemonEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("read daemon registry %s: %w", path, err)
	}
	return &entry, nil
}
