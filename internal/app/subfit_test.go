package app_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/subfitfake"
)

func withSubfit(f *subfitfake.Server) func(map[string]string) {
	return func(env map[string]string) { env["MARQUEE_SUBFIT_URL"] = f.URL }
}

func newSubfit(t *testing.T) *subfitfake.Server {
	t.Helper()
	f := subfitfake.New()
	t.Cleanup(f.Close)
	return f
}

// hooks waits for n webhooks and returns them as "event library path [previous]" lines.
func hooks(t *testing.T, f *subfitfake.Server, n int) []string {
	t.Helper()
	waitFor(t, "the subfit webhooks", 5*time.Second, func() bool { return f.Calls() >= n })
	// A webhook that should not be there has time to arrive.
	time.Sleep(100 * time.Millisecond)
	var out []string
	for _, h := range f.Hooks() {
		line := h.Event + " " + h.Library + " " + h.Path
		if h.PreviousPath != "" {
			line += " <- " + h.PreviousPath
		}
		out = append(out, line)
	}
	slices.Sort(out)
	return out
}

func subfitWarnings(s *realSvc) []string {
	var out []string
	for _, l := range strings.Split(s.logs.String(), "\n") {
		if strings.Contains(l, "subfit webhook") && strings.Contains(l, "level=WARN") {
			out = append(out, l)
		}
	}
	return out
}

func TestAMovieImportPostsOneImportWebhookWithItsRelativePath(t *testing.T) {
	f := newSubfit(t)
	r, _ := setupWith(t, withSubfit(f))
	r.enqueue(movieJob(r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "dune.mkv"))
	r.waitHistoryKind("imported", 1)

	got := hooks(t, f, 1)
	if want := []string{"import movies " + duneNew}; !slices.Equal(got, want) {
		t.Errorf("webhooks %q, want %q", got, want)
	}
}

