// Test cases translated from Sonarr's SingleEpisodeParserFixture and
// MultiEpisodeParserFixture (https://github.com/Sonarr/Sonarr), licensed
// under the GNU General Public License v3.0, as is marquee. Only the series
// title column is dropped: marquee reads the folder name instead. Cases for
// the formats the parser leaves out are not carried over (see parse.go).

package naming

import (
	"slices"
	"testing"
)

func rng(from, to int) []int {
	var out []int
	for n := from; n <= to; n++ {
		out = append(out, n)
	}
	return out
}

func TestParseEpisodeTokenSingleEpisodes(t *testing.T) {
	for _, c := range []struct {
		name            string
		season, episode int
	}{
		{"Series.With.Title.S02E15", 2, 15},
		{"Series.Title.4x05.HDTV.XviD-LOL", 4, 5},
		{"Series.Title.S03E06.DVDRip.XviD-WiDE", 3, 6},
		{"Series.Title.S10E27.WS.DSR.XviD-2HD", 10, 27},
		{"Series.Title.2010.S02E14.HDTV.XviD-LOL", 2, 14},
		{"Series Title 0 S01E19 720p WEB DL DD5 1 H 264 NT", 1, 19},
		{"The Series Title S01E14 A Message Back 720p WEB DL DD5 1 H264 SURFER", 1, 14},
		{"Series Title S01E07 WS PDTV XviD FUtV", 1, 7},
		{"S03E09 WS PDTV XviD FUtV", 3, 9},
		{"5x10 WS PDTV XviD FUtV", 5, 10},
		{"Series.Title.1995.S03E20.HDTV.XviD-LOL", 3, 20},
		{"Series.Title.S03E115.DVDRip.XviD-OSiTV", 3, 115},
		{`Series Title - S02E21 - 94 Meetings - 720p TV.mkv`, 2, 21},
		{`24-7 Series - Title - Road to the Sonarr - S01E03 - Episode 3.mkv`, 1, 3},
		{"Series.Title.Inc.S03E19.DVDRip.\"XviD\"-OSiTV", 3, 19},
		{"Series Title-0 (2010) - 1x05 - Missing Title", 1, 5},
		{"Series Title - S06E13 - 5 to 9 [DVD]", 6, 13},
		{"Series Title - S02E21 - 18-5-4", 2, 21},
		{"Series.Title.S01E07.21.0.Jump.Street.720p.WEB-DL.DD5.1.h.264-KiNGS", 1, 7},
		{"Series Title - 10x12 - 24 Hour Propane People [SDTV]", 10, 12},
		{"Series Title S01E06 3 Beers For Batali DVDRip XviD SPRiNTER", 1, 6},
		{"24-7 Series-Title - Road to the Sonarr - S01E01 - Part 1", 1, 1},
		{"S6E02-Unwrapped-(Playing With Food) - [DarkData]", 6, 2},
		{"S06E03-Unwrapped-(Number Ones Unwrapped) - [DarkData]", 6, 3},
		{"Series Title S02E21 18 5 4 720p WEB DL DD5 1 h 264 EbP", 2, 21},
		{"01x04 - Halloween, Part 1 - 720p WEB-DL", 1, 4},
		{"series.s03.e05.ws.dvdrip.xvid-m00tv", 3, 5},
		{"Series Title - S1936E18 - I Love to Singa", 1936, 18},
		{"Series_Title!_-_7x6_-_The_Scarlett_Getter_[SDTV]", 7, 6},
		{"Series_Title_-_1x1_-_Live_and_Learn_[HDTV-720p]", 1, 1},
		{"Top Series - 07x03 - 2005.11.70", 7, 3},
		{"Series.S04E09.Swan.Song.1080p.WEB-DL.DD5.1.H.264-ECI", 4, 9},
		{"S08E20 50-50 Carla [DVD]", 8, 20},
		{"Series S08E20 50-50 Carla [DVD]", 8, 20},
		{"S02E10 6-50 to SLC [SDTV]", 2, 10},
		{"Developers & Coders S02E10 6-50 to SLC [SDTV]", 2, 10},
		{"The_Big_Series_Title_-_6x12_-_The_Code_Review_Equivalency_[HDTV-720p]", 6, 12},
		{"Series_Title.19x06.720p_HDTV_x264-FoV", 19, 6},
		{"Series.S03E10.Alexandra.720p.WEB-DL.AAC2.0.H.264-CROM.mkv", 3, 10},
		{`(Series of Title s03 e - "Series of Title Season 3 Episode 10"`, 3, 10},
		{"Series.Hunters.Galactic.S05E607.720p.hdtv.x264", 5, 607},
		{"Series.Time.With.Dev1.And.Dev2.S01E20.720p.BluRay.x264-DEiMOS", 1, 20},
		{"Series.S01E04.2-45.PM.[HDTV-720p].mkv", 1, 4},
		{"S01E04", 1, 4},
		{"1x04", 1, 4},
		{"10.Lines.You.Know.About.Code.S02E04.Prohibition.HDTV.XviD-AFG", 2, 4},
		{"30 Series - S01E01 - Pilot.avi", 1, 1},
		{"666 Series Title - S01E01", 1, 1},
		{"Series 13 - S01E01", 1, 1},
		{"Don't Ever Trust The B---- in Code 23.S01E01", 1, 1},
		{"Warehouse.13.S01E01", 1, 1},
		{"42 S01E01", 1, 1},
		{"69.S01E01", 1, 1},
		{"Series - 2x12 - The Choice [HDTV-1080p].mkv", 2, 12},
		{"Series - 2x4 - New Car Smell [HDTV-1080p].mkv", 2, 4},
		{"Top Series - 06x11 - 2005.08.07", 6, 11},
		{"The_Series_US_s06e19_04.28.2014_hdtv.x264.Poke.mp4", 6, 19},
		{"2009x09 [SDTV].avi", 2009, 9},
		{"S2009E09 [SDTV].avi", 2009, 9},
		{"Series Week S2009E09 [SDTV].avi", 2009, 9},
		{"[Impatience] Series - 0x01 [720p][34073169].mkv", 0, 1},
		{"Series.Title.S15.E06.City.Sushi", 15, 6},
		{"Series Title - S15 E06 - City Code", 15, 6},
		{"Series S1-E1-WEB-DL-1080p-NZBgeek", 1, 1},
		{"Series S1E1-WEB-DL-1080p-NZBgeek", 1, 1},
		{"Series.S010E16.720p.HDTV.X264-DIMENSION", 10, 16},
		{"[ www.Torrenting.com ] - Series.2012.S02E17.720p.HDTV.X264-DIMENSION", 2, 17},
		{"Series.2012.S02E18.720p.HDTV.X264-DIMENSION.mkv", 2, 18},
		{"Series - Season 1 - Episode 01 (Resolution).avi", 1, 1},
		{"5x09 - 100 [720p WEB-DL].mkv", 5, 9},
		{"1x03 - 274 [1080p BluRay].mkv", 1, 3},
		{"1x03 - The 112th Congress [1080p BluRay].mkv", 1, 3},
		{"Series.2012.S02E14.720p.HDTV.X264-DIMENSION [PublicHD].mkv", 2, 14},
		{"Series.2009.S06E03.720p.HDTV.X264-DIMENSION [PublicHD].mkv", 6, 3},
		{"20-1.2014.S02E01.720p.HDTV.x264-CROOKS", 2, 1},
		{"Series - S01E09 - Debate 109", 1, 9},
		{"Series - S02E02 - My Maserati Does 185", 2, 2},
		{"6x13 - The Series Show 100th Episode Special", 6, 13},
		{"The Series And the Show - S41 E10478 - 2014-08-15", 41, 10478},
		{"The Series And the Show - S42 E10591 - 2015-01-27", 42, 10591},
		{"Series Title [1x05] Episode Title", 1, 5},
		{"Series Title [S01E05] Episode Title", 1, 5},
		{"Series Title Season 01 Episode 05 720p", 1, 5},
		{"The Series And the Show - S42 E10713 - 2015-07-20.mp4", 42, 10713},
		{"Series - 01x02 - The Rooster Prince - [itz_theo]", 1, 2},
		{"Series (2009) - [06x16] - Room 147.mp4", 6, 16},
		{"grp-zoos01e11-1080p", 1, 11},
		{"grp-zoo-s01e11-1080p", 1, 11},
		{"Series!.S2016E14.2016-01-20.avi", 2016, 14},
		{"Judge Developer 2016 02 25 S20E142", 20, 142},
		{"Red Show - S02 - E06 - Parallel Series", 2, 6},
		{"The.100000.Series.Title.2016.S01E05.720p.HDTV.x264-W4F", 1, 5},
		{"Series S01E02 (22 October 2016) HDTV 720p [Webrip]", 1, 2},
		{"Series - S2016E231", 2016, 231},
		{"Series - 2016x231", 2016, 231},
		{"Short.Series.S26E022.HDTV.x264-FiHTV", 26, 22},
		{"Super.Series.S01.Ep06.1080p.BluRay.DTS.x264-MiR", 1, 6},
		{"Series 104 - S01E07 The Developers [SDTV]", 1, 7},
		{"Plus Series la title - S14E3533 FRENCH WEBRIP H.264 AAC (09.05.2018)", 14, 3533},
		{"The Series - S01E02 - Earth Skills HDTV-1080p AVC DTS [EN+FR+ES+PT+DA+FI+NB+SV]", 1, 2},
		{"Series Title - S01E01 - Day 100 [SDTV]", 1, 1},
		{"Series.Title.S01.Ep.01.English.AC3.DL.1080p.BluRay-Sonarr", 1, 1},
		{"Series.Title.S01.E.01.English.AC3.DL.1080p.BluRay-Sonarr", 1, 1},
		{"Series.Title.S01.Ep01.English.AC3.DL.1080p.BluRay-Sonarr", 1, 1},
		{"Series.Title.S01.E01.English.AC3.DL.1080p.BluRay-Sonarr", 1, 1},
		{"Series.Title.S01EP01.English.AC3.DL.1080p.BluRay-Sonarr", 1, 1},
		{"Series Title - S01E01 [AC3 5.1 Castellano][www.descargas2020.org]", 1, 1},
		{"Series Title - [02x01] - Episode 1", 2, 1},
		{"Series.Title.Of.S01E01.xyz", 1, 1},
		{"[RlsGrp] Series Title - S01E27 - 24-Hour", 1, 27},
		{"Series Title - S02E01 1920x910", 2, 1},
		{"Anime Title - S2020E1527 [1527] [2020-10-11] - Episode Title", 2020, 1527},
		{"Anime Title - S2010E994 [0994] [2010-02-28] - Episode Title [x264 720p][AAC 2ch][HS][Shion+GakiDave]", 2010, 994},
		{"Series Title 1x1", 1, 1},
		{"1x1", 1, 1},
		{"Series Title [2022] [S25E13] [PL] [720p] [WEB-DL-CZRG] [x264] ", 25, 13},
		{"[Anime Chap] Anime Title! S01E09 [WEB 1080p] {OP & ED Lyrics} - Episode 9 (The Eminence in Shadow)", 1, 9},
		{"SeriesTitle-S16E08-10426008-0.mkv", 16, 8},
		{"Series-S07E12-31st_Century_Fox-[Bluray-1080p].mkv", 7, 12},
		{"TheTitle-S12E13-3_Acts_of_God.mkv", 12, 13},
		{"Босх: Спадок (S2E1) / Series: Legacy (S2E1) (2023) WEB-DL 1080p Ukr/Eng | sub Eng", 2, 1},
		{"Босх: Спадок / Series: Legacy / S2E1 of 10 (2023) WEB-DL 1080p Ukr/Eng | sub Eng", 2, 1},
		{"Titles.s06e01.1999.BDRip.1080p.Ukr.Eng.AC3.Hurtom.TNU.Tenax555", 6, 1},
		{"[Judas] Series Title (2024) - S01E14", 1, 14},
		{"[ReleaseGroup] SeriesTitle S01E1 Webdl 1080p", 1, 1},
		{"[SubsPlus+] Series no Chill - S02E01 (NF WEB 1080p AVC AAC)", 2, 1},
		{"[SubsPlus+] Series no Chill - S02E01v2 (NF WEB 1080p AVC AAC)", 2, 1},
		{"Amazing Title (2024/S01E07/DSNP/WEB-DL/1080p/ESP/EAC3 5.1/ING/EAC3 5.1 Atmos/SUBS) SPWEB", 1, 7},
		{"Mini Title (Miniserie) (2024/S01E07/DSNP/WEB-DL/1080p/ESP/EAC3 5.1/ING/EAC3 5.1 Atmos/SUBS) SPWEB", 1, 7},
		{"Series.S006E18.Some.Title.Name-Part.1.1080p.WEB-DL.AAC2.0.H.264-Release", 6, 18},
		{"Series.2006.S006E18.Some.Title.Name-Part.1.1080p.WEB-DL.AAC2.0.H.264-Release", 6, 18},
		{"我的人间烟火.Fireworks.Series.S01E01.2023.V2.1080p.WEB-DL.H264.AAC-SeeWEB", 1, 1},
		{"Fireworks.Series.S01E01.2023.V2.1080p.WEB-DL.H264.AAC-SeeWEB", 1, 1},
		{"S3E3 - Seventeen Seconds [1080p]", 3, 3},
		{"S3E3 - Part 3 Seventeen Seconds [1080p]", 3, 3},
		{"S03E03 - Part 3 Seventeen Seconds [1080p]", 3, 3},
		{"[VARYG] Series at Age 29 S01E03 Rirui and Anyango 1080p CR WEB-DL AAC2.0 H.264 (29-sai Dokushin Chuuken Boukensha no Nichijou, Multi-Subs)", 1, 3},
		{"Series S093E081 1080p ALL4 WEB-DL AAC2 0 H 264-JJUNAHJAMESON", 93, 81},
		{"Series S87E136 1080p WEB-DL x264-NGP", 87, 136},
		{"Series S092E001 1080p ALL4 WEB-DL AAC2 0 H 264-JJunahJameson", 92, 1},
		{"Series s90 e43 1080p HDTV AAC H264", 90, 43},
		{"Series - S3E10 - Episode #3.10 - rus 720p WEBDL (LostFilm)", 3, 10},
		{"Series - S03E10 - Episode #3.10 - rus 720p WEBDL (LostFilm)", 3, 10},
		{"Series Title S01E11.5 [SP]-The Poppies Bloom Red on the Battlefield", 1, 11},
		{"Series.Title.S06E01b.Fade.Out.Fade.in.Part.2.1080p.DSNP.WEB-DL.AAC2.0.H.264-FLUX", 6, 1},
	} {
		got, ok := ParseEpisodeToken(c.name)
		if !ok || got.Season != c.season || !slices.Equal(got.Episodes, []int{c.episode}) {
			t.Errorf("%q = %+v (ok %v), want season %d episode %d", c.name, got, ok, c.season, c.episode)
		}
	}
}

