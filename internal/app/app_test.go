package app_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/davepintodev/marquee/internal/app"
)

const manifestVal = "https://aio.example.test/stremio/SECRET123/manifest.json"

func testEnv(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"movies", "series"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return map[string]string{
		"MARQUEE_AIOSTREAMS_MANIFEST_URL": manifestVal,
		"MARQUEE_TMDB_KEY":                "tmdb-SECRET456",
		"MARQUEE_MOVIES_PATH":             filepath.Join(root, "movies"),
		"MARQUEE_SERIES_PATH":             filepath.Join(root, "series"),
		"MARQUEE_TEMP_PATH":               filepath.Join(root, "tmp"),
		"MARQUEE_DATA_DIR":                filepath.Join(root, "data"),
		"MARQUEE_LISTEN":                  "127.0.0.1:0",
		"MARQUEE_API_TOKEN":               "token-SECRETabc",
	}
}

type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSyncBuf() *syncBuf { return &syncBuf{} }

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func start(t *testing.T, env map[string]string, logs io.Writer) string {
	t.Helper()
	ui := fstest.MapFS{
		"index.html":    {Data: []byte("<html>marquee ui</html>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
	ctx, cancel := context.WithCancel(context.Background())
	addrc := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, func(k string) string { return env[k] }, logs, ui, func(a net.Addr) { addrc <- a })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v after shutdown", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("service did not shut down")
		}
	})
	select {
	case a := <-addrc:
		return "http://" + a.String()
	case err := <-done:
		t.Fatalf("service exited early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("service did not start")
	}
	return ""
}

func get(t *testing.T, url string) (int, string, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header.Get("Content-Type")
}

func TestServesEmbeddedUIAndAPIOnTheConfiguredAddress(t *testing.T) {
	logs := newSyncBuf()
	base := start(t, testEnv(t), logs)

	if code, body, _ := get(t, base+"/"); code != 200 || !strings.Contains(body, "marquee ui") {
		t.Errorf("/ = %d %q", code, body)
	}
	if code, body, _ := get(t, base+"/assets/app.js"); code != 200 || !strings.Contains(body, "console.log") {
		t.Errorf("asset = %d %q", code, body)
	}
	if code, body, _ := get(t, base+"/titles/42"); code != 200 || !strings.Contains(body, "marquee ui") {
		t.Errorf("client route must fall back to index.html, got %d %q", code, body)
	}
	code, body, ctype := get(t, base+"/api/health")
	if code != 200 || !strings.Contains(body, `"ok"`) || !strings.HasPrefix(ctype, "application/json") {
		t.Errorf("/api/health = %d %q %q", code, body, ctype)
	}
	if code, body, _ := get(t, base+"/api/nope"); code != 404 || strings.Contains(body, "marquee ui") {
		t.Errorf("unknown API path must 404 as JSON, got %d %q", code, body)
	}

	for _, secret := range []string{"SECRET123", "SECRET456", "SECRETabc"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("logs leak %s:\n%s", secret, logs.String())
		}
	}
}

func TestMissingUIBuildGives404NotAPanic(t *testing.T) {
	env := testEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrc := make(chan net.Addr, 1)
	go func() {
		_ = app.Run(ctx, func(k string) string { return env[k] }, io.Discard, fstest.MapFS{}, func(a net.Addr) { addrc <- a })
	}()
	a := <-addrc
	if code, _, _ := get(t, "http://"+a.String()+"/"); code != 404 {
		t.Errorf("got %d", code)
	}
}

func TestStartupFailuresDoNotLeakSecrets(t *testing.T) {
	env := testEnv(t)
	delete(env, "MARQUEE_TEMP_PATH")
	err := app.Run(context.Background(), func(k string) string { return env[k] }, io.Discard, fstest.MapFS{}, nil)
	if err == nil || !strings.Contains(err.Error(), "MARQUEE_TEMP_PATH") {
		t.Fatalf("want an error naming MARQUEE_TEMP_PATH, got %v", err)
	}
	for _, secret := range []string{"SECRET123", "SECRET456", "SECRETabc"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks %s: %v", secret, err)
		}
	}

	env = testEnv(t)
	env["MARQUEE_LISTEN"] = "not-an-address-SECRET123"
	err = app.Run(context.Background(), func(k string) string { return env[k] }, io.Discard, fstest.MapFS{}, nil)
	if err == nil || !strings.Contains(err.Error(), "MARQUEE_LISTEN") {
		t.Fatalf("want an error naming MARQUEE_LISTEN, got %v", err)
	}
}
