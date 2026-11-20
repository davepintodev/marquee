package httpapi

import (
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/davepintodev/marquee/internal/metadata"
)

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// TMDB's own size names: a 2:3 poster and a 16:9 backdrop. The backdrop is the full-width header of the title
// screen, up to 1920 CSS pixels (twice that on a retina screen), so it needs TMDB's w1280, not the w780 that
// only covers a tile.
func imageSize(kind string) string {
	if kind == "backdrop" {
		return "w1280"
	}
	return "w500"
}

const placeholderPoster = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 300"><rect width="200" height="300" fill="#2a2a2e"/></svg>`
const placeholderBackdrop = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 320 180"><rect width="320" height="180" fill="#2a2a2e"/></svg>`

// image serves a poster or backdrop from the disk cache, fetching it from
// TMDB the first time. A title TMDB has no picture for gets a plain
// placeholder, so a tile never shows a broken image.
func (a *api) image(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("kind"), r.PathValue("titleId")
	tkind, tmdbID, ok := parseTitleID(id)
	if (kind != "poster" && kind != "backdrop") || !ok {
		fail(w, http.StatusNotFound, "no such image")
		return
	}
	path, found, err := a.Catalog.ImagePath(r.Context(), id, kind)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if !found {
		m, err := a.TMDB.Summary(r.Context(), tkind, tmdbID)
		if err != nil {
			a.upstream(w, r, err)
			return
		}
		if err := a.Catalog.RememberImages(r.Context(), imageRefs(id, m)); err != nil {
			a.internal(w, r, err)
			return
		}
		path = m.PosterPath
		if kind == "backdrop" {
			path = m.BackdropPath
		}
	}
	if path == "" {
		svg := placeholderPoster
		if kind == "backdrop" {
			svg = placeholderBackdrop
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write([]byte(svg))
		return
	}
	a.serveImage(w, r, imageSize(kind), path)
}

// serveImage serves a TMDB image from the disk cache, fetching it first when the cache has none.
func (a *api) serveImage(w http.ResponseWriter, r *http.Request, size, path string) {
	file, err := a.Images.Open(r.Context(), size, path)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			fail(w, http.StatusNotFound, "TMDB no longer has this image")
			return
		}
		if errors.Is(err, metadata.ErrLocal) {
			a.internal(w, r, err)
			return
		}
		a.upstream(w, r, err)
		return
	}
	f, err := os.Open(file)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		a.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", metadata.ContentType(file))
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeContent(w, r, "", st.ModTime(), f)
}
