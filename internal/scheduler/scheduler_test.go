package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/pipeline"
	"github.com/davepintodev/marquee/internal/scheduler"
)

type oneRelease struct{}

func (oneRelease) Search(context.Context, string, string) ([]aiostreams.Stream, error) {
	return []aiostreams.Stream{{
		URL: "https://cdn.example/one.mkv", Filename: "House.S01E01.1080p.WEB-DL.mkv", Size: 1 << 30,
		Type: "debrid", Cached: true, InfoHash: "0000000000000000000000000000000000000001",
		ParsedFile: &aiostreams.ParsedFile{Resolution: "1080p", Quality: "WEB-DL", Seasons: []int{1}, Episodes: []int{1}},
	}}, nil
}

type vanishedDownload struct{}

func (vanishedDownload) Enqueue(context.Context, pipeline.Job) (catalog.Download, error) {
	return catalog.Download{}, catalog.ErrActiveDownload
}

func TestADownloadThatEndsBeforeItCanBeNamedStillFollowsUpTheEpisode(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cat, err := catalog.Open(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	if _, _, err := cat.Add(ctx, catalog.NewTitle{
		Kind: "series", TMDBID: 7, Title: "House", TVDBID: 403000,
		Episodes: []catalog.NewEpisode{{Season: 1, Number: 1, Name: "One", AirDate: "2005-01-01"}},
	}); err != nil {
		t.Fatal(err)
	}
	s := scheduler.New(scheduler.Options{
		Catalog: cat, Source: oneRelease{}, Transfers: vanishedDownload{}, Tuning: scheduler.Tuning{Tick: -1},
	})

	s.Step(ctx)

	targets, err := cat.Targets(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || !targets[0].FollowUp {
		t.Errorf("the episode's own download ended in the race, so the next search follows up at once: %+v", targets)
	}
}

type goneUnderTheSearch struct{}

func (goneUnderTheSearch) Enqueue(context.Context, pipeline.Job) (catalog.Download, error) {
	return catalog.Download{}, pipeline.ErrBadJob
}

func TestSearchNowEndsWhenTheTitleChangedUnderItsSearch(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cat, err := catalog.Open(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	if _, _, err := cat.Add(ctx, catalog.NewTitle{
		Kind: "series", TMDBID: 7, Title: "House", TVDBID: 403000,
		Episodes: []catalog.NewEpisode{{Season: 1, Number: 1, Name: "One", AirDate: "2005-01-01"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := cat.RequestSearch(ctx, "series-7", &catalog.EpisodeRef{Season: 1, Number: 1}); err != nil {
		t.Fatal(err)
	}
	s := scheduler.New(scheduler.Options{
		Catalog: cat, Source: oneRelease{}, Transfers: goneUnderTheSearch{}, Tuning: scheduler.Tuning{Tick: -1},
	})

	s.Step(ctx)

	got, err := cat.Get(ctx, "series-7")
	if err != nil {
		t.Fatal(err)
	}
	if got.Badge.State == "searching" {
		t.Errorf("the asked-for search ran, so the series no longer says Searching: %+v", got.Badge)
	}
}
