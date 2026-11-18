package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davepintodev/marquee/internal/fake"
	"github.com/davepintodev/marquee/internal/metadata"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

func TestDownloadsTheArtAndNeverPrintsTheKey(t *testing.T) {
	tm := tmdbfake.New()
	defer tm.Close()
	for _, ti := range fake.Titles() {
		tm.Add(tmdbfake.Title{Kind: ti.Kind, ID: ti.TmdbID, Name: ti.Name, Poster: "/p.jpg", Backdrop: "/b.jpg"})
	}
	dir := filepath.Join(t.TempDir(), "art")
	env := map[string]string{"MARQUEE_TMDB_KEY": tmdbfake.Key}
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), func(k string) string { return env[k] }, []string{dir}, &stdout, &stderr,
		metadata.Options{APIBase: tm.APIBase(), ImageBase: tm.ImageBase()})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	files, _ := os.ReadDir(dir)
	if want := 2 * len(fake.Titles()); len(files) != want {
		t.Errorf("%d files, want %d", len(files), want)
	}
	if strings.Contains(stdout.String()+stderr.String(), tmdbfake.Key) {
		t.Error("the key was printed")
	}
}

func TestFailsNamingWhatIsMissing(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
		want string
	}{
		"no key":    {map[string]string{}, []string{"art"}, "MARQUEE_TMDB_KEY"},
		"no folder": {map[string]string{"MARQUEE_TMDB_KEY": "k-SECRET1"}, nil, "<art-dir>"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), func(k string) string { return tc.env[k] }, tc.args, &stdout, &stderr, metadata.Options{})
			if code == 0 || !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("exit %d, stderr %q, want it to name %s", code, stderr.String(), tc.want)
			}
			if strings.Contains(stderr.String(), "SECRET1") {
				t.Error("the key was printed")
			}
		})
	}
}
