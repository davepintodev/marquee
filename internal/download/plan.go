package download

import (
	"encoding/json"
	"os"
	"sync"
)

// segment is a byte range [Pos, End) of the output file that is still missing.
// End shrinks when an idle connection takes over the upper half of a busy segment.
type segment struct {
	Pos      int64 `json:"pos"`
	End      int64 `json:"end"`
	busy     bool
	inflight int64 // bytes past Pos being written right now; a split never cuts into them
}

// plan tracks the missing ranges of one ranged download. It is saved next to
// the partial file so a retry or a restart resumes instead of starting over.
type plan struct {
	mu       sync.Mutex
	path     string
	minSteal int64
	Total    int64      `json:"total"`
	Segs     []*segment `json:"segments"`
}

func newPlan(path string, total int64, n int, minSteal int64) *plan {
	p := &plan{path: path, Total: total, minSteal: minSteal}
	seg := (total + int64(n) - 1) / int64(n)
	for off := int64(0); off < total; off += seg {
		p.Segs = append(p.Segs, &segment{Pos: off, End: min(off+seg, total)})
	}
	return p
}

// loadPlan returns the saved plan at path, or nil if there is none or it is
// for a different file.
func loadPlan(path string, total, minSteal int64) *plan {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	p := &plan{path: path, minSteal: minSteal}
	if json.Unmarshal(b, p) != nil || p.Total != total {
		return nil
	}
	for _, s := range p.Segs {
		if s == nil || s.Pos < 0 || s.Pos > s.End || s.End > total {
			return nil
		}
	}
	return p
}

// take hands a connection the idle segment with the most bytes left or, when
// every segment is busy, splits the largest busy one and hands over its upper
// half. It returns nil when there is nothing left worth a connection.
func (p *plan) take() *segment {
	p.mu.Lock()
	defer p.mu.Unlock()
	var idle, busy *segment
	for _, s := range p.Segs {
		if s.Pos >= s.End {
			continue
		}
		if !s.busy && (idle == nil || s.End-s.Pos > idle.End-idle.Pos) {
			idle = s
		}
		if s.busy && (busy == nil || s.End-s.Pos > busy.End-busy.Pos) {
			busy = s
		}
	}
	if idle != nil {
		idle.busy = true
		return idle
	}
	if busy == nil || busy.End-busy.Pos < 2*p.minSteal {
		return nil
	}
	mid := busy.Pos + busy.inflight + (busy.End-busy.Pos-busy.inflight)/2
	s := &segment{Pos: mid, End: busy.End, busy: true}
	busy.End = mid
	p.Segs = append(p.Segs, s)
	return s
}

func (p *plan) bounds(s *segment) (pos, end int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return s.Pos, s.End
}

// claim reserves up to n bytes at the segment's start for writing and reports
// how many are still part of the segment (its End may have moved since the
// read began).
func (p *plan) claim(s *segment, n int64) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	s.inflight = max(min(n, s.End-s.Pos), 0)
	return s.inflight
}

// advance records the claimed bytes as written and returns the segment's new
// bounds.
func (p *plan) advance(s *segment) (pos, end int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s.Pos += s.inflight
	s.inflight = 0
	return s.Pos, s.End
}

func (p *plan) release(s *segment) {
	p.mu.Lock()
	s.busy = false
	p.mu.Unlock()
}

func (p *plan) remaining() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var n int64
	for _, s := range p.Segs {
		n += max(s.End-s.Pos, 0)
	}
	return n
}

// save writes the plan atomically, dropping finished segments. A segment only
// advances after its bytes are written, so the plan never claims missing data.
func (p *plan) save() error {
	p.mu.Lock()
	live := p.Segs[:0]
	for _, s := range p.Segs {
		if s.Pos < s.End || s.busy {
			live = append(live, s)
		}
	}
	p.Segs = live
	b, err := json.Marshal(p)
	p.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}
