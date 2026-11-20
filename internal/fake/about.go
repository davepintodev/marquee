package fake

import (
	"net/http"
	"os"
	"strconv"
)

type rating struct {
	Source string  `json:"source"`
	Score  float64 `json:"score"`
	Scale  int     `json:"scale"`
	Votes  *int    `json:"votes,omitempty"`
}

type castMember struct {
	PersonID    int    `json:"personId"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profilePath,omitempty"`
}

type aboutEntry struct {
	Ratings        []rating     `json:"ratings"`
	Genres         []string     `json:"genres"`
	RuntimeMinutes int          `json:"runtimeMinutes,omitempty"`
	Status         string       `json:"status,omitempty"`
	Language       string       `json:"language,omitempty"`
	Cast           []castMember `json:"cast"`
	Similar        []string     `json:"similar"`
}

type aboutOut struct {
	Ratings        []rating     `json:"ratings"`
	Genres         []string     `json:"genres"`
	RuntimeMinutes int          `json:"runtimeMinutes,omitempty"`
	Status         string       `json:"status,omitempty"`
	Language       string       `json:"language,omitempty"`
	Cast           []castMember `json:"cast"`
	Similar        []summary    `json:"similar"`
}

func (f *Fake) about(w http.ResponseWriter, r *http.Request) {
	t := f.find(r.PathValue("id"))
	a, ok := f.aboutBy[r.PathValue("id")]
	if t == nil || !ok {
		fail(w, http.StatusNotFound, "no such title")
		return
	}
	out := aboutOut{Ratings: a.Ratings, Genres: a.Genres, RuntimeMinutes: a.RuntimeMinutes, Status: a.Status, Language: a.Language, Cast: []castMember{}, Similar: []summary{}}
	for _, c := range a.Cast {
		// A photo is linked only when the art folder has one, as TMDB leaves some cast without.
		if f.opts.ArtDir != "" {
			if _, err := os.Stat(ProfileFile(f.opts.ArtDir, c.PersonID)); err == nil {
				c.ProfilePath = "/api/people/" + strconv.Itoa(c.PersonID) + "/image"
			}
		}
		out.Cast = append(out.Cast, c)
	}
	for _, id := range a.Similar {
		if s := f.find(id); s != nil {
			out.Similar = append(out.Similar, f.summary(s))
		}
	}
	write(w, http.StatusOK, out)
}

// personImage serves profile-<personId>.jpg from the art folder. Outside the fake's lock, like image.
func (f *Fake) personImage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("personId"))
	if err != nil || id < 1 || f.opts.ArtDir == "" {
		fail(w, http.StatusNotFound, "no such image")
		return
	}
	file, err := os.Open(ProfileFile(f.opts.ArtDir, id))
	if err != nil {
		fail(w, http.StatusNotFound, "no such image")
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		fail(w, http.StatusNotFound, "no such image")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", info.ModTime(), file)
}
