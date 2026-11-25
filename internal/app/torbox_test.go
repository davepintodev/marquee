package app_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/torboxfake"
)

const (
	ep1Name = "Dune.Prophecy.S01E01.1080p.WEB-DL.mkv"
	ep2Name = "Dune.Prophecy.S01E02.1080p.WEB-DL.mkv"
	ep1ID   = "tvdb:403000:1:1"
	ep2ID   = "tvdb:403000:1:2"
	hashA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	epSize  = 53 << 20
)

type tbRig struct {
	*schedRig
	tb *torboxfake.Fake
}

// newTBRig is marquee with TorBox set up against the fake.
func newTBRig(t *testing.T, env map[string]string) *tbRig {
	t.Helper()
	tb := torboxfake.New()
	t.Cleanup(tb.Close)
	r := newSchedRigTweaked(t, false, newTMDB(t), func(e map[string]string) {
		e["MARQUEE_TORBOX_API_KEY"] = torboxfake.Key
		for k, v := range env {
			e[k] = v
		}
	}, func(o *app.Options) {
		o.TorBoxAPIBase = tb.APIBase()
		o.TorBoxPoll = 10 * time.Millisecond
	})
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(r.logs.String())
		}
	})
	return &tbRig{r, tb}
}

// episodeFile makes TorBox hold a one-file torrent of the episode, once cached.
// Its bytes differ per hash, or the duplicate-episode check would reject the second.
func (r *tbRig) episodeFile(hash, name string) {
	body := debridfake.MKV(epSize)
	for _, off := range []int{1 << 20, epSize / 2, epSize - 1000} {
		body[off] = hash[0]
	}
	r.tb.SetFiles(hash, torboxfake.File{ID: 0, Name: name, Size: epSize, Body: body})
}

func (r *tbRig) torrentIDs(n int) []int {
	r.t.Helper()
	waitFor(r.t, fmt.Sprintf("%d torrents", n), 10*time.Second, func() bool { return len(r.tb.Magnets()) >= n })
	ids := make([]int, n)
	for i := range ids {
		ids[i] = i + 1
	}
	return ids
}

func (r *tbRig) waitCaching(percent int) {
	r.t.Helper()
	waitFor(r.t, fmt.Sprintf("caching %d%%", percent), 10*time.Second, func() bool {
		ds := r.downloads()
		return len(ds) == 1 && ds[0].(map[string]any)["state"] == "caching" && ds[0].(map[string]any)["percent"] == float64(percent)
	})
}

func healthIDs(r *realSvc) []string {
	var ids []string
	for _, c := range r.mustCall("GET", "/api/health", "", 200)["checks"].([]any) {
		ids = append(ids, c.(map[string]any)["id"].(string))
	}
	return ids
}

func TestWithoutATorBoxKeyMarqueeStaysCachedOnlyAndHealthOmitsTorBox(t *testing.T) {
	r := newSchedRig(t, false)
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	if slices.Contains(healthIDs(r.realSvc), "torbox") {
		t.Fatalf("health lists TorBox without a key: %v", healthIDs(r.realSvc))
	}
	cands, dropped := r.releases("/api/titles/series-1408/releases?episode=s01e01")
	if len(cands) != 0 || len(dropped) != 1 {
		t.Fatalf("the uncached release must be dropped: %v %v", cands, dropped)
	}
	r.mustCall("POST", "/api/titles/series-1408/releases/"+dropped[0]["id"].(string)+"/grab", `{"episode":"s01e01"}`, 409)
	r.step()
	if d := r.downloads(); len(d) != 0 {
		t.Fatalf("a cached-only marquee queued %v", d)
	}
}

func TestHealthProbesTorBoxWhenAKeyIsSet(t *testing.T) {
	r := newTBRig(t, nil)
	if ids := healthIDs(r.realSvc); !slices.Contains(ids, "torbox") {
		t.Fatalf("health = %v", ids)
	}
	h := r.mustCall("GET", "/api/health", "", 200)
	for _, c := range h["checks"].([]any) {
		if m := c.(map[string]any); m["id"] == "torbox" && (m["status"] != "ok" || m["name"] != "TorBox") {
			t.Errorf("torbox check = %v", m)
		}
	}
	r.tb.Reject()
	for _, c := range r.mustCall("GET", "/api/health", "", 200)["checks"].([]any) {
		if m := c.(map[string]any); m["id"] == "torbox" {
			if m["status"] != "error" || strings.Contains(fmt.Sprint(m), torboxfake.Key) {
				t.Errorf("a refused key: %v", m)
			}
		}
	}
	noSecrets(t, "the logs", r.logs.String())
}

