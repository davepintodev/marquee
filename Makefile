GO_PKGS := ./cmd/... ./internal/... ./web
WEB := web

.PHONY: gate vet lint test web-typecheck web-lint web-test web-build build

gate: vet lint test web-typecheck web-lint web-test build

$(WEB)/node_modules: $(WEB)/package-lock.json
	cd $(WEB) && npm ci
	@touch $@

vet:
	go vet $(GO_PKGS)

lint:
	golangci-lint run $(GO_PKGS)

test:
	go test $(GO_PKGS)

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
