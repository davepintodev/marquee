package catalog_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

func houseSeason() []catalog.NewEpisode {
	return []catalog.NewEpisode{
		{Season: 1, Number: 1, Name: "One", AirDate: "2005-01-01"},
		{Season: 1, Number: 2, Name: "Two", AirDate: "2005-01-08"},
		{Season: 1, Number: 3, Name: "Three", AirDate: "2005-01-15"},
		{Season: 1, Number: 4, Name: "Four", AirDate: "2005-01-22"},
	}
}

func addHouse(t *testing.T, s *catalog.Store) {
	t.Helper()
	if _, _, err := s.Add(context.Background(), catalog.NewTitle{Kind: "series", TMDBID: 7, Title: "House", Episodes: houseSeason()}); err != nil {
		t.Fatal(err)
	}
}

func queue(t *testing.T, s *catalog.Store, id string, season, number int, covered ...int) {
	t.Helper()
	_, err := s.AddDownload(context.Background(), catalog.NewDownload{
		ID: id, TitleID: "series-7", Season: season, Number: number, Covered: covered,
		Release: catalog.ReleaseID{InfoHash: "h" + id, FileName: id + ".mkv", Size: 1}, ReleaseName: id,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func episodeOf(t *testing.T, s *catalog.Store, season, number int) catalog.Episode {
	t.Helper()
	title, err := s.Get(context.Background(), "series-7")
	if err != nil {
		t.Fatal(err)
	}
	for _, se := range title.Seasons {
		for _, e := range se.Episodes {
			if se.Number == season && e.Number == number {
				return e
			}
		}
	}
	t.Fatalf("no S%02dE%02d", season, number)
	return catalog.Episode{}
}

func TestFinishingAMultiEpisodeDownloadRecordsTheSharedFileOnEveryCoveredEpisode(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	addHouse(t, s)
	if err := s.RequestSearch(ctx, "series-7", &catalog.EpisodeRef{Season: 1, Number: 2}); err != nil {
		t.Fatal(err)
	}
	queue(t, s, "pair", 1, 1, 2, 1) // out of order, and repeating the target
	f := catalog.File{Path: "House/Season 01/House - S01E01-E02 - One + Two [WEBDL-1080p].mkv", Size: 4242, Fingerprint: "fp-pair"}
	if err := s.FinishDownload(ctx, "pair", f); err != nil {
		t.Fatal(err)
	}

	files, err := s.TrackedFiles(ctx, "series-7")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("tracked files: %+v", files)
	}
	for i, n := range []int{1, 2} {
		got := files[i]
		if got.Season != 1 || got.Number != n || !got.Have || got.File != f {
			t.Errorf("S01E0%d: %+v, want the shared file %+v", n, got, f)
		}
		if e := episodeOf(t, s, 1, n); e.State != "downloaded" || !slices.Equal(e.Shared, []string{"s01e01", "s01e02"}) {
			t.Errorf("S01E0%d: state %q, shared %v", n, e.State, e.Shared)
		}
	}
	for _, n := range []int{3, 4} {
		if e := episodeOf(t, s, 1, n); e.State == "downloaded" || len(e.Shared) != 0 {
			t.Errorf("S01E0%d does not belong to the file: %+v", n, e)
		}
	}
	targets, err := s.Targets(ctx, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		if tg.Number <= 2 {
			t.Errorf("a covered episode is still a target (its Search now request must be dropped): %+v", tg)
		}
	}
	d, err := s.GetDownload(ctx, "pair")
	if err != nil || !slices.Equal(d.Covered, []int{1, 2}) {
		t.Errorf("the download keeps what it covered: %+v, %v", d.Covered, err)
	}
}

func TestADownloadOfOneEpisodeCoversNothingElse(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	addHouse(t, s)
	queue(t, s, "solo", 1, 1)
	if err := s.FinishDownload(ctx, "solo", catalog.File{Path: "House/Season 01/one.mkv", Size: 1, Fingerprint: "a"}); err != nil {
		t.Fatal(err)
	}
	if e := episodeOf(t, s, 1, 1); len(e.Shared) != 0 {
		t.Errorf("a file of one episode is not shared: %v", e.Shared)
	}
	if e := episodeOf(t, s, 1, 2); e.State == "downloaded" {
		t.Errorf("S01E02 has no file: %+v", e)
	}
	d, err := s.GetDownload(ctx, "solo")
	if err != nil || len(d.Covered) != 0 {
		t.Errorf("covered %v, %v", d.Covered, err)
	}
}

func TestACoveredEpisodeIsNotSearchedWhileTheFileThatCoversItIsComing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s := openAt(t, now)
	addHouse(t, s)
	queue(t, s, "pair", 1, 1, 1, 2)

	wanted := func() []int {
		ts, err := s.Targets(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		var out []int
		for _, tg := range ts {
			out = append(out, tg.Number)
		}
		return out
	}
	if got := wanted(); !slices.Equal(got, []int{3, 4}) {
		t.Errorf("targets while S01E01-E02 downloads: %v, want 3 and 4", got)
	}
	for _, n := range []int{1, 2} {
		if e := episodeOf(t, s, 1, n); e.State != "downloading" {
			t.Errorf("S01E0%d is %q while its file downloads", n, e.State)
		}
	}
	if got, err := s.ActiveDownloadFor(ctx, "series-7", 1, 2); err != nil || got.ID != "pair" {
		t.Errorf("the download covering S01E02 is the active one: %+v, %v", got.ID, err)
	}
	_, err := s.AddDownload(ctx, catalog.NewDownload{ID: "second", TitleID: "series-7", Season: 1, Number: 2,
		Release: catalog.ReleaseID{InfoHash: "x", FileName: "x.mkv", Size: 1}})
	if !errors.Is(err, catalog.ErrActiveDownload) {
		t.Errorf("a second download of a covered episode: %v, want ErrActiveDownload", err)
	}
	_, err = s.AddDownload(ctx, catalog.NewDownload{ID: "wide", TitleID: "series-7", Season: 1, Number: 3, Covered: []int{3, 2},
		Release: catalog.ReleaseID{InfoHash: "y", FileName: "y.mkv", Size: 1}})
	if !errors.Is(err, catalog.ErrActiveDownload) {
		t.Errorf("a download that would cover S01E02 again: %v, want ErrActiveDownload", err)
	}
	queue(t, s, "third", 1, 3)
	_, err = s.AddDownload(ctx, catalog.NewDownload{ID: "over-single", TitleID: "series-7", Season: 1, Number: 4, Covered: []int{3, 4},
		Release: catalog.ReleaseID{InfoHash: "z", FileName: "z.mkv", Size: 1}})
	if !errors.Is(err, catalog.ErrActiveDownload) {
		t.Errorf("a file that would cover an episode another download is fetching: %v, want ErrActiveDownload", err)
	}

	if err := s.FailDownload(ctx, "pair", "stalled", "Stalled", false); err != nil {
		t.Fatal(err)
	}
	if got := wanted(); !slices.Equal(got, []int{1, 2, 4}) {
		t.Errorf("targets after the file failed: %v, want 1, 2 and 4", got)
	}
}

func TestTheSharedFingerprintOfACoveredEpisodeIsNoDuplicate(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	addHouse(t, s)
	queue(t, s, "pair", 1, 1, 1, 2)
	if err := s.FinishDownload(ctx, "pair", catalog.File{Path: "p.mkv", Size: 1, Fingerprint: "fp"}); err != nil {
		t.Fatal(err)
	}
	if sn, nr, found, err := s.DuplicateEpisode(ctx, "series-7", "fp", 1, []int{3}); err != nil || !found || sn != 1 || nr != 1 {
		t.Errorf("another episode with the same bytes is a duplicate: %d %d %v %v", sn, nr, found, err)
	}
	if _, _, found, err := s.DuplicateEpisode(ctx, "series-7", "fp", 1, []int{1, 2}); err != nil || found {
		t.Errorf("a file that covers both episodes is not a duplicate of itself: %v %v", found, err)
	}
	if _, _, found, err := s.DuplicateEpisode(ctx, "series-7", "fp", 1, []int{2}); err != nil || !found {
		t.Errorf("a single episode never excuses the other holder of the bytes: %v %v", found, err)
	}
}

func TestOwnedEpisodesAreTheSeasonsEpisodesWithAFile(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	if _, _, err := s.Adopt(ctx, catalog.NewTitle{Kind: "series", TMDBID: 7, Title: "House", Episodes: append(houseSeason(),
		catalog.NewEpisode{Season: 2, Number: 1, Name: "Next", AirDate: "2006-01-01"})},
		catalog.Found{Episodes: []catalog.EpisodeRef{{Season: 1, Number: 2}, {Season: 1, Number: 4}, {Season: 2, Number: 1}}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.OwnedEpisodes(ctx, "series-7", 1)
	if err != nil || !slices.Equal(got, []int{2, 4}) {
		t.Errorf("owned in season 1: %v, %v", got, err)
	}
	if got, err := s.OwnedEpisodes(ctx, "series-7", 9); err != nil || len(got) != 0 {
		t.Errorf("owned in an unknown season: %v, %v", got, err)
	}
	if _, err := s.OwnedEpisodes(ctx, "series-404", 1); err != nil {
		t.Errorf("an unknown title owns nothing: %v", err)
	}
}

func TestEachEpisodeHasItsOwnDailySearchBudget(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	addHouse(t, s)
	const day = "2026-11-18"
	for range 3 {
		if err := s.CountSearch(ctx, "series-7", 1, 1, day); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CountSearch(ctx, "series-7", 1, 2, day); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ season, number, want int }{{1, 1, 3}, {1, 2, 1}, {1, 3, 0}, {0, 0, 0}} {
		if got, err := s.SearchesToday(ctx, "series-7", c.season, c.number, day); err != nil || got != c.want {
			t.Errorf("S%02dE%02d used %d, want %d (%v)", c.season, c.number, got, c.want, err)
		}
	}
	if got, _ := s.SearchesToday(ctx, "series-7", 1, 1, "2026-11-19"); got != 0 {
		t.Errorf("a new day starts at 0, got %d", got)
	}
	if err := s.CountSearch(ctx, "series-7", 1, 1, "2026-11-19"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SearchesToday(ctx, "series-7", 1, 1, "2026-11-19"); got != 1 {
		t.Errorf("a new day counts from 1, got %d", got)
	}
	if got, _ := s.SearchesToday(ctx, "series-7", 1, 2, day); got != 1 {
		t.Errorf("another episode's day is untouched, got %d", got)
	}
	if err := s.CountSearch(ctx, "series-404", 1, 1, day); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("counting a search of a removed title: %v", err)
	}
}

// oldBudget turns a current database back into one at schema 14: the
// search budget kept per title.
const oldBudget = `
CREATE TABLE search_budget_old (
  title_id TEXT PRIMARY KEY REFERENCES titles(id) ON DELETE CASCADE,
  day      TEXT NOT NULL,
  used     INTEGER NOT NULL
) WITHOUT ROWID;
INSERT INTO search_budget_old (title_id, day, used) SELECT title_id, day, SUM(used) FROM search_budget GROUP BY title_id;
DROP TABLE search_budget;
ALTER TABLE search_budget_old RENAME TO search_budget;
PRAGMA user_version = 14;
`

func TestTheSearchBudgetMigrationKeepsEveryTitlesCountAndGivesEpisodesTheirOwn(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s, err := catalog.Open(dir, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	movie := addMovie(t, s, 1)
	addHouse(t, s)
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 8, Title: "Idle", Episodes: houseSeason()}); err != nil {
		t.Fatal(err)
	}
	const day = "2026-11-18"
	for range 2 {
		if err := s.CountSearch(ctx, movie, 0, 0, day); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 {
		if err := s.CountSearch(ctx, "series-7", 1, 1, day); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db := rawDB(t, dir)
	if _, err := db.Exec(oldBudget); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s, err = catalog.Open(dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("the migration must run on a database from before it: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if got, err := s.SearchesToday(ctx, movie, 0, 0, day); err != nil || got != 2 {
		t.Errorf("a movie keeps its count: %d, %v", got, err)
	}
	if got, err := s.SearchesToday(ctx, "series-7", 0, 0, day); err != nil || got != 4 {
		t.Errorf("a series' old title-wide count lands on season 0, number 0: %d, %v", got, err)
	}
	for _, n := range []int{1, 2} {
		if got, err := s.SearchesToday(ctx, "series-7", 1, n, day); err != nil || got != 0 {
			t.Errorf("S01E0%d starts with a full budget: %d, %v", n, got, err)
		}
	}
	for range 4 {
		if err := s.CountSearch(ctx, "series-7", 1, 2, day); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.SearchesToday(ctx, "series-7", 1, 2, day); got != 4 {
		t.Errorf("S01E02 used %d", got)
	}

	db = rawDB(t, dir)
	var pk string
	if err := db.QueryRow(`SELECT group_concat(name) FROM (SELECT name FROM pragma_table_info('search_budget') WHERE pk > 0 ORDER BY pk)`).Scan(&pk); err != nil || pk != "title_id,season,number" {
		t.Errorf("primary key %q, %v", pk, err)
	}
	if _, err := db.Exec(`DELETE FROM titles WHERE id = 'series-7'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_budget WHERE title_id = 'series-7'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("removing a title still cascades to its budget: %d, %v", n, err)
	}
}
