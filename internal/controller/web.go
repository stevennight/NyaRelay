package controller

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"strings"
)

//go:embed webdist/*
var embeddedWeb embed.FS

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.ErrNotSupported, http.StatusNotFound)
		return
	}
	if dist, ok := localWebDist(); ok {
		serveWebFS(w, r, dist)
		return
	}
	dist, err := fs.Sub(embeddedWeb, "webdist")
	if err == nil {
		serveWebFS(w, r, dist)
		return
	}
	s.handleFallbackWeb(w, r)
}

func localWebDist() (fs.FS, bool) {
	if _, err := os.Stat(".tmp-webdist/index.html"); err != nil {
		return nil, false
	}
	return os.DirFS(".tmp-webdist"), true
}

func serveWebFS(w http.ResponseWriter, r *http.Request, dist fs.FS) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(dist, path); err != nil {
		path = "index.html"
	}
	http.ServeFileFS(w, r, dist, path)
}
