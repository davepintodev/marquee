// Package adopter keeps the catalog in step with the library on disk. The
// first run takes over an existing library: it reads the movie and series
// folders, matches each to TMDB and records what is there. Later scans, on a
// timer and on demand, notice files added or deleted outside marquee and
// folders dropped into the library. It only reads the library; nothing in
// it can rename, move or delete a file.
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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/chooser"
	"github.com/davepintodev/marquee/internal/importer"
	"github.com/davepintodev/marquee/internal/metadata"
	"github.com/davepintodev/marquee/internal/naming"
)

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

// Adopter scans the library. Scans never overlap: one that is asked for
// while another runs waits for it.
type Adopter struct {
	Options
	mu sync.Mutex
}

func New(o Options) *Adopter { return &Adopter{Options: o} }

// ScanResult counts what one full scan did.
type ScanResult struct {
	TitlesScanned int
	// Adopted is the number of new titles taken from folders the catalog did not know.
	Adopted int
	// FoundOnDisk counts movies and episodes that were missing and have a file now.
	FoundOnDisk int
	// MissingOnDisk counts movies and episodes whose file was deleted outside marquee.
	MissingOnDisk int
	// Problems is the number of library problems after the scan.
	Problems int
}

// UnavailableError says the library cannot be scanned now, which is not
// the same as it being empty: nothing is changed.
type UnavailableError struct{ Message string }

func (e *UnavailableError) Error() string { return e.Message }

// UpstreamError says TMDB, not the library or the database, stopped a scan.
type UpstreamError struct{ Err error }

func (e *UpstreamError) Error() string { return e.Err.Error() }
func (e *UpstreamError) Unwrap() error { return e.Err }

// folderRe is "Title (Year)": the year, when there is one, ends the name.
var folderRe = regexp.MustCompile(`^(.*\S)\s*\((\d{4})\)$`)

type library struct {
	label, kind, root string
}

func (a *Adopter) libraries() []library {
	return []library{{"Movies", "movie", a.MoviesPath}, {"Series", "series", a.SeriesPath}}
}

func (a *Adopter) root(kind string) string {
	if kind == "movie" {
		return a.MoviesPath
	}
	return a.SeriesPath
}

func labelOf(kind string) string {
	if kind == "movie" {
		return "Movies"
	}
	return "Series"
}

// Run adopts the library unless an earlier run completed. It stops at the
// first failure of TMDB or the database without recording anything as
// unmatched, so the next start tries again; titles already adopted stay.
func (a *Adopter) Run(ctx context.Context) error {
	if done, err := a.Catalog.AdoptionDone(ctx); err != nil || done {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	res, err := a.scanAll(ctx, true)
	if err != nil {
		return err
	}
	a.Log.Info("library adopted", "titles", res.TitlesScanned, "problems", res.Problems)
	return nil
}

// ScanAll reads the whole library and brings the catalog in step with it:
// files deleted outside marquee make their movie or episode missing and
// unmonitored, files found are recorded, folders the catalog does not know
// are matched against TMDB and adopted, and the library problems are
// replaced by this scan's. A scan that cannot finish, because TMDB or the
// library is unavailable, changes no tracked file and keeps the old problems.
func (a *Adopter) ScanAll(ctx context.Context) (ScanResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	res, err := a.scanAll(ctx, false)
	if err != nil {
		return ScanResult{}, err
	}
	a.Log.Info("library scanned", "titles", res.TitlesScanned, "adopted", res.Adopted, "found", res.FoundOnDisk, "missing", res.MissingOnDisk, "problems", res.Problems)
	return res, nil
}

// ScanTitle reads only the folder of one title, never asks TMDB, and
// applies what it finds as ScanAll does, refreshing the library problems
// under that folder. A title not in the library answers catalog.ErrNotFound.
func (a *Adopter) ScanTitle(ctx context.Context, id string) (catalog.Title, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, err := a.Catalog.ScanTitleByID(ctx, id)
	if err != nil {
		return catalog.Title{}, err
	}
	all, err := a.Catalog.ScanTitles(ctx)
	if err != nil {
		return catalog.Title{}, err
	}
	s := newScan(a)
	label, root := labelOf(t.Kind), a.root(t.Kind)
	if _, err := readLibrary(library{label, t.Kind, root}, all); err != nil {
		return catalog.Title{}, err
	}
	var scopes []string
	for _, folder := range titleFolders(t, all) {
		info, err := os.Stat(filepath.Join(root, folder))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return catalog.Title{}, &UnavailableError{fmt.Sprintf("cannot read the %s folder %q (%s)", strings.ToLower(label), folder, why(err))}
		}
		if err == nil && info.IsDir() {
			s.matches[t.ID] = append(s.matches[t.ID], folderRef{label: label, kind: t.Kind, root: root, name: folder})
		}
	}
	for _, f := range s.matches[t.ID] {
		scopes = append(scopes, f.label+"/"+f.name)
	}
	for _, f := range tracked(t) {
		scopes = append(scopes, label+"/"+f)
	}
	found, gone, err := s.reconcile(ctx, t)
	if err != nil {
		return catalog.Title{}, err
	}
	if _, err := a.Catalog.ApplyScan(ctx, id, found, gone); err != nil {
		return catalog.Title{}, err
	}
	if len(scopes) > 0 {
		if err := a.Catalog.ReplaceLibraryProblems(ctx, s.problems, scopes...); err != nil {
			return catalog.Title{}, err
		}
	}
	return a.Catalog.Get(ctx, id)
}

