package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/config"
)

// States of a download. A download moves queued, downloading, verifying,
// importing, done, or ends failed with a reason.
const (
	StateQueued      = "queued"
	StateDownloading = "downloading"
	StateVerifying   = "verifying"
	StateImporting   = "importing"
	StateDone        = "done"
	StateFailed      = "failed"
)

// ErrActiveDownload means the title or episode already has a download
// that has not finished.
var ErrActiveDownload = errors.New("catalog: the title or episode already has an active download")

// ReleaseID is a release's identity for the blocklist: the same release is
// the same whatever link it is reached by, and links may be ephemeral.
type ReleaseID struct {
	InfoHash string
	FileName string
	Size     int64
}

type NewDownload struct {
	ID          string
	TitleID     string
	Season      int // zero for a movie
	Number      int // zero for a movie
	Release     ReleaseID
	ReleaseName string
	Resolution  string // AIOStreams' parsed resolution, for the quality tag
	Quality     string // AIOStreams' parsed quality, for the quality tag
	URL         config.Secret
}

type Download struct {
	NewDownload
	State       string
	BytesDone   int64
	BytesTotal  int64
	Reason      string
	Message     string
	LibraryPath string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	TitleName   string
}

const downloadColumns = `d.id, d.title_id, t.title, d.season, d.number, d.state, d.info_hash, d.file_name, d.size,
	d.release_name, d.resolution, d.quality, d.url, d.bytes_done, d.bytes_total, d.reason, d.message,
	d.library_path, d.created_at, d.updated_at`

func scanDownload(sc interface{ Scan(...any) error }) (Download, error) {
	var d Download
	var url, created, updated string
	if err := sc.Scan(&d.ID, &d.TitleID, &d.TitleName, &d.Season, &d.Number, &d.State, &d.Release.InfoHash,
		&d.Release.FileName, &d.Release.Size, &d.ReleaseName, &d.Resolution, &d.Quality, &url, &d.BytesDone,
		&d.BytesTotal, &d.Reason, &d.Message, &d.LibraryPath, &created, &updated); err != nil {
		return Download{}, err
	}
	d.URL = config.Secret(url)
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return d, nil
}

func (s *Store) stamp() string { return s.now().UTC().Format(time.RFC3339Nano) }

// AddDownload queues a download. A title or episode with an unfinished
// download answers ErrActiveDownload, a title that is not in the library
// ErrNotFound.
func (s *Store) AddDownload(ctx context.Context, n NewDownload) (Download, error) {
	var exists int
	if err := s.w.QueryRowContext(ctx, `SELECT 1 FROM titles WHERE id = ?`, n.TitleID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Download{}, ErrNotFound
		}
		return Download{}, err
	}
	now := s.stamp()
	_, err := s.w.ExecContext(ctx, `INSERT INTO downloads (id, title_id, season, number, state, info_hash, file_name, size,
		release_name, resolution, quality, url, created_at, updated_at) VALUES (?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.TitleID, n.Season, n.Number, n.Release.InfoHash, n.Release.FileName, n.Release.Size,
		n.ReleaseName, n.Resolution, n.Quality, n.URL.Reveal(), now, now)
	if err != nil {
		if isUnique(err) {
			return Download{}, ErrActiveDownload
		}
		return Download{}, err
	}
	return s.GetDownload(ctx, n.ID)
}

// ActiveDownloadFor returns the unfinished download of a title or episode.
func (s *Store) ActiveDownloadFor(ctx context.Context, titleID string, season, number int) (Download, error) {
	return scanDownload(s.r.QueryRowContext(ctx, `SELECT `+downloadColumns+` FROM downloads d JOIN titles t ON t.id = d.title_id
		WHERE d.title_id = ? AND d.season = ? AND d.number = ? AND d.state NOT IN ('done', 'failed')`, titleID, season, number))
}

func (s *Store) GetDownload(ctx context.Context, id string) (Download, error) {
	d, err := scanDownload(s.r.QueryRowContext(ctx, `SELECT `+downloadColumns+` FROM downloads d JOIN titles t ON t.id = d.title_id WHERE d.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Download{}, ErrNotFound
	}
	return d, err
}

