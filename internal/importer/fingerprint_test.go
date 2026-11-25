package importer_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/davepintodev/marquee/internal/importer"
)

const mib = 1 << 20

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + i/251)
	}
	return b
}

func sum(size int, parts ...[]byte) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d:", size)
	for _, p := range parts {
		h.Write(p)
	}
	return fmt.Sprintf("%d:%x", size, h.Sum(nil))
}

func writeFile(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.mkv")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFingerprintHashesWholeSmallFilesAndThreeChunksOfBigOnes(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
		want func(d []byte) string
	}{
		{"empty", 0, func(d []byte) string { return sum(0) }},
		{"one byte", 1, func(d []byte) string { return sum(1, d) }},
		{"exactly 3 MiB", 3 * mib, func(d []byte) string { return sum(3*mib, d) }},
		{"one byte over 3 MiB", 3*mib + 1, func(d []byte) string {
			size := len(d)
			mid := (size - mib) / 2
			return sum(size, d[:mib], d[mid:mid+mib], d[size-mib:])
		}},
		{"10 MiB", 10 * mib, func(d []byte) string {
			size := len(d)
			mid := (size - mib) / 2
			return sum(size, d[:mib], d[mid:mid+mib], d[size-mib:])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := pattern(tc.size)
			got, err := importer.Fingerprint(writeFile(t, data))
			if err != nil {
				t.Fatal(err)
			}
			if want := tc.want(data); got != want {
				t.Errorf("fingerprint = %s, want %s", got, want)
			}
		})
	}
}

func TestFingerprintIsDeterministicAndSeesEachChunkAndTheSize(t *testing.T) {
	data := pattern(10 * mib)
	base, err := importer.Fingerprint(writeFile(t, data))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := importer.Fingerprint(writeFile(t, data))
	if again != base {
		t.Errorf("same bytes gave %s and %s", base, again)
	}
	mid := (len(data) - mib) / 2
	for name, off := range map[string]int{"first": 5, "middle": mid + 7, "last": len(data) - 3} {
		d := append([]byte(nil), data...)
		d[off] ^= 0xff
		got, _ := importer.Fingerprint(writeFile(t, d))
		if got == base {
			t.Errorf("a changed byte in the %s chunk left the fingerprint unchanged", name)
		}
	}
	d := append([]byte(nil), data...)
	d[mib+5] ^= 0xff
	if got, _ := importer.Fingerprint(writeFile(t, d)); got != base {
		t.Error("a byte between the chunks must not matter: the fingerprint is a fast sample")
	}
	if got, _ := importer.Fingerprint(writeFile(t, append(data, 0))); got == base {
		t.Error("a longer file got the same fingerprint")
	}
}

func TestFingerprintOfAMissingFileFails(t *testing.T) {
	if _, err := importer.Fingerprint(filepath.Join(t.TempDir(), "nope.mkv")); err == nil {
		t.Error("expected an error")
	}
}
