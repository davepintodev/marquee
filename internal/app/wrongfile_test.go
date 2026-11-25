package app_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/debridfake"
)

const (
	duneNew      = "Dune (2021)/Dune (2021) [WEBDL-1080p].mkv"
	duneNewSub   = "Dune (2021)/Dune (2021) [WEBDL-1080p].en.srt"
	duneNewOther = "Dune (2021)/Dune (2021) [WEBDL-1080p] Extended.mkv"
)

// downloadedDune has the download manager import Dune, so the catalog
// knows the download that made the file.
func downloadedDune(t *testing.T) (*rig, []byte) {
	t.Helper()
	r, _ := setup(t)
	data := debridfake.MKV(100 << 10)
	r.enqueue(movieJob(r.cdn.Add("dune.mkv", debridfake.File{Data: data}), "dune.mkv"))
	r.waitHistoryKind("imported", 1)
	lay(t, r.env["MARQUEE_MOVIES_PATH"], duneNewSub, duneNewOther)
	return r, data
}

func (s *realSvc) entries(kind string) []map[string]any {
	s.t.Helper()
	var out []map[string]any
	for _, e := range s.history() {
		if m := e.(map[string]any); m["kind"] == kind {
			out = append(out, m)
		}
	}
	return out
}

func (r *rig) wasBlocked(name string) bool {
	r.t.Helper()
	store, err := catalog.Open(r.env["MARQUEE_DATA_DIR"], nil)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	return isBlocked(r.t, store, catalog.ReleaseID{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FileName: name})
}

func TestWrongFileDeletesTheMoviesFileBlocklistsItsReleaseAndSearchesAtOnce(t *testing.T) {
	r, _ := downloadedDune(t)
	waitFor(t, "the import's Jellyfin refresh", 5*time.Second, func() bool { return r.jf.Refreshes() == 1 })

	title := r.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 200)
	if title["id"] != "movie-438631" || title["monitored"] != true {
		t.Errorf("the reply is the movie, monitored: %v", title)
	}
	if got := title["badge"].(map[string]any)["state"]; got == "downloaded" {
		t.Errorf("the movie still shows its file: %v", title["badge"])
	}
	if present(r.moviePath(duneNew)) || present(r.moviePath(duneNewSub)) {
		t.Error("the video and its sidecar should be deleted")
	}
	if !present(r.moviePath(duneNewOther)) {
		t.Error("another video sharing a name prefix must stay")
	}
	if row := r.row("movie-438631", 0, 0); row.have || row.path != "" || !row.monitored {
		t.Errorf("the catalog row: %+v", row)
	}
	if !r.wasBlocked("dune.mkv") {
		t.Error("the release that made the file is blocklisted")
	}
	notes := r.entries("wrong_file")
	if len(notes) != 1 || !strings.Contains(notes[0]["message"].(string), "Wrong file") {
		t.Errorf("history: %v", notes)
	}
	waitFor(t, "Jellyfin to be refreshed again", 5*time.Second, func() bool { return r.jf.Refreshes() == 2 })
	var forced int
	if err := r.db().QueryRow(`SELECT COUNT(*) FROM schedule WHERE title_id = 'movie-438631' AND forced = 1`).Scan(&forced); err != nil || forced != 1 {
		t.Errorf("a search at once is asked for, whatever the backoff: %d, %v", forced, err)
	}
}

func TestWrongFileWithoutAReSearchLeavesTheChoiceToTheOwner(t *testing.T) {
	r, _ := downloadedDune(t)
	r.mustCall("POST", "/api/titles/movie-438631/wrong-file", `{"reSearch":false}`, 200)
	var forced int
	if err := r.db().QueryRow(`SELECT COUNT(*) FROM schedule WHERE forced = 1`).Scan(&forced); err != nil || forced != 0 {
		t.Errorf("forced searches: %d, %v", forced, err)
	}
	if row := r.row("movie-438631", 0, 0); row.have || !row.monitored {
		t.Errorf("the movie is missing and monitored: %+v", row)
	}
	if !r.wasBlocked("dune.mkv") {
		t.Error("the release is blocklisted either way")
	}
}

