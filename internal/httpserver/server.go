package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/dashboardapi"
	"github.com/olhapi/maestro/internal/dashboardui"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
)

type Server struct {
	http *http.Server
}

func Start(ctx context.Context, addr string, store *kanban.Store, provider dashboardapi.Provider) *Server {
	mux := http.NewServeMux()
	dashboardapi.NewServer(store, provider).Register(mux)
	observability.RegisterRoutes(mux, provider)

	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dashboard" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/dashboard/", func(w http.ResponseWriter, r *http.Request) {
		target := strings.TrimPrefix(r.URL.Path, "/dashboard")
		if target == "" {
			target = "/"
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	})
	mux.Handle("/", dashboardui.Handler())

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	go func() {
		slog.Info("HTTP API started", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP API failed", "error", err)
		}
	}()

	return &Server{http: srv}
}
