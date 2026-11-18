// Package server is marquee's HTTP surface: /api and the embedded UI.
package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// New serves /api and, for every other path, the UI files in ui with
// index.html as the fallback for client-side routes.
func New(ui fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	mux.Handle("/", uiHandler(ui))
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
