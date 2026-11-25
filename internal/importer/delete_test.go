package importer_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/davepintodev/marquee/internal/importer"
)

func put(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

func listing(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if fi, serr := os.Stat(p); serr == nil && fi.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func TestDeleteMediaFileRemovesTheVideoAndEverySubtitleSidecarOfItsStem(t *testing.T) {
	root := t.TempDir()
	put(t, root,
		"Movie (2020)/Movie (2020).mkv",
		"Movie (2020)/Movie (2020).srt",
		"Movie (2020)/Movie (2020).es.srt",
		"Movie (2020)/Movie (2020).en.forced.srt",
		"Movie (2020)/Movie (2020).es.hi.ass",
		"Movie (2020)/Movie (2020).SSA",
		"Movie (2020)/Movie (2020).sub",
		"Movie (2020)/Movie (2020).vtt",
		"Movie (2020)/Movie (2020) Extended.mkv",
		"Movie (2020)/Movie (2020) Extended.srt",
		"Movie (2020)/Movie (2020) Extended.en.srt",
		"Movie (2020)/Movie (2020).nfo",
		"Movie (2020)/Movie (2020).jpg",
		"Movie (2020)/Movie (2020)-trailer.mkv",
		"Movie (2020)/poster.jpg",
		"Movie (2020)/Movie (2021).srt",
	)
	if err := importer.DeleteMediaFile(root, "Movie (2020)/Movie (2020).mkv"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Movie (2020)/Movie (2020) Extended.en.srt",
		"Movie (2020)/Movie (2020) Extended.mkv",
		"Movie (2020)/Movie (2020) Extended.srt",
		"Movie (2020)/Movie (2020).jpg",
		"Movie (2020)/Movie (2020).nfo",
		"Movie (2020)/Movie (2020)-trailer.mkv",
		"Movie (2020)/Movie (2021).srt",
		"Movie (2020)/poster.jpg",
	}
	slices.Sort(want)
	if got := listing(t, root); !slices.Equal(got, want) {
		t.Errorf("left on disk:\n%v\nwant\n%v", got, want)
	}
}

func TestDeleteMediaFileOfAnExtendedCutLeavesTheShorterNamesFilesAlone(t *testing.T) {
	root := t.TempDir()
	put(t, root, "M/Movie (2020).mkv", "M/Movie (2020).en.srt", "M/Movie (2020) Extended.mkv", "M/Movie (2020) Extended.en.srt")
	if err := importer.DeleteMediaFile(root, "M/Movie (2020) Extended.mkv"); err != nil {
		t.Fatal(err)
	}
	if got, want := listing(t, root), []string{"M/Movie (2020).en.srt", "M/Movie (2020).mkv"}; !slices.Equal(got, want) {
		t.Errorf("left on disk: %v", got)
	}
}

func TestDeleteMediaFileMatchesTheStemCaseSensitively(t *testing.T) {
	root := t.TempDir()
	put(t, root, "M/Movie.mkv", "M/movie.en.srt", "M/Movie.en.srt")
	if err := importer.DeleteMediaFile(root, "M/Movie.mkv"); err != nil {
		t.Fatal(err)
	}
	if got := listing(t, root); !slices.Equal(got, []string{"M/movie.en.srt"}) {
		t.Errorf("left on disk: %v", got)
	}
}

func TestDeleteMediaFileOfAFileThatIsAlreadyGoneStillRemovesItsSidecars(t *testing.T) {
	root := t.TempDir()
	put(t, root, "M/Movie.en.srt", "M/Other.mkv")
	if err := importer.DeleteMediaFile(root, "M/Movie.mkv"); err != nil {
		t.Fatalf("a missing video is not an error: %v", err)
	}
	if got := listing(t, root); !slices.Equal(got, []string{"M/Other.mkv"}) {
		t.Errorf("left on disk: %v", got)
	}
}

func TestDeleteMediaFileRefusesWhatCouldEscapeTheRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	outside := filepath.Join(base, "outside")
	put(t, root, "Keep/Keep.mkv")
	put(t, outside, "Secret.mkv", "Secret.en.srt", "dir/Inner.mkv")
	if err := os.Symlink(outside, filepath.Join(root, "Escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "dir"), filepath.Join(root, "Keep", "Season 01")); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(outside, "Secret.mkv")

	for _, rel := range []string{
		"",
		".",
		"..",
		"../outside/Secret.mkv",
		"Keep/../../outside/Secret.mkv",
		"Keep/../Keep/Keep.mkv",
		abs,
		"/" + "Keep/Keep.mkv",
		"Escape/Secret.mkv",
		"Keep/Season 01/Inner.mkv",
		"Keep/",
		"Keep",
		"Keep/Keep.mkv\x00.jpg",
	} {
		err := importer.DeleteMediaFile(root, rel)
		if !errors.Is(err, importer.ErrUnsafePath) {
			t.Errorf("DeleteMediaFile(%q) = %v, want ErrUnsafePath", rel, err)
		}
	}
	want := []string{"Keep/Keep.mkv"}
	if got := listing(t, root); !slices.Equal(got, want) {
		t.Errorf("library: %v", got)
	}
	if got := listing(t, outside); !slices.Equal(got, []string{"Secret.en.srt", "Secret.mkv", "dir/Inner.mkv"}) {
		t.Errorf("a file outside the library was touched: %v", got)
	}
}

func TestDeleteMediaFileFollowsASymlinkedFolderThatStaysInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	put(t, root, "Real/Season 01/Show - S01E01.mkv", "Real/Season 01/Show - S01E01.en.srt")
	if err := os.Symlink(filepath.Join(root, "Real"), filepath.Join(root, "Alias")); err != nil {
		t.Fatal(err)
	}
	if err := importer.DeleteMediaFile(root, "Alias/Season 01/Show - S01E01.mkv"); err != nil {
		t.Fatal(err)
	}
	if got := listing(t, root); len(got) != 0 {
		t.Errorf("left on disk: %v", got)
	}
}

func TestDeleteMediaFileWorksThroughASymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	put(t, base, "real/M/Movie.mkv")
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "lib")); err != nil {
		t.Fatal(err)
	}
	if err := importer.DeleteMediaFile(filepath.Join(base, "lib"), "M/Movie.mkv"); err != nil {
		t.Fatal(err)
	}
	if got := listing(t, filepath.Join(base, "real")); len(got) != 0 {
		t.Errorf("left on disk: %v", got)
	}
}

