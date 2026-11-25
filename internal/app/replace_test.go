package app_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/importer"
)

const (
	duneOld      = "Dune (2021)/Dune (2021) [HDTV-720p].mkv"
	duneOldEn    = "Dune (2021)/Dune (2021) [HDTV-720p].en.srt"
	duneOldEs    = "Dune (2021)/Dune (2021) [HDTV-720p].es.forced.srt"
	duneOldOther = "Dune (2021)/Dune (2021) [HDTV-720p] Extended.mkv"
	prophecyE1   = "Dune - Prophecy/Season 01/Dune - Prophecy - S01E01 - Aired One [WEBDL-1080p].mkv"
	prophecyE2   = "Dune - Prophecy/Season 01/Dune - Prophecy - S01E02 - Aired Two [WEBDL-1080p].mkv"
)

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (s *realSvc) write(path string, data []byte) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		s.t.Fatal(err)
	}
}

// ownedDune is the rig with Dune's old file, two sidecars and nothing else
// on disk, all recorded by a rescan.
func ownedDune(t *testing.T) *rig {
	t.Helper()
	r, _ := setup(t)
	lay(t, r.env["MARQUEE_MOVIES_PATH"], duneOld, duneOldEn, duneOldEs)
	r.rescan("/api/titles/movie-438631/rescan")
	if row := r.row("movie-438631", 0, 0); !row.have || row.path != duneOld {
		t.Fatalf("Dune's old file is recorded: %+v", row)
	}
	return r
}

func replaceJob(r *rig, name string, f debridfake.File) {
	j := movieJob(r.cdn.Add(name, f), name)
	j.Replace = true
	r.enqueue(j)
}

func TestAReplacementLeavesTheOldFileUntilItIsVerifiedThenSwapsItInAndDeletesTheOld(t *testing.T) {
	r := ownedDune(t)
	lay(t, r.env["MARQUEE_MOVIES_PATH"], duneOldOther)
	data := debridfake.MKV(2 << 20)
	replaceJob(r, "new.mkv", debridfake.File{Data: data, Pace: 30 * time.Millisecond})

	waitFor(t, "the download to run", 5*time.Second, func() bool { return r.cdn.Streaming() > 0 })
	if got := read(t, r.moviePath(duneOld)); string(got) != "content of "+duneOld {
		t.Errorf("the old file changed while the replacement downloads: %q", got)
	}
	if !present(r.moviePath(duneOldEn)) || !present(r.moviePath(duneOldEs)) {
		t.Error("the old sidecars stay until the swap")
	}
	if row := r.row("movie-438631", 0, 0); !row.have || row.path != duneOld {
		t.Errorf("have_file stays 1 and points at the old file meanwhile: %+v", row)
	}

	r.waitHistoryKind("imported", 1)
	newRel := "Dune (2021)/Dune (2021) [WEBDL-1080p].mkv"
	if got := read(t, r.moviePath(newRel)); !bytes.Equal(got, data) {
		t.Error("the new file is not the downloaded one")
	}
	for _, gone := range []string{duneOld, duneOldEn, duneOldEs} {
		if present(r.moviePath(gone)) {
			t.Errorf("%s should be deleted by the swap", gone)
		}
	}
	if !present(r.moviePath(duneOldOther)) {
		t.Error("another video that shares the old name's prefix must stay")
	}
	want, _ := importer.Fingerprint(r.moviePath(newRel))
	if row := r.row("movie-438631", 0, 0); !row.have || row.path != newRel || row.fingerprint != want || row.size != int64(len(data)) {
		t.Errorf("row after the swap: %+v", row)
	}
	if left := tree(t, filepath.Join(filepath.Dir(r.env["MARQUEE_MOVIES_PATH"]), "tmp")); len(left) != 0 {
		t.Errorf("temp folder holds %v", left)
	}
	waitFor(t, "the Jellyfin refresh", 5*time.Second, func() bool { return r.jf.Refreshes() == 1 })
}

