package download

import (
	"bytes"
	"errors"
	"io"
	"os"
)

// CheckVideo reads the head of path and fails with ReasonNotVideo unless
// it begins like a video container: Matroska/WebM, MP4/MOV, AVI, MPEG
// transport or program stream, or ASF/WMV.
func CheckVideo(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 400)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	if !looksLikeVideo(head[:n]) {
		return &Failure{ReasonNotVideo, "the file is not a video (its first bytes are not a known video container)"}
	}
	return nil
}

var atomsAfterSize = [][]byte{[]byte("ftyp"), []byte("moov"), []byte("mdat"), []byte("free"), []byte("wide"), []byte("skip")}

func looksLikeVideo(b []byte) bool {
	switch {
	case bytes.HasPrefix(b, []byte{0x1A, 0x45, 0xDF, 0xA3}): // Matroska, WebM
		return true
	case len(b) >= 12 && bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("AVI ")):
		return true
	case bytes.HasPrefix(b, []byte{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11}): // ASF
		return true
	case bytes.HasPrefix(b, []byte{0x00, 0x00, 0x01, 0xBA}), bytes.HasPrefix(b, []byte{0x00, 0x00, 0x01, 0xB3}): // MPEG PS, ES
		return true
	case len(b) >= 8:
		for _, a := range atomsAfterSize {
			if bytes.Equal(b[4:8], a) { // MP4, MOV
				return true
			}
		}
	}
	return transportStream(b, 0, 188) || transportStream(b, 4, 192) // .ts, .m2ts
}

// transportStream wants sync bytes at three consecutive packet starts.
func transportStream(b []byte, first, size int) bool {
	for i := 0; i < 3; i++ {
		at := first + i*size
		if at >= len(b) || b[at] != 0x47 {
			return false
		}
	}
	return true
}