func TestDeleteMediaFileRemovesASymlinkedVideoButNeverItsTarget(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	put(t, base, "outside/Real.mkv")
	put(t, root, "M/Other.mkv")
	if err := os.Symlink(filepath.Join(base, "outside", "Real.mkv"), filepath.Join(root, "M", "Movie.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "outside", "Real.mkv"), filepath.Join(root, "M", "Movie.en.srt")); err != nil {
		t.Fatal(err)
	}
	if err := importer.DeleteMediaFile(root, "M/Movie.mkv"); err != nil {
		t.Fatal(err)
	}
	if got := listing(t, root); !slices.Equal(got, []string{"M/Other.mkv"}) {
		t.Errorf("library: %v", got)
	}
	if got := listing(t, filepath.Join(base, "outside")); !slices.Equal(got, []string{"Real.mkv"}) {
		t.Errorf("the link's target was deleted: %v", got)
	}
}

func TestDeleteMediaFileRefusesAVideoLinkWhoseFolderIsTheRootItself(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	put(t, base, "outside/Real.mkv")
	put(t, root, "Other.mkv")
	if err := os.Symlink(filepath.Join(base, "outside", "Real.mkv"), filepath.Join(root, "Link.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := importer.DeleteMediaFile(root, "Link.mkv"); !errors.Is(err, importer.ErrUnsafePath) {
		t.Errorf("a link directly in the root: %v", err)
	}
}

func TestDeleteMediaFileRefusesADirectoryAndAnythingThatIsNotAVideo(t *testing.T) {
	root := t.TempDir()
	put(t, root, "M/Movie.mkv/inside.txt", "M/notes.txt", "M/poster.jpg", "M/Movie.srt")
	for _, rel := range []string{"M/Movie.mkv", "M/notes.txt", "M/poster.jpg", "M/Movie.srt", "M"} {
		if err := importer.DeleteMediaFile(root, rel); !errors.Is(err, importer.ErrUnsafePath) {
			t.Errorf("DeleteMediaFile(%q) = %v, want ErrUnsafePath", rel, err)
		}
	}
	if got := listing(t, root); len(got) != 4 {
		t.Errorf("library: %v", got)
	}
}

func TestDeleteMediaFileRefusesARootThatIsNotThere(t *testing.T) {
	if err := importer.DeleteMediaFile(filepath.Join(t.TempDir(), "nope"), "M/Movie.mkv"); err == nil {
		t.Error("a missing root must not be treated as an empty library")
	}
	if err := importer.DeleteMediaFile("", "M/Movie.mkv"); err == nil {
		t.Error("an empty root is refused")
	}
}

func TestDeleteSidecarsLeavesTheVideoInPlace(t *testing.T) {
	root := t.TempDir()
	put(t, root, "M/Movie.mkv", "M/Movie.en.srt", "M/Movie.es.ass", "M/Other.srt")
	if err := importer.DeleteSidecars(root, "M/Movie.mkv"); err != nil {
		t.Fatal(err)
	}
	if got := listing(t, root); !slices.Equal(got, []string{"M/Movie.mkv", "M/Other.srt"}) {
		t.Errorf("left on disk: %v", got)
	}
}

func TestPruneEmptyDirsRemovesEmptyFoldersUpToButNeverIncludingTheRoot(t *testing.T) {
	root := t.TempDir()
	put(t, root, "Show/Season 01/a.mkv", "Show/Season 02/b.mkv", "Show/poster.jpg", "Other/Season 01/c.mkv")
	for _, f := range []string{"Show/Season 01/a.mkv", "Other/Season 01/c.mkv"} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(f))); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"Show/Season 01/a.mkv", "Other/Season 01/c.mkv"} {
		if err := importer.PruneEmptyDirs(root, rel); err != nil {
			t.Fatal(err)
		}
	}
	for _, gone := range []string{"Show/Season 01", "Other/Season 01", "Other"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned: %v", gone, err)
		}
	}
	for _, kept := range []string{"Show", "Show/Season 02", "."} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Errorf("%s must stay: %v", kept, err)
		}
	}
}

