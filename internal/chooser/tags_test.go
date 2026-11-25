package chooser_test

import (
	"reflect"
	"testing"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/chooser"
)

func TestExtractTagsFromFileNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		want []string
	}{
		{"nothing", "Movie.2021.1080p.WEB-DL.mkv", nil},
		{"truehd", "Movie.2021.2160p.BluRay.TrueHD.7.1.x265.mkv", []string{"truehd", "hevc"}},
		{"truehd atmos stays truehd", "Movie.2160p.WEB-DL.TrueHD.Atmos.7.1.mkv", []string{"truehd"}},
		{"truehd with a space", "Movie 2160p WEB-DL True HD 7.1.mkv", []string{"truehd"}},
		{"dts-hd ma", "Movie.1080p.BluRay.DTS-HD.MA.5.1.x264.mkv", []string{"dts-hd", "avc"}},
		{"dts-hd ma dotted", "Movie.1080p.BluRay.DTS-HD.MA.mkv", []string{"dts-hd"}},
		{"dts-hd hra", "Movie.1080p.BluRay.DTS-HD HRA.mkv", []string{"dts-hd"}},
		{"dtshd", "Movie.1080p.BluRay.DTSHD.mkv", []string{"dts-hd"}},
		{"dts-x colon", "Movie.2160p.BluRay.DTS:X.7.1.mkv", []string{"dts-x"}},
		{"dts-x dash", "Movie.2160p.BluRay.DTS-X.mkv", []string{"dts-x"}},
		{"dtsx", "Movie.2160p.BluRay.DTSX.mkv", []string{"dts-x"}},
		{"dts-hd and dts-x together", "Movie.2160p.DTS-HD.MA.DTS-X.mkv", []string{"dts-hd", "dts-x"}},
		{"plain dts", "Movie.1080p.BluRay.DTS.5.1.mkv", []string{"dts"}},
		{"plain dts glued to channels", "Movie.1080p.BluRay.DTS5.1.mkv", []string{"dts"}},
		{"dts-es is plain dts", "Movie.1080p.BluRay.DTS-ES.mkv", []string{"dts"}},
		{"dd+ atmos", "Movie.2160p.WEB-DL.DD+.Atmos.5.1.mkv", []string{"dd+-atmos", "dd+"}},
		{"ddp atmos", "Movie.2160p.WEB-DL.DDP5.1.Atmos.mkv", []string{"dd+-atmos", "dd+"}},
		{"eac3 atmos", "Movie.2160p.WEB-DL.EAC3.Atmos.mkv", []string{"dd+-atmos", "dd+"}},
		{"e-ac-3 atmos", "Movie.2160p.WEB-DL.E-AC-3.Atmos.mkv", []string{"dd+-atmos", "dd+"}},
		{"dd+ glued to channels", "Movie.1080p.WEB-DL.DD+5.1.mkv", []string{"dd+"}},
		{"ddp", "Movie.1080p.WEB-DL.DDP5.1.mkv", []string{"dd+"}},
		{"eac3", "Movie.1080p.WEB-DL.EAC3.mkv", []string{"dd+"}},
		{"e-ac-3", "Movie.1080p.WEB-DL.E-AC-3.mkv", []string{"dd+"}},
		{"dd5.1", "Movie.1080p.WEB-DL.DD5.1.mkv", []string{"dd"}},
		{"dd2.0", "Movie.1080p.WEB-DL.DD2.0.mkv", []string{"dd"}},
		{"bare dd", "Movie.1080p.WEB-DL.DD.mkv", []string{"dd"}},
		{"ac3", "Movie.1080p.WEB-DL.AC3.mkv", []string{"dd"}},
		{"ac-3", "Movie.1080p.WEB-DL.AC-3.mkv", []string{"dd"}},
		{"dd+ is not dd", "Movie.1080p.WEB-DL.DD+.mkv", []string{"dd+"}},
		{"atmos alone", "Movie.1080p.WEB-DL.Atmos.mkv", nil},
		{"aac", "Movie.1080p.WEB-DL.AAC2.0.mkv", []string{"aac"}},
		{"flac", "Movie.1080p.BluRay.FLAC.2.0.mkv", []string{"flac"}},
		{"opus", "Movie.1080p.WEB-DL.Opus.mkv", []string{"opus"}},
		{"dv", "Movie.2160p.WEB-DL.DV.mkv", []string{"dv"}},
		{"dovi", "Movie.2160p.WEB-DL.DoVi.mkv", []string{"dv"}},
		{"dolby vision", "Movie.2160p.WEB-DL.Dolby.Vision.mkv", []string{"dv"}},
		{"dolby vision with a space", "Movie 2160p WEB-DL Dolby Vision.mkv", []string{"dv"}},
		{"dvd is not dv", "Movie.DVDRip.mkv", nil},
		{"hdr10+", "Movie.2160p.WEB-DL.HDR10+.mkv", []string{"hdr10+", "hdr"}},
		{"hdr10plus", "Movie.2160p.WEB-DL.HDR10Plus.mkv", []string{"hdr10+", "hdr"}},
		{"hdr10p", "Movie.2160p.WEB-DL.HDR10P.mkv", []string{"hdr10+", "hdr"}},
		{"hdr10+ glued to dv", "Movie.2160p.WEB-DL.HDR10+DV.mkv", []string{"dv", "hdr10+", "hdr"}},
		{"hdr10", "Movie.2160p.WEB-DL.HDR10.mkv", []string{"hdr10"}},
		{"hdr", "Movie.2160p.WEB-DL.HDR.mkv", []string{"hdr"}},
		{"av1", "Movie.1080p.WEB-DL.AV1.mkv", []string{"av1"}},
		{"hevc", "Movie.1080p.WEB-DL.HEVC.mkv", []string{"hevc"}},
		{"x265", "Movie.1080p.WEB-DL.x265.mkv", []string{"hevc"}},
		{"h.265", "Movie.1080p.WEB-DL.H.265.mkv", []string{"hevc"}},
		{"h265", "Movie.1080p.WEB-DL.H265.mkv", []string{"hevc"}},
		{"avc", "Movie.1080p.WEB-DL.AVC.mkv", []string{"avc"}},
		{"x264", "Movie.1080p.WEB-DL.x264.mkv", []string{"avc"}},
		{"h.264", "Movie.1080p.WEB-DL.H.264.mkv", []string{"avc"}},
		{"h264", "Movie.1080p.WEB-DL.H264.mkv", []string{"avc"}},
		{"lowercase", "movie.2160p.web-dl.ddp5.1.atmos.hdr10+.hevc.mkv", []string{"dd+-atmos", "dd+", "hdr10+", "hdr", "hevc"}},
		{"words that merely contain a token", "Added.Daddy.Flacon.Operus.Advent.mkv", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := chooser.ExtractTags(aiostreams.Stream{Filename: tc.file})
			if !sameTags(got, tc.want) {
				t.Errorf("%q: got %v, want %v", tc.file, got, tc.want)
			}
		})
	}
}

