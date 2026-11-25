package download

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/naming"
	"github.com/davepintodev/marquee/internal/torbox"
)

// TorBoxClient is the part of the TorBox client the downloader uses.
type TorBoxClient interface {
	CreateTorrent(ctx context.Context, infoHash string) (torbox.Torrent, error)
	GetTorrent(ctx context.Context, torrentID int) (torbox.Torrent, error)
	RequestDownloadLink(ctx context.Context, torrentID, fileID int) (config.Secret, error)
	DeleteTorrent(ctx context.Context, torrentID int) error
}

// TorBoxOptions are the downloader's dependencies; the zero values of the
// durations are production.
type TorBoxOptions struct {
	Client TorBoxClient
	// HTTP moves the bytes once TorBox holds the file, and any release that
	// is cached already.
	HTTP Downloader
	// Poll is how often a caching torrent is looked at; default 15 s.
	Poll time.Duration
	// StallTimeout is how long a torrent may make no progress; default 30 minutes.
	StallTimeout time.Duration
	// MaxLifetime is how long caching may take in all; default 4 hours.
	MaxLifetime time.Duration
	// Now is the clock the two limits are judged by; nil is the system clock.
	Now func() time.Time
	Log *slog.Logger
}

// TorBox downloads a release through TorBox when the debrid service does
// not have it cached: it has TorBox fetch the torrent, waits, then hands
// the file's link to the HTTP downloader. A cached release goes straight to
// the HTTP downloader.
type TorBox struct {
	o TorBoxOptions
}

func NewTorBox(o TorBoxOptions) *TorBox {
	if o.Poll <= 0 {
		o.Poll = 15 * time.Second
	}
	if o.StallTimeout <= 0 {
		o.StallTimeout = 30 * time.Minute
	}
	if o.MaxLifetime <= 0 {
		o.MaxLifetime = 4 * time.Hour
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &TorBox{o: o}
}

func (t *TorBox) Download(ctx context.Context, req Request) (Result, error) {
	if req.Cached && req.URL.Reveal() != "" {
		return t.o.HTTP.Download(ctx, req)
	}
	id := req.TorBoxTorrentID
	if id == 0 {
		if req.InfoHash == "" {
			return Result{}, &Failure{ReasonLocalError, "the release lists no info hash for TorBox to fetch it by"}
		}
		created, err := t.o.Client.CreateTorrent(ctx, req.InfoHash)
		if err != nil {
			return Result{}, t.failure(ctx, err)
		}
		id = created.ID
		if err := t.report(ctx, req, 0, id, 0); err != nil {
			if errors.Is(err, ErrRemoved) {
				t.delete(ctx, req, id)
			}
			return Result{}, err
		}
	}

	tor, err := t.cache(ctx, req, id)
	if err != nil {
		return Result{}, err
	}
	if req.OnPresent != nil {
		name, fileID, err := req.OnPresent(id, tor.Files)
		if err != nil {
			if _, ok := AsFailure(err); ok {
				t.delete(ctx, req, id)
			}
			return Result{}, err
		}
		if name != "" {
			req.FileName, req.TorBoxFileID = name, fileID
		}
	}
	file, err := pickFile(tor.Files, req)
	if err != nil {
		t.delete(ctx, req, id)
		return Result{}, err
	}
	if err := t.report(ctx, req, 100, id, file.ID); err != nil {
		if errors.Is(err, ErrRemoved) {
			t.delete(ctx, req, id)
		}
		return Result{}, err
	}
	link, err := t.o.Client.RequestDownloadLink(ctx, id, file.ID)
	if err != nil {
		return Result{}, t.failure(ctx, err)
	}
	req.URL, req.Cached = link, true
	return t.o.HTTP.Download(ctx, req)
}

// cache waits until TorBox holds the whole torrent. A torrent that makes no
// progress for the stall timeout, or is still caching after the lifetime,
// fails the download and is deleted from TorBox unless another download
// still needs it. Shutting down leaves it there to be picked up again.
func (t *TorBox) cache(ctx context.Context, req Request, id int) (torbox.Torrent, error) {
	started := req.StartedAt
	progressAt := t.o.Now()
	if started.IsZero() {
		started = progressAt
	}
	var best float64
	for {
		tor, err := t.o.Client.GetTorrent(ctx, id)
		switch {
		case ctx.Err() != nil:
			return torbox.Torrent{}, ctx.Err()
		case errors.Is(err, torbox.ErrNotFound):
			return torbox.Torrent{}, &Failure{ReasonLocalError, "TorBox no longer has the torrent; it may have been deleted from the account"}
		case errors.Is(err, torbox.ErrRejected):
			return torbox.Torrent{}, t.failure(ctx, err)
		case err == nil:
			if tor.DownloadPresent {
				return tor, nil
			}
			if tor.Progress > best || tor.DownloadSpeed > 0 {
				progressAt = t.o.Now()
			}
			best = max(best, tor.Progress)
			if rerr := t.report(ctx, req, int(best*100), id, 0); rerr != nil {
				if errors.Is(rerr, ErrRemoved) {
					t.delete(ctx, req, id)
				}
				return torbox.Torrent{}, rerr
			}
		}
		// Any other error is TorBox being busy; the limits below still run.
		now := t.o.Now()
		switch {
		case now.Sub(progressAt) >= t.o.StallTimeout:
			t.delete(ctx, req, id)
			return torbox.Torrent{}, &Failure{ReasonStalled, fmt.Sprintf("TorBox made no progress caching it for %s", t.o.StallTimeout)}
		case now.Sub(started) >= t.o.MaxLifetime:
			t.delete(ctx, req, id)
			return torbox.Torrent{}, &Failure{ReasonCachingTimeout, fmt.Sprintf("TorBox was still caching it after %s", t.o.MaxLifetime)}
		}
		sleep(ctx, t.o.Poll)
		if ctx.Err() != nil {
			return torbox.Torrent{}, ctx.Err()
		}
	}
}

func (t *TorBox) report(ctx context.Context, req Request, percent, torrentID, fileID int) error {
	if req.OnCaching == nil {
		return nil
	}
	err := req.OnCaching(percent, torrentID, fileID)
	if err != nil && !errors.Is(err, ErrRemoved) && ctx.Err() == nil {
		t.o.Log.Warn("cannot record the TorBox progress", "err", err)
		return nil
	}
	return err
}

// delete removes the torrent from the account, best effort: marquee has
// already given up on it. A torrent another download still needs stays.
func (t *TorBox) delete(ctx context.Context, req Request, id int) {
	ctx = context.WithoutCancel(ctx)
	if req.Keep != nil && req.Keep(ctx, id) {
		return
	}
	if err := t.o.Client.DeleteTorrent(ctx, id); err != nil && !errors.Is(err, torbox.ErrNotFound) {
		t.o.Log.Warn("cannot delete a torrent from TorBox", "err", err)
	}
}

// failure is a TorBox call that failed. None of them is the release's fault.
func (t *TorBox) failure(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, torbox.ErrRejected):
		return &Failure{ReasonLocalError, "TorBox refused marquee's API key; check the TorBox API key setting"}
	case errors.Is(err, torbox.ErrRateLimited):
		return &Failure{ReasonLocalError, "TorBox is rate-limiting marquee"}
	}
	return &Failure{ReasonLocalError, "TorBox is not answering"}
}

