package app_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/jellyfinfake"
)

func (s *realSvc) moviePath(rel string) string {
	return filepath.Join(s.env["MARQUEE_MOVIES_PATH"], filepath.FromSlash(rel))
}

func (s *realSvc) showPath(rel string) string {
	return filepath.Join(s.env["MARQUEE_SERIES_PATH"], filepath.FromSlash(rel))
}

func present(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// startFiles starts marquee on the adopted Dune and House library with a
// Jellyfin around it.
func startFiles(t *testing.T, dryRun bool) (*realSvc, *jellyfinfake.Server) {
	t.Helper()
	return startFilesWith(t, dryRun, nil)
}

// startFilesWith is startFiles with prep run on the environment and opts on the service options.
func startFilesWith(t *testing.T, dryRun bool, prep func(env map[string]string), opts ...svcOpt) (*realSvc, *jellyfinfake.Server) {
	t.Helper()
	jf := jellyfinfake.New()
	t.Cleanup(jf.Close)
	lib := library(t,
		[]string{duneFile, "Dune (2021)/Dune (2021) [Bluray-1080p].en.srt", "Dune (2021)/Dune (2021) [Bluray-1080p].es.forced.srt"},
		[]string{houseS3E12, houseS4E1, houseS4E2, "House/Season 04/Making of House.mkv"})
	s, err := startAdoptWith(t, adoptTMDB(t), func(env map[string]string) {
		lib(env)
		env["MARQUEE_JELLYFIN_URL"] = jf.URL
		env["MARQUEE_JELLYFIN_KEY"] = jellyfinfake.Key
		if dryRun {
			env["MARQUEE_DRY_RUN"] = "true"
		}
		if prep != nil {
			prep(env)
		}
	}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s, jf
}

func TestRemovingATitleWithDeleteFilesDeletesItsFilesAndSidecarsAndRefreshesJellyfin(t *testing.T) {
	s, jf := startFiles(t, false)
	lay(t, s.env["MARQUEE_MOVIES_PATH"],
		"Dune (2021)/Dune (2021) [Bluray-1080p] Extended.mkv", "Dune (2021)/Dune (2021) [Bluray-1080p] Extended.en.srt", "Dune (2021)/poster.jpg")

	s.mustCall("DELETE", "/api/titles/movie-438631?deleteFiles=true", "", 204)

	for _, gone := range []string{duneFile, "Dune (2021)/Dune (2021) [Bluray-1080p].en.srt", "Dune (2021)/Dune (2021) [Bluray-1080p].es.forced.srt"} {
		if present(s.moviePath(gone)) {
			t.Errorf("%s should be deleted", gone)
		}
	}
	for _, kept := range []string{"Dune (2021)/Dune (2021) [Bluray-1080p] Extended.mkv", "Dune (2021)/Dune (2021) [Bluray-1080p] Extended.en.srt", "Dune (2021)/poster.jpg"} {
		if !present(s.moviePath(kept)) {
			t.Errorf("%s is another file and must stay", kept)
		}
	}
	for _, kept := range []string{houseS3E12, houseS4E1, houseS4E2} {
		if !present(s.showPath(kept)) {
			t.Errorf("another title's %s was touched", kept)
		}
	}
	s.mustCall("GET", "/api/titles/movie-438631", "", 404)
	waitFor(t, "the Jellyfin refresh", 5*time.Second, func() bool { return jf.Refreshes() == 1 })
}

func TestRemovingAnOnlyTitleAlsoRemovesItsEmptyFolderButNeverTheLibraryRoot(t *testing.T) {
	s, _ := startFiles(t, false)
	s.mustCall("DELETE", "/api/titles/movie-438631?deleteFiles=true", "", 204)
	if present(s.moviePath("Dune (2021)")) {
		t.Error("the empty title folder should be pruned")
	}
	if !present(s.env["MARQUEE_MOVIES_PATH"]) {
		t.Error("the library root must never be removed")
	}
}

func TestRemovingASeriesDeletesASharedFileOnceAndPrunesOnlyEmptyFolders(t *testing.T) {
	s, _ := startFiles(t, false)
	s.mustCall("DELETE", "/api/titles/series-1622?deleteFiles=true", "", 204)
	for _, gone := range []string{houseS3E12, houseS4E1, houseS4E2} {
		if present(s.showPath(gone)) {
			t.Errorf("%s should be deleted", gone)
		}
	}
	if present(s.showPath("House/Season 03")) {
		t.Error("an empty season folder is pruned")
	}
	if !present(s.showPath("House/Season 04/Making of House.mkv")) || !present(s.showPath("House/Season 04")) {
		t.Error("a file marquee does not track keeps its folder")
	}
	if !present(s.moviePath(duneFile)) {
		t.Error("another title was touched")
	}
}

func TestRemovingATitleWithoutDeleteFilesNeverTouchesTheDisk(t *testing.T) {
	for _, path := range []string{"", "?deleteFiles=false"} {
		s, jf := startFiles(t, false)
		before := libraryFingerprint(t, s.env)
		s.mustCall("DELETE", "/api/titles/movie-438631"+path, "", 204)
		s.mustCall("DELETE", "/api/titles/series-1622"+path, "", 204)
		if after := libraryFingerprint(t, s.env); after != before {
			t.Errorf("DELETE %q changed the library", path)
		}
		if n := jf.Refreshes(); n != 0 {
			t.Errorf("DELETE %q refreshed Jellyfin %d times", path, n)
		}
	}
}

func TestInDryRunDeleteFilesIsRefusedAndNothingChanges(t *testing.T) {
	s, jf := startFiles(t, true)
	before := libraryFingerprint(t, s.env)
	got := s.mustCall("DELETE", "/api/titles/movie-438631?deleteFiles=true", "", 409)
	if got["error"] == "" {
		t.Errorf("a refusal says why: %v", got)
	}
	s.mustCall("GET", "/api/titles/movie-438631", "", 200)
	if libraryFingerprint(t, s.env) != before || jf.Calls() != 0 {
		t.Error("dry-run changed the library or called Jellyfin")
	}
	s.mustCall("DELETE", "/api/titles/movie-438631", "", 204)
}

func TestDeleteFilesMustBeTrueOrFalse(t *testing.T) {
	s, _ := startFiles(t, false)
	if got := s.bad("DELETE", "/api/titles/movie-438631?deleteFiles=maybe", "").status; got != 400 {
		t.Errorf("deleteFiles=maybe: %d", got)
	}
	s.mustCall("GET", "/api/titles/movie-438631", "", 200)
	s.mustCall("DELETE", "/api/titles/movie-1?deleteFiles=true", "", 404)
}

func TestAnUnknownTitleOrOneWithNoFilesCanStillBeRemovedWithDeleteFiles(t *testing.T) {
	s, _ := startFiles(t, false)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":949}`, 201)
	s.mustCall("DELETE", "/api/titles/movie-949?deleteFiles=true", "", 204)
	s.mustCall("GET", "/api/titles/movie-949", "", 404)
}

func TestARecordedPathThatLeavesTheLibraryDeletesNothingAndKeepsTheTitle(t *testing.T) {
	s, _ := startFiles(t, false)
	secret := filepath.Join(filepath.Dir(s.env["MARQUEE_MOVIES_PATH"]), "secret.mkv")
	if err := os.WriteFile(secret, []byte("not the library's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db().Exec(`UPDATE episodes SET file_path = '../secret.mkv' WHERE title_id = 'series-1622' AND season = 4 AND number = 2`); err != nil {
		t.Fatal(err)
	}
	before := libraryFingerprint(t, s.env)
	got := s.mustCall("DELETE", "/api/titles/series-1622?deleteFiles=true", "", 409)
	if got["error"] == "" {
		t.Errorf("a refusal says why: %v", got)
	}
	if libraryFingerprint(t, s.env) != before {
		t.Error("one bad path must stop the whole delete before any file goes")
	}
	if b, _ := os.ReadFile(secret); string(b) != "not the library's" {
		t.Error("a file outside the library was touched")
	}
	s.mustCall("GET", "/api/titles/series-1622", "", 200)
}

func TestASeasonFolderLinkedOutsideTheLibraryIsNeverFollowed(t *testing.T) {
	s, _ := startFiles(t, false)
	outside := filepath.Join(filepath.Dir(s.env["MARQUEE_SERIES_PATH"]), "elsewhere")
	if err := os.MkdirAll(filepath.Dir(outside), 0o750); err != nil {
		t.Fatal(err)
	}
	season := s.showPath("House/Season 04")
	if err := os.Rename(season, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, season); err != nil {
		t.Fatal(err)
	}
	before := fingerprint(t, outside)
	s.mustCall("DELETE", "/api/titles/series-1622?deleteFiles=true", "", 409)
	if fingerprint(t, outside) != before {
		t.Error("files behind a link out of the library were touched")
	}
	if !present(s.showPath(houseS3E12)) {
		t.Error("nothing is deleted when one path is refused")
	}
	s.mustCall("GET", "/api/titles/series-1622", "", 200)
}

func TestAFileWhoseLocationIsNotRecordedBlocksDeleteFilesUntilARescan(t *testing.T) {
	s, _ := startFiles(t, false)
	s.forgetFiles("movie-438631")
	before := libraryFingerprint(t, s.env)
	got := s.mustCall("DELETE", "/api/titles/movie-438631?deleteFiles=true", "", 409)
	if got["error"] == "" {
		t.Errorf("a refusal says why: %v", got)
	}
	if libraryFingerprint(t, s.env) != before {
		t.Error("nothing is deleted from a guess")
	}
	s.mustCall("GET", "/api/titles/movie-438631", "", 200)

	s.rescan("/api/titles/movie-438631/rescan")
	s.mustCall("DELETE", "/api/titles/movie-438631?deleteFiles=true", "", 204)
	if present(s.moviePath(duneFile)) {
		t.Error("after the rescan the file is known and goes")
	}
}
