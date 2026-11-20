package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/omdbfake"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

// aboutTMDB is newTMDB plus what the details screen reads about Dune and Dune: Prophecy.
func aboutTMDB(t *testing.T) *tmdbfake.Fake {
	t.Helper()
	f := newTMDB(t)
	f.Replace(tmdbfake.Title{
		Kind: "movie", ID: 438631, Name: "Dune", Date: "2021-09-15", Overview: "Paul Atreides.", Poster: "/dune-p.jpg", Backdrop: "/dune-b.jpg", IMDb: "tt1160419",
		Genres: []string{"Science Fiction", "Adventure"}, Runtime: 155, Status: "Released", Language: "en", LanguageName: "English", Score: 7.8, Votes: 12000,
		Cast: []tmdbfake.Person{
			{ID: 1190668, Name: "Timothée Chalamet", Character: "Paul Atreides", Profile: "/timothee.jpg"},
			{ID: 3, Name: "Extra Without Photo", Character: ""},
		},
		Similar: []int{7, 99},
	})
	f.Replace(tmdbfake.Title{
		Kind: "series", ID: 1408, Name: "Dune: Prophecy", Date: "2024-11-17", Overview: "Sisterhood.", Poster: "/prophecy-p.jpg", Backdrop: "/prophecy-b.jpg", IMDb: "tt10466872", TVDB: 403000,
		Genres: []string{"Drama"}, Runtime: 55, Status: "Returning Series", Language: "en", LanguageName: "English", Score: 7.1, Votes: 800,
		Seasons: []tmdbfake.Season{{Number: 1, Episodes: []tmdbfake.Episode{{Number: 1, Name: "Aired One", AirDate: "2026-11-01"}}}},
	})
	return f
}

type aboutRig struct {
	*realSvc
	omdb *omdbfake.Fake
}

func startAbout(t *testing.T, tm *tmdbfake.Fake, withKey bool) aboutRig {
	t.Helper()
	om := omdbfake.New()
	t.Cleanup(om.Close)
	om.Add("tt1160419", omdbfake.Entry{IMDb: "8.0", IMDbVotes: "752,119", Tomatoes: "83%"})
	prep := func(env map[string]string) {
		if withKey {
			env["MARQUEE_OMDB_KEY"] = omdbfake.Key
		}
	}
	s := startRealWith(t, tm, "", prep, func(o *app.Options) { o.OMDbBase = om.URL() })
	return aboutRig{s, om}
}

func sources(v map[string]any) []string {
	var out []string
	for _, r := range v["ratings"].([]any) {
		out = append(out, r.(map[string]any)["source"].(string))
	}
	return out
}

func TestAboutAnswersFromTMDBAloneWhenNoOMDbKeyIsSet(t *testing.T) {
	r := startAbout(t, aboutTMDB(t), false)
	v := r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)

	if got := sources(v); len(got) != 1 || got[0] != "tmdb" {
		t.Fatalf("with no OMDb key the score is TMDB's alone: %v", v["ratings"])
	}
	tmdb := v["ratings"].([]any)[0].(map[string]any)
	if tmdb["score"] != 7.8 || tmdb["scale"] != float64(10) || tmdb["votes"] != float64(12000) {
		t.Errorf("tmdb rating: %v", tmdb)
	}
	if r.omdb.Hits() != 0 {
		t.Errorf("OMDb was asked %d times without a key", r.omdb.Hits())
	}
	genres := v["genres"].([]any)
	if len(genres) != 2 || genres[0] != "Science Fiction" || v["runtimeMinutes"] != float64(155) || v["status"] != "Released" || v["language"] != "English" {
		t.Errorf("facts: %v", v)
	}
	cast := v["cast"].([]any)
	if len(cast) != 2 || cast[0].(map[string]any)["name"] != "Timothée Chalamet" || cast[0].(map[string]any)["character"] != "Paul Atreides" {
		t.Fatalf("cast in billing order: %v", cast)
	}
	if p := cast[0].(map[string]any)["profilePath"]; p != "/api/people/1190668/image" {
		t.Errorf("a cast member with a photo links its same-origin path: %v", p)
	}
	if _, has := cast[1].(map[string]any)["profilePath"]; has {
		t.Errorf("a cast member TMDB has no photo for links none: %v", cast[1])
	}
	similar := v["similar"].([]any)
	if len(similar) != 2 || similar[0].(map[string]any)["id"] != "movie-7" || similar[0].(map[string]any)["posterPath"] != "/api/images/poster/movie-7" {
		t.Errorf("similar titles are summaries with same-origin art: %v", similar)
	}
}

