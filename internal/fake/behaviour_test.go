package fake_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/routers"

	"github.com/davepintodev/marquee/internal/fake"
)

func exchange(t *testing.T, srv *httptest.Server, router routers.Router, method, path, body string, status int) map[string]any {
	t.Helper()
	c := call{method, path, body, status}
	resp := do(t, srv, c, path)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, status, raw)
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	validate(t, router, c, path, resp, status)
	var out map[string]any
	if len(raw) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestUnmonitoringASeriesKeepsItsBadge(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	title := exchange(t, srv, router, "PATCH", "/api/titles/series-1408", `{"monitored":false}`, 200)
	if title["badge"] == nil {
		t.Fatal("an in-library title must carry a badge")
	}
}

func TestGrabRefusesReleasesTheChooserWouldDrop(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	for _, id := range []string{"rel-movie-3", "rel-movie-4", "rel-movie-5"} {
		out := exchange(t, srv, router, "POST", "/api/titles/movie-335984/releases/"+id+"/grab", "", 409)
		if msg, _ := out["error"].(string); msg == "" {
			t.Errorf("%s: 409 must carry the drop reason", id)
		}
	}
	exchange(t, srv, router, "POST", "/api/titles/series-1408/releases/rel-series-5/grab", `{"episode":"s04e02"}`, 202)
}

func TestInteractiveSearchWorksForATitleNotInTheLibrary(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)

	exchange(t, srv, router, "GET", "/api/titles/series-95396/releases?episode=s01e01", "", 200)
	rel := exchange(t, srv, router, "GET", "/api/titles/movie-438631/releases", "", 200)
	if len(rel["candidates"].([]any)) == 0 {
		t.Fatal("no candidates for a title that is not in the library")
	}
	if got := exchange(t, srv, router, "GET", "/api/titles/movie-438631", "", 200); got["inLibrary"] != false {
		t.Fatalf("listing releases must not add the title: %v", got["inLibrary"])
	}

	exchange(t, srv, router, "POST", "/api/titles/movie-438631/releases/rel-movie-1/grab", "", 202)
	got := exchange(t, srv, router, "GET", "/api/titles/movie-438631", "", 200)
	if got["inLibrary"] != true || got["monitored"] != true {
		t.Errorf("grabbing must add and monitor the title: %v %v", got["inLibrary"], got["monitored"])
	}

	exchange(t, srv, router, "POST", "/api/titles/series-95396/releases/rel-series-1/grab", `{"episode":"s01e02"}`, 202)
	if got := exchange(t, srv, router, "GET", "/api/titles/series-95396", "", 200); got["inLibrary"] != true {
		t.Error("grabbing an episode must add the series")
	}
	exchange(t, srv, router, "GET", "/api/titles/series-83867/releases?episode=s01e01", "", 200)
}

type streamLine struct{ event, data, comment string }

func openStream(t *testing.T, srv *httptest.Server) <-chan streamLine {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	lines := make(chan streamLine, 256)
	go func() {
		defer close(lines)
		var event string
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, ": "):
				lines <- streamLine{comment: strings.TrimPrefix(line, ": ")}
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				lines <- streamLine{event: event, data: strings.TrimPrefix(line, "data: ")}
			}
		}
	}()
	waitFor(t, lines, func(l streamLine) bool { return l.comment == "connected" })
	return lines
}

func waitFor(t *testing.T, lines <-chan streamLine, match func(streamLine) bool) streamLine {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("stream ended")
			}
			if match(l) {
				return l
			}
		case <-timeout:
			t.Fatal("the awaited stream line never arrived")
		}
	}
}

func TestTitleEventFollowsEveryBadgeChange(t *testing.T) {
	doc, router := loadSpec(t)
	schema := doc.Components.Schemas["TitleEvent"].Value
	srv := startFakeTick(t, time.Hour)
	lines := openStream(t, srv)

	titleEvent := func(id string) map[string]any {
		l := waitFor(t, lines, func(l streamLine) bool {
			return l.event == "title" && strings.Contains(l.data, `"`+id+`"`)
		})
		var v map[string]any
		if err := json.Unmarshal([]byte(l.data), &v); err != nil {
			t.Fatal(err)
		}
		if err := schema.VisitJSON(v); err != nil {
			t.Fatalf("title event does not match its schema: %v\n%s", err, l.data)
		}
		return v
	}

	exchange(t, srv, router, "PATCH", "/api/titles/series-1408", `{"monitored":false}`, 200)
	if v := titleEvent("series-1408"); v["badge"] == nil {
		t.Error("unmonitoring must announce the title's new badge")
	}

	exchange(t, srv, router, "POST", "/api/titles/movie-335984/search", "", 200)
	if v := titleEvent("movie-335984"); v["badge"].(map[string]any)["state"] != "searching" {
		t.Errorf("search now: %v", v["badge"])
	}

	exchange(t, srv, router, "DELETE", "/api/titles/movie-13187", "", 204)
	if v := titleEvent("movie-13187"); v["badge"] != nil {
		t.Errorf("a removed title has no badge: %v", v["badge"])
	}
}

func TestStreamSendsKeepAliveCommentsWhenIdle(t *testing.T) {
	f := fake.New(fake.Options{Tick: time.Hour, KeepAlive: 10 * time.Millisecond})
	srv := httptest.NewServer(f)
	t.Cleanup(func() { f.Close(); srv.Close() })
	lines := openStream(t, srv)
	waitFor(t, lines, func(l streamLine) bool { return l.comment == "keep-alive" })
}

