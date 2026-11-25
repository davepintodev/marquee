package app_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/debridfake"
)

func (r *schedRig) catalogDB() *sql.DB {
	r.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.env["MARQUEE_DATA_DIR"], catalog.FileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = db.Close() })
	return db
}

func (r *schedRig) count(query string) int {
	r.t.Helper()
	var n int
	if err := r.catalogDB().QueryRow(query).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *schedRig) restartAs(dryRun string) {
	r.t.Helper()
	r.stop()
	r.env["MARQUEE_DRY_RUN"] = dryRun
	r.start()
}

func (r *schedRig) lastDryRun() (value int, ok bool) {
	r.t.Helper()
	err := r.catalogDB().QueryRow(`SELECT last_dry_run FROM run_mode`).Scan(&value)
	if err == sql.ErrNoRows {
		return 0, false
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return value, true
}

func dryRunWithAGrabbableDune(t *testing.T) *schedRig {
	t.Helper()
	r := newSchedRig(t, true)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.BluRay.mkv", size: 300 << 20, url: url}))
	r.addMovie()
	r.step()
	return r
}

func TestLeavingDryRunResetsTheSearchTimersOnce(t *testing.T) {
	r := dryRunWithAGrabbableDune(t)
	if n := len(r.entries("would_grab")); n != 1 {
		t.Fatalf("dry-run recorded %d would_grab entries, want 1", n)
	}
	r.wantSearches(1, "dry-run")
	if r.count(`SELECT count(*) FROM schedule WHERE next_at IS NOT NULL`) == 0 || r.count(`SELECT count(*) FROM search_budget`) == 0 {
		t.Fatal("dry-run left no backoff or budget row to reset")
	}

	r.restartAs("false")
	if n := r.count(`SELECT count(*) FROM schedule WHERE next_at IS NOT NULL OR attempts != 0`); n != 0 {
		t.Errorf("%d schedule rows still back off after leaving dry-run", n)
	}
	if n := r.count(`SELECT count(*) FROM search_budget`); n != 0 {
		t.Errorf("%d search_budget rows survived leaving dry-run", n)
	}
	r.step()
	r.wantSearches(2, "the first live step searches at once, not a day later")
	if d := r.downloads(); len(d) != 1 {
		t.Errorf("the first live step queued %v, want the Dune download", d)
	}
	if v, ok := r.lastDryRun(); !ok || v != 0 {
		t.Errorf("last_dry_run = %d, %v after the live start, want 0", v, ok)
	}

	if r.count(`SELECT count(*) FROM search_budget`) == 0 {
		t.Fatal("the live step spent no budget")
	}
	r.advance(time.Minute)
	r.restartAs("false")
	if n := r.count(`SELECT count(*) FROM search_budget`); n == 0 {
		t.Error("a second live start cleared the budget again")
	}
}

func TestARestartThatStaysLiveKeepsTheTimers(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	r.step()
	r.advance(15 * time.Minute)
	r.step()
	r.wantSearches(2, "before the restart")
	timers := r.count(`SELECT count(*) FROM schedule WHERE next_at IS NOT NULL AND attempts > 0`)
	budget := r.count(`SELECT count(*) FROM search_budget`)
	if timers == 0 || budget == 0 {
		t.Fatalf("the live run left %d timers and %d budget rows", timers, budget)
	}

	r.advance(time.Minute)
	r.restartAs("false")
	if got := r.count(`SELECT count(*) FROM schedule WHERE next_at IS NOT NULL AND attempts > 0`); got != timers {
		t.Errorf("a live restart changed the backoff rows: %d, was %d", got, timers)
	}
	if got := r.count(`SELECT count(*) FROM search_budget`); got != budget {
		t.Errorf("a live restart changed the budget rows: %d, was %d", got, budget)
	}
	r.step()
	r.wantSearches(2, "a live restart keeps backing off")
}

func TestACatalogWithNoRecordedModeIsNeverReset(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	r.step()
	r.advance(15 * time.Minute)
	r.step()
	r.stop()
	if _, err := r.catalogDB().Exec(`DELETE FROM run_mode`); err != nil {
		t.Fatal(err)
	}

	r.advance(time.Minute)
	r.start()
	r.step()
	r.wantSearches(2, "a database from before the mode was recorded keeps its backoff")
	if v, ok := r.lastDryRun(); !ok || v != 0 {
		t.Errorf("last_dry_run = %d, %v, want the current mode recorded as 0", v, ok)
	}
}

func TestEnteringDryRunKeepsTheTimersAndRecordsTheMode(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	r.step()
	r.advance(15 * time.Minute)
	r.step()
	timers := r.count(`SELECT count(*) FROM schedule WHERE next_at IS NOT NULL AND attempts > 0`)
	budget := r.count(`SELECT count(*) FROM search_budget`)

	r.advance(time.Minute)
	r.restartAs("true")
	if got := r.count(`SELECT count(*) FROM schedule WHERE next_at IS NOT NULL AND attempts > 0`); got != timers || timers == 0 {
		t.Errorf("entering dry-run changed the backoff rows: %d, was %d", got, timers)
	}
	if got := r.count(`SELECT count(*) FROM search_budget`); got != budget {
		t.Errorf("entering dry-run changed the budget rows: %d, was %d", got, budget)
	}
	if v, ok := r.lastDryRun(); !ok || v != 1 {
		t.Errorf("last_dry_run = %d, %v after the dry-run start, want 1", v, ok)
	}
	r.step()
	r.wantSearches(2, "dry-run keeps the live backoff")
}
