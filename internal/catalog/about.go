package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// aboutKeep is how long an unused copy of a title's details is kept: a stale
// one is still better than an error when TMDB is down, but not for ever.
const aboutKeep = 30 * 24 * time.Hour

// PersonPhoto is TMDB's file path for a cast member's photo.
type PersonPhoto struct {
	ID   int
	Path string
}

// CachedAbout returns the last details document stored for a title and when it was stored.
func (s *Store) CachedAbout(ctx context.Context, titleID string) (body []byte, at time.Time, found bool, err error) {
	var ms int64
	err = s.r.QueryRowContext(ctx, `SELECT body, fetched_ms FROM about_cache WHERE title_id = ?`, titleID).Scan(&body, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	return body, time.UnixMilli(ms).UTC(), true, nil
}

// RememberAbout stores a title's details document, stamped with the store's
// clock, together with the cast photos it links, and forgets documents too
// old to be worth serving.
func (s *Store) RememberAbout(ctx context.Context, titleID string, body []byte, people []PersonPhoto) error {
	now := s.now()
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO about_cache (title_id, fetched_ms, body) VALUES (?, ?, ?)
		ON CONFLICT(title_id) DO UPDATE SET fetched_ms = excluded.fetched_ms, body = excluded.body`, titleID, now.UnixMilli(), string(body)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM about_cache WHERE fetched_ms < ?`, now.Add(-aboutKeep).UnixMilli()); err != nil {
		return err
	}
	for _, p := range people {
		if _, err := tx.ExecContext(ctx, `INSERT INTO people (person_id, profile_path) VALUES (?, ?)
			ON CONFLICT(person_id) DO UPDATE SET profile_path = excluded.profile_path`, p.ID, p.Path); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PersonPhotoPath returns TMDB's file path for a cast member's photo; found
// is false for a person no listed cast has shown.
func (s *Store) PersonPhotoPath(ctx context.Context, personID int) (path string, found bool, err error) {
	err = s.r.QueryRowContext(ctx, `SELECT profile_path FROM people WHERE person_id = ?`, personID).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return path, err == nil, err
}
