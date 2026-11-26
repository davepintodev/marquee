package subfit_test

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/subfit"
	"github.com/davepintodev/marquee/internal/subfitfake"
)

type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newClient(t *testing.T, url string, o subfit.Options) (*subfit.Client, *syncBuf) {
	t.Helper()
	logs := &syncBuf{}
	o.URL = config.Secret(url)
	o.Log = slog.New(slog.NewTextHandler(logs, nil))
	c, err := subfit.New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, logs
}

func warnings(logs *syncBuf) []string {
	var out []string
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "level=WARN") {
			out = append(out, l)
		}
	}
	return out
}

func TestEachEventIsPostedOnceToTheHookWithItsBody(t *testing.T) {
	f := subfitfake.New()
	defer f.Close()
	c, logs := newClient(t, f.URL+"/", subfit.Options{})

	c.Import("series", "Show/Season 01/Show - S01E01.mkv")
	c.Replace("movies", "Dune (2021)/new.mkv", "Dune (2021)/old.mkv")
	c.Delete("movies", "Dune (2021)/gone.mkv")
	c.Close()

	got := f.Hooks()
	if len(got) != 3 {
		t.Fatalf("hooks: %+v", got)
	}
	by := map[string]subfitfake.Hook{}
	for _, h := range got {
		by[h.Event] = h
		if h.URLPath != "/hook/marquee" || !strings.HasPrefix(h.ContentType, "application/json") {
			t.Errorf("request: %+v", h)
		}
	}
	if h := by["import"]; h.Library != "series" || h.Path != "Show/Season 01/Show - S01E01.mkv" || len(h.Fields) != 3 {
		t.Errorf("import: %+v", h)
	}
	if h := by["replace"]; h.Library != "movies" || h.Path != "Dune (2021)/new.mkv" || h.PreviousPath != "Dune (2021)/old.mkv" || len(h.Fields) != 4 {
		t.Errorf("replace: %+v", h)
	}
	if h := by["delete"]; h.Library != "movies" || h.Path != "Dune (2021)/gone.mkv" || len(h.Fields) != 3 {
		t.Errorf("delete: %+v", h)
	}
	if w := warnings(logs); len(w) != 0 {
		t.Errorf("a good webhook warns nothing: %v", w)
	}
}

func TestAnErrorAnswerLogsOneWarningWithTheEventAndPathAndIsNotRetried(t *testing.T) {
	f := subfitfake.New()
	defer f.Close()
	f.FailWith(500)
	c, logs := newClient(t, f.URL, subfit.Options{})

	c.Delete("movies", "Dune (2021)/gone.mkv")
	c.Close()

	if f.Calls() != 1 {
		t.Errorf("requests: %d, want 1 (nothing is retried)", f.Calls())
	}
	w := warnings(logs)
	if len(w) != 1 || !strings.Contains(w[0], "event=delete") || !strings.Contains(w[0], "Dune (2021)/gone.mkv") || !strings.Contains(w[0], "500") {
		t.Errorf("warnings: %v", w)
	}
}

func TestAWarningNeverHoldsTheURLOrItsCredentials(t *testing.T) {
	for name, mk := range map[string]func(*subfitfake.Server) string{
		"unreachable": func(f *subfitfake.Server) string { u := f.URL; f.Close(); return u },
		"error":       func(f *subfitfake.Server) string { f.FailWith(503); return f.URL },
	} {
		t.Run(name, func(t *testing.T) {
			f := subfitfake.New()
			if name != "unreachable" {
				defer f.Close()
			}
			base := strings.Replace(mk(f), "http://", "http://user:PASSsecret@", 1) + "/prefix-SECRETtoken?key=QUERYsecret"
			c, logs := newClient(t, base, subfit.Options{})
			c.Import("movies", "Dune (2021)/Dune (2021).mkv")
			c.Close()
			w := warnings(logs)
			if len(w) != 1 {
				t.Fatalf("warnings: %v", w)
			}
			for _, leak := range []string{"PASSsecret", "SECRETtoken", "QUERYsecret", "127.0.0.1", "user:"} {
				if strings.Contains(logs.String(), leak) {
					t.Errorf("the log holds %q: %s", leak, logs.String())
				}
			}
		})
	}
}

