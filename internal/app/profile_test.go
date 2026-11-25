package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/debridfake"
)

const twoProfilesConfig = `{
  "profiles": {
    "tv-safe": {
      "qualities": ["WEBDL-1080p", "WEBDL-720p"],
      "reject": ["truehd"],
      "prefer": ["dd+", "dd"],
      "size_limits": {"min_mb": 250, "max_mb": 20000}
    },
    "anything": {"qualities": ["WEBDL-1080p", "WEBDL-720p", "Remux-2160p"]}
  },
  "default_movie_profile": "tv-safe",
  "default_series_profile": "anything"
}`

// withConfigFile lays the JSON config file a test starts the service with.
func withConfigFile(t *testing.T, body string) func(map[string]string) {
	t.Helper()
	return func(env map[string]string) {
		file := filepath.Join(t.TempDir(), "marquee.json")
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		env["MARQUEE_CONFIG"] = file
	}
}

func TestProfilesListsTheBuiltInDefaultWhenTheConfigDefinesNone(t *testing.T) {
	s := startReal(t, newTMDB(t))
	v := s.mustCall("GET", "/api/profiles", "", 200)
	profiles := v["profiles"].([]any)
	if len(profiles) != 1 || v["defaultMovieProfile"] != "default" || v["defaultSeriesProfile"] != "default" {
		t.Fatalf("%v", v)
	}
	p := profiles[0].(map[string]any)
	if p["name"] != "default" || len(p["qualities"].([]any)) == 0 {
		t.Errorf("%v", p)
	}
	reject := map[string]bool{}
	for _, tag := range p["reject"].([]any) {
		reject[tag.(string)] = true
	}
	for _, tag := range []string{"truehd", "dts-hd", "dts-x", "dv-p7"} {
		if !reject[tag] {
			t.Errorf("the TV-safe default must reject %s: %v", tag, p["reject"])
		}
	}
}

func TestProfilesListsTheConfiguredProfilesByNameWithTheirLimitsAndTheDefaultOfEachKind(t *testing.T) {
	s := startRealWith(t, newTMDB(t), "", withConfigFile(t, twoProfilesConfig))
	v := s.mustCall("GET", "/api/profiles", "", 200)
	if v["defaultMovieProfile"] != "tv-safe" || v["defaultSeriesProfile"] != "anything" {
		t.Errorf("defaults: %v", v)
	}
	profiles := v["profiles"].([]any)
	if len(profiles) != 2 || profiles[0].(map[string]any)["name"] != "anything" || profiles[1].(map[string]any)["name"] != "tv-safe" {
		t.Fatalf("alphabetical by name: %v", profiles)
	}
	tv := profiles[1].(map[string]any)
	limits := tv["sizeLimits"].(map[string]any)
	if limits["minMb"] != float64(250) || limits["maxMb"] != float64(20000) {
		t.Errorf("limits: %v", limits)
	}
	if q := tv["qualities"].([]any); len(q) != 2 || q[0] != "WEBDL-1080p" {
		t.Errorf("qualities keep the owner's order: %v", q)
	}
	open := profiles[0].(map[string]any)
	if open["reject"] == nil || open["prefer"] == nil || len(open["reject"].([]any)) != 0 {
		t.Errorf("a list the config leaves out is empty, never null: %v", open)
	}
}

