GO_PKGS := ./cmd/... ./internal/... ./web
WEB := web

.PHONY: gate smoke smokeserve fake-art api vet lint test web-api web-api-check web-typecheck web-lint web-test web-build build

gate: vet lint test web-api-check web-typecheck web-lint web-test build

$(WEB)/node_modules: $(WEB)/package-lock.json
	cd $(WEB) && npm ci
	@touch $@

vet:
	go vet $(GO_PKGS)

lint:
	golangci-lint run $(GO_PKGS)

test:
	go test $(GO_PKGS)

api: web-api

web-api: $(WEB)/node_modules
	cd $(WEB) && npm run api

web-api-check: $(WEB)/node_modules
	cd $(WEB) && npx openapi-typescript ../api/openapi.yaml | diff -u src/api/schema.d.ts - \
		|| { echo "web/src/api/schema.d.ts is stale: run 'make api' and commit it"; exit 1; }

web-typecheck: $(WEB)/node_modules
	cd $(WEB) && npm run typecheck

web-lint: $(WEB)/node_modules
	cd $(WEB) && npm run lint

web-test: $(WEB)/node_modules
	cd $(WEB) && npm test

# Vite empties dist/, which removes the placeholder that lets the Go
# embed compile before the first web build.
web-build: $(WEB)/node_modules
	cd $(WEB) && npm run build
	@touch $(WEB)/dist/.gitkeep

build: web-build
	go build -o bin/marquee ./cmd/marquee

# The real service with its outside world faked, serving the built UI: the
# smoke test's second server, and the fakes for the idle-memory measurement.
smokeserve: web-build
	go build -o bin/smokeserve ./cmd/smokeserve

# Not part of the gate: needs a Playwright browser (`cd web && npx playwright
# install chromium`). Screenshots and recordings go to MARQUEE_SHOTS_DIR.
smoke: build smokeserve
	cd $(WEB) && npx playwright test

# Not part of the gate: downloads the fixture titles' real posters and
# backdrops into ART_DIR for `marquee --fake`. Needs MARQUEE_TMDB_KEY.
fake-art:
	@test -n "$(ART_DIR)" || { echo "usage: make fake-art ART_DIR=<art-dir>"; exit 2; }
	go run ./cmd/fakeart "$(ART_DIR)"
