package torbox_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/torbox"
	"github.com/davepintodev/marquee/internal/torboxfake"
)

const hash = "0123456789abcdef0123456789abcdef01234567"

func newClient(t *testing.T, fake *torboxfake.Fake) *torbox.Client {
	t.Helper()
	c, err := torbox.New(torbox.Options{APIKey: config.Secret(torboxfake.Key), Base: fake.APIBase()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPingAcceptsTheKeyAndRejectsAnother(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	if err := newClient(t, fake).Ping(context.Background()); err != nil {
		t.Fatalf("ping with the right key: %v", err)
	}
	bad, err := torbox.New(torbox.Options{APIKey: "not-the-key", Base: fake.APIBase()})
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Ping(context.Background()); !errors.Is(err, torbox.ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}

func TestNewNeedsAKey(t *testing.T) {
	if _, err := torbox.New(torbox.Options{}); err == nil {
		t.Fatal("want an error for a missing key")
	}
}

func TestStatusesMapToTheSentinels(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{401, torbox.ErrRejected},
		{403, torbox.ErrRejected},
		{429, torbox.ErrRateLimited},
		{500, torbox.ErrUnavailable},
		{502, torbox.ErrUnavailable},
		{400, torbox.ErrUnavailable},
		{302, torbox.ErrUnavailable},
	}
	calls := map[string]func(*torbox.Client) error{
		"Ping": func(c *torbox.Client) error { return c.Ping(context.Background()) },
		"CreateTorrent": func(c *torbox.Client) error {
			_, err := c.CreateTorrent(context.Background(), hash)
			return err
		},
		"GetTorrent": func(c *torbox.Client) error {
			_, err := c.GetTorrent(context.Background(), 1)
			return err
		},
		"RequestDownloadLink": func(c *torbox.Client) error {
			_, err := c.RequestDownloadLink(context.Background(), 1, 1)
			return err
		},
		"DeleteTorrent": func(c *torbox.Client) error { return c.DeleteTorrent(context.Background(), 1) },
	}
	for name, call := range calls {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/%d", name, tc.status), func(t *testing.T) {
				fake := torboxfake.New()
				defer fake.Close()
				fake.Down("", tc.status)
				if err := call(newClient(t, fake)); !errors.Is(err, tc.want) {
					t.Fatalf("status %d: want %v, got %v", tc.status, tc.want, err)
				}
			})
		}
	}
}

