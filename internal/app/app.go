// Package app wires configuration, checks and the HTTP server together.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/davepintodev/marquee/internal/adopter"
	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/fake"
	"github.com/davepintodev/marquee/internal/httpapi"
	"github.com/davepintodev/marquee/internal/importer"
	"github.com/davepintodev/marquee/internal/metadata"
	"github.com/davepintodev/marquee/internal/pipeline"
	"github.com/davepintodev/marquee/internal/scheduler"
	"github.com/davepintodev/marquee/internal/server"
)

// Options are the seams tests use to replace what the outside world
// provides. The zero value is production.
type Options struct {
	// Now judges which episodes have aired; nil is the system clock.
	Now func() time.Time
	// TMDBAPIBase and TMDBImageBase replace TMDB's hosts; empty is TMDB.
	TMDBAPIBase, TMDBImageBase string
	// TMDBTimeout bounds a wait on TMDB; zero is ten seconds.
	TMDBTimeout time.Duration
	// TMDBImageTimeout bounds one whole image download; zero is thirty seconds.
	TMDBImageTimeout time.Duration
	// AIOStreamsTimeout bounds one AIOStreams search; zero is ninety seconds.
	AIOStreamsTimeout time.Duration
	// Sleep waits out a TMDB rate limit; nil really sleeps.
	Sleep func(ctx context.Context, d time.Duration) error
	// Transfers tunes the download pipeline; the zero value is production.
	Transfers pipeline.Tuning
	// OnPipeline is called with the download manager once it is built,
	// before it starts; tests use it to queue a release, as the scheduler will.
	OnPipeline func(*pipeline.Manager)
	// OnAdopted is called once the first-run adoption has finished, with
	// its error, or with nil when an earlier run had already completed it.
	OnAdopted func(error)
	// HealthTimeout bounds each probe of a Health read; zero is eight seconds.
	HealthTimeout time.Duration
	// Scheduler tunes the scheduler; the zero value is production.
	Scheduler scheduler.Tuning
	// OnScheduler is called with the scheduler once it is built; tests
	// drive it with Step.
	OnScheduler func(*scheduler.Scheduler)
	// EventsEvery is how often the live event stream looks for changes;
	// zero is one second.
	EventsEvery time.Duration
}

// Run loads and checks the configuration, then serves until ctx ends.
// ready, if set, is called with the bound address once the listener is up.
func Run(ctx context.Context, getenv func(string) string, logOut io.Writer, ui fs.FS, ready func(net.Addr)) error {
	return RunWith(ctx, Options{}, getenv, logOut, ui, ready)
}

