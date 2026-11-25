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

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func openClock(t *testing.T, at time.Time) (*catalog.Store, *clock) {
	t.Helper()
	c := &clock{at}
	s, err := catalog.Open(t.TempDir(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, c
}

// dropAtMs turns a current database back into one before the migration
// that added the at_ms columns (and so before every later one), for a test
// to reopen it.
const dropFileColumns = `DROP INDEX episodes_by_fingerprint;
	ALTER TABLE titles DROP COLUMN file_path; ALTER TABLE titles DROP COLUMN file_size; ALTER TABLE titles DROP COLUMN file_fingerprint;
	ALTER TABLE episodes DROP COLUMN file_path; ALTER TABLE episodes DROP COLUMN file_size; ALTER TABLE episodes DROP COLUMN file_fingerprint; `

const dropAtMs = `DROP INDEX downloads_finished; DROP INDEX downloads_finished_by_target; DROP INDEX activity_by_at;
	ALTER TABLE downloads DROP COLUMN at_ms; ALTER TABLE activity DROP COLUMN at_ms;
	ALTER TABLE titles DROP COLUMN monitor_new; ` + dropFileColumns

func addMovie(t *testing.T, s *catalog.Store, tmdb int) string {
	t.Helper()
	if _, _, err := s.Add(context.Background(), catalog.NewTitle{Kind: "movie", TMDBID: tmdb, Title: "Film"}); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("movie-%d", tmdb)
}

// finish queues a download of an episode (a movie when season is 0) and
// ends it, failed for a reason or done.
func finish(t *testing.T, s *catalog.Store, id, title string, season, number int, reason string) {
	t.Helper()
	ctx := context.Background()
	_, err := s.AddDownload(ctx, catalog.NewDownload{
		ID: id, TitleID: title, Season: season, Number: number,
		Release: catalog.ReleaseID{InfoHash: "h" + id, FileName: id + ".mkv", Size: 1}, ReleaseName: id,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reason == "" {
		err = s.FinishDownload(ctx, id, catalog.File{})
	} else {
		err = s.FailDownload(ctx, id, reason, "failed", false)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func note(t *testing.T, s *catalog.Store, title string) {
	t.Helper()
	if err := s.AddActivity(context.Background(), catalog.Activity{TitleID: title, Kind: catalog.ActivitySearched, Message: "m"}); err != nil {
		t.Fatal(err)
	}
}

func entryID(it catalog.HistoryItem) string {
	if it.Download != nil {
		return it.Download.ID
	}
	return it.Activity.ID
}

func ids(items []catalog.HistoryItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, entryID(it))
	}
	return out
}

// Many entries share one instant, so only the tie-break keeps a cursor exact.
func TestHistoryPagesByCursorWhileEntriesArriveAndLeave(t *testing.T) {
	ctx := context.Background()
	s, _ := openClock(t, time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC))
	title := addMovie(t, s, 1)
	other := addMovie(t, s, 2)
	for i := range 4 {
		finish(t, s, fmt.Sprintf("d%d", i), title, 0, 0, "stalled")
		note(t, s, title)
		note(t, s, other)
	}
	all, err := s.History(ctx, "", "", 0)
	if err != nil || len(all.Items) != 12 || all.Next != "" {
		t.Fatalf("unpaged: %d entries, next %q, %v", len(all.Items), all.Next, err)
	}
	want := ids(all.Items)

	first, err := s.History(ctx, "", "", 5)
	if err != nil || len(first.Items) != 5 || first.Next == "" {
		t.Fatalf("page 1: %d entries, next %q, %v", len(first.Items), first.Next, err)
	}
	gone := map[string]bool{}
	notes, err := s.History(ctx, other, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids(notes.Items) {
		gone[id] = true
	}
	// New entries land at the head, and a whole title leaves, between the two requests.
	note(t, s, title)
	finish(t, s, "late", title, 0, 0, "stalled")
	if _, err := s.Remove(ctx, other); err != nil {
		t.Fatal(err)
	}
	var got []string
	got = append(got, ids(first.Items)...)
	for cursor := first.Next; cursor != ""; {
		page, err := s.History(ctx, "", cursor, 5)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ids(page.Items)...)
		cursor = page.Next
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Errorf("entry %s repeated in %v", id, got)
		}
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] && !gone[id] {
			t.Errorf("entry %s was skipped: %v", id, got)
		}
	}
}

func TestHistoryOrdersByInstantThenDownloadsBeforeNotesThenNewestRow(t *testing.T) {
	ctx := context.Background()
	s, c := openClock(t, time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC))
	title := addMovie(t, s, 1)
	note(t, s, title)
	finish(t, s, "d1", title, 0, 0, "stalled")
	// Whole seconds must sort after a fraction of the same second: RFC3339Nano text does not.
	c.t = c.t.Add(time.Second)
	note(t, s, title)
	c.t = c.t.Add(500 * time.Millisecond)
	finish(t, s, "d2", title, 0, 0, "stalled")
	c.t = c.t.Add(500 * time.Millisecond)
	note(t, s, title)
	page, err := s.History(ctx, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, it := range page.Items {
		if it.Download != nil {
			kinds = append(kinds, it.Download.ID)
		} else {
			kinds = append(kinds, "note")
		}
	}
	if fmt.Sprint(kinds) != "[note d2 note d1 note]" {
		t.Errorf("order = %v", kinds)
	}
}

func TestHistoryRefusesACursorTheServerDidNotGive(t *testing.T) {
	s, _ := openClock(t, time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC))
	for _, c := range []string{"nonsense", "1.2", "x.1.2", "1.7.2", "-1.1.1", "1.1.1.1"} {
		if _, err := s.History(context.Background(), "", c, 5); err == nil {
			t.Errorf("cursor %q was accepted", c)
		}
	}
}

