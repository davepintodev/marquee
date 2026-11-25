package catalog

import (
	"context"
	"database/sql"
	"sort"
)

// TrackedFile is a movie or an episode as a disk scan compares it with
// what is on disk. A movie has Season and Number 0.
type TrackedFile struct {
	Season, Number int
	Have           bool
	Monitored      bool
	File
}

// ScanTitle is a library title with the files the catalog believes it has.
type ScanTitle struct {
	ID, Kind, Title string
	Year            int
	Files           []TrackedFile
}

// ScanTitles lists every title with its tracked files, in id order. Season 0
// is left out: specials are not part of the library.
func (s *Store) ScanTitles(ctx context.Context) ([]ScanTitle, error) {
	return s.scanTitles(ctx, "")
}

// ScanTitleByID is one title as ScanTitles lists it, or ErrNotFound.
func (s *Store) ScanTitleByID(ctx context.Context, id string) (ScanTitle, error) {
	ts, err := s.scanTitles(ctx, id)
	if err != nil {
		return ScanTitle{}, err
	}
	if len(ts) == 0 {
		return ScanTitle{}, ErrNotFound
	}
	return ts[0], nil
}

func (s *Store) scanTitles(ctx context.Context, only string) ([]ScanTitle, error) {
	where, args := "", []any(nil)
	if only != "" {
		where, args = " WHERE id = ?", []any{only}
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, kind, title, year, monitored, have_file, file_path, file_size, file_fingerprint FROM titles`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	var out []ScanTitle
	byID := map[string]int{}
	for rows.Next() {
		var t ScanTitle
		var f TrackedFile
		var mon, have int
		if err := rows.Scan(&t.ID, &t.Kind, &t.Title, &t.Year, &mon, &have, &f.Path, &f.Size, &f.Fingerprint); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if t.Kind == "movie" {
			f.Monitored, f.Have = mon == 1, have == 1
			t.Files = []TrackedFile{f}
		}
		byID[t.ID] = len(out)
		out = append(out, t)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	q := `SELECT title_id, season, number, monitored, have_file, file_path, file_size, file_fingerprint FROM episodes WHERE season > 0`
	if only != "" {
		q += ` AND title_id = ?`
	}
	erows, err := s.r.QueryContext(ctx, q+` ORDER BY title_id, season, number`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = erows.Close() }()
	for erows.Next() {
		var id string
		var f TrackedFile
		var mon, have int
		if err := erows.Scan(&id, &f.Season, &f.Number, &mon, &have, &f.Path, &f.Size, &f.Fingerprint); err != nil {
			return nil, err
		}
		f.Monitored, f.Have = mon == 1, have == 1
		if i, ok := byID[id]; ok {
			out[i].Files = append(out[i].Files, f)
		}
	}
	return out, erows.Err()
}

// FileFound is a file a scan saw on disk for a movie or an episode.
type FileFound struct {
	Season, Number int
	File
}

// FileGone is a tracked file a scan found missing from disk, with the path
// the scan judged it by.
type FileGone struct {
	Season, Number int
	Path           string
}

// ApplyScan records what a scan of one title found, in one transaction. A
// file found marks its movie or episode as having it. A file gone makes
// its movie or episode missing and unmonitored, so that nothing downloads
// again what the owner deleted, and leaves a note in the history. A file
// that changed after the scan read it (a download finished meanwhile) is
// left as it is. It returns how many files were really marked gone.
func (s *Store) ApplyScan(ctx context.Context, titleID string, found []FileFound, gone []FileGone) (int, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range found {
		if f.Season == 0 {
			_, err = tx.ExecContext(ctx, `UPDATE titles SET have_file = 1, file_path = ?, file_size = ?, file_fingerprint = ? WHERE id = ?`,
				f.Path, f.Size, f.Fingerprint, titleID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE episodes SET have_file = 1, file_path = ?, file_size = ?, file_fingerprint = ?
				WHERE title_id = ? AND season = ? AND number = ?`, f.Path, f.Size, f.Fingerprint, titleID, f.Season, f.Number)
		}
		if err != nil {
			return 0, err
		}
	}
	removed := 0
	for _, g := range gone {
		var res sql.Result
		if g.Season == 0 {
			res, err = tx.ExecContext(ctx, `UPDATE titles SET have_file = 0, monitored = 0, file_path = '', file_size = 0, file_fingerprint = ''
				WHERE id = ? AND have_file = 1 AND file_path = ?`, titleID, g.Path)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE episodes SET have_file = 0, monitored = 0, file_path = '', file_size = 0, file_fingerprint = ''
				WHERE title_id = ? AND season = ? AND number = ? AND have_file = 1 AND file_path = ?`, titleID, g.Season, g.Number, g.Path)
		}
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		removed++
		if err := s.addActivity(ctx, tx, Activity{TitleID: titleID, Season: g.Season, Number: g.Number, Kind: ActivityRemoved, Message: "File removed from disk; unmonitored."}); err != nil {
			return 0, err
		}
	}
	return removed, tx.Commit()
}

// ReplaceLibraryProblems swaps the library problems for the ones a scan
// found, in one transaction. With scopes (such as "Series/House") only the
// problems at or under those paths are swapped, and problems is expected to
// hold only paths under them; with none, all of them are.
func (s *Store) ReplaceLibraryProblems(ctx context.Context, problems []LibraryProblem, scopes ...string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if len(scopes) == 0 {
		_, err = tx.ExecContext(ctx, `DELETE FROM library_problems`)
	}
	sort.Strings(scopes)
	for _, scope := range scopes {
		if err != nil {
			break
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM library_problems WHERE path = ?1 OR substr(path, 1, length(?1) + 1) = ?1 || '/'`, scope)
	}
	if err != nil {
		return err
	}
	for _, p := range problems {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO library_problems (path, reason, message) VALUES (?, ?, ?)`, p.Path, p.Reason, p.Message); err != nil {
			return err
		}
	}
	return tx.Commit()
}
