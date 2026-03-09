package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type testProvider struct{}

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

func (testProvider) Events(since int64, limit int) map[string]interface{} {
	return map[string]interface{}{"since": since, "last_seq": 0, "events": []interface{}{}}
}

func (testProvider) RequestRefresh() map[string]interface{} {
	return map[string]interface{}{"status": "accepted"}
}

func (testProvider) RetryIssueNow(identifier string) map[string]interface{} {
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

func TestStartServesAndShutsDownWithContext(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	Start(ctx, addr, store, testProvider{})

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
