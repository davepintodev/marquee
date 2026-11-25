package download_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/download"
)

func fast(o download.Options) download.Options {
	if o.StallTimeout == 0 {
		o.StallTimeout = 2 * time.Second
	}
	o.RateLimitWait, o.MaxRateLimitWait = 10*time.Millisecond, 50*time.Millisecond
	o.GrowEvery, o.ConnStagger, o.SaveEvery = 100*time.Millisecond, time.Nanosecond, 20*time.Millisecond
	o.ProgressEvery, o.RetryUnit, o.MinSteal = 10*time.Millisecond, time.Millisecond, 16<<10
	return o
}

func req(url, dest string) download.Request {
	return download.Request{URL: config.Secret(url), Dest: dest}
}

func gone(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s should not exist (stat error: %v)", p, err)
		}
	}
}

func TestDownloadFollowsTheRedirectAndWritesTheWholeFile(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	data := debridfake.MKV(600 << 10)
	url := cdn.Add("a.mkv", debridfake.File{Data: data})
	dest := filepath.Join(t.TempDir(), "a.part")

	var lastDone, lastTotal atomic.Int64
	r := req(url, dest)
	r.Progress = func(done, total int64) { lastDone.Store(done); lastTotal.Store(total) }
	res, err := download.NewHTTP(fast(download.Options{Connections: 4})).Download(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) || res.Size != int64(len(data)) {
		t.Fatalf("downloaded %d bytes (result %d), want the %d served", len(got), res.Size, len(data))
	}
	if lastDone.Load() != int64(len(data)) || lastTotal.Load() != int64(len(data)) {
		t.Errorf("last progress = %d of %d", lastDone.Load(), lastTotal.Load())
	}
	probes := 0
	for _, q := range cdn.Requests() {
		if q.Probe {
			probes++
		}
	}
	if probes == 0 {
		t.Error("the server never saw the one-byte range request that resolves the redirect")
	}
	gone(t, dest+".plan")
}

func TestDownloadWorksWithoutRangeSupport(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	data := debridfake.MKV(300 << 10)
	url := cdn.Add("a.mkv", debridfake.File{Data: data, NoRanges: true})
	dest := filepath.Join(t.TempDir(), "a.part")
	if _, err := download.NewHTTP(fast(download.Options{})).Download(context.Background(), req(url, dest)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, data) {
		t.Fatalf("single-stream download wrote %d bytes, want %d", len(got), len(data))
	}
}

func fails(t *testing.T, err error, want download.Reason) *download.Failure {
	t.Helper()
	f, ok := download.AsFailure(err)
	if !ok {
		t.Fatalf("err = %v, want a *Failure with reason %q", err, want)
	}
	if f.Reason != want {
		t.Fatalf("reason = %q (%s), want %q", f.Reason, f.Detail, want)
	}
	if strings.Contains(f.Detail, "SECRET") || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("the failure text leaks a link: %q", f.Detail)
	}
	return f
}

func TestStalledTransferFailsAndLeavesNothing(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	url := cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(400 << 10), StallAt: 100 << 10})
	dest := filepath.Join(t.TempDir(), "a.part")
	start := time.Now()
	_, err := download.NewHTTP(fast(download.Options{StallTimeout: 200 * time.Millisecond, Connections: 1})).Download(context.Background(), req(url, dest))
	_ = fails(t, err, download.ReasonStalled)
	if time.Since(start) > 5*time.Second {
		t.Errorf("a stalled transfer took %s to fail", time.Since(start))
	}
	gone(t, dest, dest+".plan")
}

func TestStallOnOneConnectionOfManyStillFailsOnlyWhenNothingMoves(t *testing.T) {
	// The server hangs at one offset; the other connections finish their
	// ranges, then nothing moves anywhere and the download fails.
	cdn := debridfake.New()
	defer cdn.Close()
	url := cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(800 << 10), StallAt: 300 << 10})
	dest := filepath.Join(t.TempDir(), "a.part")
	_, err := download.NewHTTP(fast(download.Options{StallTimeout: 300 * time.Millisecond, Connections: 4})).Download(context.Background(), req(url, dest))
	_ = fails(t, err, download.ReasonStalled)
	gone(t, dest, dest+".plan")
}

func TestTruncatedTransferFailsWithSizeMismatch(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	url := cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(400 << 10), TruncateAt: 200 << 10})
	dest := filepath.Join(t.TempDir(), "a.part")
	_, err := download.NewHTTP(fast(download.Options{Connections: 2, MaxRetries: 3})).Download(context.Background(), req(url, dest))
	_ = fails(t, err, download.ReasonSizeMismatch)
	gone(t, dest, dest+".plan")
}

func TestTruncatedSingleStreamFailsWithSizeMismatch(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	url := cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(400 << 10), TruncateAt: 200 << 10, NoRanges: true})
	dest := filepath.Join(t.TempDir(), "a.part")
	_, err := download.NewHTTP(fast(download.Options{})).Download(context.Background(), req(url, dest))
	_ = fails(t, err, download.ReasonSizeMismatch)
	gone(t, dest)
}

func TestReleaseSizeFarFromTheServersFails(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	url := cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(100 << 10)})
	dest := filepath.Join(t.TempDir(), "a.part")
	r := req(url, dest)
	r.ReleaseSize = 10 << 20
	_, err := download.NewHTTP(fast(download.Options{})).Download(context.Background(), r)
	_ = fails(t, err, download.ReasonSizeMismatch)
	if n := cdn.MaxConcurrent("a.mkv"); n != 0 {
		t.Errorf("the file was fetched (%d streams) although its size was already wrong", n)
	}
}

