package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/debridfake"
)

func (r *realSvc) getTitle(id string) map[string]any {
	r.t.Helper()
	return r.mustCall("GET", "/api/titles/"+id, "", 200)
}

func badgeOf(m map[string]any) map[string]any {
	b, _ := m["badge"].(map[string]any)
	return b
}

// slowMovie keeps a download in flight for a few seconds.
func (r *rig) slowMovie() {
	r.enqueue(movieJob(r.cdn.Add("slow.mkv", debridfake.File{Data: debridfake.MKV(1 << 20), Pace: 50 * time.Millisecond}), "slow.mkv"))
}

func tileOf(items []any, id string) map[string]any {
	for _, it := range items {
		m := it.(map[string]any)["title"].(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	return nil
}

func TestAMoviesBadgeFollowsItsDownloadOnTheWallSearchAndDetail(t *testing.T) {
	r, _ := setup(t)
	r.slowMovie()

	var detail map[string]any
	waitFor(t, "a download in progress on the title", 5*time.Second, func() bool {
		detail = r.getTitle("movie-438631")
		b := badgeOf(detail)
		return b["state"] == "downloading" && b["percent"] != nil && b["percent"].(float64) > 0
	})
	b := badgeOf(detail)
	pct := int(b["percent"].(float64))
	if !strings.HasPrefix(b["label"].(string), "Downloading ") || !strings.HasSuffix(b["label"].(string), "%") {
		t.Errorf("label = %q", b["label"])
	}
	_ = pct

	wall := r.mustCall("GET", "/api/wall", "", 200)["items"].([]any)
	if got := badgeOf(tileOf(wall, "movie-438631")); got["state"] != "downloading" {
		t.Errorf("wall badge = %v", got)
	}
	for _, it := range r.mustCall("GET", "/api/search?q=dune", "", 200)["results"].([]any) {
		if m := it.(map[string]any); m["id"] == "movie-438631" && badgeOf(m)["state"] != "downloading" {
			t.Errorf("search badge = %v", badgeOf(m))
		}
	}

	r.waitHistory(1)
	if got := badgeOf(r.getTitle("movie-438631")); got["state"] != "downloaded" || got["label"] != "Downloaded" {
		t.Errorf("once imported the badge = %v", got)
	}
}

func TestASeriesBadgesFollowAnEpisodeDownload(t *testing.T) {
	r, _ := setup(t)
	r.enqueue(episodeJob(r.cdn.Add("ep.mkv", debridfake.File{Data: debridfake.MKV(1 << 20), Pace: 50 * time.Millisecond}), "ep.mkv"))

	var detail map[string]any
	waitFor(t, "an episode download in progress", 5*time.Second, func() bool {
		detail = r.getTitle("series-1408")
		return badgeOf(detail)["state"] == "downloading"
	})
	season := detail["seasons"].([]any)[0].(map[string]any)
	if got := badgeOf(season); got["state"] != "downloading" {
		t.Errorf("season badge = %v", got)
	}
	ep := season["episodes"].([]any)[0].(map[string]any)
	if ep["id"] != "s01e01" || ep["state"] != "downloading" {
		t.Errorf("episode = %v", ep)
	}
	if _, ok := ep["percent"]; !ok {
		t.Errorf("a downloading episode has a percent: %v", ep)
	}
	if other := season["episodes"].([]any)[1].(map[string]any); other["state"] != "searching" {
		t.Errorf("the other aired episode keeps its state: %v", other)
	}

	r.waitHistory(1)
	if got := badgeOf(r.getTitle("series-1408")); got["state"] != "partial_season" {
		t.Errorf("one episode in, one aired episode still wanted: %v", got)
	}
}

func TestABadgeSaysStalledWhenBytesStopMoving(t *testing.T) {
	r, _ := setup(t)
	r.stop()
	r.stallTimeout = time.Minute
	r.startService()
	r.enqueue(movieJob(r.cdn.Add("stuck.mkv", debridfake.File{Data: debridfake.MKV(400 << 10), StallAt: 100 << 10}), "stuck.mkv"))
	waitFor(t, "bytes to arrive", 5*time.Second, func() bool {
		d := r.downloads()
		return len(d) == 1 && d[0].(map[string]any)["bytesDone"].(float64) > 0
	})
	time.Sleep(200 * time.Millisecond)
	r.now.Add(int64(20 * time.Second))

	if d := r.downloads()[0].(map[string]any); d["stalled"] != true {
		t.Fatalf("the queue should call it stalled: %v", d)
	}
	if got := badgeOf(r.getTitle("movie-438631")); got["state"] != "stalled" || got["label"] != "Stalled" {
		t.Errorf("badge = %v", got)
	}
	wall := r.mustCall("GET", "/api/wall", "", 200)["items"].([]any)
	if got := badgeOf(tileOf(wall, "movie-438631")); got["state"] != "stalled" {
		t.Errorf("wall badge = %v", got)
	}
}

func TestATitleCarriesItsHistoryAndASummaryOfIt(t *testing.T) {
	r, _ := setup(t)
	if got := r.getTitle("movie-438631"); got["historySummary"] != "Nothing tried yet." {
		t.Errorf("a new title: %v", got["historySummary"])
	}

	notVideo := append([]byte("MZ\x90\x00"), make([]byte, 100<<10)...)
	r.enqueue(movieJob(r.cdn.Add("bad.mkv", debridfake.File{Data: notVideo}), "bad.mkv"))
	r.waitHistory(1)
	got := r.getTitle("movie-438631")
	if got["historySummary"] != "Tried 1 release, 1 fake." {
		t.Errorf("summary = %q", got["historySummary"])
	}
	hist := got["history"].([]any)
	if len(hist) != 1 || hist[0].(map[string]any)["kind"] != "failed" || hist[0].(map[string]any)["titleId"] != "movie-438631" {
		t.Errorf("history = %v", hist)
	}

	r.enqueue(movieJob(r.cdn.Add("good.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "good.mkv"))
	r.waitHistory(2)
	got = r.getTitle("movie-438631")
	if got["historySummary"] != "Tried 2 releases, 1 fake." || len(got["history"].([]any)) != 2 {
		t.Errorf("after an import: %v %v", got["historySummary"], got["history"])
	}
	// The series' history is its own.
	if h := r.getTitle("series-1408")["history"].([]any); len(h) != 0 {
		t.Errorf("series history = %v", h)
	}
}
