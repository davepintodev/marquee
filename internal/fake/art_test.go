package fake_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/fake"
)

func startFakeWithArt(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	f := fake.New(fake.Options{Tick: time.Hour, ArtDir: dir})
	srv := httptest.NewServer(f)
	t.Cleanup(func() { f.Close(); srv.Close() })
	return srv
}

func fetch(t *testing.T, srv *httptest.Server, path string) (int, string, []byte) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

func TestServesRealArtFromTheFolderAndGradientsForTheRest(t *testing.T) {
	dir := t.TempDir()
	poster := []byte{0xff, 0xd8, 0xff, 0xe0, 'p', 0xff, 0xd9}
	if err := os.WriteFile(filepath.Join(dir, "poster-movie-438631.jpg"), poster, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startFakeWithArt(t, dir)

	code, ctype, body := fetch(t, srv, "/api/images/poster/movie-438631")
	if code != 200 || ctype != "image/jpeg" || string(body) != string(poster) {
		t.Errorf("poster with a file: %d %q %q", code, ctype, body)
	}
	code, ctype, body = fetch(t, srv, "/api/images/backdrop/movie-438631")
	if code != 200 || ctype != "image/svg+xml" || !strings.Contains(string(body), "<svg") {
		t.Errorf("backdrop without a file must stay a gradient: %d %q", code, ctype)
	}
	code, ctype, _ = fetch(t, srv, "/api/images/poster/movie-693134")
	if code != 200 || ctype != "image/svg+xml" {
		t.Errorf("title without a file must stay a gradient: %d %q", code, ctype)
	}
}

func TestWithoutAnArtFolderEveryImageIsAGradient(t *testing.T) {
	srv := startFakeWithArt(t, "")
	if _, ctype, _ := fetch(t, srv, "/api/images/poster/movie-438631"); ctype != "image/svg+xml" {
		t.Errorf("content type %q", ctype)
	}
}

func TestAnUnreadableArtFileFallsBackToTheGradient(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "poster-movie-438631.jpg"), 0o700); err != nil {
		t.Fatal(err)
	}
	srv := startFakeWithArt(t, dir)
	if code, ctype, _ := fetch(t, srv, "/api/images/poster/movie-438631"); code != 200 || ctype != "image/svg+xml" {
		t.Errorf("a directory is not art: %d %q", code, ctype)
	}
}

func TestArtNeverEscapesTheFolderOrServesUnknownTitles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "poster-movie-1.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "poster-secret.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startFakeWithArt(t, dir)
	for _, p := range []string{"/api/images/poster/movie-1", "/api/images/poster/..%2Fposter-secret", "/api/images/poster/..%2F" + filepath.Base(dir) + "%2Fposter-movie-1"} {
		if code, _, _ := fetch(t, srv, p); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, code)
		}
	}
}

func TestFixtureTmdbIdsAreTheRealTMDBEntries(t *testing.T) {
	want := []struct {
		kind  string
		id    int
		title string
	}{
		{"movie", 687163, "Project Hail Mary"},
		{"movie", 13187, "A Charlie Brown Christmas"},
		{"movie", 693134, "Dune: Part Two"},
		{"movie", 329865, "Arrival"},
		{"movie", 335984, "Blade Runner 2049"},
		{"series", 1408, "House"},
		{"series", 226745, "Camp Snoopy"},
		{"series", 213375, "VisionQuest"},
		{"movie", 27205, "Inception"},
		{"movie", 157336, "Interstellar"},
		{"movie", 286217, "The Martian"},
		{"movie", 438631, "Dune"},
		{"series", 95396, "Severance"},
		{"series", 83867, "Andor"},
		{"series", 62560, "Mr. Robot"},
	}
	got := map[string]fake.Title{}
	for _, ti := range fake.Titles() {
		got[ti.ID] = ti
	}
	if len(got) != len(want) {
		t.Errorf("fixtures hold %d titles, this test pins %d: add the new ones", len(got), len(want))
	}
	for _, w := range want {
		id := w.kind + "-" + strconv.Itoa(w.id)
		ti, ok := got[id]
		if !ok || ti.Name != w.title || ti.Kind != w.kind || ti.TmdbID != w.id {
			t.Errorf("%s %q: fixtures have %+v", id, w.title, ti)
		}
	}
}

func TestGradientsRevalidateInsteadOfCaching(t *testing.T) {
	srv := startFakeWithArt(t, "")
	resp, err := srv.Client().Get(srv.URL + "/api/images/poster/movie-438631")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control %q, want no-cache", got)
	}
}

func TestArtRevalidatesAndAnswers304WhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "poster-movie-438631.jpg"), []byte{0xff, 0xd8, 0xff, 0xd9}, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startFakeWithArt(t, dir)
	url := srv.URL + "/api/images/poster/movie-438631"

	resp, err := srv.Client().Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control %q, want no-cache", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type %q, want image/jpeg", got)
	}
	lastModified := resp.Header.Get("Last-Modified")
	if lastModified == "" {
		t.Fatal("no Last-Modified to revalidate with")
	}

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("If-Modified-Since", lastModified)
	resp, err = srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("conditional request: status %d, want 304", resp.StatusCode)
	}
}

func TestAboutLinksACastPhotoOnlyWhenTheFolderHasIt(t *testing.T) {
	dir := t.TempDir()
	photo := []byte{0xff, 0xd8, 0xff, 0xe0, 'f', 0xff, 0xd9}
	if err := os.WriteFile(fake.ProfileFile(dir, 1001), photo, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startFakeWithArt(t, dir)

	var about struct {
		Cast []struct {
			PersonID    int
			ProfilePath string
		}
	}
	getJSON(t, srv, "/api/titles/movie-687163/about", &about)
	if len(about.Cast) < 2 || about.Cast[0].PersonID != 1001 || about.Cast[0].ProfilePath != "/api/people/1001/image" || about.Cast[1].ProfilePath != "" {
		t.Fatalf("only the cast member with a file in the folder has a photo: %+v", about.Cast)
	}
	code, ctype, body := fetch(t, srv, "/api/people/1001/image")
	if code != 200 || ctype != "image/jpeg" || string(body) != string(photo) {
		t.Errorf("photo: %d %q %q", code, ctype, body)
	}
	for _, path := range []string{"/api/people/1002/image", "/api/people/0/image", "/api/people/abc/image"} {
		if code, _, _ := fetch(t, srv, path); code != 404 {
			t.Errorf("%s: %d, want 404", path, code)
		}
	}
	if code, _, _ := fetch(t, startFake(t), "/api/people/1001/image"); code != 404 {
		t.Errorf("with no art folder there are no photos: %d", code)
	}
}

func TestAboutOfEveryFixtureTitleHasAScoreAndKnownSimilarTitles(t *testing.T) {
	srv := startFake(t)
	for _, ti := range fake.Titles() {
		var about struct {
			Ratings []struct{ Source string }
			Similar []struct{ ID string }
		}
		getJSON(t, srv, "/api/titles/"+ti.ID+"/about", &about)
		if len(about.Ratings) == 0 || about.Ratings[0].Source != "tmdb" {
			t.Errorf("%s: TMDB's score comes first: %+v", ti.ID, about.Ratings)
		}
		if len(about.Similar) == 0 {
			t.Errorf("%s: no similar titles", ti.ID)
		}
	}
}
