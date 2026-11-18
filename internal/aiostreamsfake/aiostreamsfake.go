// Package aiostreamsfake is a stand-in for an AIOStreams instance's search
// API, for tests that must never reach the real network. It records the
// searches it receives and can be told to rate-limit, refuse the
// credentials, fail or hang.
package aiostreamsfake

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
)

// User and Password are the Basic-auth pair the fake accepts, and the two
// segments of its manifest URL.
const (
	User     = "00000000-0000-4000-8000-000000000001"
	Password = "test-password"
)

//go:embed fixtures/*.json
var fixtures embed.FS

// Fixture returns a committed response body by file name.
func Fixture(name string) []byte {
	b, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		panic("aiostreamsfake: no fixture " + name)
	}
	return b
}

// Step scripts one answer to every search: a bare status, or a hang that
// holds the request until the fake closes.
type Step struct {
	Status int
	Hang   bool
}

type Fake struct {
	srv     *httptest.Server
	release chan struct{}

	mu       sync.Mutex
	answers  map[string][]byte
	searches []Search
	always   *Step
}

// Search is one request the fake received.
type Search struct{ Type, ID string }

func New() *Fake {
	f := &Fake{answers: map[string][]byte{}, release: make(chan struct{})}
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

// ManifestURL is what MARQUEE_AIOSTREAMS_MANIFEST_URL is set to.
func (f *Fake) ManifestURL() string {
	return f.srv.URL + "/stremio/" + User + "/" + Password + "/manifest.json"
}

// Host is the fake's address, for tests that check nothing leaks it.
func (f *Fake) Host() string { u, _ := url.Parse(f.srv.URL); return u.Host }

// Answer sets the response body for searches of a type ("movie" or
// "series") and Stremio id. An unset search answers no results.
func (f *Fake) Answer(typ, id string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[typ+"|"+id] = body
}

// Fail makes every search answer with the step instead of the body.
func (f *Fake) Fail(s Step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.always = &s
}

// Heal undoes Fail.
func (f *Fake) Heal() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.always = nil
}

// Searches lists the requests received, in order.
func (f *Fake) Searches() []Search {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Search(nil), f.searches...)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/search" {
		http.NotFound(w, r)
		return
	}
	if u, p, ok := r.BasicAuth(); !ok || u != User || p != Password {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"error":{"code":"UNAUTHORIZED","message":"invalid credentials"}}`))
		return
	}
	q := r.URL.Query()
	typ, id := q.Get("type"), q.Get("id")
	f.mu.Lock()
	f.searches = append(f.searches, Search{typ, id})
	step := f.always
	body, ok := f.answers[typ+"|"+id]
	f.mu.Unlock()
	if step != nil {
		if step.Hang {
			select {
			case <-f.release:
			case <-r.Context().Done():
			}
			return
		}
		w.WriteHeader(step.Status)
		return
	}
	if !ok {
		body = Fixture("empty.json")
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
