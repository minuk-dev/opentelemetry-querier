.PHONY: generate build-dist build test vet fmt run tidy all

# Regenerate qdata / OTQP Go code from protobuf.
generate:
	buf lint
	buf generate

# Regenerate the distribution (main.go + components.go) from the builder manifest.
build-dist:
	go run ./cmd/builder --config builder.yaml

# Build the distribution binary.
build:
	go build -o bin/querier ./cmd/querier

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
