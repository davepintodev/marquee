package catalog_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

func TestTheAboutCacheMigrationLeavesEarlierRowsAlone(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := catalog.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	title := addMovie(t, s, 1)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, catalog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE about_cache; DROP TABLE people; PRAGMA user_version = 9`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = catalog.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Get(ctx, title); err != nil {
		t.Errorf("the title written before the migration: %v", err)
	}
	if err := s.RememberAbout(ctx, title, []byte(`{"a":1}`), nil); err != nil {
		t.Fatal(err)
	}
}

func TestAboutIsRememberedWithItsTimeAndPeoplePhotosByPerson(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	s, err := catalog.Open(t.TempDir(), func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, _, found, err := s.CachedAbout(ctx, "movie-1"); err != nil || found {
		t.Fatalf("nothing remembered yet: %v %v", found, err)
	}
	people := []catalog.PersonPhoto{{ID: 7, Path: "/a.jpg"}, {ID: 8, Path: "/b.jpg"}}
	if err := s.RememberAbout(ctx, "movie-1", []byte(`{"v":1}`), people); err != nil {
		t.Fatal(err)
	}
	body, when, found, err := s.CachedAbout(ctx, "movie-1")
	if err != nil || !found || string(body) != `{"v":1}` || !when.Equal(at) {
		t.Fatalf("cached: %s %v %v %v", body, when, found, err)
	}

	// A later fetch replaces the document and a photo TMDB changed.
	at = at.Add(time.Hour)
	if err := s.RememberAbout(ctx, "movie-1", []byte(`{"v":2}`), []catalog.PersonPhoto{{ID: 7, Path: "/new.jpg"}}); err != nil {
		t.Fatal(err)
	}
	body, when, _, _ = s.CachedAbout(ctx, "movie-1")
	if string(body) != `{"v":2}` || !when.Equal(at) {
		t.Errorf("replaced: %s %v", body, when)
	}
	if p, ok, err := s.PersonPhotoPath(ctx, 7); err != nil || !ok || p != "/new.jpg" {
		t.Errorf("person 7: %q %v %v", p, ok, err)
	}
	if p, ok, _ := s.PersonPhotoPath(ctx, 8); !ok || p != "/b.jpg" {
		t.Errorf("another title's listing keeps person 8: %q %v", p, ok)
	}
	if _, ok, _ := s.PersonPhotoPath(ctx, 9); ok {
		t.Error("an unknown person has no photo")
	}
}

func TestAboutOlderThanAMonthIsForgottenWhenANewOneIsRemembered(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s, err := catalog.Open(t.TempDir(), func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.RememberAbout(ctx, "movie-1", []byte(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	at = at.Add(29 * 24 * time.Hour)
	if err := s.RememberAbout(ctx, "movie-2", []byte(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, found, _ := s.CachedAbout(ctx, "movie-1"); !found {
		t.Error("29 days old is kept")
	}
	at = at.Add(3 * 24 * time.Hour)
	if err := s.RememberAbout(ctx, "movie-3", []byte(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, found, _ := s.CachedAbout(ctx, "movie-1"); found {
		t.Error("32 days old is dropped")
	}
	if _, _, found, _ := s.CachedAbout(ctx, "movie-2"); !found {
		t.Error("3 days old is kept")
	}
}
