package app_test

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/davepintodev/marquee/internal/tmdbfake"
)

// adoptTMDB is a TMDB that knows a small library: "House" has five seasons
// of three episodes with the last one still to air, two series and two
// movies share a name, and "Dune Documentary" only contains one.
func adoptTMDB(t *testing.T) *tmdbfake.Fake {
	t.Helper()
	f := tmdbfake.New()
	t.Cleanup(f.Close)
	f.Add(tmdbfake.Title{Kind: "movie", ID: 438631, Name: "Dune", Date: "2021-09-15", Overview: "Paul Atreides.", Poster: "/dune-p.jpg", Backdrop: "/dune-b.jpg", IMDb: "tt1160419"})
	f.Add(tmdbfake.Title{Kind: "movie", ID: 99, Name: "Dune Documentary", Date: "2021-01-01", Overview: "Making of."})
	f.Add(tmdbfake.Title{Kind: "movie", ID: 329865, Name: "Arrival", Date: "2016-11-10", Overview: "Linguist.", Poster: "/a-p.jpg", Backdrop: "/a-b.jpg"})
	f.Add(tmdbfake.Title{Kind: "movie", ID: 949, Name: "Heat", Date: "1995-12-15", Overview: "Heist."})
	f.Add(tmdbfake.Title{Kind: "movie", ID: 8700, Name: "Heat", Date: "1986-04-04", Overview: "Other heist."})
	f.Add(tmdbfake.Title{Kind: "series", ID: 1622, Name: "House", Date: "2004-11-16", Overview: "Diagnostics.", Poster: "/h-p.jpg", Backdrop: "/h-b.jpg", IMDb: "tt0412142", TVDB: 73255,
		Seasons: []tmdbfake.Season{
			season(0, "2004-01-01"),
			season(1, "2004-11-16", "2004-11-23", "2004-11-30"),
			season(2, "2005-09-13", "2005-09-20", "2005-09-27"),
			season(3, "2006-09-05", "2006-09-12", "2006-09-19"),
			season(4, "2007-09-25", "2007-10-02", "2007-10-09"),
			season(5, "2026-10-01", "2026-11-18", "2026-12-25", ""),
		}})
	f.Add(tmdbfake.Title{Kind: "series", ID: 1972, Name: "Battlestar Galactica", Date: "1978-09-17", Overview: "Original.",
		Seasons: []tmdbfake.Season{season(1, "1978-09-17")}})
	f.Add(tmdbfake.Title{Kind: "series", ID: 71365, Name: "Battlestar Galactica", Date: "2004-12-08", Overview: "Reimagined.",
		Seasons: []tmdbfake.Season{season(1, "2004-12-08")}})
	return f
}

func season(n int, dates ...string) tmdbfake.Season {
	s := tmdbfake.Season{Number: n}
	for i, d := range dates {
		s.Episodes = append(s.Episodes, tmdbfake.Episode{Number: i + 1, Name: fmt.Sprintf("Episode %d", i+1), AirDate: d})
	}
	return s
}

