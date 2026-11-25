package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/config"
)

// States of a download. A download moves queued, downloading, verifying,
// importing, done, or ends failed with a reason. One TorBox has to fetch
// first spends its time between queued and downloading caching.
const (
	StateQueued      = "queued"
	StateCaching     = "caching"
	StateDownloading = "downloading"
	StateVerifying   = "verifying"
	StateImporting   = "importing"
	StateDone        = "done"
	StateFailed      = "failed"
)

// StallAfter is how long a download's bytes may stand still before the
// queue and the badges show it as stalled; the downloader itself fails it
// after a minute.
const StallAfter = 15 * time.Second

// Percent is how much of the file has arrived, or of the torrent TorBox has
// cached; 0 until the size is known.
func (d Download) Percent() int {
	if d.State == StateCaching {
		return min(d.CachePercent, 100)
	}
	if d.BytesTotal <= 0 {
		return 0
	}
	return int(min(d.BytesDone*100/d.BytesTotal, 100))
}

// Stalled reports a download whose bytes stopped moving; one still queued
// or past downloading is not waiting on bytes.
func (d Download) Stalled(now time.Time) bool {
	return d.State == StateDownloading && now.Sub(d.UpdatedAt) > StallAfter
}

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
	// ReplaceExisting marks a download that swaps in for the file the movie
	// or episode already has: the old file stays until the new one is
	// verified and placed.
	ReplaceExisting bool
	// Covered are the episode numbers of Season that the one file holds, as
	// a release named S01E01E02 does. A file of a single episode leaves it
	// empty; otherwise it is sorted and holds Number.
	Covered []int
	// Uncached marks a release the debrid service does not hold, which TorBox
	// must fetch before the file can be downloaded; it has no playback link.
	Uncached bool
}

type Download struct {
	NewDownload
	// CachePercent is how much of the torrent TorBox has fetched so far.
	CachePercent int
	// TorBoxTorrentID and TorBoxFileID name the torrent TorBox created for
	// the download, and the file of it that was chosen, once they are known.
	TorBoxTorrentID, TorBoxFileID int
	State                         string
	BytesDone                     int64
	BytesTotal                    int64
	Reason                        string
	Message                       string
	LibraryPath                   string
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
	TitleName                     string
}

const downloadColumns = `d.id, d.title_id, t.title, d.season, d.number, d.state, d.info_hash, d.file_name, d.size,
	d.release_name, d.resolution, d.quality, d.url, d.bytes_done, d.bytes_total, d.reason, d.message,
	d.library_path, d.created_at, d.updated_at, d.replace_existing, d.covered_episodes,
	d.cached, d.cache_percent, d.torbox_torrent_id, d.torbox_file_id`

func scanDownload(sc interface{ Scan(...any) error }, extra ...any) (Download, error) {
	var d Download
	var url, created, updated, covered string
	var replace, cached int
	dest := append([]any{&d.ID, &d.TitleID, &d.TitleName, &d.Season, &d.Number, &d.State, &d.Release.InfoHash,
		&d.Release.FileName, &d.Release.Size, &d.ReleaseName, &d.Resolution, &d.Quality, &url, &d.BytesDone,
		&d.BytesTotal, &d.Reason, &d.Message, &d.LibraryPath, &created, &updated, &replace, &covered,
		&cached, &d.CachePercent, &d.TorBoxTorrentID, &d.TorBoxFileID}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return Download{}, err
	}
	d.URL, d.ReplaceExisting, d.Uncached = config.Secret(url), replace == 1, cached == 0
	d.Covered = decodeCovered(covered)
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return d, nil
}

func (s *Store) stamp() string { return s.now().UTC().Format(time.RFC3339Nano) }