// pickFile chooses the file of the torrent that is the release: the one
// with the release's file id and name, else the one at the release's path,
// else the first named as the release is, else, for an episode, the first
// whose name says that episode, else the only video the torrent holds. Its
// size must be the size the release was listed with, when that is known.
func pickFile(files []torbox.File, req Request) (torbox.File, error) {
	var videos []torbox.File
	for _, f := range files {
		if chooser.IsVideoFile(f.Name) {
			videos = append(videos, f)
		}
	}
	named := func(f torbox.File) bool {
		return req.FileName != "" && strings.EqualFold(path.Base(f.Name), path.Base(req.FileName))
	}
	tests := []func(torbox.File) bool{
		func(f torbox.File) bool { return f.ID == req.TorBoxFileID && named(f) },
		func(f torbox.File) bool { return req.FileName != "" && strings.EqualFold(f.Name, req.FileName) },
		named,
		func(f torbox.File) bool {
			tok, ok := naming.ParseEpisodeToken(path.Base(f.Name))
			return req.Number > 0 && ok && tok.Season == req.Season && slices.Contains(tok.Episodes, req.Number)
		},
		func(torbox.File) bool { return len(videos) == 1 },
	}
	for _, test := range tests {
		if i := slices.IndexFunc(videos, test); i >= 0 {
			pick := videos[i]
			if r := req.ReleaseSize; r > 0 && pick.Size != r {
				return torbox.File{}, &Failure{ReasonSizeMismatch, fmt.Sprintf("TorBox has a %d-byte file but the release is listed at %d bytes, so it is not the file that was asked for", pick.Size, r)}
			}
			return pick, nil
		}
	}
	return torbox.File{}, &Failure{ReasonNotVideo, "TorBox's copy has no video file that matches the release"}
}
