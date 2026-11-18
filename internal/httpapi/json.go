package httpapi

import (
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/metadata"
)

type badgeJSON struct {
	State        string `json:"state"`
	Label        string `json:"label"`
	Season       *int   `json:"season,omitempty"`
	MissingCount *int   `json:"missingCount,omitempty"`
}

func badgeOf(b *catalog.Badge) *badgeJSON {
	if b == nil {
		return nil
	}
	return &badgeJSON{State: b.State, Label: b.Label, Season: b.Season, MissingCount: b.MissingCount}
}

type summaryJSON struct {
	ID           string     `json:"id"`
	TMDBID       int        `json:"tmdbId"`
	Kind         string     `json:"kind"`
	Title        string     `json:"title"`
	Year         int        `json:"year"`
	Overview     string     `json:"overview"`
	PosterPath   string     `json:"posterPath"`
	BackdropPath string     `json:"backdropPath"`
	InLibrary    bool       `json:"inLibrary"`
	Monitored    *bool      `json:"monitored,omitempty"`
	Badge        *badgeJSON `json:"badge,omitempty"`
}

func imagePath(kind, id string) string { return "/api/images/" + kind + "/" + id }

func summaryOf(id string, kind string, tmdbID int, title string, year int, overview string) summaryJSON {
	return summaryJSON{
		ID: id, TMDBID: tmdbID, Kind: kind, Title: title, Year: year, Overview: overview,
		PosterPath: imagePath("poster", id), BackdropPath: imagePath("backdrop", id),
	}
}

func fromLibrary(t catalog.Title) summaryJSON {
	s := summaryOf(t.ID, t.Kind, t.TMDBID, t.Title, t.Year, t.Overview)
	s.InLibrary, s.Monitored, s.Badge = true, &t.Monitored, badgeOf(t.Badge)
	return s
}

// fromTMDB describes a title seen in search or trending, with its library
// state when marquee already has it.
func fromTMDB(m metadata.Summary, library map[string]catalog.Title) summaryJSON {
	id := catalog.TitleID(m.Kind, m.TMDBID)
	if t, ok := library[id]; ok {
		return fromLibrary(t)
	}
	return summaryOf(id, m.Kind, m.TMDBID, m.Title, m.Year, m.Overview)
}

type episodeJSON struct {
	ID        string `json:"id"`
	Number    int    `json:"number"`
	Name      string `json:"name"`
	AirDate   string `json:"airDate"`
	Monitored bool   `json:"monitored"`
	State     string `json:"state"`
}

type seasonJSON struct {
	Number   int           `json:"number"`
	Badge    *badgeJSON    `json:"badge,omitempty"`
	Episodes []episodeJSON `json:"episodes"`
}

type titleJSON struct {
	summaryJSON
	Seasons          []seasonJSON `json:"seasons"`
	PreferredQuality *string      `json:"preferredQuality"`
	HistorySummary   string       `json:"historySummary"`
}

func titleOf(t catalog.Title) titleJSON {
	out := titleJSON{summaryJSON: fromLibrary(t), Seasons: []seasonJSON{}}
	if t.PreferredQuality != "" {
		out.PreferredQuality = &t.PreferredQuality
	}
	switch {
	case !t.Monitored:
		out.HistorySummary = "Not monitored."
	case t.Badge != nil && t.Badge.State == "downloaded":
		out.HistorySummary = "Added and monitored. Everything wanted is downloaded."
	default:
		out.HistorySummary = "Added and monitored."
	}
	for _, s := range t.Seasons {
		sj := seasonJSON{Number: s.Number, Badge: badgeOf(s.Badge), Episodes: []episodeJSON{}}
		for _, e := range s.Episodes {
			sj.Episodes = append(sj.Episodes, episodeJSON{
				ID: catalog.EpisodeID(s.Number, e.Number), Number: e.Number, Name: e.Name,
				AirDate: e.AirDate, Monitored: e.Monitored, State: e.State,
			})
		}
		out.Seasons = append(out.Seasons, sj)
	}
	return out
}

func imageRefs(id string, m metadata.Summary) []catalog.ImageRef {
	return []catalog.ImageRef{
		{TitleID: id, Kind: "poster", Path: m.PosterPath},
		{TitleID: id, Kind: "backdrop", Path: m.BackdropPath},
	}
}
