package config_test

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/config"
)

const (
	manifestVal = "https://aio.example.test/stremio/uuid-SECRET123/token/manifest.json"
	tmdbVal     = "tmdb-SECRET456"
	jellyVal    = "jelly-SECRET789"
	tokenVal    = "api-SECRETabc"
	omdbVal     = "omdb-SECRETdef"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func fullEnv() map[string]string {
	return map[string]string{
		"MARQUEE_AIOSTREAMS_MANIFEST_URL": manifestVal,
		"MARQUEE_TMDB_KEY":                tmdbVal,
		"MARQUEE_MOVIES_PATH":             "/media/movies",
		"MARQUEE_SERIES_PATH":             "/media/series",
		"MARQUEE_TEMP_PATH":               "/media/tmp",
		"MARQUEE_DATA_DIR":                "/data",
		"MARQUEE_JELLYFIN_URL":            "http://jellyfin.example.test:8096",
		"MARQUEE_JELLYFIN_KEY":            jellyVal,
		"MARQUEE_API_TOKEN":               tokenVal,
		"MARQUEE_OMDB_KEY":                omdbVal,
	}
}

func assertNoSecrets(t *testing.T, what, s string) {
	t.Helper()
	for _, secret := range []string{"SECRET123", "SECRET456", "SECRET789", "SECRETabc", "SECRETdef"} {
		if strings.Contains(s, secret) {
			t.Errorf("%s leaks a secret: %q", what, s)
		}
	}
}

func TestLoadFromEnvironment(t *testing.T) {
	cfg, err := config.Load(envOf(fullEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MoviesPath != "/media/movies" || cfg.Listen != ":8080" || cfg.DryRun {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if cfg.TMDBKey.Reveal() != tmdbVal {
		t.Error("secret value must stay usable through Reveal")
	}
}

func TestTheOMDbKeyIsOptionalAndComesFromEnvironmentOrFile(t *testing.T) {
	env := fullEnv()
	delete(env, "MARQUEE_OMDB_KEY")
	cfg, err := config.Load(envOf(env))
	if err != nil {
		t.Fatalf("a missing OMDb key must not stop startup: %v", err)
	}
	if cfg.OMDbKey != "" {
		t.Errorf("no key was given: %+v", cfg)
	}

	if cfg, err = config.Load(envOf(fullEnv())); err != nil || cfg.OMDbKey.Reveal() != omdbVal {
		t.Errorf("env key: %v %v", cfg.OMDbKey, err)
	}

	file := filepath.Join(t.TempDir(), "marquee.json")
	body := `{"aiostreams_manifest_url":"` + manifestVal + `","tmdb_key":"` + tmdbVal + `",
	"movies_path":"/a","series_path":"/b","temp_path":"/c","data_dir":"/d","omdb_key":"` + omdbVal + `"}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = config.Load(envOf(map[string]string{"MARQUEE_CONFIG": file})); err != nil || cfg.OMDbKey.Reveal() != omdbVal {
		t.Errorf("file key: %v %v", cfg.OMDbKey, err)
	}
	if cfg, err = config.Load(envOf(map[string]string{"MARQUEE_CONFIG": file, "MARQUEE_OMDB_KEY": "other"})); err != nil || cfg.OMDbKey.Reveal() != "other" {
		t.Errorf("env must override the file: %v %v", cfg.OMDbKey, err)
	}
}

func TestLoadFromFileWithEnvironmentOverride(t *testing.T) {
	file := filepath.Join(t.TempDir(), "marquee.json")
	body := `{"aiostreams_manifest_url":"` + manifestVal + `","tmdb_key":"` + tmdbVal + `",
	"movies_path":"/a","series_path":"/b","temp_path":"/c","data_dir":"/d","dry_run":true,"listen":":9000"}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(envOf(map[string]string{"MARQUEE_CONFIG": file, "MARQUEE_LISTEN": ":9100"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MoviesPath != "/a" || !cfg.DryRun {
		t.Errorf("file values not loaded: %+v", cfg)
	}
	if cfg.Listen != ":9100" {
		t.Errorf("environment must override the file, got %q", cfg.Listen)
	}
}

func TestMissingEssentialsAreNamed(t *testing.T) {
	for _, tc := range []struct{ env, key string }{
		{"MARQUEE_AIOSTREAMS_MANIFEST_URL", "aiostreams_manifest_url"},
		{"MARQUEE_TMDB_KEY", "tmdb_key"},
		{"MARQUEE_MOVIES_PATH", "movies_path"},
		{"MARQUEE_SERIES_PATH", "series_path"},
		{"MARQUEE_TEMP_PATH", "temp_path"},
		{"MARQUEE_DATA_DIR", "data_dir"},
	} {
		t.Run(tc.env, func(t *testing.T) {
			env := fullEnv()
			delete(env, tc.env)
			_, err := config.Load(envOf(env))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.env) || !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error must name the setting, got %q", err)
			}
			assertNoSecrets(t, "error", err.Error())
		})
	}
}

func TestInvalidValuesNeverEchoSecrets(t *testing.T) {
	env := fullEnv()
	env["MARQUEE_AIOSTREAMS_MANIFEST_URL"] = "ht!tp://SECRET123"
	_, err := config.Load(envOf(env))
	if err == nil {
		t.Fatal("want an error for an invalid manifest URL")
	}
	if !strings.Contains(err.Error(), "MARQUEE_AIOSTREAMS_MANIFEST_URL") {
		t.Errorf("error must name the setting, got %q", err)
	}
	assertNoSecrets(t, "error", err.Error())

	env = fullEnv()
	env["MARQUEE_DRY_RUN"] = "SECRET123"
	_, err = config.Load(envOf(env))
	if err == nil || !strings.Contains(err.Error(), "MARQUEE_DRY_RUN") {
		t.Fatalf("want an error naming MARQUEE_DRY_RUN, got %v", err)
	}
	assertNoSecrets(t, "error", err.Error())
}

func TestMalformedConfigFileErrorNamesNoSecret(t *testing.T) {
	file := filepath.Join(t.TempDir(), "marquee.json")
	if err := os.WriteFile(file, []byte(`{"tmdb_key": SECRET456}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(envOf(map[string]string{"MARQUEE_CONFIG": file}))
	if err == nil {
		t.Fatal("want an error for a malformed file")
	}
	assertNoSecrets(t, "error", err.Error())
}

func TestNonJSONConfigFileErrorLeaksNoContent(t *testing.T) {
	content := "QJ#Z%K^W~9&Y@V!U"
	file := filepath.Join(t.TempDir(), "marquee.json")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(envOf(map[string]string{"MARQUEE_CONFIG": file}))
	if err == nil {
		t.Fatal("want an error for a non-JSON file")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not a valid config file") {
		t.Errorf("error must say the config file is invalid, got %q", msg)
	}
	if strings.Contains(msg, "invalid character") {
		t.Errorf("error must not carry the decoder's character message, got %q", msg)
	}
	for i := 0; i < len(content); i++ {
		for j := i + 2; j <= len(content); j++ {
			if strings.Contains(msg, content[i:j]) {
				t.Errorf("error leaks file content %q: %q", content[i:j], msg)
			}
		}
	}
}

func TestSecretsAreRedactedEverywhere(t *testing.T) {
	cfg, err := config.Load(envOf(fullEnv()))
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecrets(t, "%v", fmt.Sprintf("%v", cfg))
	assertNoSecrets(t, "%+v", fmt.Sprintf("%+v", cfg))
	assertNoSecrets(t, "%#v", fmt.Sprintf("%#v", cfg))
	assertNoSecrets(t, "%s", cfg.TMDBKey.String())

	var buf strings.Builder
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config", "config", cfg)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("config", "config", cfg)
	assertNoSecrets(t, "slog", buf.String())
}

func TestCheckPathsRefusesTempOnAnotherFilesystem(t *testing.T) {
	if _, err := os.Stat("/proc/self"); err != nil {
		t.Skip("no /proc to stand in for a second filesystem")
	}
	for name, libs := range map[string][2]string{
		"movies library elsewhere": {"/proc", ""},
		"series library elsewhere": {"", "/proc"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cfg := config.Config{
				MoviesPath: root, SeriesPath: root,
				TempPath: filepath.Join(root, "tmp"), DataDir: filepath.Join(root, "data"),
			}
			if libs[0] != "" {
				cfg.MoviesPath = libs[0]
			}
			if libs[1] != "" {
				cfg.SeriesPath = libs[1]
			}
			err := cfg.CheckPaths()
			if err == nil {
				t.Fatal("want a refusal")
			}
			if !strings.Contains(err.Error(), "MARQUEE_TEMP_PATH") || !strings.Contains(err.Error(), "filesystem") {
				t.Errorf("unclear message: %q", err)
			}
		})
	}
}

func TestCheckPathsAcceptsSameFilesystemAndCreatesDirs(t *testing.T) {
	root := t.TempDir()
	movies, series := filepath.Join(root, "movies"), filepath.Join(root, "series")
	for _, d := range []string{movies, series} {
		if err := os.Mkdir(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{
		MoviesPath: movies, SeriesPath: series,
		TempPath: filepath.Join(root, "tmp"), DataDir: filepath.Join(root, "data"),
	}
	if err := cfg.CheckPaths(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{cfg.TempPath, cfg.DataDir} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Errorf("%s should have been created", d)
		}
	}
}

func TestCheckPathsRefusesMissingLibrary(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		MoviesPath: filepath.Join(root, "nope"), SeriesPath: root, TempPath: filepath.Join(root, "tmp"), DataDir: filepath.Join(root, "data"),
	}
	err := cfg.CheckPaths()
	if err == nil || !strings.Contains(err.Error(), "MARQUEE_MOVIES_PATH") {
		t.Fatalf("want an error naming MARQUEE_MOVIES_PATH, got %v", err)
	}
}

func TestTheRescanIntervalDefaultsToThirtyMinutesAndComesFromEnvironmentOrFile(t *testing.T) {
	cfg, err := config.Load(envOf(fullEnv()))
	if err != nil || cfg.RescanInterval != 30*time.Minute {
		t.Fatalf("default: %v, %v", cfg.RescanInterval, err)
	}

	env := fullEnv()
	env["MARQUEE_RESCAN_INTERVAL"] = "90s"
	if cfg, err = config.Load(envOf(env)); err != nil || cfg.RescanInterval != 90*time.Second {
		t.Errorf("env: %v, %v", cfg.RescanInterval, err)
	}

	file := filepath.Join(t.TempDir(), "marquee.json")
	body := `{"aiostreams_manifest_url":"` + manifestVal + `","tmdb_key":"` + tmdbVal + `",
	"movies_path":"/a","series_path":"/b","temp_path":"/c","data_dir":"/d","rescan_interval":"2h"}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = config.Load(envOf(map[string]string{"MARQUEE_CONFIG": file})); err != nil || cfg.RescanInterval != 2*time.Hour {
		t.Errorf("file: %v, %v", cfg.RescanInterval, err)
	}
	if cfg, err = config.Load(envOf(map[string]string{"MARQUEE_CONFIG": file, "MARQUEE_RESCAN_INTERVAL": "10m"})); err != nil || cfg.RescanInterval != 10*time.Minute {
		t.Errorf("env overrides the file: %v, %v", cfg.RescanInterval, err)
	}
}

func TestARescanIntervalOfZeroDisablesTheTimerAndABadOneStopsStartup(t *testing.T) {
	env := fullEnv()
	env["MARQUEE_RESCAN_INTERVAL"] = "0"
	if cfg, err := config.Load(envOf(env)); err != nil || cfg.RescanInterval != 0 {
		t.Errorf("zero disables the timer: %v, %v", cfg.RescanInterval, err)
	}
	for _, bad := range []string{"soon", "30", "-5m"} {
		env["MARQUEE_RESCAN_INTERVAL"] = bad
		_, err := config.Load(envOf(env))
		if err == nil || !strings.Contains(err.Error(), "MARQUEE_RESCAN_INTERVAL") {
			t.Errorf("%q: error %v must name the setting", bad, err)
		}
	}
}

const torboxVal = "torbox-SECRETghi"

func TestTorBoxSettingsDefaultToCachedOnlyAndComeFromEnvironmentOrFile(t *testing.T) {
	cfg, err := config.Load(envOf(fullEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TorBoxAPIKey != "" || cfg.TorBoxMaxActive != 2 || cfg.TorBoxStallTimeout != 30*time.Minute ||
		cfg.TorBoxMaxLifetime != 4*time.Hour || !cfg.TorBoxDeleteOnComplete {
		t.Errorf("defaults: %+v", cfg)
	}

	env := fullEnv()
	env["MARQUEE_TORBOX_API_KEY"] = torboxVal
	env["MARQUEE_TORBOX_MAX_ACTIVE"] = "3"
	env["MARQUEE_TORBOX_STALL_TIMEOUT"] = "10m"
	env["MARQUEE_TORBOX_MAX_LIFETIME"] = "90m"
	env["MARQUEE_TORBOX_DELETE_ON_COMPLETE"] = "false"
	cfg, err = config.Load(envOf(env))
	if err != nil || cfg.TorBoxAPIKey.Reveal() != torboxVal || cfg.TorBoxMaxActive != 3 ||
		cfg.TorBoxStallTimeout != 10*time.Minute || cfg.TorBoxMaxLifetime != 90*time.Minute || cfg.TorBoxDeleteOnComplete {
		t.Fatalf("env: %+v, %v", cfg, err)
	}

	file := filepath.Join(t.TempDir(), "marquee.json")
	body := `{"aiostreams_manifest_url":"` + manifestVal + `","tmdb_key":"` + tmdbVal + `",
	"movies_path":"/a","series_path":"/b","temp_path":"/c","data_dir":"/d","torbox_api_key":"` + torboxVal + `",
	"torbox_max_active":4,"torbox_stall_timeout":"20m","torbox_max_lifetime":"2h","torbox_delete_on_complete":false}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(envOf(map[string]string{"MARQUEE_CONFIG": file}))
	if err != nil || cfg.TorBoxMaxActive != 4 || cfg.TorBoxStallTimeout != 20*time.Minute || cfg.TorBoxMaxLifetime != 2*time.Hour || cfg.TorBoxDeleteOnComplete {
		t.Fatalf("file: %+v, %v", cfg, err)
	}
	cfg, err = config.Load(envOf(map[string]string{"MARQUEE_CONFIG": file, "MARQUEE_TORBOX_MAX_ACTIVE": "1", "MARQUEE_TORBOX_DELETE_ON_COMPLETE": "true"}))
	if err != nil || cfg.TorBoxMaxActive != 1 || !cfg.TorBoxDeleteOnComplete {
		t.Errorf("env overrides the file: %+v, %v", cfg, err)
	}
}

func TestBadTorBoxSettingsStopStartupNamingTheSetting(t *testing.T) {
	for env, value := range map[string]string{
		"MARQUEE_TORBOX_MAX_ACTIVE":         "0",
		"MARQUEE_TORBOX_STALL_TIMEOUT":      "soon",
		"MARQUEE_TORBOX_MAX_LIFETIME":       "0s",
		"MARQUEE_TORBOX_DELETE_ON_COMPLETE": "maybe",
	} {
		e := fullEnv()
		e[env] = value
		_, err := config.Load(envOf(e))
		if err == nil || !strings.Contains(err.Error(), env) {
			t.Errorf("%s=%s: want an error naming it, got %v", env, value, err)
		}
	}
}

func TestTheTorBoxKeyIsRedactedEverywhere(t *testing.T) {
	env := fullEnv()
	env["MARQUEE_TORBOX_API_KEY"] = torboxVal
	cfg, err := config.Load(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	slog.New(slog.NewTextHandler(&buf, nil)).Info("config", "config", cfg)
	for _, s := range []string{buf.String(), fmt.Sprintf("%v %+v %#v", cfg, cfg, cfg)} {
		if strings.Contains(s, "SECRETghi") {
			t.Errorf("leaks the TorBox key: %q", s)
		}
	}
}
