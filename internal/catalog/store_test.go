package catalog_test

import (
	"context"
	"testing"
	"time"

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

func openAt(t *testing.T, now time.Time) *catalog.Store {
	t.Helper()
	s, err := catalog.Open(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func monitoredEpisodes(t *testing.T, s *catalog.Store, id string) map[string]bool {
	t.Helper()
	title, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, se := range title.Seasons {
		for _, e := range se.Episodes {
			out[catalog.EpisodeID(se.Number, e.Number)] = e.Monitored
		}
	}
	return out
}

func checkMonitored(t *testing.T, got map[string]bool, want map[string]bool) {
	t.Helper()
	for id, w := range want {
		if g, ok := got[id]; !ok || g != w {
			t.Errorf("%s monitored = %v (present %v), want %v", id, g, ok, w)
		}
	}
}

func houseEpisodes() []catalog.NewEpisode {
	var eps []catalog.NewEpisode
	for s := 1; s <= 4; s++ {
		eps = append(eps, catalog.NewEpisode{Season: s, Number: 1, Name: "One", AirDate: "2005-01-01"},
			catalog.NewEpisode{Season: s, Number: 2, Name: "Two", AirDate: "2005-01-08"})
	}
	return eps
}

func TestARefreshOfAnAdoptedSeriesMonitorsOnlyFutureEpisodesAndThoseOfSeasonsOnDisk(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	house := catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "House", Episodes: houseEpisodes()}
	_, _, err := s.Adopt(ctx, house, catalog.Found{Episodes: []catalog.EpisodeRef{{Season: 2, Number: 1}, {Season: 3, Number: 1}, {Season: 4, Number: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	err = s.ApplyRefresh(ctx, "series-1", catalog.Refresh{Episodes: append(houseEpisodes(),
		catalog.NewEpisode{Season: 1, Number: 3, Name: "Late dated", AirDate: "2005-01-15"},
		catalog.NewEpisode{Season: 1, Number: 4, Name: "Future", AirDate: "2027-01-01"},
		catalog.NewEpisode{Season: 3, Number: 3, Name: "Aired", AirDate: "2005-01-15"},
		catalog.NewEpisode{Season: 5, Number: 1, Name: "New season aired", AirDate: "2026-11-01"},
		catalog.NewEpisode{Season: 5, Number: 2, Name: "New season future", AirDate: "2027-01-01"},
	)})
	if err != nil {
		t.Fatal(err)
	}
	checkMonitored(t, monitoredEpisodes(t, s, "series-1"), map[string]bool{
		"s01e01": false, "s01e02": false, "s02e02": true, "s03e02": true,
		"s01e03": false, "s01e04": true, "s03e03": true, "s05e01": false, "s05e02": true,
	})
}

func TestARefreshOfAnAdoptedSeriesWithNoFilesMonitorsOnlyFutureEpisodes(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	if _, _, err := s.Adopt(ctx, catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "House", Episodes: houseEpisodes()}, catalog.Found{}); err != nil {
		t.Fatal(err)
	}
	err := s.ApplyRefresh(ctx, "series-1", catalog.Refresh{Episodes: append(houseEpisodes(),
		catalog.NewEpisode{Season: 1, Number: 3, Name: "Aired", AirDate: "2005-01-15"},
		catalog.NewEpisode{Season: 1, Number: 4, Name: "Future", AirDate: "2027-01-01"},
	)})
	if err != nil {
		t.Fatal(err)
	}
	checkMonitored(t, monitoredEpisodes(t, s, "series-1"), map[string]bool{"s01e03": false, "s01e04": true})
}

func TestARefreshOfASeriesTheOwnerAddedMonitorsEveryNewEpisode(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	house := catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "House", Episodes: houseEpisodes()}
	if _, _, err := s.Add(ctx, house); err != nil {
		t.Fatal(err)
	}
	// Adopting a title the owner already added keeps it an owner's series.
	if _, _, err := s.Adopt(ctx, house, catalog.Found{Episodes: []catalog.EpisodeRef{{Season: 2, Number: 1}}}); err != nil {
		t.Fatal(err)
	}
	err := s.ApplyRefresh(ctx, "series-1", catalog.Refresh{Episodes: append(houseEpisodes(),
		catalog.NewEpisode{Season: 1, Number: 3, Name: "Aired", AirDate: "2005-01-15"},
		catalog.NewEpisode{Season: 1, Number: 4, Name: "Future", AirDate: "2027-01-01"},
	)})
	if err != nil {
		t.Fatal(err)
	}
	checkMonitored(t, monitoredEpisodes(t, s, "series-1"), map[string]bool{"s01e03": true, "s01e04": true})
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
