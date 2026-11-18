package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreamsfake"
	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

func newAIO(t *testing.T) *aiostreamsfake.Fake {
	t.Helper()
	f := aiostreamsfake.New()
	t.Cleanup(f.Close)
	return f
}

func shortAIO(o *app.Options) { o.AIOStreamsTimeout = 300 * time.Millisecond }

func TestASourceErrorIsAnErrorAndNeverNothingFound(t *testing.T) {
	cases := []struct {
		name     string
		step     *aiostreamsfake.Step
		password string
		status   int
		words    string
	}{
		{"rate limited", &aiostreamsfake.Step{Status: 429}, "", 503, "rate-limiting"},
		{"server error", &aiostreamsfake.Step{Status: 500}, "", 502, "not answering"},
		{"hangs", &aiostreamsfake.Step{Hang: true}, "", 502, "not answering"},
		{"bad credentials", nil, "wrong-password", 502, "credentials"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			aio := newAIO(t)
			aio.Answer("movie", "tt1160419", aiostreamsfake.Fixture("movie-dune.json"))
			manifest := aio.ManifestURL()
			if c.password != "" {
				manifest = strings.Replace(manifest, aiostreamsfake.Password, c.password, 1)
			}
			if c.step != nil {
				aio.Fail(*c.step)
			}
			s := startRealAIO(t, newTMDB(t), manifest, shortAIO)
			r := s.call("GET", "/api/titles/movie-438631/releases", "")
			if r.status != c.status {
				t.Fatalf("%d %s, want %d", r.status, r.body, c.status)
			}
			if msg, _ := r.json(t)["error"].(string); !strings.Contains(msg, c.words) {
				t.Errorf("error %q should say %q", msg, c.words)
			}
		})
	}
}

type listing struct {
	candidates []map[string]any
	dropped    []map[string]any
}

func (s *realSvc) releases(path string) listing {
	s.t.Helper()
	v := s.mustCall("GET", path, "", 200)
	var l listing
	for _, c := range v["candidates"].([]any) {
		l.candidates = append(l.candidates, c.(map[string]any))
	}
	for _, d := range v["dropped"].([]any) {
		l.dropped = append(l.dropped, d.(map[string]any))
	}
	return l
}

func (l listing) candidateNames() []string {
	var out []string
	for _, c := range l.candidates {
		out = append(out, c["name"].(string))
	}
	return out
}

// reasons maps each dropped release's name to its reason and message.
func (l listing) reasons() map[string][2]string {
	out := map[string][2]string{}
	for _, d := range l.dropped {
		out[d["release"].(map[string]any)["name"].(string)] = [2]string{d["reason"].(string), d["message"].(string)}
	}
	return out
}

func startWithDune(t *testing.T) (*realSvc, *aiostreamsfake.Fake) {
	t.Helper()
	aio := newAIO(t)
	aio.Answer("movie", "tt1160419", aiostreamsfake.Fixture("movie-dune.json"))
	aio.Answer("series", "tvdb:403000:1:2", aiostreamsfake.Fixture("episode-prophecy-s01e02.json"))
	return startRealAIO(t, newTMDB(t), aio.ManifestURL(), shortAIO), aio
}

func TestABadReleaseIsDroppedWithItsReasonInWords(t *testing.T) {
	s, aio := startWithDune(t)
	l := s.releases("/api/titles/movie-438631/releases")

	if got := strings.Join(l.candidateNames(), ","); got != "Dune.2021.1080p.BluRay.x264-GROUP.mkv,Dune.2021.720p.WEB-DL.mkv" {
		t.Errorf("candidates in AIOStreams' order: %s", got)
	}
	want := map[string][2]string{
		"Dune.2021.2160p.WEB-DL.mkv": {"not_cached", "not cached"},
		"Dune.2021.1080p.WEB-DL.exe": {"not_video", "a .exe file, not a video"},
		"Dune.2021.720p.BluRay.7z":   {"not_video", "a .7z file, not a video"},
		"Dune.2021.sample.mkv":       {"too_small", "30 MB, too small to be a movie"},
	}
	got := l.reasons()
	if len(got) != len(want) {
		t.Errorf("dropped %v", got)
	}
	for name, w := range want {
		if got[name][0] != w[0] || !strings.Contains(got[name][1], w[1]) {
			t.Errorf("%s: got %v, want %v", name, got[name], w)
		}
	}
	if sr := aio.Searches(); len(sr) != 1 || sr[0] != (aiostreamsfake.Search{Type: "movie", ID: "tt1160419"}) {
		t.Errorf("searches: %v", sr)
	}
	if lib := s.mustCall("GET", "/api/titles", "", 200)["titles"].([]any); len(lib) != 0 {
		t.Errorf("listing releases must not add the title: %v", lib)
	}
}

