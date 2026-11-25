package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

func queueUncached(t *testing.T, s *catalog.Store, id string, number int) {
	t.Helper()
	_, err := s.AddDownload(context.Background(), catalog.NewDownload{
		ID: id, TitleID: "series-7", Season: 1, Number: number, Uncached: true,
		Release: catalog.ReleaseID{InfoHash: "h" + id, FileName: id + ".mkv", Size: 1}, ReleaseName: id,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestACachingDownloadShowsItsPercentOnTheEpisodeAndTheTitleBadge(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	addHouse(t, s)
	queueUncached(t, s, "slow", 2)

	d, err := s.GetDownload(ctx, "slow")
	if err != nil || !d.Uncached || d.State != catalog.StateQueued || d.TorBoxTorrentID != 0 {
		t.Fatalf("queued: %+v, %v", d, err)
	}
	if err := s.SetDownloadCaching(ctx, "slow", 42, 77, 3); err != nil {
		t.Fatal(err)
	}
	d, _ = s.GetDownload(ctx, "slow")
	if d.State != catalog.StateCaching || d.Percent() != 42 || d.TorBoxTorrentID != 77 || d.TorBoxFileID != 3 {
		t.Fatalf("caching: %+v (percent %d)", d, d.Percent())
	}
	ep := episodeOf(t, s, 1, 2)
	if ep.State != "caching" || ep.Percent == nil || *ep.Percent != 42 {
		t.Fatalf("episode: %+v", ep)
	}
	title, _ := s.Get(ctx, "series-7")
	if title.Badge.State != "caching" || title.Badge.Label != "Caching 42%" || title.Badge.Percent == nil || *title.Badge.Percent != 42 {
		t.Fatalf("badge: %+v", title.Badge)
	}
	if title.Seasons[0].Badge.Label != "Caching 42%" {
		t.Fatalf("season badge: %+v", title.Seasons[0].Badge)
	}
}

func TestAMovieCachingBadgeAndTheStateMovingOnToDownloading(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	id := addMovie(t, s, 9)
	if _, err := s.AddDownload(ctx, catalog.NewDownload{ID: "m", TitleID: id, Uncached: true, Release: catalog.ReleaseID{InfoHash: "hm", FileName: "m.mkv", Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDownloadCaching(ctx, "m", 7, 1, 0); err != nil {
		t.Fatal(err)
	}
	title, _ := s.Get(ctx, id)
	if title.Badge.Label != "Caching 7%" {
		t.Fatalf("badge: %+v", title.Badge)
	}
	if err := s.SetDownloadState(ctx, "m", catalog.StateDownloading); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDownloadCaching(ctx, "m", 99, 1, 0); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GetDownload(ctx, "m"); d.State != catalog.StateDownloading {
		t.Fatalf("a late caching report moved a transferring download back to %s", d.State)
	}
}

func TestSettingCachingOnAFinishedOrMissingDownloadIsNotFound(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	addHouse(t, s)
	if err := s.SetDownloadCaching(ctx, "nope", 1, 1, 0); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	queueUncached(t, s, "x", 1)
	if err := s.FailDownload(ctx, "x", "stalled", "m", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDownloadCaching(ctx, "x", 1, 1, 0); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("finished: %v", err)
	}
}

func TestUncachedActiveCountsQueuedAndCachingUncachedDownloadsOnly(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	addHouse(t, s)
	queue(t, s, "cached", 1, 1)
	queueUncached(t, s, "q", 2)
	queueUncached(t, s, "c", 3)
	queueUncached(t, s, "t", 4)
	if err := s.SetDownloadCaching(ctx, "c", 5, 8, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDownloadCaching(ctx, "t", 100, 9, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDownloadState(ctx, "t", catalog.StateDownloading); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UncachedActive(ctx); err != nil || n != 2 {
		t.Fatalf("got %d, %v; want 2 (queued and caching)", n, err)
	}
}

func TestATorrentIsInUseWhileAnotherUnfinishedDownloadReferencesIt(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	addHouse(t, s)
	queueUncached(t, s, "a", 1)
	queueUncached(t, s, "b", 2)
	for _, id := range []string{"a", "b"} {
		if err := s.SetDownloadCaching(ctx, id, 50, 31, 0); err != nil {
			t.Fatal(err)
		}
	}
	if used, err := s.TorrentInUse(ctx, 31, "a"); err != nil || !used {
		t.Fatalf("sibling b is active: %v, %v", used, err)
	}
	if err := s.FailDownload(ctx, "b", "stalled", "m", false); err != nil {
		t.Fatal(err)
	}
	if used, err := s.TorrentInUse(ctx, 31, "a"); err != nil || used {
		t.Fatalf("no sibling is active any more: %v, %v", used, err)
	}
}
