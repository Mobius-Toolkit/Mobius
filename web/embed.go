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
func Handler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(fsys, name); err != nil || info.IsDir() {
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		files.ServeHTTP(w, r)
	})
}
