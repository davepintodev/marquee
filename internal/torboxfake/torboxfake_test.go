package torboxfake_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/torbox"
	"github.com/davepintodev/marquee/internal/torboxfake"
)

func get(t *testing.T, url, auth string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func TestRequiresTheBearerKey(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	for _, auth := range []string{"", "Bearer wrong", torboxfake.Key} {
		if got, _ := get(t, fake.APIBase()+"/user/me", auth); got != http.StatusUnauthorized {
			t.Errorf("auth %q: status %d, want 401", auth, got)
		}
	}
	if got, _ := get(t, fake.APIBase()+"/user/me", "Bearer "+torboxfake.Key); got != http.StatusOK {
		t.Errorf("right key: status %d", got)
	}
}

func TestListsEveryTorrentWithoutAnID(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	_, raw := get(t, fake.APIBase()+"/torrents/mylist", "Bearer "+torboxfake.Key)
	var body struct {
		Success bool             `json:"success"`
		Data    []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || body.Data == nil || len(body.Data) != 0 {
		t.Fatalf("an empty account lists []: %+v", body)
	}
}

func TestCreatingAHashTheAccountHoldsAnswersWithThatTorrent(t *testing.T) {
	fake := torboxfake.New()
	defer fake.Close()
	c, err := torbox.New(torbox.Options{APIKey: config.Secret(torboxfake.Key), Base: fake.APIBase()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const hash = "0123456789abcdef0123456789abcdef01234567"
	a, err := c.CreateTorrent(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.CreateTorrent(ctx, hash)
	if err != nil || b.ID != a.ID {
		t.Fatalf("second create = %+v, %v; want torrent %d again", b, err, a.ID)
	}
	if err := c.DeleteTorrent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	again, err := c.CreateTorrent(ctx, hash)
	if err != nil || again.ID == a.ID {
		t.Fatalf("a deleted torrent must be created anew: %+v, %v", again, err)
	}
}