func (s *Store) queryDownloads(ctx context.Context, where string, args ...any) ([]Download, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+downloadColumns+` FROM downloads d JOIN titles t ON t.id = d.title_id `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ActiveDownloads are the downloads that have not finished, oldest first.
func (s *Store) ActiveDownloads(ctx context.Context) ([]Download, error) {
	return s.queryDownloads(ctx, `WHERE d.state NOT IN ('done', 'failed') ORDER BY d.created_at, d.rowid`)
}

// FinishedDownloads are the done and failed downloads, newest first; a
// non-empty titleID narrows them to one title.
func (s *Store) FinishedDownloads(ctx context.Context, titleID string) ([]Download, error) {
	if titleID != "" {
		return s.queryDownloads(ctx, `WHERE d.state IN ('done', 'failed') AND d.title_id = ? ORDER BY d.updated_at DESC, d.rowid DESC`, titleID)
	}
	return s.queryDownloads(ctx, `WHERE d.state IN ('done', 'failed') ORDER BY d.updated_at DESC, d.rowid DESC`)
}

// SetDownloadState moves an unfinished download to state. A download that
// is gone (its title was removed) or already finished answers ErrNotFound.
func (s *Store) SetDownloadState(ctx context.Context, id, state string) error {
	res, err := s.w.ExecContext(ctx, `UPDATE downloads SET state = ?, updated_at = ? WHERE id = ? AND state NOT IN ('done', 'failed')`, state, s.stamp(), id)
	return affected(res, err)
}

// SetDownloadProgress records the bytes written and the size the server reported.
func (s *Store) SetDownloadProgress(ctx context.Context, id string, done, total int64) error {
	// updated_at moves only with the bytes, so a download whose bytes stop
	// moving shows as stalled.
	res, err := s.w.ExecContext(ctx, `UPDATE downloads SET updated_at = CASE WHEN bytes_done != ? THEN ? ELSE updated_at END,
		bytes_done = ?, bytes_total = ? WHERE id = ? AND state NOT IN ('done', 'failed')`, done, s.stamp(), done, total, id)
	return affected(res, err)
}

// SetDownloadLibraryPath remembers where an import is moving the file, so a
// restart in the middle can tell whether the move happened.
func (s *Store) SetDownloadLibraryPath(ctx context.Context, id, path string) error {
	res, err := s.w.ExecContext(ctx, `UPDATE downloads SET library_path = ?, updated_at = ? WHERE id = ? AND state NOT IN ('done', 'failed')`, path, s.stamp(), id)
	return affected(res, err)
}

// FinishDownload marks an imported download done and the title or episode
// as having its file, together, and drops the playback link.
func (s *Store) FinishDownload(ctx context.Context, id string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var titleID string
	var season, number int
	if err := tx.QueryRowContext(ctx, `SELECT title_id, season, number FROM downloads WHERE id = ? AND state NOT IN ('done', 'failed')`, id).Scan(&titleID, &season, &number); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE downloads SET state = 'done', url = '', bytes_done = bytes_total, updated_at = ? WHERE id = ?`, s.stamp(), id); err != nil {
		return err
	}
	if season == 0 && number == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE titles SET have_file = 1 WHERE id = ?`, titleID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE episodes SET have_file = 1 WHERE title_id = ? AND season = ? AND number = ?`, titleID, season, number)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// FailDownload ends a download with a reason and a plain-language message
// and drops the playback link. With blocklist set the release is also
// blocklisted, so it is not tried again.
func (s *Store) FailDownload(ctx context.Context, id, reason, message string, blocklist bool) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var r ReleaseID
	if err := tx.QueryRowContext(ctx, `SELECT info_hash, file_name, size FROM downloads WHERE id = ? AND state NOT IN ('done', 'failed')`, id).
		Scan(&r.InfoHash, &r.FileName, &r.Size); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	now := s.stamp()
	if _, err := tx.ExecContext(ctx, `UPDATE downloads SET state = 'failed', url = '', reason = ?, message = ?, updated_at = ? WHERE id = ?`, reason, message, now, id); err != nil {
		return err
	}
	if blocklist {
		if _, err := tx.ExecContext(ctx, `INSERT INTO blocklist (identity, blocked_at, reason) VALUES (?, ?, ?)
			ON CONFLICT (identity) DO UPDATE SET blocked_at = excluded.blocked_at, reason = excluded.reason`,
			chooser.IdentityOf(r.InfoHash, r.FileName, r.Size), s.now().UTC().Format(time.RFC3339), reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ComponentStatus is the last known outcome of calling an outside service.
type ComponentStatus struct {
	OK      bool
	Message string
	At      time.Time
}

// SetComponentStatus records the outcome of the latest call to component,
// for the Health page.
func (s *Store) SetComponentStatus(ctx context.Context, component string, ok bool, message string) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO component_status (component, ok, message, at) VALUES (?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET ok = excluded.ok, message = excluded.message, at = excluded.at`, component, b2i(ok), message, s.stamp())
	return err
}

// GetComponentStatus returns the recorded outcome, and false when there is none.
func (s *Store) GetComponentStatus(ctx context.Context, component string) (ComponentStatus, bool, error) {
	var st ComponentStatus
	var ok int
	var at string
	err := s.r.QueryRowContext(ctx, `SELECT ok, message, at FROM component_status WHERE component = ?`, component).Scan(&ok, &st.Message, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return ComponentStatus{}, false, nil
	}
	if err != nil {
		return ComponentStatus{}, false, err
	}
	st.OK = ok == 1
	st.At, _ = time.Parse(time.RFC3339Nano, at)
	return st, true, nil
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// isUnique recognises SQLite's UNIQUE and PRIMARY KEY constraint errors
// (extended result codes 2067 and 1555) without importing the driver here.
func isUnique(err error) bool {
	var c interface{ Code() int }
	return errors.As(err, &c) && (c.Code() == 2067 || c.Code() == 1555)
}
