// Package server is marquee's HTTP surface: /api and the embedded UI.
package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// New serves api for every /api/ request and, for every other path, the
// UI files in ui with index.html as the fallback for client-side routes.
func New(ui fs.FS, api http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	mux.Handle("/", uiHandler(ui))
	return mux
}

func uiHandler(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "."
		}
		if st, err := fs.Stat(ui, name); err == nil && !st.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(ui, "index.html"); err != nil {
			http.Error(w, "web UI is not built", http.StatusNotFound)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		files.ServeHTTP(w, r2)
	})
}
