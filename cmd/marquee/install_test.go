package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreamsfake"
	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

func repoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func composeEnvironment(t *testing.T) map[string]string {
	t.Helper()
	env := map[string]string{}
	in := false
	sc := bufio.NewScanner(strings.NewReader(repoFile(t, "compose.example.yaml")))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "environment:":
			in = true
		case in && strings.HasPrefix(trimmed, "MARQUEE_"):
			k, v, _ := strings.Cut(trimmed, ":")
			env[k] = strings.Trim(strings.TrimSpace(v), `"`)
		case in && trimmed != "" && !strings.HasPrefix(trimmed, "#"):
			in = false
		}
	}
	return env
}

func essentialSettings(t *testing.T) []string {
	t.Helper()
	_, err := config.Load(func(string) string { return "" })
	if err == nil {
		t.Fatal("an empty configuration must be refused")
	}
	names := regexp.MustCompile(`MARQUEE_[A-Z_]+`).FindAllString(err.Error(), -1)
	if len(names) == 0 {
		t.Fatalf("the startup error names no setting: %v", err)
	}
	return names
}

func TestInstallDocsNameEveryEssentialSetting(t *testing.T) {
	compose, readme := composeEnvironment(t), repoFile(t, "README.md")
	for _, name := range essentialSettings(t) {
		if _, ok := compose[name]; !ok {
			t.Errorf("compose.example.yaml does not set %s", name)
		}
		if !strings.Contains(readme, "`"+name+"`") {
			t.Errorf("README.md does not explain %s", name)
		}
	}
}

func TestComposeKeepsLibraryAndTempOnOneMountAndStartsInDryRun(t *testing.T) {
	env := composeEnvironment(t)
	if env["MARQUEE_DRY_RUN"] != "true" {
		t.Errorf("the example must start in dry-run, got %q", env["MARQUEE_DRY_RUN"])
	}
	for _, k := range []string{"MARQUEE_MOVIES_PATH", "MARQUEE_SERIES_PATH", "MARQUEE_TEMP_PATH"} {
		if !strings.HasPrefix(env[k], "/media/") {
			t.Errorf("%s = %q must sit under the one /media mount so the import is an atomic rename", k, env[k])
		}
	}
	if !strings.Contains(repoFile(t, "compose.example.yaml"), ":/media\n") {
		t.Error("compose.example.yaml must mount the media folder at /media")
	}
}

func TestComposeExampleStartsAndHealthAnswers(t *testing.T) {
	aio := aiostreamsfake.New()
	defer aio.Close()
	tm := tmdbfake.New()
	defer tm.Close()

	media, data := t.TempDir(), t.TempDir()
	for _, d := range []string{"movies", "tv"} {
		if err := os.Mkdir(filepath.Join(media, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := composeEnvironment(t)
	for k, v := range env {
		v = strings.NewReplacer("/media", media, "/data", data).Replace(v)
		switch {
		case strings.Contains(v, "<aiostreams-manifest-url>"):
			v = aio.ManifestURL()
		case strings.Contains(v, "<tmdb-api-key>"):
			v = tmdbfake.Key
		case strings.Contains(v, "<"):
			t.Fatalf("%s still holds a placeholder the test does not fill: %q", k, v)
		}
		env[k] = v
	}
	env["MARQUEE_LISTEN"] = "127.0.0.1:0"

	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	var logs bytes.Buffer
	go func() {
		done <- app.RunWith(ctx, app.Options{TMDBAPIBase: tm.APIBase(), TMDBImageBase: tm.ImageBase(), HealthTimeout: time.Second},
			func(k string) string { return env[k] }, &logs, nil, func(a net.Addr) { addr <- a })
	}()
	var bound net.Addr
	select {
	case bound = <-addr:
	case err := <-done:
		t.Fatalf("marquee did not start: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("marquee did not start in time")
	}

	resp, err := http.Get("http://" + bound.String() + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var h struct {
		Status string `json:"status"`
		DryRun bool   `json:"dryRun"`
		Checks []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: status %d, err %v", resp.StatusCode, err)
	}
	if !h.DryRun || h.Status != "warning" {
		t.Errorf("health = %+v, want dry-run on and a warning (dry-run itself)", h)
	}
	if len(h.Checks) == 0 {
		t.Error("health lists no checks")
	}
	for _, c := range h.Checks {
		if c.Status == "error" {
			t.Errorf("check %s is failing with the example's settings", c.ID)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

// The repository is public: a stranger must be able to install it, and it
// must not name the owner's machines or network.
func TestRepositoryHasNoPrivateNames(t *testing.T) {
	out, err := exec.Command("git", "-C", filepath.Join("..", ".."), "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	patterns := map[string]*regexp.Regexp{
		"home path":    regexp.MustCompile(`/home/[a-z]|/Users/[A-Za-z]|C:\\Users`),
		"IP address":   regexp.MustCompile(`\b(?:10\.\d+|192\.168|172\.(?:1[6-9]|2\d|3[01])|100\.(?:6[4-9]|[7-9]\d|1[01]\d|12[0-7]))\.\d+\.\d+\b`),
		"private name": regexp.MustCompile(`(?i)\.ts\.net|\.lan\b|\.home\.arpa|tailscale\.com/`),
	}
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if f == "web/package-lock.json" || f == "AGENTS.md" || f == "CLAUDE.md" || f == "cmd/marquee/install_test.go" {
			continue
		}
		b, err := os.ReadFile(filepath.Join("..", "..", f))
		if err != nil {
			continue
		}
		for name, re := range patterns {
			if m := re.Find(b); m != nil {
				t.Errorf("%s holds a %s: %q", f, name, m)
			}
		}
	}
}
