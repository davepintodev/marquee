package fake

import (
	"net/http"
	"os"
	"path/filepath"
)

// Title is what the art downloader needs to know about a fixture title.
type Title struct {
	ID     string
	Kind   string
	Name   string
	TmdbID int
}

// Titles lists the fixture catalog's titles.
func Titles() []Title {
	var entries []*entry
	load("catalog.json", &entries)
	out := make([]Title, len(entries))
	for i, e := range entries {
		out[i] = Title{ID: e.id(), Kind: e.Kind, Name: e.Title, TmdbID: e.TmdbID}
	}
	return out
}

func ArtFile(dir, kind, titleID string) string {
	return filepath.Join(dir, kind+"-"+titleID+".jpg")
}

func (f *Fake) serveArt(w http.ResponseWriter, r *http.Request, kind, titleID string) bool {
	if f.opts.ArtDir == "" {
		return false
	}
	file, err := os.Open(ArtFile(f.opts.ArtDir, kind, titleID))
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, "", info.ModTime(), file)
	return true
}
