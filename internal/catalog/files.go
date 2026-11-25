package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/davepintodev/marquee/internal/chooser"
)

// ActivityWrongFile notes a file the owner declared wrong and had deleted.
const ActivityWrongFile = "wrong_file"

// ErrFileChanged means the file a delete or reset was about is no longer
// the one the catalog records, because a scan or an import changed it
// meanwhile. Nothing was changed.
var ErrFileChanged = errors.New("catalog: the recorded file changed")

// TrackedFiles lists the files the catalog records for a title: the movie's
// own, or each episode's, specials included. A movie or episode flagged as
// having a file whose location is not recorded (a library adopted before
// marquee kept paths, not rescanned yet) is listed with an empty Path.
func (s *Store) TrackedFiles(ctx context.Context, titleID string) ([]TrackedFile, error) {
	var kind string
	if err := s.r.QueryRowContext(ctx, `SELECT kind FROM titles WHERE id = ?`, titleID).Scan(&kind); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	q := `SELECT 0, 0, monitored, have_file, file_path, file_size, file_fingerprint FROM titles WHERE id = ?1`
	if kind == "series" {
		q = `SELECT season, number, monitored, have_file, file_path, file_size, file_fingerprint FROM episodes WHERE title_id = ?1 ORDER BY season, number`
	}
	rows, err := s.r.QueryContext(ctx, q, titleID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TrackedFile
	for rows.Next() {
		var f TrackedFile
		var mon, have int
		if err := rows.Scan(&f.Season, &f.Number, &mon, &have, &f.Path, &f.Size, &f.Fingerprint); err != nil {
			return nil, err
		}
		f.Monitored, f.Have = mon == 1, have == 1
		if f.Have || f.Path != "" {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}

// FileOf is the file the catalog records for a movie (season and number
// 0) or an episode, whether or not it has one; ErrNotFound when the title
// or the episode is not in the library.
func (s *Store) FileOf(ctx context.Context, titleID string, season, number int) (TrackedFile, error) {
	q, args := `SELECT monitored, have_file, file_path, file_size, file_fingerprint FROM titles WHERE id = ? AND kind = 'movie'`, []any{titleID}
	if season != 0 || number != 0 {
		q, args = `SELECT monitored, have_file, file_path, file_size, file_fingerprint FROM episodes WHERE title_id = ? AND season = ? AND number = ?`,
			[]any{titleID, season, number}
	}
	f := TrackedFile{Season: season, Number: number}
	var mon, have int
	err := s.r.QueryRowContext(ctx, q, args...).Scan(&mon, &have, &f.Path, &f.Size, &f.Fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return TrackedFile{}, ErrNotFound
	}
	f.Monitored, f.Have = mon == 1, have == 1
	return f, err
}

// WrongFile describes a file the owner declared wrong, after it has been
// deleted from disk. Path is the library-relative path FileOf gave; every
// movie or episode of the title that records the same path (a file that
// covers several episodes) is reset together. Season and Number name the
// one the owner asked about, and are zero for a movie.
type WrongFile struct {
	TitleID        string
	Season, Number int
	Path           string
	// ReSearch has the scheduler search for a replacement at once.
	ReSearch bool
}

// WrongFileResult says what ResetWrongFile did.
type WrongFileResult struct {
	Reset []EpisodeRef
	// Blocklisted is set when the release that produced the file was known
	// and is now on the blocklist.
	Blocklisted bool
}

// ResetWrongFile, in one transaction, makes what recorded the file missing
// and monitored again, blocklists the release that made it when a finished
// download recorded exactly this file, notes it in the history and, with
// ReSearch, marks every reset target for an immediate search. A file the
// catalog no longer records at that path answers ErrFileChanged and
// nothing is written.
func (s *Store) ResetWrongFile(ctx context.Context, w WrongFile) (WrongFileResult, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return WrongFileResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM titles WHERE id = ?`, w.TitleID).Scan(&kind); errors.Is(err, sql.ErrNoRows) {
		return WrongFileResult{}, ErrNotFound
	} else if err != nil {
		return WrongFileResult{}, err
	}
	if w.Path == "" {
		return WrongFileResult{}, ErrFileChanged
	}

	var res WrongFileResult
	if kind == "movie" {
		r, err := tx.ExecContext(ctx, `UPDATE titles SET have_file = 0, monitored = 1, file_path = '', file_size = 0, file_fingerprint = ''
			WHERE id = ? AND file_path = ?`, w.TitleID, w.Path)
		if err != nil {
			return WrongFileResult{}, err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return WrongFileResult{}, ErrFileChanged
		}
		res.Reset = []EpisodeRef{{}}
	} else {
		rows, err := tx.QueryContext(ctx, `SELECT season, number FROM episodes WHERE title_id = ? AND file_path = ? ORDER BY season, number`, w.TitleID, w.Path)
		if err != nil {
			return WrongFileResult{}, err
		}
		asked := false
		for rows.Next() {
			var e EpisodeRef
			if err := rows.Scan(&e.Season, &e.Number); err != nil {
				_ = rows.Close()
				return WrongFileResult{}, err
			}
			asked = asked || e == EpisodeRef{Season: w.Season, Number: w.Number}
			res.Reset = append(res.Reset, e)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return WrongFileResult{}, err
		}
		if !asked {
			return WrongFileResult{}, ErrFileChanged
		}
		if _, err := tx.ExecContext(ctx, `UPDATE episodes SET have_file = 0, monitored = 1, file_path = '', file_size = 0, file_fingerprint = ''
			WHERE title_id = ? AND file_path = ?`, w.TitleID, w.Path); err != nil {
			return WrongFileResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE titles SET monitored = 1 WHERE id = ?`, w.TitleID); err != nil {
			return WrongFileResult{}, err
		}
	}

	if res.Blocklisted, err = s.blocklistMaker(ctx, tx, w.TitleID, w.Path); err != nil {
		return WrongFileResult{}, err
	}
	for _, e := range res.Reset {
		if w.ReSearch {
			_, err = tx.ExecContext(ctx, `INSERT INTO schedule (title_id, season, number, forced) VALUES (?, ?, ?, 1)
				ON CONFLICT (title_id, season, number) DO UPDATE SET forced = 1`, w.TitleID, e.Season, e.Number)
		} else {
			err = dropSchedule(ctx, tx, w.TitleID, e.Season, e.Number)
		}
		if err != nil {
			return WrongFileResult{}, err
		}
	}
	if err := s.addActivity(ctx, tx, Activity{TitleID: w.TitleID, Season: w.Season, Number: w.Number, Kind: ActivityWrongFile, Message: wrongFileMessage(w, res)}); err != nil {
		return WrongFileResult{}, err
	}
	return res, tx.Commit()
}

// blocklistMaker blocklists the release of the latest finished download
// that placed the file at path, and reports whether there was one. A file
// adopted from disk has no download and so no release to blame.
func (s *Store) blocklistMaker(ctx context.Context, tx *sql.Tx, titleID, rel string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT info_hash, file_name, size, library_path FROM downloads
		WHERE title_id = ? AND state = 'done' AND library_path != '' ORDER BY at_ms DESC, rowid DESC`, titleID)
	if err != nil {
		return false, err
	}
	var made *ReleaseID
	for rows.Next() {
		var r ReleaseID
		var lib string
		if err := rows.Scan(&r.InfoHash, &r.FileName, &r.Size, &lib); err != nil {
			_ = rows.Close()
			return false, err
		}
		if made == nil && (filepath.ToSlash(lib) == rel || strings.HasSuffix(filepath.ToSlash(lib), "/"+rel)) {
			made = &r
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, err
	}
	if made == nil {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO blocklist (identity, blocked_at, reason) VALUES (?, ?, 'wrong_file')
		ON CONFLICT (identity) DO UPDATE SET blocked_at = excluded.blocked_at, reason = excluded.reason`,
		chooser.IdentityOf(made.InfoHash, made.FileName, made.Size), s.now().UTC().Format(time.RFC3339))
	return err == nil, err
}

func wrongFileMessage(w WrongFile, res WrongFileResult) string {
	msg := "Wrong file: deleted " + path.Base(w.Path) + "."
	var also []string
	for _, e := range res.Reset {
		if e != (EpisodeRef{Season: w.Season, Number: w.Number}) {
			also = append(also, fmt.Sprintf("S%02dE%02d", e.Season, e.Number))
		}
	}
	if len(also) > 0 {
		msg += " It also covered " + strings.Join(also, ", ") + "."
	}
	if res.Blocklisted {
		msg += " Its release is blocklisted."
	}
	if w.ReSearch {
		return msg + " Searching for another release."
	}
	return msg + " Pick another release."
}

// SharesFile reports whether an episode of the title outside the given
// ones records the same file, as each episode of a multi-episode file does.
// numbers are the episodes of season that are leaving the file.
func (s *Store) SharesFile(ctx context.Context, titleID, path string, season int, numbers []int) (bool, error) {
	if path == "" {
		return false, nil
	}
	leaving, _ := json.Marshal(numbers)
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM episodes WHERE title_id = ? AND file_path = ?
		AND NOT (season = ? AND number IN (SELECT value FROM json_each(?)))`,
		titleID, path, season, string(leaving)).Scan(&n)
	return n > 0, err
}
