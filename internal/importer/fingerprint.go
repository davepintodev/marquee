package importer

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

const (
	chunk       = 1 << 20
	wholeFileAt = 3 * chunk
)

// Fingerprint identifies a file's content cheaply: its size, then the
// SHA-256 of the whole file when it is at most 3 MiB, or of its first,
// middle and last MiB otherwise. Reading three chunks keeps it fast on a
// multi-GiB file, and the size in front makes files of different sizes
// never collide. Two files with the same fingerprint are the same file for
// marquee's purposes (a duplicate episode), not a cryptographic promise.
func Fingerprint(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fi.Size()
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d:", size)
	if size <= wholeFileAt {
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
	} else {
		for _, off := range []int64{0, (size - chunk) / 2, size - chunk} {
			if _, err := io.Copy(h, io.NewSectionReader(f, off, chunk)); err != nil {
				return "", err
			}
		}
	}
	return fmt.Sprintf("%d:%x", size, h.Sum(nil)), nil
}