func TestASlowAnswerTimesOutWithOneWarningAndNoRetry(t *testing.T) {
	f := subfitfake.New()
	defer f.Close()
	f.Delay(5 * time.Second)
	c, logs := newClient(t, f.URL, subfit.Options{Timeout: 100 * time.Millisecond})

	start := time.Now()
	c.Import("movies", "Dune (2021)/Dune (2021).mkv")
	c.Close()

	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the webhook held on for %v", took)
	}
	if f.Calls() != 1 {
		t.Errorf("requests: %d", f.Calls())
	}
	if w := warnings(logs); len(w) != 1 || !strings.Contains(w[0], "did not answer in time") {
		t.Errorf("warnings: %v", w)
	}
}

func TestSendingNeverWaitsForSubfit(t *testing.T) {
	f := subfitfake.New()
	defer f.Close()
	f.Delay(5 * time.Second)
	c, _ := newClient(t, f.URL, subfit.Options{Timeout: 5 * time.Second, CloseGrace: 50 * time.Millisecond})

	start := time.Now()
	for range 50 {
		c.Delete("series", "Show/Season 01/Show - S01E01.mkv")
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("50 sends took %v", took)
	}
}

func TestCloseDoesNotHangOnARequestInFlight(t *testing.T) {
	f := subfitfake.New()
	defer f.Close()
	f.Delay(time.Minute)
	c, logs := newClient(t, f.URL, subfit.Options{Timeout: time.Minute, CloseGrace: 100 * time.Millisecond})

	c.Import("movies", "Dune (2021)/Dune (2021).mkv")
	deadline := time.Now().Add(5 * time.Second)
	for f.Calls() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the request never arrived")
		}
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	c.Close()
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Close took %v", took)
	}
	if w := warnings(logs); len(w) != 0 {
		t.Errorf("giving up at shutdown is not a failure to warn about: %v", w)
	}
	c.Import("movies", "after-close.mkv")
	if f.Calls() != 1 {
		t.Errorf("nothing is sent after Close: %d", f.Calls())
	}
}

func TestCloseLetsAQuickRequestFinish(t *testing.T) {
	f := subfitfake.New()
	defer f.Close()
	f.Delay(100 * time.Millisecond)
	c, logs := newClient(t, f.URL, subfit.Options{})
	c.Import("movies", "Dune (2021)/Dune (2021).mkv")
	c.Close()
	if len(warnings(logs)) != 0 {
		t.Errorf("logs: %s", logs)
	}
}

func TestARedirectIsAnErrorNotFollowed(t *testing.T) {
	f := subfitfake.New()
	defer f.Close()
	f.FailWith(307)
	c, logs := newClient(t, f.URL, subfit.Options{})
	c.Import("movies", "x.mkv")
	c.Close()
	if f.Calls() != 1 || len(warnings(logs)) != 1 {
		t.Errorf("calls %d, warnings %v", f.Calls(), warnings(logs))
	}
}

func TestANilClientDoesNothing(t *testing.T) {
	var c *subfit.Client
	c.Import("movies", "a.mkv")
	c.Replace("movies", "a.mkv", "b.mkv")
	c.Delete("movies", "a.mkv")
	c.Close()
}

func TestNewRejectsAnInvalidURLWithoutQuotingIt(t *testing.T) {
	for _, bad := range []string{"", "ftp://host", "not a url PASSsecret", "http://"} {
		_, err := subfit.New(subfit.Options{URL: config.Secret(bad)})
		if err == nil {
			t.Errorf("%q was accepted", bad)
			continue
		}
		if strings.Contains(err.Error(), "PASSsecret") || strings.Contains(err.Error(), "ftp://host") {
			t.Errorf("the error quotes the setting: %v", err)
		}
	}
}

func TestLibraryNamesTheLibraryOfAKind(t *testing.T) {
	if subfit.Library("movie") != "movies" || subfit.Library("series") != "series" {
		t.Errorf("movie %q, series %q", subfit.Library("movie"), subfit.Library("series"))
	}
}
