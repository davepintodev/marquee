package pipeline

import (
	"sync"
	"time"
)

// SpeedWindow is how far back a download's rate looks; a longer window
// smooths a bursty source, a shorter one follows a change sooner.
const SpeedWindow = 5 * time.Second

// meter turns a download's progress ticks into a rate. It keeps the ticks
// of the last SpeedWindow only, so it holds a handful of numbers however
// long the download runs, and it does no work per byte.
type meter struct {
	mu      sync.Mutex
	samples []sample
}

type sample struct {
	at   time.Time
	done int64
}

func (m *meter) add(at time.Time, done int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := len(m.samples); n > 0 && done < m.samples[n-1].done {
		m.samples = m.samples[:0]
	}
	m.samples = append(m.samples, sample{at, done})
	m.trim(at)
}

func (m *meter) trim(now time.Time) {
	keep := 0
	for keep < len(m.samples) && now.Sub(m.samples[keep].at) > SpeedWindow {
		keep++
	}
	m.samples = append(m.samples[:0], m.samples[keep:]...)
}

// rate is the bytes a second over the window ending now: zero until two
// ticks are in it, and again once the ticks have stopped for the whole window.
func (m *meter) rate(now time.Time) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := len(m.samples); n == 0 || now.Sub(m.samples[n-1].at) > SpeedWindow {
		return 0
	}
	m.trim(now)
	if len(m.samples) < 2 {
		return 0
	}
	first, last := m.samples[0], m.samples[len(m.samples)-1]
	span := last.at.Sub(first.at)
	if span <= 0 {
		return 0
	}
	return int64(float64(last.done-first.done) / span.Seconds())
}
