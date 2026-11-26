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
	"time"
)

const (
	redacted              = "[redacted]"
	defaultRescanInterval = 30 * time.Minute
)

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
	// SubfitURL is optional: set, marquee tells subfit when files change. It
	// is a Secret because an address can carry credentials.
	SubfitURL Secret
	Listen    string
	APIToken  Secret
	// SubfitToken opens the file identity lookup and nothing else; empty
	// leaves the lookup to the API token alone.
	SubfitToken Secret
	DryRun      bool
	// RescanInterval is how often the library is read again; zero turns the timer off.
	RescanInterval time.Duration

	// TorBoxAPIKey is optional: without it marquee only downloads releases
	// the debrid service already has cached.
	TorBoxAPIKey Secret
	// TorBoxMaxActive caps the torrents TorBox is caching for marquee at once.
	TorBoxMaxActive int
	// TorBoxStallTimeout and TorBoxMaxLifetime end a caching torrent that
	// makes no progress, or takes too long overall.
	TorBoxStallTimeout, TorBoxMaxLifetime time.Duration
	// TorBoxDeleteOnComplete removes a torrent from the TorBox account once
	// its file has been imported.
	TorBoxDeleteOnComplete bool

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
		slog.Any("subfit_url", c.SubfitURL),
		slog.String("listen", c.Listen),
		slog.Any("api_token", c.APIToken),
		slog.Any("subfit_token", c.SubfitToken),
		slog.Bool("dry_run", c.DryRun),
		slog.Duration("rescan_interval", c.RescanInterval),
		slog.Any("torbox_key", c.TorBoxAPIKey),
		slog.Int("torbox_max_active", c.TorBoxMaxActive),
		slog.Duration("torbox_stall_timeout", c.TorBoxStallTimeout),
		slog.Duration("torbox_max_lifetime", c.TorBoxMaxLifetime),
		slog.Bool("torbox_delete_on_complete", c.TorBoxDeleteOnComplete),
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
	SubfitURL             string `json:"subfit_url"`
	Listen                string `json:"listen"`
	APIToken              string `json:"api_token"`
	SubfitToken           string `json:"subfit_token"`
	DryRun                *bool  `json:"dry_run"`
	RescanInterval        string `json:"rescan_interval"`

	TorBoxAPIKey           string `json:"torbox_api_key"`
	TorBoxMaxActive        *int   `json:"torbox_max_active"`
	TorBoxStallTimeout     string `json:"torbox_stall_timeout"`
	TorBoxMaxLifetime      string `json:"torbox_max_lifetime"`
	TorBoxDeleteOnComplete *bool  `json:"torbox_delete_on_complete"`

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
		SubfitURL:             Secret(pick("MARQUEE_SUBFIT_URL", f.SubfitURL)),
		Listen:                pick("MARQUEE_LISTEN", f.Listen),
		APIToken:              Secret(pick("MARQUEE_API_TOKEN", f.APIToken)),
		SubfitToken:           Secret(pick("MARQUEE_SUBFIT_TOKEN", f.SubfitToken)),
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

	c.RescanInterval = defaultRescanInterval
	if v := pick("MARQUEE_RESCAN_INTERVAL", f.RescanInterval); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return Config{}, errors.New("MARQUEE_RESCAN_INTERVAL (rescan_interval in the config file) must be a duration such as 30m, or 0 to turn the timer off")
		}
		c.RescanInterval = d
	}

	if err := loadTorBox(&c, &f, getenv, pick); err != nil {
		return Config{}, err
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
	if c.SubfitURL != "" {
		su, err := url.Parse(c.SubfitURL.Reveal())
		if err != nil || (su.Scheme != "http" && su.Scheme != "https") || su.Host == "" {
			return Config{}, errors.New("MARQUEE_SUBFIT_URL (subfit_url in the config file) is not a valid http(s) URL")
		}
	}
	if err := resolveProfiles(&c,
		pick("MARQUEE_DEFAULT_MOVIE_PROFILE", f.DefaultMovieProfile),
		pick("MARQUEE_DEFAULT_SERIES_PROFILE", f.DefaultSeriesProfile)); err != nil {
		return Config{}, err
	}
	return c, nil
}

const (
	defaultTorBoxMaxActive    = 2
	defaultTorBoxStallTimeout = 30 * time.Minute
	defaultTorBoxMaxLifetime  = 4 * time.Hour
)

func loadTorBox(c *Config, f *fileConfig, getenv func(string) string, pick func(env, fromFile string) string) error {
	c.TorBoxAPIKey = Secret(pick("MARQUEE_TORBOX_API_KEY", f.TorBoxAPIKey))

	c.TorBoxMaxActive = defaultTorBoxMaxActive
	if f.TorBoxMaxActive != nil {
		c.TorBoxMaxActive = *f.TorBoxMaxActive
	}
	if v := getenv("MARQUEE_TORBOX_MAX_ACTIVE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			n = 0
		}
		c.TorBoxMaxActive = n
	}
	if c.TorBoxMaxActive < 1 {
		return errors.New("MARQUEE_TORBOX_MAX_ACTIVE (torbox_max_active in the config file) must be a whole number of at least 1")
	}

	for _, d := range []struct {
		dst           *time.Duration
		def           time.Duration
		env, key, val string
	}{
		{&c.TorBoxStallTimeout, defaultTorBoxStallTimeout, "MARQUEE_TORBOX_STALL_TIMEOUT", "torbox_stall_timeout", f.TorBoxStallTimeout},
		{&c.TorBoxMaxLifetime, defaultTorBoxMaxLifetime, "MARQUEE_TORBOX_MAX_LIFETIME", "torbox_max_lifetime", f.TorBoxMaxLifetime},
	} {
		*d.dst = d.def
		if v := pick(d.env, d.val); v != "" {
			parsed, err := time.ParseDuration(v)
			if err != nil || parsed < time.Second {
				return fmt.Errorf("%s (%s in the config file) must be a duration of at least 1s, such as 30m", d.env, d.key)
			}
			*d.dst = parsed
		}
	}

	c.TorBoxDeleteOnComplete = true
	if f.TorBoxDeleteOnComplete != nil {
		c.TorBoxDeleteOnComplete = *f.TorBoxDeleteOnComplete
	}
	if v := getenv("MARQUEE_TORBOX_DELETE_ON_COMPLETE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return errors.New("MARQUEE_TORBOX_DELETE_ON_COMPLETE must be true or false")
		}
		c.TorBoxDeleteOnComplete = b
	}
	return nil
}
