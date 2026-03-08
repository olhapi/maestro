package observability

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// StatusProvider describes a source of runtime status.
type StatusProvider interface {
	Status() map[string]interface{}
}

type EventProvider interface {
	Events(since int64, limit int) map[string]interface{}
}

type SessionProvider interface {
	LiveSessions() map[string]interface{}
}

// Server is a lightweight observability HTTP API.
type Server struct {
	http *http.Server
}

func RegisterRoutes(mux *http.ServeMux, provider StatusProvider) {
	if mux == nil {
		return
	}

	// Start launches an HTTP server exposing runtime status endpoints.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "ts": time.Now().UTC().Format(time.RFC3339)})
	})

	mux.HandleFunc("/api/v1/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if sp, ok := provider.(SnapshotProvider); ok {
			_ = json.NewEncoder(w).Encode(StatePayload(sp))
			return
		}
		_ = json.NewEncoder(w).Encode(provider.Status())
	})

	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if sp, ok := provider.(SessionProvider); ok {
			all := sp.LiveSessions()
			issue := r.URL.Query().Get("issue")
			if issue == "" {
				_ = json.NewEncoder(w).Encode(all)
				return
			}
			if sessions, ok := all["sessions"].(map[string]interface{}); ok {
				if one, ok := sessions[issue]; ok {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"issue": issue, "session": one})
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "session_not_found", "issue": issue})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"sessions": map[string]interface{}{}})
	})

	mux.HandleFunc("/api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ep, ok := provider.(EventProvider)
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"since": 0, "last_seq": 0, "events": []interface{}{}})
			return
		}
		var since int64
		if raw := r.URL.Query().Get("since"); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
				since = v
			}
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil {
				limit = v
			}
		}
		_ = json.NewEncoder(w).Encode(ep.Events(since, limit))
	})

	mux.HandleFunc("/api/v1/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "method_not_allowed"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if rp, ok := provider.(RefreshProvider); ok {
			_ = json.NewEncoder(w).Encode(RefreshPayload(rp))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"requested_at": time.Now().UTC().Format(time.RFC3339)})
	})

	mux.HandleFunc("/api/v1/dashboard", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		out := map[string]interface{}{
			"state": provider.Status(),
		}
		if sp, ok := provider.(SessionProvider); ok {
			out["sessions"] = sp.LiveSessions()
		}
		if ep, ok := provider.(EventProvider); ok {
			out["events"] = ep.Events(0, 25)
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "method_not_allowed"})
			return
		}
		identifier := strings.TrimPrefix(r.URL.Path, "/api/v1/")
		if identifier == "" || strings.Contains(identifier, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if sp, ok := provider.(SnapshotProvider); ok {
			payload, found := IssuePayload(sp, identifier)
			if !found {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "issue_not_found"})
				return
			}
			_ = json.NewEncoder(w).Encode(payload)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "issue_not_found"})
	})
}

// Start launches an HTTP server exposing runtime status endpoints.
func Start(ctx context.Context, addr string, provider StatusProvider) *Server {
	mux := http.NewServeMux()
	RegisterRoutes(mux, provider)

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