func TestProfileCanBeSetAndCleared(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	title := exchange(t, srv, router, "PATCH", "/api/titles/movie-335984", `{"profile":"everything"}`, 200)
	if title["profile"] != "everything" {
		t.Fatalf("set: %v", title["profile"])
	}
	title = exchange(t, srv, router, "PATCH", "/api/titles/movie-335984", `{"profile":null}`, 200)
	if v, ok := title["profile"]; !ok || v != nil {
		t.Fatalf("clear: %v present=%v", v, ok)
	}
	title = exchange(t, srv, router, "GET", "/api/titles/movie-335984", "", 200)
	if title["profile"] != nil {
		t.Fatalf("cleared profile came back: %v", title["profile"])
	}
	for _, body := range []string{`{"profile":"nonsense"}`, `{"profile":""}`, `{"preferredQuality":"1080p"}`} {
		resp := do(t, srv, call{"PATCH", "", body, 400}, "/api/titles/movie-335984")
		_ = resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("%s: %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestATitleIsAddedWithAKnownProfileOnly(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	title := exchange(t, srv, router, "POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"profile":"everything"}`, 201)
	if title["profile"] != "everything" {
		t.Errorf("added with a profile: %v", title["profile"])
	}
	for _, body := range []string{`{"kind":"movie","tmdbId":438631,"profile":"nonsense"}`, `{"kind":"movie","tmdbId":438631,"profile":""}`, `{"kind":"movie","tmdbId":438631,"preferredQuality":"1080p"}`} {
		resp := do(t, srv, call{"POST", "", body, 400}, "/api/titles")
		_ = resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("%s: %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestATitleAddedWithANullProfileUsesTheKindsDefault(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	title := exchange(t, srv, router, "POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"profile":null}`, 201)
	if v, ok := title["profile"]; !ok || v != nil {
		t.Errorf("a null profile stays null: %v", title)
	}
}

func TestProfilesListsTheDefaultAndAnOpenOne(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	out := exchange(t, srv, router, "GET", "/api/profiles", "", 200)
	names := map[string]map[string]any{}
	for _, p := range out["profiles"].([]any) {
		names[p.(map[string]any)["name"].(string)] = p.(map[string]any)
	}
	if names["default"] == nil || names["everything"] == nil || out["defaultMovieProfile"] != "default" || out["defaultSeriesProfile"] != "default" {
		t.Fatalf("%v", out)
	}
	if len(names["default"]["reject"].([]any)) == 0 || len(names["everything"]["reject"].([]any)) != 0 {
		t.Errorf("default rejects the TV-unsafe formats, everything none: %v", names)
	}
}

func TestReleasesShowTheirTagsAndTheProfileRejectedOnesAreDropped(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	reasons := func() (map[string]string, map[string][]any) {
		out := exchange(t, srv, router, "GET", "/api/titles/movie-335984/releases", "", 200)
		drops, tags := map[string]string{}, map[string][]any{}
		for _, d := range out["dropped"].([]any) {
			rel := d.(map[string]any)["release"].(map[string]any)
			drops[rel["id"].(string)] = d.(map[string]any)["reason"].(string)
			tags[rel["id"].(string)], _ = rel["tags"].([]any)
		}
		for _, c := range out["candidates"].([]any) {
			rel := c.(map[string]any)
			tags[rel["id"].(string)], _ = rel["tags"].([]any)
		}
		return drops, tags
	}
	drops, tags := reasons()
	for _, id := range []string{"rel-movie-9", "rel-movie-7"} {
		if drops[id] != "profile_rejected" {
			t.Errorf("%s: dropped as %q, want profile_rejected", id, drops[id])
		}
	}
	if len(tags["rel-movie-9"]) == 0 || tags["rel-movie-2"] == nil {
		t.Errorf("tags are listed, empty when there are none: %v", tags)
	}
	exchange(t, srv, router, "PATCH", "/api/titles/movie-335984", `{"profile":"everything"}`, 200)
	if drops, _ = reasons(); drops["rel-movie-9"] != "" || drops["rel-movie-7"] != "" {
		t.Errorf("the open profile keeps them as candidates: %v", drops)
	}
}

func TestGrabTakesAProfileRejectedReleaseButNotAWrongEpisode(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	exchange(t, srv, router, "POST", "/api/titles/movie-335984/releases/rel-movie-7/grab", "", 202)
	out := exchange(t, srv, router, "POST", "/api/titles/series-1408/releases/rel-series-3/grab", `{"episode":"s04e02"}`, 409)
	if msg, _ := out["error"].(string); msg == "" {
		t.Error("409 must carry the drop reason")
	}
}

func TestDetailTellsWhatWasTriedAndWhenTheNextSearchIs(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)

	title := exchange(t, srv, router, "GET", "/api/titles/movie-335984", "", 200)
	if got, want := title["historySummary"], "Tried 3 releases, 2 fake, 1 stalled, retrying in 6h."; got != want {
		t.Errorf("historySummary %q, want %q", got, want)
	}
	next, _ := title["nextSearchAt"].(string)
	at, err := time.Parse(time.RFC3339, next)
	if err != nil || !at.After(time.Now()) {
		t.Errorf("nextSearchAt %q must be a future date-time (%v)", next, err)
	}

	done := exchange(t, srv, router, "GET", "/api/titles/movie-687163", "", 200)
	if v, ok := done["nextSearchAt"]; !ok || v != nil {
		t.Errorf("a downloaded title has nothing to search for: %v present=%v", v, ok)
	}

	house := exchange(t, srv, router, "GET", "/api/titles/series-1408", "", 200)
	eps := house["seasons"].([]any)[3].(map[string]any)["episodes"].([]any)
	if eps[1].(map[string]any)["nextSearchAt"] == nil {
		t.Error("a missing episode shows when it is searched next")
	}
	if eps[0].(map[string]any)["nextSearchAt"] != nil {
		t.Error("a downloaded episode has no next search")
	}
}

