// Package adopter takes over an existing library on first run: it reads the
// movie and series folders, matches each to TMDB and records what is on
// disk. It only reads the library; nothing in it can rename, move or delete
// a file.
package adopter

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/metadata"
	"github.com/davepintodev/marquee/internal/naming"
)

// TMDB is the part of the metadata client adoption uses.
type TMDB interface {
	Search(ctx context.Context, q string) ([]metadata.Summary, error)
	Details(ctx context.Context, kind string, id int) (metadata.Details, error)
}

type Options struct {
	Catalog                *catalog.Store
	TMDB                   TMDB
	MoviesPath, SeriesPath string
	Log                    *slog.Logger
}

type Adopter struct {
	Options
}

func New(o Options) *Adopter { return &Adopter{o} }

// folderRe is "Title (Year)": the year, when there is one, ends the name.
var folderRe = regexp.MustCompile(`^(.*\S)\s*\((\d{4})\)$`)

type run struct {
	*Adopter
	// adopted maps a title to the folder it was adopted from in this run.
	adopted  map[string]string
	problems []catalog.LibraryProblem
	titles   int
}

// Run adopts the library unless an earlier run completed. It stops at the
// first failure of TMDB or the database without recording anything as
// unmatched, so the next start tries again; titles already adopted stay.
func (a *Adopter) Run(ctx context.Context) error {
	if done, err := a.Catalog.AdoptionDone(ctx); err != nil || done {
		return err
	}
	r := &run{Adopter: a, adopted: map[string]string{}}
	if err := r.library(ctx, "Movies", "movie", a.MoviesPath); err != nil {
		return err
	}
	if err := r.library(ctx, "Series", "series", a.SeriesPath); err != nil {
		return err
	}
	if err := a.Catalog.FinishAdoption(ctx, r.problems); err != nil {
		return err
	}
	a.Log.Info("library adopted", "titles", r.titles, "problems", len(r.problems))
	return nil
}

