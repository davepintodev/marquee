package fake_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestPreferredQualityCanBeSetAndCleared(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	title := exchange(t, srv, router, "PATCH", "/api/titles/movie-335984", `{"preferredQuality":"1080p"}`, 200)
	if title["preferredQuality"] != "1080p" {
		t.Fatalf("set: %v", title["preferredQuality"])
	}
	title = exchange(t, srv, router, "PATCH", "/api/titles/movie-335984", `{"preferredQuality":null}`, 200)
	if v, ok := title["preferredQuality"]; !ok || v != nil {
		t.Fatalf("clear: %v present=%v", v, ok)
	}
	title = exchange(t, srv, router, "GET", "/api/titles/movie-335984", "", 200)
	if title["preferredQuality"] != nil {
		t.Fatalf("cleared quality came back: %v", title["preferredQuality"])
	}
	resp := do(t, srv, call{"PATCH", "", `{"preferredQuality":"480p"}`, 400}, "/api/titles/movie-335984")
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("an unknown quality: %d, want 400", resp.StatusCode)
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

func TestHistoryPagesLikeTheRealService(t *testing.T) {
	_, router := loadSpec(t)
	srv := startFakeTick(t, time.Hour)
	all := exchange(t, srv, router, "GET", "/api/history", "", 200)
	if all["nextOffset"] != nil || len(all["history"].([]any)) < 3 {
		t.Fatalf("the fixtures fit the default page: %v", all)
	}
	first := exchange(t, srv, router, "GET", "/api/history?limit=2", "", 200)
	if len(first["history"].([]any)) != 2 || first["nextOffset"] != float64(2) {
		t.Errorf("first page: %v", first)
	}
	rest := exchange(t, srv, router, "GET", "/api/history?offset=2", "", 200)
	if want := len(all["history"].([]any)) - 2; len(rest["history"].([]any)) != want || rest["nextOffset"] != nil {
		t.Errorf("the rest: %v, want %d entries", rest, want)
	}
}
