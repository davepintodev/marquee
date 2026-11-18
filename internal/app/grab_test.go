package app_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/debridfake"
)

// releaseIDs lists the ids of what the release search shows for the movie.
func (r *schedRig) releases(path string) (candidates, dropped []map[string]any) {
	r.t.Helper()
	v := r.mustCall("GET", path, "", 200)
	for _, c := range v["candidates"].([]any) {
		candidates = append(candidates, c.(map[string]any))
	}
	for _, d := range v["dropped"].([]any) {
		dropped = append(dropped, d.(map[string]any)["release"].(map[string]any))
	}
	return candidates, dropped
}

func (r *schedRig) waitEntries(kind string, n int) {
	r.t.Helper()
	waitFor(r.t, "history entries", 10*time.Second, func() bool { return len(r.entries(kind)) >= n })
}

func TestGrabbingAReleaseOfATitleNotInTheLibraryAddsItAndQueuesTheDownload(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.WEB-DL.mkv", size: 300 << 20, url: url}))
	cands, _ := r.releases("/api/titles/movie-438631/releases")
	if len(cands) != 1 {
		t.Fatalf("candidates = %v", cands)
	}
	r.mustCall("GET", "/api/titles/movie-438631", "", 404)

	d := r.mustCall("POST", "/api/titles/movie-438631/releases/"+cands[0]["id"].(string)+"/grab", "", 202)
	if d["titleId"] != "movie-438631" || d["releaseName"] != "Dune.2021.1080p.WEB-DL.mkv" || d["titleName"] != "Dune" {
		t.Errorf("download = %v", d)
	}
	if v := r.getTitle("movie-438631"); v["inLibrary"] != true || v["monitored"] != true {
		t.Errorf("a grab adds and monitors the title: %v", v)
	}
	noSecrets(t, "the logs", r.logs.String())
	noSecrets(t, "the answer", fmt.Sprint(d))
}

func TestGrabbingTheSameReleaseTwiceQueuesOneDownload(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("ep.mkv", debridfake.File{Data: debridfake.MKV(53 << 20), Pace: 100 * time.Millisecond})
	r.aio.Answer("series", "tvdb:403000:1:1", results(listed{name: "Dune.Prophecy.S01E01.1080p.WEB-DL.mkv", size: 53 << 20, url: url, season: 1, ep: 1}))
	cands, _ := r.releases("/api/titles/series-1408/releases?episode=s01e01")
	path := "/api/titles/series-1408/releases/" + cands[0]["id"].(string) + "/grab"
	first := r.mustCall("POST", path, `{"episode":"s01e01"}`, 202)
	second := r.mustCall("POST", path, `{"episode":"s01e01"}`, 202)
	if first["id"] != second["id"] {
		t.Errorf("two grabs made two downloads: %v %v", first["id"], second["id"])
	}
	if d := r.downloads(); len(d) != 1 {
		t.Errorf("queue = %v", d)
	}
}

func TestAReleaseTheChooserDropsCannotBeGrabbedAndAddsNothing(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.WEB-DL.exe", size: 300 << 20, url: url}))
	_, dropped := r.releases("/api/titles/movie-438631/releases")
	if len(dropped) != 1 {
		t.Fatalf("dropped = %v", dropped)
	}
	got := r.mustCall("POST", "/api/titles/movie-438631/releases/"+dropped[0]["id"].(string)+"/grab", "", 409)
	if got["error"] == "" {
		t.Errorf("a refusal says why: %v", got)
	}
	r.mustCall("GET", "/api/titles/movie-438631", "", 404)
	if d := r.downloads(); len(d) != 0 {
		t.Errorf("queue = %v", d)
	}
}

func TestGrabbingARejectedRequestAnswersInTheContractsShape(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.WEB-DL.mkv", size: 300 << 20, url: url}))
	r.mustCall("POST", "/api/titles/movie-438631/releases/nothere/grab", "", 404)
	if r.bad("POST", "/api/titles/nonsense/releases/x/grab", "").status != 404 {
		t.Error("a malformed title id is not found")
	}
	// A series needs the episode.
	r.mustCall("POST", "/api/titles/series-1408/releases/x/grab", "", 400)
	r.mustCall("POST", "/api/titles/series-1408/releases/x/grab", `{"episode":"s09e09"}`, 400)
	if r.bad("POST", "/api/titles/movie-438631/releases/x/grab", `{"unknown":1}`).status != 400 {
		t.Error("an unknown field is refused")
	}
	r.mustCall("GET", "/api/titles/movie-438631", "", 404)
}

