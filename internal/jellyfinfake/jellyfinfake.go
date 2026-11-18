// Package jellyfinfake is a Jellyfin for tests: it records library refresh
// calls and can be told to fail.
package jellyfinfake

import (
	"net/http"
	"net/http/httptest"
	"sync"
)

// Key is the API key the fake accepts.
const Key = "jellyfin-SECRETkey"

type Server struct {
	URL string
	srv *httptest.Server

	mu      sync.Mutex
	refresh int
	rejects int
	status  int
	auth    []string
}

func New() *Server {
	s := &Server{}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	s.URL = s.srv.URL
	return s
}

func (s *Server) Close() { s.srv.Close() }

// Refreshes is the number of library refreshes accepted.
func (s *Server) Refreshes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refresh
}

// Calls is the number of refresh calls, accepted or not.
func (s *Server) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refresh + s.rejects
}

// Authorization returns the Authorization header of every call.
func (s *Server) Authorization() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auth...)
}

// FailWith makes every refresh answer status; zero heals the server.
func (s *Server) FailWith(status int) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/Library/Refresh" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	if s.status != 0 {
		s.rejects++
		w.WriteHeader(s.status)
		return
	}
	if r.Header.Get("Authorization") != `MediaBrowser Token="`+Key+`"` {
		s.rejects++
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.refresh++
	w.WriteHeader(http.StatusNoContent)
}
