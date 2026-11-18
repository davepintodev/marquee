package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestExitsNonZeroNamingTheMissingSetting(t *testing.T) {
	env := map[string]string{
		"MARQUEE_AIOSTREAMS_MANIFEST_URL": "https://aio.example.test/SECRET123/manifest.json",
		"MARQUEE_TMDB_KEY":                "tmdb-SECRET456",
	}
	var stderr bytes.Buffer
	code := run(context.Background(), func(k string) string { return env[k] }, &stderr)
	if code == 0 {
		t.Fatal("want a non-zero exit")
	}
	out := stderr.String()
	for _, want := range []string{"MARQUEE_MOVIES_PATH", "MARQUEE_SERIES_PATH", "MARQUEE_TEMP_PATH", "MARQUEE_DATA_DIR"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr must name %s, got %q", want, out)
		}
	}
	if strings.Contains(out, "SECRET") {
		t.Errorf("stderr leaks a secret: %q", out)
	}
}
