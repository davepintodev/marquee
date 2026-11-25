// Package scheduler decides what marquee searches for and when: it looks
// for every monitored title or aired episode that has no file, backs off
// when a search finds nothing, hands what it finds to the download queue,
// and keeps each series' episode list fresh. State lives in the catalog, so
// a restart carries on where it stopped.
//
// In dry-run it searches and chooses but only records what it would grab:
// nothing is queued, so nothing downstream needs to know.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/metadata"
	"github.com/davepintodev/marquee/internal/pipeline"
	"github.com/davepintodev/marquee/internal/torbox"
)

const (
	// SearchGap is the least time between two searches of AIOStreams, each
	// of which fans out to its addons and the debrid.
	SearchGap = 5 * time.Second
	// DailyBudget is how many scheduled searches one movie or episode gets a
	// day; Search now and the search after a failed download are outside it.
	DailyBudget = 4
	// RefreshRetry is how long a failed TMDB refresh waits to be tried again.
	RefreshRetry = time.Hour
)

// A search that finds nothing waits the step for the number of such searches
// in a row, and the last step, a day, for ever after.
var backoff = []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

// The source failing is its own backoff, independent of any title's.
var sourceBackoff = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

func step(steps []time.Duration, n int) time.Duration { return steps[min(n, len(steps)-1)] }

// Source is the part of the AIOStreams client the scheduler uses.
type Source interface {
	Search(ctx context.Context, typ, id string) ([]aiostreams.Stream, error)
}

// TMDB is the part of the metadata client the scheduler uses.
type TMDB interface {
	Details(ctx context.Context, kind string, id int) (metadata.Details, error)
}

// TorBox is the part of the TorBox client the scheduler resolves season
// packs with.
type TorBox interface {
	CreateTorrent(ctx context.Context, infoHash string) (torbox.Torrent, error)
	GetTorrent(ctx context.Context, torrentID int) (torbox.Torrent, error)
	DeleteTorrent(ctx context.Context, torrentID int) error
}

type Transfers interface {
	Enqueue(ctx context.Context, j pipeline.Job) (catalog.Download, error)
}

// Tuning holds what tests change; the zero value is production.
type Tuning struct {
	// Tick is how often the loop looks for work; default 1 s. A negative
	// Tick runs no loop, and the caller drives the scheduler with Step.
	Tick time.Duration
}

// Options are the scheduler's dependencies.
type Options struct {
	Catalog   *catalog.Store
	Source    Source
	TMDB      TMDB
	Transfers Transfers
	// DryRun makes the scheduler record "would grab" instead of queueing.
	DryRun bool
	// Profiles are the quality profiles a title is judged by; the zero value
	// has only the built-in default.
	Profiles config.ProfileSet
	// TorBoxMaxActive is how many uncached downloads TorBox may be caching
	// at once. Zero means TorBox is not set up, and only cached releases count.
	TorBoxMaxActive int
	// TorBox resolves season packs into their episodes; nil leaves packs out.
	TorBox TorBox
	Log    *slog.Logger
	Tuning          Tuning
}

// Scheduler searches for what is missing. Every time it reads is the
// catalog's clock, which tests control.
type Scheduler struct {
	cat      *catalog.Store
	src      Source
	tmdb     TMDB
	tr       Transfers
	dry      bool
	profiles config.ProfileSet
	log      *slog.Logger
	tick     time.Duration
	wake     chan struct{}
	torbox   int
	tb       TorBox

	mu         sync.Mutex
	lastSearch time.Time
	// deferred are the targets whose search found only an uncached release
	// while TorBox had no place for it. They wait, unsearched, until one opens.
	deferred map[slot]bool
}

type slot struct {
	titleID        string
	season, number int
}

func New(o Options) *Scheduler {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	tick := o.Tuning.Tick
	if tick == 0 {
		tick = time.Second
	}
	return &Scheduler{
		cat: o.Catalog, src: o.Source, tmdb: o.TMDB, tr: o.Transfers, dry: o.DryRun, profiles: o.Profiles, log: log,
		tick: tick, wake: make(chan struct{}, 1), torbox: o.TorBoxMaxActive, tb: o.TorBox, deferred: map[slot]bool{},
	}
}

