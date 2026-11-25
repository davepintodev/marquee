package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/metadata"
	"github.com/davepintodev/marquee/internal/pipeline"
)

// Source is the part of the AIOStreams client the API uses.
type Source interface {
	Search(ctx context.Context, typ, id string) ([]aiostreams.Stream, error)
}

// searchTarget is what a release search needs to know about a title.
type searchTarget struct {
	kind       string
	tmdbID     int
	imdbID     string
	tvdbID     int
	profile    config.Profile
	hasEpisode func(season, number int) bool
}

// target describes the title from the library, or, for one marquee does
// not have, from TMDB: listing releases never adds a title.
func (a *api) target(r *http.Request, id, kind string, tmdbID int) (searchTarget, error) {
	t, err := a.Catalog.Get(r.Context(), id)
	if err == nil {
		profile, _ := a.profileFor(t)
		return searchTarget{
			kind: kind, tmdbID: tmdbID, imdbID: t.IMDbID, tvdbID: t.TVDBID, profile: profile,
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
	profile, _, _ := a.Profiles.Resolve(kind, "")
	return searchTarget{
		kind: kind, tmdbID: tmdbID, imdbID: d.IMDbID, tvdbID: d.TVDBID, profile: profile,
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
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Quality   string   `json:"quality"`
	SizeBytes int64    `json:"sizeBytes"`
	Source    string   `json:"source"`
	Cached    bool     `json:"cached"`
	Tags      []string `json:"tags"`
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
	return releaseJSON{
		ID: c.ID, Name: name, Quality: qualityOf(s.ParsedFile), SizeBytes: max(s.Size, 0), Source: source, Cached: s.Cached,
		Tags: nonNil(chooser.ExtractTags(s)),
	}
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

// releaseSearch is a title's releases as the chooser sees them, and what
// they were searched for.
type releaseSearch struct {
	kind            string
	tmdbID          int
	season, episode int
	result          chooser.Result
}

// searchReleases finds and chooses the releases for a title, or for one of
// its episodes, and answers the error itself, returning false, when it cannot.
// With ignoreBlocklist a release that failed before is a candidate again.
func (a *api) searchReleases(w http.ResponseWriter, r *http.Request, id, episodeID string, ignoreBlocklist bool) (releaseSearch, bool) {
	kind, tmdbID, ok := parseTitleID(id)
	if !ok {
		fail(w, http.StatusNotFound, "no such title")
		return releaseSearch{}, false
	}
	out := releaseSearch{kind: kind, tmdbID: tmdbID}
	if kind == "series" {
		m := episodeIDRe.FindStringSubmatch(episodeID)
		if m == nil {
			fail(w, http.StatusBadRequest, "episode must name an episode of this series, such as s01e02")
			return releaseSearch{}, false
		}
		out.season, _ = strconv.Atoi(m[1])
		out.episode, _ = strconv.Atoi(m[2])
	}
	t, err := a.target(r, id, kind, tmdbID)
	if err != nil {
		if isTMDBError(err) || r.Context().Err() != nil {
			a.upstream(w, r, err)
		} else {
			a.internal(w, r, err)
		}
		return releaseSearch{}, false
	}
	if kind == "series" && !t.hasEpisode(out.season, out.episode) {
		fail(w, http.StatusBadRequest, "episode must name an episode of this series, such as s01e02")
		return releaseSearch{}, false
	}
	sourceID, ok := t.sourceID(out.season, out.episode)
	if !ok {
		fail(w, http.StatusUnprocessableEntity, "TMDB lists neither a TVDB nor an IMDb id for this series, so AIOStreams cannot be asked about it")
		return releaseSearch{}, false
	}
	streams, err := a.Source.Search(r.Context(), kind, sourceID)
	if err != nil {
		a.sourceFailure(w, r, err)
		return releaseSearch{}, false
	}
	var blocked map[string]time.Time
	if !ignoreBlocklist {
		if blocked, err = a.Catalog.Blocklist(r.Context()); err != nil {
			a.internal(w, r, err)
			return releaseSearch{}, false
		}
	}
	out.result = chooser.Choose(streams, chooser.Context{
		Kind: kind, Season: out.season, Episode: out.episode, Profile: t.profile,
		Blocklist: blocked, Now: a.Now(),
	})
	return out, true
}

func (a *api) listReleases(w http.ResponseWriter, r *http.Request) {
	found, ok := a.searchReleases(w, r, r.PathValue("id"), r.URL.Query().Get("episode"), false)
	if !ok {
		return
	}
	out := struct {
		Candidates []releaseJSON `json:"candidates"`
		Dropped    []droppedJSON `json:"dropped"`
	}{Candidates: []releaseJSON{}, Dropped: []droppedJSON{}}
	for _, c := range found.result.Candidates {
		out.Candidates = append(out.Candidates, releaseOf(c))
	}
	for _, d := range found.result.Dropped {
		out.Dropped = append(out.Dropped, droppedJSON{Release: releaseOf(d.Candidate), Reason: string(d.Reason), Message: d.Message})
	}
	writeJSON(w, http.StatusOK, out)
}

// grabRelease queues the release the owner picked. The same search is made
// again to find it, since a release's playback link is never kept or sent
// to the browser; a blocklisted release or one its profile rejected can be
// picked, one the chooser drops for being unusable cannot.
func (a *api) grabRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Episode string `json:"episode"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
			fail(w, http.StatusBadRequest, "body must be {\"episode\": \"sNNeNN\"} or empty")
			return
		}
	}
	id := r.PathValue("id")
	if a.DryRun {
		fail(w, http.StatusConflict, "dry-run is on, so nothing is downloaded; turn it off to grab a release")
		return
	}
	found, ok := a.searchReleases(w, r, id, req.Episode, true)
	if !ok {
		return
	}
	releaseID := r.PathValue("releaseId")
	var pick *chooser.Candidate
	for i, c := range found.result.Candidates {
		if c.ID == releaseID {
			pick = &found.result.Candidates[i]
		}
	}
	if pick == nil {
		for i, d := range found.result.Dropped {
			switch {
			case d.ID != releaseID:
			case d.Reason == chooser.ProfileRejected:
				pick = &found.result.Dropped[i].Candidate
			default:
				fail(w, http.StatusConflict, "this release cannot be grabbed: "+d.Message)
				return
			}
		}
	}
	if pick == nil {
		fail(w, http.StatusNotFound, "no such release; the search may have changed, so list the releases again")
		return
	}
	if err := a.ensureTitle(r, id, found.kind, found.tmdbID); err != nil {
		if isTMDBError(err) || r.Context().Err() != nil {
			a.upstream(w, r, err)
		} else {
			a.internal(w, r, err)
		}
		return
	}
	st := pick.Stream
	job := pipeline.Job{
		TitleID: id, Season: found.season, Number: found.episode,
		Release:     catalog.ReleaseID{InfoHash: st.InfoHash, FileName: st.Filename, Size: st.Size},
		ReleaseName: releaseOf(*pick).Name, URL: st.URL,
	}
	if p := st.ParsedFile; p != nil {
		job.Resolution, job.Quality = p.Resolution, p.Quality
	}
	d, err := a.Transfers.Enqueue(r.Context(), job)
	switch {
	case err == nil, errors.Is(err, catalog.ErrActiveDownload):
		writeJSON(w, http.StatusAccepted, downloadOf(d, a.Catalog.Now()))
	case errors.Is(err, pipeline.ErrBadJob):
		fail(w, http.StatusNotFound, "no such title or episode in the library")
	default:
		a.internal(w, r, err)
	}
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
