// Command census measures, against a real AIOStreams instance, how many
// cached releases exist for the titles in the library folders. It is not
// part of the service and nothing runs it by itself: run it by hand, with
// the same MARQUEE_* settings the service reads, to decide whether the
// cached-only rule holds for a library.
//
//	MARQUEE_AIOSTREAMS_MANIFEST_URL, MARQUEE_TMDB_KEY,
//	MARQUEE_MOVIES_PATH, MARQUEE_SERIES_PATH
//
// It searches one movie per folder and, for a series, the first episode
// of its first and of its latest season, one search every
// MARQUEE_CENSUS_PAUSE (a Go duration, default 5s, the service's own
// search rate). It prints a Markdown table on stdout and never prints a
// credential.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/metadata"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Getenv, os.Stdout, os.Stderr, runOptions{}))
}

type runOptions struct {
	// TMDBAPIBase replaces TMDB's host in tests.
	TMDBAPIBase string
	Now         func() time.Time
}

type source interface {
	Search(ctx context.Context, typ, id string) ([]aiostreams.Stream, error)
}

type tmdb interface {
	Search(ctx context.Context, q string) ([]metadata.Summary, error)
	Details(ctx context.Context, kind string, id int) (metadata.Details, error)
}

func run(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer, o runOptions) int {
	fail := func(format string, a ...any) int {
		_, _ = fmt.Fprintf(stderr, "census: "+format+"\n", a...)
		return 1
	}
	var missing []string
	for _, k := range []string{"MARQUEE_AIOSTREAMS_MANIFEST_URL", "MARQUEE_TMDB_KEY", "MARQUEE_MOVIES_PATH", "MARQUEE_SERIES_PATH"} {
		if getenv(k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fail("missing setting(s): %s", strings.Join(missing, ", "))
	}
	pause := 5 * time.Second
	if v := getenv("MARQUEE_CENSUS_PAUSE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fail("MARQUEE_CENSUS_PAUSE must be a duration such as 5s")
		}
		pause = d
	}
	src, err := aiostreams.New(aiostreams.Options{Manifest: config.Secret(getenv("MARQUEE_AIOSTREAMS_MANIFEST_URL"))})
	if err != nil {
		return fail("MARQUEE_AIOSTREAMS_MANIFEST_URL %v", err)
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	meta := metadata.New(metadata.Options{APIBase: o.TMDBAPIBase, Key: config.Secret(getenv("MARQUEE_TMDB_KEY")), Timeout: 20 * time.Second})

	movies, err := folders(getenv("MARQUEE_MOVIES_PATH"))
	if err != nil {
		return fail("MARQUEE_MOVIES_PATH: %v", err)
	}
	series, err := folders(getenv("MARQUEE_SERIES_PATH"))
	if err != nil {
		return fail("MARQUEE_SERIES_PATH: %v", err)
	}

	c := &census{src: src, tmdb: meta, pause: pause, now: now}
	for _, f := range movies {
		c.movie(ctx, f)
	}
	for _, f := range series {
		c.series(ctx, f)
	}
	if ctx.Err() != nil {
		return fail("interrupted")
	}
	c.print(stdout)
	return 0
}

func folders(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errors.New("cannot read the folder")
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

var yearRe = regexp.MustCompile(`^(.*\S)\s*\((\d{4})\)\s*$`)

type row struct {
	folder, search string
	found          int
	candidates     int
	drops          map[chooser.Reason]int
	problem        string
}

type census struct {
	src      source
	tmdb     tmdb
	pause    time.Duration
	now      func() time.Time
	rows     []row
	searched bool
}

func (c *census) wait(ctx context.Context) {
	if !c.searched {
		c.searched = true
		return
	}
	select {
	case <-time.After(c.pause):
	case <-ctx.Done():
	}
}

// match is the first TMDB result of the kind, preferring the folder's year.
func (c *census) match(ctx context.Context, name, kind string, year int) (metadata.Summary, bool, error) {
	found, err := c.tmdb.Search(ctx, name)
	if err != nil {
		return metadata.Summary{}, false, err
	}
	var first *metadata.Summary
	for i, s := range found {
		if s.Kind != kind {
			continue
		}
		if year != 0 && s.Year == year {
			return s, true, nil
		}
		if first == nil {
			first = &found[i]
		}
	}
	if first == nil {
		return metadata.Summary{}, false, nil
	}
	return *first, true, nil
}

func (c *census) movie(ctx context.Context, folder string) {
	name, year := folder, 0
	if m := yearRe.FindStringSubmatch(folder); m != nil {
		name = m[1]
		_, _ = fmt.Sscan(m[2], &year)
	}
	s, ok, err := c.match(ctx, name, "movie", year)
	if !ok || err != nil {
		c.rows = append(c.rows, row{folder: folder, problem: matchProblem(err)})
		return
	}
	d, err := c.tmdb.Details(ctx, "movie", s.TMDBID)
	if err != nil {
		c.rows = append(c.rows, row{folder: folder, problem: "TMDB details failed"})
		return
	}
	c.rows = append(c.rows, c.measure(ctx, folder, "movie", aiostreams.MovieID(d.IMDbID, d.TMDBID), chooser.Context{Kind: "movie", Profile: everything}))
}

// everything is a profile that refuses nothing: the census counts what
// AIOStreams offers, not what an owner's profile would keep.
var everything = config.Profile{Qualities: config.QualityTiers()}

func (c *census) series(ctx context.Context, folder string) {
	s, ok, err := c.match(ctx, folder, "series", 0)
	if !ok || err != nil {
		c.rows = append(c.rows, row{folder: folder, problem: matchProblem(err)})
		return
	}
	d, err := c.tmdb.Details(ctx, "series", s.TMDBID)
	if err != nil {
		c.rows = append(c.rows, row{folder: folder, problem: "TMDB details failed"})
		return
	}
	var seasons []int
	for _, se := range d.Seasons {
		if len(se.Episodes) > 0 && se.Episodes[0].AirDate != "" && se.Episodes[0].AirDate <= c.now().Format(time.DateOnly) {
			seasons = append(seasons, se.Number)
		}
	}
	if len(seasons) == 0 {
		c.rows = append(c.rows, row{folder: folder, problem: "no aired season on TMDB"})
		return
	}
	picks := []int{seasons[0]}
	if last := seasons[len(seasons)-1]; last != picks[0] {
		picks = append(picks, last)
	}
	for _, n := range picks {
		id, ok := aiostreams.EpisodeID(d.TVDBID, d.IMDbID, n, 1)
		if !ok {
			c.rows = append(c.rows, row{folder: folder, search: fmt.Sprintf("S%02dE01", n), problem: "TMDB has no TVDB or IMDb id"})
			continue
		}
		r := c.measure(ctx, folder, "series", id, chooser.Context{Kind: "series", Season: n, Episode: 1, Profile: everything})
		r.search = fmt.Sprintf("S%02dE01", n)
		c.rows = append(c.rows, r)
	}
}

func matchProblem(err error) string {
	if err != nil {
		return "TMDB search failed"
	}
	return "no TMDB match for the folder name"
}

func (c *census) measure(ctx context.Context, folder, typ, id string, cx chooser.Context) row {
	c.wait(ctx)
	r := row{folder: folder, search: "movie"}
	streams, err := c.src.Search(ctx, typ, id)
	if err != nil {
		r.problem = "source error: " + err.Error()
		return r
	}
	cx.Now = c.now()
	res := chooser.Choose(streams, cx)
	r.found, r.candidates, r.drops = len(streams), len(res.Candidates), map[chooser.Reason]int{}
	for _, d := range res.Dropped {
		r.drops[d.Reason]++
	}
	return r
}

func (c *census) print(w io.Writer) {
	var withCached, measured, sourceErrors, unmatched int
	for _, r := range c.rows {
		switch {
		case strings.HasPrefix(r.problem, "source error"):
			sourceErrors++
		case r.problem != "":
			unmatched++
		default:
			measured++
			if r.candidates > 0 {
				withCached++
			}
		}
	}
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }
	p("# Cached releases per library title")
	p("")
	p("Measured %s.", c.now().UTC().Format(time.DateOnly))
	p("")
	p("- searches measured: %d, with at least one cached candidate: %d", measured, withCached)
	p("- searches that failed at the source (not counted as empty): %d", sourceErrors)
	p("- folders or episodes that could not be searched: %d", unmatched)
	p("")
	p("| Folder | Search | Results | Candidates | Not cached | Not video | Too small | Wrong episode | Note |")
	p("|---|---|---|---|---|---|---|---|---|")
	for _, r := range c.rows {
		p("| %s | %s | %d | %d | %d | %d | %d | %d | %s |", cell(r.folder), r.search, r.found, r.candidates,
			r.drops[chooser.NotCached], r.drops[chooser.NotVideo], r.drops[chooser.TooSmall], r.drops[chooser.WrongEpisode], r.problem)
	}
}

func cell(s string) string { return strings.ReplaceAll(s, "|", "/") }
