package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreamsfake"
	"github.com/davepintodev/marquee/internal/apitest"
	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/scheduler"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

var today = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

type realSvc struct {
	t       *testing.T
	base    string
	env     map[string]string
	logs    *syncBuf
	tmdb    *tmdbfake.Fake
	now     atomic.Int64
	sleeps  []time.Duration
	sleepMu sync.Mutex
	stop    func()
	// adoptErr is how the first-run adoption ended.
	adoptErr error
}

type svcOpt func(*app.Options)

func startReal(t *testing.T, tm *tmdbfake.Fake, opts ...svcOpt) *realSvc {
	t.Helper()
	return startRealAIO(t, tm, "", opts...)
}

// startRealAIO also sets the AIOStreams manifest URL; empty starts a quiet
// AIOStreams fake for the service to point at.
func startRealAIO(t *testing.T, tm *tmdbfake.Fake, manifest string, opts ...svcOpt) *realSvc {
	t.Helper()
	return startRealWith(t, tm, manifest, nil, opts...)
}

// startRealWith is startRealAIO with prep run on the environment before the
// service starts, which is where a test lays out the media folders; a
// non-nil env is a restart on the same directories.
func startRealWith(t *testing.T, tm *tmdbfake.Fake, manifest string, prep func(env map[string]string), opts ...svcOpt) *realSvc {
	t.Helper()
	s := &realSvc{t: t, env: testEnv(t), logs: newSyncBuf(), tmdb: tm}
	if prep != nil {
		prep(s.env)
	}
	if manifest == "" && s.env["MARQUEE_AIOSTREAMS_MANIFEST_URL"] == manifestVal {
		// Health asks AIOStreams, and a placeholder host would reach for real DNS.
		aio := aiostreamsfake.New()
		t.Cleanup(aio.Close)
		manifest = aio.ManifestURL()
	}
	if manifest != "" {
		s.env["MARQUEE_AIOSTREAMS_MANIFEST_URL"] = manifest
	}
	s.env["MARQUEE_TMDB_KEY"] = tmdbfake.Key
	s.now.Store(today.UnixNano())
	o := app.Options{
		Now:           func() time.Time { return time.Unix(0, s.now.Load()).UTC() },
		TMDBAPIBase:   tm.APIBase(),
		TMDBImageBase: tm.ImageBase(),
		TMDBTimeout:   2 * time.Second,
		Scheduler:     scheduler.Tuning{Tick: -1},
		Sleep: func(_ context.Context, d time.Duration) error {
			s.sleepMu.Lock()
			s.sleeps = append(s.sleeps, d)
			s.sleepMu.Unlock()
			return nil
		},
	}
	adopted := make(chan error, 4)
	o.OnAdopted = func(err error) { adopted <- err }
	for _, f := range opts {
		f(&o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addrc := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- app.RunWith(ctx, o, func(k string) string { return s.env[k] }, s.logs, fstest.MapFS{}, func(a net.Addr) { addrc <- a })
	}()
	stopped := false
	s.stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v after shutdown", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("service did not shut down")
		}
	}
	t.Cleanup(s.stop)
	select {
	case a := <-addrc:
		s.base = "http://" + a.String()
	case err := <-done:
		t.Fatalf("service did not start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("service did not start")
	}
	s.adoptErr = awaitAdoption(t, adopted)
	return s
}

// awaitAdoption waits for the first-run adoption, which runs in the
// background from the start, so a test lays out its library either before
// the service starts (to be adopted) or after this returns (to be left alone).
func awaitAdoption(t *testing.T, adopted <-chan error) error {
	t.Helper()
	select {
	case err := <-adopted:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("adoption did not finish")
		return nil
	}
}

type reply struct {
	status int
	body   []byte
	header http.Header
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, r.body)
	}
	return v
}

// call sends the request and, when the contract declares the status for
// it, checks the response against the contract.
func (s *realSvc) call(method, path, body string) reply {
	s.t.Helper()
	return s.send(method, path, body, true)
}

