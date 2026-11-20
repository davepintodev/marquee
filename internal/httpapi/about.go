package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/metadata"
	"github.com/davepintodev/marquee/internal/omdb"
)

// Scorer is the part of the OMDb client the API uses.
type Scorer interface {
	Scores(ctx context.Context, imdbID string) (omdb.Scores, error)
}

// aboutFresh is how long a title's details are served without asking TMDB
// or OMDb again; OMDb's free tier allows a thousand calls a day.
const aboutFresh = 24 * time.Hour

// scoresRetry is how soon a title whose OMDb scores were refused (key or
// quota) is fetched again; the rest of it is served from the cache meanwhile.
const scoresRetry = 15 * time.Minute

// profileSize is a cast photo shown at most about 120 CSS pixels wide.
const profileSize = "w185"

type ratingJSON struct {
	Source string  `json:"source"`
	Score  float64 `json:"score"`
	Scale  int     `json:"scale"`
	Votes  *int    `json:"votes,omitempty"`
}

type castJSON struct {
	PersonID    int    `json:"personId"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profilePath,omitempty"`
}

type aboutFacts struct {
	Ratings        []ratingJSON `json:"ratings"`
	Genres         []string     `json:"genres"`
	RuntimeMinutes int          `json:"runtimeMinutes,omitempty"`
	Status         string       `json:"status,omitempty"`
	Language       string       `json:"language,omitempty"`
	Cast           []castJSON   `json:"cast"`
}

// aboutDoc is what is kept between requests. Similar titles stay TMDB's
// summaries: whether one is in the library is read when it is served.
type aboutDoc struct {
	aboutFacts
	Similar []metadata.Summary `json:"similar"`
	// ScoresPending marks a copy kept without OMDb's scores because OMDb refused; it is fetched again sooner.
	ScoresPending bool `json:"scoresPending,omitempty"`
}

type aboutJSON struct {
	aboutFacts
	Similar []summaryJSON `json:"similar"`
}

func oneDecimal(x float64) float64 { return math.Round(x*10) / 10 }

func (a *api) about(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kind, tmdbID, ok := parseTitleID(id)
	if !ok {
		fail(w, http.StatusNotFound, "no such title")
		return
	}
	// Opening a title twice at once fetches it once.
	defer a.abouts.Lock(id)()

	body, at, found, err := a.Catalog.CachedAbout(r.Context(), id)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	var kept aboutDoc
	if found && json.Unmarshal(body, &kept) != nil {
		found = false
	}
	fresh := aboutFresh
	if kept.ScoresPending {
		fresh = scoresRetry
	}
	if found && a.Now().Sub(at) < fresh {
		a.writeAbout(w, r, kept)
		return
	}

	doc, photos, outcome, err := a.fetchAbout(r, kind, tmdbID)
	if err != nil {
		if found {
			a.writeAbout(w, r, kept)
			return
		}
		a.upstream(w, r, err)
		return
	}
	switch outcome {
	case scoresMissing:
		// OMDb did not answer: its scores stay as they were, and it is asked again next time.
		doc.Ratings = withKept(doc.Ratings, kept.Ratings)
		a.writeAbout(w, r, doc)
		return
	case scoresRefused:
		doc.Ratings = withKept(doc.Ratings, kept.Ratings)
		doc.ScoresPending = true
	}
	if err := a.keepAbout(r, id, doc, photos); err != nil {
		a.internal(w, r, err)
		return
	}
	a.writeAbout(w, r, doc)
}

// withKept adds the scores of other services from an earlier copy to what TMDB just said.
func withKept(fresh, earlier []ratingJSON) []ratingJSON {
	for _, r := range earlier {
		if r.Source != "tmdb" {
			fresh = append(fresh, r)
		}
	}
	return fresh
}

// scoresOutcome says how the OMDb half of a fetch went.
type scoresOutcome int

