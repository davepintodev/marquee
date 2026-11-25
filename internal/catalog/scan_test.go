package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

func TestReplacingProblemsUnderAFolderLeavesFoldersWhoseNameStartsTheSame(t *testing.T) {
	ctx := context.Background()
	s, err := catalog.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	p := func(path string) catalog.LibraryProblem {
		return catalog.LibraryProblem{Path: path, Reason: catalog.ProblemUnparseable, Message: "m"}
	}
	if err := s.ReplaceLibraryProblems(ctx, []catalog.LibraryProblem{
		p("Series/House"), p("Series/House/Season 01/a.mkv"), p("Series/House of Cards/b.mkv"), p("Series/House_"), p("Movies/House/c.mkv"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceLibraryProblems(ctx, []catalog.LibraryProblem{p("Series/House/Season 02/d.mkv")}, "Series/House"); err != nil {
		t.Fatal(err)
	}
	got, err := s.LibraryProblems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, g := range got {
		paths = append(paths, g.Path)
	}
	want := []string{"Movies/House/c.mkv", "Series/House of Cards/b.mkv", "Series/House/Season 02/d.mkv", "Series/House_"}
	if len(paths) != len(want) {
		t.Fatalf("problems %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("problems %v, want %v", paths, want)
		}
	}
}

func TestAScanDropsASearchNowRequestSoADeletedFileIsNotDownloadedAgain(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	file := catalog.File{Path: "Show/Season 01/a.mkv", Size: 1, Fingerprint: "f"}

	tests := []struct {
		name           string
		id             string
		n              catalog.NewTitle
		ep             *catalog.EpisodeRef
		season, number int
	}{
		{"movie", "movie-1", catalog.NewTitle{Kind: "movie", TMDBID: 1, Title: "Dune"}, nil, 0, 0},
		{"episode", "series-2", catalog.NewTitle{Kind: "series", TMDBID: 2, Title: "House", Episodes: []catalog.NewEpisode{
			{Season: 1, Number: 1, Name: "One", AirDate: "2005-01-01"},
		}}, &catalog.EpisodeRef{Season: 1, Number: 1}, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := openAt(t, now)
			if _, _, err := s.Adopt(ctx, tc.n, catalog.Found{}); err != nil {
				t.Fatal(err)
			}
			if err := s.RequestSearch(ctx, tc.id, tc.ep); err != nil {
				t.Fatal(err)
			}
			if got := forcedTargets(t, s, now); len(got) != 1 {
				t.Fatalf("forced %v, want the requested search", got)
			}
			found := []catalog.FileFound{{Season: tc.season, Number: tc.number, File: file}}
			if _, err := s.ApplyScan(ctx, tc.id, found, nil); err != nil {
				t.Fatal(err)
			}
			gone := []catalog.FileGone{{Season: tc.season, Number: tc.number, Path: file.Path}}
			if n, err := s.ApplyScan(ctx, tc.id, nil, gone); err != nil || n != 1 {
				t.Fatalf("removed %d, %v", n, err)
			}
			targets, err := s.Targets(ctx, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(targets) != 0 {
				t.Errorf("targets %+v: a file the owner deleted must not be searched for again", targets)
			}
		})
	}
}

func TestAScanLeavesAFileThatChangedAfterItWasReadAlone(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s := openAt(t, now)
	if _, _, err := s.Adopt(ctx, catalog.NewTitle{Kind: "movie", TMDBID: 1, Title: "Dune"}, catalog.Found{}); err != nil {
		t.Fatal(err)
	}
	found := []catalog.FileFound{{File: catalog.File{Path: "Dune/new.mkv", Size: 1, Fingerprint: "f"}}}
	if _, err := s.ApplyScan(ctx, "movie-1", found, nil); err != nil {
		t.Fatal(err)
	}
	stale := []catalog.FileGone{{Path: "Dune/old.mkv"}}
	n, err := s.ApplyScan(ctx, "movie-1", nil, stale)
	if err != nil || n != 0 {
		t.Fatalf("removed %d, %v", n, err)
	}
	title, err := s.Get(ctx, "movie-1")
	if err != nil {
		t.Fatal(err)
	}
	if !title.Monitored {
		t.Errorf("a file that changed after the scan read it must not unmonitor the title: %+v", title)
	}
	page, err := s.History(ctx, "movie-1", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.Activity != nil && it.Activity.Kind == catalog.ActivityRemoved {
			t.Errorf("unexpected removed note: %+v", it.Activity)
		}
	}
}
