package httpserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

func (testProvider) RequestProjectRefresh(projectID string) map[string]interface{} {
	return map[string]interface{}{"status": "accepted", "project_id": projectID}
}

func (testProvider) StopProjectRuns(projectID string) map[string]interface{} {
	return map[string]interface{}{"status": "stopped", "project_id": projectID, "stopped_runs": 0}
}

func (testProvider) RetryIssueNow(identifier string) map[string]interface{} {
	return map[string]interface{}{"status": "queued_now", "issue": identifier}
}

func (testProvider) RunRecurringIssueNow(identifier string) map[string]interface{} {
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

func TestNewHandlerProxiesDashboardToDevServerWhenConfigured(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	devServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "vite-dev:"+r.URL.Path)
	}))
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

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := Start(ctx, addr, store, testProvider{}); err != nil {
		t.Fatalf("Start: %v", err)
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

func TestStartFailsWhenPortIsOccupied(t *testing.T) {
	store, err := kanban.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := Start(ctx, ln.Addr().String(), store, testProvider{}); err == nil {
		t.Fatal("expected Start to fail on an occupied port")
	}
}
