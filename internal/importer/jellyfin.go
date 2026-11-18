package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/davepintodev/marquee/internal/config"
)

// Jellyfin refreshes a Jellyfin library.
type Jellyfin struct {
	base   string
	key    config.Secret
	client *http.Client
}

// NewJellyfin returns a client for the server at url; timeout bounds one call.
func NewJellyfin(url string, key config.Secret, timeout time.Duration) *Jellyfin {
	return &Jellyfin{base: strings.TrimRight(url, "/"), key: key, client: &http.Client{Timeout: timeout}}
}

// Refresh starts a library scan. Its errors never hold the URL or the key.
func (j *Jellyfin) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.base+"/Library/Refresh", nil)
	if err != nil {
		return errors.New("the Jellyfin URL is not valid")
	}
	req.Header.Set("Authorization", `MediaBrowser Token="`+j.key.Reveal()+`"`)
	resp, err := j.client.Do(req)
	if err != nil {
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return errors.New("Jellyfin did not answer in time")
		}
		return errors.New("Jellyfin is not reachable")
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("Jellyfin refused the API key (HTTP %d); check the Jellyfin key setting", resp.StatusCode)
	}
	return fmt.Errorf("Jellyfin answered HTTP %d", resp.StatusCode)
}