func TestAnEpisodeIsSearchedByTVDBIdAndOnlyItsOwnEpisodeIsACandidate(t *testing.T) {
	s, aio := startWithDune(t)
	s.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`, 201)
	l := s.releases("/api/titles/series-1408/releases?episode=s01e02")
	if got := strings.Join(l.candidateNames(), ","); got != "Dune.Prophecy.S01E02.1080p.WEB-DL.mkv,Dune.Prophecy.S01E02.720p.HDTV.mkv" {
		t.Errorf("candidates: %s", got)
	}
	got := l.reasons()
	if r := got["Dune.Prophecy.S01E03.1080p.WEB-DL.mkv"]; r[0] != "wrong_episode" || !strings.Contains(r[1], "episode 3, not episode 2") {
		t.Errorf("wrong episode: %v", r)
	}
	if r := got["Dune.Prophecy.S01E02.2160p.WEB-DL.mkv"]; r[0] != "not_cached" {
		t.Errorf("uncached: %v", r)
	}
	if sr := aio.Searches(); len(sr) != 1 || sr[0] != (aiostreamsfake.Search{Type: "series", ID: "tvdb:403000:1:2"}) {
		t.Errorf("searches: %v", sr)
	}
}

func TestAMissingPreferredQualityStillLeavesTheBestOtherCandidateFirst(t *testing.T) {
	s, _ := startWithDune(t)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"preferredQuality":"2160p"}`, 201)
	l := s.releases("/api/titles/movie-438631/releases")
	if len(l.candidates) == 0 || l.candidates[0]["name"] != "Dune.2021.1080p.BluRay.x264-GROUP.mkv" {
		t.Errorf("the only 2160p release is uncached; the best other one must lead: %v", l.candidateNames())
	}
}

func TestABlocklistedReleaseIsDroppedUntilItsEntryExpires(t *testing.T) {
	s, _ := startWithDune(t)
	path := "/api/titles/movie-438631/releases"
	var id string
	for _, c := range s.releases(path).candidates {
		if c["name"] == "Dune.2021.720p.WEB-DL.mkv" {
			id = c["id"].(string)
		}
	}
	if id == "" {
		t.Fatal("the 720p release is not a candidate to begin with")
	}
	store, err := catalog.Open(s.env["MARQUEE_DATA_DIR"], func() time.Time { return today })
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Block(context.Background(), id, "stalled"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	l := s.releases(path)
	if r := l.reasons()["Dune.2021.720p.WEB-DL.mkv"]; r[0] != "blocklisted" || !strings.Contains(r[1], "failed before") {
		t.Errorf("blocklisted: %v", r)
	}
	if len(l.candidates) != 1 {
		t.Errorf("candidates: %v", l.candidateNames())
	}

	s.now.Store(today.Add(29 * 24 * time.Hour).UnixNano())
	if got := len(s.releases(path).candidates); got != 1 {
		t.Errorf("after 29 days the entry still holds: %d candidates", got)
	}
	s.now.Store(today.Add(30 * 24 * time.Hour).UnixNano())
	if got := len(s.releases(path).candidates); got != 2 {
		t.Errorf("after 30 days the release gets another chance: %d candidates", got)
	}
}

func TestNoResultsIsAnEmptyListAndNotAnError(t *testing.T) {
	aio := newAIO(t)
	s := startRealAIO(t, newTMDB(t), aio.ManifestURL(), shortAIO)
	l := s.releases("/api/titles/movie-438631/releases")
	if len(l.candidates) != 0 || len(l.dropped) != 0 {
		t.Errorf("%+v", l)
	}
}

func TestSourceIdsFallBackFromTVDBToIMDbAndFromIMDbToTMDB(t *testing.T) {
	tm := newTMDB(t)
	tm.Add(tmdbfake.Title{Kind: "series", ID: 2000, Name: "Imdb Only", Date: "2020-01-01", IMDb: "tt2000000", Seasons: []tmdbfake.Season{
		{Number: 1, Episodes: []tmdbfake.Episode{{Number: 1, Name: "One", AirDate: "2020-01-01"}}}}})
	tm.Add(tmdbfake.Title{Kind: "series", ID: 2001, Name: "No Ids", Date: "2020-01-01", Seasons: []tmdbfake.Season{
		{Number: 1, Episodes: []tmdbfake.Episode{{Number: 1, Name: "One", AirDate: "2020-01-01"}}}}})
	aio := newAIO(t)
	s := startRealAIO(t, tm, aio.ManifestURL(), shortAIO)

	s.releases("/api/titles/series-2000/releases?episode=s01e01")
	s.releases("/api/titles/movie-99/releases")
	r := s.call("GET", "/api/titles/series-2001/releases?episode=s01e01", "")
	if r.status != 422 || !strings.Contains(string(r.body), "TVDB") {
		t.Errorf("a series with no id AIOStreams knows must say so, not find nothing: %d %s", r.status, r.body)
	}
	want := []aiostreamsfake.Search{{Type: "series", ID: "tt2000000:1:1"}, {Type: "movie", ID: "tmdb:99"}}
	got := aio.Searches()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("searches: %v, want %v", got, want)
	}
}