func TestAboutAddsIMDbAndRottenTomatoesWhenOMDbIsConfigured(t *testing.T) {
	r := startAbout(t, aboutTMDB(t), true)
	v := r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	if got := sources(v); strings.Join(got, ",") != "tmdb,imdb,rotten_tomatoes" {
		t.Fatalf("sources: %v", got)
	}
	imdb := v["ratings"].([]any)[1].(map[string]any)
	rt := v["ratings"].([]any)[2].(map[string]any)
	if imdb["score"] != 8.0 || imdb["scale"] != float64(10) || imdb["votes"] != float64(752119) {
		t.Errorf("imdb: %v", imdb)
	}
	if rt["score"] != float64(83) || rt["scale"] != float64(100) {
		t.Errorf("rotten tomatoes: %v", rt)
	}
	if _, has := rt["votes"]; has {
		t.Errorf("a percentage has no votes: %v", rt)
	}
}

func TestAboutNeverLeaksEitherKey(t *testing.T) {
	r := startAbout(t, aboutTMDB(t), true)
	r.omdb.Down(500)
	for _, p := range []string{"/api/titles/movie-438631/about", "/api/titles/movie-424242/about"} {
		got := r.call("GET", p, "")
		for _, leak := range []string{tmdbfake.Key, omdbfake.Key, "api_key", "apikey"} {
			if strings.Contains(string(got.body), leak) || strings.Contains(r.logs.String(), leak) {
				t.Errorf("%q leaks into the response or the logs of %s: %s", leak, p, got.body)
			}
		}
	}
}

func TestAboutOfASeriesTakesTheEpisodeRuntimeAndSkipsAnEmptyScore(t *testing.T) {
	tm := aboutTMDB(t)
	tm.Add(tmdbfake.Title{Kind: "series", ID: 5, Name: "Unrated", Date: "2026-01-01", Genres: []string{"Drama"}, Runtime: 0, Votes: 0})
	r := startAbout(t, tm, false)

	v := r.mustCall("GET", "/api/titles/series-1408/about", "", 200)
	if v["runtimeMinutes"] != float64(55) || v["status"] != "Returning Series" {
		t.Errorf("series facts: %v", v)
	}
	u := r.mustCall("GET", "/api/titles/series-5/about", "", 200)
	if len(u["ratings"].([]any)) != 0 {
		t.Errorf("a title nobody voted on has no score, not 0.0: %v", u["ratings"])
	}
	if _, has := u["runtimeMinutes"]; has {
		t.Errorf("an unknown runtime is left out: %v", u)
	}
	if len(u["cast"].([]any)) != 0 || len(u["similar"].([]any)) != 0 {
		t.Errorf("empty lists, not null: %v", u)
	}
}

