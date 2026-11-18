package config_test

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/davepintodev/marquee/internal/config"
)

const (
	manifestVal = "https://aio.example.test/stremio/uuid-SECRET123/token/manifest.json"
	tmdbVal     = "tmdb-SECRET456"
	jellyVal    = "jelly-SECRET789"
	tokenVal    = "api-SECRETabc"
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
	}
}

func assertNoSecrets(t *testing.T, what, s string) {
	t.Helper()
	for _, secret := range []string{"SECRET123", "SECRET456", "SECRET789", "SECRETabc"} {
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

func sameFS(t *testing.T, a, b string) bool {
	t.Helper()
	var sa, sb syscall.Stat_t
	if err := syscall.Stat(a, &sa); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Stat(b, &sb); err != nil {
		t.Fatal(err)
	}
	return sa.Dev == sb.Dev
}

func TestCheckPathsRefusesTempOnAnotherFilesystem(t *testing.T) {
	lib := t.TempDir()
	other, err := os.MkdirTemp("/dev/shm", "marquee-test-")
	if err != nil {
		t.Skip("no /dev/shm to stand in for a second filesystem")
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	if sameFS(t, lib, other) {
		t.Skip("/dev/shm shares a filesystem with the temp dir here")
	}

	cfg := config.Config{
		MoviesPath: lib, SeriesPath: lib, TempPath: filepath.Join(other, "tmp"), DataDir: filepath.Join(lib, "data"),
	}
	err = cfg.CheckPaths()
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(err.Error(), "MARQUEE_TEMP_PATH") || !strings.Contains(err.Error(), "filesystem") {
		t.Errorf("unclear message: %q", err)
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
