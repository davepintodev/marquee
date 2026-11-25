package app_test

import (
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreamsfake"
)

func wantBadge(t *testing.T, when string, v map[string]any, state, label string) {
	t.Helper()
	if b := badgeOf(v); b["state"] != state || b["label"] != label {
		t.Errorf("%s: badge %v, want %s %q", when, b, state, label)
	}
}

func wantNext(t *testing.T, when string, v map[string]any, want time.Time) {
	t.Helper()
	s, _ := v["nextSearchAt"].(string)
	got, err := time.Parse(time.RFC3339, s)
	if err != nil || !got.Equal(want) {
		t.Errorf("%s: nextSearchAt %v, want %v", when, v["nextSearchAt"], want)
	}
}

func (r *schedRig) clock() time.Time { return time.Unix(0, r.now.Load()).UTC() }

func TestAMonitoredMovieWaitingForItsSearchSaysWantedNotSearching(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	v := r.mustCall("GET", "/api/titles/movie-438631", "", 200)
	wantBadge(t, "before any search", v, "wanted", "Wanted")
	wantNext(t, "a title never searched is due now", v, r.clock())

	r.step()
	at := r.clock()
	v = r.mustCall("GET", "/api/titles/movie-438631", "", 200)
	wantBadge(t, "after a search that found nothing", v, "not_found", "Not found")
	wantNext(t, "the next try follows the backoff", v, at.Add(15*time.Minute))

	r.advance(time.Hour)
	v = r.mustCall("GET", "/api/titles/movie-438631", "", 200)
	wantNext(t, "an overdue search is due now", v, r.clock())
	for _, it := range r.mustCall("GET", "/api/titles", "", 200)["titles"].([]any) {
		wantBadge(t, "the library tile", it.(map[string]any), "not_found", "Not found")
	}
}

func TestATitleSaysSearchingOnlyWhileItsSearchRuns(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	var during map[string]any
	r.aio.Before(func(aiostreamsfake.Search) { during = r.mustCall("GET", "/api/titles/movie-438631", "", 200) })
	r.step()
	r.aio.Before(nil)
	wantBadge(t, "while the search runs", during, "searching", "Searching")
	if during["nextSearchAt"] != nil {
		t.Errorf("a running search has no next search: %v", during["nextSearchAt"])
	}
	wantBadge(t, "when it ends with nothing", r.mustCall("GET", "/api/titles/movie-438631", "", 200), "not_found", "Not found")
}

func TestSearchNowSaysSearchingAtOnceAndTheStateFollows(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	r.step()
	first := r.clock()
	r.advance(time.Minute)

	v := r.mustCall("POST", "/api/titles/movie-438631/search", "", 200)
	wantBadge(t, "Search now answers", v, "searching", "Searching")
	r.step()
	v = r.mustCall("GET", "/api/titles/movie-438631", "", 200)
	wantBadge(t, "after the asked-for search", v, "not_found", "Not found")
	wantNext(t, "Search now leaves the schedule", v, first.Add(15*time.Minute))
}

func TestEpisodesSayWantedSearchingOrNotFoundAndTheTitleTheSoonestNextSearch(t *testing.T) {
	r := newSchedRig(t, false)
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	v := r.mustCall("GET", "/api/titles/series-1408", "", 200)
	eps := episodes(t, v)
	for _, id := range []string{"s01e01", "s01e02"} {
		if eps[id]["state"] != "wanted" {
			t.Errorf("%s waits for its first search: %v", id, eps[id])
		}
		wantNext(t, id+" is due", eps[id], r.clock())
	}
	if eps["s01e03"]["nextSearchAt"] != nil {
		t.Errorf("an unaired episode has no next search: %v", eps["s01e03"])
	}
	wantBadge(t, "a series whose episodes wait", v, "wanted", "Wanted")

	r.step()
	at := r.clock()
	r.advance(10 * time.Second)
	v = r.mustCall("GET", "/api/titles/series-1408", "", 200)
	eps = episodes(t, v)
	if eps["s01e01"]["state"] != "not_found" || eps["s01e02"]["state"] != "wanted" {
		t.Errorf("one searched, one not yet: %v %v", eps["s01e01"], eps["s01e02"])
	}
	wantNext(t, "the searched episode", eps["s01e01"], at.Add(15*time.Minute))
	wantNext(t, "the title has its soonest episode's", v, r.clock())
	wantBadge(t, "some not searched yet", v, "wanted", "Wanted")

	r.step()
	v = r.mustCall("GET", "/api/titles/series-1408", "", 200)
	wantBadge(t, "every wanted episode found nothing", v, "not_found", "Not found")
	wantNext(t, "the title has its soonest episode's", v, at.Add(15*time.Minute))
	if got := v["searchingEpisodes"].([]any); len(got) != 2 {
		t.Errorf("episodes that found nothing still wait for a release: %v", got)
	}

	v = r.mustCall("POST", "/api/titles/series-1408/episodes/s01e02/search", "", 200)
	if e := episodes(t, v)["s01e02"]; e["state"] != "searching" || e["nextSearchAt"] != nil {
		t.Errorf("Search now on an episode: %v", e)
	}
	wantBadge(t, "an episode searching", v, "searching", "Searching")
	if e := episodes(t, v)["s01e01"]; e["state"] != "not_found" {
		t.Errorf("the other episode is left alone: %v", e)
	}
}