const (
	scoresDone    scoresOutcome = iota // answered, or not asked, or the title is not OMDb's: nothing more to ask
	scoresMissing                      // no usable answer: nothing is cached, OMDb is asked again next time
	scoresRefused                      // key or quota refused: cached without scores, asked again soon
)

// fetchAbout asks TMDB, then OMDb when it is set up.
func (a *api) fetchAbout(r *http.Request, kind string, tmdbID int) (doc aboutDoc, photos []catalog.PersonPhoto, outcome scoresOutcome, err error) {
	m, err := a.TMDB.About(r.Context(), kind, tmdbID)
	if err != nil {
		return aboutDoc{}, nil, scoresDone, err
	}
	doc = aboutDoc{
		aboutFacts: aboutFacts{
			Ratings: []ratingJSON{}, Genres: m.Genres, RuntimeMinutes: m.RuntimeMinutes, Status: m.Status, Language: m.Language,
			Cast: []castJSON{},
		},
		Similar: m.Similar,
	}
	if doc.Similar == nil {
		doc.Similar = []metadata.Summary{}
	}
	if m.Votes > 0 {
		doc.Ratings = append(doc.Ratings, ratingJSON{Source: "tmdb", Score: oneDecimal(m.Score), Scale: 10, Votes: &m.Votes})
	}
	for _, c := range m.Cast {
		cj := castJSON{PersonID: c.PersonID, Name: c.Name, Character: c.Character}
		if c.ProfilePath != "" {
			cj.ProfilePath = "/api/people/" + strconv.Itoa(c.PersonID) + "/image"
			photos = append(photos, catalog.PersonPhoto{ID: c.PersonID, Path: c.ProfilePath})
		}
		doc.Cast = append(doc.Cast, cj)
	}
	if a.OMDb != nil && m.IMDbID != "" {
		s, err := a.OMDb.Scores(r.Context(), m.IMDbID)
		switch {
		case errors.Is(err, omdb.ErrNotFound):
			// OMDb answered: it has no scores for this title.
		case errors.Is(err, omdb.ErrRefused):
			a.Log.Warn("omdb refused the key or the daily limit; the scores are TMDB's alone for now", "path", r.URL.Path)
			outcome = scoresRefused
		case err != nil:
			a.Log.Warn("omdb is not answering; the scores are TMDB's alone", "path", r.URL.Path)
			outcome = scoresMissing
		default:
			if s.IMDb > 0 {
				votes := s.IMDbVotes
				rate := ratingJSON{Source: "imdb", Score: s.IMDb, Scale: 10}
				if votes > 0 {
					rate.Votes = &votes
				}
				doc.Ratings = append(doc.Ratings, rate)
			}
			if s.RottenTomatoes != nil {
				doc.Ratings = append(doc.Ratings, ratingJSON{Source: "rotten_tomatoes", Score: float64(*s.RottenTomatoes), Scale: 100})
			}
		}
	}
	a.rememberImages(r, doc.Similar)
	return doc, photos, outcome, nil
}

func (a *api) keepAbout(r *http.Request, id string, doc aboutDoc, photos []catalog.PersonPhoto) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return a.Catalog.RememberAbout(r.Context(), id, body, photos)
}

func (a *api) writeAbout(w http.ResponseWriter, r *http.Request, doc aboutDoc) {
	library, err := a.libraryByID(r)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := aboutJSON{aboutFacts: doc.aboutFacts, Similar: make([]summaryJSON, 0, len(doc.Similar))}
	for _, m := range doc.Similar {
		out.Similar = append(out.Similar, fromTMDB(m, library))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) personImage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("personId"))
	if err != nil || id < 1 {
		fail(w, http.StatusNotFound, "no such image")
		return
	}
	path, found, err := a.Catalog.PersonPhotoPath(r.Context(), id)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if !found {
		fail(w, http.StatusNotFound, "no such image")
		return
	}
	a.serveImage(w, r, profileSize, path)
}
