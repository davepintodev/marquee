// Package subfitfake is a subfit for tests: it records the webhooks marquee
// sends to /hook/marquee and can be told to fail or answer slowly.
package subfitfake

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// Hook is one webhook the fake received.
type Hook struct {
	Event        string `json:"event"`
	Library      string `json:"library"`
	Path         string `json:"path"`
	PreviousPath string `json:"previousPath"`
	// URLPath and ContentType are how the request arrived.
	URLPath, ContentType string
	// Fields are the JSON keys the body carried.
	Fields []string
}

type Server struct {
	URL string
	srv *httptest.Server

	mu     sync.Mutex
	calls  int
	hooks  []Hook
	status int
	delay  time.Duration
	quit   chan struct{}
	once   sync.Once
}

func New() *Server {
	s := &Server{quit: make(chan struct{})}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	s.URL = s.srv.URL
	return s
}

func (s *Server) Close() {
	s.once.Do(func() {
		close(s.quit)
		s.srv.Close()
	})
}

// Calls is the number of requests received, whatever they were answered.
func (s *Server) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// Hooks returns the webhooks received, in arrival order.
func (s *Server) Hooks() []Hook {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Hook(nil), s.hooks...)
}

// FailWith makes every webhook answer status; zero heals the server.
func (s *Server) FailWith(status int) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

// Delay makes every webhook wait d before it is answered.
func (s *Server) Delay(d time.Duration) {
	s.mu.Lock()
	s.delay = d
	s.mu.Unlock()
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	var raw map[string]json.RawMessage
	h := Hook{URLPath: r.URL.Path, ContentType: r.Header.Get("Content-Type")}
	if err := json.NewDecoder(r.Body).Decode(&raw); err == nil {
		for k, v := range raw {
			h.Fields = append(h.Fields, k)
			var str string
			_ = json.Unmarshal(v, &str)
			switch k {
			case "event":
				h.Event = str
			case "library":
				h.Library = str
			case "path":
				h.Path = str
			case "previousPath":
				h.PreviousPath = str
			}
		}
	}
	s.mu.Lock()
	s.calls++
	s.hooks = append(s.hooks, h)
	status, delay := s.status, s.delay
	s.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		case <-s.quit:
			return
		}
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"queued"}`))
}
