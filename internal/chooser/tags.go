package chooser

import (
	"regexp"
	"strings"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/naming"
)

const (
	// lead and trail stand for the edges of a token: a name is split on
	// anything that is not a letter or a digit, so "DD" is not in "Daddy".
	lead  = `(?:^|[^a-z0-9])`
	trail = `(?:$|[^a-z0-9])`
)

func token(body string) *regexp.Regexp { return regexp.MustCompile(lead + body + trail) }

var (
	reTrueHD = token(`true[ ._-]?hd`)
	reDTSX   = token(`dts[ ._:-]?x`)
	reDTSHD  = token(`dts[ ._-]?hd(?:[ ._-]?(?:ma|hra))?`)
	reDTS    = token(`dts\d*`)
	reAtmos  = token(`atmos`)
	reDDP    = regexp.MustCompile(lead + `(?:dd\+|ddp\d*` + trail + `|e[ ._-]?ac[ ._-]?3` + trail + `)`)
	reDD     = token(`(?:dd|ac[ ._-]?3)\d*`)
	reAAC    = token(`aac\d*`)
	reFLAC   = token(`flac`)
	reOpus   = token(`opus`)

	reDV     = token(`(?:dv(?:p[78])?|dovi|dolby[ ._-]?vision)`)
	reHDR10P = regexp.MustCompile(lead + `hdr10(?:\+|(?:plus|p)` + trail + `)`)
	reHDR10  = token(`hdr10`)
	reHDR    = token(`hdr`)
	reAV1    = token(`av1`)
	reHEVC   = token(`(?:hevc|x265|h[ ._-]?265)`)
	reAVC    = token(`(?:avc|x264|h[ ._-]?264)`)
	reP7     = token(`(?:(?:dv|dovi)[ ._-]?)?(?:p|profile[ ._-]?)7`)
	reFEL    = token(`fel`)
	reP8     = token(`(?:(?:dv|dovi)[ ._-]?)?(?:p|profile[ ._-]?)8`)
	reRemux  = token(`remux`)
	reBluray = token(`(?:blu[ ._-]?ray|bdrip|brrip)`)
	reWebSrc = token(`(?:web(?:[ ._-]?(?:dl|rip))?)`)
)

// scrub blanks out every match so a shorter token cannot be found inside a
// longer one ("DD" in "DD+", "DTS" in "DTS-HD").
func scrub(text string, res ...*regexp.Regexp) string {
	for _, re := range res {
		text = re.ReplaceAllString(text, " ")
	}
	return text
}

// ExtractTags lists the format tags a release carries, in a fixed order:
// audio first, then video and HDR. AIOStreams' parsed tags are read
// together with the file and folder names, because an addon may leave the
// tags out. The names are lower-cased before matching.
func ExtractTags(s aiostreams.Stream) []string {
	parts := []string{s.Filename, s.FolderName}
	tier := "Unknown"
	if p := s.ParsedFile; p != nil {
		parts = append(parts, p.Quality, p.Encode)
		parts = append(parts, p.AudioTags...)
		parts = append(parts, p.VisualTags...)
		tier = naming.CanonicalQuality(p.Resolution, p.Quality)
	}
	text := strings.ToLower(strings.Join(parts, " "))

	var tags []string
	add := func(ok bool, tag string) {
		if ok {
			tags = append(tags, tag)
		}
	}

	truehd := reTrueHD.MatchString(text)
	dtsx := reDTSX.MatchString(text)
	dtshd := reDTSHD.MatchString(text)
	ddp := reDDP.MatchString(text)
	rest := scrub(text, reDTSX, reDTSHD, reDDP)
	add(truehd, config.TagTrueHD)
	add(dtshd, config.TagDTSHD)
	add(dtsx, config.TagDTSX)
	add(ddp && reAtmos.MatchString(text), config.TagDDPAtmos)
	add(ddp, config.TagDDP)
	add(reDTS.MatchString(rest), config.TagDTS)
	add(reDD.MatchString(rest), config.TagDD)
	add(reAAC.MatchString(text), config.TagAAC)
	add(reFLAC.MatchString(text), config.TagFLAC)
	add(reOpus.MatchString(text), config.TagOpus)

	dv := reDV.MatchString(text)
	hdr10p := reHDR10P.MatchString(text)
	rest = scrub(text, reHDR10P)
	add(dv && dolbyP7(text, tier), config.TagDVP7)
	add(dv, config.TagDV)
	add(hdr10p, config.TagHDR10Plus)
	add(reHDR10.MatchString(rest), config.TagHDR10)
	add(hdr10p || reHDR.MatchString(rest), config.TagHDR)
	add(reAV1.MatchString(text), config.TagAV1)
	add(reHEVC.MatchString(text), config.TagHEVC)
	add(reAVC.MatchString(text), config.TagAVC)
	return tags
}

// dolbyP7 guesses Dolby Vision Profile 7 without reading the file: discs
// carry it and web releases do not, and Profile 7 is what an Android TV
// cannot play. A Remux is always assumed to be Profile 7, and a disc encode
// with no web marker is too unless it says Profile 8; an explicit Profile 7
// or FEL marker decides on its own.
func dolbyP7(text, tier string) bool {
	if reP7.MatchString(text) || reFEL.MatchString(text) {
		return true
	}
	if strings.HasPrefix(tier, "Remux-") || reRemux.MatchString(text) {
		return true
	}
	disc := strings.HasPrefix(tier, "Bluray-") || reBluray.MatchString(text)
	return disc && !reWebSrc.MatchString(text) && !reP8.MatchString(text)
}
