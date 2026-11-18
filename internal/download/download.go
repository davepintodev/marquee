// Package download fetches one release file over HTTP into a temp file.
// The HTTP implementation is ported from the owner's aiobridge worker.
package download

import (
	"context"
	"errors"

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
)

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
