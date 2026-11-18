package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreamsfake"
	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/jellyfinfake"
	"github.com/davepintodev/marquee/internal/naming"
	"github.com/davepintodev/marquee/internal/pipeline"
	"github.com/davepintodev/marquee/internal/scheduler"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

const (
	movieBody  = `{"kind":"movie","tmdbId":438631}`
	seriesBody = `{"kind":"series","tmdbId":1408}`
	duneID     = "tt1160419"
)

// schedRig is marquee with the scheduler stepped by hand: the test owns the
// clock and calls Step, so every search happens exactly when the test says.
type schedRig struct {
	*realSvc
	aio  *aiostreamsfake.Fake
	cdn  *debridfake.Server
	jf   *jellyfinfake.Server
	sch  *scheduler.Scheduler
	root string
	stop func()
}

func newSchedRig(t *testing.T, dryRun bool) *schedRig {
	t.Helper()
	r := &schedRig{aio: newAIO(t), cdn: debridfake.New(), jf: jellyfinfake.New()}
	t.Cleanup(r.cdn.Close)
	t.Cleanup(r.jf.Close)
	env := testEnv(t)
	env["MARQUEE_AIOSTREAMS_MANIFEST_URL"] = r.aio.ManifestURL()
	env["MARQUEE_TMDB_KEY"] = tmdbfake.Key
	env["MARQUEE_JELLYFIN_URL"] = r.jf.URL
	env["MARQUEE_JELLYFIN_KEY"] = jellyfinfake.Key
	if dryRun {
		env["MARQUEE_DRY_RUN"] = "true"
	}
	r.root = filepath.Dir(env["MARQUEE_MOVIES_PATH"])
	r.realSvc = &realSvc{t: t, env: env, logs: newSyncBuf(), tmdb: newTMDB(t)}
	r.now.Store(today.UnixNano())
	r.start()
	return r
}

// start runs marquee on the rig's directories; after stopService it is a restart.
func (r *schedRig) start() {
	r.t.Helper()
	opts := app.Options{
		Now:               func() time.Time { return time.Unix(0, r.now.Load()).UTC() },
		TMDBAPIBase:       r.tmdb.APIBase(),
		TMDBImageBase:     r.tmdb.ImageBase(),
		TMDBTimeout:       2 * time.Second,
		AIOStreamsTimeout: 2 * time.Second,
		Sleep:             func(context.Context, time.Duration) error { return nil },
		Transfers: pipeline.Tuning{
			MovieFloor: 1 << 10, EpisodeFloor: 1 << 10, Poll: 20 * time.Millisecond,
			JellyfinTimeout: time.Second, Download: fastDownloads(),
		},
		Scheduler:   scheduler.Tuning{Tick: -1},
		OnScheduler: func(s *scheduler.Scheduler) { r.sch = s },
	}
	r.run(opts)
}

