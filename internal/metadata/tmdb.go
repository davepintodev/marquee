// Package metadata is marquee's view of TMDB: search, details, trending,
// episode lists, external ids and the images, which it keeps on disk.
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/davepintodev/marquee/internal/config"
)

// The errors callers branch on. None of them carries a URL or a key.
var (
	ErrNotFound    = errors.New("tmdb: not found")
	ErrRateLimited = errors.New("tmdb: rate limited")
	ErrUnavailable = errors.New("tmdb: not answering")
	ErrRejected    = errors.New("tmdb: refused the API key")
)

const (
	maxRetryAfter  = 5 * time.Second
	defaultWait    = time.Second
	maxJSONBytes   = 8 << 20
	defaultTimeout = 10 * time.Second
	// A stalled body would otherwise hold the image's lock for ever.
	defaultImageTimeout = 30 * time.Second
	defaultAPIBase      = "https://api.themoviedb.org/3"
	defaultImgBase      = "https://image.tmdb.org/t/p"
)

type Options struct {
	APIBase   string
	ImageBase string
	Key       config.Secret
	Timeout   time.Duration
	// ImageTimeout bounds one whole image download, body included.
	ImageTimeout time.Duration
	// Sleep waits between a 429 and its retry; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

type Client struct {
	apiBase, imageBase string
	key                config.Secret
	timeout            time.Duration
	imageTimeout       time.Duration
	http               *http.Client
	sleep              func(ctx context.Context, d time.Duration) error
}

func New(o Options) *Client {
	c := &Client{apiBase: o.APIBase, imageBase: o.ImageBase, key: o.Key, timeout: o.Timeout, imageTimeout: o.ImageTimeout, sleep: o.Sleep}
	if c.apiBase == "" {
		c.apiBase = defaultAPIBase
	}
	if c.imageBase == "" {
		c.imageBase = defaultImgBase
	}
	if c.timeout == 0 {
		c.timeout = defaultTimeout
	}
	if c.imageTimeout == 0 {
		c.imageTimeout = defaultImageTimeout
	}
	if c.sleep == nil {
		c.sleep = sleep
	}
	// Both kinds of call bound the whole exchange themselves; the transport
	// only cuts a connection that never answers.
	c.http = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: c.timeout, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}}
	return c
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Summary is what a search, trending or details call knows about a title.
type Summary struct {
	Kind         string // "movie" or "series"
	TMDBID       int
	Title        string
	Year         int // 0 when TMDB has no date
	Overview     string
	PosterPath   string // TMDB file path, empty when none
	BackdropPath string
}

type Episode struct {
	Number  int
	Name    string
	AirDate string // YYYY-MM-DD; empty while TMDB lists no date
}

type Season struct {
	Number   int
	Episodes []Episode
}

// Details is a Summary with the ids other sources need and, for a series,
// every season's episodes.
type Details struct {
	Summary
	IMDbID  string
	TVDBID  int
	Seasons []Season
}

