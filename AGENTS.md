# marquee

One small Go service with one React screen: it searches AIOStreams for releases, downloads the chosen file over HTTP into a media library and keeps retrying until a title is complete.

## Conventions

- Go + SQLite (SQLite arrives with the catalog), React + Vite in `web/`, built and embedded in the binary: one process, one port, one container.
- Idle memory stays under 80 MB RSS. Downloaded bytes go to disk, never into memory.
- Configuration comes from a JSON file (`MARQUEE_CONFIG`) or `MARQUEE_*` environment variables, which override it. Missing essentials stop startup with a message naming the setting. Secrets use `config.Secret` and are redacted in logs, errors and dumps; never format a revealed secret.
- The temp path must be on the library's filesystem so the final move is an atomic rename.
- Tests drive the real service in-process over HTTP with fakes for the outside world; no real network in tests. Pure ports (naming, parsers) get table tests.
- Comments only for a non-obvious why. No hostnames, domains, IPs, usernames or home paths anywhere; use placeholders.
- Commits: `<type>(<scope>): <summary>`, imperative, lowercase, with the why in the body.

## Fresh checkout

Needs Go (see `go.mod`), Node 24 with npm, and `golangci-lint` v2.15+ on `PATH`. `make gate` runs `npm ci` in `web/` by itself the first time; run `cd web && npm ci` to set up the web side without the gate.

## Gate

`make gate` is the one check before a commit and the CI job named `gate`. It runs, in order:

1. `go vet`
2. `golangci-lint run`
3. `go test`
4. web typecheck (`tsc --noEmit`)
5. web lint (`eslint`)
6. web tests (`vitest`)
7. build: Vite build into `web/dist`, then `go build -o bin/marquee ./cmd/marquee`

CI also runs a `gitleaks` job.
