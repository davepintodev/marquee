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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/download"
	"github.com/davepintodev/marquee/internal/importer"
	"github.com/davepintodev/marquee/internal/naming"
	"github.com/davepintodev/marquee/internal/subfit"
	"github.com/davepintodev/marquee/internal/torbox"
)

// ReasonLocalError is a failure on marquee's side (a full disk, a file
// already in the library); the release itself is not at fault, so it is not
// blocklisted.
const ReasonLocalError = string(download.ReasonLocalError)

// ReasonDuplicateEpisode fails a file that is the same as another episode's
// in the series: the release is blocklisted, as it is not the episode asked for.
const ReasonDuplicateEpisode = "duplicate_episode"

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
	// HoldDispatch, when set, is called by a dispatch pass that found
	// downloads, after it read the Pause all flag and before it starts any,
	// so a test can land a pause in that window.
	HoldDispatch func()
}

// PackExpander resolves a season pack whose torrent TorBox has just finished
// caching for download d: it records the file of the pack d takes, then
// queues the pack's other wanted episodes.
type PackExpander interface {
	ExpandPack(ctx context.Context, d catalog.Download, torrentID int, files []torbox.File) (PackExpansion, error)
}

type PackExpansion struct {
	// Own is d as it now stands, naming its file of the pack; Holds is false
	// when the pack has no usable file of d's episode, and Own is then unset.
	// Holds and Own stay set when queueing the other episodes then fails.
	Own    catalog.Download
	Holds  bool
	Queued int
}

type TorrentDeleter interface {
	DeleteTorrent(ctx context.Context, torrentID int) error
}

// Options are the manager's dependencies.
type Options struct {
	Catalog *catalog.Store
	// Downloader replaces the HTTP downloader built from Tuning.Download.
	Downloader download.Downloader
	// JellyfinURL and JellyfinKey are optional; without a URL nothing is refreshed.
	JellyfinURL string
	JellyfinKey config.Secret
	// Subfit is optional: when set, an import or replace is told to subfit
	// once it is final.
	Subfit *subfit.Client
	// MoviesPath, SeriesPath and TempPath come from the configuration; the
	// temp path must be on the libraries' filesystem.
	MoviesPath, SeriesPath, TempPath string
	Log                              *slog.Logger
	Tuning                           Tuning
	// TorBoxMaxActive is how many uncached downloads TorBox may be caching
	// at once; zero means TorBox is not set up and no download is uncached.
	TorBoxMaxActive int
	// TorBoxCleanup, when set, deletes a torrent from the TorBox account
	// once its file has been imported and no other download uses it.
	TorBoxCleanup TorrentDeleter
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
	// Replace has the download take the place of the file the movie or
	// episode already has, once it has been verified.
	Replace bool
	// Covered are the episode numbers of Season that the release's one
	// file holds, such as 1 and 2 for S01E01E02. Episodes the series does
	// not list are left out, and Number is always counted in.
	Covered []int
	// Uncached marks a release the debrid service has not cached, which
	// TorBox must fetch first; URL may then be empty.
	Uncached bool
	// TorBoxTorrentID and TorBoxFileID are the season pack torrent the
	// scheduler already created on TorBox and the file of it to take. A
	// download of a pack whose files are not known yet has no Release
	// FileName and no file id; once TorBox holds the pack it queues the
	// pack's other wanted episodes.
	TorBoxTorrentID, TorBoxFileID int
}

// Manager runs the downloads.
type Manager struct {
	cat     *catalog.Store
	dl      download.Downloader
	jf      *importer.Jellyfin
	sf      *subfit.Client
	movies  string
	series  string
	tempDir string
	log     *slog.Logger
	tune    Tuning
	wake    chan struct{}
	mu      sync.Mutex
	running map[string]bool
	// caching holds the running downloads that are waiting for TorBox rather
	// than moving bytes; they do not take one of the Concurrency places.
	caching map[string]bool
	// runs are the running downloads' handles, by id, under mu.
	runs     map[string]*run
	idSource func() string
	// meters are the rates of the downloads moving bytes, by id, under mu.
	meters map[string]*meter

	torboxMax int
	cleanup   TorrentDeleter

	// leaving holds the downloads that are giving up on their TorBox torrent,
	// so that two downloads of one torrent failing together do not each
	// find the other still using it and both leave it on the account.
	leaveMu sync.Mutex
	leaving map[string]bool
	// deleted remembers torrents already removed from TorBox, so two
	// siblings finishing together do not each delete it.
	deleted map[int]bool

	claimMu sync.Mutex
	claims  map[string]claim

	packMu sync.RWMutex
	packs  PackExpander
}