func TestBadReleaseRequestsAreRefusedBeforeAnySearch(t *testing.T) {
	s, aio := startWithDune(t)
	for path, want := range map[string]int{
		"/api/titles/series-1408/releases":                400,
		"/api/titles/series-1408/releases?episode=s09e09": 400,
		"/api/titles/series-1408/releases?episode=x":      400,
		"/api/titles/nonsense/releases":                   404,
		"/api/titles/movie-424242/releases":               404,
	} {
		if r := s.bad("GET", path, ""); r.status != want {
			t.Errorf("%s: %d %s, want %d", path, r.status, r.body, want)
		}
	}
	if n := len(aio.Searches()); n != 0 {
		t.Errorf("AIOStreams was searched %d times", n)
	}
}

func TestTheSourceFailsInTheContractsShapeAndLeaksNothing(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	host := strings.TrimPrefix(dead.URL, "http://")
	manifest := dead.URL + "/stremio/" + aiostreamsfake.User + "/" + aiostreamsfake.Password + "/manifest.json"
	dead.Close()
	s := startRealAIO(t, newTMDB(t), manifest, shortAIO)
	r := s.call("GET", "/api/titles/movie-438631/releases", "")
	if r.status != 502 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	for _, leak := range []string{host, aiostreamsfake.User, aiostreamsfake.Password} {
		if strings.Contains(string(r.body), leak) || strings.Contains(s.logs.String(), leak) {
			t.Errorf("%q leaks: %s", leak, r.body)
		}
	}
}

func TestNoPlaybackLinkLeavesTheServiceInAResponseOrALog(t *testing.T) {
	s, _ := startWithDune(t)
	for _, path := range []string{"/api/titles/movie-438631/releases"} {
		r := s.call("GET", path, "")
		for _, leak := range []string{"PLAYBACK-TOKEN", "aio.example.test", aiostreamsfake.Password} {
			if strings.Contains(string(r.body), leak) || strings.Contains(s.logs.String(), leak) {
				t.Errorf("%q leaks: %s", leak, r.body)
			}
		}
	}
}

func TestAManifestOfTheWrongShapeStopsStartupWithoutEchoingIt(t *testing.T) {
	env := testEnv(t)
	env["MARQUEE_AIOSTREAMS_MANIFEST_URL"] = "https://aio.example.test/SECRET123/manifest.json"
	err := app.Run(context.Background(), func(k string) string { return env[k] }, io.Discard, fstest.MapFS{}, nil)
	if err == nil || !strings.Contains(err.Error(), "MARQUEE_AIOSTREAMS_MANIFEST_URL") || strings.Contains(err.Error(), "SECRET123") {
		t.Errorf("got %v", err)
	}
}

func TestAnUnreadableOrFailedAnswerIsASourceErrorToo(t *testing.T) {
	for name, body := range map[string]string{
		"garbage":         `<html>bad gateway</html>`,
		"success false":   `{"success":false,"error":{"message":"addon failed"}}`,
		"truncated":       `{"success":true,"data":{"results":[{"filename":`,
		"no data section": `{"success":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			aio := newAIO(t)
			aio.Answer("movie", "tt1160419", []byte(body))
			s := startRealAIO(t, newTMDB(t), aio.ManifestURL(), shortAIO)
			want := 502
			if name == "no data section" {
				want = 200 // a successful search with nothing in it is "no results"
			}
			if r := s.call("GET", "/api/titles/movie-438631/releases", ""); r.status != want {
				t.Errorf("%d %s, want %d", r.status, r.body, want)
			}
		})
	}
}
