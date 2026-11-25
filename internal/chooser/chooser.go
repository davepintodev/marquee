// Package chooser turns the releases AIOStreams found into the candidates
// marquee may download, and says in words why it dropped each other one.
// It is a pure function: nothing here reads a clock, a database or the
// network.
package chooser

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/naming"
)

// BlockExpiry is how long a blocklist entry holds.
const BlockExpiry = 30 * 24 * time.Hour

const (
	mb           = 1 << 20
	minMovie     = 200 * mb
	minEpisode   = 50 * mb
	kindSeries   = "series"
	identityHash = 16 // bytes of the digest kept in an identity
)

// Reason is why a release was dropped. The values are the contract's.
type Reason string

const (
	NotCached       Reason = "not_cached"
	NotVideo        Reason = "not_video"
	TooSmall        Reason = "too_small"
	WrongEpisode    Reason = "wrong_episode"
	Blocklisted     Reason = "blocklisted"
	ProfileRejected Reason = "profile_rejected"
)

var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".wmv": true, ".ts": true, ".m2ts": true, ".webm": true, ".mpg": true, ".mpeg": true,
}

func IsVideoFile(name string) bool { return videoExtensions[strings.ToLower(path.Ext(name))] }

// Context is what the releases are chosen for.
type Context struct {
	Kind            string // "movie" or "series"
	Season, Episode int    // the episode wanted; ignored for a movie
	// OwnedEpisodes are the episodes of Season that already have a file.
	// Between releases that tie otherwise, the one that repeats the fewest
	// of them ranks first, so a single episode beats a multi-episode file
	// that would download an owned episode again.
	OwnedEpisodes []int
	// Profile decides which qualities and formats are allowed and ranks
	// what is left; a profile with no Qualities allows nothing.
	Profile config.Profile
	// AllowUncached keeps releases the debrid service has not cached, which
	// TorBox can fetch; every cached release still ranks before every one of them.
	AllowUncached bool
	// Blocklist maps a release identity to when it was blocked. Entries
	// older than BlockExpiry are ignored, so the caller need not purge.
	Blocklist map[string]time.Time
	Now       time.Time
}

type Candidate struct {
	// ID is the release's identity, stable across searches and addons.
	ID     string
	Stream aiostreams.Stream
}

type Drop struct {
	Candidate
	Reason  Reason
	Message string
}

type Result struct {
	// Candidates are in the order marquee would try them.
	Candidates []Candidate
	Dropped    []Drop
}

// Identity names a release by what it is, never by the link to it, which
// carries credentials and may change: the info hash, the file name and
// the size.
func Identity(s aiostreams.Stream) string {
	return IdentityOf(s.InfoHash, s.Filename, s.Size)
}

// IdentityOf is Identity for a release known only by its parts, such as
// a download taken from the history.
func IdentityOf(infoHash, fileName string, size int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", strings.ToLower(infoHash), fileName, size)))
	return hex.EncodeToString(sum[:identityHash])
}

// Choose filters releases in a fixed order (cached, video, size, episode,
// profile, blocklist), so each drop names the first rule it broke. The
// candidates are ranked cached before uncached, then by the profile's
// quality order, then by how few
// owned episodes they repeat, then by its prefer boosts, and keep
// AIOStreams' order where those tie. The same file listed twice keeps its first listing.
func Choose(releases []aiostreams.Stream, c Context) Result {
	out := Result{Candidates: []Candidate{}, Dropped: []Drop{}}
	seen := map[string]bool{}
	type ranked struct {
		Candidate
		tier, boost, overlap int
		uncached             bool
	}
	var kept []ranked
	for _, s := range releases {
		id := Identity(s)
		if seen[id] {
			continue
		}
		seen[id] = true
		cand := Candidate{ID: id, Stream: s}
		if reason, msg := check(s, id, c); reason != "" {
			out.Dropped = append(out.Dropped, Drop{cand, reason, msg})
			continue
		}
		kept = append(kept, ranked{cand, slices.Index(c.Profile.Qualities, Tier(s)), boost(c.Profile, ExtractTags(s)), overlap(s, c), !s.Cached})
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].uncached != kept[j].uncached {
			return !kept[i].uncached
		}
		if kept[i].tier != kept[j].tier {
			return kept[i].tier < kept[j].tier
		}
		if kept[i].overlap != kept[j].overlap {
			return kept[i].overlap < kept[j].overlap
		}
		return kept[i].boost > kept[j].boost
	})
	for _, k := range kept {
		out.Candidates = append(out.Candidates, k.Candidate)
	}
	return out
}

// overlap counts the episodes a series release covers that are already on
// disk. The episode being searched for is never one of them.
func overlap(s aiostreams.Stream, c Context) int {
	if c.Kind != kindSeries || s.ParsedFile == nil {
		return 0
	}
	n := 0
	for _, e := range s.ParsedFile.Episodes {
		if e != c.Episode && slices.Contains(c.OwnedEpisodes, e) {
			n++
		}
	}
	return n
}

// CoveredEpisodes are the episodes of season that one file of the release
// holds. A release that spans several seasons says nothing certain about
// where its episode numbers belong, so it covers none.
func CoveredEpisodes(s aiostreams.Stream, season int) []int {
	if s.ParsedFile == nil || len(s.ParsedFile.Seasons) != 1 || s.ParsedFile.Seasons[0] != season {
		return nil
	}
	return s.ParsedFile.Episodes
}

