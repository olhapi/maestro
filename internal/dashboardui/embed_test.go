package dashboardui

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
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

func TestHandlerServesManifestMetadataOnIndex(t *testing.T) {
	handler := Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := strings.Join(strings.Fields(rec.Body.String()), " ")
	checks := []string{
		`name="description"`,
		`content="Local control center for supervising Maestro work, sessions, retries, and queue state."`,
		`name="theme-color" content="#08090c"`,
		`rel="manifest" href="/manifest.webmanifest"`,
		`rel="icon" type="image/svg+xml" href="/favicon.svg"`,
		`rel="icon" type="image/png" sizes="32x32" href="/favicon-32x32.png"`,
		`rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png"`,
		`name="apple-mobile-web-app-title" content="Maestro"`,
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Fatalf("expected index html to include %q", check)
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

func TestHandlerServesRootAssetsWithoutSPAFallback(t *testing.T) {
	handler := Handler()

	assets := []struct {
		path        string
		contentType string
	}{
		{path: "/manifest.webmanifest"},
		{path: "/favicon.svg", contentType: "image/svg+xml"},
		{path: "/favicon-32x32.png", contentType: "image/png"},
		{path: "/apple-touch-icon.png", contentType: "image/png"},
		{path: "/icon-192.png", contentType: "image/png"},
		{path: "/icon-512.png", contentType: "image/png"},
	}

	for _, asset := range assets {
		t.Run(asset.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, asset.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			contentType := rec.Header().Get("Content-Type")
			if asset.contentType != "" && !strings.Contains(contentType, asset.contentType) {
				t.Fatalf("expected %q content type, got %q", asset.contentType, contentType)
			}
			body := rec.Body.String()
			if body == "" {
				t.Fatal("expected asset body")
			}
			if strings.Contains(strings.ToLower(body), "<html") {
				t.Fatalf("expected asset body, got html fallback")
			}
		})
	}
}

func TestHandlerServesManifestContents(t *testing.T) {
	handler := Handler()
	req := httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var manifest struct {
		Name            string `json:"name"`
		ShortName       string `json:"short_name"`
		Description     string `json:"description"`
		ID              string `json:"id"`
		StartURL        string `json:"start_url"`
		Scope           string `json:"scope"`
		Display         string `json:"display"`
		BackgroundColor string `json:"background_color"`
		ThemeColor      string `json:"theme_color"`
		Icons           []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	if manifest.Name != "Maestro Control Center" {
		t.Fatalf("expected manifest name, got %q", manifest.Name)
	}
	if manifest.ShortName != "Maestro" {
		t.Fatalf("expected manifest short name, got %q", manifest.ShortName)
	}
	if manifest.Description != "Local control center for supervising Maestro work, sessions, retries, and queue state." {
		t.Fatalf("unexpected manifest description: %q", manifest.Description)
	}
	if manifest.ID != "/" || manifest.StartURL != "/" || manifest.Scope != "/" {
		t.Fatalf("unexpected manifest routing: id=%q start_url=%q scope=%q", manifest.ID, manifest.StartURL, manifest.Scope)
	}
	if manifest.Display != "standalone" {
		t.Fatalf("expected standalone display, got %q", manifest.Display)
	}
	if manifest.BackgroundColor != "#08090c" || manifest.ThemeColor != "#08090c" {
		t.Fatalf("unexpected manifest colors: background=%q theme=%q", manifest.BackgroundColor, manifest.ThemeColor)
	}
	if len(manifest.Icons) != 2 {
		t.Fatalf("expected 2 manifest icons, got %d", len(manifest.Icons))
	}

	expectedIcons := []struct {
		src   string
		sizes string
	}{
		{src: "/icon-192.png", sizes: "192x192"},
		{src: "/icon-512.png", sizes: "512x512"},
	}
	for i, expected := range expectedIcons {
		icon := manifest.Icons[i]
		if icon.Src != expected.src || icon.Sizes != expected.sizes {
			t.Fatalf("unexpected icon %d: %+v", i, icon)
		}
		if icon.Type != "image/png" {
			t.Fatalf("unexpected icon type for %s: %q", icon.Src, icon.Type)
		}
		if icon.Purpose != "any" {
			t.Fatalf("unexpected icon purpose for %s: %q", icon.Src, icon.Purpose)
		}
	}
}

func TestServeIndexReturnsNotFoundWhenIndexMissing(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	serveIndex(fstest.MapFS{}, rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when index is missing, got %d", rec.Code)
	}
}
