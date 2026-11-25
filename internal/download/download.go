// Package download fetches one release file over HTTP into a temp file.
// The HTTP implementation downloads straight from a debrid link.
package download

import (
	"context"
	"errors"
	"time"

	"github.com/davepintodev/marquee/internal/config"
)

// Reason is why a download failed, in the vocabulary of the API's
// FailureReason.
type Reason string

const (
	ReasonStalled      Reason = "stalled"
	ReasonNotVideo     Reason = "not_video"
	ReasonSizeMismatch Reason = "size_mismatch"
	ReasonSourceDown   Reason = "source_down"
	// ReasonCachingTimeout is a torrent TorBox was still caching after the
	// longest wait marquee allows.
	ReasonCachingTimeout Reason = "caching_timeout"
	// ReasonLocalError is a failure that is not the release's fault, such as
	// TorBox refusing the API key, so the release is not blocklisted.
	ReasonLocalError Reason = "local_error"
)

// ErrRemoved is what a Request's OnCaching returns when the download it
// belongs to has gone, because its title was removed.
var ErrRemoved = errors.New("download: the download was removed")

// Failure is a download that failed for a reason worth recording. Detail is
// plain language and never holds a URL.
type Failure struct {
	Reason Reason
	Detail string
}

func (f *Failure) Error() string { return f.Detail }

// AsFailure returns the Failure inside err, if any.
func AsFailure(err error) (*Failure, bool) {
	var f *Failure
	return f, errors.As(err, &f)
}

// Request is one file to fetch.
type Request struct {
	// URL is the playback link, which carries credentials.
	URL config.Secret
	// Dest is the temp file. A partial file and its progress record, at
	// Dest+".plan", are kept after a cancelled run so the next run resumes.
	Dest string
	// ReleaseSize is the size the release was listed with; zero when unknown.
	ReleaseSize int64
	// Cached is false for a release the debrid service does not hold, which
	// TorBox has to fetch first; URL is then empty.
	Cached bool
	// InfoHash, FileName, Season and Number say which release and which file
	// of it to take from a TorBox torrent; Season and Number are zero for a movie.
	InfoHash, FileName string
	Season, Number     int
	// TorBoxTorrentID is the torrent an earlier run already created, which
	// this run keeps watching; zero starts one.
	TorBoxTorrentID int
	// StartedAt is when TorBox began caching the torrent, for the longest
	// time it may take; zero is now.
	StartedAt time.Time
	// Keep says whether another download still needs the torrent; it is asked
	// whenever this one is about to delete the torrent from TorBox, which it
	// then leaves alone. Nil means nobody else does.
	Keep func(ctx context.Context, torrentID int) bool
	// OnCaching is told how far TorBox has got, as a percent, and which torrent
	// and, once it is chosen, file that is. It returns ErrRemoved when the
	// download is gone.
	OnCaching func(percent, torrentID, fileID int) error
	// Progress is called every second or so with the bytes written so far
	// and the total the server reported.
	Progress func(done, total int64)
}

// Result is a finished download.
type Result struct {
	Size int64
}

// Downloader fetches a release file. Cancelling ctx stops the download and
// leaves what was fetched for a later call to resume. Any other failure is
// a *Failure and leaves nothing behind.
type Downloader interface {
	Download(ctx context.Context, req Request) (Result, error)
}
