ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BIN_DIR ?= $(HOME)/bin
COMMAND ?= ach
DIST := $(ROOT)/dist

.PHONY: install test build build-darwin-arm64 build-darwin-amd64

build: build-darwin-arm64 build-darwin-amd64

build-darwin-arm64:
	@mkdir -p "$(DIST)"
	GOOS=darwin GOARCH=arm64 go build -o "$(DIST)/ach-darwin-arm64" ./cmd/ach

build-darwin-amd64:
	@mkdir -p "$(DIST)"
	GOOS=darwin GOARCH=amd64 go build -o "$(DIST)/ach-darwin-amd64" ./cmd/ach

# Downloads the latest release binary for this machine's arch -- no Go
# toolchain needed on the machine running `make install`. Only the
# maintainer's machine needs Go, to `make build` and push a release tag.
# See install.sh for the actual logic -- this just forwards to it so
# there's one copy shared between `make install` and the no-clone curl path.
install:
	@BIN_DIR="$(BIN_DIR)" COMMAND="$(COMMAND)" "$(ROOT)/install.sh"

test:
	@go test ./...
