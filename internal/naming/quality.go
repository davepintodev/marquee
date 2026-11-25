// Translated from Sonarr (https://github.com/Sonarr/Sonarr) and Radarr
// (https://github.com/Radarr/Radarr), both licensed under the GNU General
// Public License v3.0, as is marquee: the quality names and the rules that pick them
// (Quality definitions and QualityParser). The pre-release sources, HDRip,
// and 1440p handling are marquee's own mapping of AIOStreams' vocabulary.

package naming

import (
	"strconv"
	"strings"
)

// Kind selects the upstream whose quality names apply.
const (
	KindMovie  = "movie"  // Radarr
	KindSeries = "series" // Sonarr
)

type source int

const (
	srcUnknown source = iota
	srcBluray
	srcWebDL
	srcWebRip
	srcHDTV
	srcDVD
	srcCAM
	srcTelesync
	srcTelecine
	srcScreener
)

// QualityName is the quality tag upstream writes into a file name for an
// AIOStreams parsed file: its resolution ("1080p") and quality ("BluRay
// REMUX", "WEB-DL"). Sonarr writes "Bluray-1080p Remux" where Radarr writes
// "Remux-1080p"; pre-release sources exist only in Radarr. The fallbacks
// follow upstream's QualityParser.
func QualityName(kind, resolution, quality string) string {
	src, remux := parseSource(quality)
	res := parseResolution(resolution)

	if kind == KindSeries {
		switch src {
		case srcCAM, srcTelesync, srcTelecine, srcScreener:
			return "Unknown"
		}
	}

	switch src {
	case srcCAM:
		return "CAM"
	case srcTelesync:
		return "TELESYNC"
	case srcTelecine:
		return "TELECINE"
	case srcScreener:
		return "DVDSCR"
	case srcDVD:
		return "DVD"
	case srcBluray:
		switch {
		case res == 2160 || res == 1080:
			if remux {
				return remuxName(kind, res)
			}
			return "Bluray-" + strconv.Itoa(res) + "p"
		case res == 576 || res == 480:
			return "Bluray-" + strconv.Itoa(res) + "p"
		case remux && res != 720:
			return remuxName(kind, 1080)
		}
		return "Bluray-720p"
	case srcWebDL:
		return "WEBDL-" + webResolution(res)
	case srcWebRip:
		return "WEBRip-" + webResolution(res)
	case srcHDTV:
		return tvName(res)
	}
	if remux && res != 0 {
		if res == 2160 || res == 1080 {
			return remuxName(kind, res)
		}
		return "Bluray-" + strconv.Itoa(res) + "p"
	}
	if res == 0 {
		return "Unknown"
	}
	return tvName(res)
}

// CanonicalQuality is QualityName for the profiles: one tier name per
// quality whatever the kind, so Radarr's "Remux-1080p" stands for
// Sonarr's "Bluray-1080p Remux" too, and the pre-release sources keep
// their names for a series.
func CanonicalQuality(resolution, quality string) string {
	return QualityName(KindMovie, resolution, quality)
}

func remuxName(kind string, res int) string {
	if kind == KindMovie {
		return "Remux-" + strconv.Itoa(res) + "p"
	}
	return "Bluray-" + strconv.Itoa(res) + "p Remux"
}

func webResolution(res int) string {
	switch res {
	case 2160, 1080, 720:
		return strconv.Itoa(res) + "p"
	}
	return "480p"
}

func tvName(res int) string {
	switch res {
	case 2160, 1080, 720:
		return "HDTV-" + strconv.Itoa(res) + "p"
	}
	return "SDTV"
}

func parseSource(quality string) (src source, remux bool) {
	q := strings.ToLower(quality)
	remux = strings.Contains(q, "remux")
	q = strings.NewReplacer(" ", "", "-", "", "_", "", ".", "").Replace(q)
	switch {
	case strings.Contains(q, "bluray"), strings.Contains(q, "bdrip"), strings.Contains(q, "brrip"):
		return srcBluray, remux
	case strings.Contains(q, "webdl"):
		return srcWebDL, remux
	case strings.Contains(q, "webrip"), strings.Contains(q, "hdrip"):
		return srcWebRip, remux
	case strings.Contains(q, "hdtv"), strings.Contains(q, "pdtv"):
		return srcHDTV, remux
	case strings.Contains(q, "dvd") && !strings.Contains(q, "scr"):
		return srcDVD, remux
	case q == "cam":
		return srcCAM, remux
	case q == "ts", q == "telesync":
		return srcTelesync, remux
	case q == "tc", q == "telecine":
		return srcTelecine, remux
	case q == "scr", strings.Contains(q, "dvdscr"):
		return srcScreener, remux
	}
	return srcUnknown, remux
}

// parseResolution maps AIOStreams' resolution to the heights upstream has
// qualities for; 0 means unknown. A 1440p file is named 1080p.
func parseResolution(s string) int {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "p"))
	switch {
	case err != nil || n <= 0:
		return 0
	case n >= 2160:
		return 2160
	case n >= 1080:
		return 1080
	case n >= 720:
		return 720
	case n >= 576:
		return 576
	}
	return 480
}
