package app_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/davepintodev/marquee/api"
	"github.com/davepintodev/marquee/internal/app"
)

func authGet(t *testing.T, url string, header map[string]string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

func TestWithAnAPITokenTheAPIAnswers401WithoutIt(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatal(err)
	}
	logs := newSyncBuf()
	base := start(t, testEnv(t), logs)

	for name, header := range map[string]map[string]string{
		"no header":          nil,
		"wrong token":        {"Authorization": "Bearer token-SECRETabd"},
		"a prefix":           {"Authorization": "Bearer token-SECRET"},
		"a longer token":     {"Authorization": "Bearer token-SECRETabcd"},
		"no scheme":          {"Authorization": testToken},
		"another scheme":     {"Authorization": "Basic " + testToken},
		"an empty bearer":    {"Authorization": "Bearer "},
		"the token in a url": nil,
	} {
		url := base + "/api/downloads"
		if name == "the token in a url" {
			url += "?token=" + testToken
		}
		code, body, h := authGet(t, url, header)
		if code != 401 {
			t.Errorf("%s: %d %s, want 401", name, code, body)
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(body), &v); err != nil || v["error"] == nil || h.Get("Content-Type") != "application/json" {
			t.Errorf("%s: 401 is not the contract's error shape: %q %v", name, body, h)
		} else if err := doc.Components.Schemas["Error"].Value.VisitJSON(v); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if strings.Contains(body, "SECRET") {
			t.Errorf("%s: the 401 echoes a secret: %s", name, body)
		}
	}

	for _, ok := range []string{"Bearer " + testToken, "bearer " + testToken} {
		if code, body, _ := authGet(t, base+"/api/downloads", map[string]string{"Authorization": ok}); code != 200 {
			t.Errorf("%q: %d %s, want 200", ok, code, body)
		}
	}

	// The UI has to load before it can ask for the token.
	if code, body, _ := authGet(t, base+"/", nil); code != 200 || !strings.Contains(body, "marquee ui") {
		t.Errorf("the UI must stay open: %d %q", code, body)
	}
	if strings.Contains(logs.String(), "SECRETabc") {
		t.Errorf("logs leak the token:\n%s", logs.String())
	}
}

func TestImagesAndTheEventStreamStayOpenUntilTheyCanCarryTheToken(t *testing.T) {
	base := start(t, testEnv(t), io.Discard)
	if code, body, _ := authGet(t, base+"/api/images/poster/movie-438631", nil); code == 401 {
		t.Errorf("an image answered 401 without a token: %s", body)
	}
	// The event stream never ends, so only its status is read.
	resp, err := http.Get(base + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == 401 {
		t.Error("the event stream answered 401 without a token")
	}
}

func TestWithoutAnAPITokenTheAPIAnswersNormally(t *testing.T) {
	env := testEnv(t)
	delete(env, "MARQUEE_API_TOKEN")
	base := start(t, env, io.Discard)
	if code, body, _ := authGet(t, base+"/api/downloads", nil); code != 200 {
		t.Errorf("no token configured: %d %s, want 200", code, body)
	}
	// A client that sends a header anyway is not refused.
	if code, body, _ := authGet(t, base+"/api/downloads", map[string]string{"Authorization": "Bearer anything"}); code != 200 {
		t.Errorf("no token configured, header sent: %d %s, want 200", code, body)
	}
}

func TestTheFakeHonoursTheTokenSoTheUnlockCanBeBuiltAgainstIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrc := make(chan net.Addr, 1)
	done := make(chan error, 1)
	env := map[string]string{"MARQUEE_FAKE": "1", "MARQUEE_LISTEN": "127.0.0.1:0", "MARQUEE_API_TOKEN": "fake-token"}
	go func() {
		done <- app.Run(ctx, func(k string) string { return env[k] }, io.Discard, fstest.MapFS{}, func(a net.Addr) { addrc <- a })
	}()
	base := "http://" + (<-addrc).String()
	if code, _, _ := authGet(t, base+"/api/wall", nil); code != 401 {
		t.Errorf("fake without token: %d, want 401", code)
	}
	if code, _, _ := authGet(t, base+"/api/wall", map[string]string{"Authorization": "Bearer fake-token"}); code != 200 {
		t.Errorf("fake with token: %d, want 200", code)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("fake did not shut down")
	}
}
