package app_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/torboxfake"
)

func (r *realSvc) download(id string) map[string]any {
	r.t.Helper()
	for _, d := range r.downloads() {
		if m := d.(map[string]any); m["id"] == id {
			return m
		}
	}
	return nil
}

func (r *realSvc) blocked(id catalog.ReleaseID) bool {
	r.t.Helper()
	store, err := catalog.Open(r.env["MARQUEE_DATA_DIR"], nil)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	return isBlocked(r.t, store, id)
}

func (r *rig) waitPercent(id string, at float64) {
	r.t.Helper()
	waitFor(r.t, fmt.Sprintf("download %s at %v%%", id, at), 10*time.Second, func() bool {
		d := r.download(id)
		return d != nil && d["state"] == "downloading" && d["percent"].(float64) >= at
	})
}

func streamedFrom(cdn *debridfake.Server, since int, file string) bool {
	for _, q := range cdn.Requests()[since:] {
		if strings.HasSuffix(q.Path, file) && !q.Probe {
			return true
		}
	}
	return false
}

func TestPausingARunningDownloadStopsItKeepsThePartialAndListsItPaused(t *testing.T) {
	r, root := setup(t)
	url := r.cdn.Add("big.mkv", debridfake.File{Data: debridfake.MKV(3 << 20), Pace: 8 * time.Millisecond})
	d := r.enqueue(movieJob(url, "big.mkv"))
	r.waitPercent(d.ID, 15)

	got := r.mustCall("POST", "/api/downloads/"+d.ID+"/pause", "", 200)
	if got["id"] != d.ID || got["state"] != "paused" || got["stalled"] != false {
		t.Fatalf("pause answered %v", got)
	}
	if n := r.cdn.Streaming(); n != 0 {
		t.Errorf("%d transfers still streaming after the pause answered", n)
	}
	if len(tree(t, filepath.Join(root, "tmp"))) == 0 {
		t.Fatal("the pause threw the partial file away")
	}
	before := r.download(d.ID)
	seen := len(r.cdn.Requests())
	time.Sleep(200 * time.Millisecond)
	after := r.download(d.ID)
	if after["state"] != "paused" || after["bytesDone"] != before["bytesDone"] || after["stalled"] != false {
		t.Errorf("a paused download moved: %v then %v", before, after)
	}
	if n := len(r.cdn.Requests()); n != seen {
		t.Errorf("a paused download made %d more requests", n-seen)
	}
	if h := r.history(); len(h) != 0 {
		t.Errorf("a pause is not history: %v", h)
	}
	again := r.mustCall("POST", "/api/downloads/"+d.ID+"/pause", "", 200)
	if again["state"] != "paused" {
		t.Errorf("a second pause answered %v", again)
	}
}

func TestAResumedDownloadContinuesFromTheBytesOnDisk(t *testing.T) {
	r, root := setup(t)
	r.noStall()
	data := debridfake.MKV(3 << 20)
	url := r.cdn.Add("big.mkv", debridfake.File{Data: data, Pace: 8 * time.Millisecond})
	d := r.enqueue(movieJob(url, "big.mkv"))
	r.waitPercent(d.ID, 15)
	r.mustCall("POST", "/api/downloads/"+d.ID+"/pause", "", 200)
	seen := len(r.cdn.Requests())

	got := r.mustCall("POST", "/api/downloads/"+d.ID+"/resume", "", 200)
	if got["state"] == "paused" {
		t.Fatalf("resume answered %v", got)
	}
	if e := r.waitHistory(1)[0].(map[string]any); e["kind"] != "imported" {
		t.Fatalf("history = %v", e)
	}
	low := int64(-1)
	for _, q := range r.cdn.Requests()[seen:] {
		if !q.Probe && !q.Playback && q.RangeStart >= 0 && (low < 0 || q.RangeStart < low) {
			low = q.RangeStart
		}
	}
	if low <= 0 {
		t.Errorf("after the resume the lowest range start is %d; it should continue past the start", low)
	}
	file, _ := os.ReadFile(filepath.Join(root, "movies", "Dune (2021)", "Dune (2021) [WEBDL-1080p].mkv"))
	if !bytes.Equal(file, data) {
		t.Error("the resumed file differs from the served one")
	}
}