func TestAnUncachedReleaseIsGrabbableInInteractiveSearchAndCachedOnesComeFirst(t *testing.T) {
	r := newTBRig(t, nil)
	r.aio.Answer("series", ep1ID, results(
		listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1},
		listed{name: "Dune.Prophecy.S01E01.720p.WEB-DL.mkv", size: epSize, url: r.cdn.Add("c.mkv", debridfake.File{Data: debridfake.MKV(epSize)}), hash: hashB, season: 1, ep: 1},
	))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	cands, dropped := r.releases("/api/titles/series-1408/releases?episode=s01e01")
	if len(cands) != 2 || len(dropped) != 0 || cands[0]["cached"] != true || cands[1]["cached"] != false {
		t.Fatalf("cached first, then the uncached one: %v %v", cands, dropped)
	}
	r.episodeFile(hashA, ep1Name)
	d := r.mustCall("POST", "/api/titles/series-1408/releases/"+cands[1]["id"].(string)+"/grab", `{"episode":"s01e01"}`, 202)
	if d["titleId"] != "series-1408" {
		t.Errorf("download = %v", d)
	}
	r.torrentIDs(1)
}

func TestAutoSearchTakesTheCachedReleaseEvenWhenTheUncachedOneIsBetter(t *testing.T) {
	r := newTBRig(t, nil)
	url := r.cdn.Add("c.mkv", debridfake.File{Data: debridfake.MKV(epSize)})
	r.aio.Answer("series", ep1ID, results(
		listed{name: "Dune.Prophecy.S01E01.2160p.WEB-DL.mkv", size: epSize, uncached: true, hash: hashA, season: 1, ep: 1},
		listed{name: ep1Name, size: epSize, url: url, hash: hashB, season: 1, ep: 1},
	))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	waitFor(t, "the import", 20*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	if n := r.tb.Hits("/torrents/createtorrent"); n != 0 {
		t.Fatalf("TorBox was asked to fetch %d torrents although a cached release was there", n)
	}
}

func TestAnUncachedReleaseCachesShowsItsPercentThenImportsAndTheTorrentIsDeleted(t *testing.T) {
	r := newTBRig(t, nil)
	r.episodeFile(hashA, "Dune.Prophecy.S01/"+ep1Name)
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	s := r.openStream()
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	id := r.torrentIDs(1)[0]
	r.tb.Step(id, 0.42, 1000)
	r.waitCaching(42)

	b := s.next("a caching badge", isTitle("series-1408", "caching"))
	if got := b.data["badge"].(map[string]any); got["label"] == "" {
		t.Errorf("badge = %v", got)
	}
	p := s.next("a caching progress event", func(e sseEvent) bool { return e.name == "progress" && e.data["percent"] == float64(42) })
	if p.data["state"] != "caching" || p.data["episode"] != "s01e01" {
		t.Errorf("progress = %v", p.data)
	}
	title := r.getTitle("series-1408")
	badge := title["badge"].(map[string]any)
	if badge["state"] != "caching" || badge["label"] != "Caching 42%" || badge["percent"] != float64(42) {
		t.Errorf("title badge = %v", badge)
	}
	var ep map[string]any
	for _, e := range title["seasons"].([]any)[0].(map[string]any)["episodes"].([]any) {
		if e.(map[string]any)["id"] == "s01e01" {
			ep = e.(map[string]any)
		}
	}
	if ep["state"] != "caching" || ep["percent"] != float64(42) {
		t.Errorf("episode = %v", ep)
	}

	r.tb.Step(id, 0.8, 1000)
	r.waitCaching(80)
	r.tb.Present(id)
	waitFor(t, "the import", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	if got := tree(t, r.env["MARQUEE_SERIES_PATH"]); len(got) != 1 || !strings.Contains(got[0], "S01E01") {
		t.Errorf("library: %v", got)
	}
	waitFor(t, "the torrent to be deleted", 10*time.Second, func() bool { return slices.Equal(r.tb.Deleted(), []int{id}) })
	noSecrets(t, "the logs", r.logs.String())
}

func TestTheTorrentStaysOnTorBoxWhenDeleteOnCompleteIsOff(t *testing.T) {
	r := newTBRig(t, map[string]string{"MARQUEE_TORBOX_DELETE_ON_COMPLETE": "false"})
	r.episodeFile(hashA, ep1Name)
	r.tb.Cached(hashA)
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	waitFor(t, "the import", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	time.Sleep(100 * time.Millisecond)
	if got := r.tb.Deleted(); len(got) != 0 {
		t.Fatalf("deleted %v", got)
	}
}

func TestARestartKeepsPollingTheSameTorrent(t *testing.T) {
	r := newTBRig(t, nil)
	r.episodeFile(hashA, ep1Name)
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	id := r.torrentIDs(1)[0]
	r.tb.Step(id, 0.5, 100)
	r.waitCaching(50)
	r.stop()
	if len(r.tb.Deleted()) != 0 {
		t.Fatal("a shutdown must leave the torrent on TorBox")
	}

	r.start()
	r.waitCaching(50)
	r.tb.Present(id)
	waitFor(t, "the import", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	if got := r.tb.Magnets(); len(got) != 1 {
		t.Fatalf("a second torrent was created after the restart: %v", got)
	}
}

func TestAStalledTorrentIsDeletedBlocklistedAndTheNextCandidateTried(t *testing.T) {
	r := newTBRig(t, nil)
	good := r.cdn.Add("good.mkv", debridfake.File{Data: debridfake.MKV(epSize)})
	r.episodeFile(hashA, ep1Name)
	// Only the uncached one exists at first; the cached one appears after it fails.
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	id := r.torrentIDs(1)[0]
	r.tb.Step(id, 0.3, 100)
	r.waitCaching(30)
	r.tb.Stall(id)
	r.advance(29 * time.Minute)
	time.Sleep(100 * time.Millisecond)
	if d := r.downloads(); len(d) != 1 {
		t.Fatalf("29 minutes without progress must not fail it: %v", d)
	}
	r.advance(2 * time.Minute)
	waitFor(t, "the failure", 10*time.Second, func() bool { return len(r.entries("failed")) == 1 })
	if e := r.entries("failed")[0]; e["reason"] != "stalled" {
		t.Errorf("failure = %v", e)
	}
	if got := r.tb.Deleted(); !slices.Equal(got, []int{id}) {
		t.Fatalf("deleted %v", got)
	}

	r.aio.Answer("series", ep1ID, results(
		listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1},
		listed{name: "Dune.Prophecy.S01E01.720p.WEB-DL.mkv", size: epSize, url: good, hash: hashB, season: 1, ep: 1},
	))
	r.advance(5 * time.Second)
	r.step()
	waitFor(t, "the next candidate to import", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	if n := len(r.tb.Magnets()); n != 1 {
		t.Errorf("the blocklisted release was fetched again: %d torrents", n)
	}
}

func TestATorrentStillCachingAfterTheMaxLifetimeIsDeletedAndFailsAsCachingTimeout(t *testing.T) {
	r := newTBRig(t, map[string]string{"MARQUEE_TORBOX_MAX_LIFETIME": "2h"})
	r.episodeFile(hashA, ep1Name)
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	id := r.torrentIDs(1)[0]
	r.tb.Step(id, 0.1, 100)
	r.waitCaching(10)
	r.advance(20 * time.Minute)
	r.tb.Step(id, 0.2, 100)
	r.waitCaching(20)
	r.advance(100 * time.Minute)
	r.tb.Step(id, 0.3, 100)
	waitFor(t, "the failure", 10*time.Second, func() bool { return len(r.entries("failed")) == 1 })
	if e := r.entries("failed")[0]; e["reason"] != "caching_timeout" {
		t.Errorf("failure = %v", e)
	}
	if got := r.tb.Deleted(); !slices.Equal(got, []int{id}) {
		t.Fatalf("deleted %v", got)
	}
}

func TestActiveCachingNeverExceedsTheCapAndTheNextGrabWaitsQueued(t *testing.T) {
	r := newTBRig(t, map[string]string{"MARQUEE_TORBOX_MAX_ACTIVE": "1"})
	r.episodeFile(hashA, ep1Name)
	r.episodeFile(hashB, ep2Name)
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	r.aio.Answer("series", ep2ID, results(listed{name: ep2Name, size: epSize, uncached: true, hash: hashB, season: 1, ep: 2}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	for _, ep := range []string{"s01e01", "s01e02"} {
		cands, _ := r.releases("/api/titles/series-1408/releases?episode=" + ep)
		r.mustCall("POST", "/api/titles/series-1408/releases/"+cands[0]["id"].(string)+"/grab", `{"episode":"`+ep+`"}`, 202)
	}
	id := r.torrentIDs(1)[0]
	r.tb.Step(id, 0.5, 100)
	time.Sleep(300 * time.Millisecond)
	if n := len(r.tb.Magnets()); n != 1 {
		t.Fatalf("a second torrent was started past the cap: %d", n)
	}
	states := map[string]int{}
	for _, d := range r.downloads() {
		states[d.(map[string]any)["state"].(string)]++
	}
	if states["caching"] != 1 || states["queued"] != 1 {
		t.Fatalf("states = %v", states)
	}

	r.tb.Present(id)
	waitFor(t, "the second torrent", 30*time.Second, func() bool { return len(r.tb.Magnets()) == 2 })
	r.tb.Present(2)
	waitFor(t, "both imports", 60*time.Second, func() bool { return len(r.entries("imported")) == 2 })
}

func TestTheSchedulerDefersAnUncachedReleaseWhenFullWithoutSpendingBudgetOrBackingOff(t *testing.T) {
	r := newTBRig(t, map[string]string{"MARQUEE_TORBOX_MAX_ACTIVE": "1"})
	r.episodeFile(hashA, ep1Name)
	r.episodeFile(hashB, ep2Name)
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	r.aio.Answer("series", ep2ID, results(listed{name: ep2Name, size: epSize, uncached: true, hash: hashB, season: 1, ep: 2}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	first := r.torrentIDs(1)[0]
	r.tb.Step(first, 0.5, 100)
	r.waitCaching(50)

	for i := 0; i < 6; i++ {
		r.advance(10 * time.Second)
		r.step()
	}
	if got := r.searchedID(ep2ID); got != 1 {
		t.Fatalf("the deferred episode was searched %d times, want 1", got)
	}
	if n := len(r.tb.Magnets()); n != 1 {
		t.Fatalf("%d torrents", n)
	}
	if got := r.entries("searched"); len(got) != 0 {
		t.Errorf("a deferral is not a search that found nothing: %v", got)
	}
	db := r.db()
	var used int
	if err := db.QueryRow(`SELECT COALESCE(SUM(used), 0) FROM search_budget WHERE title_id = 'series-1408' AND number = 2`).Scan(&used); err != nil || used != 0 {
		t.Fatalf("the deferred episode spent %d searches of its budget (%v)", used, err)
	}
	var attempts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schedule WHERE title_id = 'series-1408' AND number = 2`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("the deferred episode has %d schedule rows (%v)", attempts, err)
	}

	r.tb.Present(first)
	waitFor(t, "the first import", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	r.advance(10 * time.Second)
	r.step()
	if got := r.searchedID(ep2ID); got != 2 {
		t.Fatalf("a free place makes the deferred episode searched again: %d", got)
	}
	waitFor(t, "the second torrent", 10*time.Second, func() bool { return len(r.tb.Magnets()) == 2 })
}

func TestTheUncachedColumnsSurviveInTheDownloadsTable(t *testing.T) {
	r := newTBRig(t, nil)
	r.episodeFile(hashA, ep1Name)
	r.aio.Answer("series", ep1ID, results(listed{name: ep1Name, size: epSize, uncached: true, hash: hashA, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	id := r.torrentIDs(1)[0]
	r.tb.Step(id, 0.25, 100)
	r.waitCaching(25)
	var cached, torrent, pct int
	var state string
	err := r.db().QueryRow(`SELECT cached, torbox_torrent_id, cache_percent, state FROM downloads`).Scan(&cached, &torrent, &pct, &state)
	if err != nil || cached != 0 || torrent != id || pct != 25 || state != catalog.StateCaching {
		t.Fatalf("row = cached %d torrent %d percent %d state %s (%v)", cached, torrent, pct, state, err)
	}
}
