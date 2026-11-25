package app_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/importer"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

const (
	duneFile    = "Dune (2021)/Dune (2021) [Bluray-1080p].mkv"
	arrivalFile = "Arrival (2016)/Arrival (2016) [Bluray-1080p].mkv"
	houseS3E12  = "House/Season 03/House - S03E01-E02 - Two in one [Bluray-1080p].mkv"
	houseS4E1   = "House/Season 04/House - S04E01 - Episode 1 [Bluray-1080p].mkv"
	houseS4E2   = "House/Season 04/House - S04E02 - Episode 2 [Bluray-1080p].mkv"
)

type fileRow struct {
	have, monitored bool
	path            string
	size            int64
	fingerprint     string
}

func (s *realSvc) db() *sql.DB {
	s.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(s.env["MARQUEE_DATA_DIR"], catalog.FileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = db.Close() })
	return db
}

func (s *realSvc) row(titleID string, season, number int) fileRow {
	s.t.Helper()
	q, args := `SELECT have_file, monitored, file_path, file_size, file_fingerprint FROM titles WHERE id = ?`, []any{titleID}
	if season != 0 {
		q, args = `SELECT have_file, monitored, file_path, file_size, file_fingerprint FROM episodes WHERE title_id = ? AND season = ? AND number = ?`, []any{titleID, season, number}
	}
	var f fileRow
	var have, mon int
	if err := s.db().QueryRow(q, args...).Scan(&have, &mon, &f.path, &f.size, &f.fingerprint); err != nil {
		s.t.Fatal(err)
	}
	f.have, f.monitored = have == 1, mon == 1
	return f
}

// forgetFiles puts a title's file columns back as a v1 database had them.
func (s *realSvc) forgetFiles(titleID string) {
	s.t.Helper()
	for _, q := range []string{
		`UPDATE titles SET file_path = '', file_size = 0, file_fingerprint = '' WHERE id = ?`,
		`UPDATE episodes SET file_path = '', file_size = 0, file_fingerprint = '' WHERE title_id = ?`,
	} {
		if _, err := s.db().Exec(q, titleID); err != nil {
			s.t.Fatal(err)
		}
	}
}

// abs resolves a library-relative path: House/... lives under the series folder, the rest under movies.
func (s *realSvc) abs(rel string) string {
	if strings.HasPrefix(rel, "House/") {
		return filepath.Join(s.env["MARQUEE_SERIES_PATH"], filepath.FromSlash(rel))
	}
	return filepath.Join(s.env["MARQUEE_MOVIES_PATH"], filepath.FromSlash(rel))
}

func (s *realSvc) remove(rel string) {
	s.t.Helper()
	if err := os.Remove(s.abs(rel)); err != nil {
		s.t.Fatal(err)
	}
}

func (s *realSvc) rescan(path string) map[string]any {
	s.t.Helper()
	return s.mustCall("POST", path, "", 200)
}

func (s *realSvc) removedNotes(titleID string) []map[string]any {
	s.t.Helper()
	var out []map[string]any
	for _, h := range s.history() {
		m := h.(map[string]any)
		if m["kind"] == "removed" && (titleID == "" || m["titleId"] == titleID) {
			out = append(out, m)
		}
	}
	return out
}

