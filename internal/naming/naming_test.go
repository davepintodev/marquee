// Portions of these cases are translated from the Sonarr and Radarr test
// suites (https://github.com/Sonarr/Sonarr, https://github.com/Radarr/Radarr),
// which are licensed under the GNU General Public License v3.0. Cases marked
// "adapted" differ from upstream only in the function that is called.

package naming

import "testing"

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