func TestParseEpisodeTokenMultipleEpisodes(t *testing.T) {
	for _, c := range []struct {
		name     string
		season   int
		episodes []int
	}{
		{"Series.S03E01-06.DUAL.BDRip.XviD.AC3.-HELLYWOOD", 3, rng(1, 6)},
		{"Series.S03E01.S03E02.720p.HDTV.X264-DIMENSION", 3, rng(1, 2)},
		{"The Series S01e01 e02 ShoHD On Demand 1080i DD5 1 ALANiS", 1, rng(1, 2)},
		{"Series.Title.2x04.2x05.720p.BluRay-FUTV", 2, rng(4, 5)},
		{"Series.Title.S07E22E23.720p.HDTV.X264-DIMENSION", 7, rng(22, 23)},
		{"Series Title - S07E22 - S07E23 - And Lots of Security.. [HDTV-720p].mkv", 7, rng(22, 23)},
		{"S03E01.S03E02.720p.HDTV.X264-DIMENSION", 3, rng(1, 2)},
		{"2x04x05.720p.BluRay-FUTV", 2, rng(4, 5)},
		{"S02E04E05.720p.BluRay-FUTV", 2, rng(4, 5)},
		{"S02E03-04-05.720p.BluRay-FUTV", 2, rng(3, 5)},
		{"Series.Kings.S02E09-E10.HDTV.x264-ASAP", 2, rng(9, 10)},
		{"Series Kings - 2x9-2x10 - Served Code [SDTV] ", 2, rng(9, 10)},
		{"Series Kings - 2x09-2x10 - Served Code [SDTV] ", 2, rng(9, 10)},
		{"Hell on Series S02E09 E10 HDTV x264 EVOLVE", 2, rng(9, 10)},
		{"Hell.on.Series.S02E09-E10.720p.HDTV.x264-EVOLVE", 2, rng(9, 10)},
		{"Series's Sonarr - 8x01_02 - Free Falling", 8, rng(1, 2)},
		{"8x01_02 - Free Falling", 8, rng(1, 2)},
		{"Series.S01E91-E100", 1, rng(91, 100)},
		{"Series.S29E161-E165.PDTV.x264-FQM", 29, rng(161, 165)},
		{"Shortland.Series.S22E5363-E5366.HDTV.x264-FiHTV", 22, rng(5363, 5366)},
		{"World Series of Sonarr - 2010x15 - 2010x16 - HD TV.mkv", 2010, rng(15, 16)},
		{"The Series US S01E01-E02 720p HDTV x264", 1, rng(1, 2)},
		{"Series Title Season 01 Episode 05-06 720p", 1, rng(5, 6)},
		{"The Series And The Code - S42 Ep10718 - Ep10722", 42, rng(10718, 10722)},
		{"The Series And The Code - S42 Ep10688 - Ep10692", 42, rng(10688, 10692)},
		{"Series.S01E02E03.1080p.BluRay.x264-DeBTViD", 1, rng(2, 3)},
		{"grp-zoos01e11e12-1080p", 1, rng(11, 12)},
		{"grp-zoo-s01e11e12-1080p", 1, rng(11, 12)},
		{"Series Title.S6.E1.E2.Episode Name.1080p.WEB-DL", 6, rng(1, 2)},
		{"Series Title.S6E1-E2.Episode Name.1080p.WEB-DL", 6, rng(1, 2)},
		{"Series Title.S6E1-S6E2.Episode Name.1080p.WEB-DL", 6, rng(1, 2)},
		{"Series Title.S6E1E2.Episode Name.1080p.WEB-DL", 6, rng(1, 2)},
		{"Series Title.S6E1-E2-E3.Episode Name.1080p.WEB-DL", 6, rng(1, 3)},
		{"Series Title.S6.E1E3.Episode Name.1080p.WEB-DL", 6, rng(1, 3)},
		{"Series Title.S6.E1-E2.Episode Name.1080p.WEB-DL", 6, rng(1, 2)},
		{"Series Title.S6.E1-S6E2.Episode Name.1080p.WEB-DL", 6, rng(1, 2)},
		{"Series.Title.S05E01-E02.720p.5.1Ch.BluRay", 5, rng(1, 2)},
		{"Series.Title.S05E01-02.720p.5.1Ch.BluRay", 5, rng(1, 2)},
		{"S01E01-E03 - Episode Title.HDTV-720p", 1, rng(1, 3)},
		{"1x01-x03 - Episode Title.HDTV-720p", 1, rng(1, 3)},
		{"The Series Title (2010) - [S01E01-02-03] - Episode Title", 1, rng(1, 3)},
		{"[AqusiQ-TorrentS.pl]The.Name.of.the.Series.S01E05-06.PL.2160p-Ralf[shogho]", 1, rng(5, 6)},
		{"[AgusiQ-TorrentS.pl] The.Name.of.the.Series.S01E05-E06.PL.1080i.Ralf [jans12]", 1, rng(5, 6)},
		{"The.Name.of.the.Series.S01E05-6.PL.1080p.WEBRip.x264-666", 1, rng(5, 6)},
		{"Series Title - S15E06-07 - City Sushi HDTV-720p", 15, rng(6, 7)},
		{"Series Title - S01E01-02-03 - Episode Title HDTV-720p", 1, rng(1, 3)},
		{"Series Title - [02x01x02] - Episode 1", 2, rng(1, 2)},
		{"Series Title - [02x01-x02] - Episode 1", 2, rng(1, 2)},
		{"Series Title - [02x01-02] - Episode 1", 2, rng(1, 2)},
		{"Series Title (2011) - S01E23-E24 - ...E i nuovi orizzonti [HDTV 360p] [ITA].mkv", 1, rng(23, 24)},
		{"The Series Title! - S01E01-02-03", 1, rng(1, 3)},
		{"Series Title! (2013) - S04E44-E45 - Il 200 spettacolare episodio da narcisisti!", 4, rng(44, 45)},
		{"Series Title! (2013) - S04E44-E45 - Il 200 spettacolare episodio da narcisisti! [NetflixHD 720p HEVC] [ITA+ENG].mkv", 4, rng(44, 45)},
		{"Series Title (S15E06-08) City Sushi", 15, rng(6, 8)},
		{"Series Title (S05E06-08 of 24) City Sushi", 5, rng(6, 8)},
		{"Series Title (S05E06-08_of_24) City Sushi", 5, rng(6, 8)},
		{"Series Title (S5E6-8.of.24) City Sushi", 5, rng(6, 8)},
		{"Series Title (S5E6-8.of.XX) City Sushi", 5, rng(6, 8)},
		{"Босх: Спадок (S2E1-4) / Series: Legacy (S2E1-4) (2023) WEB-DL 1080p Ukr/Eng | sub Eng", 2, rng(1, 4)},
		{"Босх: Спадок / Series: Legacy / S2E1-4 of 10 (2023) WEB-DL 1080p Ukr/Eng | sub Eng", 2, rng(1, 4)},
		{"Series Title - S26E96-97-98-99-100 - Episode 5931 + Episode 5932 + Episode 5933 + Episode 5934 + Episode 5935", 26, rng(96, 100)},
		{"Series.S101E01-E02.Some.Title.Name.1080p.WEB-DL.AAC2.0.H.264-Release", 101, rng(1, 2)},
		{"Series.2006.S101E01-E02.Some.Title.Name.1080p.WEB-DL.AAC2.0.H.264-Release", 101, rng(1, 2)},
		{"[AL] Series.Title.S04E17-28.Part.2.German.DL.DTS.1080p.BluRay.x264-Pudding.GerSub-sama", 4, rng(17, 28)},
		{"[AL] Series.Title.S04E01-16.German.DL.DTS.1080p.BluRay.x264-Pudding.GerSub-sama", 4, rng(1, 16)},
	} {
		got, ok := ParseEpisodeToken(c.name)
		if !ok || got.Season != c.season || !slices.Equal(got.Episodes, c.episodes) {
			t.Errorf("%q = %+v (ok %v), want season %d episodes %v", c.name, got, ok, c.season, c.episodes)
		}
	}
}