func TestCreateTorrentStartsCachingAndGetTorrentFollowsIt(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	c := newClient(t, fake)
	ctx := context.Background()

	created, err := c.CreateTorrent(ctx, strings.ToUpper(hash))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || !strings.EqualFold(created.Hash, hash) {
		t.Fatalf("created torrent: %+v", created)
	}
	if got := fake.Magnets(); len(got) != 1 || got[0] != "magnet:?xt=urn:btih:"+strings.ToUpper(hash) {
		t.Fatalf("the fake saw magnets %v", got)
	}

	got, err := c.GetTorrent(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Progress != 0 || got.DownloadSpeed != 0 || got.DownloadPresent {
		t.Fatalf("a new torrent has not started: %+v", got)
	}

	fake.Step(created.ID, 0.42, 1_500_000)
	got, err = c.GetTorrent(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Progress != 0.42 || got.DownloadSpeed != 1_500_000 || got.DownloadPresent {
		t.Fatalf("after a step: %+v", got)
	}

	fake.Present(created.ID)
	got, err = c.GetTorrent(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Progress != 1 || got.DownloadSpeed != 0 || !got.DownloadPresent {
		t.Fatalf("once present: %+v", got)
	}
}

func TestCreateTorrentRejectsAMalformedHash(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	for _, h := range []string{"", "xyz", hash + "&dn=x", "magnet:?xt=urn:btih:" + hash} {
		if _, err := newClient(t, fake).CreateTorrent(context.Background(), h); !errors.Is(err, torbox.ErrBadInfoHash) {
			t.Errorf("hash %q: want ErrBadInfoHash, got %v", h, err)
		}
	}
	if n := fake.Hits("/torrents/createtorrent"); n != 0 {
		t.Errorf("a bad hash reached the network %d times", n)
	}
}

func TestAlreadyCachedTorrentIsPresentAtOnce(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	fake.Cached(hash)
	c := newClient(t, fake)
	created, err := c.CreateTorrent(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetTorrent(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.DownloadPresent || got.Progress != 1 {
		t.Fatalf("a cached torrent should be present: %+v", got)
	}
}

func TestSeasonPackFileList(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	fake.SetFiles(hash,
		torboxfake.File{ID: 0, Name: "Show.S01/Show.S01E01.mkv", Size: 1_000},
		torboxfake.File{ID: 1, Name: "Show.S01/Show.S01E02.mkv", Size: 2_000},
		torboxfake.File{ID: 2, Name: "Show.S01/Show.S01E03.mkv", Size: 3_000},
	)
	c := newClient(t, fake)
	created, err := c.CreateTorrent(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetTorrent(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 3 {
		t.Fatalf("want 3 files, got %+v", got.Files)
	}
	if f := got.Files[1]; f.ID != 1 || f.Name != "Show.S01/Show.S01E02.mkv" || f.ShortName != "Show.S01E02.mkv" || f.Size != 2_000 {
		t.Fatalf("second file: %+v", f)
	}
}

func TestRequestDownloadLinkNeedsAPresentTorrentAndHidesTheToken(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	fake.SetFiles(hash, torboxfake.File{ID: 7, Name: "Movie.mkv", Size: 4, Body: []byte("data")})
	c := newClient(t, fake)
	ctx := context.Background()
	created, err := c.CreateTorrent(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RequestDownloadLink(ctx, created.ID, 7); err == nil {
		t.Fatal("a torrent still caching must not hand out a link")
	}

	fake.Present(created.ID)
	link, err := c.RequestDownloadLink(ctx, created.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if link.Reveal() == "" || fmt.Sprint(link) == link.Reveal() || strings.Contains(fmt.Sprintf("%v %+v %#v", link, link, link), link.Reveal()) {
		t.Fatalf("the link must be a config.Secret that prints a placeholder")
	}
	resp, err := http.Get(link.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	if body.String() != "data" {
		t.Fatalf("the link served %q", body.String())
	}
}

func TestDeleteTorrentIsRecordedAndForgetsTheTorrent(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	c := newClient(t, fake)
	ctx := context.Background()
	a, _ := c.CreateTorrent(ctx, hash)
	b, _ := c.CreateTorrent(ctx, strings.Repeat("a", 40))
	if err := c.DeleteTorrent(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := fake.Deleted(); len(got) != 1 || got[0] != b.ID {
		t.Fatalf("deleted = %v, want [%d]", got, b.ID)
	}
	if _, err := c.GetTorrent(ctx, b.ID); !errors.Is(err, torbox.ErrNotFound) {
		t.Fatalf("a deleted torrent: want ErrNotFound, got %v", err)
	}
	if _, err := c.GetTorrent(ctx, a.ID); err != nil {
		t.Fatalf("the other torrent must survive: %v", err)
	}
	if err := c.DeleteTorrent(ctx, 9999); !errors.Is(err, torbox.ErrNotFound) {
		t.Fatalf("deleting an unknown torrent: want ErrNotFound, got %v", err)
	}
}

func TestScriptedFailuresThenRecovery(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	fake.Script("/user/me", torboxfake.Step{Status: 429, RetryAfter: "1"}, torboxfake.Step{Status: 500})
	c := newClient(t, fake)
	if err := c.Ping(context.Background()); !errors.Is(err, torbox.ErrRateLimited) {
		t.Fatalf("first: %v", err)
	}
	if err := c.Ping(context.Background()); !errors.Is(err, torbox.ErrUnavailable) {
		t.Fatalf("second: %v", err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("third: %v", err)
	}
	fake.Reject()
	if err := c.Ping(context.Background()); !errors.Is(err, torbox.ErrRejected) {
		t.Fatalf("after Reject: %v", err)
	}
	fake.Heal()
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("after Heal: %v", err)
	}
}

func TestStallKeepsProgressButDropsTheSpeed(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	c := newClient(t, fake)
	created, _ := c.CreateTorrent(context.Background(), hash)
	fake.Step(created.ID, 0.3, 900)
	fake.Stall(created.ID)
	got, err := c.GetTorrent(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Progress != 0.3 || got.DownloadSpeed != 0 || got.DownloadPresent {
		t.Fatalf("stalled torrent: %+v", got)
	}
}

func TestMalformedAnswersAreUnavailable(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      "<html>oops",
		"success false": `{"success":false,"error":"BOOM","detail":"x"}`,
		"wrong shape":   `{"success":true,"data":42}`,
		"empty link":    `{"success":true,"data":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			c, err := torbox.New(torbox.Options{APIKey: "k", Base: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.GetTorrent(context.Background(), 1); !errors.Is(err, torbox.ErrUnavailable) {
				t.Fatalf("GetTorrent: want ErrUnavailable, got %v", err)
			}
			if _, err := c.RequestDownloadLink(context.Background(), 1, 1); !errors.Is(err, torbox.ErrUnavailable) {
				t.Fatalf("RequestDownloadLink: want ErrUnavailable, got %v", err)
			}
			if _, err := c.CreateTorrent(context.Background(), hash); !errors.Is(err, torbox.ErrUnavailable) {
				t.Fatalf("CreateTorrent: want ErrUnavailable, got %v", err)
			}
		})
	}
}

func TestANullTorrentIsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":null}`))
	}))
	defer srv.Close()
	c, err := torbox.New(torbox.Options{APIKey: "k", Base: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetTorrent(context.Background(), 1); !errors.Is(err, torbox.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCancelledContextReturnsItsError(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	fake.Script("/user/me", torboxfake.Step{Hang: true})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := newClient(t, fake).Ping(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
}

func TestClientTimeoutIsUnavailable(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	fake.Script("/user/me", torboxfake.Step{Hang: true})
	c, err := torbox.New(torbox.Options{APIKey: torboxfake.Key, Base: fake.APIBase(), Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); !errors.Is(err, torbox.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

const (
	secretKey   = "key-that-must-not-leak"
	secretToken = "link-token-that-must-not-leak"
)

func TestNoErrorOrLogLineRepeatsTheKeyOrALinkToken(t *testing.T) {
	echo := func(status int, body string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, "%s %s %s", body, r.Header.Get("Authorization"), r.URL)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	closed := echo(200, "")
	closedURL := closed.URL
	closed.Close()

	servers := map[string]string{
		"network failure": closedURL,
		"http error":      echo(500, secretKey+" "+secretToken).URL,
		"bad json":        echo(200, `{"success":true,"data":"`+secretToken+`"`).URL,
		"rejected":        echo(401, secretKey).URL,
		"rate limited":    echo(429, secretKey).URL,
	}
	for name, base := range servers {
		t.Run(name, func(t *testing.T) {
			c, err := torbox.New(torbox.Options{APIKey: secretKey, Base: base})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			_, errGet := c.GetTorrent(ctx, 1)
			_, errLink := c.RequestDownloadLink(ctx, 1, 1)
			_, errCreate := c.CreateTorrent(ctx, hash)
			for _, err := range []error{c.Ping(ctx), errGet, errLink, errCreate, c.DeleteTorrent(ctx, 1)} {
				if err == nil {
					t.Fatal("want an error")
				}
				var logs bytes.Buffer
				slog.New(slog.NewTextHandler(&logs, nil)).Error("torbox", "err", err)
				for _, out := range []string{err.Error(), fmt.Sprintf("%v|%+v|%#v|%q", err, err, err, err), logs.String()} {
					if strings.Contains(out, secretKey) || strings.Contains(out, secretToken) {
						t.Fatalf("a secret leaked into %q", out)
					}
				}
			}
		})
	}
}