// Tier is the canonical quality a profile lists a release under.
func Tier(s aiostreams.Stream) string {
	if s.ParsedFile == nil {
		return naming.CanonicalQuality("", "")
	}
	return naming.CanonicalQuality(s.ParsedFile.Resolution, s.ParsedFile.Quality)
}

func boost(p config.Profile, tags []string) int {
	sum := 0
	for i, t := range p.Prefer {
		if slices.Contains(tags, t) {
			sum += len(p.Prefer) - i
		}
	}
	return sum
}

func check(s aiostreams.Stream, id string, c Context) (Reason, string) {
	if !s.Cached {
		switch {
		case !c.AllowUncached:
			return NotCached, "not cached on the debrid service, so it would have to be fetched first"
		case s.InfoHash == "":
			return NotCached, "not cached, and it lists no info hash for TorBox to fetch it by"
		}
	}
	if msg := notVideo(s.Filename); msg != "" {
		return NotVideo, msg
	}
	if msg := tooSmall(s.Size, c.Kind); msg != "" {
		return TooSmall, msg
	}
	if c.Kind == kindSeries {
		if msg := wrongEpisode(s.ParsedFile, c.Season, c.Episode); msg != "" {
			return WrongEpisode, msg
		}
	}
	if msg := profileRefuses(s, c.Profile); msg != "" {
		return ProfileRejected, msg
	}
	if at, ok := c.Blocklist[id]; ok && c.Now.Sub(at) < BlockExpiry {
		return Blocklisted, fmt.Sprintf("failed before, blocked on %s for %d days", at.UTC().Format("2006-01-02"), int(BlockExpiry/(24*time.Hour)))
	}
	return "", ""
}

var tagWords = map[string]string{
	config.TagTrueHD:    "TrueHD audio",
	config.TagDTSHD:     "DTS-HD audio",
	config.TagDTSX:      "DTS:X audio",
	config.TagDDPAtmos:  "Dolby Digital Plus Atmos audio",
	config.TagDDP:       "Dolby Digital Plus audio",
	config.TagDTS:       "DTS audio",
	config.TagDD:        "Dolby Digital audio",
	config.TagAAC:       "AAC audio",
	config.TagFLAC:      "FLAC audio",
	config.TagOpus:      "Opus audio",
	config.TagDVP7:      "DV Profile 7",
	config.TagDV:        "Dolby Vision",
	config.TagHDR10Plus: "HDR10+",
	config.TagHDR10:     "HDR10",
	config.TagHDR:       "HDR",
	config.TagAV1:       "AV1 video",
	config.TagHEVC:      "HEVC video",
	config.TagAVC:       "AVC video",
}

func profileRefuses(s aiostreams.Stream, p config.Profile) string {
	if tier := Tier(s); !slices.Contains(p.Qualities, tier) {
		return tier + " is not allowed by this profile"
	}
	tags := ExtractTags(s)
	var hit []string
	for _, t := range p.Reject {
		if slices.Contains(tags, t) {
			hit = append(hit, tagWords[t])
		}
	}
	if len(hit) > 0 {
		return "rejected by profile: " + strings.Join(hit, ", ")
	}
	if lo := p.SizeLimits.MinMB; lo > 0 && s.Size < lo*mb {
		return fmt.Sprintf("%d MB, below this profile's minimum of %d MB", s.Size/mb, lo)
	}
	if hi := p.SizeLimits.MaxMB; hi > 0 && s.Size > hi*mb {
		return fmt.Sprintf("%d MB, above this profile's maximum of %d MB", (s.Size+mb-1)/mb, hi)
	}
	return ""
}

func notVideo(filename string) string {
	if filename == "" {
		return "AIOStreams gave no file name, so it cannot be told to be a video"
	}
	ext := strings.ToLower(path.Ext(filename))
	switch {
	case ext == "":
		return "the file has no file extension, so it cannot be told to be a video"
	case !videoExtensions[ext]:
		return fmt.Sprintf("a %s file, not a video", ext)
	}
	return ""
}

func tooSmall(size int64, kind string) string {
	floor, what := int64(minMovie), "a movie"
	if kind == kindSeries {
		floor, what = minEpisode, "an episode"
	}
	switch {
	case size <= 0:
		return "its size is unknown, so it cannot be told from a sample"
	case size < floor:
		return fmt.Sprintf("%d MB, too small to be %s (at least %d MB)", size/mb, what, floor/mb)
	}
	return ""
}

func wrongEpisode(p *aiostreams.ParsedFile, season, episode int) string {
	if p == nil {
		return "AIOStreams could not read a season or episode from the file name"
	}
	if len(p.Seasons) == 0 {
		return "the file name does not say which season this is"
	}
	if !contains(p.Seasons, season) {
		return fmt.Sprintf("this is season %s, not season %d", join(p.Seasons), season)
	}
	if len(p.Episodes) == 0 {
		return "the file name does not say which episode this is"
	}
	if !contains(p.Episodes, episode) {
		return fmt.Sprintf("this is episode %s, not episode %d", join(p.Episodes), episode)
	}
	return ""
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func join(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprint(x)
	}
	return strings.Join(parts, " and ")
}
