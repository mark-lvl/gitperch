GO ?= go
VERSION ?= 0.1.0

.PHONY: fmt fmt-check test race vet build check release-linux install-dev
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
	$(GO) build -o bin/gitperch ./cmd/gitperch
install-dev:
	bash scripts/install-dev.sh
release-linux:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o dist/gitperch_$(VERSION)_linux_amd64 ./cmd/gitperch
	sha256sum dist/gitperch_$(VERSION)_linux_amd64
# Mirrors CI.
check: fmt-check test race vet build
