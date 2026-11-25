package scheduler

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreams"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/pipeline"
	"github.com/davepintodev/marquee/internal/torbox"
)

const emptyPack = "empty_pack"

// tryPack reports whether the target needs nothing more from this search.
// A cached pack beats any single release, but a cached single release beats
// an uncached pack.
func (s *Scheduler) tryPack(ctx context.Context, now time.Time, t catalog.Target, streams []aiostreams.Stream, cc chooser.Context, pick *chooser.Candidate) bool {
	if s.tb == nil || s.torbox == 0 || t.Kind != "series" {
		return false
	}
	packs := chooser.Packs(streams, cc)
	if len(packs) == 0 {
		return false
	}
	p := packs[0]
	if !p.Stream.Cached && pick != nil && pick.Stream.Cached {
		return false
	}
	missing, err := s.cat.MissingEpisodes(ctx, t.TitleID, t.Season, now)
	if err != nil {
		s.logErr(ctx, "cannot read a season's missing episodes", err)
		return false
	}
	if len(missing) < 2 {
		return false
	}
	switch {
	case s.dry && p.Stream.Cached:
		s.wouldGrabPack(ctx, now, t, p, len(missing))
		return true
	case s.dry, !p.Stream.Cached && s.noRoomOnTorBox(ctx):
		return false
	}
	return s.grabPack(ctx, t, p, missing, cc)
}

func (s *Scheduler) wouldGrabPack(ctx context.Context, now time.Time, t catalog.Target, p chooser.Candidate, missing int) {
	s.wouldGrabAs(ctx, now, t, p.ID, fmt.Sprintf("Would grab season pack %s for %d missing episodes", chooser.PackName(p.Stream), missing))
}

func (s *Scheduler) grabPack(ctx context.Context, t catalog.Target, p chooser.Candidate, missing []int, cc chooser.Context) bool {
	st := p.Stream
	created, err := s.tb.CreateTorrent(ctx, st.InfoHash)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("TorBox would not take a season pack", "title", t.TitleID, "err", err)
		}
		return false
	}
	resolution, quality := parsedQuality(st)
	job := packJob{titleID: t.TitleID, infoHash: st.InfoHash, resolution: resolution, quality: quality, torrentID: created.ID, cc: cc}
	tor, err := s.tb.GetTorrent(ctx, created.ID)
	if err == nil && tor.DownloadPresent {
		owned, err := s.cat.OwnedEpisodes(ctx, t.TitleID, t.Season)
		if err != nil {
			s.logErr(ctx, "cannot read the episodes on disk", err)
			s.dropTorrent(ctx, created.ID)
			return false
		}
		matches := chooser.MatchPack(packFiles(tor.Files), st.InfoHash, t.Season, missing, owned, cc)
		if len(matches) == 0 {
			s.dropPack(ctx, t, p, created.ID)
			return false
		}
		queued, err := s.queuePack(ctx, job, matches, missing)
		if err != nil && ctx.Err() == nil {
			s.log.Error("cannot queue a season pack's episodes", "title", t.TitleID, "err", err)
		}
		if len(queued) == 0 {
			s.dropTorrent(ctx, created.ID)
			return false
		}
		s.log.Info("queued a season pack", "title", t.TitleID, "season", t.Season, "episodes", len(queued))
		if !slices.Contains(queued, t.Number) {
			return false
		}
		s.ignoreGone(s.cat.SetSchedule(ctx, t, catalog.Schedule{Attempts: t.Attempts, FollowUp: true, ClearForced: t.Forced}))
		return true
	}
	// AIOStreams can call a pack cached that TorBox must still fetch, and
	// TorBox starts fetching on create, so it needs a free slot after all.
	if st.Cached && s.noRoomOnTorBox(ctx) {
		s.dropTorrent(ctx, created.ID)
		return false
	}
	_, err = s.tr.Enqueue(ctx, pipeline.Job{
		TitleID: t.TitleID, Season: t.Season, Number: t.Number,
		Release: catalog.ReleaseID{InfoHash: st.InfoHash}, ReleaseName: chooser.PackName(st),
		Resolution: resolution, Quality: quality, Uncached: true, TorBoxTorrentID: created.ID,
	})
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, catalog.ErrActiveDownload) {
			s.log.Error("cannot queue a season pack", "title", t.TitleID, "err", err)
		}
		s.dropTorrent(ctx, created.ID)
		return false
	}
	s.ignoreGone(s.cat.SetSchedule(ctx, t, catalog.Schedule{Attempts: t.Attempts, FollowUp: true, ClearForced: t.Forced}))
	return true
}

