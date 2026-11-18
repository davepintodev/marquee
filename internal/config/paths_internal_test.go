package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Directories made in one test always share a filesystem, so the comparison
// is driven through an injected device lookup.
func TestTempProblemsFlagsALibraryOnAnotherFilesystem(t *testing.T) {
	root := t.TempDir()
	p := Paths{Movies: filepath.Join(root, "movies"), Series: filepath.Join(root, "series"), Temp: filepath.Join(root, "tmp")}
	for _, d := range []string{p.Movies, p.Series, p.Temp} {
		if err := os.Mkdir(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	dev := func(m map[string]uint64) func(string) (uint64, error) {
		return func(path string) (uint64, error) { return m[path], nil }
	}
	for _, tc := range []struct {
		name string
		dev  func(string) (uint64, error)
		want []string // every word the message must hold; nil means no problem
	}{
		{"all on one filesystem", dev(map[string]uint64{p.Temp: 1, p.Movies: 1, p.Series: 1}), nil},
		{"series elsewhere", dev(map[string]uint64{p.Temp: 1, p.Movies: 1, p.Series: 2}), []string{"different filesystem", "series folder"}},
		{"movies elsewhere", dev(map[string]uint64{p.Temp: 1, p.Movies: 2, p.Series: 1}), []string{"different filesystem", "movies folder"}},
		{"temp elsewhere", dev(map[string]uint64{p.Temp: 9, p.Movies: 1, p.Series: 1}), []string{"different filesystem", "movies folder", "series folder"}},
		{"cannot inspect", func(string) (uint64, error) { return 0, errors.New("boom") }, []string{"cannot tell"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := p.tempProblem(tc.dev)
			if tc.want == nil {
				if got != "" {
					t.Fatalf("problem = %q, want none", got)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("problem %q lacks %q", got, w)
				}
			}
			if strings.Contains(got, "boom") {
				t.Errorf("problem repeats the system error: %q", got)
			}
		})
	}
}
