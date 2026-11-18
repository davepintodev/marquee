package catalog_test

import (
	"context"
	"testing"

	"github.com/davepintodev/marquee/internal/catalog"
)

// External ids are stored for the Source module and no API route shows
// them yet, so this is the one place they are checked.
func TestExternalIDsAndMigrationsSurviveReopening(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := catalog.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, created, err := s.Add(ctx, catalog.NewTitle{Kind: "series", TMDBID: 5, Title: "Show", IMDbID: "tt123", TVDBID: 456})
	if err != nil || !created {
		t.Fatalf("add: %v %v", created, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = catalog.Open(dir, nil)
	if err != nil {
		t.Fatalf("reopening runs no migration twice: %v", err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.Get(ctx, "series-5")
	if err != nil {
		t.Fatal(err)
	}
	if got.IMDbID != "tt123" || got.TVDBID != 456 {
		t.Errorf("external ids: %+v", got)
	}
}
