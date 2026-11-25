package catalog

import (
	"context"
	"database/sql"
	"time"
)

// Searching records that the scheduler is searching for a movie (season
// and number zero) or an episode, until the returned func is called. It
// lives in memory only: a search does not outlast the process.
func (s *Store) Searching(titleID string, season, number int) (done func()) {
	k := target{titleID, season, number}
	s.runMu.Lock()
	if s.running == nil {
		s.running = map[target]int{}
	}
	s.running[k]++
	s.runMu.Unlock()
	return func() {
		s.runMu.Lock()
		defer s.runMu.Unlock()
		if s.running[k]--; s.running[k] <= 0 {
			delete(s.running, k)
		}
	}
}

// Hold records that the scheduler sets a movie or an episode aside until
// TorBox has a place for its release, so a Search now waiting on it does
// not read as searching. ReleaseHolds ends every hold.
func (s *Store) Hold(titleID string, season, number int) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.held == nil {
		s.held = map[target]bool{}
	}
	s.held[target{titleID, season, number}] = true
}

func (s *Store) ReleaseHolds() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	clear(s.held)
}

func (s *Store) isRunning(k target) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.running[k] > 0
}

func (s *Store) isHeld(k target) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.held[k]
}

type pending struct {
	searchNow bool
	searching bool
	nothing   bool
	nextAt    time.Time
}

// A Search now request reads as searching only while nothing keeps it from
// starting, or a source outage would leave it pulsing for hours.
func (s *Store) schedules(ctx context.Context, q queryer, where string, args []any, now time.Time) (map[target]pending, error) {
	backoffEnds, err := sourceBackoffEnds(ctx, q)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT title_id, season, number, attempts, next_at, forced FROM schedule
		WHERE title_id IN (SELECT id FROM titles `+where+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[target]pending{}
	for rows.Next() {
		var k target
		var attempts, forced int
		var next sql.NullInt64
		if err := rows.Scan(&k.titleID, &k.season, &k.number, &attempts, &next, &forced); err != nil {
			return nil, err
		}
		p := pending{searchNow: forced == 1, nothing: attempts > 0}
		if next.Valid {
			p.nextAt = time.UnixMilli(next.Int64).UTC()
		}
		switch {
		case !p.searchNow:
		case now.Before(backoffEnds):
			p.nextAt = backoffEnds
		case !s.isHeld(k):
			p.searching = true
		}
		out[k] = p
	}
	return out, rows.Err()
}

func sourceBackoffEnds(ctx context.Context, q queryer) (time.Time, error) {
	rows, err := q.QueryContext(ctx, `SELECT until FROM source_backoff WHERE id = 1`)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = rows.Close() }()
	var until int64
	if rows.Next() {
		if err := rows.Scan(&until); err != nil {
			return time.Time{}, err
		}
	}
	return time.UnixMilli(until).UTC(), rows.Err()
}

func waitState(p pending, running bool, now time.Time) (string, time.Time) {
	if running || p.searching {
		return "searching", time.Time{}
	}
	next := p.nextAt
	if next.Before(now) {
		next = now
	}
	if p.nothing {
		return "not_found", next
	}
	return "wanted", next
}
