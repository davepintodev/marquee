package catalog

import (
	"context"
	"time"
)

// EpisodeRef is one episode of a series.
type EpisodeRef struct{ Season, Number int }

// Found is what an adopted folder holds on disk.
type Found struct {
	// Movie is true when a movie's folder holds a video file.
	Movie bool
	// Episodes are the episodes the series' video files name, including any
	// TMDB does not list: a season with a file on disk is a season owned.
	Episodes []EpisodeRef
}

// Adopt stores a title already on disk, in one transaction so nothing
// watches it half adopted. A series monitors the episodes of the seasons
// that have files plus every episode still to air; the rest of its aired
// episodes are left alone, which is what Sonarr was doing. A title already
// in the library keeps its monitoring and only learns of its files. It
// reports whether the title was new.
func (s *Store) Adopt(ctx context.Context, n NewTitle, f Found) (Title, bool, error) {
	id := TitleID(n.Kind, n.TMDBID)
	owned := make(map[EpisodeRef]bool, len(f.Episodes))
	ownedSeasons := map[int]bool{}
	for _, e := range f.Episodes {
		owned[e] = true
		ownedSeasons[e.Season] = true
	}
	today := s.now().UTC().Format(time.DateOnly)

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Title{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `INSERT INTO titles (id, kind, tmdb_id, title, year, overview, imdb_id, tvdb_id, monitored, have_file, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?) ON CONFLICT(id) DO NOTHING`,
		id, n.Kind, n.TMDBID, n.Title, n.Year, n.Overview, n.IMDbID, n.TVDBID, b2i(f.Movie), s.stamp())
	if err != nil {
		return Title{}, false, err
	}
	created := false
	if rows, _ := res.RowsAffected(); rows == 1 {
		created = true
		for _, e := range n.Episodes {
			monitored := ownedSeasons[e.Season] || e.AirDate > today
			if _, err := tx.ExecContext(ctx, `INSERT INTO episodes (title_id, season, number, name, air_date, monitored, have_file) VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING`, id, e.Season, e.Number, e.Name, e.AirDate, b2i(monitored), b2i(owned[EpisodeRef{e.Season, e.Number}])); err != nil {
				return Title{}, false, err
			}
		}
		for _, im := range n.Images {
			if err := rememberImage(ctx, tx, ImageRef{TitleID: id, Kind: im.Kind, Path: im.Path}); err != nil {
				return Title{}, false, err
			}
		}
	} else {
		if f.Movie {
			if _, err := tx.ExecContext(ctx, `UPDATE titles SET have_file = 1 WHERE id = ?`, id); err != nil {
				return Title{}, false, err
			}
		}
		for _, e := range f.Episodes {
			if _, err := tx.ExecContext(ctx, `UPDATE episodes SET have_file = 1 WHERE title_id = ? AND season = ? AND number = ?`, id, e.Season, e.Number); err != nil {
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

// Reasons a library folder or file needs the owner's attention.
const (
	ProblemUnmatched   = "unmatched"
	ProblemAmbiguous   = "ambiguous"
	ProblemUnparseable = "unparsed_file"
)

// LibraryProblem is a folder or file adoption could not use.
type LibraryProblem struct {
	Path    string // "Movies/<folder>" or "Series/<folder>[/<file>]"
	Reason  string
	Message string
}

// AdoptionDone reports whether a first-run adoption has completed.
func (s *Store) AdoptionDone(ctx context.Context) (bool, error) {
	var n int
	if err := s.r.QueryRowContext(ctx, `SELECT count(*) FROM adoption`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// FinishAdoption replaces the library problems with those of the run that
// just completed and marks adoption done, together: a run that did not
// complete leaves no marker, so the next start tries again.
func (s *Store) FinishAdoption(ctx context.Context, problems []LibraryProblem) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_problems`); err != nil {
		return err
	}
	for _, p := range problems {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO library_problems (path, reason, message) VALUES (?, ?, ?)`, p.Path, p.Reason, p.Message); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO adoption (done, at) VALUES (1, ?)`, s.stamp()); err != nil {
		return err
	}
	return tx.Commit()
}

// LibraryProblems lists what adoption left for the owner, by path.
func (s *Store) LibraryProblems(ctx context.Context) ([]LibraryProblem, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT path, reason, message FROM library_problems ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []LibraryProblem{}
	for rows.Next() {
		var p LibraryProblem
		if err := rows.Scan(&p.Path, &p.Reason, &p.Message); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
