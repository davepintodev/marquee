package app_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/tmdbfake"
)

const multiSeasonID = 5000

// addMultiSeason puts a series with two regular seasons and a special (which
// the catalog never stores) on TMDB: season 1 has aired, season 2 has one aired and one future episode
// (today is 2026-11-18).
func addMultiSeason(r *schedRig) {
	r.tmdb.Add(tmdbfake.Title{Kind: "series", ID: multiSeasonID, Name: "Many Seasons", Date: "2025-01-01", IMDb: "tt5000", TVDB: 500000,
		Seasons: []tmdbfake.Season{
			{Number: 0, Episodes: []tmdbfake.Episode{{Number: 1, Name: "Special", AirDate: "2025-01-01"}}},
			{Number: 1, Episodes: []tmdbfake.Episode{
				{Number: 1, Name: "S1 One", AirDate: "2025-02-01"},
				{Number: 2, Name: "S1 Two", AirDate: "2025-02-08"},
			}},
			{Number: 2, Episodes: []tmdbfake.Episode{
				{Number: 1, Name: "S2 One", AirDate: "2026-06-01"},
				{Number: 2, Name: "S2 Two", AirDate: "2026-12-25"},
			}},
		}})
}

func multiSeasonBody(monitor string) string {
	if monitor == "" {
		return fmt.Sprintf(`{"kind":"series","tmdbId":%d}`, multiSeasonID)
	}
	return fmt.Sprintf(`{"kind":"series","tmdbId":%d,"monitor":%q}`, multiSeasonID, monitor)
}

const multiSeasonPath = "/api/titles/series-5000"

