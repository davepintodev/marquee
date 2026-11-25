package catalog_test

import (
	"context"
	"testing"

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
