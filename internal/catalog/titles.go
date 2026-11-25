package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

type NewEpisode struct {
	Season, Number int
	Name           string
	AirDate        string // YYYY-MM-DD, never empty: undated episodes are not stored
}

type NewTitle struct {
	Kind     string
	TMDBID   int
	Title    string
	Year     int
	Overview string
	IMDbID   string
	TVDBID   int
	// Profile names the quality profile the title is judged by; empty means
	// the default of its kind. The catalog keeps the name and cannot tell
	// whether the config still defines it.
	Profile string
	// Monitor is what a series monitors when added; empty means MonitorAll.
	// A movie is always monitored.
	Monitor  string
	Episodes []NewEpisode
	Images   []ImageRef
}

// What a series monitors when it is added.
const (
	MonitorAll          = "all"
	MonitorFuture       = "future"
	MonitorFirstSeason  = "first_season"
	MonitorLatestSeason = "latest_season"
	MonitorNone         = "none"
)

// ValidMonitor reports whether m is a choice Add accepts; empty is the default.
func ValidMonitor(m string) bool {
	switch m {
	case "", MonitorAll, MonitorFuture, MonitorFirstSeason, MonitorLatestSeason, MonitorNone:
		return true
	}
	return false
}

// monitorRule returns which of the episodes a series added with choice
// monitors. Specials (season 0) are never picked by a season choice.
func monitorRule(choice, today string, eps []NewEpisode) func(NewEpisode) bool {
	first, latest := 0, 0
	for _, e := range eps {
		if e.Season > 0 && (first == 0 || e.Season < first) {
			first = e.Season
		}
		latest = max(latest, e.Season)
	}
	switch choice {
	case MonitorFuture:
		return func(e NewEpisode) bool { return e.AirDate > today }
	case MonitorFirstSeason:
		return func(e NewEpisode) bool { return e.Season > 0 && e.Season == first }
	case MonitorLatestSeason:
		return func(e NewEpisode) bool { return e.Season > 0 && e.Season == latest }
	case MonitorNone:
		return func(NewEpisode) bool { return false }
	}
	return func(NewEpisode) bool { return true }
}

type ImageRef struct {
	TitleID string
	Kind    string // poster or backdrop
	Path    string // TMDB file path
}

type Episode struct {
	Number    int
	Name      string
	AirDate   string
	Monitored bool
	State     string // downloaded, wanted, searching, not_found, caching, downloading, stalled, missing or unaired
	Percent   *int   // set while caching or downloading
	// NextSearchAt is when the scheduler searches for it next; zero unless
	// it is wanted or found nothing.
	NextSearchAt time.Time
	// Shared lists, in order, the ids of every episode that shares this
	// episode's file, itself included; it is empty unless the file holds
	// more than one episode.
	Shared []string
}

type Season struct {
	Number   int
	Badge    *Badge
	Episodes []Episode
}

type Title struct {
	ID        string
	Kind      string
	TMDBID    int
	Title     string
	Year      int
	Overview  string
	IMDbID    string
	TVDBID    int
	Monitored bool
	Profile   string // empty when the title uses its kind's default
	Seasons   []Season
	Badge     *Badge
	// NextSearchAt is the soonest next search of the movie or an episode;
	// zero when nothing waits for one.
	NextSearchAt time.Time
}

// TitleID is the id of a TMDB title in the API and the database.
func TitleID(kind string, tmdbID int) string { return fmt.Sprintf("%s-%d", kind, tmdbID) }

// EpisodeID is the API's id for an episode.
func EpisodeID(season, number int) string { return fmt.Sprintf("s%02de%02d", season, number) }

