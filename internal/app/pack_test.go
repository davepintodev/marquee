package app_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/tmdbfake"
	"github.com/davepintodev/marquee/internal/torboxfake"
)

const (
	packName  = "Dune.Prophecy.S01.1080p.WEB-DL.mkv"
	packDir   = "Dune.Prophecy.S01"
	hashC     = "cccccccccccccccccccccccccccccccccccccccc"
	sampleLen = 1 << 20
)

// packBody is an episode's bytes, distinct per episode so the
// duplicate-episode check passes.
func packBody(b byte) []byte {
	data := debridfake.MKV(epSize)
	for _, off := range []int{1 << 20, epSize / 2, epSize - 1000} {
		data[off] = b
	}
	return data
}

func (r *tbRig) packOf(hash, folder string, names []string, bodies map[string]bool) {
	var files []torboxfake.File
	for i, n := range names {
		f := torboxfake.File{ID: i, Name: folder + "/" + n, Size: epSize}
		if bodies == nil || bodies[n] {
			f.Body = packBody(byte('a' + i))
		}
		files = append(files, f)
	}
	r.tb.SetFiles(hash, files...)
}

func (r *tbRig) torrentOf() map[string]int {
	r.t.Helper()
	rows, err := r.db().Query(`SELECT season || 'x' || number, torbox_torrent_id FROM downloads`)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var ep string
		var id int
		if err := rows.Scan(&ep, &id); err != nil {
			r.t.Fatal(err)
		}
		out[ep] = id
	}
	return out
}

func (r *tbRig) noMutations(when string) {
	r.t.Helper()
	if c, d := r.tb.Hits("/torrents/createtorrent"), len(r.tb.Deleted()); c != 0 || d != 0 {
		r.t.Fatalf("%s: %d torrents created and %d deleted on TorBox", when, c, d)
	}
}

func TestACachedSeasonPackQueuesEveryMissingMonitoredEpisodeItHoldsFromOneTorrent(t *testing.T) {
	r := newTBRig(t, nil)
	var eps []tmdbfake.Episode
	for n := 1; n <= 6; n++ {
		eps = append(eps, tmdbfake.Episode{Number: n, Name: fmt.Sprintf("E%d", n), AirDate: "2020-01-01"})
	}
	r.tmdb.Add(tmdbfake.Title{Kind: "series", ID: 4000, Name: "Long", Date: "2020-01-01", IMDb: "tt4000000", TVDB: 4000,
		Seasons: []tmdbfake.Season{{Number: 1, Episodes: eps}}})
	r.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":4000}`, 201)
	r.mustCall("PATCH", "/api/titles/series-4000/episodes/s01e03", `{"monitored":false}`, 200)
	if _, err := r.db().Exec(`UPDATE episodes SET have_file = 1, file_path = 'Long/Season 01/e5.mkv' WHERE title_id = 'series-4000' AND number = 5`); err != nil {
		t.Fatal(err)
	}
	var names []string
	for n := 1; n <= 5; n++ {
		names = append(names, fmt.Sprintf("Long.S01E%02d.1080p.WEB-DL.mkv", n))
	}
	r.packOf(hashA, "Long.S01.1080p.WEB-DL", names, map[string]bool{names[0]: true, names[1]: true, names[3]: true})
	r.tb.Cached(hashA)
	single := r.cdn.Add("e1.mkv", debridfake.File{Data: debridfake.MKV(epSize)})
	r.aio.Answer("series", "tvdb:4000:1:1", results(
		listed{name: "Long.S01E01.2160p.WEB-DL.mkv", size: epSize, url: single, hash: hashB, season: 1, ep: 1},
		listed{name: "Long.S01.1080p.WEB-DL.mkv", size: 5 * epSize, hash: hashA, season: 1, pack: true},
	))

	r.step()
	waitFor(t, "three imports", 30*time.Second, func() bool { return len(r.entries("imported")) == 3 })
	if n := r.tb.Hits("/torrents/createtorrent"); n != 1 {
		t.Fatalf("one torrent for the pack, got %d", n)
	}
	got := r.torrentOf()
	if len(got) != 3 || got["1x1"] != 1 || got["1x2"] != 1 || got["1x4"] != 1 {
		t.Fatalf("downloads and their torrent = %v; want E01, E02 and E04 from torrent 1 (E03 unmonitored, E05 owned)", got)
	}
	waitFor(t, "the torrent to be deleted", 10*time.Second, func() bool { return len(r.tb.Deleted()) >= 1 })
	time.Sleep(100 * time.Millisecond)
	if d := r.tb.Deleted(); !slices.Equal(d, []int{1}) {
		t.Fatalf("deleted %v, want the pack torrent once, after the last import", d)
	}
	title := r.getTitle("series-4000")
	have := map[string]bool{}
	for _, e := range title["seasons"].([]any)[0].(map[string]any)["episodes"].([]any) {
		m := e.(map[string]any)
		have[m["id"].(string)] = m["state"] == "downloaded"
	}
	for _, ep := range []string{"s01e01", "s01e02", "s01e04", "s01e05"} {
		if !have[ep] {
			t.Errorf("%s is not downloaded: %v", ep, have)
		}
	}
	if have["s01e03"] || have["s01e06"] {
		t.Errorf("an unmonitored episode or one the pack lacks was filled: %v", have)
	}

	r.aio.Answer("series", "tvdb:4000:1:6", results(listed{name: "Long.S01E06.1080p.WEB-DL.mkv", size: epSize,
		url: r.cdn.Add("e6.mkv", debridfake.File{Data: packBody('z')}), hash: hashB, season: 1, ep: 6}))
	r.advance(10 * time.Second)
	r.step()
	waitFor(t, "E06 on its own", 30*time.Second, func() bool { return len(r.entries("imported")) == 4 })
}

