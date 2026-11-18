package download_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davepintodev/marquee/internal/download"
)

func TestCheckVideo(t *testing.T) {
	pad := func(head []byte) []byte { return append(head, make([]byte, 500)...) }
	ts := func(offset, size int) []byte {
		b := make([]byte, 500)
		for _, at := range []int{offset, offset + size, offset + 2*size} {
			b[at] = 0x47
		}
		return b
	}
	for _, tc := range []struct {
		name  string
		data  []byte
		video bool
	}{
		{"matroska", pad([]byte{0x1A, 0x45, 0xDF, 0xA3}), true},
		{"mp4", pad([]byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}), true},
		{"mov", pad([]byte{0, 0, 0, 0x08, 'w', 'i', 'd', 'e'}), true},
		{"avi", pad([]byte("RIFF\x00\x00\x00\x00AVI LIST")), true},
		{"asf", pad([]byte{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11}), true},
		{"mpeg program stream", pad([]byte{0, 0, 1, 0xBA}), true},
		{"transport stream", ts(0, 188), true},
		{"m2ts", ts(4, 192), true},
		{"windows executable", pad([]byte("MZ\x90\x00")), false},
		{"zip", pad([]byte("PK\x03\x04")), false},
		{"7z", pad([]byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}), false},
		{"html error page", pad([]byte("<!DOCTYPE html><html>")), false},
		{"rar", pad([]byte("Rar!\x1a\x07\x00")), false},
		{"empty", nil, false},
		{"one transport sync byte only", pad([]byte{0x47}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "f")
			if err := os.WriteFile(p, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			err := download.CheckVideo(p)
			if tc.video && err != nil {
				t.Fatalf("a video was rejected: %v", err)
			}
			if !tc.video {
				f, ok := download.AsFailure(err)
				if !ok || f.Reason != download.ReasonNotVideo {
					t.Fatalf("err = %v, want a not_video failure", err)
				}
			}
		})
	}
	if err := download.CheckVideo(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing file passed")
	}
}
