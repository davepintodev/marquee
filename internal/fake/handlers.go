package fake

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"net/http"
	"slices"
	"strings"
)

type summary struct {
	ID           string `json:"id"`
	TmdbID       int    `json:"tmdbId"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Year         int    `json:"year"`
	Overview     string `json:"overview"`
	PosterPath   string `json:"posterPath"`
	BackdropPath string `json:"backdropPath"`
	InLibrary    bool   `json:"inLibrary"`
	Monitored    *bool  `json:"monitored,omitempty"`
	Badge        *badge `json:"badge,omitempty"`
}

type episodeOut struct {
	ID        string `json:"id"`
	Number    int    `json:"number"`
	Name      string `json:"name"`
	AirDate   string `json:"airDate"`
	Monitored bool   `json:"monitored"`
	State     string `json:"state"`
	Percent   *int   `json:"percent,omitempty"`
}

type seasonOut struct {
	Number   int          `json:"number"`
	Badge    *badge       `json:"badge,omitempty"`
	Episodes []episodeOut `json:"episodes"`
}

type titleOut struct {
	summary
	Seasons          []seasonOut     `json:"seasons"`
	PreferredQuality string          `json:"preferredQuality,omitempty"`
	HistorySummary   string          `json:"historySummary"`
	History          []*historyEntry `json:"history"`
}

type downloadOut struct {
	*download
	TitleName string `json:"titleName"`
	State     string `json:"state"`
	BytesDone int64  `json:"bytesDone"`
}

func (f *Fake) routes() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(w http.ResponseWriter, r *http.Request)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			fn(w, r)
		})
	}
	h("GET /api/wall", f.wall)
	h("GET /api/search", f.search)
	h("GET /api/titles", f.list)
	h("POST /api/titles", f.add)
	h("GET /api/titles/{id}", f.get)
	h("PATCH /api/titles/{id}", f.update)
	h("DELETE /api/titles/{id}", f.remove)
	h("POST /api/titles/{id}/search", f.searchNow)
	h("PATCH /api/titles/{id}/episodes/{episodeId}", f.updateEpisode)
	h("POST /api/titles/{id}/episodes/{episodeId}/search", f.searchNow)
	h("GET /api/titles/{id}/releases", f.listReleases)
	h("POST /api/titles/{id}/releases/{releaseId}/grab", f.grab)
	h("GET /api/downloads", f.listDownloads)
	h("GET /api/history", f.listHistory)
	h("POST /api/history/{historyId}/retry", f.retry)
	h("GET /api/images/{kind}/{titleId}", f.image)
	h("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(f.health)
	})
	mux.HandleFunc("GET /api/events", f.events)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { fail(w, http.StatusNotFound, "not found") })
	return mux
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil
}

func (f *Fake) summary(t *entry) summary {
	s := summary{
		ID: t.id(), TmdbID: t.TmdbID, Kind: t.Kind, Title: t.Title, Year: t.Year, Overview: t.Overview,
		PosterPath: "/api/images/poster/" + t.id(), BackdropPath: "/api/images/backdrop/" + t.id(),
		InLibrary: t.InLibrary, Badge: f.badge(t),
	}
	if t.InLibrary {
		s.Monitored = &t.monitored
	}
	return s
}

func (f *Fake) historyFor(id string) []*historyEntry {
	out := []*historyEntry{}
	for _, h := range f.history {
		if h.TitleID == id {
			out = append(out, h)
		}
	}
	return out
}

func summarize(hs []*historyEntry) string {
	var failed, tried int
	for _, h := range hs {
		switch h.Kind {
		case "failed":
			failed++
			tried++
		case "imported", "would_grab":
			tried++
		}
	}
	if tried == 0 {
		return "Nothing grabbed yet."
	}
	return fmt.Sprintf("Tried %d release(s), %d failed.", tried, failed)
}

func (f *Fake) title(t *entry) titleOut {
	hs := f.historyFor(t.id())
	out := titleOut{summary: f.summary(t), Seasons: []seasonOut{}, PreferredQuality: t.quality, HistorySummary: summarize(hs), History: hs}
	for _, s := range t.Seasons {
		so := seasonOut{Number: s.Number, Badge: f.seasonBadge(t, s), Episodes: []episodeOut{}}
		for _, e := range s.Episodes {
			st, p := f.epState(t, s, e)
			eo := episodeOut{epID(s, e), e.Number, e.Name, e.AirDate, e.monitored, st, nil}
			if st == "downloading" {
				eo.Percent = &p
			}
			so.Episodes = append(so.Episodes, eo)
		}
		out.Seasons = append(out.Seasons, so)
	}
	return out
}

func (f *Fake) wall(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		Source string  `json:"source"`
		Title  summary `json:"title"`
	}
	items := []item{}
	for _, t := range f.titles {
		if t.InLibrary {
			items = append(items, item{"library", f.summary(t)})
		}
	}
	for _, t := range f.titles {
		if !t.InLibrary {
			items = append(items, item{"trending", f.summary(t)})
		}
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}

func (f *Fake) search(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q == "" {
		fail(w, http.StatusBadRequest, "q must not be empty")
		return
	}
	results := []summary{}
	for _, t := range f.titles {
		if strings.Contains(strings.ToLower(t.Title), q) {
			results = append(results, f.summary(t))
		}
	}
	write(w, http.StatusOK, map[string]any{"results": results})
}

func (f *Fake) list(w http.ResponseWriter, _ *http.Request) {
	titles := []summary{}
	for _, t := range f.titles {
		if t.InLibrary {
			titles = append(titles, f.summary(t))
		}
	}
	write(w, http.StatusOK, map[string]any{"titles": titles})
}

func (f *Fake) libraryTitle(w http.ResponseWriter, r *http.Request) *entry {
	t := f.find(r.PathValue("id"))
	if t == nil || !t.InLibrary {
		fail(w, http.StatusNotFound, "that title is not in the library")
		return nil
	}
	return t
}

func (f *Fake) add(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind             string `json:"kind"`
		TmdbID           int    `json:"tmdbId"`
		PreferredQuality string `json:"preferredQuality"`
	}
	if !decode(r, &req) || (req.Kind != "movie" && req.Kind != "series") || req.TmdbID <= 0 {
		fail(w, http.StatusBadRequest, "kind must be movie or series and tmdbId a positive number")
		return
	}
	t := f.find(fmt.Sprintf("%s-%d", req.Kind, req.TmdbID))
	if t == nil {
		fail(w, http.StatusNotFound, "TMDB has no such title")
		return
	}
	if t.InLibrary {
		write(w, http.StatusOK, f.title(t))
		return
	}
	t.InLibrary, t.monitored, t.quality = true, true, req.PreferredQuality
	if t.Kind == "movie" {
		t.State = "searching"
	} else {
		if len(t.Seasons) == 0 {
			s := &season{Number: 1}
			for i := 1; i <= 3; i++ {
				s.Episodes = append(s.Episodes, &episode{Number: i, Name: fmt.Sprintf("Episode %d", i), AirDate: fmt.Sprintf("2020-01-%02d", i), monitored: true})
			}
			t.Seasons = []*season{s}
		}
		for _, s := range t.Seasons {
			for _, e := range s.Episodes {
				if e.State != "unaired" {
					e.State = "searching"
				}
			}
		}
	}
	f.addHistory("added", t, "", "Added and monitored.", "", false)
	f.emit("title", map[string]any{"titleId": t.id(), "badge": f.badge(t)})
	write(w, http.StatusCreated, f.title(t))
}

func (f *Fake) get(w http.ResponseWriter, r *http.Request) {
	t := f.find(r.PathValue("id"))
	if t == nil {
		fail(w, http.StatusNotFound, "no such title")
		return
	}
	write(w, http.StatusOK, f.title(t))
}

func (f *Fake) update(w http.ResponseWriter, r *http.Request) {
	t := f.libraryTitle(w, r)
	if t == nil {
		return
	}
	var req struct {
		Monitored        *bool  `json:"monitored"`
		PreferredQuality string `json:"preferredQuality"`
	}
	if !decode(r, &req) || (req.Monitored == nil && req.PreferredQuality == "") {
		fail(w, http.StatusBadRequest, "send monitored, preferredQuality or both")
		return
	}
	if req.Monitored != nil {
		t.monitored = *req.Monitored
		for _, s := range t.Seasons {
			for _, e := range s.Episodes {
				e.monitored = *req.Monitored
			}
		}
	}
	if req.PreferredQuality != "" {
		t.quality = req.PreferredQuality
	}
	write(w, http.StatusOK, f.title(t))
}

func (f *Fake) remove(w http.ResponseWriter, r *http.Request) {
	t := f.libraryTitle(w, r)
	if t == nil {
		return
	}
	t.InLibrary, t.monitored, t.quality = false, false, ""
	if t.Kind == "movie" {
		t.State = "missing"
	}
	for _, s := range t.Seasons {
		for _, e := range s.Episodes {
			e.monitored = true
			if e.State != "unaired" {
				e.State = "missing"
			}
		}
	}
	f.downloads = slices.DeleteFunc(f.downloads, func(d *download) bool { return d.TitleID == t.id() })
	w.WriteHeader(http.StatusNoContent)
}

func (f *Fake) updateEpisode(w http.ResponseWriter, r *http.Request) {
	t := f.libraryTitle(w, r)
	if t == nil {
		return
	}
	e := f.findEpisode(t, r.PathValue("episodeId"))
	if e == nil {
		fail(w, http.StatusNotFound, "no such episode")
		return
	}
	var req struct {
		Monitored *bool `json:"monitored"`
	}
	if !decode(r, &req) || req.Monitored == nil {
		fail(w, http.StatusBadRequest, "send monitored")
		return
	}
	e.monitored = *req.Monitored
	write(w, http.StatusOK, f.title(t))
}

// searchNow moves what is missing to searching: the whole title, or the
// one episode named in the path.
func (f *Fake) searchNow(w http.ResponseWriter, r *http.Request) {
	t := f.libraryTitle(w, r)
	if t == nil {
		return
	}
	epID := r.PathValue("episodeId")
	if epID != "" {
		e := f.findEpisode(t, epID)
		if e == nil {
			fail(w, http.StatusNotFound, "no such episode")
			return
		}
		if e.State == "missing" {
			e.State = "searching"
		}
	} else if t.Kind == "movie" {
		if t.State == "missing" {
			t.State = "searching"
		}
	} else {
		for _, s := range t.Seasons {
			for _, e := range s.Episodes {
				if e.State == "missing" {
					e.State = "searching"
				}
			}
		}
	}
	f.addHistory("searched", t, epID, "Search now: skipping the backoff.", "", false)
	write(w, http.StatusOK, f.title(t))
}

func (f *Fake) listReleases(w http.ResponseWriter, r *http.Request) {
	t := f.libraryTitle(w, r)
	if t == nil {
		return
	}
	if t.Kind == "series" {
		ep := r.URL.Query().Get("episode")
		if ep == "" || f.findEpisode(t, ep) == nil {
			fail(w, http.StatusBadRequest, "episode must name an episode of this series")
			return
		}
	}
	type release struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Quality   string `json:"quality"`
		SizeBytes int64  `json:"sizeBytes"`
		Source    string `json:"source"`
		Cached    bool   `json:"cached"`
	}
	type dropped struct {
		Release release `json:"release"`
		Reason  string  `json:"reason"`
		Message string  `json:"message"`
	}
	candidates, drops := []release{}, []dropped{}
	for _, x := range f.releases[t.Kind] {
		rel := release{x.ID, x.Name, x.Quality, x.SizeBytes, x.Source, x.Cached}
		if x.Drop == "" {
			candidates = append(candidates, rel)
		} else {
			drops = append(drops, dropped{rel, x.Drop, x.Message})
		}
	}
	write(w, http.StatusOK, map[string]any{"candidates": candidates, "dropped": drops})
}

func (f *Fake) grab(w http.ResponseWriter, r *http.Request) {
	t := f.libraryTitle(w, r)
	if t == nil {
		return
	}
	var req struct {
		Episode string `json:"episode"`
	}
	if r.ContentLength != 0 && !decode(r, &req) {
		fail(w, http.StatusBadRequest, "body must be {\"episode\": \"sNNeNN\"} or empty")
		return
	}
	var rel *release
	for i, x := range f.releases[t.Kind] {
		if x.ID == r.PathValue("releaseId") {
			rel = &f.releases[t.Kind][i]
		}
	}
	if rel == nil {
		fail(w, http.StatusNotFound, "no such release")
		return
	}
	if t.Kind == "movie" {
		req.Episode = ""
	} else if f.findEpisode(t, req.Episode) == nil {
		fail(w, http.StatusBadRequest, "episode must name an episode of this series")
		return
	}
	d := f.activeDownload(t.id(), req.Episode)
	if d == nil {
		d = &download{ID: f.newID("dl"), TitleID: t.id(), Episode: req.Episode, ReleaseName: rel.Name, BytesTotal: rel.SizeBytes}
		f.downloads = append(f.downloads, d)
		f.emit("title", map[string]any{"titleId": t.id(), "badge": f.badge(t)})
	}
	write(w, http.StatusAccepted, f.downloadOut(d))
}

func (f *Fake) downloadOut(d *download) downloadOut {
	name := d.TitleID
	if t := f.find(d.TitleID); t != nil {
		name = t.Title
	}
	return downloadOut{download: d, TitleName: name, State: "downloading", BytesDone: d.BytesTotal * int64(d.Percent) / 100}
}

func (f *Fake) listDownloads(w http.ResponseWriter, _ *http.Request) {
	out := []downloadOut{}
	for _, d := range f.downloads {
		out = append(out, f.downloadOut(d))
	}
	write(w, http.StatusOK, map[string]any{"downloads": out})
}

func (f *Fake) listHistory(w http.ResponseWriter, r *http.Request) {
	out := f.history
	if id := r.URL.Query().Get("titleId"); id != "" {
		out = f.historyFor(id)
	}
	write(w, http.StatusOK, map[string]any{"history": append([]*historyEntry{}, out...)})
}

func (f *Fake) retry(w http.ResponseWriter, r *http.Request) {
	var h *historyEntry
	for _, x := range f.history {
		if x.ID == r.PathValue("historyId") {
			h = x
		}
	}
	if h == nil {
		fail(w, http.StatusNotFound, "no such history entry")
		return
	}
	t := f.find(h.TitleID)
	if t == nil || !t.InLibrary {
		fail(w, http.StatusNotFound, "that title is no longer in the library")
		return
	}
	if h.Kind != "failed" || !h.Retryable {
		fail(w, http.StatusConflict, "only a failed item can be retried, and only once")
		return
	}
	h.Retryable = false
	if h.Episode != "" {
		if e := f.findEpisode(t, h.Episode); e != nil && e.State == "missing" {
			e.State = "searching"
		}
	} else if t.State == "missing" {
		t.State = "searching"
	}
	f.downloads = slices.DeleteFunc(f.downloads, func(d *download) bool { return d.TitleID == t.id() && d.Episode == h.Episode && d.Stalled })
	f.addHistory("searched", t, h.Episode, "Retrying after a failure.", "", false)
	write(w, http.StatusOK, f.title(t))
}

func (f *Fake) image(w http.ResponseWriter, r *http.Request) {
	t := f.find(r.PathValue("titleId"))
	kind := r.PathValue("kind")
	if t == nil || (kind != "poster" && kind != "backdrop") {
		fail(w, http.StatusNotFound, "no such image")
		return
	}
	hue := fnv.New32a()
	_, _ = hue.Write([]byte(t.Title))
	h := int(hue.Sum32() % 360)
	width, height := 200, 300
	if kind == "backdrop" {
		width, height = 320, 180
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d">`, width, height, width, height)
	fmt.Fprintf(&b, `<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="hsl(%d 55%% 34%%)"/><stop offset="1" stop-color="hsl(%d 60%% 14%%)"/></linearGradient></defs>`, h, (h+50)%360)
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" fill="url(#g)"/><text x="50%%" y="50%%" fill="#fff" font-family="sans-serif" font-size="16" text-anchor="middle">%s</text></svg>`, html.EscapeString(t.Title))
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(b.Bytes())
}

func (f *Fake) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	ch := make(chan event, 32)
	f.mu.Lock()
	f.subs[ch] = struct{}{}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.subs, ch)
		f.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-f.stop:
			return
		case ev := <-ch:
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data)
			fl.Flush()
		}
	}
}
