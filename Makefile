.PHONY: generate build-dist build test vet fmt run tidy all

# Regenerate qdata / OTQP Go code from protobuf.
generate:
	buf lint
	buf generate

# Regenerate the distribution (main.go + components.go) from the builder manifest.
build-dist:
	go run ./cmd/builder --config builder.yaml

# Version stamped into the binary (see main.version). Overridable, e.g.
# `make build VERSION=v1.2.3`; defaults to the current git description.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Build the distribution binary.
build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/querier ./cmd/querier

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

# Run the distribution against the example config.
run: build
	./bin/querier --config config.yaml

all: generate build-dist fmt vet test build
