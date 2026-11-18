package httpapi

import (
	"context"
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
		item := map[string]any{
			"id": d.ID, "titleId": d.TitleID, "titleName": d.TitleName, "releaseName": d.ReleaseName,
			"state": d.State, "percent": d.Percent(), "bytesDone": d.BytesDone, "bytesTotal": d.BytesTotal,
			"stalled": d.Stalled(now),
		}
		if ep := episodeRef(d); ep != "" {
			item["episode"] = ep
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"downloads": out})
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
			item.Kind, item.Message, item.Reason = "failed", d.Message, d.Reason
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
