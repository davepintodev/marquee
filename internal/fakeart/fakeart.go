// Package fakeart downloads the real posters and backdrops of the fake's
// fixture titles into a local folder, so a preview can show real art that
// the repository never ships.
package fakeart

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/davepintodev/marquee/internal/fake"
	"github.com/davepintodev/marquee/internal/metadata"
)

// The sizes the real image route asks TMDB for.
const (
	posterSize   = "w500"
	backdropSize = "w1280"
)

// Fetch downloads every title's poster and backdrop into dir as
// poster-<titleId>.jpg and backdrop-<titleId>.jpg, reporting progress on out.
// A title TMDB does not know, or a failed download, is reported at the end
// and does not stop the others; a refused key or a cancelled ctx does.
func Fetch(ctx context.Context, c *metadata.Client, titles []fake.Title, dir string, out io.Writer) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("art folder: %w", err)
	}
	var failed []error
	for _, t := range titles {
		if err := ctx.Err(); err != nil {
			return err
		}
		s, err := c.Summary(ctx, t.Kind, t.TmdbID)
		if err != nil {
			if errors.Is(err, metadata.ErrRejected) || ctx.Err() != nil {
				return err
			}
			failed = append(failed, fmt.Errorf("%s: %w", t.ID, err))
			continue
		}
		for _, img := range []struct{ kind, size, path string }{
			{"poster", posterSize, s.PosterPath},
			{"backdrop", backdropSize, s.BackdropPath},
		} {
			if img.path == "" {
				_, _ = fmt.Fprintf(out, "%s: TMDB has no %s\n", t.ID, img.kind)
				continue
			}
			if err := download(ctx, c, img.size, img.path, fake.ArtFile(dir, img.kind, t.ID)); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				failed = append(failed, fmt.Errorf("%s %s: %w", t.ID, img.kind, err))
				continue
			}
			_, _ = fmt.Fprintf(out, "%s: %s saved\n", t.ID, img.kind)
		}
	}
	return errors.Join(failed...)
}

// profileSize is a cast photo shown at most about 120 CSS pixels wide.
const profileSize = "w185"

// FetchPeople downloads the profile photo of every fixture cast member TMDB has
// one for, as profile-<personId>.jpg. The fixtures' person ids are their own, so
// each member is found in the title's TMDB credits by name. A title whose credits
// cannot be read, or a member TMDB has no photo for, is reported and skipped; a
// refused key or a cancelled ctx stops the run.
func FetchPeople(ctx context.Context, c *metadata.Client, cast []fake.Cast, dir string, out io.Writer) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("art folder: %w", err)
	}
	var failed []error
	credits := map[string][]metadata.Credit{}
	saved := map[int]bool{}
	for _, m := range cast {
		if err := ctx.Err(); err != nil {
			return err
		}
		if saved[m.PersonID] {
			continue
		}
		list, ok := credits[m.TitleID]
		if !ok {
			about, err := c.About(ctx, m.Kind, m.TmdbID)
			if err != nil {
				if errors.Is(err, metadata.ErrRejected) || ctx.Err() != nil {
					return err
				}
				failed = append(failed, fmt.Errorf("%s credits: %w", m.TitleID, err))
			}
			list = about.Cast
			credits[m.TitleID] = list
		}
		path := ""
		for _, p := range list {
			if strings.EqualFold(p.Name, m.Name) {
				path = p.ProfilePath
				break
			}
		}
		if path == "" {
			_, _ = fmt.Fprintf(out, "%s: no photo for %s\n", m.TitleID, m.Name)
			continue
		}
		if err := download(ctx, c, profileSize, path, fake.ProfileFile(dir, m.PersonID)); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed = append(failed, fmt.Errorf("%s %s photo: %w", m.TitleID, m.Name, err))
			continue
		}
		saved[m.PersonID] = true
		_, _ = fmt.Fprintf(out, "%s: photo of %s saved\n", m.TitleID, m.Name)
	}
	return errors.Join(failed...)
}

// download writes through a temporary file so a failure never leaves half
// an image, or replaces a good earlier one.
func download(ctx context.Context, c *metadata.Client, size, tmdbPath, file string) (err error) {
	body, err := c.FetchImage(ctx, size, tmdbPath)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(file), "art-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = io.Copy(tmp, body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}