func TestAboutCapsCastAndSimilarTitlesAndMarksTheOnesInTheLibrary(t *testing.T) {
	tm := aboutTMDB(t)
	var cast []tmdbfake.Person
	var similar []int
	for i := 1; i <= 30; i++ {
		cast = append(cast, tmdbfake.Person{ID: 1000 + i, Name: "Actor"})
		similar = append(similar, 2000+i)
		tm.Add(tmdbfake.Title{Kind: "movie", ID: 2000 + i, Name: "Similar", Date: "2020-01-01"})
	}
	tm.Replace(tmdbfake.Title{Kind: "movie", ID: 600, Name: "Big", Date: "2020-01-01", Cast: cast, Similar: similar})
	r := startAbout(t, tm, false)
	r.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":2001}`, 201)

	v := r.mustCall("GET", "/api/titles/movie-600/about", "", 200)
	if n := len(v["cast"].([]any)); n != 20 {
		t.Errorf("cast is capped at 20: %d", n)
	}
	list := v["similar"].([]any)
	if len(list) != 6 {
		t.Fatalf("similar is capped at 6: %d", len(list))
	}
	if first := list[0].(map[string]any); first["id"] != "movie-2001" || first["inLibrary"] != true {
		t.Errorf("a similar title already in the library says so: %v", first)
	}
	if second := list[1].(map[string]any); second["inLibrary"] != false {
		t.Errorf("another is not: %v", second)
	}
}

func TestAboutIsCachedForADayAndFetchedAgainAfterwards(t *testing.T) {
	tm := aboutTMDB(t)
	r := startAbout(t, tm, true)
	r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	tmdbHits, omdbHits := tm.Hits("/3/movie/438631"), r.omdb.Hits()
	if tmdbHits != 1 || omdbHits != 1 {
		t.Fatalf("one fetch each: tmdb %d, omdb %d", tmdbHits, omdbHits)
	}

	r.now.Store(today.Add(23 * time.Hour).UnixNano())
	r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	if tm.Hits("/3/movie/438631") != tmdbHits || r.omdb.Hits() != omdbHits {
		t.Errorf("a day-old answer is served from the cache: tmdb %d, omdb %d", tm.Hits("/3/movie/438631"), r.omdb.Hits())
	}

	r.now.Store(today.Add(25 * time.Hour).UnixNano())
	r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	if tm.Hits("/3/movie/438631") != tmdbHits+1 || r.omdb.Hits() != omdbHits+1 {
		t.Errorf("past a day both are asked again: tmdb %d, omdb %d", tm.Hits("/3/movie/438631"), r.omdb.Hits())
	}
}

func TestAboutAnswersWithTheStaleCopyWhenTMDBFailsAfterItExpired(t *testing.T) {
	tm := aboutTMDB(t)
	r := startAbout(t, tm, true)
	r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)

	r.now.Store(today.Add(48 * time.Hour).UnixNano())
	tm.Down("/3/movie/438631", 500)
	v := r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	if len(v["ratings"].([]any)) != 3 || len(v["cast"].([]any)) != 2 {
		t.Errorf("the stale copy is better than an error: %v", v)
	}
}

func TestAboutKeepsTheScoresItHadWhenOMDbFailsOnRefresh(t *testing.T) {
	tm := aboutTMDB(t)
	r := startAbout(t, tm, true)
	r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)

	r.now.Store(today.Add(48 * time.Hour).UnixNano())
	r.omdb.Down(503)
	v := r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	if got := sources(v); strings.Join(got, ",") != "tmdb,imdb,rotten_tomatoes" {
		t.Errorf("IMDb and Rotten Tomatoes must not vanish because OMDb blinked: %v", got)
	}
}

func TestAboutWithOMDbDownAnswersWithTMDBAndAsksOMDbAgainNextTime(t *testing.T) {
	r := startAbout(t, aboutTMDB(t), true)
	r.omdb.Down(503)
	v := r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	if got := sources(v); len(got) != 1 || got[0] != "tmdb" {
		t.Errorf("TMDB's score alone: %v", got)
	}
	r.omdb.Heal()
	v = r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	if len(sources(v)) != 3 {
		t.Errorf("a failed OMDb call is not cached as an answer: %v", v["ratings"])
	}
}

