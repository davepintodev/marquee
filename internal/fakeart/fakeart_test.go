package fakeart_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/fake"
	"github.com/davepintodev/marquee/internal/fakeart"
	"github.com/davepintodev/marquee/internal/metadata"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

func tmdbWithEveryFixture(t *testing.T, skip func(fake.Title) (poster, backdrop string, listed bool)) *tmdbfake.Fake {
	t.Helper()
	tm := tmdbfake.New()
	t.Cleanup(tm.Close)
	for _, ti := range fake.Titles() {
		poster, backdrop := "/"+ti.ID+"-p.jpg", "/"+ti.ID+"-b.jpg"
		if skip != nil {
			var listed bool
			if poster, backdrop, listed = skip(ti); !listed {
				continue
			}
		}
		tm.Add(tmdbfake.Title{Kind: ti.Kind, ID: ti.TmdbID, Name: ti.Name, Poster: poster, Backdrop: backdrop})
	}
	return tm
}

func client(tm *tmdbfake.Fake, key string) *metadata.Client {
	return metadata.New(metadata.Options{APIBase: tm.APIBase(), ImageBase: tm.ImageBase(), Key: config.Secret(key)})
}

func TestDownloadsEveryFixturePosterAndBackdrop(t *testing.T) {
	tm := tmdbWithEveryFixture(t, nil)
	dir := filepath.Join(t.TempDir(), "art")
	var out bytes.Buffer

	if err := fakeart.Fetch(context.Background(), client(tm, tmdbfake.Key), fake.Titles(), dir, &out); err != nil {
		t.Fatal(err)
	}
	for _, ti := range fake.Titles() {
		for _, kind := range []string{"poster", "backdrop"} {
			got, err := os.ReadFile(filepath.Join(dir, kind+"-"+ti.ID+".jpg"))
			if err != nil {
				t.Fatalf("%s %s: %v", kind, ti.ID, err)
			}
			suffix := "-p.jpg"
			if kind == "backdrop" {
				suffix = "-b.jpg"
			}
			if want := tmdbfake.ImageBytes("/" + ti.ID + suffix); !bytes.Equal(got, want) {
				t.Errorf("%s %s holds the wrong bytes", kind, ti.ID)
			}
		}
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(leftovers) > 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestAWrongKeyStopsAtOnceWithoutPrintingIt(t *testing.T) {
	tm := tmdbWithEveryFixture(t, nil)
	dir := t.TempDir()
	var out bytes.Buffer

	err := fakeart.Fetch(context.Background(), client(tm, "wrong-key-SECRET789"), fake.Titles(), dir, &out)
	if !errors.Is(err, metadata.ErrRejected) {
		t.Fatalf("want the rejected-key error, got %v", err)
	}
	if strings.Contains(err.Error(), "SECRET789") || strings.Contains(out.String(), "SECRET789") {
		t.Error("the key leaked")
	}
	if n := tm.Hits("/3/"); n != 1 {
		t.Errorf("a refused key must stop the run after one request, saw %d", n)
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Errorf("nothing should be written, found %d files", len(files))
	}
}

func TestATitleTMDBDoesNotKnowOrHasNoArtDoesNotStopTheOthers(t *testing.T) {
	titles := fake.Titles()
	unknown, noBackdrop := titles[0], titles[1]
	tm := tmdbWithEveryFixture(t, func(ti fake.Title) (string, string, bool) {
		switch ti.ID {
		case unknown.ID:
			return "", "", false
		case noBackdrop.ID:
			return "/p.jpg", "", true
		}
		return "/" + ti.ID + "-p.jpg", "/" + ti.ID + "-b.jpg", true
	})
	dir := t.TempDir()
	var out bytes.Buffer

	err := fakeart.Fetch(context.Background(), client(tm, tmdbfake.Key), titles, dir, &out)
	if err == nil || !strings.Contains(err.Error(), unknown.ID) {
		t.Fatalf("want an error naming %s, got %v", unknown.ID, err)
	}
	if strings.Contains(err.Error(), noBackdrop.ID) {
		t.Errorf("a missing backdrop is a note, not a failure: %v", err)
	}
	for _, name := range []string{"poster-" + noBackdrop.ID, "poster-" + titles[2].ID, "backdrop-" + titles[2].ID} {
		if _, err := os.Stat(filepath.Join(dir, name+".jpg")); err != nil {
			t.Errorf("%s should have been downloaded: %v", name, err)
		}
	}
	for _, name := range []string{"backdrop-" + noBackdrop.ID, "poster-" + unknown.ID} {
		if _, err := os.Stat(filepath.Join(dir, name+".jpg")); err == nil {
			t.Errorf("%s should not exist", name)
		}
	}
	if !strings.Contains(out.String(), "no backdrop") {
		t.Errorf("the report should say what TMDB lacks: %q", out.String())
	}
}

func TestAnImageFailureLeavesNoPartialFileAndTheOldOneStays(t *testing.T) {
	tm := tmdbWithEveryFixture(t, nil)
	first := fake.Titles()[0]
	dir := t.TempDir()
	old := filepath.Join(dir, "poster-"+first.ID+".jpg")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	tm.Down("/t/p/", 500)

	err := fakeart.Fetch(context.Background(), client(tm, tmdbfake.Key), first1(first), dir, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want an error")
	}
	if got, _ := os.ReadFile(old); string(got) != "old" {
		t.Errorf("a failed download replaced the earlier file: %q", got)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(leftovers) > 0 {
		t.Errorf("partial files left behind: %v", leftovers)
	}
}

func first1(t fake.Title) []fake.Title { return []fake.Title{t} }

func TestCancelledRunStops(t *testing.T) {
	tm := tmdbWithEveryFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fakeart.Fetch(ctx, client(tm, tmdbfake.Key), fake.Titles(), t.TempDir(), &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