func TestAPausedDownloadGivesItsConnectionToTheNextInTheQueueAndKeepsItsPlace(t *testing.T) {
	r, _ := setup(t)
	r.noStall()
	file := func(name string, kib int) string {
		return r.cdn.Add(name, debridfake.File{Data: debridfake.MKV(kib << 10), Pace: 40 * time.Millisecond})
	}
	first := r.enqueue(movieJob(file("a.mkv", 6000), "a.mkv"))
	second := r.enqueue(episodeJob(file("b.mkv", 6001), "b.mkv"))
	third := episodeJob(file("c.mkv", 402), "c.mkv")
	third.Number = 2
	queued := r.enqueue(third)
	r.waitPercent(first.ID, 1)
	r.waitPercent(second.ID, 1)
	if d := r.download(queued.ID); d["state"] != "queued" {
		t.Fatalf("the third download should wait for a place: %v", d)
	}

	r.mustCall("POST", "/api/downloads/"+first.ID+"/pause", "", 200)
	waitFor(t, "the queued download to start", 10*time.Second, func() bool { return r.download(queued.ID)["state"] != "queued" })
	var order []string
	for _, d := range r.downloads() {
		order = append(order, d.(map[string]any)["id"].(string))
	}
	if len(order) == 0 || order[0] != first.ID {
		t.Errorf("the paused download lost its place in the queue: %v", order)
	}
	if n := r.cdn.MaxFiles(); n > 2 {
		t.Errorf("%d files streamed at once", n)
	}
}

