// Command smokeserve runs the real service with the outside world faked
// (TMDB, AIOStreams, the debrid file server, TorBox, Jellyfin), for the Playwright
// smoke test and the idle-memory measurement. It is a development tool and
// is never part of an image.
//
//	smokeserve [--listen addr]            the whole service, UI included
//	smokeserve --fakes-only --dir <dir>   a seeded library and the fakes'
//	                                      settings, for the real binary
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/davepintodev/marquee/internal/aiostreamsfake"
	"github.com/davepintodev/marquee/internal/app"
	"github.com/davepintodev/marquee/internal/catalog"
	"github.com/davepintodev/marquee/internal/debridfake"
	"github.com/davepintodev/marquee/internal/jellyfinfake"
	"github.com/davepintodev/marquee/internal/pipeline"
	"github.com/davepintodev/marquee/internal/tmdbfake"
	"github.com/davepintodev/marquee/internal/torboxfake"
	"github.com/davepintodev/marquee/web"
)

const (
	duneTMDB = 438631
	duneIMDb = "tt1160419"
	// Arrival is on the wall as trending, for picking a release by hand.
	arrivalIMDb = "tt2543164"
	// The chooser drops movies under 200 MB, so the fake release is a little over.
	duneBytes = 210 << 20
	// Heat has no cached release: only TorBox can fetch it.
	heatTMDB = 949
	heatIMDb = "tt0113277"
	heatHash = "2222222222222222222222222222222222222222"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("smokeserve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:18081", "address of the service")
	fakesOnly := fs.Bool("fakes-only", false, "serve only the fakes and print the settings that point a real marquee at them")
	dir := fs.String("dir", "", "folder for the library, data and temp files (default: a new temporary folder)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := *dir
	if root == "" {
		var err error
		if root, err = os.MkdirTemp("", "marquee-smoke-"); err != nil {
			_, _ = fmt.Fprintln(stderr, "smokeserve:", err)
			return 1
		}
		defer func() { _ = os.RemoveAll(root) }()
	}
	var err error
	if *fakesOnly {
		err = serveFakes(ctx, root, stdout)
	} else {
		err = serveService(ctx, root, *listen, stdout, stderr)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "smokeserve:", err)
		return 1
	}
	return 0
}

func layout(root string) (map[string]string, error) {
	env := map[string]string{
		"MARQUEE_MOVIES_PATH": filepath.Join(root, "movies"),
		"MARQUEE_SERIES_PATH": filepath.Join(root, "series"),
		"MARQUEE_TEMP_PATH":   filepath.Join(root, "tmp"),
		"MARQUEE_DATA_DIR":    filepath.Join(root, "data"),
	}
	for _, k := range []string{"MARQUEE_MOVIES_PATH", "MARQUEE_SERIES_PATH"} {
		if err := os.MkdirAll(env[k], 0o750); err != nil {
			return nil, err
		}
	}
	return env, nil
}

