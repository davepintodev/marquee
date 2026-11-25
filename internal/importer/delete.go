package importer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/davepintodev/marquee/internal/chooser"
)

// ErrUnsafePath means a delete was refused because the path is not a video
// file inside the library root: it is empty, absolute, climbs with "..",
// resolves outside the root through a symlink, or names a folder or a file
// that is not a video.
var ErrUnsafePath = errors.New("importer: refusing to touch a path outside the library")

var sidecarExts = map[string]bool{".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".vtt": true}

// DeleteMediaFile removes the video at relPath under root and the subtitle
// sidecars beside it (see DeleteSidecars). A video or folder that is
// already gone is not an error, so a delete can be repeated.
//
// Nothing outside root is ever touched. relPath must be relative and free
// of "..". The root and the video's folder are resolved through symlinks
// and the folder must still be inside the root; a video that is itself a
// symlink is removed as a link and its target is left alone, but only when
// its folder is strictly inside the root.
func DeleteMediaFile(root, relPath string) error {
	dir, name, err := resolveMedia(root, relPath)
	if err != nil {
		return err
	}
	if dir == "" {
		return nil
	}
	if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot delete %s: %w", name, err)
	}
	return removeSidecars(dir, name)
}

// CheckMediaFile reports whether DeleteMediaFile would accept relPath,
// without deleting anything, so a batch of deletes can be refused whole
// before the first file goes.
func CheckMediaFile(root, relPath string) error {
	_, _, err := resolveMedia(root, relPath)
	return err
}

// DeleteSidecars removes the subtitle sidecars of the video at relPath
// and leaves the video where it is. A sidecar is a regular file or
// symlink in the video's folder named "<stem>.<anything>" with the
// extension .srt, .ass, .ssa, .sub or .vtt, such as "<stem>.es.srt" or
// "<stem>.en.forced.srt". The stem is matched case-sensitively, so
// "Movie (2020) Extended.srt" is not a sidecar of "Movie (2020).mkv".
func DeleteSidecars(root, relPath string) error {
	dir, name, err := resolveMedia(root, relPath)
	if err != nil {
		return err
	}
	if dir == "" {
		return nil
	}
	return removeSidecars(dir, name)
}

func removeSidecars(dir, video string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("cannot read the folder of %s: %w", video, err)
	}
	prefix := strings.TrimSuffix(video, filepath.Ext(video)) + "."
	var errs []error
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, prefix) || !sidecarExts[strings.ToLower(filepath.Ext(n))] || n == video {
			continue
		}
		if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("cannot delete %s: %w", n, err))
		}
	}
	return errors.Join(errs...)
}

// PruneEmptyDirs removes the folders that held the file at relPath, from
// the innermost up, for as long as each is empty. It stops at the first
// folder that holds anything and never removes the root or leaves it.
func PruneEmptyDirs(root, relPath string) error {
	resolvedRoot, rel, err := checkRel(root, relPath)
	if err != nil {
		return err
	}
	for dir := path.Dir(rel); dir != "."; dir = path.Dir(dir) {
		resolved, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, filepath.FromSlash(dir)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot look at %s: %w", dir, err)
		}
		if !strictlyInside(resolvedRoot, resolved) {
			return ErrUnsafePath
		}
		entries, err := os.ReadDir(resolved)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", dir, err)
		}
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(resolved); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("cannot remove the empty folder %s: %w", dir, err)
		}
	}
	return nil
}

// resolveMedia validates relPath and returns the real folder of the video
// and its name. An empty folder with a nil error means the folder is not
// there, so nothing needs deleting.
func resolveMedia(root, relPath string) (dir, name string, err error) {
	resolvedRoot, rel, err := checkRel(root, relPath)
	if err != nil {
		return "", "", err
	}
	name = path.Base(rel)
	if !chooser.IsVideoFile(name) {
		return "", "", ErrUnsafePath
	}
	dir, err = filepath.EvalSymlinks(filepath.Join(resolvedRoot, filepath.FromSlash(path.Dir(rel))))
	if errors.Is(err, fs.ErrNotExist) {
		return "", name, nil
	}
	if err != nil {
		return "", "", fmt.Errorf("cannot look at the folder of %s: %w", name, err)
	}
	rootRel, err := filepath.Rel(resolvedRoot, dir)
	if err != nil || rootRel == ".." || strings.HasPrefix(rootRel, ".."+string(filepath.Separator)) {
		return "", "", ErrUnsafePath
	}
	fi, err := os.Lstat(filepath.Join(dir, name))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return dir, name, nil
	case err != nil:
		return "", "", fmt.Errorf("cannot look at %s: %w", name, err)
	case fi.IsDir():
		return "", "", ErrUnsafePath
	case fi.Mode()&fs.ModeSymlink != 0 && !strictlyInside(resolvedRoot, dir):
		return "", "", ErrUnsafePath
	}
	return dir, name, nil
}

// checkRel resolves the root and returns relPath cleaned, in slash form.
func checkRel(root, relPath string) (resolvedRoot, rel string, err error) {
	if root == "" {
		return "", "", errors.New("importer: no library root to delete from")
	}
	if relPath == "" || strings.ContainsRune(relPath, 0) || filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, `\`) {
		return "", "", ErrUnsafePath
	}
	rel = filepath.ToSlash(relPath)
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", "", ErrUnsafePath
		}
	}
	if rel = path.Clean(rel); rel == "." {
		return "", "", ErrUnsafePath
	}
	resolvedRoot, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("cannot resolve the library folder: %w", err)
	}
	if fi, err := os.Stat(resolvedRoot); err != nil || !fi.IsDir() {
		return "", "", errors.New("importer: the library root is not a folder")
	}
	return resolvedRoot, rel, nil
}

func strictlyInside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
