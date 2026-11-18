package app_test

import (
	"bytes"
	"context"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/download"
	"github.com/davepintodev/marquee/internal/jellyfinfake"
	"github.com/davepintodev/marquee/internal/pipeline"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

// rig is a running marquee with a debrid file server and a Jellyfin around it.
type rig struct {
	*realSvc
	t    *testing.T
	cdn  *debridfake.Server
	jf   *jellyfinfake.Server
	mgr  *pipeline.Manager
	stop func()
}

func fastDownloads() download.Options {
	return download.Options{
		Connections: 4, StallTimeout: 400 * time.Millisecond,
		RateLimitWait: 10 * time.Millisecond, MaxRateLimitWait: 50 * time.Millisecond,
		GrowEvery: 100 * time.Millisecond, ConnStagger: time.Nanosecond, SaveEvery: 20 * time.Millisecond,
		ProgressEvery: 20 * time.Millisecond, RetryUnit: time.Millisecond, MaxRetries: 3, MinSteal: 16 << 10,
	}
}

func newRig(t *testing.T, env map[string]string, cdn *debridfake.Server, jf *jellyfinfake.Server, tm *tmdbfake.Fake) *rig {
	t.Helper()
	r := &rig{t: t, cdn: cdn, jf: jf}
	r.realSvc = &realSvc{t: t, env: env, logs: newSyncBuf(), tmdb: tm}
	r.startService()
	return r
}

// startService starts marquee on r's environment; calling it again after
// stopService is a restart on the same directories.
func (r *rig) startService() {
	r.t.Helper()
	opts := app.Options{
		Now:           func() time.Time { return today },
		TMDBAPIBase:   r.tmdb.APIBase(),
		TMDBImageBase: r.tmdb.ImageBase(),
		Transfers: pipeline.Tuning{
			MovieFloor: 1 << 10, EpisodeFloor: 1 << 10, Poll: 20 * time.Millisecond,
			JellyfinTimeout: time.Second, Download: fastDownloads(),
		},
		OnPipeline: func(m *pipeline.Manager) { r.mgr = m },
	}
	ctx, cancel := context.WithCancel(context.Background())
	addrc := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- app.RunWith(ctx, opts, func(k string) string { return r.env[k] }, r.logs, fstest.MapFS{}, func(a net.Addr) { addrc <- a })
	}()
	stopped := false
	r.stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				r.t.Errorf("Run returned %v after shutdown", err)
			}
		case <-time.After(10 * time.Second):
			r.t.Error("service did not shut down")
		}
	}
	r.t.Cleanup(r.stop)
	select {
	case a := <-addrc:
		r.base = "http://" + a.String()
	case err := <-done:
		r.t.Fatalf("service did not start: %v", err)
	case <-time.After(5 * time.Second):
		r.t.Fatal("service did not start")
	}
}

