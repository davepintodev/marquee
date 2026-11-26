package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/importer"
	"github.com/davepintodev/marquee/internal/subfit"
)

func (a *api) libraryRoot(kind string) string {
	if kind == "movie" {
		return a.Paths.Movies
	}
	return a.Paths.Series
}

// checkDeletable refuses, before any file goes, a path that would leave the
// library. It answers the error itself and returns false when it refuses.
func (a *api) checkDeletable(w http.ResponseWriter, r *http.Request, root string, paths ...string) bool {
	for _, p := range paths {
		err := importer.CheckMediaFile(root, p)
		switch {
		case err == nil:
		case errors.Is(err, importer.ErrUnsafePath):
			a.Log.Warn("refusing to delete a path that is not a video in the library", "path", r.URL.Path)
			fail(w, http.StatusConflict, "marquee will not delete "+p+" because it is not a video file inside the library folder")
			return false
		default:
			a.internal(w, r, err)
			return false
		}
	}
	return true
}

// deleteTitleFiles deletes every file the title has, with its sidecars, and
// the folders that end up empty. It answers the error itself and returns
// false when the title must stay as it is.
func (a *api) deleteTitleFiles(w http.ResponseWriter, r *http.Request, id string) bool {
	files, err := a.Catalog.TrackedFiles(r.Context(), id)
	if errors.Is(err, catalog.ErrNotFound) {
		fail(w, http.StatusNotFound, "no such title in the library")
		return false
	}
	if err != nil {
		a.internal(w, r, err)
		return false
	}
	if a.DryRun {
		fail(w, http.StatusConflict, "dry-run is on, so marquee will not delete files from disk")
		return false
	}
	kind, _, _ := parseTitleID(id)
	root := a.libraryRoot(kind)
	var paths []string
	unknown := 0
	for _, f := range files {
		switch {
		case f.Path == "":
			unknown++
		case !slices.Contains(paths, f.Path):
			paths = append(paths, f.Path)
		}
	}
	if unknown > 0 {
		fail(w, http.StatusConflict, fmt.Sprintf("marquee has not recorded where %d of this title's files are, so it cannot delete them; rescan the title first, or remove it without deleting files", unknown))
		return false
	}
	if !a.checkDeletable(w, r, root, paths...) {
		return false
	}
	for _, p := range paths {
		if err := importer.DeleteMediaFile(root, p); err != nil {
			a.internal(w, r, err)
			return false
		}
	}
	for _, p := range paths {
		a.Subfit.Delete(subfit.Library(kind), p)
	}
	for _, p := range paths {
		if err := importer.PruneEmptyDirs(root, p); err != nil {
			a.Log.Warn("cannot remove an empty folder", "err", err)
		}
	}
	return true
}

// wrongFile deletes the file of a movie, or of an episode named in the
// path, and makes it wanted again.
func (a *api) wrongFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReSearch *bool `json:"reSearch"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
			fail(w, http.StatusBadRequest, "body must be {\"reSearch\": true|false} or empty")
			return
		}
	}
	reSearch := req.ReSearch == nil || *req.ReSearch
	id := r.PathValue("id")
	season, number := 0, 0
	episodic := r.PathValue("episodeId") != ""
	if episodic {
		m := episodeIDRe.FindStringSubmatch(r.PathValue("episodeId"))
		if m == nil {
			fail(w, http.StatusNotFound, "no such episode")
			return
		}
		season, number = atoi(m[1]), atoi(m[2])
	}
	kind, _, ok := parseTitleID(id)
	if !ok {
		fail(w, http.StatusNotFound, "no such title in the library")
		return
	}
	if kind == "series" && !episodic {
		fail(w, http.StatusBadRequest, "a series has no file of its own; mark one of its episodes instead")
		return
	}
	f, err := a.Catalog.FileOf(r.Context(), id, season, number)
	if errors.Is(err, catalog.ErrNotFound) {
		fail(w, http.StatusNotFound, "no such title or episode in the library")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	switch {
	case a.DryRun:
		fail(w, http.StatusConflict, "dry-run is on, so marquee will not delete files from disk")
		return
	case !f.Have:
		fail(w, http.StatusConflict, "there is no file to delete: it is already missing")
		return
	case f.Path == "":
		fail(w, http.StatusConflict, "marquee has not recorded where this file is, so it cannot delete it; rescan the title first")
		return
	}
	root := a.libraryRoot(kind)
	if !a.checkDeletable(w, r, root, f.Path) {
		return
	}
	if err := importer.DeleteMediaFile(root, f.Path); err != nil {
		a.internal(w, r, err)
		return
	}
	a.Subfit.Delete(subfit.Library(kind), f.Path)
	_, err = a.Catalog.ResetWrongFile(r.Context(), catalog.WrongFile{TitleID: id, Season: season, Number: number, Path: f.Path, ReSearch: reSearch})
	switch {
	case errors.Is(err, catalog.ErrFileChanged), errors.Is(err, catalog.ErrNotFound):
		fail(w, http.StatusConflict, "the file changed while it was being deleted; rescan the title and look again")
		return
	case err != nil:
		a.internal(w, r, err)
		return
	}
	if reSearch && a.Wake != nil {
		a.Wake()
	}
	a.refreshLibrary(r.Context())
	t, err := a.Catalog.Get(r.Context(), id)
	a.titleReply(w, r, t, err)
}

func (a *api) refreshLibrary(ctx context.Context) {
	if a.Transfers != nil {
		a.Transfers.RefreshLibrary(ctx)
	}
}

func (a *api) ownsFile(ctx context.Context, id string, season, number int) (bool, error) {
	f, err := a.Catalog.FileOf(ctx, id, season, number)
	if errors.Is(err, catalog.ErrNotFound) {
		return false, nil
	}
	return f.Have, err
}