func serveService(ctx context.Context, root, listen string, stdout, stderr io.Writer) error {
	env, err := layout(root)
	if err != nil {
		return err
	}
	tm := tmdbfake.New()
	defer tm.Close()
	tm.Add(tmdbfake.Title{Kind: "movie", ID: duneTMDB, Name: "Dune", Date: "2021-09-15", Overview: "Paul Atreides.", Poster: "/dune-p.jpg", Backdrop: "/dune-b.jpg", IMDb: duneIMDb})
	tm.Add(tmdbfake.Title{Kind: "movie", ID: 7, Name: "Arrival", Date: "2016-11-11", Overview: "Linguist.", Poster: "/arrival-p.jpg", Backdrop: "/arrival-b.jpg", IMDb: arrivalIMDb})
	tm.SetTrending(7)

	cdn := debridfake.New()
	defer cdn.Close()
	// Paced, so the badge is seen moving before the import.
	url := cdn.Add("dune.mkv", debridfake.File{Data: debridfake.MKV(duneBytes), Pace: 2 * time.Millisecond})
	aio := aiostreamsfake.New()
	defer aio.Close()
	aio.Answer("movie", duneIMDb, release(url, duneBytes, "Dune.2021.1080p.WEB-DL.mkv"))
	aio.Answer("movie", arrivalIMDb, release(url, duneBytes, "Arrival.2016.1080p.WEB-DL.mkv"))
	tb := torboxfake.New()
	defer tb.Close()
	tm.Add(tmdbfake.Title{Kind: "movie", ID: heatTMDB, Name: "Heat", Date: "1995-12-15", Overview: "A crew and a detective.", Poster: "/heat-p.jpg", Backdrop: "/heat-b.jpg", IMDb: heatIMDb})
	tb.SetFiles(heatHash, torboxfake.File{ID: 0, Name: "Heat.1995.1080p.WEB-DL.mkv", Size: duneBytes, Body: debridfake.MKV(duneBytes)})
	aio.Answer("movie", heatIMDb, uncachedRelease(heatHash, duneBytes, "Heat.1995.1080p.WEB-DL.mkv"))
	jf := jellyfinfake.New()
	defer jf.Close()

	cfgPath, err := writeConfig(root)
	if err != nil {
		return err
	}
	env["MARQUEE_CONFIG"] = cfgPath
	env["MARQUEE_AIOSTREAMS_MANIFEST_URL"] = aio.ManifestURL()
	env["MARQUEE_TMDB_KEY"] = tmdbfake.Key
	env["MARQUEE_TORBOX_API_KEY"] = torboxfake.Key
	env["MARQUEE_JELLYFIN_URL"], env["MARQUEE_JELLYFIN_KEY"] = jf.URL, jellyfinfake.Key
	env["MARQUEE_LISTEN"] = listen

	go cacheOnTorBox(ctx, tb)

	opts := app.Options{
		TMDBAPIBase: tm.APIBase(), TMDBImageBase: tm.ImageBase(),
		TorBoxAPIBase: tb.APIBase(), TorBoxPoll: 200 * time.Millisecond,
		Transfers: pipeline.Tuning{Poll: 100 * time.Millisecond},
	}
	ready := func(a net.Addr) { _, _ = fmt.Fprintln(stdout, "listening on", a) }
	return app.RunWith(ctx, opts, func(k string) string { return env[k] }, stderr, web.Dist(), ready)
}

// writeConfig stores the config file the service reads its profiles from,
// so the smoke test goes through the same JSON as a real install.
func writeConfig(root string) (string, error) {
	cfg := map[string]any{
		"profiles": map[string]any{
			"tv-safe": map[string]any{
				"qualities":   []string{"WEBDL-1080p", "WEBRip-1080p", "Bluray-1080p", "WEBDL-720p", "WEBRip-720p"},
				"reject":      []string{"truehd", "dts-hd", "dts-x", "dv-p7"},
				"prefer":      []string{"dd+-atmos", "dd+", "hdr10+"},
				"size_limits": map[string]any{"min_mb": 100, "max_mb": 30000},
			},
			"any-1080p": map[string]any{
				"qualities": []string{"Remux-1080p", "Bluray-1080p", "WEBDL-1080p"},
			},
		},
		"default_movie_profile":  "tv-safe",
		"default_series_profile": "tv-safe",
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "marquee.json")
	return path, os.WriteFile(path, b, 0o600)
}

// cacheOnTorBox plays TorBox fetching the first torrent it is asked for, in
// visible steps, so the Caching state is seen before the file is ready.
func cacheOnTorBox(ctx context.Context, tb *torboxfake.Fake) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for len(tb.Magnets()) == 0 {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
	for _, p := range []float64{0.2, 0.55, 0.9} {
		tb.Step(1, p, 5<<20)
		select {
		case <-ctx.Done():
			return
		case <-time.After(1500 * time.Millisecond):
		}
	}
	tb.Present(1)
}