func (r *schedRig) run(opts app.Options) {
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

func (r *schedRig) step() {
	r.t.Helper()
	r.sch.Step(context.Background())
}

func (r *schedRig) advance(d time.Duration) { r.now.Add(int64(d)) }

func (r *schedRig) setClock(t time.Time) { r.now.Store(t.UnixNano()) }

func (r *schedRig) searched() int { return len(r.aio.Searches()) }

// searchedID counts the searches for one Stremio id.
func (r *schedRig) searchedID(id string) int {
	n := 0
	for _, s := range r.aio.Searches() {
		if s.ID == id {
			n++
		}
	}
	return n
}

func (r *schedRig) wantSearches(want int, when string) {
	r.t.Helper()
	if got := r.searched(); got != want {
		r.t.Fatalf("%s: %d searches, want %d (%v)", when, got, want, r.aio.Searches())
	}
}

// history returns the entries of the given kind, newest first.
func (r *schedRig) entries(kind string) []map[string]any {
	r.t.Helper()
	var out []map[string]any
	for _, e := range r.history() {
		if m := e.(map[string]any); m["kind"] == kind {
			out = append(out, m)
		}
	}
	return out
}

func (r *schedRig) addMovie() { r.mustCall("POST", "/api/titles", movieBody, 201) }

type listed struct {
	name       string
	size       int64
	url        string
	season, ep int
}

// results is an AIOStreams answer holding the given releases, all cached.
func results(rs ...listed) []byte {
	var list []map[string]any
	for i, l := range rs {
		parsed := map[string]any{"resolution": "1080p", "quality": "WEB-DL", "seasons": []int{}, "episodes": []int{}}
		if l.season > 0 {
			parsed["seasons"], parsed["episodes"] = []int{l.season}, []int{l.ep}
		}
		list = append(list, map[string]any{
			"url": l.url, "filename": l.name, "folderName": strings.TrimSuffix(l.name, ".mkv"), "size": l.size,
			"type": "debrid", "service": "torbox", "cached": true, "addon": "Torrentio",
			"infoHash": fmt.Sprintf("%040d", i+1), "parsedFile": parsed,
		})
	}
	b, err := json.Marshal(map[string]any{"success": true, "data": map[string]any{"results": list}})
	if err != nil {
		panic(err)
	}
	return b
}

func TestAMonitoredMovieWithNoCandidatesIsSearchedAt15mThen1hThen6hThenDailyForever(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	r.step()
	r.wantSearches(1, "when the movie is added")

	n := 1
	for _, gap := range []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour, 24 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		r.advance(gap - time.Minute)
		r.step()
		r.wantSearches(n, fmt.Sprintf("a minute before the %v search", gap))
		r.advance(time.Minute)
		r.step()
		n++
		r.wantSearches(n, fmt.Sprintf("at the %v search", gap))
	}
	if got := r.entries("searched"); len(got) != n {
		t.Errorf("Activity should say each search found nothing: %d entries for %d searches", len(got), n)
	}
}

func TestASourceErrorDoesNotAdvanceTheTitlesOwnBackoff(t *testing.T) {
	r := newSchedRig(t, false)
	r.aio.Fail(aiostreamsfake.Step{Status: 500})
	r.addMovie()
	r.step()
	r.wantSearches(1, "the first search, which fails")

	r.advance(30 * time.Second)
	r.step()
	r.wantSearches(1, "the source is given time to recover")
	r.advance(31 * time.Second)
	r.step()
	r.wantSearches(2, "the source backoff is over, and it fails again")

	r.aio.Heal()
	r.advance(5*time.Minute - time.Second)
	r.step()
	r.wantSearches(2, "the second source error backs off longer")
	r.advance(time.Second)
	r.step()
	r.wantSearches(3, "the source answers, with nothing")

	// Two failed searches must not have moved the title to its 1 h stage.
	r.advance(14 * time.Minute)
	r.step()
	r.wantSearches(3, "14 minutes after the first search that counted")
	r.advance(time.Minute)
	r.step()
	r.wantSearches(4, "15 minutes after it: the title's own first backoff step")

	if got := r.entries("failed"); len(got) != 1 || got[0]["reason"] != "source_down" || !strings.Contains(got[0]["message"].(string), "not answering") {
		t.Errorf("one outage is one entry in plain words: %v", got)
	}
	if got := len(r.entries("searched")); got != 2 {
		t.Errorf("only the two searches that found nothing are 'searched' entries: %d", got)
	}
}

func TestAFailedDownloadMovesToTheNextCandidate(t *testing.T) {
	r := newSchedRig(t, false)
	// Each file is far smaller than its listing says, so each fails at once.
	first := r.cdn.Add("first.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	second := r.cdn.Add("second.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(
		listed{name: "Dune.2021.1080p.first.mkv", size: 300 << 20, url: first},
		listed{name: "Dune.2021.720p.second.mkv", size: 300 << 20, url: second},
	))
	played := func() []string {
		var names []string
		for _, q := range r.cdn.Requests() {
			if q.Playback && q.Probe {
				names = append(names, strings.TrimPrefix(q.Path, "/play/SECRETPLAY-"))
			}
		}
		return names
	}

	r.addMovie()
	r.step()
	waitFor(t, "the first download to fail", 10*time.Second, func() bool { return len(r.entries("failed")) == 1 })
	if got := played(); len(got) != 1 || got[0] != "first.mkv" {
		t.Fatalf("the first candidate is tried first: %v", got)
	}

	r.advance(5 * time.Second)
	r.step()
	waitFor(t, "the second download to fail", 10*time.Second, func() bool { return len(r.entries("failed")) == 2 })
	if got := played(); len(got) != 2 || got[1] != "second.mkv" {
		t.Errorf("the blocklisted release is skipped and the next one tried: %v", got)
	}

	r.advance(5 * time.Second)
	r.step()
	r.wantSearches(3, "one search after each failure, no waiting for a backoff")
	if got := played(); len(got) != 2 {
		t.Errorf("nothing is left to try: %v", got)
	}
	if got := len(r.entries("searched")); got != 1 {
		t.Errorf("the search that found nothing left is on Activity: %d", got)
	}
	r.advance(15 * time.Minute)
	r.step()
	r.wantSearches(4, "with every release blocklisted the title falls back to its backoff, from its first step")
}

