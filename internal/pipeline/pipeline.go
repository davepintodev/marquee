// Package pipeline runs downloads from the queue to the library: it takes
// queued downloads, fetches each file, verifies it, imports it under its
// Sonarr/Radarr name and refreshes Jellyfin. State lives in the catalog, so
// a restart picks up where each download was.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/download"
	"github.com/davepintodev/marquee/internal/importer"
	"github.com/davepintodev/marquee/internal/naming"
)

// ReasonLocalError is a failure on marquee's side (a full disk, a file
// already in the library); the release itself is not at fault, so it is not
// blocklisted.
const ReasonLocalError = "local_error"

// Default size floors: a finished file smaller than this is a sample or a stub.
const (
	DefaultMovieFloor   = 200 << 20
	DefaultEpisodeFloor = 50 << 20
)

// Tuning holds what tests change; the zero value is production.
type Tuning struct {
	// Concurrency is the downloads in flight at once; default 2.
	Concurrency int
	// MovieFloor and EpisodeFloor are the smallest sizes a finished file may have.
	MovieFloor, EpisodeFloor int64
	// Poll is how often the queue is looked at when nothing wakes it; default 5 s.
	Poll time.Duration
	// JellyfinTimeout bounds a refresh call; default 10 s.
	JellyfinTimeout time.Duration
	// Download tunes the HTTP downloader.
	Download download.Options
}

// Options are the manager's dependencies.
type Options struct {
	Catalog *catalog.Store
	// Downloader replaces the HTTP downloader built from Tuning.Download.
	Downloader download.Downloader
	// JellyfinURL and JellyfinKey are optional; without a URL nothing is refreshed.
	JellyfinURL string
	JellyfinKey config.Secret
	// MoviesPath, SeriesPath and TempPath come from the configuration; the
	// temp path must be on the libraries' filesystem.
	MoviesPath, SeriesPath, TempPath string
	Log                              *slog.Logger
	Tuning                           Tuning
}

// Job is one release to download into the library.
type Job struct {
	TitleID string
	// Season and Number identify the episode of a series, and are zero for a movie.
	Season, Number int
	Release        catalog.ReleaseID
	// ReleaseName is the release's name for the history, Resolution and
	// Quality its parsed resolution and quality for the file name's tag.
	ReleaseName, Resolution, Quality string
	// URL is the playback link, which carries credentials.
	URL config.Secret
}

// Manager runs the downloads.
type Manager struct {
	cat      *catalog.Store
	dl       download.Downloader
	jf       *importer.Jellyfin
	movies   string
	series   string
	tempDir  string
	log      *slog.Logger
	tune     Tuning
	wake     chan struct{}
	mu       sync.Mutex
	running  map[string]bool
	idSource func() string
}

