// Package importer moves a finished download into the media library and
// tells Jellyfin about it.
package importer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// ErrExists means a file is already at the destination. The importer never
// replaces a file it did not create.
var ErrExists = errors.New("importer: a file already exists at the destination")

// Place moves src, a finished file in the temp area, to dst in the library.
// The move is atomic, so Jellyfin never sees a half-written file, and it
// never replaces an existing file: src is hard-linked to dst, which fails
// when dst exists, and only then removed. The temp area must be on the
// library's filesystem; a copy would not be atomic, so there is none.
func Place(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // library folders must be readable by the media server
		return fmt.Errorf("cannot create the library folder: %w", err)
	}
	err := os.Link(src, dst)
	switch {
	case err == nil:
		return os.Remove(src)
	case errors.Is(err, fs.ErrExist):
		return ErrExists
	case errors.Is(err, syscall.EXDEV):
		return errors.New("the temp path and the library are not on the same filesystem, so the file cannot be moved atomically")
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("the downloaded file is gone: %w", err)
	}
	// The filesystem has no hard links: fall back to a rename, which
	// replaces silently, so check first.
	if _, statErr := os.Lstat(dst); statErr == nil {
		return ErrExists
	}
	return os.Rename(src, dst)
}
