package chooser_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/chooser"
)

const mb = 1 << 20

var now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

type opt func(*aiostreams.Stream)

func rel(name string, size int64, opts ...opt) aiostreams.Stream {
	s := aiostreams.Stream{
		Filename: name, Size: size, Cached: true, Addon: "Torrentio",
		InfoHash:   fmt.Sprintf("%040x", len(name)*7+int(size%1000)),
		ParsedFile: &aiostreams.ParsedFile{Resolution: "1080p"},
	}
	for _, o := range opts {
		o(&s)
	}
	return s
}

func uncached(s *aiostreams.Stream) { s.Cached = false }
func res(r string) opt              { return func(s *aiostreams.Stream) { s.ParsedFile.Resolution = r } }
func parsed(season []int, ep []int) opt {
	return func(s *aiostreams.Stream) { s.ParsedFile.Seasons, s.ParsedFile.Episodes = season, ep }
}
func noParse(s *aiostreams.Stream) { s.ParsedFile = nil }

func names(cs []chooser.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Stream.Filename)
	}
	return out
}

func TestEachDropHasItsReasonInWords(t *testing.T) {
	movie := chooser.Context{Kind: "movie", Now: now}
	episode := chooser.Context{Kind: "series", Season: 1, Episode: 2, Now: now}
	blocked := rel("Show.S01E02.blocked.mkv", 800*mb, parsed([]int{1}, []int{2}))
	cases := []struct {
		name   string
		ctx    chooser.Context
		stream aiostreams.Stream
		reason chooser.Reason
		words  string
	}{
		{"uncached", movie, rel("Film.mkv", 2000*mb, uncached), chooser.NotCached, "not cached"},
		{"exe", movie, rel("Film.2021.exe", 2000*mb), chooser.NotVideo, "a .exe file, not a video"},
		{"7z in capitals", movie, rel("Film.7Z", 2000*mb), chooser.NotVideo, "a .7z file, not a video"},
		{"zip", movie, rel("Film.zip", 2000*mb), chooser.NotVideo, ".zip"},
		{"no extension", movie, rel("Film", 2000*mb), chooser.NotVideo, "no file extension"},
		{"no file name", movie, rel("", 2000*mb), chooser.NotVideo, "no file name"},
		{"sample", movie, rel("Film.sample.mkv", 30*mb), chooser.TooSmall, "30 MB, too small to be a movie"},
		{"movie just under the floor", movie, rel("Film.mkv", 200*mb-1), chooser.TooSmall, "at least 200 MB"},
		{"episode just under the floor", episode, rel("S01E02.mkv", 50*mb-1, parsed([]int{1}, []int{2})), chooser.TooSmall, "too small to be an episode"},
		{"size unknown", movie, rel("Film.mkv", 0), chooser.TooSmall, "size is unknown"},
		{"wrong episode", episode, rel("S01E03.mkv", 800*mb, parsed([]int{1}, []int{3})), chooser.WrongEpisode, "episode 3, not episode 2"},
		{"wrong season", episode, rel("S02E02.mkv", 800*mb, parsed([]int{2}, []int{2})), chooser.WrongEpisode, "season 2, not season 1"},
		{"no episode number", episode, rel("Pack.mkv", 800*mb, parsed([]int{1}, nil)), chooser.WrongEpisode, "does not say which episode"},
		{"no season number", episode, rel("E02.mkv", 800*mb, parsed(nil, []int{2})), chooser.WrongEpisode, "does not say which season"},
		{"not parsed", episode, rel("x.mkv", 800*mb, noParse), chooser.WrongEpisode, "could not read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chooser.Choose([]aiostreams.Stream{c.stream}, c.ctx)
			if len(got.Candidates) != 0 || len(got.Dropped) != 1 {
				t.Fatalf("want one drop and no candidate: %+v", got)
			}
			d := got.Dropped[0]
			if d.Reason != c.reason || !strings.Contains(d.Message, c.words) {
				t.Errorf("got %s %q, want %s containing %q", d.Reason, d.Message, c.reason, c.words)
			}
		})
	}

	ctx := episode
	ctx.Blocklist = map[string]time.Time{chooser.Identity(blocked): now.Add(-24 * time.Hour)}
	got := chooser.Choose([]aiostreams.Stream{blocked}, ctx)
	if len(got.Dropped) != 1 || got.Dropped[0].Reason != chooser.Blocklisted || !strings.Contains(got.Dropped[0].Message, "failed before") {
		t.Errorf("blocklisted: %+v", got)
	}
}