func TestATitleTakesAProfileByNameAndNullMeansTheKindsDefault(t *testing.T) {
	s := startRealWith(t, newTMDB(t), "", withConfigFile(t, twoProfilesConfig))
	if v := s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"profile":"anything"}`, 201); v["profile"] != "anything" {
		t.Errorf("added with a profile: %v", v["profile"])
	}
	if v := s.mustCall("POST", "/api/titles", `{"kind":"series","tmdbId":1408}`, 201); v["profile"] != nil {
		t.Errorf("no profile is null, not the default's name: %v", v["profile"])
	}
	if v := s.getTitle("movie-438631"); v["profile"] != "anything" {
		t.Errorf("get: %v", v["profile"])
	}
	if v := s.mustCall("PATCH", "/api/titles/movie-438631", `{"profile":"tv-safe"}`, 200); v["profile"] != "tv-safe" {
		t.Errorf("patched: %v", v["profile"])
	}
	if v := s.mustCall("PATCH", "/api/titles/movie-438631", `{"monitored":false}`, 200); v["profile"] != "tv-safe" {
		t.Errorf("a patch that leaves the profile out keeps it: %v", v["profile"])
	}
	v := s.mustCall("PATCH", "/api/titles/movie-438631", `{"profile":null}`, 200)
	if p, ok := v["profile"]; !ok || p != nil {
		t.Errorf("null clears it: %v", v)
	}
}

func TestAnUnknownProfileOrTheOldPreferredQualityIsRefused(t *testing.T) {
	s := startRealWith(t, newTMDB(t), "", withConfigFile(t, twoProfilesConfig))
	for _, c := range [][3]string{
		{"POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"profile":"nonsense"}`},
		{"POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"profile":""}`},
		{"POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"profile":null}`},
		{"POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"preferredQuality":"1080p"}`},
	} {
		if r := s.bad(c[0], c[1], c[2]); r.status != 400 {
			t.Errorf("%s %s %s: %d %s", c[0], c[1], c[2], r.status, r.body)
		}
	}
	s.mustCall("GET", "/api/titles/movie-438631", "", 404)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631}`, 201)
	for _, body := range []string{`{"profile":"nonsense"}`, `{"profile":""}`, `{"profile":7}`, `{"preferredQuality":"1080p"}`, `{"preferredQuality":null}`} {
		if r := s.bad("PATCH", "/api/titles/movie-438631", body); r.status != 400 {
			t.Errorf("PATCH %s: %d %s", body, r.status, r.body)
		}
	}
	if v := s.getTitle("movie-438631"); v["profile"] != nil {
		t.Errorf("a refused patch changes nothing: %v", v["profile"])
	}
}

func TestAStoredProfileTheConfigNoLongerDefinesFallsBackToTheDefaultWithAWarning(t *testing.T) {
	aio := newAIO(t)
	aio.Answer("movie", "tt1160419", results(
		listed{name: "Dune.2021.1080p.WEB-DL.TrueHD.7.1.mkv", size: 3000 << 20, url: "http://127.0.0.1:1/a"},
		listed{name: "Dune.2021.1080p.WEB-DL.DDP5.1.mkv", size: 3000 << 20, url: "http://127.0.0.1:1/b"},
	))
	s := startRealWith(t, newTMDB(t), aio.ManifestURL(), withConfigFile(t, twoProfilesConfig), shortAIO)
	s.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"profile":"anything"}`, 201)

	store, err := catalog.Open(s.env["MARQUEE_DATA_DIR"], nil)
	if err != nil {
		t.Fatal(err)
	}
	gone := "removed-from-config"
	if _, err := store.Update(context.Background(), "movie-438631", catalog.Patch{Profile: &gone}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	if v := s.getTitle("movie-438631"); v["profile"] != nil {
		t.Errorf("a name the config lost answers as the default: %v", v["profile"])
	}
	l := s.releases("/api/titles/movie-438631/releases")
	if r := l.reasons()["Dune.2021.1080p.WEB-DL.TrueHD.7.1.mkv"]; r[0] != "profile_rejected" {
		t.Errorf("judged by the movie default, which rejects TrueHD: %v", l.reasons())
	}
	if logs := s.logs.String(); !strings.Contains(logs, "removed-from-config") || !strings.Contains(logs, "WARN") {
		t.Errorf("the fallback is logged as a warning naming the profile:\n%s", logs)
	}
}

