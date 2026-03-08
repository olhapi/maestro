package dashboardui

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed dist/*
var embedded embed.FS

func Handler() http.Handler {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if cleanPath == "" || cleanPath == "." {
			serveIndex(dist, w, r)
			return
		}
		if _, err := fs.Stat(dist, cleanPath); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(dist, w, r)
	})
}

func serveIndex(dist fs.FS, w http.ResponseWriter, r *http.Request) {
	f, err := dist.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", zeroTime, f.(interface {
		io.ReadSeeker
	}))
}

var zeroTime time.Time
