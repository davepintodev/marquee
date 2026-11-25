package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RecordRunMode stores whether this start is a dry-run and reports whether
// it left dry-run for live. Then, and only then, the backoff of every
// search target and the search budget are cleared: a dry-run advances them
// although nothing was grabbed, and they would hold the titles back for up
// to a day. A catalog with no recorded mode is never reset, because it may
// be a live one from before the mode was recorded.
func (s *Store) RecordRunMode(ctx context.Context, dryRun bool) (reset bool, err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("catalog: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var last bool
	switch err := tx.QueryRowContext(ctx, `SELECT last_dry_run FROM run_mode WHERE id = 1`).Scan(&last); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, fmt.Errorf("catalog: %w", err)
	default:
		reset = last && !dryRun
	}
	if reset {
		if _, err := tx.ExecContext(ctx, `UPDATE schedule SET attempts = 0, next_at = NULL`); err != nil {
			return false, fmt.Errorf("catalog: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_budget`); err != nil {
			return false, fmt.Errorf("catalog: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_mode (id, last_dry_run) VALUES (1, ?1)
		ON CONFLICT (id) DO UPDATE SET last_dry_run = excluded.last_dry_run`, b2i(dryRun)); err != nil {
		return false, fmt.Errorf("catalog: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("catalog: %w", err)
	}
	return reset, nil
}
