package fake

import (
	"net/http"
	"sort"

	"github.com/davepintodev/marquee/internal/config"
)

// openProfile is the second profile the fake offers, so the picker has a
// choice; a title on it keeps the releases the default profile rejects.
const openProfile = "everything"

func fakeProfiles() map[string]config.Profile {
	def := config.DefaultProfile()
	return map[string]config.Profile{
		config.DefaultProfileName: def,
		openProfile:               {Qualities: config.QualityTiers(), Reject: []string{}, Prefer: def.Prefer},
	}
}

func (f *Fake) knownProfile(name string) bool {
	_, ok := fakeProfiles()[name]
	return ok
}

func (f *Fake) listProfiles(w http.ResponseWriter, _ *http.Request) {
	type profile struct {
		Name       string   `json:"name"`
		Qualities  []string `json:"qualities"`
		Reject     []string `json:"reject"`
		Prefer     []string `json:"prefer"`
		SizeLimits struct {
			MinMb int64 `json:"minMb"`
			MaxMb int64 `json:"maxMb"`
		} `json:"sizeLimits"`
	}
	all := fakeProfiles()
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []profile{}
	for _, n := range names {
		p := all[n]
		out = append(out, profile{Name: n, Qualities: nonNil(p.Qualities), Reject: nonNil(p.Reject), Prefer: nonNil(p.Prefer)})
	}
	write(w, http.StatusOK, map[string]any{
		"profiles": out, "defaultMovieProfile": config.DefaultProfileName, "defaultSeriesProfile": config.DefaultProfileName,
	})
}