func TestCancellingARunningDownloadDeletesThePartialBlocklistsItAndSearchesForAnotherRelease(t *testing.T) {
	r := newSchedRig(t, false)
	slow := r.cdn.Add("first.mkv", debridfake.File{Data: debridfake.MKV(epSize), Pace: 10 * time.Millisecond})
	fast := r.cdn.Add("second.mkv", debridfake.File{Data: debridfake.MKV(epSize)})
	r.aio.Answer("series", ep1ID, results(
		listed{name: "Dune.Prophecy.S01E01.1080p.first.mkv", size: epSize, url: slow, season: 1, ep: 1},
		listed{name: "Dune.Prophecy.S01E01.720p.second.mkv", size: epSize, url: fast, season: 1, ep: 1},
	))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	var id string
	waitFor(t, "the first release to be downloading", 10*time.Second, func() bool {
		ds := r.downloads()
		if len(ds) != 1 {
			return false
		}
		d := ds[0].(map[string]any)
		id = d["id"].(string)
		return d["state"] == "downloading" && d["bytesDone"].(float64) > 0
	})
	tmp := r.env["MARQUEE_TEMP_PATH"]
	if len(tree(t, tmp)) == 0 {
		t.Fatal("no partial file to delete")
	}

	resp := r.call("POST", "/api/downloads/"+id+"/cancel", "")
	if resp.status != 204 {
		t.Fatalf("cancel: %d %s", resp.status, resp.body)
	}
	if n := r.cdn.Streaming(); n != 0 {
		t.Errorf("%d transfers still streaming after the cancel answered", n)
	}
	if got := tree(t, tmp); len(got) != 0 {
		t.Errorf("the cancel left %v in the temp folder", got)
	}
	if ds := r.downloads(); len(ds) != 0 {
		t.Errorf("the cancelled download is still queued: %v", ds)
	}
	h := r.entries("cancelled")
	if len(h) != 1 || h[0]["retryable"] != false || h[0]["id"] != id {
		t.Fatalf("history of the cancel = %v (all: %v)", h, r.history())
	}
	if f := r.entries("failed"); len(f) != 0 {
		t.Errorf("a cancel is not a failure: %v", f)
	}
	if resp := r.call("POST", "/api/history/"+id+"/retry", ""); resp.status != 409 {
		t.Errorf("retrying a cancel: %d, want 409", resp.status)
	}
	for _, e := range r.getTitle("series-1408")["seasons"].([]any)[0].(map[string]any)["episodes"].([]any) {
		if m := e.(map[string]any); m["id"] == "s01e01" && (m["monitored"] != true || m["state"] != "wanted") {
			t.Errorf("the cancelled episode = %v, want it monitored and wanted", m)
		}
	}

	r.advance(5 * time.Second)
	r.step()
	waitFor(t, "the other release to be imported", 10*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	var played []string
	for _, q := range r.cdn.Requests() {
		if q.Playback && q.Probe {
			played = append(played, strings.TrimPrefix(q.Path, "/play/SECRETPLAY-"))
		}
	}
	if !slices.Equal(played, []string{"first.mkv", "second.mkv"}) {
		t.Errorf("played %v: the cancelled release is blocklisted and the next one taken", played)
	}
}

func TestCancellingAQueuedDownloadBlocklistsItWithoutEverFetchingIt(t *testing.T) {
	r, _ := setup(t)
	r.noStall()
	file := func(name string, kib int) string {
		return r.cdn.Add(name, debridfake.File{Data: debridfake.MKV(kib << 10), Pace: 40 * time.Millisecond})
	}
	first := r.enqueue(movieJob(file("a.mkv", 6000), "a.mkv"))
	second := r.enqueue(episodeJob(file("b.mkv", 6001), "b.mkv"))
	third := episodeJob(file("c.mkv", 402), "c.mkv")
	third.Number, third.Release.InfoHash = 2, "cccccccccccccccccccccccccccccccccccccccc"
	queued := r.enqueue(third)
	r.waitPercent(first.ID, 1)
	r.waitPercent(second.ID, 1)

	resp := r.call("POST", "/api/downloads/"+queued.ID+"/cancel", "")
	if resp.status != 204 {
		t.Fatalf("cancel: %d %s", resp.status, resp.body)
	}
	if r.download(queued.ID) != nil {
		t.Error("the cancelled download is still in the queue")
	}
	if !r.blocked(third.Release) {
		t.Error("the cancelled release is not blocklisted")
	}
	r.waitHistoryKind("imported", 2)
	if streamedFrom(r.cdn, 0, "c.mkv") {
		t.Error("the cancelled download was fetched")
	}
}

func TestPausingResumingAndCancellingByTitleChangeAllItsDownloadsAndNoOther(t *testing.T) {
	r, _ := setup(t)
	r.noStall()
	file := func(name string, kib int) string {
		return r.cdn.Add(name, debridfake.File{Data: debridfake.MKV(kib << 10), Pace: 40 * time.Millisecond})
	}
	movie := r.enqueue(movieJob(file("a.mkv", 6000), "a.mkv"))
	ep1 := r.enqueue(episodeJob(file("b.mkv", 6001), "b.mkv"))
	j := episodeJob(file("c.mkv", 6002), "c.mkv")
	j.Number = 2
	ep2 := r.enqueue(j)
	r.waitPercent(movie.ID, 1)
	r.waitPercent(ep1.ID, 1)

	got := r.mustCall("POST", "/api/titles/series-1408/downloads/pause", "", 200)["downloads"].([]any)
	var ids []string
	for _, d := range got {
		m := d.(map[string]any)
		ids = append(ids, m["id"].(string))
		if m["state"] != "paused" {
			t.Errorf("after pausing the series: %v", m)
		}
	}
	slices.Sort(ids)
	want := []string{ep1.ID, ep2.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Errorf("pausing the series changed %v, want %v", ids, want)
	}
	for _, id := range want {
		if d := r.download(id); d["state"] != "paused" {
			t.Errorf("episode download %v", d)
		}
	}
	if d := r.download(movie.ID); d["state"] != "downloading" {
		t.Errorf("the movie was touched: %v", d)
	}

	got = r.mustCall("POST", "/api/titles/series-1408/downloads/resume", "", 200)["downloads"].([]any)
	if len(got) != 2 {
		t.Errorf("resuming the series changed %v", got)
	}
	for _, id := range want {
		if d := r.download(id); d["state"] == "paused" {
			t.Errorf("still paused after resume: %v", d)
		}
	}

	resp := r.call("POST", "/api/titles/series-1408/downloads/cancel", "")
	if resp.status != 204 {
		t.Fatalf("cancel the series: %d %s", resp.status, resp.body)
	}
	for _, id := range want {
		if d := r.download(id); d != nil {
			t.Errorf("still queued after the cancel: %v", d)
		}
	}
	if d := r.download(movie.ID); d == nil || d["state"] == "paused" {
		t.Errorf("the movie was touched: %v", d)
	}
	if n := len(r.waitHistoryKind("cancelled", 2)); n < 2 {
		t.Errorf("history: %v", r.history())
	}
	r.waitHistoryKind("imported", 1)
}

func TestAPausedDownloadIsStillPausedAfterARestart(t *testing.T) {
	r, root := setup(t)
	url := r.cdn.Add("big.mkv", debridfake.File{Data: debridfake.MKV(3 << 20), Pace: 8 * time.Millisecond})
	d := r.enqueue(movieJob(url, "big.mkv"))
	r.waitPercent(d.ID, 15)
	r.mustCall("POST", "/api/downloads/"+d.ID+"/pause", "", 200)
	r.stop()

	seen := len(r.cdn.Requests())
	r.startService()
	time.Sleep(200 * time.Millisecond)
	if got := r.download(d.ID); got == nil || got["state"] != "paused" {
		t.Fatalf("after a restart: %v", got)
	}
	if n := len(r.cdn.Requests()); n != seen {
		t.Errorf("a paused download made %d requests after the restart", n-seen)
	}
	if len(tree(t, filepath.Join(root, "tmp"))) == 0 {
		t.Error("the restart threw the paused partial away")
	}
	r.mustCall("POST", "/api/downloads/"+d.ID+"/resume", "", 200)
	r.waitHistoryKind("imported", 1)
}

func TestPauseResumeAndCancelAnswer404ForAnUnknownDownloadAnd409ForAFinishedOne(t *testing.T) {
	r, _ := setup(t)
	url := r.cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(300 << 10)})
	d := r.enqueue(movieJob(url, "a.mkv"))
	r.waitHistoryKind("imported", 1)
	for _, op := range []string{"pause", "resume", "cancel"} {
		if resp := r.call("POST", "/api/downloads/no-such-id/"+op, ""); resp.status != 404 {
			t.Errorf("%s an unknown download: %d %s", op, resp.status, resp.body)
		}
		if resp := r.call("POST", "/api/downloads/"+d.ID+"/"+op, ""); resp.status != 409 {
			t.Errorf("%s a finished download: %d %s", op, resp.status, resp.body)
		}
		if resp := r.call("POST", "/api/titles/movie-1/downloads/"+op, ""); resp.status != 404 {
			t.Errorf("%s by an unknown title: %d %s", op, resp.status, resp.body)
		}
	}
	if got := r.mustCall("POST", "/api/titles/movie-438631/downloads/pause", "", 200)["downloads"].([]any); len(got) != 0 {
		t.Errorf("pausing a title with nothing running changed %v", got)
	}
}

