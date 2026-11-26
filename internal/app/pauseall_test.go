package app_test

import (
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/debridfake"
)

func (r *realSvc) queue() (downloads []any, paused bool) {
	r.t.Helper()
	got := r.mustCall("GET", "/api/downloads", "", 200)
	p, ok := got["paused"].(bool)
	if !ok {
		r.t.Fatalf("GET /api/downloads has no paused flag: %v", got)
	}
	return got["downloads"].([]any), p
}

func (r *realSvc) stateOf(id string) string {
	r.t.Helper()
	if d := r.download(id); d != nil {
		return d["state"].(string)
	}
	return ""
}

// threeDownloads queues two that run (the concurrency is 2) and one that waits.
func (r *rig) threeDownloads() (a, b, c string) {
	r.t.Helper()
	file := func(name string, kib int) string {
		return r.cdn.Add(name, debridfake.File{Data: debridfake.MKV(kib << 10), Pace: 30 * time.Millisecond})
	}
	first := r.enqueue(movieJob(file("a.mkv", 4000), "a.mkv"))
	second := r.enqueue(episodeJob(file("b.mkv", 4001), "b.mkv"))
	third := episodeJob(file("c.mkv", 402), "c.mkv")
	third.Number = 2
	queued := r.enqueue(third)
	r.waitPercent(first.ID, 10)
	r.waitPercent(second.ID, 10)
	return first.ID, second.ID, queued.ID
}

func TestPauseAllStopsEveryTransferStartsNothingAndResumeAllContinuesInOrder(t *testing.T) {
	r, root := setup(t)
	r.noStall()
	a, b, c := r.threeDownloads()
	if _, paused := r.queue(); paused {
		t.Fatal("Pause all is on before anyone asked for it")
	}

	got := r.mustCall("POST", "/api/downloads/pause", "", 200)
	if got["paused"] != true || len(got["downloads"].([]any)) != 3 {
		t.Fatalf("pause all answered %v", got)
	}
	if n := r.cdn.Streaming(); n != 0 {
		t.Errorf("%d transfers still streaming after Pause all answered", n)
	}
	if len(tree(t, filepath.Join(root, "tmp"))) == 0 {
		t.Fatal("Pause all threw the partial files away")
	}
	if s := r.stateOf(a) + "/" + r.stateOf(b) + "/" + r.stateOf(c); s != "paused/paused/queued" {
		t.Errorf("states under Pause all = %s, want paused/paused/queued", s)
	}
	before, seen := r.download(a), len(r.cdn.Requests())
	late := episodeJob(r.cdn.Add("d.mkv", debridfake.File{Data: debridfake.MKV(403 << 10)}), "d.mkv")
	late.Number = 3
	d := r.enqueue(late)
	time.Sleep(300 * time.Millisecond)
	if s := r.stateOf(d.ID); s != "queued" {
		t.Errorf("a download queued under Pause all is %q", s)
	}
	if after := r.download(a); after["bytesDone"] != before["bytesDone"] || after["bytesPerSecond"] != float64(0) {
		t.Errorf("a paused download moved or still has a rate: %v then %v", before, after)
	}
	if n := len(r.cdn.Requests()); n != seen {
		t.Errorf("%d requests were made under Pause all", n-seen)
	}
	if again := r.mustCall("POST", "/api/downloads/pause", "", 200); again["paused"] != true {
		t.Errorf("a second Pause all answered %v", again)
	}

	got = r.mustCall("POST", "/api/downloads/resume", "", 200)
	if got["paused"] != false {
		t.Fatalf("resume all answered %v", got)
	}
	waitFor(t, "both paused downloads to run again", 10*time.Second, func() bool {
		return r.stateOf(a) == "downloading" && r.stateOf(b) == "downloading"
	})
	if s := r.stateOf(c); s != "queued" {
		t.Errorf("the queued download jumped the paused ones: %q", s)
	}
	waitFor(t, "the queued downloads to start in their turn", 15*time.Second, func() bool { return r.stateOf(c) != "queued" })
	r.waitHistoryKind("imported", 4)
	if again := r.mustCall("POST", "/api/downloads/resume", "", 200); again["paused"] != false {
		t.Errorf("a second Resume all answered %v", again)
	}
}