// RunWith is Run with the seams of opts.
func RunWith(ctx context.Context, opts Options, getenv func(string) string, logOut io.Writer, ui fs.FS, ready func(net.Addr)) error {
	log := slog.New(slog.NewTextHandler(logOut, nil))

	var (
		listen string
		token  config.Secret
		api    http.Handler
		closer func()
		msg    string
		attrs  []any
	)
	fakeMode := false
	if v := getenv("MARQUEE_FAKE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return errors.New("MARQUEE_FAKE must be true or false")
		}
		fakeMode = b
	}
	if fakeMode {
		listen = getenv("MARQUEE_LISTEN")
		if listen == "" {
			listen = ":8080"
		}
		token = config.Secret(getenv("MARQUEE_API_TOKEN"))
		f := fake.New(fake.Options{ArtDir: getenv("MARQUEE_FAKE_ART_DIR")})
		defer f.Close()
		api, closer = f, f.Close
		msg = "marquee starting in fake mode: the API serves committed fixtures and nothing is downloaded; TMDB key, library paths and the other essential settings are not required"
	} else {
		cfg, err := config.Load(getenv)
		if err != nil {
			return err
		}
		if err := cfg.CheckPaths(); err != nil {
			return err
		}
		store, err := catalog.Open(cfg.DataDir, opts.Now)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		transfers := pipeline.New(pipeline.Options{
			Catalog: store, JellyfinURL: cfg.JellyfinURL, JellyfinKey: cfg.JellyfinKey,
			MoviesPath: cfg.MoviesPath, SeriesPath: cfg.SeriesPath, TempPath: cfg.TempPath,
			Log: log, Tuning: opts.Transfers,
		})
		if opts.OnPipeline != nil {
			opts.OnPipeline(transfers)
		}
		// Dry-run never downloads or writes to the library, so the queue is not worked at all.
		if !cfg.DryRun {
			transfersCtx, stopTransfers := context.WithCancel(context.Background())
			transfersDone := make(chan struct{})
			go func() {
				defer close(transfersDone)
				transfers.Run(transfersCtx)
			}()
			// The database closes after this: a download in flight must stop and save its progress first.
			defer func() {
				stopTransfers()
				<-transfersDone
			}()
		}
		tmdb := metadata.New(metadata.Options{
			APIBase: opts.TMDBAPIBase, ImageBase: opts.TMDBImageBase, Key: cfg.TMDBKey,
			Timeout: opts.TMDBTimeout, ImageTimeout: opts.TMDBImageTimeout, Sleep: opts.Sleep,
		})
		source, err := aiostreams.New(aiostreams.Options{Manifest: cfg.AIOStreamsManifestURL, Timeout: opts.AIOStreamsTimeout})
		if err != nil {
			return fmt.Errorf("MARQUEE_AIOSTREAMS_MANIFEST_URL %w", err)
		}
		// Adoption reads the library and records it; it runs whatever the dry-run
		// setting says, since it never writes to the library.
		adoptCtx, stopAdopt := context.WithCancel(context.Background())
		adoptDone := make(chan struct{})
		go func() {
			defer close(adoptDone)
			err := adopter.New(adopter.Options{
				Catalog: store, TMDB: tmdb, MoviesPath: cfg.MoviesPath, SeriesPath: cfg.SeriesPath, Log: log,
			}).Run(adoptCtx)
			if err != nil && adoptCtx.Err() == nil {
				log.Error("library adoption stopped; it will run again at the next start", "err", err)
			}
			if opts.OnAdopted != nil {
				opts.OnAdopted(err)
			}
		}()
		// The database closes after this: a run in flight must stop first.
		defer func() {
			stopAdopt()
			<-adoptDone
		}()
		sched := scheduler.New(scheduler.Options{
			Catalog: store, Source: source, TMDB: tmdb, Transfers: transfers, DryRun: cfg.DryRun, Log: log, Tuning: opts.Scheduler,
		})
		if opts.OnScheduler != nil {
			opts.OnScheduler(sched)
		}
		schedCtx, stopSched := context.WithCancel(context.Background())
		schedDone := make(chan struct{})
		go func() {
			defer close(schedDone)
			sched.Run(schedCtx)
		}()
		// A search in flight must stop before the database closes.
		defer func() {
			stopSched()
			<-schedDone
		}()
		listen = cfg.Listen
		token = cfg.APIToken
		streamsStop := make(chan struct{})
		closer = func() { close(streamsStop) }
		var jellyfin httpapi.Pinger
		if cfg.JellyfinURL != "" {
			jellyfin = importer.NewJellyfin(cfg.JellyfinURL, cfg.JellyfinKey, opts.HealthTimeout)
		}
		api = httpapi.New(httpapi.Deps{
			Catalog: store, TMDB: tmdb, Source: source, Now: opts.Now, Log: log, DryRun: cfg.DryRun, Wake: sched.Wake,
			Paths: cfg.Paths(), Jellyfin: jellyfin, HealthTimeout: opts.HealthTimeout,
			Images: metadata.NewImages(filepath.Join(cfg.DataDir, "images"), metadata.DefaultCacheBytes, tmdb),
			EventsEvery: opts.EventsEvery, Stop: streamsStop,
		})
		msg, attrs = "marquee starting", []any{"config", cfg}
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("MARQUEE_LISTEN: cannot listen on %s: %w", listen, err)
	}
	srv := &http.Server{Handler: server.New(ui, httpapi.RequireToken(token, api)), ReadHeaderTimeout: 10 * time.Second}
	if closer != nil {
		// Shutdown waits for handlers, and an event stream only returns once the fake closes.
		srv.RegisterOnShutdown(closer)
	}

	log.Info(msg, append([]any{"addr", ln.Addr().String()}, attrs...)...)
	if ready != nil {
		ready(ln.Addr())
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
