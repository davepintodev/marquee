package httpapi

import (
	"fmt"
	"slices"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/metadata"
)

type badgeJSON struct {
	State        string `json:"state"`
	Label        string `json:"label"`
	Percent      *int   `json:"percent,omitempty"`
	Season       *int   `json:"season,omitempty"`
	MissingCount *int   `json:"missingCount,omitempty"`
}

func badgeOf(b *catalog.Badge) *badgeJSON {
	if b == nil {
		return nil
	}
	return &badgeJSON{State: b.State, Label: b.Label, Percent: b.Percent, Season: b.Season, MissingCount: b.MissingCount}
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

	SearchingEpisodes []string `json:"searchingEpisodes,omitempty"`
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
	for _, season := range t.Seasons {
		for _, e := range season.Episodes {
			if e.Monitored && e.State == "searching" {
				s.SearchingEpisodes = append(s.SearchingEpisodes, catalog.EpisodeID(season.Number, e.Number))
			}
		}
	}
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
	Percent   *int   `json:"percent,omitempty"`
}

type seasonJSON struct {
	Number   int           `json:"number"`
	Badge    *badgeJSON    `json:"badge,omitempty"`
	Episodes []episodeJSON `json:"episodes"`
}

type titleJSON struct {
	summaryJSON
	Seasons          []seasonJSON  `json:"seasons"`
	PreferredQuality *string       `json:"preferredQuality"`
	HistorySummary   string        `json:"historySummary"`
	History          []historyJSON `json:"history"`
}

func titleOf(t catalog.Title, history []historyJSON) titleJSON {
	out := titleJSON{summaryJSON: fromLibrary(t), Seasons: []seasonJSON{}, History: history, HistorySummary: summarize(t, history)}
	if t.PreferredQuality != "" {
		out.PreferredQuality = &t.PreferredQuality
	}
	for _, s := range t.Seasons {
		sj := seasonJSON{Number: s.Number, Badge: badgeOf(s.Badge), Episodes: []episodeJSON{}}
		for _, e := range s.Episodes {
			sj.Episodes = append(sj.Episodes, episodeJSON{
				ID: catalog.EpisodeID(s.Number, e.Number), Number: e.Number, Name: e.Name,
				AirDate: e.AirDate, Monitored: e.Monitored, State: e.State, Percent: e.Percent,
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

var reasonWords = map[string]string{
	"stalled": "stalled", "not_video": "fake", "wrong_episode": "wrong episode",
	"too_small": "too small", "size_mismatch": "wrong size", "source_down": "source down", "local_error": "local error", "duplicate_episode": "duplicate episode",
}

// summarize says in one sentence what was tried, such as "Tried 3
// releases, 2 fake, 1 stalled." The reasons come most common first.
func summarize(t catalog.Title, history []historyJSON) string {
	if !t.Monitored {
		return "Not monitored."
	}
	tried := 0
	reasons := map[string]int{}
	var order []string
	for _, h := range history {
		switch h.Kind {
		case "failed":
			tried++
			if reasons[h.Reason] == 0 {
				order = append(order, h.Reason)
			}
			reasons[h.Reason]++
		case "imported", "would_grab":
			tried++
		}
	}
	if tried == 0 {
		return "Nothing tried yet."
	}
	out := fmt.Sprintf("Tried %d release", tried)
	if tried != 1 {
		out += "s"
	}
	slices.SortStableFunc(order, func(a, b string) int { return reasons[b] - reasons[a] })
	for _, r := range order {
		words := reasonWords[r]
		if words == "" {
			words = r
		}
		out += fmt.Sprintf(", %d %s", reasons[r], words)
	}
	return out + "."
}
