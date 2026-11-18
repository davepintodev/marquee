package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
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

// Paths are the folders marquee reads and writes, for Health to look at
// after startup. Its messages are plain words for the owner and never
// repeat a system error.
type Paths struct{ Movies, Series, Temp string }

func (c Config) Paths() Paths {
	return Paths{Movies: c.MoviesPath, Series: c.SeriesPath, Temp: c.TempPath}
}

// LibraryProblem says what is wrong with the movie and series folders, or
// is empty. It never creates anything: dry-run promises no writes there.
func (p Paths) LibraryProblem() string {
	var out []string
	for _, f := range []struct{ name, path string }{{"movies", p.Movies}, {"series", p.Series}} {
		if msg := folderProblem(f.name, f.path); msg != "" {
			out = append(out, msg)
		}
	}
	return strings.Join(out, " ")
}

// TempProblem says what is wrong with the temp folder, including living on
// another filesystem than a library, or is empty.
func (p Paths) TempProblem() string { return p.tempProblem(device) }

func (p Paths) tempProblem(dev func(string) (uint64, error)) string {
	if msg := folderProblem("temp", p.Temp); msg != "" {
		return msg
	}
	tempDev, err := dev(p.Temp)
	if err != nil {
		return "marquee cannot tell which filesystem the temp folder is on."
	}
	var elsewhere []string
	for _, f := range []struct{ name, path string }{{"movies", p.Movies}, {"series", p.Series}} {
		// A library folder that cannot be used is the library check's to report.
		if folderProblem(f.name, f.path) != "" {
			continue
		}
		d, err := dev(f.path)
		if err != nil {
			return "marquee cannot tell which filesystem the " + f.name + " folder is on."
		}
		if d != tempDev {
			elsewhere = append(elsewhere, "the "+f.name+" folder")
		}
	}
	if len(elsewhere) > 0 {
		return "The temp folder is on a different filesystem than " + strings.Join(elsewhere, " and ") +
			", so an import cannot be an atomic move; put the temp path on the library's filesystem."
	}
	return ""
}

const (
	accessExec  = 1
	accessWrite = 2
	accessRead  = 4
)

// folderProblem uses access(2), which asks the kernel without touching the folder.
func folderProblem(name, path string) string {
	st, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) || (err == nil && !st.IsDir()):
		return "The " + name + " folder does not exist (" + path + ")."
	case err != nil:
		return "marquee cannot inspect the " + name + " folder (" + path + ")."
	case syscall.Access(path, accessRead|accessExec) != nil:
		return "marquee cannot read the " + name + " folder (" + path + "); check its permissions."
	case syscall.Access(path, accessWrite) != nil:
		return "marquee cannot write to the " + name + " folder (" + path + "); check its permissions."
	}
	return ""
}
