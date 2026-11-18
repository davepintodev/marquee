package httpapi

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
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

// retryable picks, from the finished downloads newest first, the failures
// worth a retry: the latest outcome of a title or episode that has no
// download running. An older failure was superseded by a later try, and a
// retry is already under way when a download is running.
func retryable(finished, active []catalog.Download) map[string]bool {
	type target struct {
		title          string
		season, number int
	}
	running := map[target]bool{}
	for _, d := range active {
		running[target{d.TitleID, d.Season, d.Number}] = true
	}
	seen := map[target]bool{}
	out := map[string]bool{}
	for _, d := range finished {
		k := target{d.TitleID, d.Season, d.Number}
		if seen[k] {
			continue
		}
		seen[k] = true
		if d.State == catalog.StateFailed && !running[k] {
			out[d.ID] = true
		}
	}
	return out
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
	finished, err := a.Catalog.FinishedDownloads(ctx, d.TitleID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	active, err := a.Catalog.ActiveDownloads(ctx)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if !retryable(finished, active)[d.ID] {
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

// history is what happened, newest first, to one title or, with an empty
// titleID, to all of them: finished downloads and the scheduler's notes.
func (a *api) history(ctx context.Context, titleID string) ([]historyJSON, error) {
	ds, err := a.Catalog.FinishedDownloads(ctx, titleID)
	if err != nil {
		return nil, err
	}
	events, err := a.Catalog.Activities(ctx, titleID)
	if err != nil {
		return nil, err
	}
	active, err := a.Catalog.ActiveDownloads(ctx)
	if err != nil {
		return nil, err
	}
	again := retryable(ds, active)
	type entry struct {
		at   time.Time
		item historyJSON
	}
	entries := make([]entry, 0, len(ds)+len(events))
	for _, d := range ds {
		item := historyJSON{ID: d.ID, At: d.UpdatedAt.UTC().Format(time.RFC3339), TitleID: d.TitleID, TitleName: d.TitleName, Episode: episodeRef(d)}
		if d.State == catalog.StateDone {
			item.Kind, item.Message = "imported", "Imported "+filepath.Base(d.LibraryPath)
		} else {
			item.Kind, item.Message, item.Reason, item.Retryable = "failed", d.Message, d.Reason, again[d.ID]
		}
		entries = append(entries, entry{d.UpdatedAt, item})
	}
	for _, e := range events {
		item := historyJSON{ID: e.ID, At: e.At.UTC().Format(time.RFC3339), TitleID: e.TitleID, TitleName: e.TitleName, Kind: e.Kind, Message: e.Message, Reason: e.Reason}
		if e.Season != 0 || e.Number != 0 {
			item.Episode = catalog.EpisodeID(e.Season, e.Number)
		}
		entries = append(entries, entry{e.At, item})
	}
	// Stable, so that entries of the same instant keep downloads before the scheduler's notes.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.After(entries[j].at) })
	out := make([]historyJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.item)
	}
	return out, nil
}

func (a *api) listHistory(w http.ResponseWriter, r *http.Request) {
	titleID := r.URL.Query().Get("titleId")
	if titleID != "" {
		if _, _, ok := parseTitleID(titleID); !ok {
			fail(w, http.StatusBadRequest, "titleId is not a title id")
			return
		}
	}
	out, err := a.history(r.Context(), titleID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": out})
}