func startRescan(t *testing.T, opts ...svcOpt) *realSvc {
	t.Helper()
	s, err := startAdoptWith(t, adoptTMDB(t), library(t,
		[]string{duneFile, "Dune (2021)/Dune (2021) [Bluray-1080p].en.srt"},
		[]string{houseS3E12, houseS4E1, houseS4E2, "House/Season 04/Making of House.mkv"}), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func startAdoptWith(t *testing.T, tm *tmdbfake.Fake, prep func(map[string]string), opts ...svcOpt) (*realSvc, error) {
	t.Helper()
	s := startRealWith(t, tm, "", prep, opts...)
	return s, s.adoptErr
}

func TestAdoptionRecordsEachFilesPathSizeAndFingerprint(t *testing.T) {
	s := startRescan(t)
	want, err := importer.Fingerprint(s.abs(duneFile))
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(s.abs(duneFile))
	if got := s.row("movie-438631", 0, 0); got.path != duneFile || got.size != info.Size() || got.fingerprint != want {
		t.Errorf("movie row %+v, want %s, %d, %s", got, duneFile, info.Size(), want)
	}
	two, one := s.row("series-1622", 3, 1), s.row("series-1622", 3, 2)
	if two.path != houseS3E12 || two.fingerprint == "" || two != one {
		t.Errorf("a two-episode file is recorded against both episodes: %+v %+v", two, one)
	}
	if e := s.row("series-1622", 4, 2); e.path != houseS4E2 || e.fingerprint == "" {
		t.Errorf("episode row %+v", e)
	}
}

func TestAFullRescanMarksAFileDeletedOutsideMarqueeMissingAndUnmonitored(t *testing.T) {
	s := startRescan(t)
	s.remove(duneFile)
	s.remove(houseS4E2)

	got := s.rescan("/api/library/rescan")
	if got["missingOnDisk"] != float64(2) || got["titlesScanned"] != float64(2) || got["adopted"] != float64(0) || got["foundOnDisk"] != float64(0) {
		t.Errorf("result: %v", got)
	}
	if m := s.row("movie-438631", 0, 0); m != (fileRow{}) {
		t.Errorf("a movie whose file is gone is missing, unmonitored and has no file recorded: %+v", m)
	}
	if e := s.row("series-1622", 4, 2); e != (fileRow{}) {
		t.Errorf("episode row %+v", e)
	}
	if e := s.row("series-1622", 4, 1); !e.have || !e.monitored || e.path != houseS4E1 {
		t.Errorf("an episode whose file is still there is untouched: %+v", e)
	}
	if e := s.episodes("series-1622")["s04e02"]; e["state"] != "missing" || e["monitored"] != false {
		t.Errorf("episode in the API: %v", e)
	}
	notes := s.removedNotes("movie-438631")
	if len(notes) != 1 || notes[0]["message"] != "File removed from disk; unmonitored." {
		t.Errorf("history of the movie: %v", notes)
	}
	if n := len(s.removedNotes("series-1622")); n != 1 {
		t.Errorf("one note for the one episode: %d", n)
	}

	again := s.rescan("/api/library/rescan")
	if again["missingOnDisk"] != float64(0) || len(s.removedNotes("")) != 2 {
		t.Errorf("a file already recorded as gone is not reported again: %v", again)
	}
}

func TestDeletingOneFileOfATwoEpisodeFileUnmonitorsBothEpisodes(t *testing.T) {
	s := startRescan(t)
	s.remove(houseS3E12)
	s.rescan("/api/library/rescan")
	for _, n := range []int{1, 2} {
		if e := s.row("series-1622", 3, n); e != (fileRow{}) {
			t.Errorf("S03E0%d: %+v", n, e)
		}
	}
}

func TestATitleRescanReadsOnlyThatTitleAndNeverAsksTMDB(t *testing.T) {
	s := startRescan(t)
	s.remove(duneFile)
	s.remove(houseS4E2)
	hits := s.tmdb.Hits("/3")

	title := s.rescan("/api/titles/movie-438631/rescan")
	if title["id"] != "movie-438631" || title["monitored"] != false {
		t.Errorf("the reply is the updated title: %v", title)
	}
	if e := s.row("series-1622", 4, 2); !e.have || !e.monitored {
		t.Errorf("another title was not scanned: %+v", e)
	}
	if n := s.tmdb.Hits("/3"); n != hits {
		t.Errorf("a title rescan asked TMDB %d times", n-hits)
	}
	s.mustCall("POST", "/api/titles/series-1622/rescan", "", 200)
	if e := s.row("series-1622", 4, 2); e.have || e.monitored {
		t.Errorf("after its own rescan: %+v", e)
	}
	if n := s.tmdb.Hits("/3"); n != hits {
		t.Errorf("a title rescan asked TMDB %d times", n-hits)
	}
}

func TestATitleRescanOfAnUnknownTitleIsNotFound(t *testing.T) {
	s := startRescan(t)
	for _, id := range []string{"movie-1", "series-1"} {
		s.mustCall("POST", "/api/titles/"+id+"/rescan", "", 404)
	}
}

func TestARescanRecordsAFileFoundOnDiskForAMissingTitle(t *testing.T) {
	s := startRescan(t)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":329865}`, 201)
	if m := s.row("movie-329865", 0, 0); m.have {
		t.Fatalf("a movie added by hand has no file: %+v", m)
	}
	lay(t, s.env["MARQUEE_MOVIES_PATH"], arrivalFile)
	lay(t, s.env["MARQUEE_SERIES_PATH"], "House/Season 04/House - S04E03 - Episode 3 [Bluray-1080p].mkv")

	got := s.rescan("/api/library/rescan")
	if got["foundOnDisk"] != float64(2) {
		t.Errorf("result: %v", got)
	}
	want, _ := importer.Fingerprint(s.abs(arrivalFile))
	if m := s.row("movie-329865", 0, 0); !m.have || m.path != arrivalFile || m.fingerprint != want || m.size == 0 {
		t.Errorf("the movie: %+v", m)
	}
	if e := s.row("series-1622", 4, 3); !e.have || e.path != "House/Season 04/House - S04E03 - Episode 3 [Bluray-1080p].mkv" || e.fingerprint == "" {
		t.Errorf("the episode: %+v", e)
	}
}

func TestATitleRescanRecordsAFileFoundInItsFolder(t *testing.T) {
	s := startRescan(t)
	lay(t, s.env["MARQUEE_SERIES_PATH"], "House/Season 04/House - S04E03 - Episode 3 [Bluray-1080p].mkv")
	s.rescan("/api/titles/series-1622/rescan")
	if e := s.row("series-1622", 4, 3); !e.have || e.fingerprint == "" {
		t.Errorf("the episode: %+v", e)
	}
}

func TestARescanFillsTheFileColumnsOfARowAV1DatabaseLeftEmpty(t *testing.T) {
	s := startRescan(t)
	before := s.row("movie-438631", 0, 0)
	s.forgetFiles("movie-438631")
	s.forgetFiles("series-1622")

	got := s.rescan("/api/library/rescan")
	if got["foundOnDisk"] != float64(0) || got["missingOnDisk"] != float64(0) {
		t.Errorf("rows that already had their file are not counted as found or missing: %v", got)
	}
	if after := s.row("movie-438631", 0, 0); after != before {
		t.Errorf("movie row %+v, want %+v", after, before)
	}
	if e := s.row("series-1622", 4, 1); !e.have || e.path != houseS4E1 || e.fingerprint == "" || e.size == 0 {
		t.Errorf("episode row %+v", e)
	}
}

func TestAFingerprintIsRecomputedOnlyWhenEmptyOrTheSizeChanged(t *testing.T) {
	s := startRescan(t)
	if _, err := s.db().Exec(`UPDATE titles SET file_fingerprint = 'kept' WHERE id = 'movie-438631'`); err != nil {
		t.Fatal(err)
	}
	s.rescan("/api/library/rescan")
	if m := s.row("movie-438631", 0, 0); m.fingerprint != "kept" {
		t.Errorf("an unchanged file keeps its fingerprint: %+v", m)
	}
	if err := os.WriteFile(s.abs(duneFile), []byte("a replacement of another length"), 0o640); err != nil {
		t.Fatal(err)
	}
	s.rescan("/api/library/rescan")
	want, _ := importer.Fingerprint(s.abs(duneFile))
	if m := s.row("movie-438631", 0, 0); m.fingerprint != want || m.size != int64(len("a replacement of another length")) {
		t.Errorf("a file of another size is fingerprinted again: %+v", m)
	}
}

func TestAFullRescanAdoptsANewFolderAndAsksTMDBOnlyAboutIt(t *testing.T) {
	s := startRescan(t)
	hits := s.tmdb.Hits("/3")
	if got := s.rescan("/api/library/rescan"); got["adopted"] != float64(0) {
		t.Errorf("nothing new: %v", got)
	}
	if n := s.tmdb.Hits("/3"); n != hits {
		t.Errorf("a rescan of a library it knows asked TMDB %d times", n-hits)
	}

	lay(t, s.env["MARQUEE_MOVIES_PATH"], arrivalFile)
	got := s.rescan("/api/library/rescan")
	if got["adopted"] != float64(1) || got["titlesScanned"] != float64(3) {
		t.Errorf("result: %v", got)
	}
	arrival := s.mustCall("GET", "/api/titles/movie-329865", "", 200)
	if arrival["badge"].(map[string]any)["state"] != "downloaded" {
		t.Errorf("the adopted movie: %v", arrival)
	}
	if m := s.row("movie-329865", 0, 0); m.path != arrivalFile || m.fingerprint == "" {
		t.Errorf("the adopted movie's file: %+v", m)
	}
	hits = s.tmdb.Hits("/3")
	s.rescan("/api/library/rescan")
	if n := s.tmdb.Hits("/3"); n != hits {
		t.Errorf("the adopted folder is known now, but TMDB was asked %d times", n-hits)
	}
}

func TestAFullRescanReplacesTheLibraryProblemsWithTheCurrentOnes(t *testing.T) {
	s := startRescan(t)
	if _, ok := s.problem("Series/House/Season 04/Making of House.mkv"); !ok {
		t.Fatalf("the stray file is a problem at the start: %v", s.problems())
	}
	lay(t, s.env["MARQUEE_MOVIES_PATH"], "Nowhere Film (2001)/Nowhere Film (2001) [Bluray-1080p].mkv")
	got := s.rescan("/api/library/rescan")
	if got["problems"] != float64(2) {
		t.Errorf("result: %v", got)
	}
	if p, ok := s.problem("Movies/Nowhere Film (2001)"); !ok || p.reason != "unmatched" {
		t.Errorf("the new unmatched folder: %+v", p)
	}

	s.remove("House/Season 04/Making of House.mkv")
	if err := os.RemoveAll(filepath.Join(s.env["MARQUEE_MOVIES_PATH"], "Nowhere Film (2001)")); err != nil {
		t.Fatal(err)
	}
	if got := s.rescan("/api/library/rescan"); got["problems"] != float64(0) {
		t.Errorf("resolved problems clear: %v %v", got, s.problems())
	}
	if p := s.problems(); len(p) != 0 {
		t.Errorf("problems: %v", p)
	}
}

func TestATitleRescanRefreshesOnlyTheProblemsUnderItsFolder(t *testing.T) {
	s := startRescan(t)
	lay(t, s.env["MARQUEE_MOVIES_PATH"], "Nowhere Film (2001)/Nowhere Film (2001) [Bluray-1080p].mkv")
	s.rescan("/api/library/rescan")
	s.remove("House/Season 04/Making of House.mkv")
	lay(t, s.env["MARQUEE_SERIES_PATH"], "House/Season 04/Outtakes.mkv")

	s.rescan("/api/titles/series-1622/rescan")
	if _, ok := s.problem("Series/House/Season 04/Making of House.mkv"); ok {
		t.Error("a problem the file no longer causes is cleared")
	}
	if _, ok := s.problem("Series/House/Season 04/Outtakes.mkv"); !ok {
		t.Error("a new problem under the folder is listed")
	}
	if _, ok := s.problem("Movies/Nowhere Film (2001)"); !ok {
		t.Error("a problem outside the folder is left alone")
	}
}

func TestARescanFailingOnTMDBChangesNothingAndTheNextOneCatchesUp(t *testing.T) {
	s := startRescan(t)
	s.remove(duneFile)
	lay(t, s.env["MARQUEE_MOVIES_PATH"], arrivalFile)
	s.tmdb.Down("/3/search", 500)

	if r := s.bad("POST", "/api/library/rescan", ""); r.status != 502 {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
	if m := s.row("movie-438631", 0, 0); !m.have || !m.monitored {
		t.Errorf("a scan that did not finish removed nothing: %+v", m)
	}
	s.tmdb.Heal()
	got := s.rescan("/api/library/rescan")
	if got["adopted"] != float64(1) || got["missingOnDisk"] != float64(1) {
		t.Errorf("the next scan: %v", got)
	}
}

func TestAnEmptyLibraryFolderIsNotTakenForEveryFileDeleted(t *testing.T) {
	s := startRescan(t)
	if err := os.RemoveAll(filepath.Join(s.env["MARQUEE_MOVIES_PATH"], "Dune (2021)")); err != nil {
		t.Fatal(err)
	}
	if r := s.bad("POST", "/api/library/rescan", ""); r.status != 503 || !strings.Contains(string(r.body), "movies") {
		t.Errorf("status %d: %s", r.status, r.body)
	}
	if m := s.row("movie-438631", 0, 0); !m.have || !m.monitored {
		t.Errorf("an unmounted share must not unmonitor the library: %+v", m)
	}
}

func TestAMissingFolderOfATrackedTitleIsFilesDeleted(t *testing.T) {
	s := startRescan(t)
	lay(t, s.env["MARQUEE_SERIES_PATH"], "Other Show/Season 01/Other Show - S01E01 - Pilot [Bluray-1080p].mkv")
	if err := os.RemoveAll(filepath.Join(s.env["MARQUEE_SERIES_PATH"], "House")); err != nil {
		t.Fatal(err)
	}
	if got := s.rescan("/api/library/rescan"); got["missingOnDisk"] != float64(4) {
		t.Errorf("result: %v", got)
	}
}

func TestAFolderTheServiceCannotReadIsNotTakenForDeletedFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not stop root")
	}
	s := startRescan(t)
	dir := filepath.Join(s.env["MARQUEE_SERIES_PATH"], "House", "Season 04")
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
	s.rescan("/api/library/rescan")
	if e := s.row("series-1622", 4, 1); !e.have || !e.monitored {
		t.Errorf("an unreadable season keeps its episodes: %+v", e)
	}
}

func TestARescanInDryRunReadsTheLibraryAndChangesNothingOnDisk(t *testing.T) {
	s := startRescan(t)
	s.stop()
	s2, err := startAdoptWith(t, s.tmdb, func(env map[string]string) {
		for k, v := range s.env {
			env[k] = v
		}
		env["MARQUEE_DRY_RUN"] = "true"
	})
	if err != nil {
		t.Fatal(err)
	}
	s2.remove(duneFile)
	before := libraryFingerprint(t, s2.env)
	if got := s2.rescan("/api/library/rescan"); got["missingOnDisk"] != float64(1) {
		t.Errorf("result: %v", got)
	}
	s2.rescan("/api/titles/series-1622/rescan")
	if after := libraryFingerprint(t, s2.env); after != before {
		t.Errorf("the media root changed:\n%s\n--\n%s", before, after)
	}
}

func TestConcurrentRescansAreSerialisedAndAllSucceed(t *testing.T) {
	s := startRescan(t)
	s.remove(houseS4E2)
	var wg sync.WaitGroup
	var missing atomic.Int64
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := s.call("POST", "/api/library/rescan", "")
			if r.status != 200 {
				t.Errorf("status %d: %s", r.status, r.body)
				return
			}
			missing.Add(int64(r.json(t)["missingOnDisk"].(float64)))
		}()
	}
	wg.Wait()
	if missing.Load() != 1 || len(s.removedNotes("series-1622")) != 1 {
		t.Errorf("the deleted file was reported %d times, with %d notes", missing.Load(), len(s.removedNotes("series-1622")))
	}
}

