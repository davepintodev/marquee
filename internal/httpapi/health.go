package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/metadata"
)

// Pinger asks a service whether it answers, without changing anything.
type Pinger interface {
	Ping(ctx context.Context) error
}

const defaultHealthTimeout = 8 * time.Second

type check struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func healthy(id, name, msg string) check { return check{id, name, "ok", msg} }
func warn(id, name, msg string) check    { return check{id, name, "warning", msg} }
func failing(id, name, msg string) check { return check{id, name, "error", msg} }

// health probes every dependency now, side by side and each bounded, so one
// hung service cannot hold the page. The probes only read: nothing is
// written to a library, nothing is refreshed, and no message repeats an
// address, a key or an error text from a client.
func (a *api) health(w http.ResponseWriter, r *http.Request) {
	timeout := a.HealthTimeout
	if timeout <= 0 {
		timeout = defaultHealthTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	var aio, tmdb, jf check
	var wg sync.WaitGroup
	probe := func(into *check, f func(context.Context) check) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			*into = f(ctx)
		}()
	}
	probe(&aio, a.checkAIOStreams)
	probe(&tmdb, a.checkTMDB)
	if a.Jellyfin != nil {
		probe(&jf, a.checkJellyfin)
	}
	library, temp := a.checkLibrary(), a.checkTemp()
	wg.Wait()
	if err := r.Context().Err(); err != nil {
		return
	}

	checks := []check{}
	if a.DryRun {
		checks = append(checks, warn("dry_run", "Dry-run", "Dry-run is on: marquee searches and chooses but never downloads."))
	}
	checks = append(checks, aio, tmdb, library, temp)
	if a.Jellyfin != nil {
		checks = append(checks, jf)
	}
	status := "ok"
	for _, c := range checks {
		if c.Status == "error" || (c.Status == "warning" && status == "ok") {
			status = c.Status
		}
	}

	problems, err := a.Catalog.LibraryProblems(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	movies, episodes, err := a.Catalog.LibraryTotals(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	type problemJSON struct {
		Path    string `json:"path"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	library2 := make([]problemJSON, 0, len(problems))
	for _, p := range problems {
		library2 = append(library2, problemJSON(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "dryRun": a.DryRun, "checks": checks, "libraryProblems": library2,
		"libraryTotals": map[string]int{"movies": movies, "episodes": episodes},
	})
}

func timedOut(err error) bool { return errors.Is(err, context.DeadlineExceeded) }

func (a *api) checkAIOStreams(ctx context.Context) check {
	const id, name = "aiostreams", "AIOStreams"
	// A search for an id nothing has is the one authenticated call the
	// client makes; it costs AIOStreams a search, so Health is read on demand only.
	_, err := a.Source.Search(ctx, "movie", "tt0000000")
	switch {
	case err == nil:
		return healthy(id, name, "Reachable.")
	case errors.Is(err, aiostreams.ErrRejected):
		return failing(id, name, "AIOStreams refused marquee's credentials; check the AIOStreams manifest URL setting.")
	case errors.Is(err, aiostreams.ErrRateLimited):
		return warn(id, name, "AIOStreams is rate-limiting marquee; searches will go through again shortly.")
	case timedOut(err):
		return failing(id, name, "AIOStreams did not answer in time.")
	}
	return failing(id, name, "AIOStreams is not answering; check that it is running and that the manifest URL is right.")
}

func (a *api) checkTMDB(ctx context.Context) check {
	const id, name = "tmdb", "TMDB"
	_, err := a.TMDB.Trending(ctx)
	switch {
	case err == nil:
		return healthy(id, name, "Key accepted.")
	case errors.Is(err, metadata.ErrRejected):
		return failing(id, name, "TMDB refused marquee's API key; check the TMDB key setting.")
	case errors.Is(err, metadata.ErrRateLimited):
		return warn(id, name, "TMDB is rate-limiting marquee; try again in a minute.")
	case timedOut(err):
		return failing(id, name, "TMDB did not answer in time.")
	}
	return failing(id, name, "TMDB is not answering; try again shortly.")
}

func (a *api) checkLibrary() check {
	const id, name = "library_paths", "Library paths"
	if msg := a.Paths.LibraryProblem(); msg != "" {
		return failing(id, name, msg)
	}
	return healthy(id, name, "Movie and series folders are readable and writable.")
}

func (a *api) checkTemp() check {
	const id, name = "temp_path", "Temp path"
	if msg := a.Paths.TempProblem(); msg != "" {
		return failing(id, name, msg)
	}
	return healthy(id, name, "On the same filesystem as the library, so imports are atomic.")
}

// checkJellyfin separates two failures: a server that does not answer, seen
// by pinging it now, and one that answers but refused the last refresh, which
// the importer recorded.
func (a *api) checkJellyfin(ctx context.Context) check {
	const id, name = "jellyfin", "Jellyfin"
	if err := a.Jellyfin.Ping(ctx); err != nil {
		if timedOut(err) {
			return failing(id, name, "Jellyfin did not answer in time. Imports still succeed; refreshes are skipped.")
		}
		return failing(id, name, "Jellyfin is unreachable. Imports still succeed; refreshes are skipped.")
	}
	if st, found, err := a.Catalog.GetComponentStatus(ctx, id); err == nil && found && !st.OK {
		return warn(id, name, st.Message)
	}
	return healthy(id, name, "Reachable.")
}
