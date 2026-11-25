package naming_test

import (
	"slices"
	"testing"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/naming"
)

func TestCanonicalQuality(t *testing.T) {
	for _, tc := range []struct{ resolution, quality, want string }{
		{"2160p", "BluRay REMUX", "Remux-2160p"},
		{"1080p", "BluRay REMUX", "Remux-1080p"},
		{"720p", "BluRay REMUX", "Bluray-720p"},
		{"2160p", "BluRay", "Bluray-2160p"},
		{"1080p", "BluRay", "Bluray-1080p"},
		{"576p", "BluRay", "Bluray-576p"},
		{"480p", "BluRay", "Bluray-480p"},
		{"2160p", "WEB-DL", "WEBDL-2160p"},
		{"720p", "WEBRip", "WEBRip-720p"},
		{"480p", "WEB-DL", "WEBDL-480p"},
		{"480p", "WEBRip", "WEBRip-480p"},
		{"1440p", "WEB-DL", "WEBDL-1080p"},
		{"2160p", "HDTV", "HDTV-2160p"},
		{"1080p", "HDTV", "HDTV-1080p"},
		{"480p", "HDTV", "SDTV"},
		{"480p", "DVDRip", "DVD"},
		{"1080p", "CAM", "CAM"},
		{"1080p", "TS", "TELESYNC"},
		{"1080p", "TC", "TELECINE"},
		{"480p", "SCR", "DVDSCR"},
		{"", "", "Unknown"},
		{"Unknown", "Unknown", "Unknown"},
	} {
		if got := naming.CanonicalQuality(tc.resolution, tc.quality); got != tc.want {
			t.Errorf("CanonicalQuality(%q, %q) = %q, want %q", tc.resolution, tc.quality, got, tc.want)
		}
	}
}

func TestCanonicalQualityIsAlwaysAProfileTier(t *testing.T) {
	tiers := config.QualityTiers()
	for _, res := range []string{"", "Unknown", "360p", "480p", "576p", "720p", "1080p", "1440p", "2160p", "4320p"} {
		for _, q := range []string{"", "Unknown", "BluRay", "BluRay REMUX", "WEB-DL", "WEBRip", "HDRip", "HDTV", "DVD", "BDRip", "CAM", "TS", "TC", "SCR"} {
			if got := naming.CanonicalQuality(res, q); !slices.Contains(tiers, got) {
				t.Errorf("CanonicalQuality(%q, %q) = %q, not a tier a profile can name", res, q, got)
			}
		}
	}
}
