package httpapi

import (
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

// stallAfter is how long a download's bytes may stand still before the
// queue shows it as stalled; the downloader itself fails it after a minute.
const stallAfter = 15 * time.Second

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
		percent := 0
		if d.BytesTotal > 0 {
			percent = int(min(d.BytesDone*100/d.BytesTotal, 100))
		}
		item := map[string]any{
			"id": d.ID, "titleId": d.TitleID, "titleName": d.TitleName, "releaseName": d.ReleaseName,
			"state": d.State, "percent": percent, "bytesDone": d.BytesDone, "bytesTotal": d.BytesTotal,
			"stalled": d.State == catalog.StateDownloading && now.Sub(d.UpdatedAt) > stallAfter,
		}
		if ep := episodeRef(d); ep != "" {
			item["episode"] = ep
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"downloads": out})
}

func (a *api) listHistory(w http.ResponseWriter, r *http.Request) {
	titleID := r.URL.Query().Get("titleId")
	if titleID != "" {
		if _, _, ok := parseTitleID(titleID); !ok {
			fail(w, http.StatusBadRequest, "titleId is not a title id")
			return
		}
	}
	ds, err := a.Catalog.FinishedDownloads(r.Context(), titleID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	events, err := a.Catalog.Activities(r.Context(), titleID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	type entry struct {
		at   time.Time
		item map[string]any
	}
	entries := make([]entry, 0, len(ds)+len(events))
	for _, d := range ds {
		item := map[string]any{
			"id": d.ID, "at": d.UpdatedAt.UTC().Format(time.RFC3339), "titleId": d.TitleID, "titleName": d.TitleName,
			// The retry route is not built, so no failure can be retried yet.
			"retryable": false,
		}
		if d.State == catalog.StateDone {
			item["kind"], item["message"] = "imported", "Imported "+filepath.Base(d.LibraryPath)
		} else {
			item["kind"], item["message"], item["reason"] = "failed", d.Message, d.Reason
		}
		if ep := episodeRef(d); ep != "" {
			item["episode"] = ep
		}
		entries = append(entries, entry{d.UpdatedAt, item})
	}
	for _, e := range events {
		item := map[string]any{
			"id": e.ID, "at": e.At.UTC().Format(time.RFC3339), "titleId": e.TitleID, "titleName": e.TitleName,
			"kind": e.Kind, "message": e.Message, "retryable": false,
		}
		if e.Reason != "" {
			item["reason"] = e.Reason
		}
		if e.Season != 0 || e.Number != 0 {
			item["episode"] = catalog.EpisodeID(e.Season, e.Number)
		}
		entries = append(entries, entry{e.At, item})
	}
	// Stable, so that entries of the same instant keep downloads before the scheduler's notes.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.After(entries[j].at) })
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": out})
}