// bad sends a request the contract does not allow, to see how the service refuses it.
func (s *realSvc) bad(method, path, body string) reply {
	s.t.Helper()
	return s.send(method, path, body, false)
}

func (s *realSvc) send(method, path, body string, validate bool) reply {
	s.t.Helper()
	req, err := http.NewRequest(method, s.base+path, strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	if tok := s.env["MARQUEE_API_TOKEN"]; tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	r := reply{resp.StatusCode, raw, resp.Header}
	if validate && r.status != http.StatusNotImplemented {
		_, router := apitest.LoadSpec(s.t)
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		apitest.Validate(s.t, router, method, path, body, resp, r.status)
	}
	return r
}

func (s *realSvc) mustCall(method, path, body string, want int) map[string]any {
	s.t.Helper()
	r := s.call(method, path, body)
	if r.status != want {
		s.t.Fatalf("%s %s: %d, want %d: %s", method, path, r.status, want, r.body)
	}
	if len(r.body) == 0 || r.header.Get("Content-Type") != "application/json" {
		return nil
	}
	return r.json(s.t)
}

func newTMDB(t *testing.T) *tmdbfake.Fake {
	t.Helper()
	f := tmdbfake.New()
	t.Cleanup(f.Close)
	f.Add(tmdbfake.Title{Kind: "movie", ID: 438631, Name: "Dune", Date: "2021-09-15", Overview: "Paul Atreides.", Poster: "/dune-p.jpg", Backdrop: "/dune-b.jpg", IMDb: "tt1160419"})
	f.Add(tmdbfake.Title{Kind: "series", ID: 1408, Name: "Dune: Prophecy", Date: "2024-11-17", Overview: "Sisterhood.", Poster: "/prophecy-p.jpg", Backdrop: "/prophecy-b.jpg", IMDb: "tt10466872", TVDB: 403000,
		Seasons: []tmdbfake.Season{
			{Number: 0, Episodes: []tmdbfake.Episode{{Number: 1, Name: "Special", AirDate: "2024-01-01"}}},
			{Number: 1, Episodes: []tmdbfake.Episode{
				{Number: 1, Name: "Aired One", AirDate: "2026-11-01"},
				{Number: 2, Name: "Aired Today", AirDate: "2026-11-18"},
				{Number: 3, Name: "Future", AirDate: "2026-12-25"},
				{Number: 4, Name: "No Date Yet", AirDate: ""},
			}},
		}})
	f.Add(tmdbfake.Title{Kind: "movie", ID: 99, Name: "Dune Documentary", Date: "", Overview: "No images.", IMDb: ""})
	f.Add(tmdbfake.Title{Kind: "movie", ID: 7, Name: "Trending Movie", Date: "2020-02-02", Overview: "Hot.", Poster: "/t-p.jpg", Backdrop: "/t-b.jpg"})
	return f
}

func TestSearchReturnsMoviesAndSeriesTogetherWithYearAndKind(t *testing.T) {
	s := startReal(t, newTMDB(t))
	v := s.mustCall("GET", "/api/search?q=dune", "", 200)
	got := map[string]map[string]any{}
	for _, it := range v["results"].([]any) {
		m := it.(map[string]any)
		got[m["id"].(string)] = m
	}
	if len(got) != 3 {
		t.Fatalf("want the 2 movies and the series, no person: %v", got)
	}
	if m := got["movie-438631"]; m["kind"] != "movie" || m["year"] != float64(2021) || m["title"] != "Dune" || m["inLibrary"] != false {
		t.Errorf("movie: %v", m)
	}
	if m := got["series-1408"]; m["kind"] != "series" || m["year"] != float64(2024) {
		t.Errorf("series: %v", m)
	}
	if m := got["movie-438631"]; m["posterPath"] != "/api/images/poster/movie-438631" || m["backdropPath"] != "/api/images/backdrop/movie-438631" {
		t.Errorf("images must be same-origin paths: %v", m)
	}
	if got["movie-99"]["year"] != float64(0) {
		t.Errorf("an undated title has year 0: %v", got["movie-99"])
	}
}

func TestSearchMarksTitlesAlreadyInTheLibraryWithTheirBadge(t *testing.T) {
	s := startReal(t, newTMDB(t))
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	v := s.mustCall("GET", "/api/search?q=dune", "", 200)
	for _, it := range v["results"].([]any) {
		m := it.(map[string]any)
		if m["id"] != "movie-438631" {
			continue
		}
		b, _ := m["badge"].(map[string]any)
		if m["inLibrary"] != true || m["monitored"] != true || b["state"] != "searching" || b["label"] != "Searching" {
			t.Errorf("library title: %v", m)
		}
	}
}

func TestSearchNeedsAQuery(t *testing.T) {
	s := startReal(t, newTMDB(t))
	for _, p := range []string{"/api/search", "/api/search?q="} {
		if r := s.bad("GET", p, ""); r.status != 400 || r.json(t)["error"] == "" {
			t.Errorf("%s: %d %s", p, r.status, r.body)
		}
	}
}

func episodes(t *testing.T, title map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, se := range title["seasons"].([]any) {
		for _, e := range se.(map[string]any)["episodes"].([]any) {
			m := e.(map[string]any)
			out[m["id"].(string)] = m
		}
	}
	return out
}

func TestAddedSeriesMonitorsAiredEpisodesAndUnairedWaitForTheirDate(t *testing.T) {
	s := startReal(t, newTMDB(t))
	s.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`, 201)
	title := s.mustCall("GET", "/api/titles/series-1408", "", 200)

	eps := episodes(t, title)
	if len(eps) != 3 {
		t.Fatalf("want 3 dated episodes of season 1 and no specials: %v", eps)
	}
	for _, id := range []string{"s01e01", "s01e02"} {
		if eps[id]["monitored"] != true || eps[id]["state"] != "searching" {
			t.Errorf("%s is aired: %v", id, eps[id])
		}
	}
	if e := eps["s01e03"]; e["state"] != "unaired" || e["airDate"] != "2026-12-25" || e["monitored"] != true {
		t.Errorf("s01e03 waits for its air date: %v", e)
	}
	if b := title["badge"].(map[string]any); b["state"] != "searching" {
		t.Errorf("badge %v", b)
	}

	s.now.Store(time.Date(2026, 12, 25, 0, 0, 1, 0, time.UTC).UnixNano())
	eps = episodes(t, s.mustCall("GET", "/api/titles/series-1408", "", 200))
	if eps["s01e03"]["state"] != "searching" {
		t.Errorf("on its air date the episode is wanted: %v", eps["s01e03"])
	}
}

func TestAddingAMovieMonitorsItAndAcceptsAPreferredQuality(t *testing.T) {
	s := startReal(t, newTMDB(t))
	v := s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"preferredQuality":"2160p"}`, 201)
	if v["monitored"] != true || v["preferredQuality"] != "2160p" || v["inLibrary"] != true || v["title"] != "Dune" {
		t.Errorf("%v", v)
	}
	if len(v["seasons"].([]any)) != 0 {
		t.Errorf("a movie has no seasons: %v", v["seasons"])
	}
	if s.bad("POST", "/api/titles", `{"kind":"movie","tmdbId":1,"preferredQuality":"480p"}`).status != 400 {
		t.Error("an unknown quality must be refused")
	}
	for _, body := range []string{`{}`, `{"kind":"book","tmdbId":1}`, `{"kind":"movie","tmdbId":0}`, `{"kind":"movie","tmdbId":1,"x":1}`, `nope`} {
		if r := s.bad("POST", "/api/titles", body); r.status != 400 {
			t.Errorf("%s: %d", body, r.status)
		}
	}
}

