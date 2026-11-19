package fake

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
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

	NextSearchAt *time.Time `json:"nextSearchAt"`
}

type seasonOut struct {
	Number   int          `json:"number"`
	Badge    *badge       `json:"badge,omitempty"`
	Episodes []episodeOut `json:"episodes"`
}

type titleOut struct {
	summary
	Seasons          []seasonOut     `json:"seasons"`
	PreferredQuality *string         `json:"preferredQuality"`
	NextSearchAt     *time.Time      `json:"nextSearchAt"`
	HistorySummary   string          `json:"historySummary"`
	History          []*historyEntry `json:"history"`
}

type downloadOut struct {
	*download
	TitleName string `json:"titleName"`
	BytesDone int64  `json:"bytesDone"`
}

func (f *Fake) routes() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(w http.ResponseWriter, r *http.Request)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			before := f.badgeSnapshot()
			fn(w, r)
			f.emitChangedBadges(before)
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
	h("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(f.health)
	})
	// Outside h: a slow client reading a large file must not hold the fake's lock.
	mux.HandleFunc("GET /api/images/{kind}/{titleId}", f.image)
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

var reasonWords = map[string]string{
	"stalled": "stalled", "not_video": "fake", "wrong_episode": "wrong episode",
	"too_small": "too small", "size_mismatch": "wrong size", "source_down": "source down", "local_error": "local error",
}

func summarize(hs []*historyEntry, next *time.Time) string {
	tried := 0
	reasons := map[string]int{}
	var order []string
	for _, h := range hs {
		switch h.Kind {
		case "failed":
			tried++
			if reasons[h.Reason] == 0 {
				order = append(order, h.Reason)
			}
			reasons[h.Reason]++
		case "imported", "would_grab":
			tried++
		}
	}
	out := "Nothing tried yet"
	if tried > 0 {
		out = fmt.Sprintf("Tried %d release", tried)
		if tried != 1 {
			out += "s"
		}
		slices.SortStableFunc(order, func(a, b string) int { return reasons[b] - reasons[a] })
		for _, r := range order {
			out += fmt.Sprintf(", %d %s", reasons[r], reasonWords[r])
		}
	}
	if next != nil {
		out += ", retrying in " + inWords(time.Until(*next))
	}
	return out + "."
}

func inWords(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int((d+30*time.Minute)/time.Hour))
	}
	return fmt.Sprintf("%dm", max(1, int((d+30*time.Second)/time.Minute)))
}

func searchable(state string) bool { return state == "searching" || state == "missing" }

func (f *Fake) nextSearch(t *entry) *time.Time {
	if !t.InLibrary || !t.monitored {
		return nil
	}
	if t.Kind == "movie" {
		if searchable(t.State) && f.activeDownload(t.id(), "") == nil {
			return &t.nextSearch
		}
		return nil
	}
	for _, s := range t.Seasons {
		for _, e := range s.Episodes {
			if st, _ := f.epState(t, s, e); e.monitored && searchable(st) {
				return &t.nextSearch
			}
		}
	}
	return nil
}