func TestPruneEmptyDirsNeverLeavesTheRootOrFollowsALinkOut(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	put(t, base, "outside/empty/placeholder")
	if err := os.Remove(filepath.Join(base, "outside", "empty", "placeholder")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Show"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "outside", "empty"), filepath.Join(root, "Show", "Season 01")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Show/Season 01/a.mkv", "../outside/empty/a.mkv", "/outside/empty/a.mkv"} {
		if err := importer.PruneEmptyDirs(root, rel); err == nil || !errors.Is(err, importer.ErrUnsafePath) {
			t.Errorf("PruneEmptyDirs(%q) = %v, want ErrUnsafePath", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "outside", "empty")); err != nil {
		t.Errorf("a folder outside the library was removed: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("the root was removed: %v", err)
	}
}

func TestPruneEmptyDirsStopsAtALinkedFolderInsteadOfRemovingItsTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "OtherEmpty"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "OtherEmpty"), filepath.Join(root, "Movie (2020)")); err != nil {
		t.Fatal(err)
	}
	if err := importer.PruneEmptyDirs(root, "Movie (2020)/Movie (2020).mkv"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "OtherEmpty")); err != nil || !fi.IsDir() {
		t.Errorf("the link's target was removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "Movie (2020)")); err != nil {
		t.Errorf("the link was left dangling: %v", err)
	}
}

func TestPruneEmptyDirsStopsBelowALinkedAncestorInsteadOfRemovingThroughIt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "RealShow", "Season 01"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "RealShow"), filepath.Join(root, "Show")); err != nil {
		t.Fatal(err)
	}
	if err := importer.PruneEmptyDirs(root, "Show/Season 01/a.mkv"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "RealShow", "Season 01")); err != nil || !fi.IsDir() {
		t.Errorf("a folder behind a linked ancestor was removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "Show")); err != nil {
		t.Errorf("the link was left dangling: %v", err)
	}
}
