// Package subfit tells subfit about library changes with a small webhook.
// Sending is asynchronous and best effort: subfit's nightly scan is the
// backstop, so an event is sent once and a failure is only a warning.
package subfit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/davepintodev/marquee/internal/config"
)

const (
	// DefaultTimeout bounds one webhook.
	DefaultTimeout = 5 * time.Second
	// DefaultCloseGrace is how long Close lets requests in flight finish.
	DefaultCloseGrace = 2 * time.Second

	queueSize = 1024
	workers   = 4
)

// Options configure a Client.
type Options struct {
	// URL is subfit's base address; the hook is at <URL>/hook/marquee. It is
	// a Secret because an address can carry credentials.
	URL config.Secret
	// Timeout bounds one webhook; zero is DefaultTimeout.
	Timeout time.Duration
	// CloseGrace is how long Close waits for requests in flight before it
	// abandons them; zero is DefaultCloseGrace.
	CloseGrace time.Duration
	Log        *slog.Logger
}

type event struct {
	Event        string `json:"event"`
	Library      string `json:"library"`
	Path         string `json:"path"`
	PreviousPath string `json:"previousPath,omitempty"`
}

// Client sends webhooks. Its methods return at once, and a nil *Client does
// nothing, so a marquee without a subfit address needs no checks.
type Client struct {
	hook   string
	client *http.Client
	trans  *http.Transport
	log    *slog.Logger
	grace  time.Duration
	queue  chan event
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
	ctx    context.Context
}

// New returns a client for the subfit at o.URL. The error never holds the address.
func New(o Options) (*Client, error) {
	u, err := url.Parse(o.URL.Reveal())
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("is not a valid http(s) URL")
	}
	timeout, grace := o.Timeout, o.CloseGrace
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if grace <= 0 {
		grace = DefaultCloseGrace
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	trans := http.DefaultTransport.(*http.Transport).Clone()
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		hook:  strings.TrimRight(o.URL.Reveal(), "/") + "/hook/marquee",
		trans: trans,
		client: &http.Client{
			Timeout:   timeout,
			Transport: trans,
			// Sent once, to the address configured: a redirect is a failure.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		log: log, grace: grace, queue: make(chan event, queueSize), cancel: cancel, ctx: ctx,
	}
	for range workers {
		c.wg.Add(1)
		go c.work()
	}
	return c, nil
}

// Library is the name subfit knows the library of a title kind by.
func Library(kind string) string {
	if kind == "movie" {
		return "movies"
	}
	return "series"
}

// Import tells subfit a file has been imported. path is relative to the library's root.
func (c *Client) Import(library, path string) {
	c.send(event{Event: "import", Library: library, Path: path})
}

// Replace tells subfit that previous was swapped for path.
func (c *Client) Replace(library, path, previous string) {
	c.send(event{Event: "replace", Library: library, Path: path, PreviousPath: previous})
}

// Delete tells subfit a file is gone.
func (c *Client) Delete(library, path string) {
	c.send(event{Event: "delete", Library: library, Path: path})
}

func (c *Client) send(e event) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.queue <- e:
	default:
		c.log.Warn("subfit webhook dropped: too many are waiting", "event", e.Event, "path", e.Path)
	}
}

// Close stops taking events, lets the requests in flight finish for the
// grace period, then abandons them and returns. It is safe to call twice.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.queue)
	}
	c.mu.Unlock()
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(c.grace):
		c.cancel()
		<-done
	}
	c.cancel()
	c.trans.CloseIdleConnections()
}

func (c *Client) work() {
	defer c.wg.Done()
	for e := range c.queue {
		if c.ctx.Err() != nil {
			continue
		}
		if err := c.post(e); err != nil && c.ctx.Err() == nil {
			c.log.Warn("subfit webhook failed; its nightly scan will catch up", "event", e.Event, "path", e.Path, "err", err)
		}
	}
}

// post sends one webhook. Its errors never hold the address or the path.
func (c *Client) post(e event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return errors.New("cannot encode the event")
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, c.hook, bytes.NewReader(body))
	if err != nil {
		return errors.New("the subfit URL is not valid")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return errors.New("subfit did not answer in time")
		}
		return errors.New("subfit is not reachable")
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("subfit answered HTTP %d", resp.StatusCode)
	}
	return nil
}
