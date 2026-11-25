package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Profile is a named quality and format rule set. A release whose quality
// is not in Qualities, or that carries a Reject tag, is never downloaded
// automatically; Qualities is also the preference order, best first, and
// Prefer ranks releases within one quality.
type Profile struct {
	Qualities  []string   `json:"qualities"`
	Reject     []string   `json:"reject"`
	Prefer     []string   `json:"prefer"`
	SizeLimits SizeLimits `json:"size_limits"`
}

// SizeLimits bounds a release's size in MiB; zero means no limit.
type SizeLimits struct {
	MinMB int64 `json:"min_mb"`
	MaxMB int64 `json:"max_mb"`
}

// DefaultProfileName is the built-in profile's name and the default of
// both default_movie_profile and default_series_profile.
const DefaultProfileName = "default"

var qualityTiers = []string{
	"Remux-2160p", "Bluray-2160p", "WEBDL-2160p", "WEBRip-2160p", "HDTV-2160p",
	"Remux-1080p", "Bluray-1080p", "WEBDL-1080p", "WEBRip-1080p", "HDTV-1080p",
	"Bluray-720p", "WEBDL-720p", "WEBRip-720p", "HDTV-720p",
	"Bluray-576p", "Bluray-480p", "WEBDL-480p", "WEBRip-480p",
	"DVD", "SDTV", "CAM", "TELESYNC", "TELECINE", "DVDSCR", "Unknown",
}

// The format tags a profile may reject or prefer.
const (
	TagTrueHD    = "truehd"
	TagDTSHD     = "dts-hd"
	TagDTSX      = "dts-x"
	TagDDPAtmos  = "dd+-atmos"
	TagDDP       = "dd+"
	TagDTS       = "dts"
	TagDD        = "dd"
	TagAAC       = "aac"
	TagFLAC      = "flac"
	TagOpus      = "opus"
	TagDVP7      = "dv-p7"
	TagDV        = "dv"
	TagHDR10Plus = "hdr10+"
	TagHDR10     = "hdr10"
	TagHDR       = "hdr"
	TagAV1       = "av1"
	TagHEVC      = "hevc"
	TagAVC       = "avc"
)

var formatTags = []string{
	TagTrueHD, TagDTSHD, TagDTSX, TagDDPAtmos, TagDDP, TagDTS, TagDD, TagAAC, TagFLAC, TagOpus,
	TagDVP7, TagDV, TagHDR10Plus, TagHDR10, TagHDR, TagAV1, TagHEVC, TagAVC,
}

// QualityTiers lists the canonical quality names a profile may use.
func QualityTiers() []string { return slices.Clone(qualityTiers) }

// FormatTags lists the format tags a profile may use.
func FormatTags() []string { return slices.Clone(formatTags) }

// DefaultProfile is the built-in TV-safe profile used when the config
// defines none: no Remux or camera sources, and nothing an Android TV
// cannot direct-play.
func DefaultProfile() Profile {
	return Profile{
		Qualities: []string{
			"WEBDL-2160p", "WEBRip-2160p", "Bluray-2160p",
			"WEBDL-1080p", "WEBRip-1080p", "Bluray-1080p", "HDTV-1080p",
			"WEBDL-720p", "WEBRip-720p", "Bluray-720p", "HDTV-720p",
		},
		Reject: []string{TagTrueHD, TagDTSHD, TagDTSX, TagDVP7},
		Prefer: []string{TagDV, TagHDR10Plus, TagDDPAtmos, TagDDP, TagDTS, TagDD},
	}
}

func (p Profile) validate(name string) error {
	if len(p.Qualities) == 0 {
		return fmt.Errorf("profiles.%s.qualities: needs at least one quality", name)
	}
	for _, q := range p.Qualities {
		if !slices.Contains(qualityTiers, q) {
			return fmt.Errorf("profiles.%s.qualities: unknown quality %q (one of %s)", name, q, strings.Join(qualityTiers, ", "))
		}
	}
	for _, list := range []struct {
		field string
		tags  []string
	}{{"reject", p.Reject}, {"prefer", p.Prefer}} {
		for _, t := range list.tags {
			if !slices.Contains(formatTags, t) {
				return fmt.Errorf("profiles.%s.%s: unknown tag %q (one of %s)", name, list.field, t, strings.Join(formatTags, ", "))
			}
		}
	}
	l := p.SizeLimits
	switch {
	case l.MinMB < 0:
		return fmt.Errorf("profiles.%s.size_limits.min_mb: must not be negative", name)
	case l.MaxMB < 0:
		return fmt.Errorf("profiles.%s.size_limits.max_mb: must not be negative", name)
	case l.MinMB > 0 && l.MaxMB > 0 && l.MinMB > l.MaxMB:
		return fmt.Errorf("profiles.%s.size_limits: min_mb (%d) is above max_mb (%d)", name, l.MinMB, l.MaxMB)
	}
	return nil
}

func resolveProfiles(c *Config, movie, series string) error {
	if len(c.Profiles) == 0 {
		c.Profiles = map[string]Profile{DefaultProfileName: DefaultProfile()}
	}
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if n == "" {
			return fmt.Errorf("profiles: a profile needs a non-empty name")
		}
		if err := c.Profiles[n].validate(n); err != nil {
			return err
		}
	}
	for _, d := range []struct {
		env, key, value string
		dst             *string
	}{
		{"MARQUEE_DEFAULT_MOVIE_PROFILE", "default_movie_profile", movie, &c.DefaultMovieProfile},
		{"MARQUEE_DEFAULT_SERIES_PROFILE", "default_series_profile", series, &c.DefaultSeriesProfile},
	} {
		v := d.value
		if v == "" {
			v = DefaultProfileName
		}
		if _, ok := c.Profiles[v]; !ok {
			return fmt.Errorf("%s (%s in the config file): profile %q is not defined in profiles", d.env, d.key, v)
		}
		*d.dst = v
	}
	return nil
}

// ProfileSet is the profiles a running marquee knows and which one each
// kind of title uses when it names none.
type ProfileSet struct {
	Profiles      map[string]Profile
	DefaultMovie  string
	DefaultSeries string
}

// ProfileSet returns the profiles the config defines.
func (c Config) ProfileSet() ProfileSet {
	return ProfileSet{Profiles: c.Profiles, DefaultMovie: c.DefaultMovieProfile, DefaultSeries: c.DefaultSeriesProfile}
}

// Names lists the profile names in alphabetical order.
func (s ProfileSet) Names() []string {
	names := make([]string, 0, len(s.Profiles))
	for n := range s.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Default is the name of the profile a title of this kind uses when it
// names none.
func (s ProfileSet) Default(kind string) string {
	name := s.DefaultMovie
	if kind == "series" {
		name = s.DefaultSeries
	}
	if name == "" {
		return DefaultProfileName
	}
	return name
}

// Resolve returns the profile a title of this kind is judged by, and its
// name. An empty name means the kind's default; a name the config no
// longer defines also gets the default, and fellBack says so, so the
// caller can warn.
func (s ProfileSet) Resolve(kind, name string) (p Profile, resolved string, fellBack bool) {
	if name != "" {
		if p, ok := s.Profiles[name]; ok {
			return p, name, false
		}
		fellBack = true
	}
	resolved = s.Default(kind)
	if p, ok := s.Profiles[resolved]; ok {
		return p, resolved, fellBack
	}
	return DefaultProfile(), DefaultProfileName, fellBack
}
