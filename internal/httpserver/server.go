package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/olhapi/symphony-go/internal/dashboardapi"
	"github.com/olhapi/symphony-go/internal/dashboardui"
	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/internal/observability"
)

type Server struct {
	http *http.Server
}

func Start(ctx context.Context, addr string, store *kanban.Store, provider dashboardapi.Provider) *Server {
	mux := http.NewServeMux()
	dashboardapi.NewServer(store, provider).Register(mux)
	observability.RegisterRoutes(mux, provider)

	dashboardHandler := http.StripPrefix("/dashboard/", dashboardui.Handler())
	mux.Handle("/dashboard/", dashboardHandler)
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dashboard" {
			http.NotFound(w, r)
			return
		}
		dashboardui.Handler().ServeHTTP(w, r)
	})

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
