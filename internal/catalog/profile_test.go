package catalog_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

func TestATitleKeepsTheProfileItWasAddedWithAndAPatchChangesOrClearsIt(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	added, _, err := s.Add(ctx, catalog.NewTitle{Kind: "movie", TMDBID: 1, Title: "Film", Profile: "tv-safe"})
	if err != nil || added.Profile != "tv-safe" {
		t.Fatalf("add: %+v %v", added, err)
	}
	plain, _, err := s.Add(ctx, catalog.NewTitle{Kind: "movie", TMDBID: 2, Title: "Other"})
	if err != nil || plain.Profile != "" {
		t.Fatalf("no profile means the kind's default: %+v %v", plain, err)
	}
	other := "anything"
	got, err := s.Update(ctx, "movie-1", catalog.Patch{Profile: &other})
	if err != nil || got.Profile != "anything" {
		t.Fatalf("patch: %+v %v", got, err)
	}
	clear := ""
	if got, err = s.Update(ctx, "movie-1", catalog.Patch{Profile: &clear}); err != nil || got.Profile != "" {
		t.Fatalf("clear: %+v %v", got, err)
	}
	if got, err = s.Update(ctx, "movie-1", catalog.Patch{Profile: &other}); err != nil || got.Profile != "anything" {
		t.Fatalf("patch: %+v %v", got, err)
	}
	monitored := false
	if got, err = s.Update(ctx, "movie-1", catalog.Patch{Monitored: &monitored}); err != nil || got.Profile != "anything" {
		t.Errorf("a patch that leaves the profile out keeps it: %+v %v", got, err)
	}
}

func TestTargetsCarryTheirTitlesProfile(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	for _, n := range []catalog.NewTitle{
		{Kind: "movie", TMDBID: 1, Title: "Film", Profile: "anything"},
		{Kind: "series", TMDBID: 2, Title: "Show", Profile: "tv-safe", Episodes: houseEpisodes()},
		{Kind: "movie", TMDBID: 3, Title: "Plain"},
	} {
		if _, _, err := s.Add(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	targets, err := s.Targets(ctx, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	profiles := map[string]string{}
	for _, tg := range targets {
		profiles[tg.TitleID] = tg.Profile
	}
	for id, want := range map[string]string{"movie-1": "anything", "series-2": "tv-safe", "movie-3": ""} {
		if got, ok := profiles[id]; !ok || got != want {
			t.Errorf("%s: profile %q (found %v), want %q", id, got, ok, want)
		}
	}
}

func TestTheProfileMigrationKeepsEveryExistingTitleAndEpisodeAndDropsThePreferredQuality(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s, err := catalog.Open(dir, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []catalog.NewTitle{
		{Kind: "movie", TMDBID: 1, Title: "Film"},
		{Kind: "series", TMDBID: 2, Title: "Show", Episodes: houseEpisodes()},
		{Kind: "series", TMDBID: 3, Title: "Paused", Monitor: catalog.MonitorNone, Episodes: houseEpisodes()},
	} {
		if _, _, err := s.Add(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Put the database back as the version before this migration left it.
	db, err := sql.Open("sqlite", filepath.Join(dir, catalog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(dropProfile + `UPDATE titles SET preferred_quality = '2160p' WHERE id = 'movie-1'; DROP TABLE run_mode; DROP TABLE pause_all; PRAGMA user_version = 12`); err != nil {
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
	all, err := s.List(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("titles: %d %v", len(all), err)
	}
	for _, title := range all {
		if title.Profile != "" {
			t.Errorf("%s: an old preference must not become a profile: %q", title.ID, title.Profile)
		}
	}
	show, err := s.Get(ctx, "series-2")
	if err != nil || len(show.Seasons) == 0 || !show.Monitored {
		t.Errorf("series kept with its episodes: %+v %v", show, err)
	}
	paused, err := s.Get(ctx, "series-3")
	if err != nil || paused.Monitored {
		t.Errorf("an unmonitored series stays so: %+v %v", paused, err)
	}
	name := "tv-safe"
	if got, err := s.Update(ctx, "movie-1", catalog.Patch{Profile: &name}); err != nil || got.Profile != name {
		t.Errorf("the migrated column takes a profile: %+v %v", got, err)
	}
}
