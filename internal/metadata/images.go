package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/davepintodev/marquee/internal/keylock"
)

const (
	// A poster or backdrop is a few hundred kilobytes; a larger body is not one.
	maxImageBytes = 20 << 20
	// DefaultCacheBytes bounds the image cache; the oldest files go first.
	DefaultCacheBytes = 256 << 20
)

// Images keeps TMDB images on disk, keyed by TMDB's file path and the size
// asked for, so the browser never talks to TMDB and each file is fetched once.
type Images struct {
	dir      string
	maxBytes int64
	fetch    func(ctx context.Context, size, filePath string) (io.ReadCloser, error)
	locks    keylock.Locks
	evictMu  sync.Mutex
}

// NewImages caches under dir, which is created on first use and must not be
// inside a library.
func NewImages(dir string, maxBytes int64, c *Client) *Images {
	if maxBytes <= 0 {
		maxBytes = DefaultCacheBytes
	}
	return &Images{dir: dir, maxBytes: maxBytes, fetch: c.FetchImage}
}

func (i *Images) file(size, filePath string) string {
	sum := sha256.Sum256([]byte(size + "|" + filePath))
	ext := strings.ToLower(path.Ext(filePath))
	if _, ok := contentTypes[ext]; !ok {
		ext = ".jpg"
	}
	return filepath.Join(i.dir, size, hex.EncodeToString(sum[:16])+ext)
}

var contentTypes = map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}

// ContentType is the type of a cached file, from its extension.
func ContentType(file string) string { return contentTypes[strings.ToLower(filepath.Ext(file))] }

// Open returns the cached file for the image, fetching it first when the
// cache has none. Concurrent calls for one image share one fetch.
func (i *Images) Open(ctx context.Context, size, filePath string) (string, error) {
	file := i.file(size, filePath)
	if _, err := os.Stat(file); err == nil {
		return file, nil
	}
	unlock := i.locks.Lock(file)
	defer unlock()
	if _, err := os.Stat(file); err == nil {
		return file, nil
	}
	if err := i.download(ctx, size, filePath, file); err != nil {
		return "", err
	}
	i.evict(file)
	return file, nil
}

func (i *Images) download(ctx context.Context, size, filePath, file string) (err error) {
	body, err := i.fetch(ctx, size, filePath)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	// The temp file sits beside the target so the rename never crosses filesystems.
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return fmt.Errorf("image cache: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".partial-*")
	if err != nil {
		return fmt.Errorf("image cache: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	n, copyErr := io.Copy(tmp, io.LimitReader(body, maxImageBytes+1))
	switch {
	case copyErr != nil && ctx.Err() != nil:
		err = ctx.Err()
	case copyErr != nil:
		err = fmt.Errorf("%w: image cut short", ErrUnavailable)
	case n > maxImageBytes:
		err = fmt.Errorf("%w: image too large", ErrUnavailable)
	case n == 0:
		err = fmt.Errorf("%w: empty image", ErrUnavailable)
	}
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("image cache: %w", err)
	}
	if err = os.Rename(tmp.Name(), file); err != nil {
		return fmt.Errorf("image cache: %w", err)
	}
	return nil
}

// Forget deletes a title's cached copy of an image, if there is one.
func (i *Images) Forget(size, filePath string) {
	file := i.file(size, filePath)
	unlock := i.locks.Lock(file)
	defer unlock()
	_ = os.Remove(file)
}

// evict removes the oldest files while the cache is over its bound, never
// the file just written.
func (i *Images) evict(keep string) {
	i.evictMu.Lock()
	defer i.evictMu.Unlock()
	type cached struct {
		path string
		size int64
		mod  int64
	}
	var files []cached
	var total int64
	_ = filepath.WalkDir(i.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".partial-") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, cached{p, info.Size(), info.ModTime().UnixNano()})
		total += info.Size()
		return nil
	})
	if total <= i.maxBytes {
		return
	}
	sort.Slice(files, func(a, b int) bool { return files[a].mod < files[b].mod })
	for _, f := range files {
		if total <= i.maxBytes {
			break
		}
		if f.path == keep {
			continue
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}
