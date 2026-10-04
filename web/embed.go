// Package web holds the built UI and serves it.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Dist holds the files of the UI below the "dist" directory.
//
//go:embed all:dist
var Dist embed.FS

// Handler serves the files of fsys. A path that is not a file gets index.html,
// so that the UI router can handle it.
//
// index.html, the service worker, the web app manifest and ui-version get
// "Cache-Control: no-cache", so the browser always asks for the current version.
// ui-version holds the build identifier of the UI. Vite writes it.
func Handler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(fsys, name); err != nil || info.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		switch name {
		case "sw.js", "ui-version":
			w.Header().Set("Cache-Control", "no-cache")
		case "manifest.webmanifest":
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		files.ServeHTTP(w, r)
	})
}
