package download

import (
	"sync"
	"time"
)

// limiter adapts one file's connection count: it closes connections when the
// CDN answers 429/503 and adds one back after each growEvery without a 429.
type limiter struct {
	mu        sync.Mutex
	max       int
	target    int
	active    int
	lastCut   time.Time
	calm      time.Time // last 429, or last time a connection was added back
	cutEvery  time.Duration
	growEvery time.Duration
}

func newLimiter(n int, cutEvery, growEvery time.Duration) *limiter {
	return &limiter{max: n, target: n, calm: time.Now(), cutEvery: cutEvery, growEvery: growEvery}
}

// throttled records a 429/503. It lowers the target once per burst
// (connections tend to get 429 together) and reports whether the caller
// should close its connection, plus the resulting target.
func (l *limiter) throttled() (closeConn bool, target int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.calm = now
	if l.target > 1 && now.Sub(l.lastCut) > l.cutEvery {
		l.target = max(1, l.active*3/4)
		l.lastCut = now
	}
	if l.active > l.target {
		l.active--
		return true, l.target
	}
	return false, l.target
}

// grow reports whether the caller should start another connection: one is
// owed below the target, and the target rises by one after a 429-free
// growEvery.
func (l *limiter) grow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.target < l.max && time.Since(l.calm) > l.growEvery {
		l.target++
		l.calm = time.Now()
	}
	if l.active < l.target {
		l.active++
		return true
	}
	return false
}

func (l *limiter) leave() {
	l.mu.Lock()
	l.active--
	l.mu.Unlock()
}
