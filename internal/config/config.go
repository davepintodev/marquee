// Package config loads marquee's settings from a JSON file and
// environment variables, and keeps secrets out of every output.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const redacted = "[redacted]"

// Secret holds a credential. Every formatting path prints a placeholder;
// the value is only reachable through Reveal.
type Secret string

func (s Secret) Reveal() string { return string(s) }

func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

func (s Secret) GoString() string              { return strconv.Quote(s.String()) }
func (s Secret) MarshalText() ([]byte, error)  { return []byte(s.String()), nil }
func (s Secret) LogValue() slog.Value          { return slog.StringValue(s.String()) }
func (s Secret) MarshalJSON() ([]byte, error)  { return json.Marshal(s.String()) }
func (s *Secret) UnmarshalText(b []byte) error { *s = Secret(b); return nil }
func (s *Secret) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, (*string)(s)) }

// Config is marquee's complete configuration.
type Config struct {
	AIOStreamsManifestURL Secret
	TMDBKey               Secret
	// OMDbKey is optional: without it the details screen has TMDB's score only.
	OMDbKey     Secret
	MoviesPath  string
	SeriesPath  string
	TempPath    string
	DataDir     string
	JellyfinURL string
	JellyfinKey Secret
	Listen      string
	APIToken    Secret
	DryRun      bool

	// Profiles is never empty after Load: without any in the file it holds
	// the built-in "default". Both defaults name one of them.
	Profiles             map[string]Profile
	DefaultMovieProfile  string
	DefaultSeriesProfile string
}

func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("aiostreams_manifest_url", c.AIOStreamsManifestURL),
		slog.Any("tmdb_key", c.TMDBKey),
		slog.Any("omdb_key", c.OMDbKey),
		slog.String("movies_path", c.MoviesPath),
		slog.String("series_path", c.SeriesPath),
		slog.String("temp_path", c.TempPath),
		slog.String("data_dir", c.DataDir),
		slog.String("jellyfin_url", c.JellyfinURL),
		slog.Any("jellyfin_key", c.JellyfinKey),
		slog.String("listen", c.Listen),
		slog.Any("api_token", c.APIToken),
		slog.Bool("dry_run", c.DryRun),
		slog.Int("profiles", len(c.Profiles)),
		slog.String("default_movie_profile", c.DefaultMovieProfile),
		slog.String("default_series_profile", c.DefaultSeriesProfile),
	)
}

type fileConfig struct {
	AIOStreamsManifestURL string `json:"aiostreams_manifest_url"`
	TMDBKey               string `json:"tmdb_key"`
	OMDbKey               string `json:"omdb_key"`
	MoviesPath            string `json:"movies_path"`
	SeriesPath            string `json:"series_path"`
	TempPath              string `json:"temp_path"`
	DataDir               string `json:"data_dir"`
	JellyfinURL           string `json:"jellyfin_url"`
	JellyfinKey           string `json:"jellyfin_key"`
	Listen                string `json:"listen"`
	APIToken              string `json:"api_token"`
	DryRun                *bool  `json:"dry_run"`

	Profiles             map[string]Profile `json:"profiles"`
	DefaultMovieProfile  string             `json:"default_movie_profile"`
	DefaultSeriesProfile string             `json:"default_series_profile"`
}

// Load reads the file named by MARQUEE_CONFIG (if any), lets MARQUEE_*
// environment variables override it, and checks the essentials. No
// returned error ever contains a setting's value.
func Load(getenv func(string) string) (Config, error) {
	var f fileConfig
	if path := getenv("MARQUEE_CONFIG"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("MARQUEE_CONFIG: cannot read %s", path)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			// A syntax error's text quotes a character of the file, which may be a secret.
			var syn *json.SyntaxError
			if errors.As(err, &syn) {
				return Config{}, fmt.Errorf("MARQUEE_CONFIG: %s is not a valid config file: invalid JSON near byte %d", path, syn.Offset)
			}
			return Config{}, fmt.Errorf("MARQUEE_CONFIG: %s is not a valid config file: %w", path, err)
		}
	}

	pick := func(env, fromFile string) string {
		if v := getenv(env); v != "" {
			return v
		}
		return fromFile
	}

	c := Config{
		AIOStreamsManifestURL: Secret(pick("MARQUEE_AIOSTREAMS_MANIFEST_URL", f.AIOStreamsManifestURL)),
		TMDBKey:               Secret(pick("MARQUEE_TMDB_KEY", f.TMDBKey)),
		OMDbKey:               Secret(pick("MARQUEE_OMDB_KEY", f.OMDbKey)),
		MoviesPath:            pick("MARQUEE_MOVIES_PATH", f.MoviesPath),
		SeriesPath:            pick("MARQUEE_SERIES_PATH", f.SeriesPath),
		TempPath:              pick("MARQUEE_TEMP_PATH", f.TempPath),
		DataDir:               pick("MARQUEE_DATA_DIR", f.DataDir),
		JellyfinURL:           pick("MARQUEE_JELLYFIN_URL", f.JellyfinURL),
		JellyfinKey:           Secret(pick("MARQUEE_JELLYFIN_KEY", f.JellyfinKey)),
		Listen:                pick("MARQUEE_LISTEN", f.Listen),
		APIToken:              Secret(pick("MARQUEE_API_TOKEN", f.APIToken)),
		Profiles:              f.Profiles,
	}
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if f.DryRun != nil {
		c.DryRun = *f.DryRun
	}
	if v := getenv("MARQUEE_DRY_RUN"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, errors.New("MARQUEE_DRY_RUN must be true or false")
		}
		c.DryRun = b
	}

	var missing []string
	for _, e := range []struct {
		env, key, value string
	}{
		{"MARQUEE_AIOSTREAMS_MANIFEST_URL", "aiostreams_manifest_url", c.AIOStreamsManifestURL.Reveal()},
		{"MARQUEE_TMDB_KEY", "tmdb_key", c.TMDBKey.Reveal()},
		{"MARQUEE_MOVIES_PATH", "movies_path", c.MoviesPath},
		{"MARQUEE_SERIES_PATH", "series_path", c.SeriesPath},
		{"MARQUEE_TEMP_PATH", "temp_path", c.TempPath},
		{"MARQUEE_DATA_DIR", "data_dir", c.DataDir},
	} {
		if e.value == "" {
			missing = append(missing, fmt.Sprintf("%s (%s in the config file)", e.env, e.key))
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required setting(s): %s", strings.Join(missing, ", "))
	}

	// url.Parse errors quote the input, which here is a credential.
	u, err := url.Parse(c.AIOStreamsManifestURL.Reveal())
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, errors.New("MARQUEE_AIOSTREAMS_MANIFEST_URL is not a valid http(s) URL")
	}
	if err := resolveProfiles(&c,
		pick("MARQUEE_DEFAULT_MOVIE_PROFILE", f.DefaultMovieProfile),
		pick("MARQUEE_DEFAULT_SERIES_PROFILE", f.DefaultSeriesProfile)); err != nil {
		return Config{}, err
	}
	return c, nil
}