// SetPacks gives the manager what queues a season pack's episodes; the
// scheduler, which judges releases, is made after the manager.
func (m *Manager) SetPacks(p PackExpander) {
	m.packMu.Lock()
	m.packs = p
	m.packMu.Unlock()
}

// packLead reports whether the download fetches a season pack whose files
// were not known when it was queued, and so must queue the pack's other episodes.
func packLead(d catalog.Download) bool {
	return d.Release.FileName == "" && d.TorBoxTorrentID != 0 && d.Number > 0
}

// onPresent fails a pack lead whose pack holds none of the missing episodes
// or lacks its own, which blocklists the whole pack: its file name is
// still empty, so its release is the pack.
func (m *Manager) onPresent(ctx context.Context, d *catalog.Download) func(int, []torbox.File) (string, int, error) {
	if !packLead(*d) {
		return nil
	}
	return func(torrentID int, files []torbox.File) (string, int, error) {
		m.packMu.RLock()
		p := m.packs
		m.packMu.RUnlock()
		if p == nil {
			return "", 0, nil
		}
		x, err := p.ExpandPack(ctx, *d, torrentID, files)
		if err != nil {
			if ctx.Err() != nil {
				return "", 0, ctx.Err()
			}
			m.log.Warn("cannot queue a season pack's episodes", "download", d.ID, "err", err)
			if !x.Holds {
				return "", 0, &download.Failure{Reason: download.ReasonLocalError, Detail: "the season pack's episodes could not be queued"}
			}
		}
		if x.Queued > 0 {
			m.log.Info("queued a season pack's episodes", "download", d.ID, "episodes", x.Queued)
		}
		switch {
		case !x.Holds && x.Queued == 0:
			return "", 0, &download.Failure{Reason: download.ReasonNotVideo, Detail: "the season pack holds none of the missing episodes"}
		case !x.Holds:
			return "", 0, &download.Failure{Reason: download.ReasonNotVideo, Detail: "the season pack has no usable file of this episode"}
		}
		d.Release, d.Covered, d.TorBoxFileID = x.Own.Release, x.Own.Covered, x.Own.TorBoxFileID
		return d.Release.FileName, d.TorBoxFileID, nil
	}
}

type run struct {
	cancel  context.CancelFunc
	done    chan struct{}
	discard bool
}

