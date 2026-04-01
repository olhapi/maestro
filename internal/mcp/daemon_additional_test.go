package mcp

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMCPDaemonRegistryAndLockHelpers(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv(daemonRegistryEnv, rootDir)

	root, err := daemonRegistryRoot()
	if err != nil {
		t.Fatalf("daemonRegistryRoot failed: %v", err)
	}
	if filepath.Clean(root) != filepath.Clean(rootDir) {
		t.Fatalf("unexpected daemon registry root: %q", root)
	}

	entryPath, err := daemonEntryPath("store-a")
	if err != nil {
		t.Fatalf("daemonEntryPath failed: %v", err)
	}
	if !strings.HasSuffix(entryPath, filepath.Join("store-a.json")) {
		t.Fatalf("unexpected daemon entry path: %q", entryPath)
	}

	lockPath, err := daemonLockPath("store-a")
	if err != nil {
		t.Fatalf("daemonLockPath failed: %v", err)
	}
	if !strings.HasSuffix(lockPath, filepath.Join("store-a.lock")) {
		t.Fatalf("unexpected daemon lock path: %q", lockPath)
	}

	if got := daemonEntryTransport(DaemonEntry{}); got != daemonTransportHTTP {
		t.Fatalf("expected empty transport to normalize to http, got %q", got)
	}
	if got := daemonEntryTransport(DaemonEntry{Transport: daemonTransportInProcess}); got != daemonTransportInProcess {
		t.Fatalf("expected in-process transport to survive, got %q", got)
	}
	if got := daemonEntryTransport(DaemonEntry{Transport: "custom"}); got != daemonTransportHTTP {
		t.Fatalf("expected unknown transport to normalize to http, got %q", got)
	}

	absDBPath, err := canonicalDBPath(filepath.Join("nested", "maestro.db"))
	if err != nil {
		t.Fatalf("canonicalDBPath failed: %v", err)
	}
	if !filepath.IsAbs(absDBPath) {
		t.Fatalf("expected canonical DB path to be absolute, got %q", absDBPath)
	}

	token, err := generateSecretToken(8)
	if err != nil {
		t.Fatalf("generateSecretToken failed: %v", err)
	}
	if len(token) != 16 {
		t.Fatalf("expected 8 random bytes to encode to 16 hex chars, got %q", token)
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("expected token to be valid hex, got %q: %v", token, err)
	}

	entry := DaemonEntry{
		StoreID:     "store-a",
		DBPath:      absDBPath,
		PID:         os.Getpid(),
		StartedAt:   time.Now().UTC(),
		BaseURL:     "http://127.0.0.1:20001/mcp",
		BearerToken: "secret-token",
		Version:     "v1",
		Transport:   daemonTransportInProcess,
	}
	if err := writeDaemonEntry(entry); err != nil {
		t.Fatalf("writeDaemonEntry failed: %v", err)
	}

	readEntry, readPath, err := readDaemonEntry(entry.StoreID)
	if err != nil {
		t.Fatalf("readDaemonEntry failed: %v", err)
	}
	if readPath != entryPath {
		t.Fatalf("unexpected daemon entry path: %q", readPath)
	}
	if readEntry.StoreID != entry.StoreID || readEntry.DBPath != entry.DBPath || readEntry.Transport != entry.Transport {
		t.Fatalf("unexpected read daemon entry: %#v", readEntry)
	}

	valid2 := DaemonEntry{
		StoreID:     "store-b",
		DBPath:      absDBPath,
		PID:         os.Getpid(),
		StartedAt:   time.Now().UTC(),
		BaseURL:     "http://127.0.0.1:20002/mcp",
		BearerToken: "secret-token-2",
		Version:     "v1",
	}
	if err := writeDaemonEntry(valid2); err != nil {
		t.Fatalf("writeDaemonEntry(store-b) failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "ignore.txt"), []byte("ignore"), 0o600); err != nil {
		t.Fatalf("write ignore file: %v", err)
	}

	entries, err := listDaemonEntries()
	if err != nil {
		t.Fatalf("listDaemonEntries failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected two daemon entries, got %#v", entries)
	}

	if err := removeDaemonEntryIfOwned(entry); err != nil {
		t.Fatalf("removeDaemonEntryIfOwned failed: %v", err)
	}
	if _, err := os.Stat(entryPath); !os.IsNotExist(err) {
		t.Fatalf("expected owned daemon entry to be removed, stat err=%v", err)
	}

	if err := closeDaemonLock(nil); err != nil {
		t.Fatalf("closeDaemonLock(nil) failed: %v", err)
	}

	f1, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open first lock file: %v", err)
	}
	defer f1.Close()
	if err := tryLockFile(f1); err != nil {
		t.Fatalf("tryLockFile(first) failed: %v", err)
	}

	f2, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open second lock file: %v", err)
	}
	defer f2.Close()
	if err := tryLockFile(f2); !errors.Is(err, errDaemonLockAlreadyHeld) {
		t.Fatalf("expected second lock attempt to be rejected, got %v", err)
	}
	if err := closeDaemonLock(f1); err != nil {
		t.Fatalf("closeDaemonLock(first) failed: %v", err)
	}
}

