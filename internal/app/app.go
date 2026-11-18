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
	"strconv"
	"time"

	"github.com/davepintodev/marquee/internal/config"
	"github.com/davepintodev/marquee/internal/fake"
	"github.com/davepintodev/marquee/internal/server"
)

// Run loads and checks the configuration, then serves until ctx ends.
// ready, if set, is called with the bound address once the listener is up.
func Run(ctx context.Context, getenv func(string) string, logOut io.Writer, ui fs.FS, ready func(net.Addr)) error {
	log := slog.New(slog.NewTextHandler(logOut, nil))

	var (
		listen string
		api    http.Handler
		msg    string
		attrs  []any
	)
	fakeMode := false
	if v := getenv("MARQUEE_FAKE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return errors.New("MARQUEE_FAKE must be true or false")
		}
		fakeMode = b
	}
	if fakeMode {
		listen = getenv("MARQUEE_LISTEN")
		if listen == "" {
			listen = ":8080"
		}
		f := fake.New(fake.Options{})
		defer f.Close()
		api = f
		msg = "marquee starting in fake mode: the API serves committed fixtures and nothing is downloaded; TMDB key, library paths and the other essential settings are not required"
	} else {
		cfg, err := config.Load(getenv)
		if err != nil {
			return err
		}
		if err := cfg.CheckPaths(); err != nil {
			return err
		}
		listen = cfg.Listen
		msg, attrs = "marquee starting", []any{"config", cfg}
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("MARQUEE_LISTEN: cannot listen on %s: %w", listen, err)
	}
	srv := &http.Server{Handler: server.New(ui, api), ReadHeaderTimeout: 10 * time.Second}

	log.Info(msg, append([]any{"addr", ln.Addr().String()}, attrs...)...)
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
