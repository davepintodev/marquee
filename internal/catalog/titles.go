package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Qualities a title can prefer.
var Qualities = []string{"2160p", "1080p", "720p"}

type NewEpisode struct {
	Season, Number int
	Name           string
	AirDate        string // YYYY-MM-DD, never empty: undated episodes are not stored
}

type NewTitle struct {
	Kind             string
	TMDBID           int
	Title            string
	Year             int
	Overview         string
	IMDbID           string
	TVDBID           int
	PreferredQuality string
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
	State     string // downloaded, searching, downloading, stalled, missing or unaired
	Percent   *int   // set while downloading
}

type Season struct {
	Number   int
	Badge    *Badge
	Episodes []Episode
}

type Title struct {
	ID               string
	Kind             string
	TMDBID           int
	Title            string
	Year             int
	Overview         string
	IMDbID           string
	TVDBID           int
	Monitored        bool
	PreferredQuality string // empty when none
	Seasons          []Season
	Badge            *Badge
}

// TitleID is the id of a TMDB title in the API and the database.
func TitleID(kind string, tmdbID int) string { return fmt.Sprintf("%s-%d", kind, tmdbID) }

// EpisodeID is the API's id for an episode.
func EpisodeID(season, number int) string { return fmt.Sprintf("s%02de%02d", season, number) }

const titleColumns = `id, kind, tmdb_id, title, year, overview, imdb_id, tvdb_id, monitored, preferred_quality, have_file`

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
		var quality sql.NullString
		if err := rows.Scan(&t.ID, &t.Kind, &t.TMDBID, &t.Title, &t.Year, &t.Overview, &t.IMDbID, &t.TVDBID, &mon, &quality, &file); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t.Monitored, t.PreferredQuality = mon == 1, quality.String
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
	erows, err := q.QueryContext(ctx, `SELECT title_id, season, number, name, air_date, monitored, have_file FROM episodes WHERE title_id IN (SELECT id FROM titles `+where+`) ORDER BY title_id, season, number`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = erows.Close() }()
	for erows.Next() {
		var id string
		var e epRow
		var mon, file int
		if err := erows.Scan(&id, &e.season, &e.number, &e.name, &e.airDate, &mon, &file); err != nil {
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

	today := s.now().UTC().Format(time.DateOnly)
	for i := range titles {
		t := &titles[i]
		if t.Kind == "movie" {
			t.Badge = movieBadge(t.Monitored, have[t.ID], flying[target{t.ID, 0, 0}])
			continue
		}
		t.Seasons, t.Badge = buildSeasons(eps[t.ID], today, func(season, number int) *inFlight { return flying[target{t.ID, season, number}] })
	}
	return titles, nil
}

type target struct {
	titleID string
	season  int
	number  int
}

// inFlight returns the unfinished downloads of the titles selected by
// where, by movie or episode.
func (s *Store) inFlight(ctx context.Context, q queryer, where string, args []any) (map[target]*inFlight, error) {
	rows, err := q.QueryContext(ctx, `SELECT title_id, season, number, state, bytes_done, bytes_total, updated_at FROM downloads
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
		var updated string
		if err := rows.Scan(&k.titleID, &k.season, &k.number, &d.State, &d.BytesDone, &d.BytesTotal, &updated); err != nil {
			return nil, err
		}
		d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		out[k] = &inFlight{stalled: d.Stalled(now), percent: d.Percent()}
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
	if (n.PreferredQuality != "" && !validQuality(n.PreferredQuality)) || !ValidMonitor(n.Monitor) {
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

	var quality any
	if n.PreferredQuality != "" {
		quality = n.PreferredQuality
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO titles (id, kind, tmdb_id, title, year, overview, imdb_id, tvdb_id, monitored, monitor_new, preferred_quality, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		id, n.Kind, n.TMDBID, n.Title, n.Year, n.Overview, n.IMDbID, n.TVDBID, b2i(monitored), b2i(monitorsNew), quality, s.now().UTC().Format(time.RFC3339Nano))
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

// Patch changes a title's monitored state or preferred quality. Nil leaves
// a field alone; a Quality pointing at "" clears it.
type Patch struct {
	Monitored *bool
	Quality   *string
}

// Update applies p in one transaction. Monitoring or unmonitoring a series
// applies to all of its episodes.
func (s *Store) Update(ctx context.Context, id string, p Patch) (Title, error) {
	if p.Quality != nil && *p.Quality != "" && !validQuality(*p.Quality) {
		return Title{}, ErrInvalid
	}
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
	if p.Quality != nil {
		var q any
		if *p.Quality != "" {
			q = *p.Quality
		}
		if _, err := tx.ExecContext(ctx, `UPDATE titles SET preferred_quality = ? WHERE id = ?`, q, id); err != nil {
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

func validQuality(q string) bool {
	for _, v := range Qualities {
		if v == q {
			return true
		}
	}
	return false
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type epRow struct {
	season, number      int
	name, airDate       string
	monitored, haveFile bool
}

func buildSeasons(rows []epRow, today string, flying func(season, number int) *inFlight) ([]Season, *Badge) {
	eps := map[int][]Episode{}
	views := map[int][]epView{}
	var numbers []int
	var all []epView
	for _, r := range rows {
		if _, ok := eps[r.season]; !ok {
			numbers = append(numbers, r.season)
		}
		state := episodeState(r, today)
		percent := 0
		if dl := flying(r.season, r.number); dl != nil {
			state, percent = "downloading", dl.percent
			if dl.stalled {
				state = "stalled"
			}
		}
		ep := Episode{Number: r.number, Name: r.name, AirDate: r.airDate, Monitored: r.monitored, State: state}
		if state == "downloading" {
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

func episodeState(r epRow, today string) string {
	switch {
	case r.haveFile:
		return "downloaded"
	case r.airDate > today:
		return "unaired"
	case r.monitored:
		return "searching"
	}
	return "missing"
}