func TestAddingAgainChangesNothingAndAsksTMDBNothing(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	s.mustCall("PATCH", "/api/titles/movie-438631", `{"monitored":false,"preferredQuality":"720p"}`, 200)
	before := tm.Hits("/3/")
	tm.Down("/", 500)

	v := s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"preferredQuality":"2160p"}`, 200)
	if v["monitored"] != false || v["preferredQuality"] != "720p" {
		t.Errorf("a second add must not change the title: %v", v)
	}
	if tm.Hits("/3/") != before {
		t.Error("a title already in the library must not be fetched again")
	}
	lib := s.mustCall("GET", "/api/titles", "", 200)["titles"].([]any)
	if len(lib) != 1 {
		t.Errorf("library: %v", lib)
	}
}

func TestConcurrentAddsOfOneTitleCreateOne(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	var wg sync.WaitGroup
	codes := make(chan int, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- s.call("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`).status
		}()
	}
	wg.Wait()
	close(codes)
	created := 0
	for c := range codes {
		switch c {
		case 201:
			created++
		case 200:
		default:
			t.Errorf("status %d", c)
		}
	}
	if created != 1 {
		t.Errorf("%d adds created the title", created)
	}
	if n := len(s.mustCall("GET", "/api/titles", "", 200)["titles"].([]any)); n != 1 {
		t.Errorf("library has %d titles", n)
	}
	if n := tm.Hits("/3/tv/1408"); n > 3 {
		t.Errorf("the series was fetched %d times", n)
	}
}

