// Package catalog owns marquee's database: titles, their episodes, the
// monitored state and the TMDB image paths. It knows nothing about files in
// a library, so nothing it does can touch one.
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("catalog: not found")
	ErrInvalid  = errors.New("catalog: invalid value")
)

// Writes share one connection so they never contend; WAL lets the read
// pool run during a write.
type Store struct {
	w, r          *sql.DB
	now           func() time.Time
	activityKeep  int
	downloadsKeep int
}

// FileName is the database file inside the data directory.
const FileName = "marquee.db"

// Open creates or migrates the database in dir. now judges which episodes
// have aired; nil means the system clock.
func Open(dir string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("catalog: cannot create the data directory: %w", err)
	}
	abs, err := filepath.Abs(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: url.Values{"_pragma": {
		"busy_timeout(5000)", "journal_mode(WAL)", "foreign_keys(1)", "synchronous(NORMAL)",
	}}.Encode()}).String()

	s := &Store{now: now, activityKeep: ActivityKeep, downloadsKeep: DownloadsKeep}
	if s.w, err = sql.Open("sqlite", dsn); err != nil {
		return nil, err
	}
	s.w.SetMaxOpenConns(1)
	if err := migrate(context.Background(), s.w); err != nil {
		_ = s.w.Close()
		return nil, err
	}
	if s.r, err = sql.Open("sqlite", dsn); err != nil {
		_ = s.w.Close()
		return nil, err
	}
	s.r.SetMaxOpenConns(4)
	return s, nil
}

func (s *Store) Close() error {
	return errors.Join(s.r.Close(), s.w.Close())
}