type rescanTimer struct {
	ticks chan time.Time
	asked atomic.Int64
	built atomic.Int64
}

func newRescanTimer() *rescanTimer { return &rescanTimer{ticks: make(chan time.Time)} }

func (r *rescanTimer) opt(o *app.Options) {
	o.RescanTick = func(d time.Duration) (<-chan time.Time, func()) {
		r.asked.Store(int64(d))
		r.built.Add(1)
		return r.ticks, func() {}
	}
}

func TestTheRescanTimerRunsAScanOnEachTickAtTheConfiguredInterval(t *testing.T) {
	timer := newRescanTimer()
	s := startRescan(t, timer.opt)
	if got := time.Duration(timer.asked.Load()); got != 30*time.Minute {
		t.Errorf("default interval %v, want 30m", got)
	}
	s.remove(duneFile)
	time.Sleep(50 * time.Millisecond)
	if m := s.row("movie-438631", 0, 0); !m.have {
		t.Fatal("no tick came, and the library was scanned")
	}
	select {
	case timer.ticks <- time.Now():
	case <-time.After(5 * time.Second):
		t.Fatal("the timer is not waiting for ticks")
	}
	waitFor(t, "the tick's scan", 10*time.Second, func() bool { return !s.row("movie-438631", 0, 0).have })
	if m := s.row("movie-438631", 0, 0); m.monitored {
		t.Errorf("the scan unmonitors: %+v", m)
	}
}