// lay writes files (paths relative to root) with content that differs per file.
func lay(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, rel := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content of "+rel), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

// library lays out the movie and series folders before the service starts.
func library(t *testing.T, movies, series []string) func(env map[string]string) {
	return func(env map[string]string) {
		lay(t, env["MARQUEE_MOVIES_PATH"], movies...)
		lay(t, env["MARQUEE_SERIES_PATH"], series...)
	}
}

// startAdopt starts the service on a library laid out by prep and returns
// how its first-run adoption ended.
func startAdopt(t *testing.T, tm *tmdbfake.Fake, prep func(env map[string]string)) (*realSvc, error) {
	t.Helper()
	s := startRealWith(t, tm, "", prep)
	return s, s.adoptErr
}

// fingerprint covers every name, size, modification time and byte under
// root, so any rename, move, delete, write or touch changes it.
func fingerprint(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		line := fmt.Sprintf("%s|%v|%d|%d", rel, d.IsDir(), info.Size(), info.ModTime().UnixNano())
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			line += fmt.Sprintf("|%x", sha256.Sum256(b))
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func libraryFingerprint(t *testing.T, env map[string]string) string {
	t.Helper()
	return fingerprint(t, env["MARQUEE_MOVIES_PATH"]) + "\n--\n" + fingerprint(t, env["MARQUEE_SERIES_PATH"])
}

type problem struct{ path, reason, message string }

func (s *realSvc) problems() []problem {
	s.t.Helper()
	var out []problem
	for _, it := range s.mustCall("GET", "/api/health", "", 200)["libraryProblems"].([]any) {
		m := it.(map[string]any)
		out = append(out, problem{m["path"].(string), m["reason"].(string), m["message"].(string)})
	}
	return out
}

func (s *realSvc) problem(path string) (problem, bool) {
	for _, p := range s.problems() {
		if p.path == path {
			return p, true
		}
	}
	return problem{}, false
}

func (s *realSvc) episodes(id string) map[string]map[string]any {
	s.t.Helper()
	out := map[string]map[string]any{}
	for _, se := range s.mustCall("GET", "/api/titles/"+id, "", 200)["seasons"].([]any) {
		for _, e := range se.(map[string]any)["episodes"].([]any) {
			m := e.(map[string]any)
			out[m["id"].(string)] = m
		}
	}
	return out
}

func (s *realSvc) titleIDs() []string {
	s.t.Helper()
	var ids []string
	for _, it := range s.mustCall("GET", "/api/titles", "", 200)["titles"].([]any) {
		ids = append(ids, it.(map[string]any)["id"].(string))
	}
	sort.Strings(ids)
	return ids
}

func TestAdoptionRecordsAnOwnersLibraryAndChangesNothingOnDisk(t *testing.T) {
	var before string
	s, err := startAdopt(t, adoptTMDB(t), func(env map[string]string) {
		library(t,
			[]string{"Dune (2021)/Dune (2021) [Bluray-1080p].mkv", "Dune (2021)/Dune (2021) [Bluray-1080p].en.srt", "Dune (2021)/poster.jpg"},
			[]string{
				"House/Season 04/House - S04E01 - Name [Bluray-1080p].mkv",
				"House/Season 04/House - S04E01 - Name [Bluray-1080p].es.srt",
				"House/tvshow.nfo",
				".stfolder/.marker",
			})(env)
		lay(t, env["MARQUEE_MOVIES_PATH"], "@eaDir/Dune (2021)/SYNOFILE_THUMB_M.jpg", ".hidden/Dune (2021)/x.mkv")
		before = libraryFingerprint(t, env)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.titleIDs(); strings.Join(got, ",") != "movie-438631,series-1622" {
		t.Fatalf("titles after adoption: %v", got)
	}
	dune := s.mustCall("GET", "/api/titles/movie-438631", "", 200)
	if dune["title"] != "Dune" || dune["year"] != float64(2021) || dune["badge"].(map[string]any)["state"] != "downloaded" || dune["monitored"] != true {
		t.Errorf("movie: %v", dune)
	}
	if e := s.episodes("series-1622")["s04e01"]; e["state"] != "downloaded" {
		t.Errorf("the owned episode: %v", e)
	}
	if got := s.titleIDs(); len(got) != 2 {
		t.Errorf("hidden and NAS metadata folders are not titles: %v", got)
	}
	if p := s.problems(); len(p) != 0 {
		t.Errorf("an owner's tidy library has no problems, got %v (sidecars and posters are not problems)", p)
	}
	if after := libraryFingerprint(t, s.env); after != before {
		t.Errorf("the media root changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestAnAdoptedSeriesMonitorsOnlyMissingEpisodesOfSeasonsOnDiskAndFutureOnes(t *testing.T) {
	s, err := startAdopt(t, adoptTMDB(t), library(t, nil, []string{
		"House/Season 02/House - S02E01 - Episode 1 [Bluray-1080p].mkv",
		"House/Season 02/House - S02E02 - Episode 2 [Bluray-1080p].mkv",
		"House/Season 03/House - S03E01-E02 - Two in one [Bluray-1080p].mkv",
		"House/Season 03/House - S03E03 - Episode 3 [Bluray-1080p].mkv",
		"House/Season 04/House - S04E01 - Episode 1 [Bluray-1080p].mkv",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]struct {
		monitored bool
		state     string
	}{
		"s01e01": {false, "missing"}, "s01e02": {false, "missing"}, "s01e03": {false, "missing"},
		"s02e01": {true, "downloaded"}, "s02e02": {true, "downloaded"}, "s02e03": {true, "searching"},
		"s03e01": {true, "downloaded"}, "s03e02": {true, "downloaded"}, "s03e03": {true, "downloaded"},
		"s04e01": {true, "downloaded"}, "s04e02": {true, "searching"}, "s04e03": {true, "searching"},
		"s05e01": {false, "missing"}, "s05e02": {false, "missing"}, "s05e03": {true, "unaired"},
	} {
		got, ok := s.episodes("series-1622")[id]
		if !ok || got["monitored"] != want.monitored || got["state"] != want.state {
			t.Errorf("%s = %v, want monitored %v, state %s", id, got, want.monitored, want.state)
		}
	}
	title := s.mustCall("GET", "/api/titles/series-1622", "", 200)
	if title["monitored"] != true {
		t.Errorf("the adopted series is monitored: %v", title["monitored"])
	}
}

func TestFoldersWithNoOrSeveralTMDBMatchesAreListedForHealthWithTheReason(t *testing.T) {
	s, err := startAdopt(t, adoptTMDB(t), library(t,
		[]string{
			"Heat/Heat [Bluray-1080p].mkv",
			"Heat (1995)/Heat (1995) [Bluray-1080p].mkv",
			"Nowhere Film (2001)/Nowhere Film (2001) [Bluray-1080p].mkv",
		},
		[]string{
			"Battlestar Galactica/Season 01/Battlestar Galactica - S01E01 - Pilot [Bluray-1080p].mkv",
			"Battlestar Galactica (2004)/Season 01/Battlestar Galactica (2004) - S01E01 - Pilot [Bluray-1080p].mkv",
			"No Such Show/Season 01/No Such Show - S01E01 - Pilot [Bluray-1080p].mkv",
		}))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.titleIDs(); strings.Join(got, ",") != "movie-949,series-71365" {
		t.Errorf("only the unambiguous folders are adopted: %v", got)
	}
	for path, want := range map[string]string{
		"Movies/Heat":                 "ambiguous",
		"Movies/Nowhere Film (2001)":  "unmatched",
		"Series/Battlestar Galactica": "ambiguous",
		"Series/No Such Show":         "unmatched",
	} {
		p, ok := s.problem(path)
		if !ok || p.reason != want || p.message == "" {
			t.Errorf("%s = %+v (listed %v), want %s with a message", path, p, ok, want)
		}
	}
	if p, _ := s.problem("Movies/Heat"); !strings.Contains(p.message, "1995") || !strings.Contains(p.message, "1986") {
		t.Errorf("an ambiguous folder names what it matched: %q", p.message)
	}
	if n := len(s.problems()); n != 4 {
		t.Errorf("%d problems listed, want 4: %v", n, s.problems())
	}
}

func TestAFileWithoutAnEpisodeTokenIsListedAndOtherwiseIgnored(t *testing.T) {
	s, err := startAdopt(t, adoptTMDB(t), library(t, nil, []string{
		"House/Season 04/House - S04E01 - Name [Bluray-1080p].mkv",
		"House/Season 04/Making of House.mkv",
		"House/Season 04/notes.txt",
		"House/Season 04/House - S04E02 - Name [Bluray-1080p].es.srt",
		"House/Extras/Gag reel.mp4",
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := s.problems()
	if len(got) != 2 {
		t.Fatalf("problems: %v", got)
	}
	for _, path := range []string{"Series/House/Season 04/Making of House.mkv", "Series/House/Extras/Gag reel.mp4"} {
		if p, ok := s.problem(path); !ok || p.reason != "unparsed_file" || p.message == "" {
			t.Errorf("%s = %+v (listed %v)", path, p, ok)
		}
	}
	if e := s.episodes("series-1622"); e["s04e01"]["state"] != "downloaded" || e["s04e02"]["state"] != "searching" {
		t.Errorf("the series is adopted from the video files that parse, and a sidecar owns nothing: %v", e["s04e02"])
	}
}

func TestAnEpisodeTMDBDoesNotListIsReportedNotDropped(t *testing.T) {
	s, err := startAdopt(t, adoptTMDB(t), library(t, nil, []string{
		"House/Season 04/House - S04E01 - Name [Bluray-1080p].mkv",
		"House/Season 04/House - S04E09 - Not in TMDB [Bluray-1080p].mkv",
		"House/Season 00/House - S00E01 - A special [Bluray-1080p].mkv",
	}))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := s.problem("Series/House/Season 04/House - S04E09 - Not in TMDB [Bluray-1080p].mkv")
	if !ok || p.reason != "unparsed_file" || !strings.Contains(p.message, "S04E09") {
		t.Errorf("an episode TMDB lacks: %+v (listed %v)", p, ok)
	}
	if n := len(s.problems()); n != 1 {
		t.Errorf("specials are out of v1 and are not listed: %v", s.problems())
	}
}

func TestAMovieFolderWithNoVideoIsAdoptedAsWanted(t *testing.T) {
	s, err := startAdopt(t, adoptTMDB(t), library(t, []string{"Arrival (2016)/Arrival.nfo", "Arrival (2016)/Extras/Behind the scenes.mkv"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	v := s.mustCall("GET", "/api/titles/movie-329865", "", 200)
	if v["monitored"] != true || v["badge"].(map[string]any)["state"] != "searching" {
		t.Errorf("a folder with no video is a wanted movie: %v", v)
	}
}

func TestTwoFoldersForOneTitleAdoptTheFirstAndListTheSecond(t *testing.T) {
	s, err := startAdopt(t, adoptTMDB(t), library(t, []string{
		"Dune (2021)/Dune (2021) [Bluray-1080p].mkv",
		"dune (2021)/dune (2021) [WEBDL-1080p].mkv",
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.titleIDs(); len(got) != 1 {
		t.Errorf("titles: %v", got)
	}
	if p, ok := s.problem("Movies/dune (2021)"); !ok || p.reason != "ambiguous" || !strings.Contains(p.message, "Dune (2021)") {
		t.Errorf("the duplicate folder: %+v (listed %v)", p, ok)
	}
	if _, ok := s.problem("Movies/Dune (2021)"); ok {
		t.Error("the folder that was adopted is not a problem")
	}
}

func TestAdoptionRunsOnTheFirstStartOnly(t *testing.T) {
	tm := adoptTMDB(t)
	first, err := startAdopt(t, tm, library(t, []string{"Dune (2021)/Dune (2021) [Bluray-1080p].mkv"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	first.mustCall("DELETE", "/api/titles/movie-438631", "", 204)
	hits := tm.Hits("/3")
	first.stop()
	lay(t, first.env["MARQUEE_MOVIES_PATH"], "Arrival (2016)/Arrival (2016) [Bluray-1080p].mkv")

	second, err := startAdopt(t, tm, func(env map[string]string) {
		for k, v := range first.env {
			env[k] = v
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := second.titleIDs(); len(got) != 0 {
		t.Errorf("a restart adopted again: %v", got)
	}
	if n := tm.Hits("/3"); n != hits {
		t.Errorf("a restart asked TMDB %d more times", n-hits)
	}
}

func TestAdoptionStillRunsInDryRun(t *testing.T) {
	s, err := startAdopt(t, adoptTMDB(t), func(env map[string]string) {
		env["MARQUEE_DRY_RUN"] = "true"
		library(t, []string{"Dune (2021)/Dune (2021) [Bluray-1080p].mkv"}, nil)(env)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.titleIDs(); len(got) != 1 {
		t.Errorf("titles: %v", got)
	}
}

func TestAnAdoptionTMDBCannotFinishRecordsNothingWrongAndRetriesOnTheNextStart(t *testing.T) {
	tm := adoptTMDB(t)
	tm.Down("/3/search", 500)
	first, err := startAdopt(t, tm, library(t, []string{"Dune (2021)/Dune (2021) [Bluray-1080p].mkv"}, nil))
	if err == nil {
		t.Fatal("an adoption TMDB cannot serve must report an error")
	}
	if p := first.problems(); len(p) != 0 {
		t.Errorf("a TMDB outage is not an unmatched folder: %v", p)
	}
	if strings.Contains(first.logs.String(), tmdbfake.Key) {
		t.Errorf("logs leak the key: %s", first.logs.String())
	}
	first.stop()
	tm.Heal()

	second, err := startAdopt(t, tm, func(env map[string]string) {
		for k, v := range first.env {
			env[k] = v
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := second.titleIDs(); strings.Join(got, ",") != "movie-438631" {
		t.Errorf("the retry adopted %v", got)
	}
}

func TestAdoptionKeepsTheMonitoringOfATitleAddedByHandAndLearnsItsFiles(t *testing.T) {
	tm := adoptTMDB(t)
	tm.Down("/3/search", 500)
	first, err := startAdopt(t, tm, library(t, nil, []string{"House/Season 04/House - S04E01 - Name [Bluray-1080p].mkv"}))
	if err == nil {
		t.Fatal("the first adoption should not have finished")
	}
	tm.Heal()
	first.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1622}`, 201)
	first.stop()

	second, err := startAdopt(t, tm, func(env map[string]string) {
		for k, v := range first.env {
			env[k] = v
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	e := second.episodes("series-1622")
	if e["s04e01"]["state"] != "downloaded" {
		t.Errorf("the file on disk is learned: %v", e["s04e01"])
	}
	if e["s01e01"]["monitored"] != true || e["s04e02"]["monitored"] != true {
		t.Errorf("a series added by hand keeps monitoring every aired episode: %v %v", e["s01e01"], e["s04e02"])
	}
}