// Each entry runs once, in order, and is never edited after release;
// the database's user_version counts how many have run.
var migrations = []string{`
CREATE TABLE titles (
  id                TEXT PRIMARY KEY,
  kind              TEXT NOT NULL CHECK (kind IN ('movie', 'series')),
  tmdb_id           INTEGER NOT NULL,
  title             TEXT NOT NULL,
  year              INTEGER NOT NULL,
  overview          TEXT NOT NULL,
  imdb_id           TEXT NOT NULL DEFAULT '',
  tvdb_id           INTEGER NOT NULL DEFAULT 0,
  monitored         INTEGER NOT NULL,
  preferred_quality TEXT,
  have_file         INTEGER NOT NULL DEFAULT 0,
  added_at          TEXT NOT NULL
);
CREATE TABLE episodes (
  title_id  TEXT NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
  season    INTEGER NOT NULL,
  number    INTEGER NOT NULL,
  name      TEXT NOT NULL,
  air_date  TEXT NOT NULL,
  monitored INTEGER NOT NULL,
  have_file INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (title_id, season, number)
) WITHOUT ROWID;
CREATE TABLE images (
  title_id  TEXT NOT NULL,
  kind      TEXT NOT NULL CHECK (kind IN ('poster', 'backdrop')),
  tmdb_path TEXT NOT NULL,
  PRIMARY KEY (title_id, kind)
) WITHOUT ROWID;
`, `
CREATE TABLE blocklist (
  identity   TEXT PRIMARY KEY,
  blocked_at TEXT NOT NULL,
  reason     TEXT NOT NULL
) WITHOUT ROWID;
`, `
CREATE TABLE downloads (
  id           TEXT PRIMARY KEY,
  title_id     TEXT NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
  season       INTEGER NOT NULL DEFAULT 0,
  number       INTEGER NOT NULL DEFAULT 0,
  state        TEXT NOT NULL CHECK (state IN ('queued', 'downloading', 'verifying', 'importing', 'done', 'failed')),
  info_hash    TEXT NOT NULL,
  file_name    TEXT NOT NULL,
  size         INTEGER NOT NULL,
  release_name TEXT NOT NULL,
  resolution   TEXT NOT NULL DEFAULT '',
  quality      TEXT NOT NULL DEFAULT '',
  url          TEXT NOT NULL,
  bytes_done   INTEGER NOT NULL DEFAULT 0,
  bytes_total  INTEGER NOT NULL DEFAULT 0,
  reason       TEXT NOT NULL DEFAULT '',
  message      TEXT NOT NULL DEFAULT '',
  library_path TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE UNIQUE INDEX downloads_one_active ON downloads (title_id, season, number)
  WHERE state NOT IN ('done', 'failed');
CREATE TABLE component_status (
  component TEXT PRIMARY KEY,
  ok        INTEGER NOT NULL,
  message   TEXT NOT NULL,
  at        TEXT NOT NULL
) WITHOUT ROWID;
`, `
CREATE TABLE schedule (
  title_id  TEXT NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
  season    INTEGER NOT NULL,
  number    INTEGER NOT NULL,
  attempts  INTEGER NOT NULL DEFAULT 0,
  next_at   INTEGER,
  forced    INTEGER NOT NULL DEFAULT 0,
  follow_up INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (title_id, season, number)
) WITHOUT ROWID;
CREATE TABLE search_budget (
  title_id TEXT PRIMARY KEY REFERENCES titles(id) ON DELETE CASCADE,
  day      TEXT NOT NULL,
  used     INTEGER NOT NULL
) WITHOUT ROWID;
CREATE TABLE series_refresh (
  title_id TEXT PRIMARY KEY REFERENCES titles(id) ON DELETE CASCADE,
  due_at   INTEGER NOT NULL
) WITHOUT ROWID;
INSERT INTO series_refresh (title_id, due_at) SELECT id, 0 FROM titles WHERE kind = 'series';
CREATE TABLE source_backoff (
  id       INTEGER PRIMARY KEY CHECK (id = 1),
  failures INTEGER NOT NULL,
  until    INTEGER NOT NULL
);
CREATE TABLE activity (
  id         TEXT PRIMARY KEY,
  title_id   TEXT NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
  season     INTEGER NOT NULL DEFAULT 0,
  number     INTEGER NOT NULL DEFAULT 0,
  kind       TEXT NOT NULL,
  reason     TEXT NOT NULL DEFAULT '',
  message    TEXT NOT NULL,
  release_id TEXT NOT NULL DEFAULT '',
  at         TEXT NOT NULL
);
CREATE INDEX activity_by_title ON activity (title_id);
`, `
CREATE TABLE library_problems (
  path    TEXT PRIMARY KEY,
  reason  TEXT NOT NULL CHECK (reason IN ('unmatched', 'ambiguous', 'unparsed_file')),
  message TEXT NOT NULL
) WITHOUT ROWID;
CREATE TABLE adoption (
  done INTEGER PRIMARY KEY CHECK (done = 1),
  at   TEXT NOT NULL
);
`, `
ALTER TABLE titles ADD COLUMN adopted INTEGER NOT NULL DEFAULT 0;
`, `
CREATE INDEX activity_by_target ON activity (title_id, season, number, kind);
`, `
INSERT OR IGNORE INTO series_refresh (title_id, due_at) SELECT id, 0 FROM titles WHERE kind = 'series';
`, `
ALTER TABLE downloads ADD COLUMN at_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE activity ADD COLUMN at_ms INTEGER NOT NULL DEFAULT 0;
UPDATE downloads SET at_ms = CAST(ROUND((julianday(updated_at) - 2440587.5) * 86400000) AS INTEGER)
  WHERE state IN ('done', 'failed');
UPDATE activity SET at_ms = CAST(ROUND((julianday(at) - 2440587.5) * 86400000) AS INTEGER);
CREATE INDEX downloads_finished ON downloads (at_ms) WHERE state IN ('done', 'failed');
CREATE INDEX downloads_finished_by_target ON downloads (title_id, season, number, at_ms) WHERE state IN ('done', 'failed');
CREATE INDEX activity_by_at ON activity (at_ms);
`, `
CREATE TABLE IF NOT EXISTS about_cache (
  title_id   TEXT PRIMARY KEY,
  fetched_ms INTEGER NOT NULL,
  body       TEXT NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS people (
  person_id    INTEGER PRIMARY KEY,
  profile_path TEXT NOT NULL
);
`, `
ALTER TABLE titles ADD COLUMN monitor_new INTEGER NOT NULL DEFAULT 1;
UPDATE titles SET monitor_new = monitored;
`, `
ALTER TABLE titles ADD COLUMN file_path TEXT NOT NULL DEFAULT '';
ALTER TABLE titles ADD COLUMN file_size INTEGER NOT NULL DEFAULT 0;
ALTER TABLE titles ADD COLUMN file_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN file_path TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN file_size INTEGER NOT NULL DEFAULT 0;
ALTER TABLE episodes ADD COLUMN file_fingerprint TEXT NOT NULL DEFAULT '';
CREATE INDEX episodes_by_fingerprint ON episodes (title_id, file_fingerprint) WHERE file_fingerprint != '';
`, `
ALTER TABLE titles ADD COLUMN profile TEXT NOT NULL DEFAULT '';
ALTER TABLE titles DROP COLUMN preferred_quality;
`}

func migrate(ctx context.Context, db *sql.DB) error {
	var have int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&have); err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	if have > len(migrations) {
		return fmt.Errorf("catalog: database is from a newer marquee (schema %d, this one knows %d)", have, len(migrations))
	}
	for i := have; i < len(migrations); i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("catalog: %w", err)
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("catalog: migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("catalog: migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("catalog: migration %d: %w", i+1, err)
		}
	}
	return nil
}

// Now is the store's clock, the one that stamps its rows.
func (s *Store) Now() time.Time { return s.now() }
