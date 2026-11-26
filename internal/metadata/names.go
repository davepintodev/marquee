package metadata

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

const maxAlternativeTitles = 15

// Names is what TMDB says about the names of a title: its original
// language (ISO 639-1, empty when TMDB has none) and the other titles it is
// known by.
type Names struct {
	OriginalLanguage  string
	AlternativeTitles []string
}

type altTitle struct {
	Title string `json:"title"`
}

// Names asks TMDB, in one request, for the original language and the
// alternative titles of a title. The original title comes first when it
// differs from title; repeats and title itself are dropped, ignoring case,
// and at most fifteen are kept.
func (c *Client) Names(ctx context.Context, kind string, id int, title string) (Names, error) {
	var r struct {
		OriginalLanguage  string `json:"original_language"`
		OriginalTitle     string `json:"original_title"`
		OriginalName      string `json:"original_name"`
		AlternativeTitles struct {
			Titles  []altTitle `json:"titles"`
			Results []altTitle `json:"results"`
		} `json:"alternative_titles"`
	}
	if err := c.getJSON(ctx, "/"+apiKind(kind)+"/"+strconv.Itoa(id), url.Values{"append_to_response": {"alternative_titles"}}, &r); err != nil {
		return Names{}, err
	}
	candidates := []string{r.OriginalTitle, r.OriginalName}
	for _, a := range append(r.AlternativeTitles.Titles, r.AlternativeTitles.Results...) {
		candidates = append(candidates, a.Title)
	}
	n := Names{OriginalLanguage: r.OriginalLanguage, AlternativeTitles: []string{}}
	seen := map[string]bool{strings.ToLower(strings.TrimSpace(title)): true}
	for _, t := range candidates {
		t = strings.TrimSpace(t)
		key := strings.ToLower(t)
		if t == "" || seen[key] {
			continue
		}
		seen[key] = true
		if n.AlternativeTitles = append(n.AlternativeTitles, t); len(n.AlternativeTitles) == maxAlternativeTitles {
			break
		}
	}
	return n, nil
}