// folderRef names a folder of the library that belongs to a title.
type folderRef struct {
	label, kind, root, name string
}

func (f folderRef) dir() string { return filepath.Join(f.root, f.name) }

type scan struct {
	*Adopter
	// matches are the folders found on disk for each title.
	matches map[string][]folderRef
	// states are folders already read for a title adopted in this scan.
	states   map[string]*disk
	problems []catalog.LibraryProblem
	// prints remembers the fingerprint of each file read, since a file of several episodes is one file.
	prints map[string]string
}

func newScan(a *Adopter) *scan {
	return &scan{Adopter: a, matches: map[string][]folderRef{}, states: map[string]*disk{}, prints: map[string]string{}}
}

func (a *Adopter) scanAll(ctx context.Context, first bool) (ScanResult, error) {
	s := newScan(a)
	var res ScanResult
	titles, err := a.Catalog.ScanTitles(ctx)
	if err != nil {
		return res, err
	}
	owner := map[string]map[string]string{"movie": {}, "series": {}}
	for _, t := range titles {
		for _, f := range tracked(t) {
			owner[t.Kind][f] = t.ID
		}
	}
	for _, t := range titles {
		for _, f := range yearFolders(t) {
			if _, taken := owner[t.Kind][f]; !taken {
				owner[t.Kind][f] = t.ID
			}
		}
	}
	known := map[string]bool{}
	for _, t := range titles {
		known[t.ID] = true
	}

	// Folders nobody claims wait until the claimed ones are placed, so that a second folder for a title is the one reported.
	var unclaimed []folderRef
	for _, lib := range a.libraries() {
		entries, err := readLibrary(lib, titles)
		if err != nil {
			return res, err
		}
		for _, e := range entries {
			if !isFolder(lib.root, e) || skipped(e.Name()) {
				continue
			}
			ref := folderRef{label: lib.label, kind: lib.kind, root: lib.root, name: e.Name()}
			if id, ok := owner[lib.kind][e.Name()]; ok {
				s.matches[id] = append(s.matches[id], ref)
			} else {
				unclaimed = append(unclaimed, ref)
			}
		}
	}
	for _, ref := range unclaimed {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		adopted, err := s.newFolder(ctx, ref, known)
		if err != nil {
			return res, err
		}
		if adopted {
			res.Adopted++
		}
	}

	if titles, err = a.Catalog.ScanTitles(ctx); err != nil {
		return res, err
	}
	type change struct {
		title      catalog.ScanTitle
		found      []catalog.FileFound
		gone       []catalog.FileGone
		newlyFound int
	}
	var changes []change
	for _, t := range titles {
		found, gone, err := s.reconcile(ctx, t)
		if err != nil {
			return res, err
		}
		c := change{title: t, found: found, gone: gone}
		for _, f := range found {
			if !trackedRow(t, f.Season, f.Number).Have {
				c.newlyFound++
			}
		}
		changes = append(changes, c)
	}
	for _, c := range changes {
		removed, err := a.Catalog.ApplyScan(ctx, c.title.ID, c.found, c.gone)
		if err != nil {
			return res, err
		}
		res.FoundOnDisk += c.newlyFound
		res.MissingOnDisk += removed
	}
	res.TitlesScanned = len(titles)
	res.Problems = len(s.problems)
	if first {
		return res, a.Catalog.FinishAdoption(ctx, s.problems)
	}
	return res, a.Catalog.ReplaceLibraryProblems(ctx, s.problems)
}