func TestAHalfFailedAddWritesNothingAndCanBeRetried(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	tm.Down("/3/tv/1408/season/1", 500)
	r := s.call("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`)
	if r.status != 502 || r.json(t)["error"] == "" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if n := len(s.mustCall("GET", "/api/titles", "", 200)["titles"].([]any)); n != 0 {
		t.Fatalf("a failed add left %d titles", n)
	}
	tm.Heal()
	s.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`, 201)
}

func TestAddingAnUnknownTitleIsNotFound(t *testing.T) {
	s := startReal(t, newTMDB(t))
	if r := s.call("POST", "/api/titles", `{"kind":"movie","tmdbId":123456}`); r.status != 404 {
		t.Errorf("%d %s", r.status, r.body)
	}
}

func fileSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, _ := d.Info()
		out[p] = string(b) + "|" + info.ModTime().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRemovingATitleTouchesNoFileUnderTheMediaRoot(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	for dir, name := range map[string]string{"MARQUEE_MOVIES_PATH": "Dune (2021)/Dune (2021) [Bluray-1080p].mkv", "MARQUEE_SERIES_PATH": "Dune Prophecy/Season 01/Dune Prophecy - S01E01 - Aired One.mkv"} {
		p := filepath.Join(s.env[dir], name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("video bytes "+name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Dir(s.env["MARQUEE_MOVIES_PATH"])
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	s.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`, 201)
	_ = s.call("GET", "/api/images/poster/movie-438631", "")
	before := fileSnapshot(t, s.env["MARQUEE_MOVIES_PATH"])
	for k, v := range fileSnapshot(t, s.env["MARQUEE_SERIES_PATH"]) {
		before[k] = v
	}

	s.mustCall("DELETE", "/api/titles/movie-438631", "", 204)
	s.mustCall("DELETE", "/api/titles/series-1408", "", 204)

	after := fileSnapshot(t, s.env["MARQUEE_MOVIES_PATH"])
	for k, v := range fileSnapshot(t, s.env["MARQUEE_SERIES_PATH"]) {
		after[k] = v
	}
	if len(before) != 2 || len(after) != 2 {
		t.Fatalf("setup: %d files before, %d after", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed or disappeared (%s)", k, root)
		}
	}
	if r := s.call("GET", "/api/titles/movie-438631", ""); r.status != 404 {
		t.Errorf("removed title still there: %d", r.status)
	}
	if r := s.call("DELETE", "/api/titles/movie-438631", ""); r.status != 404 {
		t.Errorf("removing twice: %d", r.status)
	}
}

