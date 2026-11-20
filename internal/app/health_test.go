package app_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreamsfake"
	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/jellyfinfake"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

type healthRig struct {
	*realSvc
	aio *aiostreamsfake.Fake
	jf  *jellyfinfake.Server
}

// startHealth starts the service with every outside dependency a fake and
// healthy; a test then breaks one.
func startHealth(t *testing.T, opts ...svcOpt) *healthRig {
	t.Helper()
	aio := aiostreamsfake.New()
	t.Cleanup(aio.Close)
	jf := jellyfinfake.New()
	t.Cleanup(jf.Close)
	s := startRealWith(t, newTMDB(t), aio.ManifestURL(), func(env map[string]string) {
		env["MARQUEE_JELLYFIN_URL"] = jf.URL
		env["MARQUEE_JELLYFIN_KEY"] = jellyfinfake.Key
	}, append([]svcOpt{func(o *app.Options) { o.HealthTimeout = time.Second }}, opts...)...)
	return &healthRig{realSvc: s, aio: aio, jf: jf}
}

func (r *healthRig) health() map[string]any { return r.mustCall("GET", "/api/health", "", 200) }

func (r *healthRig) check(id string) map[string]any {
	r.t.Helper()
	h := r.health()
	for _, c := range h["checks"].([]any) {
		if m := c.(map[string]any); m["id"] == id {
			return m
		}
	}
	r.t.Fatalf("health lists no %q check: %v", id, h["checks"])
	return nil
}

func (r *healthRig) wantCheck(id, status, words string) {
	r.t.Helper()
	c := r.check(id)
	if c["status"] != status || !strings.Contains(strings.ToLower(c["message"].(string)), strings.ToLower(words)) {
		r.t.Errorf("%s check = %v, want status %s and a message saying %q", id, c, status, words)
	}
}

// noSecretsOnHealth reads Health in its current state and checks that nothing
// the owner configured appears in it or in the logs.
func (r *healthRig) noSecretsOnHealth(when string) {
	r.t.Helper()
	body := string(r.call("GET", "/api/health", "").body)
	for _, secret := range []string{"SECRET123", "PASS789", aiostreamsfake.User, aiostreamsfake.Password, r.aio.Host(), tmdbfake.Key, jellyfinfake.Key, "SECRETabc"} {
		if strings.Contains(body, secret) || strings.Contains(r.logs.String(), secret) {
			r.t.Errorf("%s: %q leaks into Health or the logs:\n%s", when, secret, body)
		}
	}
	// The Jellyfin address is a plain setting, not a secret, but Health has no need to repeat it.
	if strings.Contains(body, r.jf.URL) {
		r.t.Errorf("%s: Health repeats the Jellyfin address:\n%s", when, body)
	}
}

func TestHealthIsOkWhenEveryDependencyIsFine(t *testing.T) {
	r := startHealth(t)
	h := r.health()
	if h["status"] != "ok" {
		t.Fatalf("health = %v", h)
	}
	for _, id := range []string{"aiostreams", "tmdb", "library_paths", "temp_path", "jellyfin"} {
		if c := r.check(id); c["status"] != "ok" {
			t.Errorf("%s = %v", id, c)
		}
	}
	r.noSecretsOnHealth("healthy")
}

func TestHealthNamesAnAIOStreamsOutageBadCredentialsAndRateLimiting(t *testing.T) {
	r := startHealth(t)

	r.aio.Fail(aiostreamsfake.Step{Status: 503})
	r.wantCheck("aiostreams", "error", "not answering")
	r.noSecretsOnHealth("AIOStreams down")

	r.aio.Fail(aiostreamsfake.Step{Status: 401})
	r.wantCheck("aiostreams", "error", "refused")
	r.noSecretsOnHealth("AIOStreams refusing")

	r.aio.Fail(aiostreamsfake.Step{Status: 429})
	r.wantCheck("aiostreams", "warning", "rate-limiting")

	r.aio.Fail(aiostreamsfake.Step{Hang: true})
	r.wantCheck("aiostreams", "error", "in time")

	r.aio.Heal()
	r.wantCheck("aiostreams", "ok", "reachable")
	if got := r.health()["status"]; got != "ok" {
		t.Errorf("status after healing = %v", got)
	}
}

func TestHealthNamesAnAIOStreamsManifestWithTheWrongPassword(t *testing.T) {
	aio := aiostreamsfake.New()
	defer aio.Close()
	manifest := strings.Replace(aio.ManifestURL(), aiostreamsfake.Password, "not-the-password", 1)
	r := &healthRig{realSvc: startRealAIO(t, newTMDB(t), manifest), aio: aio, jf: jellyfinfake.New()}
	defer r.jf.Close()
	r.wantCheck("aiostreams", "error", "refused")
	if h := r.health(); h["status"] != "error" {
		t.Errorf("one failing check must make the whole status an error: %v", h)
	}
	for _, s := range []string{"not-the-password", aiostreamsfake.Password} {
		if strings.Contains(string(r.call("GET", "/api/health", "").body), s) || strings.Contains(r.logs.String(), s) {
			t.Errorf("%q leaks", s)
		}
	}
}

func TestHealthNamesATMDBKeyThatIsRefusedAndATMDBOutage(t *testing.T) {
	r := startHealth(t)

	r.tmdb.Down("", 401)
	r.wantCheck("tmdb", "error", "refused")
	r.noSecretsOnHealth("TMDB refusing the key")

	r.tmdb.Heal()
	r.tmdb.Down("", 503)
	r.wantCheck("tmdb", "error", "not answering")

	r.tmdb.Heal()
	r.wantCheck("tmdb", "ok", "accepted")
}

