package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// WebFS contains the embedded single-page dashboard assets.
//
//go:embed index.html static/*
var WebFS embed.FS

// FS is an alias to WebFS.
var FS = WebFS

// Handler returns an http.Handler that serves the embedded web dashboard.
// It serves static assets from the static/ directory and delivers index.html
// for root and unknown paths (single-page application routing).
func Handler() http.Handler {
	fileServer := http.FileServer(http.FS(WebFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlPath := strings.TrimPrefix(r.URL.Path, "/")
		if urlPath == "" || urlPath == "index.html" {
			serveIndex(w)
			return
		}

		// Check if the requested file exists in embedded FS
		f, err := WebFS.Open(urlPath)
		if err == nil {
			stat, statErr := f.Stat()
			_ = f.Close()
			if statErr == nil && !stat.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// Fallback to index.html for SPA routes
		serveIndex(w)
	})
}

// GetFileSystem returns an http.FileSystem backed by the embedded assets.
func GetFileSystem() http.FileSystem {
	return http.FS(WebFS)
}

// SubFS returns a sub-filesystem rooted at the given directory.
func SubFS(dir string) (fs.FS, error) {
	return fs.Sub(WebFS, dir)
}

func serveIndex(w http.ResponseWriter) {
	data, err := WebFS.ReadFile("index.html")
	if err != nil {
		http.Error(w, "Dashboard index.html not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