func (f *Fake) title(t *entry) titleOut {
	hs := f.historyFor(t.id())
	next := f.nextSearch(t)
	out := titleOut{summary: f.summary(t), Seasons: []seasonOut{}, NextSearchAt: next, HistorySummary: summarize(hs, next), History: hs}
	if t.quality != "" {
		out.PreferredQuality = &t.quality
	}
	for _, s := range t.Seasons {
		so := seasonOut{Number: s.Number, Badge: f.seasonBadge(t, s), Episodes: []episodeOut{}}
		for _, e := range s.Episodes {
			st, p := f.epState(t, s, e)
			eo := episodeOut{ID: epID(s, e), Number: e.Number, Name: e.Name, AirDate: e.AirDate, Monitored: e.monitored, State: st}
			if st == "downloading" {
				eo.Percent = &p
			}
			if t.InLibrary && e.monitored && t.monitored && searchable(st) {
				eo.NextSearchAt = &t.nextSearch
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

func (f *Fake) knownTitle(w http.ResponseWriter, r *http.Request) *entry {
	t := f.find(r.PathValue("id"))
	if t == nil {
		fail(w, http.StatusNotFound, "no such title")
	}
	return t
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
	f.addToLibrary(t, req.PreferredQuality)
	write(w, http.StatusCreated, f.title(t))
}

func (f *Fake) addToLibrary(t *entry, quality string) {
	t.InLibrary, t.monitored, t.quality = true, true, quality
	t.nextSearch = time.Now().Add(defaultNextSearch)
	if t.Kind == "movie" {
		t.State = "searching"
	}
	for _, s := range t.Seasons {
		for _, e := range s.Episodes {
			e.monitored = true
			if e.State != "unaired" {
				e.State = "searching"
			}
		}
	}
	f.addHistory("added", t, "", "Added and monitored.", "", false)
}

func (f *Fake) get(w http.ResponseWriter, r *http.Request) {
	t := f.find(r.PathValue("id"))
	// The real service answers 404 until a series is added, so the popup's Interactive search says to add it first; movies keep answering.
	if t == nil || (t.Kind == "series" && !t.InLibrary) {
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
		Monitored        *bool           `json:"monitored"`
		PreferredQuality json.RawMessage `json:"preferredQuality"`
	}
	if !decode(r, &req) || (req.Monitored == nil && req.PreferredQuality == nil) {
		fail(w, http.StatusBadRequest, "send monitored, preferredQuality or both")
		return
	}
	quality, clear := "", string(req.PreferredQuality) == "null"
	if req.PreferredQuality != nil && !clear {
		if json.Unmarshal(req.PreferredQuality, &quality) != nil || !slices.Contains([]string{"2160p", "1080p", "720p"}, quality) {
			fail(w, http.StatusBadRequest, "preferredQuality must be 2160p, 1080p, 720p or null")
			return
		}
	}
	if req.Monitored != nil {
		t.monitored = *req.Monitored
		for _, s := range t.Seasons {
			for _, e := range s.Episodes {
				e.monitored = *req.Monitored
			}
		}
	}
	if clear {
		t.quality = ""
	} else if quality != "" {
		t.quality = quality
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
	t.nextSearch = time.Now().Add(defaultNextSearch)
	f.addHistory("searched", t, epID, "Search now: skipping the backoff.", "", false)
	write(w, http.StatusOK, f.title(t))
}

func (f *Fake) listReleases(w http.ResponseWriter, r *http.Request) {
	t := f.knownTitle(w, r)
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

var ungrabbable = []string{"not_cached", "not_video", "too_small"}

func (f *Fake) grab(w http.ResponseWriter, r *http.Request) {
	t := f.knownTitle(w, r)
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
	if slices.Contains(ungrabbable, rel.Drop) {
		fail(w, http.StatusConflict, "this release cannot be grabbed: "+rel.Message)
		return
	}
	if t.Kind == "movie" {
		req.Episode = ""
	} else if f.findEpisode(t, req.Episode) == nil {
		fail(w, http.StatusBadRequest, "episode must name an episode of this series")
		return
	}
	if !t.InLibrary {
		f.addToLibrary(t, "")
	}
	d := f.activeDownload(t.id(), req.Episode)
	if d == nil {
		d = &download{ID: f.newID("dl"), TitleID: t.id(), Episode: req.Episode, ReleaseName: rel.Name, BytesTotal: rel.SizeBytes, State: "queued"}
		f.downloads = append(f.downloads, d)
	}
	write(w, http.StatusAccepted, f.downloadOut(d))
}

func (f *Fake) downloadOut(d *download) downloadOut {
	name := d.TitleID
	if t := f.find(d.TitleID); t != nil {
		name = t.Title
	}
	return downloadOut{download: d, TitleName: name, BytesDone: d.BytesTotal * int64(d.Percent) / 100}
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
	limit, offset := 1000, 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = min(v, len(out))
	}
	body := map[string]any{}
	out = out[offset:]
	if len(out) > limit {
		out = out[:limit]
		body["nextOffset"] = offset + limit
	}
	body["history"] = append([]*historyEntry{}, out...)
	write(w, http.StatusOK, body)
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
	kind := r.PathValue("kind")
	f.mu.Lock()
	t := f.find(r.PathValue("titleId"))
	f.mu.Unlock()
	if t == nil || (kind != "poster" && kind != "backdrop") {
		fail(w, http.StatusNotFound, "no such image")
		return
	}
	if f.serveArt(w, r, kind, t.id()) {
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
	w.Header().Set("Cache-Control", "no-cache")
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
	beat := time.NewTicker(f.opts.KeepAlive)
	defer beat.Stop()
	for {
		select {
		case <-beat.C:
			_, _ = fmt.Fprint(w, ": keep-alive\n\n")
			fl.Flush()
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
