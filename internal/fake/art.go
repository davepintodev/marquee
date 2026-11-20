package fake

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", info.ModTime(), file)
	return true
}

// Cast is a fixture cast member and the title the fixtures list them under.
type Cast struct {
	TitleID  string
	Kind     string
	TmdbID   int
	PersonID int
	Name     string
}

// CastList lists every cast member of the fixture titles, in billing order.
func CastList() []Cast {
	var about map[string]aboutEntry
	load("about.json", &about)
	var out []Cast
	for _, t := range Titles() {
		for _, c := range about[t.ID].Cast {
			out = append(out, Cast{TitleID: t.ID, Kind: t.Kind, TmdbID: t.TmdbID, PersonID: c.PersonID, Name: c.Name})
		}
	}
	return out
}

// ProfileFile names the photo of a cast member inside the art folder.
func ProfileFile(dir string, personID int) string {
	return filepath.Join(dir, "profile-"+strconv.Itoa(personID)+".jpg")
}