func TestExtractTagsReadsTheFolderNameAndParsedFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    aiostreams.Stream
		want []string
	}{
		{"folder name when the file name says nothing",
			aiostreams.Stream{Filename: "movie.mkv", FolderName: "Movie.2160p.BluRay.DTS-HD.MA.7.1.x265"},
			[]string{"dts-hd", "hevc"}},
		{"parsed audio and visual tags with no file name",
			aiostreams.Stream{ParsedFile: &aiostreams.ParsedFile{AudioTags: []string{"DD+", "Atmos"}, VisualTags: []string{"HDR10+", "DV"}, Encode: "HEVC"}},
			[]string{"dd+-atmos", "dd+", "dv", "hdr10+", "hdr", "hevc"}},
		{"parsed truehd and atmos is not dd+ atmos",
			aiostreams.Stream{ParsedFile: &aiostreams.ParsedFile{AudioTags: []string{"TrueHD", "Atmos"}}},
			[]string{"truehd"}},
		{"parsed dts-hd ma",
			aiostreams.Stream{ParsedFile: &aiostreams.ParsedFile{AudioTags: []string{"DTS-HD MA"}}},
			[]string{"dts-hd"}},
		{"parsed dts:x",
			aiostreams.Stream{ParsedFile: &aiostreams.ParsedFile{AudioTags: []string{"DTS:X"}}},
			[]string{"dts-x"}},
		{"parsed and file name tags are merged",
			aiostreams.Stream{Filename: "Movie.2160p.TrueHD.mkv", ParsedFile: &aiostreams.ParsedFile{VisualTags: []string{"DV"}, Encode: "AV1"}},
			[]string{"truehd", "dv", "av1"}},
		{"hdr+dv parsed tag", aiostreams.Stream{ParsedFile: &aiostreams.ParsedFile{VisualTags: []string{"HDR+DV"}}},
			[]string{"dv", "hdr"}},
		{"no information at all", aiostreams.Stream{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chooser.ExtractTags(tc.s); !sameTags(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExtractTagsDolbyVisionProfile7Heuristic(t *testing.T) {
	pf := func(res, quality string) *aiostreams.ParsedFile {
		return &aiostreams.ParsedFile{Resolution: res, Quality: quality, VisualTags: []string{"DV"}}
	}
	for _, tc := range []struct {
		name   string
		s      aiostreams.Stream
		wantP7 bool
	}{
		{"dv remux", aiostreams.Stream{Filename: "Movie.2160p.BluRay.REMUX.DV.mkv", ParsedFile: pf("2160p", "BluRay REMUX")}, true},
		{"dv remux 1080p", aiostreams.Stream{ParsedFile: pf("1080p", "BluRay REMUX")}, true},
		{"dv remux with explicit p8 is still remux", aiostreams.Stream{Filename: "Movie.DV.P8.mkv", ParsedFile: pf("2160p", "BluRay REMUX")}, true},
		{"dv remux known only from the file name", aiostreams.Stream{Filename: "Movie.2160p.UHD.BluRay.Remux.DV.HDR10.mkv"}, true},
		{"dv bluray encode", aiostreams.Stream{ParsedFile: pf("2160p", "BluRay")}, true},
		{"dv bluray known only from the file name", aiostreams.Stream{Filename: "Movie.2160p.BluRay.DV.x265.mkv"}, true},
		{"dv bdrip", aiostreams.Stream{Filename: "Movie.2160p.BDRip.DV.mkv"}, true},
		{"dv bluray with explicit p8", aiostreams.Stream{Filename: "Movie.2160p.BluRay.DV.P8.mkv", ParsedFile: pf("2160p", "BluRay")}, false},
		{"dv bluray with profile 8", aiostreams.Stream{Filename: "Movie.2160p.BluRay.DV.Profile.8.mkv", ParsedFile: pf("2160p", "BluRay")}, false},
		{"dv bluray with dv.p8", aiostreams.Stream{Filename: "Movie.2160p.BluRay.DV.P8.HDR.mkv"}, false},
		{"dv bluray with web in the name", aiostreams.Stream{Filename: "Movie.2160p.BluRay.WEB-DL.DV.mkv"}, false},
		{"dv bluray hybrid web", aiostreams.Stream{ParsedFile: pf("2160p", "WEB-DL"), Filename: "Movie.BluRay.Hybrid.WEB.DV.mkv"}, false},
		{"dv web-dl", aiostreams.Stream{ParsedFile: pf("2160p", "WEB-DL"), Filename: "Movie.2160p.WEB-DL.DV.mkv"}, false},
		{"dv webrip", aiostreams.Stream{Filename: "Movie.2160p.WEBRip.DV.mkv"}, false},
		{"dv with no source", aiostreams.Stream{ParsedFile: &aiostreams.ParsedFile{VisualTags: []string{"DV"}}}, false},
		{"explicit p7 on a web release", aiostreams.Stream{Filename: "Movie.2160p.WEB-DL.DV.P7.mkv", ParsedFile: pf("2160p", "WEB-DL")}, true},
		{"explicit dvp7", aiostreams.Stream{Filename: "Movie.2160p.WEB-DL.DVP7.mkv"}, true},
		{"explicit profile 7", aiostreams.Stream{Filename: "Movie.2160p.WEB-DL.DV.Profile 7.mkv"}, true},
		{"explicit profile.7", aiostreams.Stream{Filename: "Movie.2160p.WEB-DL.DV.Profile.7.mkv"}, true},
		{"fel", aiostreams.Stream{Filename: "Movie.2160p.WEB-DL.DV.FEL.mkv"}, true},
		{"folder name carries p7", aiostreams.Stream{Filename: "movie.mkv", FolderName: "Movie.2160p.DV.P7", ParsedFile: pf("2160p", "WEB-DL")}, true},
		{"p7 without dv is not a dv tag", aiostreams.Stream{Filename: "Movie.2160p.WEB-DL.P7.mkv"}, false},
		{"remux without dv", aiostreams.Stream{ParsedFile: &aiostreams.ParsedFile{Resolution: "2160p", Quality: "BluRay REMUX", VisualTags: []string{"HDR10"}}}, false},
		{"bluray without dv", aiostreams.Stream{Filename: "Movie.2160p.BluRay.HDR10.mkv"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tags := chooser.ExtractTags(tc.s)
			if has(tags, "dv-p7") != tc.wantP7 {
				t.Errorf("dv-p7 = %v, want %v (tags %v)", has(tags, "dv-p7"), tc.wantP7, tags)
			}
			if tc.wantP7 && !has(tags, "dv") {
				t.Errorf("dv-p7 must come with dv: %v", tags)
			}
		})
	}
}

func TestExtractTagsAreDeterministicAndKnown(t *testing.T) {
	s := aiostreams.Stream{Filename: "Movie.2160p.BluRay.REMUX.DV.HDR10+.TrueHD.Atmos.DTS-HD.MA.x265.mkv"}
	first := chooser.ExtractTags(s)
	for range 20 {
		if got := chooser.ExtractTags(s); !reflect.DeepEqual(got, first) {
			t.Fatalf("order changed: %v then %v", first, got)
		}
	}
	want := []string{"truehd", "dts-hd", "dv-p7", "dv", "hdr10+", "hdr", "hevc"}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("got %v, want %v", first, want)
	}
}

func has(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

func sameTags(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, w := range want {
		if !has(got, w) {
			return false
		}
	}
	return true
}