func TestAnEpisodeImportPostsOneImportWebhookForTheSeriesLibrary(t *testing.T) {
	f := newSubfit(t)
	r, _ := setupWith(t, withSubfit(f))
	r.enqueue(episodeJob(r.cdn.Add("ep.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "ep.mkv"))
	r.waitHistoryKind("imported", 1)

	got := hooks(t, f, 1)
	if want := []string{"import series " + prophecyE1}; !slices.Equal(got, want) {
		t.Errorf("webhooks %q, want %q", got, want)
	}
}

func TestReplaceFileSwapPostsOneReplaceWebhookWithTheNewAndTheOldPath(t *testing.T) {
	f := newSubfit(t)
	r, _ := setupWith(t, withSubfit(f))
	lay(t, r.env["MARQUEE_MOVIES_PATH"], duneOld, duneOldEn)
	r.rescan("/api/titles/movie-438631/rescan")
	replaceJob(r, "new.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.waitHistoryKind("imported", 1)

	got := hooks(t, f, 1)
	if want := []string{"replace movies " + duneNew + " <- " + duneOld}; !slices.Equal(got, want) {
		t.Errorf("webhooks %q, want %q", got, want)
	}
}

func TestAReplacementThatKeepsTheNamePostsAReplaceWithBothPathsTheSame(t *testing.T) {
	f := newSubfit(t)
	r, _ := setupWith(t, withSubfit(f))
	lay(t, r.env["MARQUEE_MOVIES_PATH"], duneNew)
	r.rescan("/api/titles/movie-438631/rescan")
	replaceJob(r, "new.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	r.waitHistoryKind("imported", 1)

	got := hooks(t, f, 1)
	if want := []string{"replace movies " + duneNew + " <- " + duneNew}; !slices.Equal(got, want) {
		t.Errorf("webhooks %q, want %q", got, want)
	}
}

func TestReplacingOneEpisodeOfASharedFileKeepsItAndPostsAnImportNotAReplace(t *testing.T) {
	f := newSubfit(t)
	r, _ := setupWith(t, withSubfit(f))
	lay(t, r.env["MARQUEE_SERIES_PATH"], pairName)
	r.rescan("/api/titles/series-1408/rescan")
	if one, two := r.storedFile("series-1408", 1, 1), r.storedFile("series-1408", 1, 2); one.path != pairName || one != two {
		t.Fatalf("both episodes share the file: %+v %+v", one, two)
	}

	j := episodeJob(r.cdn.Add("ep.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "ep.mkv")
	j.Replace = true
	r.enqueue(j)
	r.waitHistoryKind("imported", 1)

	if !present(filepath.Join(r.env["MARQUEE_SERIES_PATH"], filepath.FromSlash(pairName))) {
		t.Error("the shared file stays: S01E02 still owns it")
	}
	got := hooks(t, f, 1)
	if want := []string{"import series " + prophecyE1}; !slices.Equal(got, want) {
		t.Errorf("webhooks %q, want %q", got, want)
	}
}

func TestWrongFilePostsOneDeleteWebhookForTheFile(t *testing.T) {
	f := newSubfit(t)
	s, _ := startFilesWith(t, false, withSubfit(f))
	s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 200)

	got := hooks(t, f, 1)
	if want := []string{"delete movies " + duneFile}; !slices.Equal(got, want) {
		t.Errorf("webhooks %q, want %q", got, want)
	}
}

func TestWrongFileOnATwoEpisodeFilePostsOneDelete(t *testing.T) {
	f := newSubfit(t)
	s, _ := startFilesWith(t, false, withSubfit(f))
	s.mustCall("POST", "/api/titles/series-1622/episodes/s03e02/wrong-file", `{"reSearch":false}`, 200)

	got := hooks(t, f, 1)
	if want := []string{"delete series " + houseS3E12}; !slices.Equal(got, want) {
		t.Errorf("webhooks %q, want %q", got, want)
	}
}

func TestRemovingATitleWithDeleteFilesPostsOneDeleteWebhookPerDeletedVideo(t *testing.T) {
	f := newSubfit(t)
	s, _ := startFilesWith(t, false, withSubfit(f))
	s.mustCall("DELETE", "/api/titles/series-1622?deleteFiles=true", "", 204)

	got := hooks(t, f, 3)
	want := []string{"delete series " + houseS3E12, "delete series " + houseS4E1, "delete series " + houseS4E2}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("webhooks %q, want %q", got, want)
	}
}

func TestRemovingATitleWithoutDeleteFilesPostsNothing(t *testing.T) {
	f := newSubfit(t)
	s, _ := startFilesWith(t, false, withSubfit(f))
	s.mustCall("DELETE", "/api/titles/movie-438631", "", 204)
	time.Sleep(200 * time.Millisecond)
	if f.Calls() != 0 {
		t.Errorf("webhooks: %v", f.Hooks())
	}
}

func TestWithoutASubfitURLNoRequestIsMade(t *testing.T) {
	f := newSubfit(t)
	s, _ := startFilesWith(t, false, nil)
	s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 200)
	s.mustCall("DELETE", "/api/titles/series-1622?deleteFiles=true", "", 204)
	time.Sleep(200 * time.Millisecond)
	if f.Calls() != 0 {
		t.Errorf("webhooks: %v", f.Hooks())
	}

	r, _ := setupWith(t, nil)
	r.enqueue(movieJob(r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "dune.mkv"))
	r.waitHistoryKind("imported", 1)
	if f.Calls() != 0 {
		t.Errorf("webhooks after an import: %v", f.Hooks())
	}
}

func TestInDryRunNoWebhookIsSent(t *testing.T) {
	f := newSubfit(t)
	s, _ := startFilesWith(t, true, withSubfit(f))
	s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 409)
	s.mustCall("DELETE", "/api/titles/movie-438631?deleteFiles=true", "", 409)
	s.mustCall("DELETE", "/api/titles/movie-438631", "", 204)
	time.Sleep(200 * time.Millisecond)
	if f.Calls() != 0 {
		t.Errorf("webhooks: %v", f.Hooks())
	}
}

func TestAnImportStillCompletesWhenSubfitAnswersAnError(t *testing.T) {
	f := newSubfit(t)
	f.FailWith(500)
	r, _ := setupWith(t, withSubfit(f))
	r.enqueue(movieJob(r.cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)}), "dune.mkv"))
	r.waitHistoryKind("imported", 1)

	if !present(r.moviePath(duneNew)) {
		t.Error("the file is imported")
	}
	waitFor(t, "the warning", 5*time.Second, func() bool { return len(subfitWarnings(r.realSvc)) == 1 })
	w := subfitWarnings(r.realSvc)[0]
	if !strings.Contains(w, "event=import") || !strings.Contains(w, "HTTP 500") || strings.Contains(w, f.URL) {
		t.Errorf("warning: %s", w)
	}
	time.Sleep(100 * time.Millisecond)
	if f.Calls() != 1 || len(subfitWarnings(r.realSvc)) != 1 {
		t.Errorf("one request and one warning, no retry: %d, %v", f.Calls(), subfitWarnings(r.realSvc))
	}
}

