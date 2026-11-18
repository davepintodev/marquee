package config

import (
	"fmt"
	"os"
	"syscall"
)

// The final move into a library must be an atomic rename, so the temp
// path has to share a filesystem with both libraries.
func (c Config) CheckPaths() error {
	for _, p := range []struct{ env, path string }{
		{"MARQUEE_MOVIES_PATH", c.MoviesPath},
		{"MARQUEE_SERIES_PATH", c.SeriesPath},
	} {
		st, err := os.Stat(p.path)
		if err != nil || !st.IsDir() {
			return fmt.Errorf("%s: %s is not an existing directory", p.env, p.path)
		}
	}
	for _, p := range []struct{ env, path string }{
		{"MARQUEE_TEMP_PATH", c.TempPath},
		{"MARQUEE_DATA_DIR", c.DataDir},
	} {
		if err := os.MkdirAll(p.path, 0o750); err != nil {
			return fmt.Errorf("%s: cannot create %s", p.env, p.path)
		}
	}

	tempDev, err := device(c.TempPath)
	if err != nil {
		return fmt.Errorf("MARQUEE_TEMP_PATH: cannot inspect %s", c.TempPath)
	}
	for _, p := range []struct{ env, path string }{
		{"MARQUEE_MOVIES_PATH", c.MoviesPath},
		{"MARQUEE_SERIES_PATH", c.SeriesPath},
	} {
		dev, err := device(p.path)
		if err != nil {
			return fmt.Errorf("%s: cannot inspect %s", p.env, p.path)
		}
		if dev != tempDev {
			return fmt.Errorf("MARQUEE_TEMP_PATH %s is on a different filesystem than %s (%s); "+
				"put the temp path on the library's filesystem so imports can be an atomic rename",
				c.TempPath, p.env, p.path)
		}
	}
	return nil
}

func device(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil //nolint:unconvert // Dev's width differs per OS
}
