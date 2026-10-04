# where `make install` links the binary; override for a prefix that needs no privileges
BINDIR ?= /usr/local/bin

all: test build

# build to a temp file and mv: rewriting a running Mach-O in place makes macOS refuse to exec it,
# and a local `sharefly server` is often running from .bin/ while iterating
build:
	go build -ldflags "-s -w" -o .bin/sharefly.tmp ./cmd/sharefly
	mv -f .bin/sharefly.tmp .bin/sharefly

install: build
	rm -f "$(BINDIR)/sharefly"
	ln -s "$(CURDIR)/.bin/sharefly" "$(BINDIR)/sharefly"
	@echo "$(BINDIR)/sharefly -> $(CURDIR)/.bin/sharefly"

uninstall:
	rm -f "$(BINDIR)/sharefly"

test:
	go clean -testcache
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	rm coverage.out

vet:
	go vet ./...

lint:
	golangci-lint run --max-issues-per-linter=0 --max-same-issues=0

fmt:
	gofmt -s -w $(shell find . -type f -name "*.go")
	goimports -w $(shell find . -type f -name "*.go")

plist:
	plutil -lint deploy/com.sharefly.server.plist

check: vet plist test
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "run make fmt" && exit 1)

run: build
	.bin/sharefly server --api-addr 127.0.0.1:8787 --public-url http://127.0.0.1:8080 --data-dir .bin/data

clean:
	rm -rf .bin dist

.PHONY: all build install uninstall test vet lint fmt plist check run clean
