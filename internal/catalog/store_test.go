package catalog_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
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

func TestTheRefreshBackfillGivesOnlyAdoptedSeriesARowAndKeepsTheOthers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s, err := catalog.Open(dir, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "Added"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "movie", TMDBID: 2, Title: "Film"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Adopt(ctx, catalog.NewTitle{Kind: "series", TMDBID: 3, Title: "Adopted"}, catalog.Found{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, catalog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(dropAtMs+`DELETE FROM series_refresh WHERE title_id = 'series-3'; DROP TABLE run_mode; PRAGMA user_version = 7`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = catalog.Open(dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("the backfill must not fail on a library that already has refresh rows: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	due := func(at time.Time) []string {
		targets, err := s.RefreshDue(ctx, at)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, tg := range targets {
			ids = append(ids, tg.TitleID)
		}
		slices.Sort(ids)
		return ids
	}
	if got := due(now); !slices.Equal(got, []string{"series-3"}) {
		t.Errorf("due right after the upgrade = %v, want only the backfilled series-3 (the added series keeps its day, the movie has none)", got)
	}
	if got := due(now.Add(catalog.RefreshEvery)); !slices.Equal(got, []string{"series-1", "series-3"}) {
		t.Errorf("due a day later = %v, want series-1 and series-3 and no movie", got)
	}
}

func TestAddChoosesEpisodesToMonitorAtTheEdges(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	special := catalog.NewEpisode{Season: 0, Number: 1, Name: "Special", AirDate: "2020-01-01"}
	s3 := catalog.NewEpisode{Season: 3, Number: 1, Name: "Three", AirDate: "2026-11-18"}
	cases := []struct {
		name      string
		monitor   string
		episodes  []catalog.NewEpisode
		monitored []string
		title     bool
	}{
		{"specials only never match a season choice", catalog.MonitorFirstSeason, []catalog.NewEpisode{special}, nil, true},
		{"nothing to air leaves a finished series with no episode monitored", catalog.MonitorFuture, []catalog.NewEpisode{s3}, nil, true},
		{"an episode airing today has aired", catalog.MonitorFuture, []catalog.NewEpisode{s3, {Season: 3, Number: 2, Name: "Next", AirDate: "2026-11-19"}}, []string{"s03e02"}, true},
		{"seasons need not start at one", catalog.MonitorFirstSeason, []catalog.NewEpisode{s3, {Season: 5, Number: 1, Name: "Five", AirDate: "2026-01-01"}}, []string{"s03e01"}, true},
		{"the latest season skips specials", catalog.MonitorLatestSeason, []catalog.NewEpisode{special, s3}, []string{"s03e01"}, true},
		{"a series with no episodes yet", catalog.MonitorLatestSeason, nil, nil, true},
		{"none", catalog.MonitorNone, []catalog.NewEpisode{s3}, nil, false},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := openAt(t, now)
			title, _, err := s.Add(context.Background(), catalog.NewTitle{Kind: "series", TMDBID: i + 1, Title: "Show", Monitor: c.monitor, Episodes: c.episodes})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for id, m := range monitoredEpisodes(t, s, title.ID) {
				if m {
					got = append(got, id)
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, c.monitored) || title.Monitored != c.title {
				t.Errorf("monitored %v title %v, want %v %v", got, title.Monitored, c.monitored, c.title)
			}
		})
	}
}

func TestAddRefusesAnUnknownMonitorChoiceAndStoresNothing(t *testing.T) {
	s := openAt(t, time.Now())
	if _, _, err := s.Add(context.Background(), catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "Show", Monitor: "some"}); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.Get(context.Background(), "series-1"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func TestSetSeasonMonitoredRollsBackOnAnUnknownSeason(t *testing.T) {
	s := openAt(t, time.Now())
	ctx := context.Background()
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "Show", Monitor: catalog.MonitorNone,
		Episodes: []catalog.NewEpisode{{Season: 1, Number: 1, Name: "One", AirDate: "2020-01-01"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSeasonMonitored(ctx, "series-1", 2, true); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if got, _ := s.Get(ctx, "series-1"); got.Monitored {
		t.Error("a refused call must not monitor the title")
	}
	if _, err := s.SetSeasonMonitored(ctx, "series-9", 1, true); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestARefreshMonitorsNewEpisodesByTheRuleTheSeriesWasAddedWith(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		monitor string
		want    map[string]bool
	}{
		{"", map[string]bool{"s01e03": true, "s05e01": true, "s05e02": true}},
		{catalog.MonitorAll, map[string]bool{"s01e03": true, "s05e01": true, "s05e02": true}},
		{catalog.MonitorFuture, map[string]bool{"s01e03": true, "s05e01": true, "s05e02": true}},
		{catalog.MonitorFirstSeason, map[string]bool{"s01e03": false, "s05e01": false, "s05e02": false}},
		{catalog.MonitorLatestSeason, map[string]bool{"s01e03": false, "s05e01": false, "s05e02": false}},
	}
	for _, c := range cases {
		t.Run(c.monitor, func(t *testing.T) {
			ctx := context.Background()
			s := openAt(t, now)
			if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "House", Monitor: c.monitor, Episodes: houseEpisodes()}); err != nil {
				t.Fatal(err)
			}
			err := s.ApplyRefresh(ctx, "series-1", catalog.Refresh{Episodes: append(houseEpisodes(),
				catalog.NewEpisode{Season: 1, Number: 3, Name: "Late", AirDate: "2005-01-15"},
				catalog.NewEpisode{Season: 5, Number: 1, Name: "New season aired", AirDate: "2026-11-01"},
				catalog.NewEpisode{Season: 5, Number: 2, Name: "New season future", AirDate: "2027-01-01"},
			)})
			if err != nil {
				t.Fatal(err)
			}
			checkMonitored(t, monitoredEpisodes(t, s, "series-1"), c.want)
		})
	}
}

func TestARefreshOfASeriesAddedWithNoneStaysUnmonitoredUntilTheOwnerMonitorsTheSeries(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "House", Monitor: catalog.MonitorNone, Episodes: houseEpisodes()}); err != nil {
		t.Fatal(err)
	}
	refresh := func(number int) {
		t.Helper()
		err := s.ApplyRefresh(ctx, "series-1", catalog.Refresh{Episodes: append(houseEpisodes(),
			catalog.NewEpisode{Season: 1, Number: number, Name: "New", AirDate: "2027-01-01"})})
		if err != nil {
			t.Fatal(err)
		}
	}
	refresh(3)
	checkMonitored(t, monitoredEpisodes(t, s, "series-1"), map[string]bool{"s01e03": false})

	yes := true
	if _, err := s.Update(ctx, "series-1", catalog.Patch{Monitored: &yes}); err != nil {
		t.Fatal(err)
	}
	refresh(4)
	checkMonitored(t, monitoredEpisodes(t, s, "series-1"), map[string]bool{"s01e03": true, "s01e04": true})

	no := false
	if _, err := s.Update(ctx, "series-1", catalog.Patch{Monitored: &no}); err != nil {
		t.Fatal(err)
	}
	refresh(5)
	checkMonitored(t, monitoredEpisodes(t, s, "series-1"), map[string]bool{"s01e05": false})
}

func TestTheMonitorNewMigrationKeepsExistingSeriesMonitoringNewEpisodes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s, err := catalog.Open(dir, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "Watched", Episodes: houseEpisodes()}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 2, Title: "Paused", Monitor: catalog.MonitorNone, Episodes: houseEpisodes()}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, catalog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(dropAtMs + `DROP TABLE run_mode; PRAGMA user_version = 8`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = catalog.Open(dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("the migration must run on a library from before it: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for id, want := range map[string]bool{"series-1": true, "series-2": false} {
		err := s.ApplyRefresh(ctx, id, catalog.Refresh{Episodes: append(houseEpisodes(),
			catalog.NewEpisode{Season: 5, Number: 1, Name: "New", AirDate: "2027-01-01"})})
		if err != nil {
			t.Fatal(err)
		}
		checkMonitored(t, monitoredEpisodes(t, s, id), map[string]bool{"s05e01": want})
	}
}