// claim is a verified download's place in its series until it has been
// imported or dropped.
type claim struct {
	titleID, fingerprint string
	season               int
	numbers              []int
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
		cat: o.Catalog, dl: dl, sf: o.Subfit, movies: o.MoviesPath, series: o.SeriesPath,
		tempDir: filepath.Join(o.TempPath, "marquee"), log: log, tune: t,
		wake: make(chan struct{}, 1), running: map[string]bool{}, caching: map[string]bool{}, runs: map[string]*run{}, meters: map[string]*meter{},
		claims: map[string]claim{}, leaving: map[string]bool{}, deleted: map[int]bool{}, idSource: newID, torboxMax: o.TorBoxMaxActive, cleanup: o.TorBoxCleanup,
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
	var covered []int
	if t.Kind == "series" {
		covered = ListedEpisodes(t, j.Season, j.Number, j.Covered)
	}
	d, err := m.cat.AddDownload(ctx, catalog.NewDownload{
		ID: m.idSource(), TitleID: j.TitleID, Season: j.Season, Number: j.Number, Release: j.Release,
		ReleaseName: j.ReleaseName, Resolution: j.Resolution, Quality: j.Quality, URL: j.URL, ReplaceExisting: j.Replace,
		Covered: covered, Uncached: j.Uncached, PackTorrentID: j.TorBoxTorrentID, PackFileID: j.TorBoxFileID,
	})
	if errors.Is(err, catalog.ErrActiveDownload) {
		// The busy episode may be one the file covers rather than the one asked for.
		for _, n := range append([]int{j.Number}, covered...) {
			if existing, gerr := m.cat.ActiveDownloadFor(ctx, j.TitleID, j.Season, n); gerr == nil {
				return existing, catalog.ErrActiveDownload
			}
		}
		return catalog.Download{}, err
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
	all, err := m.cat.PausedAll(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.log.Error("cannot read whether Pause all is on", "err", err)
		}
		return
	}
	if len(active) > 0 && m.tune.HoldDispatch != nil {
		m.tune.HoldDispatch()
	}
	slots := m.cachingSlotsUsed(active)
	for _, d := range active {
		if d.Paused || (all && transferable(d)) {
			continue
		}
		waiting := d.Uncached && (d.State == catalog.StateQueued || d.State == catalog.StateCaching)
		m.mu.Lock()
		skip := m.running[d.ID]
		switch {
		case skip:
		case waiting && d.State == catalog.StateQueued && m.torboxMax > 0 && slots >= m.torboxMax:
			skip = true
		case !waiting && len(m.running)-len(m.caching) >= m.tune.Concurrency:
			skip = true
		}
		var r *run
		dctx := ctx
		if !skip {
			m.running[d.ID] = true
			if waiting {
				m.caching[d.ID] = true
				if d.State == catalog.StateQueued {
					slots++
				}
			}
			var cancel context.CancelFunc
			dctx, cancel = context.WithCancel(ctx)
			r = &run{cancel: cancel, done: make(chan struct{})}
			m.runs[d.ID] = r
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
				delete(m.caching, d.ID)
				delete(m.runs, d.ID)
				delete(m.meters, d.ID)
				discard := r.discard
				m.mu.Unlock()
				m.leaveMu.Lock()
				delete(m.leaving, d.ID)
				m.leaveMu.Unlock()
				m.claimMu.Lock()
				delete(m.claims, d.ID)
				m.claimMu.Unlock()
				if discard {
					m.discard(ctx, d.ID)
				}
				r.cancel()
				close(r.done)
				m.kick()
			}()
			// The handle is registered before these reads, so a pause, a
			// Pause all or a cancel that lands after the queue was read is
			// either seen here or cancels dctx.
			fresh, err := m.cat.GetDownload(dctx, d.ID)
			if err != nil || fresh.Paused || fresh.State == catalog.StateDone || fresh.State == catalog.StateFailed {
				return
			}
			if now, err := m.cat.PausedAll(dctx); err != nil || (now && transferable(fresh)) {
				return
			}
			m.process(dctx, fresh)
		}()
	}
}

// transferable reports a download Pause all holds back: one that has not
// got all its bytes yet. Verifying and importing spend no bandwidth, so they
// finish.
func transferable(d catalog.Download) bool {
	switch d.State {
	case catalog.StateQueued, catalog.StateCaching, catalog.StateDownloading:
		return true
	}
	return false
}

// cachingSlotsUsed counts the uncached downloads that hold one of TorBox's
// places: those it is caching, and those started and not yet recorded as such.
func (m *Manager) cachingSlotsUsed(active []catalog.Download) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range active {
		if d.Paused {
			continue
		}
		if d.Uncached && (d.State == catalog.StateCaching || (d.State == catalog.StateQueued && m.running[d.ID])) {
			n++
		}
	}
	return n
}

// Pause stops a download and keeps what it fetched, and returns once its
// transfer has stopped or ctx ends. See catalog.PauseDownload for the errors.
func (m *Manager) Pause(ctx context.Context, id string) (catalog.Download, error) {
	d, err := m.cat.PauseDownload(ctx, id)
	if err != nil {
		return catalog.Download{}, err
	}
	m.halt(ctx, id, false)
	m.kick()
	return d, nil
}

// Resume lets a paused download continue from what it fetched.
func (m *Manager) Resume(ctx context.Context, id string) (catalog.Download, error) {
	d, err := m.cat.ResumeDownload(ctx, id)
	if err != nil {
		return catalog.Download{}, err
	}
	m.kick()
	return d, nil
}

// PauseAll holds every download back until ResumeAll: transfers stop with
// their partial files kept, and nothing queued starts. It returns once the
// transfers have stopped or ctx ends. A download's own pause is untouched,
// and so is a torrent TorBox is caching, which carries on on TorBox's side
// but is watched again only after ResumeAll.
func (m *Manager) PauseAll(ctx context.Context) error {
	if _, err := m.cat.SetPausedAll(ctx, true); err != nil {
		return err
	}
	active, err := m.cat.ActiveDownloads(ctx)
	if err != nil {
		return err
	}
	for _, d := range active {
		if transferable(d) {
			m.halt(ctx, d.ID, false)
		}
	}
	m.kick()
	return nil
}

