package observability

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// StatusProvider describes a source of runtime status.
type StatusProvider interface {
	Status() map[string]interface{}
}

type SessionProvider interface {
	LiveSessions() map[string]interface{}
}

// Server is a lightweight observability HTTP API.
type Server struct {
	http *http.Server
}

// Start launches an HTTP server exposing runtime status endpoints.
func Start(ctx context.Context, addr string, provider StatusProvider) *Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "ts": time.Now().UTC().Format(time.RFC3339)})
	})

	mux.HandleFunc("/api/v1/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(provider.Status())
	})

	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if sp, ok := provider.(SessionProvider); ok {
			_ = json.NewEncoder(w).Encode(sp.LiveSessions())
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"sessions": map[string]interface{}{}})
	})

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	go func() {
		slog.Info("Observability API started", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Observability API failed", "error", err)
		}
	}()

	return &Server{http: srv}
}
