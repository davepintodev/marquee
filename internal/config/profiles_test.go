package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/davepintodev/marquee/internal/config"
)

// loadWith loads a config file holding the essentials plus extra JSON
// members (without surrounding braces), with env on top.
func loadWith(t *testing.T, extra string, env map[string]string) (config.Config, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "marquee.json")
	body := `{"aiostreams_manifest_url":"` + manifestVal + `","tmdb_key":"` + tmdbVal + `",
	"movies_path":"/a","series_path":"/b","temp_path":"/c","data_dir":"/d"`
	if extra != "" {
		body += "," + extra
	}
	body += "}"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	e := map[string]string{"MARQUEE_CONFIG": file}
	for k, v := range env {
		e[k] = v
	}
	return config.Load(envOf(e))
}

func TestOmittedProfilesLoadTheBuiltInTVSafeDefault(t *testing.T) {
	for name, extra := range map[string]string{
		"omitted": "",
		"empty":   `"profiles":{}`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadWith(t, extra, nil)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DefaultMovieProfile != "default" || cfg.DefaultSeriesProfile != "default" {
				t.Errorf("defaults = %q, %q", cfg.DefaultMovieProfile, cfg.DefaultSeriesProfile)
			}
			if len(cfg.Profiles) != 1 {
				t.Fatalf("want exactly the built-in profile, got %v", cfg.Profiles)
			}
			want := config.Profile{
				Qualities: []string{
					"WEBDL-2160p", "WEBRip-2160p", "Bluray-2160p",
					"WEBDL-1080p", "WEBRip-1080p", "Bluray-1080p", "HDTV-1080p",
					"WEBDL-720p", "WEBRip-720p", "Bluray-720p", "HDTV-720p",
				},
				Reject: []string{"truehd", "dts-hd", "dts-x", "dv-p7"},
				Prefer: []string{"dv", "hdr10+", "dd+-atmos", "dd+", "dts", "dd"},
			}
			if got := cfg.Profiles["default"]; !reflect.DeepEqual(got, want) {
				t.Errorf("built-in profile = %+v, want %+v", got, want)
			}
		})
	}
}

