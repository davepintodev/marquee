package catalog_test

import (
	"context"
	"testing"

	"github.com/davepintodev/marquee/internal/catalog"
)

// External ids are stored for the Source module and no API route shows
// them yet, so this is the one place they are checked.
func TestExternalIDsAndMigrationsSurviveReopening(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := catalog.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, created, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 5, Title: "Show", IMDbID: "tt123", TVDBID: 456})
	if err != nil || !created {
		t.Fatalf("add: %v %v", created, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = catalog.Open(dir, nil)
	if err != nil {
		t.Fatalf("reopening runs no migration twice: %v", err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.Get(ctx, "series-5")
	if err != nil {
		t.Fatal(err)
	}
	if got.IMDbID != "tt123" || got.TVDBID != 456 {
		t.Errorf("external ids: %+v", got)
	}
}

func TestActivityKeepsTheNewestEntriesAndTheLastDecisionOfEachTarget(t *testing.T) {
	ctx := context.Background()
	s, err := catalog.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.SetActivityKeep(5)
	for _, tmdb := range []int{1, 2} {
		if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "movie", TMDBID: tmdb, Title: "Film"}); err != nil {
			t.Fatal(err)
		}
	}
	add := func(id, kind, release string) {
		t.Helper()
		if err := s.AddActivity(ctx, catalog.Activity{TitleID: id, Kind: kind, Message: "m", ReleaseID: release}); err != nil {
			t.Fatal(err)
		}
	}
	add("movie-1", catalog.ActivityWouldGrab, "old")
	add("movie-1", catalog.ActivityWouldGrab, "rel-1")
	add("movie-2", catalog.ActivityWouldGrab, "rel-2")
	for range 20 {
		add("movie-1", catalog.ActivitySearched, "")
	}

	all, err := s.Activities(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 7 {
		t.Fatalf("5 newest plus the last would-grab of each of the 2 targets: %d entries", len(all))
	}
	for i, e := range all[:5] {
		if e.Kind != catalog.ActivitySearched {
			t.Errorf("entry %d of the newest five is %q", i, e.Kind)
		}
	}
	for tmdb, want := range map[string]string{"movie-1": "rel-1", "movie-2": "rel-2"} {
		got, had, err := s.LastWouldGrab(ctx, tmdb, 0, 0)
		if err != nil || !had || got != want {
			t.Errorf("last would-grab of %s = %q %v %v, want %q", tmdb, got, had, err, want)
		}
	}
}
