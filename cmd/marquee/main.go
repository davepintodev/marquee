// Command marquee is the single-binary media service.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Getenv, os.Args[1:], os.Stderr))
}

func run(ctx context.Context, getenv func(string) string, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("marquee", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fakeFlag := fs.Bool("fake", false, "serve fixture data instead of the real backend (same as MARQUEE_FAKE=1)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *fakeFlag {
		inner := getenv
		getenv = func(k string) string {
			if k == "MARQUEE_FAKE" {
				return "true"
			}
			return inner(k)
		}
	}
	if err := app.Run(ctx, getenv, stderr, web.Dist(), nil); err != nil {
		_, _ = fmt.Fprintln(stderr, "marquee:", err)
		return 1
	}
	return 0
}
