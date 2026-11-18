// Command marquee is the single-binary media service.
package main

import (
	"context"
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
	os.Exit(run(ctx, os.Getenv, os.Stderr))
}

func run(ctx context.Context, getenv func(string) string, stderr io.Writer) int {
	if err := app.Run(ctx, getenv, stderr, web.Dist(), nil); err != nil {
		_, _ = fmt.Fprintln(stderr, "marquee:", err)
		return 1
	}
	return 0
}
