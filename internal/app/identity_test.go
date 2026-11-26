package app_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/apitest"
	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

const lookupToken = "subfit-SECRETxyz"

func identityTMDB(t *testing.T) *tmdbfake.Fake {
	t.Helper()
	f := adoptTMDB(t)
	alts := []string{"Dune", "dune", "Dune: Part One", "Duna", "Duna", "DUNA"}
	for i := 1; i <= 20; i++ {
		alts = append(alts, fmt.Sprintf("Dune %d", i))
	}
	f.Replace(tmdbfake.Title{Kind: "movie", ID: 438631, Name: "Dune", Date: "2021-09-15", Overview: "Paul Atreides.", IMDb: "tt1160419",
		Language: "en", OriginalName: "Dune: Part One", AltTitles: alts})
	f.Replace(tmdbfake.Title{Kind: "series", ID: 1622, Name: "House", Date: "2004-11-16", Overview: "Diagnostics.", IMDb: "tt0412142", TVDB: 73255,
		Language: "en", OriginalName: "House", AltTitles: []string{"House, M.D.", "Dr. House", "House, M.D."},
		Seasons: []tmdbfake.Season{
			season(1, "2004-11-16", "2004-11-23", "2004-11-30"),
			season(2, "2005-09-13", "2005-09-20", "2005-09-27"),
			season(3, "2006-09-05", "2006-09-12", "2006-09-19"),
			season(4, "2007-09-25", "2007-10-02", "2007-10-09"),
		}})
	return f
}