func TestPauseAllSurvivesARestartAndNothingDownloadsUntilResumeAll(t *testing.T) {
	r, root := setup(t)
	a, _, _ := r.threeDownloads()
	r.mustCall("POST", "/api/downloads/pause", "", 200)
	r.stop()

	seen := len(r.cdn.Requests())
	r.startService()
	time.Sleep(300 * time.Millisecond)
	if _, paused := r.queue(); !paused {
		t.Fatal("Pause all was forgotten by the restart")
	}
	if s := r.stateOf(a); s != "paused" {
		t.Errorf("after the restart the running download is %q", s)
	}
	if n := len(r.cdn.Requests()); n != seen {
		t.Errorf("%d requests after a restart under Pause all", n-seen)
	}
	if len(tree(t, filepath.Join(root, "tmp"))) == 0 {
		t.Error("the restart threw the partial files away")
	}
	r.mustCall("POST", "/api/downloads/resume", "", 200)
	waitFor(t, "the download to run again", 10*time.Second, func() bool { return r.stateOf(a) == "downloading" })
}

func TestResumeAllLeavesWhatWasPausedOnItsOwnPaused(t *testing.T) {
	r, _ := setup(t)
	r.noStall()
	a, b, _ := r.threeDownloads()
	r.mustCall("POST", "/api/downloads/"+a+"/pause", "", 200)
	r.mustCall("POST", "/api/downloads/pause", "", 200)
	r.mustCall("POST", "/api/downloads/resume", "", 200)

	waitFor(t, "the other download to run again", 10*time.Second, func() bool { return r.stateOf(b) == "downloading" })
	time.Sleep(200 * time.Millisecond)
	if s := r.stateOf(a); s != "paused" {
		t.Errorf("a download paused on its own is %q after Resume all", s)
	}
	r.mustCall("POST", "/api/downloads/"+a+"/resume", "", 200)
	waitFor(t, "the download resumed on its own to run", 10*time.Second, func() bool { return r.stateOf(a) == "downloading" })
}

func TestADownloadingFileReportsItsRateInTheListAndInProgressEvents(t *testing.T) {
	r, _ := setup(t)
	r.noStall()
	s := r.openStream()
	url := r.cdn.Add("big.mkv", debridfake.File{Data: debridfake.MKV(24 << 20), Pace: 40 * time.Millisecond})
	d := r.enqueue(movieJob(url, "big.mkv"))
	r.waitPercent(d.ID, 25)

	t0, done0 := time.Now(), r.download(d.ID)["bytesDone"].(float64)
	time.Sleep(time.Second)
	got := r.download(d.ID)
	real := (got["bytesDone"].(float64) - done0) / time.Since(t0).Seconds()
	rate, ok := got["bytesPerSecond"].(float64)
	if !ok || rate <= 0 || rate < real/2 || rate > real*2 {
		t.Errorf("bytesPerSecond = %v (present %v), but %.0f bytes a second really arrived", got["bytesPerSecond"], ok, real)
	}
	ev := s.next("a progress event with a rate", func(e sseEvent) bool {
		v, _ := e.data["bytesPerSecond"].(float64)
		return e.name == "progress" && e.data["downloadId"] == d.ID && v > 0
	})
	if v := ev.data["bytesPerSecond"].(float64); v < real/2 || v > real*2 {
		t.Errorf("the progress event says %v bytes a second, but %.0f really arrived", v, real)
	}

	r.mustCall("POST", "/api/downloads/"+d.ID+"/pause", "", 200)
	if v := r.download(d.ID)["bytesPerSecond"]; v != float64(0) {
		t.Errorf("a paused download has a rate of %v", v)
	}
	s.next("a progress event of the paused download", func(e sseEvent) bool {
		return e.name == "progress" && e.data["downloadId"] == d.ID && e.data["state"] == "paused" && e.data["bytesPerSecond"] == float64(0)
	})
}

func TestTheEventStreamAnnouncesPauseAllAndResumeAll(t *testing.T) {
	r, _ := setup(t)
	s := r.openStream()
	r.mustCall("POST", "/api/downloads/pause", "", 200)
	s.next("the downloads event for Pause all", func(e sseEvent) bool { return e.name == "downloads" && e.data["paused"] == true })
	r.mustCall("POST", "/api/downloads/resume", "", 200)
	s.next("the downloads event for Resume all", func(e sseEvent) bool { return e.name == "downloads" && e.data["paused"] == false })
}

func TestPauseAllStartsNoNewTorBoxCacheAndLeavesTheTorrentsAlone(t *testing.T) {
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

	r.mustCall("POST", "/api/downloads/pause", "", 200)
	r.waitStates("paused", 1)
	r.waitStates("queued", 1)
	r.tb.Present(first)
	time.Sleep(300 * time.Millisecond)
	if n := len(r.tb.Magnets()); n != 1 {
		t.Errorf("Pause all let a new torrent be started: %d magnets", n)
	}
	if got := r.tb.Deleted(); len(got) != 0 {
		t.Errorf("Pause all deleted torrents %v", got)
	}
	if got := r.entries("imported"); len(got) != 0 {
		t.Errorf("a file was imported under Pause all: %v", got)
	}

	r.mustCall("POST", "/api/downloads/resume", "", 200)
	waitFor(t, "the queued episode to take the TorBox slot", 10*time.Second, func() bool { return len(r.tb.Magnets()) == 2 })
	r.tb.Present(2)
	waitFor(t, "both episodes to import", 30*time.Second, func() bool { return len(r.entries("imported")) == 2 })
	if n := len(r.tb.Magnets()); n != 2 {
		t.Errorf("Resume all asked TorBox for a torrent it already had: %d magnets", n)
	}
}

