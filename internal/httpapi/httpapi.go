// Package httpapi serves the contract in api/openapi.yaml from the catalog
// and TMDB.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/keylock"
	"github.com/davepintodev/marquee/internal/metadata"
	"github.com/davepintodev/marquee/internal/pipeline"
	"github.com/davepintodev/marquee/internal/subfit"
)

// TMDB is the part of the metadata client the API uses.
type TMDB interface {
	Search(ctx context.Context, q string) ([]metadata.Summary, error)
	Trending(ctx context.Context) ([]metadata.Summary, error)
	Summary(ctx context.Context, kind string, id int) (metadata.Summary, error)
	Details(ctx context.Context, kind string, id int) (metadata.Details, error)
	About(ctx context.Context, kind string, id int) (metadata.About, error)
}

// Transfers is the part of the download manager the API uses; an
// unfinished download of the same title or episode comes back with
// catalog.ErrActiveDownload.
type Transfers interface {
	Enqueue(ctx context.Context, j pipeline.Job) (catalog.Download, error)
	// RefreshLibrary asks the media server to look at the library again.
	RefreshLibrary(ctx context.Context)
	// Pause, Resume and Cancel answer catalog.ErrNotFound for no such
	// download, catalog.ErrFinished or catalog.ErrSettling for one past
	// stopping.
	Pause(ctx context.Context, id string) (catalog.Download, error)
	Resume(ctx context.Context, id string) (catalog.Download, error)
	Cancel(ctx context.Context, id string) error
	// PauseTitle, ResumeTitle and CancelTitle act on every download of a
	// title and answer catalog.ErrNotFound for a title not in the library.
	PauseTitle(ctx context.Context, titleID string) ([]catalog.Download, error)
	ResumeTitle(ctx context.Context, titleID string) ([]catalog.Download, error)
	CancelTitle(ctx context.Context, titleID string) error
	// PauseAll and ResumeAll switch Pause all on and off.
	PauseAll(ctx context.Context) error
	ResumeAll(ctx context.Context) error
	// Speed is the bytes a second a download is moving, zero when it is not.
	Speed(id string) int64
}

type Deps struct {
	Catalog   *catalog.Store
	Transfers Transfers
	TMDB      TMDB
	// OMDb is nil when no OMDb key is set, which leaves the scores to TMDB's own.
	OMDb   Scorer
	Source Source
	// Scanner reads the library folders again; nil leaves the rescan routes unanswered.
	Scanner Scanner
	Images  *metadata.Images
	Log     *slog.Logger
	DryRun  bool
	// Now is the clock the blocklist's expiry is judged by; nil is the system clock.
	Now func() time.Time
	// Wake tells the scheduler a Search now was asked for; nil does nothing.
	Wake func()
	// Paths are the folders Health looks at.
	Paths config.Paths
	// Profiles are the quality profiles a title can use; the zero value
	// has only the built-in default.
	Profiles config.ProfileSet
	// Subfit is nil when subfit is not set up; deleting files then tells nobody.
	Subfit *subfit.Client
	// Jellyfin is nil when Jellyfin is not set up, which leaves it off Health.
	Jellyfin Pinger
	// TorBox is nil when no TorBox key is set: releases the debrid service
	// has not cached are then unusable, and TorBox is left off Health.
	TorBox Pinger
	// HealthTimeout bounds each probe a Health read makes; zero is eight seconds.
	HealthTimeout time.Duration
	// EventsEvery is how often the event stream looks for changes while a
	// client is connected; zero is one second.
	EventsEvery time.Duration
	// Stop ends every open event stream when closed, so that shutdown does
	// not wait for browsers to hang up; nil never ends them.
	Stop <-chan struct{}
}

type api struct {
	Deps
	adds   keylock.Locks
	abouts keylock.Locks
	hub    *hub
}

var (
	titleIDRe   = regexp.MustCompile(`^(movie|series)-([0-9]+)$`)
	episodeIDRe = regexp.MustCompile(`^s([0-9]{2,3})e([0-9]{2,3})$`)
)

