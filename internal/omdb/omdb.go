// Package omdb reads IMDb and Rotten Tomatoes scores from OMDb, which
// needs a key of its own. Without a key marquee does not call it.
package omdb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/davepintodev/marquee/internal/config"
)

// ErrUnavailable covers every way OMDb fails to give an answer. It carries
// no URL: the key travels in the query string, and net/http's errors quote it.
var ErrUnavailable = errors.New("omdb: no answer")

const (
	defaultBase    = "https://www.omdbapi.com/"
	defaultTimeout = 8 * time.Second
	maxBytes       = 1 << 20
)

type Options struct {
	// Base replaces OMDb's address; empty is OMDb.
	Base    string
	Key     config.Secret
	Timeout time.Duration
}

type Client struct {
	base    string
	key     config.Secret
	timeout time.Duration
	http    *http.Client
}

func New(o Options) *Client {
	c := &Client{base: o.Base, key: o.Key, timeout: o.Timeout}
	if c.base == "" {
		c.base = defaultBase
	}
	if c.timeout == 0 {
		c.timeout = defaultTimeout
	}
	c.http = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: c.timeout, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}}
	return c
}

// Scores are what OMDb knows of one title; a field is zero or nil when OMDb lacks that score.
type Scores struct {
	IMDb           float64
	IMDbVotes      int
	RottenTomatoes *int // percent; 0% is a score, so absence is nil
}

// Scores looks a title up by its IMDb id.
func (c *Client) Scores(ctx context.Context, imdbID string) (Scores, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	u, err := url.Parse(c.base)
	if err != nil {
		return Scores{}, ErrUnavailable
	}
	u.RawQuery = url.Values{"i": {imdbID}, "apikey": {c.key.Reveal()}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Scores{}, ErrUnavailable
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Scores{}, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	var v struct {
		Response   string `json:"Response"`
		IMDbRating string `json:"imdbRating"`
		IMDbVotes  string `json:"imdbVotes"`
		Ratings    []struct {
			Source string `json:"Source"`
			Value  string `json:"Value"`
		} `json:"Ratings"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, maxBytes)).Decode(&v) != nil || v.Response != "True" {
		return Scores{}, ErrUnavailable
	}
	var s Scores
	if f, err := strconv.ParseFloat(v.IMDbRating, 64); err == nil && f > 0 {
		s.IMDb = f
		s.IMDbVotes, _ = strconv.Atoi(strings.ReplaceAll(v.IMDbVotes, ",", ""))
	}
	for _, r := range v.Ratings {
		if r.Source != "Rotten Tomatoes" {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSuffix(r.Value, "%")); err == nil && n >= 0 {
			s.RottenTomatoes = &n
		}
	}
	return s, nil
}
