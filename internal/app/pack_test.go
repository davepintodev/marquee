package app_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/tmdbfake"
	"github.com/davepintodev/marquee/internal/torboxfake"
)

const packName = "Dune.Prophecy.S01.1080p.WEB-DL.mkv"

// packBody is an episode's bytes, distinct per episode so the
// duplicate-episode check passes.
func packBody(b byte) []byte {
	data := debridfake.MKV(epSize)
	for _, off := range []int{1 << 20, epSize / 2, epSize - 1000} {
		data[off] = b
	}
	return data
}

// packOf makes TorBox's torrent of hash hold the named episode files, in
// folder; only the ones in bodies can be downloaded.
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

// torrentOf lists the TorBox torrent every download row reads from.
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