func TestAPackHoldingOneOfTwoMissingEpisodesQueuesThatOneAndTheOtherIsSearchedNormally(t *testing.T) {
	r := newTBRig(t, nil)
	r.packOf(hashA, "Dune.Prophecy.S01", []string{ep1Name}, nil)
	r.tb.Cached(hashA)
	r.aio.Answer("series", ep1ID, results(listed{name: packName, size: epSize, hash: hashA, season: 1, pack: true}))
	r.aio.Answer("series", ep2ID, results(listed{name: ep2Name, size: epSize, url: r.cdn.Add("e2.mkv", debridfake.File{Data: packBody('q')}), hash: hashB, season: 1, ep: 2}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	waitFor(t, "E01 from the pack", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	if got := r.torrentOf(); len(got) != 1 || got["1x1"] != 1 {
		t.Fatalf("downloads = %v", got)
	}
	r.advance(10 * time.Second)
	r.step()
	waitFor(t, "E02 on its own", 30*time.Second, func() bool { return len(r.entries("imported")) == 2 })
	if got := r.torrentOf(); got["1x2"] != 0 {
		t.Fatalf("E02 came from TorBox: %v", got)
	}
}

func TestACachedSingleEpisodeBeatsAnUncachedPack(t *testing.T) {
	r := newTBRig(t, nil)
	r.aio.Answer("series", ep1ID, results(
		listed{name: packName, size: 2 * epSize, uncached: true, hash: hashA, season: 1, pack: true},
		listed{name: ep1Name, size: epSize, url: r.cdn.Add("e1.mkv", debridfake.File{Data: debridfake.MKV(epSize)}), hash: hashB, season: 1, ep: 1},
	))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	waitFor(t, "the import", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	r.noMutations("a cached single release was there")
}

func TestWithNothingCachedAnUncachedPackIsCachedOnceAndThenFillsEveryEpisode(t *testing.T) {
	r := newTBRig(t, nil)
	r.packOf(hashA, "Dune.Prophecy.S01", []string{ep1Name, ep2Name}, nil)
	r.aio.Answer("series", ep1ID, results(
		listed{name: ep1Name, size: epSize, uncached: true, hash: hashB, season: 1, ep: 1},
		listed{name: packName, size: 2 * epSize, uncached: true, hash: hashA, season: 1, pack: true},
	))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	id := r.torrentIDs(1)[0]
	if m := r.tb.Magnets(); len(m) != 1 || !strings.Contains(m[0], hashA) {
		t.Fatalf("magnets = %v, want the pack's", m)
	}
	r.tb.Step(id, 0.5, 100)
	r.waitCaching(50)
	r.tb.Present(id)
	waitFor(t, "both imports", 30*time.Second, func() bool { return len(r.entries("imported")) == 2 })
	if got := r.torrentOf(); len(got) != 2 || got["1x1"] != id || got["1x2"] != id {
		t.Fatalf("downloads = %v", got)
	}
	waitFor(t, "the torrent to be deleted", 10*time.Second, func() bool { return len(r.tb.Deleted()) >= 1 })
	time.Sleep(100 * time.Millisecond)
	if d := r.tb.Deleted(); !slices.Equal(d, []int{id}) {
		t.Fatalf("deleted %v", d)
	}
	if n := len(r.tb.Magnets()); n != 1 {
		t.Fatalf("%d torrents created, want the pack only", n)
	}
}

func TestAnUncachedPackWithoutAnyMissingEpisodeIsDeletedAndBlocklisted(t *testing.T) {
	r := newTBRig(t, nil)
	r.packOf(hashA, "Dune.Prophecy.S01", []string{"Dune.Prophecy.S01E07.1080p.WEB-DL.mkv"}, nil)
	r.aio.Answer("series", ep1ID, results(listed{name: packName, size: 2 * epSize, uncached: true, hash: hashA, season: 1, pack: true}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	id := r.torrentIDs(1)[0]
	r.tb.Present(id)
	waitFor(t, "the failure", 10*time.Second, func() bool { return len(r.entries("failed")) == 1 })
	if d := r.tb.Deleted(); !slices.Equal(d, []int{id}) {
		t.Fatalf("deleted %v", d)
	}
	r.advance(10 * time.Second)
	r.step()
	if n := len(r.tb.Magnets()); n != 1 {
		t.Fatalf("the blocklisted pack was taken again: %d torrents", n)
	}
}

func TestACachedPackWithoutAnyMissingEpisodeIsDeletedBlocklistedAndTheSingleReleaseTaken(t *testing.T) {
	r := newTBRig(t, nil)
	r.packOf(hashA, "Dune.Prophecy.S01", []string{"Dune.Prophecy.S01E07.1080p.WEB-DL.mkv"}, nil)
	r.tb.Cached(hashA)
	r.aio.Answer("series", ep1ID, results(
		listed{name: packName, size: 2 * epSize, hash: hashA, season: 1, pack: true},
		listed{name: ep1Name, size: epSize, url: r.cdn.Add("e1.mkv", debridfake.File{Data: debridfake.MKV(epSize)}), hash: hashB, season: 1, ep: 1},
	))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	waitFor(t, "the single import", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	if d := r.tb.Deleted(); !slices.Equal(d, []int{1}) {
		t.Fatalf("deleted %v", d)
	}
	var n int
	if err := r.db().QueryRow(`SELECT COUNT(*) FROM blocklist WHERE reason = 'empty_pack'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("blocklisted packs = %d (%v)", n, err)
	}
}

func TestDryRunNamesTheSeasonPackItWouldGrabAndNeverTouchesTorBox(t *testing.T) {
	r := newTBRig(t, map[string]string{"MARQUEE_DRY_RUN": "true"})
	r.packOf(hashA, "Dune.Prophecy.S01", []string{ep1Name, ep2Name}, nil)
	r.tb.Cached(hashA)
	r.aio.Answer("series", ep1ID, results(listed{name: packName, size: 2 * epSize, hash: hashA, season: 1, pack: true}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	got := r.entries("would_grab")
	if len(got) != 1 || !strings.HasPrefix(got[0]["message"].(string), "Would grab season pack Dune.Prophecy.S01.1080p.WEB-DL for 2 missing episodes") {
		t.Fatalf("would-grab = %v", got)
	}
	r.noMutations("dry-run")
	if d := r.downloads(); len(d) != 0 {
		t.Fatalf("dry-run queued %v", d)
	}
}

func (r *tbRig) episodeStates(titleID string) map[string]string {
	r.t.Helper()
	out := map[string]string{}
	for _, se := range r.getTitle(titleID)["seasons"].([]any) {
		for _, e := range se.(map[string]any)["episodes"].([]any) {
			m := e.(map[string]any)
			out[m["id"].(string)] = m["state"].(string)
		}
	}
	return out
}

func (r *tbRig) emptyPacks() int {
	r.t.Helper()
	var n int
	if err := r.db().QueryRow(`SELECT COUNT(*) FROM blocklist WHERE reason = 'empty_pack'`).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *tbRig) settled(n int) {
	r.t.Helper()
	waitFor(r.t, fmt.Sprintf("%d downloads to end", n), 30*time.Second, func() bool {
		return len(r.entries("imported"))+len(r.entries("failed")) >= n
	})
}

func TestAPackAIOStreamsCallsCachedButTorBoxMustFetchWaitsForAFreeSlot(t *testing.T) {
	r := newTBRig(t, map[string]string{"MARQUEE_TORBOX_MAX_ACTIVE": "1"})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.WEB-DL.mkv", size: 2 << 30, uncached: true, hash: hashC}))
	r.addMovie()
	r.step()
	r.torrentIDs(1)

	r.packOf(hashA, packDir, []string{ep1Name, ep2Name}, nil)
	r.aio.Answer("series", ep1ID, results(
		listed{name: packName, size: 2 * epSize, hash: hashA, season: 1, pack: true},
		listed{name: ep1Name, size: epSize, url: r.cdn.Add("e1.mkv", debridfake.File{Data: packBody('1')}), hash: hashB, season: 1, ep: 1},
	))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.advance(10 * time.Second)
	r.step()
	waitFor(t, "E01 on its own", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	var fetching, fromPack int
	if err := r.db().QueryRow(`SELECT COUNT(*) FROM downloads WHERE cached = 0 AND state IN ('queued', 'caching')`).Scan(&fetching); err != nil || fetching != 1 {
		t.Fatalf("%d downloads wait for TorBox with a cap of 1 (%v)", fetching, err)
	}
	if err := r.db().QueryRow(`SELECT COUNT(*) FROM downloads WHERE info_hash = ?`, hashA).Scan(&fromPack); err != nil || fromPack != 0 {
		t.Fatalf("%d downloads read the pack (%v)", fromPack, err)
	}
	for i, m := range r.tb.Magnets() {
		if strings.Contains(m, hashA) && !slices.Contains(r.tb.Deleted(), i+1) {
			t.Fatalf("the pack's torrent %d was left caching on TorBox: deleted %v", i+1, r.tb.Deleted())
		}
	}
}

func TestWithOneEpisodeMissingThePackIsLeftForTheSingleRelease(t *testing.T) {
	r := newTBRig(t, nil)
	r.packOf(hashA, packDir, []string{ep1Name, ep2Name}, nil)
	r.tb.Cached(hashA)
	r.aio.Answer("series", ep1ID, results(
		listed{name: packName, size: 2 * epSize, hash: hashA, season: 1, pack: true},
		listed{name: ep1Name, size: epSize, url: r.cdn.Add("e1.mkv", debridfake.File{Data: packBody('1')}), hash: hashB, season: 1, ep: 1},
	))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	if _, err := r.db().Exec(`UPDATE episodes SET have_file = 1, file_path = 'Dune/Season 01/e2.mkv' WHERE title_id = 'series-1408' AND season = 1 AND number = 2`); err != nil {
		t.Fatal(err)
	}
	r.step()
	waitFor(t, "E01 on its own", 30*time.Second, func() bool { return len(r.entries("imported")) == 1 })
	r.noMutations("one episode missing")
}

func TestAnUncachedPackTakesTheFirstEpisodeAndNotItsSample(t *testing.T) {
	r := newTBRig(t, nil)
	r.tb.SetFiles(hashA,
		torboxfake.File{ID: 0, Name: packDir + "/Sample/Dune.Prophecy.S01E01.sample.mkv", Size: sampleLen, Body: debridfake.MKV(sampleLen)},
		torboxfake.File{ID: 1, Name: packDir + "/" + ep1Name, Size: epSize, Body: packBody('1')},
		torboxfake.File{ID: 2, Name: packDir + "/" + ep2Name, Size: epSize, Body: packBody('2')})
	r.aio.Answer("series", ep1ID, results(listed{name: packName, size: 2 * epSize, uncached: true, hash: hashA, season: 1, pack: true}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	r.tb.Present(r.torrentIDs(1)[0])
	r.settled(2)
	if n := len(r.entries("imported")); n != 2 {
		t.Fatalf("%d imports, want both episodes", n)
	}
	root := r.env["MARQUEE_SERIES_PATH"]
	for _, f := range tree(t, root) {
		if st, err := os.Stat(filepath.Join(root, f)); err != nil || st.Size() != epSize {
			t.Errorf("%s is not the episode's file (%v)", f, err)
		}
	}
}

func TestAnUncachedPackWhoseFirstFileHoldsTwoEpisodesFillsBoth(t *testing.T) {
	r := newTBRig(t, nil)
	r.tb.SetFiles(hashA, torboxfake.File{ID: 0, Name: packDir + "/Dune.Prophecy.S01E01E02.1080p.WEB-DL.mkv", Size: epSize, Body: packBody('m')})
	r.aio.Answer("series", ep1ID, results(listed{name: packName, size: epSize, uncached: true, hash: hashA, season: 1, pack: true}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	r.tb.Present(r.torrentIDs(1)[0])
	r.settled(1)
	if got := tree(t, r.env["MARQUEE_SERIES_PATH"]); len(got) != 1 || !strings.Contains(got[0], "S01E01-E02") {
		t.Fatalf("library = %v, want one file of E01 and E02", got)
	}
	if st := r.episodeStates("series-1408"); st["s01e01"] != "downloaded" || st["s01e02"] != "downloaded" {
		t.Fatalf("episodes = %v", st)
	}
	var n int
	if err := r.db().QueryRow(`SELECT COUNT(*) FROM downloads`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d downloads, want the one file (%v)", n, err)
	}
}

func TestACachedPackTakesTheFileItChoseWhenTwoShareAName(t *testing.T) {
	r := newTBRig(t, nil)
	r.tb.SetFiles(hashA,
		torboxfake.File{ID: 0, Name: "Extras/" + ep1Name, Size: sampleLen, Body: debridfake.MKV(sampleLen)},
		torboxfake.File{ID: 1, Name: packDir + "/" + ep1Name, Size: epSize, Body: packBody('1')},
		torboxfake.File{ID: 2, Name: packDir + "/" + ep2Name, Size: epSize, Body: packBody('2')})
	r.tb.Cached(hashA)
	r.aio.Answer("series", ep1ID, results(listed{name: packName, size: 2 * epSize, hash: hashA, season: 1, pack: true}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	r.settled(2)
	if n := len(r.entries("imported")); n != 2 {
		t.Fatalf("%d imports, want both episodes: %v", n, r.entries("failed"))
	}
}

// grabMeanwhile holds the cached pack's TorBox lookup while both episodes
// are grabbed by hand from rival releases, runs settle, and then lets the
// lookup answer.
func (r *tbRig) grabMeanwhile(rival debridfake.File, settle func()) {
	r.t.Helper()
	r.packOf(hashA, packDir, []string{ep1Name, ep2Name}, nil)
	r.tb.Cached(hashA)
	r.aio.Answer("series", ep1ID, results(
		listed{name: packName, size: 2 * epSize, hash: hashA, season: 1, pack: true},
		listed{name: ep1Name, size: epSize, url: r.cdn.Add("e1.mkv", rival), hash: hashB, season: 1, ep: 1},
	))
	rival2 := rival
	rival2.Data = packBody('2')
	r.aio.Answer("series", ep2ID, results(
		listed{name: ep2Name, size: epSize, url: r.cdn.Add("e2.mkv", rival2), hash: hashC, season: 1, ep: 2},
	))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	hold := make(chan struct{})
	r.tb.Script("/torrents/mylist", torboxfake.Step{Until: hold})
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.sch.Step(context.Background())
	}()
	waitFor(r.t, "the pack lookup", 10*time.Second, func() bool { return r.tb.Hits("/torrents/mylist") >= 1 })
	for _, ep := range []struct{ id, name string }{{"s01e01", ep1Name}, {"s01e02", ep2Name}} {
		cands, _ := r.releases("/api/titles/series-1408/releases?episode=" + ep.id)
		i := slices.IndexFunc(cands, func(c map[string]any) bool { return c["name"] == ep.name })
		if i < 0 {
			r.t.Fatalf("no %s among %v", ep.name, cands)
		}
		r.mustCall("POST", "/api/titles/series-1408/releases/"+cands[i]["id"].(string)+"/grab", `{"episode":"`+ep.id+`"}`, 202)
	}
	settle()
	close(hold)
	<-done
}

func (r *tbRig) packUnused() {
	r.t.Helper()
	if n := r.emptyPacks(); n != 0 {
		r.t.Fatalf("a pack that held both episodes was blocklisted as empty")
	}
	waitFor(r.t, "the pack's torrent to be deleted", 10*time.Second, func() bool { return slices.Equal(r.tb.Deleted(), []int{1}) })
	var all, fromPack int
	if err := r.db().QueryRow(`SELECT COUNT(*), COUNT(*) FILTER (WHERE info_hash = ?) FROM downloads`, hashA).Scan(&all, &fromPack); err != nil {
		r.t.Fatal(err)
	}
	if all != 2 || fromPack != 0 {
		r.t.Fatalf("%d downloads, %d from the pack; want only the two grabbed by hand", all, fromPack)
	}
}

func TestAPackWhoseEpisodesAreBeingGrabbedMeanwhileIsDroppedUnblocklisted(t *testing.T) {
	r := newTBRig(t, nil)
	r.grabMeanwhile(debridfake.File{Data: packBody('1'), StallAt: epSize / 2}, func() {
		waitFor(t, "both rivals downloading", 10*time.Second, func() bool {
			var n int
			if err := r.db().QueryRow(`SELECT COUNT(*) FROM downloads WHERE state = 'downloading'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n == 2
		})
	})
	r.packUnused()
}

func TestAPackWhoseEpisodesWereAllImportedMeanwhileIsDroppedUnblocklisted(t *testing.T) {
	r := newTBRig(t, nil)
	r.grabMeanwhile(debridfake.File{Data: packBody('1')}, func() {
		waitFor(t, "both rivals imported", 30*time.Second, func() bool { return len(r.entries("imported")) == 2 })
	})
	r.packUnused()
}

func TestAnUncachedPackWithoutTheSearchedEpisodeStillFillsTheOthers(t *testing.T) {
	r := newTBRig(t, nil)
	r.packOf(hashA, packDir, []string{ep2Name}, nil)
	r.aio.Answer("series", ep1ID, results(listed{name: packName, size: epSize, uncached: true, hash: hashA, season: 1, pack: true}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	id := r.torrentIDs(1)[0]
	r.tb.Present(id)
	r.settled(2)
	if n, f := len(r.entries("imported")), len(r.entries("failed")); n != 1 || f != 1 {
		t.Fatalf("imported %d and failed %d; want E02 imported and the E01 lead failed", n, f)
	}
	if st := r.episodeStates("series-1408"); st["s01e02"] != "downloaded" {
		t.Fatalf("episodes = %v", st)
	}
	waitFor(t, "the torrent to be deleted", 10*time.Second, func() bool { return len(r.tb.Deleted()) >= 1 })
	time.Sleep(100 * time.Millisecond)
	if d := r.tb.Deleted(); !slices.Equal(d, []int{id}) {
		t.Fatalf("deleted %v, want the torrent once", d)
	}
}

func TestAPackTorrentStaysOnTorBoxWhenDeleteOnCompleteIsOff(t *testing.T) {
	r := newTBRig(t, map[string]string{"MARQUEE_TORBOX_DELETE_ON_COMPLETE": "false"})
	r.packOf(hashA, packDir, []string{ep1Name, ep2Name}, nil)
	r.tb.Cached(hashA)
	r.aio.Answer("series", ep1ID, results(listed{name: packName, size: 2 * epSize, hash: hashA, season: 1, pack: true}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	waitFor(t, "both imports", 30*time.Second, func() bool { return len(r.entries("imported")) == 2 })
	time.Sleep(100 * time.Millisecond)
	if d := r.tb.Deleted(); len(d) != 0 {
		t.Fatalf("deleted %v", d)
	}
}

func TestDryRunNamesNoUncachedSeasonPack(t *testing.T) {
	r := newTBRig(t, map[string]string{"MARQUEE_DRY_RUN": "true"})
	r.aio.Answer("series", ep1ID, results(listed{name: packName, size: 2 * epSize, uncached: true, hash: hashA, season: 1, pack: true}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	for _, e := range r.entries("would_grab") {
		if strings.HasPrefix(e["message"].(string), "Would grab season pack") {
			t.Fatalf("dry-run named an uncached pack: %v", e["message"])
		}
	}
	r.noMutations("dry-run")
}

func TestAPackLeadKeepsItsFileWhenQueueingThePacksOtherEpisodesFails(t *testing.T) {
	r := newTBRig(t, nil)
	r.packOf(hashA, packDir, []string{ep1Name, ep2Name}, nil)
	r.aio.Answer("series", ep1ID, results(listed{name: packName, size: 2 * epSize, uncached: true, hash: hashA, season: 1, pack: true}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	id := r.torrentIDs(1)[0]
	if _, err := r.db().Exec(`CREATE TRIGGER no_e02 BEFORE INSERT ON downloads WHEN NEW.number = 2 BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	r.tb.Present(id)
	r.settled(1)
	if n, f := len(r.entries("imported")), r.entries("failed"); n != 1 || len(f) != 0 {
		t.Fatalf("imported %d and failed %v; want the lead's own episode imported", n, f)
	}
	if st := r.episodeStates("series-1408"); st["s01e01"] != "downloaded" {
		t.Fatalf("episodes = %v", st)
	}
}
