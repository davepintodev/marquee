package fake_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"

	"github.com/davepintodev/marquee/internal/apitest"
	"github.com/davepintodev/marquee/internal/fake"
)

func loadSpec(t *testing.T) (*openapi3.T, routers.Router) {
	t.Helper()
	return apitest.LoadSpec(t)
}

func startFake(t *testing.T) *httptest.Server {
	t.Helper()
	return startFakeTick(t, 5*time.Millisecond)
}

func startFakeTick(t *testing.T, tick time.Duration) *httptest.Server {
	t.Helper()
	f := fake.New(fake.Options{Tick: tick, Step: 10})
	srv := httptest.NewServer(f)
	t.Cleanup(func() { f.Close(); srv.Close() })
	return srv
}

type call struct {
	method, path, body string
	status             int
}

var calls = map[string]call{
	"getWall":           {"GET", "/api/wall", "", 200},
	"searchTitles":      {"GET", "/api/search?q=dune", "", 200},
	"listTitles":        {"GET", "/api/titles", "", 200},
	"addTitle":          {"POST", "/api/titles", `{"kind":"series","tmdbId":83867}`, 201},
	"getTitle":          {"GET", "/api/titles/series-1408", "", 200},
	"updateTitle":       {"PATCH", "/api/titles/movie-693134", `{"monitored":false}`, 200},
	"removeTitle":       {"DELETE", "/api/titles/movie-13187", "", 204},
	"searchTitleNow":    {"POST", "/api/titles/movie-329865/search", "", 200},
	"updateEpisode":     {"PATCH", "/api/titles/series-1408/episodes/s04e02", `{"monitored":false}`, 200},
	"searchEpisodeNow":  {"POST", "/api/titles/series-1408/episodes/s04e02/search", "", 200},
	"listReleases":      {"GET", "/api/titles/series-1408/releases?episode=s04e02", "", 200},
	"grabRelease":       {"POST", "/api/titles/movie-335984/releases/rel-movie-1/grab", "", 202},
	"listDownloads":     {"GET", "/api/downloads", "", 200},
	"listHistory":       {"GET", "/api/history", "", 200},
	"retryHistoryEntry": {"POST", "/api/history/h-failed-stalled/retry", "", 200},
	"streamEvents":      {"GET", "/api/events", "", 200},
	"getImage":          {"GET", "/api/images/poster/movie-438631", "", 200},
	"getHealth":         {"GET", "/api/health", "", 200},
}

func do(t *testing.T, srv *httptest.Server, c call, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(c.method, srv.URL+path, strings.NewReader(c.body))
	if err != nil {
		t.Fatal(err)
	}
	if c.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func validate(t *testing.T, router routers.Router, c call, path string, resp *http.Response, status int) {
	t.Helper()
	apitest.Validate(t, router, c.method, path, c.body, resp, status)
}

func TestEveryOperationIsCoveredAndMatchesItsSchema(t *testing.T) {
	doc, router := loadSpec(t)
	srv := startFake(t)

	seen := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			c, ok := calls[op.OperationID]
			if !ok {
				t.Errorf("%s %s (%s) has no case in the contract test", method, path, op.OperationID)
				continue
			}
			seen[op.OperationID] = true
			if c.method != method {
				t.Errorf("%s: case uses %s, contract says %s", op.OperationID, c.method, method)
			}
		}
	}
	for id := range calls {
		if !seen[id] {
			t.Errorf("case %s is not an operation of the contract", id)
		}
	}

	for id, c := range calls {
		t.Run(id, func(t *testing.T) {
			if id == "streamEvents" {
				return // an endless stream; TestEventsStreamEmitsProgressForARunningDownload covers it
			}
			resp := do(t, srv, c, c.path)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != c.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, c.status)
			}
			validate(t, router, c, c.path, resp, c.status)
		})
	}
}