func TestPausingACachingDownloadFreesItsTorBoxSlotAndLeavesTheTorrent(t *testing.T) {
	r := newTBRig(t, map[string]string{"MARQUEE_TORBOX_MAX_ACTIVE": "1"})
	r.episodeFile(hashA, ep1Name)
	r.episodeFile(hashB, ep2Name)
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	r.aio.Answer("series", ep2ID, results(listed{name: ep2Name, size: epSize, uncached: true, hash: hashB, season: 1, ep: 2}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.grabEpisodes("s01e01", "s01e02")
	first := r.torrentIDs(1)[0]
	r.tb.Step(first, 0.3, 100)
	r.waitStates("caching", 1)
	r.waitStates("queued", 1)
	var caching string
	for _, d := range r.downloads() {
		if m := d.(map[string]any); m["state"] == "caching" {
			caching = m["id"].(string)
		}
	}

	r.mustCall("POST", "/api/downloads/"+caching+"/pause", "", 200)
	waitFor(t, "the queued episode to take the TorBox slot", 10*time.Second, func() bool { return len(r.tb.Magnets()) == 2 })
	r.waitStates("paused", 1)
	if got := r.tb.Deleted(); len(got) != 0 {
		t.Errorf("pausing deleted torrents %v", got)
	}

	r.mustCall("POST", "/api/downloads/"+caching+"/resume", "", 200)
	time.Sleep(100 * time.Millisecond)
	r.waitStates("queued", 1)
	r.tb.Present(2)
	waitFor(t, "the second episode's import", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	r.tb.Present(first)
	waitFor(t, "the first episode's import", 30*time.Second, func() bool { return len(r.entries("imported")) == 2 })
	if n := len(r.tb.Magnets()); n != 2 {
		t.Errorf("the resumed download asked TorBox for a new torrent: %d magnets", n)
	}
}

func TestCancellingACachingDownloadDeletesItsTorrentUnlessASiblingStillNeedsIt(t *testing.T) {
	r := newTBRig(t, nil)
	r.seasonPack()
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.grabEpisodes("s01e01", "s01e02")
	id := r.torrentIDs(1)[0]
	r.tb.Step(id, 0.3, 100)
	r.waitStates("caching", 2)
	ds := r.downloads()
	one, two := ds[0].(map[string]any)["id"].(string), ds[1].(map[string]any)["id"].(string)

	if resp := r.call("POST", "/api/downloads/"+one+"/cancel", ""); resp.status != 204 {
		t.Fatalf("cancel: %d %s", resp.status, resp.body)
	}
	if got := r.tb.Deleted(); len(got) != 0 {
		t.Fatalf("the torrent went while the other episode still needs it: %v", got)
	}
	if resp := r.call("POST", "/api/downloads/"+two+"/cancel", ""); resp.status != 204 {
		t.Fatalf("cancel: %d %s", resp.status, resp.body)
	}
	if got := r.tb.Deleted(); !slices.Equal(got, []int{id}) {
		t.Errorf("deleted %v, want the pack once its last download was cancelled", got)
	}
	if len(r.downloads()) != 0 {
		t.Errorf("queue: %v", r.downloads())
	}
}

func TestCancellingAManualGrabOfABackedOffEpisodeSearchesItAgainAtOnce(t *testing.T) {
	r := newSchedRig(t, false)
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	r.advance(time.Minute)
	r.step()
	if n := r.searchedID(ep1ID); n != 1 {
		t.Fatalf("searched the episode %d times, want once and then a backoff", n)
	}

	slow := r.cdn.Add("first.mkv", debridfake.File{Data: debridfake.MKV(epSize), Pace: 10 * time.Millisecond})
	r.aio.Answer("series", ep1ID, results(listed{name: "Dune.Prophecy.S01E01.1080p.first.mkv", size: epSize, url: slow, season: 1, ep: 1}))
	cands, _ := r.releases("/api/titles/series-1408/releases?episode=s01e01")
	d := r.mustCall("POST", "/api/titles/series-1408/releases/"+cands[0]["id"].(string)+"/grab", `{"episode":"s01e01"}`, 202)
	id := d["id"].(string)
	waitFor(t, "the grab to be downloading", 10*time.Second, func() bool {
		got := r.download(id)
		return got != nil && got["state"] == "downloading"
	})
	if resp := r.call("POST", "/api/downloads/"+id+"/cancel", ""); resp.status != 204 {
		t.Fatalf("cancel: %d %s", resp.status, resp.body)
	}

	before := r.searchedID(ep1ID)
	r.advance(5 * time.Second)
	r.step()
	if got := r.searchedID(ep1ID); got != before+1 {
		t.Errorf("after the cancel the episode was searched %d times, want once at once, not after its backoff", got-before)
	}
}

// pausedWhileCreating grabs one uncached episode and lands a pause's write
// while TorBox is still creating its torrent, before the pause stops the
// transfer: the window between the two is what is tested.
func pausedWhileCreating(t *testing.T) (*tbRig, string) {
	r := newTBRig(t, nil)
	r.episodeFile(hashA, ep1Name)
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	created := make(chan struct{})
	r.tb.Script("/torrents/createtorrent", torboxfake.Step{Until: created})
	r.grabEpisodes("s01e01")
	waitFor(t, "the torrent to be asked for", 10*time.Second, func() bool { return r.tb.Hits("/torrents/createtorrent") == 1 })
	id := r.downloads()[0].(map[string]any)["id"].(string)

	if _, err := r.db().Exec(`UPDATE downloads SET paused = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	close(created)
	waitFor(t, "TorBox to be polled for the new torrent", 10*time.Second, func() bool { return r.tb.Hits("/torrents/mylist") > 0 })
	r.mustCall("POST", "/api/downloads/"+id+"/pause", "", 200)
	return r, id
}

func TestCancellingADownloadPausedWhileItsTorrentWasCreatedDeletesTheTorrent(t *testing.T) {
	r, id := pausedWhileCreating(t)
	if resp := r.call("POST", "/api/downloads/"+id+"/cancel", ""); resp.status != 204 {
		t.Fatalf("cancel: %d %s", resp.status, resp.body)
	}
	if got := r.tb.Deleted(); !slices.Equal(got, []int{1}) {
		t.Errorf("deleted %v, want the torrent the paused download had made", got)
	}
}

func TestResumingADownloadPausedWhileItsTorrentWasCreatedKeepsThatTorrent(t *testing.T) {
	r, id := pausedWhileCreating(t)
	polled := r.tb.Hits("/torrents/mylist")
	r.mustCall("POST", "/api/downloads/"+id+"/resume", "", 200)
	waitFor(t, "the resumed download to poll TorBox", 10*time.Second, func() bool { return r.tb.Hits("/torrents/mylist") > polled })
	if n := len(r.tb.Magnets()); n != 1 {
		t.Errorf("the resumed download asked TorBox for %d torrents, want the one it had", n)
	}
}