func TestALocalFailureIsNotRetriedAtOnce(t *testing.T) {
	r := newSchedRig(t, false)
	r.tmdb.Add(tmdbfake.Title{Kind: "series", ID: 3000, Name: "Solo", Date: "2020-01-01", IMDb: "tt3000000", TVDB: 3000,
		Seasons: []tmdbfake.Season{{Number: 1, Episodes: []tmdbfake.Episode{{Number: 1, Name: "Only", AirDate: "2020-01-01"}}}}})
	url := r.cdn.Add("solo.mkv", debridfake.File{Data: debridfake.MKV(53 << 20)})
	r.aio.Answer("series", "tvdb:3000:1:1", results(listed{name: "Solo.S01E01.1080p.WEB-DL.mkv", size: 53 << 20, url: url, season: 1, ep: 1}))
	taken := filepath.Join(r.env["MARQUEE_SERIES_PATH"], naming.SeriesFolder("Solo"), naming.SeasonFolder(1),
		naming.EpisodeFile("Solo", 1, 1, "Only", naming.QualityName(naming.KindSeries, "1080p", "WEB-DL"), ".mkv"))
	if err := os.MkdirAll(filepath.Dir(taken), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taken, []byte("already here"), 0o600); err != nil {
		t.Fatal(err)
	}

	r.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":3000}`, 201)
	r.step()
	waitFor(t, "the import to be refused", 20*time.Second, func() bool { return len(r.entries("failed")) == 1 })
	if got := r.entries("failed")[0]["reason"]; got != "local_error" {
		t.Fatalf("reason %v, want local_error", got)
	}

	// The same release would be chosen again, so waiting is the only sensible thing.
	r.advance(5 * time.Second)
	r.step()
	r.wantSearches(1, "marquee's own failure is not retried at once")
	r.advance(15 * time.Minute)
	r.step()
	r.wantSearches(2, "it is retried on the normal backoff")
	if b, _ := os.ReadFile(taken); string(b) != "already here" {
		t.Error("marquee touched a file it did not create")
	}
}

