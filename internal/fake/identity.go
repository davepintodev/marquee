package fake

import (
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var episodeTokenRe = regexp.MustCompile(`[Ss]([0-9]{1,3})[Ee]([0-9]{1,3})`)

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

// fileIdentity knows a downloaded fixture by its folder, "<Title> (<Year>)",
// and for a series by the first SxxEyy in the file name.
func (f *Fake) fileIdentity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, ok := map[string]string{"movies": "movie", "series": "series"}[q.Get("library")]
	if !ok {
		fail(w, http.StatusBadRequest, "library must be series or movies")
		return
	}
	p := q.Get("path")
	c := path.Clean(p)
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		fail(w, http.StatusBadRequest, "path must be a file's path relative to the library root")
		return
	}
	folder, _, _ := strings.Cut(c, "/")
	for _, t := range f.titles {
		if !t.InLibrary || t.Kind != kind || folder != fmt.Sprintf("%s (%d)", t.Title, t.Year) {
			continue
		}
		out := fileIdentity{Kind: "movie", TMDBID: t.TmdbID, Title: t.Title, Year: t.Year, OriginalLanguage: "en", AlternativeTitles: []string{}}
		if kind == "movie" {
			if t.State == "downloaded" {
				write(w, http.StatusOK, out)
				return
			}
			break
		}
		m := episodeTokenRe.FindStringSubmatch(path.Base(c))
		if m == nil {
			break
		}
		sn, _ := strconv.Atoi(m[1])
		en, _ := strconv.Atoi(m[2])
		for _, s := range t.Seasons {
			for _, e := range s.Episodes {
				if s.Number == sn && e.Number == en && e.State == "downloaded" {
					out.Kind, out.Season, out.Episode, out.EpisodeTitle = "episode", &s.Number, &e.Number, &e.Name
					write(w, http.StatusOK, out)
					return
				}
			}
		}
	}
	fail(w, http.StatusNotFound, "no movie or episode in the catalog has this file")
}
