package dashboardui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexForRootAndClientRoutes(t *testing.T) {
	handler := Handler()

	for _, path := range []string{"/", "/projects/abc", "/issues/ISS-1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rec.Code)
		}
		if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
			t.Fatalf("%s: expected html content type, got %q", path, contentType)
		}
		if !strings.Contains(rec.Body.String(), "<!doctype html>") && !strings.Contains(strings.ToLower(rec.Body.String()), "<html") {
			t.Fatalf("%s: expected index html body", path)
		}
	}
}

func TestHandlerServesEmbeddedAssetsWithoutSPAFallback(t *testing.T) {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		t.Fatalf("sub dist fs: %v", err)
	}

	entries, err := fs.ReadDir(dist, "assets")
	if err != nil {
		t.Fatalf("read assets dir: %v", err)
	}

	handler := Handler()
	var checked int
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".css") && !strings.HasSuffix(name, ".js") {
			continue
		}

		asset := "/assets/" + name
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, asset, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			contentType := rec.Header().Get("Content-Type")
			if strings.HasSuffix(name, ".js") && !strings.Contains(contentType, "javascript") {
				t.Fatalf("expected javascript content type, got %q", contentType)
			}
			if strings.HasSuffix(name, ".css") && !strings.Contains(contentType, "text/css") {
				t.Fatalf("expected css content type, got %q", contentType)
			}
			body := rec.Body.String()
			if body == "" {
				t.Fatal("expected asset body")
			}
			if strings.Contains(strings.ToLower(body), "<html") {
				t.Fatalf("expected asset body, got html fallback")
			}
		})
		checked++
	}
	if checked == 0 {
		t.Fatal("expected at least one embedded asset")
	}
}

func TestHandlerReturnsNotFoundForMissingAssets(t *testing.T) {
	handler := Handler()

	req := httptest.NewRequest(http.MethodGet, "/assets/missing.css", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing asset, got %d body=%q", rec.Code, rec.Body.String())
	}
}