func readLibrary(lib library, titles []catalog.ScanTitle) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(lib.root)
	if err != nil {
		return nil, &UnavailableError{fmt.Sprintf("cannot read the %s folder (%s)", strings.ToLower(lib.label), why(err))}
	}
	if len(entries) == 0 && hasFiles(titles, lib.kind) {
		return nil, &UnavailableError{fmt.Sprintf("the %s folder is empty although the library has files in it; not scanning in case a drive is not mounted", strings.ToLower(lib.label))}
	}
	return entries, nil
}

func trackedRow(t catalog.ScanTitle, season, number int) catalog.TrackedFile {
	for _, f := range t.Files {
		if f.Season == season && f.Number == number {
			return f
		}
	}
	return catalog.TrackedFile{}
}

func hasFiles(titles []catalog.ScanTitle, kind string) bool {
	for _, t := range titles {
		if t.Kind != kind {
			continue
		}
		for _, f := range t.Files {
			if f.Have {
				return true
			}
		}
	}
	return false
}

// tracked is the top-level folders the catalog's files of a title are in.
func tracked(t catalog.ScanTitle) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range t.Files {
		if f.Path == "" {
			continue
		}
		top, _, _ := strings.Cut(f.Path, "/")
		if !seen[top] {
			seen[top] = true
			out = append(out, top)
		}
	}
	return out
}

// yearFolders are the names a folder of the title has when it is named as
// marquee would, with the year: the names claim a folder with no query to TMDB.
func yearFolders(t catalog.ScanTitle) []string {
	if t.Kind == "movie" {
		return []string{naming.MovieFolder(t.Title, t.Year)}
	}
	return []string{fmt.Sprintf("%s (%d)", naming.SeriesFolder(t.Title), t.Year)}
}