// Wake makes the loop look for work now, as Search now does.
func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run works until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	if s.tick < 0 {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		s.Step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-t.C:
		}
	}
}

// Step does one pass: it refreshes at most one series' episode list and
// makes at most one search, so a slow TMDB or AIOStreams delays the loop by
// one call and never by a queue of them.
func (s *Scheduler) Step(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshOne(ctx)
	s.searchOne(ctx)
}

func (s *Scheduler) refreshOne(ctx context.Context) {
	now := s.cat.Now()
	due, err := s.cat.RefreshDue(ctx, now)
	if err != nil {
		s.logErr(ctx, "cannot look for series to refresh", err)
		return
	}
	if len(due) == 0 {
		return
	}
	r := due[0]
	d, err := s.tmdb.Details(ctx, "series", r.TMDBID)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		s.log.Warn("cannot refresh a series' episodes", "title", r.TitleID, "err", err)
		s.ignoreGone(s.cat.DeferRefresh(ctx, r.TitleID, now.Add(RefreshRetry)))
		return
	}
	ref := catalog.Refresh{IMDbID: d.IMDbID, TVDBID: d.TVDBID, DueAt: now.Add(catalog.RefreshEvery)}
	for _, e := range d.DatedEpisodes() {
		ref.Episodes = append(ref.Episodes, catalog.NewEpisode(e))
	}
	if err := s.cat.ApplyRefresh(ctx, r.TitleID, ref); err != nil {
		s.ignoreGone(err)
	}
}

func (s *Scheduler) searchOne(ctx context.Context) {
	now := s.cat.Now()
	if ctx.Err() != nil || (!s.lastSearch.IsZero() && now.Before(s.lastSearch.Add(SearchGap))) {
		return
	}
	failures, until, err := s.cat.SourceBackoff(ctx)
	if err != nil {
		s.logErr(ctx, "cannot read the source backoff", err)
		return
	}
	if now.Before(until) {
		return
	}
	targets, err := s.cat.Targets(ctx, now)
	if err != nil {
		s.logErr(ctx, "cannot look for what to search", err)
		return
	}
	day := now.UTC().Format(time.DateOnly)
	used := map[slot]int{}
	waiting, err := s.torboxFull(ctx)
	if err != nil {
		s.logErr(ctx, "cannot count the torrents TorBox is caching", err)
		return
	}
	for _, t := range targets {
		if waiting && s.deferred[slot{t.TitleID, t.Season, t.Number}] {
			continue
		}
		if t.FollowUp && !t.Forced && s.failedLocally(ctx, t) {
			// The same release would be chosen again: wait like any other title.
			s.backOff(ctx, now, t, "")
			continue
		}
		if !t.Forced && !t.FollowUp {
			k := slot{t.TitleID, t.Season, t.Number}
			n, ok := used[k]
			if !ok {
				if n, err = s.cat.SearchesToday(ctx, t.TitleID, t.Season, t.Number, day); err != nil {
					s.logErr(ctx, "cannot read a search budget", err)
					return
				}
				used[k] = n
			}
			if n >= DailyBudget {
				continue
			}
		}
		s.search(ctx, now, t, failures)
		return
	}
}

// torboxFull reports whether TorBox has no place for another uncached
// download. When it has one again, every deferred target is searched afresh.
func (s *Scheduler) torboxFull(ctx context.Context) (bool, error) {
	if s.torbox == 0 || len(s.deferred) == 0 {
		return false, nil
	}
	n, err := s.cat.UncachedActive(ctx)
	if err != nil {
		return false, err
	}
	if n < s.torbox {
		clear(s.deferred)
		return false, nil
	}
	return true, nil
}

