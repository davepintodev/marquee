package metadata

import (
	"context"
	"net/url"
	"strconv"
)

const (
	maxCast    = 20
	maxSimilar = 12
)

// Credit is one cast member in billing order.
type Credit struct {
	PersonID    int
	Name        string
	Character   string
	ProfilePath string // TMDB file path, empty when TMDB has no photo
}

// About is what the details screen shows beyond the title itself.
type About struct {
	Genres         []string
	RuntimeMinutes int // 0 when TMDB gives none; an episode's for a series
	Status         string
	Language       string
	// Score and Votes are TMDB's own rating; Votes is 0 when nobody has voted yet.
	Score   float64
	Votes   int
	IMDbID  string
	Cast    []Credit
	Similar []Summary
}

// About fetches a title's facts, cast and similar titles in one request.
func (c *Client) About(ctx context.Context, kind string, id int) (About, error) {
	var r struct {
		rawTitle
		Genres []struct {
			Name string `json:"name"`
		} `json:"genres"`
		Runtime int `json:"runtime"`
		// TMDB leaves a series' episode_run_time empty for many shows; its latest episode knows.
		LastEpisode struct {
			Runtime int `json:"runtime"`
		} `json:"last_episode_to_air"`
		Status          string `json:"status"`
		OriginalLang    string `json:"original_language"`
		SpokenLanguages []struct {
			Code string `json:"iso_639_1"`
			Name string `json:"english_name"`
		} `json:"spoken_languages"`
		VoteAverage float64 `json:"vote_average"`
		VoteCount   int     `json:"vote_count"`
		Credits     struct {
			Cast []struct {
				ID          int     `json:"id"`
				Name        string  `json:"name"`
				Character   string  `json:"character"`
				ProfilePath *string `json:"profile_path"`
			} `json:"cast"`
		} `json:"credits"`
		Similar struct {
			Results []rawTitle `json:"results"`
		} `json:"similar"`
	}
	q := url.Values{"append_to_response": {"credits,similar,external_ids"}}
	if err := c.getJSON(ctx, "/"+apiKind(kind)+"/"+strconv.Itoa(id), q, &r); err != nil {
		return About{}, err
	}
	a := About{Status: r.Status, Score: r.VoteAverage, Votes: r.VoteCount, Genres: []string{}}
	for _, g := range r.Genres {
		a.Genres = append(a.Genres, g.Name)
	}
	a.RuntimeMinutes = max(r.Runtime, r.LastEpisode.Runtime)
	for _, l := range r.SpokenLanguages {
		if l.Code == r.OriginalLang {
			a.Language = l.Name
		}
	}
	if r.ExternalIDs.IMDbID != nil {
		a.IMDbID = *r.ExternalIDs.IMDbID
	}
	for _, m := range r.Credits.Cast {
		if len(a.Cast) == maxCast {
			break
		}
		cr := Credit{PersonID: m.ID, Name: m.Name, Character: m.Character}
		if m.ProfilePath != nil {
			cr.ProfilePath = *m.ProfilePath
		}
		a.Cast = append(a.Cast, cr)
	}
	for _, s := range r.Similar.Results {
		if len(a.Similar) == maxSimilar {
			break
		}
		a.Similar = append(a.Similar, s.summary(kind))
	}
	return a, nil
}
