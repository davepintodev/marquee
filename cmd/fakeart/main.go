// Command fakeart downloads real posters and backdrops for the fake's
// fixture titles into a local folder; point MARQUEE_FAKE_ART_DIR at it.
// The art is TMDB's, so it stays on this machine and out of the repository.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/fake"
	"github.com/davepintodev/marquee/internal/fakeart"
	"github.com/davepintodev/marquee/internal/metadata"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Getenv, os.Args[1:], os.Stdout, os.Stderr, metadata.Options{}))
}

// tmdb carries the test seams (base URLs); run adds the key.
func run(ctx context.Context, getenv func(string) string, args []string, stdout, stderr io.Writer, tmdb metadata.Options) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "fakeart: usage: fakeart <art-dir>")
		return 2
	}
	key := getenv("MARQUEE_TMDB_KEY")
	if key == "" {
		_, _ = fmt.Fprintln(stderr, "fakeart: MARQUEE_TMDB_KEY is not set")
		return 2
	}
	tmdb.Key = config.Secret(key)
	if err := fakeart.Fetch(ctx, metadata.New(tmdb), fake.Titles(), args[0], stdout); err != nil {
		if errors.Is(err, metadata.ErrRejected) {
			err = errors.New("TMDB refused MARQUEE_TMDB_KEY")
		}
		_, _ = fmt.Fprintln(stderr, "fakeart:", err)
		return 1
	}
	return 0
}
