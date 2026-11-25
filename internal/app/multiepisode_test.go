package app_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

const pairName = "Dune - Prophecy/Season 01/Dune - Prophecy - S01E01-E02 - Aired One + Aired Today [WEBDL-1080p].mkv"

func pairJob(r *rig, data []byte) catalog.Download {
	j := episodeJob(r.cdn.Add("pair.mkv", debridfake.File{Data: data}), "pair.mkv")
	j.Covered = []int{1, 2}
	return r.enqueue(j)
}

func episodeOf(t *testing.T, title map[string]any, id string) map[string]any {
	t.Helper()
	for _, s := range title["seasons"].([]any) {
		for _, e := range s.(map[string]any)["episodes"].([]any) {
			if ep := e.(map[string]any); ep["id"] == id {
				return ep
			}
		}
	}
	t.Fatalf("no episode %s in %v", id, title["seasons"])
	return nil
}

func sharedOf(ep map[string]any) []string {
	var out []string
	for _, v := range ep["sharedEpisodes"].([]any) {
		out = append(out, v.(string))
	}
	return out
}

func TestAMultiEpisodeDownloadIsNamedForEveryEpisodeAndRecordedOnEachOfThem(t *testing.T) {
	r, root := setup(t)
	data := debridfake.MKV(200 << 10)
	pairJob(r, data)
	r.waitHistoryKind("imported", 1)

	if got := tree(t, filepath.Join(root, "series")); len(got) != 1 || got[0] != pairName {
		t.Fatalf("series folder holds %v, want [%s]", got, pairName)
	}
	one, two := r.storedFile("series-1408", 1, 1), r.storedFile("series-1408", 1, 2)
	if one.path != pairName || one.size != int64(len(data)) || one.fingerprint == "" || one != two {
		t.Errorf("both episodes record the same file: %+v and %+v", one, two)
	}
	if other := r.storedFile("series-1408", 1, 3); other != (storedFile{}) {
		t.Errorf("S01E03 is not in the file: %+v", other)
	}

	title := r.mustCall("GET", "/api/titles/series-1408", "", 200)
	for _, id := range []string{"s01e01", "s01e02"} {
		ep := episodeOf(t, title, id)
		if ep["state"] != "downloaded" || !slices.Equal(sharedOf(ep), []string{"s01e01", "s01e02"}) {
			t.Errorf("%s: %v", id, ep)
		}
	}
	if ep := episodeOf(t, title, "s01e03"); ep["state"] == "downloaded" || ep["sharedEpisodes"] != nil {
		t.Errorf("s01e03: %v", ep)
	}
	for _, e := range r.history() {
		if m := e.(map[string]any); m["kind"] == "failed" {
			t.Errorf("a failure: %v", m)
		}
	}
	waitFor(t, "the Jellyfin refresh", 5*time.Second, func() bool { return r.jf.Refreshes() == 1 })
}