func TestAReplacementWithTheSameNameRenamesOverTheOldFileAndDropsOnlyItsSidecars(t *testing.T) {
	r, _ := setup(t)
	same := "Dune (2021)/Dune (2021) [WEBDL-1080p].mkv"
	lay(t, r.env["MARQUEE_MOVIES_PATH"], same, "Dune (2021)/Dune (2021) [WEBDL-1080p].en.srt")
	r.rescan("/api/titles/movie-438631/rescan")
	lay(t, r.env["MARQUEE_MOVIES_PATH"], "Dune (2021)/Dune (2021) [WEBDL-1080p] Extended.en.srt")
	data := debridfake.MKV(300 << 10)
	replaceJob(r, "new.mkv", debridfake.File{Data: data})
	r.waitHistoryKind("imported", 1)

	if got := read(t, r.moviePath(same)); !bytes.Equal(got, data) {
		t.Error("the file at the old name is not the downloaded one")
	}
	if present(r.moviePath("Dune (2021)/Dune (2021) [WEBDL-1080p].en.srt")) {
		t.Error("the old sidecar goes")
	}
	if !present(r.moviePath("Dune (2021)/Dune (2021) [WEBDL-1080p] Extended.en.srt")) {
		t.Error("a sidecar of another video stays")
	}
	if got := tree(t, r.env["MARQUEE_MOVIES_PATH"]); len(got) != 2 {
		t.Errorf("movies folder holds %v", got)
	}
	want, _ := importer.Fingerprint(r.moviePath(same))
	if row := r.row("movie-438631", 0, 0); !row.have || row.path != same || row.fingerprint != want {
		t.Errorf("row: %+v", row)
	}
}

func TestAFailedReplacementLeavesTheOldFileAndItsSidecarsAndBlocklistsTheRelease(t *testing.T) {
	notVideo := append([]byte("MZ\x90\x00"), make([]byte, 100<<10)...)
	for name, f := range map[string]debridfake.File{
		"not a video": {Data: notVideo},
		"stalls":      {Data: debridfake.MKV(400 << 10), StallAt: 100 << 10},
	} {
		t.Run(name, func(t *testing.T) {
			r := ownedDune(t)
			replaceJob(r, "bad.mkv", f)
			entry := r.waitHistoryKind("failed", 1)[0].(map[string]any)
			if entry["kind"] != "failed" {
				t.Fatalf("history = %v", entry)
			}
			if got := read(t, r.moviePath(duneOld)); string(got) != "content of "+duneOld {
				t.Errorf("the old file changed: %q", got)
			}
			if !present(r.moviePath(duneOldEn)) || !present(r.moviePath(duneOldEs)) {
				t.Error("the old sidecars must stay")
			}
			if row := r.row("movie-438631", 0, 0); !row.have || row.path != duneOld {
				t.Errorf("have_file stays 1: %+v", row)
			}
			if !r.wasBlocked("bad.mkv") {
				t.Error("a failed replacement's release is blocklisted")
			}
			if got := tree(t, r.env["MARQUEE_MOVIES_PATH"]); len(got) != 3 {
				t.Errorf("movies folder holds %v", got)
			}
			waitFor(t, "the temp files to go", 5*time.Second, func() bool {
				return len(tree(t, filepath.Join(filepath.Dir(r.env["MARQUEE_MOVIES_PATH"]), "tmp"))) == 0
			})
		})
	}
}

func TestAReplacementNeverOverwritesAnUnrelatedFileAlreadyAtTheNewName(t *testing.T) {
	r := ownedDune(t)
	taken := "Dune (2021)/Dune (2021) [WEBDL-1080p].mkv"
	lay(t, r.env["MARQUEE_MOVIES_PATH"], taken)
	replaceJob(r, "new.mkv", debridfake.File{Data: debridfake.MKV(300 << 10)})
	entry := r.waitHistoryKind("failed", 1)[0].(map[string]any)
	if entry["reason"] != "local_error" {
		t.Errorf("history = %v", entry)
	}
	if string(read(t, r.moviePath(taken))) != "content of "+taken || string(read(t, r.moviePath(duneOld))) != "content of "+duneOld {
		t.Error("a file marquee did not create was touched, or the old file was")
	}
	if row := r.row("movie-438631", 0, 0); !row.have || row.path != duneOld {
		t.Errorf("row: %+v", row)
	}
	if r.wasBlocked("new.mkv") {
		t.Error("a problem on marquee's side blocklists nothing")
	}
}

