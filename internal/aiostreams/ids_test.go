package aiostreams_test

import (
	"testing"

	"github.com/davepintodev/marquee/internal/aiostreams"
)

func TestMovieID(t *testing.T) {
	for _, c := range []struct {
		imdb string
		tmdb int
		want string
	}{{"tt1160419", 438631, "tt1160419"}, {"", 99, "tmdb:99"}} {
		if got := aiostreams.MovieID(c.imdb, c.tmdb); got != c.want {
			t.Errorf("MovieID(%q, %d) = %q, want %q", c.imdb, c.tmdb, got, c.want)
		}
	}
}

func TestEpisodeID(t *testing.T) {
	for _, c := range []struct {
		name   string
		tvdb   int
		imdb   string
		want   string
		wantOK bool
	}{
		{"tvdb wins", 403000, "tt10466872", "tvdb:403000:1:2", true},
		{"imdb fallback", 0, "tt10466872", "tt10466872:1:2", true},
		{"neither", 0, "", "", false},
	} {
		got, ok := aiostreams.EpisodeID(c.tvdb, c.imdb, 1, 2)
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s: got %q, %v", c.name, got, ok)
		}
	}
}