func TestMCPInMemoryDaemonLifecycle(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "1")
	useInMemoryDaemonTransport.Store(false)
	inMemoryDaemonBasePort.Store(0)
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
		inMemoryDaemonBasePort.Store(0)
	})

	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	store := testStore(t, dbPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version")
	if err != nil {
		t.Fatalf("StartManagedDaemon failed: %v", err)
	}
	defer func() { _ = handle.Close() }()

	if handle.Entry.Transport != daemonTransportInProcess {
		t.Fatalf("expected in-process daemon transport, got %#v", handle.Entry)
	}
	if !strings.HasPrefix(handle.Entry.BaseURL, "http://127.0.0.1:") {
		t.Fatalf("expected loopback in-process base URL, got %q", handle.Entry.BaseURL)
	}

	if _, err := DiscoverDaemonForStore(context.Background(), store.Identity()); err != nil {
		t.Fatalf("DiscoverDaemonForStore failed: %v", err)
	}
	if _, err := DiscoverDaemonForDBPath(context.Background(), dbPath); err != nil {
		t.Fatalf("DiscoverDaemonForDBPath failed: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, handle.Entry.BaseURL, nil)
	if err != nil {
		t.Fatalf("new GET request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+handle.Entry.BearerToken)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET request to in-process daemon failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected SSE GETs to be rejected with 405, got %d", resp.StatusCode)
	}

	entryPath, err := daemonEntryPath(handle.Entry.StoreID)
	if err != nil {
		t.Fatalf("daemonEntryPath failed: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	waitForMissingFile(t, entryPath)
}

func TestMCPInMemoryDaemonHandler(t *testing.T) {
	handler := buildInMemoryDaemonHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expected GET requests to be intercepted before reaching the next handler")
	}), "token")

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example/mcp", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Accept", "text/event-stream")
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected SSE GETs to be rejected with 405, got %d", recorder.Code)
	}
}

type fakeDaemonListener struct {
	closed chan struct{}
	once   sync.Once
}

type fakeDaemonAddr string

func (a fakeDaemonAddr) Network() string { return "fake" }

func (a fakeDaemonAddr) String() string { return string(a) }

func newFakeDaemonListener() *fakeDaemonListener {
	return &fakeDaemonListener{closed: make(chan struct{})}
}

func (l *fakeDaemonListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *fakeDaemonListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *fakeDaemonListener) Addr() net.Addr {
	return fakeDaemonAddr("fake-listener")
}

func TestMCPManagedDaemonListenFailure(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "")
	useInMemoryDaemonTransport.Store(false)
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
	})

	prevListen := listenFunc
	listenFunc = func(network, address string) (net.Listener, error) {
		return nil, errors.New("boom")
	}
	t.Cleanup(func() { listenFunc = prevListen })

	store := testStore(t, filepath.Join(t.TempDir(), "listen-failure.db"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version"); err == nil || !strings.Contains(err.Error(), "listen for private MCP endpoint") {
		t.Fatalf("expected listen failure, got %v", err)
	}
}

