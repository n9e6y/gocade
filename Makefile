.PHONY: build test run vet fmt-check bin demo play loadtest bench fuzz

# The version the binaries print. From git when there is a repository, else "dev".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
FUZZTIME ?= 10s

build:
	go build ./...

test:
	go test -race ./...

run:
	go run ./cmd/arena

vet:
	go vet ./...

# Fails (and lists the files) if any file is not gofmt-clean.
fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

# Binaries in bin/, with the version filled in.
bin:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/arena ./cmd/arena
	go build -o bin/loadtest ./cmd/loadtest

# Play from this one terminal: starts a server, connects, stops it on exit.
demo: bin
	sh scripts/demo.sh

# Connect to a server that is already running (make run, in another terminal).
play:
	sh scripts/play.sh

# 50 rooms of fake players against a race-enabled server.
loadtest:
	sh scripts/loadtest.sh $(ARGS)

bench:
	go test -run '^$$' -bench . -benchmem ./...

# Fuzz the input decoder: make fuzz FUZZTIME=1m for longer.
fuzz:
	go test -fuzz=FuzzDecoder -fuzztime=$(FUZZTIME) ./internal/input
