package config_test

import (
	"reflect"
	"testing"

	"github.com/davepintodev/marquee/internal/config"
)

func twoProfiles(t *testing.T) config.ProfileSet {
	t.Helper()
	cfg, err := loadWith(t, `"profiles":{
		"tv-safe":{"qualities":["WEBDL-1080p"]},
		"anything":{"qualities":["WEBDL-2160p","Remux-2160p"]}},
		"default_movie_profile":"anything","default_series_profile":"tv-safe"`, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.ProfileSet()
}

func TestAProfileSetNamesItsProfilesInOrderAndItsDefaultsPerKind(t *testing.T) {
	set := twoProfiles(t)
	if got := set.Names(); !reflect.DeepEqual(got, []string{"anything", "tv-safe"}) {
		t.Errorf("names = %v", got)
	}
	if set.Default("movie") != "anything" || set.Default("series") != "tv-safe" {
		t.Errorf("defaults = %q, %q", set.Default("movie"), set.Default("series"))
	}
}

func TestResolveFindsTheNamedProfileOrFallsBackToTheKindsDefault(t *testing.T) {
	set := twoProfiles(t)
	for _, c := range []struct {
		kind, stored, want string
		fellBack           bool
	}{
		{"movie", "", "anything", false},
		{"series", "", "tv-safe", false},
		{"series", "anything", "anything", false},
		{"movie", "removed-from-config", "anything", true},
		{"series", "removed-from-config", "tv-safe", true},
	} {
		p, name, fellBack := set.Resolve(c.kind, c.stored)
		if name != c.want || fellBack != c.fellBack || !reflect.DeepEqual(p, set.Profiles[c.want]) {
			t.Errorf("Resolve(%s, %q) = %q fellBack=%v", c.kind, c.stored, name, fellBack)
		}
	}
}

func TestAnEmptyProfileSetResolvesToTheBuiltInDefault(t *testing.T) {
	p, name, fellBack := config.ProfileSet{}.Resolve("movie", "")
	if name != config.DefaultProfileName || fellBack || !reflect.DeepEqual(p, config.DefaultProfile()) {
		t.Errorf("%q %v %+v", name, fellBack, p)
	}
}