// ResumeAll lets the downloads that were not paused on their own run again,
// from their partial files and in their queue order.
func (m *Manager) ResumeAll(ctx context.Context) error {
	if _, err := m.cat.SetPausedAll(ctx, false); err != nil {
		return err
	}
	m.kick()
	return nil
}

// Speed is the bytes a second a download is moving, averaged over the last
// SpeedWindow of its progress; zero when it is not moving bytes.
func (m *Manager) Speed(id string) int64 {
	m.mu.Lock()
	mt := m.meters[id]
	m.mu.Unlock()
	if mt == nil {
		return 0
	}
	return mt.rate(time.Now())
}

const cancelMessage = "Cancelled; this release will not be tried again."

// Cancel stops a download, deletes what it fetched and its TorBox torrent
// unless another download needs it, and blocklists the release, so that the
// next search finds another. It returns once that is done or ctx ends.
func (m *Manager) Cancel(ctx context.Context, id string) error {
	if _, err := m.cat.CancelDownload(ctx, id, cancelMessage); err != nil {
		return err
	}
	if !m.halt(ctx, id, true) {
		m.discard(ctx, id)
	}
	m.kick()
	return nil
}

func (m *Manager) meterOf(id string) *meter {
	m.mu.Lock()
	defer m.mu.Unlock()
	mt := m.meters[id]
	if mt == nil {
		mt = &meter{}
		m.meters[id] = mt
	}
	return mt
}

func (m *Manager) halt(ctx context.Context, id string, discard bool) bool {
	m.mu.Lock()
	r, ok := m.runs[id]
	if ok {
		r.discard = r.discard || discard
		r.cancel()
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case <-r.done:
	case <-ctx.Done():
	}
	return true
}

func (m *Manager) discard(ctx context.Context, id string) {
	m.removeTemp(id)
	ctx = context.WithoutCancel(ctx)
	d, err := m.cat.GetDownload(ctx, id)
	if err != nil {
		if !errors.Is(err, catalog.ErrNotFound) {
			m.log.Warn("cannot read a cancelled download", "download", id, "err", err)
		}
		return
	}
	m.deleteTorrent(ctx, d)
}

// PauseTitle pauses every download of a title that can still be paused
// and returns them. A title that is not in the library answers
// catalog.ErrNotFound.
func (m *Manager) PauseTitle(ctx context.Context, titleID string) ([]catalog.Download, error) {
	return m.eachOf(ctx, titleID, func(catalog.Download) bool { return true }, m.Pause)
}

// ResumeTitle resumes every paused download of a title and returns them.
func (m *Manager) ResumeTitle(ctx context.Context, titleID string) ([]catalog.Download, error) {
	return m.eachOf(ctx, titleID, func(d catalog.Download) bool { return d.Paused }, m.Resume)
}

// CancelTitle cancels every download of a title that can still be cancelled.
func (m *Manager) CancelTitle(ctx context.Context, titleID string) error {
	_, err := m.eachOf(ctx, titleID, func(catalog.Download) bool { return true }, func(ctx context.Context, id string) (catalog.Download, error) {
		return catalog.Download{}, m.Cancel(ctx, id)
	})
	return err
}