func TestHistoryMarksRetryableAgainstTheWholeTableNotThePage(t *testing.T) {
	ctx := context.Background()
	s, c := openClock(t, time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC))
	if _, _, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 5, Title: "Show"}); err != nil {
		t.Fatal(err)
	}
	finish(t, s, "old-fail", "series-5", 1, 1, "stalled")
	c.t = c.t.Add(time.Minute)
	finish(t, s, "other-fail", "series-5", 1, 2, "stalled")
	c.t = c.t.Add(time.Minute)
	finish(t, s, "new-done", "series-5", 1, 1, "")
	page1, err := s.History(ctx, "", "", 1)
	if err != nil || ids(page1.Items)[0] != "new-done" {
		t.Fatalf("page 1 = %v %v", ids(page1.Items), err)
	}
	page2, err := s.History(ctx, "", page1.Next, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page2.Items {
		switch entryID(it) {
		case "old-fail":
			if it.Retryable {
				t.Error("old-fail was superseded by new-done, which is on page 1")
			}
		case "other-fail":
			if !it.Retryable {
				t.Error("other-fail is the latest outcome of its episode")
			}
		}
	}
	// A running download takes the retry away.
	if _, err := s.AddDownload(ctx, catalog.NewDownload{ID: "run", TitleID: "series-5", Season: 1, Number: 2, Release: catalog.ReleaseID{FileName: "r"}}); err != nil {
		t.Fatal(err)
	}
	all, _ := s.History(ctx, "", "", 0)
	for _, it := range all.Items {
		if it.Retryable {
			t.Errorf("%s is retryable while its episode is downloading", entryID(it))
		}
	}
}

func TestHistoryOfOneTitleLeavesTheOthersOut(t *testing.T) {
	ctx := context.Background()
	s, _ := openClock(t, time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC))
	a, b := addMovie(t, s, 1), addMovie(t, s, 2)
	finish(t, s, "da", a, 0, 0, "stalled")
	finish(t, s, "db", b, 0, 0, "stalled")
	note(t, s, a)
	note(t, s, b)
	page, err := s.History(ctx, a, "", 1)
	if err != nil || len(page.Items) != 1 || page.Next == "" {
		t.Fatalf("page = %v %q %v", ids(page.Items), page.Next, err)
	}
	if page.Items[0].Download == nil || page.Items[0].Download.ID != "da" {
		t.Fatalf("a download comes before a note of the same instant: %v", ids(page.Items))
	}
	rest, err := s.History(ctx, a, page.Next, 1)
	if err != nil || len(rest.Items) != 1 || rest.Next != "" || rest.Items[0].Activity == nil || rest.Items[0].Activity.TitleID != a {
		t.Fatalf("rest = %v %q %v", ids(rest.Items), rest.Next, err)
	}
}

func TestOldFinishedDownloadsArePrunedButNotTheLatestOutcomeOfAnyTarget(t *testing.T) {
	ctx := context.Background()
	s, c := openClock(t, time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC))
	s.SetDownloadsKeep(3)
	a, b := addMovie(t, s, 1), addMovie(t, s, 2)
	finish(t, s, "b-local", b, 0, 0, "local_error") // the old, only outcome of movie 2
	for i := range 10 {
		c.t = c.t.Add(time.Minute)
		finish(t, s, fmt.Sprintf("a%d", i), a, 0, 0, "stalled")
	}
	ds, err := s.FinishedDownloads(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range ds {
		got = append(got, d.ID)
	}
	if fmt.Sprint(got) != "[a9 a8 a7 b-local]" {
		t.Fatalf("kept %v, want the newest 3 and the latest outcome of each target", got)
	}
	failedLocally, err := s.FinishedDownloads(ctx, b)
	if err != nil || len(failedLocally) != 1 || failedLocally[0].Reason != "local_error" {
		t.Errorf("the scheduler reads %v %v", failedLocally, err)
	}
	page, err := s.History(ctx, b, "", 0)
	if err != nil || len(page.Items) != 1 || !page.Items[0].Retryable {
		t.Errorf("the old failure stays retryable: %+v %v", page.Items, err)
	}
	// A download that is still running is never pruned, and neither is the blocklist.
	if _, err := s.AddDownload(ctx, catalog.NewDownload{ID: "live", TitleID: a, Release: catalog.ReleaseID{FileName: "l"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDownload(ctx, "live"); err != nil {
		t.Errorf("running download: %v", err)
	}
}

// Rows written before at_ms existed keep their order, though RFC3339Nano text
// sorts a whole second after a fraction of it.
func TestTheAtMsMigrationOrdersOldRowsByTheirInstants(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := catalog.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	title := addMovie(t, s, 1)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, catalog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	insert := func(id, at string) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO downloads (id, title_id, state, info_hash, file_name, size, release_name, url, created_at, updated_at)
			VALUES (?, ?, 'failed', ?, ?, 1, ?, '', ?, ?)`, id, title, id, id, id, at, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(dropAtMs + `PRAGMA user_version = 8`); err != nil {
		t.Fatal(err)
	}
	insert("whole", "2026-11-19T10:00:01Z")
	insert("fraction", "2026-11-19T10:00:00.500000001Z")
	insert("later", "2026-11-19T10:00:01.25Z")
	if _, err := db.Exec(`INSERT INTO activity (id, title_id, kind, message, at) VALUES ('note', ?, 'searched', 'm', '2026-11-19T10:00:01.1Z')`, title); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = catalog.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	page, err := s.History(ctx, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range page.Items {
		if it.Download != nil {
			got = append(got, it.Download.ID)
		} else {
			got = append(got, "note")
		}
	}
	if fmt.Sprint(got) != "[later note whole fraction]" {
		t.Errorf("order after the migration = %v", got)
	}
}