func TestAReplacementIsStillCheckedAgainstTheOtherEpisodesOfTheSeriesButNotAgainstItself(t *testing.T) {
	r, _ := setup(t)
	one, two := debridfake.MKV(100<<10), debridfake.MKV(100<<10)
	two[len(two)-1] = 7
	r.write(r.showPath(prophecyE1), one)
	r.write(r.showPath(prophecyE2), two)
	r.rescan("/api/titles/series-1408/rescan")
	if row := r.row("series-1408", 1, 2); !row.have || row.fingerprint == "" {
		t.Fatalf("episode 2 is recorded: %+v", row)
	}

	dup := episodeJob(r.cdn.Add("dup.mkv", debridfake.File{Data: two}), "dup.mkv")
	dup.Replace = true
	r.enqueue(dup)
	entry := r.waitHistoryKind("failed", 1)[0].(map[string]any)
	if entry["reason"] != "duplicate_episode" {
		t.Fatalf("history = %v", entry)
	}
	if !bytes.Equal(read(t, r.showPath(prophecyE1)), one) {
		t.Error("the old episode file changed")
	}
	if !r.wasBlocked("dup.mkv") {
		t.Error("a duplicate's release is blocklisted")
	}

	same := episodeJob(r.cdn.Add("same.mkv", debridfake.File{Data: one}), "same.mkv")
	same.Replace = true
	r.enqueue(same)
	r.waitHistoryKind("imported", 1)
	if row := r.row("series-1408", 1, 1); !row.have || row.path != prophecyE1 {
		t.Errorf("replacing a file with identical bytes is not a duplicate of itself: %+v", row)
	}
}

func TestReplacingOneEpisodeOfATwoEpisodeFileLeavesTheOtherEpisodesFileAlone(t *testing.T) {
	r, _ := setup(t)
	shared := "Dune - Prophecy/Season 01/Dune - Prophecy - S01E01-E02 - Two [HDTV-720p].mkv"
	sidecar := "Dune - Prophecy/Season 01/Dune - Prophecy - S01E01-E02 - Two [HDTV-720p].en.srt"
	old := debridfake.MKV(100 << 10)
	r.write(r.showPath(shared), old)
	lay(t, r.env["MARQUEE_SERIES_PATH"], sidecar)
	r.rescan("/api/titles/series-1408/rescan")
	if a, b := r.row("series-1408", 1, 1), r.row("series-1408", 1, 2); a.path != shared || b.path != shared {
		t.Fatalf("both episodes record the shared file: %+v %+v", a, b)
	}

	j := episodeJob(r.cdn.Add("new.mkv", debridfake.File{Data: debridfake.MKV(150 << 10)}), "new.mkv")
	j.Replace = true
	r.enqueue(j)
	r.waitHistoryKind("imported", 1)

	if row := r.row("series-1408", 1, 1); row.path != prophecyE1 || !row.have {
		t.Errorf("episode 1 has its new file: %+v", row)
	}
	if row := r.row("series-1408", 1, 2); row.path != shared || !row.have {
		t.Errorf("episode 2 still has the shared file: %+v", row)
	}
	if !bytes.Equal(read(t, r.showPath(shared)), old) || !present(r.showPath(sidecar)) {
		t.Error("the shared file, still the other episode's, was deleted")
	}
}

