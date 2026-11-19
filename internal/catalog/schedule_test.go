package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

func forcedTargets(t *testing.T, s *catalog.Store, now time.Time) []string {
	t.Helper()
	targets, err := s.Targets(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tg := range targets {
		if tg.Forced {
			out = append(out, tg.TitleID+" "+catalog.EpisodeID(tg.Season, tg.Number))
		}
	}
	return out
}

func TestSearchNowForcesNothingWhenASeriesHasMonitoredEpisodesAndTheMissingAiredOnesAreOff(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	t.Run("the monitored episode has its file", func(t *testing.T) {
		s := openAt(t, now)
		show := catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "House", Episodes: []catalog.NewEpisode{
			{Season: 1, Number: 1, Name: "One", AirDate: "2005-01-01"},
			{Season: 1, Number: 2, Name: "Two", AirDate: "2005-01-08"},
		}}
		if _, _, err := s.Adopt(ctx, show, catalog.Found{Episodes: []catalog.EpisodeRef{{Season: 1, Number: 1}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetEpisodeMonitored(ctx, "series-1", 1, 2, false); err != nil {
			t.Fatal(err)
		}
		checkMonitored(t, monitoredEpisodes(t, s, "series-1"), map[string]bool{"s01e01": true, "s01e02": false})
		if err := s.RequestSearch(ctx, "series-1", nil); err != nil {
			t.Fatal(err)
		}
		if got := forcedTargets(t, s, now); len(got) != 0 {
			t.Errorf("forced %v, want nothing: the series has a monitored episode", got)
		}
	})

	t.Run("the only monitored episode has not aired", func(t *testing.T) {
		s := openAt(t, now)
		show := catalog.NewTitle{Kind: "series", TMDBID: 1, Title: "House", Episodes: []catalog.NewEpisode{
			{Season: 1, Number: 1, Name: "One", AirDate: "2005-01-01"},
			{Season: 1, Number: 2, Name: "Future", AirDate: "2027-01-01"},
		}}
		if _, _, err := s.Adopt(ctx, show, catalog.Found{}); err != nil {
			t.Fatal(err)
		}
		checkMonitored(t, monitoredEpisodes(t, s, "series-1"), map[string]bool{"s01e01": false, "s01e02": true})
		if err := s.RequestSearch(ctx, "series-1", nil); err != nil {
			t.Fatal(err)
		}
		if got := forcedTargets(t, s, now); len(got) != 0 {
			t.Errorf("forced %v, want nothing: the series has a monitored episode", got)
		}
	})
}
