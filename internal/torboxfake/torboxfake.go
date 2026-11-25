// Package torboxfake is a stand-in for TorBox's REST API, for tests that
// must never reach the real network. Tests drive a torrent's caching by hand
// (Step, Stall, Present), script failures per path, and read back what was
// asked of it (Magnets, Deleted, Hits).
package torboxfake

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Key is the API key the fake accepts.
const Key = "torbox-test-key"

type File struct {
	ID   int
	Name string // full path inside the torrent, as TorBox reports it
	Size int64
	// Body is what the file's download link serves; empty serves nothing.
	Body []byte
}

// Step scripts one answer: a status, an optional Retry-After, or a hang
// that holds the request until the fake closes.
type Step struct {
	Status     int
	RetryAfter string
	Hang       bool
}

type rule struct {
	prefix  string
	steps   []Step
	forever bool
}

type torrent struct {
	id       int
	hash     string
	files    []File
	progress float64
	speed    int64
	present  bool
	stalled  bool
}

type Fake struct {
	srv *httptest.Server

	mu      sync.Mutex
	nextID  int
	byID    map[int]*torrent
	files   map[string][]File
	cached  map[string]bool
	magnets []string
	deleted []int
	rules   []*rule
	hits    map[string]int
	release chan struct{}
}

func New() *Fake {
	f := &Fake{
		nextID:  1,
		byID:    map[int]*torrent{},
		files:   map[string][]File{},
		cached:  map[string]bool{},
		hits:    map[string]int{},
		release: make(chan struct{}),
	}
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

func (f *Fake) APIBase() string { return f.srv.URL + "/v1/api" }

// SetFiles fixes the file list a torrent with this info hash will have, for
// example the episodes of a season pack. Without it a torrent has one file.
func (f *Fake) SetFiles(hash string, files ...File) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[strings.ToLower(hash)] = files
}

// Cached makes TorBox already hold this info hash, so a torrent created
// from it is present at once.
func (f *Fake) Cached(hash string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cached[strings.ToLower(hash)] = true
}

// Step sets a torrent's caching progress (0..1) and speed in bytes a second.
func (f *Fake) Step(id int, progress float64, speed int64) {
	f.with(id, func(t *torrent) {
		t.progress, t.speed, t.stalled = progress, speed, false
	})
}

// Stall keeps a torrent's progress and drops its speed to zero.
func (f *Fake) Stall(id int) {
	f.with(id, func(t *torrent) { t.speed, t.stalled = 0, true })
}

// Present finishes caching: the torrent is complete and its files can be
// downloaded.
func (f *Fake) Present(id int) {
	f.with(id, func(t *torrent) { t.progress, t.speed, t.present, t.stalled = 1, 0, true, false })
}

func (f *Fake) with(id int, fn func(*torrent)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.byID[id]
	if !ok {
		panic(fmt.Sprintf("torboxfake: no torrent %d", id))
	}
	fn(t)
}

// Script answers the next requests whose path (below /v1/api) starts with
// prefix with steps, then goes back to normal.
func (f *Fake) Script(prefix string, steps ...Step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, &rule{prefix: prefix, steps: steps})
}

// Down answers every request whose path starts with prefix with status
// until Heal; an empty prefix is every request.
func (f *Fake) Down(prefix string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, &rule{prefix: prefix, steps: []Step{{Status: status}}, forever: true})
}

// Reject answers every request as if the key were refused.
func (f *Fake) Reject() { f.Down("", http.StatusUnauthorized) }

func (f *Fake) Heal() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = nil
}

// Hits counts the requests seen so far whose path (below /v1/api) starts
// with prefix.
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

// Magnets lists the magnet links createtorrent received, in order.
func (f *Fake) Magnets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.magnets...)
}

// Deleted lists the torrent ids a delete was received for, in order.
func (f *Fake) Deleted() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.deleted...)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	if tail, ok := strings.CutPrefix(r.URL.Path, "/dl/"); ok {
		f.download(w, r, tail)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/v1/api")

	f.mu.Lock()
	f.hits[p]++
	var step *Step
	for _, ru := range f.rules {
		if strings.HasPrefix(p, ru.prefix) && len(ru.steps) > 0 {
			s := ru.steps[0]
			if !ru.forever {
				ru.steps = ru.steps[1:]
			}
			step = &s
			break
		}
	}
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
		if step.RetryAfter != "" {
			w.Header().Set("Retry-After", step.RetryAfter)
		}
		http.Error(w, "scripted", step.Status)
		return
	}

	if r.Header.Get("Authorization") != "Bearer "+Key {
		fail(w, http.StatusUnauthorized, "BAD_TOKEN")
		return
	}
	f.api(w, r, p)
}