func TestDownloadsReportTheirRealState(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	out := exchange(t, srv, router, "GET", "/api/downloads", "", 200)
	states := map[string]bool{}
	for _, d := range out["downloads"].([]any) {
		d := d.(map[string]any)
		states[d["state"].(string)] = true
		if d["stalled"] == true && d["state"] != "downloading" {
			t.Errorf("a stalled download is still downloading, got %v", d["state"])
		}
	}
	for _, s := range []string{"queued", "downloading", "verifying"} {
		if !states[s] {
			t.Errorf("no fixture download is %s: %v", s, states)
		}
	}
}

func TestCloseSurvivesConcurrentCalls(t *testing.T) {
	for range 1000 {
		f := fake.New(fake.Options{Tick: time.Hour})
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.Close()
			}()
		}
		wg.Wait()
	}
}

func TestSeriesDetailReadsItsHistoryInWords(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	house := exchange(t, srv, router, "GET", "/api/titles/series-1408", "", 200)
	if got, want := house["historySummary"], "Tried 3 releases, 1 stalled, 1 fake, retrying in 6h."; got != want {
		t.Errorf("historySummary = %q, want %q", got, want)
	}
	history := house["history"].([]any)
	if len(history) != 3 {
		t.Errorf("history has %d entries, want 3", len(history))
	}
	for _, h := range history {
		if name := h.(map[string]any)["titleName"]; name != "House" {
			t.Errorf("history entry names its title %q, want House", name)
		}
	}
}

func TestASeriesNotInTheLibraryIsNotFoundButAMovieIs(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)

	got := exchange(t, srv, router, "GET", "/api/titles/series-62560", "", 404)
	if msg, _ := got["error"].(string); msg == "" {
		t.Errorf("a 404 must carry the contract's error shape: %v", got)
	}
	if got := exchange(t, srv, router, "GET", "/api/titles/movie-438631", "", 200); got["inLibrary"] != false {
		t.Errorf("a movie not in the library keeps answering 200: %v", got["inLibrary"])
	}

	exchange(t, srv, router, "POST", "/api/titles", `{"kind":"series","tmdbId":62560}`, 201)
	if got := exchange(t, srv, router, "GET", "/api/titles/series-62560", "", 200); len(got["seasons"].([]any)) == 0 {
		t.Error("once added, the series' episodes are served")
	}
}

func TestHistoryPagesByCursorLikeTheRealService(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	all := exchange(t, srv, router, "GET", "/api/history", "", 200)
	every := all["history"].([]any)
	if all["nextCursor"] != nil || len(every) < 3 {
		t.Fatalf("the fixtures fit the default page: %v", all)
	}
	first := exchange(t, srv, router, "GET", "/api/history?limit=2", "", 200)
	cursor, _ := first["nextCursor"].(string)
	if len(first["history"].([]any)) != 2 || cursor == "" {
		t.Fatalf("first page: %v", first)
	}
	// An entry that lands at the head between the two pages shifts nothing.
	exchange(t, srv, router, "POST", "/api/titles/movie-335984/search", "", 200)
	rest := exchange(t, srv, router, "GET", "/api/history?cursor="+cursor, "", 200)
	got := rest["history"].([]any)
	if len(got) != len(every)-2 || rest["nextCursor"] != nil {
		t.Fatalf("the rest: %v, want %d entries", rest, len(every)-2)
	}
	for i, e := range got {
		if e.(map[string]any)["id"] != every[i+2].(map[string]any)["id"] {
			t.Errorf("entry %d of the rest is %v, want %v", i, e, every[i+2])
		}
	}
	for _, q := range []string{"limit=0", "limit=1001", "limit=x", "cursor=x", "cursor=0", "cursor=-3"} {
		resp, err := srv.Client().Get(srv.URL + "/api/history?" + q)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("?%s: status %d, want 400", q, resp.StatusCode)
		}
	}
}

func monitoredOf(title map[string]any) (monitored []string, total int) {
	for _, se := range title["seasons"].([]any) {
		for _, e := range se.(map[string]any)["episodes"].([]any) {
			m := e.(map[string]any)
			total++
			if m["monitored"] == true {
				monitored = append(monitored, m["id"].(string))
			}
		}
	}
	return monitored, total
}

