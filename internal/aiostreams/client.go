package aiostreams

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/davepintodev/marquee/internal/config"
)

// The errors callers branch on. None carries the instance's address or
// credentials, so each may be logged and shown.
var (
	ErrRateLimited = errors.New("aiostreams: rate limited")
	ErrRejected    = errors.New("aiostreams: refused the credentials")
	ErrUnavailable = errors.New("aiostreams: not answering")
)

const (
	defaultTimeout = 90 * time.Second
	maxBodyBytes   = 32 << 20
)

var manifestRe = regexp.MustCompile(`^(https?://[^/]+(?:/[^/]+)*?)/stremio/([^/]+)/([^/]+)/manifest\.json$`)

// ErrManifest is returned for a manifest URL of the wrong shape; its text
// never repeats the URL.
var ErrManifest = errors.New("must look like https://<host>/stremio/<uuid>/<password>/manifest.json")

type Options struct {
	// Manifest is the instance's Stremio manifest URL. The search API's
	// address and its Basic-auth credentials (the uuid and the encrypted
	// password, which the API accepts) are derived from it.
	Manifest config.Secret
	// Timeout bounds one search; zero is ninety seconds, because a search
	// fans out to every addon and the debrid.
	Timeout time.Duration
}

type Client struct {
	base       string
	user, pass config.Secret
	http       *http.Client
}

func New(o Options) (*Client, error) {
	m := manifestRe.FindStringSubmatch(o.Manifest.Reveal())
	if m == nil {
		return nil, ErrManifest
	}
	if o.Timeout == 0 {
		o.Timeout = defaultTimeout
	}
	return &Client{base: m[1], user: config.Secret(m[2]), pass: config.Secret(m[3]), http: &http.Client{Timeout: o.Timeout}}, nil
}

// Search returns the releases for a Stremio id of the given type
// ("movie" or "series"): tt123, tmdb:1, tt123:1:2 or tvdb:1:1:2. No
// results is an empty slice and a nil error; anything that stops AIOStreams
// from answering is one of the errors above, so a caller never mistakes an
// outage for an empty catalogue.
func (c *Client) Search(ctx context.Context, typ, id string) ([]Stream, error) {
	q := url.Values{"type": {typ}, "id": {id}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/search?"+q.Encode(), nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	req.SetBasicAuth(c.user.Reveal(), c.pass.Reveal())
	req.Header.Set("User-Agent", "marquee")
	resp, err := c.http.Do(req)
	if err != nil {
		// A *url.Error quotes the request URL, which holds the host.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrRejected
	case resp.StatusCode != http.StatusOK:
		return nil, ErrUnavailable
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Results []Stream `json:"results"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body); err != nil || !body.Success {
		return nil, ErrUnavailable
	}
	if body.Data.Results == nil {
		return []Stream{}, nil
	}
	return body.Data.Results, nil
}