func (f *Fake) api(w http.ResponseWriter, r *http.Request, p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && p == "/user/me":
		ok(w, map[string]any{"id": 1, "email": "owner@example.invalid", "plan": 2})

	case r.Method == http.MethodPost && p == "/torrents/createtorrent":
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			fail(w, http.StatusBadRequest, "BAD_REQUEST")
			return
		}
		magnet := r.FormValue("magnet")
		hash, found := strings.CutPrefix(magnet, "magnet:?xt=urn:btih:")
		if !found || hash == "" {
			fail(w, http.StatusBadRequest, "INVALID_MAGNET")
			return
		}
		f.magnets = append(f.magnets, magnet)
		hash = strings.ToLower(hash)
		// TorBox answers a hash the account already holds with that torrent.
		var t *torrent
		for _, have := range f.byID {
			if have.hash == hash && (t == nil || have.id < t.id) {
				t = have
			}
		}
		if t == nil {
			t = f.add(hash)
		}
		ok(w, map[string]any{"torrent_id": t.id, "hash": t.hash, "name": t.hash})

	case r.Method == http.MethodGet && p == "/torrents/mylist":
		if raw := q.Get("id"); raw != "" {
			id, _ := strconv.Atoi(raw)
			t, found := f.byID[id]
			if !found {
				fail(w, http.StatusNotFound, "NO_TORRENT")
				return
			}
			ok(w, t.json())
			return
		}
		list := []map[string]any{}
		for id := 1; id < f.nextID; id++ {
			if t, found := f.byID[id]; found {
				list = append(list, t.json())
			}
		}
		ok(w, list)

	case r.Method == http.MethodGet && p == "/torrents/requestdl":
		// TorBox documents requestdl's key as a "token" query parameter, so
		// its permalinks work outside an authenticated client.
		if q.Get("token") != Key {
			fail(w, http.StatusUnauthorized, "BAD_TOKEN")
			return
		}
		id, _ := strconv.Atoi(q.Get("torrent_id"))
		fileID, _ := strconv.Atoi(q.Get("file_id"))
		t, found := f.byID[id]
		if !found {
			fail(w, http.StatusNotFound, "NO_TORRENT")
			return
		}
		if !t.present {
			fail(w, http.StatusBadRequest, "DOWNLOAD_NOT_PRESENT")
			return
		}
		for _, file := range t.files {
			if file.ID == fileID {
				ok(w, fmt.Sprintf("%s/dl/%d-%d", f.srv.URL, id, fileID))
				return
			}
		}
		fail(w, http.StatusNotFound, "NO_FILE")

	case r.Method == http.MethodPost && p == "/torrents/controltorrent":
		var body struct {
			TorrentID int    `json:"torrent_id"`
			Operation string `json:"operation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Operation != "delete" {
			fail(w, http.StatusBadRequest, "BAD_REQUEST")
			return
		}
		f.deleted = append(f.deleted, body.TorrentID)
		if _, found := f.byID[body.TorrentID]; !found {
			fail(w, http.StatusNotFound, "NO_TORRENT")
			return
		}
		delete(f.byID, body.TorrentID)
		ok(w, nil)

	default:
		fail(w, http.StatusNotFound, "NO_ROUTE")
	}
}

// add must be called with f.mu held.
func (f *Fake) add(hash string) *torrent {
	files, set := f.files[hash]
	if !set {
		files = []File{{ID: 0, Name: hash + ".mkv", Size: 1000}}
	}
	t := &torrent{id: f.nextID, hash: hash, files: files}
	if f.cached[hash] {
		t.progress, t.present = 1, true
	}
	f.nextID++
	f.byID[t.id] = t
	return t
}

func (f *Fake) download(w http.ResponseWriter, r *http.Request, tail string) {
	idPart, filePart, _ := strings.Cut(tail, "-")
	id, _ := strconv.Atoi(idPart)
	fileID, _ := strconv.Atoi(filePart)
	f.mu.Lock()
	var body []byte
	found := false
	if t, ok := f.byID[id]; ok && t.present {
		for _, file := range t.files {
			if file.ID == fileID {
				body, found = file.Body, true
			}
		}
	}
	f.mu.Unlock()
	if !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}

func (t *torrent) json() map[string]any {
	files := []map[string]any{}
	for _, file := range t.files {
		files = append(files, map[string]any{"id": file.ID, "name": file.Name, "short_name": path.Base(file.Name), "size": file.Size})
	}
	state := "metaDL"
	switch {
	case t.present:
		state = "cached"
	case t.stalled:
		state = "stalled (no seeds)"
	case t.progress > 0:
		state = "downloading"
	}
	return map[string]any{
		"id": t.id, "hash": t.hash, "name": t.hash,
		"progress": t.progress, "download_speed": t.speed,
		"download_present": t.present, "download_finished": t.present,
		"active": !t.present, "download_state": state, "files": files,
	}
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "error": nil, "detail": "ok", "data": data})
}

func fail(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"success": false, "error": code, "detail": code, "data": nil})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Println("torboxfake:", err)
	}
}