func (r *run) library(ctx context.Context, label, kind, root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("cannot read the %s folder: %w", strings.ToLower(label), err)
	}
	for _, e := range entries {
		if !isFolder(root, e) || skipped(e.Name()) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.folder(ctx, label, kind, filepath.Join(root, e.Name()), e.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) problem(path, reason, format string, args ...any) {
	r.problems = append(r.problems, catalog.LibraryProblem{Path: path, Reason: reason, Message: fmt.Sprintf(format, args...)})
}

func (r *run) folder(ctx context.Context, label, kind, dir, dirName string) error {
	where := label + "/" + dirName
	name, year := parseFolder(dirName)
	found, err := r.TMDB.Search(ctx, name)
	if err != nil {
		return fmt.Errorf("searching TMDB for %q: %w", dirName, err)
	}
	var matches []metadata.Summary
	for _, m := range found {
		if m.Kind == kind && strings.EqualFold(naming.SeriesFolder(m.Title), name) && (year == 0 || m.Year == year) {
			matches = append(matches, m)
		}
	}
	switch {
	case len(matches) == 0:
		if year > 0 {
			r.problem(where, catalog.ProblemUnmatched, "No TMDB %s is named %q from %d.", kind, name, year)
		} else {
			r.problem(where, catalog.ProblemUnmatched, "No TMDB %s is named %q.", kind, name)
		}
		return nil
	case len(matches) > 1:
		var names []string
		for _, m := range matches {
			names = append(names, describe(m.Title, m.Year))
		}
		r.problem(where, catalog.ProblemAmbiguous, "Matched more than one TMDB title (%s); add the title to pick one.", strings.Join(names, ", "))
		return nil
	}

	id := catalog.TitleID(kind, matches[0].TMDBID)
	if first, dup := r.adopted[id]; dup {
		r.problem(where, catalog.ProblemAmbiguous, "Matches the same TMDB title as %q, which is already adopted; keep one of the two folders.", first)
		return nil
	}
	d, err := r.TMDB.Details(ctx, kind, matches[0].TMDBID)
	if errors.Is(err, metadata.ErrNotFound) {
		r.problem(where, catalog.ProblemUnmatched, "TMDB has no details for %q.", name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching %q from TMDB: %w", dirName, err)
	}

	n := catalog.NewTitleFromTMDB(d)
	var f catalog.Found
	if kind == "movie" {
		f.Movie, err = hasVideo(dir)
	} else {
		f.Episodes, err = r.episodes(where, dir, d, n)
	}
	if err != nil {
		// One folder the service cannot read must not keep the rest of the library from being adopted.
		r.problem(where, catalog.ProblemUnmatched, "The folder cannot be read (%v); not adopted.", why(err))
		return nil
	}
	if _, _, err := r.Catalog.Adopt(ctx, n, f); err != nil {
		return err
	}
	r.adopted[id] = dirName
	r.titles++
	return nil
}

// episodes reads the series folder's video files for their episode tokens.
// A file with none, or one naming an episode TMDB does not list, goes on the
// problem list and is otherwise ignored. Season 0 is left out: specials are
// not part of v1.
func (r *run) episodes(where, dir string, d metadata.Details, n catalog.NewTitle) ([]catalog.EpisodeRef, error) {
	known := make(map[catalog.EpisodeRef]bool, len(n.Episodes))
	for _, e := range n.Episodes {
		known[catalog.EpisodeRef{Season: e.Season, Number: e.Number}] = true
	}
	var refs []catalog.EpisodeRef
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		switch {
		case err != nil && p == dir:
			return err
		case err != nil:
			r.problem(where+"/"+filepath.ToSlash(relTo(dir, p)), catalog.ProblemUnparseable, "Cannot be read (%v); ignored.", why(err))
			if e != nil && e.IsDir() {
				return fs.SkipDir
			}
			return nil
		case e.IsDir():
			if p != dir && skipped(e.Name()) {
				return fs.SkipDir
			}
			return nil
		case !chooser.IsVideoFile(e.Name()):
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		file := where + "/" + filepath.ToSlash(rel)
		tok, ok := naming.ParseEpisodeToken(e.Name())
		if !ok {
			r.problem(file, catalog.ProblemUnparseable, "The file name has no S01E01 token; ignored.")
			return nil
		}
		if tok.Season == 0 {
			return nil
		}
		var unknown []string
		for _, ep := range tok.Episodes {
			ref := catalog.EpisodeRef{Season: tok.Season, Number: ep}
			refs = append(refs, ref)
			if !known[ref] {
				unknown = append(unknown, fmt.Sprintf("S%02dE%02d", tok.Season, ep))
			}
		}
		if len(unknown) > 0 {
			r.problem(file, catalog.ProblemUnparseable, "%s not an episode TMDB lists for %q; ignored.", isOrAre(unknown), d.Title)
		}
		return nil
	})
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Season != refs[j].Season {
			return refs[i].Season < refs[j].Season
		}
		return refs[i].Number < refs[j].Number
	})
	return refs, err
}

// why is what went wrong without the path, which the problem already names.
func why(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

func relTo(dir, p string) string {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return p
	}
	return rel
}

func isOrAre(eps []string) string {
	if len(eps) == 1 {
		return eps[0] + " is"
	}
	return strings.Join(eps, ", ") + " are"
}

// hasVideo reports whether a movie's own folder holds a video file; extras
// in subfolders do not count.
func hasVideo(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !e.IsDir() && chooser.IsVideoFile(e.Name()) {
			return true, nil
		}
	}
	return false, nil
}

func parseFolder(name string) (title string, year int) {
	if m := folderRe.FindStringSubmatch(name); m != nil {
		year, _ = strconv.Atoi(m[2])
		return m[1], year
	}
	return strings.TrimSpace(name), 0
}

func describe(title string, year int) string {
	if year == 0 {
		return title + ", year unknown"
	}
	return fmt.Sprintf("%s %d", title, year)
}

// isFolder follows a symbolic link to a folder; a library on a NAS is often
// assembled from links.
func isFolder(root string, e fs.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path.Join(filepath.ToSlash(root), e.Name()))
	return err == nil && info.IsDir()
}

// skipped names the folders that are not part of the library: hidden ones
// and a NAS's metadata folders such as @eaDir.
func skipped(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@")
}
