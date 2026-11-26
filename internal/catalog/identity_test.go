package catalog_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/davepintodev/marquee/internal/catalog"
)

func TestTheTitleNamesMigrationKeepsAMixedLibraryAndItsFilesFindable(t *testing.T) {
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
	if _, err := s.ApplyScan(ctx, movie, []catalog.FileFound{{File: catalog.File{Path: "Film (2000)/Film.mkv", Size: 1}}}, nil); err != nil {
		t.Fatal(err)
	}
	pair := "House/Season 02/House - S02E01-E02.mkv"
	if _, err := s.ApplyScan(ctx, "series-2", []catalog.FileFound{
		{Season: 2, Number: 2, File: catalog.File{Path: pair, Size: 1}},
		{Season: 2, Number: 1, File: catalog.File{Path: pair, Size: 1}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	finish(t, s, "dl-1", movie, 0, 0, "")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, catalog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(dropTitleNames + `PRAGMA user_version = 19`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = catalog.Open(dir, nil)
	if err != nil {
		t.Fatalf("the migration must run on a library with titles, episodes and downloads: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ep, err := s.IdentifyFile(ctx, "series", pair)
	if err != nil || ep.TitleID != "series-2" || ep.Season != 2 || ep.Number != 1 || ep.EpisodeTitle != "One" || ep.Names != nil {
		t.Errorf("the shared file: %+v %v", ep, err)
	}
	if _, err := s.IdentifyFile(ctx, "movie", pair); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("an episode's file in the movie library: %v", err)
	}
	if err := s.SetTitleNames(ctx, "series-2", catalog.TitleNames{OriginalLanguage: "en"}); err != nil {
		t.Fatal(err)
	}
	ep, err = s.IdentifyFile(ctx, "series", pair)
	if err != nil || ep.Names == nil || ep.Names.OriginalLanguage != "en" || ep.Names.AlternativeTitles == nil || len(ep.Names.AlternativeTitles) != 0 {
		t.Errorf("names kept with no alternative titles: %+v %v", ep.Names, err)
	}
	if err := s.SetTitleNames(ctx, "series-9", catalog.TitleNames{}); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("a title not in the library: %v", err)
	}
}