const titleColumns = `id, kind, tmdb_id, title, year, overview, imdb_id, tvdb_id, monitored, profile, have_file`

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Store) load(ctx context.Context, q queryer, where string, args ...any) ([]Title, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+titleColumns+` FROM titles `+where, args...)
	if err != nil {
		return nil, err
	}
	var titles []Title
	have := map[string]bool{}
	for rows.Next() {
		var t Title
		var mon, file int
		var profile sql.NullString
		if err := rows.Scan(&t.ID, &t.Kind, &t.TMDBID, &t.Title, &t.Year, &t.Overview, &t.IMDbID, &t.TVDBID, &mon, &profile, &file); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t.Monitored, t.Profile = mon == 1, profile.String
		have[t.ID] = file == 1
		titles = append(titles, t)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	eps := map[string][]epRow{}
	erows, err := q.QueryContext(ctx, `SELECT title_id, season, number, name, air_date, monitored, have_file, file_path FROM episodes WHERE title_id IN (SELECT id FROM titles `+where+`) ORDER BY title_id, season, number`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = erows.Close() }()
	for erows.Next() {
		var id string
		var e epRow
		var mon, file int
		if err := erows.Scan(&id, &e.season, &e.number, &e.name, &e.airDate, &mon, &file, &e.path); err != nil {
			return nil, err
		}
		e.monitored, e.haveFile = mon == 1, file == 1
		eps[id] = append(eps[id], e)
	}
	if err := erows.Err(); err != nil {
		return nil, err
	}

	flying, err := s.inFlight(ctx, q, where, args)
	if err != nil {
		return nil, err
	}
	now := s.now()
	sched, err := s.schedules(ctx, q, where, args, now)
	if err != nil {
		return nil, err
	}

	today := now.UTC().Format(time.DateOnly)
	for i := range titles {
		t := &titles[i]
		if t.Kind == "movie" {
			k := target{t.ID, 0, 0}
			t.Badge, t.NextSearchAt = movieBadge(t.Monitored, have[t.ID], flying[k], sched[k], s.isRunning(k), now)
			continue
		}
		t.Seasons, t.Badge = buildSeasons(eps[t.ID], today, func(season, number int) episodeInfo {
			k := target{t.ID, season, number}
			return episodeInfo{flying[k], sched[k], s.isRunning(k), now}
		})
		for _, se := range t.Seasons {
			for _, e := range se.Episodes {
				if !e.NextSearchAt.IsZero() && (t.NextSearchAt.IsZero() || e.NextSearchAt.Before(t.NextSearchAt)) {
					t.NextSearchAt = e.NextSearchAt
				}
			}
		}
	}
	return titles, nil
}

type episodeInfo struct {
	dl      *inFlight
	sched   pending
	running bool
	now     time.Time
}

type target struct {
	titleID string
	season  int
	number  int
}