// New returns a manager; call Run to start it.
func New(o Options) *Manager {
	t := o.Tuning
	if t.Concurrency <= 0 {
		t.Concurrency = 2
	}
	if t.MovieFloor <= 0 {
		t.MovieFloor = DefaultMovieFloor
	}
	if t.EpisodeFloor <= 0 {
		t.EpisodeFloor = DefaultEpisodeFloor
	}
	if t.Poll <= 0 {
		t.Poll = 5 * time.Second
	}
	if t.JellyfinTimeout <= 0 {
		t.JellyfinTimeout = 10 * time.Second
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	dl := o.Downloader
	if dl == nil {
		if t.Download.Log == nil {
			t.Download.Log = log
		}
		dl = download.NewHTTP(t.Download)
	}
	m := &Manager{
		cat: o.Catalog, dl: dl, movies: o.MoviesPath, series: o.SeriesPath,
		tempDir: filepath.Join(o.TempPath, "marquee"), log: log, tune: t,
		wake: make(chan struct{}, 1), running: map[string]bool{}, idSource: newID,
	}
	if o.JellyfinURL != "" {
		m.jf = importer.NewJellyfin(o.JellyfinURL, o.JellyfinKey, t.JellyfinTimeout)
	}
	return m
}

// ErrBadJob is returned for a job that names no such title or episode.
var ErrBadJob = errors.New("pipeline: no such title or episode")

// Enqueue queues a download. A title or episode that already has an
// unfinished download gets that one back with catalog.ErrActiveDownload, so
// a double click or a second search does no harm.
func (m *Manager) Enqueue(ctx context.Context, j Job) (catalog.Download, error) {
	t, err := m.cat.Get(ctx, j.TitleID)
	if errors.Is(err, catalog.ErrNotFound) {
		return catalog.Download{}, ErrBadJob
	}
	if err != nil {
		return catalog.Download{}, err
	}
	if t.Kind == "movie" {
		if j.Season != 0 || j.Number != 0 {
			return catalog.Download{}, ErrBadJob
		}
	} else if _, ok := episodeName(t, j.Season, j.Number); !ok {
		return catalog.Download{}, ErrBadJob
	}
	d, err := m.cat.AddDownload(ctx, catalog.NewDownload{
		ID: m.idSource(), TitleID: j.TitleID, Season: j.Season, Number: j.Number, Release: j.Release,
		ReleaseName: j.ReleaseName, Resolution: j.Resolution, Quality: j.Quality, URL: j.URL,
	})
	if errors.Is(err, catalog.ErrActiveDownload) {
		existing, gerr := m.cat.ActiveDownloadFor(ctx, j.TitleID, j.Season, j.Number)
		if gerr != nil {
			return catalog.Download{}, err
		}
		return existing, catalog.ErrActiveDownload
	}
	if err != nil {
		return catalog.Download{}, err
	}
	m.kick()
	return d, nil
}

func (m *Manager) kick() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Run works the queue until ctx ends and returns once every download in
// flight has stopped, leaving partial files for the next run to resume.
func (m *Manager) Run(ctx context.Context) {
	m.pruneTemp(ctx)
	var wg sync.WaitGroup
	tick := time.NewTicker(m.tune.Poll)
	defer tick.Stop()
	for {
		m.dispatch(ctx, &wg)
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-m.wake:
		case <-tick.C:
		}
	}
}

func (m *Manager) dispatch(ctx context.Context, wg *sync.WaitGroup) {
	active, err := m.cat.ActiveDownloads(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.log.Error("cannot read the download queue", "err", err)
		}
		return
	}
	for _, d := range active {
		m.mu.Lock()
		skip := m.running[d.ID] || len(m.running) >= m.tune.Concurrency
		if !skip {
			m.running[d.ID] = true
		}
		m.mu.Unlock()
		if skip {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				m.mu.Lock()
				delete(m.running, d.ID)
				m.mu.Unlock()
				m.kick()
			}()
			m.process(ctx, d)
		}()
	}
}

func (m *Manager) partPath(id string) string { return filepath.Join(m.tempDir, id+".part") }

func (m *Manager) removeTemp(id string) {
	p := m.partPath(id)
	for _, f := range []string{p, p + ".plan", p + ".plan.tmp"} {
		_ = os.Remove(f)
	}
}

// pruneTemp removes what a crash left in marquee's own temp folder for
// downloads that no longer exist. It touches nothing else in the temp path.
func (m *Manager) pruneTemp(ctx context.Context) {
	entries, err := os.ReadDir(m.tempDir)
	if err != nil {
		return
	}
	active, err := m.cat.ActiveDownloads(ctx)
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, d := range active {
		live[d.ID] = true
	}
	for _, e := range entries {
		id, _, _ := strings.Cut(e.Name(), ".part")
		if e.IsDir() || !strings.Contains(e.Name(), ".part") || live[id] {
			continue
		}
		_ = os.Remove(filepath.Join(m.tempDir, e.Name()))
	}
}

var reasonLabels = map[string]string{
	string(download.ReasonStalled):      "Stalled",
	string(download.ReasonNotVideo):     "Not a video",
	string(download.ReasonSizeMismatch): "Wrong size",
	string(download.ReasonSourceDown):   "Source down",
	"too_small":                         "Too small",
	ReasonLocalError:                    "Could not save",
}