func TestAboutWithOMDbHangingStillAnswersWithinTheTimeout(t *testing.T) {
	om := omdbfake.New()
	t.Cleanup(om.Close)
	om.Hang()
	prep := func(env map[string]string) { env["MARQUEE_OMDB_KEY"] = omdbfake.Key }
	s := startRealWith(t, aboutTMDB(t), "", prep, func(o *app.Options) { o.OMDbBase, o.OMDbTimeout = om.URL(), 300*time.Millisecond })
	start := time.Now()
	v := s.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	if got := sources(v); len(got) != 1 || time.Since(start) > 3*time.Second {
		t.Errorf("sources %v after %v", got, time.Since(start))
	}
}

func TestAboutAnswersTMDBsFailuresInTheContractsShape(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*tmdbfake.Fake)
		want   int
	}{
		{"no such title", func(*tmdbfake.Fake) {}, 404},
		{"down", func(f *tmdbfake.Fake) { f.Down("/3/movie/438631", 500) }, 502},
		{"rate limited", func(f *tmdbfake.Fake) { f.Down("/3/movie/438631", 429) }, 503},
		{"refused key", func(f *tmdbfake.Fake) { f.Down("/3/movie/438631", 401) }, 502},
		{"hanging", func(f *tmdbfake.Fake) { f.Script("/3/movie/438631", tmdbfake.Step{Hang: true}) }, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tm := aboutTMDB(t)
			tc.break_(tm)
			r := startAbout(t, tm, true)
			id := "movie-438631"
			if tc.want == 404 {
				id = "movie-424242"
			}
			got := r.call("GET", "/api/titles/"+id+"/about", "")
			if got.status != tc.want || got.json(t)["error"] == "" {
				t.Errorf("%d %s, want %d", got.status, got.body, tc.want)
			}
			if r.omdb.Hits() != 0 {
				t.Errorf("OMDb is asked only once TMDB has answered: %d", r.omdb.Hits())
			}
		})
	}
}

func TestAboutRefusesAnIdThatIsNoTitle(t *testing.T) {
	r := startAbout(t, aboutTMDB(t), false)
	for _, p := range []string{"/api/titles/nonsense/about", "/api/titles/movie-0x/about"} {
		if got := r.bad("GET", p, ""); got.status != 404 {
			t.Errorf("%s: %d", p, got.status)
		}
	}
}

func TestAPersonsPhotoIsServedFromTheCacheAfterTheCastWasListed(t *testing.T) {
	tm := aboutTMDB(t)
	r := startAbout(t, tm, false)
	if got := r.bad("GET", "/api/people/1190668/image", ""); got.status != 404 {
		t.Fatalf("a person no title listed yet: %d", got.status)
	}
	r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)

	got := r.call("GET", "/api/people/1190668/image", "")
	if got.status != 200 || got.header.Get("Content-Type") != "image/jpeg" || string(got.body) != string(tmdbfake.ImageBytes("/timothee.jpg")) {
		t.Fatalf("photo: %d %s %q", got.status, got.header.Get("Content-Type"), got.body)
	}
	hits := tm.Hits("/t/p/")
	r.call("GET", "/api/people/1190668/image", "")
	if tm.Hits("/t/p/") != hits {
		t.Error("the photo is fetched from TMDB once, then served from disk")
	}
	if entries, _ := os.ReadDir(filepath.Join(r.env["MARQUEE_DATA_DIR"], "images")); len(entries) == 0 {
		t.Error("expected the photo in the image cache on disk")
	}
	if got := r.call("GET", "/api/people/3/image", ""); got.status != 404 {
		t.Errorf("a cast member with no photo has no image: %d", got.status)
	}
	if got := r.bad("GET", "/api/people/abc/image", ""); got.status != 404 {
		t.Errorf("a bad id: %d", got.status)
	}
}

func TestAPersonsPhotoFailsInTheContractsShapeWhenTMDBDoes(t *testing.T) {
	tm := aboutTMDB(t)
	r := startAbout(t, tm, false)
	r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	tm.Down("/t/p/", 500)
	if got := r.call("GET", "/api/people/1190668/image", ""); got.status != 502 || got.json(t)["error"] == "" {
		t.Errorf("%d %s", got.status, got.body)
	}
	tm.Heal()
	tm.Down("/t/p/", 404)
	if got := r.call("GET", "/api/people/1190668/image", ""); got.status != 404 {
		t.Errorf("a photo TMDB dropped: %d", got.status)
	}
}

