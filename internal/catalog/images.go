package catalog

import (
	"context"
	"database/sql"
	"errors"
)

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func rememberImage(ctx context.Context, e execer, r ImageRef) error {
	_, err := e.ExecContext(ctx, `INSERT INTO images (title_id, kind, tmdb_path) VALUES (?, ?, ?)
		ON CONFLICT(title_id, kind) DO UPDATE SET tmdb_path = excluded.tmdb_path`, r.TitleID, r.Kind, r.Path)
	return err
}

// RememberImages records TMDB's poster and backdrop paths for titles seen
// in search or trending, so an image request needs no TMDB lookup.
func (s *Store) RememberImages(ctx context.Context, refs []ImageRef) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range refs {
		if err := rememberImage(ctx, tx, r); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ImagePath returns TMDB's file path for a title's image. found is false
// when the title's images were never seen; the path is empty when TMDB has none.
func (s *Store) ImagePath(ctx context.Context, titleID, kind string) (path string, found bool, err error) {
	err = s.r.QueryRowContext(ctx, `SELECT tmdb_path FROM images WHERE title_id = ? AND kind = ?`, titleID, kind).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return path, err == nil, err
}
