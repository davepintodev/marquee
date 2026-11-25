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

	SearchingEpisodes []string `json:"searchingEpisodes,omitempty"`
}

type episodeOut struct {
	ID        string `json:"id"`
	Number    int    `json:"number"`
	Name      string `json:"name"`
	AirDate   string `json:"airDate"`
	Monitored bool   `json:"monitored"`
	State     string `json:"state"`
	Percent   *int   `json:"percent,omitempty"`

	SharedEpisodes []string   `json:"sharedEpisodes,omitempty"`
	NextSearchAt   *time.Time `json:"nextSearchAt"`
}

type seasonOut struct {
	Number   int          `json:"number"`
	Badge    *badge       `json:"badge,omitempty"`
	Episodes []episodeOut `json:"episodes"`
}

type titleOut struct {
	summary
	Seasons        []seasonOut     `json:"seasons"`
	Profile        *string         `json:"profile"`
	NextSearchAt   *time.Time      `json:"nextSearchAt"`
	HistorySummary string          `json:"historySummary"`
	History        []*historyEntry `json:"history"`
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
	h("GET /api/profiles", f.listProfiles)
	h("GET /api/titles", f.list)
	h("POST /api/titles", f.add)
	h("GET /api/titles/{id}", f.get)
	h("PATCH /api/titles/{id}", f.update)
	h("DELETE /api/titles/{id}", f.remove)
	h("POST /api/titles/{id}/search", f.searchNow)
	h("POST /api/titles/{id}/wrong-file", f.wrongFile)
	h("POST /api/titles/{id}/episodes/{episodeId}/wrong-file", f.wrongFile)
	h("POST /api/titles/{id}/rescan", f.rescanTitle)
	h("POST /api/library/rescan", f.rescanLibrary)
	h("PUT /api/titles/{id}/seasons/{number}", f.updateSeason)
	h("PATCH /api/titles/{id}/episodes/{episodeId}", f.updateEpisode)
	h("POST /api/titles/{id}/episodes/{episodeId}/search", f.searchNow)
	h("GET /api/titles/{id}/releases", f.listReleases)
	h("GET /api/titles/{id}/about", f.about)
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
	mux.HandleFunc("GET /api/people/{personId}/image", f.personImage)
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
		for _, se := range t.Seasons {
			for _, e := range se.Episodes {
				if st, _ := f.epState(t, se, e); e.monitored && (st == "searching" || waiting(st)) {
					s.SearchingEpisodes = append(s.SearchingEpisodes, epID(se, e))
				}
			}
		}
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
	"too_small": "too small", "size_mismatch": "wrong size", "source_down": "source down", "local_error": "local error", "duplicate_episode": "duplicate episode",
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

func searchable(state string) bool { return waiting(state) }

func firstQueued(t *entry) *time.Time {
	var soonest *time.Time
	for _, s := range t.Seasons {
		for _, e := range s.Episodes {
			if queued(e) && (soonest == nil || e.searchAt.Before(*soonest)) {
				soonest = &e.searchAt
			}
		}
	}
	return soonest
}

// queueEnd is when the last running or queued search of t's episodes ends,
// so a second Search now queues after it.
func queueEnd(t *entry) time.Time {
	end := time.Now()
	for _, s := range t.Seasons {
		for _, e := range s.Episodes {
			if e.searchUntil.After(end) {
				end = e.searchUntil
			}
		}
	}
	return end
}

func (f *Fake) nextSearch(t *entry) *time.Time {
	if soonest := firstQueued(t); soonest != nil {
		return soonest
	}
	if !t.InLibrary || !t.monitored {
		return nil
	}
	if t.Kind == "movie" {
		if searchable(movieState(t)) && f.activeDownload(t.id(), "") == nil {
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
	if t.profile != "" {
		out.Profile = &t.profile
	}
	for _, s := range t.Seasons {
		so := seasonOut{Number: s.Number, Badge: f.seasonBadge(t, s), Episodes: []episodeOut{}}
		for _, e := range s.Episodes {
			st, p := f.epState(t, s, e)
			eo := episodeOut{ID: epID(s, e), Number: e.Number, Name: e.Name, AirDate: e.AirDate, Monitored: e.monitored, State: st}
			if st == "downloading" || st == "caching" {
				eo.Percent = &p
			}
			if sharers := sharedFile(s, e); len(sharers) > 1 {
				eo.SharedEpisodes = sharers
			}
			switch {
			case queued(e):
				eo.NextSearchAt = &e.searchAt
			case t.InLibrary && e.monitored && t.monitored && searchable(st):
				eo.NextSearchAt = &t.nextSearch
			}
			so.Episodes = append(so.Episodes, eo)
		}
		out.Seasons = append(out.Seasons, so)
	}
	return out
}

// sharedFile lists the downloaded episodes of the season in the same file
// as e, e included, or nothing for an episode with no file.
func sharedFile(s *season, e *episode) []string {
	if e.File == "" || e.State != "downloaded" {
		return nil
	}
	var out []string
	for _, o := range s.Episodes {
		if o.File == e.File && o.State == "downloaded" {
			out = append(out, epID(s, o))
		}
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
		Kind    string          `json:"kind"`
		TmdbID  int             `json:"tmdbId"`
		Profile json.RawMessage `json:"profile"`
		Monitor json.RawMessage `json:"monitor"`
	}
	if !decode(r, &req) || (req.Kind != "movie" && req.Kind != "series") || req.TmdbID <= 0 {
		fail(w, http.StatusBadRequest, "kind must be movie or series and tmdbId a positive number")
		return
	}
	monitor := "all"
	if req.Monitor != nil {
		monitor = ""
		if err := json.Unmarshal(req.Monitor, &monitor); err != nil || !slices.Contains([]string{"all", "future", "first_season", "latest_season", "none"}, monitor) {
			fail(w, http.StatusBadRequest, "monitor must be all, future, first_season, latest_season or none")
			return
		}
	}
	profile := ""
	if req.Profile != nil && string(bytes.TrimSpace(req.Profile)) != "null" {
		if json.Unmarshal(req.Profile, &profile) != nil || !f.knownProfile(profile) {
			fail(w, http.StatusBadRequest, "profile must be the name of a profile from GET /api/profiles")
			return
		}
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
	f.addToLibrary(t, profile, monitor)
	write(w, http.StatusCreated, f.title(t))
}

func (f *Fake) addToLibrary(t *entry, profile, monitor string) {
	if t.Kind == "movie" {
		monitor = "all"
	}
	t.InLibrary, t.monitored, t.profile = true, monitor != "none", profile
	t.nextSearch = time.Now().Add(defaultNextSearch)
	if t.Kind == "movie" {
		t.State = "wanted"
	}
	first, latest := 0, 0
	for _, s := range t.Seasons {
		if s.Number > 0 && (first == 0 || s.Number < first) {
			first = s.Number
		}
		latest = max(latest, s.Number)
	}
	for _, s := range t.Seasons {
		for _, e := range s.Episodes {
			setMonitored(e, monitor == "all" ||
				(monitor == "future" && e.State == "unaired") ||
				(monitor == "first_season" && s.Number > 0 && s.Number == first) ||
				(monitor == "latest_season" && s.Number > 0 && s.Number == latest))
		}
	}
	if t.monitored {
		f.addHistory("added", t, "", "Added and monitored.", "", false)
	} else {
		f.addHistory("added", t, "", "Added, nothing monitored.", "", false)
	}
}

// setMonitored sets an episode's monitoring and, as the real service does,
// reads an episode with no file as wanted while it is monitored and
// missing while it is not; an unaired or downloaded one keeps its state.
func setMonitored(e *episode, monitored bool) {
	if e.monitored == monitored && e.State != "missing" {
		return
	}
	e.monitored = monitored
	switch e.State {
	case "wanted", "not_found", "missing":
		e.State = "missing"
		if monitored {
			e.State = "wanted"
		}
	}
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
		Monitored *bool           `json:"monitored"`
		Profile   json.RawMessage `json:"profile"`
	}
	if !decode(r, &req) || (req.Monitored == nil && req.Profile == nil) {
		fail(w, http.StatusBadRequest, "send monitored, profile or both")
		return
	}
	profile, clear := "", string(req.Profile) == "null"
	if req.Profile != nil && !clear {
		if json.Unmarshal(req.Profile, &profile) != nil || !f.knownProfile(profile) {
			fail(w, http.StatusBadRequest, "profile must be the name of a profile from GET /api/profiles, or null")
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
		t.profile = ""
	} else if profile != "" {
		t.profile = profile
	}
	write(w, http.StatusOK, f.title(t))
}

func (f *Fake) remove(w http.ResponseWriter, r *http.Request) {
	if v := r.URL.Query().Get("deleteFiles"); v != "" && v != "true" && v != "false" {
		fail(w, http.StatusBadRequest, "deleteFiles must be true or false")
		return
	}
	t := f.libraryTitle(w, r)
	if t == nil {
		return
	}
	t.InLibrary, t.monitored, t.profile, t.searchUntil = false, false, "", time.Time{}
	if t.Kind == "movie" {
		t.State = "missing"
	}
	for _, s := range t.Seasons {
		for _, e := range s.Episodes {
			e.monitored, e.searchAt, e.searchUntil = true, time.Time{}, time.Time{}
			if e.State != "unaired" {
				e.State = "missing"
			}
		}
	}
	f.downloads = slices.DeleteFunc(f.downloads, func(d *download) bool { return d.TitleID == t.id() })
	w.WriteHeader(http.StatusNoContent)
}

// wrongFile serves both routes: a movie's, and an episode's with its id in the path.
func (f *Fake) wrongFile(w http.ResponseWriter, r *http.Request) {
	t := f.libraryTitle(w, r)
	if t == nil {
		return
	}
	var req struct {
		ReSearch *bool `json:"reSearch"`
	}
	if r.ContentLength != 0 && !decode(r, &req) {
		fail(w, http.StatusBadRequest, "body must be {\"reSearch\": true|false} or empty")
		return
	}
	reSearch := req.ReSearch == nil || *req.ReSearch
	which := r.PathValue("episodeId")
	var state *string
	switch {
	case which != "":
		e := f.findEpisode(t, which)
		if e == nil {
			fail(w, http.StatusNotFound, "no such episode")
			return
		}
		state = &e.State
	case t.Kind == "movie":
		state = &t.State
	default:
		fail(w, http.StatusBadRequest, "name the episode: use the episode's wrong-file route")
		return
	}
	if *state != "downloaded" {
		fail(w, http.StatusConflict, "there is no file to delete")
		return
	}
	*state = "missing"
	t.monitored = true
	if which != "" {
		asked := f.findEpisode(t, which)
		setMonitored(asked, true)
		for _, s := range t.Seasons {
			for _, e := range s.Episodes {
				if e != asked && asked.File != "" && e.File == asked.File && e.State == "downloaded" {
					e.State = "missing"
					setMonitored(e, true)
				}
			}
		}
	} else {
		*state = "wanted"
	}
	msg := "Wrong file: deleted the file. Pick another release."
	if reSearch {
		msg = "Wrong file: deleted the file. Searching for another release."
		var asked *episode
		if which != "" {
			asked = f.findEpisode(t, which)
		}
		f.startSearch(t, asked, time.Now())
	}
	f.addHistory("wrong_file", t, which, msg, "", false)
	write(w, http.StatusOK, f.title(t))
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

func (f *Fake) updateSeason(w http.ResponseWriter, r *http.Request) {
	t := f.libraryTitle(w, r)
	if t == nil {
		return
	}
	var season *season
	if n, err := strconv.Atoi(r.PathValue("number")); err == nil {
		for _, s := range t.Seasons {
			if s.Number == n {
				season = s
			}
		}
	}
	if season == nil {
		fail(w, http.StatusNotFound, "no such season")
		return
	}
	var req struct {
		Monitored *bool `json:"monitored"`
	}
	if !decode(r, &req) || req.Monitored == nil {
		fail(w, http.StatusBadRequest, "send monitored")
		return
	}
	for _, e := range season.Episodes {
		setMonitored(e, *req.Monitored)
	}
	if *req.Monitored {
		t.monitored = true
	}
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
		if e.State != "downloaded" && e.State != "unaired" {
			f.startSearch(t, e, time.Now())
		}
	} else if t.Kind == "movie" {
		if t.State != "downloaded" {
			f.startSearch(t, nil, time.Now())
		}
	} else {
		anyMonitored, at := false, queueEnd(t)
		for _, s := range t.Seasons {
			for _, e := range s.Episodes {
				anyMonitored = anyMonitored || (s.Number > 0 && e.monitored)
			}
		}
		for _, s := range t.Seasons {
			for _, e := range s.Episodes {
				if s.Number > 0 && e.State != "downloaded" && e.State != "unaired" && (e.monitored || !anyMonitored) && !queued(e) && !running(e.searchAt, e.searchUntil) {
					f.startSearch(t, e, at)
					at = e.searchUntil
				}
			}
		}
	}
	f.addHistory("searched", t, epID, "Search now: skipping the backoff.", "", false)
	write(w, http.StatusOK, f.title(t))
}

func (f *Fake) rescanTitle(w http.ResponseWriter, r *http.Request) {
	t := f.libraryTitle(w, r)
	if t == nil {
		return
	}
	write(w, http.StatusOK, f.title(t))
}

func (f *Fake) rescanLibrary(w http.ResponseWriter, _ *http.Request) {
	var health struct {
		LibraryProblems []json.RawMessage `json:"libraryProblems"`
	}
	_ = json.Unmarshal(f.health, &health)
	scanned := 0
	for _, t := range f.titles {
		if t.InLibrary {
			scanned++
		}
	}
	write(w, http.StatusOK, map[string]int{"titlesScanned": scanned, "adopted": 0, "foundOnDisk": 0, "missingOnDisk": 0, "problems": len(health.LibraryProblems)})
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
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Quality   string   `json:"quality"`
		Tags      []string `json:"tags"`
		SizeBytes int64    `json:"sizeBytes"`
		Source    string   `json:"source"`
		Cached    bool     `json:"cached"`
	}
	type dropped struct {
		Release release `json:"release"`
		Reason  string  `json:"reason"`
		Message string  `json:"message"`
	}
	candidates, drops, uncached := []release{}, []dropped{}, []release{}
	for _, x := range f.releases[t.Kind] {
		rel := release{x.ID, x.Name, x.Quality, nonNil(x.Tags), x.SizeBytes, x.Source, x.Cached}
		if ep := r.URL.Query().Get("episode"); len(ep) == 6 {
			rel.Name = strings.ReplaceAll(strings.ReplaceAll(rel.Name, "S04E03", "S"+strings.ToUpper(ep[1:3])+"E"+nextEp(ep[4:])), "S04E02", strings.ToUpper(ep))
		}
		switch {
		case x.Drop == "not_cached" && f.opts.TorBox:
			uncached = append(uncached, rel)
		case x.Drop == "" || (x.Drop == "profile_rejected" && t.profile == openProfile):
			candidates = append(candidates, rel)
		default:
			drops = append(drops, dropped{rel, x.Drop, x.Message})
		}
	}
	write(w, http.StatusOK, map[string]any{"candidates": append(candidates, uncached...), "dropped": drops})
}

var ungrabbable = []string{"not_cached", "not_video", "too_small", "wrong_episode"}

func (f *Fake) grab(w http.ResponseWriter, r *http.Request) {
	t := f.knownTitle(w, r)
	if t == nil {
		return
	}
	var req struct {
		Episode string `json:"episode"`
		Replace bool   `json:"replace"`
	}
	if r.ContentLength != 0 && !decode(r, &req) {
		fail(w, http.StatusBadRequest, "body must be {\"episode\": \"sNNeNN\", \"replace\": true|false} or empty")
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
	if slices.Contains(ungrabbable, rel.Drop) && (rel.Drop != "not_cached" || !f.opts.TorBox) {
		fail(w, http.StatusConflict, "this release cannot be grabbed: "+rel.Message)
		return
	}
	if t.Kind == "movie" {
		req.Episode = ""
	} else if f.findEpisode(t, req.Episode) == nil {
		fail(w, http.StatusBadRequest, "episode must name an episode of this series")
		return
	}
	if t.InLibrary && !req.Replace && f.hasFile(t, req.Episode) {
		fail(w, http.StatusConflict, "this item already has a file; pass replace: true to replace it")
		return
	}
	if !t.InLibrary {
		f.addToLibrary(t, "", "all")
	}
	d := f.activeDownload(t.id(), req.Episode)
	if d == nil {
		state := "queued"
		if !rel.Cached {
			state = "caching"
		}
		d = &download{ID: f.newID("dl"), TitleID: t.id(), Episode: req.Episode, ReleaseName: rel.Name, BytesTotal: rel.SizeBytes, State: state}
		f.downloads = append(f.downloads, d)
	}
	write(w, http.StatusAccepted, f.downloadOut(d))
}

func (f *Fake) hasFile(t *entry, ep string) bool {
	if t.Kind == "movie" {
		return t.State == "downloaded"
	}
	e := f.findEpisode(t, ep)
	return e != nil && e.State == "downloaded"
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
	limit, ok := pageParam(r, "limit", 1000, 1, 1000)
	if !ok {
		fail(w, http.StatusBadRequest, "limit must be a number from 1 to 1000")
		return
	}
	if c := r.URL.Query().Get("cursor"); c != "" {
		seq, err := strconv.Atoi(c)
		if err != nil || seq < 1 || strconv.Itoa(seq) != c {
			fail(w, http.StatusBadRequest, "cursor is not one this server gave")
			return
		}
		out = slices.DeleteFunc(slices.Clone(out), func(h *historyEntry) bool { return h.seq >= seq })
	}
	body := map[string]any{}
	if len(out) > limit {
		out = out[:limit]
		body["nextCursor"] = strconv.Itoa(out[limit-1].seq)
	}
	body["history"] = append([]*historyEntry{}, out...)
	write(w, http.StatusOK, body)
}

func pageParam(r *http.Request, name string, def, lo, hi int) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	return n, err == nil && n >= lo && n <= hi
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
		if e := f.findEpisode(t, h.Episode); e != nil && e.State != "downloaded" {
			f.startSearch(t, e, time.Now())
		}
	} else if t.State != "downloaded" {
		f.startSearch(t, nil, time.Now())
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

func nextEp(n string) string {
	v, _ := strconv.Atoi(n)
	return fmt.Sprintf("%02d", v+1)
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