func TestAFileJustOverTheFloorAndAMultiEpisodeFileAreCandidates(t *testing.T) {
	episode := chooser.Context{Kind: "series", Season: 1, Episode: 2, Now: now}
	got := chooser.Choose([]aiostreams.Stream{
		rel("S01E02.mkv", 50*mb, parsed([]int{1}, []int{2})),
		rel("S01E01-E02.mkv", 900*mb, parsed([]int{1}, []int{1, 2})),
	}, episode)
	if len(got.Candidates) != 2 || len(got.Dropped) != 0 {
		t.Errorf("%+v", got)
	}
	for _, ext := range []string{"mkv", "mp4", "avi", "m4v", "mov", "wmv", "ts", "m2ts", "webm", "MKV"} {
		got := chooser.Choose([]aiostreams.Stream{rel("Film."+ext, 2000*mb)}, chooser.Context{Kind: "movie", Now: now})
		if len(got.Candidates) != 1 {
			t.Errorf(".%s should be a video: %+v", ext, got)
		}
	}
}

func TestTheBlocklistExpiresAfterThirtyDays(t *testing.T) {
	r := rel("Film.mkv", 2000*mb)
	blockedAt := func(ago time.Duration) chooser.Context {
		return chooser.Context{Kind: "movie", Now: now, Blocklist: map[string]time.Time{chooser.Identity(r): now.Add(-ago)}}
	}
	day := 24 * time.Hour
	for _, c := range []struct {
		ago     time.Duration
		blocked bool
	}{{29 * day, true}, {30*day - time.Second, true}, {30 * day, false}, {31 * day, false}} {
		got := chooser.Choose([]aiostreams.Stream{r}, blockedAt(c.ago))
		if (len(got.Dropped) == 1) != c.blocked {
			t.Errorf("blocked %s ago: want blocked=%v, got %+v", c.ago, c.blocked, got)
		}
	}
}

func TestTheIdentityIgnoresTheURLAndTheAddon(t *testing.T) {
	a := rel("Film.mkv", 2000*mb)
	b := a
	b.URL, b.Addon = "https://example.test/other-token", "Comet"
	if chooser.Identity(a) != chooser.Identity(b) {
		t.Error("the same file from another addon or link must have one identity")
	}
	c := a
	c.Size++
	if chooser.Identity(a) == chooser.Identity(c) {
		t.Error("another size is another release")
	}
	if strings.Contains(chooser.Identity(a), "http") {
		t.Error("identity must not hold a URL")
	}
}

func TestTheSameFileFromTwoAddonsIsListedOnceInAIOStreamsOrder(t *testing.T) {
	a := rel("Film.mkv", 2000*mb)
	b := a
	b.Addon = "Comet"
	got := chooser.Choose([]aiostreams.Stream{a, b}, chooser.Context{Kind: "movie", Now: now})
	if len(got.Candidates) != 1 || got.Candidates[0].Stream.Addon != "Torrentio" {
		t.Errorf("%+v", got)
	}
}

func TestAIOStreamsOrderIsKeptAndThePreferredQualityComesFirstWhenThereIsOne(t *testing.T) {
	list := []aiostreams.Stream{
		rel("a.mkv", 2000*mb, res("1080p")),
		rel("bb.mkv", 3000*mb, res("2160p"), uncached),
		rel("ccc.mkv", 4000*mb, res("720p")),
		rel("dddd.mkv", 5000*mb, res("2160p")),
		rel("eeeee.mkv", 6000*mb, res("2160p")),
	}
	ctx := chooser.Context{Kind: "movie", Now: now}
	if got := names(chooser.Choose(list, ctx).Candidates); strings.Join(got, ",") != "a.mkv,ccc.mkv,dddd.mkv,eeeee.mkv" {
		t.Errorf("no preference keeps the order: %v", got)
	}
	ctx.PreferredQuality = "2160p"
	if got := names(chooser.Choose(list, ctx).Candidates); strings.Join(got, ",") != "dddd.mkv,eeeee.mkv,a.mkv,ccc.mkv" {
		t.Errorf("2160p first, each group in AIOStreams' order: %v", got)
	}
	// the only 2160p is not cached, so the preference finds nothing and the best other one stays first
	only := []aiostreams.Stream{list[0], list[1], list[2]}
	if got := names(chooser.Choose(only, ctx).Candidates); strings.Join(got, ",") != "a.mkv,ccc.mkv" {
		t.Errorf("a missing preferred quality must not empty the list: %v", got)
	}
}

func TestNothingToChooseFromGivesEmptyListsNotNil(t *testing.T) {
	got := chooser.Choose(nil, chooser.Context{Kind: "movie", Now: now})
	if got.Candidates == nil || got.Dropped == nil {
		t.Errorf("want empty, non-nil lists: %+v", got)
	}
}
