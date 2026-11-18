package download

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MaxConnections is TorBox's per-link limit: an 11th concurrent request gets
// HTTP 429.
const MaxConnections = 10

// Options tunes the HTTP downloader; the zero value is production.
type Options struct {
	// Connections is the parallel ranges per file, at most MaxConnections.
	Connections int
	// StallTimeout is how long a transfer may go without a byte arriving on
	// any connection before it fails. Default one minute.
	StallTimeout time.Duration
	// RateLimitWait is the first wait after HTTP 429/503 without a
	// Retry-After; it doubles per repeat up to MaxRateLimitWait.
	RateLimitWait, MaxRateLimitWait time.Duration
	// GrowEvery is the 429-free time before a closed connection is added back.
	GrowEvery time.Duration
	// ConnStagger spaces the opening of the connections.
	ConnStagger time.Duration
	// SaveEvery is how often the progress record is written.
	SaveEvery time.Duration
	// ProgressEvery is how often Request.Progress is called.
	ProgressEvery time.Duration
	// RetryUnit is the base of the wait after a failed request: it doubles
	// per consecutive failure up to sixty units.
	RetryUnit time.Duration
	// MaxRetries is how many consecutive requests without progress a
	// connection tries before the download fails.
	MaxRetries int
	// MinSteal is the smallest half an idle connection takes over from a busy one.
	MinSteal int64
	// ResolveTimeout bounds the redirect-resolving probe.
	ResolveTimeout time.Duration
	Log            *slog.Logger
	// Transport replaces the HTTP/1.1 transport; tests use it.
	Transport http.RoundTripper
}

func (o Options) withDefaults() Options {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	if o.Connections <= 0 || o.Connections > MaxConnections {
		o.Connections = MaxConnections
	}
	def(&o.StallTimeout, time.Minute)
	def(&o.RateLimitWait, 15*time.Second)
	def(&o.MaxRateLimitWait, 5*time.Minute)
	def(&o.GrowEvery, time.Minute)
	def(&o.ConnStagger, 250*time.Millisecond)
	def(&o.SaveEvery, 10*time.Second)
	def(&o.ProgressEvery, time.Second)
	def(&o.RetryUnit, time.Second)
	def(&o.ResolveTimeout, 2*time.Minute)
	if o.MaxRetries <= 0 {
		o.MaxRetries = 8
	}
	if o.MinSteal <= 0 {
		o.MinSteal = 64 << 20
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return o
}

// HTTP downloads a file straight from a debrid link: it follows the
// playback redirect to the CDN, then fetches the file with parallel range
// requests.
type HTTP struct {
	opt    Options
	client *http.Client
}

func NewHTTP(opt Options) *HTTP {
	opt = opt.withDefaults()
	rt := opt.Transport
	if rt == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		// HTTP/1.1 only: HTTP/2 would multiplex every range request over one
		// TCP connection, which is exactly the per-connection limit being
		// worked around. The clone's TLS config still offers h2 via ALPN, so
		// pin NextProtos as well.
		tr.Protocols = new(http.Protocols)
		tr.Protocols.SetHTTP1(true)
		tr.TLSClientConfig = &tls.Config{NextProtos: []string{"http/1.1"}}
		tr.MaxIdleConnsPerHost = opt.Connections * 2
		tr.ResponseHeaderTimeout = 60 * time.Second
		tr.DisableCompression = true
		rt = tr
	}
	return &HTTP{opt: opt, client: &http.Client{Transport: rt}}
}

var errTruncated = errors.New("the server ended the transfer early")

type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("the server answered HTTP %d", e.status) }

// tracker is the shared state of one transfer.
type tracker struct {
	done     atomic.Int64
	last     atomic.Int64 // unix nanoseconds of the last byte, or of the last moment nothing was in flight
	inflight atomic.Int32
	stalled  atomic.Bool
}

func (t *tracker) add(n int64) {
	t.done.Add(n)
	t.last.Store(time.Now().UnixNano())
}

// watch cancels the transfer when requests are in flight but no byte has
// arrived on any of them for timeout. Time spent waiting between requests
// (a rate-limit pause, a retry) does not count: those waits are bounded by
// the retry limit instead.
func (t *tracker) watch(ctx context.Context, cancel context.CancelFunc, timeout time.Duration) {
	tick := time.NewTicker(max(timeout/5, time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			if t.inflight.Load() == 0 {
				t.last.Store(now.UnixNano())
				continue
			}
			if now.UnixNano()-t.last.Load() > int64(timeout) {
				t.stalled.Store(true)
				cancel()
				return
			}
		}
	}
}

