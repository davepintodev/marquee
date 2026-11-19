package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sources of the history feed, in the order they rank at one instant:
// downloads come before the scheduler's notes.
const (
	srcNote     = 0
	srcDownload = 1
)

// historyKey places an entry in the feed. It is unique, because rowid is
// unique within a source, and never changes for a row that exists, so a
// page can be cut after it whatever else arrives or leaves.
type historyKey struct {
	atMs  int64
	src   int
	rowid int64
}

func (k historyKey) String() string { return fmt.Sprintf("%d.%d.%d", k.atMs, k.src, k.rowid) }

// after reports whether k is newer than o, the feed's order.
func (k historyKey) after(o historyKey) bool {
	if k.atMs != o.atMs {
		return k.atMs > o.atMs
	}
	if k.src != o.src {
		return k.src > o.src
	}
	return k.rowid > o.rowid
}

func parseHistoryKey(s string) (historyKey, error) {
	parts := strings.Split(s, ".")
	if len(parts) == 3 {
		var k historyKey
		var err1, err2, err3 error
		var src int64
		k.atMs, err1 = strconv.ParseInt(parts[0], 10, 64)
		src, err2 = strconv.ParseInt(parts[1], 10, 64)
		k.rowid, err3 = strconv.ParseInt(parts[2], 10, 64)
		k.src = int(src)
		// Only what String wrote is accepted, so no two cursors name one key.
		if err1 == nil && err2 == nil && err3 == nil && k.atMs >= 0 && (k.src == srcNote || k.src == srcDownload) && k.rowid > 0 && k.String() == s {
			return k, nil
		}
	}
	return historyKey{}, fmt.Errorf("%w: not a history cursor", ErrInvalid)
}

// HistoryItem is one entry of the feed: a finished download or a note of
// the scheduler.
type HistoryItem struct {
	Download *Download
	Activity *Activity
	// Retryable is set for a failed download that is the latest outcome of
	// its movie or episode, with no download running for it.
	Retryable bool
	key       historyKey
}

// HistoryPage is a page of the feed, newest first. Next is the cursor of
// the page after it, empty on the last page.
type HistoryPage struct {
	Items []HistoryItem
	Next  string
}

// retryableSQL is true for a failed download d that no later finished
// download and no running download of the same target has replaced.
const retryableSQL = `(d.state = 'failed' AND NOT EXISTS (SELECT 1 FROM downloads n
	WHERE n.title_id = d.title_id AND n.season = d.season AND n.number = d.number AND n.id != d.id
	AND n.state NOT IN ('done', 'failed'))
	AND NOT EXISTS (SELECT 1 FROM downloads n
	WHERE n.title_id = d.title_id AND n.season = d.season AND n.number = d.number AND n.id != d.id
	AND n.state IN ('done', 'failed') AND (n.at_ms, n.rowid) > (d.at_ms, d.rowid)))`

// Retryable reports whether the download is a failure worth retrying. A
// download that is gone answers ErrNotFound.
func (s *Store) Retryable(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.r.QueryRowContext(ctx, `SELECT `+retryableSQL+` FROM downloads d WHERE d.id = ?`, id).Scan(&ok)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return ok, err
}

// History pages through what happened, newest first, to one title or, with
// an empty titleID, to all of them. cursor is the Next of the previous
// page, empty for the newest entries; a cursor the store did not give
// answers ErrInvalid. A limit of zero or less means every entry.
func (s *Store) History(ctx context.Context, titleID, cursor string, limit int) (HistoryPage, error) {
	var from *historyKey
	if cursor != "" {
		k, err := parseHistoryKey(cursor)
		if err != nil {
			return HistoryPage{}, err
		}
		from = &k
	}
	sqlLimit := -1
	if limit > 0 {
		sqlLimit = limit + 1
	}
	items, err := s.historyDownloads(ctx, titleID, from, sqlLimit)
	if err != nil {
		return HistoryPage{}, err
	}
	notes, err := s.historyNotes(ctx, titleID, from, sqlLimit)
	if err != nil {
		return HistoryPage{}, err
	}
	items = append(items, notes...)
	sort.Slice(items, func(i, j int) bool { return items[i].key.after(items[j].key) })
	page := HistoryPage{Items: items}
	if limit > 0 && len(items) > limit {
		page.Items = items[:limit]
		page.Next = items[limit-1].key.String()
	}
	return page, nil
}

func olderThan(alias string, src int, from *historyKey) (string, []any) {
	if from == nil {
		return "", nil
	}
	switch {
	case src == from.src:
		return " AND (" + alias + ".at_ms, " + alias + ".rowid) < (?, ?)", []any{from.atMs, from.rowid}
	case src > from.src:
		return " AND " + alias + ".at_ms < ?", []any{from.atMs}
	default:
		return " AND " + alias + ".at_ms <= ?", []any{from.atMs}
	}
}

func (s *Store) historyDownloads(ctx context.Context, titleID string, from *historyKey, limit int) ([]HistoryItem, error) {
	q := `SELECT ` + downloadColumns + `, d.at_ms, d.rowid, ` + retryableSQL + `
		FROM downloads d JOIN titles t ON t.id = d.title_id WHERE d.state IN ('done', 'failed')`
	var args []any
	if titleID != "" {
		q += ` AND d.title_id = ?`
		args = append(args, titleID)
	}
	cond, condArgs := olderThan("d", srcDownload, from)
	rows, err := s.r.QueryContext(ctx, q+cond+` ORDER BY d.at_ms DESC, d.rowid DESC LIMIT ?`, append(append(args, condArgs...), limit)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HistoryItem
	for rows.Next() {
		var it HistoryItem
		k := historyKey{src: srcDownload}
		var retryable bool
		d, err := scanDownload(rows, &k.atMs, &k.rowid, &retryable)
		if err != nil {
			return nil, err
		}
		it.Download, it.Retryable, it.key = &d, retryable, k
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) historyNotes(ctx context.Context, titleID string, from *historyKey, limit int) ([]HistoryItem, error) {
	q := `SELECT a.id, a.at, a.title_id, t.title, a.season, a.number, a.kind, a.reason, a.message, a.release_id, a.at_ms, a.rowid
		FROM activity a JOIN titles t ON t.id = a.title_id WHERE 1 = 1`
	var args []any
	if titleID != "" {
		q += ` AND a.title_id = ?`
		args = append(args, titleID)
	}
	cond, condArgs := olderThan("a", srcNote, from)
	rows, err := s.r.QueryContext(ctx, q+cond+` ORDER BY a.at_ms DESC, a.rowid DESC LIMIT ?`, append(append(args, condArgs...), limit)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HistoryItem
	for rows.Next() {
		var a Activity
		var at string
		k := historyKey{src: srcNote}
		if err := rows.Scan(&a.ID, &at, &a.TitleID, &a.TitleName, &a.Season, &a.Number, &a.Kind, &a.Reason, &a.Message, &a.ReleaseID, &k.atMs, &k.rowid); err != nil {
			return nil, err
		}
		a.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, HistoryItem{Activity: &a, key: k})
	}
	return out, rows.Err()
}
