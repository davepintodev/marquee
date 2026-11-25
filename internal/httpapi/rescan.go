package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/davepintodev/marquee/internal/adopter"
	"github.com/davepintodev/marquee/internal/catalog"
)

// Scanner is the part of the library scanner the API uses.
type Scanner interface {
	ScanAll(ctx context.Context) (adopter.ScanResult, error)
	ScanTitle(ctx context.Context, id string) (catalog.Title, error)
}

func (a *api) rescanLibrary(w http.ResponseWriter, r *http.Request) {
	if a.Scanner == nil {
		fail(w, http.StatusNotImplemented, "this build cannot scan the library")
		return
	}
	res, err := a.Scanner.ScanAll(r.Context())
	if err != nil {
		a.scanFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"titlesScanned": res.TitlesScanned, "adopted": res.Adopted, "foundOnDisk": res.FoundOnDisk,
		"missingOnDisk": res.MissingOnDisk, "problems": res.Problems,
	})
}

func (a *api) rescanTitle(w http.ResponseWriter, r *http.Request) {
	if a.Scanner == nil {
		fail(w, http.StatusNotImplemented, "this build cannot scan the library")
		return
	}
	t, err := a.Scanner.ScanTitle(r.Context(), r.PathValue("id"))
	if err != nil {
		a.scanFailed(w, r, err)
		return
	}
	a.writeTitle(w, r, http.StatusOK, t)
}

func (a *api) scanFailed(w http.ResponseWriter, r *http.Request, err error) {
	var unavailable *adopter.UnavailableError
	var upstream *adopter.UpstreamError
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		fail(w, http.StatusNotFound, "no such title in the library")
	case errors.As(err, &unavailable):
		a.Log.Warn("the library cannot be scanned", "err", err)
		fail(w, http.StatusServiceUnavailable, unavailable.Message)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		fail(w, http.StatusBadGateway, "request cancelled")
	case errors.As(err, &upstream):
		a.upstream(w, r, upstream.Err)
	default:
		a.internal(w, r, err)
	}
}