func TestReplaceOnATitleWithNoFileIsAPlainImport(t *testing.T) {
	r, _ := setup(t)
	replaceJob(r, "new.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.waitHistoryKind("imported", 1)
	if row := r.row("movie-438631", 0, 0); !row.have || row.path != "Dune (2021)/Dune (2021) [WEBDL-1080p].mkv" {
		t.Errorf("row: %+v", row)
	}
}

func TestAReplacementThatCrashedAfterTheMoveFinishesByDeletingTheOldFile(t *testing.T) {
	r := ownedDune(t)
	data := debridfake.MKV(80 << 10)
	r.stop()
	store, err := catalog.Open(r.env["MARQUEE_DATA_DIR"], nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.AddDownload(ctx, catalog.NewDownload{
		ID: "crash-replace", TitleID: "movie-438631", Resolution: "1080p", Quality: "WEB-DL", ReleaseName: "Dune", ReplaceExisting: true,
		Release: catalog.ReleaseID{InfoHash: "dddddddddddddddddddddddddddddddddddddddd", FileName: "a.mkv"}, URL: "http://x.invalid/gone",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDownloadProgress(ctx, "crash-replace", int64(len(data)), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDownloadState(ctx, "crash-replace", catalog.StateImporting); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	newRel := "Dune (2021)/Dune (2021) [WEBDL-1080p].mkv"
	r.write(r.moviePath(newRel), data)

	r.startService()
	r.waitHistoryKind("imported", 1)
	if present(r.moviePath(duneOld)) || present(r.moviePath(duneOldEn)) {
		t.Error("the old file and its sidecars go when the interrupted swap is finished")
	}
	if row := r.row("movie-438631", 0, 0); !row.have || row.path != newRel {
		t.Errorf("row: %+v", row)
	}
}

func TestAnOwnedMovieOrEpisodeCannotBeGrabbedWithoutReplaceAndNothingIsQueued(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	lay(t, r.env["MARQUEE_MOVIES_PATH"], duneOld)
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	lay(t, r.env["MARQUEE_SERIES_PATH"], prophecyE1)
	r.rescan("/api/titles/movie-438631/rescan")
	r.rescan("/api/titles/series-1408/rescan")

	for _, tc := range []struct{ path, body string }{
		{"/api/titles/movie-438631/releases/anything/grab", ""},
		{"/api/titles/movie-438631/releases/anything/grab", `{"replace":false}`},
		{"/api/titles/series-1408/releases/anything/grab", `{"episode":"s01e01"}`},
		{"/api/titles/series-1408/releases/anything/grab", `{"episode":"s01e01","replace":false}`},
	} {
		got := r.mustCall("POST", tc.path, tc.body, 409)
		if msg, _ := got["error"].(string); !strings.Contains(msg, "replace") {
			t.Errorf("POST %s %s: the refusal says to pass replace: %v", tc.path, tc.body, got)
		}
	}
	if d := r.downloads(); len(d) != 0 {
		t.Errorf("queue = %v", d)
	}
	r.wantSearches(0, "an owned item is refused before AIOStreams is asked")
	// With replace the grab goes on to look for the release, which is not there.
	r.mustCall("POST", "/api/titles/movie-438631/releases/anything/grab", `{"replace":true}`, 404)
	// An episode that has no file is not owned.
	r.mustCall("POST", "/api/titles/series-1408/releases/anything/grab", `{"episode":"s01e02"}`, 404)
}

func TestGrabbingWithReplaceSwapsAnOwnedEpisodeForTheChosenRelease(t *testing.T) {
	r := newSchedRig(t, false)
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	lay(t, r.env["MARQUEE_SERIES_PATH"], prophecyE1)
	r.rescan("/api/titles/series-1408/rescan")
	data := debridfake.MKV(53 << 20)
	url := r.cdn.Add("ep.mkv", debridfake.File{Data: data})
	r.aio.Answer("series", "tvdb:403000:1:1", results(listed{name: "Dune.Prophecy.S01E01.1080p.WEB-DL.mkv", size: int64(len(data)), url: url, season: 1, ep: 1}))
	cands, _ := r.releases("/api/titles/series-1408/releases?episode=s01e01")
	if len(cands) != 1 {
		t.Fatalf("candidates = %v", cands)
	}
	path := "/api/titles/series-1408/releases/" + cands[0]["id"].(string) + "/grab"
	r.mustCall("POST", path, `{"episode":"s01e01"}`, 409)
	d := r.mustCall("POST", path, `{"episode":"s01e01","replace":true}`, 202)
	if d["episode"] != "s01e01" {
		t.Errorf("download = %v", d)
	}
	r.waitEntries("imported", 1)
	if got := read(t, filepath.Join(r.env["MARQUEE_SERIES_PATH"], filepath.FromSlash(prophecyE1))); !bytes.Equal(got, data) {
		t.Error("the episode's file was not replaced")
	}
	if row := r.row("series-1408", 1, 1); !row.have || row.size != int64(len(data)) {
		t.Errorf("row: %+v", row)
	}
}

func TestGrabbingAnOwnedItemInDryRunIsRefusedForDryRunNotForReplace(t *testing.T) {
	r := newSchedRig(t, true)
	r.addMovie()
	got := r.mustCall("POST", "/api/titles/movie-438631/releases/anything/grab", `{"replace":true}`, 409)
	if msg, _ := got["error"].(string); !strings.Contains(msg, "dry-run") {
		t.Errorf("the refusal names dry-run: %v", got)
	}
}
