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

// emptyPack is the blocklist reason of a season pack that held none of the
// missing episodes.
const emptyPack = "empty_pack"

// tryPack takes a season pack instead of the target's own release when
// TorBox is set up and at least two episodes of the season are missing.
// A cached pack beats any single release, but a cached single release beats
// an uncached pack. It reports whether the target needs nothing more from
// this search; otherwise the caller goes on with pick.
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
	case s.dry:
		s.wouldGrabPack(ctx, now, t, p, len(missing))
		return true
	case !p.Stream.Cached && s.noRoomOnTorBox(ctx):
		return false
	}
	return s.grabPack(ctx, t, p, missing, cc)
}

func (s *Scheduler) wouldGrabPack(ctx context.Context, now time.Time, t catalog.Target, p chooser.Candidate, missing int) {
	s.wouldGrabAs(ctx, now, t, p.ID, fmt.Sprintf("Would grab season pack %s for %d missing episodes", chooser.PackName(p.Stream), missing))
}

// grabPack has TorBox take the pack. When TorBox holds it already, every
// wanted episode it has is queued now; otherwise the target is queued from
// it, and the rest follow once TorBox has it (see ExpandPack).
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
	tor, err := s.tb.GetTorrent(ctx, created.ID)
	if err == nil && tor.DownloadPresent {
		owned, err := s.cat.OwnedEpisodes(ctx, t.TitleID, t.Season)
		if err != nil {
			s.logErr(ctx, "cannot read the episodes on disk", err)
			s.dropTorrent(ctx, created.ID)
			return false
		}
		queued, err := s.queuePack(ctx, t.TitleID, t.Season, st.InfoHash, resolution, quality, created.ID, tor.Files, missing, owned, cc)
		if err != nil && ctx.Err() == nil {
			s.log.Error("cannot queue a season pack's episodes", "title", t.TitleID, "err", err)
		}
		if len(queued) == 0 {
			s.dropPack(ctx, t, p, created.ID)
			return false
		}
		s.log.Info("queued a season pack", "title", t.TitleID, "season", t.Season, "episodes", len(queued))
		if !slices.Contains(queued, t.Number) {
			return false
		}
		s.ignoreGone(s.cat.SetSchedule(ctx, t, catalog.Schedule{Attempts: t.Attempts, FollowUp: true, ClearForced: t.Forced}))
		return true
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

// ExpandPack queues the wanted episodes of a season pack that TorBox has
// finished caching for download d, other than d's own.
func (s *Scheduler) ExpandPack(ctx context.Context, d catalog.Download, torrentID int, files []torbox.File) (int, bool, error) {
	pf := packFiles(files)
	holds := chooser.HoldsEpisode(pf, d.Season, d.Number)
	t, err := s.cat.Get(ctx, d.TitleID)
	if err != nil {
		return 0, holds, err
	}
	now := s.cat.Now()
	missing, err := s.cat.MissingEpisodes(ctx, d.TitleID, d.Season, now)
	if err != nil {
		return 0, holds, err
	}
	owned, err := s.cat.OwnedEpisodes(ctx, d.TitleID, d.Season)
	if err != nil {
		return 0, holds, err
	}
	blocked, err := s.cat.Blocklist(ctx)
	if err != nil {
		return 0, holds, err
	}
	profile, _, _ := s.profiles.Resolve(t.Kind, t.Profile)
	cc := chooser.Context{Kind: t.Kind, Season: d.Season, Profile: profile, Blocklist: blocked, Now: now}
	queued, err := s.queuePack(ctx, d.TitleID, d.Season, d.Release.InfoHash, d.Resolution, d.Quality, torrentID, files, missing, owned, cc)
	return len(queued), holds, err
}

// queuePack queues a download of every file of the pack that holds wanted
// episodes, all reading from the one torrent, and returns the episodes queued.
func (s *Scheduler) queuePack(ctx context.Context, titleID string, season int, infoHash, resolution, quality string,
	torrentID int, files []torbox.File, wanted, owned []int, cc chooser.Context) ([]int, error) {
	var queued []int
	for _, m := range chooser.MatchPack(packFiles(files), infoHash, season, wanted, owned, cc) {
		number := m.Episodes[slices.IndexFunc(m.Episodes, func(e int) bool { return slices.Contains(wanted, e) })]
		name := path.Base(m.Name)
		_, err := s.tr.Enqueue(ctx, pipeline.Job{
			TitleID: titleID, Season: season, Number: number,
			Release:     catalog.ReleaseID{InfoHash: infoHash, FileName: name, Size: m.Size},
			ReleaseName: name, Resolution: resolution, Quality: quality, Covered: m.Episodes,
			TorBoxTorrentID: torrentID, TorBoxFileID: m.ID,
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

// dropPack gives up on a pack that held no wanted episode: it leaves TorBox
// and the blocklist, so no search takes it again.
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