func TestTheBuiltInDefaultIsNotSharedBetweenLoads(t *testing.T) {
	a, err := loadWith(t, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	a.Profiles["default"].Qualities[0] = "CAM"
	b, err := loadWith(t, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Profiles["default"].Qualities[0] != "WEBDL-2160p" {
		t.Error("a caller's edit leaked into the next load")
	}
}

func TestCustomProfilesLoadWithTheirDefaults(t *testing.T) {
	cfg, err := loadWith(t, `"profiles":{
		"default":{"qualities":["WEBDL-1080p"],"reject":["truehd"],"prefer":["dv"]},
		"anime":{"qualities":["Bluray-1080p","WEBDL-1080p"],"reject":[],"prefer":["flac","av1"],"size_limits":{"min_mb":100,"max_mb":4000}},
		"movies":{"qualities":["Remux-2160p"],"reject":[],"prefer":[]}
	}`, map[string]string{"MARQUEE_DEFAULT_SERIES_PROFILE": "anime"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultMovieProfile != "default" {
		t.Errorf("an omitted default falls back to the profile named default, got %q", cfg.DefaultMovieProfile)
	}
	if cfg.DefaultSeriesProfile != "anime" {
		t.Errorf("env must set the series default, got %q", cfg.DefaultSeriesProfile)
	}
	anime := cfg.Profiles["anime"]
	if anime.SizeLimits.MinMB != 100 || anime.SizeLimits.MaxMB != 4000 || !reflect.DeepEqual(anime.Prefer, []string{"flac", "av1"}) {
		t.Errorf("anime = %+v", anime)
	}
	if cfg.Profiles["movies"].SizeLimits != (config.SizeLimits{}) {
		t.Errorf("size_limits is optional: %+v", cfg.Profiles["movies"])
	}
}

func TestDefaultsComeFromTheFileAndEnvironmentOverridesThem(t *testing.T) {
	profiles := `"profiles":{
		"a":{"qualities":["WEBDL-1080p"],"reject":[],"prefer":[]},
		"b":{"qualities":["WEBDL-720p"],"reject":[],"prefer":[]}},
		"default_movie_profile":"a","default_series_profile":"b"`
	cfg, err := loadWith(t, profiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultMovieProfile != "a" || cfg.DefaultSeriesProfile != "b" {
		t.Errorf("file defaults = %q, %q", cfg.DefaultMovieProfile, cfg.DefaultSeriesProfile)
	}
	cfg, err = loadWith(t, profiles, map[string]string{"MARQUEE_DEFAULT_MOVIE_PROFILE": "b", "MARQUEE_DEFAULT_SERIES_PROFILE": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultMovieProfile != "b" || cfg.DefaultSeriesProfile != "a" {
		t.Errorf("env defaults = %q, %q", cfg.DefaultMovieProfile, cfg.DefaultSeriesProfile)
	}
}

func TestInvalidProfilesStopStartupNamingTheSetting(t *testing.T) {
	good := `"qualities":["WEBDL-1080p"],"reject":[],"prefer":[]`
	for _, tc := range []struct {
		name  string
		extra string
		env   map[string]string
		want  []string
	}{
		{"unknown quality", `"profiles":{"default":{"qualities":["WEBDL-1080p","Ultra-9000p"],"reject":[],"prefer":[]}}`,
			nil, []string{"profiles.default.qualities", "Ultra-9000p"}},
		{"quality names are case sensitive", `"profiles":{"default":{"qualities":["webdl-1080p"],"reject":[],"prefer":[]}}`,
			nil, []string{"profiles.default.qualities", "webdl-1080p"}},
		{"empty qualities", `"profiles":{"default":{"qualities":[],"reject":[],"prefer":[]}}`,
			nil, []string{"profiles.default.qualities", "at least one"}},
		{"missing qualities", `"profiles":{"default":{"reject":[],"prefer":[]}}`,
			nil, []string{"profiles.default.qualities", "at least one"}},
		{"unknown reject tag", `"profiles":{"default":{"qualities":["WEBDL-1080p"],"reject":["atmos"],"prefer":[]}}`,
			nil, []string{"profiles.default.reject", "atmos"}},
		{"unknown prefer tag", `"profiles":{"default":{"qualities":["WEBDL-1080p"],"reject":[],"prefer":["dolby"]}}`,
			nil, []string{"profiles.default.prefer", "dolby"}},
		{"empty profile name", `"profiles":{"":{` + good + `}}`,
			nil, []string{"profiles", "name"}},
		{"negative min", `"profiles":{"default":{` + good + `,"size_limits":{"min_mb":-1}}}`,
			nil, []string{"profiles.default.size_limits", "min_mb"}},
		{"negative max", `"profiles":{"default":{` + good + `,"size_limits":{"max_mb":-5}}}`,
			nil, []string{"profiles.default.size_limits", "max_mb"}},
		{"min above max", `"profiles":{"default":{` + good + `,"size_limits":{"min_mb":500,"max_mb":100}}}`,
			nil, []string{"profiles.default.size_limits", "min_mb", "max_mb"}},
		{"unknown profile field", `"profiles":{"default":{` + good + `,"cutoff":"WEBDL-1080p"}}`,
			nil, []string{"cutoff"}},
		{"unknown size_limits field", `"profiles":{"default":{` + good + `,"size_limits":{"mb":1}}}`,
			nil, []string{"mb"}},
		{"movie default undefined", `"profiles":{"default":{` + good + `}},"default_movie_profile":"nope"`,
			nil, []string{"MARQUEE_DEFAULT_MOVIE_PROFILE", "default_movie_profile", "nope"}},
		{"series default undefined", `"profiles":{"default":{` + good + `}}`,
			map[string]string{"MARQUEE_DEFAULT_SERIES_PROFILE": "nope"},
			[]string{"MARQUEE_DEFAULT_SERIES_PROFILE", "default_series_profile", "nope"}},
		{"no profile named default and no default chosen", `"profiles":{"custom":{` + good + `}}`,
			nil, []string{"MARQUEE_DEFAULT_MOVIE_PROFILE", "default"}},
		{"only the series default is missing", `"profiles":{"custom":{` + good + `}},"default_movie_profile":"custom"`,
			nil, []string{"MARQUEE_DEFAULT_SERIES_PROFILE", "default"}},
		{"a default with the built-in profile only", "",
			map[string]string{"MARQUEE_DEFAULT_MOVIE_PROFILE": "custom"},
			[]string{"MARQUEE_DEFAULT_MOVIE_PROFILE", "custom"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadWith(t, tc.extra, tc.env)
			if err == nil {
				t.Fatal("expected a startup error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q should mention %q", err, w)
				}
			}
			assertNoSecrets(t, "the error", err.Error())
		})
	}
}

func TestEveryDocumentedQualityAndTagIsAccepted(t *testing.T) {
	quals := `"` + strings.Join(config.QualityTiers(), `","`) + `"`
	tags := `"` + strings.Join(config.FormatTags(), `","`) + `"`
	_, err := loadWith(t, `"profiles":{"default":{"qualities":[`+quals+`],"reject":[`+tags+`],"prefer":[`+tags+`]}}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(config.QualityTiers()); got != 25 {
		t.Errorf("want 25 canonical tiers, got %d", got)
	}
	if got := len(config.FormatTags()); got != 18 {
		t.Errorf("want 18 format tags, got %d", got)
	}
}

func TestProfilesAreNotSettableFromTheEnvironment(t *testing.T) {
	env := fullEnv()
	env["MARQUEE_PROFILES"] = `{"x":{}}`
	cfg, err := config.Load(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Profiles["x"]; ok || len(cfg.Profiles) != 1 {
		t.Errorf("profiles come from the file only: %v", cfg.Profiles)
	}
}