// AddDownload queues a download. A title or episode with an unfinished
// download, or one that an unfinished download's file already covers,
// answers ErrActiveDownload, a title that is not in the library
// ErrNotFound.
func (s *Store) AddDownload(ctx context.Context, n NewDownload) (Download, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Download{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM titles WHERE id = ?`, n.TitleID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Download{}, ErrNotFound
		}
		return Download{}, err
	}
	covered := normalizeCovered(n.Number, n.Covered)
	if n.Season != 0 || n.Number != 0 {
		busy, err := coveredBusy(ctx, tx, n.TitleID, n.Season, append([]int{n.Number}, covered...))
		if err != nil {
			return Download{}, err
		}
		if busy {
			return Download{}, ErrActiveDownload
		}
	}
	stored := ""
	if len(covered) > 1 {
		b, _ := json.Marshal(covered)
		stored = string(b)
	}
	now := s.stamp()
	_, err = tx.ExecContext(ctx, `INSERT INTO downloads (id, title_id, season, number, state, info_hash, file_name, size,
		release_name, resolution, quality, url, replace_existing, covered_episodes, cached, created_at, updated_at) VALUES (?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.TitleID, n.Season, n.Number, n.Release.InfoHash, n.Release.FileName, n.Release.Size,
		n.ReleaseName, n.Resolution, n.Quality, n.URL.Reveal(), b2i(n.ReplaceExisting), stored, b2i(!n.Uncached), now, now)
	if err != nil {
		if isUnique(err) {
			return Download{}, ErrActiveDownload
		}
		return Download{}, err
	}
	if err := tx.Commit(); err != nil {
		return Download{}, err
	}
	return s.GetDownload(ctx, n.ID)
}

// normalizeCovered is the episodes a file holds, sorted and without
// repeats, with the target among them; a single episode is none at all.
func normalizeCovered(target int, covered []int) []int {
	all := slices.Clone(covered)
	if len(all) > 0 {
		all = append(all, target)
	}
	slices.Sort(all)
	return slices.Compact(all)
}

func decodeCovered(stored string) []int {
	if stored == "" {
		return nil
	}
	var out []int
	_ = json.Unmarshal([]byte(stored), &out)
	return out
}

// coveredBusy reports whether an unfinished download of the season already
// targets or covers any of the episodes, so that two files never claim the same one.
func coveredBusy(ctx context.Context, tx *sql.Tx, titleID string, season int, numbers []int) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT number, covered_episodes FROM downloads
		WHERE title_id = ? AND season = ? AND state NOT IN ('done', 'failed')`, titleID, season)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var number int
		var stored string
		if err := rows.Scan(&number, &stored); err != nil {
			return false, err
		}
		for _, c := range append(decodeCovered(stored), number) {
			if slices.Contains(numbers, c) {
				return true, nil
			}
		}
	}
	return false, rows.Err()
}

// ActiveDownloadFor returns the unfinished download of a title or episode,
// the one that targets it or whose file covers it.
func (s *Store) ActiveDownloadFor(ctx context.Context, titleID string, season, number int) (Download, error) {
	ds, err := s.queryDownloads(ctx, `WHERE d.title_id = ? AND d.season = ? AND d.state NOT IN ('done', 'failed')
		ORDER BY d.number = ? DESC, d.created_at, d.rowid`, titleID, season, number)
	if err != nil {
		return Download{}, err
	}
	for _, d := range ds {
		if d.Number == number || slices.Contains(d.Covered, number) {
			return d, nil
		}
	}
	return Download{}, sql.ErrNoRows
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
		return s.queryDownloads(ctx, `WHERE d.state IN ('done', 'failed') AND d.title_id = ? ORDER BY d.at_ms DESC, d.rowid DESC`, titleID)
	}
	return s.queryDownloads(ctx, `WHERE d.state IN ('done', 'failed') ORDER BY d.at_ms DESC, d.rowid DESC`)
}

// DownloadsKeep is how many of the newest finished downloads are kept.
const DownloadsKeep = 5000

// pruneDownloads drops the finished downloads older than the newest
// downloadsKeep, except the latest of each movie and episode: the
// scheduler reads how a target last ended and a retry needs the failure.
// Running downloads are never touched, and the blocklist and the files
// the library has live elsewhere.
func (s *Store) pruneDownloads(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM downloads
		WHERE state IN ('done', 'failed')
		AND (at_ms, rowid) < (SELECT at_ms, rowid FROM downloads WHERE state IN ('done', 'failed')
			ORDER BY at_ms DESC, rowid DESC LIMIT 1 OFFSET ?)
		AND rowid != (SELECT l.rowid FROM downloads l
			WHERE l.title_id = downloads.title_id AND l.season = downloads.season AND l.number = downloads.number
			AND l.state IN ('done', 'failed') ORDER BY l.at_ms DESC, l.rowid DESC LIMIT 1)`, s.downloadsKeep-1)
	return err
}

// SetDownloadState moves an unfinished download to state. A download that
// is gone (its title was removed) or already finished answers ErrNotFound.
func (s *Store) SetDownloadState(ctx context.Context, id, state string) error {
	res, err := s.w.ExecContext(ctx, `UPDATE downloads SET state = ?, updated_at = ? WHERE id = ? AND state NOT IN ('done', 'failed')`, state, s.stamp(), id)
	return affected(res, err)
}