func TestAddHonoursTheMonitorChoice(t *testing.T) {
	_, router := loadSpec(t)
	cases := []struct {
		monitor   string
		monitored []string
		title     bool
		badge     string
	}{
		{"", []string{"s01e01", "s01e02", "s01e03"}, true, "wanted"},
		{"all", []string{"s01e01", "s01e02", "s01e03"}, true, "wanted"},
		{"future", []string{"s01e03"}, true, "missing"},
		{"first_season", []string{"s01e01", "s01e02", "s01e03"}, true, "wanted"},
		{"latest_season", []string{"s01e01", "s01e02", "s01e03"}, true, "wanted"},
		{"none", nil, false, "missing"},
	}
	for _, c := range cases {
		t.Run("monitor="+c.monitor, func(t *testing.T) {
			srv := startFakeTick(t, time.Hour)
			body := `{"kind":"series","tmdbId":95396}`
			if c.monitor != "" {
				body = `{"kind":"series","tmdbId":95396,"monitor":"` + c.monitor + `"}`
			}
			title := exchange(t, srv, router, "POST", "/api/titles", body, 201)
			got, total := monitoredOf(title)
			if strings.Join(got, ",") != strings.Join(c.monitored, ",") || total != 3 {
				t.Errorf("monitored %v of %d, want %v", got, total, c.monitored)
			}
			if title["monitored"] != c.title {
				t.Errorf("title monitored %v", title["monitored"])
			}
			if b := title["badge"].(map[string]any); b["state"] != c.badge {
				t.Errorf("badge %v, want %s", b, c.badge)
			}
		})
	}
}

func TestAddPicksTheSeasonForFirstAndLatest(t *testing.T) {
	_, router := loadSpec(t)
	// The fixture series has eight seasons of twenty-two episodes.
	season := func(n int) string {
		var ids []string
		for e := 1; e <= 22; e++ {
			ids = append(ids, fmt.Sprintf("s%02de%02d", n, e))
		}
		return strings.Join(ids, ",")
	}
	for monitor, want := range map[string]string{"first_season": season(1), "latest_season": season(8)} {
		t.Run(monitor, func(t *testing.T) {
			srv := startFakeTick(t, time.Hour)
			exchange(t, srv, router, "DELETE", "/api/titles/series-1408", "", 204)
			title := exchange(t, srv, router, "POST", "/api/titles", `{"kind":"series","tmdbId":1408,"monitor":"`+monitor+`"}`, 201)
			if got, _ := monitoredOf(title); strings.Join(got, ",") != want {
				t.Errorf("monitored %v, want %s", got, want)
			}
		})
	}
}

// refuse sends a request the contract does not describe, so only the
// status is checked.
func refuse(t *testing.T, srv *httptest.Server, method, path, body string, status int) {
	t.Helper()
	resp := do(t, srv, call{method, path, body, status}, path)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != status {
		t.Errorf("%s %s %q: status %d, want %d", method, path, body, resp.StatusCode, status)
	}
}

func TestAddRefusesAnUnknownMonitorChoiceAndIgnoresItForAMovie(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	for _, body := range []string{
		`{"kind":"series","tmdbId":95396,"monitor":"some"}`, `{"kind":"series","tmdbId":95396,"monitor":""}`,
		`{"kind":"series","tmdbId":95396,"monitor":null}`,
		`{"kind":"movie","tmdbId":438631,"monitor":"some"}`,
	} {
		refuse(t, srv, "POST", "/api/titles", body, 400)
	}
	if got := exchange(t, srv, router, "GET", "/api/titles/movie-438631", "", 200); got["inLibrary"] != false {
		t.Error("a refused add added the title")
	}
	movie := exchange(t, srv, router, "POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"monitor":"none"}`, 201)
	if movie["monitored"] != true {
		t.Errorf("a movie is always monitored: %v", movie["monitored"])
	}
}

func TestUpdateSeasonMonitorsItsEpisodesAndTheTitle(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	exchange(t, srv, router, "POST", "/api/titles", `{"kind":"series","tmdbId":95396,"monitor":"none"}`, 201)

	title := exchange(t, srv, router, "PUT", "/api/titles/series-95396/seasons/1", `{"monitored":true}`, 200)
	if got, total := monitoredOf(title); len(got) != 3 || total != 3 || title["monitored"] != true {
		t.Errorf("monitored %v, title %v", got, title["monitored"])
	}
	season := title["seasons"].([]any)[0].(map[string]any)
	if season["badge"] == nil {
		t.Error("the season's badge follows")
	}

	title = exchange(t, srv, router, "PUT", "/api/titles/series-95396/seasons/1", `{"monitored":false}`, 200)
	if got, _ := monitoredOf(title); len(got) != 0 || title["monitored"] != true {
		t.Errorf("monitored %v, title %v", got, title["monitored"])
	}
	if b := title["badge"].(map[string]any); b["state"] != "missing" {
		t.Errorf("an unmonitored season reads as missing: %v", b)
	}
}

func TestUpdateSeasonRefusesWhatIsNotThere(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	for _, p := range []string{
		"/api/titles/series-1408/seasons/9", "/api/titles/movie-693134/seasons/1", "/api/titles/series-95396/seasons/1",
	} {
		exchange(t, srv, router, "PUT", p, `{"monitored":true}`, 404)
	}
	for _, p := range []string{"/api/titles/series-1408/seasons/x", "/api/titles/series-1408/seasons/-1"} {
		refuse(t, srv, "PUT", p, `{"monitored":true}`, 404)
	}
	for _, body := range []string{``, `{}`, `{"monitored":"yes"}`} {
		refuse(t, srv, "PUT", "/api/titles/series-1408/seasons/4", body, 400)
	}
}

func TestTheLibraryListsTheSearchingEpisodesOfEachSeries(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	searching := func() map[string]any {
		out := map[string]any{}
		for _, it := range exchange(t, srv, router, "GET", "/api/titles", "", 200)["titles"].([]any) {
			m := it.(map[string]any)
			out[m["id"].(string)] = m["searchingEpisodes"]
		}
		return out
	}

	got := searching()
	if want := []any{"s01e01", "s01e02"}; !reflect.DeepEqual(got["series-226745"], want) {
		t.Errorf("a series waiting on every episode: %v, want %v", got["series-226745"], want)
	}
	if got["movie-693134"] != nil {
		t.Errorf("a movie omits the field: %v", got["movie-693134"])
	}
	if eps, _ := got["series-1408"].([]any); len(eps) == 0 || eps[0] != "s04e02" {
		t.Errorf("episodes that found nothing still wait for a release: %v", got["series-1408"])
	}

	exchange(t, srv, router, "PATCH", "/api/titles/series-226745/episodes/s01e01", `{"monitored":false}`, 200)
	if want := []any{"s01e02"}; !reflect.DeepEqual(searching()["series-226745"], want) {
		t.Errorf("an unmonitored episode is not searched for: %v, want %v", searching()["series-226745"], want)
	}
}

func TestWrongFileMakesADownloadedMovieOrEpisodeMissingAgain(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)

	movie := exchange(t, srv, router, "POST", "/api/titles/movie-687163/wrong-file", "", 200)
	if b := movie["badge"].(map[string]any); b["state"] == "downloaded" {
		t.Errorf("the movie still looks downloaded: %v", b)
	}
	if movie["monitored"] != true {
		t.Error("a wrong file is monitored again")
	}
	exchange(t, srv, router, "POST", "/api/titles/movie-687163/wrong-file", "", 409)

	house := exchange(t, srv, router, "POST", "/api/titles/series-1408/episodes/s01e01/wrong-file", `{"reSearch":false}`, 200)
	ep := house["seasons"].([]any)[0].(map[string]any)["episodes"].([]any)[0].(map[string]any)
	if ep["state"] == "downloaded" || ep["monitored"] != true {
		t.Errorf("the episode: %v", ep)
	}
	exchange(t, srv, router, "POST", "/api/titles/series-1408/episodes/s01e01/wrong-file", "", 409)
	exchange(t, srv, router, "POST", "/api/titles/series-1408/episodes/s09e09/wrong-file", "", 404)
	exchange(t, srv, router, "POST", "/api/titles/series-1408/wrong-file", "", 400)
	exchange(t, srv, router, "POST", "/api/titles/movie-693134/wrong-file", "", 409)
	exchange(t, srv, router, "POST", "/api/titles/movie-1/wrong-file", "", 404)
	resp := do(t, srv, call{"POST", "", `{"nope":1}`, 400}, "/api/titles/movie-687163/wrong-file")
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("an unknown field: %d", resp.StatusCode)
	}

	notes := 0
	for _, h := range exchange(t, srv, router, "GET", "/api/history?titleId=movie-687163", "", 200)["history"].([]any) {
		if h.(map[string]any)["kind"] == "wrong_file" {
			notes++
		}
	}
	if notes != 1 {
		t.Errorf("one history note for the movie: %d", notes)
	}
}

