package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/metadata"
)

// Source is the part of the AIOStreams client the API uses.
type Source interface {
	Search(ctx context.Context, typ, id string) ([]aiostreams.Stream, error)
}

// searchTarget is what a release search needs to know about a title.
type searchTarget struct {
	kind             string
	tmdbID           int
	imdbID           string
	tvdbID           int
	preferredQuality string
	hasEpisode       func(season, number int) bool
}

// target describes the title from the library, or, for one marquee does
// not have, from TMDB: listing releases never adds a title.
func (a *api) target(r *http.Request, id, kind string, tmdbID int) (searchTarget, error) {
	t, err := a.Catalog.Get(r.Context(), id)
	if err == nil {
		return searchTarget{
			kind: kind, tmdbID: tmdbID, imdbID: t.IMDbID, tvdbID: t.TVDBID, preferredQuality: t.PreferredQuality,
			hasEpisode: func(season, number int) bool {
				for _, s := range t.Seasons {
					for _, e := range s.Episodes {
						if s.Number == season && e.Number == number {
							return true
						}
					}
				}
				return false
			},
		}, nil
	}
	if !errors.Is(err, catalog.ErrNotFound) {
		return searchTarget{}, err
	}
	d, err := a.TMDB.Details(r.Context(), kind, tmdbID)
	if err != nil {
		return searchTarget{}, err
	}
	return searchTarget{
		kind: kind, tmdbID: tmdbID, imdbID: d.IMDbID, tvdbID: d.TVDBID,
		hasEpisode: func(season, number int) bool {
			for _, s := range d.Seasons {
				for _, e := range s.Episodes {
					if s.Number == season && e.Number == number {
						return true
					}
				}
			}
			return false
		},
	}, nil
}

func (t searchTarget) sourceID(season, episode int) (string, bool) {
	if t.kind == "movie" {
		return aiostreams.MovieID(t.imdbID, t.tmdbID), true
	}
	return aiostreams.EpisodeID(t.tvdbID, t.imdbID, season, episode)
}

type releaseJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Quality   string `json:"quality"`
	SizeBytes int64  `json:"sizeBytes"`
	Source    string `json:"source"`
	Cached    bool   `json:"cached"`
}

type droppedJSON struct {
	Release releaseJSON `json:"release"`
	Reason  string      `json:"reason"`
	Message string      `json:"message"`
}

// releaseOf never includes the playback URL, which carries credentials.
func releaseOf(c chooser.Candidate) releaseJSON {
	s := c.Stream
	name := s.Filename
	if name == "" {
		name = s.FolderName
	}
	source := s.Addon
	if source == "" {
		source = s.Indexer
	}
	return releaseJSON{ID: c.ID, Name: name, Quality: qualityOf(s.ParsedFile), SizeBytes: max(s.Size, 0), Source: source, Cached: s.Cached}
}

// qualityOf is the AIOStreams quality and resolution joined, such as
// "BluRay-1080p". The upstream names Sonarr and Radarr use come with the
// importer.
func qualityOf(p *aiostreams.ParsedFile) string {
	if p == nil {
		return "Unknown"
	}
	var parts []string
	for _, v := range []string{p.Quality, p.Resolution} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return "Unknown"
	}
	return strings.Join(parts, "-")
}

func (a *api) listReleases(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kind, tmdbID, ok := parseTitleID(id)
	if !ok {
		fail(w, http.StatusNotFound, "no such title")
		return
	}
	var season, episode int
	if kind == "series" {
		m := episodeIDRe.FindStringSubmatch(r.URL.Query().Get("episode"))
		if m == nil {
			fail(w, http.StatusBadRequest, "episode must name an episode of this series, such as s01e02")
			return
		}
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
	}
	t, err := a.target(r, id, kind, tmdbID)
	if err != nil {
		if isTMDBError(err) || r.Context().Err() != nil {
			a.upstream(w, r, err)
		} else {
			a.internal(w, r, err)
		}
		return
	}
	if kind == "series" && !t.hasEpisode(season, episode) {
		fail(w, http.StatusBadRequest, "episode must name an episode of this series, such as s01e02")
		return
	}
	sourceID, ok := t.sourceID(season, episode)
	if !ok {
		fail(w, http.StatusUnprocessableEntity, "TMDB lists neither a TVDB nor an IMDb id for this series, so AIOStreams cannot be asked about it")
		return
	}
	streams, err := a.Source.Search(r.Context(), kind, sourceID)
	if err != nil {
		a.sourceFailure(w, r, err)
		return
	}
	blocked, err := a.Catalog.Blocklist(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	res := chooser.Choose(streams, chooser.Context{
		Kind: kind, Season: season, Episode: episode, PreferredQuality: t.preferredQuality,
		Blocklist: blocked, Now: a.Now(),
	})
	out := struct {
		Candidates []releaseJSON `json:"candidates"`
		Dropped    []droppedJSON `json:"dropped"`
	}{Candidates: []releaseJSON{}, Dropped: []droppedJSON{}}
	for _, c := range res.Candidates {
		out.Candidates = append(out.Candidates, releaseOf(c))
	}
	for _, d := range res.Dropped {
		out.Dropped = append(out.Dropped, droppedJSON{Release: releaseOf(d.Candidate), Reason: string(d.Reason), Message: d.Message})
	}
	writeJSON(w, http.StatusOK, out)
}

func isTMDBError(err error) bool {
	for _, e := range []error{metadata.ErrNotFound, metadata.ErrRateLimited, metadata.ErrRejected, metadata.ErrUnavailable} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// sourceFailure answers a failed AIOStreams search. The error text of
// aiostreams never holds the instance's address or credentials.
func (a *api) sourceFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, aiostreams.ErrRateLimited):
		a.Log.Warn("aiostreams is rate limiting marquee", "path", r.URL.Path)
		fail(w, http.StatusServiceUnavailable, "AIOStreams is rate-limiting marquee; try again in a minute")
	case errors.Is(err, aiostreams.ErrRejected):
		a.Log.Error("aiostreams refused the credentials", "path", r.URL.Path)
		fail(w, http.StatusBadGateway, "AIOStreams refused marquee's credentials; check the AIOStreams manifest URL setting")
	case r.Context().Err() != nil:
		fail(w, http.StatusBadGateway, "request cancelled")
	default:
		a.Log.Warn("aiostreams is not answering", "path", r.URL.Path, "err", err)
		fail(w, http.StatusBadGateway, "AIOStreams is not answering; try again shortly")
	}
}
