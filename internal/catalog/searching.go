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

func (s *Store) isRunning(k target) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.running[k] > 0
}

// pending is where a movie or an episode stands with the scheduler.
type pending struct {
	// searching is a search running now or asked for with Search now.
	searching bool
	// nothing is set after a search that found nothing usable.
	nothing bool
	nextAt  time.Time
}

func (s *Store) schedules(ctx context.Context, q queryer, where string, args []any) (map[target]pending, error) {
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
		p := pending{searching: forced == 1, nothing: attempts > 0}
		if next.Valid {
			p.nextAt = time.UnixMilli(next.Int64).UTC()
		}
		out[k] = p
	}
	return out, rows.Err()
}

// waitState says whether a monitored item with no file is searching, has
// found nothing so far or is wanted, and when its next scheduled search is:
// now when it is already due, zero while it is searching.
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
