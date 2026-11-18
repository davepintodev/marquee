package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestExitsNonZeroNamingTheMissingSetting(t *testing.T) {
	env := map[string]string{
		"MARQUEE_AIOSTREAMS_MANIFEST_URL": "https://aio.example.test/SECRET123/manifest.json",
		"MARQUEE_TMDB_KEY":                "tmdb-SECRET456",
	}
	var stderr bytes.Buffer
	code := run(context.Background(), func(k string) string { return env[k] }, nil, &stderr)
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

func TestFakeFlagSkipsTheEssentialSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var stderr bytes.Buffer
	env := map[string]string{"MARQUEE_LISTEN": "127.0.0.1:0"}
	done := make(chan int, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, []string{"--fake"}, &stderr) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
}

func TestUnknownFlagExitsNonZero(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), func(string) string { return "" }, []string{"--nope"}, &stderr); code == 0 {
		t.Fatal("want a non-zero exit")
	}
}