// titleFolders are the folders to read for one title with no help from
// TMDB: where its files are, how marquee names its folder, and, for a series
// no other series shares the name of, the plain name an adopted folder has.
func titleFolders(t catalog.ScanTitle, all []catalog.ScanTitle) []string {
	out := append(tracked(t), yearFolders(t)...)
	if t.Kind == "series" {
		plain := naming.SeriesFolder(t.Title)
		sameName := 0
		for _, o := range all {
			if o.Kind == "series" && strings.EqualFold(naming.SeriesFolder(o.Title), plain) {
				sameName++
			}
		}
		if sameName == 1 {
			out = append(out, plain)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (s *scan) problem(path, reason, format string, args ...any) {
	s.problems = append(s.problems, catalog.LibraryProblem{Path: path, Reason: reason, Message: fmt.Sprintf(format, args...)})
}

// newFolder matches a folder the catalog has no claim on to TMDB. A title
// the catalog already holds only gains the folder; a new one is adopted. It
// reports whether a title was adopted.
func (s *scan) newFolder(ctx context.Context, ref folderRef, known map[string]bool) (bool, error) {
	where := ref.label + "/" + ref.name
	name, year := parseFolder(ref.name)
	found, err := s.TMDB.Search(ctx, name)
	if err != nil {
		return false, &UpstreamError{fmt.Errorf("searching TMDB for %q: %w", ref.name, err)}
	}
	var matches []metadata.Summary
	for _, m := range found {
		if m.Kind == ref.kind && strings.EqualFold(naming.SeriesFolder(m.Title), name) && (year == 0 || m.Year == year) {
			matches = append(matches, m)
		}
	}
	switch {
	case len(matches) == 0:
		if year > 0 {
			s.problem(where, catalog.ProblemUnmatched, "No TMDB %s is named %q from %d.", ref.kind, name, year)
		} else {
			s.problem(where, catalog.ProblemUnmatched, "No TMDB %s is named %q.", ref.kind, name)
		}
		return false, nil
	case len(matches) > 1:
		var names []string
		for _, m := range matches {
			names = append(names, describe(m.Title, m.Year))
		}
		s.problem(where, catalog.ProblemAmbiguous, "Matched more than one TMDB title (%s); add the title to pick one.", strings.Join(names, ", "))
		return false, nil
	}

	id := catalog.TitleID(ref.kind, matches[0].TMDBID)
	if held := s.matches[id]; len(held) > 0 {
		s.problem(where, catalog.ProblemAmbiguous, "Matches the same TMDB title as %q, which is already adopted; keep one of the two folders.", held[0].name)
		return false, nil
	}
	if known[id] {
		s.matches[id] = append(s.matches[id], ref)
		return false, nil
	}
	d, err := s.TMDB.Details(ctx, ref.kind, matches[0].TMDBID)
	if errors.Is(err, metadata.ErrNotFound) {
		s.problem(where, catalog.ProblemUnmatched, "TMDB has no details for %q.", name)
		return false, nil
	}
	if err != nil {
		return false, &UpstreamError{fmt.Errorf("fetching %q from TMDB: %w", ref.name, err)}
	}

	n := catalog.NewTitleFromTMDB(d)
	listed := make(map[catalog.EpisodeRef]bool, len(n.Episodes))
	for _, e := range n.Episodes {
		listed[catalog.EpisodeRef{Season: e.Season, Number: e.Number}] = true
	}
	st, err := s.read(ref, d.Title, listed)
	if err != nil {
		// One folder the service cannot read must not keep the rest of the library from being adopted.
		s.problem(where, catalog.ProblemUnmatched, "The folder cannot be read (%v); not adopted.", why(err))
		return false, nil
	}
	var f catalog.Found
	if ref.kind == "movie" {
		f.Movie = len(st.movie) > 0
	} else {
		for r := range st.eps {
			f.Episodes = append(f.Episodes, r)
		}
		sort.Slice(f.Episodes, func(i, j int) bool {
			if f.Episodes[i].Season != f.Episodes[j].Season {
				return f.Episodes[i].Season < f.Episodes[j].Season
			}
			return f.Episodes[i].Number < f.Episodes[j].Number
		})
	}
	if _, _, err := s.Catalog.Adopt(ctx, n, f); err != nil {
		return false, err
	}
	known[id] = true
	s.matches[id] = append(s.matches[id], ref)
	s.states[id] = st
	return true, nil
}

// disk is what a title's folders hold. A path is relative to the library folder.
type disk struct {
	movie []string
	eps   map[catalog.EpisodeRef][]string
	// incomplete is set when part of a folder could not be read, so that a
	// file not seen is not proof of a file deleted.
	incomplete bool
}

// read lists the video files of a title's folder. listed are the episodes
// the series is known to have; a file naming another is a problem.
func (s *scan) read(ref folderRef, name string, listed map[catalog.EpisodeRef]bool) (*disk, error) {
	d := &disk{eps: map[catalog.EpisodeRef][]string{}}
	if ref.kind == "movie" {
		files, err := videos(ref.dir())
		for _, f := range files {
			d.movie = append(d.movie, path.Join(ref.name, f))
		}
		return d, err
	}
	err := s.episodes(ref, name, listed, d)
	return d, err
}

// reconcile compares one title's rows with its folders and lists what to
// record and what is gone. A title with no folder on disk and no tracked
// path has nothing to compare, and is left as it is.
func (s *scan) reconcile(ctx context.Context, t catalog.ScanTitle) ([]catalog.FileFound, []catalog.FileGone, error) {
	folders := s.matches[t.ID]
	if len(folders) == 0 && len(tracked(t)) == 0 {
		return nil, nil, nil
	}
	d := s.states[t.ID]
	if d == nil {
		d = &disk{eps: map[catalog.EpisodeRef][]string{}}
		listed := map[catalog.EpisodeRef]bool{}
		for _, f := range t.Files {
			listed[catalog.EpisodeRef{Season: f.Season, Number: f.Number}] = true
		}
		for _, ref := range folders {
			part, err := s.read(ref, t.Title, listed)
			if err != nil {
				s.problem(ref.label+"/"+ref.name, catalog.ProblemUnmatched, "The folder cannot be read (%v); not rescanned.", why(err))
				return nil, nil, nil
			}
			d.movie = append(d.movie, part.movie...)
			for r, p := range part.eps {
				d.eps[r] = append(d.eps[r], p...)
			}
			d.incomplete = d.incomplete || part.incomplete
		}
	}

	var found []catalog.FileFound
	var gone []catalog.FileGone
	root := s.root(t.Kind)
	for _, f := range t.Files {
		candidates := d.movie
		if t.Kind == "series" {
			candidates = d.eps[catalog.EpisodeRef{Season: f.Season, Number: f.Number}]
		}
		rel := choose(candidates, f.Path)
		if rel == "" {
			if f.Have && !d.incomplete {
				gone = append(gone, catalog.FileGone{Season: f.Season, Number: f.Number, Path: f.Path})
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		fp := f.Fingerprint
		if f.Path != rel || f.Size != info.Size() || fp == "" {
			fp = s.fingerprint(abs)
		}
		if f.Have && f.Path == rel && f.Size == info.Size() && f.Fingerprint == fp {
			continue
		}
		found = append(found, catalog.FileFound{Season: f.Season, Number: f.Number, File: catalog.File{Path: rel, Size: info.Size(), Fingerprint: fp}})
	}
	return found, gone, nil
}

// choose picks the file a movie or episode is on disk, preferring the one
// already recorded so that a second copy never moves it.
func choose(candidates []string, recorded string) string {
	for _, c := range candidates {
		if c == recorded {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

func (s *scan) fingerprint(abs string) string {
	if fp, ok := s.prints[abs]; ok {
		return fp
	}
	fp, err := importer.Fingerprint(abs)
	if err != nil {
		s.Log.Warn("cannot fingerprint a file in the library", "err", why(err))
		fp = ""
	}
	s.prints[abs] = fp
	return fp
}

// videos lists the video files in a folder itself; extras in subfolders do not count.
func videos(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && chooser.IsVideoFile(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// episodes reads the series folder's video files for their episode tokens.
// A file with none, or one naming an episode TMDB does not list, goes on the
// problem list and is otherwise ignored. Season 0 is left out: specials are
// not part of the library.
func (s *scan) episodes(ref folderRef, name string, listed map[catalog.EpisodeRef]bool, d *disk) error {
	where := ref.label + "/" + ref.name
	// A link that points back up the tree would otherwise be followed for ever.
	const maxLinkDepth = 4
	var walk func(root, shown string, depth int) error
	walk = func(root, shown string, depth int) error {
		return filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			rel := path.Join(shown, filepath.ToSlash(relTo(root, p)))
			switch {
			case err != nil && p == root && depth == 0:
				return err
			case err != nil:
				d.incomplete = true
				s.problem(where+"/"+rel, catalog.ProblemUnparseable, "Cannot be read (%v); ignored.", why(err))
				if e != nil && e.IsDir() {
					return fs.SkipDir
				}
				return nil
			case e.IsDir():
				if p != root && skipped(e.Name()) {
					return fs.SkipDir
				}
				return nil
			case e.Type()&fs.ModeSymlink != 0 && isFolder(filepath.Dir(p), e):
				if depth == maxLinkDepth {
					return nil
				}
				target, err := filepath.EvalSymlinks(p)
				if err != nil {
					d.incomplete = true
					s.problem(where+"/"+rel, catalog.ProblemUnparseable, "Cannot be read (%v); ignored.", why(err))
					return nil
				}
				return walk(target, rel, depth+1)
			case !chooser.IsVideoFile(e.Name()):
				return nil
			}
			file := where + "/" + rel
			tok, ok := naming.ParseEpisodeToken(e.Name())
			if !ok {
				s.problem(file, catalog.ProblemUnparseable, "The file name has no S01E01 token; ignored.")
				return nil
			}
			if tok.Season == 0 {
				return nil
			}
			var unknown []string
			for _, ep := range tok.Episodes {
				r := catalog.EpisodeRef{Season: tok.Season, Number: ep}
				d.eps[r] = append(d.eps[r], path.Join(ref.name, rel))
				if !listed[r] {
					unknown = append(unknown, fmt.Sprintf("S%02dE%02d", tok.Season, ep))
				}
			}
			if len(unknown) > 0 {
				s.problem(file, catalog.ProblemUnparseable, "%s not an episode TMDB lists for %q; ignored.", isOrAre(unknown), name)
			}
			return nil
		})
	}
	return walk(ref.dir(), "", 0)
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
