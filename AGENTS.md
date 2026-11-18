# marquee

One small Go service with one React screen: it searches AIOStreams for releases, downloads the chosen file over HTTP into a media library and keeps retrying until a title is complete.

## Conventions

- Go + SQLite (the pure-Go `modernc.org/sqlite` driver, so the container needs no cgo; schema changes are appended to the embedded migration list in `internal/catalog`, never edited), React + Vite in `web/`, built and embedded in the binary: one process, one port, one container.
- Idle memory stays under 80 MB RSS. Downloaded bytes go to disk, never into memory.
- Configuration comes from a JSON file (`MARQUEE_CONFIG`) or `MARQUEE_*` environment variables, which override it. Missing essentials stop startup with a message naming the setting. Secrets use `config.Secret` and are redacted in logs, errors and dumps; never format a revealed secret.
- The temp path must be on the library's filesystem so the final move is an atomic rename.
- Tests drive the real service in-process over HTTP with fakes for the outside world; no real network in tests. `internal/tmdbfake` is the TMDB fake (counts requests; can fail, rate-limit or hang), `app.Options` injects its URLs, the clock and the rate-limit sleeper, and `internal/apitest` checks real responses against the contract. Pure ports (naming, parsers) get table tests.
- Comments only for a non-obvious why. No hostnames, domains, IPs, usernames or home paths anywhere; use placeholders.
- Commits: `<type>(<scope>): <summary>`, imperative, lowercase, with the why in the body.

## API contract and fake

`api/openapi.yaml` is the one description of the HTTP API; change it first, then run `make api`. `marquee --fake` (or `MARQUEE_FAKE=1`) serves the whole contract from the fixtures in `internal/fake/fixtures/`, with no essential settings needed, so the UI can be built against it. In real mode (no `--fake`) the routes of tickets not built yet answer 501 in the contract's error shape. A new operation needs a fake route and a case in `internal/fake/contract_test.go`, or the test fails.

## Fresh checkout

Needs Go (see `go.mod`), Node 24 with npm, and `golangci-lint` v2.15+ on `PATH`. `make gate` runs `npm ci` in `web/` by itself the first time; run `cd web && npm ci` to set up the web side without the gate.

## Previews

To show a running build, serve it from the one binary, not the Vite dev server (which rejects unknown `Host` headers and needs a second port):

```sh
make build
MARQUEE_LISTEN=:8080 bin/marquee --fake
```

- Serve over Tailscale with plain HTTP: the person looking opens `http://<machine-tailscale-name-or-ip>:8080` from a device on the tailnet. Bind to all interfaces (`:8080`, no host before the colon), no TLS, no `tailscale serve --https`, no public tunnel or funnel. Hand over that address and the tab, never `localhost`.
- Preview against the fake (`--fake`) unless the ticket needs real mode; real mode needs the essential settings, which never go in a command line you print, a log or a PR.
- Check the page at laptop and phone width before handing it over. `make smoke` (not part of the gate) takes the Playwright screenshots and recordings.
- Stop the server when the look is signed off.

## Gate

`make gate` is the one check before a commit and the CI job named `gate`. It runs, in order:

1. `go vet`
2. `golangci-lint run`
3. `go test` (includes the contract test: `api/openapi.yaml` must validate and every operation must match the fake)
4. web API types check: `web/src/api/schema.d.ts` is generated from the contract and committed; the gate regenerates it and fails when it differs (`make api` rewrites it)
5. web typecheck (`tsc --noEmit`)
6. web lint (`eslint`)
7. web tests (`vitest`)
8. build: Vite build into `web/dist`, then `go build -o bin/marquee ./cmd/marquee`

CI also runs a `gitleaks` job.
