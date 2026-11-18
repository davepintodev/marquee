// Quality names are translated from the Quality definitions and QualityParser
// of Sonarr and Radarr (https://github.com/Sonarr/Sonarr,
// https://github.com/Radarr/Radarr), GNU General Public License v3.0.

package naming

import "testing"

func TestQualityName(t *testing.T) {
	// Inputs are the resolution and quality strings of an AIOStreams parsed file.
	for _, tc := range []struct {
		resolution, quality string
		movie, series       string
	}{
		{"2160p", "BluRay REMUX", "Remux-2160p", "Bluray-2160p Remux"},
		{"1080p", "BluRay REMUX", "Remux-1080p", "Bluray-1080p Remux"},
		{"2160p", "BluRay", "Bluray-2160p", "Bluray-2160p"},
		{"1080p", "BluRay", "Bluray-1080p", "Bluray-1080p"},
		{"720p", "BluRay", "Bluray-720p", "Bluray-720p"},
		{"576p", "BluRay", "Bluray-576p", "Bluray-576p"},
		{"480p", "BluRay", "Bluray-480p", "Bluray-480p"},
		{"720p", "BluRay REMUX", "Bluray-720p", "Bluray-720p"},
		{"Unknown", "BluRay REMUX", "Remux-1080p", "Bluray-1080p Remux"},
		{"Unknown", "BluRay", "Bluray-720p", "Bluray-720p"},
		{"2160p", "WEB-DL", "WEBDL-2160p", "WEBDL-2160p"},
		{"1080p", "WEB-DL", "WEBDL-1080p", "WEBDL-1080p"},
		{"720p", "WEB-DL", "WEBDL-720p", "WEBDL-720p"},
		{"480p", "WEB-DL", "WEBDL-480p", "WEBDL-480p"},
		{"Unknown", "WEB-DL", "WEBDL-480p", "WEBDL-480p"},
		{"1080p", "WEBRip", "WEBRip-1080p", "WEBRip-1080p"},
		{"2160p", "WEBRip", "WEBRip-2160p", "WEBRip-2160p"},
		{"720p", "WEBRip", "WEBRip-720p", "WEBRip-720p"},
		{"1080p", "HDRip", "WEBRip-1080p", "WEBRip-1080p"},
		{"1080p", "HC HD-Rip", "WEBRip-1080p", "WEBRip-1080p"},
		{"1080p", "HDTV", "HDTV-1080p", "HDTV-1080p"},
		{"720p", "HDTV", "HDTV-720p", "HDTV-720p"},
		{"2160p", "HDTV", "HDTV-2160p", "HDTV-2160p"},
		{"480p", "HDTV", "SDTV", "SDTV"},
		{"480p", "DVDRip", "DVD", "DVD"},
		{"Unknown", "DVD", "DVD", "DVD"},
		{"1080p", "BDRip", "Bluray-1080p", "Bluray-1080p"},
		{"720p", "BRRip", "Bluray-720p", "Bluray-720p"},
		{"1440p", "WEB-DL", "WEBDL-1080p", "WEBDL-1080p"},
		{"1080p", "", "HDTV-1080p", "HDTV-1080p"},
		{"720p", "Unknown", "HDTV-720p", "HDTV-720p"},
		{"480p", "", "SDTV", "SDTV"},
		{"", "", "Unknown", "Unknown"},
		{"Unknown", "Unknown", "Unknown", "Unknown"},
		{"1080p", "CAM", "CAM", "Unknown"},
		{"1080p", "TS", "TELESYNC", "Unknown"},
		{"1080p", "TC", "TELECINE", "Unknown"},
		{"480p", "SCR", "DVDSCR", "Unknown"},
		{"2160p", "bluray remux", "Remux-2160p", "Bluray-2160p Remux"},
	} {
		if got := QualityName(KindMovie, tc.resolution, tc.quality); got != tc.movie {
			t.Errorf("movie %q %q = %q, want %q", tc.resolution, tc.quality, got, tc.movie)
		}
		if got := QualityName(KindSeries, tc.resolution, tc.quality); got != tc.series {
			t.Errorf("series %q %q = %q, want %q", tc.resolution, tc.quality, got, tc.series)
		}
	}
}