// failedLocally reports whether the target's last download failed on
// marquee's side, which does not blocklist the release.
func (s *Scheduler) failedLocally(ctx context.Context, t catalog.Target) bool {
	ds, err := s.cat.FinishedDownloads(ctx, t.TitleID)
	if err != nil {
		s.logErr(ctx, "cannot read a title's downloads", err)
		return false
	}
	for _, d := range ds {
		if d.Season == t.Season && d.Number == t.Number {
			return d.Reason == pipeline.ReasonLocalError
		}
	}
	return false
}

func (s *Scheduler) search(ctx context.Context, now time.Time, t catalog.Target, failures int) {
	id, ok := sourceID(t)
	if !ok {
		s.log.Warn("a series has neither a TVDB nor an IMDb id", "title", t.TitleID)
		s.backOff(ctx, now, t, "Cannot search: TMDB lists neither a TVDB nor an IMDb id for this series.")
		return
	}
	s.lastSearch = now
	streams, err := s.src.Search(ctx, t.Kind, id)
	if err != nil {
		if ctx.Err() == nil {
			s.sourceFailed(ctx, now, t, failures, err)
		}
		return
	}
	if failures > 0 {
		s.logErr(ctx, "cannot record that the source answers", s.cat.SetSourceBackoff(ctx, 0, time.Time{}))
	}
	if !t.Forced && !t.FollowUp {
		s.ignoreGone(s.cat.CountSearch(ctx, t.TitleID, t.Season, t.Number, now.UTC().Format(time.DateOnly)))
	}
	blocked, err := s.cat.Blocklist(ctx)
	if err != nil {
		s.logErr(ctx, "cannot read the blocklist", err)
		return
	}
	profile, name, fellBack := s.profiles.Resolve(t.Kind, t.Profile)
	if fellBack {
		s.log.Warn("a title names a profile the config does not define; using the default", "title", t.TitleID, "profile", t.Profile, "default", name)
	}
	var owned []int
	if t.Kind == "series" {
		if owned, err = s.cat.OwnedEpisodes(ctx, t.TitleID, t.Season); err != nil {
			s.logErr(ctx, "cannot read the episodes on disk", err)
			return
		}
	}
	cc := chooser.Context{
		Kind: t.Kind, Season: t.Season, Episode: t.Number, OwnedEpisodes: owned, Profile: profile,
		AllowUncached: s.torbox > 0, Blocklist: blocked, Now: now,
	}
	res := chooser.Choose(streams, cc)
	if !s.stillWanted(ctx, now, t) {
		return
	}
	var pick *chooser.Candidate
	for i, c := range res.Candidates {
		// An uncached release has no link until TorBox holds it.
		if c.Stream.URL.Reveal() != "" || !c.Stream.Cached {
			pick = &res.Candidates[i]
			break
		}
	}
	if s.tryPack(ctx, now, t, streams, cc, pick) {
		return
	}
	switch {
	case pick == nil:
		s.backOff(ctx, now, t, nothingUsable(len(streams), res.Dropped))
	case s.dry:
		s.wouldGrab(ctx, now, t, *pick)
	case !pick.Stream.Cached && s.noRoomOnTorBox(ctx):
		s.deferTarget(ctx, t, now)
	default:
		s.grab(ctx, now, t, *pick)
	}
}

func (s *Scheduler) noRoomOnTorBox(ctx context.Context) bool {
	n, err := s.cat.UncachedActive(ctx)
	if err != nil {
		s.logErr(ctx, "cannot count the torrents TorBox is caching", err)
		return true
	}
	return n >= s.torbox
}

// deferTarget sets a target aside until TorBox has a place for its release.
// It is neither a search that found nothing, which would back off, nor one
// that was spent, so the budget is given back.
func (s *Scheduler) deferTarget(ctx context.Context, t catalog.Target, now time.Time) {
	s.deferred[slot{t.TitleID, t.Season, t.Number}] = true
	if !t.Forced && !t.FollowUp {
		s.ignoreGone(s.cat.RefundSearch(ctx, t.TitleID, t.Season, t.Number, now.UTC().Format(time.DateOnly)))
	}
	s.log.Info("an uncached release waits for a free place on TorBox", "title", t.TitleID)
}