func TestTheRescanIntervalComesFromTheEnvironmentAndZeroTurnsTheTimerOff(t *testing.T) {
	timer := newRescanTimer()
	if _, err := startAdoptWith(t, adoptTMDB(t), func(env map[string]string) {
		library(t, []string{duneFile}, nil)(env)
		env["MARQUEE_RESCAN_INTERVAL"] = "45s"
	}, timer.opt); err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(timer.asked.Load()); got != 45*time.Second {
		t.Errorf("interval %v, want 45s", got)
	}

	off := newRescanTimer()
	if _, err := startAdoptWith(t, adoptTMDB(t), func(env map[string]string) {
		library(t, []string{duneFile}, nil)(env)
		env["MARQUEE_RESCAN_INTERVAL"] = "0"
	}, off.opt); err != nil {
		t.Fatal(err)
	}
	if off.built.Load() != 0 {
		t.Error("an interval of 0 starts no timer")
	}
}

func TestAnInvalidRescanIntervalStopsStartupNamingTheSetting(t *testing.T) {
	env := testEnv(t)
	env["MARQUEE_RESCAN_INTERVAL"] = "often"
	env["MARQUEE_TMDB_KEY"] = "k"
	err := app.RunWith(context.Background(), app.Options{}, func(k string) string { return env[k] }, newSyncBuf(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "MARQUEE_RESCAN_INTERVAL") {
		t.Errorf("error: %v", err)
	}
}

func TestATitleRescanFindsTheFolderOfARowAV1DatabaseLeftWithoutAPath(t *testing.T) {
	s := startRescan(t)
	s.forgetFiles("series-1622")
	s.rescan("/api/titles/series-1622/rescan")
	if e := s.row("series-1622", 4, 1); !e.have || e.path != houseS4E1 || e.fingerprint == "" {
		t.Errorf("the series' plain-named folder is its folder: %+v", e)
	}
}

func TestATitleRescanLeavesATitleWhoseFolderItCannotTellAlone(t *testing.T) {
	s := startRescan(t)
	s.forgetFiles("movie-438631")
	if err := os.Rename(filepath.Join(s.env["MARQUEE_MOVIES_PATH"], "Dune (2021)"), filepath.Join(s.env["MARQUEE_MOVIES_PATH"], "Dune")); err != nil {
		t.Fatal(err)
	}
	s.rescan("/api/titles/movie-438631/rescan")
	if m := s.row("movie-438631", 0, 0); !m.have || !m.monitored {
		t.Errorf("a folder the scan cannot place is not a deleted file: %+v", m)
	}
	if got := s.rescan("/api/library/rescan"); got["missingOnDisk"] != float64(0) {
		t.Errorf("the full scan places the folder through TMDB: %v", got)
	}
	if m := s.row("movie-438631", 0, 0); !m.have || m.path != "Dune/Dune (2021) [Bluray-1080p].mkv" {
		t.Errorf("a renamed folder is followed: %+v", m)
	}
}
