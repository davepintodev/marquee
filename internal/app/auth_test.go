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

// status reads only the status of a request: an event stream never ends.
func status(t *testing.T, method, url string, header map[string]string, cookie *http.Cookie) (int, http.Header) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode, resp.Header
}

func sessionCookie(t *testing.T, h http.Header) *http.Cookie {
	t.Helper()
	resp := http.Response{Header: h}
	for _, c := range resp.Cookies() {
		if c.Name == "marquee_session" {
			return c
		}
	}
	return nil
}

func TestImagesAndTheEventStreamNeedTheTokenOrItsSession(t *testing.T) {
	base := start(t, testEnv(t), io.Discard)
	for _, path := range []string{"/api/images/poster/movie-438631", "/api/events"} {
		if code, _ := status(t, http.MethodGet, base+path, nil, nil); code != 401 {
			t.Errorf("%s without a credential: %d, want 401", path, code)
		}
		if code, _ := status(t, http.MethodGet, base+path, map[string]string{"Authorization": "Bearer " + testToken}, nil); code == 401 {
			t.Errorf("%s with the bearer token: 401", path)
		}
	}
}

func TestABearerRequestHandsOutASessionThatOnlyReadsImagesAndEvents(t *testing.T) {
	base := start(t, testEnv(t), io.Discard)
	bearer := map[string]string{"Authorization": "Bearer " + testToken}

	code, h := status(t, http.MethodGet, base+"/api/downloads", bearer, nil)
	if code != 200 {
		t.Fatalf("downloads with the token: %d", code)
	}
	c := sessionCookie(t, h)
	if c == nil {
		t.Fatalf("no session cookie after a bearer request: %v", h)
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/api/" || c.MaxAge <= 0 || c.Secure {
		t.Errorf("cookie attributes: %+v", c)
	}
	if strings.Contains(c.Value, "SECRET") || strings.Contains(c.Value, testToken) || c.Value == "" {
		t.Errorf("the cookie must not carry the token: %q", c.Value)
	}

	for _, path := range []string{"/api/images/poster/movie-438631", "/api/events"} {
		if code, _ := status(t, http.MethodGet, base+path, nil, c); code == 401 {
			t.Errorf("%s with the session: 401", path)
		}
	}
	// It opens nothing else: another app on the same host shares the cookie.
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/downloads"},
		{http.MethodGet, "/api/wall"},
		{http.MethodPost, "/api/titles"},
		{http.MethodDelete, "/api/titles/movie-1"},
		{http.MethodPost, "/api/events"},
	} {
		if code, _ := status(t, tc.method, base+tc.path, nil, c); code != 401 {
			t.Errorf("%s %s with the session: %d, want 401", tc.method, tc.path, code)
		}
	}
	if _, h := status(t, http.MethodGet, base+"/api/downloads", bearer, c); sessionCookie(t, h) != nil {
		t.Error("a request that already holds the session got another cookie")
	}
	stale := &http.Cookie{Name: "marquee_session", Value: "stale"}
	if code, _ := status(t, http.MethodGet, base+"/api/events", nil, stale); code != 401 {
		t.Errorf("a stale session: %d, want 401", code)
	}
	if _, h := status(t, http.MethodGet, base+"/api/downloads", bearer, stale); sessionCookie(t, h) == nil {
		t.Error("a stale session was not replaced")
	}
	if _, h := status(t, http.MethodGet, base+"/api/downloads", map[string]string{"Authorization": "Bearer wrong"}, nil); sessionCookie(t, h) != nil {
		t.Error("a refused request got a cookie")
	}
}

func TestWithoutAnAPITokenImagesAndEventsAnswerWithoutACookie(t *testing.T) {
	env := testEnv(t)
	delete(env, "MARQUEE_API_TOKEN")
	base := start(t, env, io.Discard)
	for _, path := range []string{"/api/images/poster/movie-438631", "/api/events", "/api/downloads"} {
		code, h := status(t, http.MethodGet, base+path, nil, nil)
		if code == 401 {
			t.Errorf("%s: 401 with no token configured", path)
		}
		if sessionCookie(t, h) != nil {
			t.Errorf("%s: a cookie with no token configured", path)
		}
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
