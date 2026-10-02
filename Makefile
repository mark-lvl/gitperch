GO ?= go

.PHONY: fmt test race vet build check
fmt:
	$(GO) fmt ./...
test:
	$(GO) test ./...
race:
	$(GO) test -race ./...
vet:
	$(GO) vet ./...
build:
	$(GO) build -o bin/repodash ./cmd/repodash
check: test vet build
