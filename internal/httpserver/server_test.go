package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/internal/testutil/inprocessserver"
)

type testProvider struct{}

type staticAddr string

func (a staticAddr) Network() string { return "tcp" }
func (a staticAddr) String() string  { return string(a) }

type stubListener struct {
	addr net.Addr
}

func (l *stubListener) Accept() (net.Conn, error) {
	return nil, errors.New("accept not used")
}

func (l *stubListener) Close() error {
	return nil
}

func (l *stubListener) Addr() net.Addr {
	return l.addr
}

func (testProvider) Status() map[string]interface{} {
	return map[string]interface{}{"active_runs": 1}
}

func (testProvider) Snapshot() observability.Snapshot {
	return observability.Snapshot{
		GeneratedAt: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
	}
}

func (testProvider) LiveSessions() map[string]interface{} {
	return map[string]interface{}{"sessions": map[string]interface{}{}}
}

func (testProvider) PendingInterrupts() appserver.PendingInteractionSnapshot {
	return appserver.PendingInteractionSnapshot{}
}

func (testProvider) PendingInterruptForIssue(issueID, identifier string) (*appserver.PendingInteraction, bool) {
	return nil, false
}

func (testProvider) RespondToInterrupt(ctx context.Context, interactionID string, response appserver.PendingInteractionResponse) error {
	return nil
}

func (testProvider) AcknowledgeInterrupt(ctx context.Context, interactionID string) error {
	return nil
}

func (testProvider) Events(since int64, limit int) map[string]interface{} {
	return map[string]interface{}{"since": since, "last_seq": 0, "events": []interface{}{}}
}

func (testProvider) RequestRefresh() map[string]interface{} {
	return map[string]interface{}{"status": "accepted"}
}

func (testProvider) RequestProjectRefresh(projectID string) map[string]interface{} {
	return map[string]interface{}{"status": "accepted", "project_id": projectID}
}

func (testProvider) StopProjectRuns(projectID string) map[string]interface{} {
	return map[string]interface{}{"status": "stopped", "project_id": projectID, "stopped_runs": 0}
}

func (testProvider) RetryIssueNow(ctx context.Context, identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func (testProvider) RunRecurringIssueNow(ctx context.Context, identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func TestNewHandlerRedirectsDashboardRoutes(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	handler := newHandler(store, testProvider{})

	for path, want := range map[string]string{
		"/dashboard":          "/",
		"/dashboard/projects": "/projects",
		"/dashboard/issues/1": "/issues/1",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusTemporaryRedirect {
			t.Fatalf("%s: expected 307, got %d", path, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != want {
			t.Fatalf("%s: expected redirect to %q, got %q", path, want, got)
		}
	}
}

func TestNewHandlerServesAPIAndSPAContent(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	handler := newHandler(store, testProvider{})

	apiReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("health: expected 200, got %d", apiRec.Code)
	}

	spaReq := httptest.NewRequest(http.MethodGet, "/projects/demo", nil)
	spaRec := httptest.NewRecorder()
	handler.ServeHTTP(spaRec, spaReq)
	if spaRec.Code != http.StatusOK {
		t.Fatalf("spa route: expected 200, got %d", spaRec.Code)
	}
	if contentType := spaRec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("spa route: expected html content type, got %q", contentType)
	}
}

func TestNewHandlerServesWebhookRoute(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	handler := newHandler(store, testProvider{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", strings.NewReader(`{"event":"issue.retry","payload":{"issue_identifier":"ISS-1"}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("webhooks: expected 503 when auth is disabled, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "MAESTRO_WEBHOOK_BEARER_TOKEN") {
		t.Fatalf("webhooks: expected disabled auth guidance, got %q", rec.Body.String())
	}
}

func TestNewHandlerProxiesDashboardToDevServerWhenConfigured(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	devServer, err := inprocessserver.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "vite-dev:"+r.URL.Path)
	}))
	if err != nil {
		t.Fatalf("in-process dev server failed: %v", err)
	}
	defer devServer.Close()

	t.Setenv(uiDevProxyEnv, devServer.URL)

	handler := newHandler(store, testProvider{})

	spaReq := httptest.NewRequest(http.MethodGet, "/projects/demo", nil)
	spaRec := httptest.NewRecorder()
	handler.ServeHTTP(spaRec, spaReq)
	if spaRec.Code != http.StatusOK {
		t.Fatalf("spa route: expected 200, got %d", spaRec.Code)
	}
	if got := spaRec.Body.String(); got != "vite-dev:/projects/demo" {
		t.Fatalf("spa route: expected proxied dev body, got %q", got)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("health: expected 200, got %d", apiRec.Code)
	}
	if strings.Contains(apiRec.Body.String(), "vite-dev:") {
		t.Fatalf("health: expected backend response, got proxied body %q", apiRec.Body.String())
	}
}