// New returns the handler for every /api/ request.
func New(d Deps) http.Handler {
	a := &api{Deps: d}
	if len(a.Profiles.Profiles) == 0 {
		a.Profiles.Profiles = map[string]config.Profile{config.DefaultProfileName: config.DefaultProfile()}
	}
	a.hub = newHub(a, d.EventsEvery)
	if a.Now == nil {
		a.Now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/downloads", a.listDownloads)
	mux.HandleFunc("POST /api/downloads/pause", a.switchPauseAll(func(ctx context.Context) error { return a.Transfers.PauseAll(ctx) }))
	mux.HandleFunc("POST /api/downloads/resume", a.switchPauseAll(func(ctx context.Context) error { return a.Transfers.ResumeAll(ctx) }))
	mux.HandleFunc("POST /api/downloads/{downloadId}/pause", a.changeDownload(func(ctx context.Context, id string) (catalog.Download, error) {
		return a.Transfers.Pause(ctx, id)
	}))
	mux.HandleFunc("POST /api/downloads/{downloadId}/resume", a.changeDownload(func(ctx context.Context, id string) (catalog.Download, error) {
		return a.Transfers.Resume(ctx, id)
	}))
	mux.HandleFunc("POST /api/downloads/{downloadId}/cancel", a.cancelDownload)
	mux.HandleFunc("POST /api/titles/{id}/downloads/pause", a.changeTitleDownloads(func(ctx context.Context, id string) ([]catalog.Download, error) {
		return a.Transfers.PauseTitle(ctx, id)
	}))
	mux.HandleFunc("POST /api/titles/{id}/downloads/resume", a.changeTitleDownloads(func(ctx context.Context, id string) ([]catalog.Download, error) {
		return a.Transfers.ResumeTitle(ctx, id)
	}))
	mux.HandleFunc("POST /api/titles/{id}/downloads/cancel", a.cancelTitleDownloads)
	mux.HandleFunc("GET /api/history", a.listHistory)
	mux.HandleFunc("GET /api/wall", a.wall)
	mux.HandleFunc("GET /api/search", a.search)
	mux.HandleFunc("GET /api/profiles", a.listProfiles)
	mux.HandleFunc("GET /api/titles", a.listTitles)
	mux.HandleFunc("POST /api/titles", a.addTitle)
	mux.HandleFunc("GET /api/titles/{id}", a.getTitle)
	mux.HandleFunc("PATCH /api/titles/{id}", a.updateTitle)
	mux.HandleFunc("DELETE /api/titles/{id}", a.removeTitle)
	mux.HandleFunc("PATCH /api/titles/{id}/episodes/{episodeId}", a.updateEpisode)
	mux.HandleFunc("PUT /api/titles/{id}/seasons/{number}", a.updateSeason)
	mux.HandleFunc("POST /api/titles/{id}/search", a.searchNow)
	mux.HandleFunc("POST /api/titles/{id}/wrong-file", a.wrongFile)
	mux.HandleFunc("POST /api/titles/{id}/episodes/{episodeId}/wrong-file", a.wrongFile)
	mux.HandleFunc("POST /api/titles/{id}/rescan", a.rescanTitle)
	mux.HandleFunc("POST /api/library/rescan", a.rescanLibrary)
	mux.HandleFunc("POST /api/titles/{id}/episodes/{episodeId}/search", a.searchNow)
	mux.HandleFunc("GET /api/titles/{id}/releases", a.listReleases)
	mux.HandleFunc("POST /api/titles/{id}/releases/{releaseId}/grab", a.grabRelease)
	mux.HandleFunc("POST /api/history/{historyId}/retry", a.retry)
	mux.HandleFunc("GET /api/images/{kind}/{titleId}", a.image)
	mux.HandleFunc("GET /api/titles/{id}/about", a.about)
	mux.HandleFunc("GET /api/people/{personId}/image", a.personImage)
	mux.HandleFunc("GET /api/events", a.events)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		fail(w, http.StatusNotFound, "not found")
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// upstream answers a failed TMDB call. The error text of metadata never
// holds a URL or the key, so logging it is safe.
func (a *api) upstream(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, metadata.ErrNotFound):
		fail(w, http.StatusNotFound, "TMDB has no such title")
	case errors.Is(err, metadata.ErrRateLimited):
		a.Log.Warn("tmdb is rate limiting marquee", "path", r.URL.Path)
		fail(w, http.StatusServiceUnavailable, "TMDB is rate-limiting marquee; try again in a minute")
	case errors.Is(err, metadata.ErrRejected):
		a.Log.Error("tmdb refused the API key", "path", r.URL.Path)
		fail(w, http.StatusBadGateway, "TMDB refused marquee's API key; check the TMDB key setting")
	case r.Context().Err() != nil:
		fail(w, http.StatusBadGateway, "request cancelled")
	default:
		a.Log.Warn("tmdb is not answering", "path", r.URL.Path, "err", err)
		fail(w, http.StatusBadGateway, "TMDB is not answering; try again shortly")
	}
}

func (a *api) internal(w http.ResponseWriter, r *http.Request, err error) {
	a.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	fail(w, http.StatusInternalServerError, "something went wrong inside marquee")
}

func parseTitleID(id string) (kind string, tmdbID int, ok bool) {
	m := titleIDRe.FindStringSubmatch(id)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	return m[1], n, err == nil
}