func TestGrabbingAnEpisodeOfAMonitoredSeries(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("ep.mkv", debridfake.File{Data: debridfake.MKV(53 << 20)})
	r.aio.Answer("series", "tvdb:403000:1:1", results(listed{name: "Dune.Prophecy.S01E01.1080p.WEB-DL.mkv", size: 53 << 20, url: url, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	cands, _ := r.releases("/api/titles/series-1408/releases?episode=s01e01")
	d := r.mustCall("POST", "/api/titles/series-1408/releases/"+cands[0]["id"].(string)+"/grab", `{"episode":"s01e01"}`, 202)
	if d["episode"] != "s01e01" || d["titleId"] != "series-1408" {
		t.Errorf("download = %v", d)
	}
	r.waitEntries("imported", 1)
}

func TestAReleaseOnTheBlocklistCanStillBeGrabbed(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("stuck.mkv", debridfake.File{Data: debridfake.MKV(400 << 10), StallAt: 100 << 10})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.WEB-DL.mkv", size: 300 << 20, url: url}))
	r.addMovie()
	r.step()
	r.waitEntries("failed", 1)

	cands, dropped := r.releases("/api/titles/movie-438631/releases")
	if len(cands) != 0 || len(dropped) != 1 {
		t.Fatalf("the failed release should be blocklisted: %v %v", cands, dropped)
	}
	r.mustCall("POST", "/api/titles/movie-438631/releases/"+dropped[0]["id"].(string)+"/grab", "", 202)
	r.waitEntries("failed", 2)
}

func TestInDryRunNothingCanBeGrabbed(t *testing.T) {
	r := newSchedRig(t, true)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.WEB-DL.mkv", size: 300 << 20, url: url}))
	cands, _ := r.releases("/api/titles/movie-438631/releases")
	r.mustCall("POST", "/api/titles/movie-438631/releases/"+cands[0]["id"].(string)+"/grab", "", 409)
	if reqs := r.cdn.Requests(); len(reqs) != 0 {
		t.Errorf("dry-run asked the debrid: %v", reqs)
	}
}

func TestRetryingAFailureSearchesAtOnceAndFindsWhatAppearedSince(t *testing.T) {
	r := newSchedRig(t, false)
	stuck := r.cdn.Add("stuck.mkv", debridfake.File{Data: debridfake.MKV(53 << 20), StallAt: 100 << 10})
	r.aio.Answer("series", "tvdb:403000:1:1", results(listed{name: "Dune.Prophecy.S01E01.1080p.WEB-DL.mkv", size: 53 << 20, url: stuck, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	r.waitEntries("failed", 1)
	for range 2 { // the follow-up search finds only the blocklisted release; the other episode finds nothing
		r.advance(5 * time.Second)
		r.step()
	}
	failed := r.entries("failed")[0]
	if failed["retryable"] != true || failed["episode"] != "s01e01" {
		t.Fatalf("the latest failure of an episode that still lacks its file can be retried: %v", failed)
	}

	good := r.cdn.Add("good.mkv", debridfake.File{Data: debridfake.MKV(53 << 20)})
	r.aio.Answer("series", "tvdb:403000:1:1", results(listed{name: "Dune.Prophecy.S01E01.1080p.BluRay.mkv", size: 53 << 20, url: good, season: 1, ep: 1}))
	title := r.mustCall("POST", "/api/history/"+failed["id"].(string)+"/retry", "", 200)
	if title["id"] != "series-1408" {
		t.Errorf("retry answers the title: %v", title)
	}
	if got := r.entries("searched")[0]["message"]; got != "Retrying after a failure." {
		t.Errorf("the retry is on the history: %v", r.history())
	}
	r.advance(5 * time.Second)
	r.step()
	r.waitEntries("imported", 1)
	if e := r.entries("imported")[0]; e["titleId"] != "series-1408" || e["episode"] != "s01e01" {
		t.Errorf("entry = %v", e)
	}
	if f := r.entries("failed")[0]; f["retryable"] != false {
		t.Errorf("a failure that has been superseded is not retryable: %v", f)
	}
	r.mustCall("POST", "/api/history/"+failed["id"].(string)+"/retry", "", 409)
}

func TestOnlyAFailureCanBeRetried(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("ep.mkv", debridfake.File{Data: debridfake.MKV(53 << 20)})
	r.aio.Answer("series", "tvdb:403000:1:1", results(listed{name: "Dune.Prophecy.S01E01.1080p.WEB-DL.mkv", size: 53 << 20, url: url, season: 1, ep: 1}))
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.step()
	r.waitEntries("imported", 1)
	r.advance(5 * time.Second)
	r.step()

	r.mustCall("POST", "/api/history/nothing/retry", "", 404)
	n := 0
	for _, e := range r.history() {
		m := e.(map[string]any)
		if m["retryable"] != false {
			t.Errorf("%v is retryable", m)
		}
		r.mustCall("POST", "/api/history/"+m["id"].(string)+"/retry", "", 409)
		n++
	}
	if n < 2 {
		t.Errorf("want an import and a search in the history: %v", r.history())
	}
}

func TestGrabbingWithAnEmptyBodyOfUnknownLengthIsAccepted(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.WEB-DL.mkv", size: 300 << 20, url: url}))
	cands, _ := r.releases("/api/titles/movie-438631/releases")
	if len(cands) != 1 {
		t.Fatalf("candidates = %v", cands)
	}
	// A reader the client cannot measure is sent chunked, so the server sees ContentLength -1.
	body := io.NopCloser(strings.NewReader(""))
	req, err := http.NewRequest("POST", r.base+"/api/titles/movie-438631/releases/"+cands[0]["id"].(string)+"/grab", body)
	if err != nil {
		t.Fatal(err)
	}
	if tok := r.env["MARQUEE_API_TOKEN"]; tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 202 {
		t.Fatalf("grab with an empty chunked body: %d, want 202: %s", resp.StatusCode, raw)
	}
}
