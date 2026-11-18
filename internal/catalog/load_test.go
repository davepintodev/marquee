package catalog

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

type recorder struct {
	q    queryer
	sqls []string
	args [][]any
}

func (r *recorder) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	r.sqls, r.args = append(r.sqls, q), append(r.args, args)
	return r.q.QueryContext(ctx, q, args...)
}

// Loading one title must read only that title's episodes: the work of a
// request may not grow with the other series in the library.
func TestLoadingOneTitleDoesNotScanTheEpisodesOfOthers(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for id := 1; id <= 3; id++ {
		eps := []NewEpisode{{Season: 1, Number: 1, Name: "One", AirDate: "2020-01-01"}, {Season: 1, Number: 2, Name: "Two", AirDate: "2020-01-02"}}
		if _, _, err := s.Add(ctx, NewTitle{Kind: "series", TMDBID: id, Title: "Show", Episodes: eps}); err != nil {
			t.Fatal(err)
		}
	}

	rec := &recorder{q: s.r}
	ts, err := s.load(ctx, rec, `WHERE id = ?`, "series-2")
	if err != nil || len(ts) != 1 || len(ts[0].Seasons[0].Episodes) != 2 {
		t.Fatalf("load: %v %+v", err, ts)
	}
	for i, q := range rec.sqls {
		if !strings.Contains(q, "FROM episodes") {
			continue
		}
		rows, err := s.r.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q, rec.args[i]...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN episodes") {
				t.Errorf("episodes are scanned whole: %s\nquery: %s", detail, q)
			}
		}
		_ = rows.Close()
	}
}
