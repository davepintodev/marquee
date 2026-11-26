package pipeline

import (
	"testing"
	"time"
)

func TestMeterRate(t *testing.T) {
	t0 := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	type tick struct {
		at   float64
		done int64
	}
	for _, c := range []struct {
		name  string
		ticks []tick
		now   float64
		want  int64
	}{
		{"no ticks", nil, 1, 0},
		{"one tick is no rate", []tick{{1, 100}}, 1, 0},
		{"steady", []tick{{0, 0}, {1, 1000}, {2, 2000}, {3, 3000}}, 3, 1000},
		{"a window older than the last tick fades to zero", []tick{{0, 0}, {1, 1000}, {2, 2000}}, 8, 0},
		{"only the window counts", []tick{{0, 0}, {1, 10000}, {2, 10100}, {3, 10200}, {4, 10300}, {5, 10400}, {6, 10500}}, 6, 100},
		{"a slow tick keeps its predecessor", []tick{{0, 0}, {4, 4000}, {8, 8000}}, 8, 1000},
		{"bytes standing still", []tick{{0, 500}, {1, 500}, {2, 500}}, 2, 0},
		{"a restart from fewer bytes forgets the old run", []tick{{0, 9000}, {1, 9500}, {2, 100}, {3, 600}}, 3, 500},
		{"two ticks at one instant", []tick{{1, 0}, {1, 100}}, 1, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			var m meter
			for _, k := range c.ticks {
				m.add(at(k.at), k.done)
			}
			if got := m.rate(at(c.now)); got != c.want {
				t.Errorf("rate = %d, want %d", got, c.want)
			}
		})
	}
}

func TestMeterKeepsOnlyTheWindow(t *testing.T) {
	var m meter
	t0 := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	for i := range 10000 {
		m.add(t0.Add(time.Duration(i)*time.Second), int64(i)*1000)
	}
	if n := len(m.samples); n > int(SpeedWindow/time.Second)+2 {
		t.Errorf("the meter holds %d samples after 10000 ticks", n)
	}
}
