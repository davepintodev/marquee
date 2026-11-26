package catalog_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/chooser"
)

// oldDownloads turns a current database back into one at schema 13: the
// downloads table without the v2 columns and without the 'caching' state.
const oldDownloads = `
CREATE TABLE downloads_old (
  id TEXT PRIMARY KEY,
  title_id TEXT NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
  season INTEGER NOT NULL DEFAULT 0,
  number INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL CHECK (state IN ('queued', 'downloading', 'verifying', 'importing', 'done', 'failed')),
  info_hash TEXT NOT NULL, file_name TEXT NOT NULL, size INTEGER NOT NULL, release_name TEXT NOT NULL,
  resolution TEXT NOT NULL DEFAULT '', quality TEXT NOT NULL DEFAULT '', url TEXT NOT NULL,
  bytes_done INTEGER NOT NULL DEFAULT 0, bytes_total INTEGER NOT NULL DEFAULT 0,
  reason TEXT NOT NULL DEFAULT '', message TEXT NOT NULL DEFAULT '', library_path TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, at_ms INTEGER NOT NULL DEFAULT 0
);
INSERT INTO downloads_old (rowid, id, title_id, season, number, state, info_hash, file_name, size, release_name, resolution, quality, url,
  bytes_done, bytes_total, reason, message, library_path, created_at, updated_at, at_ms)
SELECT rowid, id, title_id, season, number, state, info_hash, file_name, size, release_name, resolution, quality, url,
  bytes_done, bytes_total, reason, message, library_path, created_at, updated_at, at_ms FROM downloads;
DROP TABLE downloads;
ALTER TABLE downloads_old RENAME TO downloads;
CREATE UNIQUE INDEX downloads_one_active ON downloads (title_id, season, number) WHERE state NOT IN ('done', 'failed');
CREATE INDEX downloads_finished ON downloads (at_ms) WHERE state IN ('done', 'failed');
CREATE INDEX downloads_finished_by_target ON downloads (title_id, season, number, at_ms) WHERE state IN ('done', 'failed');
DROP TABLE run_mode; DROP TABLE pause_all; PRAGMA user_version = 13;
`

func rawDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, catalog.FileName)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestTheDownloadsRebuildKeepsEveryRowOfEveryStateAndItsOrder(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	clk := &clock{time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	s, err := catalog.Open(dir, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	movie := addMovie(t, s, 1)
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 2, Title: "House", Episodes: houseEpisodes()}); err != nil {
		t.Fatal(err)
	}
	finish(t, s, "done-movie", movie, 0, 0, "")
	clk.t = clk.t.Add(time.Minute)
	finish(t, s, "failed-ep", "series-2", 1, 1, "stalled")
	clk.t = clk.t.Add(time.Minute)
	finish(t, s, "done-ep", "series-2", 1, 2, "")
	for i, state := range []string{catalog.StateQueued, catalog.StateDownloading, catalog.StateVerifying, catalog.StateImporting} {
		id := "live-" + state
		if _, err := s.AddDownload(ctx, catalog.NewDownload{
			ID: id, TitleID: "series-2", Season: 3 + i/2, Number: i%2 + 1,
			Release: catalog.ReleaseID{InfoHash: "h" + id, FileName: id + ".mkv", Size: 9}, ReleaseName: id,
		}); err != nil {
			t.Fatal(err)
		}
		if state != catalog.StateQueued {
			if err := s.SetDownloadState(ctx, id, state); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, err := s.FinishedDownloads(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db := rawDB(t, dir)
	var rowids []int64
	rows, err := db.Query(`SELECT rowid FROM downloads ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n int64
		_ = rows.Scan(&n)
		rowids = append(rowids, n)
	}
	_ = rows.Close()
	if _, err := db.Exec(oldDownloads); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s, err = catalog.Open(dir, clk.now)
	if err != nil {
		t.Fatalf("the rebuild must run on a database from before it: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	after, err := s.FinishedDownloads(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || len(after) != 3 {
		t.Fatalf("finished downloads: %d before, %d after", len(before), len(after))
	}
	for i := range before {
		if after[i].ID != before[i].ID || after[i].State != before[i].State || after[i].Reason != before[i].Reason || !after[i].UpdatedAt.Equal(before[i].UpdatedAt) {
			t.Errorf("finished download %d: %+v, was %+v", i, after[i], before[i])
		}
		if after[i].ReplaceExisting {
			t.Errorf("an old download is not a replacement: %+v", after[i])
		}
	}
	active, err := s.ActiveDownloads(ctx)
	if err != nil || len(active) != 4 {
		t.Fatalf("active downloads: %d, %v", len(active), err)
	}
	states := map[string]string{}
	for _, d := range active {
		states[d.ID] = d.State
	}
	for _, st := range []string{catalog.StateQueued, catalog.StateDownloading, catalog.StateVerifying, catalog.StateImporting} {
		if states["live-"+st] != st {
			t.Errorf("live-%s is %q", st, states["live-"+st])
		}
	}

	db = rawDB(t, dir)
	var got []int64
	rows, err = db.Query(`SELECT rowid FROM downloads ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n int64
		_ = rows.Scan(&n)
		got = append(got, n)
	}
	_ = rows.Close()
	if len(got) != len(rowids) {
		t.Fatalf("rowids %v, were %v", got, rowids)
	}
	for i := range got {
		if got[i] != rowids[i] {
			t.Errorf("rowids %v, were %v: history cursors depend on them", got, rowids)
			break
		}
	}

	var replace, cached, torrent, file, percent int
	var covered string
	if err := db.QueryRow(`SELECT replace_existing, covered_episodes, cached, torbox_torrent_id, torbox_file_id, cache_percent FROM downloads WHERE id = 'done-movie'`).
		Scan(&replace, &covered, &cached, &torrent, &file, &percent); err != nil {
		t.Fatal(err)
	}
	if replace != 0 || covered != "" || cached != 1 || torrent != 0 || file != 0 || percent != 0 {
		t.Errorf("defaults: %d %q %d %d %d %d", replace, covered, cached, torrent, file, percent)
	}
	for _, idx := range []string{"downloads_one_active", "downloads_finished", "downloads_finished_by_target"} {
		var def string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ? AND tbl_name = 'downloads'`, idx).Scan(&def); err != nil {
			t.Errorf("index %s is not there: %v", idx, err)
		} else if !strings.Contains(def, "WHERE") {
			t.Errorf("index %s lost its condition: %s", idx, def)
		}
	}
	if _, err := db.Exec(`UPDATE downloads SET state = 'caching' WHERE id = 'live-queued'`); err != nil {
		t.Errorf("the caching state is allowed: %v", err)
	}
	if _, err := db.Exec(`UPDATE downloads SET state = 'bogus' WHERE id = 'live-queued'`); err == nil {
		t.Error("an unknown state is still refused")
	}
	if _, err := db.Exec(`INSERT INTO downloads (id, title_id, season, number, state, info_hash, file_name, size, release_name, url, created_at, updated_at)
		VALUES ('dup', 'series-2', 3, 1, 'queued', '', '', 0, '', '', '', '')`); err == nil {
		t.Error("one active download per target still holds")
	}
	if _, err := db.Exec(`DELETE FROM titles WHERE id = 'series-2'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM downloads WHERE title_id = 'series-2'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("removing a title still cascades to its downloads: %d, %v", n, err)
	}
}

func TestADownloadRemembersThatItReplacesAFile(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	movie := addMovie(t, s, 1)
	d, err := s.AddDownload(ctx, catalog.NewDownload{
		ID: "r", TitleID: movie, Release: catalog.ReleaseID{InfoHash: "h", FileName: "f.mkv", Size: 1}, ReleaseName: "r", ReplaceExisting: true,
	})
	if err != nil || !d.ReplaceExisting {
		t.Fatalf("added: %+v, %v", d, err)
	}
	for _, get := range []func() (catalog.Download, error){
		func() (catalog.Download, error) { return s.GetDownload(ctx, "r") },
		func() (catalog.Download, error) { return s.ActiveDownloadFor(ctx, movie, 0, 0) },
	} {
		if got, err := get(); err != nil || !got.ReplaceExisting {
			t.Errorf("read back: %+v, %v", got, err)
		}
	}
	active, err := s.ActiveDownloads(ctx)
	if err != nil || len(active) != 1 || !active[0].ReplaceExisting {
		t.Errorf("active: %+v, %v", active, err)
	}
}

func seedFiles(t *testing.T, s *catalog.Store) {
	t.Helper()
	ctx := context.Background()
	addMovie(t, s, 1)
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 2, Title: "House", Episodes: houseEpisodes()}); err != nil {
		t.Fatal(err)
	}
}

func doneDownload(t *testing.T, s *catalog.Store, id, title string, season, number int, hash, name string, size int64, libraryPath string, f catalog.File) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.AddDownload(ctx, catalog.NewDownload{
		ID: id, TitleID: title, Season: season, Number: number,
		Release: catalog.ReleaseID{InfoHash: hash, FileName: name, Size: size}, ReleaseName: id,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDownloadLibraryPath(ctx, id, libraryPath); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishDownload(ctx, id, f); err != nil {
		t.Fatal(err)
	}
}

func TestTrackedFilesListsEveryFileOfATitleIncludingSpecialsAndSharedOnes(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	seedFiles(t, s)
	doneDownload(t, s, "a", "series-2", 1, 1, "h1", "a.mkv", 5, "/lib/tv/House/Season 01/House - S01E01.mkv", catalog.File{Path: "House/Season 01/House - S01E01.mkv", Size: 5})
	doneDownload(t, s, "b", "series-2", 2, 1, "h2", "b.mkv", 5, "/lib/tv/House/Season 02/House - S02E01-E02.mkv", catalog.File{Path: "House/Season 02/House - S02E01-E02.mkv", Size: 5})
	if _, err := s.ApplyScan(ctx, "series-2", []catalog.FileFound{{Season: 2, Number: 2, File: catalog.File{Path: "House/Season 02/House - S02E01-E02.mkv", Size: 5}}}, nil); err != nil {
		t.Fatal(err)
	}
	files, err := s.TrackedFiles(ctx, "series-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("tracked files: %+v", files)
	}
	if _, err := s.TrackedFiles(ctx, "series-99"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("an unknown title: %v", err)
	}
	if got, err := s.TrackedFiles(ctx, "movie-1"); err != nil || len(got) != 0 {
		t.Errorf("a movie with no file tracks none: %+v, %v", got, err)
	}
}

func TestWrongFileResetsEveryTargetOfTheSharedFileAndBlocklistsTheRelease(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	seedFiles(t, s)
	shared := catalog.File{Path: "House/Season 02/House - S02E01-E02.mkv", Size: 5, Fingerprint: "fp"}
	doneDownload(t, s, "b", "series-2", 2, 1, "hh", "pack.mkv", 99, "/lib/tv/"+shared.Path, shared)
	doneDownload(t, s, "o", "series-2", 1, 1, "ho", "other.mkv", 7, "/lib/tv/House/Season 01/House - S01E01.mkv", catalog.File{Path: "House/Season 01/House - S01E01.mkv", Size: 7})
	if _, err := s.ApplyScan(ctx, "series-2", []catalog.FileFound{{Season: 2, Number: 2, File: shared}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, ep := range [][2]int{{2, 1}, {2, 2}} {
		if _, err := s.SetEpisodeMonitored(ctx, "series-2", ep[0], ep[1], false); err != nil {
			t.Fatal(err)
		}
	}

	res, err := s.ResetWrongFile(ctx, catalog.WrongFile{TitleID: "series-2", Season: 2, Number: 2, Path: shared.Path, ReSearch: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Reset) != 2 || !res.Blocklisted {
		t.Errorf("result: %+v", res)
	}
	files, _ := s.TrackedFiles(ctx, "series-2")
	if len(files) != 1 || files[0].Path != "House/Season 01/House - S01E01.mkv" {
		t.Errorf("only the other episode keeps its file: %+v", files)
	}
	title, _ := s.Get(ctx, "series-2")
	for _, e := range title.Seasons[1].Episodes {
		if e.State != "wanted" && e.State != "missing" {
			t.Errorf("S02E%02d is %q, want missing or wanted", e.Number, e.State)
		}
		if !e.Monitored {
			t.Errorf("S02E%02d must be monitored again", e.Number)
		}
	}
	blocked, _ := s.Blocklist(ctx)
	if _, ok := blocked[chooser.IdentityOf("hh", "pack.mkv", 99)]; !ok || len(blocked) != 1 {
		t.Errorf("the release that made the file is blocklisted, and only it: %v", blocked)
	}
	acts, _ := s.Activities(ctx, "series-2")
	if len(acts) != 1 || acts[0].Kind != catalog.ActivityWrongFile || acts[0].Season != 2 || acts[0].Number != 2 || !strings.Contains(acts[0].Message, "S02E01") {
		t.Errorf("activity: %+v", acts)
	}
	due, _ := s.Targets(ctx, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	forced := 0
	for _, tg := range due {
		if tg.Forced && tg.Season == 2 {
			forced++
		}
	}
	if forced != 2 {
		t.Errorf("both episodes are marked for an immediate search: %+v", due)
	}
}

func TestWrongFileWithoutReSearchLeavesTheScheduleAlone(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	movie := addMovie(t, s, 1)
	doneDownload(t, s, "m", movie, 0, 0, "hm", "m.mkv", 3, "/lib/movies/Film/Film.mkv", catalog.File{Path: "Film/Film.mkv", Size: 3})
	res, err := s.ResetWrongFile(ctx, catalog.WrongFile{TitleID: movie, Path: "Film/Film.mkv"})
	if err != nil || !res.Blocklisted {
		t.Fatalf("%+v, %v", res, err)
	}
	due, _ := s.Targets(ctx, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	for _, tg := range due {
		if tg.Forced {
			t.Errorf("nothing is forced when the owner picks the release: %+v", tg)
		}
	}
	got, _ := s.Get(ctx, movie)
	if !got.Monitored || got.Badge == nil || got.Badge.State == "downloaded" {
		t.Errorf("the movie is missing and monitored again: %+v", got)
	}
}

func TestWrongFileOfAnAdoptedFileBlocklistsNothing(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	movie := addMovie(t, s, 1)
	if _, err := s.ApplyScan(ctx, movie, []catalog.FileFound{{File: catalog.File{Path: "Film/Film.mkv", Size: 3}}}, nil); err != nil {
		t.Fatal(err)
	}
	res, err := s.ResetWrongFile(ctx, catalog.WrongFile{TitleID: movie, Path: "Film/Film.mkv", ReSearch: true})
	if err != nil || res.Blocklisted {
		t.Fatalf("%+v, %v", res, err)
	}
	if blocked, _ := s.Blocklist(ctx); len(blocked) != 0 {
		t.Errorf("blocklist: %v", blocked)
	}
}

func TestWrongFileBlocklistsTheDownloadThatMadeThisFileNotAnOlderOne(t *testing.T) {
	ctx := context.Background()
	clk := &clock{time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	s, err := catalog.Open(t.TempDir(), clk.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	movie := addMovie(t, s, 1)
	doneDownload(t, s, "old", movie, 0, 0, "hold", "old.mkv", 1, "/lib/movies/Film/Film [old].mkv", catalog.File{Path: "Film/Film [old].mkv"})
	clk.t = clk.t.Add(time.Hour)
	doneDownload(t, s, "new", movie, 0, 0, "hnew", "new.mkv", 2, "/lib/movies/Film/Film [new].mkv", catalog.File{Path: "Film/Film [new].mkv"})
	if _, err := s.ResetWrongFile(ctx, catalog.WrongFile{TitleID: movie, Path: "Film/Film [new].mkv"}); err != nil {
		t.Fatal(err)
	}
	blocked, _ := s.Blocklist(ctx)
	if _, ok := blocked[chooser.IdentityOf("hnew", "new.mkv", 2)]; !ok || len(blocked) != 1 {
		t.Errorf("blocklist: %v", blocked)
	}
}

func TestWrongFileRefusesAFileThatChangedMeanwhileAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	movie := addMovie(t, s, 1)
	doneDownload(t, s, "m", movie, 0, 0, "hm", "m.mkv", 3, "/lib/movies/Film/Film.mkv", catalog.File{Path: "Film/Film.mkv", Size: 3})
	_, err := s.ResetWrongFile(ctx, catalog.WrongFile{TitleID: movie, Path: "Film/Other.mkv", ReSearch: true})
	if !errors.Is(err, catalog.ErrFileChanged) {
		t.Fatalf("err = %v", err)
	}
	if files, _ := s.TrackedFiles(ctx, movie); len(files) != 1 {
		t.Errorf("tracked: %+v", files)
	}
	if blocked, _ := s.Blocklist(ctx); len(blocked) != 0 {
		t.Errorf("blocklist: %v", blocked)
	}
	if acts, _ := s.Activities(ctx, movie); len(acts) != 0 {
		t.Errorf("activity: %+v", acts)
	}
	if _, err := s.ResetWrongFile(ctx, catalog.WrongFile{TitleID: "movie-9", Path: "x.mkv"}); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("an unknown title: %v", err)
	}
}