func (m *Manager) eachOf(ctx context.Context, titleID string, pick func(catalog.Download) bool, change func(context.Context, string) (catalog.Download, error)) ([]catalog.Download, error) {
	if _, err := m.cat.Get(ctx, titleID); err != nil {
		return nil, err
	}
	active, err := m.cat.ActiveDownloads(ctx)
	if err != nil {
		return nil, err
	}
	out := []catalog.Download{}
	for _, d := range active {
		if d.TitleID != titleID || !pick(d) {
			continue
		}
		got, err := change(ctx, d.ID)
		switch {
		case errors.Is(err, catalog.ErrNotFound), errors.Is(err, catalog.ErrFinished), errors.Is(err, catalog.ErrSettling):
		case err != nil:
			return out, err
		default:
			out = append(out, got)
		}
	}
	return out, nil
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
	string(download.ReasonStalled):        "Stalled",
	string(download.ReasonCachingTimeout): "Caching timed out",
	string(download.ReasonNotVideo):       "Not a video",
	string(download.ReasonSizeMismatch):   "Wrong size",
	string(download.ReasonSourceDown):     "Source down",
	"too_small":                           "Too small",
	ReasonLocalError:                      "Could not save",
	ReasonDuplicateEpisode:                "Duplicate episode",
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
	if d.Uncached && m.torboxMax == 0 {
		m.fail(ctx, d, ReasonLocalError, "TorBox is not set up, so a release that is not cached cannot be fetched", false)
		return
	}
	state := d.State
	if state == catalog.StateQueued || state == catalog.StateCaching || state == catalog.StateDownloading {
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
	// An uncached download is caching until TorBox holds the file; its state
	// moves to downloading when the first bytes are reported.
	transferring := !d.Uncached
	if transferring {
		if err := m.cat.SetDownloadState(ctx, d.ID, catalog.StateDownloading); err != nil {
			m.gone(ctx, *d, err)
			return false
		}
	}
	fctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var removed bool
	var mu sync.Mutex
	gone := func() {
		mu.Lock()
		removed = true
		mu.Unlock()
		cancel()
	}
	res, err := m.dl.Download(fctx, download.Request{
		URL: d.URL, Dest: m.partPath(d.ID), ReleaseSize: d.Release.Size,
		Cached: !d.Uncached, InfoHash: d.Release.InfoHash, FileName: d.Release.FileName,
		Season: d.Season, Number: d.Number, TorBoxTorrentID: d.TorBoxTorrentID, TorBoxFileID: d.TorBoxFileID,
		StartedAt: d.CachingStartedAt, Keep: m.keepTorrent(d.ID), OnPresent: m.onPresent(fctx, d),
		OnCaching: func(percent, torrentID, fileID int) error {
			err := m.cat.SetDownloadCaching(fctx, d.ID, percent, torrentID, fileID)
			if errors.Is(err, catalog.ErrNotFound) {
				gone()
				return download.ErrRemoved
			}
			if err == nil {
				// The import deletes this torrent once it is done.
				d.TorBoxTorrentID = torrentID
			}
			return err
		},
		Progress: func(done, total int64) {
			if transferring {
				m.meterOf(d.ID).add(time.Now(), done)
			}
			if !transferring {
				if err := m.cat.SetDownloadState(fctx, d.ID, catalog.StateDownloading); err == nil {
					transferring = true
					m.mu.Lock()
					delete(m.caching, d.ID)
					m.mu.Unlock()
				}
			}
			if err := m.cat.SetDownloadProgress(fctx, d.ID, done, total); errors.Is(err, catalog.ErrNotFound) {
				gone()
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
			m.fail(ctx, *d, string(f.Reason), f.Detail, f.Reason != download.ReasonLocalError)
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

// gone handles a download whose row vanished (its title was removed), was
// paused, or a database error. A paused download keeps what it fetched.
func (m *Manager) gone(ctx context.Context, d catalog.Download, err error) {
	if errors.Is(err, catalog.ErrNotFound) {
		m.removeTemp(d.ID)
		return
	}
	if errors.Is(err, catalog.ErrPaused) {
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
	case m.isDuplicate(ctx, d, t, part):
	default:
		if err := m.cat.SetDownloadState(ctx, d.ID, catalog.StateImporting); err != nil {
			m.gone(ctx, d, err)
			return false
		}
		return true
	}
	return false
}

// isDuplicate fails the download and reports true when an episode's file is
// byte-for-byte what another episode of the series already has, which is
// how a pack's mislabelled file looks. A fingerprint that cannot be read
// fails it on marquee's side. The other episode may be one still being
// imported, so the check and the claim on the fingerprint are made under one
// lock, and the claim is only dropped after the import has been recorded
// (see dispatch). A shutdown leaves the download untouched to resume.
func (m *Manager) isDuplicate(ctx context.Context, d catalog.Download, t catalog.Title, part string) bool {
	if t.Kind == "movie" {
		return false
	}
	fp, err := importer.Fingerprint(part)
	if err != nil {
		if ctx.Err() != nil {
			return true
		}
		m.fail(ctx, d, ReasonLocalError, "the downloaded file could not be read", false)
		return true
	}
	own := episodesOf(d)
	m.claimMu.Lock()
	season, number, found, err := m.cat.DuplicateEpisode(ctx, d.TitleID, fp, d.Season, own)
	if err == nil && !found {
		for id, c := range m.claims {
			if id != d.ID && c.titleID == d.TitleID && c.fingerprint == fp && (c.season != d.Season || !slices.Contains(own, c.numbers[0])) {
				season, number, found = c.season, c.numbers[0], true
				break
			}
		}
	}
	if err == nil && !found {
		m.claims[d.ID] = claim{titleID: d.TitleID, fingerprint: fp, season: d.Season, numbers: own}
	}
	m.claimMu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			return true
		}
		m.log.Error("cannot look for a duplicate episode", "download", d.ID, "err", err)
		m.fail(ctx, d, ReasonLocalError, "the library could not be checked for duplicates", false)
		return true
	}
	if !found {
		return false
	}
	m.fail(ctx, d, ReasonDuplicateEpisode,
		fmt.Sprintf("this file is identical to S%02dE%02d, which is already in the library", season, number), true)
	return true
}

func (m *Manager) place(ctx context.Context, d catalog.Download) {
	t, err := m.cat.Get(ctx, d.TitleID)
	if err != nil {
		m.gone(ctx, d, err)
		return
	}
	dst := m.libraryPath(t, d)
	root, rel := m.rootOf(t), m.relPath(t, dst)
	old, err := m.replaced(ctx, d)
	if err != nil {
		if ctx.Err() == nil {
			m.log.Error("cannot look up the file to replace", "download", d.ID, "err", err)
			m.fail(ctx, d, ReasonLocalError, "the library could not be read", false)
		}
		return
	}
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
	} else if err := m.move(part, dst, old == rel); err != nil {
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
	// The old file goes before the download is recorded as done, so a crash
	// in between leaves the download importing and the next run finishes the job.
	m.removeReplaced(ctx, d, root, old, rel)
	if err := m.cat.FinishDownload(ctx, d.ID, m.recordFile(t, d, dst)); err != nil {
		m.gone(ctx, d, err)
		return
	}
	m.log.Info("imported", "download", d.ID, "title", d.TitleID)
	if old != "" {
		m.sf.Replace(subfit.Library(t.Kind), rel, old)
	} else {
		m.sf.Import(subfit.Library(t.Kind), rel)
	}
	m.deleteTorrent(ctx, d)
	m.refreshJellyfin(ctx)
}

// deleteTorrent removes the torrent TorBox fetched for an imported download
// from the account, unless another unfinished download still reads from it.
// The file is in the library already, so a failure is only logged.
func (m *Manager) deleteTorrent(ctx context.Context, d catalog.Download) {
	if m.cleanup == nil || d.TorBoxTorrentID == 0 {
		return
	}
	// One lock for the check and the delete: a sibling that gives up marks
	// itself leaving before it fails, so it no longer counts as a user, and
	// two siblings finishing together delete the torrent once.
	m.leaveMu.Lock()
	defer m.leaveMu.Unlock()
	if m.deleted[d.TorBoxTorrentID] {
		return
	}
	users, err := m.cat.TorrentUsers(ctx, d.TorBoxTorrentID, d.ID)
	if err != nil {
		if ctx.Err() == nil {
			m.log.Warn("cannot tell whether a TorBox torrent is still in use", "download", d.ID, "err", err)
		}
		return
	}
	for _, u := range users {
		if !m.leaving[u] {
			return
		}
	}
	m.deleted[d.TorBoxTorrentID] = true
	if err := m.cleanup.DeleteTorrent(context.WithoutCancel(ctx), d.TorBoxTorrentID); err != nil && !errors.Is(err, torbox.ErrNotFound) && ctx.Err() == nil {
		m.log.Warn("cannot delete an imported torrent from TorBox", "download", d.ID, "err", err)
	}
}

// keepTorrent answers whether a download about to delete its torrent from
// TorBox must leave it: another unfinished download still reads from it and
// is not itself giving up. Not knowing counts as in use.
func (m *Manager) keepTorrent(id string) func(context.Context, int) bool {
	return func(ctx context.Context, torrentID int) bool {
		m.leaveMu.Lock()
		defer m.leaveMu.Unlock()
		m.leaving[id] = true
		users, err := m.cat.TorrentUsers(ctx, torrentID, id)
		if err != nil {
			m.log.Warn("cannot tell whether a TorBox torrent is still in use", "download", id, "err", err)
			return true
		}
		for _, u := range users {
			if !m.leaving[u] {
				return true
			}
		}
		return false
	}
}

// move puts the verified file at dst. Only a replacement of the very file at
// dst may overwrite it, by an atomic rename; anything else already there
// stays and fails the import.
func (m *Manager) move(part, dst string, overwrite bool) error {
	if overwrite {
		if fi, err := os.Lstat(dst); err == nil && fi.Mode().IsRegular() {
			return os.Rename(part, dst)
		}
	}
	return importer.Place(part, dst)
}

// replaced is the library-relative path of the file a replacing download
// takes the place of, empty when it replaces none.
func (m *Manager) replaced(ctx context.Context, d catalog.Download) (string, error) {
	if !d.ReplaceExisting {
		return "", nil
	}
	f, err := m.cat.FileOf(ctx, d.TitleID, d.Season, d.Number)
	if err != nil {
		return "", err
	}
	if !f.Have {
		return "", nil
	}
	return f.Path, nil
}

// removeReplaced deletes what the new file replaced: the old video and its
// subtitle sidecars, or, when the new file took the old one's name, only the
// sidecars. A file another episode still records stays. A failure leaves an
// orphan file behind, which is better than failing an import that has
// already happened.
func (m *Manager) removeReplaced(ctx context.Context, d catalog.Download, root, old, now string) {
	if old == "" {
		return
	}
	var err error
	if old == now {
		err = importer.DeleteSidecars(root, old)
	} else if shared, serr := m.cat.SharesFile(ctx, d.TitleID, old, d.Season, episodesOf(d)); serr != nil || shared {
		err = serr
	} else if err = importer.DeleteMediaFile(root, old); err == nil {
		err = importer.PruneEmptyDirs(root, old)
	}
	if err != nil && ctx.Err() == nil {
		m.log.Warn("cannot delete the file a download replaced", "download", d.ID, "err", err)
	}
}

// recordFile describes the placed file for the catalog. A fingerprint that
// cannot be read is left empty rather than failing an import that already
// happened.
func (m *Manager) recordFile(t catalog.Title, d catalog.Download, dst string) catalog.File {
	f := catalog.File{Path: m.relPath(t, dst), Size: d.BytesTotal}
	if fi, err := os.Stat(dst); err == nil {
		f.Size = fi.Size()
	}
	if fp, err := importer.Fingerprint(dst); err == nil {
		f.Fingerprint = fp
	} else {
		m.log.Warn("cannot fingerprint an imported file", "download", d.ID, "err", err)
	}
	return f
}

func (m *Manager) rootOf(t catalog.Title) string {
	if t.Kind == "movie" {
		return m.movies
	}
	return m.series
}

func (m *Manager) relPath(t catalog.Title, dst string) string {
	rel, err := filepath.Rel(m.rootOf(t), dst)
	if err != nil {
		rel = dst
	}
	return filepath.ToSlash(rel)
}

// RefreshLibrary asks Jellyfin to scan, as an import does, for a change
// made to the library by something else, such as deleting files.
func (m *Manager) RefreshLibrary(ctx context.Context) { m.refreshJellyfin(ctx) }

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
	q := naming.QualityName(naming.KindSeries, d.Resolution, d.Quality)
	numbers := episodesOf(d)
	names := make([]string, len(numbers))
	for i, n := range numbers {
		names[i], _ = episodeName(t, d.Season, n)
	}
	return filepath.Join(m.series, naming.SeriesFolder(t.Title), naming.SeasonFolder(d.Season),
		naming.MultiEpisodeFile(t.Title, d.Season, numbers, names, q, ext))
}

// episodesOf are the episode numbers the download's file holds: the ones
// it covers, or the one it targets.
func episodesOf(d catalog.Download) []int {
	if len(d.Covered) > 0 {
		return d.Covered
	}
	return []int{d.Number}
}

// ListedEpisodes is what a release's file covers among the episodes the
// series lists for the season, with the target counted in; nothing when
// that is the target alone.
func ListedEpisodes(t catalog.Title, season, number int, wanted []int) []int {
	covered := []int{number}
	for _, n := range wanted {
		if _, ok := episodeName(t, season, n); ok && !slices.Contains(covered, n) {
			covered = append(covered, n)
		}
	}
	if len(covered) < 2 {
		return nil
	}
	slices.Sort(covered)
	return covered
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