func TestMissingStillMeansNotMonitored(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	v := r.mustCall("PATCH", "/api/titles/movie-438631", `{"monitored":false}`, 200)
	wantBadge(t, "unmonitored", v, "missing", "Missing")
	if v["nextSearchAt"] != nil {
		t.Errorf("an unmonitored title is not searched: %v", v["nextSearchAt"])
	}
}

func TestSearchNowDuringASourceOutageSaysWhenItWillRunAndRunsThen(t *testing.T) {
	r := newSchedRig(t, false)
	r.aio.Fail(aiostreamsfake.Step{Status: 500})
	r.addMovie()
	r.step()
	backoffEnds := r.clock().Add(time.Minute)
	wantBadge(t, "after the search the source failed", r.mustCall("GET", "/api/titles/movie-438631", "", 200), "wanted", "Wanted")

	r.advance(10 * time.Second)
	v := r.mustCall("POST", "/api/titles/movie-438631/search", "", 200)
	wantBadge(t, "Search now while the source backs off", v, "wanted", "Wanted")
	wantNext(t, "Search now while the source backs off", v, backoffEnds)
	r.step()
	v = r.mustCall("GET", "/api/titles/movie-438631", "", 200)
	wantBadge(t, "a step during the backoff", v, "wanted", "Wanted")
	wantNext(t, "a step during the backoff", v, backoffEnds)
	r.wantSearches(1, "nothing is searched during the backoff")

	r.aio.Heal()
	r.advance(time.Minute)
	r.step()
	r.wantSearches(2, "the asked-for search runs once the backoff ends")
	wantBadge(t, "after it", r.mustCall("GET", "/api/titles/movie-438631", "", 200), "not_found", "Not found")
}

func TestSearchNowThatTheSourceFailsStopsSayingSearching(t *testing.T) {
	r := newSchedRig(t, false)
	r.addMovie()
	r.mustCall("PATCH", "/api/titles/movie-438631", `{"monitored":false}`, 200)
	r.aio.Fail(aiostreamsfake.Step{Status: 500})
	r.mustCall("POST", "/api/titles/movie-438631/search", "", 200)
	r.step()
	backoffEnds := r.clock().Add(time.Minute)
	v := r.mustCall("GET", "/api/titles/movie-438631", "", 200)
	wantBadge(t, "an unmonitored title whose Search now the source failed", v, "wanted", "Wanted")
	wantNext(t, "it runs again after the backoff", v, backoffEnds)

	r.aio.Heal()
	r.advance(time.Minute)
	r.step()
	r.wantSearches(2, "the request is not lost")
	wantBadge(t, "once it ran", r.mustCall("GET", "/api/titles/movie-438631", "", 200), "missing", "Missing")
}

func TestAnUnmonitoredSeriesWhoseSearchNowWaitsOnTheSourceSaysWanted(t *testing.T) {
	r := newSchedRig(t, false)
	r.mustCall("POST", "/api/titles", seriesBody, 201)
	r.mustCall("PATCH", "/api/titles/series-1408", `{"monitored":false}`, 200)
	r.aio.Fail(aiostreamsfake.Step{Status: 500})
	r.mustCall("POST", "/api/titles/series-1408/search", "", 200)
	r.step()
	v := r.mustCall("GET", "/api/titles/series-1408", "", 200)
	wantBadge(t, "the asked-for episodes wait for the source", v, "wanted", "Wanted")
	wantNext(t, "they run when the backoff ends", v, r.clock().Add(time.Minute))
}