func TestAnImageServedTwiceIsFetchedFromTMDBOnce(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	get := func(kind string) reply {
		r := s.call("GET", "/api/images/"+kind+"/movie-438631", "")
		if r.status != 200 {
			t.Fatalf("%s: %d %s", kind, r.status, r.body)
		}
		return r
	}
	first, second := get("poster"), get("poster")
	if !bytes.Equal(first.body, second.body) || !bytes.Equal(first.body, tmdbfake.ImageBytes("/dune-p.jpg")) {
		t.Errorf("poster bytes: %q", first.body)
	}
	if ct := first.header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content type %q", ct)
	}
	if n := tm.Hits("/t/p/"); n != 1 {
		t.Fatalf("TMDB saw %d image fetches for two requests", n)
	}
	if b := get("backdrop"); bytes.Equal(b.body, first.body) {
		t.Error("the backdrop must not be the poster")
	}
	if n := tm.Hits("/t/p/"); n != 2 {
		t.Errorf("poster and backdrop are two fetches, got %d", n)
	}
}

func TestConcurrentFirstRequestsForAnImageFetchItOnce(t *testing.T) {
	tm := newTMDB(t)
	tm.SetImageDelay(100 * time.Millisecond)
	s := startReal(t, tm)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := s.call("GET", "/api/images/poster/movie-7", ""); r.status != 200 {
				t.Errorf("%d", r.status)
			}
		}()
	}
	wg.Wait()
	if n := tm.Hits("/t/p/"); n != 1 {
		t.Errorf("%d fetches", n)
	}
}