func TestWrongFileDeletesOnlyTheVideoAndItsOwnSidecars(t *testing.T) {
	s, _ := startFiles(t, false)
	lay(t, s.env["MARQUEE_MOVIES_PATH"],
		"Dune (2021)/Dune (2021) [Bluray-1080p] Extended.mkv", "Dune (2021)/Dune (2021) [Bluray-1080p] Extended.en.srt",
		"Dune (2021)/Dune (2021) [Bluray-1080p].nfo", "Dune (2021)/poster.jpg")
	s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 200)
	for _, gone := range []string{duneFile, "Dune (2021)/Dune (2021) [Bluray-1080p].en.srt", "Dune (2021)/Dune (2021) [Bluray-1080p].es.forced.srt"} {
		if present(s.moviePath(gone)) {
			t.Errorf("%s should be deleted", gone)
		}
	}
	for _, kept := range []string{"Dune (2021)/Dune (2021) [Bluray-1080p] Extended.mkv", "Dune (2021)/Dune (2021) [Bluray-1080p] Extended.en.srt",
		"Dune (2021)/Dune (2021) [Bluray-1080p].nfo", "Dune (2021)/poster.jpg"} {
		if !present(s.moviePath(kept)) {
			t.Errorf("%s must stay", kept)
		}
	}
	if notes := s.mustCall("GET", "/api/titles/movie-438631", "", 200)["history"].([]any); len(notes) == 0 {
		t.Error("the history says what happened")
	}
}

func TestWrongFileOnAnAdoptedFileBlocklistsNothingAndResetsEveryEpisodeItCovers(t *testing.T) {
	s, jf := startFiles(t, false)
	title := s.mustCall("POST", "/api/titles/series-1622/episodes/s03e02/wrong-file", `{"reSearch":false}`, 200)
	if present(s.showPath(houseS3E12)) {
		t.Error("the two-episode file should be deleted")
	}
	for _, kept := range []string{houseS4E1, houseS4E2, "House/Season 04/Making of House.mkv"} {
		if !present(s.showPath(kept)) {
			t.Errorf("%s must stay", kept)
		}
	}
	for _, n := range []int{1, 2} {
		if row := s.row("series-1622", 3, n); row.have || !row.monitored || row.path != "" || row.fingerprint != "" {
			t.Errorf("S03E0%d: %+v", n, row)
		}
	}
	if row := s.row("series-1622", 4, 1); !row.have {
		t.Errorf("another episode: %+v", row)
	}
	for _, e := range title["seasons"].([]any) {
		season := e.(map[string]any)
		if season["number"] != float64(3) {
			continue
		}
		for _, ep := range season["episodes"].([]any) {
			if st := ep.(map[string]any)["state"]; st == "downloaded" {
				t.Errorf("an episode of the shared file still shows downloaded: %v", ep)
			}
		}
	}
	var notes []map[string]any
	for _, h := range s.history() {
		if m := h.(map[string]any); m["kind"] == "wrong_file" {
			notes = append(notes, m)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0]["message"].(string), "S03E01") || notes[0]["episode"] != "s03e02" {
		t.Errorf("one note on the episode asked about, naming the other: %v", notes)
	}
	waitFor(t, "the Jellyfin refresh", 5*time.Second, func() bool { return jf.Refreshes() == 1 })
}

func TestWrongFileOfAFileAlreadyGoneFromDiskStillResetsTheCatalog(t *testing.T) {
	s, _ := startFiles(t, false)
	s.remove(duneFile)
	s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 200)
	if row := s.row("movie-438631", 0, 0); row.have || !row.monitored || row.path != "" {
		t.Errorf("row: %+v", row)
	}
	if present(s.moviePath("Dune (2021)/Dune (2021) [Bluray-1080p].en.srt")) {
		t.Error("the sidecars of a video that is already gone still go")
	}
}

