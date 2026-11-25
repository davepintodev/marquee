package download_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/download"
	"github.com/davepintodev/marquee/internal/torbox"
	"github.com/davepintodev/marquee/internal/torboxfake"
)

const tbHash = "0123456789abcdef0123456789abcdef01234567"

type clock struct{ ns atomic.Int64 }

func newClock() *clock {
	c := &clock{}
	c.ns.Store(time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC).UnixNano())
	return c
}
func (c *clock) Now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *clock) Advance(d time.Duration) { c.ns.Add(int64(d)) }

type caching struct {
	mu      sync.Mutex
	percent []int
	torrent int
	file    int
	err     error
}

func (c *caching) on(percent, torrentID, fileID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.percent = append(c.percent, percent)
	c.torrent, c.file = torrentID, fileID
	return c.err
}

func (c *caching) seen() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.percent)
}

func (c *caching) ids() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.torrent, c.file
}

type rig struct {
	fake  *torboxfake.Fake
	clock *clock
	dl    *download.TorBox
	dest  string
}

func newRig(t *testing.T, mutate func(*download.TorBoxOptions)) *rig {
	t.Helper()
	fake := torboxfake.New()
	t.Cleanup(fake.Close)
	client, err := torbox.New(torbox.Options{APIKey: config.Secret(torboxfake.Key), Base: fake.APIBase()})
	if err != nil {
		t.Fatal(err)
	}
	clk := newClock()
	o := download.TorBoxOptions{
		Client: client, HTTP: download.NewHTTP(fast(download.Options{})),
		Poll: 2 * time.Millisecond, StallTimeout: 30 * time.Minute, MaxLifetime: 4 * time.Hour, Now: clk.Now,
	}
	if mutate != nil {
		mutate(&o)
	}
	return &rig{fake: fake, clock: clk, dl: download.NewTorBox(o), dest: filepath.Join(t.TempDir(), "x.part")}
}

type outcome struct {
	res download.Result
	err error
}

func (r *rig) start(ctx context.Context, req download.Request) <-chan outcome {
	if req.Dest == "" {
		req.Dest = r.dest
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := r.dl.Download(ctx, req)
		done <- outcome{res, err}
	}()
	return done
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func finish(t *testing.T, done <-chan outcome) outcome {
	t.Helper()
	select {
	case o := <-done:
		return o
	case <-time.After(10 * time.Second):
		t.Fatal("the download did not end")
		return outcome{}
	}
}

func TestACachedReleaseWithALinkGoesStraightToHTTPWithoutTouchingTorBox(t *testing.T) {
	r := newRig(t, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.ServeContent(w, req, "f.mkv", time.Time{}, strings.NewReader("cached bytes"))
	}))
	defer srv.Close()
	out := finish(t, r.start(context.Background(), download.Request{URL: config.Secret(srv.URL), Cached: true, InfoHash: tbHash}))
	if out.err != nil || out.res.Size != int64(len("cached bytes")) {
		t.Fatalf("got %+v", out)
	}
	if n := r.fake.Hits(""); n != 0 {
		t.Fatalf("TorBox was asked %d times for a cached release", n)
	}
}

func TestAnUncachedReleaseIsCreatedReportedPercentByPercentThenDownloaded(t *testing.T) {
	r := newRig(t, nil)
	body := []byte("the whole movie")
	r.fake.SetFiles(tbHash, torboxfake.File{ID: 4, Name: "Pack/Movie.2021.1080p.mkv", Size: int64(len(body)), Body: body})
	c := &caching{}
	done := r.start(context.Background(), download.Request{
		InfoHash: tbHash, FileName: "Movie.2021.1080p.mkv", ReleaseSize: int64(len(body)), OnCaching: c.on,
	})

	waitFor(t, "the torrent to be created", func() bool { id, _ := c.ids(); return id != 0 })
	id, _ := c.ids()
	if got := r.fake.Magnets(); len(got) != 1 || got[0] != "magnet:?xt=urn:btih:"+tbHash {
		t.Fatalf("magnets %v", got)
	}
	r.fake.Step(id, 0.42, 1000)
	waitFor(t, "42 percent", func() bool { return slices.Contains(c.seen(), 42) })
	r.fake.Present(id)

	out := finish(t, done)
	if out.err != nil || out.res.Size != int64(len(body)) {
		t.Fatalf("got %+v", out)
	}
	got, err := os.ReadFile(r.dest)
	if err != nil || string(got) != string(body) {
		t.Fatalf("downloaded %q, %v", got, err)
	}
	if _, file := c.ids(); file != 4 {
		t.Fatalf("the chosen file was reported as %d, want 4", file)
	}
	if len(r.fake.Deleted()) != 0 {
		t.Fatalf("the downloader must leave a finished torrent to the pipeline, deleted %v", r.fake.Deleted())
	}
}