func TestImagesAreCachedInTheDataDirectoryAndNeverInTheMediaRoot(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	s.mustCall("GET", "/api/search?q=dune", "", 200)
	s.call("GET", "/api/images/poster/movie-438631", "")
	var found int
	_ = filepath.WalkDir(s.env["MARQUEE_DATA_DIR"], func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && bytes.Equal(mustRead(t, p), tmdbfake.ImageBytes("/dune-p.jpg")) {
			found++
		}
		return nil
	})
	if found != 1 {
		t.Errorf("want the poster cached once under the data dir, found %d", found)
	}
	for _, k := range []string{"MARQUEE_MOVIES_PATH", "MARQUEE_SERIES_PATH", "MARQUEE_TEMP_PATH"} {
		if len(fileSnapshot(t, s.env[k])) != 0 {
			t.Errorf("%s was written to", k)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(s.env["MARQUEE_DATA_DIR"], "images")); len(entries) == 0 {
		t.Error("expected an images folder in the data dir")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRemovingATitleDropsItsCachedImagesOnly(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":7}`, 201)
	s.call("GET", "/api/images/poster/movie-438631", "")
	s.call("GET", "/api/images/poster/movie-7", "")
	if tm.Hits("/t/p/") != 2 {
		t.Fatal("setup")
	}
	s.mustCall("DELETE", "/api/titles/movie-438631", "", 204)

	s.call("GET", "/api/images/poster/movie-7", "")
	if tm.Hits("/t/p/") != 2 {
		t.Error("the other title's image must stay cached")
	}
	s.call("GET", "/api/images/poster/movie-438631", "")
	if tm.Hits("/t/p/") != 3 {
		t.Error("the removed title's image must be gone from the cache")
	}
}

func TestAnImageThatTMDBHasNoneOfIsAPlaceholderAndAnUnknownTitleIsNotFound(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	r := s.call("GET", "/api/images/poster/movie-99", "")
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "image/svg+xml") || !bytes.Contains(r.body, []byte("<svg")) {
		t.Errorf("%d %s %s", r.status, r.header.Get("Content-Type"), r.body)
	}
	if r := s.call("GET", "/api/images/poster/movie-424242", ""); r.status != 404 {
		t.Errorf("unknown title: %d", r.status)
	}
	if r := s.bad("GET", "/api/images/banner/movie-7", ""); r.status != 404 && r.status != 400 {
		t.Errorf("unknown kind: %d", r.status)
	}
}

func TestAnImageFailureIsNotCachedAndAnswersBadGateway(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	tm.Down("/t/p/", 503)
	if r := s.call("GET", "/api/images/poster/movie-438631", ""); r.status != 502 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	tm.Heal()
	if r := s.call("GET", "/api/images/poster/movie-438631", ""); r.status != 200 || !bytes.Equal(r.body, tmdbfake.ImageBytes("/dune-p.jpg")) {
		t.Errorf("a failed fetch must not poison the cache: %d %q", r.status, r.body)
	}
}

func TestPngAndWebpImagesKeepTheirOwnTypeAndMatchTheContract(t *testing.T) {
	tm := newTMDB(t)
	tm.Add(tmdbfake.Title{Kind: "movie", ID: 55, Name: "Formats", Date: "2020-01-01", Poster: "/f.png", Backdrop: "/f.webp"})
	s := startReal(t, tm)
	for kind, want := range map[string]string{"poster": "image/png", "backdrop": "image/webp"} {
		r := s.call("GET", "/api/images/"+kind+"/movie-55", "")
		if r.status != 200 || r.header.Get("Content-Type") != want {
			t.Errorf("%s: %d %q, want %s", kind, r.status, r.header.Get("Content-Type"), want)
		}
	}
}

func TestALocalCacheFailureIsAnInternalErrorNotABadGateway(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	blocker := filepath.Join(s.env["MARQUEE_DATA_DIR"], "images")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := s.call("GET", "/api/images/poster/movie-438631", "")
	if r.status != 500 || strings.Contains(string(r.body), "TMDB") {
		t.Fatalf("a broken cache directory is marquee's fault: %d %s", r.status, r.body)
	}
	if strings.Contains(s.logs.String(), tmdbfake.Key) {
		t.Error("the key reached the logs")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if r := s.call("GET", "/api/images/poster/movie-438631", ""); r.status != 200 {
		t.Errorf("after the cache is repaired: %d %s", r.status, r.body)
	}
}

func TestAnImageBodyThatStallsIsCutOffAndTheImageCanBeFetchedAgain(t *testing.T) {
	tm := newTMDB(t)
	tm.Script("/t/p/", tmdbfake.Step{StallBody: true})
	s := startReal(t, tm, func(o *app.Options) { o.TMDBImageTimeout = 300 * time.Millisecond })
	start := time.Now()
	r := s.call("GET", "/api/images/poster/movie-438631", "")
	if r.status != 502 {
		t.Fatalf("a stalled body is TMDB not answering: %d %s", r.status, r.body)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the stall held the request for %v", d)
	}
	r = s.call("GET", "/api/images/poster/movie-438631", "")
	if r.status != 200 || !bytes.Equal(r.body, tmdbfake.ImageBytes("/dune-p.jpg")) {
		t.Errorf("the lock must be free once the first fetch fails: %d %q", r.status, r.body)
	}
}

func TestReMonitoringAnEpisodeOfAnUnmonitoredSeriesMonitorsTheSeriesAgain(t *testing.T) {
	s := startReal(t, newTMDB(t))
	s.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`, 201)
	s.mustCall("PATCH", "/api/titles/series-1408", `{"monitored":false}`, 200)
	v := s.mustCall("PATCH", "/api/titles/series-1408/episodes/s01e02", `{"monitored":true}`, 200)
	if v["monitored"] != true || v["historySummary"] == "Not monitored." {
		t.Errorf("a title searching for an episode must be monitored: %v %v", v["monitored"], v["historySummary"])
	}
	if eps := episodes(t, v); eps["s01e01"]["monitored"] != false {
		t.Errorf("the other episodes stay as they were: %v", eps["s01e01"])
	}
	v = s.mustCall("PATCH", "/api/titles/series-1408/episodes/s01e02", `{"monitored":false}`, 200)
	if v["monitored"] != true {
		t.Error("unmonitoring the last monitored episode must not unmonitor the title")
	}
}

func TestMonitoringAndPreferredQualityCanBeChanged(t *testing.T) {
	s := startReal(t, newTMDB(t))
	s.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1408,"preferredQuality":"1080p"}`, 201)

	v := s.mustCall("PATCH", "/api/titles/series-1408", `{"monitored":false}`, 200)
	if v["monitored"] != false || v["preferredQuality"] != "1080p" {
		t.Errorf("%v", v)
	}
	for id, e := range episodes(t, v) {
		if e["monitored"] != false {
			t.Errorf("%s stays monitored after unmonitoring the series", id)
		}
	}
	if b := v["badge"].(map[string]any); b["state"] != "missing" {
		t.Errorf("an unmonitored title with nothing on disk is missing: %v", b)
	}
	v = s.mustCall("PATCH", "/api/titles/series-1408/episodes/s01e02", `{"monitored":true}`, 200)
	if e := episodes(t, v)["s01e02"]; e["monitored"] != true || episodes(t, v)["s01e01"]["monitored"] != false {
		t.Errorf("only the one episode: %v", e)
	}
	v = s.mustCall("PATCH", "/api/titles/series-1408", `{"preferredQuality":null}`, 200)
	if v["preferredQuality"] != nil {
		t.Errorf("null clears the preference: %v", v["preferredQuality"])
	}
	for _, c := range []struct{ path, body string }{
		{"/api/titles/series-1408", `{}`}, {"/api/titles/series-1408", `{"preferredQuality":"480p"}`},
		{"/api/titles/series-1408/episodes/s01e02", `{}`},
	} {
		if r := s.bad("PATCH", c.path, c.body); r.status != 400 {
			t.Errorf("%s %s: %d", c.path, c.body, r.status)
		}
	}
	for _, p := range []string{"/api/titles/series-1", "/api/titles/series-1408/episodes/s09e09", "/api/titles/nonsense"} {
		method, body := "GET", ""
		if strings.Contains(p, "/episodes/") {
			method, body = "PATCH", `{"monitored":true}`
		}
		if r := s.bad(method, p, body); r.status != 404 {
			t.Errorf("%s: %d", p, r.status)
		}
	}
}

func TestTheWallShowsTheLibraryFirstThenTrending(t *testing.T) {
	tm := newTMDB(t)
	tm.SetTrending(438631, 7)
	s := startReal(t, tm)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	v := s.mustCall("GET", "/api/wall", "", 200)
	var order []string
	for _, it := range v["items"].([]any) {
		m := it.(map[string]any)
		order = append(order, m["source"].(string)+":"+m["title"].(map[string]any)["id"].(string))
	}
	if strings.Join(order, ",") != "library:movie-438631,trending:movie-7" {
		t.Errorf("%v", order)
	}
}

func TestTheWallFallsBackToTheLibraryWhenTMDBIsDown(t *testing.T) {
	tm := newTMDB(t)
	tm.SetTrending(7)
	s := startReal(t, tm)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	tm.Down("/", 500)
	if n := len(s.mustCall("GET", "/api/wall", "", 200)["items"].([]any)); n != 1 {
		t.Errorf("want the one library title, got %d", n)
	}
	if got := s.mustCall("GET", "/api/titles", "", 200)["titles"].([]any); len(got) != 1 {
		t.Errorf("the library does not need TMDB: %v", got)
	}
}

func TestTMDBFailuresBecomeClearErrorsInTheContractsShape(t *testing.T) {
	cases := []struct {
		name   string
		script func(*tmdbfake.Fake)
		want   int
	}{
		{"5xx", func(f *tmdbfake.Fake) { f.Down("/3/search", 500) }, 502},
		{"rate limited twice", func(f *tmdbfake.Fake) {
			f.Script("/3/search", tmdbfake.Step{Status: 429, RetryAfter: "2"}, tmdbfake.Step{Status: 429, RetryAfter: "2"})
		}, 503},
		{"key refused", func(f *tmdbfake.Fake) { f.Down("/3/search", 401) }, 502},
		{"timeout", func(f *tmdbfake.Fake) { f.Script("/3/search", tmdbfake.Step{Hang: true}) }, 502},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tm := newTMDB(t)
			c.script(tm)
			s := startReal(t, tm, func(o *app.Options) { o.TMDBTimeout = 200 * time.Millisecond })
			r := s.call("GET", "/api/search?q=dune", "")
			if r.status != c.want || r.json(t)["error"] == "" {
				t.Fatalf("%d %s", r.status, r.body)
			}
			for _, leak := range []string{tmdbfake.Key, "api_key"} {
				if strings.Contains(string(r.body), leak) || strings.Contains(s.logs.String(), leak) {
					t.Errorf("%q leaks into the response or the logs: %s", leak, r.body)
				}
			}
			if strings.Contains(string(r.body), "127.0.0.1") {
				t.Errorf("the response names TMDB's address: %s", r.body)
			}
		})
	}
}