func setup(t *testing.T) (*rig, string) {
	t.Helper()
	cdn := debridfake.New()
	t.Cleanup(cdn.Close)
	jf := jellyfinfake.New()
	t.Cleanup(jf.Close)
	env := testEnv(t)
	env["MARQUEE_TMDB_KEY"] = tmdbfake.Key
	env["MARQUEE_JELLYFIN_URL"] = jf.URL
	env["MARQUEE_JELLYFIN_KEY"] = jellyfinfake.Key
	r := newRig(t, env, cdn, jf, newTMDB(t))
	r.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	r.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`, 201)
	return r, filepath.Dir(env["MARQUEE_MOVIES_PATH"])
}

func movieJob(url, name string) pipeline.Job {
	return pipeline.Job{
		TitleID: "movie-438631", URL: config.Secret(url),
		Release:     catalog.ReleaseID{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FileName: name, Size: 0},
		ReleaseName: "Dune.2021.1080p.WEB-DL", Resolution: "1080p", Quality: "WEB-DL",
	}
}

func episodeJob(url, name string) pipeline.Job {
	j := movieJob(url, name)
	j.TitleID, j.Season, j.Number = "series-1408", 1, 1
	j.ReleaseName = "Dune.Prophecy.S01E01.1080p.WEB-DL"
	return j
}

func (r *rig) enqueue(j pipeline.Job) catalog.Download {
	r.t.Helper()
	d, err := r.mgr.Enqueue(context.Background(), j)
	if err != nil {
		r.t.Fatalf("enqueue: %v", err)
	}
	return d
}

func waitFor(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *rig) history() []any {
	r.t.Helper()
	return r.mustCall("GET", "/api/history", "", 200)["history"].([]any)
}

func (r *rig) downloads() []any {
	r.t.Helper()
	return r.mustCall("GET", "/api/downloads", "", 200)["downloads"].([]any)
}

func (r *rig) waitHistory(n int) []any {
	r.t.Helper()
	var h []any
	waitFor(r.t, "history to have an entry", 10*time.Second, func() bool { h = r.history(); return len(h) >= n })
	return h
}

func tree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func noSecrets(t *testing.T, where, text string) {
	t.Helper()
	for _, s := range []string{"SECRETPLAY", "SECRETCDN", jellyfinfake.Key} {
		if strings.Contains(text, s) {
			t.Errorf("%s leaks %q", where, s)
		}
	}
}

func (r *rig) checkNoSecretsAnywhere() {
	r.t.Helper()
	noSecrets(r.t, "the logs", r.logs.String())
	for _, p := range []string{"/api/downloads", "/api/history", "/api/health", "/api/titles/movie-438631", "/api/titles/series-1408"} {
		resp := r.call("GET", p, "")
		noSecrets(r.t, p, string(resp.body))
	}
}

func TestADownloadedMovieLandsUnderRadarrsNameAndJellyfinIsToldOnce(t *testing.T) {
	r, root := setup(t)
	data := debridfake.MKV(300 << 10)
	r.enqueue(movieJob(r.cdn.Add("Dune.2021.1080p.WEB-DL.mkv", debridfake.File{Data: data}), "Dune.2021.1080p.WEB-DL.mkv"))

	h := r.waitHistory(1)
	entry := h[0].(map[string]any)
	if entry["kind"] != "imported" || entry["titleId"] != "movie-438631" {
		t.Fatalf("history entry = %v", entry)
	}
	want := "Dune (2021)/Dune (2021) [WEBDL-1080p].mkv"
	if got := tree(t, filepath.Join(root, "movies")); len(got) != 1 || got[0] != want {
		t.Fatalf("movies folder holds %v, want [%s]", got, want)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "movies", want)); !bytes.Equal(got, data) {
		t.Error("the imported file differs from the served one")
	}
	if left := tree(t, filepath.Join(root, "tmp")); len(left) != 0 {
		t.Errorf("temp folder still holds %v", left)
	}
	waitFor(t, "the Jellyfin refresh", 5*time.Second, func() bool { return r.jf.Refreshes() >= 1 })
	time.Sleep(100 * time.Millisecond)
	if n := r.jf.Refreshes(); n != 1 {
		t.Errorf("Jellyfin got %d refreshes, want 1", n)
	}
	if d := r.downloads(); len(d) != 0 {
		t.Errorf("the queue still holds %v after the import", d)
	}
	if got := r.mustCall("GET", "/api/titles/movie-438631", "", 200)["badge"].(map[string]any)["state"]; got != "downloaded" {
		t.Errorf("title badge = %v, want downloaded the moment the file is imported", got)
	}
	r.checkNoSecretsAnywhere()
}

func TestADownloadedEpisodeLandsInItsSeasonFolderUnderSonarrsName(t *testing.T) {
	r, root := setup(t)
	r.enqueue(episodeJob(r.cdn.Add("ep.mkv", debridfake.File{Data: debridfake.MKV(200 << 10)}), "ep.mkv"))
	r.waitHistory(1)
	want := "Dune - Prophecy/Season 01/Dune - Prophecy - S01E01 - Aired One [WEBDL-1080p].mkv"
	if got := tree(t, filepath.Join(root, "series")); len(got) != 1 || got[0] != want {
		t.Fatalf("series folder holds %v, want [%s]", got, want)
	}
	title := r.mustCall("GET", "/api/titles/series-1408", "", 200)
	for _, s := range title["seasons"].([]any) {
		for _, e := range s.(map[string]any)["episodes"].([]any) {
			ep := e.(map[string]any)
			if ep["id"] == "s01e01" && ep["state"] != "downloaded" {
				t.Errorf("S01E01 is %v after its import, want downloaded", ep["state"])
			}
			if ep["id"] == "s01e02" && ep["state"] == "downloaded" {
				t.Error("S01E02 switched to downloaded by S01E01's import")
			}
		}
	}
	waitFor(t, "the Jellyfin refresh", 5*time.Second, func() bool { return r.jf.Refreshes() == 1 })
}

func TestRemuxQualityNamesDifferBetweenMoviesAndSeries(t *testing.T) {
	r, root := setup(t)
	m := movieJob(r.cdn.Add("m.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "m.mkv")
	m.Resolution, m.Quality = "2160p", "BluRay REMUX"
	e := episodeJob(r.cdn.Add("e.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "e.mkv")
	e.Resolution, e.Quality = "1080p", "BluRay REMUX"
	r.enqueue(m)
	r.enqueue(e)
	r.waitHistory(2)
	if got := tree(t, filepath.Join(root, "movies")); len(got) != 1 || !strings.HasSuffix(got[0], "[Remux-2160p].mkv") {
		t.Errorf("movie: %v", got)
	}
	if got := tree(t, filepath.Join(root, "series")); len(got) != 1 || !strings.HasSuffix(got[0], "[Bluray-1080p Remux].mkv") {
		t.Errorf("episode: %v", got)
	}
}

func TestBadFilesFailWithAReasonAreBlocklistedAndNeverReachTheLibrary(t *testing.T) {
	notVideo := append([]byte("MZ\x90\x00"), make([]byte, 100<<10)...)
	for _, tc := range []struct {
		name   string
		file   debridfake.File
		size   int64 // the size the release was listed with
		reason string
	}{
		{"stalls", debridfake.File{Data: debridfake.MKV(400 << 10), StallAt: 100 << 10}, 0, "stalled"},
		{"truncates", debridfake.File{Data: debridfake.MKV(400 << 10), TruncateAt: 150 << 10}, 0, "size_mismatch"},
		{"is not a video", debridfake.File{Data: notVideo}, 0, "not_video"},
		{"is not the size listed", debridfake.File{Data: debridfake.MKV(100 << 10)}, 5 << 20, "size_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, root := setup(t)
			const hash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			job := movieJob(r.cdn.Add("bad.mkv", tc.file), "bad.mkv")
			job.Release = catalog.ReleaseID{InfoHash: hash, FileName: "bad.mkv", Size: tc.size}
			r.enqueue(job)

			entry := r.waitHistory(1)[0].(map[string]any)
			if entry["kind"] != "failed" || entry["reason"] != tc.reason || entry["message"] == "" {
				t.Fatalf("history entry = %v, want a failure with reason %s and a message", entry, tc.reason)
			}
			if got := tree(t, filepath.Join(root, "movies")); len(got) != 0 {
				t.Errorf("the library holds %v", got)
			}
			if got := tree(t, filepath.Join(root, "tmp")); len(got) != 0 {
				t.Errorf("temp folder still holds %v", got)
			}
			if n := r.jf.Calls(); n != 0 {
				t.Errorf("Jellyfin was called %d times for a failed download", n)
			}
			if d := r.downloads(); len(d) != 0 {
				t.Errorf("a failed download is still queued: %v", d)
			}
			if got := r.mustCall("GET", "/api/titles/movie-438631", "", 200)["badge"].(map[string]any)["state"]; got == "downloaded" {
				t.Error("the title shows Downloaded after a failed download")
			}

			// The blocklist is keyed by infohash + file name + size, never by link.
			store, err := catalog.Open(r.env["MARQUEE_DATA_DIR"], nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			for _, k := range []struct {
				id   catalog.ReleaseID
				want bool
			}{
				{catalog.ReleaseID{InfoHash: hash, FileName: "bad.mkv", Size: tc.size}, true},
				{catalog.ReleaseID{InfoHash: hash, FileName: "bad.mkv", Size: tc.size + 1}, false},
				{catalog.ReleaseID{InfoHash: hash, FileName: "other.mkv", Size: tc.size}, false},
				{catalog.ReleaseID{InfoHash: "cccccccccccccccccccccccccccccccccccccccc", FileName: "bad.mkv", Size: tc.size}, false},
			} {
				got, err := store.Blocklisted(context.Background(), k.id)
				if err != nil || got != k.want {
					t.Errorf("Blocklisted(%+v) = %v, %v; want %v", k.id, got, err, k.want)
				}
			}
			r.checkNoSecretsAnywhere()
		})
	}
}

func TestABlocklistEntryExpiresAfterThirtyDays(t *testing.T) {
	r, _ := setup(t)
	r.enqueue(movieJob(r.cdn.Add("bad.mkv", debridfake.File{Data: append([]byte("PK\x03\x04"), make([]byte, 5<<10)...)}), "bad.mkv"))
	r.waitHistory(1)
	now := today
	store, err := catalog.Open(r.env["MARQUEE_DATA_DIR"], func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	id := catalog.ReleaseID{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FileName: "bad.mkv"}
	if ok, _ := store.Blocklisted(context.Background(), id); !ok {
		t.Fatal("a fresh failure is not blocklisted")
	}
	now = today.Add(catalog.BlocklistTTL + time.Hour)
	if ok, _ := store.Blocklisted(context.Background(), id); ok {
		t.Error("the entry outlived its thirty days")
	}
}

func TestSamplesUnderTheSizeFloorAreRefusedEvenWhenTheyAreVideo(t *testing.T) {
	r, root := setup(t)
	r.enqueue(movieJob(r.cdn.Add("sample.mkv", debridfake.File{Data: debridfake.MKV(500)}), "sample.mkv"))
	entry := r.waitHistory(1)[0].(map[string]any)
	if entry["reason"] != "too_small" {
		t.Fatalf("history entry = %v, want too_small", entry)
	}
	if got := tree(t, filepath.Join(root, "movies")); len(got) != 0 {
		t.Errorf("the library holds %v", got)
	}
}

func TestADownloadInterruptedByARestartResumesFromItsSavedProgress(t *testing.T) {
	r, root := setup(t)
	data := debridfake.MKV(3 << 20)
	url := r.cdn.Add("big.mkv", debridfake.File{Data: data, Pace: 8 * time.Millisecond})
	r.enqueue(movieJob(url, "big.mkv"))

	waitFor(t, "the download to be well under way", 10*time.Second, func() bool {
		d := r.downloads()
		return len(d) == 1 && d[0].(map[string]any)["percent"].(float64) >= 15
	})
	r.stop()

	part := tree(t, filepath.Join(root, "tmp"))
	if len(part) == 0 {
		t.Fatal("a restart left no partial download in the temp folder to resume")
	}
	if got := tree(t, filepath.Join(root, "movies")); len(got) != 0 {
		t.Fatalf("a half-written file reached the library: %v", got)
	}
	seen := len(r.cdn.Requests())

	r.startService()
	if d := r.downloads(); len(d) != 1 {
		t.Fatalf("after the restart the queue holds %v, want the interrupted download", d)
	}
	if h := r.history(); len(h) != 0 {
		t.Fatalf("an interrupted download must not be recorded as a failure: %v", h)
	}
	entry := r.waitHistory(1)[0].(map[string]any)
	if entry["kind"] != "imported" {
		t.Fatalf("history entry = %v", entry)
	}
	low := int64(-1)
	for _, q := range r.cdn.Requests()[seen:] {
		if !q.Probe && !q.Playback && q.RangeStart >= 0 && (low < 0 || q.RangeStart < low) {
			low = q.RangeStart
		}
	}
	if low <= 0 {
		t.Errorf("after the restart the fake saw a lowest range start of %d; it should resume past the start", low)
	}
	got, _ := os.ReadFile(filepath.Join(root, "movies", "Dune (2021)", "Dune (2021) [WEBDL-1080p].mkv"))
	if !bytes.Equal(got, data) {
		t.Error("the resumed file differs from the served one")
	}
	if n := r.jf.Refreshes(); n != 1 {
		t.Errorf("Jellyfin got %d refreshes, want 1", n)
	}
	r.checkNoSecretsAnywhere()
}

func TestJellyfinBeingDownLeavesTheImportSuccessfulAndShowsOnHealth(t *testing.T) {
	r, root := setup(t)
	r.jf.FailWith(503)
	r.enqueue(movieJob(r.cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "a.mkv"))
	entry := r.waitHistory(1)[0].(map[string]any)
	if entry["kind"] != "imported" {
		t.Fatalf("history entry = %v, want the import to succeed with Jellyfin down", entry)
	}
	if got := tree(t, filepath.Join(root, "movies")); len(got) != 1 {
		t.Fatalf("library holds %v", got)
	}
	var health map[string]any
	waitFor(t, "Jellyfin on the Health page", 5*time.Second, func() bool {
		health = r.mustCall("GET", "/api/health", "", 200)
		return health["status"] != "ok"
	})
	checks := health["checks"].([]any)
	if len(checks) != 1 || checks[0].(map[string]any)["id"] != "jellyfin" || checks[0].(map[string]any)["status"] != "warning" {
		t.Fatalf("health checks = %v, want one Jellyfin warning", checks)
	}
	if msg := checks[0].(map[string]any)["message"].(string); !strings.Contains(msg, "503") {
		t.Errorf("health message %q does not say what Jellyfin answered", msg)
	}

	// Jellyfin comes back; the next import clears the warning.
	r.jf.FailWith(0)
	r.enqueue(episodeJob(r.cdn.Add("b.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "b.mkv"))
	r.waitHistory(2)
	waitFor(t, "the Health warning to clear", 5*time.Second, func() bool {
		return r.mustCall("GET", "/api/health", "", 200)["status"] == "ok"
	})
	r.checkNoSecretsAnywhere()
}

func TestWithoutAJellyfinURLNothingIsRefreshedAndNothingIsReported(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	env := testEnv(t)
	env["MARQUEE_TMDB_KEY"] = tmdbfake.Key
	r := newRig(t, env, cdn, nil, newTMDB(t))
	r.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	r.enqueue(movieJob(cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "a.mkv"))
	r.waitHistory(1)
	if got := r.mustCall("GET", "/api/health", "", 200); got["status"] != "ok" {
		t.Errorf("health = %v", got)
	}
}

func TestAtMostTwoDownloadsRunAtOnce(t *testing.T) {
	r, _ := setup(t)
	file := func(name string) string {
		return r.cdn.Add(name, debridfake.File{Data: debridfake.MKV(400 << 10), Pace: 15 * time.Millisecond})
	}
	second := episodeJob(file("b.mkv"), "b.mkv")
	third := episodeJob(file("c.mkv"), "c.mkv")
	third.Number = 2
	r.enqueue(movieJob(file("a.mkv"), "a.mkv"))
	r.enqueue(second)
	r.enqueue(third)
	r.waitHistory(3)
	if n := r.cdn.MaxFiles(); n != 2 {
		t.Errorf("the fake saw %d files streaming at once, want exactly 2", n)
	}
}

func TestQueueingATitleThatIsAlreadyDownloadingChangesNothing(t *testing.T) {
	r, _ := setup(t)
	url := r.cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(300 << 10), Pace: 20 * time.Millisecond})
	first := r.enqueue(movieJob(url, "a.mkv"))
	second, err := r.mgr.Enqueue(context.Background(), movieJob(url, "a.mkv"))
	if err == nil || second.ID != first.ID {
		t.Fatalf("second enqueue = %v, %v; want the first download back with an error", second.ID, err)
	}
	if got := len(r.downloads()); got != 1 {
		t.Errorf("queue holds %d downloads, want 1", got)
	}
	r.waitHistory(1)
	if len(r.history()) != 1 {
		t.Error("the duplicate produced a second history entry")
	}
}

func TestADownloadForATitleThatIsNotInTheLibraryIsRefused(t *testing.T) {
	r, _ := setup(t)
	j := movieJob("http://x.invalid/a", "a.mkv")
	j.TitleID = "movie-1"
	if _, err := r.mgr.Enqueue(context.Background(), j); err == nil {
		t.Error("a download for an unknown title was accepted")
	}
	e := episodeJob("http://x.invalid/a", "a.mkv")
	e.Season, e.Number = 9, 9
	if _, err := r.mgr.Enqueue(context.Background(), e); err == nil {
		t.Error("a download for an unknown episode was accepted")
	}
}

func TestRemovingATitleMidDownloadStopsItAndLeavesNoTempFiles(t *testing.T) {
	r, root := setup(t)
	url := r.cdn.Add("big.mkv", debridfake.File{Data: debridfake.MKV(3 << 20), Pace: 10 * time.Millisecond})
	r.enqueue(movieJob(url, "big.mkv"))
	waitFor(t, "the download to start", 5*time.Second, func() bool { return r.cdn.Streaming() > 0 })
	r.mustCall("DELETE", "/api/titles/movie-438631", "", 204)
	waitFor(t, "the temp files to go", 10*time.Second, func() bool { return len(tree(t, filepath.Join(root, "tmp"))) == 0 && r.cdn.Streaming() == 0 })
	if got := tree(t, filepath.Join(root, "movies")); len(got) != 0 {
		t.Errorf("a removed title's download reached the library: %v", got)
	}
}

func TestAFileAlreadyInTheLibraryIsNeverReplaced(t *testing.T) {
	r, root := setup(t)
	dir := filepath.Join(root, "movies", "Dune (2021)")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "Dune (2021) [WEBDL-1080p].mkv")
	if err := os.WriteFile(existing, []byte("the owner's own copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.enqueue(movieJob(r.cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "a.mkv"))
	entry := r.waitHistory(1)[0].(map[string]any)
	if entry["kind"] != "failed" || entry["reason"] != "local_error" {
		t.Fatalf("history entry = %v, want a local_error failure", entry)
	}
	if b, _ := os.ReadFile(existing); string(b) != "the owner's own copy" {
		t.Errorf("the existing file now holds %q", b)
	}
	if got := tree(t, filepath.Join(root, "tmp")); len(got) != 0 {
		t.Errorf("temp folder holds %v", got)
	}
	store, err := catalog.Open(r.env["MARQUEE_DATA_DIR"], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if ok, _ := store.Blocklisted(context.Background(), catalog.ReleaseID{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FileName: "a.mkv"}); ok {
		t.Error("a release was blocklisted for a problem on marquee's side")
	}
}

func TestDryRunNeverDownloadsWritesOrCallsJellyfin(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	jf := jellyfinfake.New()
	defer jf.Close()
	env := testEnv(t)
	env["MARQUEE_TMDB_KEY"] = tmdbfake.Key
	env["MARQUEE_JELLYFIN_URL"], env["MARQUEE_JELLYFIN_KEY"] = jf.URL, jellyfinfake.Key
	env["MARQUEE_DRY_RUN"] = "true"
	r := newRig(t, env, cdn, jf, newTMDB(t))
	r.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	r.enqueue(movieJob(cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "a.mkv"))
	time.Sleep(300 * time.Millisecond)
	if n := len(cdn.Requests()); n != 0 {
		t.Errorf("dry-run made %d requests to the file server", n)
	}
	for _, p := range []string{"MARQUEE_MOVIES_PATH", "MARQUEE_SERIES_PATH", "MARQUEE_TEMP_PATH"} {
		if got := tree(t, env[p]); len(got) != 0 {
			t.Errorf("dry-run wrote %v under %s", got, p)
		}
	}
	if jf.Calls() != 0 {
		t.Error("dry-run called Jellyfin")
	}
}

func TestTheDownloadQueueShowsProgressAndStateUntilTheImportCompletes(t *testing.T) {
	r, _ := setup(t)
	r.enqueue(movieJob(r.cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(1 << 20), Pace: 10 * time.Millisecond}), "a.mkv"))
	var seen map[string]any
	waitFor(t, "a download in progress", 5*time.Second, func() bool {
		d := r.downloads()
		if len(d) == 1 && d[0].(map[string]any)["percent"].(float64) > 0 {
			seen = d[0].(map[string]any)
			return true
		}
		return false
	})
	if seen["state"] != "downloading" || seen["titleId"] != "movie-438631" || seen["titleName"] != "Dune" ||
		seen["releaseName"] != "Dune.2021.1080p.WEB-DL" || seen["bytesTotal"].(float64) != 1<<20 || seen["stalled"] != false {
		t.Errorf("queue entry = %v", seen)
	}
	if _, has := seen["episode"]; has {
		t.Errorf("a movie's download names an episode: %v", seen)
	}
	r.waitHistory(1)
}

func TestHistoryCanBeNarrowedToATitleAndNamesTheEpisode(t *testing.T) {
	r, _ := setup(t)
	r.enqueue(movieJob(r.cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(50 << 10)}), "a.mkv"))
	r.enqueue(episodeJob(r.cdn.Add("b.mkv", debridfake.File{Data: debridfake.MKV(50 << 10)}), "b.mkv"))
	r.waitHistory(2)
	h := r.mustCall("GET", "/api/history?titleId=series-1408", "", 200)["history"].([]any)
	if len(h) != 1 || h[0].(map[string]any)["episode"] != "s01e01" {
		t.Errorf("history for the series = %v", h)
	}
	if r := r.bad("GET", "/api/history?titleId=nonsense", ""); r.status != 400 {
		t.Errorf("a malformed titleId answered %d, want 400", r.status)
	}
}

// seedInterrupted leaves a download in the database in state, as a crash
// would, with the service stopped.
func (r *rig) seedInterrupted(id, state string, total int64) {
	r.t.Helper()
	r.stop()
	store, err := catalog.Open(r.env["MARQUEE_DATA_DIR"], nil)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if _, err := store.AddDownload(ctx, catalog.NewDownload{
		ID: id, TitleID: "movie-438631", Resolution: "1080p", Quality: "WEB-DL", ReleaseName: "Dune",
		Release: catalog.ReleaseID{InfoHash: "dddddddddddddddddddddddddddddddddddddddd", FileName: "a.mkv"}, URL: "http://x.invalid/gone",
	}); err != nil {
		r.t.Fatal(err)
	}
	if err := store.SetDownloadProgress(ctx, id, total, total); err != nil {
		r.t.Fatal(err)
	}
	if err := store.SetDownloadState(ctx, id, state); err != nil {
		r.t.Fatal(err)
	}
}

func TestADownloadThatCrashedWhileVerifyingIsVerifiedAndImportedNotFetchedAgain(t *testing.T) {
	r, root := setup(t)
	data := debridfake.MKV(80 << 10)
	r.seedInterrupted("crash1", "verifying", int64(len(data)))
	if err := os.MkdirAll(filepath.Join(root, "tmp", "marquee"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tmp", "marquee", "crash1.part"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	r.startService()
	r.waitHistory(1)
	if got, _ := os.ReadFile(filepath.Join(root, "movies", "Dune (2021)", "Dune (2021) [WEBDL-1080p].mkv")); !bytes.Equal(got, data) {
		t.Error("the finished file was not imported as it was")
	}
	if n := len(r.cdn.Requests()); n != 0 {
		t.Errorf("a finished download was fetched again (%d requests)", n)
	}
}

func TestAnImportThatCrashedAfterTheMoveIsCompletedWithoutMovingAnything(t *testing.T) {
	r, root := setup(t)
	data := debridfake.MKV(80 << 10)
	r.seedInterrupted("crash2", "importing", int64(len(data)))
	dir := filepath.Join(root, "movies", "Dune (2021)")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dune (2021) [WEBDL-1080p].mkv"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	r.startService()
	entry := r.waitHistory(1)[0].(map[string]any)
	if entry["kind"] != "imported" {
		t.Fatalf("history entry = %v, want the import completed", entry)
	}
	if got := r.mustCall("GET", "/api/titles/movie-438631", "", 200)["badge"].(map[string]any)["state"]; got != "downloaded" {
		t.Errorf("badge = %v", got)
	}
}

func TestADownloadWhoseTempFileVanishedFailsWithoutBlocklistingTheRelease(t *testing.T) {
	r, _ := setup(t)
	r.seedInterrupted("crash3", "verifying", 1<<20)
	r.startService()
	entry := r.waitHistory(1)[0].(map[string]any)
	if entry["kind"] != "failed" || entry["reason"] != "local_error" {
		t.Fatalf("history entry = %v, want a local_error failure", entry)
	}
	store, err := catalog.Open(r.env["MARQUEE_DATA_DIR"], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if ok, _ := store.Blocklisted(context.Background(), catalog.ReleaseID{InfoHash: "dddddddddddddddddddddddddddddddddddddddd", FileName: "a.mkv"}); ok {
		t.Error("a release was blocklisted for a problem on marquee's side")
	}
}

func TestOnlyMarqueesOwnTempFolderIsPrunedAtStartup(t *testing.T) {
	r, root := setup(t)
	r.stop()
	own := filepath.Join(root, "tmp", "marquee")
	for p, body := range map[string]string{
		filepath.Join(own, "orphan.part"):             "x",
		filepath.Join(own, "orphan.part.plan"):        "x",
		filepath.Join(root, "tmp", "keep.txt"):        "not ours",
		filepath.Join(root, "tmp", "other", "a.part"): "not ours",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.startService()
	waitFor(t, "orphans to be pruned", 5*time.Second, func() bool { return len(tree(t, own)) == 0 })
	got := tree(t, filepath.Join(root, "tmp"))
	if len(got) != 2 || got[0] != "keep.txt" || got[1] != "other/a.part" {
		t.Errorf("temp path holds %v, want only the files marquee did not create", got)
	}
}

func TestATempFolderThatCannotBeUsedFailsTheDownloadAsALocalErrorAndKeepsTheServiceUp(t *testing.T) {
	r, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "tmp", "marquee"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.enqueue(movieJob(r.cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(10 << 10)}), "a.mkv"))
	entry := r.waitHistory(1)[0].(map[string]any)
	if entry["kind"] != "failed" || entry["reason"] != "local_error" {
		t.Fatalf("history entry = %v", entry)
	}
	if n := len(r.cdn.Requests()); n != 0 {
		t.Errorf("the fake saw %d requests although there was nowhere to write", n)
	}
	r.mustCall("GET", "/api/health", "", 200)
}