func TestAFoundReleaseIsDownloadedAndImportedWhenNotInDryRun(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("ep.mkv", debridfake.File{Data: debridfake.MKV(53 << 20)})
	r.aio.Answer("series", "tvdb:403000:1:1", results(listed{name: "Dune.Prophecy.S01E01.1080p.WEB-DL.mkv", size: 53 << 20, url: url, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	waitFor(t, "the import", 20*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	if got := tree(t, r.env["MARQUEE_SERIES_PATH"]); len(got) != 1 || !strings.Contains(got[0], "S01E01") {
		t.Errorf("library: %v", got)
	}

	// The episode is no longer wanted: the next search is for the other one.
	r.advance(5 * time.Second)
	r.step()
	if r.searchedID("tvdb:403000:1:1") != 1 || r.searchedID("tvdb:403000:1:2") != 1 {
		t.Errorf("an imported episode is not searched for again: %v", r.aio.Searches())
	}
}

func TestDryRunRecordsWouldGrabAndTouchesNothing(t *testing.T) {
	r := newSchedRig(t, true)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(
		listed{name: "Dune.2021.1080p.BluRay.mkv", size: 300 << 20, url: url},
		listed{name: "Dune.2021.720p.WEB-DL.mkv", size: 300 << 20, url: url},
	))
	r.addMovie()
	r.step()

	got := r.entries("would_grab")
	if len(got) != 1 || got[0]["message"] != "Would grab Dune.2021.1080p.BluRay.mkv" || got[0]["titleName"] != "Dune" {
		t.Fatalf("Activity should show what would be grabbed: %v", r.history())
	}
	if d := r.downloads(); len(d) != 0 {
		t.Errorf("dry-run queued %v", d)
	}
	// A day later the decision is taken again, and said once if it is the same.
	r.advance(24 * time.Hour)
	r.step()
	r.wantSearches(2, "the decision is re-checked daily, not at 15 minutes")
	if n := len(r.entries("would_grab")); n != 1 {
		t.Errorf("the same decision twice is one entry: %d", n)
	}
	r.advance(15 * time.Minute)
	r.step()
	r.wantSearches(2, "a decision is not repeated on the nothing-found backoff")

	// Last: a Health read searches AIOStreams once to see that it answers.
	if h := r.mustCall("GET", "/api/health", "", 200); h["dryRun"] != true {
		t.Errorf("health: %v", h)
	}

	for _, d := range []string{"movies", "series", "tmp"} {
		if f := tree(t, filepath.Join(r.root, d)); len(f) != 0 {
			t.Errorf("%s holds %v", d, f)
		}
	}
	if n := r.jf.Calls(); n != 0 {
		t.Errorf("Jellyfin was called %d times", n)
	}
	if reqs := r.cdn.Requests(); len(reqs) != 0 {
		t.Errorf("dry-run asked the debrid for the file: %v", reqs)
	}
	noSecrets(t, "the logs", r.logs.String())
	noSecrets(t, "history", fmt.Sprint(r.history()))
}

func TestSearchNowRunsAtOnceRegardlessOfBackoff(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	r.step()
	r.advance(time.Minute)
	r.step()
	r.wantSearches(1, "the title is backing off for 15 minutes")

	r.mustCall("POST", "/api/titles/movie-438631/search", "", 200)
	r.step()
	r.wantSearches(2, "Search now skips the backoff")

	r.advance(time.Minute)
	r.step()
	r.wantSearches(2, "and leaves the backoff where it was")
	r.advance(13 * time.Minute)
	r.step()
	r.wantSearches(3, "the scheduled search still comes at 15 minutes after the first")
}

func TestSearchNowOnAnEpisodeSearchesOnlyThatEpisode(t *testing.T) {
	r := newSchedRig(t, false)
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	for range 2 {
		r.step()
		r.advance(5 * time.Second)
	}
	r.wantSearches(2, "both aired episodes have been searched")
	r.mustCall("POST", "/api/titles/series-1408/episodes/s01e02/search", "", 200)
	r.step()
	if r.searchedID("tvdb:403000:1:2") != 2 || r.searchedID("tvdb:403000:1:1") != 1 {
		t.Errorf("only s01e02 is searched again: %v", r.aio.Searches())
	}
	if got := r.call("POST", "/api/titles/series-1408/episodes/s09e09/search", "").status; got != 404 {
		t.Errorf("an unknown episode: %d", got)
	}
	if got := r.call("POST", "/api/titles/movie-1/search", "").status; got != 404 {
		t.Errorf("an unknown title: %d", got)
	}
}

func TestSearchesAreSpacedByTheGlobalRateLimit(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	r.mustCall("POST", "/api/titles", seriesBody, 201) // two aired episodes: three searches wanted
	for range 5 {
		r.step()
	}
	r.wantSearches(1, "five passes at the same instant")
	r.advance(4 * time.Second)
	r.step()
	r.wantSearches(1, "4 seconds later")
	r.advance(time.Second)
	r.step()
	r.wantSearches(2, "5 seconds later")
	r.advance(5 * time.Second)
	r.step()
	r.wantSearches(3, "10 seconds later")
	r.advance(5 * time.Second)
	r.step()
	r.wantSearches(3, "everything has been searched; the backoffs are minutes long")

	// Search now is quick, not exempt from the limit.
	r.mustCall("POST", "/api/titles/movie-438631/search", "", 200)
	r.mustCall("POST", "/api/titles/series-1408/search", "", 200)
	r.step()
	r.wantSearches(4, "Search now runs at once when the gap is over")
	r.step()
	r.wantSearches(4, "and the next one at the same instant is held")
	r.advance(4 * time.Second)
	r.step()
	r.wantSearches(4, "4 seconds later")
	r.advance(time.Second)
	r.step()
	r.wantSearches(5, "5 seconds later")
	r.advance(5 * time.Second)
	r.step()
	r.wantSearches(6, "the last of the three, 5 seconds after that")
	r.advance(5 * time.Second)
	r.step()
	r.wantSearches(6, "and nothing more")
}

func TestATitleHasADailySearchBudgetThatSearchNowIgnores(t *testing.T) {
	r := newSchedRig(t, false)
	var eps []tmdbfake.Episode
	for n := 1; n <= 6; n++ {
		eps = append(eps, tmdbfake.Episode{Number: n, Name: fmt.Sprintf("E%d", n), AirDate: "2020-01-01"})
	}
	r.tmdb.Add(tmdbfake.Title{Kind: "series", ID: 4000, Name: "Long", Date: "2020-01-01", IMDb: "tt4000000", TVDB: 4000,
		Seasons: []tmdbfake.Season{{Number: 1, Episodes: eps}}})
	r.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":4000}`, 201)

	drain := func(passes int) {
		for range passes {
			r.step()
			r.advance(5 * time.Second)
		}
	}
	drain(10)
	r.wantSearches(4, "six episodes are wanted, the title's budget is four a day")

	r.mustCall("POST", "/api/titles/series-4000/search", "", 200)
	drain(10)
	if n := r.searched(); n != 10 {
		t.Errorf("Search now ignores the budget: %d searches, want 10 (4 + 6 forced)", n)
	}

	r.setClock(time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC))
	before := r.searched()
	drain(20)
	if got := r.searched() - before; got != 4 {
		t.Errorf("the next day brings a new budget of four, got %d", got)
	}
}

func TestAnEpisodeIsSearchedOnlyOnceItHasAired(t *testing.T) {
	r := newSchedRig(t, false)
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	drain := func() {
		for range 4 {
			r.step()
			r.advance(5 * time.Second)
		}
	}
	drain()
	if r.searchedID("tvdb:403000:1:1") != 1 || r.searchedID("tvdb:403000:1:2") != 1 {
		t.Fatalf("the aired episodes are searched: %v", r.aio.Searches())
	}
	for _, s := range r.aio.Searches() {
		if s.ID != "tvdb:403000:1:1" && s.ID != "tvdb:403000:1:2" {
			t.Errorf("searched for something that has not aired or is a special: %v", s)
		}
	}

	r.setClock(time.Date(2026, 12, 24, 23, 59, 0, 0, time.UTC))
	drain()
	if n := r.searchedID("tvdb:403000:1:3"); n != 0 {
		t.Errorf("searched the day before it airs: %d", n)
	}
	r.setClock(time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC))
	drain()
	if n := r.searchedID("tvdb:403000:1:3"); n != 1 {
		t.Errorf("not searched on its air date: %d", n)
	}
}

func TestNewEpisodesAreFoundByTheDailyRefreshOfMonitoredSeries(t *testing.T) {
	r := newSchedRig(t, false)
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.tmdb.Add(tmdbfake.Title{Kind: "series", ID: 5001, Name: "Quiet", Date: "2020-01-01", IMDb: "tt5001", TVDB: 5001,
		Seasons: []tmdbfake.Season{{Number: 1, Episodes: []tmdbfake.Episode{{Number: 1, Name: "One", AirDate: "2020-01-01"}}}}})
	r.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":5001}`, 201)
	r.mustCall("PATCH", "/api/titles/series-5001", `{"monitored":false}`, 200)
	addHits1408, addHits5001 := r.tmdb.Hits("/3/tv/1408"), r.tmdb.Hits("/3/tv/5001")

	grown := tmdbfake.Title{Kind: "series", ID: 1408, Name: "Dune: Prophecy", Date: "2024-11-17", IMDb: "tt10466872", TVDB: 403000,
		Seasons: []tmdbfake.Season{
			{Number: 0, Episodes: []tmdbfake.Episode{{Number: 1, Name: "Special", AirDate: "2024-01-01"}}},
			{Number: 1, Episodes: []tmdbfake.Episode{
				{Number: 1, Name: "Aired One", AirDate: "2026-11-01"},
				{Number: 2, Name: "Aired Today", AirDate: "2026-11-18"},
				{Number: 3, Name: "Future", AirDate: "2026-12-25"},
				{Number: 4, Name: "Now Dated", AirDate: "2026-11-20"},
				{Number: 5, Name: "Brand New", AirDate: "2026-11-30"},
			}}}}
	r.tmdb.Replace(grown)
	episodes := func() map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, s := range r.mustCall("GET", "/api/titles/series-1408", "", 200)["seasons"].([]any) {
			for _, e := range s.(map[string]any)["episodes"].([]any) {
				out[e.(map[string]any)["id"].(string)] = e.(map[string]any)
			}
		}
		return out
	}

	r.advance(23 * time.Hour)
	r.step()
	if _, ok := episodes()["s01e05"]; ok {
		t.Fatal("the title was refreshed before a day went by")
	}

	r.advance(time.Hour)
	r.step()
	eps := episodes()
	if e, ok := eps["s01e05"]; !ok || e["monitored"] != true || e["name"] != "Brand New" {
		t.Errorf("a new episode is monitored once TMDB lists it: %v", eps["s01e05"])
	}
	if e := eps["s01e04"]; e["name"] != "Now Dated" || e["airDate"] != "2026-11-20" {
		t.Errorf("an episode TMDB dated later is stored: %v", e)
	}
	if got := r.tmdb.Hits("/3/tv/1408") - addHits1408; got != addHits1408 {
		t.Errorf("one refresh asks TMDB as often as the add did (%d requests), got %d", addHits1408, got)
	}
	if got := r.tmdb.Hits("/3/tv/5001"); got != addHits5001 {
		t.Errorf("a series nobody monitors is not refreshed: %d requests after the add", got-addHits5001)
	}

	// A TMDB outage costs a retry in an hour and does not stop the searching.
	later := grown
	later.Seasons = []tmdbfake.Season{grown.Seasons[0], {Number: 1, Episodes: append(append([]tmdbfake.Episode(nil), grown.Seasons[1].Episodes...),
		tmdbfake.Episode{Number: 6, Name: "After Outage", AirDate: "2026-12-01"})}}
	r.tmdb.Replace(later)
	r.tmdb.Down("/3/tv/1408", 500)
	r.advance(24 * time.Hour)
	before := r.searched()
	r.step()
	if r.searched() == before {
		t.Error("a TMDB outage stopped the searching")
	}
	failed := r.tmdb.Hits("/3/tv/1408")
	r.advance(30 * time.Minute)
	r.step()
	if r.tmdb.Hits("/3/tv/1408") != failed {
		t.Error("a failed refresh is retried at once")
	}
	r.tmdb.Heal()
	r.advance(31 * time.Minute)
	r.step()
	if _, ok := episodes()["s01e06"]; !ok {
		t.Error("the refresh is retried within the hour and finds the new episode")
	}
}

