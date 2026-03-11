package httpserver

import (
	"context"
	"log/slog"
	"net"
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

func newHandler(store *kanban.Store, provider dashboardapi.Provider) http.Handler {
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
	return mux
}

func Start(ctx context.Context, addr string, store *kanban.Store, provider dashboardapi.Provider) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Addr: addr, Handler: newHandler(store, provider)}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	go func() {
		slog.Info("HTTP API started", "addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP API failed", "error", err)
		}
	}()

	return &Server{http: srv}, nil
}