func TestARestartKeepsWatchingTheSameTorrentInsteadOfCreatingAnother(t *testing.T) {
	r := newRig(t, nil)
	body := []byte("episode bytes")
	r.fake.SetFiles(tbHash, torboxfake.File{ID: 0, Name: "Show.S01E02.mkv", Size: int64(len(body)), Body: body})
	ctx, cancel := context.WithCancel(context.Background())
	c := &caching{}
	done := r.start(ctx, download.Request{InfoHash: tbHash, OnCaching: c.on})
	waitFor(t, "the torrent", func() bool { id, _ := c.ids(); return id != 0 })
	id, _ := c.ids()
	r.fake.Step(id, 0.3, 10)
	cancel()
	if out := finish(t, done); !errors.Is(out.err, context.Canceled) {
		t.Fatalf("a shutdown must return the context's error, got %v", out.err)
	}
	if len(r.fake.Deleted()) != 0 {
		t.Fatal("a shutdown must leave the torrent on TorBox")
	}

	r.fake.Present(id)
	out := finish(t, r.start(context.Background(), download.Request{InfoHash: tbHash, TorBoxTorrentID: id}))
	if out.err != nil {
		t.Fatal(out.err)
	}
	if len(r.fake.Magnets()) != 1 {
		t.Fatalf("a second torrent was created: %v", r.fake.Magnets())
	}
}

func TestATorrentThatMakesNoProgressForTheStallTimeoutIsDeletedAndFails(t *testing.T) {
	r := newRig(t, nil)
	c := &caching{}
	done := r.start(context.Background(), download.Request{InfoHash: tbHash, OnCaching: c.on})
	waitFor(t, "the torrent", func() bool { id, _ := c.ids(); return id != 0 })
	id, _ := c.ids()
	r.fake.Step(id, 0.2, 500)
	waitFor(t, "20 percent", func() bool { return slices.Contains(c.seen(), 20) })
	r.fake.Stall(id)
	r.clock.Advance(29 * time.Minute)
	time.Sleep(20 * time.Millisecond)
	select {
	case o := <-done:
		t.Fatalf("29 minutes without progress is not yet a stall: %+v", o)
	default:
	}
	r.clock.Advance(2 * time.Minute)

	f, ok := download.AsFailure(finish(t, done).err)
	if !ok || f.Reason != download.ReasonStalled {
		t.Fatalf("want a stalled failure, got %+v", f)
	}
	if got := r.fake.Deleted(); !slices.Equal(got, []int{id}) {
		t.Fatalf("deleted %v, want [%d]", got, id)
	}
}

func TestProgressKeepsAStallAtBayButNotTheLifetime(t *testing.T) {
	r := newRig(t, nil)
	c := &caching{}
	started := r.clock.Now()
	done := r.start(context.Background(), download.Request{InfoHash: tbHash, OnCaching: c.on, StartedAt: started})
	waitFor(t, "the torrent", func() bool { id, _ := c.ids(); return id != 0 })
	id, _ := c.ids()
	for i := 1; i <= 9; i++ {
		r.fake.Step(id, float64(i)/10, 100)
		waitFor(t, "a step", func() bool { return slices.Contains(c.seen(), i*10) })
		r.clock.Advance(25 * time.Minute)
		time.Sleep(10 * time.Millisecond)
	}
	r.fake.Step(id, 0.95, 100)
	waitFor(t, "95 percent", func() bool { return slices.Contains(c.seen(), 95) })
	r.clock.Advance(20 * time.Minute)
	f, ok := download.AsFailure(finish(t, done).err)
	if !ok || f.Reason != download.ReasonCachingTimeout {
		t.Fatalf("want caching_timeout once 4h have passed, got %+v", f)
	}
	if got := r.fake.Deleted(); !slices.Equal(got, []int{id}) {
		t.Fatalf("deleted %v", got)
	}
}