func (h *HTTP) Download(ctx context.Context, req Request) (Result, error) {
	res, err := h.download(ctx, req)
	if _, ok := AsFailure(err); ok {
		_ = os.Remove(req.Dest)
		_ = os.Remove(req.Dest + ".plan")
	}
	return res, err
}

func (h *HTTP) download(ctx context.Context, req Request) (Result, error) {
	playback := req.URL.Reveal()
	planPath := req.Dest + ".plan"

	cdnURL, total, ranged, err := h.resolve(ctx, playback)
	if err != nil {
		return Result{}, h.classify(ctx, err)
	}
	if total <= 0 {
		return Result{}, &Failure{ReasonSourceDown, "the server did not say how big the file is"}
	}
	// AIOStreams answers failures with a short "error" video; a wildly
	// different size means that.
	if r := req.ReleaseSize; r > 0 && (total < r*95/100 || total > r*105/100) {
		return Result{}, &Failure{ReasonSizeMismatch, fmt.Sprintf("the server has a %d-byte file but the release is listed at %d bytes (probably an error video)", total, r)}
	}

	var p *plan
	tr := &tracker{}
	if ranged {
		p = loadPlan(planPath, total, h.opt.MinSteal)
		if fi, err := os.Stat(req.Dest); p == nil || err != nil || fi.Size() != total {
			p = newPlan(planPath, total, h.opt.Connections, h.opt.MinSteal)
			_ = os.Remove(req.Dest)
		}
		tr.done.Store(total - p.remaining())
	} else {
		_ = os.Remove(planPath)
		_ = os.Remove(req.Dest)
	}
	mode := fmt.Sprintf("%d connections", h.opt.Connections)
	if !ranged {
		mode = "a single stream (no range support)"
	} else if tr.done.Load() > 0 {
		mode += fmt.Sprintf(", resuming at %.1f%%", float64(tr.done.Load())*100/float64(total))
	}
	h.opt.Log.Info("downloading", "gib", fmt.Sprintf("%.2f", float64(total)/(1<<30)), "host", hostOf(cdnURL), "mode", mode)

	tr.last.Store(time.Now().UnixNano())
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go tr.watch(runCtx, cancel, h.opt.StallTimeout)
	reported := h.report(runCtx, req.Progress, tr, total)

	if ranged {
		err = h.parallel(runCtx, req.Dest, p, &cdn{url: cdnURL, refreshed: time.Now()}, playback, tr)
	} else {
		err = h.single(runCtx, req.Dest, cdnURL, total, tr)
	}
	cancel()
	<-reported
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err() // shutdown: the partial file and its progress stay for the next run
		}
		if tr.stalled.Load() {
			return Result{}, &Failure{ReasonStalled, fmt.Sprintf("no data arrived for %s", h.opt.StallTimeout)}
		}
		return Result{}, h.classify(ctx, err)
	}
	if req.Progress != nil {
		req.Progress(total, total)
	}
	_ = os.Remove(planPath)
	return Result{Size: total}, nil
}

// classify turns a transfer error into the Failure the caller records. A
// cancelled ctx is returned as is.
func (h *HTTP) classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var se *statusError
	switch {
	case errors.As(err, &se):
		return &Failure{ReasonSourceDown, se.Error()}
	case errors.Is(err, errTruncated), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return &Failure{ReasonSizeMismatch, "the server ended the transfer before the whole file arrived: " + err.Error()}
	}
	return &Failure{ReasonSourceDown, "could not download from the server: " + err.Error()}
}

func (h *HTTP) report(ctx context.Context, progress func(done, total int64), tr *tracker, total int64) <-chan struct{} {
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if progress == nil {
			return
		}
		t := time.NewTicker(h.opt.ProgressEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				progress(tr.done.Load(), total)
			}
		}
	}()
	return stopped
}

// cdn holds the resolved (post-redirect) download URL, refreshable if it expires.
type cdn struct {
	mu        sync.Mutex
	url       string
	refreshed time.Time
}

