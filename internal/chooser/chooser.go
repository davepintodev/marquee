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
	"strings"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
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
	NotCached    Reason = "not_cached"
	NotVideo     Reason = "not_video"
	TooSmall     Reason = "too_small"
	WrongEpisode Reason = "wrong_episode"
	Blocklisted  Reason = "blocklisted"
)

var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".wmv": true, ".ts": true, ".m2ts": true, ".webm": true, ".mpg": true, ".mpeg": true,
}

// Context is what the releases are chosen for.
type Context struct {
	Kind            string // "movie" or "series"
	Season, Episode int    // the episode wanted; ignored for a movie
	// PreferredQuality is a resolution such as "2160p", or empty. It only
	// moves matching candidates first; it never drops one.
	PreferredQuality string
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
// blocklist), so each drop names the first rule it broke. AIOStreams'
// order is kept, except that candidates of the preferred quality come
// first. The same file listed twice keeps its first listing.
func Choose(releases []aiostreams.Stream, c Context) Result {
	out := Result{Candidates: []Candidate{}, Dropped: []Drop{}}
	seen := map[string]bool{}
	var preferred, others []Candidate
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
		if c.PreferredQuality != "" && s.ParsedFile != nil && strings.EqualFold(s.ParsedFile.Resolution, c.PreferredQuality) {
			preferred = append(preferred, cand)
		} else {
			others = append(others, cand)
		}
	}
	out.Candidates = append(append(out.Candidates, preferred...), others...)
	return out
}

func check(s aiostreams.Stream, id string, c Context) (Reason, string) {
	if !s.Cached {
		return NotCached, "not cached on the debrid service, so it would have to be fetched first"
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
	if at, ok := c.Blocklist[id]; ok && c.Now.Sub(at) < BlockExpiry {
		return Blocklisted, fmt.Sprintf("failed before, blocked on %s for %d days", at.UTC().Format("2006-01-02"), int(BlockExpiry/(24*time.Hour)))
	}
	return "", ""
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
