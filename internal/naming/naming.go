// Translated from Sonarr (https://github.com/Sonarr/Sonarr) and Radarr
// (https://github.com/Radarr/Radarr), both licensed under the GNU General
// Public License v3.0, as is marquee: the file-name building (FileNameBuilder)
// and its limit on file-name length.

package naming

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxFileNameBytes is upstream's LongPathSupport.MaxFileNameLength.
const maxFileNameBytes = 255

// MovieFolder is "Title (Year)".
func MovieFolder(title string, year int) string {
	return finishComponent(CleanFileName(title) + fmt.Sprintf(" (%d)", year))
}

// MovieFile is "Title (Year) [Quality].ext"; ext includes its dot.
func MovieFile(title string, year int, quality, ext string) string {
	return finishComponent(CleanFileName(title)+fmt.Sprintf(" (%d) [%s]", year, quality)) + ext
}

// SeriesFolder is the series name alone: the folders on disk carry no year.
func SeriesFolder(title string) string {
	return finishComponent(CleanFileName(title))
}

// SeasonFolder is "Season NN", and "Specials" for season 0.
func SeasonFolder(season int) string {
	if season == 0 {
		return "Specials"
	}
	return fmt.Sprintf("Season %02d", season)
}

// EpisodeFile is "Series - SNNENN - Episode [Quality].ext". The episode
// title is trimmed and, when the name would pass the file-name limit,
// shortened with an ellipsis, as upstream does.
func EpisodeFile(series string, season, number int, episode, quality, ext string) string {
	title := strings.TrimRight(episode, " .?")
	if strings.TrimSpace(title) == "" {
		title = "TBA"
	}
	title = CleanFileName(title)

	build := func(t string) string {
		c := finishComponent(fmt.Sprintf("%s - S%02dE%02d - %s [%s]", CleanFileName(series), season, number, t, quality))
		return strings.ReplaceAll(c, "{ellipsis}", "...") // kept out of the separator cleanup, as upstream does
	}
	if len(build(title)) > maxFileNameBytes-len(ext) {
		overhead := len(build("x")) - 1
		title = truncateBytes(title, maxFileNameBytes-len(ext)-overhead-len("..."))
		title = strings.TrimRight(title, " .") + "{ellipsis}"
	}
	return build(title) + ext
}

func truncateBytes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
