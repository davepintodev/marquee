package catalog

import (
	"github.com/davepintodev/marquee/internal/metadata"
)

// NewTitleFromTMDB is the title to store for what TMDB knows of it. An
// episode TMDB lists without a date cannot be waited for yet, so it is
// stored once a refresh brings its date.
func NewTitleFromTMDB(d metadata.Details) NewTitle {
	n := NewTitle{
		Kind: d.Kind, TMDBID: d.TMDBID, Title: d.Title, Year: d.Year, Overview: d.Overview,
		IMDbID: d.IMDbID, TVDBID: d.TVDBID,
		Images: []ImageRef{{Kind: "poster", Path: d.PosterPath}, {Kind: "backdrop", Path: d.BackdropPath}},
	}
	for _, e := range d.DatedEpisodes() {
		n.Episodes = append(n.Episodes, NewEpisode(e))
	}
	return n
}