// ExpandPack records d's own file of the pack before queueing the others, so
// that the episodes that file also holds are no longer missing.
func (s *Scheduler) ExpandPack(ctx context.Context, d catalog.Download, torrentID int, files []torbox.File) (pipeline.PackExpansion, error) {
	var x pipeline.PackExpansion
	t, err := s.cat.Get(ctx, d.TitleID)
	if err != nil {
		return x, err
	}
	now := s.cat.Now()
	missing, err := s.cat.MissingEpisodes(ctx, d.TitleID, d.Season, now)
	if err != nil {
		return x, err
	}
	owned, err := s.cat.OwnedEpisodes(ctx, d.TitleID, d.Season)
	if err != nil {
		return x, err
	}
	blocked, err := s.cat.Blocklist(ctx)
	if err != nil {
		return x, err
	}
	profile, _, _ := s.profiles.Resolve(t.Kind, t.Profile)
	cc := chooser.Context{Kind: t.Kind, Season: d.Season, Profile: profile, Blocklist: blocked, Now: now}
	matches := chooser.MatchPack(packFiles(files), d.Release.InfoHash, d.Season, append(slices.Clone(missing), d.Number), owned, cc)
	if i := slices.IndexFunc(matches, func(m chooser.PackMatch) bool { return slices.Contains(m.Episodes, d.Number) }); i >= 0 {
		m := matches[i]
		release := catalog.ReleaseID{InfoHash: d.Release.InfoHash, FileName: path.Base(m.Name), Size: m.Size}
		x.Own, err = s.cat.SetDownloadFile(ctx, d.ID, release, pipeline.ListedEpisodes(t, d.Season, d.Number, m.Episodes), m.ID)
		if err != nil {
			return x, err
		}
		x.Holds = true
		matches = slices.Delete(matches, i, i+1)
	}
	job := packJob{titleID: d.TitleID, infoHash: d.Release.InfoHash, resolution: d.Resolution, quality: d.Quality, torrentID: torrentID, cc: cc}
	queued, err := s.queuePack(ctx, job, matches, missing)
	x.Queued = len(queued)
	return x, err
}

type packJob struct {
	titleID, infoHash, resolution, quality string
	torrentID                              int
	cc                                     chooser.Context
}

// queuePack skips a match another download took meanwhile rather than
// failing the rest.
func (s *Scheduler) queuePack(ctx context.Context, j packJob, matches []chooser.PackMatch, wanted []int) ([]int, error) {
	var queued []int
	for _, m := range matches {
		i := slices.IndexFunc(m.Episodes, func(e int) bool { return slices.Contains(wanted, e) })
		if i < 0 {
			continue
		}
		name := path.Base(m.Name)
		_, err := s.tr.Enqueue(ctx, pipeline.Job{
			TitleID: j.titleID, Season: j.cc.Season, Number: m.Episodes[i],
			Release:     catalog.ReleaseID{InfoHash: j.infoHash, FileName: name, Size: m.Size},
			ReleaseName: name, Resolution: j.resolution, Quality: j.quality, Covered: m.Episodes,
			TorBoxTorrentID: j.torrentID, TorBoxFileID: m.ID,
		})
		switch {
		case err == nil:
			queued = append(queued, m.Episodes...)
		case errors.Is(err, catalog.ErrActiveDownload), errors.Is(err, pipeline.ErrBadJob):
		default:
			return queued, err
		}
	}
	return queued, nil
}

func (s *Scheduler) dropPack(ctx context.Context, t catalog.Target, p chooser.Candidate, torrentID int) {
	s.dropTorrent(ctx, torrentID)
	s.ignoreGone(s.cat.Block(ctx, p.ID, emptyPack))
	s.log.Info("a season pack held none of the missing episodes", "title", t.TitleID, "pack", chooser.PackName(p.Stream))
}

// dropTorrent deletes a torrent marquee asked TorBox for, unless a download
// reads from it, as one does when TorBox answered with a torrent it already had.
func (s *Scheduler) dropTorrent(ctx context.Context, torrentID int) {
	ctx = context.WithoutCancel(ctx)
	if users, err := s.cat.TorrentUsers(ctx, torrentID, ""); err != nil || len(users) > 0 {
		return
	}
	if err := s.tb.DeleteTorrent(ctx, torrentID); err != nil && !errors.Is(err, torbox.ErrNotFound) {
		s.log.Warn("cannot delete a torrent from TorBox", "err", err)
	}
}

func packFiles(files []torbox.File) []chooser.PackFile {
	out := make([]chooser.PackFile, len(files))
	for i, f := range files {
		out[i] = chooser.PackFile{ID: f.ID, Name: f.Name, Size: f.Size}
	}
	return out
}

func parsedQuality(st aiostreams.Stream) (string, string) {
	if p := st.ParsedFile; p != nil {
		return p.Resolution, p.Quality
	}
	return "", ""
}
