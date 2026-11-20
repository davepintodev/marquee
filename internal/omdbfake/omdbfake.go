// Package omdbfake is a stand-in for OMDb's API, for tests that must never
// reach the real network. It counts the requests it sees and can be told to
// fail or hang.
package omdbfake

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
)

// Key is the API key the fake accepts.
const Key = "omdb-test-key"

// Entry is what OMDb knows of one title.
type Entry struct {
	// IMDb is the 0-10 rating and IMDbVotes its voter count, as OMDb prints it ("1,234,567").
	IMDb      string
	IMDbVotes string
	Tomatoes  string // such as "91%"; empty when OMDb has no Rotten Tomatoes score
}

type Fake struct {
	srv *httptest.Server

	mu      sync.Mutex
	entries map[string]Entry
	status  int
	refusal string
	hang    bool
	hits    int
	release chan struct{}
}

func New() *Fake {
	f := &Fake{entries: map[string]Entry{}, release: make(chan struct{})}
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

func (f *Fake) URL() string { return f.srv.URL }

// Add teaches the fake the scores of an IMDb id.
func (f *Fake) Add(imdbID string, e Entry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[imdbID] = e
}

// Down answers every request with status until Heal.
func (f *Fake) Down(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

// Quota answers every request as OMDb does once the day's free calls are used up.
func (f *Fake) Quota() { f.refuse("Request limit reached!") }

// RefuseKey answers every request as OMDb does to a key it does not know.
func (f *Fake) RefuseKey() { f.refuse("Invalid API key!") }

func (f *Fake) refuse(message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refusal = message
}

// Hang holds every request until the fake closes.
func (f *Fake) Hang() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hang = true
}

func (f *Fake) Heal() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.refusal, f.hang = 0, "", false
}

// Hits counts the requests seen so far.
func (f *Fake) Hits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits++
	status, refusal, hang, release := f.status, f.refusal, f.hang, f.release
	e, known := f.entries[r.URL.Query().Get("i")]
	f.mu.Unlock()

	if hang {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		return
	}
	if status != 0 {
		http.Error(w, "scripted", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if refusal != "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"Response": "False", "Error": refusal})
		return
	}
	if r.URL.Query().Get("apikey") != Key {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"Response": "False", "Error": "Invalid API key!"})
		return
	}
	if !known {
		_ = json.NewEncoder(w).Encode(map[string]any{"Response": "False", "Error": "Incorrect IMDb ID."})
		return
	}
	ratings := []map[string]string{}
	out := map[string]any{"Response": "True", "imdbRating": "N/A", "imdbVotes": "N/A"}
	if e.IMDb != "" {
		out["imdbRating"], out["imdbVotes"] = e.IMDb, e.IMDbVotes
		ratings = append(ratings, map[string]string{"Source": "Internet Movie Database", "Value": e.IMDb + "/10"})
	}
	if e.Tomatoes != "" {
		ratings = append(ratings, map[string]string{"Source": "Rotten Tomatoes", "Value": e.Tomatoes})
	}
	out["Ratings"] = ratings
	_ = json.NewEncoder(w).Encode(out)
}