func TestNewHandlerFallsBackToEmbeddedUIWhenDevServerIsUnavailable(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	t.Setenv(uiDevProxyEnv, "http://127.0.0.1:1")

	handler := newHandler(store, testProvider{})

	spaReq := httptest.NewRequest(http.MethodGet, "/projects/demo", nil)
	spaRec := httptest.NewRecorder()
	handler.ServeHTTP(spaRec, spaReq)

	if spaRec.Code != http.StatusOK {
		t.Fatalf("spa route: expected 200, got %d", spaRec.Code)
	}
	if contentType := spaRec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("spa route: expected html content type, got %q", contentType)
	}
	if body := strings.ToLower(spaRec.Body.String()); !strings.Contains(body, "<!doctype html>") && !strings.Contains(body, "<html") {
		t.Fatalf("spa route: expected embedded UI fallback, got %q", spaRec.Body.String())
	}
}

func TestStartServesAndShutsDownWithContext(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	addr := nextFakeAddr()

	ctx, cancel := context.WithCancel(context.Background())
	server, err := Start(ctx, addr, store, testProvider{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := server.BaseURL(); got != "" {
		t.Fatalf("expected no public BaseURL for in-process server, got %q", got)
	}

	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Get("http://" + addr + "/health")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("wait for start: %v", err)
	}
	resp.Body.Close()

	cancel()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, err = http.Get("http://" + addr + "/health")
		if err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected server shutdown for %s", addr)
}

func TestStartUsesListenerAndServeHooksWithoutBinding(t *testing.T) {
	t.Setenv(inProcessServerEnv, "")

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	originalListen := listenTCP
	originalServe := serveHTTP
	t.Cleanup(func() {
		listenTCP = originalListen
		serveHTTP = originalServe
	})

	addr := "127.0.0.1:4321"
	served := make(chan struct{})
	releaseServe := make(chan struct{})
	listenTCP = func(network, address string) (net.Listener, error) {
		if network != "tcp" {
			t.Fatalf("listen network = %q, want tcp", network)
		}
		if address != addr {
			t.Fatalf("listen address = %q, want %q", address, addr)
		}
		return &stubListener{addr: staticAddr(addr)}, nil
	}
	serveHTTP = func(srv *http.Server, ln net.Listener) error {
		close(served)
		<-releaseServe
		return errors.New("serve stopped")
	}

	ctx, cancel := context.WithCancel(context.Background())
	server, err := Start(ctx, addr, store, testProvider{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if server.http == nil {
		t.Fatal("expected HTTP server to be initialized")
	}
	if got := server.BaseURL(); got != "http://127.0.0.1:4321" {
		t.Fatalf("BaseURL() = %q, want %q", got, "http://127.0.0.1:4321")
	}
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("serve hook was not called")
	}

	cancel()
	close(releaseServe)
	time.Sleep(20 * time.Millisecond)
}

func TestStartReturnsListenErrorWhenListenerSetupFails(t *testing.T) {
	t.Setenv(inProcessServerEnv, "")

	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	originalListen := listenTCP
	t.Cleanup(func() {
		listenTCP = originalListen
	})
	listenTCP = func(network, address string) (net.Listener, error) {
		return nil, errors.New("listen failed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := Start(ctx, "127.0.0.1:4321", store, testProvider{}); err == nil {
		t.Fatal("expected Start to fail when listener setup fails")
	}
}

func TestStartFailsWhenPortIsOccupied(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	addr := nextFakeAddr()
	occupied, err := inprocessserver.NewWithURL("http://"+addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if err != nil {
		t.Fatalf("occupy fake addr: %v", err)
	}
	defer occupied.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := Start(ctx, addr, store, testProvider{}); err == nil {
		t.Fatal("expected Start to fail on an occupied port")
	}
}

func TestBaseURLForAddrUsesLoopbackForWildcardHosts(t *testing.T) {
	addr := &net.TCPAddr{
		IP:   net.IPv4zero,
		Port: 8787,
	}

	if got := baseURLForAddr(addr); got != "http://127.0.0.1:8787" {
		t.Fatalf("baseURLForAddr(%v) = %q, want %q", addr, got, "http://127.0.0.1:8787")
	}
}

func TestBaseURLForAddrUsesIPv6LoopbackForWildcardIPv6Hosts(t *testing.T) {
	addr := &net.TCPAddr{
		IP:   net.IPv6zero,
		Port: 8787,
	}

	if got := baseURLForAddr(addr); got != "http://[::1]:8787" {
		t.Fatalf("baseURLForAddr(%v) = %q, want %q", addr, got, "http://[::1]:8787")
	}
}

func TestServerBaseURLUsesListenerAddr(t *testing.T) {
	server := &Server{
		listenerAddr: &net.TCPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 4321,
		},
	}

	if got := server.BaseURL(); got != "http://127.0.0.1:4321" {
		t.Fatalf("server.BaseURL() = %q, want %q", got, "http://127.0.0.1:4321")
	}
}

func TestBaseURLForAddrHandlesSplitHostPortAddresses(t *testing.T) {
	tests := []struct {
		addr net.Addr
		want string
	}{
		{addr: nil, want: ""},
		{addr: staticAddr("[::]:8787"), want: "http://[::1]:8787"},
		{addr: staticAddr("example.com:8787"), want: "http://example.com:8787"},
		{addr: staticAddr("not-a-host-port"), want: ""},
	}

	for _, tc := range tests {
		if got := baseURLForAddr(tc.addr); got != tc.want {
			t.Fatalf("baseURLForAddr(%v) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}
