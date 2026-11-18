// Translated from Sonarr (https://github.com/Sonarr/Sonarr), licensed under
// the GNU General Public License v3.0, as is marquee: the season and episode
// numbers of a file name (Parser.ParseTitle and ParseMatchCollection in
// NzbDrone.Core/Parser/Parser.cs). Go's regexp has no lookaround, no
// backreferences and no repeated captures, so each upstream family of
// patterns is a regexp plus the checks the lookarounds made, and an episode
// range is read by a loop instead of a repeated group.
//
// Ported: S01E01 and 1x01 with their multi-episode forms (S01E01E02,
// S01E01-02, S01E01-E03, S01E01.S01E02, 1x01-x03, 8x01_02), spaced and
// dotted forms (S01.E01, S01 - E01, S01.Ep.01), four-digit seasons, up to
// five-digit episodes, and "Season 1 Episode 5". Not ported, because v1
// leaves them out: daily shows, absolute (anime) numbering, mini-series
// (Part 1, 1of6), three-digit NNN numbering, season packs and tracker
// formats (Cap.101, afl.2).

package naming

import (
	"regexp"
	"strconv"
)

// EpisodeToken is what a file name says about its place in a series.
type EpisodeToken struct {
	Season int
	// Episodes holds every episode the file covers, in order; a range such
	// as S01E01-E03 holds 1, 2 and 3, as upstream does.
	Episodes []int
}

var (
	// Upstream's SimpleTitleRegex without the lookbehind forms: resolutions,
	// codecs and sound layouts would otherwise read as episode numbers.
	simpleTitle = regexp.MustCompile(`(?i)(?:(?:480|540|576|720|1080|1440|2160)[ip]|[xh][\W_]?26[456]|DD\W?5\W1|[<>?*]|848x480|1280x720|1920x1080|3840x2160|4096x2160)`)

	// "Episodes with a title" (S01E05), "S6.E1", "S01 - E01", "S01.Ep.01".
	seasonEpisode = regexp.MustCompile(`(?i)s(\d{1,4})[ ._]*-?[ ._]*e(?:p)?[ ._]?(\d{1,5})`)
	// "Episodes with a title" (1x05, [02x01]); preceded by a non-alphanumeric.
	crossEpisode = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])(\d{1,4})x(\d{1,5})`)
	// "Supports Season 01 Episode 03".
	wordEpisode = regexp.MustCompile(`(?i)season[ ._-]*(\d{1,2})[ ._-]+ep(?:isode)?[ ._-]*(\d{1,3})`)

	// What may follow an episode number to add the next one.
	nextAdjoined = regexp.MustCompile(`(?i)^(?:[-_]?[ex]|[-_])(\d{1,5})`)
	nextSpaced   = regexp.MustCompile(`(?i)^[ ._]+[ex]p?(\d{1,5})`)
	nextDashed   = regexp.MustCompile(`(?i)^[ ._]*-[ ._]*[ex]p?(\d{1,5})`)
	nextSeason   = regexp.MustCompile(`(?i)^[-_. ]+s(\d{1,4})[ ._]*-?[ ._]*e?p?[ ._]?(\d{1,5})`)
	nextCross    = regexp.MustCompile(`(?i)^[-_. ]+(\d{1,4})x(\d{1,5})`)

	// Upstream has no cap; one keeps S01E01-1992 from claiming a thousand episodes.
	maxRange = 100
)

// ParseEpisodeToken reads the season and episode numbers from a file name,
// with or without its extension. It reports false when the name has none.
func ParseEpisodeToken(name string) (EpisodeToken, bool) {
	name = simpleTitle.ReplaceAllString(name, "")
	for _, p := range []struct {
		re    *regexp.Regexp
		cross bool
	}{{seasonEpisode, false}, {crossEpisode, true}, {wordEpisode, false}} {
		for _, m := range p.re.FindAllStringSubmatchIndex(name, -1) {
			season, _ := strconv.Atoi(name[m[2]:m[3]])
			first, _ := strconv.Atoi(name[m[4]:m[5]])
			end := m[5]
			if end < len(name) && isDigit(name[end]) {
				continue
			}
			eps := extend(name[end:], season, first, p.cross)
			return EpisodeToken{Season: season, Episodes: eps}, eps != nil
		}
	}
	return EpisodeToken{}, false
}

// extend reads the episodes that follow the first, and returns the whole
// range from the first to the last as upstream does.
func extend(rest string, season, first int, cross bool) []int {
	last := first
	for {
		n, used, ok := next(rest, season, cross)
		if !ok || n <= last || n-first >= maxRange {
			break
		}
		last, rest = n, rest[used:]
	}
	eps := make([]int, 0, last-first+1)
	for n := first; n <= last; n++ {
		eps = append(eps, n)
	}
	return eps
}

// next reads one more episode number off the front of rest. The number must
// stand alone: "S01E05-31st" is not an episode 31.
func next(rest string, season int, cross bool) (n, used int, ok bool) {
	repeated := nextSeason
	if cross {
		repeated = nextCross
	}
	for _, re := range []*regexp.Regexp{nextAdjoined, nextSpaced, nextDashed, repeated} {
		m := re.FindStringSubmatchIndex(rest)
		if m == nil {
			continue
		}
		numAt := 2
		if re == repeated {
			// A repeated full token (S01E02, 2x10) must name the same season.
			if s, _ := strconv.Atoi(rest[m[2]:m[3]]); s != season {
				continue
			}
			numAt = 4
		}
		end := m[numAt+1]
		if end < len(rest) && isAlnum(rest[end]) {
			continue
		}
		v, _ := strconv.Atoi(rest[m[numAt]:end])
		return v, end, true
	}
	return 0, 0, false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlnum(c byte) bool {
	return isDigit(c) || (c|0x20) >= 'a' && (c|0x20) <= 'z'
}