func TestAWrongFileStillCompletesWhenSubfitIsDown(t *testing.T) {
	f := newSubfit(t)
	url := f.URL
	f.Close()
	s, _ := startFilesWith(t, false, func(env map[string]string) { env["MARQUEE_SUBFIT_URL"] = url })
	start := time.Now()
	s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 200)
	if present(s.moviePath(duneFile)) {
		t.Error("the file is deleted")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the request took %v", took)
	}
	waitFor(t, "the warning", 5*time.Second, func() bool { return len(subfitWarnings(s)) == 1 })
	if w := subfitWarnings(s)[0]; !strings.Contains(w, "event=delete") || !strings.Contains(w, "not reachable") {
		t.Errorf("warning: %s", w)
	}
}

func TestASlowSubfitIsGivenUpOnAndNeverDelaysTheDelete(t *testing.T) {
	f := newSubfit(t)
	f.Delay(10 * time.Second)
	s, _ := startFilesWith(t, false, withSubfit(f), func(o *app.Options) { o.SubfitTimeout = 150 * time.Millisecond })
	start := time.Now()
	s.mustCall("DELETE", "/api/titles/series-1622?deleteFiles=true", "", 204)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the delete took %v", took)
	}
	s.mustCall("GET", "/api/titles/movie-438631", "", 200)
	waitFor(t, "the warnings", 5*time.Second, func() bool { return len(subfitWarnings(s)) == 3 })
	for _, w := range subfitWarnings(s) {
		if !strings.Contains(w, "did not answer in time") {
			t.Errorf("warning: %s", w)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if f.Calls() != 3 {
		t.Errorf("each webhook is sent once: %d", f.Calls())
	}
}

func TestShutdownDoesNotWaitForASubfitThatNeverAnswers(t *testing.T) {
	f := newSubfit(t)
	f.Delay(time.Minute)
	s, _ := startFilesWith(t, false, withSubfit(f), func(o *app.Options) {
		o.SubfitTimeout = time.Minute
		o.SubfitCloseGrace = 100 * time.Millisecond
	})
	s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 200)
	waitFor(t, "the webhook to arrive", 5*time.Second, func() bool { return f.Calls() == 1 })

	start := time.Now()
	s.stop()
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("shutdown took %v", took)
	}
	if w := subfitWarnings(s); len(w) != 0 {
		t.Errorf("abandoning a webhook at shutdown is not a warning: %v", w)
	}
}

func TestTheSubfitURLNeverAppearsInTheLog(t *testing.T) {
	f := newSubfit(t)
	f.FailWith(500)
	s, _ := startFilesWith(t, false, func(env map[string]string) {
		env["MARQUEE_SUBFIT_URL"] = strings.Replace(f.URL, "http://", "http://user:PASSsecret@", 1) + "/TOKENsecret"
	})
	s.mustCall("POST", "/api/titles/movie-438631/wrong-file", "", 200)
	waitFor(t, "the warning", 5*time.Second, func() bool { return len(subfitWarnings(s)) == 1 })
	for _, leak := range []string{"PASSsecret", "TOKENsecret", f.URL[len("http://"):]} {
		if strings.Contains(s.logs.String(), leak) {
			t.Errorf("the log holds %q", leak)
		}
	}
}