func TestARateLimitedRequestIsRetriedOnceAfterTheBoundedWait(t *testing.T) {
	tm := newTMDB(t)
	tm.Script("/3/search", tmdbfake.Step{Status: 429, RetryAfter: "3"})
	s := startReal(t, tm)
	s.mustCall("GET", "/api/search?q=dune", "", 200)
	if len(s.sleeps) != 1 || s.sleeps[0] != 3*time.Second {
		t.Errorf("waits: %v", s.sleeps)
	}

	tm.Script("/3/search", tmdbfake.Step{Status: 429, RetryAfter: "86400"})
	s.mustCall("GET", "/api/search?q=dune", "", 200)
	if len(s.sleeps) != 2 || s.sleeps[1] > 30*time.Second {
		t.Errorf("the wait must be bounded: %v", s.sleeps)
	}
}

func TestAnUnreachableTMDBLeaksNeitherItsAddressNorTheKey(t *testing.T) {
	// A closed port can be handed to another parallel test's server and answer
	// 404; holding the port and dropping every connection keeps it unreachable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	base := "http://" + ln.Addr().String()
	s := startReal(t, newTMDB(t), func(o *app.Options) { o.TMDBAPIBase = base + "/3" })
	r := s.call("GET", "/api/search?q=dune", "")
	if r.status != 502 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	for _, leak := range []string{tmdbfake.Key, "api_key", base, strings.TrimPrefix(base, "http://")} {
		if strings.Contains(string(r.body), leak) || strings.Contains(s.logs.String(), leak) {
			t.Errorf("%q leaks: %s", leak, r.body)
		}
	}
}

