// Package app wires configuration, checks and the HTTP server together.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/server"
)

// Run loads and checks the configuration, then serves until ctx ends.
// ready, if set, is called with the bound address once the listener is up.
func Run(ctx context.Context, getenv func(string) string, logOut io.Writer, ui fs.FS, ready func(net.Addr)) error {
	log := slog.New(slog.NewTextHandler(logOut, nil))

	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	if err := cfg.CheckPaths(); err != nil {
		return err
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("MARQUEE_LISTEN: cannot listen on %s", cfg.Listen)
	}
	srv := &http.Server{Handler: server.New(ui), ReadHeaderTimeout: 10 * time.Second}

	log.Info("marquee starting", "addr", ln.Addr().String(), "config", cfg)
	if ready != nil {
		ready(ln.Addr())
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