// stillWanted checks that the target was not unmonitored, removed or
// started downloading while its search was in flight.
func (s *Scheduler) stillWanted(ctx context.Context, now time.Time, t catalog.Target) bool {
	due, err := s.cat.Targets(ctx, now)
	if err != nil {
		s.logErr(ctx, "cannot look for what to search", err)
		return false
	}
	for _, d := range due {
		if d.TitleID == t.TitleID && d.Season == t.Season && d.Number == t.Number {
			return true
		}
	}
	return false
}

func (s *Scheduler) wouldGrab(ctx context.Context, now time.Time, t catalog.Target, c chooser.Candidate) {
	s.wouldGrabAs(ctx, now, t, c.ID, "Would grab "+releaseName(c.Stream))
}

func (s *Scheduler) wouldGrabAs(ctx context.Context, now time.Time, t catalog.Target, id, message string) {
	last, had, err := s.cat.LastWouldGrab(ctx, t.TitleID, t.Season, t.Number)
	if err != nil {
		s.logErr(ctx, "cannot read the last decision", err)
		return
	}
	if !had || last != id {
		s.note(ctx, t, catalog.Activity{Kind: catalog.ActivityWouldGrab, Message: message, ReleaseID: id})
	}
	// Nothing is downloaded, so the title stays missing: look again daily
	// rather than on the steps of a title that finds nothing.
	daily := len(backoff) - 1
	s.ignoreGone(s.cat.SetSchedule(ctx, t, catalog.Schedule{Attempts: daily, NextAt: now.Add(step(backoff, daily)), ClearForced: t.Forced}))
}

func (s *Scheduler) grab(ctx context.Context, now time.Time, t catalog.Target, c chooser.Candidate) {
	st := c.Stream
	job := pipeline.Job{
		TitleID: t.TitleID, Season: t.Season, Number: t.Number,
		Release:     catalog.ReleaseID{InfoHash: st.InfoHash, FileName: st.Filename, Size: st.Size},
		ReleaseName: releaseName(st), URL: st.URL, Uncached: !st.Cached,
	}
	if p := st.ParsedFile; p != nil {
		job.Resolution, job.Quality = p.Resolution, p.Quality
		job.Covered = chooser.CoveredEpisodes(st, t.Season)
	}
	d, err := s.tr.Enqueue(ctx, job)
	switch {
	case errors.Is(err, catalog.ErrActiveDownload) && d.ID != "" && d.Number != t.Number && !slices.Contains(d.Covered, t.Number):
		// The release collides with a download of another episode it covers. A
		// follow-up would skip backoff and the budget and search the target every gap.
		// A zero download (the busy one ended meanwhile) is not that collision.
		s.backOff(ctx, now, t, "")
	case err == nil, errors.Is(err, catalog.ErrActiveDownload):
		// The download failing leads straight to a new search, which skips the blocklisted release.
		s.ignoreGone(s.cat.SetSchedule(ctx, t, catalog.Schedule{Attempts: t.Attempts, FollowUp: true, ClearForced: t.Forced}))
	case errors.Is(err, pipeline.ErrBadJob):
		s.log.Warn("a title changed under a search", "title", t.TitleID)
	default:
		if ctx.Err() == nil {
			s.log.Error("cannot queue a download", "title", t.TitleID, "err", err)
			s.backOff(ctx, now, t, "")
		}
	}
}

// backOff schedules the target's next search after one that found nothing,
// and says so on Activity when message is not empty. A search the owner
// asked for does not move a schedule that is still in the future.
func (s *Scheduler) backOff(ctx context.Context, now time.Time, t catalog.Target, message string) {
	sc := catalog.Schedule{Attempts: t.Attempts + 1, NextAt: now.Add(step(backoff, t.Attempts)), ClearForced: t.Forced}
	if t.Forced && !t.FollowUp && t.NextAt.After(now) {
		sc.Attempts, sc.NextAt = t.Attempts, t.NextAt
	} else if message != "" {
		message += " Searching again in " + plain(step(backoff, t.Attempts)) + "."
	}
	if message != "" {
		s.note(ctx, t, catalog.Activity{Kind: catalog.ActivitySearched, Message: message})
	}
	s.ignoreGone(s.cat.SetSchedule(ctx, t, sc))
}