func startIdentity(t *testing.T, tm *tmdbfake.Fake, env func(map[string]string)) *realSvc {
	t.Helper()
	s, err := startAdoptWith(t, tm, func(e map[string]string) {
		library(t, []string{duneFile}, []string{houseS3E12, houseS4E1})(e)
		e["MARQUEE_SUBFIT_TOKEN"] = lookupToken
		if env != nil {
			env(e)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func identityPath(library, path string) string {
	return "/api/files/identity?" + url.Values{"library": {library}, "path": {path}}.Encode()
}

// A request that breaks the contract on purpose is not checked against it.
func (s *realSvc) lookup(path, token string) reply {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.base+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	r := reply{resp.StatusCode, raw, resp.Header}
	if strings.HasPrefix(path, "/api/files/identity?") && r.status != http.StatusBadRequest {
		_, router := apitest.LoadSpec(s.t)
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		apitest.Validate(s.t, router, http.MethodGet, path, "", resp, r.status)
	}
	return r
}

func (s *realSvc) identity(library, path string) map[string]any {
	s.t.Helper()
	r := s.lookup(identityPath(library, path), lookupToken)
	if r.status != 200 {
		s.t.Fatalf("lookup of %s %s: %d %s", library, path, r.status, r.body)
	}
	return r.json(s.t)
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheLookupTokenFindsWhatAnEpisodeFileIs(t *testing.T) {
	s := startIdentity(t, identityTMDB(t), nil)
	got := jsonOf(t, s.identity("series", houseS4E1))
	want := `{"alternativeTitles":["House, M.D.","Dr. House"],"episode":1,"episodeTitle":"Episode 1","imdbId":"tt0412142","kind":"episode",` +
		`"originalLanguage":"en","season":4,"title":"House","tmdbId":1622,"year":2004}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if strings.Contains(s.logs.String(), "SECRETxyz") {
		t.Errorf("logs leak the lookup token:\n%s", s.logs.String())
	}
}

func TestAMultiEpisodeFileAnswersWithItsFirstEpisode(t *testing.T) {
	s := startIdentity(t, identityTMDB(t), nil)
	got := s.identity("series", houseS3E12)
	if got["season"] != float64(3) || got["episode"] != float64(1) || got["episodeTitle"] != "Episode 1" {
		t.Errorf("%v", got)
	}
}

func TestAMoviesOriginalTitleLeadsItsAlternativeTitlesWithoutRepeatsOrItsOwnTitle(t *testing.T) {
	s := startIdentity(t, identityTMDB(t), nil)
	got := s.identity("movies", duneFile)
	if got["kind"] != "movie" || got["season"] != nil || got["episode"] != nil || got["episodeTitle"] != nil ||
		got["imdbId"] != "tt1160419" || got["tmdbId"] != float64(438631) || got["title"] != "Dune" || got["year"] != float64(2021) {
		t.Errorf("%v", got)
	}
	want := []any{"Dune: Part One", "Duna"}
	for i := 1; i <= 13; i++ {
		want = append(want, fmt.Sprintf("Dune %d", i))
	}
	if alts := got["alternativeTitles"].([]any); !slices.Equal(alts, want) {
		t.Errorf("alternative titles %v\nwant %v", alts, want)
	}
}

func TestATitleIsAskedOfTMDBOnceAndKeptAcrossRestarts(t *testing.T) {
	tm := identityTMDB(t)
	s := startIdentity(t, tm, nil)
	before := tm.Hits("/3/tv/1622")
	first := jsonOf(t, s.identity("series", houseS4E1))
	if n := tm.Hits("/3/tv/1622") - before; n != 1 {
		t.Fatalf("first lookup asked TMDB %d times, want 1", n)
	}
	if again := jsonOf(t, s.identity("series", houseS3E12)); !strings.Contains(again, `"alternativeTitles":["House, M.D.","Dr. House"]`) {
		t.Errorf("another file of the title: %s", again)
	}

	s.stop()
	old := s.env
	s2 := startRealWith(t, tm, "", func(env map[string]string) {
		for k, v := range old {
			env[k] = v
		}
	})
	if got := jsonOf(t, s2.identity("series", houseS4E1)); got != first {
		t.Errorf("after a restart: %s\nwant %s", got, first)
	}
	if n := tm.Hits("/3/tv/1622") - before; n != 1 {
		t.Errorf("TMDB was asked %d times in all, want 1", n)
	}
}

func TestATMDBFailureAnswers503AndKeepsNothing(t *testing.T) {
	tm := identityTMDB(t)
	s := startIdentity(t, tm, nil)
	tm.Down("/3/movie/438631", http.StatusInternalServerError)
	r := s.lookup(identityPath("movies", duneFile), lookupToken)
	if r.status != 503 {
		t.Fatalf("TMDB down: %d %s, want 503", r.status, r.body)
	}
	tm.Heal()
	tm.Script("/3/movie/438631", tmdbfake.Step{Status: 429}, tmdbfake.Step{Status: 429})
	if r := s.lookup(identityPath("movies", duneFile), lookupToken); r.status != 503 {
		t.Fatalf("TMDB rate limiting: %d %s, want 503", r.status, r.body)
	}
	before := tm.Hits("/3/movie/438631")
	got := s.identity("movies", duneFile)
	if tm.Hits("/3/movie/438631") != before+1 || got["originalLanguage"] != "en" {
		t.Errorf("after TMDB is back the lookup asks it again: %d hits, %v", tm.Hits("/3/movie/438631")-before, got)
	}
}

func TestALookupOfAFileNotInTheCatalogOrOutsideTheLibraryIsRefused(t *testing.T) {
	s := startIdentity(t, identityTMDB(t), nil)
	for _, c := range []struct {
		name string
		path string
		want int
	}{
		{"a file not in the catalog", identityPath("series", "House/Season 04/House - S04E03 - Episode 3.mkv"), 404},
		{"a movie looked for in the series library", identityPath("series", duneFile), 404},
		{"an episode looked for in the movie library", identityPath("movies", houseS4E1), 404},
		{"a library that does not exist", identityPath("music", duneFile), 400},
		{"no library", "/api/files/identity?" + url.Values{"path": {duneFile}}.Encode(), 400},
		{"no path", "/api/files/identity?library=movies", 400},
		{"an empty path", identityPath("movies", ""), 400},
		{"a path above the root", identityPath("movies", "../movies/"+duneFile), 400},
		{"a path that climbs out midway", identityPath("series", "House/../../series/"+houseS4E1), 400},
		{"an absolute path", identityPath("movies", "/"+duneFile), 400},
		{"the root itself", identityPath("movies", "."), 400},
	} {
		r := s.lookup(c.path, lookupToken)
		if r.status != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, r.status, r.body, c.want)
			continue
		}
		if v := r.json(t); v["error"] == nil {
			t.Errorf("%s: not the contract's error shape: %s", c.name, r.body)
		}
	}
	if got := s.identity("series", "House/Season 04/./"+strings.TrimPrefix(houseS4E1, "House/Season 04/")); got["episode"] != float64(1) {
		t.Errorf("a path with a harmless dot is the same file: %v", got)
	}
}

func TestTheLookupNeedsItsTokenOrTheAPITokenAndTheLookupTokenOpensNothingElse(t *testing.T) {
	s := startIdentity(t, identityTMDB(t), nil)
	path := identityPath("series", houseS4E1)
	for name, tok := range map[string]string{"no token": "", "a wrong token": "subfit-SECRETxyy", "a prefix": "subfit-SECRET"} {
		if r := s.lookup(path, tok); r.status != 401 || r.json(t)["error"] == nil {
			t.Errorf("%s: %d %s, want 401", name, r.status, r.body)
		}
	}
	if r := s.lookup(path, testToken); r.status != 200 {
		t.Errorf("the API token: %d %s, want 200", r.status, r.body)
	}
	for _, p := range []string{"/api/titles", "/api/downloads", "/api/health", "/api/titles/series-1622", "/api/files/identity/x", "/api/events"} {
		if r := s.lookup(p, lookupToken); r.status != 401 {
			t.Errorf("GET %s with the lookup token: %d, want 401", p, r.status)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, s.base+"/api/library/rescan", nil)
	req.Header.Set("Authorization", "Bearer "+lookupToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("POST with the lookup token: %d, want 401", resp.StatusCode)
	}
	if r := s.lookup(path, lookupToken); len(r.header.Values("Set-Cookie")) != 0 {
		t.Errorf("the lookup token got a session cookie: %v", r.header)
	}
}

func TestWithoutALookupTokenTheLookupAnswers401EvenOnAnOpenAPI(t *testing.T) {
	s := startIdentity(t, identityTMDB(t), func(env map[string]string) {
		delete(env, "MARQUEE_SUBFIT_TOKEN")
		delete(env, "MARQUEE_API_TOKEN")
	})
	path := identityPath("series", houseS4E1)
	for _, tok := range []string{"", lookupToken} {
		if r := s.lookup(path, tok); r.status != 401 {
			t.Errorf("token %q: %d %s, want 401", tok, r.status, r.body)
		}
	}
	if r := s.lookup("/api/titles", ""); r.status != 200 {
		t.Errorf("the rest of an open API stays open: %d", r.status)
	}
}

func TestWithOnlyALookupTokenTheLookupNeedsIt(t *testing.T) {
	s := startIdentity(t, identityTMDB(t), func(env map[string]string) { delete(env, "MARQUEE_API_TOKEN") })
	path := identityPath("series", houseS4E1)
	if r := s.lookup(path, ""); r.status != 401 {
		t.Errorf("no token: %d, want 401", r.status)
	}
	if r := s.lookup(path, lookupToken); r.status != 200 {
		t.Errorf("the lookup token: %d %s, want 200", r.status, r.body)
	}
}

func TestADownloadedMultiEpisodeFileIsFoundByThePathItWasImportedTo(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("pair.mkv", debridfake.File{Data: debridfake.MKV(53 << 20)})
	r.aio.Answer("series", "tvdb:403000:1:1", results(listed{name: "Dune.Prophecy.S01E01E02.1080p.WEB-DL.mkv", size: 53 << 20, url: url, season: 1, eps: []int{1, 2}}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	waitFor(t, "the import", 20*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	rel := r.row("series-1408", 1, 2).path
	got := r.lookup(identityPath("series", rel), testToken)
	if got.status != 200 {
		t.Fatalf("%s: %d %s", rel, got.status, got.body)
	}
	if v := got.json(t); v["kind"] != "episode" || v["tmdbId"] != float64(1408) || v["season"] != float64(1) || v["episode"] != float64(1) {
		t.Errorf("%s: %v", rel, v)
	}
}

func TestLookupsOfATitleArrivingTogetherAskTMDBOnce(t *testing.T) {
	tm := identityTMDB(t)
	s := startIdentity(t, tm, nil)
	before := tm.Hits("/3/tv/1622")
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Go(func() {
			p := houseS4E1
			if i%2 == 1 {
				p = houseS3E12
			}
			req, _ := http.NewRequest(http.MethodGet, s.base+identityPath("series", p), nil)
			req.Header.Set("Authorization", "Bearer "+lookupToken)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
			codes[i] = resp.StatusCode
		})
	}
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Errorf("lookup %d: %d", i, c)
		}
	}
	if n := tm.Hits("/3/tv/1622") - before; n != 1 {
		t.Errorf("TMDB asked %d times, want 1", n)
	}
}