func TestReleasesCarryTheirFormatTagsAndAreRankedByTheProfile(t *testing.T) {
	aio := newAIO(t)
	aio.Answer("movie", "tt1160419", results(
		listed{name: "Dune.2021.1080p.WEB-DL.mkv", size: 3000 << 20, url: "http://127.0.0.1:1/a"},
		listed{name: "Dune.2021.1080p.WEB-DL.DD5.1.mkv", size: 3000 << 20, url: "http://127.0.0.1:1/b"},
		listed{name: "Dune.2021.1080p.WEB-DL.TrueHD.7.1.mkv", size: 3000 << 20, url: "http://127.0.0.1:1/c"},
		listed{name: "Dune.2021.1080p.WEB-DL.DDP5.1.mkv", size: 3000 << 20, url: "http://127.0.0.1:1/d"},
		listed{name: "Dune.2021.1080p.WEB-DL.DTS-HD.MA.mkv", size: 3000 << 20, url: "http://127.0.0.1:1/e"},
		listed{name: "Dune.2021.1080p.WEB-DL.tiny.mkv", size: 100 << 20, url: "http://127.0.0.1:1/f"},
	))
	s := startRealWith(t, newTMDB(t), aio.ManifestURL(), nil, shortAIO)
	l := s.releases("/api/titles/movie-438631/releases")
	if got := strings.Join(l.candidateNames(), ","); got != "Dune.2021.1080p.WEB-DL.DDP5.1.mkv,Dune.2021.1080p.WEB-DL.DD5.1.mkv,Dune.2021.1080p.WEB-DL.mkv" {
		t.Errorf("prefer ranks within a quality, AIOStreams' order breaks ties: %s", got)
	}
	tags := map[string][]any{}
	for _, c := range l.candidates {
		tags[c["name"].(string)] = c["tags"].([]any)
	}
	if got := tags["Dune.2021.1080p.WEB-DL.DDP5.1.mkv"]; len(got) != 1 || got[0] != "dd+" {
		t.Errorf("tags: %v", tags)
	}
	if got := tags["Dune.2021.1080p.WEB-DL.mkv"]; got == nil || len(got) != 0 {
		t.Errorf("a release with no tags has an empty list: %v", got)
	}
	got := l.reasons()
	if r := got["Dune.2021.1080p.WEB-DL.TrueHD.7.1.mkv"]; r[0] != "profile_rejected" || !strings.Contains(r[1], "TrueHD audio") {
		t.Errorf("truehd: %v", r)
	}
	if r := got["Dune.2021.1080p.WEB-DL.DTS-HD.MA.mkv"]; r[0] != "profile_rejected" || !strings.Contains(r[1], "DTS-HD audio") {
		t.Errorf("dts-hd: %v", r)
	}
	for _, d := range l.dropped {
		if d["release"].(map[string]any)["tags"] == nil {
			t.Errorf("a dropped release shows its tags too: %v", d)
		}
	}
}

func TestAProfileSizeLimitDropsAReleaseInWords(t *testing.T) {
	aio := newAIO(t)
	aio.Answer("movie", "tt1160419", results(
		listed{name: "Dune.2021.1080p.WEB-DL.small.mkv", size: 220 << 20, url: "http://127.0.0.1:1/a"},
		listed{name: "Dune.2021.1080p.WEB-DL.huge.mkv", size: 30000 << 20, url: "http://127.0.0.1:1/b"},
		listed{name: "Dune.2021.1080p.WEB-DL.mkv", size: 3000 << 20, url: "http://127.0.0.1:1/c"},
	))
	s := startRealWith(t, newTMDB(t), aio.ManifestURL(), withConfigFile(t, twoProfilesConfig), shortAIO)
	got := s.releases("/api/titles/movie-438631/releases").reasons()
	if r := got["Dune.2021.1080p.WEB-DL.small.mkv"]; r[0] != "profile_rejected" || !strings.Contains(r[1], "minimum of 250 MB") {
		t.Errorf("small: %v", r)
	}
	if r := got["Dune.2021.1080p.WEB-DL.huge.mkv"]; r[0] != "profile_rejected" || !strings.Contains(r[1], "maximum of 20000 MB") {
		t.Errorf("huge: %v", r)
	}
}