func TestAboutOfATitleOMDbDoesNotHaveIsCachedWithTMDBsScoreAlone(t *testing.T) {
	tm := aboutTMDB(t)
	tm.Add(tmdbfake.Title{
		Kind: "movie", ID: 700, Name: "Obscure", Date: "2020-01-01", IMDb: "tt0000700", Score: 6.1, Votes: 40,
		Cast: []tmdbfake.Person{{ID: 7001, Name: "Unknown Actor", Profile: "/unknown.jpg"}},
	})
	r := startAbout(t, tm, true)

	v := r.mustCall("GET", "/api/titles/movie-700/about", "", 200)
	if got := sources(v); len(got) != 1 || got[0] != "tmdb" {
		t.Fatalf("a title OMDb lacks has TMDB's score alone: %v", got)
	}
	tmdbHits, omdbHits := tm.Hits("/3/movie/700"), r.omdb.Hits()
	r.mustCall("GET", "/api/titles/movie-700/about", "", 200)
	if tm.Hits("/3/movie/700") != tmdbHits || r.omdb.Hits() != omdbHits {
		t.Errorf("a title OMDb does not know is still cached: tmdb %d then %d, omdb %d then %d", tmdbHits, tm.Hits("/3/movie/700"), omdbHits, r.omdb.Hits())
	}
	if got := r.call("GET", "/api/people/7001/image", ""); got.status != 200 {
		t.Errorf("its cast photo: %d %s", got.status, got.body)
	}
}

func TestAboutWhenOMDbRefusesKeepsTMDBsPartAndAsksOMDbAgainLater(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*omdbfake.Fake)
	}{
		{"daily limit reached", (*omdbfake.Fake).Quota},
		{"key refused", (*omdbfake.Fake).RefuseKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tm := aboutTMDB(t)
			r := startAbout(t, tm, true)
			tc.break_(r.omdb)

			v := r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
			if got := sources(v); len(got) != 1 || got[0] != "tmdb" {
				t.Fatalf("TMDB's score alone: %v", got)
			}
			tmdbHits := tm.Hits("/3/movie/438631")
			r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
			if tm.Hits("/3/movie/438631") != tmdbHits {
				t.Errorf("TMDB's part is cached though OMDb refused: %d then %d calls", tmdbHits, tm.Hits("/3/movie/438631"))
			}
			if got := r.call("GET", "/api/people/1190668/image", ""); got.status != 200 {
				t.Errorf("cast photo: %d %s", got.status, got.body)
			}

			r.omdb.Heal()
			r.now.Store(today.Add(time.Hour).UnixNano())
			v = r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
			if len(sources(v)) != 3 {
				t.Errorf("OMDb is asked again well before the day is out: %v", v["ratings"])
			}
			for _, leak := range []string{omdbfake.Key, "apikey"} {
				if strings.Contains(r.logs.String(), leak) {
					t.Errorf("%q leaks into the logs", leak)
				}
			}
		})
	}
}

func TestAboutWhenOMDbGlitchesStoresNothingSoItsScoresComeBack(t *testing.T) {
	tm := aboutTMDB(t)
	r := startAbout(t, tm, true)
	r.omdb.Glitch()

	v := r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	if got := sources(v); len(got) != 1 || got[0] != "tmdb" {
		t.Fatalf("TMDB's score alone while OMDb glitches: %v", got)
	}
	r.omdb.Heal()
	v = r.mustCall("GET", "/api/titles/movie-438631/about", "", 200)
	if len(sources(v)) != 3 {
		t.Errorf("a passing fault must not hide OMDb's scores for the day: %v", v["ratings"])
	}
}
