package aiostreams

import (
	"fmt"
	"strconv"
)

// MovieID is the Stremio id a movie is searched by: its IMDb id when TMDB
// has one, otherwise its TMDB id.
func MovieID(imdbID string, tmdbID int) string {
	if imdbID != "" {
		return imdbID
	}
	return "tmdb:" + strconv.Itoa(tmdbID)
}

// EpisodeID is the Stremio id an episode is searched by: its series' TVDB
// id when TMDB has one, otherwise the IMDb id. ok is false when TMDB lists
// neither, so the caller can say so instead of searching for nothing.
func EpisodeID(tvdbID int, imdbID string, season, episode int) (id string, ok bool) {
	switch {
	case tvdbID > 0:
		return fmt.Sprintf("tvdb:%d:%d:%d", tvdbID, season, episode), true
	case imdbID != "":
		return fmt.Sprintf("%s:%d:%d", imdbID, season, episode), true
	}
	return "", false
}