func TestGrabbingATitleThatHasItsFileNeedsReplace(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	exchange(t, srv, router, "POST", "/api/titles/movie-687163/releases/rel-movie-1/grab", "", 409)
	exchange(t, srv, router, "POST", "/api/titles/movie-687163/releases/rel-movie-1/grab", `{"replace":false}`, 409)
	exchange(t, srv, router, "POST", "/api/titles/movie-687163/releases/rel-movie-1/grab", `{"replace":true}`, 202)
	exchange(t, srv, router, "POST", "/api/titles/series-1408/releases/rel-series-5/grab", `{"episode":"s01e01"}`, 409)
	exchange(t, srv, router, "POST", "/api/titles/series-1408/releases/rel-series-5/grab", `{"episode":"s01e01","replace":true}`, 202)
}

func TestRemovingATitleTakesDeleteFilesAsTrueOrFalse(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	resp := do(t, srv, call{"DELETE", "", "", 400}, "/api/titles/movie-13187?deleteFiles=maybe")
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("deleteFiles=maybe: %d", resp.StatusCode)
	}
	exchange(t, srv, router, "DELETE", "/api/titles/movie-13187?deleteFiles=false", "", 204)
	exchange(t, srv, router, "DELETE", "/api/titles/movie-687163?deleteFiles=true", "", 204)
}

func episodeField(t *testing.T, title map[string]any, id string) map[string]any {
	t.Helper()
	for _, s := range title["seasons"].([]any) {
		for _, e := range s.(map[string]any)["episodes"].([]any) {
			if ep := e.(map[string]any); ep["id"] == id {
				return ep
			}
		}
	}
	t.Fatalf("no episode %s", id)
	return nil
}

func TestAFileOfTwoEpisodesIsSharedAndAWrongFileResetsBoth(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)

	title := exchange(t, srv, router, "GET", "/api/titles/series-213375", "", 200)
	for _, id := range []string{"s01e01", "s01e02"} {
		ep := episodeField(t, title, id)
		if got := ep["sharedEpisodes"]; !reflect.DeepEqual(got, []any{"s01e01", "s01e02"}) {
			t.Errorf("%s sharedEpisodes = %v, want both episodes of the file", id, got)
		}
	}
	if ep := episodeField(t, title, "s01e03"); ep["sharedEpisodes"] != nil {
		t.Errorf("an episode with no file shares nothing: %v", ep)
	}
	house := exchange(t, srv, router, "GET", "/api/titles/series-1408", "", 200)
	if ep := episodeField(t, house, "s01e01"); ep["sharedEpisodes"] != nil {
		t.Errorf("a file of one episode is not shared: %v", ep)
	}

	title = exchange(t, srv, router, "POST", "/api/titles/series-213375/episodes/s01e02/wrong-file", `{"reSearch":false}`, 200)
	for _, id := range []string{"s01e01", "s01e02"} {
		ep := episodeField(t, title, id)
		if ep["state"] == "downloaded" || ep["sharedEpisodes"] != nil || ep["monitored"] != true {
			t.Errorf("%s after the wrong file: %v", id, ep)
		}
	}
	exchange(t, srv, router, "POST", "/api/titles/series-213375/episodes/s01e01/wrong-file", "", 409)
}