type rawTitle struct {
	ID           int     `json:"id"`
	MediaType    string  `json:"media_type"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	Overview     string  `json:"overview"`
	PosterPath   *string `json:"poster_path"`
	BackdropPath *string `json:"backdrop_path"`
	ExternalIDs  struct {
		IMDbID *string `json:"imdb_id"`
		TVDBID *int    `json:"tvdb_id"`
	} `json:"external_ids"`
	Seasons []struct {
		SeasonNumber int `json:"season_number"`
	} `json:"seasons"`
}

func (r rawTitle) summary(kind string) Summary {
	s := Summary{Kind: kind, TMDBID: r.ID, Overview: r.Overview, Title: r.Title}
	date := r.ReleaseDate
	if kind == "series" {
		s.Title, date = r.Name, r.FirstAirDate
	}
	if len(date) >= 4 {
		s.Year, _ = strconv.Atoi(date[:4])
	}
	if r.PosterPath != nil {
		s.PosterPath = *r.PosterPath
	}
	if r.BackdropPath != nil {
		s.BackdropPath = *r.BackdropPath
	}
	return s
}

func kindOf(mediaType string) (string, bool) {
	switch mediaType {
	case "movie":
		return "movie", true
	case "tv":
		return "series", true
	}
	return "", false
}

func listOf(v struct {
	Results []rawTitle `json:"results"`
}) []Summary {
	out := []Summary{}
	for _, r := range v.Results {
		if kind, ok := kindOf(r.MediaType); ok {
			out = append(out, r.summary(kind))
		}
	}
	return out
}

// Search returns movies and series matching q, in TMDB's relevance order.
func (c *Client) Search(ctx context.Context, q string) ([]Summary, error) {
	var v struct {
		Results []rawTitle `json:"results"`
	}
	if err := c.getJSON(ctx, "/search/multi", url.Values{"query": {q}, "include_adult": {"false"}}, &v); err != nil {
		return nil, err
	}
	return listOf(v), nil
}

// Trending returns this week's trending movies and series.
func (c *Client) Trending(ctx context.Context) ([]Summary, error) {
	var v struct {
		Results []rawTitle `json:"results"`
	}
	if err := c.getJSON(ctx, "/trending/all/week", nil, &v); err != nil {
		return nil, err
	}
	return listOf(v), nil
}

func apiKind(kind string) string {
	if kind == "series" {
		return "tv"
	}
	return "movie"
}

// Summary is one title without its external ids or episodes.
func (c *Client) Summary(ctx context.Context, kind string, id int) (Summary, error) {
	var r rawTitle
	if err := c.getJSON(ctx, "/"+apiKind(kind)+"/"+strconv.Itoa(id), nil, &r); err != nil {
		return Summary{}, err
	}
	return r.summary(kind), nil
}

// Details fetches a title with its external ids and, for a series, every
// season but the specials (season 0), one request each. Nothing partial
// is returned: one failing season fails the call.
func (c *Client) Details(ctx context.Context, kind string, id int) (Details, error) {
	var r rawTitle
	path := "/" + apiKind(kind) + "/" + strconv.Itoa(id)
	if err := c.getJSON(ctx, path, url.Values{"append_to_response": {"external_ids"}}, &r); err != nil {
		return Details{}, err
	}
	d := Details{Summary: r.summary(kind)}
	if r.ExternalIDs.IMDbID != nil {
		d.IMDbID = *r.ExternalIDs.IMDbID
	}
	if r.ExternalIDs.TVDBID != nil {
		d.TVDBID = *r.ExternalIDs.TVDBID
	}
	if kind != "series" {
		return d, nil
	}
	for _, s := range r.Seasons {
		if s.SeasonNumber < 1 {
			continue
		}
		var sv struct {
			Episodes []struct {
				Number  int     `json:"episode_number"`
				Name    string  `json:"name"`
				AirDate *string `json:"air_date"`
			} `json:"episodes"`
		}
		if err := c.getJSON(ctx, fmt.Sprintf("%s/season/%d", path, s.SeasonNumber), nil, &sv); err != nil {
			return Details{}, err
		}
		season := Season{Number: s.SeasonNumber}
		for _, e := range sv.Episodes {
			ep := Episode{Number: e.Number, Name: e.Name}
			if e.AirDate != nil {
				ep.AirDate = *e.AirDate
			}
			season.Episodes = append(season.Episodes, ep)
		}
		d.Seasons = append(d.Seasons, season)
	}
	return d, nil
}

// FetchImage streams an image from TMDB's image host; the caller closes it.
func (c *Client) FetchImage(ctx context.Context, size, filePath string) (io.ReadCloser, error) {
	u, err := url.Parse(c.imageBase)
	if err != nil || !strings.HasPrefix(filePath, "/") {
		return nil, ErrNotFound
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + size + filePath
	ctx, cancel := context.WithTimeout(ctx, c.imageTimeout)
	resp, err := c.do(ctx, u, false) //nolint:bodyclose // the caller closes it through deadlineBody
	if err != nil {
		cancel()
		return nil, err
	}
	return &deadlineBody{ReadCloser: resp.Body, cancel: cancel}, nil
}

// deadlineBody keeps the download's deadline running until the body is closed.
type deadlineBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *deadlineBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	u, err := url.Parse(c.apiBase)
	if err != nil {
		return ErrUnavailable
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = q.Encode()
	resp, err := c.do(ctx, u, true)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBytes)).Decode(out); err != nil {
		return fmt.Errorf("%w: unreadable answer", ErrUnavailable)
	}
	return nil
}

// do sends one GET, honouring a 429's Retry-After once (capped), and maps
// every failure to one of the package errors. Errors from net/http quote
// the URL, which carries the key, so they never leave this function.
func (c *Client) do(ctx context.Context, u *url.URL, withKey bool) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.once(ctx, u, withKey)
		if err != nil {
			if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%w: no reply", ErrUnavailable)
		}
		switch resp.StatusCode {
		case http.StatusOK:
			return resp, nil
		case http.StatusTooManyRequests:
			wait := retryAfter(resp.Header.Get("Retry-After"))
			_ = resp.Body.Close()
			if attempt > 0 {
				return nil, ErrRateLimited
			}
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNotFound:
			return nil, ErrNotFound
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, ErrRejected
		}
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
}

func (c *Client) once(ctx context.Context, u *url.URL, withKey bool) (*http.Response, error) {
	u2 := *u
	var bearer string
	if withKey {
		// A v4 read token is a JWT and travels as a bearer token; a v3 key is a query parameter.
		if k := c.key.Reveal(); strings.HasPrefix(k, "eyJ") {
			bearer = k
		} else {
			q := u2.Query()
			q.Set("api_key", k)
			u2.RawQuery = q.Encode()
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u2.String(), nil)
	if err != nil {
		return nil, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return c.http.Do(req)
}

func retryAfter(h string) time.Duration {
	d := defaultWait
	if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && n >= 0 {
		d = time.Duration(n) * time.Second
	} else if t, err := http.ParseTime(h); err == nil {
		d = time.Until(t)
	}
	return min(max(d, 0), maxRetryAfter)
}
