package app_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/davepintodev/marquee/api"
	"github.com/davepintodev/marquee/internal/app"
)

// manifestVal names a port nothing listens on, so a service that follows it
// is refused at once and never reaches real DNS.
const manifestVal = "http://127.0.0.1:1/stremio/SECRET123/PASS789/manifest.json"

// unreachableTMDB is where start() points TMDB: nothing listens there.
const unreachableTMDB = "http://127.0.0.1:1/3"

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
		"MARQUEE_API_TOKEN":               testToken,
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
		done <- app.RunWith(ctx, app.Options{TMDBAPIBase: unreachableTMDB, TMDBImageBase: unreachableTMDB}, func(k string) string { return env[k] }, logs, ui, func(a net.Addr) { addrc <- a })
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

// testToken is the API token testEnv configures; helpers send it unless a test removes it.
const testToken = "token-SECRETabc"

func get(t *testing.T, url string) (int, string, string) {
	t.Helper()
	return getWith(t, url, testToken)
}

func getWith(t *testing.T, url, token string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
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

func TestFakeModeServesTheContractWithoutEssentialSettings(t *testing.T) {
	logs := newSyncBuf()
	base := start(t, map[string]string{"MARQUEE_FAKE": "1", "MARQUEE_LISTEN": "127.0.0.1:0"}, logs)

	code, body, ctype := get(t, base+"/api/wall")
	if code != 200 || ctype != "application/json" || !strings.Contains(body, "Downloading 43%") {
		t.Fatalf("wall: %d %q %s", code, ctype, body)
	}
	if code, _, _ := get(t, base+"/"); code != 200 {
		t.Errorf("UI still served in fake mode: %d", code)
	}
	if !strings.Contains(logs.String(), "fake mode") || !strings.Contains(logs.String(), "not required") {
		t.Errorf("startup log must say fake mode needs no essential settings: %s", logs.String())
	}
}

func TestFakeModeServesArtFromMarqueeFakeArtDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "poster-movie-438631.jpg"), []byte("jpeg-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := start(t, map[string]string{"MARQUEE_FAKE": "1", "MARQUEE_LISTEN": "127.0.0.1:0", "MARQUEE_FAKE_ART_DIR": dir}, newSyncBuf())

	code, body, ctype := get(t, base+"/api/images/poster/movie-438631")
	if code != 200 || ctype != "image/jpeg" || body != "jpeg-bytes" {
		t.Fatalf("poster: %d %q %q", code, ctype, body)
	}
}

func TestInvalidFakeSettingIsRejected(t *testing.T) {
	err := app.Run(context.Background(), func(k string) string {
		if k == "MARQUEE_FAKE" {
			return "maybe"
		}
		return ""
	}, io.Discard, fstest.MapFS{}, nil)
	if err == nil || !strings.Contains(err.Error(), "MARQUEE_FAKE") {
		t.Fatalf("want an error naming MARQUEE_FAKE, got %v", err)
	}
}

func TestShutdownEndsOpenEventStreamsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrc := make(chan net.Addr, 1)
	done := make(chan error, 1)
	env := map[string]string{"MARQUEE_FAKE": "1", "MARQUEE_LISTEN": "127.0.0.1:0"}
	go func() {
		done <- app.Run(ctx, func(k string) string { return env[k] }, io.Discard, fstest.MapFS{}, func(a net.Addr) { addrc <- a })
	}()
	resp, err := http.Get("http://" + (<-addrc).String() + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if line, _ := bufio.NewReader(resp.Body).ReadString('\n'); !strings.HasPrefix(line, ":") {
		t.Fatalf("stream did not open: %q", line)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v with a stream open", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown hung on the open event stream")
	}
}

func TestRealHealthMatchesTheContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatal(err)
	}
	env := testEnv(t)
	env["MARQUEE_DRY_RUN"] = "true"
	base := start(t, env, io.Discard)
	code, body, _ := get(t, base+"/api/health")
	if code != 200 {
		t.Fatalf("health: %d %s", code, body)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	if err := doc.Components.Schemas["Health"].Value.VisitJSON(v); err != nil {
		t.Fatalf("health does not match the contract: %v\n%s", err, body)
	}
	if v["dryRun"] != true {
		t.Errorf("dryRun must follow the configuration: %v", v["dryRun"])
	}
}
