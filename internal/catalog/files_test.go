package catalog_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/davepintodev/marquee/internal/catalog"
)

func TestTheFileColumnsMigrationKeepsEveryRowOfADatabaseAtSchemaEleven(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := catalog.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	movie := addMovie(t, s, 1)
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 2, Title: "House", Episodes: houseEpisodes()}); err != nil {
		t.Fatal(err)
	}
	series := "series-2"
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, catalog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(dropFileColumns + `DROP TABLE run_mode; PRAGMA user_version = 11`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = catalog.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, id := range []string{movie, series} {
		if _, err := s.Get(ctx, id); err != nil {
			t.Errorf("title %s lost by the migration: %v", id, err)
		}
	}
	db2, err := sql.Open("sqlite", filepath.Join(dir, catalog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	for _, table := range []string{"titles", "episodes"} {
		var n int
		if err := db2.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('` + table + `') WHERE name IN ('file_path', 'file_size', 'file_fingerprint')`).Scan(&n); err != nil || n != 3 {
			t.Errorf("%s has %d of the 3 file columns (err %v)", table, n, err)
		}
	}
}
