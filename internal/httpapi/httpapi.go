// Package httpapi serves the contract in api/openapi.yaml from the catalog
// and TMDB. Operations that belong to later work answer 501.
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
	"github.com/davepintodev/marquee/internal/keylock"
	"github.com/davepintodev/marquee/internal/metadata"
)

// TMDB is the part of the metadata client the API uses.
type TMDB interface {
	Search(ctx context.Context, q string) ([]metadata.Summary, error)
	Trending(ctx context.Context) ([]metadata.Summary, error)
	Summary(ctx context.Context, kind string, id int) (metadata.Summary, error)
	Details(ctx context.Context, kind string, id int) (metadata.Details, error)
}

type Deps struct {
	Catalog *catalog.Store
	TMDB    TMDB
	Source  Source
	Images  *metadata.Images
	Log     *slog.Logger
	DryRun  bool
	// Now is the clock the blocklist's expiry is judged by; nil is the system clock.
	Now func() time.Time
}

type api struct {
	Deps
	adds keylock.Locks
}

var (
	titleIDRe   = regexp.MustCompile(`^(movie|series)-([0-9]+)$`)
	episodeIDRe = regexp.MustCompile(`^s([0-9]{2,3})e([0-9]{2,3})$`)
)

// New returns the handler for every /api/ request.
func New(d Deps) http.Handler {
	a := &api{Deps: d}
	if a.Now == nil {
		a.Now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/downloads", a.listDownloads)
	mux.HandleFunc("GET /api/history", a.listHistory)
	mux.HandleFunc("GET /api/wall", a.wall)
	mux.HandleFunc("GET /api/search", a.search)
	mux.HandleFunc("GET /api/titles", a.listTitles)
	mux.HandleFunc("POST /api/titles", a.addTitle)
	mux.HandleFunc("GET /api/titles/{id}", a.getTitle)
	mux.HandleFunc("PATCH /api/titles/{id}", a.updateTitle)
	mux.HandleFunc("DELETE /api/titles/{id}", a.removeTitle)
	mux.HandleFunc("PATCH /api/titles/{id}/episodes/{episodeId}", a.updateEpisode)
	mux.HandleFunc("GET /api/titles/{id}/releases", a.listReleases)
	mux.HandleFunc("GET /api/images/{kind}/{titleId}", a.image)
	for _, route := range []string{
		"POST /api/titles/{id}/search",
		"POST /api/titles/{id}/episodes/{episodeId}/search",
		"POST /api/titles/{id}/releases/{releaseId}/grab",
		"POST /api/history/{historyId}/retry",
		"GET /api/events",
	} {
		mux.HandleFunc(route, func(w http.ResponseWriter, _ *http.Request) {
			fail(w, http.StatusNotImplemented, "this part of marquee is not built yet")
		})
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		fail(w, http.StatusNotFound, "not found")
	})
	return mux
}

func (a *api) health(w http.ResponseWriter, r *http.Request) {
	checks := []map[string]any{}
	status := "ok"
	// A refresh that failed after an import is the one check recorded so
	// far; the Health page proper adds the rest.
	if st, ok, err := a.Catalog.GetComponentStatus(r.Context(), "jellyfin"); err == nil && ok && !st.OK {
		checks = append(checks, map[string]any{"id": "jellyfin", "name": "Jellyfin", "status": "warning", "message": st.Message})
		status = "warning"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "dryRun": a.DryRun, "checks": checks, "libraryProblems": []any{},
	})
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
