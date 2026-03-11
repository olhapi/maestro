package mcp

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedDaemonRegistryLifecycle(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())

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

func TestManagedDaemonRejectsSecondOwnerForSameStore(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())

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
	} else if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("unexpected duplicate-owner error: %v", err)
	}
}

func TestManagedDaemonAllowsDifferentStores(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())

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