func TestMissingResourcesAnswerWithTheContractsError(t *testing.T) {
	doc, router := loadSpec(t)
	srv := startFake(t)
	swap := strings.NewReplacer(
		"series-1408", "series-1", "movie-693134", "movie-1", "movie-13187", "movie-1",
		"movie-329865", "movie-1", "movie-335984", "movie-1", "movie-438631", "movie-1",
		"h-failed-stalled", "h-none", "rel-movie-1", "rel-none",
	)
	for path, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			if op.Responses.Status(404) == nil {
				continue
			}
			c := calls[op.OperationID]
			p := swap.Replace(c.path)
			if op.OperationID == "addTitle" {
				c.body, p = `{"kind":"movie","tmdbId":1}`, c.path
			}
			if p == c.path && op.OperationID != "addTitle" {
				t.Errorf("%s (%s): case has nothing to make missing", op.OperationID, path)
				continue
			}
			t.Run(op.OperationID, func(t *testing.T) {
				resp := do(t, srv, c, p)
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != 404 {
					t.Fatalf("status %d, want 404", resp.StatusCode)
				}
				validate(t, router, c, p, resp, 404)
			})
		}
	}
}

func TestEventsStreamEmitsProgressForARunningDownload(t *testing.T) {
	doc, _ := loadSpec(t)
	srv := startFake(t)
	schema := doc.Components.Schemas["ProgressEvent"].Value

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}

	var percents []int
	var name string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() && len(percents) < 2 {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: ") && name == "progress":
			var v map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v); err != nil {
				t.Fatal(err)
			}
			if v["downloadId"] != "dl-1" {
				continue
			}
			if err := schema.VisitJSON(v); err != nil {
				t.Fatalf("progress event does not match its schema: %v", err)
			}
			percents = append(percents, int(v["percent"].(float64)))
		}
	}
	if len(percents) < 2 {
		t.Fatalf("want two progress events, got %v (%v)", percents, sc.Err())
	}
	if percents[1] <= percents[0] {
		t.Errorf("progress did not advance: %v", percents)
	}
}

func getJSON(t *testing.T, srv *httptest.Server, path string, v any) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestFixturesShowEveryBadgeState(t *testing.T) {
	srv := startFakeTick(t, time.Hour)
	var w struct {
		Items []struct {
			Title struct {
				Badge *struct {
					State, Label string
				}
			}
		}
	}
	getJSON(t, srv, "/api/wall", &w)
	labels := map[string]map[string]bool{}
	for _, it := range w.Items {
		if b := it.Title.Badge; b != nil {
			if labels[b.State] == nil {
				labels[b.State] = map[string]bool{}
			}
			labels[b.State][b.Label] = true
		}
	}
	want := map[string]string{
		"downloaded": "Downloaded", "searching": "Searching", "downloading": "Downloading 43%",
		"stalled": "Stalled", "missing": "Missing", "partial_season": "Season 4 · 3 missing",
	}
	for state, label := range want {
		if !labels[state][label] {
			t.Errorf("badge %s: want %q among %v", state, label, labels[state])
		}
	}
}

func TestAddingATitleIsIdempotentAndItAppearsInTheLibrary(t *testing.T) {
	srv := startFake(t)
	post := func() int {
		resp, err := srv.Client().Post(srv.URL+"/api/titles", "application/json", bytes.NewBufferString(`{"kind":"movie","tmdbId":438631}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := post(); got != 201 {
		t.Fatalf("first add: %d", got)
	}
	if got := post(); got != 200 {
		t.Fatalf("second add: %d, want 200 and no change", got)
	}
	var lib struct{ Titles []struct{ ID string } }
	getJSON(t, srv, "/api/titles", &lib)
	n := 0
	for _, x := range lib.Titles {
		if x.ID == "movie-438631" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("title is in the library %d times", n)
	}
}

func TestAGrabbedReleaseDownloadsAndTheTitleBecomesDownloaded(t *testing.T) {
	srv := startFake(t)
	resp, err := srv.Client().Post(srv.URL+"/api/titles/movie-335984/releases/rel-movie-1/grab", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("grab: %d", resp.StatusCode)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var title struct{ Badge struct{ State string } }
		getJSON(t, srv, "/api/titles/movie-335984", &title)
		if title.Badge.State == "downloaded" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("title never became downloaded")
}

func TestRetryingTwiceIsRefused(t *testing.T) {
	srv := startFake(t)
	retry := func() int {
		resp, err := srv.Client().Post(srv.URL+"/api/history/h-failed-fake/retry", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := retry(); got != 200 {
		t.Fatalf("first retry: %d", got)
	}
	if got := retry(); got != 409 {
		t.Fatalf("second retry: %d, want 409", got)
	}
}
