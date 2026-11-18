package importer_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/importer"
	"github.com/davepintodev/marquee/internal/jellyfinfake"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlaceMovesTheFileAndCreatesItsFolders(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "tmp", "a.part"), filepath.Join(root, "lib", "Show", "Season 01", "Show - S01E01 - X [WEBDL-1080p].mkv")
	write(t, src, "video")
	if err := importer.Place(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "video" {
		t.Errorf("destination holds %q", b)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the temp file is still there: %v", err)
	}
}

func TestPlaceNeverReplacesAFileItDidNotCreate(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "a.part"), filepath.Join(root, "lib", "Movie (2020)", "Movie (2020) [WEBDL-1080p].mkv")
	write(t, src, "new")
	write(t, dst, "owners own file")
	err := importer.Place(src, dst)
	if !errors.Is(err, importer.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "owners own file" {
		t.Errorf("the existing file was changed to %q", b)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the temp file must stay when nothing was moved: %v", err)
	}
}

func TestPlaceMissingSourceFails(t *testing.T) {
	root := t.TempDir()
	if err := importer.Place(filepath.Join(root, "nope"), filepath.Join(root, "lib", "x.mkv")); err == nil {
		t.Fatal("moving a missing file succeeded")
	}
}

func TestPlaceAcrossFilesystemsFailsClearlyInsteadOfCopying(t *testing.T) {
	shm := "/dev/shm"
	if st, err := os.Stat(shm); err != nil || !st.IsDir() {
		t.Skip("no second filesystem to test with")
	}
	root := t.TempDir()
	a, _ := os.Stat(root)
	b, _ := os.Stat(shm)
	if os.SameFile(a, b) {
		t.Skip("same filesystem")
	}
	src := filepath.Join(shm, "marquee-importer-test.part")
	write(t, src, "video")
	defer func() { _ = os.Remove(src) }()
	dst := filepath.Join(root, "x.mkv")
	if err := importer.Place(src, dst); err == nil || !strings.Contains(err.Error(), "same filesystem") {
		t.Fatalf("err = %v, want a message about the temp path and library sharing a filesystem", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("a copy was left in the library")
	}
}

func TestJellyfinRefreshSendsTheKeyInTheHeaderOnly(t *testing.T) {
	fake := jellyfinfake.New()
	defer fake.Close()
	j := importer.NewJellyfin(fake.URL, config.Secret(jellyfinfake.Key), time.Second)
	if err := j.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.Refreshes() != 1 {
		t.Errorf("refreshes = %d, want 1", fake.Refreshes())
	}
}

func TestJellyfinRefreshErrorsNameTheProblemWithoutTheKey(t *testing.T) {
	fake := jellyfinfake.New()
	defer fake.Close()
	for _, tc := range []struct {
		name   string
		key    string
		status int
		want   string
	}{
		{"wrong key", "wrong", 0, "key"},
		{"server error", jellyfinfake.Key, 503, "503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake.FailWith(tc.status)
			err := importer.NewJellyfin(fake.URL, config.Secret(tc.key), time.Second).Refresh(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if strings.Contains(err.Error(), tc.key) || strings.Contains(err.Error(), fake.URL) {
				t.Errorf("error leaks the key or URL: %v", err)
			}
		})
	}
}

func TestJellyfinRefreshWhenUnreachable(t *testing.T) {
	fake := jellyfinfake.New()
	url := fake.URL
	fake.Close()
	err := importer.NewJellyfin(url, config.Secret(jellyfinfake.Key), time.Second).Refresh(context.Background())
	if err == nil || strings.Contains(err.Error(), jellyfinfake.Key) {
		t.Fatalf("err = %v, want an error that does not hold the key", err)
	}
}

func TestJellyfinRefreshIsBoundedByItsTimeout(t *testing.T) {
	hang := make(chan struct{})
	srv := newHangingServer(hang)
	defer srv.Close()
	defer close(hang)
	start := time.Now()
	err := importer.NewJellyfin(srv.URL, config.Secret(jellyfinfake.Key), 100*time.Millisecond).Refresh(context.Background())
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("err = %v after %s, want a timeout error soon", err, time.Since(start))
	}
}