// resolve follows the playback redirects with a 1-byte range request and
// reports the final URL, total size and whether ranges work.
func (h *HTTP) resolve(ctx context.Context, playback string) (final string, total int64, ranged bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, h.opt.ResolveTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, playback, nil)
	if err != nil {
		return "", 0, false, safeErr(err)
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", "marquee/1")
	resp, err := h.client.Do(req)
	if err != nil {
		return "", 0, false, safeErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	final = resp.Request.URL.String()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		cr := resp.Header.Get("Content-Range")
		if i := strings.LastIndexByte(cr, '/'); i >= 0 {
			total, _ = strconv.ParseInt(cr[i+1:], 10, 64)
		}
		return final, total, total > 0, nil
	case http.StatusOK:
		return final, resp.ContentLength, false, nil
	default:
		return "", 0, false, &statusError{resp.StatusCode}
	}
}

// parallel downloads the missing ranges of p with up to Connections
// connections, one long request per segment (like aria2/curl): many short
// range requests get HTTP 429 from the CDN. Connections close on 429s and
// come back slowly; a connection that runs out of work takes over half of
// the largest remaining segment.
func (h *HTTP) parallel(ctx context.Context, dst string, p *plan, src *cdn, playback string, tr *tracker) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // the file becomes library media, which must be readable by the media server
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() != p.Total {
		if err := f.Truncate(p.Total); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	lim := newLimiter(h.opt.Connections, h.opt.RateLimitWait, h.opt.GrowEvery)
	exits := make(chan error)
	running := 0
	start := func(delay time.Duration) {
		running++
		go func() {
			sleep(ctx, delay)
			exits <- h.conn(ctx, f, p, lim, src, playback, tr)
		}()
	}
	// Staggered, so the CDN does not see a burst of simultaneous requests.
	for i := 0; lim.grow(); i++ {
		start(time.Duration(i) * h.opt.ConnStagger)
	}
	tick := time.NewTicker(h.opt.SaveEvery)
	defer tick.Stop()
	save := func() {
		// The plan must never claim bytes the disk may not have.
		if err := f.Sync(); err != nil {
			h.opt.Log.Warn("cannot flush the partial download", "err", err)
			return
		}
		if err := p.save(); err != nil {
			h.opt.Log.Warn("cannot save download progress", "err", err)
		}
	}
	var firstErr error
	for running > 0 {
		select {
		case err := <-exits:
			running--
			if err != nil && firstErr == nil {
				firstErr = err
				cancel()
			}
		case <-tick.C:
			save()
			if firstErr == nil && lim.grow() {
				start(0)
			}
		}
	}
	save()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n := p.remaining(); n > 0 {
		return fmt.Errorf("%w: %d bytes still missing", errTruncated, n)
	}
	return nil
}