func TestWrongFileIsRefusedWithoutChangingAnythingWhenItCannotBeDone(t *testing.T) {
	s, _ := startFiles(t, false)
	before := libraryFingerprint(t, s.env)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":329865}`, 201)
	s.mustCall("POST", "/api/titles/movie-329865/wrong-file", "", 409)
	s.mustCall("POST", "/api/titles/series-1622/episodes/s05e01/wrong-file", "", 409)
	s.mustCall("POST", "/api/titles/movie-1/wrong-file", "", 404)
	s.mustCall("POST", "/api/titles/series-1/episodes/s01e01/wrong-file", "", 404)
	s.mustCall("POST", "/api/titles/movie-438631/episodes/s01e01/wrong-file", "", 404)
	s.mustCall("POST", "/api/titles/series-1622/episodes/s09e09/wrong-file", "", 404)
	s.mustCall("POST", "/api/titles/series-1622/wrong-file", "", 400)
	for _, bad := range []struct{ path, body string }{
		{"/api/titles/movie-438631/wrong-file", `{"reSearch":"yes"}`},
		{"/api/titles/movie-438631/wrong-file", `{"other":true}`},
		{"/api/titles/movie-438631/wrong-file", `[]`},
	} {
		if got := s.bad("POST", bad.path, bad.body).status; got != 400 {
			t.Errorf("POST %s %s: %d, want 400", bad.path, bad.body, got)
		}
	}
	if got := s.bad("POST", "/api/titles/series-1622/episodes/nonsense/wrong-file", "").status; got != 404 {
		t.Errorf("a malformed episode id: %d, want 404", got)
	}
	if libraryFingerprint(t, s.env) != before {
		t.Error("a refusal changed the library")
	}
	if row := s.row("movie-438631", 0, 0); !row.have {
		t.Errorf("row: %+v", row)
	}
}

func TestWrongFileInDryRunIsRefusedAndTheFileStays(t *testing.T) {
	s, jf := startFiles(t, true)
	before := libraryFingerprint(t, s.env)
	got := s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 409)
	if got["error"] == "" {
		t.Errorf("a refusal says why: %v", got)
	}
	if libraryFingerprint(t, s.env) != before || jf.Calls() != 0 {
		t.Error("dry-run deleted a file or called Jellyfin")
	}
	if row := s.row("movie-438631", 0, 0); !row.have {
		t.Errorf("row: %+v", row)
	}
}

func TestWrongFileNeverTouchesAPathOutsideTheLibrary(t *testing.T) {
	s, _ := startFiles(t, false)
	secret := s.env["MARQUEE_MOVIES_PATH"] + "/../secret.mkv"
	if err := os.WriteFile(secret, []byte("not the library's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db().Exec(`UPDATE titles SET file_path = '../secret.mkv' WHERE id = 'movie-438631'`); err != nil {
		t.Fatal(err)
	}
	s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 409)
	if b, _ := os.ReadFile(secret); string(b) != "not the library's" {
		t.Error("a file outside the library was touched")
	}
	if !present(s.moviePath(duneFile)) {
		t.Error("the real file was touched")
	}
	if row := s.row("movie-438631", 0, 0); !row.have || row.path != "../secret.mkv" {
		t.Errorf("a refused delete leaves the catalog alone: %+v", row)
	}
}

func TestWrongFileOfAFileWhoseLocationIsNotRecordedAsksForARescan(t *testing.T) {
	s, _ := startFiles(t, false)
	s.forgetFiles("movie-438631")
	got := s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 409)
	if msg, _ := got["error"].(string); !strings.Contains(msg, "rescan") {
		t.Errorf("the refusal says to rescan: %v", got)
	}
	if !present(s.moviePath(duneFile)) {
		t.Error("nothing is deleted from a guess")
	}
}
