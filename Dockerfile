# syntax=docker/dockerfile:1

# Build stage: compile the querier distribution from the Go workspace. It runs
# on the build platform and cross-compiles to the target arch, so multi-arch
# builds don't emulate the compiler.
FROM --platform=$BUILDPLATFORM golang:1.25.4-bookworm AS build
WORKDIR /src

# ./cmd/querier depends on sibling modules via go.work replace directives, so
# the whole workspace must be present to build it.
COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
# Keep the build reproducible and offline w.r.t. toolchain (matches CI).
ENV GOTOOLCHAIN=local
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/querier ./cmd/querier

# Runtime stage: distroless static (no shell, runs as nonroot uid 65532) for a
# minimal image surface.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/querier /usr/local/bin/querier
# Ship the example config as a default; override by mounting your own and
# passing --config (see CMD).
COPY config.yaml /etc/querier/config.yaml

# otqp acceptor endpoints from config.yaml: gRPC 4327, HTTP 4328.
EXPOSE 4327 4328
ENTRYPOINT ["/usr/local/bin/querier"]
CMD ["--config", "/etc/querier/config.yaml"]
