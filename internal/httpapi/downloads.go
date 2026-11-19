package httpapi

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

func episodeRef(d catalog.Download) string {
	if d.Season == 0 && d.Number == 0 {
		return ""
	}
	return catalog.EpisodeID(d.Season, d.Number)
}

func (a *api) listDownloads(w http.ResponseWriter, r *http.Request) {
	ds, err := a.Catalog.ActiveDownloads(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	now := a.Catalog.Now()
	out := make([]map[string]any, 0, len(ds))
	for _, d := range ds {
		out = append(out, downloadOf(d, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"downloads": out})
}

func downloadOf(d catalog.Download, now time.Time) map[string]any {
	item := map[string]any{
		"id": d.ID, "titleId": d.TitleID, "titleName": d.TitleName, "releaseName": d.ReleaseName,
		"state": d.State, "percent": d.Percent(), "bytesDone": d.BytesDone, "bytesTotal": d.BytesTotal,
		"stalled": d.Stalled(now),
	}
	if ep := episodeRef(d); ep != "" {
		item["episode"] = ep
	}
	return item
}

type historyJSON struct {
	ID        string `json:"id"`
	At        string `json:"at"`
	Kind      string `json:"kind"`
	TitleID   string `json:"titleId"`
	TitleName string `json:"titleName"`
	Episode   string `json:"episode,omitempty"`
	Message   string `json:"message"`
	Reason    string `json:"reason,omitempty"`
	Retryable bool   `json:"retryable"`
}

// retry sends a failed item back to the scheduler, which searches for it
// at once whatever its backoff.
func (a *api) retry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("historyId")
	d, err := a.Catalog.GetDownload(ctx, id)
	if errors.Is(err, catalog.ErrNotFound) {
		events, aerr := a.Catalog.Activities(ctx, "")
		if aerr != nil {
			a.internal(w, r, aerr)
			return
		}
		for _, e := range events {
			if e.ID == id {
				fail(w, http.StatusConflict, "only a failed item can be retried")
				return
			}
		}
		fail(w, http.StatusNotFound, "no such history entry")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if d.State != catalog.StateFailed {
		fail(w, http.StatusConflict, "only a failed item can be retried")
		return
	}
	again, err := a.Catalog.Retryable(ctx, d.ID)
	if errors.Is(err, catalog.ErrNotFound) {
		fail(w, http.StatusNotFound, "no such history entry")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if !again {
		fail(w, http.StatusConflict, "this failure was already retried or a download is running")
		return
	}
	var ep *catalog.EpisodeRef
	if d.Season != 0 || d.Number != 0 {
		ep = &catalog.EpisodeRef{Season: d.Season, Number: d.Number}
	}
	if err := a.Catalog.RequestSearch(ctx, d.TitleID, ep); err != nil {
		a.titleReply(w, r, catalog.Title{}, err)
		return
	}
	err = a.Catalog.AddActivity(ctx, catalog.Activity{TitleID: d.TitleID, Season: d.Season, Number: d.Number, Kind: catalog.ActivitySearched, Message: "Retrying after a failure."})
	if err != nil && !errors.Is(err, catalog.ErrNotFound) {
		a.internal(w, r, err)
		return
	}
	if a.Wake != nil {
		a.Wake()
	}
	t, err := a.Catalog.Get(ctx, d.TitleID)
	a.titleReply(w, r, t, err)
}

// historyPage is what happened, newest first, to one title or, with an
// empty titleID, to all of them: finished downloads and the scheduler's
// notes. A limit of zero or less asks for every entry.
func (a *api) historyPage(ctx context.Context, titleID, cursor string, limit int) ([]historyJSON, string, error) {
	page, err := a.Catalog.History(ctx, titleID, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]historyJSON, 0, len(page.Items))
	for _, it := range page.Items {
		if d := it.Download; d != nil {
			item := historyJSON{ID: d.ID, At: d.UpdatedAt.UTC().Format(time.RFC3339), TitleID: d.TitleID, TitleName: d.TitleName, Episode: episodeRef(*d)}
			if d.State == catalog.StateDone {
				item.Kind, item.Message = "imported", "Imported "+filepath.Base(d.LibraryPath)
			} else {
				item.Kind, item.Message, item.Reason, item.Retryable = "failed", d.Message, d.Reason, it.Retryable
			}
			out = append(out, item)
			continue
		}
		e := it.Activity
		item := historyJSON{ID: e.ID, At: e.At.UTC().Format(time.RFC3339), TitleID: e.TitleID, TitleName: e.TitleName, Kind: e.Kind, Message: e.Message, Reason: e.Reason}
		if e.Season != 0 || e.Number != 0 {
			item.Episode = catalog.EpisodeID(e.Season, e.Number)
		}
		out = append(out, item)
	}
	return out, page.Next, nil
}

// history is every entry of one title, for its detail.
func (a *api) history(ctx context.Context, titleID string) ([]historyJSON, error) {
	out, _, err := a.historyPage(ctx, titleID, "", 0)
	return out, err
}

// historyMaxLimit is the page size of GET /api/history when none is asked,
// and the most one page may hold.
const historyMaxLimit = 1000

// pageParam reads an integer query parameter within min and max, def when absent.
func pageParam(r *http.Request, name string, def, min, max int) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	return n, err == nil && n >= min && n <= max
}

func (a *api) listHistory(w http.ResponseWriter, r *http.Request) {
	titleID := r.URL.Query().Get("titleId")
	if titleID != "" {
		if _, _, ok := parseTitleID(titleID); !ok {
			fail(w, http.StatusBadRequest, "titleId is not a title id")
			return
		}
	}
	limit, ok := pageParam(r, "limit", historyMaxLimit, 1, historyMaxLimit)
	if !ok {
		fail(w, http.StatusBadRequest, "limit must be a number from 1 to "+strconv.Itoa(historyMaxLimit))
		return
	}
	out, next, err := a.historyPage(r.Context(), titleID, r.URL.Query().Get("cursor"), limit)
	if errors.Is(err, catalog.ErrInvalid) {
		fail(w, http.StatusBadRequest, "cursor is not one this server gave")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	body := map[string]any{"history": out}
	if next != "" {
		body["nextCursor"] = next
	}
	writeJSON(w, http.StatusOK, body)
}