func TestHealthNamesALibraryFolderThatIsGoneOrCannotBeWritten(t *testing.T) {
	r := startHealth(t)
	movies, series := r.env["MARQUEE_MOVIES_PATH"], r.env["MARQUEE_SERIES_PATH"]

	if os.Geteuid() != 0 {
		// A read-only library is read, not written, so Health must not need to create a file to know.
		if err := os.Chmod(series, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(series, 0o750) })
		r.wantCheck("library_paths", "error", "cannot write")
		if c := r.check("library_paths"); !strings.Contains(c["message"].(string), "series") || strings.Contains(c["message"].(string), "movies") {
			t.Errorf("the message must name the series folder only: %v", c)
		}
		if got := tree(t, series); len(got) != 0 {
			t.Errorf("Health wrote into the library: %v", got)
		}
		_ = os.Chmod(series, 0o750)
	}

	if err := os.RemoveAll(movies); err != nil {
		t.Fatal(err)
	}
	r.wantCheck("library_paths", "error", "movies folder")
	if got := r.health()["status"]; got != "error" {
		t.Errorf("status = %v", got)
	}
	r.noSecretsOnHealth("movies folder gone")

	if err := os.Mkdir(movies, 0o750); err != nil {
		t.Fatal(err)
	}
	r.wantCheck("library_paths", "ok", "readable and writable")
}

func TestHealthNamesATempFolderThatIsGoneOrCannotBeWritten(t *testing.T) {
	r := startHealth(t)
	temp := r.env["MARQUEE_TEMP_PATH"]
	r.wantCheck("temp_path", "ok", "same filesystem")

	if os.Geteuid() != 0 {
		if err := os.Chmod(temp, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(temp, 0o750) })
		r.wantCheck("temp_path", "error", "cannot write")
		_ = os.Chmod(temp, 0o750)
	}

	if err := os.RemoveAll(temp); err != nil {
		t.Fatal(err)
	}
	r.wantCheck("temp_path", "error", "does not exist")
	if _, err := os.Stat(temp); err == nil {
		t.Error("Health must not create the temp folder")
	}
}

func TestHealthNamesJellyfinBeingUnreachable(t *testing.T) {
	r := startHealth(t)
	r.wantCheck("jellyfin", "ok", "reachable")
	r.jf.Close()
	r.wantCheck("jellyfin", "error", "unreachable")
	if got := r.health()["status"]; got != "error" {
		t.Errorf("status = %v", got)
	}
	r.noSecretsOnHealth("Jellyfin down")
}

func TestHealthHasNoJellyfinCheckWhenJellyfinIsNotConfigured(t *testing.T) {
	aio := aiostreamsfake.New()
	defer aio.Close()
	s := startRealAIO(t, newTMDB(t), aio.ManifestURL())
	for _, c := range s.mustCall("GET", "/api/health", "", 200)["checks"].([]any) {
		if c.(map[string]any)["id"] == "jellyfin" {
			t.Errorf("Jellyfin is optional and not set up, yet Health lists it: %v", c)
		}
	}
}

func TestHealthSaysWhenDryRunIsOn(t *testing.T) {
	aio := aiostreamsfake.New()
	defer aio.Close()
	s := startRealWith(t, newTMDB(t), aio.ManifestURL(), func(env map[string]string) { env["MARQUEE_DRY_RUN"] = "true" })
	r := &healthRig{realSvc: s, aio: aio, jf: jellyfinfake.New()}
	defer r.jf.Close()
	r.wantCheck("dry_run", "warning", "never downloads")
	if h := r.health(); h["status"] != "warning" || h["dryRun"] != true {
		t.Errorf("health = %v", h)
	}
}

func libraryTotals(t *testing.T, s *realSvc) (movies, episodes float64) {
	t.Helper()
	totals, ok := s.mustCall("GET", "/api/health", "", 200)["libraryTotals"].(map[string]any)
	if !ok {
		t.Fatal("health carries no libraryTotals")
	}
	return totals["movies"].(float64), totals["episodes"].(float64)
}

func TestHealthCountsTheLibraryMoviesAndEpisodesAcrossSeries(t *testing.T) {
	tm := newTMDB(t)
	tm.Add(tmdbfake.Title{Kind: "series", ID: 2000, Name: "Second Show", Date: "2020-01-01", Overview: "More.", IMDb: "tt2000000",
		Seasons: []tmdbfake.Season{
			{Number: 1, Episodes: []tmdbfake.Episode{{Number: 1, Name: "One", AirDate: "2020-01-01"}, {Number: 2, Name: "Two", AirDate: "2020-01-08"}}},
			{Number: 2, Episodes: []tmdbfake.Episode{{Number: 1, Name: "Three", AirDate: "2021-01-01"}}},
		}})
	s := startReal(t, tm)

	if m, e := libraryTotals(t, s); m != 0 || e != 0 {
		t.Errorf("an empty library counts %v movies and %v episodes, want 0 and 0", m, e)
	}

	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":7}`, 201)
	s.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`, 201)
	s.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":2000}`, 201)

	if m, e := libraryTotals(t, s); m != 2 || e != 6 {
		t.Errorf("library counts %v movies and %v episodes, want 2 and 6 (3 dated episodes in each series)", m, e)
	}

	s.mustCall("DELETE", "/api/titles/movie-7", "", 204)
	s.mustCall("DELETE", "/api/titles/series-1408", "", 204)
	if m, e := libraryTotals(t, s); m != 1 || e != 3 {
		t.Errorf("after removals the library counts %v movies and %v episodes, want 1 and 3", m, e)
	}
}