func TestBackoffAndHistorySurviveARestart(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	r.step()
	r.advance(15 * time.Minute)
	r.step()
	r.wantSearches(2, "before the restart")
	r.stop()

	r.advance(time.Minute)
	r.start()
	r.step()
	r.wantSearches(2, "the restart does not search at once: the title is backing off")
	if n := len(r.entries("searched")); n != 2 {
		t.Errorf("history after the restart: %d entries, want 2", n)
	}
	r.advance(time.Hour - 2*time.Minute)
	r.step()
	r.wantSearches(2, "the 1 h step carries on from before the restart")
	r.advance(time.Minute)
	r.step()
	r.wantSearches(3, "the third search comes an hour after the second")
	r.advance(time.Hour)
	r.step()
	r.wantSearches(3, "and the next step is 6 h, not 1 h again")
}

func TestUnmonitoringWhileASearchIsInFlightStopsTheGrab(t *testing.T) {
	r := newSchedRig(t, true)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.BluRay.mkv", size: 300 << 20, url: url}))
	r.addMovie()
	r.aio.Before(func(aiostreamsfake.Search) {
		r.mustCall("PATCH", "/api/titles/movie-438631", `{"monitored":false}`, 200)
	})
	r.step()
	if got := r.entries("would_grab"); len(got) != 0 {
		t.Errorf("a title unmonitored during its search was still decided on: %v", got)
	}
	r.aio.Before(nil)
	r.advance(24 * time.Hour)
	r.step()
	r.wantSearches(1, "an unmonitored title is not searched")
}

