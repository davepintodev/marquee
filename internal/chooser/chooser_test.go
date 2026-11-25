package chooser_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/config"
)

const mb = 1 << 20

var now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

var anyProfile = config.Profile{Qualities: config.QualityTiers()}

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
	movie := chooser.Context{Kind: "movie", Now: now, Profile: anyProfile}
	episode := chooser.Context{Kind: "series", Season: 1, Episode: 2, Now: now, Profile: anyProfile}
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
	episode := chooser.Context{Kind: "series", Season: 1, Episode: 2, Now: now, Profile: anyProfile}
	got := chooser.Choose([]aiostreams.Stream{
		rel("S01E02.mkv", 50*mb, parsed([]int{1}, []int{2})),
		rel("S01E01-E02.mkv", 900*mb, parsed([]int{1}, []int{1, 2})),
	}, episode)
	if len(got.Candidates) != 2 || len(got.Dropped) != 0 {
		t.Errorf("%+v", got)
	}
	for _, ext := range []string{"mkv", "mp4", "avi", "m4v", "mov", "wmv", "ts", "m2ts", "webm", "MKV"} {
		got := chooser.Choose([]aiostreams.Stream{rel("Film."+ext, 2000*mb)}, chooser.Context{Kind: "movie", Now: now, Profile: anyProfile})
		if len(got.Candidates) != 1 {
			t.Errorf(".%s should be a video: %+v", ext, got)
		}
	}
}

