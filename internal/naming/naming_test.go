// Portions of these cases are translated from the Sonarr and Radarr test
// suites (https://github.com/Sonarr/Sonarr, https://github.com/Radarr/Radarr),
// which are licensed under the GNU General Public License v3.0. Cases marked
// "adapted" differ from upstream only in the function that is called.

package naming

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanFileName(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		// CleanFilenameFixture, Sonarr and Radarr.
		{"colon space", "Law & Order: Criminal Intent - S10E07 - Icarus [HDTV-720p]", "Law & Order - Criminal Intent - S10E07 - Icarus [HDTV-720p]"},
		{"colon space movie", "Mission: Impossible - no [HDTV-720p]", "Mission - Impossible - no [HDTV-720p]"},
		{"leading period", ".hack s01e01", "hack s01e01"},
		{"leading period year", ".45 (2006)", "45 (2006)"},
		{"leading space", " Series Title - S01E01 - Episode Title", "Series Title - S01E01 - Episode Title"},
		{"trailing space", "Series Title - S01E01 - Episode Title ", "Series Title - S01E01 - Episode Title"},
		{"both spaces", " The Movie Title ", "The Movie Title"},
		// ColonReplacementFixture, the Smart rows (the only format marquee uses).
		{"smart colon space", "CSI: Vegas", "CSI - Vegas"},
		{"smart bare colon", "Series:Title", "Series-Title"},
		// BadCharacters/GoodCharacters tables in FileNameBuilder.
		{"slashes", `AC/DC \ Live`, "AC+DC + Live"},
		{"angle brackets removed", "a<b>c", "abc"},
		{"question mark", "Who Is It?", "Who Is It!"},
		{"star", "A*B", "A-B"},
		{"pipe and quote removed", `A|"B"`, "AB"},
		{"unicode kept", "Amélie", "Amélie"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanFileName(tc.in); got != tc.want {
				t.Errorf("CleanFileName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestReplaceReservedDeviceNames(t *testing.T) {
	// ReservedDeviceNameFixture (adapted: upstream builds the name from a
	// "{Series.Title}" token pattern that dots the title; the function under
	// test is the same).
	for _, tc := range []struct{ in, want string }{
		{"Con.Game", "Con_Game"},
		{"Com1.Sat", "Com1_Sat"},
		{"Console.Game", "Console.Game"},
		{"South.Park", "South.Park"},
	} {
		if got := replaceReservedDeviceNames(tc.in); got != tc.want {
			t.Errorf("replaceReservedDeviceNames(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMovieNames(t *testing.T) {
	for _, tc := range []struct {
		title        string
		year         int
		quality, ext string
		folder, file string
	}{
		{"Project Hail Mary", 2026, "Remux-2160p", ".mkv", "Project Hail Mary (2026)", "Project Hail Mary (2026) [Remux-2160p].mkv"},
		{"A Charlie Brown Christmas", 1965, "WEBDL-1080p", ".mkv", "A Charlie Brown Christmas (1965)", "A Charlie Brown Christmas (1965) [WEBDL-1080p].mkv"},
		{"Mission: Impossible", 1996, "Bluray-1080p", ".mp4", "Mission - Impossible (1996)", "Mission - Impossible (1996) [Bluray-1080p].mp4"},
		{"Who?", 2000, "HDTV-720p", ".avi", "Who! (2000)", "Who! (2000) [HDTV-720p].avi"},
		{"Con.Game", 2021, "WEBDL-1080p", ".mkv", "Con_Game (2021)", "Con_Game (2021) [WEBDL-1080p].mkv"},
	} {
		if got := MovieFolder(tc.title, tc.year); got != tc.folder {
			t.Errorf("MovieFolder(%q) = %q, want %q", tc.title, got, tc.folder)
		}
		if got := MovieFile(tc.title, tc.year, tc.quality, tc.ext); got != tc.file {
			t.Errorf("MovieFile(%q) = %q, want %q", tc.title, got, tc.file)
		}
	}
}

func TestEpisodeNames(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		series                              string
		season, number                      int
		episode, quality, ext, folder, file string
	}{
		{"plain", "House", 2, 3, "Autopsy", "WEBDL-1080p", ".mkv", "Season 02", "House - S02E03 - Autopsy [WEBDL-1080p].mkv"},
		{"colon in series", "CSI: Vegas", 1, 6, "What Happens in Vegas", "HDTV-720p", ".mkv", "Season 01", "CSI - Vegas - S01E06 - What Happens in Vegas [HDTV-720p].mkv"},
		{"trailing punctuation trimmed", "Show", 1, 1, "Who Is It?", "WEBDL-720p", ".mkv", "Season 01", "Show - S01E01 - Who Is It [WEBDL-720p].mkv"},
		{"empty title is TBA", "Show", 10, 12, "", "WEBDL-720p", ".mkv", "Season 10", "Show - S10E12 - TBA [WEBDL-720p].mkv"},
		{"three digit episode", "Daily", 2024, 100, "Hundred", "HDTV-1080p", ".mkv", "Season 2024", "Daily - S2024E100 - Hundred [HDTV-1080p].mkv"},
		{"specials", "Show", 0, 1, "Pilot Cut", "Bluray-1080p Remux", ".mkv", "Specials", "Show - S00E01 - Pilot Cut [Bluray-1080p Remux].mkv"},
		{"reserved device name", "Con.Game", 1, 1, "One", "WEBDL-1080p", ".mkv", "Season 01", "Con_Game - S01E01 - One [WEBDL-1080p].mkv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SeasonFolder(tc.season); got != tc.folder {
				t.Errorf("SeasonFolder = %q, want %q", got, tc.folder)
			}
			if got := EpisodeFile(tc.series, tc.season, tc.number, tc.episode, tc.quality, tc.ext); got != tc.file {
				t.Errorf("EpisodeFile = %q, want %q", got, tc.file)
			}
		})
	}
}

func TestSeriesFolder(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"House", "House"},
		{"CSI: Vegas", "CSI - Vegas"},
		{"Con.Game", "Con_Game"},
		{"What If...?", "What If.!"},
	} {
		if got := SeriesFolder(tc.in); got != tc.want {
			t.Errorf("SeriesFolder(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEpisodeFileFitsTheFileNameLimit(t *testing.T) {
	long := ""
	for len(long) < 400 {
		long += "Very Long Episode Title "
	}
	got := EpisodeFile("Show", 1, 1, long, "WEBDL-1080p", ".mkv")
	if len(got) > 255 {
		t.Fatalf("file name is %d bytes, over the 255-byte limit: %q", len(got), got)
	}
	const tail = "... [WEBDL-1080p].mkv"
	if len(got) < len(tail) || got[len(got)-len(tail):] != tail {
		t.Errorf("truncated name should end with an ellipsis before the quality tag, got %q", got)
	}
	multi := EpisodeFile("Show", 1, 1, "Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé Ünïcödé Tïtlé", "WEBDL-1080p", ".mkv")
	if len(multi) > 255 {
		t.Errorf("multi-byte name is %d bytes, over the limit", len(multi))
	}
}

func TestMultiEpisodeFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		numbers []int
		titles  []string
		want    string
	}{
		{"single is the episode file", []int{1}, []string{"Ep1"}, "Show - S01E01 - Ep1 [WEBDL-1080p].mkv"},
		{"two contiguous", []int{1, 2}, []string{"Ep1", "Ep2"}, "Show - S01E01-E02 - Ep1 + Ep2 [WEBDL-1080p].mkv"},
		{"three contiguous", []int{1, 2, 3}, []string{"Ep1", "Ep2", "Ep3"}, "Show - S01E01-E03 - Ep1 + Ep2 + Ep3 [WEBDL-1080p].mkv"},
		{"non-contiguous", []int{1, 3}, []string{"Ep1", "Ep3"}, "Show - S01E01E03 - Ep1 + Ep3 [WEBDL-1080p].mkv"},
		{"a gap after a run", []int{1, 2, 4}, []string{"A", "B", "D"}, "Show - S01E01E02E04 - A + B + D [WEBDL-1080p].mkv"},
		{"identical titles appear once", []int{1, 2}, []string{"Pilot", "Pilot"}, "Show - S01E01-E02 - Pilot [WEBDL-1080p].mkv"},
		{"numbered parts stay distinct", []int{1, 2}, []string{"Pilot (1)", "Pilot (2)"}, "Show - S01E01-E02 - Pilot (1) + Pilot (2) [WEBDL-1080p].mkv"},
		{"unsorted numbers keep their titles", []int{2, 1}, []string{"Two", "One"}, "Show - S01E01-E02 - One + Two [WEBDL-1080p].mkv"},
		{"trailing punctuation trimmed per title", []int{1, 2}, []string{"Who Is It?", "Why."}, "Show - S01E01-E02 - Who Is It + Why [WEBDL-1080p].mkv"},
		{"empty titles are TBA once", []int{1, 2}, []string{"", " "}, "Show - S01E01-E02 - TBA [WEBDL-1080p].mkv"},
		{"fewer titles than numbers", []int{1, 2}, []string{"One"}, "Show - S01E01-E02 - One + TBA [WEBDL-1080p].mkv"},
		{"duplicate numbers collapse", []int{1, 1, 2}, []string{"One", "One", "Two"}, "Show - S01E01-E02 - One + Two [WEBDL-1080p].mkv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MultiEpisodeFile("Show", 1, tc.numbers, tc.titles, "WEBDL-1080p", ".mkv"); got != tc.want {
				t.Errorf("MultiEpisodeFile = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMultiEpisodeFileOfASingleEpisodeIsEpisodeFile(t *testing.T) {
	long := strings.Repeat("Very Long Episode Title ", 20)
	for _, title := range []string{"Autopsy", "", "Who Is It?", long} {
		want := EpisodeFile("House", 2, 3, title, "WEBDL-1080p", ".mkv")
		if got := MultiEpisodeFile("House", 2, []int{3}, []string{title}, "WEBDL-1080p", ".mkv"); got != want {
			t.Errorf("single %q: %q, want %q", title, got, want)
		}
	}
}

func TestMultiEpisodeFileFitsTheFileNameLimit(t *testing.T) {
	long := strings.Repeat("Very Long Episode Title ", 12)
	for _, tc := range []struct {
		name   string
		titles []string
	}{
		{"ascii", []string{long + "One", long + "Two", long + "Three"}},
		{"multi-byte", []string{strings.Repeat("Ünïcödé Tïtlé ", 12), strings.Repeat("Çöñtïnüé ", 20)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			numbers := make([]int, len(tc.titles))
			for i := range numbers {
				numbers[i] = i + 1
			}
			got := MultiEpisodeFile("Show", 1, numbers, tc.titles, "WEBDL-1080p", ".mkv")
			if len(got) > 255 {
				t.Fatalf("file name is %d bytes, over the 255-byte limit: %q", len(got), got)
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncation cut a character in half: %q", got)
			}
			const tail = "... [WEBDL-1080p].mkv"
			if !strings.HasSuffix(got, tail) {
				t.Errorf("truncated name should end with an ellipsis before the quality tag, got %q", got)
			}
			if !strings.HasPrefix(got, "Show - S01E01-E0") {
				t.Errorf("the episode token must survive truncation: %q", got)
			}
		})
	}
}
