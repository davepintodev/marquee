package catalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// RefreshEvery is how often a monitored series' episode list is fetched again.
const RefreshEvery = 24 * time.Hour

// Target is one thing the scheduler may search for: a movie, with Season and
// Number zero, or an episode of a series.
type Target struct {
	TitleID          string
	Kind             string
	Title            string
	TMDBID           int
	IMDbID           string
	TVDBID           int
	PreferredQuality string
	Season, Number   int
	// Attempts is how many searches in a row found nothing: the backoff step.
	Attempts int
	// NextAt is when the target is next due; zero when it never was searched.
	NextAt time.Time
	// Forced is set by Search now. FollowUp is set when a download for the
	// target was queued, so that its failure leads straight to the next candidate.
	Forced, FollowUp bool
}

// Schedule is the state kept for one target between searches.
type Schedule struct {
	Attempts int
	// NextAt is zero for a target due at once.
	NextAt   time.Time
	FollowUp bool
	// ClearForced drops a Search now request; without it a request made
	// while a search was in flight survives.
	ClearForced bool
}

func nullMillis(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

// Targets returns what is due at now, most urgent first: Search now
// requests, then targets whose download failed, then the longest waiting.
// A target is wanted when it has no file and no download in flight, and
// either the owner asked for it or it is monitored and, for an episode,
// has aired. Specials (season 0) are not searched.
func (s *Store) Targets(ctx context.Context, now time.Time) ([]Target, error) {
	rows, err := s.r.QueryContext(ctx, `
SELECT * FROM (
  SELECT t.id, t.kind, t.title, t.tmdb_id, t.imdb_id, t.tvdb_id, COALESCE(t.preferred_quality, ''), 0 AS season, 0 AS number,
         COALESCE(s.attempts, 0), s.next_at, COALESCE(s.forced, 0) AS forced, COALESCE(s.follow_up, 0) AS follow_up
  FROM titles t LEFT JOIN schedule s ON s.title_id = t.id AND s.season = 0 AND s.number = 0
  WHERE t.kind = 'movie' AND t.have_file = 0
    AND (COALESCE(s.forced, 0) = 1 OR (t.monitored = 1 AND (s.next_at IS NULL OR s.next_at <= ?1)))
    AND NOT EXISTS (SELECT 1 FROM downloads d WHERE d.title_id = t.id AND d.season = 0 AND d.number = 0 AND d.state NOT IN ('done', 'failed'))
  UNION ALL
  SELECT t.id, t.kind, t.title, t.tmdb_id, t.imdb_id, t.tvdb_id, COALESCE(t.preferred_quality, ''), e.season, e.number,
         COALESCE(s.attempts, 0), s.next_at, COALESCE(s.forced, 0), COALESCE(s.follow_up, 0)
  FROM episodes e JOIN titles t ON t.id = e.title_id
       LEFT JOIN schedule s ON s.title_id = e.title_id AND s.season = e.season AND s.number = e.number
  WHERE e.season > 0 AND e.have_file = 0 AND e.air_date <= ?2
    AND (COALESCE(s.forced, 0) = 1 OR (t.monitored = 1 AND e.monitored = 1 AND (s.next_at IS NULL OR s.next_at <= ?1)))
    AND NOT EXISTS (SELECT 1 FROM downloads d WHERE d.title_id = e.title_id AND d.season = e.season AND d.number = e.number AND d.state NOT IN ('done', 'failed'))
) ORDER BY forced DESC, follow_up DESC, COALESCE(next_at, 0), id, season, number`,
		now.UnixMilli(), now.UTC().Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Target
	for rows.Next() {
		var t Target
		var next sql.NullInt64
		var forced, follow int
		if err := rows.Scan(&t.TitleID, &t.Kind, &t.Title, &t.TMDBID, &t.IMDbID, &t.TVDBID, &t.PreferredQuality,
			&t.Season, &t.Number, &t.Attempts, &next, &forced, &follow); err != nil {
			return nil, err
		}
		if next.Valid {
			t.NextAt = time.UnixMilli(next.Int64).UTC()
		}
		t.Forced, t.FollowUp = forced == 1, follow == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetSchedule records where a target stands after a search. A title that
// was removed meanwhile answers ErrNotFound.
func (s *Store) SetSchedule(ctx context.Context, t Target, sc Schedule) error {
	res, err := s.w.ExecContext(ctx, `
INSERT INTO schedule (title_id, season, number, attempts, next_at, follow_up)
SELECT id, ?2, ?3, ?4, ?5, ?6 FROM titles WHERE id = ?1
ON CONFLICT (title_id, season, number) DO UPDATE SET attempts = excluded.attempts, next_at = excluded.next_at,
  follow_up = excluded.follow_up, forced = CASE WHEN ?7 THEN 0 ELSE forced END`,
		t.TitleID, t.Season, t.Number, sc.Attempts, nullMillis(sc.NextAt), b2i(sc.FollowUp), sc.ClearForced)
	return affected(res, err)
}

type EpisodeRef struct{ Season, Number int }

// RequestSearch marks what Search now asks for, so that the scheduler
// searches it at once whatever its backoff and budget: a movie, one
// episode, or, for a series without an episode, its monitored aired
// episodes that have no file, or all of its aired episodes that have no
// file when none is monitored. The title or the episode not being there
// answers ErrNotFound.
func (s *Store) RequestSearch(ctx context.Context, titleID string, ep *EpisodeRef) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM titles WHERE id = ?`, titleID).Scan(&kind); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	const force = `ON CONFLICT (title_id, season, number) DO UPDATE SET forced = 1`
	switch {
	case ep != nil:
		res, err := tx.ExecContext(ctx, `INSERT INTO schedule (title_id, season, number, forced)
			SELECT title_id, season, number, 1 FROM episodes WHERE title_id = ? AND season = ? AND number = ? `+force, titleID, ep.Season, ep.Number)
		if err := affected(res, err); err != nil {
			return err
		}
	case kind == "movie":
		if _, err := tx.ExecContext(ctx, `INSERT INTO schedule (title_id, season, number, forced) VALUES (?, 0, 0, 1) `+force, titleID); err != nil {
			return err
		}
	default:
		const missing = `title_id = ?1 AND have_file = 0 AND season > 0 AND air_date <= ?2`
		if _, err := tx.ExecContext(ctx, `INSERT INTO schedule (title_id, season, number, forced)
			SELECT title_id, season, number, 1 FROM episodes
			WHERE `+missing+` AND (monitored = 1 OR NOT EXISTS (SELECT 1 FROM episodes WHERE `+missing+` AND monitored = 1)) `+force,
			titleID, s.now().UTC().Format(time.DateOnly)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SearchesToday is how many searches the title has used on day (YYYY-MM-DD)
// of its daily budget.
func (s *Store) SearchesToday(ctx context.Context, titleID, day string) (int, error) {
	var used int
	err := s.r.QueryRowContext(ctx, `SELECT used FROM search_budget WHERE title_id = ? AND day = ?`, titleID, day).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return used, err
}

func (s *Store) CountSearch(ctx context.Context, titleID, day string) error {
	res, err := s.w.ExecContext(ctx, `
INSERT INTO search_budget (title_id, day, used) SELECT id, ?2, 1 FROM titles WHERE id = ?1
ON CONFLICT (title_id) DO UPDATE SET used = CASE WHEN day = excluded.day THEN used + 1 ELSE 1 END, day = excluded.day`,
		titleID, day)
	return affected(res, err)
}

// SourceBackoff is how many searches in a row the source failed, and until
// when no search is made.
func (s *Store) SourceBackoff(ctx context.Context) (failures int, until time.Time, err error) {
	var ms int64
	err = s.r.QueryRowContext(ctx, `SELECT failures, until FROM source_backoff WHERE id = 1`).Scan(&failures, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, nil
	}
	return failures, time.UnixMilli(ms).UTC(), err
}

func (s *Store) SetSourceBackoff(ctx context.Context, failures int, until time.Time) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO source_backoff (id, failures, until) VALUES (1, ?, ?)
		ON CONFLICT (id) DO UPDATE SET failures = excluded.failures, until = excluded.until`, failures, until.UnixMilli())
	return err
}

// RefreshTarget is a monitored series whose episode list is due to be fetched again.
type RefreshTarget struct {
	TitleID string
	TMDBID  int
}

// RefreshDue lists the monitored series due for a refresh at now, the most overdue first.
func (s *Store) RefreshDue(ctx context.Context, now time.Time) ([]RefreshTarget, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT t.id, t.tmdb_id FROM series_refresh r JOIN titles t ON t.id = r.title_id
		WHERE t.monitored = 1 AND r.due_at <= ? ORDER BY r.due_at, t.id`, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RefreshTarget
	for rows.Next() {
		var r RefreshTarget
		if err := rows.Scan(&r.TitleID, &r.TMDBID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeferRefresh moves a series' next refresh to due, after a failed one.
func (s *Store) DeferRefresh(ctx context.Context, titleID string, due time.Time) error {
	res, err := s.w.ExecContext(ctx, `UPDATE series_refresh SET due_at = ? WHERE title_id = ?`, due.UnixMilli(), titleID)
	return affected(res, err)
}

// Refresh is what TMDB lists for a series now.
type Refresh struct {
	IMDbID   string
	TVDBID   int
	Episodes []NewEpisode
	DueAt    time.Time
}

// ApplyRefresh adds the episodes the series has gained, updates the name and
// air date of those it had, and fills in an id the series lacked. A new
// episode is monitored like the title's own, except in an adopted series,
// where it follows the adoption rule: monitored when it is still to air or
// its season has a file. It never changes whether an existing episode is
// monitored or has a file, and never removes one. The title having gone
// answers ErrNotFound.
func (s *Store) ApplyRefresh(ctx context.Context, titleID string, r Refresh) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var monitored, adopted int
	if err := tx.QueryRowContext(ctx, `SELECT monitored, adopted FROM titles WHERE id = ?`, titleID).Scan(&monitored, &adopted); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	ownedSeasons := map[int]bool{}
	if adopted == 1 {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT season FROM episodes WHERE title_id = ? AND have_file = 1`, titleID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n int
			if err := rows.Scan(&n); err != nil {
				_ = rows.Close()
				return err
			}
			ownedSeasons[n] = true
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
	}
	today := s.now().UTC().Format(time.DateOnly)
	if _, err := tx.ExecContext(ctx, `UPDATE titles SET imdb_id = CASE WHEN imdb_id = '' THEN ? ELSE imdb_id END,
		tvdb_id = CASE WHEN tvdb_id = 0 THEN ? ELSE tvdb_id END WHERE id = ?`, r.IMDbID, r.TVDBID, titleID); err != nil {
		return err
	}
	for _, e := range r.Episodes {
		mon := monitored
		if adopted == 1 && !ownedSeasons[e.Season] && e.AirDate <= today {
			mon = 0
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO episodes (title_id, season, number, name, air_date, monitored) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (title_id, season, number) DO UPDATE SET name = excluded.name, air_date = excluded.air_date`,
			titleID, e.Season, e.Number, e.Name, e.AirDate, mon); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE series_refresh SET due_at = ? WHERE title_id = ?`, r.DueAt.UnixMilli(), titleID); err != nil {
		return err
	}
	return tx.Commit()
}

// Activity kinds the scheduler writes; the downloads supply the others.
const (
	ActivitySearched  = "searched"
	ActivityWouldGrab = "would_grab"
	ActivityFailed    = "failed"
)

// Activity is something the scheduler did or decided, for the Activity page.
type Activity struct {
	ID        string
	At        time.Time
	TitleID   string
	TitleName string
	// Season and Number are zero for a movie or a whole title.
	Season, Number int
	Kind           string
	// Reason is set for a failure.
	Reason  string
	Message string
	// ReleaseID is the chooser's identity of the release a would-grab names.
	ReleaseID string
}

func newActivityID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ActivityKeep is how many of the newest Activity entries are kept.
const ActivityKeep = 5000

// AddActivity records an entry and prunes the oldest beyond ActivityKeep,
// except the last would-grab of each target, which tells the scheduler
// whether it has already said so. A title that was removed meanwhile
// answers ErrNotFound.
func (s *Store) AddActivity(ctx context.Context, a Activity) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `INSERT INTO activity (id, title_id, season, number, kind, reason, message, release_id, at)
		SELECT ?, id, ?, ?, ?, ?, ?, ?, ? FROM titles WHERE id = ?`,
		newActivityID(), a.Season, a.Number, a.Kind, a.Reason, a.Message, a.ReleaseID, s.stamp(), a.TitleID)
	if err := affected(res, err); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM activity
		WHERE rowid <= (SELECT rowid FROM activity ORDER BY rowid DESC LIMIT 1 OFFSET ?)
		AND NOT (kind = ? AND rowid = (SELECT MAX(b.rowid) FROM activity b
			WHERE b.title_id = activity.title_id AND b.season = activity.season AND b.number = activity.number AND b.kind = ?))`,
		s.activityKeep, ActivityWouldGrab, ActivityWouldGrab); err != nil {
		return err
	}
	return tx.Commit()
}

// Activities lists the entries, newest first; a non-empty titleID narrows them to one title.
func (s *Store) Activities(ctx context.Context, titleID string) ([]Activity, error) {
	q := `SELECT a.id, a.at, a.title_id, t.title, a.season, a.number, a.kind, a.reason, a.message, a.release_id
		FROM activity a JOIN titles t ON t.id = a.title_id `
	var args []any
	if titleID != "" {
		q += `WHERE a.title_id = ? `
		args = append(args, titleID)
	}
	rows, err := s.r.QueryContext(ctx, q+`ORDER BY a.rowid DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var at string
		if err := rows.Scan(&a.ID, &at, &a.TitleID, &a.TitleName, &a.Season, &a.Number, &a.Kind, &a.Reason, &a.Message, &a.ReleaseID); err != nil {
			return nil, err
		}
		a.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, a)
	}
	return out, rows.Err()
}

// LastWouldGrab is the release the target was last said to be grabbed, and
// false when it never was.
func (s *Store) LastWouldGrab(ctx context.Context, titleID string, season, number int) (string, bool, error) {
	var id string
	err := s.r.QueryRowContext(ctx, `SELECT release_id FROM activity WHERE title_id = ? AND season = ? AND number = ? AND kind = ?
		ORDER BY rowid DESC LIMIT 1`, titleID, season, number, ActivityWouldGrab).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}
