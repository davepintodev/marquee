// Package debridfake is a debrid file server for tests: a playback link that
// redirects to a CDN path, range support, and files that stall, truncate or
// are not video. It records what it was asked for.
package debridfake

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MKV returns n deterministic bytes that start like a Matroska file.
func MKV(n int) []byte {
	b := make([]byte, n)
	r := rand.New(rand.NewPCG(uint64(n), 7))
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	copy(b, []byte{0x1A, 0x45, 0xDF, 0xA3})
	return b
}

// File is one file the server holds and how it misbehaves.
type File struct {
	Data []byte
	// StallAt: a response that reaches this offset stops sending and hangs.
	StallAt int64
	// TruncateAt: a response that reaches this offset is cut off, while its
	// headers promised more.
	TruncateAt int64
	// Pace delays every 32 KiB chunk, to keep a download in flight.
	Pace time.Duration
	// NoRanges makes the server ignore Range headers.
	NoRanges bool
}

// Request is one request the server saw.
type Request struct {
	Path        string
	RangeStart  int64 // -1 without a Range header
	RangeEnd    int64
	Status      int
	Probe       bool // the one-byte range request that resolves the redirect
	Playback    bool // asked on the playback link rather than the CDN path
	ConnsAtOpen int
}

// Server is the fake. Playback URLs carry the marker "SECRETPLAY" and CDN
// paths "SECRETCDN", so tests can check they never leak.
type Server struct {
	srv *httptest.Server

	mu         sync.Mutex
	files      map[string]*File // by name
	paths      map[string]string
	reqs       []Request
	inflight   map[string]int // streaming responses per file name
	maxPerFile map[string]int
	maxFiles   int
	limit      int
	retryAfter string
	closing    chan struct{}
}

// New starts the server. Call Close when done.
func New() *Server {
	s := &Server{
		files: map[string]*File{}, paths: map[string]string{},
		inflight: map[string]int{}, maxPerFile: map[string]int{}, closing: make(chan struct{}),
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *Server) Close() {
	close(s.closing)
	s.srv.CloseClientConnections()
	s.srv.Close()
}

// Add registers a file under name and returns its playback URL.
func (s *Server) Add(name string, f File) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = &f
	s.paths["/cdn/SECRETCDN-"+name] = name
	return s.srv.URL + "/play/SECRETPLAY-" + name
}

// Limit makes the server answer 429 to a streaming request while n others
// are streaming; zero disables it. retryAfter is the header's value, if any.
func (s *Server) Limit(n int, retryAfter string) {
	s.mu.Lock()
	s.limit, s.retryAfter = n, retryAfter
	s.mu.Unlock()
}

// Requests returns a copy of what the server saw.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

// LowestStart is the lowest range start asked of the CDN path for name,
// ignoring the one-byte probes; -1 when it was never asked.
func (s *Server) LowestStart(name string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	low := int64(-1)
	for _, r := range s.reqs {
		if r.Probe || r.Playback || !strings.HasSuffix(r.Path, name) || r.RangeStart < 0 {
			continue
		}
		if low < 0 || r.RangeStart < low {
			low = r.RangeStart
		}
	}
	return low
}

// MaxConcurrent is the most responses streaming at once for name.
func (s *Server) MaxConcurrent(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxPerFile[name]
}

// MaxFiles is the most different files streaming at once.
func (s *Server) MaxFiles() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxFiles
}

// Streaming is the number of responses being sent right now.
func (s *Server) Streaming() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.inflight {
		n += c
	}
	return n
}

func (s *Server) record(r Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r)
	s.mu.Unlock()
}

func parseRange(h string, size int64) (start, end int64, ok bool) {
	if !strings.HasPrefix(h, "bytes=") {
		return 0, 0, false
	}
	a, b, found := strings.Cut(strings.TrimPrefix(h, "bytes="), "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	end = size - 1
	if b != "" {
		if end, err = strconv.ParseInt(b, 10, 64); err != nil {
			return 0, 0, false
		}
	}
	return start, min(end, size-1), start <= end
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	rangeHdr := r.Header.Get("Range")
	if name, ok := strings.CutPrefix(r.URL.Path, "/play/SECRETPLAY-"); ok {
		s.mu.Lock()
		_, known := s.files[name]
		s.mu.Unlock()
		rec := Request{Path: r.URL.Path, RangeStart: -1, Playback: true, Probe: rangeHdr == "bytes=0-0"}
		if !known {
			rec.Status = http.StatusNotFound
			s.record(rec)
			http.NotFound(w, r)
			return
		}
		rec.Status = http.StatusFound
		s.record(rec)
		http.Redirect(w, r, "/cdn/SECRETCDN-"+name, http.StatusFound)
		return
	}
	s.mu.Lock()
	name, ok := s.paths[r.URL.Path]
	f := s.files[name]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	size := int64(len(f.Data))
	rec := Request{Path: r.URL.Path, RangeStart: -1, RangeEnd: -1}
	start, end := int64(0), size-1
	ranged := false
	if !f.NoRanges {
		if a, b, ok := parseRange(rangeHdr, size); ok {
			start, end, ranged = a, b, true
			rec.RangeStart, rec.RangeEnd = a, b
			rec.Probe = a == 0 && b == 0
		}
	}

	if !rec.Probe {
		s.mu.Lock()
		over := s.limit > 0 && s.streamingLocked() >= s.limit
		if !over {
			s.inflight[name]++
			s.maxPerFile[name] = max(s.maxPerFile[name], s.inflight[name])
			files := 0
			for _, c := range s.inflight {
				if c > 0 {
					files++
				}
			}
			s.maxFiles = max(s.maxFiles, files)
		}
		retryAfter := s.retryAfter
		s.mu.Unlock()
		if over {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			rec.Status = http.StatusTooManyRequests
			s.record(rec)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		defer func() {
			s.mu.Lock()
			s.inflight[name]--
			s.mu.Unlock()
		}()
	}

	w.Header().Set("Accept-Ranges", "bytes")
	if ranged {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		rec.Status = http.StatusPartialContent
	} else {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusOK)
		rec.Status = http.StatusOK
	}
	s.record(rec)

	flush, _ := w.(http.Flusher)
	for pos := start; pos <= end; {
		n := min(32<<10, end-pos+1)
		if f.TruncateAt > 0 && pos+n > f.TruncateAt {
			n = max(f.TruncateAt-pos, 0)
		}
		if f.StallAt > 0 && pos+n > f.StallAt {
			n = max(f.StallAt-pos, 0)
		}
		if n > 0 {
			if _, err := w.Write(f.Data[pos : pos+n]); err != nil {
				return
			}
			if flush != nil {
				flush.Flush()
			}
			pos += n
		}
		switch {
		case f.TruncateAt > 0 && pos >= f.TruncateAt && pos <= end:
			panic(http.ErrAbortHandler)
		case f.StallAt > 0 && pos >= f.StallAt && pos <= end:
			s.hang(r.Context())
			return
		}
		if f.Pace > 0 {
			select {
			case <-time.After(f.Pace):
			case <-r.Context().Done():
				return
			case <-s.closing:
				return
			}
		}
	}
}

func (s *Server) streamingLocked() int {
	n := 0
	for _, c := range s.inflight {
		n += c
	}
	return n
}

func (s *Server) hang(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-s.closing:
	}
}
