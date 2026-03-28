package inprocessserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
)

type Server struct {
	URL string

	client    *http.Client
	closeOnce sync.Once
	closeFn   func()
}

type transport struct {
	fallback http.RoundTripper

	mu       sync.RWMutex
	handlers map[string]http.Handler
}

var sharedTransport = newTransport(http.DefaultTransport)

var nextPort atomic.Uint32

func New(handler http.Handler) (*Server, error) {
	return NewWithURL(nextURL(), handler)
}

func NewWithURL(rawURL string, handler http.Handler) (*Server, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	key, err := transportKey(parsed)
	if err != nil {
		return nil, err
	}

	ensureTransportInstalled()
	if err := sharedTransport.register(key, handler); err != nil {
		return nil, err
	}

	srv := &Server{
		URL:    rawURL,
		client: &http.Client{Transport: sharedTransport},
		closeFn: func() {
			sharedTransport.unregister(key)
		},
	}
	return srv, nil
}

func (s *Server) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		if s.closeFn != nil {
			s.closeFn()
		}
	})
}

func (s *Server) Client() *http.Client {
	if s == nil {
		return nil
	}
	return s.client
}

func ensureTransportInstalled() {
	if sharedTransport == nil {
		sharedTransport = newTransport(http.DefaultTransport)
	}
	http.DefaultTransport = sharedTransport
}

func nextURL() string {
	port := 25000 + int(nextPort.Add(1))
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func newTransport(fallback http.RoundTripper) *transport {
	if fallback == nil {
		fallback = http.DefaultTransport
	}
	return &transport{
		fallback: fallback,
		handlers: map[string]http.Handler{},
	}
}

func (t *transport) register(rawURL string, handler http.Handler) error {
	if handler == nil {
		return fmt.Errorf("handler is required")
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.handlers[rawURL]; ok {
		return fmt.Errorf("server already registered for %s", rawURL)
	}
	t.handlers[rawURL] = handler
	return nil
}

func (t *transport) unregister(rawURL string) {
	t.mu.Lock()
	delete(t.handlers, rawURL)
	t.mu.Unlock()
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("request URL is required")
	}

	key, err := transportKey(req.URL)
	if err != nil {
		return nil, err
	}

	t.mu.RLock()
	handler := t.handlers[key]
	t.mu.RUnlock()
	if handler == nil {
		return t.fallback.RoundTrip(req)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req.Clone(req.Context()))
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

func (s *Server) String() string {
	if s == nil {
		return ""
	}
	return s.URL
}

func (t *transport) CloseIdleConnections() {
	if closer, ok := t.fallback.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func transportKey(u *url.URL) (string, error) {
	if u == nil {
		return "", fmt.Errorf("request URL is required")
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("URL must include scheme and host")
	}
	return u.Scheme + "://" + u.Host, nil
}