func TestTheBlocklistExpiresAfterThirtyDays(t *testing.T) {
	r := rel("Film.mkv", 2000*mb)
	blockedAt := func(ago time.Duration) chooser.Context {
		return chooser.Context{Kind: "movie", Now: now, Profile: anyProfile, Blocklist: map[string]time.Time{chooser.Identity(r): now.Add(-ago)}}
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
	got := chooser.Choose([]aiostreams.Stream{a, b}, chooser.Context{Kind: "movie", Now: now, Profile: anyProfile})
	if len(got.Candidates) != 1 || got.Candidates[0].Stream.Addon != "Torrentio" {
		t.Errorf("%+v", got)
	}
}

func profile(qualities []string, reject, prefer []string) config.Profile {
	return config.Profile{Qualities: qualities, Reject: reject, Prefer: prefer}
}

func pf(name string, size int64, resolution, quality string, tags ...string) aiostreams.Stream {
	s := rel(name, size, res(resolution))
	s.ParsedFile.Quality = quality
	s.ParsedFile.AudioTags = tags
	return s
}

func TestAReleaseTheProfileRefusesIsDroppedInWords(t *testing.T) {
	p := config.DefaultProfile()
	p.SizeLimits = config.SizeLimits{MinMB: 1000, MaxMB: 5000}
	ctx := chooser.Context{Kind: "movie", Now: now, Profile: p}
	cases := []struct {
		name   string
		stream aiostreams.Stream
		words  []string
	}{
		{"quality not in the profile", pf("Film.2160p.REMUX.mkv", 3000*mb, "2160p", "BluRay REMUX"), []string{"Remux-2160p", "not allowed by this profile"}},
		{"truehd", pf("Film.1080p.WEB-DL.TrueHD.mkv", 3000*mb, "1080p", "WEB-DL"), []string{"rejected by profile", "TrueHD audio"}},
		{"dts-hd", pf("Film.1080p.WEB-DL.DTS-HD.MA.5.1.mkv", 3000*mb, "1080p", "WEB-DL"), []string{"rejected by profile", "DTS-HD audio"}},
		{"dts-x", pf("Film.1080p.WEB-DL.DTS-X.mkv", 3000*mb, "1080p", "WEB-DL"), []string{"rejected by profile", "DTS:X audio"}},
		{"dv profile 7", pf("Film.2160p.WEB-DL.DV.P7.mkv", 3000*mb, "2160p", "WEB-DL"), []string{"rejected by profile", "DV Profile 7"}},
		{"two tags are both named", pf("Film.2160p.WEB-DL.DV.P7.TrueHD.mkv", 3000*mb, "2160p", "WEB-DL"), []string{"TrueHD audio, DV Profile 7"}},
		{"under the minimum", pf("Film.1080p.WEB-DL.mkv", 600*mb, "1080p", "WEB-DL"), []string{"600 MB", "minimum", "1000 MB"}},
		{"over the maximum", pf("Film.1080p.WEB-DL.mkv", 6000*mb, "1080p", "WEB-DL"), []string{"6000 MB", "maximum", "5000 MB"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chooser.Choose([]aiostreams.Stream{c.stream}, ctx)
			if len(got.Candidates) != 0 || len(got.Dropped) != 1 {
				t.Fatalf("want one drop and no candidate: %+v", got)
			}
			d := got.Dropped[0]
			if d.Reason != chooser.ProfileRejected || string(d.Reason) != "profile_rejected" {
				t.Fatalf("reason %q", d.Reason)
			}
			for _, w := range c.words {
				if !strings.Contains(d.Message, w) {
					t.Errorf("message %q lacks %q", d.Message, w)
				}
			}
		})
	}
}

func TestTheProfileChecksComeAfterTheSafetyChecksAndBeforeTheBlocklist(t *testing.T) {
	ctx := chooser.Context{Kind: "movie", Now: now, Profile: config.DefaultProfile()}
	trueHD := func(name string, size int64, opts ...opt) aiostreams.Stream {
		s := pf(name, size, "1080p", "WEB-DL", "TrueHD")
		for _, o := range opts {
			o(&s)
		}
		return s
	}
	cases := []struct {
		name   string
		stream aiostreams.Stream
		reason chooser.Reason
	}{
		{"uncached", trueHD("a.mkv", 3000*mb, uncached), chooser.NotCached},
		{"not a video", trueHD("a.exe", 3000*mb), chooser.NotVideo},
		{"too small", trueHD("a.mkv", 30*mb), chooser.TooSmall},
		{"profile before blocklist", trueHD("b.mkv", 3000*mb), chooser.ProfileRejected},
	}
	blocked := trueHD("b.mkv", 3000*mb)
	ctx.Blocklist = map[string]time.Time{chooser.Identity(blocked): now.Add(-time.Hour)}
	for _, c := range cases {
		got := chooser.Choose([]aiostreams.Stream{c.stream}, ctx)
		if len(got.Dropped) != 1 || got.Dropped[0].Reason != c.reason {
			t.Errorf("%s: %+v", c.name, got.Dropped)
		}
	}
}

func TestAnEpisodeOfAnotherNumberIsWrongBeforeItIsProfileRejected(t *testing.T) {
	ctx := chooser.Context{Kind: "series", Season: 1, Episode: 2, Now: now, Profile: config.DefaultProfile()}
	s := pf("S01E03.TrueHD.mkv", 800*mb, "1080p", "WEB-DL", "TrueHD")
	s.ParsedFile.Seasons, s.ParsedFile.Episodes = []int{1}, []int{3}
	got := chooser.Choose([]aiostreams.Stream{s}, ctx)
	if len(got.Dropped) != 1 || got.Dropped[0].Reason != chooser.WrongEpisode {
		t.Errorf("%+v", got.Dropped)
	}
}

func TestEarlierQualitiesWinThenPreferBoostsThenAIOStreamsOrder(t *testing.T) {
	p := profile(
		[]string{"WEBDL-2160p", "WEBDL-1080p", "Bluray-1080p", "WEBDL-720p"},
		nil,
		[]string{config.TagDV, config.TagHDR10Plus, config.TagDDP},
	)
	list := []aiostreams.Stream{
		pf("a720.mkv", 2000*mb, "720p", "WEB-DL", "DD+"),
		pf("b1080-bluray.mkv", 3000*mb, "1080p", "BluRay", "DD+"),
		pf("c1080-plain.mkv", 4000*mb, "1080p", "WEB-DL"),
		pf("d1080-ddp.mkv", 5000*mb, "1080p", "WEB-DL", "DD+"),
		pf("e2160.mkv", 6000*mb, "2160p", "WEB-DL"),
		pf("f1080-dv.mkv", 7000*mb, "1080p", "WEB-DL"),
		pf("g1080-plain.mkv", 8000*mb, "1080p", "WEB-DL"),
	}
	list[5].ParsedFile.VisualTags = []string{"DV"}
	got := names(chooser.Choose(list, chooser.Context{Kind: "movie", Now: now, Profile: p}).Candidates)
	want := "e2160.mkv,f1080-dv.mkv,d1080-ddp.mkv,c1080-plain.mkv,g1080-plain.mkv,b1080-bluray.mkv,a720.mkv"
	if strings.Join(got, ",") != want {
		t.Errorf("got  %v\nwant %s", got, want)
	}
}

func TestThePreferBoostIsTheSumOfLenMinusIndexForEveryMatchedTag(t *testing.T) {
	// dv scores 3, hdr10+ 2, dd+ 1: dv alone and hdr10+ with dd+ both make 3, so AIOStreams' order settles it
	p := profile([]string{"WEBDL-1080p"}, nil, []string{config.TagDV, config.TagHDR10Plus, config.TagDDP})
	dv := pf("dv.mkv", 2000*mb, "1080p", "WEB-DL")
	dv.ParsedFile.VisualTags = []string{"DV"}
	both := pf("both.mkv", 3000*mb, "1080p", "WEB-DL", "DD+")
	both.ParsedFile.VisualTags = []string{"HDR10+"}
	got := names(chooser.Choose([]aiostreams.Stream{both, dv}, chooser.Context{Kind: "movie", Now: now, Profile: p}).Candidates)
	if strings.Join(got, ",") != "both.mkv,dv.mkv" {
		t.Errorf("a tie keeps AIOStreams' order: %v", got)
	}
	got = names(chooser.Choose([]aiostreams.Stream{dv, both}, chooser.Context{Kind: "movie", Now: now, Profile: p}).Candidates)
	if strings.Join(got, ",") != "dv.mkv,both.mkv" {
		t.Errorf("a tie keeps AIOStreams' order: %v", got)
	}
	three := pf("three.mkv", 4000*mb, "1080p", "WEB-DL", "DD+")
	three.ParsedFile.VisualTags = []string{"DV", "HDR10+"}
	got = names(chooser.Choose([]aiostreams.Stream{both, dv, three}, chooser.Context{Kind: "movie", Now: now, Profile: p}).Candidates)
	if got[0] != "three.mkv" {
		t.Errorf("all three tags (6) win: %v", got)
	}
}

func TestAReleaseWithoutParsedFieldsRanksAsUnknownQuality(t *testing.T) {
	p := profile([]string{"WEBDL-1080p", "Unknown"}, nil, nil)
	list := []aiostreams.Stream{rel("x.mkv", 2000*mb, noParse), pf("y.mkv", 3000*mb, "1080p", "WEB-DL")}
	got := chooser.Choose(list, chooser.Context{Kind: "movie", Now: now, Profile: p})
	if strings.Join(names(got.Candidates), ",") != "y.mkv,x.mkv" {
		t.Errorf("%v", names(got.Candidates))
	}
}

func TestNothingToChooseFromGivesEmptyListsNotNil(t *testing.T) {
	got := chooser.Choose(nil, chooser.Context{Kind: "movie", Now: now, Profile: anyProfile})
	if got.Candidates == nil || got.Dropped == nil {
		t.Errorf("want empty, non-nil lists: %+v", got)
	}
}