func TestParseEpisodeTokenMarqueeOwnFileNames(t *testing.T) {
	got, ok := ParseEpisodeToken("Series - S04E01 - Name [Bluray-1080p Remux].mkv")
	if !ok || got.Season != 4 || !slices.Equal(got.Episodes, []int{1}) {
		t.Errorf("Sonarr's own scheme: %+v %v", got, ok)
	}
}

func TestParseEpisodeTokenRefusesNamesWithoutOne(t *testing.T) {
	for _, name := range []string{
		"", "notes.txt", "Series - Behind the Scenes.mkv", "Movie.2021.1080p.BluRay.x264.mkv",
		"Series - 1080p.mkv", "poster.jpg", "S01.mkv", "Series - Season 1.mkv",
	} {
		if got, ok := ParseEpisodeToken(name); ok {
			t.Errorf("%q parsed as %+v", name, got)
		}
	}
}

func TestParseEpisodeTokenKeepsAnEpisodeRangeBounded(t *testing.T) {
	got, ok := ParseEpisodeToken("Series.S01E01-1992.mkv")
	if !ok || !slices.Equal(got.Episodes, []int{1}) {
		t.Errorf("a year after an episode is not an episode range: %+v %v", got, ok)
	}
	got, ok = ParseEpisodeToken("Series.S01E05.S01E03.mkv")
	if !ok || !slices.Equal(got.Episodes, []int{5}) {
		t.Errorf("a repeated token going backwards is not a range: %+v %v", got, ok)
	}
}