func TestRemovingATitleWhileASearchIsInFlightLeavesNothingBehind(t *testing.T) {
	r := newSchedRig(t, true)
	r.addMovie()
	r.aio.Before(func(aiostreamsfake.Search) { r.mustCall("DELETE", "/api/titles/movie-438631", "", 204) })
	r.step()
	if got := r.history(); len(got) != 0 {
		t.Errorf("history of a removed title: %v", got)
	}
	if strings.Contains(r.logs.String(), "level=ERROR") {
		t.Errorf("a removed title is not an error:\n%s", r.logs.String())
	}
}

func TestTheLoopSearchesOnItsOwnAndStopsCleanly(t *testing.T) {
	r := newSchedRig(t, false)
	r.stop()
	r.run(app.Options{
		Now:               func() time.Time { return time.Unix(0, r.now.Load()).UTC() },
		TMDBAPIBase:       r.tmdb.APIBase(),
		TMDBImageBase:     r.tmdb.ImageBase(),
		AIOStreamsTimeout: 2 * time.Second,
		Scheduler:         scheduler.Tuning{Tick: 10 * time.Millisecond},
	})
	r.addMovie()
	waitFor(t, "the loop to search for the new movie", 5*time.Second, func() bool { return r.searched() == 1 })
	r.mustCall("POST", "/api/titles/movie-438631/search", "", 200)
	r.advance(5 * time.Second)
	waitFor(t, "Search now to be picked up by the loop", 5*time.Second, func() bool { return r.searched() == 2 })
}