// inFlight returns the unfinished downloads of the titles selected by
// where, by movie or episode.
func (s *Store) inFlight(ctx context.Context, q queryer, where string, args []any) (map[target]*inFlight, error) {
	rows, err := q.QueryContext(ctx, `SELECT title_id, season, number, state, bytes_done, bytes_total, cache_percent, updated_at, covered_episodes FROM downloads
		WHERE state NOT IN ('done', 'failed') AND title_id IN (SELECT id FROM titles `+where+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	now := s.now()
	out := map[target]*inFlight{}
	for rows.Next() {
		var k target
		var d Download
		var updated, covered string
		if err := rows.Scan(&k.titleID, &k.season, &k.number, &d.State, &d.BytesDone, &d.BytesTotal, &d.CachePercent, &updated, &covered); err != nil {
			return nil, err
		}
		d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		fl := &inFlight{stalled: d.Stalled(now), caching: d.State == StateCaching, percent: d.Percent()}
		out[k] = fl
		for _, n := range decodeCovered(covered) {
			out[target{k.titleID, k.season, n}] = fl
		}
	}
	return out, rows.Err()
}

// MissingEpisodes lists, in order, the aired episodes of a season that the
// scheduler would search for: monitored, without a file, and neither the
// target of an unfinished download nor covered by one.
func (s *Store) MissingEpisodes(ctx context.Context, titleID string, season int, now time.Time) ([]int, error) {
	rows, err := s.r.QueryContext(ctx, `
SELECT e.number FROM episodes e JOIN titles t ON t.id = e.title_id
WHERE e.title_id = ?1 AND e.season = ?2 AND e.season > 0 AND e.have_file = 0 AND e.air_date <= ?3
  AND t.monitored = 1 AND e.monitored = 1
  AND NOT EXISTS (SELECT 1 FROM downloads d WHERE d.title_id = e.title_id AND d.season = e.season AND d.number = e.number AND d.state NOT IN ('done', 'failed'))
  AND NOT EXISTS (SELECT 1 FROM downloads d, json_each(CASE WHEN d.covered_episodes = '' THEN '[]' ELSE d.covered_episodes END) c
    WHERE d.title_id = e.title_id AND d.season = e.season AND d.state NOT IN ('done', 'failed') AND c.value = e.number)
ORDER BY e.number`, titleID, season, now.UTC().Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// OwnedEpisodes lists, in order, the numbers of the season's episodes that
// have a file. A title or season that is not there owns none.
func (s *Store) OwnedEpisodes(ctx context.Context, titleID string, season int) ([]int, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT number FROM episodes WHERE title_id = ? AND season = ? AND have_file = 1 ORDER BY number`, titleID, season)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Get returns one title, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Title, error) {
	ts, err := s.load(ctx, s.r, `WHERE id = ?`, id)
	if err != nil {
		return Title{}, err
	}
	if len(ts) == 0 {
		return Title{}, ErrNotFound
	}
	return ts[0], nil
}

// List returns the library, newest addition first.
func (s *Store) List(ctx context.Context) ([]Title, error) {
	ts, err := s.load(ctx, s.r, `ORDER BY added_at DESC, rowid DESC`)
	if ts == nil {
		ts = []Title{}
	}
	return ts, err
}

// Add stores a title with its episodes in one transaction, monitored, and
// reports whether it was new. A series monitors the episodes n.Monitor
// names; with MonitorNone the title itself is stored unmonitored, which
// keeps the scheduler and the daily refresh, both of which skip an
// unmonitored title, from monitoring anything of it. A title already in the
// library is returned untouched.
func (s *Store) Add(ctx context.Context, n NewTitle) (Title, bool, error) {
	if !ValidMonitor(n.Monitor) {
		return Title{}, false, ErrInvalid
	}
	monitored := n.Kind != "series" || n.Monitor != MonitorNone
	monitorsNew := n.Kind != "series" || n.Monitor == "" || n.Monitor == MonitorAll || n.Monitor == MonitorFuture
	picked := monitorRule(MonitorAll, "", nil)
	if n.Kind == "series" {
		picked = monitorRule(n.Monitor, s.now().UTC().Format(time.DateOnly), n.Episodes)
	}
	id := TitleID(n.Kind, n.TMDBID)
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Title{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `INSERT INTO titles (id, kind, tmdb_id, title, year, overview, imdb_id, tvdb_id, monitored, monitor_new, profile, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		id, n.Kind, n.TMDBID, n.Title, n.Year, n.Overview, n.IMDbID, n.TVDBID, b2i(monitored), b2i(monitorsNew), n.Profile, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return Title{}, false, err
	}
	created := false
	if rows, _ := res.RowsAffected(); rows == 1 {
		created = true
		for _, e := range n.Episodes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO episodes (title_id, season, number, name, air_date, monitored) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING`, id, e.Season, e.Number, e.Name, e.AirDate, b2i(picked(e))); err != nil {
				return Title{}, false, err
			}
		}
		for _, im := range n.Images {
			if err := rememberImage(ctx, tx, ImageRef{TitleID: id, Kind: im.Kind, Path: im.Path}); err != nil {
				return Title{}, false, err
			}
		}
		if n.Kind == "series" {
			// The episodes were fetched a moment ago; the first refresh is a day away.
			if _, err := tx.ExecContext(ctx, `INSERT INTO series_refresh (title_id, due_at) VALUES (?, ?)`, id, s.now().Add(RefreshEvery).UnixMilli()); err != nil {
				return Title{}, false, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return Title{}, false, err
	}
	t, err := s.Get(ctx, id)
	return t, created, err
}

// Patch changes a title's monitored state or profile. Nil leaves a field
// alone; a Profile pointing at "" returns the title to its kind's default.
type Patch struct {
	Monitored *bool
	Profile   *string
}

// Update applies p in one transaction. Monitoring or unmonitoring a series
// applies to all of its episodes.
func (s *Store) Update(ctx context.Context, id string, p Patch) (Title, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Title{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM titles WHERE id = ?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return Title{}, ErrNotFound
	} else if err != nil {
		return Title{}, err
	}
	if p.Monitored != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE titles SET monitored = ?, monitor_new = monitor_new OR ? WHERE id = ?`, b2i(*p.Monitored), b2i(*p.Monitored), id); err != nil {
			return Title{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE episodes SET monitored = ? WHERE title_id = ?`, b2i(*p.Monitored), id); err != nil {
			return Title{}, err
		}
	}
	if p.Profile != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE titles SET profile = ? WHERE id = ?`, *p.Profile, id); err != nil {
			return Title{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Title{}, err
	}
	return s.Get(ctx, id)
}

// SetEpisodeMonitored monitors or unmonitors one episode. Monitoring an
// episode also monitors its title, so a title never searches for an
// episode while reporting itself unmonitored; unmonitoring an episode
// leaves the title as it was.
func (s *Store) SetEpisodeMonitored(ctx context.Context, id string, season, number int, monitored bool) (Title, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Title{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE episodes SET monitored = ? WHERE title_id = ? AND season = ? AND number = ?`, b2i(monitored), id, season, number)
	if err != nil {
		return Title{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Title{}, ErrNotFound
	}
	if monitored {
		if _, err := tx.ExecContext(ctx, `UPDATE titles SET monitored = 1 WHERE id = ?`, id); err != nil {
			return Title{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Title{}, err
	}
	return s.Get(ctx, id)
}

// SetSeasonMonitored monitors or unmonitors every episode of a season.
// Like SetEpisodeMonitored, monitoring also monitors the title and
// unmonitoring leaves it as it was. A title or a season that is not there
// answers ErrNotFound.
func (s *Store) SetSeasonMonitored(ctx context.Context, id string, season int, monitored bool) (Title, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Title{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE episodes SET monitored = ? WHERE title_id = ? AND season = ?`, b2i(monitored), id, season)
	if err != nil {
		return Title{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Title{}, ErrNotFound
	}
	if monitored {
		if _, err := tx.ExecContext(ctx, `UPDATE titles SET monitored = 1 WHERE id = ?`, id); err != nil {
			return Title{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Title{}, err
	}
	return s.Get(ctx, id)
}

// Remove deletes a title, its episodes and its image paths, and returns
// those image paths so their cached files can go too.
func (s *Store) Remove(ctx context.Context, id string) ([]ImageRef, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM titles WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind, tmdb_path FROM images WHERE title_id = ?`, id)
	if err != nil {
		return nil, err
	}
	var refs []ImageRef
	for rows.Next() {
		r := ImageRef{TitleID: id}
		if err := rows.Scan(&r.Kind, &r.Path); err != nil {
			_ = rows.Close()
			return nil, err
		}
		refs = append(refs, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM images WHERE title_id = ?`, id); err != nil {
		return nil, err
	}
	return refs, tx.Commit()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type epRow struct {
	season, number      int
	name, airDate, path string
	monitored, haveFile bool
}

func buildSeasons(rows []epRow, today string, info func(season, number int) episodeInfo) ([]Season, *Badge) {
	sharing := map[string][]string{}
	for _, r := range rows {
		if r.haveFile && r.path != "" {
			sharing[r.path] = append(sharing[r.path], EpisodeID(r.season, r.number))
		}
	}
	eps := map[int][]Episode{}
	views := map[int][]epView{}
	var numbers []int
	var all []epView
	for _, r := range rows {
		if _, ok := eps[r.season]; !ok {
			numbers = append(numbers, r.season)
		}
		in := info(r.season, r.number)
		state, next := episodeState(r, today, in)
		percent := 0
		if dl := in.dl; dl != nil {
			next = time.Time{}
			state, percent = "downloading", dl.percent
			switch {
			case dl.caching:
				state = "caching"
			case dl.stalled:
				state = "stalled"
			}
		}
		ep := Episode{Number: r.number, Name: r.name, AirDate: r.airDate, Monitored: r.monitored, State: state, NextSearchAt: next}
		if ids := sharing[r.path]; r.haveFile && r.path != "" && len(ids) > 1 {
			ep.Shared = ids
		}
		if state == "downloading" || state == "caching" {
			ep.Percent = &percent
		}
		eps[r.season] = append(eps[r.season], ep)
		v := epView{r.season, r.monitored, state, percent}
		views[r.season] = append(views[r.season], v)
		all = append(all, v)
	}
	sort.Ints(numbers)
	seasons := make([]Season, 0, len(numbers))
	for _, n := range numbers {
		seasons = append(seasons, Season{Number: n, Badge: groupBadge(views[n]), Episodes: eps[n]})
	}
	if b := groupBadge(all); b != nil {
		return seasons, b
	}
	return seasons, unmonitoredBadge(all)
}

// episodeState also answers when the episode is next searched. Specials
// (season 0) are never searched, so they wait without a time.
func episodeState(r epRow, today string, in episodeInfo) (string, time.Time) {
	switch {
	case r.haveFile:
		return "downloaded", time.Time{}
	case r.airDate > today:
		return "unaired", time.Time{}
	case r.season == 0 && r.monitored:
		return "wanted", time.Time{}
	case r.season > 0 && (r.monitored || in.running || in.sched.searchNow):
		return waitState(in.sched, in.running, in.now)
	}
	return "missing", time.Time{}
}