func TestTheCatalogSurvivesARestart(t *testing.T) {
	tm := newTMDB(t)
	first := startReal(t, tm)
	first.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`, 201)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrc := make(chan net.Addr, 1)
	env := map[string]string{}
	for k, v := range first.env {
		env[k] = v
	}
	env["MARQUEE_LISTEN"] = "127.0.0.1:0"
	go func() {
		_ = app.RunWith(ctx, app.Options{TMDBAPIBase: tm.APIBase(), TMDBImageBase: tm.ImageBase(), Scheduler: scheduler.Tuning{Tick: -1}}, func(k string) string { return env[k] }, io.Discard, fstest.MapFS{}, func(a net.Addr) { addrc <- a })
	}()
	a := <-addrc
	if code, body, _ := get(t, "http://"+a.String()+"/api/titles/series-1408"); code != 200 {
		t.Errorf("after a restart: %d %s", code, body)
	}
}

func TestAnUnknownAPIRouteAnswers404InTheContractsErrorShape(t *testing.T) {
	s := startReal(t, newTMDB(t))
	if r := s.bad("GET", "/api/unknown", ""); r.status != 404 || r.json(t)["error"] == "" {
		t.Errorf("unknown route: %d %s", r.status, r.body)
	}
}

func TestTheTMDBKeyNeverAppearsInLogs(t *testing.T) {
	tm := newTMDB(t)
	s := startReal(t, tm)
	s.mustCall("GET", "/api/search?q=dune", "", 200)
	tm.Down("/", 500)
	s.call("GET", "/api/wall", "")
	s.call("GET", "/api/search?q=dune", "")
	if strings.Contains(s.logs.String(), tmdbfake.Key) || strings.Contains(s.logs.String(), "api_key") {
		t.Errorf("logs leak the key: %s", s.logs.String())
	}
}
