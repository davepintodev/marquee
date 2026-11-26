package httpapi

import (
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/metadata"
)

// IdentityPath is the one route the lookup token opens.
const IdentityPath = "/api/files/identity"

type fileIdentity struct {
	Kind              string   `json:"kind"`
	IMDbID            string   `json:"imdbId"`
	TMDBID            int      `json:"tmdbId"`
	Title             string   `json:"title"`
	Year              int      `json:"year"`
	Season            *int     `json:"season"`
	Episode           *int     `json:"episode"`
	EpisodeTitle      *string  `json:"episodeTitle"`
	OriginalLanguage  string   `json:"originalLanguage"`
	AlternativeTitles []string `json:"alternativeTitles"`
}

var libraryKinds = map[string]string{"movies": "movie", "series": "series"}

func (a *api) fileIdentity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, ok := libraryKinds[q.Get("library")]
	if !ok {
		fail(w, http.StatusBadRequest, "library must be series or movies")
		return
	}
	rel, ok := insideLibrary(q.Get("path"))
	if !ok {
		fail(w, http.StatusBadRequest, "path must be a file's path relative to the library root")
		return
	}
	f, err := a.Catalog.IdentifyFile(r.Context(), kind, rel)
	if errors.Is(err, catalog.ErrNotFound) {
		fail(w, http.StatusNotFound, "no movie or episode in the catalog has this file")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if f.Names == nil {
		if f.Names, err = a.fetchNames(r, f); err != nil {
			a.namesFailed(w, r, err)
			return
		}
	}
	out := fileIdentity{
		Kind: "movie", IMDbID: f.IMDbID, TMDBID: f.TMDBID, Title: f.Title, Year: f.Year,
		OriginalLanguage: f.Names.OriginalLanguage, AlternativeTitles: f.Names.AlternativeTitles,
	}
	if f.Kind == "series" {
		out.Kind, out.Season, out.Episode, out.EpisodeTitle = "episode", &f.Season, &f.Number, &f.EpisodeTitle
	}
	if out.AlternativeTitles == nil {
		out.AlternativeTitles = []string{}
	}
	writeJSON(w, http.StatusOK, out)
}

// insideLibrary cleans a library-relative path and refuses one that is
// empty, absolute, the root itself or climbs out of it.
func insideLibrary(p string) (string, bool) {
	if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") {
		return "", false
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	return c, true
}

// fetchNames asks TMDB for the title's names once, however many lookups of
// its files arrive together, and keeps them.
func (a *api) fetchNames(r *http.Request, f catalog.FileIdentity) (*catalog.TitleNames, error) {
	unlock := a.names.Lock(f.TitleID)
	defer unlock()
	if kept, err := a.Catalog.TitleNamesOf(r.Context(), f.TitleID); err != nil || kept != nil {
		return kept, err
	}
	n, err := a.TMDB.Names(r.Context(), f.Kind, f.TMDBID, f.Title)
	if err != nil {
		return nil, err
	}
	names := &catalog.TitleNames{OriginalLanguage: n.OriginalLanguage, AlternativeTitles: n.AlternativeTitles}
	return names, a.Catalog.SetTitleNames(r.Context(), f.TitleID, *names)
}

func (a *api) namesFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		fail(w, http.StatusNotFound, "no movie or episode in the catalog has this file")
	case errors.Is(err, metadata.ErrRateLimited):
		a.Log.Warn("tmdb is rate limiting marquee", "path", r.URL.Path)
		fail(w, http.StatusServiceUnavailable, "TMDB is rate-limiting marquee; try again in a minute")
	case errors.Is(err, metadata.ErrRejected):
		a.Log.Error("tmdb refused the API key", "path", r.URL.Path)
		fail(w, http.StatusServiceUnavailable, "TMDB refused marquee's API key; check the TMDB key setting")
	case errors.Is(err, metadata.ErrNotFound), errors.Is(err, metadata.ErrUnavailable), r.Context().Err() != nil:
		a.Log.Warn("tmdb did not give a title's names", "path", r.URL.Path, "err", err)
		fail(w, http.StatusServiceUnavailable, "TMDB is not answering; try again shortly")
	default:
		a.internal(w, r, err)
	}
}