func TestMCPManagedDaemonNetworkLifecycleWithFakeListener(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	t.Setenv("MAESTRO_MCP_INPROCESS", "")
	useInMemoryDaemonTransport.Store(false)
	inMemoryDaemonBasePort.Store(0)
	t.Cleanup(func() {
		useInMemoryDaemonTransport.Store(false)
		inMemoryDaemonBasePort.Store(0)
	})

	prevListen := listenFunc
	listener := newFakeDaemonListener()
	listenFunc = func(network, address string) (net.Listener, error) {
		return listener, nil
	}
	t.Cleanup(func() { listenFunc = prevListen })

	store := testStore(t, filepath.Join(t.TempDir(), "listener.db"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := StartManagedDaemon(ctx, store, testRuntimeProvider{store: store}, nil, "test-version")
	if err != nil {
		t.Fatalf("StartManagedDaemon with fake listener failed: %v", err)
	}
	if handle.Entry.Transport != daemonTransportHTTP {
		t.Fatalf("expected HTTP transport on fake listener path, got %#v", handle.Entry)
	}
	if !strings.HasPrefix(handle.Entry.BaseURL, "http://fake-listener") {
		t.Fatalf("unexpected base URL from fake listener: %q", handle.Entry.BaseURL)
	}

	entryPath, err := daemonEntryPath(handle.Entry.StoreID)
	if err != nil {
		t.Fatalf("daemonEntryPath failed: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("Close with fake listener failed: %v", err)
	}
	waitForMissingFile(t, entryPath)
}

func TestMCPDaemonRegistryErrorBranches(t *testing.T) {
	t.Run("default root", func(t *testing.T) {
		t.Setenv(daemonRegistryEnv, "")
		root, err := daemonRegistryRoot()
		if err != nil {
			t.Fatalf("daemonRegistryRoot default failed: %v", err)
		}
		if !filepath.IsAbs(root) || !strings.Contains(filepath.Clean(root), filepath.Join("maestro", "daemons")) {
			t.Fatalf("unexpected default daemon registry root: %q", root)
		}
	})

	t.Run("mismatched ownership and invalid file", func(t *testing.T) {
		rootDir := t.TempDir()
		t.Setenv(daemonRegistryEnv, rootDir)

		entry := DaemonEntry{
			StoreID:     "store-a",
			DBPath:      filepath.Join(t.TempDir(), "maestro.db"),
			PID:         os.Getpid(),
			StartedAt:   time.Now().UTC(),
			BaseURL:     "http://127.0.0.1:20001/mcp",
			BearerToken: "secret-token",
			Version:     "v1",
		}
		if err := writeDaemonEntry(entry); err != nil {
			t.Fatalf("writeDaemonEntry failed: %v", err)
		}
		mismatch := entry
		mismatch.PID++
		if err := removeDaemonEntryIfOwned(mismatch); err != nil {
			t.Fatalf("removeDaemonEntryIfOwned(mismatch) failed: %v", err)
		}
		entryPath, err := daemonEntryPath(entry.StoreID)
		if err != nil {
			t.Fatalf("daemonEntryPath failed: %v", err)
		}
		if _, err := os.Stat(entryPath); err != nil {
			t.Fatalf("expected mismatched daemon entry to remain, stat err=%v", err)
		}
		if err := removeDaemonEntryIfOwned(DaemonEntry{StoreID: "missing"}); err != nil {
			t.Fatalf("removeDaemonEntryIfOwned(missing) failed: %v", err)
		}

		brokenPath := filepath.Join(rootDir, "broken.json")
		if err := os.WriteFile(brokenPath, []byte("{"), 0o600); err != nil {
			t.Fatalf("write broken daemon file: %v", err)
		}
		if _, err := readDaemonEntryFile(brokenPath); err == nil {
			t.Fatal("expected invalid daemon entry JSON to fail")
		}
	})

	t.Run("discover missing daemon", func(t *testing.T) {
		t.Setenv(daemonRegistryEnv, t.TempDir())
		if _, err := DiscoverDaemonForDBPath(context.Background(), filepath.Join(t.TempDir(), "missing.db")); err == nil || !strings.Contains(err.Error(), "no live Maestro daemon found") {
			t.Fatalf("expected missing daemon discovery error, got %v", err)
		}
	})
}

func TestMCPWriteDaemonEntryRejectsFileRegistryRoot(t *testing.T) {
	rootFile := filepath.Join(t.TempDir(), "registry-root")
	if err := os.WriteFile(rootFile, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatalf("write registry root file: %v", err)
	}
	t.Setenv(daemonRegistryEnv, rootFile)

	err := writeDaemonEntry(DaemonEntry{
		StoreID:     "store-a",
		DBPath:      filepath.Join(t.TempDir(), "maestro.db"),
		PID:         os.Getpid(),
		StartedAt:   time.Now().UTC(),
		BaseURL:     "http://127.0.0.1:20001/mcp",
		BearerToken: "secret-token",
		Version:     "v1",
		Transport:   daemonTransportHTTP,
	})
	if err == nil {
		t.Fatal("expected writeDaemonEntry to fail when the registry root is a file")
	}
}
