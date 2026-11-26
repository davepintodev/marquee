package catalog_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

func TestPauseAllIsOffUntilSwitchedOnAndKeepsEachDownloadsOwnPause(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	s := openAt(t, now)
	addHouse(t, s)
	queueUncached(t, s, "own", 1)
	queueUncached(t, s, "other", 2)
	if err := s.SetDownloadState(ctx, "other", catalog.StateDownloading); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PauseDownload(ctx, "own"); err != nil {
		t.Fatal(err)
	}
	if on, err := s.PausedAll(ctx); err != nil || on {
		t.Fatalf("Pause all before anyone asked: %v, %v", on, err)
	}

	for i, want := range []bool{true, true, false} {
		on := i == 0
		changed, err := s.SetPausedAll(ctx, on)
		if err != nil || changed != want {
			t.Fatalf("SetPausedAll(%v) changed %v, %v; want changed %v", on, changed, err, want)
		}
	}
	own, _ := s.GetDownload(ctx, "own")
	other, _ := s.GetDownload(ctx, "other")
	if !own.Paused || other.Paused {
		t.Errorf("Pause all changed the downloads' own pauses: own %v, other %v", own.Paused, other.Paused)
	}

	if _, err := s.SetPausedAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	if state := other.APIState(true); state != "paused" {
		t.Errorf("a downloading file under Pause all shows %q", state)
	}
	queued := catalog.Download{State: catalog.StateQueued}
	if state := queued.APIState(true); state != "queued" {
		t.Errorf("a queued download under Pause all shows %q", state)
	}
	settling := catalog.Download{State: catalog.StateImporting}
	if state := settling.APIState(true); state != "importing" {
		t.Errorf("an importing download under Pause all shows %q", state)
	}
	if stale := (catalog.Download{State: catalog.StateDownloading, UpdatedAt: now.Add(-time.Minute)}); stale.Stalled(now, true) || !stale.Stalled(now, false) {
		t.Error("only a download that is not held can be stalled")
	}
}

func TestPauseAllSurvivesReopeningTheCatalogAndAnOlderLibraryStartsWithItOff(t *testing.T) {
	ctx := context.Background()
	now := func() time.Time { return time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC) }
	dir := t.TempDir()
	s, err := catalog.Open(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	addHouse(t, s)
	queueUncached(t, s, "a", 1)
	if _, err := s.SetPausedAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = catalog.Open(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if on, err := s.PausedAll(ctx); err != nil || !on {
		t.Fatalf("after reopening: %v, %v", on, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, catalog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	var have int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&have); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE pause_all`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, have-1)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = catalog.Open(dir, now)
	if err != nil {
		t.Fatalf("the migration must run on a library with downloads: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if on, err := s.PausedAll(ctx); err != nil || on {
		t.Errorf("an older library starts with Pause all %v, %v", on, err)
	}
	if d, err := s.GetDownload(ctx, "a"); err != nil || d.State != catalog.StateQueued {
		t.Errorf("the migration changed a download: %+v, %v", d, err)
	}
}
