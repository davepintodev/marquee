// Package naming builds the folder and file names Sonarr and Radarr give
// imported media, so Jellyfin and an existing library stay consistent.
//
// The character cleaning, the quality names and their selection rules are
// translated from Sonarr (https://github.com/Sonarr/Sonarr) and Radarr
// (https://github.com/Radarr/Radarr), both licensed under the GNU General
// Public License v3.0, as is marquee. The scheme is fixed: Sonarr's and
// Radarr's defaults for colon replacement and illegal characters.
package naming

import (
	"regexp"
	"strings"
)

// Upstream's BadCharacters and GoodCharacters, paired by index.
var (
	badCharacters  = []string{`\`, "/", "<", ">", "?", "*", "|", `"`}
	goodCharacters = []string{"+", "+", "", "", "!", "-", "", ""}
)

var (
	trailingSeparators = regexp.MustCompile(`[- ._]+$`)
	reservedDevice     = regexp.MustCompile(`(?i)^(?:aux|com[1-9]|con|lpt[1-9]|nul|prn)\.`)
)

// CleanFileName is upstream's CleanFileName with its default configuration:
// "Smart" colon replacement and illegal characters replaced rather than dropped.
func CleanFileName(name string) string {
	name = strings.ReplaceAll(name, ": ", " - ")
	name = strings.ReplaceAll(name, ":", "-")
	for i, bad := range badCharacters {
		name = strings.ReplaceAll(name, bad, goodCharacters[i])
	}
	return strings.TrimRight(strings.TrimLeft(name, " ."), " ")
}

// replaceReservedDeviceNames keeps a name from starting with a Windows
// device name followed by a dot.
func replaceReservedDeviceNames(s string) string {
	return reservedDevice.ReplaceAllStringFunc(s, func(m string) string {
		return strings.ReplaceAll(m, ".", "_")
	})
}

// finishComponent is the end of upstream's BuildFileName for one path
// component: collapse repeated separators, trim trailing ones, protect
// device names.
func finishComponent(s string) string {
	s = strings.TrimSpace(s)
	s = collapseRepeats(s)
	s = trailingSeparators.ReplaceAllString(s, "")
	return replaceReservedDeviceNames(s)
}

// collapseRepeats shortens a run of the same separator to one, as upstream's
// ([- ._])(\1)+ replacement does (Go's regexp has no backreferences).
func collapseRepeats(s string) string {
	var b strings.Builder
	var prev rune
	for _, r := range s {
		if r == prev && strings.ContainsRune("- ._", r) {
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}