func (s *Scheduler) sourceFailed(ctx context.Context, now time.Time, t catalog.Target, failures int, err error) {
	wait := step(sourceBackoff, failures)
	s.log.Warn("the source failed a search", "title", t.TitleID, "failures", failures+1, "err", err)
	if err := s.cat.SetSourceBackoff(ctx, failures+1, now.Add(wait)); err != nil {
		s.logErr(ctx, "cannot record the source backoff", err)
		return
	}
	// One entry per outage, not one per attempt.
	if failures == 0 {
		s.note(ctx, t, catalog.Activity{
			Kind: catalog.ActivityFailed, Reason: "source_down",
			Message: sourceMessage(err) + " Searching again in " + plain(wait) + ".",
		})
	}
}

func sourceMessage(err error) string {
	switch {
	case errors.Is(err, aiostreams.ErrRateLimited):
		return "AIOStreams is rate-limiting marquee."
	case errors.Is(err, aiostreams.ErrRejected):
		return "AIOStreams refused marquee's credentials; check the AIOStreams manifest URL setting."
	}
	return "AIOStreams is not answering."
}

func sourceID(t catalog.Target) (string, bool) {
	if t.Kind == "movie" {
		return aiostreams.MovieID(t.IMDbID, t.TMDBID), true
	}
	return aiostreams.EpisodeID(t.TVDBID, t.IMDbID, t.Season, t.Number)
}

func releaseName(s aiostreams.Stream) string {
	if s.Filename != "" {
		return s.Filename
	}
	return s.FolderName
}

// nothingUsable says in words why a search left nothing to download.
func nothingUsable(found int, dropped []chooser.Drop) string {
	if found == 0 {
		return "Searched and found no releases."
	}
	counts := map[chooser.Reason]int{}
	for _, d := range dropped {
		counts[d.Reason]++
	}
	var why []string
	for _, r := range []struct {
		reason chooser.Reason
		label  string
	}{
		{chooser.NotCached, "not cached"}, {chooser.NotVideo, "not a video"}, {chooser.TooSmall, "too small"},
		{chooser.WrongEpisode, "wrong episode"}, {chooser.ProfileRejected, "rejected by profile"},
		{chooser.Blocklisted, "failed before"},
	} {
		if n := counts[r.reason]; n > 0 {
			why = append(why, fmt.Sprintf("%d %s", n, r.label))
		}
	}
	msg := fmt.Sprintf("Searched and found %d releases, none usable", found)
	if found == 1 {
		msg = "Searched and found 1 release, none usable"
	}
	if len(why) > 0 {
		msg += " ("
		for i, w := range why {
			if i > 0 {
				msg += ", "
			}
			msg += w
		}
		msg += ")"
	}
	return msg + "."
}

// plain is a wait in words: "15 minutes", "1 hour", "1 day".
func plain(d time.Duration) string {
	n, unit := int(d/time.Minute), "minute"
	switch {
	case d >= 24*time.Hour:
		n, unit = int(d/(24*time.Hour)), "day"
	case d >= time.Hour:
		n, unit = int(d/time.Hour), "hour"
	}
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// note writes an Activity entry for the target; failing to is only logged,
// because the search it describes has happened.
func (s *Scheduler) note(ctx context.Context, t catalog.Target, a catalog.Activity) {
	a.TitleID, a.Season, a.Number = t.TitleID, t.Season, t.Number
	s.ignoreGone(s.cat.AddActivity(ctx, a))
}

// ignoreGone logs a database error, except that a title removed while
// the scheduler worked on it is no error at all.
func (s *Scheduler) ignoreGone(err error) {
	if err != nil && !errors.Is(err, catalog.ErrNotFound) && !errors.Is(err, context.Canceled) {
		s.log.Error("scheduler cannot record its work", "err", err)
	}
}

func (s *Scheduler) logErr(ctx context.Context, what string, err error) {
	if err != nil && ctx.Err() == nil {
		s.log.Error(what, "err", err)
	}
}
