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

	_ "modernc.org/sqlite" // pure-Go driver: the one container needs no cgo
)

var (
	ErrNotFound = errors.New("catalog: not found")
	ErrInvalid  = errors.New("catalog: invalid value")
)

// Store is the catalog. All writes go through one connection, so they
// never contend with each other; reads use a small pool beside it (WAL
// lets them run during a write).
type Store struct {
	w, r *sql.DB
	now  func() time.Time
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

	s := &Store{now: now}
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
