.PHONY: build test run vet fmt-check

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