// fail ends the download with a reason. A release that is at fault is
// blocklisted; a failure on marquee's side is not.
func (m *Manager) fail(ctx context.Context, d catalog.Download, reason, detail string, blocklist bool) {
	m.removeTemp(d.ID)
	msg := reasonLabels[reason] + ": " + detail
	err := m.cat.FailDownload(context.WithoutCancel(ctx), d.ID, reason, msg, blocklist)
	if err != nil && !errors.Is(err, catalog.ErrNotFound) {
		m.log.Error("cannot record a failed download", "download", d.ID, "err", err)
		return
	}
	m.log.Warn("download failed", "download", d.ID, "title", d.TitleID, "reason", reason, "detail", detail)
}

func (m *Manager) process(ctx context.Context, d catalog.Download) {
	state := d.State
	if state == catalog.StateQueued || state == catalog.StateDownloading {
		if !m.fetch(ctx, &d) {
			return
		}
		state = catalog.StateVerifying
	}
	if state == catalog.StateVerifying {
		if !m.verify(ctx, d) {
			return
		}
	}
	m.place(ctx, d)
}

func (m *Manager) fetch(ctx context.Context, d *catalog.Download) bool {
	if err := os.MkdirAll(m.tempDir, 0o750); err != nil {
		m.fail(ctx, *d, ReasonLocalError, "cannot create the temp folder", false)
		return false
	}
	if err := m.cat.SetDownloadState(ctx, d.ID, catalog.StateDownloading); err != nil {
		m.gone(ctx, *d, err)
		return false
	}
	fctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var removed bool
	var mu sync.Mutex
	res, err := m.dl.Download(fctx, download.Request{
		URL: d.URL, Dest: m.partPath(d.ID), ReleaseSize: d.Release.Size,
		Progress: func(done, total int64) {
			if err := m.cat.SetDownloadProgress(fctx, d.ID, done, total); errors.Is(err, catalog.ErrNotFound) {
				mu.Lock()
				removed = true
				mu.Unlock()
				cancel()
			}
		},
	})
	mu.Lock()
	wasRemoved := removed
	mu.Unlock()
	switch {
	case wasRemoved:
		m.removeTemp(d.ID)
		return false
	case ctx.Err() != nil:
		return false // shutting down: what was fetched stays for the next run
	case err != nil:
		if f, ok := download.AsFailure(err); ok {
			m.fail(ctx, *d, string(f.Reason), f.Detail, true)
		} else {
			m.log.Error("download error", "download", d.ID, "err", err)
			m.fail(ctx, *d, ReasonLocalError, "the file could not be written to the temp folder", false)
		}
		return false
	}
	if err := m.cat.SetDownloadProgress(ctx, d.ID, res.Size, res.Size); err != nil {
		m.gone(ctx, *d, err)
		return false
	}
	if err := m.cat.SetDownloadState(ctx, d.ID, catalog.StateVerifying); err != nil {
		m.gone(ctx, *d, err)
		return false
	}
	d.BytesTotal = res.Size
	return true
}

// gone handles a download whose row vanished (its title was removed) or a
// database error.
func (m *Manager) gone(ctx context.Context, d catalog.Download, err error) {
	if errors.Is(err, catalog.ErrNotFound) {
		m.removeTemp(d.ID)
		return
	}
	if ctx.Err() == nil {
		m.log.Error("cannot update a download", "download", d.ID, "err", err)
	}
}

func (m *Manager) verify(ctx context.Context, d catalog.Download) bool {
	part := m.partPath(d.ID)
	fi, err := os.Stat(part)
	if err != nil {
		m.fail(ctx, d, ReasonLocalError, "the downloaded file is gone from the temp folder", false)
		return false
	}
	t, err := m.cat.Get(ctx, d.TitleID)
	if err != nil {
		m.gone(ctx, d, err)
		return false
	}
	floor := m.tune.EpisodeFloor
	if t.Kind == "movie" {
		floor = m.tune.MovieFloor
	}
	videoErr := download.CheckVideo(part)
	switch {
	case fi.Size() != d.BytesTotal:
		m.fail(ctx, d, string(download.ReasonSizeMismatch), fmt.Sprintf("the file is %d bytes but the server said it has %d", fi.Size(), d.BytesTotal), true)
	case videoErr != nil:
		if f, ok := download.AsFailure(videoErr); ok {
			m.fail(ctx, d, string(f.Reason), f.Detail, true)
		} else {
			m.fail(ctx, d, ReasonLocalError, "the downloaded file could not be read", false)
		}
	case fi.Size() < floor:
		m.fail(ctx, d, "too_small", fmt.Sprintf("the file is %d bytes, under the %d-byte minimum, so it is a sample or a stub", fi.Size(), floor), true)
	default:
		if err := m.cat.SetDownloadState(ctx, d.ID, catalog.StateImporting); err != nil {
			m.gone(ctx, d, err)
			return false
		}
		return true
	}
	return false
}