func TestACoveredEpisodeTheSeriesDoesNotListIsLeftOutOfTheFile(t *testing.T) {
	r, root := setup(t)
	j := episodeJob(r.cdn.Add("pair.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "pair.mkv")
	j.Covered = []int{2, 1, 9}
	r.enqueue(j)
	r.waitHistoryKind("imported", 1)
	if got := tree(t, filepath.Join(root, "series")); len(got) != 1 || got[0] != pairName {
		t.Errorf("series folder holds %v, want [%s]", got, pairName)
	}
}

func TestADownloadThatCoversTheEpisodeAnotherOneIsFetchingIsRefused(t *testing.T) {
	r, _ := setup(t)
	single := episodeJob(r.cdn.Add("one.mkv", debridfake.File{Data: debridfake.MKV(1 << 20), Pace: 50 * time.Millisecond}), "one.mkv")
	single.Number = 2
	r.enqueue(single)
	wide := episodeJob(r.cdn.Add("wide.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "wide.mkv")
	wide.Covered = []int{1, 2}
	if _, err := r.mgr.Enqueue(t.Context(), wide); err == nil || !strings.Contains(err.Error(), "active download") {
		t.Errorf("enqueue over a download in flight: %v", err)
	}
}

func TestWrongFileOnAMultiEpisodeDownloadDeletesTheSharedFileOnceAndResetsEveryEpisodeItCovered(t *testing.T) {
	r, root := setup(t)
	pairJob(r, debridfake.MKV(200<<10))
	r.waitHistoryKind("imported", 1)
	sub := filepath.Join(root, "series", "Dune - Prophecy/Season 01/Dune - Prophecy - S01E01-E02 - Aired One + Aired Today [WEBDL-1080p].en.srt")
	if err := os.WriteFile(sub, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	title := r.mustCall("POST", "/api/titles/series-1408/episodes/s01e01/wrong-file", `{"reSearch":false}`, 200)
	if got := tree(t, filepath.Join(root, "series")); len(got) != 0 {
		t.Errorf("the shared file and its sidecar should both be gone: %v", got)
	}
	for _, n := range []int{1, 2} {
		if row := r.row("series-1408", 1, n); row.have || !row.monitored || row.path != "" || row.fingerprint != "" {
			t.Errorf("S01E0%d: %+v", n, row)
		}
	}
	for _, id := range []string{"s01e01", "s01e02"} {
		if ep := episodeOf(t, title, id); ep["state"] == "downloaded" || ep["sharedEpisodes"] != nil {
			t.Errorf("%s: %v", id, ep)
		}
	}
	var notes []map[string]any
	for _, h := range r.history() {
		if m := h.(map[string]any); m["kind"] == "wrong_file" {
			notes = append(notes, m)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0]["message"].(string), "S01E02") || !strings.Contains(notes[0]["message"].(string), "blocklisted") {
		t.Errorf("one note naming the other episode and the blocklisting: %v", notes)
	}
}

func TestReplacingASharedFileWithTheSameBytesIsNoDuplicateEpisode(t *testing.T) {
	r, root := setup(t)
	data := debridfake.MKV(200 << 10)
	pairJob(r, data)
	r.waitHistoryKind("imported", 1)

	j := episodeJob(r.cdn.Add("again.mkv", debridfake.File{Data: data}), "again.mkv")
	j.Covered, j.Replace = []int{1, 2}, true
	j.Release = catalog.ReleaseID{InfoHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", FileName: "again.mkv", Size: int64(len(data))}
	r.enqueue(j)
	r.waitHistoryKind("imported", 2)
	for _, e := range r.history() {
		if m := e.(map[string]any); m["kind"] == "failed" {
			t.Errorf("a failure: %v (the file shares its bytes with its own episodes)", m)
		}
	}
	if got := tree(t, filepath.Join(root, "series")); len(got) != 1 || got[0] != pairName {
		t.Errorf("series folder holds %v, want [%s]", got, pairName)
	}
	if one, two := r.storedFile("series-1408", 1, 1), r.storedFile("series-1408", 1, 2); one != two || one.path != pairName {
		t.Errorf("%+v %+v", one, two)
	}
}

func TestAnAdoptedMultiEpisodeFileIsSharedByEveryEpisodeItNames(t *testing.T) {
	s, _ := startFiles(t, false)
	title := s.mustCall("GET", "/api/titles/series-1622", "", 200)
	for _, id := range []string{"s03e01", "s03e02"} {
		ep := episodeOf(t, title, id)
		if !slices.Equal(sharedOf(ep), []string{"s03e01", "s03e02"}) {
			t.Errorf("%s: %v", id, ep)
		}
	}
	if ep := episodeOf(t, title, "s04e01"); ep["sharedEpisodes"] != nil {
		t.Errorf("a file of one episode is not shared: %v", ep)
	}
}

func TestAMultiEpisodeReleaseFoundForOneEpisodeIsDownloadedOnceAndCoversTheNext(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("pair.mkv", debridfake.File{Data: debridfake.MKV(53 << 20)})
	r.aio.Answer("series", "tvdb:403000:1:1", results(listed{name: "Dune.Prophecy.S01E01E02.1080p.WEB-DL.mkv", size: 53 << 20, url: url, season: 1, eps: []int{1, 2}}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	r.advance(5 * time.Second)
	r.step()
	if n := r.searchedID("tvdb:403000:1:2"); n != 0 {
		t.Errorf("S01E02 is covered by the download in flight and is not searched: %d", n)
	}
	waitFor(t, "the import", 20*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	for _, id := range []string{"s01e01", "s01e02"} {
		if row := r.row("series-1408", 1, int(id[5]-'0')); !row.have || !strings.Contains(row.path, "S01E01-E02") {
			t.Errorf("%s: %+v", id, row)
		}
	}
	r.advance(5 * time.Second)
	r.step()
	if n := r.searchedID("tvdb:403000:1:2"); n != 0 {
		t.Errorf("S01E02 has its file and is not searched: %d", n)
	}
}

func TestASingleEpisodeReleaseIsPickedOverAMultiEpisodeOneWhenAnotherCoveredEpisodeIsOnDisk(t *testing.T) {
	r := newSchedRig(t, false)
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.mustCall("PATCH", "/api/titles/series-1408/episodes/s01e01", `{"monitored":false}`, 200)
	two := r.cdn.Add("two.mkv", debridfake.File{Data: debridfake.MKV(53 << 20)})
	r.aio.Answer("series", "tvdb:403000:1:2", results(listed{name: "Dune.Prophecy.S01E02.1080p.WEB-DL.mkv", size: 53 << 20, url: two, season: 1, ep: 2}))
	r.step()
	waitFor(t, "S01E02's import", 20*time.Second, func() bool { return len(r.entries("imported")) == 1 })

	multi := r.cdn.Add("multi.mkv", debridfake.File{Data: debridfake.MKV(54 << 20)})
	single := r.cdn.Add("single.mkv", debridfake.File{Data: debridfake.MKV(55 << 20)})
	r.aio.Answer("series", "tvdb:403000:1:1", results(
		listed{name: "Dune.Prophecy.S01E01E02.1080p.WEB-DL.mkv", size: 54 << 20, url: multi, season: 1, eps: []int{1, 2}},
		listed{name: "Dune.Prophecy.S01E01.1080p.WEB-DL.mkv", size: 55 << 20, url: single, season: 1, ep: 1},
	))
	list := r.mustCall("GET", "/api/titles/series-1408/releases?episode=s01e01", "", 200)["candidates"].([]any)
	if len(list) != 2 || !strings.HasSuffix(list[0].(map[string]any)["name"].(string), "S01E01.1080p.WEB-DL.mkv") {
		t.Errorf("Interactive Search lists the single episode first: %v", list)
	}

	r.mustCall("PATCH", "/api/titles/series-1408/episodes/s01e01", `{"monitored":true}`, 200)
	r.advance(5 * time.Second)
	r.step()
	waitFor(t, "S01E01's import", 20*time.Second, func() bool { return len(r.entries("imported")) == 2 })
	one := r.row("series-1408", 1, 1)
	if !one.have || strings.Contains(one.path, "-E02") || !strings.Contains(one.path, "S01E01 - Aired One") {
		t.Errorf("the single episode was chosen: %+v", one)
	}
	if two := r.row("series-1408", 1, 2); !strings.Contains(two.path, "S01E02") {
		t.Errorf("S01E02 keeps its own file: %+v", two)
	}
}

func TestEveryEpisodeHasItsOwnDailySearchBudgetOfFourThatSearchNowIgnores(t *testing.T) {
	r := newSchedRig(t, false)
	var eps []tmdbfake.Episode
	for n := 1; n <= 6; n++ {
		eps = append(eps, tmdbfake.Episode{Number: n, Name: fmt.Sprintf("E%d", n), AirDate: "2020-01-01"})
	}
	r.tmdb.Add(tmdbfake.Title{Kind: "series", ID: 4000, Name: "Long", Date: "2020-01-01", IMDb: "tt4000000", TVDB: 4000,
		Seasons: []tmdbfake.Season{{Number: 1, Episodes: eps}}})
	r.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":4000}`, 201)
	id := func(n int) string { return fmt.Sprintf("tvdb:4000:1:%d", n) }

	drain := func(passes int) {
		for range passes {
			r.step()
			r.advance(5 * time.Second)
		}
	}
	drain(10)
	r.wantSearches(6, "six episodes are wanted and each has a budget of its own: one search for every one")

	store, err := catalog.Open(r.env["MARQUEE_DATA_DIR"], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	day := time.Unix(0, r.now.Load()).UTC().Format(time.DateOnly)
	for range 4 {
		if err := store.CountSearch(t.Context(), "series-4000", 1, 1, day); err != nil {
			t.Fatal(err)
		}
	}
	r.advance(15 * time.Minute)
	drain(10)
	if got := r.searchedID(id(1)); got != 1 {
		t.Errorf("episode 1 spent its four searches and waits for tomorrow: %d searches", got)
	}
	for n := 2; n <= 6; n++ {
		if got := r.searchedID(id(n)); got != 2 {
			t.Errorf("episode %d: %d searches, want 2 (its budget is untouched)", n, got)
		}
	}

	r.mustCall("POST", "/api/titles/series-4000/episodes/s01e01/search", "", 200)
	drain(4)
	if got := r.searchedID(id(1)); got != 2 {
		t.Errorf("Search now ignores the budget: %d searches of episode 1, want 2", got)
	}

	r.setClock(time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC))
	drain(10)
	if got := r.searchedID(id(1)); got != 3 {
		t.Errorf("the next day brings a new budget: %d searches of episode 1, want 3", got)
	}
}

func TestReplacingASharedFileWithANewlyNamedOneDeletesTheOldFileOnceAndKeepsBothEpisodes(t *testing.T) {
	r, root := setup(t)
	pairJob(r, debridfake.MKV(200<<10))
	r.waitHistoryKind("imported", 1)

	data := debridfake.MKV(300 << 10)
	j := episodeJob(r.cdn.Add("hd.mkv", debridfake.File{Data: data}), "hd.mkv")
	j.Covered, j.Replace, j.Resolution, j.Quality = []int{1, 2}, true, "720p", "WEB-DL"
	j.Release = catalog.ReleaseID{InfoHash: "cccccccccccccccccccccccccccccccccccccccc", FileName: "hd.mkv", Size: int64(len(data))}
	r.enqueue(j)
	r.waitHistoryKind("imported", 2)

	const want = "Dune - Prophecy/Season 01/Dune - Prophecy - S01E01-E02 - Aired One + Aired Today [WEBDL-720p].mkv"
	if got := tree(t, filepath.Join(root, "series")); len(got) != 1 || got[0] != want {
		t.Errorf("the old shared file should be gone and the new one placed: %v", got)
	}
	for _, n := range []int{1, 2} {
		if row := r.row("series-1408", 1, n); !row.have || row.path != want {
			t.Errorf("S01E0%d: %+v", n, row)
		}
	}
}
