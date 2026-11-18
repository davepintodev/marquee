package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreamsfake"
	"github.com/davepintodev/marquee/internal/tmdbfake"
)

func TestTheCensusCountsCachedCandidatesPerFolderAndKeepsSourceErrorsApart(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"movies/Dune (2021)", "movies/Unknown Film (1999)", "series/Dune Prophecy"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	tm := tmdbfake.New()
	defer tm.Close()
	tm.Add(tmdbfake.Title{Kind: "movie", ID: 438631, Name: "Dune", Date: "2021-09-15", IMDb: "tt1160419"})
	tm.Add(tmdbfake.Title{Kind: "series", ID: 1408, Name: "Dune Prophecy", Date: "2024-11-17", IMDb: "tt10466872", TVDB: 403000,
		Seasons: []tmdbfake.Season{{Number: 1, Episodes: []tmdbfake.Episode{{Number: 1, Name: "One", AirDate: "2024-11-17"}}}}})
	aio := aiostreamsfake.New()
	defer aio.Close()
	aio.Answer("movie", "tt1160419", aiostreamsfake.Fixture("movie-dune.json"))
	// the series has no answer set for S01E01: an empty result, not an error

	env := map[string]string{
		"MARQUEE_AIOSTREAMS_MANIFEST_URL": aio.ManifestURL(),
		"MARQUEE_TMDB_KEY":                tmdbfake.Key,
		"MARQUEE_MOVIES_PATH":             filepath.Join(root, "movies"),
		"MARQUEE_SERIES_PATH":             filepath.Join(root, "series"),
		"MARQUEE_CENSUS_PAUSE":            "0s",
	}
	var out, errs bytes.Buffer
	code := run(context.Background(), func(k string) string { return env[k] }, &out, &errs, runOptions{
		TMDBAPIBase: tm.APIBase(), Now: func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	got := out.String()
	for _, want := range []string{
		"| Dune (2021) | movie | 6 | 2 | 1 | 2 | 1 | 0 |",
		"| Unknown Film (1999) |  | 0 | 0 | 0 | 0 | 0 | 0 | no TMDB match for the folder name |",
		"| Dune Prophecy | S01E01 | 0 | 0 |",
		"searches measured: 2, with at least one cached candidate: 1",
		"folders or episodes that could not be searched: 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{aiostreamsfake.Password, aiostreamsfake.User, tmdbfake.Key, "PLAYBACK-TOKEN"} {
		if strings.Contains(got+errs.String(), leak) {
			t.Errorf("output leaks %q", leak)
		}
	}

	aio.Fail(aiostreamsfake.Step{Status: 429})
	out.Reset()
	if code := run(context.Background(), func(k string) string { return env[k] }, &out, &errs, runOptions{TMDBAPIBase: tm.APIBase()}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "source error") || !strings.Contains(out.String(), "searches that failed at the source (not counted as empty): 2") {
		t.Errorf("a 429 must be reported as a source error:\n%s", out.String())
	}
}

func TestTheCensusNamesWhatIsMissingAndDoesNotRunWithoutIt(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run(context.Background(), func(string) string { return "" }, &out, &errs, runOptions{}); code != 1 || !strings.Contains(errs.String(), "MARQUEE_AIOSTREAMS_MANIFEST_URL") {
		t.Errorf("%d %s", code, errs.String())
	}
}
