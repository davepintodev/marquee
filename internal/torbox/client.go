// Package torbox is a small client for TorBox's REST API: enough to start a
// torrent from an info hash, watch it cache, fetch a file's download link
// and delete the torrent again.
package torbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/davepintodev/marquee/internal/config"
)

// The errors callers branch on. None carries the API key, a link or the
// request's address, so each may be logged and shown.
var (
	ErrRateLimited = errors.New("torbox: rate limited")
	ErrRejected    = errors.New("torbox: refused the API key")
	ErrUnavailable = errors.New("torbox: not answering")
	ErrNotFound    = errors.New("torbox: no such torrent")
	ErrBadInfoHash = errors.New("torbox: info hash must be 40 hex or 32 base32 characters")
	ErrNoKey       = errors.New("torbox: an API key is required")
)

const (
	// DefaultBase is TorBox's production API.
	DefaultBase    = "https://api.torbox.app/v1/api"
	defaultTimeout = 30 * time.Second
	maxBodyBytes   = 8 << 20
)

var infoHashRe = regexp.MustCompile(`^(?:[0-9A-Fa-f]{40}|[A-Za-z2-7]{32})$`)

type Options struct {
	APIKey config.Secret
	// Base is the API root; empty is DefaultBase. Tests point it at torboxfake.
	Base string
	// Timeout bounds one request; zero is thirty seconds.
	Timeout time.Duration
}

type File struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	Size      int64  `json:"size"`
}

type Torrent struct {
	ID   int    `json:"id"`
	Hash string `json:"hash"`
	Name string `json:"name"`
	// Progress is 0..1 while TorBox caches the torrent.
	Progress float64 `json:"progress"`
	// DownloadSpeed is bytes a second.
	DownloadSpeed   int64  `json:"download_speed"`
	DownloadPresent bool   `json:"download_present"`
	DownloadState   string `json:"download_state"`
	Files           []File `json:"files"`
}

type Client struct {
	base string
	key  config.Secret
	http *http.Client
}

func New(o Options) (*Client, error) {
	if o.APIKey.Reveal() == "" {
		return nil, ErrNoKey
	}
	if o.Base == "" {
		o.Base = DefaultBase
	}
	if o.Timeout == 0 {
		o.Timeout = defaultTimeout
	}
	return &Client{
		base: strings.TrimRight(o.Base, "/"),
		key:  o.APIKey,
		http: &http.Client{
			Timeout: o.Timeout,
			// A redirect would carry the key to wherever it points.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Ping checks that TorBox answers and accepts the key.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/user/me", nil, "", nil)
	if errors.Is(err, ErrNotFound) {
		return ErrUnavailable
	}
	return err
}

// CreateTorrent asks TorBox to fetch the torrent with this info hash. When
// TorBox already holds it, the torrent is present at once. Only ID and Hash
// are filled; GetTorrent has the rest.
func (c *Client) CreateTorrent(ctx context.Context, infoHash string) (Torrent, error) {
	if !infoHashRe.MatchString(infoHash) {
		return Torrent{}, ErrBadInfoHash
	}
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	if err := mw.WriteField("magnet", "magnet:?xt=urn:btih:"+infoHash); err != nil {
		return Torrent{}, ErrUnavailable
	}
	if err := mw.Close(); err != nil {
		return Torrent{}, ErrUnavailable
	}
	var out struct {
		TorrentID int    `json:"torrent_id"`
		Hash      string `json:"hash"`
	}
	data, err := c.do(ctx, http.MethodPost, "/torrents/createtorrent", nil, mw.FormDataContentType(), &form)
	if errors.Is(err, ErrNotFound) {
		return Torrent{}, ErrUnavailable
	}
	if err != nil {
		return Torrent{}, err
	}
	if json.Unmarshal(data, &out) != nil || out.TorrentID == 0 {
		return Torrent{}, ErrUnavailable
	}
	return Torrent{ID: out.TorrentID, Hash: out.Hash}, nil
}

// GetTorrent reads a torrent's state, bypassing TorBox's cache. A torrent
// TorBox no longer has is ErrNotFound.
func (c *Client) GetTorrent(ctx context.Context, torrentID int) (Torrent, error) {
	q := url.Values{"id": {strconv.Itoa(torrentID)}, "bypass_cache": {"true"}}
	data, err := c.do(ctx, http.MethodGet, "/torrents/mylist", q, "", nil)
	if err != nil {
		return Torrent{}, err
	}
	if string(bytes.TrimSpace(data)) == "null" {
		return Torrent{}, ErrNotFound
	}
	var t Torrent
	if json.Unmarshal(data, &t) != nil || t.ID == 0 {
		return Torrent{}, ErrUnavailable
	}
	return t, nil
}

// RequestDownloadLink returns a direct link to one file of a present
// torrent. The link carries a token, so it stays a config.Secret.
func (c *Client) RequestDownloadLink(ctx context.Context, torrentID, fileID int) (config.Secret, error) {
	q := url.Values{"torrent_id": {strconv.Itoa(torrentID)}, "file_id": {strconv.Itoa(fileID)}, "redirect": {"false"}}
	data, err := c.do(ctx, http.MethodGet, "/torrents/requestdl", q, "", nil)
	if err != nil {
		return "", err
	}
	var link string
	if json.Unmarshal(data, &link) != nil || link == "" {
		return "", ErrUnavailable
	}
	return config.Secret(link), nil
}

// DeleteTorrent removes a torrent from the account.
func (c *Client) DeleteTorrent(ctx context.Context, torrentID int) error {
	body, err := json.Marshal(map[string]any{"torrent_id": torrentID, "operation": "delete"})
	if err != nil {
		return ErrUnavailable
	}
	_, err = c.do(ctx, http.MethodPost, "/torrents/controltorrent", nil, "application/json", bytes.NewReader(body))
	return err
}

// do returns the "data" member of a successful answer. Every failure is one
// of the package's sentinels: a *url.Error would quote the request URL and a
// decode error can quote the body, either of which may hold a secret.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, contentType string, body io.Reader) (json.RawMessage, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+c.key.Reveal())
	req.Header.Set("User-Agent", "marquee")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
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
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, ErrUnavailable
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&envelope); err != nil || !envelope.Success {
		return nil, ErrUnavailable
	}
	return envelope.Data, nil
}