// release is the one AIOStreams result for a title: a cached release whose
// playback link is the debrid fake's.
func release(url string, size int, name string) []byte {
	return fmt.Appendf(nil, `{"success":true,"data":{"results":[{"url":%q,"filename":%q,"folderName":%q,"size":%d,"type":"debrid","service":"torbox","cached":true,"addon":"Fake","infoHash":"1111111111111111111111111111111111111111","parsedFile":{"resolution":"1080p","quality":"WEB-DL","seasons":[],"episodes":[]}}]}}`, url, name, strings.TrimSuffix(name, ".mkv"), size)
}

// uncachedRelease is a result the debrid service does not hold: it has no
// playback link, only the info hash TorBox can be asked to fetch.
func uncachedRelease(hash string, size int, name string) []byte {
	return fmt.Appendf(nil, `{"success":true,"data":{"results":[{"filename":%q,"folderName":%q,"size":%d,"type":"debrid","service":"torbox","cached":false,"addon":"Fake","infoHash":%q,"parsedFile":{"resolution":"1080p","quality":"WEB-DL","seasons":[],"episodes":[]}}]}}`, name, strings.TrimSuffix(name, ".mkv"), size, hash)
}

// serveFakes seeds a small library in root and prints the settings that
// point a real marquee at the fakes, one KEY=value per line. TMDB is left
// at its default: an idle service does not ask it anything.
func serveFakes(ctx context.Context, root string, stdout io.Writer) error {
	env, err := layout(root)
	if err != nil {
		return err
	}
	if err := seed(env["MARQUEE_DATA_DIR"]); err != nil {
		return err
	}
	aio := aiostreamsfake.New()
	defer aio.Close()
	jf := jellyfinfake.New()
	defer jf.Close()
	env["MARQUEE_AIOSTREAMS_MANIFEST_URL"] = aio.ManifestURL()
	env["MARQUEE_TMDB_KEY"] = "not-a-real-key"
	env["MARQUEE_JELLYFIN_URL"], env["MARQUEE_JELLYFIN_KEY"] = jf.URL, jellyfinfake.Key
	for _, k := range []string{
		"MARQUEE_AIOSTREAMS_MANIFEST_URL", "MARQUEE_TMDB_KEY", "MARQUEE_MOVIES_PATH", "MARQUEE_SERIES_PATH",
		"MARQUEE_TEMP_PATH", "MARQUEE_DATA_DIR", "MARQUEE_JELLYFIN_URL", "MARQUEE_JELLYFIN_KEY",
	} {
		_, _ = fmt.Fprintf(stdout, "%s=%s\n", k, env[k])
	}
	<-ctx.Done()
	return nil
}

// seed stores a small library: six monitored movies and two series of
// ten episodes in each of three seasons, all wanted.
func seed(dataDir string) error {
	store, err := catalog.Open(dataDir, nil)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	for i := 1; i <= 6; i++ {
		_, _, err := store.Add(ctx, catalog.NewTitle{Kind: "movie", TMDBID: 1000 + i, Title: fmt.Sprintf("Movie %d", i), Year: 2000 + i, IMDbID: fmt.Sprintf("tt%07d", i)})
		if err != nil {
			return err
		}
	}
	for i := 1; i <= 2; i++ {
		n := catalog.NewTitle{Kind: "series", TMDBID: 2000 + i, Title: fmt.Sprintf("Series %d", i), Year: 2010 + i, IMDbID: fmt.Sprintf("tt%07d", 100+i), TVDBID: 3000 + i}
		for s := 1; s <= 3; s++ {
			for e := 1; e <= 10; e++ {
				n.Episodes = append(n.Episodes, catalog.NewEpisode{Season: s, Number: e, Name: fmt.Sprintf("Episode %d", e), AirDate: fmt.Sprintf("%d-01-%02d", 2011+s, e)})
			}
		}
		if _, _, err := store.Add(ctx, n); err != nil {
			return err
		}
	}
	return nil
}