// monitoredIDs lists the monitored episodes of a title, sorted.
func monitoredIDs(t *testing.T, title map[string]any) []string {
	t.Helper()
	out := []string{}
	for id, e := range episodes(t, title) {
		if e["monitored"] == true {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// searchedEpisodes steps the scheduler until it is idle and returns the
// episodes of the multi-season series it searched for.
func (r *schedRig) searchedEpisodes() []string {
	for range 6 {
		r.step()
		r.advance(5 * time.Second)
	}
	var out []string
	for _, s := range r.aio.Searches() {
		if rest, ok := strings.CutPrefix(s.ID, "tvdb:500000:"); ok {
			out = append(out, rest)
		}
	}
	slices.Sort(out)
	return out
}

func TestAddingASeriesMonitorsExactlyWhatTheChoiceNames(t *testing.T) {
	cases := []struct {
		monitor   string
		title     bool
		monitored []string
		searched  []string
	}{
		{"", true, []string{"s01e01", "s01e02", "s02e01", "s02e02"}, []string{"1:1", "1:2", "2:1"}},
		{"all", true, []string{"s01e01", "s01e02", "s02e01", "s02e02"}, []string{"1:1", "1:2", "2:1"}},
		{"future", true, []string{"s02e02"}, nil},
		{"first_season", true, []string{"s01e01", "s01e02"}, []string{"1:1", "1:2"}},
		{"latest_season", true, []string{"s02e01", "s02e02"}, []string{"2:1"}},
		{"none", false, []string{}, nil},
	}
	for _, c := range cases {
		t.Run("monitor="+c.monitor, func(t *testing.T) {
			r := newSchedRig(t, false)
			addMultiSeason(r)
			v := r.mustCall("POST", "/api/titles", multiSeasonBody(c.monitor), 201)
			if got := monitoredIDs(t, v); !slices.Equal(got, c.monitored) {
				t.Errorf("monitored episodes %v, want %v", got, c.monitored)
			}
			if v["monitored"] != c.title {
				t.Errorf("title monitored %v, want %v", v["monitored"], c.title)
			}
			if got := r.searchedEpisodes(); !slices.Equal(got, c.searched) {
				t.Errorf("searched %v, want %v", got, c.searched)
			}
			// What the add answered is what the library holds.
			if got := monitoredIDs(t, r.mustCall("GET", multiSeasonPath, "", 200)); !slices.Equal(got, c.monitored) {
				t.Errorf("after reading it back: %v, want %v", got, c.monitored)
			}
		})
	}
}

func TestASeriesAddedWithNoneIsNotSearchedNorRefreshedUntilSomethingIsMonitored(t *testing.T) {
	r := newSchedRig(t, false)
	addMultiSeason(r)
	v := r.mustCall("POST", "/api/titles", multiSeasonBody("none"), 201)
	if b := v["badge"].(map[string]any); b["state"] != "missing" {
		t.Errorf("a title nobody monitors with nothing on disk is missing: %v", b)
	}
	if v["historySummary"] != "Not monitored." {
		t.Errorf("history summary %v", v["historySummary"])
	}
	r.tmdb.Replace(tmdbfake.Title{Kind: "series", ID: multiSeasonID, Name: "Many Seasons", Date: "2025-01-01", IMDb: "tt5000", TVDB: 500000,
		Seasons: []tmdbfake.Season{{Number: 1, Episodes: []tmdbfake.Episode{
			{Number: 1, Name: "S1 One", AirDate: "2025-02-01"}, {Number: 2, Name: "S1 Two", AirDate: "2025-02-08"},
			{Number: 3, Name: "S1 Three", AirDate: "2026-11-10"},
		}}}})
	r.advance(25 * time.Hour)
	if got := r.searchedEpisodes(); got != nil {
		t.Fatalf("searched %v", got)
	}
	v = r.mustCall("GET", multiSeasonPath, "", 200)
	if got := monitoredIDs(t, v); len(got) != 0 {
		t.Errorf("the refresh must not monitor a series the owner chose to monitor nothing of: %v", got)
	}
}

func TestMonitorIsIgnoredForAMovie(t *testing.T) {
	r := newSchedRig(t, false)
	v := r.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"monitor":"none"}`, 201)
	if v["monitored"] != true {
		t.Errorf("a movie is always monitored: %v", v["monitored"])
	}
	r.step()
	r.wantSearches(1, "the movie is searched")
}

func TestAnUnknownMonitorValueIsRefusedAndAddsNothing(t *testing.T) {
	r := newSchedRig(t, false)
	addMultiSeason(r)
	for _, body := range []string{
		fmt.Sprintf(`{"kind":"series","tmdbId":%d,"monitor":"everything"}`, multiSeasonID),
		fmt.Sprintf(`{"kind":"series","tmdbId":%d,"monitor":""}`, multiSeasonID),
		fmt.Sprintf(`{"kind":"series","tmdbId":%d,"monitor":3}`, multiSeasonID),
		`{"kind":"movie","tmdbId":438631,"monitor":"everything"}`,
	} {
		if resp := r.bad("POST", "/api/titles", body); resp.status != 400 {
			t.Errorf("%s: %d", body, resp.status)
		}
	}
	if got := r.mustCall("GET", "/api/titles", "", 200)["titles"].([]any); len(got) != 0 {
		t.Errorf("a refused add left %d titles behind", len(got))
	}
	if r.tmdb.Hits("/3/tv/5000") != 0 {
		t.Error("the value is checked before TMDB is asked")
	}
}

func TestAddingASeriesAlreadyInTheLibraryIgnoresTheChoice(t *testing.T) {
	r := newSchedRig(t, false)
	addMultiSeason(r)
	r.mustCall("POST", "/api/titles", multiSeasonBody(""), 201)
	v := r.mustCall("POST", "/api/titles", multiSeasonBody("none"), 200)
	if v["monitored"] != true || len(monitoredIDs(t, v)) != 4 {
		t.Errorf("a second add changes nothing: %v", monitoredIDs(t, v))
	}
}

func TestSettingASeasonMonitoredMonitorsItsEpisodesAndTheTitle(t *testing.T) {
	r := newSchedRig(t, false)
	addMultiSeason(r)
	r.mustCall("POST", "/api/titles", multiSeasonBody("none"), 201)

	v := r.mustCall("PUT", multiSeasonPath+"/seasons/2", `{"monitored":true}`, 200)
	if got := monitoredIDs(t, v); !slices.Equal(got, []string{"s02e01", "s02e02"}) {
		t.Errorf("monitored %v", got)
	}
	if v["monitored"] != true {
		t.Error("monitoring a season monitors the title, so it never searches while reporting itself unmonitored")
	}
	if b := seasonBadge(t, v, 2); b["state"] != "searching" {
		t.Errorf("the season's badge follows: %v", b)
	}
	if got := r.searchedEpisodes(); !slices.Equal(got, []string{"2:1"}) {
		t.Errorf("only the aired monitored episode is searched: %v", got)
	}

	v = r.mustCall("PUT", multiSeasonPath+"/seasons/1", `{"monitored":true}`, 200)
	if got := monitoredIDs(t, v); len(got) != 4 {
		t.Errorf("the other seasons keep their state: %v", got)
	}

	v = r.mustCall("PUT", multiSeasonPath+"/seasons/2", `{"monitored":false}`, 200)
	if got := monitoredIDs(t, v); !slices.Equal(got, []string{"s01e01", "s01e02"}) {
		t.Errorf("monitored %v", got)
	}
	if v["monitored"] != true {
		t.Error("unmonitoring a season leaves the title monitored")
	}
	if b := seasonBadge(t, v, 2); b != nil {
		t.Errorf("a season with nothing monitored has no badge: %v", b)
	}

	v = r.mustCall("PUT", multiSeasonPath+"/seasons/1", `{"monitored":false}`, 200)
	if got := monitoredIDs(t, v); len(got) != 0 || v["monitored"] != true {
		t.Errorf("unmonitoring the last monitored season leaves the title monitored: %v %v", got, v["monitored"])
	}
}

func TestAnUnmonitoredSeasonIsNotSearched(t *testing.T) {
	r := newSchedRig(t, false)
	addMultiSeason(r)
	r.mustCall("POST", "/api/titles", multiSeasonBody(""), 201)
	r.mustCall("PUT", multiSeasonPath+"/seasons/1", `{"monitored":false}`, 200)
	if got := r.searchedEpisodes(); !slices.Equal(got, []string{"2:1"}) {
		t.Errorf("searched %v, want season 2 only", got)
	}
}

func TestSettingASeasonThatIsNotThereIs404(t *testing.T) {
	r := newSchedRig(t, false)
	addMultiSeason(r)
	r.mustCall("POST", "/api/titles", multiSeasonBody(""), 201)
	r.mustCall("POST", "/api/titles", movieBody, 201)
	for _, p := range []string{
		multiSeasonPath + "/seasons/9",
		multiSeasonPath + "/seasons/0",
		multiSeasonPath + "/seasons/-1",
		multiSeasonPath + "/seasons/one",
		"/api/titles/series-1/seasons/1",
		"/api/titles/movie-438631/seasons/1",
	} {
		resp := r.bad("PUT", p, `{"monitored":true}`)
		if resp.status != 404 {
			t.Errorf("%s: %d", p, resp.status)
			continue
		}
		if m := resp.json(t); m["error"] == nil || len(m) != 1 {
			t.Errorf("%s: the contract's error shape is {error}: %v", p, m)
		}
	}
	if v := r.mustCall("GET", multiSeasonPath, "", 200); len(monitoredIDs(t, v)) != 4 {
		t.Error("a refused call changed something")
	}
}

func TestSettingASeasonNeedsMonitoredAsABoolean(t *testing.T) {
	r := newSchedRig(t, false)
	addMultiSeason(r)
	r.mustCall("POST", "/api/titles", multiSeasonBody(""), 201)
	for _, body := range []string{``, `{}`, `{"monitored":"yes"}`, `{"monitored":null}`, `{"monitored":true,"x":1}`} {
		if resp := r.bad("PUT", multiSeasonPath+"/seasons/1", body); resp.status != 400 {
			t.Errorf("%q: %d", body, resp.status)
		}
	}
}

func seasonBadge(t *testing.T, title map[string]any, number int) map[string]any {
	t.Helper()
	for _, s := range title["seasons"].([]any) {
		m := s.(map[string]any)
		if m["number"] == float64(number) {
			b, _ := m["badge"].(map[string]any)
			return b
		}
	}
	t.Fatalf("no season %d", number)
	return nil
}
