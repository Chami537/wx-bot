VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath
CGO_ENABLED ?= 0
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: build test vet cross install clean

build:
	CGO_ENABLED=$(CGO_ENABLED) go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o wx-bot ./cmd/wx-bot

test:
	go test ./...

vet:
	go vet ./...

cross:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/wx-bot_linux_amd64  ./cmd/wx-bot
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/wx-bot_linux_arm64  ./cmd/wx-bot
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/wx-bot_darwin_amd64 ./cmd/wx-bot
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/wx-bot_darwin_arm64 ./cmd/wx-bot

install: build
	mkdir -p "$(BINDIR)"
	install -m 0755 wx-bot "$(BINDIR)/wx-bot"

clean:
	rm -rf dist wx-bot
