package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/metadata"
)

type detailsOnly struct{ TMDB }

func (detailsOnly) Details(_ context.Context, kind string, id int) (metadata.Details, error) {
	return metadata.Details{Summary: metadata.Summary{Kind: kind, TMDBID: id, Title: "Dune", Year: 2021}}, nil
}

func zeroProfilesServer(t *testing.T) *httptest.Server {
	t.Helper()
	store, err := catalog.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := httptest.NewServer(New(Deps{Catalog: store, TMDB: detailsOnly{}}))
	t.Cleanup(srv.Close)
	return srv
}

func send(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestZeroProfilesBehaveAsTheBuiltInDefault(t *testing.T) {
	srv := zeroProfilesServer(t)

	status, v := send(t, srv, "GET", "/api/profiles", "")
	profiles, _ := v["profiles"].([]any)
	if status != 200 || len(profiles) != 1 || profiles[0].(map[string]any)["name"] != "default" {
		t.Fatalf("GET /api/profiles: %d %v", status, v)
	}
	if v["defaultMovieProfile"] != "default" || v["defaultSeriesProfile"] != "default" {
		t.Errorf("defaults: %v", v)
	}

	status, v = send(t, srv, "POST", "/api/titles", `{"kind":"movie","tmdbId":438631,"profile":"default"}`)
	if status != 201 || v["profile"] != "default" {
		t.Fatalf("POST with profile default: %d %v", status, v)
	}
	status, v = send(t, srv, "PATCH", "/api/titles/movie-438631", `{"profile":"default"}`)
	if status != 200 || v["profile"] != "default" {
		t.Fatalf("PATCH with profile default: %d %v", status, v)
	}
	if status, _ = send(t, srv, "PATCH", "/api/titles/movie-438631", `{"profile":"nonsense"}`); status != 400 {
		t.Errorf("an unknown name stays a 400: %d", status)
	}
}