func TestInteractiveSearchCanGrabAProfileRejectedReleaseButNotAnUnusableOne(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(
		listed{name: "Dune.2021.1080p.WEB-DL.TrueHD.7.1.mkv", size: 3000 << 20, url: url},
		listed{name: "Dune.2021.1080p.WEB-DL.exe", size: 3000 << 20, url: url},
		listed{name: "Dune.2021.1080p.WEB-DL.sample.mkv", size: 30 << 20, url: url},
	))
	ids := map[string]string{}
	v := r.mustCall("GET", "/api/titles/movie-438631/releases", "", 200)
	for _, d := range v["dropped"].([]any) {
		rel := d.(map[string]any)["release"].(map[string]any)
		ids[rel["name"].(string)+"|"+d.(map[string]any)["reason"].(string)] = rel["id"].(string)
	}
	refuse := func(key string) {
		t.Helper()
		id, ok := ids[key]
		if !ok {
			t.Fatalf("%s is not among the drops: %v", key, ids)
		}
		r.mustCall("POST", "/api/titles/movie-438631/releases/"+id+"/grab", "", 409)
	}
	refuse("Dune.2021.1080p.WEB-DL.exe|not_video")
	refuse("Dune.2021.1080p.WEB-DL.sample.mkv|too_small")
	if d := r.downloads(); len(d) != 0 {
		t.Fatalf("nothing unusable is queued: %v", d)
	}
	d := r.mustCall("POST", "/api/titles/movie-438631/releases/"+ids["Dune.2021.1080p.WEB-DL.TrueHD.7.1.mkv|profile_rejected"]+"/grab", "", 202)
	if d["releaseName"] != "Dune.2021.1080p.WEB-DL.TrueHD.7.1.mkv" || d["titleId"] != "movie-438631" {
		t.Errorf("the picked release is queued: %v", d)
	}
}

func TestAWrongEpisodeStillCannotBeGrabbedWhileAProfileRejectedOneCan(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("ep.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("series", "tvdb:403000:1:1", results(
		listed{name: "Dune.Prophecy.S01E02.1080p.WEB-DL.mkv", size: 800 << 20, url: url, season: 1, ep: 2},
		listed{name: "Dune.Prophecy.S01E01.1080p.WEB-DL.TrueHD.mkv", size: 800 << 20, url: url, season: 1, ep: 1},
	))
	v := r.mustCall("GET", "/api/titles/series-1408/releases?episode=s01e01", "", 200)
	byReason := map[string]string{}
	for _, d := range v["dropped"].([]any) {
		byReason[d.(map[string]any)["reason"].(string)] = d.(map[string]any)["release"].(map[string]any)["id"].(string)
	}
	base := "/api/titles/series-1408/releases/"
	r.mustCall("POST", base+byReason["wrong_episode"]+"/grab", `{"episode":"s01e01"}`, 409)
	r.mustCall("POST", base+byReason["profile_rejected"]+"/grab", `{"episode":"s01e01"}`, 202)
}

func TestTheSchedulerNeverPicksAProfileRejectedReleaseAndSaysSo(t *testing.T) {
	r := newSchedRig(t, false)
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.WEB-DL.TrueHD.7.1.mkv", size: 3000 << 20, url: url}))
	r.addMovie()
	r.step()
	if d := r.downloads(); len(d) != 0 {
		t.Fatalf("a rejected release must not be queued: %v", d)
	}
	e := r.entries("searched")
	if len(e) == 0 || !strings.Contains(e[0]["message"].(string), "1 rejected by profile") {
		t.Errorf("the history says why nothing was taken: %v", e)
	}
}

func TestATitleOnAProfileThatAllowsAReleaseHasItGrabbedByTheScheduler(t *testing.T) {
	r := newSchedRigWith(t, false, newTMDB(t), withConfigFile(t, `{"profiles":{
		"strict":{"qualities":["WEBDL-1080p"],"reject":["truehd"]},
		"open":{"qualities":["WEBDL-1080p"]}},
		"default_movie_profile":"strict","default_series_profile":"strict"}`))
	url := r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.aio.Answer("movie", duneID, results(listed{name: "Dune.2021.1080p.WEB-DL.TrueHD.7.1.mkv", size: 3000 << 20, url: url}))
	r.mustCall("POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"profile":"open"}`, 201)
	r.step()
	if d := r.downloads(); len(d) != 1 {
		t.Errorf("the title's own profile allows it: %v", d)
	}
}