func TestASeriesWithNoIdToSearchByIsSaidSoAndBacksOff(t *testing.T) {
	r := newSchedRig(t, false)
	r.tmdb.Add(tmdbfake.Title{Kind: "series", ID: 6000, Name: "Nameless", Date: "2020-01-01",
		Seasons: []tmdbfake.Season{{Number: 1, Episodes: []tmdbfake.Episode{{Number: 1, Name: "One", AirDate: "2020-01-01"}}}}})
	r.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":6000}`, 201)
	r.step()
	r.wantSearches(0, "there is nothing to ask AIOStreams about")
	got := r.entries("searched")
	if len(got) != 1 || !strings.Contains(got[0]["message"].(string), "neither a TVDB nor an IMDb id") {
		t.Errorf("Activity should say why nothing was searched: %v", r.history())
	}
	r.step()
	if n := len(r.entries("searched")); n != 1 {
		t.Errorf("the title backs off like any other instead of repeating itself: %d entries", n)
	}
}

func TestAReleaseWithoutAPlaybackLinkIsNeverPicked(t *testing.T) {
	r := newSchedRig(t, true)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(
		listed{name: "Dune.2021.2160p.nolink.mkv", size: 300 << 20},
		listed{name: "Dune.2021.1080p.link.mkv", size: 300 << 20, url: url},
	))
	r.addMovie()
	r.step()
	if got := r.entries("would_grab"); len(got) != 1 || got[0]["message"] != "Would grab Dune.2021.1080p.link.mkv" {
		t.Errorf("history: %v", r.history())
	}
}

func TestWhenEveryReleaseIsDroppedActivitySaysWhy(t *testing.T) {
	r := newSchedRig(t, false)
	r.aio.Answer("movie", duneID, results(
		listed{name: "Dune.2021.sample.mkv", size: 30 << 20, url: "http://x.invalid/a"},
		listed{name: "Dune.2021.also.sample.mkv", size: 20 << 20, url: "http://x.invalid/b"},
	))
	r.addMovie()
	r.step()
	got := r.entries("searched")
	if len(got) != 1 || got[0]["message"] != "Searched and found 2 releases, none usable (2 too small). Searching again in 15 minutes." {
		t.Errorf("history: %v", r.history())
	}
}
