package mcp

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

func TestManagedDaemonRegistryLifecycle(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "1")
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
		inMemoryDaemonBasePort.Store(0)
	})

	store := testStore(t, filepath.Join(t.TempDir(), "maestro.db"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version")
	if err != nil {
		t.Fatalf("StartManagedDaemon failed: %v", err)
	}
	defer func() { _ = handle.Close() }()

	identity := store.Identity()
	path, err := daemonEntryPath(identity.StoreID)
	if err != nil {
		t.Fatalf("daemonEntryPath failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected daemon registry file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("unexpected daemon registry permissions: %o", got)
	}

	entry, err := DiscoverDaemonForStore(context.Background(), identity)
	if err != nil {
		t.Fatalf("DiscoverDaemonForStore failed: %v", err)
	}
	if entry.StoreID != identity.StoreID {
		t.Fatalf("unexpected store id: got %q want %q", entry.StoreID, identity.StoreID)
	}
	if entry.DBPath != identity.DBPath {
		t.Fatalf("unexpected db path: got %q want %q", entry.DBPath, identity.DBPath)
	}
	if !strings.Contains(entry.BaseURL, "127.0.0.1:") {
		t.Fatalf("expected loopback base URL, got %q", entry.BaseURL)
	}
	if entry.Version != "test-version" {
		t.Fatalf("unexpected version: %q", entry.Version)
	}

	if err := handle.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	waitForMissingFile(t, path)
}

func TestManagedDaemonReplacesStaleRegistryEntry(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "1")
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
		inMemoryDaemonBasePort.Store(0)
	})

	store := testStore(t, filepath.Join(t.TempDir(), "maestro.db"))
	identity := store.Identity()
	stale := DaemonEntry{
		StoreID:     identity.StoreID,
		DBPath:      identity.DBPath,
		PID:         999999,
		StartedAt:   time.Now().Add(-time.Hour).UTC(),
		BaseURL:     "http://127.0.0.1:1/mcp",
		BearerToken: "stale-token",
		Version:     "stale",
	}
	if err := writeDaemonEntry(stale); err != nil {
		t.Fatalf("write stale daemon entry: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handle, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "fresh")
	if err != nil {
		t.Fatalf("StartManagedDaemon failed with stale registry: %v", err)
	}
	defer func() { _ = handle.Close() }()

	entry, _, err := readDaemonEntry(identity.StoreID)
	if err != nil {
		t.Fatalf("readDaemonEntry failed: %v", err)
	}
	if entry.BearerToken == stale.BearerToken || entry.BaseURL == stale.BaseURL {
		t.Fatalf("expected stale daemon entry to be replaced, got %#v", entry)
	}
}

func TestDiscoverDaemonForStoreRejectsProcessLocalEntryFromAnotherProcess(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "1")
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
		inMemoryDaemonBasePort.Store(0)
	})

	store := testStore(t, filepath.Join(t.TempDir(), "maestro.db"))
	identity := store.Identity()
	entry := DaemonEntry{
		StoreID:     identity.StoreID,
		DBPath:      identity.DBPath,
		PID:         os.Getpid() + 1,
		StartedAt:   time.Now().UTC(),
		BaseURL:     "http://127.0.0.1:20001/mcp",
		BearerToken: "process-local",
		Version:     "test-version",
		Transport:   daemonTransportInProcess,
	}
	if err := writeDaemonEntry(entry); err != nil {
		t.Fatalf("write process-local daemon entry: %v", err)
	}

	_, err := DiscoverDaemonForStore(context.Background(), identity)
	if err == nil {
		t.Fatal("expected process-local daemon discovery to fail from another process")
	}
	if !strings.Contains(err.Error(), "process-local transport") {
		t.Fatalf("expected process-local discovery error, got %v", err)
	}
}

func TestManagedDaemonRejectsSecondOwnerForSameStore(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "1")
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
		inMemoryDaemonBasePort.Store(0)
	})

	store := testStore(t, filepath.Join(t.TempDir(), "maestro.db"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test")
	if err != nil {
		t.Fatalf("StartManagedDaemon failed: %v", err)
	}
	defer func() { _ = handle.Close() }()

	if _, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test"); err == nil {
		t.Fatal("expected second daemon start to fail for the same store")
	} else if !strings.Contains(err.Error(), "already") {
		t.Fatalf("unexpected duplicate-owner error: %v", err)
	}
}

func TestManagedDaemonClaimsOwnershipAtomically(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "1")
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
		inMemoryDaemonBasePort.Store(0)
	})

	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	storeA := testStore(t, dbPath)
	storeB := testStore(t, dbPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		handle *DaemonHandle
		err    error
	}

	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup

	for _, store := range []*kanban.Store{storeA, storeB} {
		store := store
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			handle, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test")
			results <- result{handle: handle, err: err}
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	var handles []*DaemonHandle
	var errs []error
	for result := range results {
		if result.err != nil {
			errs = append(errs, result.err)
			continue
		}
		handles = append(handles, result.handle)
	}
	for _, handle := range handles {
		defer func(handle *DaemonHandle) { _ = handle.Close() }(handle)
	}

	if len(handles) != 1 || len(errs) != 1 {
		t.Fatalf("expected exactly one daemon owner, got %d handles and %d errors", len(handles), len(errs))
	}
	if !strings.Contains(errs[0].Error(), "already") {
		t.Fatalf("unexpected concurrent-owner error: %v", errs[0])
	}
}

func TestManagedDaemonAllowsDifferentStores(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "1")
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
		inMemoryDaemonBasePort.Store(0)
	})

	storeA := testStore(t, filepath.Join(t.TempDir(), "a.db"))
	storeB := testStore(t, filepath.Join(t.TempDir(), "b.db"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handleA, err := StartManagedDaemon(ctx, storeA, testRuntimeProvider{store: storeA}, nil, "test")
	if err != nil {
		t.Fatalf("StartManagedDaemon(storeA) failed: %v", err)
	}
	defer func() { _ = handleA.Close() }()

	handleB, err := StartManagedDaemon(ctx, storeB, testRuntimeProvider{store: storeB}, nil, "test")
	if err != nil {
		t.Fatalf("StartManagedDaemon(storeB) failed: %v", err)
	}
	defer func() { _ = handleB.Close() }()

	if handleA.Entry.StoreID == handleB.Entry.StoreID {
		t.Fatalf("expected distinct store identities, got %q", handleA.Entry.StoreID)
	}
}

func TestManagedDaemonPrivateEndpointRequiresBearerToken(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "1")
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
		inMemoryDaemonBasePort.Store(0)
	})

	store := testStore(t, filepath.Join(t.TempDir(), "maestro.db"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test")
	if err != nil {
		t.Fatalf("StartManagedDaemon failed: %v", err)
	}
	defer func() { _ = handle.Close() }()

	req, err := http.NewRequest(http.MethodGet, handle.Entry.BaseURL, nil)
	if err != nil {
		t.Fatalf("new request without auth: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request without auth: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, handle.Entry.BaseURL, nil)
	if err != nil {
		t.Fatalf("new request with invalid auth: %v", err)
	}
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request with invalid auth: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid auth, got %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, handle.Entry.BaseURL, nil)
	if err != nil {
		t.Fatalf("new request with valid auth: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+handle.Entry.BearerToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request with valid auth: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("expected authorized request to pass auth gate")
	}
}

func waitForMissingFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected %s to be removed", path)
}