func TestTheLifetimeCountsFromWhenTheDownloadWasQueuedSoARestartDoesNotResetIt(t *testing.T) {
	r := newRig(t, nil)
	r.fake.Cached("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	created := make(chan struct{})
	var once sync.Once
	c := &caching{}
	queued := r.clock.Now().Add(-5 * time.Hour)
	done := r.start(context.Background(), download.Request{
		InfoHash: tbHash, StartedAt: queued, OnCaching: func(p, tid, fid int) error {
			once.Do(func() { close(created) })
			return c.on(p, tid, fid)
		},
	})
	<-created
	if f, ok := download.AsFailure(finish(t, done).err); !ok || f.Reason != download.ReasonCachingTimeout {
		t.Fatalf("got %+v", f)
	}
}

func TestARemovedTitleDeletesTheTorrentAndStopsCleanly(t *testing.T) {
	r := newRig(t, nil)
	c := &caching{err: download.ErrRemoved}
	out := finish(t, r.start(context.Background(), download.Request{InfoHash: tbHash, OnCaching: c.on}))
	if !errors.Is(out.err, download.ErrRemoved) {
		t.Fatalf("got %v", out.err)
	}
	if got := r.fake.Deleted(); len(got) != 1 {
		t.Fatalf("deleted %v", got)
	}
}

func TestAPackFileIsPickedByEpisodeAndAMismatchedSizeFails(t *testing.T) {
	pack := []torboxfake.File{
		{ID: 1, Name: "Show.S01/Show.S01E01.mkv", Size: 10, Body: []byte("episode one")[:10]},
		{ID: 2, Name: "Show.S01/Show.S01E02.mkv", Size: 11, Body: []byte("episode two")},
		{ID: 3, Name: "Show.S01/readme.txt", Size: 3, Body: []byte("abc")},
	}
	r := newRig(t, nil)
	r.fake.SetFiles(tbHash, pack...)
	r.fake.Cached(tbHash)
	out := finish(t, r.start(context.Background(), download.Request{InfoHash: tbHash, Season: 1, Number: 2, ReleaseSize: 11}))
	if out.err != nil {
		t.Fatal(out.err)
	}
	if got, _ := os.ReadFile(r.dest); string(got) != "episode two" {
		t.Fatalf("took %q", got)
	}

	r2 := newRig(t, nil)
	r2.fake.SetFiles(tbHash, pack...)
	r2.fake.Cached(tbHash)
	out = finish(t, r2.start(context.Background(), download.Request{InfoHash: tbHash, Season: 1, Number: 2, ReleaseSize: 999}))
	if f, ok := download.AsFailure(out.err); !ok || f.Reason != download.ReasonSizeMismatch {
		t.Fatalf("want size_mismatch, got %v", out.err)
	}

	r3 := newRig(t, nil)
	r3.fake.SetFiles(tbHash, pack...)
	r3.fake.Cached(tbHash)
	out = finish(t, r3.start(context.Background(), download.Request{InfoHash: tbHash, Season: 1, Number: 9}))
	if f, ok := download.AsFailure(out.err); !ok || f.Reason != download.ReasonNotVideo {
		t.Fatalf("an episode the pack lacks must fail, got %v", out.err)
	}
}

func TestTorBoxRefusingTheKeyIsNotTheReleasesFault(t *testing.T) {
	r := newRig(t, nil)
	r.fake.Reject()
	out := finish(t, r.start(context.Background(), download.Request{InfoHash: tbHash}))
	f, ok := download.AsFailure(out.err)
	if !ok || f.Reason != download.ReasonLocalError {
		t.Fatalf("got %v", out.err)
	}
	for _, leak := range []string{torboxfake.Key, "Bearer"} {
		if strings.Contains(f.Detail, leak) {
			t.Fatalf("the failure %q leaks %q", f.Detail, leak)
		}
	}
}

func TestATorrentTorBoxForgotAfterARestartFailsWithoutBlame(t *testing.T) {
	r := newRig(t, nil)
	out := finish(t, r.start(context.Background(), download.Request{InfoHash: tbHash, TorBoxTorrentID: 999}))
	if f, ok := download.AsFailure(out.err); !ok || f.Reason != download.ReasonLocalError {
		t.Fatalf("got %v", out.err)
	}
}

func TestATransientTorBoxOutageWhileCachingDoesNotFailTheDownload(t *testing.T) {
	r := newRig(t, nil)
	body := []byte("data")
	r.fake.SetFiles(tbHash, torboxfake.File{ID: 0, Name: "m.mkv", Size: 4, Body: body})
	c := &caching{}
	done := r.start(context.Background(), download.Request{InfoHash: tbHash, OnCaching: c.on})
	waitFor(t, "the torrent", func() bool { id, _ := c.ids(); return id != 0 })
	id, _ := c.ids()
	r.fake.Down("/torrents/mylist", http.StatusServiceUnavailable)
	time.Sleep(20 * time.Millisecond)
	r.fake.Heal()
	r.fake.Present(id)
	if out := finish(t, done); out.err != nil {
		t.Fatal(out.err)
	}
}
