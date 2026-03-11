MODULE   := github.com/infobloxopen/devedge
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  := -X $(MODULE)/internal/version.Version=$(VERSION) \
            -X $(MODULE)/internal/version.Commit=$(COMMIT)

.PHONY: all build test lint clean

all: test build

build:
	go build -ldflags "$(LDFLAGS)" -o bin/de ./cmd/de
	go build -ldflags "$(LDFLAGS)" -o bin/devedged ./cmd/devedged
	go build -ldflags "$(LDFLAGS)" -o bin/devedge-dns-webhook ./cmd/devedge-dns-webhook

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
