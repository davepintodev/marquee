package httpapi

import (
	"net/http"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/config"
)

type profileJSON struct {
	Name       string   `json:"name"`
	Qualities  []string `json:"qualities"`
	Reject     []string `json:"reject"`
	Prefer     []string `json:"prefer"`
	SizeLimits struct {
		MinMB int64 `json:"minMb"`
		MaxMB int64 `json:"maxMb"`
	} `json:"sizeLimits"`
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func (a *api) listProfiles(w http.ResponseWriter, _ *http.Request) {
	out := struct {
		Profiles             []profileJSON `json:"profiles"`
		DefaultMovieProfile  string        `json:"defaultMovieProfile"`
		DefaultSeriesProfile string        `json:"defaultSeriesProfile"`
	}{
		Profiles:             []profileJSON{},
		DefaultMovieProfile:  a.Profiles.Default("movie"),
		DefaultSeriesProfile: a.Profiles.Default("series"),
	}
	for _, name := range a.Profiles.Names() {
		p := a.Profiles.Profiles[name]
		j := profileJSON{Name: name, Qualities: nonNil(p.Qualities), Reject: nonNil(p.Reject), Prefer: nonNil(p.Prefer)}
		j.SizeLimits.MinMB, j.SizeLimits.MaxMB = p.SizeLimits.MinMB, p.SizeLimits.MaxMB
		out.Profiles = append(out.Profiles, j)
	}
	writeJSON(w, http.StatusOK, out)
}

// profileFor is the profile a title is judged by and the name the owner
// chose for it, empty when it uses the default of its kind. A stored name
// the config no longer defines is judged by that default and counts as
// not chosen, with a warning.
func (a *api) profileFor(t catalog.Title) (config.Profile, string) {
	p, name, fellBack := a.Profiles.Resolve(t.Kind, t.Profile)
	if fellBack {
		a.Log.Warn("a title names a profile the config does not define; using the default", "title", t.ID, "profile", t.Profile, "default", name)
		return p, ""
	}
	return p, t.Profile
}

func (a *api) knownProfile(name string) bool {
	_, ok := a.Profiles.Profiles[name]
	return ok
}