func TestReleaseSizeOffByOneByteFailsBeforeTransfer(t *testing.T) {
	data := debridfake.MKV(100 << 10)
	for _, tc := range []struct {
		name    string
		listed  int64
		wantErr bool
	}{
		{"one byte more", int64(len(data)) + 1, true},
		{"one byte less", int64(len(data)) - 1, true},
		{"a tenth of a percent off", int64(len(data)) * 1001 / 1000, true},
		{"exact", int64(len(data)), false},
		{"unknown", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cdn := debridfake.New()
			defer cdn.Close()
			url := cdn.Add("a.mkv", debridfake.File{Data: data})
			dest := filepath.Join(t.TempDir(), "a.part")
			r := req(url, dest)
			r.ReleaseSize = tc.listed
			_, err := download.NewHTTP(fast(download.Options{})).Download(context.Background(), r)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("download: %v", err)
				}
				return
			}
			_ = fails(t, err, download.ReasonSizeMismatch)
			if n := cdn.MaxConcurrent("a.mkv"); n != 0 {
				t.Errorf("the file was fetched (%d streams) although its size was wrong", n)
			}
			gone(t, dest, dest+".plan")
		})
	}
}

func TestUnknownPlaybackLinkFailsAsSourceDown(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	url := cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(1 << 10)})
	url = strings.Replace(url, "a.mkv", "missing.mkv", 1)
	_, err := download.NewHTTP(fast(download.Options{})).Download(context.Background(), req(url, filepath.Join(t.TempDir(), "a.part")))
	f := fails(t, err, download.ReasonSourceDown)
	if !strings.Contains(f.Detail, "404") {
		t.Errorf("detail %q does not say what the server answered", f.Detail)
	}
}

func TestUnreachableServerFailsAsSourceDownWithoutTheLink(t *testing.T) {
	cdn := debridfake.New()
	url := cdn.Add("a.mkv", debridfake.File{Data: debridfake.MKV(1 << 10)})
	cdn.Close()
	_, err := download.NewHTTP(fast(download.Options{})).Download(context.Background(), req(url, filepath.Join(t.TempDir(), "a.part")))
	_ = fails(t, err, download.ReasonSourceDown)
}

func TestCancelKeepsProgressAndTheNextRunResumes(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	data := debridfake.MKV(1 << 20)
	url := cdn.Add("a.mkv", debridfake.File{Data: data, Pace: 5 * time.Millisecond})
	dest := filepath.Join(t.TempDir(), "a.part")
	d := download.NewHTTP(fast(download.Options{Connections: 2}))

	ctx, cancel := context.WithCancel(context.Background())
	r := req(url, dest)
	r.Progress = func(done, _ int64) {
		if done >= 300<<10 {
			cancel()
		}
	}
	_, err := d.Download(ctx, r)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted download returned %v, want context.Canceled", err)
	}
	if _, ok := download.AsFailure(err); ok {
		t.Fatal("an interrupted download must not look like a failure")
	}
	for _, p := range []string{dest, dest + ".plan"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("an interrupted download must keep %s: %v", p, err)
		}
	}

	before := len(cdn.Requests())
	if _, err := d.Download(context.Background(), req(url, dest)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, data) {
		t.Fatal("the resumed file differs from the served one")
	}
	low := int64(-1)
	for _, q := range cdn.Requests()[before:] {
		if !q.Probe && q.RangeStart >= 0 && (low < 0 || q.RangeStart < low) {
			low = q.RangeStart
		}
	}
	if low <= 0 {
		t.Errorf("the second run's lowest range start is %d; it should resume past the start, not refetch it", low)
	}
}

func TestRateLimitedServerGetsFewerConnectionsAndTheFileStillArrives(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	data := debridfake.MKV(2 << 20)
	url := cdn.Add("a.mkv", debridfake.File{Data: data, Pace: 2 * time.Millisecond})
	cdn.Limit(2, "")
	dest := filepath.Join(t.TempDir(), "a.part")
	if _, err := download.NewHTTP(fast(download.Options{Connections: 6, MaxRetries: 20})).Download(context.Background(), req(url, dest)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, data) {
		t.Fatal("file differs after rate limiting")
	}
	rejected := 0
	for _, q := range cdn.Requests() {
		if q.Status == 429 {
			rejected++
		}
	}
	if rejected == 0 {
		for _, q := range cdn.Requests() {
			t.Logf("%+v", q)
		}
		t.Error("the fake never answered 429; the test proves nothing")
	}
}

func TestNeverMoreThanTenConnectionsPerFile(t *testing.T) {
	cdn := debridfake.New()
	defer cdn.Close()
	data := debridfake.MKV(4 << 20)
	url := cdn.Add("a.mkv", debridfake.File{Data: data, Pace: 3 * time.Millisecond})
	dest := filepath.Join(t.TempDir(), "a.part")
	if _, err := download.NewHTTP(fast(download.Options{Connections: 25})).Download(context.Background(), req(url, dest)); err != nil {
		t.Fatal(err)
	}
	if n := cdn.MaxConcurrent("a.mkv"); n > download.MaxConnections || n < 2 {
		t.Errorf("%d concurrent streams for one file, want between 2 and %d", n, download.MaxConnections)
	}
}
