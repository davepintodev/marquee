package chooser_test

import (
	"slices"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/config"
)

func folder(name string) opt    { return func(s *aiostreams.Stream) { s.FolderName = name } }
func pack(s *aiostreams.Stream) { s.ParsedFile.SeasonPack = true }
func web(s *aiostreams.Stream)  { s.ParsedFile.Quality = "WEB-DL" }
func hash(h string) opt         { return func(s *aiostreams.Stream) { s.InfoHash = h } }

func TestIsSeasonPack(t *testing.T) {
	cases := []struct {
		name string
		s    aiostreams.Stream
		want bool
	}{
		{"season parsed, no episode", rel("Show.S01.1080p.mkv", 9000*mb, parsed([]int{1}, []int{})), true},
		{"flagged pack listing one episode", rel("Show.S01E03.1080p.mkv", 900*mb, parsed([]int{1}, []int{3}), pack), true},
		{"single episode", rel("Show.S01E03.1080p.mkv", 900*mb, parsed([]int{1}, []int{3})), false},
		{"another season", rel("Show.S02.1080p.mkv", 9000*mb, parsed([]int{2}, []int{})), false},
		{"several seasons", rel("Show.S01-S02.1080p.mkv", 9000*mb, parsed([]int{1, 2}, []int{})), false},
		{"unparsed season folder", rel("x.mkv", 9000*mb, noParse, folder("Show Season 1 1080p")), true},
		{"unparsed S01 folder", rel("x.mkv", 9000*mb, noParse, folder("Show.S01.1080p.WEB-DL")), true},
		{"unparsed episode folder", rel("x.mkv", 900*mb, noParse, folder("Show.S01E02.1080p")), false},
		{"no info hash", rel("Show.S01.1080p.mkv", 9000*mb, parsed([]int{1}, []int{}), hash("")), false},
	}
	for _, c := range cases {
		if got := chooser.IsSeasonPack(c.s, 1); got != c.want {
			t.Errorf("%s: IsSeasonPack = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPacksRankCachedFirstAndHonourProfileAndBlocklist(t *testing.T) {
	season := func(s *aiostreams.Stream) { parsed([]int{1}, []int{})(s); web(s) }
	p1080 := rel("Show.S01.1080p.mkv", 9000*mb, season, hash("1111111111111111111111111111111111111111"))
	p2160Uncached := rel("Show.S01.2160p.mkv", 9000*mb, season, res("2160p"), uncached, hash("2222222222222222222222222222222222222222"))
	p720 := rel("Show.S01.720p.mkv", 9000*mb, season, res("720p"), hash("3333333333333333333333333333333333333333"))
	blocked := rel("Show.S01.blocked.mkv", 9000*mb, season, hash("4444444444444444444444444444444444444444"))
	single := rel("Show.S01E01.1080p.mkv", 900*mb, parsed([]int{1}, []int{1}))
	c := chooser.Context{
		Kind: "series", Season: 1, Episode: 1, Now: now, AllowUncached: true,
		Profile:   config.Profile{Qualities: []string{"WEBDL-2160p", "WEBDL-1080p", "WEBDL-720p"}, SizeLimits: config.SizeLimits{MaxMB: 2000}},
		Blocklist: map[string]time.Time{chooser.PackID(blocked.InfoHash): now},
	}
	got := names(chooser.Packs([]aiostreams.Stream{p720, p2160Uncached, single, blocked, p1080}, c))
	if want := []string{"Show.S01.1080p.mkv", "Show.S01.720p.mkv", "Show.S01.2160p.mkv"}; !slices.Equal(got, want) {
		t.Fatalf("packs = %v, want %v (the pack size is not held to the per-file limit)", got, want)
	}
	c.AllowUncached = false
	if got := names(chooser.Packs([]aiostreams.Stream{p2160Uncached}, c)); len(got) != 0 {
		t.Fatalf("an uncached pack without TorBox: %v", got)
	}
}

func TestMatchPackTakesEveryWantedEpisodeAndNothingElse(t *testing.T) {
	files := []chooser.PackFile{
		{ID: 0, Name: "Show.S01/Show.S01E01.1080p.mkv", Size: 900 * mb},
		{ID: 1, Name: "Show.S01/Show.S01E02.1080p.mkv", Size: 900 * mb},
		{ID: 2, Name: "Show.S01/Show.S01E03E04.1080p.mkv", Size: 1800 * mb},
		{ID: 3, Name: "Show.S01/Show.S01E05.1080p.mkv", Size: 900 * mb},
		{ID: 4, Name: "Show.S01/Show.S01E06.1080p.mkv", Size: 10 * mb},
		{ID: 5, Name: "Show.S01/Show.S01E07.1080p.nfo", Size: 900 * mb},
		{ID: 6, Name: "Show.S01/Show.S02E08.1080p.mkv", Size: 900 * mb},
		{ID: 7, Name: "Show.S01/Show.S01E08.1080p.mkv", Size: 900 * mb},
		{ID: 8, Name: "Show.S01/Show.S01E09.1080p.mkv", Size: 3000 * mb},
		{ID: 9, Name: "Show.S01/Show.S01E08.REPACK.1080p.mkv", Size: 901 * mb},
	}
	const h = "abababababababababababababababababababab"
	c := chooser.Context{Kind: "series", Season: 1, Now: now, Profile: config.Profile{SizeLimits: config.SizeLimits{MaxMB: 2000}},
		Blocklist: map[string]time.Time{chooser.IdentityOf(h, "Show.S01E05.1080p.mkv", 900*mb): now}}
	got := chooser.MatchPack(files, h, 1, []int{2, 3, 5, 6, 7, 8, 9}, []int{1, 4}, c)
	var ids []int
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	// 1 and 4 are owned (E03E04 repeats 4), 5 is blocked, 6 is a sample,
	// 7 not a video, S02 another season, 9 over the profile's size, the
	// second E08 a repeat.
	if want := []int{1, 7}; !slices.Equal(ids, want) {
		t.Fatalf("matched files %v, want %v", ids, want)
	}
}
