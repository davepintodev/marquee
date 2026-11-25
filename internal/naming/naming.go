// Translated from Sonarr (https://github.com/Sonarr/Sonarr) and Radarr
// (https://github.com/Radarr/Radarr), both licensed under the GNU General
// Public License v3.0, as is marquee: the file-name building (FileNameBuilder)
// and its limit on file-name length.

package naming

import (
	"fmt"
	"slices"
	"sort"
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
	return episodeFile(series, fmt.Sprintf("S%02dE%02d", season, number), episodeTitle(episode), quality, ext)
}

// MultiEpisodeFile names a file that holds several episodes of one season:
// "Series - S01E01-E02 - Ep1 + Ep2 [Quality].ext" for consecutive numbers
// and "Series - S01E01E03 - Ep1 + Ep3 [Quality].ext" when there is a gap.
// titles[i] belongs to numbers[i]; a missing title reads as TBA, and a
// title that repeats appears once. One episode is EpisodeFile.
func MultiEpisodeFile(series string, season int, numbers []int, titles []string, quality, ext string) string {
	type pair struct {
		number int
		title  string
	}
	pairs := make([]pair, 0, len(numbers))
	for i, n := range numbers {
		title := ""
		if i < len(titles) {
			title = titles[i]
		}
		pairs = append(pairs, pair{n, title})
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].number < pairs[j].number })
	pairs = slices.CompactFunc(pairs, func(a, b pair) bool { return a.number == b.number })
	if len(pairs) < 2 {
		number, title := 0, ""
		if len(pairs) == 1 {
			number, title = pairs[0].number, pairs[0].title
		}
		return EpisodeFile(series, season, number, title, quality, ext)
	}

	token := fmt.Sprintf("S%02dE%02d", season, pairs[0].number)
	last := pairs[len(pairs)-1].number
	if last-pairs[0].number == len(pairs)-1 {
		token += fmt.Sprintf("-E%02d", last)
	} else {
		for _, p := range pairs[1:] {
			token += fmt.Sprintf("E%02d", p.number)
		}
	}
	var joined []string
	for _, p := range pairs {
		if t := episodeTitle(p.title); !slices.Contains(joined, t) {
			joined = append(joined, t)
		}
	}
	return episodeFile(series, token, strings.Join(joined, " + "), quality, ext)
}

func episodeTitle(episode string) string {
	title := strings.TrimRight(episode, " .?")
	if strings.TrimSpace(title) == "" {
		title = "TBA"
	}
	return CleanFileName(title)
}

func episodeFile(series, token, title, quality, ext string) string {
	build := func(t string) string {
		c := finishComponent(fmt.Sprintf("%s - %s - %s [%s]", CleanFileName(series), token, t, quality))
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
