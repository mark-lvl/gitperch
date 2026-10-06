GO ?= go
# Release builds take the version from the nearest vX.Y.Z tag; see docs/releasing.md.
VERSION ?= $(or $(patsubst v%,%,$(shell git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null)),dev)

.PHONY: fmt fmt-check test race vet build check dist install-dev
fmt:
	$(GO) fmt ./...
fmt-check:
	test -z "$$(gofmt -l .)"
test:
	$(GO) test ./...
race:
	$(GO) test -race ./...
vet:
	$(GO) vet ./...
build:
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/gitperch ./cmd/gitperch
install-dev:
	bash scripts/install-dev.sh
# Linux release archives and checksums.txt under dist/, as published on GitHub.
dist:
	GO=$(GO) bash scripts/build-release.sh $(VERSION)
# Mirrors CI.
check: fmt-check test race vet build