func TestWithTorBoxHealthListsItAndUncachedReleasesComeLastAndCacheBeforeDownloading(t *testing.T) {
	_, router := loadSpec(t)
	f := fake.New(fake.Options{Tick: 5 * time.Millisecond, Step: 50, TorBox: true})
	srv := httptest.NewServer(f)
	t.Cleanup(func() { f.Close(); srv.Close() })

	var ids []string
	for _, c := range exchange(t, srv, router, "GET", "/api/health", "", 200)["checks"].([]any) {
		ids = append(ids, c.(map[string]any)["id"].(string))
	}
	if !reflect.DeepEqual(ids[:3], []string{"fake_data", "dry_run", "aiostreams"}) || ids[3] != "torbox" {
		t.Errorf("TorBox belongs after AIOStreams: %v", ids)
	}

	rels := exchange(t, srv, router, "GET", "/api/titles/movie-335984/releases", "", 200)
	cands := rels["candidates"].([]any)
	last := cands[len(cands)-1].(map[string]any)
	if last["cached"] != false || last["id"] != "rel-movie-3" || cands[0].(map[string]any)["cached"] != true {
		t.Fatalf("an uncached candidate comes after the cached ones: %v", cands)
	}
	for _, d := range rels["dropped"].([]any) {
		if d.(map[string]any)["reason"] == "not_cached" {
			t.Errorf("nothing is dropped as not cached once TorBox is set up: %v", d)
		}
	}

	exchange(t, srv, router, "POST", "/api/titles/movie-335984/releases/rel-movie-3/grab", "", 202)
	badge := exchange(t, srv, router, "GET", "/api/titles/movie-335984", "", 200)["badge"].(map[string]any)
	if badge["state"] != "caching" && badge["state"] != "downloading" {
		t.Errorf("badge = %v", badge)
	}
	deadline := time.Now().Add(3 * time.Second)
	seen := map[string]bool{}
	for time.Now().Before(deadline) && !seen["downloading"] {
		for _, d := range exchange(t, srv, router, "GET", "/api/downloads", "", 200)["downloads"].([]any) {
			if d.(map[string]any)["releaseName"] == "Blade.Runner.2049.2017.720p.HDTV" {
				seen[d.(map[string]any)["state"].(string)] = true
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !seen["downloading"] {
		t.Errorf("the download never moved on from caching: %v", seen)
	}
}

func TestWithoutTorBoxAnUncachedReleaseStaysDroppedAndOffHealth(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	for _, c := range exchange(t, srv, router, "GET", "/api/health", "", 200)["checks"].([]any) {
		if c.(map[string]any)["id"] == "torbox" {
			t.Fatal("TorBox is on Health without a key")
		}
	}
	exchange(t, srv, router, "POST", "/api/titles/movie-335984/releases/rel-movie-3/grab", "", 409)
}

func TestSearchNowShowsSearchingThenNothingFoundWithTheNextTry(t *testing.T) {
	_, router := loadSpec(t)
	f := fake.New(fake.Options{Tick: 5 * time.Millisecond, SearchFor: 50 * time.Millisecond})
	srv := httptest.NewServer(f)
	t.Cleanup(func() { f.Close(); srv.Close() })
	state := func(v map[string]any) any { return v["badge"].(map[string]any)["state"] }

	camp := exchange(t, srv, router, "GET", "/api/titles/series-226745", "", 200)
	if state(camp) != "wanted" || camp["nextSearchAt"] == nil {
		t.Errorf("a series waiting for its next search: %v %v", camp["badge"], camp["nextSearchAt"])
	}
	blade := exchange(t, srv, router, "GET", "/api/titles/movie-335984", "", 200)
	if state(blade) != "not_found" || blade["nextSearchAt"] == nil {
		t.Errorf("a movie whose search found nothing: %v %v", blade["badge"], blade["nextSearchAt"])
	}

	blade = exchange(t, srv, router, "POST", "/api/titles/movie-335984/search", "", 200)
	if state(blade) != "searching" || blade["nextSearchAt"] != nil {
		t.Errorf("Search now: %v %v", blade["badge"], blade["nextSearchAt"])
	}
	camp = exchange(t, srv, router, "POST", "/api/titles/series-226745/episodes/s01e02/search", "", 200)
	eps := camp["seasons"].([]any)[0].(map[string]any)["episodes"].([]any)
	if e := eps[1].(map[string]any); e["state"] != "searching" || state(camp) != "searching" {
		t.Errorf("Search now on an episode: %v, title %v", e, camp["badge"])
	}
	if e := eps[0].(map[string]any); e["state"] != "wanted" {
		t.Errorf("the other episode is left alone: %v", e)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		blade = exchange(t, srv, router, "GET", "/api/titles/movie-335984", "", 200)
		if state(blade) != "searching" || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	next, _ := blade["nextSearchAt"].(string)
	if at, err := time.Parse(time.RFC3339, next); state(blade) != "not_found" || err != nil || time.Until(at) < 5*time.Hour {
		t.Errorf("the search ends having found nothing, next try in hours: %v %v", blade["badge"], blade["nextSearchAt"])
	}
	after := episodes(exchange(t, srv, router, "GET", "/api/titles/series-226745", "", 200))
	if after[1]["state"] != "not_found" {
		t.Errorf("the episode's search ends having found nothing: %v", after[1])
	}
}

func episodes(title map[string]any) []map[string]any {
	var out []map[string]any
	for _, s := range title["seasons"].([]any) {
		for _, e := range s.(map[string]any)["episodes"].([]any) {
			out = append(out, e.(map[string]any))
		}
	}
	return out
}

func TestSearchNowOnASeriesWithNothingMonitoredSearchesEveryAiredEpisode(t *testing.T) {
	_, router := loadSpec(t)
	f := fake.New(fake.Options{Tick: time.Hour, SearchFor: time.Hour})
	srv := httptest.NewServer(f)
	t.Cleanup(func() { f.Close(); srv.Close() })

	exchange(t, srv, router, "PATCH", "/api/titles/series-226745", `{"monitored":false}`, 200)
	camp := exchange(t, srv, router, "POST", "/api/titles/series-226745/search", "", 200)
	if got := states(camp); !reflect.DeepEqual(got, []any{"searching", "wanted"}) {
		t.Errorf("as the real service does, every aired episode is queued when none is monitored, one searched at a time: %v", got)
	}
}

func states(title map[string]any) []any {
	var out []any
	for _, e := range episodes(title) {
		out = append(out, e["state"])
	}
	return out
}

func TestSearchNowOnASeriesSearchesOneEpisodeAtATimeAndTheRestReadWanted(t *testing.T) {
	_, router := loadSpec(t)
	f := fake.New(fake.Options{Tick: 5 * time.Millisecond, SearchFor: 150 * time.Millisecond})
	srv := httptest.NewServer(f)
	t.Cleanup(func() { f.Close(); srv.Close() })
	state := func(v map[string]any) any { return v["badge"].(map[string]any)["state"] }

	camp := exchange(t, srv, router, "POST", "/api/titles/series-226745/search", "", 200)
	if got := states(camp); !reflect.DeepEqual(got, []any{"searching", "wanted"}) || state(camp) != "searching" {
		t.Errorf("Search now on a series: episodes %v, title %v", got, camp["badge"])
	}
	queued := episodes(camp)[1]
	next, _ := queued["nextSearchAt"].(string)
	if at, err := time.Parse(time.RFC3339Nano, next); err != nil || time.Until(at) > time.Minute {
		t.Errorf("a queued episode is due shortly: %v", queued["nextSearchAt"])
	}

	seen := map[string]bool{}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := states(exchange(t, srv, router, "GET", "/api/titles/series-226745", "", 200))
		seen[fmt.Sprint(got)] = true
		if reflect.DeepEqual(got, []any{"not_found", "not_found"}) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !seen["[not_found searching]"] || !seen["[not_found not_found]"] || seen["[searching searching]"] {
		t.Errorf("the second episode searches after the first, never both at once: %v", seen)
	}
}

func TestStoppingAFinishedOrCancelledDownloadAnswers409(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFake(t)
	grabbed := exchange(t, srv, router, "POST", "/api/titles/movie-335984/releases/rel-movie-1/grab", "", 202)
	done := grabbed["id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var title struct{ Badge struct{ State string } }
		getJSON(t, srv, "/api/titles/movie-335984", &title)
		if title.Badge.State == "downloaded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the grab never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}

	still := startFakeTick(t, time.Hour)
	exchange(t, still, router, "POST", "/api/downloads/dl-3/cancel", "", 204)
	for _, op := range []string{"pause", "resume", "cancel"} {
		exchange(t, srv, router, "POST", "/api/downloads/"+done+"/"+op, "", 409)
		exchange(t, still, router, "POST", "/api/downloads/dl-3/"+op, "", 409)
	}
}

func downloadsByID(out map[string]any) map[string]map[string]any {
	by := map[string]map[string]any{}
	for _, d := range out["downloads"].([]any) {
		d := d.(map[string]any)
		by[d["id"].(string)] = d
	}
	return by
}

func TestPauseAllHoldsTransfersKeepsQueuedOnesQueuedAndResumeAllRestoresThemAsTheyWere(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)

	out := exchange(t, srv, router, "GET", "/api/downloads", "", 200)
	if out["paused"] != false {
		t.Fatalf("the queue starts with Pause all %v", out["paused"])
	}
	if by := downloadsByID(out); by["dl-1"]["bytesPerSecond"].(float64) <= 0 || by["dl-3"]["bytesPerSecond"] != float64(0) || by["dl-2"]["bytesPerSecond"] != float64(0) {
		t.Fatalf("only a moving download has a rate: dl-1 %v, dl-2 (stalled) %v, dl-3 (queued) %v", by["dl-1"], by["dl-2"], by["dl-3"])
	}
	exchange(t, srv, router, "POST", "/api/downloads/dl-5/pause", "", 200)

	out = exchange(t, srv, router, "POST", "/api/downloads/pause", "", 200)
	by := downloadsByID(out)
	if out["paused"] != true {
		t.Fatalf("Pause all answered %v", out)
	}
	for id, want := range map[string]string{"dl-1": "paused", "dl-2": "paused", "dl-3": "queued", "dl-4": "verifying", "dl-5": "paused"} {
		if by[id]["state"] != want {
			t.Errorf("%s is %v under Pause all, want %s", id, by[id]["state"], want)
		}
	}
	if by["dl-1"]["bytesPerSecond"] != float64(0) || by["dl-1"]["stalled"] != false || by["dl-2"]["stalled"] != false {
		t.Errorf("a held download has a rate or reads stalled: %v %v", by["dl-1"], by["dl-2"])
	}
	if again := exchange(t, srv, router, "POST", "/api/downloads/pause", "", 200); again["paused"] != true {
		t.Errorf("a second Pause all answered %v", again)
	}

	out = exchange(t, srv, router, "POST", "/api/downloads/resume", "", 200)
	by = downloadsByID(out)
	if out["paused"] != false {
		t.Fatalf("Resume all answered %v", out)
	}
	for id, want := range map[string]string{"dl-1": "downloading", "dl-2": "downloading", "dl-3": "queued", "dl-5": "paused"} {
		if by[id]["state"] != want {
			t.Errorf("%s is %v after Resume all, want %s (what was paused on its own stays paused)", id, by[id]["state"], want)
		}
	}
	if by["dl-1"]["bytesPerSecond"].(float64) <= 0 || by["dl-2"]["stalled"] != true {
		t.Errorf("Resume all did not give back the rate and the stall: %v %v", by["dl-1"], by["dl-2"])
	}
}

func TestPauseAllStopsTheSimulatedDownloadsMovingAndAnnouncesItself(t *testing.T) {
	doc, router := loadSpec(t)
	progress, downloads := doc.Components.Schemas["ProgressEvent"].Value, doc.Components.Schemas["DownloadsEvent"].Value
	srv := startFakeTick(t, 5*time.Millisecond)
	lines := openStream(t, srv)

	exchange(t, srv, router, "POST", "/api/downloads/pause", "", 200)
	l := waitFor(t, lines, func(l streamLine) bool { return l.event == "downloads" })
	var v map[string]any
	if err := json.Unmarshal([]byte(l.data), &v); err != nil {
		t.Fatal(err)
	}
	if err := downloads.VisitJSON(v); err != nil || v["paused"] != true {
		t.Fatalf("downloads event %s: %v", l.data, err)
	}
	l = waitFor(t, lines, func(l streamLine) bool {
		return l.event == "progress" && strings.Contains(l.data, `"dl-1"`) && strings.Contains(l.data, `"paused"`)
	})
	v = nil
	if err := json.Unmarshal([]byte(l.data), &v); err != nil {
		t.Fatal(err)
	}
	if err := progress.VisitJSON(v); err != nil || v["bytesPerSecond"] != float64(0) {
		t.Fatalf("progress event %s: %v", l.data, err)
	}

	first := downloadsByID(exchange(t, srv, router, "GET", "/api/downloads", "", 200))["dl-1"]["percent"]
	time.Sleep(100 * time.Millisecond)
	if later := downloadsByID(exchange(t, srv, router, "GET", "/api/downloads", "", 200))["dl-1"]["percent"]; later != first {
		t.Errorf("a download moved under Pause all: %v then %v", first, later)
	}

	exchange(t, srv, router, "POST", "/api/downloads/resume", "", 200)
	waitFor(t, lines, func(l streamLine) bool { return l.event == "downloads" && strings.Contains(l.data, "false") })
	l = waitFor(t, lines, func(l streamLine) bool {
		return l.event == "progress" && strings.Contains(l.data, `"dl-1"`) && strings.Contains(l.data, `"downloading"`)
	})
	v = nil
	_ = json.Unmarshal([]byte(l.data), &v)
	if err := progress.VisitJSON(v); err != nil || v["bytesPerSecond"].(float64) <= 0 {
		t.Fatalf("progress event %s: %v", l.data, err)
	}
}

func TestAGrabbedDownloadReportsTheDefaultRateOnceItMoves(t *testing.T) {
	_, router := loadSpec(t)
	f := fake.New(fake.Options{Tick: 5 * time.Millisecond, Step: 1})
	srv := httptest.NewServer(f)
	t.Cleanup(func() { f.Close(); srv.Close() })

	exchange(t, srv, router, "POST", "/api/titles/series-1408/releases/rel-series-5/grab", `{"episode":"s04e02"}`, 202)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, d := range exchange(t, srv, router, "GET", "/api/downloads", "", 200)["downloads"].([]any) {
			if d := d.(map[string]any); d["releaseName"] == "House.S04E02.480p.DVDRip.XviD" && d["state"] == "downloading" {
				if d["bytesPerSecond"].(float64) <= 0 {
					t.Errorf("a grabbed download is moving at no rate: %v", d)
				}
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Error("the grabbed download never started")
}

func TestAStalledBadgeClearsUnderPauseAll(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	badge := func() any {
		return exchange(t, srv, router, "GET", "/api/titles/movie-329865", "", 200)["badge"].(map[string]any)["state"]
	}
	if got := badge(); got != "stalled" {
		t.Fatalf("the stalled movie's badge is %v, want stalled", got)
	}
	exchange(t, srv, router, "POST", "/api/downloads/pause", "", 200)
	if got := badge(); got != "downloading" {
		t.Errorf("under Pause all the stalled movie's badge is %v, want downloading", got)
	}
	exchange(t, srv, router, "POST", "/api/downloads/resume", "", 200)
	if got := badge(); got != "stalled" {
		t.Errorf("after Resume all the stalled movie's badge is %v, want stalled", got)
	}
}