// SetDownloadCaching records how far TorBox has fetched a download's torrent,
// and which torrent and file that is. A download already transferring or
// further on keeps its state, so a late report never moves it back. A
// download that is gone or finished answers ErrNotFound.
func (s *Store) SetDownloadCaching(ctx context.Context, id string, percent, torrentID, fileID int) error {
	res, err := s.w.ExecContext(ctx, `UPDATE downloads SET state = 'caching', cache_percent = ?, torbox_torrent_id = ?, torbox_file_id = ?, updated_at = ?
		WHERE id = ? AND state IN ('queued', 'caching')`, percent, torrentID, fileID, s.stamp(), id)
	if err := affected(res, err); !errors.Is(err, ErrNotFound) {
		return err
	}
	var live int
	err = s.w.QueryRowContext(ctx, `SELECT 1 FROM downloads WHERE id = ? AND state NOT IN ('done', 'failed')`, id).Scan(&live)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// UncachedActive counts the downloads waiting for TorBox or being fetched by
// it, which is what the number of torrents TorBox may cache at once limits.
func (s *Store) UncachedActive(ctx context.Context) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM downloads WHERE cached = 0 AND state IN ('queued', 'caching')`).Scan(&n)
	return n, err
}

// TorrentInUse reports whether an unfinished download other than exceptID
// refers to the TorBox torrent, in which case it must not be deleted.
func (s *Store) TorrentInUse(ctx context.Context, torrentID int, exceptID string) (bool, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM downloads WHERE torbox_torrent_id = ? AND id != ? AND state NOT IN ('done', 'failed')`,
		torrentID, exceptID).Scan(&n)
	return n > 0, err
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

// File describes a video in the library: Path is relative to the movies or
// series folder, Fingerprint is importer.Fingerprint's. A zero File means
// nothing is known about it.
type File struct {
	Path        string
	Size        int64
	Fingerprint string
}

// FinishDownload marks an imported download done and the title or episode
// as having its file, together, records the file, and drops the playback
// link.
func (s *Store) FinishDownload(ctx context.Context, id string, f File) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var titleID, stored string
	var season, number int
	if err := tx.QueryRowContext(ctx, `SELECT title_id, season, number, covered_episodes FROM downloads WHERE id = ? AND state NOT IN ('done', 'failed')`, id).
		Scan(&titleID, &season, &number, &stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	numbers := []int{number}
	if c := decodeCovered(stored); len(c) > 0 {
		numbers = c
	}
	now := s.now()
	if _, err := tx.ExecContext(ctx, `UPDATE downloads SET state = 'done', url = '', bytes_done = bytes_total, updated_at = ?, at_ms = ? WHERE id = ?`,
		now.UTC().Format(time.RFC3339Nano), now.UnixMilli(), id); err != nil {
		return err
	}
	for _, n := range numbers {
		var err error
		if season == 0 && number == 0 {
			_, err = tx.ExecContext(ctx, `UPDATE titles SET have_file = 1, file_path = ?, file_size = ?, file_fingerprint = ? WHERE id = ?`,
				f.Path, f.Size, f.Fingerprint, titleID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE episodes SET have_file = 1, file_path = ?, file_size = ?, file_fingerprint = ?
				WHERE title_id = ? AND season = ? AND number = ?`, f.Path, f.Size, f.Fingerprint, titleID, season, n)
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schedule WHERE title_id = ? AND season = ? AND number = ?`, titleID, season, n); err != nil {
			return err
		}
	}
	if err := s.pruneDownloads(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// DuplicateEpisode finds an episode of the series, outside the given ones,
// that has a file with this fingerprint. numbers are the episodes of season
// the file being checked will hold, so the bytes a multi-episode file shares
// between its own episodes are no duplicate. It reports false when none has.
func (s *Store) DuplicateEpisode(ctx context.Context, titleID, fingerprint string, season int, numbers []int) (int, int, bool, error) {
	if fingerprint == "" {
		return 0, 0, false, nil
	}
	own, _ := json.Marshal(numbers)
	var sn, nr int
	err := s.r.QueryRowContext(ctx, `SELECT season, number FROM episodes
		WHERE title_id = ? AND file_fingerprint = ? AND have_file = 1
		AND NOT (season = ? AND number IN (SELECT value FROM json_each(?)))
		ORDER BY season, number LIMIT 1`, titleID, fingerprint, season, string(own)).Scan(&sn, &nr)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	return sn, nr, err == nil, err
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
	now := s.now()
	if _, err := tx.ExecContext(ctx, `UPDATE downloads SET state = 'failed', url = '', reason = ?, message = ?, updated_at = ?, at_ms = ? WHERE id = ?`,
		reason, message, now.UTC().Format(time.RFC3339Nano), now.UnixMilli(), id); err != nil {
		return err
	}
	if blocklist {
		if _, err := tx.ExecContext(ctx, `INSERT INTO blocklist (identity, blocked_at, reason) VALUES (?, ?, ?)
			ON CONFLICT (identity) DO UPDATE SET blocked_at = excluded.blocked_at, reason = excluded.reason`,
			chooser.IdentityOf(r.InfoHash, r.FileName, r.Size), now.UTC().Format(time.RFC3339), reason); err != nil {
			return err
		}
	}
	if err := s.pruneDownloads(ctx, tx); err != nil {
		return err
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