func (m *Manager) place(ctx context.Context, d catalog.Download) {
	t, err := m.cat.Get(ctx, d.TitleID)
	if err != nil {
		m.gone(ctx, d, err)
		return
	}
	dst := m.libraryPath(t, d)
	if err := m.cat.SetDownloadLibraryPath(ctx, d.ID, dst); err != nil {
		m.gone(ctx, d, err)
		return
	}
	part := m.partPath(d.ID)
	if _, err := os.Stat(part); errors.Is(err, os.ErrNotExist) {
		// A restart between the move and the bookkeeping: the file is
		// already in the library.
		if fi, serr := os.Stat(dst); serr != nil || fi.Size() != d.BytesTotal {
			m.fail(ctx, d, ReasonLocalError, "the downloaded file is gone from the temp folder", false)
			return
		}
	} else if err := importer.Place(part, dst); err != nil {
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, importer.ErrExists) {
			m.fail(ctx, d, ReasonLocalError, "a file with this name is already in the library, so marquee left it alone", false)
		} else {
			m.log.Error("import failed", "download", d.ID, "err", err)
			m.fail(ctx, d, ReasonLocalError, "the file could not be moved into the library: "+err.Error(), false)
		}
		return
	}
	// A leftover hard link source, if a crash fell between link and remove.
	_ = os.Remove(part)
	_ = os.Remove(part + ".plan")
	if err := m.cat.FinishDownload(ctx, d.ID); err != nil {
		m.gone(ctx, d, err)
		return
	}
	m.log.Info("imported", "download", d.ID, "title", d.TitleID)
	m.refreshJellyfin(ctx)
}

// refreshJellyfin asks Jellyfin to scan. The import has already succeeded;
// a failure here is recorded for the Health page and nothing more.
func (m *Manager) refreshJellyfin(ctx context.Context) {
	if m.jf == nil {
		return
	}
	err := m.jf.Refresh(ctx)
	if ctx.Err() != nil {
		return
	}
	ok, msg := err == nil, "the last library refresh worked"
	if err != nil {
		msg = err.Error()
		m.log.Warn("jellyfin refresh failed", "err", err)
	}
	if serr := m.cat.SetComponentStatus(ctx, "jellyfin", ok, msg); serr != nil && ctx.Err() == nil {
		m.log.Error("cannot record the jellyfin status", "err", serr)
	}
}

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".ts": true, ".m2ts": true,
	".wmv": true, ".mov": true, ".webm": true, ".mpg": true, ".mpeg": true,
}

// libraryPath is where a download goes: the movie or series folder, with
// the names Radarr and Sonarr give.
func (m *Manager) libraryPath(t catalog.Title, d catalog.Download) string {
	ext := strings.ToLower(filepath.Ext(d.Release.FileName))
	if !videoExts[ext] {
		ext = ".mkv"
	}
	if t.Kind == "movie" {
		q := naming.QualityName(naming.KindMovie, d.Resolution, d.Quality)
		return filepath.Join(m.movies, naming.MovieFolder(t.Title, t.Year), naming.MovieFile(t.Title, t.Year, q, ext))
	}
	name, _ := episodeName(t, d.Season, d.Number)
	q := naming.QualityName(naming.KindSeries, d.Resolution, d.Quality)
	return filepath.Join(m.series, naming.SeriesFolder(t.Title), naming.SeasonFolder(d.Season),
		naming.EpisodeFile(t.Title, d.Season, d.Number, name, q, ext))
}

func episodeName(t catalog.Title, season, number int) (string, bool) {
	for _, s := range t.Seasons {
		if s.Number != season {
			continue
		}
		for _, e := range s.Episodes {
			if e.Number == number {
				return e.Name, true
			}
		}
	}
	return "", false
}
