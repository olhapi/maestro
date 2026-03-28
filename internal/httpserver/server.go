package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/dashboardapi"
	"github.com/olhapi/maestro/internal/dashboardui"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/internal/testutil/inprocessserver"
)

type Server struct {
	http         *http.Server
	listenerAddr net.Addr
	baseURL      *string
}

const uiDevProxyEnv = "MAESTRO_UI_DEV_PROXY_URL"
const inProcessServerEnv = "MAESTRO_HTTPSERVER_INPROCESS"

var listenTCP = net.Listen
var serveHTTP = func(srv *http.Server, ln net.Listener) error {
	return srv.Serve(ln)
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
	mux.Handle("/", dashboardHandler())
	return mux
}

func dashboardHandler() http.Handler {
	embedded := dashboardui.Handler()
	rawURL := strings.TrimSpace(os.Getenv(uiDevProxyEnv))
	if rawURL == "" {
		return embedded
	}

	handler, err := newDashboardDevProxy(rawURL, embedded)
	if err != nil {
		slog.Warn("Dashboard dev proxy disabled; falling back to embedded UI", "env", uiDevProxyEnv, "value", rawURL, "error", err)
		return embedded
	}

	slog.Info("Dashboard UI proxy enabled", "target", rawURL)
	return handler
}

func newDashboardDevProxy(rawURL string, fallback http.Handler) (http.Handler, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("expected absolute URL, got %q", rawURL)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		originalDirector(r)
		r.Host = target.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Warn("Dashboard dev proxy request failed; falling back to embedded UI", "target", target.String(), "path", r.URL.Path, "error", err)
		fallback.ServeHTTP(w, r)
	}
	return proxy, nil
}

func Start(ctx context.Context, addr string, store *kanban.Store, provider dashboardapi.Provider) (*Server, error) {
	if strings.TrimSpace(os.Getenv(inProcessServerEnv)) != "" {
		return startInProcess(ctx, addr, store, provider)
	}

	ln, err := listenTCP("tcp", addr)
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
		if err := serveHTTP(srv, ln); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP API failed", "error", err)
		}
	}()

	baseURL := baseURLForAddr(ln.Addr())
	return &Server{http: srv, listenerAddr: ln.Addr(), baseURL: &baseURL}, nil
}

func startInProcess(ctx context.Context, addr string, store *kanban.Store, provider dashboardapi.Provider) (*Server, error) {
	resolved, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}

	handler := newHandler(store, provider)
	internalURL := baseURLForAddr(resolved)
	helperServer, err := inprocessserver.NewWithURL(internalURL, handler)
	if err != nil {
		return nil, err
	}

	go func() {
		<-ctx.Done()
		helperServer.Close()
	}()

	slog.Info("HTTP API started", "addr", resolved.String())
	baseURL := ""
	return &Server{listenerAddr: resolved, baseURL: &baseURL}, nil
}

func (s *Server) BaseURL() string {
	if s == nil {
		return ""
	}
	if s.baseURL != nil {
		return *s.baseURL
	}
	return baseURLForAddr(s.listenerAddr)
}

func baseURLForAddr(addr net.Addr) string {
	if addr == nil {
		return ""
	}

	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		host := normalizeBaseURLHost("")
		if tcpAddr.IP != nil && len(tcpAddr.IP) > 0 {
			host = normalizeBaseURLHost(tcpAddr.IP.String())
		}
		return "http://" + net.JoinHostPort(host, strconv.Itoa(tcpAddr.Port))
	}

	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return ""
	}
	return "http://" + net.JoinHostPort(normalizeBaseURLHost(host), port)
}

func normalizeBaseURLHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "127.0.0.1"
	}

	ip := net.ParseIP(host)
	if ip != nil && ip.IsUnspecified() {
		if ip.To4() == nil {
			return "::1"
		}
		return "127.0.0.1"
	}
	return host
}
