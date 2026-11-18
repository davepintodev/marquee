package catalog

import (
	"context"
	"fmt"
	"time"
)

// Block records that a release, named by its identity, must not be tried
// again. Blocking one already blocked renews the entry.
func (s *Store) Block(ctx context.Context, identity, reason string) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO blocklist (identity, blocked_at, reason) VALUES (?, ?, ?)
		 ON CONFLICT (identity) DO UPDATE SET blocked_at = excluded.blocked_at, reason = excluded.reason`,
		identity, s.now().UTC().Format(time.RFC3339), reason)
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	return nil
}

// Blocklist returns every entry with when it was blocked. It does not
// judge expiry: the chooser does, with the clock it is given.
func (s *Store) Blocklist(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT identity, blocked_at FROM blocklist`)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]time.Time{}
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			return nil, fmt.Errorf("catalog: %w", err)
		}
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return nil, fmt.Errorf("catalog: blocklist entry has a bad time: %w", err)
		}
		out[id] = t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	return out, nil
}
