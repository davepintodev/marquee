package chooser

import (
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/naming"
)

// PackID is a season pack's identity: the torrent as a whole, whatever file
// of it AIOStreams happened to list. A download that takes a pack before
// its file list is known carries this identity, so failing it blocks the pack.
func PackID(infoHash string) string { return IdentityOf(infoHash, "", 0) }

var seasonOnly = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:s|season[ ._-]?)0*(\d{1,3})(?:[^a-z0-9]|$)`)

// IsSeasonPack reports whether a release is a torrent of a whole or part
// season rather than one episode: AIOStreams parsed the season and no single
// episode, or flagged a pack, or, unparsed, the folder names only a season.
func IsSeasonPack(s aiostreams.Stream, season int) bool {
	if s.InfoHash == "" {
		return false
	}
	if p := s.ParsedFile; p != nil && len(p.Seasons) > 0 {
		return len(p.Seasons) == 1 && p.Seasons[0] == season && (len(p.Episodes) == 0 || p.SeasonPack)
	}
	if _, ok := naming.ParseEpisodeToken(s.FolderName); ok {
		return false
	}
	m := seasonOnly.FindStringSubmatch(s.FolderName)
	return m != nil && m[1] == strconv.Itoa(season)
}

// PackName is how a pack is named in Activity and the history.
func PackName(s aiostreams.Stream) string {
	if s.FolderName != "" {
		return s.FolderName
	}
	return s.Filename
}

// Packs are the season packs of c.Season among the releases that the
// profile allows and the blocklist does not hold, cached before uncached,
// then by the profile's quality order and prefer boosts. Size limits apply
// to each file of a pack once its list is known, not to the pack.
func Packs(releases []aiostreams.Stream, c Context) []Candidate {
	type ranked struct {
		Candidate
		tier, boost int
	}
	var kept []ranked
	seen := map[string]bool{}
	for _, s := range releases {
		if !IsSeasonPack(s, c.Season) || (!s.Cached && !c.AllowUncached) {
			continue
		}
		id := PackID(s.InfoHash)
		if seen[id] {
			continue
		}
		seen[id] = true
		if profileRefuses(s, config.Profile{Qualities: c.Profile.Qualities, Reject: c.Profile.Reject}) != "" {
			continue
		}
		if at, ok := c.Blocklist[id]; ok && c.Now.Sub(at) < BlockExpiry {
			continue
		}
		kept = append(kept, ranked{Candidate{ID: id, Stream: s}, slices.Index(c.Profile.Qualities, Tier(s)), boost(c.Profile, ExtractTags(s))})
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Stream.Cached != kept[j].Stream.Cached {
			return kept[i].Stream.Cached
		}
		if kept[i].tier != kept[j].tier {
			return kept[i].tier < kept[j].tier
		}
		return kept[i].boost > kept[j].boost
	})
	out := make([]Candidate, len(kept))
	for i, k := range kept {
		out[i] = k.Candidate
	}
	return out
}

// PackFile is one file of a pack's torrent.
type PackFile struct {
	ID   int
	Name string // path inside the torrent
	Size int64
}

// PackMatch is a file of a pack that holds wanted episodes.
type PackMatch struct {
	PackFile
	// Episodes are every episode of the season the file holds, in order.
	Episodes []int
}

// MatchPack picks the files of a pack worth downloading: videos of season
// whose name holds at least one wanted episode and no owned one, of a size
// the profile allows and an episode can have, and not blocklisted. A wanted
// episode two files hold goes to the first in episode order.
func MatchPack(files []PackFile, infoHash string, season int, wanted, owned []int, c Context) []PackMatch {
	var out []PackMatch
	for _, f := range files {
		base := path.Base(f.Name)
		if !IsVideoFile(base) || tooSmall(f.Size, kindSeries) != "" {
			continue
		}
		tok, ok := naming.ParseEpisodeToken(base)
		if !ok || tok.Season != season {
			continue
		}
		hit, clash := false, false
		for _, e := range tok.Episodes {
			hit = hit || slices.Contains(wanted, e)
			clash = clash || slices.Contains(owned, e)
		}
		if !hit || clash || sizeRefused(f.Size, c.Profile) {
			continue
		}
		if at, ok := c.Blocklist[IdentityOf(infoHash, base, f.Size)]; ok && c.Now.Sub(at) < BlockExpiry {
			continue
		}
		eps := slices.Clone(tok.Episodes)
		slices.Sort(eps)
		out = append(out, PackMatch{PackFile: f, Episodes: eps})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Episodes[0] < out[j].Episodes[0] })
	claimed := map[int]bool{}
	kept := out[:0]
	for _, m := range out {
		if slices.ContainsFunc(m.Episodes, func(e int) bool { return claimed[e] }) {
			continue
		}
		for _, e := range m.Episodes {
			claimed[e] = true
		}
		kept = append(kept, m)
	}
	return kept
}

func sizeRefused(size int64, p config.Profile) bool {
	lo, hi := p.SizeLimits.MinMB, p.SizeLimits.MaxMB
	return (lo > 0 && size < lo*mb) || (hi > 0 && size > hi*mb)
}
