// Package tmdbfake is a stand-in for TMDB's v3 API and its image host, for
// tests that must never reach the real network. It counts the requests it
// sees and can be told to fail, rate-limit or hang on a path.
package tmdbfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Key is the API key the fake accepts.
const Key = "tmdb-test-key"

type Episode struct {
	Number  int
	Name    string
	AirDate string // empty: announced without a date, which TMDB reports as null
}

type Season struct {
	Number   int
	Episodes []Episode
}

type Title struct {
	Kind     string // "movie" or "series"
	ID       int
	Name     string
	Date     string // release or first air date, YYYY-MM-DD
	Overview string
	Poster   string // TMDB file path such as /abc.jpg; empty for none
	Backdrop string
	IMDb     string
	TVDB     int
	Seasons  []Season
}

// Step scripts one answer: a status, an optional Retry-After, a hang that
// holds the request until the fake closes, or, for an image, 200 headers
// and a first few bytes followed by a stall.
type Step struct {
	Status     int
	RetryAfter string
	Hang       bool
	StallBody  bool
}

type rule struct {
	prefix  string
	steps   []Step
	forever bool
}

type Fake struct {
	srv *httptest.Server

	mu         sync.Mutex
	titles     []Title
	trending   []int
	rules      []*rule
	hits       map[string]int
	imageDelay time.Duration
	release    chan struct{}
}

func New() *Fake {
	f := &Fake{hits: map[string]int{}, release: make(chan struct{})}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *Fake) Close() {
	f.mu.Lock()
	select {
	case <-f.release:
	default:
		close(f.release)
	}
	f.mu.Unlock()
	f.srv.Close()
}

func (f *Fake) APIBase() string   { return f.srv.URL + "/3" }
func (f *Fake) ImageBase() string { return f.srv.URL + "/t/p" }

func (f *Fake) Add(t Title) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.titles = append(f.titles, t)
}

// SetTrending lists the TMDB ids, in order, that trending returns.
func (f *Fake) SetTrending(ids ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trending = ids
}

// Script answers the next requests whose path starts with prefix with steps,
// then goes back to normal.
func (f *Fake) Script(prefix string, steps ...Step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, &rule{prefix: prefix, steps: steps})
}

// Down answers every request whose path starts with prefix with status until Heal.
func (f *Fake) Down(prefix string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, &rule{prefix: prefix, steps: []Step{{Status: status}}, forever: true})
}

func (f *Fake) Heal() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = nil
}

func (f *Fake) SetImageDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imageDelay = d
}

// Hits counts the requests seen so far whose path starts with prefix, for
// example "/3/search" or "/t/p/".
func (f *Fake) Hits(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for p, c := range f.hits {
		if strings.HasPrefix(p, prefix) {
			n += c
		}
	}
	return n
}

// ImageBytes is what the fake serves for a poster or backdrop path.
func ImageBytes(path string) []byte {
	return append(append([]byte{0xff, 0xd8, 0xff, 0xe0}, path...), 0xff, 0xd9)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	var step *Step
	for _, ru := range f.rules {
		if strings.HasPrefix(r.URL.Path, ru.prefix) && len(ru.steps) > 0 {
			s := ru.steps[0]
			if !ru.forever {
				ru.steps = ru.steps[1:]
			}
			step = &s
			break
		}
	}
	delay := f.imageDelay
	release := f.release
	f.mu.Unlock()

	if step != nil {
		if step.Hang {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		if step.StallBody {
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte{0xff, 0xd8})
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		if step.RetryAfter != "" {
			w.Header().Set("Retry-After", step.RetryAfter)
		}
		http.Error(w, "scripted", step.Status)
		return
	}

	if rest, ok := strings.CutPrefix(r.URL.Path, "/t/p/"); ok {
		time.Sleep(delay)
		_, path, _ := strings.Cut(rest, "/")
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(ImageBytes("/" + path))
		return
	}

	if r.URL.Query().Get("api_key") != Key {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"status_message": "Invalid API key"})
		return
	}
	f.api(w, r)
}

func (f *Fake) api(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/3"), "/"), "/")
	switch {
	case len(parts) == 2 && parts[0] == "search" && parts[1] == "multi":
		q := strings.ToLower(r.URL.Query().Get("query"))
		var out []map[string]any
		for _, t := range f.titles {
			if strings.Contains(strings.ToLower(t.Name), q) {
				out = append(out, f.summary(t, true))
			}
		}
		writeJSON(w, 200, map[string]any{"results": append(out, map[string]any{"id": 1, "media_type": "person", "name": q})})
	case len(parts) == 3 && parts[0] == "trending":
		var out []map[string]any
		for _, id := range f.trending {
			for _, t := range f.titles {
				if t.ID == id {
					out = append(out, f.summary(t, true))
				}
			}
		}
		writeJSON(w, 200, map[string]any{"results": out})
	case len(parts) == 2 && (parts[0] == "movie" || parts[0] == "tv"):
		t, ok := f.find(parts[0], parts[1])
		if !ok {
			writeJSON(w, 404, map[string]any{"status_message": "not found"})
			return
		}
		s := f.summary(t, false)
		ext := map[string]any{"imdb_id": nilIfEmpty(t.IMDb)}
		if t.Kind == "series" {
			ext["tvdb_id"] = t.TVDB
			var seasons []map[string]any
			for _, se := range t.Seasons {
				seasons = append(seasons, map[string]any{"season_number": se.Number})
			}
			s["seasons"] = seasons
		}
		if r.URL.Query().Get("append_to_response") == "external_ids" {
			s["external_ids"] = ext
		}
		writeJSON(w, 200, s)
	case len(parts) == 4 && parts[0] == "tv" && parts[2] == "season":
		t, ok := f.find("tv", parts[1])
		n, _ := strconv.Atoi(parts[3])
		if ok {
			for _, se := range t.Seasons {
				if se.Number == n {
					var eps []map[string]any
					for _, e := range se.Episodes {
						eps = append(eps, map[string]any{"episode_number": e.Number, "name": e.Name, "air_date": nilIfEmpty(e.AirDate)})
					}
					writeJSON(w, 200, map[string]any{"season_number": n, "episodes": eps})
					return
				}
			}
		}
		writeJSON(w, 404, map[string]any{"status_message": "not found"})
	default:
		writeJSON(w, 404, map[string]any{"status_message": "no such route"})
	}
}

func (f *Fake) find(path, id string) (Title, bool) {
	kind := map[string]string{"movie": "movie", "tv": "series"}[path]
	n, _ := strconv.Atoi(id)
	for _, t := range f.titles {
		if t.Kind == kind && t.ID == n {
			return t, true
		}
	}
	return Title{}, false
}

func (f *Fake) summary(t Title, withType bool) map[string]any {
	m := map[string]any{"id": t.ID, "overview": t.Overview, "poster_path": nilIfEmpty(t.Poster), "backdrop_path": nilIfEmpty(t.Backdrop)}
	if t.Kind == "movie" {
		m["title"], m["release_date"] = t.Name, t.Date
		if withType {
			m["media_type"] = "movie"
		}
	} else {
		m["name"], m["first_air_date"] = t.Name, t.Date
		if withType {
			m["media_type"] = "tv"
		}
	}
	return m
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Println("tmdbfake:", err)
	}
}
