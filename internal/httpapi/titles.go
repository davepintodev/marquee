package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/metadata"
)

const maxBody = 1 << 16

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("extra data after the JSON value")
	}
	return nil
}

func (a *api) libraryByID(r *http.Request) (map[string]catalog.Title, error) {
	ts, err := a.Catalog.List(r.Context())
	if err != nil {
		return nil, err
	}
	m := make(map[string]catalog.Title, len(ts))
	for _, t := range ts {
		m[t.ID] = t
	}
	return m, nil
}

func (a *api) rememberImages(r *http.Request, found []metadata.Summary) {
	var refs []catalog.ImageRef
	for _, m := range found {
		refs = append(refs, imageRefs(catalog.TitleID(m.Kind, m.TMDBID), m)...)
	}
	if err := a.Catalog.RememberImages(r.Context(), refs); err != nil {
		a.Log.Warn("could not remember image paths", "err", err)
	}
}

func (a *api) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		fail(w, http.StatusBadRequest, "send a search term in q")
		return
	}
	found, err := a.TMDB.Search(r.Context(), q)
	if err != nil {
		a.upstream(w, r, err)
		return
	}
	library, err := a.libraryByID(r)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	a.rememberImages(r, found)
	results := make([]summaryJSON, 0, len(found))
	for _, m := range found {
		results = append(results, fromTMDB(m, library))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// wall is the library first and TMDB's trending after it. Trending is a
// nicety: when TMDB is down the wall is the library alone, not an error.
func (a *api) wall(w http.ResponseWriter, r *http.Request) {
	titles, err := a.Catalog.List(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	type item struct {
		Source string      `json:"source"`
		Title  summaryJSON `json:"title"`
	}
	items := make([]item, 0, len(titles))
	in := map[string]bool{}
	for _, t := range titles {
		in[t.ID] = true
		items = append(items, item{"library", fromLibrary(t)})
	}
	trending, err := a.TMDB.Trending(r.Context())
	if err != nil {
		a.Log.Warn("the wall has no trending titles", "err", err)
	} else {
		a.rememberImages(r, trending)
		for _, m := range trending {
			if id := catalog.TitleID(m.Kind, m.TMDBID); !in[id] {
				items = append(items, item{"trending", fromTMDB(m, nil)})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *api) listTitles(w http.ResponseWriter, r *http.Request) {
	ts, err := a.Catalog.List(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]summaryJSON, 0, len(ts))
	for _, t := range ts {
		out = append(out, fromLibrary(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"titles": out})
}

func (a *api) getTitle(w http.ResponseWriter, r *http.Request) {
	t, err := a.Catalog.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		fail(w, http.StatusNotFound, "no such title in the library")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, titleOf(t))
}

func (a *api) addTitle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind             string `json:"kind"`
		TMDBID           int    `json:"tmdbId"`
		PreferredQuality string `json:"preferredQuality"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "send kind (movie or series), tmdbId and optionally preferredQuality")
		return
	}
	if (req.Kind != "movie" && req.Kind != "series") || req.TMDBID < 1 {
		fail(w, http.StatusBadRequest, "kind must be movie or series and tmdbId a TMDB id")
		return
	}
	if req.PreferredQuality != "" && !isQuality(req.PreferredQuality) {
		fail(w, http.StatusBadRequest, "preferredQuality must be 2160p, 1080p or 720p")
		return
	}
	id := catalog.TitleID(req.Kind, req.TMDBID)

	defer a.adds.Lock(id)()
	if t, err := a.Catalog.Get(r.Context(), id); err == nil {
		writeJSON(w, http.StatusOK, titleOf(t))
		return
	} else if !errors.Is(err, catalog.ErrNotFound) {
		a.internal(w, r, err)
		return
	}

	// Everything is fetched before the transaction opens, so a TMDB failure
	// half way leaves nothing behind.
	d, err := a.TMDB.Details(r.Context(), req.Kind, req.TMDBID)
	if err != nil {
		a.upstream(w, r, err)
		return
	}
	n := catalog.NewTitleFromTMDB(d)
	n.PreferredQuality = req.PreferredQuality
	t, created, err := a.Catalog.Add(r.Context(), n)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, titleOf(t))
}

func isQuality(q string) bool {
	for _, v := range catalog.Qualities {
		if v == q {
			return true
		}
	}
	return false
}

func (a *api) updateTitle(w http.ResponseWriter, r *http.Request) {
	var raw map[string]json.RawMessage
	if err := decode(r, &raw); err != nil || len(raw) == 0 {
		fail(w, http.StatusBadRequest, "send monitored, preferredQuality or both")
		return
	}
	var p catalog.Patch
	for k, v := range raw {
		switch k {
		case "monitored":
			var b bool
			if err := json.Unmarshal(v, &b); err != nil {
				fail(w, http.StatusBadRequest, "monitored must be true or false")
				return
			}
			p.Monitored = &b
		case "preferredQuality":
			q := ""
			if !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				if err := json.Unmarshal(v, &q); err != nil || !isQuality(q) {
					fail(w, http.StatusBadRequest, "preferredQuality must be 2160p, 1080p, 720p or null")
					return
				}
			}
			p.Quality = &q
		default:
			fail(w, http.StatusBadRequest, "send monitored, preferredQuality or both")
			return
		}
	}
	t, err := a.Catalog.Update(r.Context(), r.PathValue("id"), p)
	a.titleReply(w, r, t, err)
}

func (a *api) updateEpisode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Monitored *bool `json:"monitored"`
	}
	if err := decode(r, &req); err != nil || req.Monitored == nil {
		fail(w, http.StatusBadRequest, "send monitored")
		return
	}
	var season, number int
	if m := episodeIDRe.FindStringSubmatch(r.PathValue("episodeId")); m != nil {
		season, number = atoi(m[1]), atoi(m[2])
	} else {
		fail(w, http.StatusNotFound, "no such episode")
		return
	}
	t, err := a.Catalog.SetEpisodeMonitored(r.Context(), r.PathValue("id"), season, number, *req.Monitored)
	a.titleReply(w, r, t, err)
}

func (a *api) titleReply(w http.ResponseWriter, r *http.Request, t catalog.Title, err error) {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		fail(w, http.StatusNotFound, "no such title or episode in the library")
	case err != nil:
		a.internal(w, r, err)
	default:
		writeJSON(w, http.StatusOK, titleOf(t))
	}
}

// removeTitle forgets a title and its cached images. The catalog has no
// notion of a library path, so no file on disk can be touched from here.
func (a *api) removeTitle(w http.ResponseWriter, r *http.Request) {
	refs, err := a.Catalog.Remove(r.Context(), r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		fail(w, http.StatusNotFound, "no such title in the library")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	for _, ref := range refs {
		if ref.Path != "" {
			a.Images.Forget(imageSize(ref.Kind), ref.Path)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