// conn is one connection's loop: take a segment, download it, repeat until
// no work is left. It resumes after errors and gives up only after
// MaxRetries consecutive attempts without progress. It returns nil when
// finished or closed by the limiter.
func (h *HTTP) conn(ctx context.Context, f *os.File, p *plan, lim *limiter, src *cdn, playback string, tr *tracker) error {
	buf := make([]byte, 256<<10)
	var s *segment
	closed := false
	defer func() {
		if s != nil {
			p.release(s)
		}
		if !closed {
			lim.leave()
		}
	}()
	stalls, limited := 0, 0
	for {
		if s == nil {
			if s = p.take(); s == nil {
				return nil
			}
		}
		src.mu.Lock()
		u := src.url
		src.mu.Unlock()
		n, status, retryAfter, err := h.getRange(ctx, f, u, p, s, buf, tr)
		if err == nil {
			p.release(s)
			s, stalls, limited = nil, 0, 0
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if n > 0 {
			stalls = 0
		}
		stalls++
		rateLimited := status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
		if rateLimited {
			var target int
			if closed, target = lim.throttled(); closed {
				h.opt.Log.Info("the server is rate-limiting", "status", status, "connections", target)
				return nil
			}
			limited++
		} else {
			limited = 0
		}
		if stalls > h.opt.MaxRetries {
			pos, _ := p.bounds(s)
			return fmt.Errorf("at byte %d: %w", pos, err)
		}
		wait := h.backoff(stalls, limited, retryAfter)
		h.opt.Log.Info("retrying a range", "err", err, "attempt", stalls, "of", h.opt.MaxRetries, "in", wait.Round(time.Millisecond))
		sleep(ctx, wait)
		// Re-resolve only when the link may have expired: on 429/503 another
		// request to the same link only adds to the load the CDN is refusing.
		if !rateLimited && stalls >= 3 {
			h.refresh(ctx, src, playback)
		}
	}
}

// backoff is the wait before a retry: after the limited-th consecutive
// 429/503 it doubles from RateLimitWait (at least Retry-After, at most
// MaxRateLimitWait); after other errors it doubles from RetryUnit up to sixty
// units. Jitter of up to 50% keeps connections from retrying in lockstep.
func (h *HTTP) backoff(stalls, limited int, retryAfter time.Duration) time.Duration {
	var d time.Duration
	if limited > 0 {
		d = min(max(h.opt.RateLimitWait<<min(limited-1, 8), retryAfter), h.opt.MaxRateLimitWait)
	} else {
		d = time.Duration(min(1<<stalls, 60)) * h.opt.RetryUnit
	}
	return d + rand.N(d/2+1)
}

// getRange downloads the rest of segment s into f, stopping early if another
// connection takes over its upper half. It returns the bytes written and, on
// failure, the HTTP status (0 for network errors) and any Retry-After.
func (h *HTTP) getRange(ctx context.Context, f *os.File, u string, p *plan, s *segment, buf []byte, tr *tracker) (int64, int, time.Duration, error) {
	pos, end := p.bounds(s)
	if pos >= end {
		return 0, 0, 0, nil
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, 0, 0, safeErr(err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", pos, end-1))
	req.Header.Set("User-Agent", "marquee/1")
	tr.inflight.Add(1)
	defer tr.inflight.Add(-1)
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, 0, 0, safeErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusPartialContent {
		var retryAfter time.Duration
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			retryAfter = time.Duration(secs) * time.Second
		}
		return 0, resp.StatusCode, retryAfter, &statusError{resp.StatusCode}
	}
	// Per-request watchdog: abort a request that gets no bytes for
	// StallTimeout, so its connection can retry while others progress.
	timer := time.AfterFunc(h.opt.StallTimeout, cancel)
	defer timer.Stop()
	var written int64
	for pos < end {
		n, rerr := resp.Body.Read(buf[:min(int64(len(buf)), end-pos)])
		if n > 0 {
			timer.Reset(h.opt.StallTimeout)
			k := p.claim(s, int64(n))
			if _, err := f.WriteAt(buf[:k], pos); err != nil {
				p.claim(s, 0)
				return written, 0, 0, err
			}
			written += k
			tr.add(k)
			pos, end = p.advance(s)
		}
		if rerr != nil && pos < end {
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				rerr = fmt.Errorf("%w: %d bytes missing", errTruncated, end-pos)
			}
			return written, 0, 0, safeErr(rerr)
		}
	}
	return written, 0, 0, nil
}

// refresh re-resolves the playback URL (CDN links can expire), at most every 30s.
func (h *HTTP) refresh(ctx context.Context, src *cdn, playback string) {
	src.mu.Lock()
	defer src.mu.Unlock()
	if time.Since(src.refreshed) < 30*time.Second {
		return
	}
	src.refreshed = time.Now()
	if u, _, ranged, err := h.resolve(ctx, playback); err == nil && ranged {
		src.url = u
		h.opt.Log.Info("re-resolved the CDN link", "host", hostOf(u))
	} else if err != nil {
		h.opt.Log.Warn("could not re-resolve the CDN link", "err", err)
	}
}

// single downloads a file whose server does not support ranges. It cannot resume.
func (h *HTTP) single(ctx context.Context, dst, u string, total int64, tr *tracker) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return safeErr(err)
	}
	req.Header.Set("User-Agent", "marquee/1")
	tr.inflight.Add(1)
	defer tr.inflight.Add(-1)
	resp, err := h.client.Do(req)
	if err != nil {
		return safeErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &statusError{resp.StatusCode}
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec // see parallel
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.CopyBuffer(f, &countingReader{r: resp.Body, tr: tr}, make([]byte, 1<<20))
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: got %d of %d bytes", errTruncated, tr.done.Load(), total)
		}
		return safeErr(err)
	}
	if tr.done.Load() != total {
		return fmt.Errorf("%w: got %d of %d bytes", errTruncated, tr.done.Load(), total)
	}
	return f.Sync()
}

type countingReader struct {
	r  io.Reader
	tr *tracker
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.tr.add(int64(n))
	}
	return n, err
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// safeErr drops the URL a *url.Error carries: playback and CDN links hold
// credentials.
func safeErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s %s: %w", ue.Op, hostOf(ue.URL), ue.Err)
	}
	return err
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "?"
}