func TestPauseAllLandingBetweenADispatchPassReadingItAndStartingTheDownloadStopsThatDownload(t *testing.T) {
	r, _ := setup(t)
	r.noStall()
	reached, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	hold := func() {
		if once.CompareAndSwap(false, true) {
			close(reached)
			<-release
		}
	}
	r.holdDispatch.Store(&hold)
	var free sync.Once
	t.Cleanup(func() { free.Do(func() { close(release) }) })

	url := r.cdn.Add("slow.mkv", debridfake.File{Data: debridfake.MKV(4 << 20), Pace: 30 * time.Millisecond})
	d := r.enqueue(movieJob(url, "slow.mkv"))
	waitFor(t, "a dispatch pass to hold with the flag read as off", 10*time.Second, func() bool {
		select {
		case <-reached:
			return true
		default:
			return false
		}
	})
	r.mustCall("POST", "/api/downloads/pause", "", 200)
	free.Do(func() { close(release) })

	time.Sleep(500 * time.Millisecond)
	if n := len(r.cdn.Requests()); n != 0 {
		t.Errorf("%d requests were made by a download that started after Pause all", n)
	}
	if s := r.stateOf(d.ID); s != "queued" {
		t.Errorf("the download is %q, want it held back as queued", s)
	}
}

func TestResumingOneDownloadWhilePauseAllIsOnDoesNotStartIt(t *testing.T) {
	r, _ := setup(t)
	r.noStall()
	a, _, _ := r.threeDownloads()
	r.mustCall("POST", "/api/downloads/pause", "", 200)
	r.mustCall("POST", "/api/downloads/"+a+"/pause", "", 200)
	seen := len(r.cdn.Requests())

	r.mustCall("POST", "/api/downloads/"+a+"/resume", "", 200)
	time.Sleep(300 * time.Millisecond)
	if s := r.stateOf(a); s != "paused" {
		t.Errorf("a download resumed under Pause all is %q, want paused", s)
	}
	if n := len(r.cdn.Requests()); n != seen {
		t.Errorf("%d requests were made under Pause all: %+v", n-seen, r.cdn.Requests()[seen:])
	}
	r.mustCall("POST", "/api/downloads/resume", "", 200)
	waitFor(t, "the download to run once Pause all ended", 10*time.Second, func() bool { return r.stateOf(a) == "downloading" })
}

func TestCancellingADownloadWhilePauseAllIsOnDeletesItsPartialAndRecordsTheCancel(t *testing.T) {
	r, root := setup(t)
	r.noStall()
	a, b, c := r.threeDownloads()
	r.mustCall("POST", "/api/downloads/pause", "", 200)

	if resp := r.call("POST", "/api/downloads/"+a+"/cancel", ""); resp.status != 204 {
		t.Fatalf("cancel: %d %s", resp.status, resp.body)
	}
	if r.download(a) != nil {
		t.Error("the cancelled download is still queued")
	}
	h := r.waitHistoryKind("cancelled", 1)
	if h[0].(map[string]any)["id"] != a {
		t.Errorf("history of the cancel = %v", h)
	}
	if got := len(tree(t, filepath.Join(root, "tmp"))); got == 0 {
		t.Error("the cancel threw away the other paused download's partial too")
	}
	if s := r.stateOf(b) + "/" + r.stateOf(c); s != "paused/queued" {
		t.Errorf("the others are %s, want paused/queued", s)
	}
}

func TestCancellingADownloadWhilePauseAllIsOnDeletesItsTorrentUnlessASiblingNeedsIt(t *testing.T) {
	r := newTBRig(t, nil)
	r.seasonPack()
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.grabEpisodes("s01e01", "s01e02")
	id := r.torrentIDs(1)[0]
	r.tb.Step(id, 0.3, 100)
	r.waitStates("caching", 2)
	ds := r.downloads()
	one, two := ds[0].(map[string]any)["id"].(string), ds[1].(map[string]any)["id"].(string)
	r.mustCall("POST", "/api/downloads/pause", "", 200)

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
	if n := len(r.entries("cancelled")); n != 2 {
		t.Errorf("%d cancelled entries in the history, want 2", n)
	}
}
